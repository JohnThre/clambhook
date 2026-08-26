#include "clambhook/runtime.h"

#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <netdb.h>
#include <poll.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/uio.h>
#include <time.h>
#include <unistd.h>

#include "clambhook/config.h"
#include "clambhook/ip_stack.h"
#include "clambhook/rules.h"
#include "internal.h"

#define ANDROID_RUNTIME_CONNECT_TIMEOUT_MS 10000

typedef struct android_packet_connection {
    int ipv4_descriptor;
    int ipv6_descriptor;
} android_packet_connection;

struct ch_runtime {
    pthread_mutex_t mutex;
    pthread_mutex_t ip_mutex;
    pthread_t tick_thread;
    atomic_bool tick_stop;
    bool tick_started;
    bool running;
    ch_config *config;
    ch_rule_engine *rules;
    char *config_path;
    char *active_profile;
    ch_ip_stack *ip_stack;
    ch_runtime_options options;
};

static char *android_optional_string(const ch_config_table *table,
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

static const ch_config_table *android_named_table(
    const ch_config_array *array, const char *name) {
    size_t count = ch_config_array_count(array);
    for (size_t index = 0U; index < count; ++index) {
        const ch_config_table *table = ch_config_array_get_table(array, index);
        char *candidate = android_optional_string(table, "name");
        int matches = candidate != NULL && strcmp(candidate, name) == 0;
        free(candidate);
        if (matches) return table;
    }
    return NULL;
}

static const char *android_selected_group_chain(
    const ch_config_table *profile, const char *group_name,
    char **out_chain) {
    *out_chain = NULL;
    const ch_config_table *group = android_named_table(
        ch_config_table_get_array(profile, "policy_group"), group_name);
    if (group == NULL) return NULL;
    *out_chain = android_optional_string(group, "selected");
    if (*out_chain != NULL && (*out_chain)[0] != '\0') return *out_chain;
    free(*out_chain);
    *out_chain = NULL;
    const ch_config_array *chains = ch_config_table_get_array(group, "chains");
    if (ch_config_array_count(chains) == 0U) return NULL;
    ch_error ignored;
    if (ch_config_array_get_string(chains, 0U, out_chain, &ignored) != CH_OK) {
        free(*out_chain);
        *out_chain = NULL;
    }
    return *out_chain;
}

static int android_chain_is_direct(const ch_runtime *runtime,
                                   const char *chain_name) {
    const ch_config_table *profile = ch_config_profile_named(
        runtime->config, runtime->active_profile);
    const ch_config_table *chain = android_named_table(
        ch_config_table_get_array(profile, "chain"), chain_name);
    const ch_config_array *servers = ch_config_table_get_array(chain, "server");
    size_t count = ch_config_array_count(servers);
    if (count == 0U) return 0;
    for (size_t index = 0U; index < count; ++index) {
        char *protocol = android_optional_string(
            ch_config_array_get_table(servers, index), "protocol");
        int direct = protocol != NULL && strcmp(protocol, "direct") == 0;
        free(protocol);
        if (!direct) return 0;
    }
    return 1;
}

static ch_status android_runtime_require_direct_route(
    ch_runtime *runtime, const char *network, const char *target,
    const char *source, const char *domain_hint, ch_error *error) {
    if (runtime->rules == NULL) return CH_OK;
    ch_rule_match_context context = {
        .network = network,
        .target = domain_hint != NULL && domain_hint[0] != '\0' ?
            domain_hint : target,
        .source = source,
        .process_name = "",
        .process_path = ""
    };
    ch_rule_decision decision;
    ch_status status = ch_rule_engine_decide(runtime->rules, &context,
                                             &decision, error);
    if (status != CH_OK) return status;
    const char *chain_name = decision.chain_name;
    char *selected_group_chain = NULL;
    if (strcmp(decision.action, CH_RULE_ACTION_BLOCK) == 0 ||
        strcmp(decision.action, CH_RULE_ACTION_REJECT) == 0) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "Android native route rejected flow");
        status = CH_ERROR_INVALID_STATE;
    } else if (strcmp(decision.action, CH_RULE_ACTION_GROUP) == 0) {
        const ch_config_table *profile = ch_config_profile_named(
            runtime->config, runtime->active_profile);
        chain_name = android_selected_group_chain(
            profile, decision.group_name, &selected_group_chain);
        if (chain_name == NULL ||
            !android_chain_is_direct(runtime, chain_name)) {
            ch_error_set(error, CH_ERROR_UNSUPPORTED,
                         "Android native policy group selected a non-direct chain");
            status = CH_ERROR_UNSUPPORTED;
        }
    } else if (strcmp(decision.action, CH_RULE_ACTION_DIRECT) != 0 &&
               (chain_name == NULL ||
                !android_chain_is_direct(runtime, chain_name))) {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "Android native encrypted chain linkage is not available");
        status = CH_ERROR_UNSUPPORTED;
    }
    free(selected_group_chain);
    ch_rule_decision_clear(&decision);
    return status;
}

static ch_status android_split_target(const char *target, char **out_host,
                                      char **out_service, ch_error *error) {
    *out_host = NULL;
    *out_service = NULL;
    if (target == NULL || target[0] == '\0') goto invalid;
    const char *host_start = target;
    const char *host_end = NULL;
    const char *service = NULL;
    if (target[0] == '[') {
        host_start = target + 1U;
        host_end = strchr(host_start, ']');
        if (host_end == NULL || host_end[1] != ':' || host_end[2] == '\0') {
            goto invalid;
        }
        service = host_end + 2U;
    } else {
        host_end = strrchr(target, ':');
        if (host_end == NULL || host_end == target || host_end[1] == '\0') {
            goto invalid;
        }
        service = host_end + 1U;
    }
    size_t host_length = (size_t)(host_end - host_start);
    *out_host = malloc(host_length + 1U);
    *out_service = ch_strdup(service);
    if (*out_host == NULL || *out_service == NULL) {
        free(*out_host);
        free(*out_service);
        *out_host = NULL;
        *out_service = NULL;
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy Android route target");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    memcpy(*out_host, host_start, host_length);
    (*out_host)[host_length] = '\0';
    return CH_OK;

invalid:
    ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                 "Android route target must be numeric host:port");
    return CH_ERROR_INVALID_ARGUMENT;
}

static ch_status android_resolve_numeric(
    const char *target, int socket_type, struct addrinfo **out_addresses,
    ch_error *error) {
    char *host = NULL;
    char *service = NULL;
    ch_status status = android_split_target(target, &host, &service, error);
    if (status != CH_OK) return status;
    struct addrinfo hints;
    memset(&hints, 0, sizeof(hints));
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = socket_type;
    hints.ai_flags = AI_NUMERICHOST | AI_NUMERICSERV;
    int result = getaddrinfo(host, service, &hints, out_addresses);
    free(host);
    free(service);
    if (result != 0) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "resolve Android numeric target: %s",
                     gai_strerror(result));
        return CH_ERROR_INVALID_ARGUMENT;
    }
    return CH_OK;
}

static ch_status android_connect_tcp(const char *target, int *out_descriptor,
                                     ch_error *error) {
    struct addrinfo *addresses = NULL;
    ch_status status = android_resolve_numeric(
        target, SOCK_STREAM, &addresses, error);
    if (status != CH_OK) return status;
    int saved_error = EHOSTUNREACH;
    *out_descriptor = -1;
    for (const struct addrinfo *candidate = addresses; candidate != NULL;
         candidate = candidate->ai_next) {
        int descriptor = socket(candidate->ai_family, SOCK_STREAM,
                                candidate->ai_protocol);
        if (descriptor < 0) {
            saved_error = errno;
            continue;
        }
        int flags = fcntl(descriptor, F_GETFL, 0);
        if (flags < 0 ||
            fcntl(descriptor, F_SETFL, flags | O_NONBLOCK) != 0) {
            saved_error = errno;
            (void)close(descriptor);
            continue;
        }
        int connected = connect(descriptor, candidate->ai_addr,
                                candidate->ai_addrlen);
        if (connected != 0 && errno == EINPROGRESS) {
            struct pollfd wait = {.fd = descriptor, .events = POLLOUT};
            do {
                connected = poll(&wait, 1U,
                                 ANDROID_RUNTIME_CONNECT_TIMEOUT_MS);
            } while (connected < 0 && errno == EINTR);
            if (connected > 0 && (wait.revents & POLLOUT) != 0) {
                socklen_t error_length = (socklen_t)sizeof(saved_error);
                if (getsockopt(descriptor, SOL_SOCKET, SO_ERROR,
                               &saved_error, &error_length) == 0 &&
                    saved_error == 0) {
                    connected = 0;
                } else {
                    connected = -1;
                }
            } else {
                saved_error = connected == 0 ? ETIMEDOUT : errno;
                connected = -1;
            }
        }
        if (connected == 0) {
            *out_descriptor = descriptor;
            break;
        }
        if (saved_error == 0) saved_error = errno;
        (void)close(descriptor);
    }
    freeaddrinfo(addresses);
    if (*out_descriptor < 0) {
        ch_error_set(error, CH_ERROR_IO, "connect Android TCP target %s: %s",
                     target, strerror(saved_error));
        return CH_ERROR_IO;
    }
    return CH_OK;
}

static ch_status android_runtime_tcp_dial(
    const char *target, const char *source, const char *domain_hint,
    int *out_descriptor, void *context, ch_error *error) {
    ch_runtime *runtime = context;
    ch_status status = android_runtime_require_direct_route(
        runtime, "tcp", target, source, domain_hint, error);
    if (status != CH_OK) return status;
    return android_connect_tcp(target, out_descriptor, error);
}

static ch_status android_runtime_udp_dial(
    const char *target, const char *source, const char *domain_hint,
    void **out_connection, void *context, ch_error *error) {
    ch_runtime *runtime = context;
    ch_status status = android_runtime_require_direct_route(
        runtime, "udp", target, source, domain_hint, error);
    if (status != CH_OK) return status;
    android_packet_connection *connection = calloc(1U, sizeof(*connection));
    if (connection == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate Android UDP connection");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    connection->ipv4_descriptor = -1;
    connection->ipv6_descriptor = -1;
    *out_connection = connection;
    return CH_OK;
}

static ch_status android_runtime_udp_send(
    void *opaque, const char *target, const uint8_t *payload,
    size_t payload_length, ch_error *error) {
    android_packet_connection *connection = opaque;
    struct addrinfo *addresses = NULL;
    ch_status status = android_resolve_numeric(
        target, SOCK_DGRAM, &addresses, error);
    if (status != CH_OK) return status;
    int saved_error = EHOSTUNREACH;
    int sent = 0;
    for (const struct addrinfo *candidate = addresses; candidate != NULL;
         candidate = candidate->ai_next) {
        int *descriptor = candidate->ai_family == AF_INET6 ?
            &connection->ipv6_descriptor : &connection->ipv4_descriptor;
        if (*descriptor < 0) {
            *descriptor = socket(candidate->ai_family, SOCK_DGRAM,
                                 candidate->ai_protocol);
            if (*descriptor < 0) {
                saved_error = errno;
                continue;
            }
        }
        ssize_t written;
        do {
            written = sendto(*descriptor, payload, payload_length, 0,
                             candidate->ai_addr, candidate->ai_addrlen);
        } while (written < 0 && errno == EINTR);
        if (written == (ssize_t)payload_length) {
            sent = 1;
            break;
        }
        saved_error = written < 0 ? errno : EMSGSIZE;
    }
    freeaddrinfo(addresses);
    if (!sent) {
        ch_error_set(error, CH_ERROR_IO, "send Android UDP target %s: %s",
                     target, strerror(saved_error));
        return CH_ERROR_IO;
    }
    return CH_OK;
}

static ch_status android_runtime_udp_receive(
    void *opaque, uint8_t *buffer, size_t buffer_capacity,
    size_t *out_length, char **out_source, ch_error *error) {
    android_packet_connection *connection = opaque;
    struct pollfd descriptors[2];
    nfds_t count = 0U;
    if (connection->ipv4_descriptor >= 0) {
        descriptors[count++] = (struct pollfd){
            .fd = connection->ipv4_descriptor, .events = POLLIN
        };
    }
    if (connection->ipv6_descriptor >= 0) {
        descriptors[count++] = (struct pollfd){
            .fd = connection->ipv6_descriptor, .events = POLLIN
        };
    }
    if (count == 0U) {
        ch_error_set(error, CH_ERROR_NOT_FOUND,
                     "Android UDP connection has no packet");
        return CH_ERROR_NOT_FOUND;
    }
    int ready;
    do {
        ready = poll(descriptors, count, 0);
    } while (ready < 0 && errno == EINTR);
    if (ready == 0) {
        ch_error_set(error, CH_ERROR_NOT_FOUND,
                     "Android UDP receive has no packet");
        return CH_ERROR_NOT_FOUND;
    }
    if (ready < 0) {
        ch_error_set(error, CH_ERROR_IO, "poll Android UDP: %s",
                     strerror(errno));
        return CH_ERROR_IO;
    }
    int descriptor = -1;
    for (nfds_t index = 0U; index < count; ++index) {
        if ((descriptors[index].revents & POLLIN) != 0) {
            descriptor = descriptors[index].fd;
            break;
        }
    }
    if (descriptor < 0) {
        ch_error_set(error, CH_ERROR_IO, "Android UDP socket closed");
        return CH_ERROR_IO;
    }
    struct sockaddr_storage source;
    struct iovec vector = {.iov_base = buffer, .iov_len = buffer_capacity};
    struct msghdr message;
    memset(&message, 0, sizeof(message));
    message.msg_name = &source;
    message.msg_namelen = (socklen_t)sizeof(source);
    message.msg_iov = &vector;
    message.msg_iovlen = 1U;
    ssize_t received;
    do {
        received = recvmsg(descriptor, &message, 0);
    } while (received < 0 && errno == EINTR);
    if (received < 0 || (message.msg_flags & MSG_TRUNC) != 0) {
        ch_error_set(error, CH_ERROR_IO, "receive Android UDP: %s",
                     received < 0 ? strerror(errno) : "truncated datagram");
        return CH_ERROR_IO;
    }
    char host[NI_MAXHOST];
    char service[NI_MAXSERV];
    int formatted = getnameinfo(
        (const struct sockaddr *)&source, message.msg_namelen,
        host, sizeof(host), service, sizeof(service),
        NI_NUMERICHOST | NI_NUMERICSERV);
    if (formatted != 0) {
        ch_error_set(error, CH_ERROR_IO, "format Android UDP source: %s",
                     gai_strerror(formatted));
        return CH_ERROR_IO;
    }
    int ipv6 = source.ss_family == AF_INET6;
    size_t source_length = strlen(host) + strlen(service) + 4U;
    char *rendered = malloc(source_length);
    if (rendered == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy Android UDP source");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    (void)snprintf(rendered, source_length,
                   ipv6 ? "[%s]:%s" : "%s:%s", host, service);
    *out_length = (size_t)received;
    *out_source = rendered;
    return CH_OK;
}

static void android_runtime_udp_close(void *opaque) {
    android_packet_connection *connection = opaque;
    if (connection == NULL) return;
    if (connection->ipv4_descriptor >= 0) {
        (void)close(connection->ipv4_descriptor);
    }
    if (connection->ipv6_descriptor >= 0) {
        (void)close(connection->ipv6_descriptor);
    }
    free(connection);
}

static void *android_runtime_tick_main(void *context) {
    ch_runtime *runtime = context;
    const struct timespec interval = {.tv_sec = 0, .tv_nsec = 10000000L};
    while (!atomic_load_explicit(&runtime->tick_stop,
                                 memory_order_acquire)) {
        struct timespec remaining = interval;
        while (nanosleep(&remaining, &remaining) != 0) {
            if (atomic_load_explicit(&runtime->tick_stop,
                                     memory_order_acquire)) {
                return NULL;
            }
        }
        pthread_mutex_lock(&runtime->ip_mutex);
        ch_ip_stack_tick(runtime->ip_stack);
        pthread_mutex_unlock(&runtime->ip_mutex);
    }
    return NULL;
}

static void android_runtime_write_packet(const uint8_t *packet, size_t length,
                                         void *context) {
    ch_runtime *runtime = context;
    runtime->options.packet_writer(packet, length,
                                   runtime->options.packet_writer_context);
}

static ch_status android_runtime_start_ip_stack(ch_runtime *runtime,
                                                ch_error *error) {
    if (runtime->options.packet_writer == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "Android runtime packet writer is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ch_ip_stack_options options = {
        .packet_writer = android_runtime_write_packet,
        .packet_writer_context = runtime,
        .tcp_dialer = android_runtime_tcp_dial,
        .tcp_dialer_context = runtime,
        .udp_dialer = android_runtime_udp_dial,
        .udp_dialer_context = runtime,
        .udp_sender = android_runtime_udp_send,
        .udp_receiver = android_runtime_udp_receive,
        .udp_closer = android_runtime_udp_close
    };
    pthread_mutex_lock(&runtime->ip_mutex);
    runtime->ip_stack = ch_ip_stack_create(&options, error);
    pthread_mutex_unlock(&runtime->ip_mutex);
    if (runtime->ip_stack == NULL) return error->code;
    atomic_store_explicit(&runtime->tick_stop, false, memory_order_release);
    if (pthread_create(&runtime->tick_thread, NULL,
                       android_runtime_tick_main, runtime) != 0) {
        pthread_mutex_lock(&runtime->ip_mutex);
        ch_ip_stack_destroy(runtime->ip_stack);
        runtime->ip_stack = NULL;
        pthread_mutex_unlock(&runtime->ip_mutex);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "start Android packet timer");
        return CH_ERROR_INTERNAL;
    }
    runtime->tick_started = true;
    return CH_OK;
}

static void android_runtime_stop_ip_stack(ch_runtime *runtime) {
    atomic_store_explicit(&runtime->tick_stop, true, memory_order_release);
    if (runtime->tick_started) {
        (void)pthread_join(runtime->tick_thread, NULL);
        runtime->tick_started = false;
    }
    pthread_mutex_lock(&runtime->ip_mutex);
    ch_ip_stack_destroy(runtime->ip_stack);
    runtime->ip_stack = NULL;
    pthread_mutex_unlock(&runtime->ip_mutex);
}

static ch_status android_runtime_apply_config(ch_runtime *runtime,
                                              const char *config_path,
                                              ch_error *error) {
    ch_config *config = NULL;
    char *path;
    char *active;
    if (config_path == NULL) config_path = "";
    path = ch_strdup(config_path);
    if (path == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy config path");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (config_path[0] == '\0') {
        active = ch_strdup("default");
    } else {
        ch_status status = ch_config_load(config_path, &config, error);
        if (status != CH_OK) {
            free(path);
            return status;
        }
        const ch_config_table *profile = ch_config_active_profile(config);
        if (profile == NULL ||
            ch_config_table_get_string(profile, "name", &active, error) != CH_OK) {
            ch_config_free(config);
            free(path);
            ch_error_set(error, CH_ERROR_PARSE, "active profile has no name");
            return CH_ERROR_PARSE;
        }
    }
    if (active == NULL) {
        ch_config_free(config);
        free(path);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy active profile");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_rule_engine *rules = NULL;
    if (config != NULL) {
        rules = ch_rule_engine_compile_config(config, active, error);
        if (rules == NULL) {
            ch_config_free(config);
            free(path);
            free(active);
            return error == NULL || error->code == CH_OK ?
                CH_ERROR_INVALID_ARGUMENT : error->code;
        }
    }
    ch_config_free(runtime->config);
    ch_rule_engine_destroy(runtime->rules);
    free(runtime->config_path);
    free(runtime->active_profile);
    runtime->config = config;
    runtime->rules = rules;
    runtime->config_path = path;
    runtime->active_profile = active;
    return CH_OK;
}

static ch_status android_runtime_select_profile(ch_runtime *runtime,
                                                char *name,
                                                ch_error *error) {
    ch_rule_engine *next_rules = ch_rule_engine_compile_config(
        runtime->config, name, error);
    if (next_rules == NULL) {
        free(name);
        return error == NULL || error->code == CH_OK ?
            CH_ERROR_INVALID_ARGUMENT : error->code;
    }
    if (strcmp(runtime->active_profile, name) == 0) {
        ch_rule_engine_destroy(next_rules);
        free(name);
        return CH_OK;
    }

    bool running = runtime->running;
    char *previous_name = runtime->active_profile;
    ch_rule_engine *previous_rules = runtime->rules;
    if (running) android_runtime_stop_ip_stack(runtime);
    runtime->active_profile = name;
    runtime->rules = next_rules;

    if (running) {
        ch_status status = android_runtime_start_ip_stack(runtime, error);
        if (status != CH_OK) {
            ch_error switch_error = *error;
            runtime->active_profile = previous_name;
            runtime->rules = previous_rules;
            ch_error rollback_error;
            ch_status rollback_status = android_runtime_start_ip_stack(
                runtime, &rollback_error);
            if (rollback_status != CH_OK) {
                runtime->running = false;
                ch_error_set(error, CH_ERROR_INTERNAL,
                             "switch Android profile failed: %s; "
                             "rollback failed: %s",
                             switch_error.message, rollback_error.message);
                status = CH_ERROR_INTERNAL;
            } else {
                *error = switch_error;
            }
            ch_rule_engine_destroy(next_rules);
            free(name);
            return status;
        }
    }

    ch_rule_engine_destroy(previous_rules);
    free(previous_name);
    return CH_OK;
}

static char *android_runtime_status_json(const ch_runtime *runtime) {
    ch_json_buffer json;
    ch_json_init(&json);
    if (!ch_json_append_format(&json, "{\"running\":%s,\"profile\":",
                               runtime->running ? "true" : "false") ||
        !ch_json_append_string(&json, runtime->active_profile) ||
        !ch_json_append(&json, ",\"network_info\":{}") ||
        (runtime->ip_stack != NULL &&
         !ch_json_append(&json, ",\"tunnel_mode\":\"tun\"")) ||
        !ch_json_append(&json, "}")) {
        ch_json_dispose(&json);
        return NULL;
    }
    return ch_json_take(&json);
}

static char *android_runtime_profiles_json(const ch_runtime *runtime) {
    ch_json_buffer json;
    ch_json_init(&json);
    if (!ch_json_append(&json, "{\"profiles\":[")) goto failure;
    if (runtime->config == NULL) {
        if (!ch_json_append_string(&json, runtime->active_profile)) goto failure;
    } else {
        size_t count = ch_config_profile_count(runtime->config);
        for (size_t index = 0U; index < count; ++index) {
            char *name = NULL;
            ch_error error;
            if ((index > 0U && !ch_json_append(&json, ",")) ||
                ch_config_table_get_string(ch_config_profile_at(runtime->config, index),
                                           "name", &name, &error) != CH_OK ||
                !ch_json_append_string(&json, name)) {
                free(name);
                goto failure;
            }
            free(name);
        }
    }
    if (!ch_json_append(&json, "],\"active\":") ||
        !ch_json_append_string(&json, runtime->active_profile) ||
        !ch_json_append(&json, "}")) goto failure;
    return ch_json_take(&json);

failure:
    ch_json_dispose(&json);
    return NULL;
}

ch_runtime *ch_runtime_create(const ch_runtime_options *options, ch_error *error) {
    ch_runtime *runtime;
    ch_error_clear(error);
    runtime = calloc(1U, sizeof(*runtime));
    if (runtime == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate runtime");
        return NULL;
    }
    if (pthread_mutex_init(&runtime->mutex, NULL) != 0) {
        free(runtime);
        ch_error_set(error, CH_ERROR_INTERNAL, "initialize runtime mutex");
        return NULL;
    }
    if (pthread_mutex_init(&runtime->ip_mutex, NULL) != 0) {
        pthread_mutex_destroy(&runtime->mutex);
        free(runtime);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "initialize Android packet mutex");
        return NULL;
    }
    atomic_init(&runtime->tick_stop, true);
    runtime->active_profile = ch_strdup("default");
    if (runtime->active_profile == NULL) {
        pthread_mutex_destroy(&runtime->mutex);
        pthread_mutex_destroy(&runtime->ip_mutex);
        free(runtime);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate default profile");
        return NULL;
    }
    if (options != NULL) runtime->options = *options;
    return runtime;
}

void ch_runtime_destroy(ch_runtime *runtime) {
    if (runtime == NULL) return;
    pthread_mutex_lock(&runtime->mutex);
    runtime->running = false;
    android_runtime_stop_ip_stack(runtime);
    ch_config_free(runtime->config);
    ch_rule_engine_destroy(runtime->rules);
    runtime->config = NULL;
    runtime->rules = NULL;
    free(runtime->config_path);
    free(runtime->active_profile);
    pthread_mutex_unlock(&runtime->mutex);
    pthread_mutex_destroy(&runtime->mutex);
    pthread_mutex_destroy(&runtime->ip_mutex);
    free(runtime);
}

ch_status ch_runtime_start(ch_runtime *runtime, const char *config_path, ch_error *error) {
    ch_status status;
    ch_error_clear(error);
    if (runtime == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "runtime is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    pthread_mutex_lock(&runtime->mutex);
    if (runtime->running) {
        pthread_mutex_unlock(&runtime->mutex);
        ch_error_set(error, CH_ERROR_INVALID_STATE, "engine already running");
        return CH_ERROR_INVALID_STATE;
    }
    status = android_runtime_apply_config(runtime, config_path, error);
    if (status == CH_OK) status = android_runtime_start_ip_stack(runtime,
                                                                error);
    if (status == CH_OK) runtime->running = true;
    pthread_mutex_unlock(&runtime->mutex);
    return status;
}

ch_status ch_runtime_stop(ch_runtime *runtime, ch_error *error) {
    ch_error_clear(error);
    if (runtime == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "runtime is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    pthread_mutex_lock(&runtime->mutex);
    runtime->running = false;
    android_runtime_stop_ip_stack(runtime);
    pthread_mutex_unlock(&runtime->mutex);
    return CH_OK;
}

ch_status ch_runtime_reload(ch_runtime *runtime, const char *config_path, ch_error *error) {
    ch_status status;
    ch_error_clear(error);
    if (runtime == NULL || config_path == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "runtime and config path are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    pthread_mutex_lock(&runtime->mutex);
    bool running = runtime->running;
    if (running) android_runtime_stop_ip_stack(runtime);
    status = android_runtime_apply_config(runtime, config_path, error);
    if (running) {
        ch_error stack_error;
        ch_status stack_status = android_runtime_start_ip_stack(runtime,
                                                                &stack_error);
        if (status == CH_OK && stack_status != CH_OK) {
            status = stack_status;
            *error = stack_error;
            runtime->running = false;
        }
    }
    pthread_mutex_unlock(&runtime->mutex);
    return status;
}

ch_status ch_runtime_inject_packet(ch_runtime *runtime, const uint8_t *packet,
                                   size_t length, ch_error *error) {
    ch_error_clear(error);
    if (runtime == NULL || packet == NULL || length == 0U) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "runtime and non-empty packet are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    pthread_mutex_lock(&runtime->mutex);
    ch_status status;
    if (!runtime->running) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "runtime is not running");
        status = CH_ERROR_INVALID_STATE;
    } else if (runtime->ip_stack == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "Android packet stack is not active");
        status = CH_ERROR_INVALID_STATE;
    } else {
        pthread_mutex_lock(&runtime->ip_mutex);
        status = ch_ip_stack_inject(runtime->ip_stack, packet, length, error);
        pthread_mutex_unlock(&runtime->ip_mutex);
    }
    pthread_mutex_unlock(&runtime->mutex);
    return status;
}

bool ch_runtime_is_running(ch_runtime *runtime) {
    bool running;
    if (runtime == NULL) return false;
    pthread_mutex_lock(&runtime->mutex);
    running = runtime->running;
    pthread_mutex_unlock(&runtime->mutex);
    return running;
}

ch_status ch_runtime_query(ch_runtime *runtime, const char *operation,
                           const char *request_json, char **response_json,
                           ch_error *error) {
    char *response = NULL;
    ch_error_clear(error);
    if (runtime == NULL || operation == NULL || response_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "runtime, operation, and response are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *response_json = NULL;
    pthread_mutex_lock(&runtime->mutex);
    if (strcmp(operation, "status") == 0) response = android_runtime_status_json(runtime);
    else if (strcmp(operation, "profiles") == 0) response = android_runtime_profiles_json(runtime);
    else if (strcmp(operation, "servers") == 0) {
        response = ch_config_servers_payload_json(runtime->config,
            runtime->active_profile, error);
    } else if (strcmp(operation, "rules") == 0) {
        response = ch_config_collection_payload_json(runtime->config, runtime->active_profile,
            "rule", "rules", 1, 0, error);
    } else if (strcmp(operation, "policy_groups") == 0) {
        response = ch_config_collection_payload_json(runtime->config, runtime->active_profile,
            "policy_group", "groups", 0, 0, error);
    } else if (strcmp(operation, "rule_sets") == 0) {
        response = ch_config_collection_payload_json(runtime->config, runtime->active_profile,
            "rule_set", "rule_sets", 0, 1, error);
    } else if (strcmp(operation, "config") == 0) {
        response = ch_config_profile_payload_json(runtime->config,
            runtime->active_profile, error);
    } else if (strcmp(operation, "test_rule") == 0) {
        ch_status status = ch_rule_explain_request_json(runtime->config,
            runtime->active_profile, request_json, &response, error);
        if (status != CH_OK) {
            pthread_mutex_unlock(&runtime->mutex);
            return status;
        }
    }
    else {
        pthread_mutex_unlock(&runtime->mutex);
        ch_error_set(error, CH_ERROR_UNSUPPORTED, "unknown runtime query operation");
        return CH_ERROR_UNSUPPORTED;
    }
    pthread_mutex_unlock(&runtime->mutex);
    if (response == NULL) {
        ch_status status = error == NULL || error->code == CH_OK
            ? CH_ERROR_OUT_OF_MEMORY : error->code;
        if (status == CH_ERROR_OUT_OF_MEMORY) {
            ch_error_set(error, status, "encode query response");
        }
        return status;
    }
    *response_json = response;
    return CH_OK;
}

ch_status ch_runtime_mutate(ch_runtime *runtime, const char *operation,
                            const char *request_json, char **response_json,
                            ch_error *error) {
    (void)request_json;
    ch_error_clear(error);
    if (runtime == NULL || operation == NULL || response_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "runtime, operation, and response are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    pthread_mutex_lock(&runtime->mutex);
    if (strcmp(operation, "connect") == 0) {
        if (!runtime->running) {
            ch_status status = android_runtime_start_ip_stack(runtime, error);
            if (status != CH_OK) {
                pthread_mutex_unlock(&runtime->mutex);
                return status;
            }
            runtime->running = true;
        }
    }
    else if (strcmp(operation, "disconnect") == 0) {
        runtime->running = false;
        android_runtime_stop_ip_stack(runtime);
    }
    else if (strcmp(operation, "set_active_profile") == 0) {
        char *name = ch_json_request_string(request_json, "name", error);
        if (name == NULL) {
            pthread_mutex_unlock(&runtime->mutex);
            return error == NULL ? CH_ERROR_INVALID_ARGUMENT : error->code;
        }
        if (runtime->config == NULL || !ch_config_has_profile(runtime->config, name)) {
            pthread_mutex_unlock(&runtime->mutex);
            ch_error_set(error, CH_ERROR_NOT_FOUND, "profile %s not found", name);
            free(name);
            return CH_ERROR_NOT_FOUND;
        }
        ch_status status = android_runtime_select_profile(runtime, name,
                                                          error);
        if (status != CH_OK) {
            pthread_mutex_unlock(&runtime->mutex);
            return status;
        }
    }
    else {
        pthread_mutex_unlock(&runtime->mutex);
        ch_error_set(error, CH_ERROR_UNSUPPORTED, "unknown runtime mutation operation");
        return CH_ERROR_UNSUPPORTED;
    }
    *response_json = android_runtime_status_json(runtime);
    pthread_mutex_unlock(&runtime->mutex);
    if (*response_json == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "encode mutation response");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    return CH_OK;
}

void ch_string_free(char *string) {
    free(string);
}
