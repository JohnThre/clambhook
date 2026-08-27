#include "clambhook/runtime.h"

#include <arpa/inet.h>
#include <ctype.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>

#include <uv.h>

#include "clambhook/config.h"
#include "clambhook/dns.h"
#include "clambhook/ip_stack.h"
#include "clambhook/netwatch.h"
#include "clambhook/protocol.h"
#include "clambhook/prompt.h"
#include "clambhook/temporary_rules.h"
#include "clambhook/traffic.h"
#include "internal.h"

#define CH_RUNTIME_MAX_CONFIG_TRANSFER_BYTES (4U * 1024U * 1024U)

typedef enum ch_command_kind {
    CH_COMMAND_START,
    CH_COMMAND_STOP,
    CH_COMMAND_RELOAD,
    CH_COMMAND_INJECT,
    CH_COMMAND_QUERY,
    CH_COMMAND_MUTATE,
    CH_COMMAND_SHUTDOWN
} ch_command_kind;

typedef struct ch_command {
    ch_command_kind kind;
    char *operation;
    char *payload;
    uint8_t *packet;
    size_t packet_length;
    char *response;
    ch_status status;
    ch_error error;
    uv_mutex_t mutex;
    uv_cond_t condition;
    int complete;
    struct ch_command *next;
} ch_command;

typedef struct ch_runtime_tun_config {
    ch_ip_stack_options options;
    char ipv4_address[INET_ADDRSTRLEN];
    char ipv4_netmask[INET_ADDRSTRLEN];
    char ipv6_address[INET6_ADDRSTRLEN];
} ch_runtime_tun_config;

struct ch_runtime {
    uv_loop_t loop;
    uv_async_t command_async;
    uv_timer_t packet_timer;
    uv_thread_t thread;
    uv_mutex_t queue_mutex;
    ch_command *queue_head;
    ch_command *queue_tail;
    atomic_bool running;
    ch_runtime_listener_set *listeners;
    ch_dns_proxy *dns;
    ch_ip_stack *ip_stack;
    ch_netwatch *network_watcher;
    ch_traffic_store *traffic;
    ch_temporary_rules *temporary_rules;
    ch_prompt_manager *prompts;
    ch_network_info network_info;
    ch_config *config;
    char *config_path;
    char *active_profile;
    ch_runtime_options options;
};

static void ch_runtime_log(ch_runtime *runtime, int level, const char *message) {
    if (runtime->options.log_writer != NULL) {
        runtime->options.log_writer(level, message, runtime->options.log_writer_context);
    }
}

static bool ch_runtime_append_network_info(ch_runtime *runtime,
                                           ch_json_buffer *json) {
    if (!ch_json_append(json, ",\"network_info\":{")) return false;
    bool has_field = false;
    if (runtime->network_info.interface_name[0] != '\0') {
        if (!ch_json_append(json, "\"interface_name\":") ||
            !ch_json_append_string(json,
                                   runtime->network_info.interface_name)) {
            return false;
        }
        has_field = true;
    }
    if (runtime->network_info.ssid[0] != '\0') {
        if ((has_field && !ch_json_append(json, ",")) ||
            !ch_json_append(json, "\"ssid\":") ||
            !ch_json_append_string(json, runtime->network_info.ssid)) {
            return false;
        }
        has_field = true;
    }
    if (runtime->network_info.is_wifi) {
        if ((has_field && !ch_json_append(json, ",")) ||
            !ch_json_append(json, "\"is_wifi\":true")) {
            return false;
        }
    }
    return ch_json_append(json, "}");
}

static bool ch_runtime_append_dns_status(ch_runtime *runtime,
                                         ch_json_buffer *json) {
    if (!ch_json_append(json, ",\"dns\":{\"enabled\":")) return false;
    if (runtime->dns == NULL) return ch_json_append(json, "false}");
    if (!ch_json_append(json, "true,\"upstreams\":[")) return false;
    size_t count = ch_dns_proxy_upstream_count(runtime->dns);
    for (size_t index = 0U; index < count; ++index) {
        if ((index > 0U && !ch_json_append(json, ",")) ||
            !ch_json_append_string(
                json, ch_dns_proxy_upstream_name(runtime->dns, index))) {
            return false;
        }
    }
    return ch_json_append(json, "]}");
}

static bool ch_runtime_append_tunnel_status(ch_runtime *runtime,
                                            ch_json_buffer *json) {
    if (runtime->ip_stack == NULL) return true;
    return ch_json_append(json, ",\"tunnel_mode\":\"tun\"");
}

static char *ch_runtime_status_json(ch_runtime *runtime) {
    ch_json_buffer json;
    ch_json_init(&json);
    if (!ch_json_append_format(
            &json,
            "{\"running\":%s,\"profile\":",
            atomic_load_explicit(&runtime->running, memory_order_acquire) ? "true" : "false"
        ) ||
        !ch_json_append_string(&json, runtime->active_profile) ||
        !ch_runtime_append_network_info(runtime, &json) ||
        !ch_runtime_append_dns_status(runtime, &json) ||
        !ch_runtime_append_tunnel_status(runtime, &json) ||
        !ch_runtime_listener_set_append_status(runtime->listeners, &json) ||
        !ch_json_append(&json, "}")) {
        ch_json_dispose(&json);
        return NULL;
    }
    return ch_json_take(&json);
}

static char *ch_runtime_profile_status_json(ch_runtime *runtime,
                                            bool persisted,
                                            const char *backup_path) {
    char *status = ch_runtime_status_json(runtime);
    if (status == NULL) return NULL;
    size_t length = strlen(status);
    if (length == 0U || status[length - 1U] != '}') {
        free(status);
        return NULL;
    }
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append_bytes(&json, status, length - 1U) &&
        ch_json_append_format(&json, ",\"persisted\":%s",
                              persisted ? "true" : "false");
    if (okay && backup_path != NULL && backup_path[0] != '\0') {
        okay = ch_json_append(&json, ",\"backup_path\":") &&
            ch_json_append_string(&json, backup_path);
    }
    if (okay) okay = ch_json_append(&json, "}");
    free(status);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    return result;
}

static char *ch_runtime_profiles_json(ch_runtime *runtime) {
    ch_json_buffer json;
    ch_json_init(&json);
    if (!ch_json_append(&json, "{\"profiles\":[")) {
        ch_json_dispose(&json);
        return NULL;
    }
    if (runtime->config == NULL) {
        if (!ch_json_append_string(&json, runtime->active_profile)) {
            ch_json_dispose(&json);
            return NULL;
        }
    } else {
        size_t count = ch_config_profile_count(runtime->config);
        for (size_t index = 0U; index < count; ++index) {
            const ch_config_table *profile = ch_config_profile_at(runtime->config, index);
            char *name = NULL;
            ch_error error;
            if ((index > 0U && !ch_json_append(&json, ",")) ||
                ch_config_table_get_string(profile, "name", &name, &error) != CH_OK ||
                !ch_json_append_string(&json, name)) {
                free(name);
                ch_json_dispose(&json);
                return NULL;
            }
            free(name);
        }
    }
    if (!ch_json_append(&json, "],\"active\":") ||
        !ch_json_append_string(&json, runtime->active_profile) ||
        !ch_json_append(&json, "}")) {
        ch_json_dispose(&json);
        return NULL;
    }
    return ch_json_take(&json);
}

static char *ch_runtime_import_result_json(ch_runtime *runtime,
                                           const char *backup_path) {
    char *profiles = ch_runtime_profiles_json(runtime);
    if (profiles == NULL) return NULL;
    size_t length = strlen(profiles);
    if (length == 0U || profiles[length - 1U] != '}') {
        free(profiles);
        return NULL;
    }
    size_t count = runtime->config == NULL ? 0U :
        ch_config_profile_count(runtime->config);
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append_bytes(&json, profiles, length - 1U) &&
        ch_json_append(&json, ",\"backup_path\":") &&
        ch_json_append_string(&json, backup_path == NULL ? "" : backup_path) &&
        ch_json_append_format(
            &json, ",\"message\":\"imported %zu profile(s)\"}", count);
    free(profiles);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    return result;
}

static char *ch_runtime_json_with_backup(char *payload,
                                         const char *backup_path) {
    if (payload == NULL) return NULL;
    size_t length = strlen(payload);
    if (length == 0U || payload[length - 1U] != '}') {
        free(payload);
        return NULL;
    }
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append_bytes(&json, payload, length - 1U) &&
        ch_json_append(&json, ",\"backup_path\":") &&
        ch_json_append_string(&json, backup_path == NULL ? "" : backup_path) &&
        ch_json_append(&json, "}");
    free(payload);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    return result;
}

static void ch_command_fail(ch_command *command, ch_status status, const char *message) {
    command->status = status;
    ch_error_set(&command->error, status, "%s", message);
}

static void ch_runtime_stop_listeners(ch_runtime *runtime) {
    ch_prompt_manager_cancel_all(runtime->prompts);
    ch_ip_stack_destroy(runtime->ip_stack);
    runtime->ip_stack = NULL;
    ch_dns_proxy_destroy(runtime->dns);
    runtime->dns = NULL;
    ch_runtime_listener_set_stop(runtime->listeners);
    runtime->listeners = NULL;
    ch_traffic_close_all(runtime->traffic, "runtime stopped");
}

static void ch_runtime_write_packet(const uint8_t *packet, size_t length,
                                    void *context) {
    ch_runtime *runtime = context;
    runtime->options.packet_writer(packet, length,
                                   runtime->options.packet_writer_context);
}

static ch_status ch_runtime_tun_tcp_dial(
    const char *target, const char *source, const char *domain_hint,
    int *out_descriptor, uint64_t *out_flow_id,
    void *context, ch_error *error) {
    ch_runtime *runtime = context;
    *out_flow_id = 0U;
    return ch_runtime_listener_set_tun_tcp_dial(
        runtime->listeners, target, source, domain_hint, out_descriptor,
        out_flow_id, error);
}

static ch_status ch_runtime_tun_udp_dial(
    const char *target, const char *source, const char *domain_hint,
    void **out_connection, uint64_t *out_flow_id, void *context,
    ch_error *error) {
    ch_runtime *runtime = context;
    *out_flow_id = 0U;
    return ch_runtime_listener_set_tun_udp_dial(
        runtime->listeners, target, source, domain_hint, out_connection,
        out_flow_id, error);
}

static ch_status ch_runtime_tun_udp_send(
    void *connection, const char *target, const uint8_t *payload,
    size_t payload_length, ch_error *error) {
    return ch_packet_connection_send(connection, target, payload,
                                     payload_length, error);
}

static ch_status ch_runtime_tun_udp_receive(
    void *connection, uint8_t *buffer, size_t buffer_capacity,
    size_t *out_length, char **out_source, ch_error *error) {
    return ch_packet_connection_receive_timeout(
        connection, buffer, buffer_capacity, out_length, out_source, 0,
        error);
}

static void ch_runtime_tun_udp_close(void *connection) {
    ch_packet_connection_close(connection);
}

static void ch_runtime_tun_flow_bytes(uint64_t flow_id, uint64_t rx_delta,
                                      uint64_t tx_delta, void *context) {
    ch_traffic_bytes(context, flow_id, rx_delta, tx_delta);
}

static void ch_runtime_tun_flow_close(uint64_t flow_id, const char *reason,
                                      void *context) {
    ch_traffic_close(context, flow_id, reason);
}

static ch_status ch_runtime_tun_dns_exchange(
    const uint8_t *query, size_t query_length, uint8_t **out_response,
    size_t *out_response_length, void *context, ch_error *error) {
    return ch_dns_proxy_exchange(context, query, query_length, out_response,
                                 out_response_length, error);
}

static bool ch_runtime_parse_tun_address(ch_runtime_tun_config *config,
                                         const char *cidr,
                                         ch_command *command) {
    const char *slash = cidr == NULL ? NULL : strrchr(cidr, '/');
    if (slash == NULL || slash == cidr || slash[1] == '\0') {
        ch_command_fail(command, CH_ERROR_INVALID_ARGUMENT,
                        "listen.tun.addresses contains an invalid CIDR");
        return false;
    }
    size_t address_length = (size_t)(slash - cidr);
    char address[INET6_ADDRSTRLEN];
    if (address_length >= sizeof(address)) {
        ch_command_fail(command, CH_ERROR_INVALID_ARGUMENT,
                        "listen.tun.addresses contains an invalid address");
        return false;
    }
    memcpy(address, cidr, address_length);
    address[address_length] = '\0';
    char *end = NULL;
    long prefix = strtol(slash + 1, &end, 10);
    if (end == slash + 1 || *end != '\0') {
        ch_command_fail(command, CH_ERROR_INVALID_ARGUMENT,
                        "listen.tun.addresses contains an invalid prefix");
        return false;
    }
    struct in_addr ipv4;
    if (inet_pton(AF_INET, address, &ipv4) == 1) {
        if (prefix < 0 || prefix > 32) {
            ch_command_fail(command, CH_ERROR_INVALID_ARGUMENT,
                            "listen.tun.addresses IPv4 prefix is invalid");
            return false;
        }
        if (config->ipv4_address[0] != '\0') return true;
        uint32_t mask = prefix == 0 ? 0U :
            UINT32_MAX << (32U - (unsigned int)prefix);
        struct in_addr netmask = {.s_addr = htonl(mask)};
        if (inet_ntop(AF_INET, &ipv4, config->ipv4_address,
                      sizeof(config->ipv4_address)) == NULL ||
            inet_ntop(AF_INET, &netmask, config->ipv4_netmask,
                      sizeof(config->ipv4_netmask)) == NULL) {
            ch_command_fail(command, CH_ERROR_INTERNAL,
                            "format listen.tun IPv4 address");
            return false;
        }
        return true;
    }
    struct in6_addr ipv6;
    if (inet_pton(AF_INET6, address, &ipv6) == 1) {
        if (prefix < 0 || prefix > 128) {
            ch_command_fail(command, CH_ERROR_INVALID_ARGUMENT,
                            "listen.tun.addresses IPv6 prefix is invalid");
            return false;
        }
        if (config->ipv6_address[0] != '\0') return true;
        if (inet_ntop(AF_INET6, &ipv6, config->ipv6_address,
                      sizeof(config->ipv6_address)) == NULL) {
            ch_command_fail(command, CH_ERROR_INTERNAL,
                            "format listen.tun IPv6 address");
            return false;
        }
        return true;
    }
    ch_command_fail(command, CH_ERROR_INVALID_ARGUMENT,
                    "listen.tun.addresses contains an invalid address");
    return false;
}

static bool ch_runtime_tun_options(
    const ch_config *config, const char *profile_name,
    ch_runtime_tun_config *out_config, ch_command *command) {
    memset(out_config, 0, sizeof(*out_config));
    if (config == NULL) return false;
    const ch_config_table *profile = ch_config_profile_named(config,
                                                             profile_name);
    const ch_config_table *listen = ch_config_table_get_table(profile,
                                                               "listen");
    const ch_config_table *tun = ch_config_table_get_table(listen, "tun");
    bool enabled = false;
    ch_error value_error;
    if (tun == NULL || !ch_config_table_has(tun, "enabled")) return false;
    if (ch_config_table_get_bool(tun, "enabled", &enabled, &value_error) !=
            CH_OK ||
        !enabled) {
        return false;
    }
    if (ch_config_table_has(tun, "mtu")) {
        int64_t mtu = 0;
        if (ch_config_table_get_int(tun, "mtu", &mtu, &value_error) != CH_OK ||
            mtu < 0 || mtu > UINT16_MAX) {
            command->status = CH_ERROR_INVALID_ARGUMENT;
            ch_error_set(&command->error, CH_ERROR_INVALID_ARGUMENT,
                         "listen.tun.mtu is invalid");
            return false;
        }
        out_config->options.mtu = (unsigned int)mtu;
    }
    const ch_config_array *addresses = ch_config_table_get_array(
        tun, "addresses");
    size_t address_count = ch_config_array_count(addresses);
    for (size_t index = 0U; index < address_count; ++index) {
        char *cidr = NULL;
        if (ch_config_array_get_string(addresses, index, &cidr,
                                       &value_error) != CH_OK ||
            !ch_runtime_parse_tun_address(out_config, cidr, command)) {
            free(cidr);
            if (command->status == CH_OK) {
                command->status = value_error.code;
                command->error = value_error;
            }
            return false;
        }
        free(cidr);
    }
    out_config->options.ipv4_address = out_config->ipv4_address[0] == '\0' ?
        NULL : out_config->ipv4_address;
    out_config->options.ipv4_netmask = out_config->ipv4_netmask[0] == '\0' ?
        NULL : out_config->ipv4_netmask;
    out_config->options.ipv6_address = out_config->ipv6_address[0] == '\0' ?
        NULL : out_config->ipv6_address;
    return true;
}

static ch_status ch_runtime_dns_route(
    const char *network, const char *target,
    ch_dns_route_action *out_action, void *context, ch_error *error) {
    return ch_runtime_listener_set_dns_route(
        context, network, target, out_action, error);
}

static ch_status ch_runtime_dns_dial(
    const char *network, const char *target,
    const char *const *bootstrap_ips, size_t bootstrap_ip_count,
    int *out_descriptor, void *context, ch_error *error) {
    return ch_runtime_listener_set_dns_dial(
        context, network, target, bootstrap_ips, bootstrap_ip_count,
        out_descriptor, error);
}

static char *ch_runtime_optional_config_string(const ch_config_table *table,
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

static bool ch_runtime_nonblank(const char *value) {
    if (value == NULL) return false;
    while (*value != '\0') {
        if (!isspace((unsigned char)*value)) return true;
        ++value;
    }
    return false;
}

static bool ch_runtime_has_network_triggers(const ch_config *config) {
    size_t profile_count = ch_config_profile_count(config);
    for (size_t profile_index = 0U; profile_index < profile_count;
         ++profile_index) {
        const ch_config_table *profile = ch_config_profile_at(
            config, profile_index);
        const ch_config_array *triggers = ch_config_table_get_array(
            profile, "network_trigger");
        size_t trigger_count = ch_config_array_count(triggers);
        for (size_t trigger_index = 0U; trigger_index < trigger_count;
             ++trigger_index) {
            const ch_config_table *trigger = ch_config_array_get_table(
                triggers, trigger_index);
            char *ssid = ch_runtime_optional_config_string(trigger, "ssid");
            char *interface_name = ch_runtime_optional_config_string(
                trigger, "interface");
            bool enabled = ch_runtime_nonblank(ssid) ||
                ch_runtime_nonblank(interface_name);
            free(ssid);
            free(interface_name);
            if (enabled) return true;
        }
    }
    return false;
}

static char *ch_runtime_matching_profile(
    const ch_config *config,
    const ch_network_info *info,
    char **out_trigger_ssid,
    char **out_trigger_interface) {
    *out_trigger_ssid = NULL;
    *out_trigger_interface = NULL;
    size_t profile_count = ch_config_profile_count(config);
    for (size_t profile_index = 0U; profile_index < profile_count;
         ++profile_index) {
        const ch_config_table *profile = ch_config_profile_at(
            config, profile_index);
        const ch_config_array *triggers = ch_config_table_get_array(
            profile, "network_trigger");
        size_t trigger_count = ch_config_array_count(triggers);
        for (size_t trigger_index = 0U; trigger_index < trigger_count;
             ++trigger_index) {
            const ch_config_table *trigger = ch_config_array_get_table(
                triggers, trigger_index);
            char *ssid = ch_runtime_optional_config_string(trigger, "ssid");
            char *interface_name = ch_runtime_optional_config_string(
                trigger, "interface");
            if (ssid != NULL && interface_name != NULL &&
                ch_network_info_matches(info, ssid, interface_name)) {
                char *profile_name = NULL;
                ch_error ignored;
                if (ch_config_table_get_string(profile, "name", &profile_name,
                                               &ignored) == CH_OK) {
                    *out_trigger_ssid = ssid;
                    *out_trigger_interface = interface_name;
                    return profile_name;
                }
                free(profile_name);
            }
            free(ssid);
            free(interface_name);
        }
    }
    return NULL;
}

static bool ch_runtime_build_services(
    ch_runtime *runtime, const ch_config *config, const char *profile_name,
    ch_runtime_listener_set **out_listeners, ch_dns_proxy **out_dns,
    ch_ip_stack **out_ip_stack, ch_command *command) {
    *out_listeners = NULL;
    *out_dns = NULL;
    *out_ip_stack = NULL;
    ch_error listener_error;
    ch_runtime_listener_set *listeners = ch_runtime_listener_set_start(
        config, profile_name, runtime->traffic, runtime->temporary_rules,
        runtime->prompts,
        &listener_error
    );
    if (listeners == NULL) {
        command->status = listener_error.code;
        command->error = listener_error;
        return false;
    }
    ch_dns_proxy *dns = NULL;
    if (config != NULL) {
        ch_dns_proxy_options options = {
            .route = ch_runtime_dns_route,
            .stream_dial = ch_runtime_dns_dial,
            .dial_context = listeners
        };
        ch_error dns_error;
        dns = ch_dns_proxy_create(config, profile_name, &options, &dns_error);
        if (dns == NULL && dns_error.code != CH_OK) {
            ch_runtime_listener_set_stop(listeners);
            command->status = dns_error.code;
            command->error = dns_error;
            return false;
        }
    }
    ch_runtime_tun_config tun_config;
    bool tun_enabled = ch_runtime_tun_options(config, profile_name,
                                              &tun_config, command);
    if (command->status != CH_OK) {
        ch_dns_proxy_destroy(dns);
        ch_runtime_listener_set_stop(listeners);
        return false;
    }
    ch_ip_stack *ip_stack = NULL;
    if (tun_enabled) {
        if (runtime->options.packet_writer == NULL) {
            ch_dns_proxy_destroy(dns);
            ch_runtime_listener_set_stop(listeners);
            ch_command_fail(command, CH_ERROR_INVALID_ARGUMENT,
                            "listen.tun requires a runtime packet writer");
            return false;
        }
        tun_config.options.packet_writer = ch_runtime_write_packet;
        tun_config.options.packet_writer_context = runtime;
        tun_config.options.tcp_dialer = ch_runtime_tun_tcp_dial;
        tun_config.options.tcp_dialer_context = runtime;
        tun_config.options.udp_dialer = ch_runtime_tun_udp_dial;
        tun_config.options.udp_dialer_context = runtime;
        tun_config.options.udp_sender = ch_runtime_tun_udp_send;
        tun_config.options.udp_receiver = ch_runtime_tun_udp_receive;
        tun_config.options.udp_closer = ch_runtime_tun_udp_close;
        tun_config.options.flow_bytes = ch_runtime_tun_flow_bytes;
        tun_config.options.flow_close = ch_runtime_tun_flow_close;
        tun_config.options.flow_observer_context = runtime->traffic;
        if (dns != NULL) {
            tun_config.options.dns_exchange = ch_runtime_tun_dns_exchange;
            tun_config.options.dns_exchange_context = dns;
        }
        ch_error ip_error;
        ip_stack = ch_ip_stack_create(&tun_config.options, &ip_error);
        if (ip_stack == NULL) {
            ch_dns_proxy_destroy(dns);
            ch_runtime_listener_set_stop(listeners);
            command->status = ip_error.code;
            command->error = ip_error;
            return false;
        }
    }
    *out_listeners = listeners;
    *out_dns = dns;
    *out_ip_stack = ip_stack;
    return true;
}

static bool ch_runtime_start_listeners(ch_runtime *runtime,
                                       const ch_config *config,
                                       const char *profile_name,
                                       ch_command *command) {
    ch_runtime_listener_set *listeners = NULL;
    ch_dns_proxy *dns = NULL;
    ch_ip_stack *ip_stack = NULL;
    if (!ch_runtime_build_services(runtime, config, profile_name, &listeners,
                                   &dns, &ip_stack, command)) {
        return false;
    }
    runtime->listeners = listeners;
    runtime->dns = dns;
    runtime->ip_stack = ip_stack;
    return true;
}

static bool ch_runtime_switch_profile(ch_runtime *runtime, const char *name,
                                      ch_command *command) {
    char *next_name = ch_strdup(name);
    if (next_name == NULL) {
        ch_command_fail(command, CH_ERROR_OUT_OF_MEMORY,
                        "copy active profile");
        return false;
    }
    if (atomic_load_explicit(&runtime->running, memory_order_acquire)) {
        char *old_name = runtime->active_profile;
        ch_runtime_stop_listeners(runtime);
        if (!ch_runtime_start_listeners(runtime, runtime->config, name,
                                        command)) {
            ch_error profile_error = command->error;
            ch_status profile_status = command->status;
            ch_command rollback_command;
            memset(&rollback_command, 0, sizeof(rollback_command));
            (void)ch_runtime_start_listeners(
                runtime, runtime->config, old_name, &rollback_command);
            free(next_name);
            command->status = profile_status;
            command->error = profile_error;
            return false;
        }
        runtime->active_profile = next_name;
        free(old_name);
    } else {
        free(runtime->active_profile);
        runtime->active_profile = next_name;
    }
    return true;
}

static void ch_runtime_trim_in_place(char *value) {
    char *start = value;
    while (*start != '\0' && isspace((unsigned char)*start) != 0) ++start;
    char *end = start + strlen(start);
    while (end > start && isspace((unsigned char)end[-1]) != 0) --end;
    size_t length = (size_t)(end - start);
    if (start != value && length > 0U) memmove(value, start, length);
    value[length] = '\0';
}

static void ch_runtime_network_log(int level, const char *message,
                                   void *context) {
    ch_runtime_log(context, level, message);
}

static void ch_runtime_network_observation(const ch_network_info *info,
                                           void *context) {
    ch_runtime *runtime = context;
    runtime->network_info = *info;
    if (!atomic_load_explicit(&runtime->running, memory_order_acquire) ||
        runtime->config == NULL) {
        return;
    }
    char *trigger_ssid = NULL;
    char *trigger_interface = NULL;
    char *winner = ch_runtime_matching_profile(
        runtime->config, info, &trigger_ssid, &trigger_interface);
    if (winner == NULL || strcmp(winner, runtime->active_profile) == 0) {
        free(winner);
        free(trigger_ssid);
        free(trigger_interface);
        return;
    }
    char old_profile[256];
    (void)snprintf(old_profile, sizeof(old_profile), "%s",
                   runtime->active_profile);
    ch_command command;
    memset(&command, 0, sizeof(command));
    if (!ch_runtime_switch_profile(runtime, winner, &command)) {
        char message[512];
        (void)snprintf(message, sizeof(message),
                       "netwatch: auto-switch to profile \"%s\" failed: %s",
                       winner, command.error.message);
        ch_runtime_log(runtime, 2, message);
    } else {
        char message[768];
        (void)snprintf(
            message, sizeof(message),
            "netwatch: switched profile \"%s\" to \"%s\" for SSID \"%s\" "
            "and interface \"%s\" (trigger SSID \"%s\", interface \"%s\")",
            old_profile, winner, info->ssid, info->interface_name,
            trigger_ssid == NULL ? "" : trigger_ssid,
            trigger_interface == NULL ? "" : trigger_interface);
        ch_runtime_log(runtime, 1, message);
    }
    free(winner);
    free(trigger_ssid);
    free(trigger_interface);
}

static void ch_runtime_refresh_network_watcher(ch_runtime *runtime) {
    ch_netwatch *previous = runtime->network_watcher;
    runtime->network_watcher = NULL;
    ch_netwatch_stop(previous);
    if (!atomic_load_explicit(&runtime->running, memory_order_acquire) ||
        runtime->config == NULL ||
        !ch_runtime_has_network_triggers(runtime->config)) {
        return;
    }
    ch_netwatch_options options = {
        .poll_milliseconds = runtime->options.network_poll_milliseconds,
        .probe = runtime->options.network_probe,
        .probe_context = runtime->options.network_probe_context,
        .observation = ch_runtime_network_observation,
        .observation_context = runtime,
        .log = ch_runtime_network_log,
        .log_context = runtime
    };
    ch_error error;
    runtime->network_watcher = ch_netwatch_start(&runtime->loop, &options,
                                                 &error);
    if (runtime->network_watcher == NULL) {
        char message[512];
        (void)snprintf(message, sizeof(message),
                       "netwatch: start failed: %s", error.message);
        ch_runtime_log(runtime, 2, message);
    }
}

static bool ch_runtime_apply_config(ch_runtime *runtime, ch_command *command,
                                    const char *path, bool start_listeners) {
    ch_config *next_config = NULL;
    char *next_path;
    char *next_profile;
    if (path == NULL) {
        path = "";
    }
    next_path = ch_strdup(path);
    if (next_path == NULL) {
        ch_command_fail(command, CH_ERROR_OUT_OF_MEMORY, "copy config path");
        return false;
    }
    if (path[0] != '\0') {
        ch_error config_error;
        ch_status status = ch_config_load(path, &next_config, &config_error);
        if (status != CH_OK) {
            free(next_path);
            command->status = status;
            command->error = config_error;
            return false;
        }
        {
            const ch_config_table *profile = ch_config_active_profile(next_config);
            ch_error name_error;
            if (profile == NULL ||
                ch_config_table_get_string(profile, "name", &next_profile, &name_error) != CH_OK) {
                ch_config_free(next_config);
                free(next_path);
                ch_command_fail(command, CH_ERROR_PARSE, "active profile has no name");
                return false;
            }
        }
    } else {
        next_profile = ch_strdup("default");
        if (next_profile == NULL) {
            free(next_path);
            ch_command_fail(command, CH_ERROR_OUT_OF_MEMORY, "copy default profile");
            return false;
        }
    }
    if (start_listeners) ch_runtime_stop_listeners(runtime);
    ch_error traffic_error;
    ch_status traffic_status = ch_traffic_store_configure(
        runtime->traffic, next_config, &traffic_error);
    if (traffic_status != CH_OK) {
        if (start_listeners) {
            ch_error ignored;
            (void)ch_traffic_store_configure(runtime->traffic,
                                             runtime->config, &ignored);
            ch_command rollback_command;
            memset(&rollback_command, 0, sizeof(rollback_command));
            (void)ch_runtime_start_listeners(
                runtime, runtime->config, runtime->active_profile,
                &rollback_command);
        }
        ch_config_free(next_config);
        free(next_path);
        free(next_profile);
        command->status = traffic_status;
        command->error = traffic_error;
        return false;
    }
    ch_error prompt_error;
    ch_status prompt_status = ch_prompt_manager_configure(
        runtime->prompts, next_config, &prompt_error);
    if (prompt_status != CH_OK) {
        ch_error ignored;
        (void)ch_traffic_store_configure(runtime->traffic, runtime->config,
                                         &ignored);
        (void)ch_prompt_manager_configure(runtime->prompts, runtime->config,
                                          &ignored);
        if (start_listeners) {
            ch_command rollback_command;
            memset(&rollback_command, 0, sizeof(rollback_command));
            (void)ch_runtime_start_listeners(
                runtime, runtime->config, runtime->active_profile,
                &rollback_command);
        }
        ch_config_free(next_config);
        free(next_path);
        free(next_profile);
        command->status = prompt_status;
        command->error = prompt_error;
        return false;
    }
    ch_runtime_listener_set *next_listeners = NULL;
    ch_dns_proxy *next_dns = NULL;
    ch_ip_stack *next_ip_stack = NULL;
    if (start_listeners) {
        if (!ch_runtime_build_services(
                runtime, next_config, next_profile, &next_listeners,
                &next_dns, &next_ip_stack, command)) {
            ch_status next_status = command->status;
            ch_error next_error = command->error;
            ch_command rollback_command;
            memset(&rollback_command, 0, sizeof(rollback_command));
            ch_error ignored;
            (void)ch_traffic_store_configure(runtime->traffic,
                                             runtime->config, &ignored);
            (void)ch_prompt_manager_configure(runtime->prompts,
                                              runtime->config, &ignored);
            (void)ch_runtime_start_listeners(
                runtime, runtime->config, runtime->active_profile,
                &rollback_command);
            ch_config_free(next_config);
            free(next_path);
            free(next_profile);
            command->status = next_status;
            command->error = next_error;
            return false;
        }
    }
    ch_config_free(runtime->config);
    free(runtime->config_path);
    free(runtime->active_profile);
    runtime->config = next_config;
    runtime->config_path = next_path;
    runtime->active_profile = next_profile;
    runtime->listeners = next_listeners;
    runtime->dns = next_dns;
    runtime->ip_stack = next_ip_stack;
    return true;
}

static bool ch_runtime_persist_active_profile(ch_runtime *runtime,
                                              const char *name,
                                              ch_command *command) {
    if (runtime->config_path == NULL || runtime->config_path[0] == '\0') {
        bool found = runtime->config == NULL
            ? strcmp(name, "default") == 0
            : ch_config_has_profile(runtime->config, name);
        if (!found) {
            ch_error_set(&command->error, CH_ERROR_NOT_FOUND,
                         "profile %s not found", name);
            command->status = CH_ERROR_NOT_FOUND;
            return false;
        }
        if (!ch_runtime_switch_profile(runtime, name, command)) return false;
        command->response = ch_runtime_profile_status_json(runtime, false,
                                                           NULL);
        return true;
    }

    char *path = ch_strdup(runtime->config_path);
    ch_config *disk_config = NULL;
    char *document = NULL;
    char *backup_path = NULL;
    ch_error config_error;
    if (path == NULL) {
        ch_command_fail(command, CH_ERROR_OUT_OF_MEMORY, "copy config path");
        return false;
    }
    ch_status status = ch_config_load(path, &disk_config, &config_error);
    if (status == CH_OK) {
        status = ch_config_document_set_active(disk_config, name, &document,
                                               &config_error);
    }
    if (status == CH_OK) {
        status = ch_config_write_atomic_document(path, document, &backup_path,
                                                 &config_error);
    }
    if (status != CH_OK) {
        command->status = status;
        command->error = config_error;
        free(backup_path);
        free(document);
        ch_config_free(disk_config);
        free(path);
        return false;
    }

    bool running = atomic_load_explicit(&runtime->running,
                                        memory_order_acquire);
    if (!ch_runtime_apply_config(runtime, command, path, running)) {
        ch_status apply_status = command->status;
        ch_error apply_error = command->error;
        ch_error restore_error;
        ch_status restore_status = ch_config_write_atomic_document(
            path, ch_config_document(disk_config), NULL, &restore_error);
        if (restore_status != CH_OK) {
            ch_error_set(&command->error, CH_ERROR_INTERNAL,
                         "%s; restore config: %s", apply_error.message,
                         restore_error.message);
            command->status = CH_ERROR_INTERNAL;
        } else {
            command->status = apply_status;
            command->error = apply_error;
        }
        free(backup_path);
        free(document);
        ch_config_free(disk_config);
        free(path);
        return false;
    }
    ch_runtime_refresh_network_watcher(runtime);
    command->response = ch_runtime_profile_status_json(runtime, true,
                                                       backup_path);
    free(backup_path);
    free(document);
    ch_config_free(disk_config);
    free(path);
    return true;
}

static bool ch_runtime_import_config(ch_runtime *runtime,
                                     const char *document,
                                     ch_command *command) {
    if (runtime->config_path == NULL || runtime->config_path[0] == '\0') {
        ch_command_fail(command, CH_ERROR_INVALID_STATE,
                        "config import requires daemon config path");
        return false;
    }
    size_t document_length = document == NULL ? 0U : strlen(document);
    if (document_length == 0U) {
        ch_command_fail(command, CH_ERROR_INVALID_ARGUMENT,
                        "empty config body");
        return false;
    }
    if (document_length > CH_RUNTIME_MAX_CONFIG_TRANSFER_BYTES) {
        ch_command_fail(command, CH_ERROR_INVALID_ARGUMENT,
                        "config exceeds import size limit");
        return false;
    }
    bool only_whitespace = true;
    for (size_t index = 0U; index < document_length; ++index) {
        if (isspace((unsigned char)document[index]) == 0) {
            only_whitespace = false;
            break;
        }
    }
    if (only_whitespace) {
        ch_command_fail(command, CH_ERROR_INVALID_ARGUMENT,
                        "empty config body");
        return false;
    }

    char *path = ch_strdup(runtime->config_path);
    ch_config *disk_config = NULL;
    ch_config *incoming = NULL;
    char *backup_path = NULL;
    ch_error config_error;
    if (path == NULL) {
        ch_command_fail(command, CH_ERROR_OUT_OF_MEMORY, "copy config path");
        return false;
    }
    ch_status status = ch_config_load(path, &disk_config, &config_error);
    if (status == CH_OK) {
        status = ch_config_parse(document, path, &incoming, &config_error);
    }
    if (status == CH_OK) {
        status = ch_config_write_atomic_document(path, document, &backup_path,
                                                 &config_error);
    }
    if (status != CH_OK) {
        command->status = status;
        command->error = config_error;
        free(backup_path);
        ch_config_free(incoming);
        ch_config_free(disk_config);
        free(path);
        return false;
    }

    bool running = atomic_load_explicit(&runtime->running,
                                        memory_order_acquire);
    if (!ch_runtime_apply_config(runtime, command, path, running)) {
        ch_status apply_status = command->status;
        ch_error apply_error = command->error;
        ch_error restore_error;
        ch_status restore_status = ch_config_write_atomic_document(
            path, ch_config_document(disk_config), NULL, &restore_error);
        if (restore_status != CH_OK) {
            ch_error_set(&command->error, CH_ERROR_INTERNAL,
                         "%s; restore config: %s", apply_error.message,
                         restore_error.message);
            command->status = CH_ERROR_INTERNAL;
        } else {
            command->status = apply_status;
            command->error = apply_error;
        }
        free(backup_path);
        ch_config_free(incoming);
        ch_config_free(disk_config);
        free(path);
        return false;
    }
    ch_runtime_refresh_network_watcher(runtime);
    command->response = ch_runtime_import_result_json(runtime, backup_path);
    free(backup_path);
    ch_config_free(incoming);
    ch_config_free(disk_config);
    free(path);
    return true;
}

static bool ch_runtime_persist_config_mutation(ch_runtime *runtime,
                                               const char *mutation,
                                               const char *response_operation,
                                               const char *request_json,
                                               bool include_backup,
                                               ch_command *command) {
    if (runtime->config_path == NULL || runtime->config_path[0] == '\0') {
        ch_command_fail(command, CH_ERROR_INVALID_STATE,
                        "configuration persistence requires daemon config path");
        return false;
    }
    char *path = ch_strdup(runtime->config_path);
    ch_config *disk_config = NULL;
    char *document = NULL;
    char *backup_path = NULL;
    ch_error config_error;
    if (path == NULL) {
        ch_command_fail(command, CH_ERROR_OUT_OF_MEMORY, "copy config path");
        return false;
    }
    ch_status status = ch_config_load(path, &disk_config, &config_error);
    if (status == CH_OK) {
        status = ch_config_mutate_document_json(
            disk_config, runtime->active_profile, mutation, request_json,
            &document, &config_error);
    }
    if (status == CH_OK) {
        status = ch_config_write_atomic_document(path, document, &backup_path,
                                                 &config_error);
    }
    if (status != CH_OK) {
        command->status = status;
        command->error = config_error;
        free(backup_path);
        free(document);
        ch_config_free(disk_config);
        free(path);
        return false;
    }
    bool running = atomic_load_explicit(&runtime->running,
                                        memory_order_acquire);
    if (!ch_runtime_apply_config(runtime, command, path, running)) {
        ch_status apply_status = command->status;
        ch_error apply_error = command->error;
        ch_error restore_error;
        ch_status restore_status = ch_config_write_atomic_document(
            path, ch_config_document(disk_config), NULL, &restore_error);
        if (restore_status != CH_OK) {
            ch_error_set(&command->error, CH_ERROR_INTERNAL,
                         "%s; restore config: %s", apply_error.message,
                         restore_error.message);
            command->status = CH_ERROR_INTERNAL;
        } else {
            command->status = apply_status;
            command->error = apply_error;
        }
        free(backup_path);
        free(document);
        ch_config_free(disk_config);
        free(path);
        return false;
    }
    ch_runtime_refresh_network_watcher(runtime);
    command->response = ch_config_query_payload_json(
        runtime->config, runtime->active_profile, response_operation,
        request_json, &command->error);
    if (command->response == NULL) {
        command->status = command->error.code == CH_OK ?
            CH_ERROR_OUT_OF_MEMORY : command->error.code;
    } else if (include_backup) {
        command->response = ch_runtime_json_with_backup(command->response,
                                                        backup_path);
        if (command->response == NULL) {
            command->status = CH_ERROR_OUT_OF_MEMORY;
            ch_error_set(&command->error, CH_ERROR_OUT_OF_MEMORY,
                         "encode configuration persistence response");
        }
    }
    free(backup_path);
    free(document);
    ch_config_free(disk_config);
    free(path);
    return command->status == CH_OK;
}

static bool ch_runtime_refresh_rule_feeds(
    ch_runtime *runtime, ch_rule_feed_kind kind, const char *request_json,
    ch_command *command) {
    if (runtime->config_path == NULL || runtime->config_path[0] == '\0' ||
        runtime->config == NULL) {
        ch_command_fail(command, CH_ERROR_INVALID_STATE,
                        "rule feed refresh requires daemon config path");
        return false;
    }
    char *response = ch_config_refresh_rule_feeds_json(
        runtime->config, runtime->active_profile, kind, request_json,
        &command->error);
    if (response == NULL) {
        command->status = command->error.code == CH_OK ?
            CH_ERROR_OUT_OF_MEMORY : command->error.code;
        return false;
    }
    char *path = ch_strdup(runtime->config_path);
    char *document = NULL;
    char *backup_path = NULL;
    ch_error write_error;
    ch_status status = path == NULL ? CH_ERROR_OUT_OF_MEMORY :
        ch_config_document_set_active(runtime->config,
                                      runtime->active_profile, &document,
                                      &write_error);
    if (status == CH_OK) {
        status = ch_config_write_atomic_document(
            path, document, &backup_path, &write_error);
    }
    if (path == NULL) {
        ch_error_set(&write_error, CH_ERROR_OUT_OF_MEMORY,
                     "copy rule feed config path");
    }
    if (status != CH_OK) {
        command->status = status;
        command->error = write_error;
        free(response); free(path); free(document); free(backup_path);
        return false;
    }
    bool running = atomic_load_explicit(&runtime->running,
                                        memory_order_acquire);
    if (!ch_runtime_apply_config(runtime, command, path, running)) {
        free(response); free(path); free(document); free(backup_path);
        return false;
    }
    ch_runtime_refresh_network_watcher(runtime);
    command->response = ch_runtime_json_with_backup(response, backup_path);
    if (command->response == NULL) {
        ch_command_fail(command, CH_ERROR_OUT_OF_MEMORY,
                        "encode rule feed refresh response");
    }
    free(path);
    free(document);
    free(backup_path);
    return command->status == CH_OK;
}

static bool ch_runtime_prompt_action(ch_runtime *runtime,
                                     ch_command *command,
                                     bool promote_silent) {
    ch_prompt_action_options options;
    ch_status status = ch_prompt_action_options_parse(
        command->payload, !promote_silent, &options, &command->error);
    if (status != CH_OK) {
        command->status = status;
        return false;
    }
    ch_prompt_snapshot snapshot;
    bool allow = options.allow;
    if (promote_silent) {
        status = ch_prompt_manager_silent_decision(
            runtime->prompts, options.id, &snapshot, &allow,
            &command->error);
        if (status == CH_OK && strcasecmp(options.scope, "once") == 0) {
            status = CH_ERROR_INVALID_ARGUMENT;
            ch_error_set(&command->error, status,
                         "silent decisions require session, until_quit, or "
                         "forever scope");
        }
    } else {
        status = ch_prompt_manager_resolve(
            runtime->prompts, options.id, options.allow, &snapshot,
            &command->error);
    }
    if (status != CH_OK) {
        command->status = status;
        ch_prompt_action_options_clear(&options);
        return false;
    }
    if (strcasecmp(options.scope, "once") == 0) {
        ch_json_buffer response;
        ch_json_init(&response);
        int okay = ch_json_append(&response, "{\"resolved\":true,\"id\":") &&
            ch_json_append_string(&response, options.id) &&
            ch_json_append(&response, ",\"action\":") &&
            ch_json_append_string(&response, allow ? "allow" : "block") &&
            ch_json_append(&response, ",\"scope\":\"once\"}");
        command->response = okay ? ch_json_take(&response) : NULL;
        ch_json_dispose(&response);
        if (command->response == NULL) {
            command->status = CH_ERROR_OUT_OF_MEMORY;
            ch_error_set(&command->error, command->status,
                         "encode prompt resolution");
        }
        ch_prompt_snapshot_clear(&snapshot);
        ch_prompt_action_options_clear(&options);
        return command->response != NULL;
    }
    if (strcasecmp(options.scope, "until_quit") == 0 &&
        snapshot.process_pid <= 0) {
        command->status = CH_ERROR_INVALID_ARGUMENT;
        ch_error_set(&command->error, command->status,
                     "until_quit requires an attributed process");
        ch_prompt_snapshot_clear(&snapshot);
        ch_prompt_action_options_clear(&options);
        return false;
    }
    char *rule_request = ch_prompt_rule_request_json(
        &snapshot, runtime->config, allow, options.match_host,
        options.match_port, options.match_protocol, &command->error);
    if (rule_request == NULL) {
        command->status = command->error.code;
        ch_prompt_snapshot_clear(&snapshot);
        ch_prompt_action_options_clear(&options);
        return false;
    }
    if (strcasecmp(options.scope, "forever") == 0) {
        bool persisted = ch_runtime_persist_config_mutation(
            runtime, "create_rule", "rules_persistence", rule_request,
            true, command);
        free(rule_request);
        ch_prompt_snapshot_clear(&snapshot);
        ch_prompt_action_options_clear(&options);
        return persisted;
    }
    command->response = ch_temporary_rules_create_from_rule_json(
        runtime->temporary_rules, rule_request, options.ttl_seconds,
        strcasecmp(options.scope, "until_quit") == 0 ?
            snapshot.process_pid : 0,
        snapshot.conn_id, snapshot.target, snapshot.target_host,
        &command->error);
    free(rule_request);
    ch_prompt_snapshot_clear(&snapshot);
    ch_prompt_action_options_clear(&options);
    if (command->response == NULL) {
        command->status = command->error.code == CH_OK ?
            CH_ERROR_OUT_OF_MEMORY : command->error.code;
        return false;
    }
    return true;
}

static void ch_command_process(ch_runtime *runtime, ch_command *command) {
    command->status = CH_OK;
    ch_error_clear(&command->error);

    switch (command->kind) {
        case CH_COMMAND_START:
            if (atomic_load_explicit(&runtime->running, memory_order_acquire)) {
                ch_command_fail(command, CH_ERROR_INVALID_STATE, "engine already running");
                break;
            }
            if (!ch_runtime_apply_config(runtime, command, command->payload, true)) {
                break;
            }
            atomic_store_explicit(&runtime->running, true, memory_order_release);
            ch_runtime_refresh_network_watcher(runtime);
            ch_runtime_log(runtime, 1, "native runtime started");
            break;

        case CH_COMMAND_STOP:
            atomic_store_explicit(&runtime->running, false, memory_order_release);
            ch_runtime_refresh_network_watcher(runtime);
            ch_runtime_stop_listeners(runtime);
            ch_runtime_log(runtime, 1, "native runtime stopped");
            break;

        case CH_COMMAND_RELOAD: {
            bool running = atomic_load_explicit(&runtime->running, memory_order_acquire);
            if (ch_runtime_apply_config(runtime, command, command->payload,
                                        running)) {
                ch_runtime_refresh_network_watcher(runtime);
            }
            break;
        }

        case CH_COMMAND_INJECT:
            if (!atomic_load_explicit(&runtime->running, memory_order_acquire)) {
                ch_command_fail(command, CH_ERROR_INVALID_STATE, "runtime is not running");
            } else if (runtime->ip_stack == NULL) {
                ch_command_fail(
                    command,
                    CH_ERROR_UNSUPPORTED,
                    "active profile does not enable a userspace packet stack"
                );
            } else {
                command->status = ch_ip_stack_inject(
                    runtime->ip_stack, command->packet,
                    command->packet_length, &command->error);
            }
            break;

        case CH_COMMAND_QUERY:
            if (strcmp(command->operation, "status") == 0) {
                command->response = ch_runtime_status_json(runtime);
            } else if (strcmp(command->operation, "profiles") == 0) {
                command->response = ch_runtime_profiles_json(runtime);
            } else if (strcmp(command->operation, "traffic") == 0 ||
                       strcmp(command->operation, "traffic_filter") == 0) {
                char *temporary = ch_temporary_rules_snapshot_json(
                    runtime->temporary_rules, runtime->active_profile,
                    &command->error);
                if (temporary == NULL) {
                    command->status = command->error.code;
                    break;
                }
                command->response = ch_traffic_snapshot_json(
                    runtime->traffic, runtime->config,
                    runtime->active_profile, command->payload, temporary,
                    &command->error);
                free(temporary);
            } else if (strcmp(command->operation,
                              "rule_from_connection") == 0) {
                command->response = ch_traffic_rule_request_json(
                    runtime->traffic, runtime->config,
                    runtime->active_profile, command->payload,
                    &command->error);
            } else if (strcmp(command->operation,
                              "cleanup_rule_request") == 0) {
                command->response = ch_traffic_cleanup_request_json(
                    runtime->traffic, runtime->config,
                    runtime->active_profile, command->payload,
                    &command->error);
            } else if (strcmp(command->operation,
                              "temporary_rules") == 0) {
                command->response = ch_temporary_rules_payload_json(
                    runtime->temporary_rules, runtime->active_profile,
                    &command->error);
            } else if (strcmp(command->operation,
                              "pending_prompts") == 0) {
                command->response = ch_prompt_manager_pending_json(
                    runtime->prompts, &command->error);
            } else if (strcmp(command->operation,
                              "silent_decisions") == 0) {
                command->response = ch_prompt_manager_silent_json(
                    runtime->prompts, &command->error);
            } else if (strcmp(command->operation, "servers") == 0 ||
                       strcmp(command->operation, "rules") == 0 ||
                       strcmp(command->operation, "policy_groups") == 0 ||
                       strcmp(command->operation, "rule_sets") == 0 ||
                       strcmp(command->operation, "dns") == 0 ||
                       strcmp(command->operation, "config_settings") == 0 ||
                       strcmp(command->operation, "conditioner") == 0 ||
                       strcmp(command->operation, "developer_settings") == 0 ||
                       strcmp(command->operation, "developer_map_rules") == 0 ||
                       strcmp(command->operation, "developer_breakpoint_rules") == 0 ||
                       strcmp(command->operation, "developer_rewrite_rules") == 0 ||
                       strcmp(command->operation, "rule_subscriptions") == 0 ||
                       strcmp(command->operation, "config") == 0) {
                command->response = ch_config_query_payload_json(
                    runtime->config, runtime->active_profile,
                    command->operation, command->payload, &command->error
                );
            } else if (strcmp(command->operation, "config_export") == 0) {
                if (runtime->config_path == NULL ||
                    runtime->config_path[0] == '\0') {
                    ch_command_fail(command, CH_ERROR_INVALID_STATE,
                                    "config export requires daemon config path");
                    break;
                }
                ch_config *disk_config = NULL;
                command->status = ch_config_load(runtime->config_path,
                                                 &disk_config,
                                                 &command->error);
                if (command->status != CH_OK) break;
                const char *document = ch_config_document(disk_config);
                if (document == NULL ||
                    strlen(document) > CH_RUNTIME_MAX_CONFIG_TRANSFER_BYTES) {
                    ch_config_free(disk_config);
                    ch_command_fail(command, CH_ERROR_INVALID_STATE,
                                    "config exceeds export size limit");
                    break;
                }
                command->response = ch_strdup(document);
                ch_config_free(disk_config);
            } else if (strcmp(command->operation, "test_rule") == 0) {
                command->status = ch_rule_explain_request_json(
                    runtime->config, runtime->active_profile, command->payload,
                    &command->response, &command->error
                );
            } else {
                ch_command_fail(command, CH_ERROR_UNSUPPORTED, "unknown runtime query operation");
                break;
            }
            if (command->response == NULL && command->status == CH_OK) {
                if (command->error.code == CH_OK) {
                    ch_command_fail(command, CH_ERROR_OUT_OF_MEMORY, "encode query response");
                } else {
                    command->status = command->error.code;
                }
            }
            break;

        case CH_COMMAND_MUTATE:
            if (strcmp(command->operation, "connect") == 0) {
                if (atomic_load_explicit(&runtime->running, memory_order_acquire)) {
                    ch_command_fail(command, CH_ERROR_INVALID_STATE, "engine already running");
                    break;
                }
                if (!ch_runtime_start_listeners(runtime, runtime->config,
                                                runtime->active_profile, command)) {
                    break;
                }
                atomic_store_explicit(&runtime->running, true, memory_order_release);
                ch_runtime_refresh_network_watcher(runtime);
                command->response = ch_runtime_status_json(runtime);
            } else if (strcmp(command->operation, "disconnect") == 0) {
                atomic_store_explicit(&runtime->running, false,
                                      memory_order_release);
                ch_runtime_refresh_network_watcher(runtime);
                ch_runtime_stop_listeners(runtime);
                command->response = ch_runtime_status_json(runtime);
            } else if (strcmp(command->operation, "set_active_profile") == 0) {
                char *name = ch_json_request_string(command->payload, "name", &command->error);
                if (name == NULL) {
                    command->status = command->error.code;
                    break;
                }
                ch_runtime_trim_in_place(name);
                if (name[0] == '\0') {
                    ch_command_fail(command, CH_ERROR_INVALID_ARGUMENT,
                                    "profile name is required");
                    free(name);
                    break;
                }
                if (runtime->config == NULL || !ch_config_has_profile(runtime->config, name)) {
                    ch_error_set(&command->error, CH_ERROR_NOT_FOUND,
                                 "profile %s not found", name);
                    command->status = CH_ERROR_NOT_FOUND;
                    free(name);
                    break;
                }
                bool switched = ch_runtime_switch_profile(runtime, name,
                                                          command);
                free(name);
                if (!switched) break;
                command->response = ch_runtime_status_json(runtime);
            } else if (strcmp(command->operation,
                              "persist_active_profile") == 0) {
                char *name = ch_json_request_string(command->payload, "name",
                                                    &command->error);
                if (name == NULL) {
                    command->status = command->error.code;
                    break;
                }
                ch_runtime_trim_in_place(name);
                if (name[0] == '\0') {
                    ch_command_fail(command, CH_ERROR_INVALID_ARGUMENT,
                                    "profile name is required");
                    free(name);
                    break;
                }
                bool persisted = ch_runtime_persist_active_profile(
                    runtime, name, command);
                free(name);
                if (!persisted) break;
            } else if (strcmp(command->operation, "config_import") == 0) {
                if (!ch_runtime_import_config(runtime, command->payload,
                                              command)) {
                    break;
                }
            } else if (strcmp(command->operation,
                              "create_temporary_rule_from_connection") == 0) {
                command->response =
                    ch_temporary_rules_create_from_connection_json(
                        runtime->temporary_rules, runtime->traffic,
                        runtime->config, runtime->active_profile,
                        command->payload, &command->error);
                if (command->response == NULL) {
                    command->status = command->error.code;
                    break;
                }
            } else if (strcmp(command->operation,
                              "remove_temporary_rule") == 0) {
                command->response = ch_temporary_rules_remove_json(
                    runtime->temporary_rules, command->payload,
                    &command->error);
                if (command->response == NULL) {
                    command->status = command->error.code;
                    break;
                }
            } else if (strcmp(command->operation, "resolve_prompt") == 0) {
                if (!ch_runtime_prompt_action(runtime, command, false)) {
                    break;
                }
            } else if (strcmp(command->operation,
                              "promote_silent_decision") == 0) {
                if (!ch_runtime_prompt_action(runtime, command, true)) {
                    break;
                }
            } else if (strcmp(command->operation,
                              "create_rule_from_connection") == 0) {
                char *request = ch_traffic_rule_request_json(
                    runtime->traffic, runtime->config,
                    runtime->active_profile, command->payload,
                    &command->error);
                if (request == NULL) {
                    command->status = command->error.code;
                    break;
                }
                bool persisted = ch_runtime_persist_config_mutation(
                    runtime, "create_rule", "rules_persistence", request,
                    true, command);
                free(request);
                if (!persisted) break;
            } else if (strcmp(command->operation,
                              "cleanup_rule_from_traffic") == 0) {
                char *request = ch_traffic_cleanup_request_json(
                    runtime->traffic, runtime->config,
                    runtime->active_profile, command->payload,
                    &command->error);
                if (request == NULL) {
                    command->status = command->error.code;
                    break;
                }
                bool persisted = ch_runtime_persist_config_mutation(
                    runtime, "replace_rules", "rules_persistence", request,
                    true, command);
                free(request);
                if (!persisted) break;
            } else if (strcmp(command->operation, "update_dns") == 0) {
                if (!ch_runtime_persist_config_mutation(
                        runtime, "update_dns", "dns", command->payload,
                        false, command)) break;
            } else if (strcmp(command->operation,
                              "update_conditioner") == 0) {
                if (!ch_runtime_persist_config_mutation(
                        runtime, "update_conditioner", "conditioner",
                        command->payload, true, command)) break;
            } else if (strcmp(command->operation,
                              "update_config_settings") == 0) {
                if (!ch_runtime_persist_config_mutation(
                        runtime, "update_config_settings", "config_settings",
                        command->payload, true, command)) break;
            } else if (strcmp(command->operation, "replace_rules") == 0 ||
                       strcmp(command->operation, "create_rule") == 0) {
                if (!ch_runtime_persist_config_mutation(
                        runtime, command->operation, "rules_persistence",
                        command->payload, true, command)) break;
            } else if (strcmp(command->operation,
                              "replace_policy_groups") == 0) {
                if (!ch_runtime_persist_config_mutation(
                        runtime, "replace_policy_groups",
                        "policy_groups_persistence", command->payload, true,
                        command)) break;
            } else if (strcmp(command->operation,
                              "replace_rule_sets") == 0) {
                if (!ch_runtime_persist_config_mutation(
                        runtime, "replace_rule_sets", "rule_sets_persistence",
                        command->payload, true, command)) break;
            } else if (strcmp(command->operation,
                              "replace_rule_subscriptions") == 0) {
                if (!ch_runtime_persist_config_mutation(
                        runtime, "replace_rule_subscriptions",
                        "rule_subscriptions_persistence", command->payload,
                        true, command)) break;
            } else if (strcmp(command->operation,
                              "refresh_rule_sets") == 0) {
                if (!ch_runtime_refresh_rule_feeds(
                        runtime, CH_RULE_FEED_RULE_SET, command->payload,
                        command)) break;
            } else if (strcmp(command->operation,
                              "refresh_rule_subscriptions") == 0) {
                if (!ch_runtime_refresh_rule_feeds(
                        runtime, CH_RULE_FEED_SUBSCRIPTION,
                        command->payload, command)) break;
            } else if (strcmp(command->operation,
                              "select_policy_group") == 0) {
                if (!ch_runtime_persist_config_mutation(
                        runtime, "select_policy_group",
                        "policy_group_selection", command->payload, true,
                        command)) break;
            } else if (strcmp(command->operation,
                              "update_developer_settings") == 0) {
                if (!ch_runtime_persist_config_mutation(
                        runtime, "update_developer_settings",
                        "developer_settings", command->payload, true,
                        command)) break;
            } else if (strcmp(command->operation,
                              "replace_developer_map_rules") == 0 ||
                       strcmp(command->operation,
                              "replace_developer_breakpoint_rules") == 0 ||
                       strcmp(command->operation,
                              "replace_developer_rewrite_rules") == 0 ||
                       strcmp(command->operation,
                              "delete_developer_map_rule") == 0 ||
                       strcmp(command->operation,
                              "delete_developer_breakpoint_rule") == 0 ||
                       strcmp(command->operation,
                              "delete_developer_rewrite_rule") == 0) {
                if (!ch_runtime_persist_config_mutation(
                        runtime, command->operation, "developer_persistence",
                        command->payload, true, command)) break;
            } else {
                ch_command_fail(command, CH_ERROR_UNSUPPORTED, "unknown runtime mutation operation");
                break;
            }
            if (command->response == NULL && command->status == CH_OK) {
                ch_command_fail(command, CH_ERROR_OUT_OF_MEMORY, "encode mutation response");
            }
            break;

        case CH_COMMAND_SHUTDOWN:
            atomic_store_explicit(&runtime->running, false,
                                  memory_order_release);
            ch_runtime_refresh_network_watcher(runtime);
            ch_runtime_stop_listeners(runtime);
            uv_timer_stop(&runtime->packet_timer);
            uv_close((uv_handle_t *)&runtime->packet_timer, NULL);
            uv_close((uv_handle_t *)&runtime->command_async, NULL);
            break;
    }
}

static void ch_runtime_packet_tick(uv_timer_t *timer) {
    ch_runtime *runtime = timer->data;
    ch_ip_stack_tick(runtime->ip_stack);
}

static void ch_command_complete(ch_command *command) {
    uv_mutex_lock(&command->mutex);
    command->complete = 1;
    uv_cond_signal(&command->condition);
    uv_mutex_unlock(&command->mutex);
}

static void ch_runtime_drain_commands(uv_async_t *async) {
    ch_runtime *runtime = async->data;
    for (;;) {
        uv_mutex_lock(&runtime->queue_mutex);
        ch_command *command = runtime->queue_head;
        if (command != NULL) {
            runtime->queue_head = command->next;
            if (runtime->queue_head == NULL) {
                runtime->queue_tail = NULL;
            }
        }
        uv_mutex_unlock(&runtime->queue_mutex);
        if (command == NULL) {
            return;
        }
        ch_command_process(runtime, command);
        ch_command_complete(command);
    }
}

static void ch_runtime_thread(void *context) {
    ch_runtime *runtime = context;
    (void)uv_run(&runtime->loop, UV_RUN_DEFAULT);
}

static void ch_command_dispose(ch_command *command) {
    if (command == NULL) {
        return;
    }
    uv_cond_destroy(&command->condition);
    uv_mutex_destroy(&command->mutex);
    free(command->operation);
    free(command->payload);
    free(command->packet);
}

static ch_status ch_runtime_dispatch(
    ch_runtime *runtime,
    ch_command_kind kind,
    const char *operation,
    const char *payload,
    const uint8_t *packet,
    size_t packet_length,
    char **response,
    ch_error *error
) {
    ch_error_clear(error);
    if (runtime == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "runtime is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (response != NULL) {
        *response = NULL;
    }

    ch_command command;
    memset(&command, 0, sizeof(command));
    command.kind = kind;
    command.operation = operation == NULL ? NULL : ch_strdup(operation);
    command.payload = payload == NULL ? NULL : ch_strdup(payload);
    if ((operation != NULL && command.operation == NULL) ||
        (payload != NULL && command.payload == NULL)) {
        free(command.operation);
        free(command.payload);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate runtime command");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (packet_length > 0U) {
        command.packet = malloc(packet_length);
        if (command.packet == NULL) {
            free(command.operation);
            free(command.payload);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate packet command");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        memcpy(command.packet, packet, packet_length);
        command.packet_length = packet_length;
    }
    if (uv_mutex_init(&command.mutex) != 0) {
        free(command.operation);
        free(command.payload);
        free(command.packet);
        ch_error_set(error, CH_ERROR_INTERNAL, "initialize command mutex");
        return CH_ERROR_INTERNAL;
    }
    if (uv_cond_init(&command.condition) != 0) {
        uv_mutex_destroy(&command.mutex);
        free(command.operation);
        free(command.payload);
        free(command.packet);
        ch_error_set(error, CH_ERROR_INTERNAL, "initialize command condition");
        return CH_ERROR_INTERNAL;
    }

    uv_mutex_lock(&command.mutex);
    uv_mutex_lock(&runtime->queue_mutex);
    if (runtime->queue_tail == NULL) {
        runtime->queue_head = &command;
    } else {
        runtime->queue_tail->next = &command;
    }
    runtime->queue_tail = &command;
    uv_mutex_unlock(&runtime->queue_mutex);
    (void)uv_async_send(&runtime->command_async);
    while (!command.complete) {
        uv_cond_wait(&command.condition, &command.mutex);
    }
    uv_mutex_unlock(&command.mutex);

    ch_status status = command.status;
    if (error != NULL) {
        *error = command.error;
    }
    if (response != NULL) {
        *response = command.response;
        command.response = NULL;
    }
    free(command.response);
    ch_command_dispose(&command);
    return status;
}

ch_runtime *ch_runtime_create(const ch_runtime_options *options, ch_error *error) {
    ch_error_clear(error);
    ch_runtime *runtime = calloc(1U, sizeof(*runtime));
    if (runtime == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate runtime");
        return NULL;
    }
    runtime->active_profile = ch_strdup("default");
    if (runtime->active_profile == NULL) {
        free(runtime);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate default profile");
        return NULL;
    }
    if (options != NULL) {
        runtime->options = *options;
    }
    runtime->traffic = ch_traffic_store_create(512U, error);
    if (runtime->traffic == NULL) {
        free(runtime->active_profile);
        free(runtime);
        return NULL;
    }
    runtime->temporary_rules = ch_temporary_rules_create(128U, error);
    if (runtime->temporary_rules == NULL) {
        ch_traffic_store_destroy(runtime->traffic);
        free(runtime->active_profile);
        free(runtime);
        return NULL;
    }
    runtime->prompts = ch_prompt_manager_create(error);
    if (runtime->prompts == NULL) {
        ch_temporary_rules_destroy(runtime->temporary_rules);
        ch_traffic_store_destroy(runtime->traffic);
        free(runtime->active_profile);
        free(runtime);
        return NULL;
    }
    atomic_init(&runtime->running, false);
    if (uv_loop_init(&runtime->loop) != 0 || uv_mutex_init(&runtime->queue_mutex) != 0) {
        ch_prompt_manager_destroy(runtime->prompts);
        ch_temporary_rules_destroy(runtime->temporary_rules);
        ch_traffic_store_destroy(runtime->traffic);
        free(runtime->active_profile);
        free(runtime);
        ch_error_set(error, CH_ERROR_INTERNAL, "initialize runtime loop");
        return NULL;
    }
    if (uv_async_init(&runtime->loop, &runtime->command_async,
                      ch_runtime_drain_commands) != 0) {
        uv_mutex_destroy(&runtime->queue_mutex);
        (void)uv_loop_close(&runtime->loop);
        ch_prompt_manager_destroy(runtime->prompts);
        ch_temporary_rules_destroy(runtime->temporary_rules);
        ch_traffic_store_destroy(runtime->traffic);
        free(runtime->active_profile);
        free(runtime);
        ch_error_set(error, CH_ERROR_INTERNAL, "initialize runtime command queue");
        return NULL;
    }
    runtime->command_async.data = runtime;
    if (uv_timer_init(&runtime->loop, &runtime->packet_timer) != 0) {
        uv_close((uv_handle_t *)&runtime->command_async, NULL);
        (void)uv_run(&runtime->loop, UV_RUN_DEFAULT);
        uv_mutex_destroy(&runtime->queue_mutex);
        (void)uv_loop_close(&runtime->loop);
        ch_prompt_manager_destroy(runtime->prompts);
        ch_temporary_rules_destroy(runtime->temporary_rules);
        ch_traffic_store_destroy(runtime->traffic);
        free(runtime->active_profile);
        free(runtime);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "initialize runtime packet timer");
        return NULL;
    }
    runtime->packet_timer.data = runtime;
    if (uv_timer_start(&runtime->packet_timer, ch_runtime_packet_tick,
                       10U, 10U) != 0) {
        uv_close((uv_handle_t *)&runtime->packet_timer, NULL);
        uv_close((uv_handle_t *)&runtime->command_async, NULL);
        (void)uv_run(&runtime->loop, UV_RUN_DEFAULT);
        uv_mutex_destroy(&runtime->queue_mutex);
        (void)uv_loop_close(&runtime->loop);
        ch_prompt_manager_destroy(runtime->prompts);
        ch_temporary_rules_destroy(runtime->temporary_rules);
        ch_traffic_store_destroy(runtime->traffic);
        free(runtime->active_profile);
        free(runtime);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "start runtime packet timer");
        return NULL;
    }
    uv_unref((uv_handle_t *)&runtime->packet_timer);
    if (uv_thread_create(&runtime->thread, ch_runtime_thread, runtime) != 0) {
        uv_timer_stop(&runtime->packet_timer);
        uv_close((uv_handle_t *)&runtime->packet_timer, NULL);
        uv_close((uv_handle_t *)&runtime->command_async, NULL);
        (void)uv_run(&runtime->loop, UV_RUN_DEFAULT);
        uv_mutex_destroy(&runtime->queue_mutex);
        (void)uv_loop_close(&runtime->loop);
        ch_prompt_manager_destroy(runtime->prompts);
        ch_temporary_rules_destroy(runtime->temporary_rules);
        ch_traffic_store_destroy(runtime->traffic);
        free(runtime->active_profile);
        free(runtime);
        ch_error_set(error, CH_ERROR_INTERNAL, "start runtime thread");
        return NULL;
    }
    return runtime;
}

void ch_runtime_destroy(ch_runtime *runtime) {
    if (runtime == NULL) {
        return;
    }
    ch_error ignored;
    (void)ch_runtime_dispatch(runtime, CH_COMMAND_SHUTDOWN, NULL, NULL, NULL, 0U, NULL, &ignored);
    (void)uv_thread_join(&runtime->thread);
    uv_mutex_destroy(&runtime->queue_mutex);
    (void)uv_loop_close(&runtime->loop);
    free(runtime->config_path);
    ch_config_free(runtime->config);
    free(runtime->active_profile);
    ch_prompt_manager_destroy(runtime->prompts);
    ch_temporary_rules_destroy(runtime->temporary_rules);
    ch_traffic_store_destroy(runtime->traffic);
    free(runtime);
}

ch_status ch_runtime_start(ch_runtime *runtime, const char *config_path, ch_error *error) {
    if (config_path == NULL) {
        config_path = "";
    }
    return ch_runtime_dispatch(runtime, CH_COMMAND_START, NULL, config_path, NULL, 0U, NULL, error);
}

ch_status ch_runtime_stop(ch_runtime *runtime, ch_error *error) {
    return ch_runtime_dispatch(runtime, CH_COMMAND_STOP, NULL, NULL, NULL, 0U, NULL, error);
}

ch_status ch_runtime_reload(ch_runtime *runtime, const char *config_path, ch_error *error) {
    if (config_path == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "config path is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    return ch_runtime_dispatch(runtime, CH_COMMAND_RELOAD, NULL, config_path, NULL, 0U, NULL, error);
}

ch_status ch_runtime_inject_packet(
    ch_runtime *runtime,
    const uint8_t *packet,
    size_t length,
    ch_error *error
) {
    if (packet == NULL || length == 0U) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "non-empty packet is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    return ch_runtime_dispatch(runtime, CH_COMMAND_INJECT, NULL, NULL, packet, length, NULL, error);
}

bool ch_runtime_is_running(ch_runtime *runtime) {
    return runtime != NULL && atomic_load_explicit(&runtime->running, memory_order_acquire);
}

ch_status ch_runtime_query(
    ch_runtime *runtime,
    const char *operation,
    const char *request_json,
    char **response_json,
    ch_error *error
) {
    if (operation == NULL || operation[0] == '\0' || response_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "operation and response are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    return ch_runtime_dispatch(
        runtime,
        CH_COMMAND_QUERY,
        operation,
        request_json == NULL ? "{}" : request_json,
        NULL,
        0U,
        response_json,
        error
    );
}

ch_status ch_runtime_mutate(
    ch_runtime *runtime,
    const char *operation,
    const char *request_json,
    char **response_json,
    ch_error *error
) {
    if (operation == NULL || operation[0] == '\0' || response_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "operation and response are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    return ch_runtime_dispatch(
        runtime,
        CH_COMMAND_MUTATE,
        operation,
        request_json == NULL ? "{}" : request_json,
        NULL,
        0U,
        response_json,
        error
    );
}

void ch_string_free(char *string) {
    free(string);
}
