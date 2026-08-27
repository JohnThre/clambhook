#include "internal.h"

#include <limits.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>

#include "clambhook/config.h"
#include "clambhook/listener.h"
#include "clambhook/procattr.h"
#include "clambhook/protocol.h"
#include "clambhook/rules.h"
#include "clambhook/temporary_rules.h"
#include "clambhook/traffic.h"
#include "cnet.h"

#define CH_RUNTIME_LISTENER_LIMIT 2U
#define CH_RUNTIME_DEFAULT_MAX_CONNECTIONS 512U

typedef struct ch_runtime_listener_entry {
    ch_runtime_listener_set *owner;
    const ch_config *config;
    const ch_config_table *profile;
    char *profile_name;
    char *default_chain;
    char *listener_protocol;
    char *listener_address;
    ch_rule_engine *rules;
    ch_proxy_listener *listener;
} ch_runtime_listener_entry;

struct ch_runtime_listener_set {
    ch_traffic_store *traffic;
    ch_temporary_rules *temporary_rules;
    ch_runtime_listener_entry entries[CH_RUNTIME_LISTENER_LIMIT];
    size_t count;
    ch_runtime_listener_entry dns_entry;
    int dns_ready;
    ch_runtime_listener_entry tun_entry;
    int tun_ready;
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

static int runtime_optional_bool(const ch_config_table *table,
                                 const char *key, int fallback) {
    bool value = fallback != 0;
    ch_error ignored;
    if (table != NULL) {
        (void)ch_config_table_get_bool(table, key, &value, &ignored);
    }
    return value ? 1 : 0;
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

static const char *runtime_decision_chain(
    const ch_runtime_listener_entry *entry,
    const ch_rule_decision *decision,
    char **out_selected_group_chain,
    ch_error *error) {
    *out_selected_group_chain = NULL;
    const char *chain_name = decision->is_default ? entry->default_chain :
                                                     decision->chain_name;
    if (strcmp(decision->action, CH_RULE_ACTION_GROUP) == 0) {
        *out_selected_group_chain = runtime_select_group_chain(
            entry, decision->group_name, error);
        chain_name = *out_selected_group_chain;
    }
    if (chain_name == NULL || chain_name[0] == '\0') {
        free(*out_selected_group_chain);
        *out_selected_group_chain = NULL;
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "route has no selected chain");
        return NULL;
    }
    return chain_name;
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
    return ch_protocol_chain_dial(chain, "tcp", target, out_descriptor, error);
}

static ch_status runtime_dial_packet_chain(
    const ch_runtime_listener_entry *entry,
    const char *chain_name,
    const char *target,
    ch_packet_connection **out_connection,
    ch_error *error) {
    const ch_config_array *chains = ch_config_table_get_array(entry->profile,
                                                               "chain");
    const ch_config_table *chain = runtime_find_named(chains, chain_name);
    if (chain == NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "chain %s not found",
                     chain_name);
        return CH_ERROR_NOT_FOUND;
    }
    return ch_protocol_chain_dial_packet(chain, target, out_connection, error);
}

static ch_status runtime_listener_decide(
    ch_runtime_listener_entry *entry,
    const char *network,
    const char *target,
    const char *source,
    ch_rule_decision *decision,
    ch_error *error) {
    ch_rule_match_context match = {
        .network = network,
        .target = target,
        .source = source,
        .process_name = "",
        .process_path = ""
    };
    bool temporary_match = false;
    ch_status status = ch_temporary_rules_decide(
        entry->owner->temporary_rules, entry->profile_name, &match, decision,
        &temporary_match, error);
    if (status != CH_OK || temporary_match) return status;

    ch_process_info process = {0};
    int attributed = ch_rule_engine_needs_process(entry->rules) &&
        ch_procattr_lookup(network, source, &process);
    match.process_name = attributed ? process.name : "";
    match.process_path = attributed ? process.path : "";
    status = ch_rule_engine_decide(entry->rules, &match, decision, error);
    ch_process_info_clear(&process);
    return status;
}

static uint64_t runtime_listener_open_traffic(
    ch_runtime_listener_entry *entry, const ch_rule_decision *decision,
    const char *network, const char *target, const char *source,
    const char *chain_name, ch_error *error) {
    ch_traffic_open_info info = {
        .profile = entry->profile_name,
        .listener_protocol = entry->listener_protocol,
        .listener_address = entry->listener_address,
        .client_address = source,
        .chain_name = chain_name,
        .group_name = decision->group_name,
        .rule_name = decision->rule_name,
        .rule_action = decision->action,
        .is_default = decision->is_default,
        .decision_ns = decision->elapsed_ns,
        .target = target,
        .target_host = decision->host,
        .target_port = decision->port,
        .network = network,
        .source = source
    };
    return ch_traffic_open(entry->owner->traffic, &info, error);
}

static void runtime_listener_flow_bytes(uint64_t flow_id, uint64_t rx_delta,
                                        uint64_t tx_delta, void *context) {
    ch_traffic_bytes(context, flow_id, rx_delta, tx_delta);
}

static void runtime_listener_flow_close(uint64_t flow_id, const char *reason,
                                        void *context) {
    ch_traffic_close(context, flow_id, reason);
}

static ch_status runtime_listener_dial_targets(
    const char *network, const char *route_target, const char *dial_target,
    const char *source, ch_proxy_route *route, int *out_descriptor,
    void *context, ch_error *error) {
    ch_runtime_listener_entry *entry = context;
    ch_rule_decision decision;
    ch_status status = runtime_listener_decide(
        entry, network, route_target, source, &decision, error);
    if (status != CH_OK) return status;
    route->action = CH_PROXY_ROUTE_CONNECT;
    route->flow_id = 0U;
    *out_descriptor = -1;
    char *selected_group_chain = NULL;
    const char *chain_name = "";
    if (strcmp(decision.action, CH_RULE_ACTION_BLOCK) != 0 &&
        strcmp(decision.action, CH_RULE_ACTION_REJECT) != 0 &&
        strcmp(decision.action, CH_RULE_ACTION_DIRECT) != 0) {
        chain_name = runtime_decision_chain(
            entry, &decision, &selected_group_chain, error);
        if (chain_name == NULL) {
            ch_rule_decision_clear(&decision);
            return error == NULL ? CH_ERROR_INVALID_ARGUMENT : error->code;
        }
    }
    route->flow_id = runtime_listener_open_traffic(
        entry, &decision, network, dial_target, source, chain_name, error);
    if (route->flow_id == 0U) {
        free(selected_group_chain);
        ch_rule_decision_clear(&decision);
        return error == NULL ? CH_ERROR_OUT_OF_MEMORY : error->code;
    }
    if (strcmp(decision.action, CH_RULE_ACTION_BLOCK) == 0) {
        route->action = CH_PROXY_ROUTE_BLOCK;
        ch_traffic_close(entry->owner->traffic, route->flow_id,
                         "blocked by rule");
    } else if (strcmp(decision.action, CH_RULE_ACTION_REJECT) == 0) {
        route->action = CH_PROXY_ROUTE_REJECT;
        ch_traffic_close(entry->owner->traffic, route->flow_id,
                         "rejected by rule");
    } else if (strcmp(decision.action, CH_RULE_ACTION_DIRECT) == 0) {
        status = ch_protocol_connect_tcp(dial_target, out_descriptor, error);
    } else {
        status = runtime_dial_chain(entry, chain_name, dial_target,
                                    out_descriptor, error);
    }
    if (status != CH_OK) {
        ch_traffic_close(entry->owner->traffic, route->flow_id,
                         error == NULL ? "dial failed" : error->message);
    }
    free(selected_group_chain);
    ch_rule_decision_clear(&decision);
    return status;
}

static ch_status runtime_listener_dial(const char *network, const char *target,
                                       const char *source, ch_proxy_route *route,
                                       int *out_descriptor, void *context,
                                       ch_error *error) {
    return runtime_listener_dial_targets(
        network, target, target, source, route, out_descriptor, context,
        error);
}

static ch_status runtime_listener_packet_dial_targets(
    const char *network,
    const char *route_target,
    const char *dial_target,
    const char *source,
    ch_proxy_route *route,
    ch_packet_connection **out_connection,
    void *context,
    ch_error *error) {
    ch_runtime_listener_entry *entry = context;
    ch_rule_decision decision;
    ch_status status = runtime_listener_decide(
        entry, network, route_target, source, &decision, error);
    if (status != CH_OK) return status;
    route->action = CH_PROXY_ROUTE_CONNECT;
    route->flow_id = 0U;
    *out_connection = NULL;
    char *selected_group_chain = NULL;
    const char *chain_name = "";
    if (strcmp(decision.action, CH_RULE_ACTION_BLOCK) != 0 &&
        strcmp(decision.action, CH_RULE_ACTION_REJECT) != 0 &&
        strcmp(decision.action, CH_RULE_ACTION_DIRECT) != 0) {
        chain_name = runtime_decision_chain(
            entry, &decision, &selected_group_chain, error);
        if (chain_name == NULL) {
            ch_rule_decision_clear(&decision);
            return error == NULL ? CH_ERROR_INVALID_ARGUMENT : error->code;
        }
    }
    route->flow_id = runtime_listener_open_traffic(
        entry, &decision, network, dial_target, source, chain_name, error);
    if (route->flow_id == 0U) {
        free(selected_group_chain);
        ch_rule_decision_clear(&decision);
        return error == NULL ? CH_ERROR_OUT_OF_MEMORY : error->code;
    }
    if (strcmp(decision.action, CH_RULE_ACTION_BLOCK) == 0) {
        route->action = CH_PROXY_ROUTE_BLOCK;
        ch_traffic_close(entry->owner->traffic, route->flow_id,
                         "blocked by rule");
    } else if (strcmp(decision.action, CH_RULE_ACTION_REJECT) == 0) {
        route->action = CH_PROXY_ROUTE_REJECT;
        ch_traffic_close(entry->owner->traffic, route->flow_id,
                         "rejected by rule");
    } else if (strcmp(decision.action, CH_RULE_ACTION_DIRECT) == 0) {
        (void)snprintf(route->session_key, sizeof(route->session_key),
                       "direct|target:%s", dial_target);
        status = ch_protocol_direct_packet_dial(out_connection, error);
    } else {
        uint8_t digest[28];
        static const char hex[] = "0123456789abcdef";
        char chain_hash[57];
        cnet_sha224((const uint8_t *)chain_name, strlen(chain_name), digest);
        for (size_t index = 0U; index < sizeof(digest); ++index) {
            chain_hash[index * 2U] = hex[digest[index] >> 4U];
            chain_hash[index * 2U + 1U] =
                hex[digest[index] & 0x0fU];
        }
        chain_hash[56] = '\0';
        (void)snprintf(route->session_key, sizeof(route->session_key),
                       "chain:%s|target:%s", chain_hash, dial_target);
        status = runtime_dial_packet_chain(entry, chain_name, dial_target,
                                           out_connection, error);
    }
    if (status != CH_OK) {
        ch_traffic_close(entry->owner->traffic, route->flow_id,
                         error == NULL ? "packet dial failed" :
                                         error->message);
    }
    free(selected_group_chain);
    ch_rule_decision_clear(&decision);
    return status;
}

static ch_status runtime_listener_packet_dial(
    const char *network, const char *target, const char *source,
    ch_proxy_route *route, ch_packet_connection **out_connection,
    void *context, ch_error *error) {
    return runtime_listener_packet_dial_targets(
        network, target, target, source, route, out_connection, context,
        error);
}

static void runtime_listener_entry_clear(ch_runtime_listener_entry *entry) {
    if (entry == NULL) return;
    ch_proxy_listener_stop(entry->listener);
    ch_rule_engine_destroy(entry->rules);
    free(entry->profile_name);
    free(entry->default_chain);
    free(entry->listener_protocol);
    free(entry->listener_address);
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

static ch_status runtime_dns_entry_initialize(
    ch_runtime_listener_set *set,
    const ch_config *config,
    const ch_config_table *profile,
    const char *profile_name,
    ch_error *error) {
    const ch_config_table *dns = ch_config_table_get_table(profile, "dns");
    if (!runtime_optional_bool(dns, "enabled", 0)) return CH_OK;
    const ch_config_array *chains = ch_config_table_get_array(profile,
                                                               "chain");
    const ch_config_table *first = ch_config_array_get_table(chains, 0U);
    if (first == NULL) return CH_OK;
    ch_runtime_listener_entry *entry = &set->dns_entry;
    entry->owner = set;
    entry->config = config;
    entry->profile = profile;
    entry->profile_name = ch_strdup(profile_name);
    entry->listener_protocol = ch_strdup("dns");
    entry->listener_address = ch_strdup("");
    const ch_config_table *listen = ch_config_table_get_table(profile,
                                                               "listen");
    const ch_config_table *tun = ch_config_table_get_table(listen, "tun");
    entry->default_chain = runtime_optional_string(tun, "chain");
    if (entry->profile_name == NULL || entry->default_chain == NULL ||
        entry->listener_protocol == NULL || entry->listener_address == NULL) {
        runtime_listener_entry_clear(entry);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy DNS route defaults");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (entry->default_chain[0] == '\0') {
        free(entry->default_chain);
        entry->default_chain = NULL;
        if (ch_config_table_get_string(first, "name",
                                       &entry->default_chain,
                                       error) != CH_OK) {
            runtime_listener_entry_clear(entry);
            return error == NULL ? CH_ERROR_PARSE : error->code;
        }
    }
    entry->rules = ch_rule_engine_compile_config(config, profile_name, error);
    if (entry->rules == NULL) {
        runtime_listener_entry_clear(entry);
        return error == NULL ? CH_ERROR_INTERNAL : error->code;
    }
    set->dns_ready = 1;
    return CH_OK;
}

static ch_status runtime_tun_entry_initialize(
    ch_runtime_listener_set *set,
    const ch_config *config,
    const ch_config_table *profile,
    const char *profile_name,
    ch_error *error) {
    const ch_config_table *listen = ch_config_table_get_table(profile,
                                                               "listen");
    const ch_config_table *tun = ch_config_table_get_table(listen, "tun");
    if (!runtime_optional_bool(tun, "enabled", 0)) return CH_OK;
    ch_runtime_listener_entry *entry = &set->tun_entry;
    entry->owner = set;
    entry->config = config;
    entry->profile = profile;
    entry->profile_name = ch_strdup(profile_name);
    entry->listener_protocol = ch_strdup("tun");
    entry->listener_address = ch_strdup("");
    if (entry->profile_name == NULL || entry->listener_protocol == NULL ||
        entry->listener_address == NULL) {
        runtime_listener_entry_clear(entry);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy TUN route profile");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_status status = runtime_listener_default_chain(
        profile, tun, "chain", &entry->default_chain, error);
    if (status != CH_OK) {
        runtime_listener_entry_clear(entry);
        return status;
    }
    entry->rules = ch_rule_engine_compile_config(config, profile_name, error);
    if (entry->rules == NULL) {
        runtime_listener_entry_clear(entry);
        return error == NULL ? CH_ERROR_INTERNAL : error->code;
    }
    set->tun_ready = 1;
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
    entry->owner = set;
    entry->config = config;
    entry->profile = profile;
    entry->profile_name = ch_strdup(profile_name);
    entry->listener_protocol = ch_strdup(
        protocol == CH_PROXY_LISTENER_SOCKS5 ? "socks5" : "http");
    entry->listener_address = ch_strdup(address);
    if (entry->profile_name == NULL ||
        entry->listener_protocol == NULL || entry->listener_address == NULL ||
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
        .packet_dial = protocol == CH_PROXY_LISTENER_SOCKS5 ?
            runtime_listener_packet_dial : NULL,
        .dial_context = entry,
        .flow_bytes = runtime_listener_flow_bytes,
        .flow_close = runtime_listener_flow_close,
        .flow_context = set->traffic
    };
    entry->listener = ch_proxy_listener_start(&options, error);
    free(username);
    free(password);
    free(address);
    if (entry->listener == NULL) {
        runtime_listener_entry_clear(entry);
        return error == NULL ? CH_ERROR_INTERNAL : error->code;
    }
    free(entry->listener_address);
    entry->listener_address = ch_strdup(
        ch_proxy_listener_address(entry->listener));
    if (entry->listener_address == NULL) {
        runtime_listener_entry_clear(entry);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy bound listener address");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ++set->count;
    return CH_OK;
}

ch_runtime_listener_set *ch_runtime_listener_set_start(const ch_config *config,
                                                        const char *profile_name,
                                                        ch_traffic_store *traffic,
                                                        ch_temporary_rules *temporary_rules,
                                                        ch_error *error) {
    ch_error_clear(error);
    ch_runtime_listener_set *set = calloc(1U, sizeof(*set));
    if (set == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate runtime listeners");
        return NULL;
    }
    set->traffic = traffic;
    set->temporary_rules = temporary_rules;
    if (traffic == NULL || temporary_rules == NULL) {
        free(set);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "traffic and temporary-rule stores are required");
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
    ch_status status = runtime_dns_entry_initialize(
        set, config, profile, selected_name, error);
    if (status == CH_OK) {
        status = runtime_tun_entry_initialize(
            set, config, profile, selected_name, error);
    }
    if (status == CH_OK) {
        status = runtime_listener_add(set, config, profile, listen,
                                      selected_name,
                                      CH_PROXY_LISTENER_SOCKS5, error);
    }
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
    runtime_listener_entry_clear(&set->dns_entry);
    runtime_listener_entry_clear(&set->tun_entry);
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

ch_status ch_runtime_listener_set_dns_route(
    ch_runtime_listener_set *set, const char *network, const char *target,
    ch_dns_route_action *out_action, ch_error *error) {
    ch_error_clear(error);
    if (set == NULL || !set->dns_ready || network == NULL || target == NULL ||
        out_action == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "native DNS route planner is not available");
        return CH_ERROR_INVALID_STATE;
    }
    ch_rule_decision decision;
    ch_status status = runtime_listener_decide(
        &set->dns_entry, network, target, "", &decision, error);
    if (status != CH_OK) return status;
    if (strcmp(decision.action, CH_RULE_ACTION_DIRECT) == 0) {
        *out_action = CH_DNS_ROUTE_DIRECT;
    } else if (strcmp(decision.action, CH_RULE_ACTION_BLOCK) == 0) {
        *out_action = CH_DNS_ROUTE_BLOCK;
    } else if (strcmp(decision.action, CH_RULE_ACTION_REJECT) == 0) {
        *out_action = CH_DNS_ROUTE_REJECT;
    } else {
        *out_action = CH_DNS_ROUTE_CONNECT;
    }
    ch_rule_decision_clear(&decision);
    return CH_OK;
}

static ch_status runtime_dns_direct_dial(
    const char *target, const char *const *bootstrap_ips,
    size_t bootstrap_ip_count, int *out_descriptor, ch_error *error) {
    if (bootstrap_ip_count == 0U) {
        return ch_protocol_connect_tcp(target, out_descriptor, error);
    }
    const char *separator = strrchr(target, ':');
    if (separator == NULL || separator[1] == '\0') {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "DNS upstream target must be host:port");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ch_status last_status = CH_ERROR_IO;
    ch_error last_error;
    ch_error_clear(&last_error);
    for (size_t index = 0U; index < bootstrap_ip_count; ++index) {
        const char *address = bootstrap_ips[index];
        if (address == NULL || address[0] == '\0') continue;
        bool ipv6 = strchr(address, ':') != NULL;
        size_t capacity = strlen(address) + strlen(separator + 1U) +
            (ipv6 ? 4U : 2U);
        char *bootstrap_target = malloc(capacity);
        if (bootstrap_target == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "allocate DNS bootstrap target");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        (void)snprintf(bootstrap_target, capacity,
                       ipv6 ? "[%s]:%s" : "%s:%s", address,
                       separator + 1U);
        ch_error attempt_error;
        last_status = ch_protocol_connect_tcp(
            bootstrap_target, out_descriptor, &attempt_error);
        free(bootstrap_target);
        if (last_status == CH_OK) return CH_OK;
        last_error = attempt_error;
    }
    if (error != NULL) *error = last_error;
    return last_status;
}

ch_status ch_runtime_listener_set_dns_dial(
    ch_runtime_listener_set *set, const char *network, const char *target,
    const char *const *bootstrap_ips, size_t bootstrap_ip_count,
    int *out_descriptor, ch_error *error) {
    ch_error_clear(error);
    if (set == NULL || !set->dns_ready || network == NULL || target == NULL ||
        out_descriptor == NULL ||
        (bootstrap_ip_count > 0U && bootstrap_ips == NULL)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "invalid native DNS dial request");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_descriptor = -1;
    ch_rule_decision decision;
    ch_status status = runtime_listener_decide(
        &set->dns_entry, network, target, "", &decision, error);
    if (status != CH_OK) return status;
    if (strcmp(decision.action, CH_RULE_ACTION_BLOCK) == 0 ||
        strcmp(decision.action, CH_RULE_ACTION_REJECT) == 0) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "DNS upstream route is %s", decision.action);
        status = CH_ERROR_INVALID_STATE;
    } else if (strcmp(decision.action, CH_RULE_ACTION_DIRECT) == 0) {
        status = runtime_dns_direct_dial(
            target, bootstrap_ips, bootstrap_ip_count, out_descriptor, error);
    } else {
        char *selected_group_chain = NULL;
        const char *chain_name = runtime_decision_chain(
            &set->dns_entry, &decision, &selected_group_chain, error);
        if (chain_name == NULL) {
            status = error == NULL ? CH_ERROR_INVALID_ARGUMENT : error->code;
        } else {
            status = runtime_dial_chain(&set->dns_entry, chain_name, target,
                                        out_descriptor, error);
        }
        free(selected_group_chain);
    }
    ch_rule_decision_clear(&decision);
    return status;
}

ch_status ch_runtime_listener_set_tun_tcp_dial(
    ch_runtime_listener_set *set, const char *target, const char *source,
    const char *domain_hint, int *out_descriptor, uint64_t *out_flow_id,
    ch_error *error) {
    ch_error_clear(error);
    if (set == NULL || !set->tun_ready || target == NULL || source == NULL ||
        out_descriptor == NULL || out_flow_id == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "native TUN route planner is not available");
        return CH_ERROR_INVALID_STATE;
    }
    *out_flow_id = 0U;
    ch_proxy_route route;
    memset(&route, 0, sizeof(route));
    const char *route_target = domain_hint != NULL && domain_hint[0] != '\0' ?
        domain_hint : target;
    ch_status status = runtime_listener_dial_targets(
        "tcp", route_target, target, source, &route, out_descriptor,
        &set->tun_entry, error);
    if (status != CH_OK) return status;
    if (route.action != CH_PROXY_ROUTE_CONNECT || *out_descriptor < 0) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "TUN route rejected TCP flow");
        return CH_ERROR_INVALID_STATE;
    }
    *out_flow_id = route.flow_id;
    return CH_OK;
}

ch_status ch_runtime_listener_set_tun_udp_dial(
    ch_runtime_listener_set *set, const char *target, const char *source,
    const char *domain_hint, void **out_connection, uint64_t *out_flow_id,
    ch_error *error) {
    ch_error_clear(error);
    if (set == NULL || !set->tun_ready || target == NULL || source == NULL ||
        out_connection == NULL || out_flow_id == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "native TUN route planner is not available");
        return CH_ERROR_INVALID_STATE;
    }
    *out_connection = NULL;
    *out_flow_id = 0U;
    ch_proxy_route route;
    memset(&route, 0, sizeof(route));
    ch_packet_connection *connection = NULL;
    const char *route_target = domain_hint != NULL && domain_hint[0] != '\0' ?
        domain_hint : target;
    ch_status status = runtime_listener_packet_dial_targets(
        "udp", route_target, target, source, &route, &connection,
        &set->tun_entry, error);
    if (status != CH_OK) return status;
    if (route.action != CH_PROXY_ROUTE_CONNECT || connection == NULL) {
        ch_packet_connection_close(connection);
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "TUN route rejected UDP flow");
        return CH_ERROR_INVALID_STATE;
    }
    *out_connection = connection;
    *out_flow_id = route.flow_id;
    return CH_OK;
}
