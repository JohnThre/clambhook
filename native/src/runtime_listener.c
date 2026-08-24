#include "internal.h"

#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <netdb.h>
#include <poll.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/socket.h>
#include <unistd.h>

#include "clambhook/config.h"
#include "clambhook/listener.h"
#include "clambhook/rules.h"

#define CH_RUNTIME_LISTENER_LIMIT 2U
#define CH_RUNTIME_DEFAULT_MAX_CONNECTIONS 512U
#define CH_RUNTIME_DIAL_TIMEOUT_MS 30000

typedef struct ch_runtime_listener_entry {
    const ch_config *config;
    const ch_config_table *profile;
    char *profile_name;
    char *default_chain;
    ch_rule_engine *rules;
    ch_proxy_listener *listener;
} ch_runtime_listener_entry;

struct ch_runtime_listener_set {
    ch_runtime_listener_entry entries[CH_RUNTIME_LISTENER_LIMIT];
    size_t count;
};

static char *runtime_optional_string(const ch_config_table *table,
                                     const char *key) {
    char *value = NULL;
    ch_error ignored;
    if (table == NULL ||
        ch_config_table_get_string(table, key, &value, &ignored) != CH_OK) {
        free(value);
        return ch_strdup("");
    }
    return value;
}

static const ch_config_table *runtime_find_named(const ch_config_array *array,
                                                  const char *name) {
    size_t count = ch_config_array_count(array);
    for (size_t index = 0U; index < count; ++index) {
        const ch_config_table *table = ch_config_array_get_table(array, index);
        char *candidate = runtime_optional_string(table, "name");
        int matches = candidate != NULL && strcmp(candidate, name) == 0;
        free(candidate);
        if (matches) return table;
    }
    return NULL;
}

static ch_status runtime_split_target(const char *target, char **out_host,
                                      char **out_service, ch_error *error) {
    *out_host = NULL;
    *out_service = NULL;
    if (target == NULL || target[0] == '\0') {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "dial target is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    const char *host_start = target;
    const char *host_end;
    const char *service_start;
    if (target[0] == '[') {
        host_start = target + 1;
        host_end = strchr(host_start, ']');
        if (host_end == NULL || host_end[1] != ':' || host_end[2] == '\0') {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "invalid bracketed dial target");
            return CH_ERROR_INVALID_ARGUMENT;
        }
        service_start = host_end + 2;
    } else {
        const char *separator = strrchr(target, ':');
        if (separator == NULL || separator == target || separator[1] == '\0' ||
            strchr(target, ':') != separator) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "dial target must be host:port");
            return CH_ERROR_INVALID_ARGUMENT;
        }
        host_end = separator;
        service_start = separator + 1;
    }
    size_t host_length = (size_t)(host_end - host_start);
    *out_host = malloc(host_length + 1U);
    *out_service = ch_strdup(service_start);
    if (*out_host == NULL || *out_service == NULL) {
        free(*out_host);
        free(*out_service);
        *out_host = NULL;
        *out_service = NULL;
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy dial target");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    memcpy(*out_host, host_start, host_length);
    (*out_host)[host_length] = '\0';
    return CH_OK;
}

static int runtime_connect_target(const char *target, ch_error *error) {
    char *host = NULL;
    char *service = NULL;
    if (runtime_split_target(target, &host, &service, error) != CH_OK) return -1;
    struct addrinfo hints;
    memset(&hints, 0, sizeof(hints));
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_STREAM;
    hints.ai_protocol = IPPROTO_TCP;
    struct addrinfo *addresses = NULL;
    int lookup = getaddrinfo(host, service, &hints, &addresses);
    free(host);
    free(service);
    if (lookup != 0) {
        ch_error_set(error, CH_ERROR_IO, "resolve dial target: %s", gai_strerror(lookup));
        return -1;
    }
    int descriptor = -1;
    int saved_error = EHOSTUNREACH;
    for (const struct addrinfo *candidate = addresses; candidate != NULL;
         candidate = candidate->ai_next) {
        descriptor = socket(candidate->ai_family, candidate->ai_socktype,
                            candidate->ai_protocol);
        if (descriptor < 0) {
            saved_error = errno;
            continue;
        }
        int flags = fcntl(descriptor, F_GETFL, 0);
        if (flags < 0 || fcntl(descriptor, F_SETFL, flags | O_NONBLOCK) != 0) {
            saved_error = errno;
            (void)close(descriptor);
            descriptor = -1;
            continue;
        }
        int connected = connect(descriptor, candidate->ai_addr,
                                candidate->ai_addrlen) == 0;
        if (!connected && errno == EINPROGRESS) {
            struct pollfd wait = {.fd = descriptor, .events = POLLOUT};
            int ready;
            do {
                ready = poll(&wait, 1U, CH_RUNTIME_DIAL_TIMEOUT_MS);
            } while (ready < 0 && errno == EINTR);
            if (ready > 0) {
                int socket_error = 0;
                socklen_t socket_error_length = (socklen_t)sizeof(socket_error);
                if (getsockopt(descriptor, SOL_SOCKET, SO_ERROR, &socket_error,
                               &socket_error_length) == 0 && socket_error == 0) {
                    connected = 1;
                } else {
                    saved_error = socket_error == 0 ? errno : socket_error;
                }
            } else {
                saved_error = ready == 0 ? ETIMEDOUT : errno;
            }
        } else if (!connected) {
            saved_error = errno;
        }
        if (connected) {
            (void)fcntl(descriptor, F_SETFL, flags);
            break;
        }
        (void)close(descriptor);
        descriptor = -1;
    }
    freeaddrinfo(addresses);
    if (descriptor < 0) {
        ch_error_set(error, CH_ERROR_IO, "dial %s: %s", target, strerror(saved_error));
    }
    return descriptor;
}

static char *runtime_select_group_chain(const ch_runtime_listener_entry *entry,
                                        const char *group_name,
                                        ch_error *error) {
    const ch_config_array *groups = ch_config_table_get_array(entry->profile,
                                                               "policy_group");
    const ch_config_table *group = runtime_find_named(groups, group_name);
    if (group == NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "policy group %s not found", group_name);
        return NULL;
    }
    char *type = runtime_optional_string(group, "type");
    char *selected = NULL;
    if (type == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy policy group type");
        return NULL;
    }
    if (strcasecmp(type, "select") == 0) {
        selected = runtime_optional_string(group, "selected");
        if (selected != NULL && selected[0] == '\0') {
            free(selected);
            selected = NULL;
        }
    }
    free(type);
    if (selected == NULL) {
        const ch_config_array *chains = ch_config_table_get_array(group, "chains");
        if (ch_config_array_count(chains) > 0U &&
            ch_config_array_get_string(chains, 0U, &selected, error) != CH_OK) {
            return NULL;
        }
    }
    if (selected == NULL || selected[0] == '\0') {
        free(selected);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "policy group %s has no member chains", group_name);
        return NULL;
    }
    return selected;
}

static ch_status runtime_dial_chain(const ch_runtime_listener_entry *entry,
                                    const char *chain_name,
                                    const char *target,
                                    int *out_descriptor,
                                    ch_error *error) {
    const ch_config_array *chains = ch_config_table_get_array(entry->profile, "chain");
    const ch_config_table *chain = runtime_find_named(chains, chain_name);
    if (chain == NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "chain %s not found", chain_name);
        return CH_ERROR_NOT_FOUND;
    }
    const ch_config_array *servers = ch_config_table_get_array(chain, "server");
    if (ch_config_array_count(servers) != 1U) {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "native chain %s requires an unported multi-hop protocol", chain_name);
        return CH_ERROR_UNSUPPORTED;
    }
    const ch_config_table *server = ch_config_array_get_table(servers, 0U);
    char *protocol = runtime_optional_string(server, "protocol");
    if (protocol == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy chain protocol");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    int direct = strcasecmp(protocol, "direct") == 0;
    free(protocol);
    if (!direct) {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "native chain %s protocol is not ported yet", chain_name);
        return CH_ERROR_UNSUPPORTED;
    }
    *out_descriptor = runtime_connect_target(target, error);
    return *out_descriptor < 0 ? error->code : CH_OK;
}

static ch_status runtime_listener_dial(const char *network, const char *target,
                                       const char *source, ch_proxy_route *route,
                                       int *out_descriptor, void *context,
                                       ch_error *error) {
    ch_runtime_listener_entry *entry = context;
    ch_rule_match_context match = {
        .network = network,
        .target = target,
        .source = source
    };
    ch_rule_decision decision;
    ch_status status = ch_rule_engine_decide(entry->rules, &match, &decision, error);
    if (status != CH_OK) return status;
    route->action = CH_PROXY_ROUTE_CONNECT;
    *out_descriptor = -1;
    if (strcmp(decision.action, CH_RULE_ACTION_BLOCK) == 0) {
        route->action = CH_PROXY_ROUTE_BLOCK;
    } else if (strcmp(decision.action, CH_RULE_ACTION_REJECT) == 0) {
        route->action = CH_PROXY_ROUTE_REJECT;
    } else if (strcmp(decision.action, CH_RULE_ACTION_DIRECT) == 0) {
        *out_descriptor = runtime_connect_target(target, error);
        status = *out_descriptor < 0 ? error->code : CH_OK;
    } else {
        char *selected_group_chain = NULL;
        const char *chain_name = decision.is_default ? entry->default_chain :
            decision.chain_name;
        if (strcmp(decision.action, CH_RULE_ACTION_GROUP) == 0) {
            selected_group_chain = runtime_select_group_chain(entry,
                                                               decision.group_name, error);
            if (selected_group_chain == NULL) {
                status = error == NULL ? CH_ERROR_INVALID_ARGUMENT : error->code;
            } else {
                chain_name = selected_group_chain;
            }
        }
        if (status == CH_OK) {
            status = runtime_dial_chain(entry, chain_name, target,
                                        out_descriptor, error);
        }
        free(selected_group_chain);
    }
    ch_rule_decision_clear(&decision);
    return status;
}

static void runtime_listener_entry_clear(ch_runtime_listener_entry *entry) {
    if (entry == NULL) return;
    ch_proxy_listener_stop(entry->listener);
    ch_rule_engine_destroy(entry->rules);
    free(entry->profile_name);
    free(entry->default_chain);
    memset(entry, 0, sizeof(*entry));
}

static ch_status runtime_listener_default_chain(const ch_config_table *profile,
                                                const ch_config_table *listen,
                                                const char *key,
                                                char **out_chain,
                                                ch_error *error) {
    *out_chain = runtime_optional_string(listen, key);
    if (*out_chain == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy listener chain");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if ((*out_chain)[0] == '\0') {
        free(*out_chain);
        *out_chain = NULL;
        const ch_config_array *chains = ch_config_table_get_array(profile, "chain");
        const ch_config_table *first = ch_config_array_get_table(chains, 0U);
        if (first == NULL ||
            ch_config_table_get_string(first, "name", out_chain, error) != CH_OK) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "listener profile has no default chain");
            return CH_ERROR_INVALID_ARGUMENT;
        }
    }
    return CH_OK;
}

static ch_status runtime_listener_add(ch_runtime_listener_set *set,
                                      const ch_config *config,
                                      const ch_config_table *profile,
                                      const ch_config_table *listen,
                                      const char *profile_name,
                                      ch_proxy_listener_protocol protocol,
                                      ch_error *error) {
    const char *address_key = protocol == CH_PROXY_LISTENER_SOCKS5 ? "socks5" : "http";
    const char *chain_key = protocol == CH_PROXY_LISTENER_SOCKS5 ?
        "socks5_chain" : "http_chain";
    const char *maximum_key = protocol == CH_PROXY_LISTENER_SOCKS5 ?
        "socks5_max_connections" : "http_max_connections";
    const char *timeout_key = protocol == CH_PROXY_LISTENER_SOCKS5 ?
        "socks5_handshake_timeout" : "http_handshake_timeout";
    const char *auth_key = protocol == CH_PROXY_LISTENER_SOCKS5 ?
        "socks5_auth" : "http_auth";
    char *address = runtime_optional_string(listen, address_key);
    if (address == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy listener address");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (address[0] == '\0') {
        free(address);
        return CH_OK;
    }
    if (set->count >= CH_RUNTIME_LISTENER_LIMIT) {
        free(address);
        ch_error_set(error, CH_ERROR_INTERNAL, "too many configured listeners");
        return CH_ERROR_INTERNAL;
    }
    ch_runtime_listener_entry *entry = &set->entries[set->count];
    entry->config = config;
    entry->profile = profile;
    entry->profile_name = ch_strdup(profile_name);
    if (entry->profile_name == NULL ||
        runtime_listener_default_chain(profile, listen, chain_key,
                                       &entry->default_chain, error) != CH_OK) {
        free(address);
        runtime_listener_entry_clear(entry);
        return error == NULL ? CH_ERROR_OUT_OF_MEMORY : error->code;
    }
    entry->rules = ch_rule_engine_compile_config(config, profile_name, error);
    if (entry->rules == NULL) {
        free(address);
        runtime_listener_entry_clear(entry);
        return error == NULL ? CH_ERROR_INTERNAL : error->code;
    }
    int64_t maximum = 0;
    ch_error ignored;
    if (ch_config_table_get_int(listen, maximum_key, &maximum, &ignored) != CH_OK ||
        maximum == 0) maximum = (int64_t)CH_RUNTIME_DEFAULT_MAX_CONNECTIONS;
    if (maximum < 0 || (uint64_t)maximum > (uint64_t)SIZE_MAX) {
        free(address);
        runtime_listener_entry_clear(entry);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "invalid listener connection limit");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    unsigned int timeout_milliseconds = 0U;
    char *timeout = runtime_optional_string(listen, timeout_key);
    if (timeout == NULL) {
        free(address);
        runtime_listener_entry_clear(entry);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy listener timeout");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (timeout[0] != '\0') {
        int64_t nanoseconds = 0;
        if (ch_config_parse_duration_ns(timeout, &nanoseconds, error) != CH_OK ||
            nanoseconds < 0) {
            free(timeout);
            free(address);
            runtime_listener_entry_clear(entry);
            return error == NULL ? CH_ERROR_INVALID_ARGUMENT : error->code;
        }
        uint64_t milliseconds = (uint64_t)nanoseconds / UINT64_C(1000000);
        timeout_milliseconds = milliseconds > (uint64_t)UINT_MAX ?
            UINT_MAX : (unsigned int)milliseconds;
    }
    free(timeout);
    const ch_config_table *auth = ch_config_table_get_table(listen, auth_key);
    char *username = runtime_optional_string(auth, "username");
    char *password = runtime_optional_string(auth, "password");
    if (username == NULL || password == NULL) {
        free(username);
        free(password);
        free(address);
        runtime_listener_entry_clear(entry);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy listener credentials");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_proxy_listener_options options = {
        .protocol = protocol,
        .address = address,
        .authentication_required = auth != NULL,
        .username = username,
        .password = password,
        .maximum_connections = (size_t)maximum,
        .handshake_timeout_milliseconds = timeout_milliseconds,
        .dial = runtime_listener_dial,
        .dial_context = entry
    };
    entry->listener = ch_proxy_listener_start(&options, error);
    free(username);
    free(password);
    free(address);
    if (entry->listener == NULL) {
        runtime_listener_entry_clear(entry);
        return error == NULL ? CH_ERROR_INTERNAL : error->code;
    }
    ++set->count;
    return CH_OK;
}

ch_runtime_listener_set *ch_runtime_listener_set_start(const ch_config *config,
                                                        const char *profile_name,
                                                        ch_error *error) {
    ch_error_clear(error);
    ch_runtime_listener_set *set = calloc(1U, sizeof(*set));
    if (set == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate runtime listeners");
        return NULL;
    }
    if (config == NULL) return set;
    const ch_config_table *profile = profile_name == NULL || profile_name[0] == '\0' ?
        ch_config_active_profile(config) : ch_config_profile_named(config, profile_name);
    if (profile == NULL) {
        free(set);
        ch_error_set(error, CH_ERROR_NOT_FOUND, "listener profile not found");
        return NULL;
    }
    char *selected_name = runtime_optional_string(profile, "name");
    if (selected_name == NULL) {
        free(set);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy listener profile name");
        return NULL;
    }
    const ch_config_table *listen = ch_config_table_get_table(profile, "listen");
    ch_status status = runtime_listener_add(set, config, profile, listen,
                                            selected_name,
                                            CH_PROXY_LISTENER_SOCKS5, error);
    if (status == CH_OK) {
        status = runtime_listener_add(set, config, profile, listen,
                                      selected_name, CH_PROXY_LISTENER_HTTP, error);
    }
    free(selected_name);
    if (status != CH_OK) {
        ch_runtime_listener_set_stop(set);
        return NULL;
    }
    return set;
}

void ch_runtime_listener_set_stop(ch_runtime_listener_set *set) {
    if (set == NULL) return;
    for (size_t index = 0U; index < CH_RUNTIME_LISTENER_LIMIT; ++index) {
        runtime_listener_entry_clear(&set->entries[index]);
    }
    free(set);
}

int ch_runtime_listener_set_append_status(ch_runtime_listener_set *set,
                                          ch_json_buffer *json) {
    if (set == NULL || set->count == 0U) return 1;
    if (!ch_json_append(json, ",\"listeners\":[")) return 0;
    for (size_t index = 0U; index < set->count; ++index) {
        ch_proxy_listener *listener = set->entries[index].listener;
        if ((index > 0U && !ch_json_append(json, ",")) ||
            !ch_json_append(json, "{\"protocol\":") ||
            !ch_json_append_string(json, ch_proxy_listener_protocol_name(listener)) ||
            !ch_json_append(json, ",\"addr\":") ||
            !ch_json_append_string(json, ch_proxy_listener_address(listener)) ||
            !ch_json_append_format(json, ",\"active_conns\":%zu}",
                                   ch_proxy_listener_active_connections(listener))) {
            return 0;
        }
    }
    return ch_json_append(json, "]");
}
