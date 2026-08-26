#include "internal.h"

#include <ctype.h>
#include <inttypes.h>
#include <stdlib.h>
#include <string.h>

#include "clambhook/config.h"
#include "clambhook/json.h"
#include "clambhook/rules.h"

static ch_status request_optional_string(const char *request_json,
                                         const char *key,
                                         char **out_value,
                                         ch_error *error);

static void trim_in_place(char *value) {
    if (value == NULL) return;
    char *start = value;
    while (*start != '\0' && isspace((unsigned char)*start) != 0) ++start;
    char *end = start + strlen(start);
    while (end > start && isspace((unsigned char)end[-1]) != 0) --end;
    size_t length = (size_t)(end - start);
    memmove(value, start, length);
    value[length] = '\0';
}

static char *empty_array(void) {
    return ch_strdup("[]");
}

static char *optional_config_string(const ch_config_table *table,
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

static bool optional_config_bool(const ch_config_table *table,
                                 const char *key, bool fallback) {
    bool value = fallback;
    ch_error ignored;
    if (table != NULL) {
        (void)ch_config_table_get_bool(table, key, &value, &ignored);
    }
    return value;
}

static int64_t optional_config_int(const ch_config_table *table,
                                   const char *key, int64_t fallback) {
    int64_t value = fallback;
    ch_error ignored;
    if (table != NULL) {
        (void)ch_config_table_get_int(table, key, &value, &ignored);
    }
    return value;
}

static double optional_config_double(const ch_config_table *table,
                                     const char *key, double fallback) {
    double value = fallback;
    ch_error ignored;
    if (table != NULL) {
        (void)ch_config_table_get_double(table, key, &value, &ignored);
    }
    return value;
}

static char *config_array_json_or_empty(const ch_config_array *array,
                                        ch_error *error) {
    char *json = NULL;
    if (array == NULL) return empty_array();
    if (ch_config_array_json(array, &json, error) != CH_OK) return NULL;
    return json;
}

static const ch_config_table *select_profile(const ch_config *config,
                                             const char *name) {
    if (config == NULL) return NULL;
    if (name != NULL && name[0] != '\0') {
        size_t count = ch_config_profile_count(config);
        for (size_t index = 0U; index < count; ++index) {
            const ch_config_table *profile = ch_config_profile_at(config, index);
            char *candidate = NULL;
            ch_error ignored;
            bool matches = ch_config_table_get_string(profile, "name", &candidate,
                                                       &ignored) == CH_OK &&
                           strcmp(candidate, name) == 0;
            free(candidate);
            if (matches) return profile;
        }
        return NULL;
    }
    return ch_config_active_profile(config);
}

static char *ch_config_dns_payload_json(const ch_config *config,
                                        const char *profile_name,
                                        ch_error *error) {
    const ch_config_table *profile = select_profile(config, profile_name);
    if (profile == NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "profile %s not found",
                     profile_name == NULL ? "" : profile_name);
        return NULL;
    }
    const ch_config_table *dns = ch_config_table_get_table(profile, "dns");
    bool enabled = optional_config_bool(dns, "enabled", false);
    char *name = optional_config_string(profile, "name");
    char *timeout = optional_config_string(dns, "timeout");
    char *upstreams = config_array_json_or_empty(
        dns == NULL ? NULL : ch_config_table_get_array(dns, "upstream"),
        error);
    if (name == NULL || timeout == NULL || upstreams == NULL) {
        free(name);
        free(timeout);
        free(upstreams);
        if (error == NULL || error->code == CH_OK) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "encode DNS payload");
        }
        return NULL;
    }
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"profile\":") &&
        ch_json_append_string(&json, name) &&
        ch_json_append(&json, ",\"strategy\":") &&
        ch_json_append_string(&json, enabled ? "encrypted" : "route") &&
        ch_json_append_format(&json, ",\"enabled\":%s",
                              enabled ? "true" : "false");
    if (okay && enabled) {
        okay = ch_json_append(&json, ",\"timeout\":") &&
            ch_json_append_string(&json, timeout[0] == '\0' ? "5s" : timeout);
    }
    if (okay) {
        okay = ch_json_append(&json, ",\"upstreams\":") &&
            ch_json_append(&json, upstreams) &&
            ch_json_append_format(&json, ",\"intercepts_port_53\":%s}",
                                  enabled ? "true" : "false");
    }
    free(name);
    free(timeout);
    free(upstreams);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "encode DNS payload");
    }
    return result;
}

static int ch_config_append_tun_payload(ch_json_buffer *json,
                                        const ch_config_table *tun,
                                        ch_error *error) {
    bool enabled = optional_config_bool(tun, "enabled", false);
    char *name = optional_config_string(tun, "name");
    char *chain = optional_config_string(tun, "chain");
    int64_t mtu = optional_config_int(tun, "mtu", 0);
    const ch_config_array *addresses = tun == NULL ? NULL :
        ch_config_table_get_array(tun, "addresses");
    const ch_config_array *routes = tun == NULL ? NULL :
        ch_config_table_get_array(tun, "routes");
    const ch_config_array *exclude_cidrs = tun == NULL ? NULL :
        ch_config_table_get_array(tun, "exclude_cidrs");
    int okay = name != NULL && chain != NULL &&
        ch_json_append_format(json, "{\"enabled\":%s",
                              enabled ? "true" : "false");
    if (okay && name[0] != '\0') {
        okay = ch_json_append(json, ",\"name\":") &&
            ch_json_append_string(json, name);
    }
    if (okay && chain[0] != '\0') {
        okay = ch_json_append(json, ",\"chain\":") &&
            ch_json_append_string(json, chain);
    }
    if (okay && mtu != 0) {
        okay = ch_json_append_format(json, ",\"mtu\":%" PRId64, mtu);
    }
    const struct {
        const char *name;
        const ch_config_array *array;
    } arrays[] = {
        {"addresses", addresses},
        {"routes", routes},
        {"exclude_cidrs", exclude_cidrs}
    };
    for (size_t index = 0U; okay && index < 3U; ++index) {
        if (ch_config_array_count(arrays[index].array) == 0U) continue;
        char *encoded = NULL;
        if (ch_config_array_json(arrays[index].array, &encoded, error) !=
            CH_OK) {
            okay = 0;
        } else {
            okay = ch_json_append(json, ",\"") &&
                ch_json_append(json, arrays[index].name) &&
                ch_json_append(json, "\":") &&
                ch_json_append(json, encoded);
        }
        free(encoded);
    }
    if (okay) okay = ch_json_append(json, "}");
    free(name);
    free(chain);
    return okay;
}

static int ch_config_append_settings_dns(ch_json_buffer *json,
                                         const ch_config_table *dns,
                                         ch_error *error) {
    bool enabled = optional_config_bool(dns, "enabled", false);
    char *timeout = optional_config_string(dns, "timeout");
    if (timeout == NULL) return 0;
    int okay = ch_json_append_format(json, "{\"enabled\":%s",
                                     enabled ? "true" : "false");
    if (okay && timeout[0] != '\0') {
        okay = ch_json_append(json, ",\"timeout\":") &&
            ch_json_append_string(json, timeout);
    }
    const ch_config_array *upstreams = dns == NULL ? NULL :
        ch_config_table_get_array(dns, "upstream");
    if (okay && ch_config_array_count(upstreams) > 0U) {
        char *encoded = NULL;
        if (ch_config_array_json(upstreams, &encoded, error) != CH_OK) {
            okay = 0;
        } else {
            okay = ch_json_append(json, ",\"upstreams\":") &&
                ch_json_append(json, encoded);
        }
        free(encoded);
    }
    if (okay) okay = ch_json_append(json, "}");
    free(timeout);
    return okay;
}

static char *ch_config_settings_payload_json(const ch_config *config,
                                             const char *profile_name,
                                             ch_error *error) {
    const ch_config_table *profile = select_profile(config, profile_name);
    if (profile == NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "profile %s not found",
                     profile_name == NULL ? "" : profile_name);
        return NULL;
    }
    const ch_config_table *listen = ch_config_table_get_table(profile, "listen");
    const ch_config_table *tun = ch_config_table_get_table(listen, "tun");
    const ch_config_table *dns = ch_config_table_get_table(profile, "dns");
    const ch_config_table *prompt = ch_config_table_get_table(
        ch_config_root(config), "prompt");
    char *name = optional_config_string(profile, "name");
    char *socks5 = optional_config_string(listen, "socks5");
    char *socks5_chain = optional_config_string(listen, "socks5_chain");
    char *http = optional_config_string(listen, "http");
    char *http_chain = optional_config_string(listen, "http_chain");
    char *silent_mode = optional_config_string(prompt, "silent_mode");
    char *triggers = config_array_json_or_empty(
        ch_config_table_get_array(profile, "network_trigger"), error);
    if (name == NULL || socks5 == NULL || socks5_chain == NULL ||
        http == NULL || http_chain == NULL || silent_mode == NULL ||
        triggers == NULL) {
        free(name); free(socks5); free(socks5_chain); free(http);
        free(http_chain); free(silent_mode); free(triggers);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode config settings payload");
        return NULL;
    }
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"profile\":") &&
        ch_json_append_string(&json, name) &&
        ch_json_append(&json, ",\"listen\":{\"socks5\":") &&
        ch_json_append_string(&json, socks5) &&
        ch_json_append(&json, ",\"socks5_chain\":") &&
        ch_json_append_string(&json, socks5_chain) &&
        ch_json_append(&json, ",\"http\":") &&
        ch_json_append_string(&json, http) &&
        ch_json_append(&json, ",\"http_chain\":") &&
        ch_json_append_string(&json, http_chain) &&
        ch_json_append(&json, ",\"tun\":") &&
        ch_config_append_tun_payload(&json, tun, error) &&
        ch_json_append(&json, "},\"dns\":") &&
        ch_config_append_settings_dns(&json, dns, error) &&
        ch_json_append(&json, ",\"network_triggers\":") &&
        ch_json_append(&json, triggers) &&
        ch_json_append_format(&json, ",\"prompt\":{\"enabled\":%s",
            optional_config_bool(prompt, "enabled", false) ? "true" : "false");
    int64_t timeout_seconds = optional_config_int(prompt, "timeout_seconds", 0);
    if (okay && timeout_seconds != 0) {
        okay = ch_json_append_format(&json, ",\"timeout_seconds\":%" PRId64,
                                     timeout_seconds);
    }
    if (okay && optional_config_bool(prompt, "default_allow", false)) {
        okay = ch_json_append(&json, ",\"default_allow\":true");
    }
    if (okay && silent_mode[0] != '\0') {
        okay = ch_json_append(&json, ",\"silent_mode\":") &&
            ch_json_append_string(&json, silent_mode);
    }
    if (okay) okay = ch_json_append(&json, "}}");
    free(name); free(socks5); free(socks5_chain); free(http);
    free(http_chain); free(silent_mode); free(triggers);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL && (error == NULL || error->code == CH_OK)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode config settings payload");
    }
    return result;
}

static char *ch_config_conditioner_payload_json(const ch_config *config,
                                                const char *profile_name,
                                                ch_error *error) {
    const ch_config_table *profile = select_profile(config, profile_name);
    if (profile == NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "profile %s not found",
                     profile_name == NULL ? "" : profile_name);
        return NULL;
    }
    const ch_config_table *conditioner = ch_config_table_get_table(
        profile, "conditioner");
    char *name = optional_config_string(profile, "name");
    char *latency = optional_config_string(conditioner, "latency");
    char *jitter = optional_config_string(conditioner, "jitter");
    if (name == NULL || latency == NULL || jitter == NULL) {
        free(name); free(latency); free(jitter);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode conditioner payload");
        return NULL;
    }
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"profile\":") &&
        ch_json_append_string(&json, name) &&
        ch_json_append_format(
            &json,
            ",\"enabled\":%s,\"download_kbps\":%" PRId64
            ",\"upload_kbps\":%" PRId64,
            optional_config_bool(conditioner, "enabled", false) ?
                "true" : "false",
            optional_config_int(conditioner, "download_kbps", 0),
            optional_config_int(conditioner, "upload_kbps", 0));
    if (okay && latency[0] != '\0') {
        okay = ch_json_append(&json, ",\"latency\":") &&
            ch_json_append_string(&json, latency);
    }
    if (okay && jitter[0] != '\0') {
        okay = ch_json_append(&json, ",\"jitter\":") &&
            ch_json_append_string(&json, jitter);
    }
    if (okay) {
        okay = ch_json_append_format(
            &json, ",\"loss_percent\":%.17g}",
            optional_config_double(conditioner, "loss_percent", 0.0));
    }
    free(name); free(latency); free(jitter);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode conditioner payload");
    }
    return result;
}

static char *ch_config_developer_settings_payload_json(const ch_config *config,
                                                       ch_error *error) {
    static const char default_headers[] =
        "[\"authorization\",\"proxy-authorization\",\"cookie\","
        "\"set-cookie\",\"x-api-key\",\"api-key\",\"x-auth-token\","
        "\"x-csrf-token\",\"x-xsrf-token\",\"csrf-token\",\"xsrf-token\"]";
    static const char default_query[] =
        "[\"token\",\"access_token\",\"refresh_token\",\"id_token\","
        "\"api_key\",\"apikey\",\"key\",\"secret\",\"password\","
        "\"passwd\",\"code\",\"session\",\"auth\"]";
    const ch_config_table *developer = ch_config_table_get_table(
        ch_config_root(config), "developer");
    const ch_config_array *header_array = developer == NULL ? NULL :
        ch_config_table_get_array(developer, "redact_headers");
    const ch_config_array *query_array = developer == NULL ? NULL :
        ch_config_table_get_array(developer, "redact_query_params");
    const ch_config_array *hosts_array = developer == NULL ? NULL :
        ch_config_table_get_array(developer, "ssl_decrypt_hosts");
    char *headers = ch_config_array_count(header_array) == 0U ?
        ch_strdup(default_headers) : config_array_json_or_empty(header_array,
                                                                error);
    char *query = ch_config_array_count(query_array) == 0U ?
        ch_strdup(default_query) : config_array_json_or_empty(query_array,
                                                              error);
    char *hosts = config_array_json_or_empty(hosts_array, error);
    int64_t capture_limit = optional_config_int(developer, "capture_limit", 0);
    int64_t body_limit = optional_config_int(developer, "body_limit_bytes", 0);
    int64_t header_limit = optional_config_int(
        developer, "header_value_limit_bytes", 0);
    if (capture_limit == 0) capture_limit = 200;
    if (body_limit == 0) body_limit = 65536;
    if (header_limit == 0) header_limit = 8192;
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = headers != NULL && query != NULL && hosts != NULL &&
        ch_json_append_format(
            &json,
            "{\"enabled\":%s,\"mitm_enabled\":%s,"
            "\"no_cache_enabled\":%s,\"capture_limit\":%" PRId64
            ",\"body_limit_bytes\":%" PRId64
            ",\"header_value_limit_bytes\":%" PRId64,
            optional_config_bool(developer, "enabled", false) ? "true" :
                "false",
            optional_config_bool(developer, "mitm_enabled", false) ? "true" :
                "false",
            optional_config_bool(developer, "no_cache_enabled", false) ?
                "true" : "false",
            capture_limit, body_limit, header_limit) &&
        ch_json_append(&json, ",\"redact_headers\":") &&
        ch_json_append(&json, headers) &&
        ch_json_append(&json, ",\"redact_query_params\":") &&
        ch_json_append(&json, query);
    if (okay && ch_config_array_count(hosts_array) > 0U) {
        okay = ch_json_append(&json, ",\"ssl_decrypt_hosts\":") &&
            ch_json_append(&json, hosts);
    }
    if (okay) okay = ch_json_append(&json, "}");
    free(headers); free(query); free(hosts);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL && (error == NULL || error->code == CH_OK)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode developer settings");
    }
    return result;
}

static char *ch_config_subscriptions_payload_json(const ch_config *config,
                                                  const char *profile_name,
                                                  ch_error *error) {
    const ch_config_table *profile = select_profile(config, profile_name);
    if (profile == NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "profile %s not found",
                     profile_name == NULL ? "" : profile_name);
        return NULL;
    }
    char *name = optional_config_string(profile, "name");
    const ch_config_array *subscriptions = ch_config_table_get_array(
        profile, "rule_subscription");
    if (name == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode subscription payload");
        return NULL;
    }
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"profile\":") &&
        ch_json_append_string(&json, name) &&
        ch_json_append(&json, ",\"subscriptions\":[");
    size_t count = ch_config_array_count(subscriptions);
    for (size_t index = 0U; okay && index < count; ++index) {
        const ch_config_table *subscription = ch_config_array_get_table(
            subscriptions, index);
        char *sub_name = optional_config_string(subscription, "name");
        char *url = optional_config_string(subscription, "url");
        char *format = optional_config_string(subscription, "format");
        char *action = optional_config_string(subscription, "action");
        const ch_config_array *networks = ch_config_table_get_array(
            subscription, "networks");
        okay = sub_name != NULL && url != NULL && format != NULL &&
            action != NULL && (index == 0U || ch_json_append(&json, ",")) &&
            ch_json_append(&json, "{\"name\":") &&
            ch_json_append_string(&json, sub_name) &&
            ch_json_append(&json, ",\"url\":") &&
            ch_json_append_string(&json, url) &&
            ch_json_append(&json, ",\"format\":") &&
            ch_json_append_string(&json, format[0] == '\0' ? "auto" : format) &&
            ch_json_append(&json, ",\"action\":") &&
            ch_json_append_string(&json, action[0] == '\0' ? "block" : action);
        if (okay && ch_config_array_count(networks) > 0U) {
            char *encoded = NULL;
            if (ch_config_array_json(networks, &encoded, error) != CH_OK) {
                okay = 0;
            } else {
                okay = ch_json_append(&json, ",\"networks\":") &&
                    ch_json_append(&json, encoded);
            }
            free(encoded);
        }
        if (okay && optional_config_bool(subscription, "disabled", false)) {
            okay = ch_json_append(&json, ",\"disabled\":true");
        }
        if (okay) {
            okay = ch_json_append(
                &json,
                ",\"cached\":false,\"domain_count\":0,\"cidr_count\":0}");
        }
        free(sub_name); free(url); free(format); free(action);
    }
    if (okay) okay = ch_json_append(&json, "]}");
    free(name);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL && (error == NULL || error->code == CH_OK)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode subscription payload");
    }
    return result;
}

static const char *policy_selection_mode(const char *type) {
    if (strcmp(type, "select") == 0) return "manual";
    if (strcmp(type, "fallback") == 0) return "fallback";
    if (strcmp(type, "load-balance") == 0) return "load-balance";
    if (strcmp(type, "smart") == 0) return "smart";
    return "latency";
}

static const char *policy_selection_reason(const char *type) {
    if (strcmp(type, "select") == 0) return "manual";
    if (strcmp(type, "fallback") == 0) return "first_healthy";
    if (strcmp(type, "load-balance") == 0) return "stable_hash";
    if (strcmp(type, "smart") == 0) return "sticky_healthy";
    return "lowest_latency";
}

static int ch_config_append_policy_groups(ch_json_buffer *json,
                                          const ch_config_table *profile,
                                          ch_error *error) {
    const ch_config_array *groups = ch_config_table_get_array(
        profile, "policy_group");
    if (!ch_json_append(json, "[")) return 0;
    for (size_t index = 0U; index < ch_config_array_count(groups); ++index) {
        const ch_config_table *group = ch_config_array_get_table(groups, index);
        char *name = optional_config_string(group, "name");
        char *type = optional_config_string(group, "type");
        char *selected = optional_config_string(group, "selected");
        char *test_url = optional_config_string(group, "test_url");
        char *interval = optional_config_string(group, "interval");
        char *timeout = optional_config_string(group, "timeout");
        char *chains = config_array_json_or_empty(
            ch_config_table_get_array(group, "chains"), error);
        if (selected != NULL && selected[0] == '\0') {
            free(selected);
            selected = NULL;
            const ch_config_array *members = ch_config_table_get_array(
                group, "chains");
            if (ch_config_array_count(members) > 0U &&
                ch_config_array_get_string(members, 0U, &selected, error) !=
                CH_OK) {
                selected = NULL;
            }
        }
        int okay = name != NULL && type != NULL && selected != NULL &&
            test_url != NULL && interval != NULL && timeout != NULL &&
            chains != NULL && (index == 0U || ch_json_append(json, ",")) &&
            ch_json_append(json, "{\"name\":") &&
            ch_json_append_string(json, name) &&
            ch_json_append(json, ",\"type\":") &&
            ch_json_append_string(json, type) &&
            ch_json_append(json, ",\"chains\":") &&
            ch_json_append(json, chains) &&
            ch_json_append(json, ",\"selected\":") &&
            ch_json_append_string(json, selected) &&
            (!optional_config_bool(group, "hidden", false) ||
             ch_json_append(json, ",\"hidden\":true")) &&
            ch_json_append(json, ",\"test_url\":") &&
            ch_json_append_string(json, test_url[0] == '\0' ?
                "https://www.gstatic.com/generate_204" : test_url) &&
            ch_json_append(json, ",\"interval\":") &&
            ch_json_append_string(json, interval[0] == '\0' ? "30s" : interval) &&
            ch_json_append(json, ",\"timeout\":") &&
            ch_json_append_string(json, timeout[0] == '\0' ? "5s" : timeout) &&
            ch_json_append(json, ",\"selected_chain\":") &&
            ch_json_append_string(json, selected) &&
            ch_json_append(json, ",\"selection_mode\":") &&
            ch_json_append_string(json, policy_selection_mode(type)) &&
            ch_json_append(json, ",\"selection_reason\":") &&
            ch_json_append_string(json, policy_selection_reason(type)) &&
            ch_json_append(json, ",\"results\":[]}");
        free(name); free(type); free(selected); free(test_url);
        free(interval); free(timeout); free(chains);
        if (!okay) return 0;
    }
    return ch_json_append(json, "]");
}

static char *ch_config_policy_snapshot_json(const ch_config *config,
                                            const char *profile_name,
                                            ch_error *error) {
    const ch_config_table *profile = select_profile(config, profile_name);
    if (profile == NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "profile %s not found",
                     profile_name == NULL ? "" : profile_name);
        return NULL;
    }
    char *name = optional_config_string(profile, "name");
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = name != NULL && ch_json_append(&json, "{\"profile\":") &&
        ch_json_append_string(&json, name) &&
        ch_json_append(&json, ",\"groups\":") &&
        ch_config_append_policy_groups(&json, profile, error) &&
        ch_json_append(&json, "}");
    free(name);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL && (error == NULL || error->code == CH_OK)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode policy group snapshot");
    }
    return result;
}

static char *ch_config_policy_selection_payload_json(
    const ch_config *config, const char *profile_name,
    const char *request_json, ch_error *error) {
    char *group = NULL;
    char *chain = NULL;
    if (request_optional_string(request_json, "group", &group, error) !=
            CH_OK ||
        request_optional_string(request_json, "chain", &chain, error) !=
            CH_OK) {
        free(group);
        free(chain);
        return NULL;
    }
    trim_in_place(group);
    trim_in_place(chain);
    const ch_config_table *profile = select_profile(config, profile_name);
    char *name = optional_config_string(profile, "name");
    ch_json_buffer groups;
    ch_json_init(&groups);
    int groups_okay = profile != NULL &&
        ch_config_append_policy_groups(&groups, profile, error);
    char *groups_json = groups_okay ? ch_json_take(&groups) : NULL;
    ch_json_dispose(&groups);
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = name != NULL && group != NULL && chain != NULL &&
        groups_json != NULL && ch_json_append(&json, "{\"profile\":") &&
        ch_json_append_string(&json, name) &&
        ch_json_append(&json, ",\"groups\":") &&
        ch_json_append(&json, groups_json) &&
        ch_json_append(&json, ",\"policy_groups\":{\"profile\":") &&
        ch_json_append_string(&json, name) &&
        ch_json_append(&json, ",\"groups\":") &&
        ch_json_append(&json, groups_json) &&
        ch_json_append(&json, "},\"group\":") &&
        ch_json_append_string(&json, group) &&
        ch_json_append(&json, ",\"chain\":") &&
        ch_json_append_string(&json, chain) &&
        ch_json_append(&json, "}");
    free(name); free(group); free(chain); free(groups_json);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL && (error == NULL || error->code == CH_OK)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode policy group selection");
    }
    return result;
}

char *ch_config_collection_payload_json(const ch_config *config,
                                        const char *fallback_profile,
                                        const char *config_key,
                                        const char *payload_key,
                                        int include_rule_fields,
                                        int include_statuses,
                                        ch_error *error) {
    const ch_config_table *profile = select_profile(config, fallback_profile);
    const ch_config_array *collection = profile == NULL
        ? NULL : ch_config_table_get_array(profile, config_key);
    char *profile_name = NULL;
    char *collection_json = NULL;
    ch_json_buffer json;
    ch_error_clear(error);
    if (config_key == NULL || payload_key == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "collection keys are required");
        return NULL;
    }
    if (profile == NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "profile %s not found",
                     fallback_profile == NULL ? "" : fallback_profile);
        return NULL;
    }
    if (ch_config_table_get_string(profile, "name", &profile_name,
                                   error) != CH_OK) goto failure;
    if (profile_name == NULL) goto out_of_memory;
    if (collection == NULL) collection_json = empty_array();
    else if (ch_config_array_json(collection, &collection_json, error) != CH_OK) goto failure;
    if (collection_json == NULL) goto out_of_memory;
    ch_json_init(&json);
    if (!ch_json_append(&json, "{\"profile\":") ||
        !ch_json_append_string(&json, profile_name) ||
        !ch_json_append(&json, ",") ||
        !ch_json_append_string(&json, payload_key) ||
        !ch_json_append(&json, ":") ||
        !ch_json_append(&json, collection_json)) {
        ch_json_dispose(&json);
        goto out_of_memory;
    }
    if (include_rule_fields != 0 &&
        (!ch_json_append(&json, ",\"generated_rules\":[],\"effective_rules\":") ||
         !ch_json_append(&json, collection_json))) {
        ch_json_dispose(&json);
        goto out_of_memory;
    }
    if (include_statuses != 0 && !ch_json_append(&json, ",\"statuses\":[]")) {
        ch_json_dispose(&json);
        goto out_of_memory;
    }
    if (!ch_json_append(&json, "}")) {
        ch_json_dispose(&json);
        goto out_of_memory;
    }
    free(profile_name);
    free(collection_json);
    return ch_json_take(&json);

out_of_memory:
    ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "encode configuration collection payload");
failure:
    free(profile_name);
    free(collection_json);
    return NULL;
}

char *ch_config_servers_payload_json(const ch_config *config,
                                     const char *fallback_profile,
                                     ch_error *error) {
    ch_error_clear(error);
    const ch_config_table *profile = select_profile(config, fallback_profile);
    if (profile == NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "selected profile not found");
        return NULL;
    }
    char *profile_name = optional_config_string(profile, "name");
    if (profile_name == NULL) goto out_of_memory;
    const ch_config_array *chains = ch_config_table_get_array(profile, "chain");
    size_t chain_count = ch_config_array_count(chains);
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"profile\":") &&
        ch_json_append_string(&json, profile_name) &&
        ch_json_append(&json, ",\"chains\":[");
    for (size_t chain_index = 0U; okay && chain_index < chain_count; ++chain_index) {
        const ch_config_table *chain = ch_config_array_get_table(chains, chain_index);
        char *chain_name = optional_config_string(chain, "name");
        const ch_config_array *servers = ch_config_table_get_array(chain, "server");
        size_t server_count = ch_config_array_count(servers);
        okay = chain_name != NULL &&
            (chain_index == 0U || ch_json_append(&json, ",")) &&
            ch_json_append(&json, "{\"name\":") &&
            ch_json_append_string(&json, chain_name) &&
            ch_json_append_format(&json, ",\"hop_count\":%zu,\"servers\":[", server_count);
        free(chain_name);
        for (size_t server_index = 0U; okay && server_index < server_count; ++server_index) {
            const ch_config_table *server = ch_config_array_get_table(servers, server_index);
            char *name = optional_config_string(server, "name");
            char *address = optional_config_string(server, "address");
            char *protocol = optional_config_string(server, "protocol");
            okay = name != NULL && address != NULL && protocol != NULL &&
                (server_index == 0U || ch_json_append(&json, ",")) &&
                ch_json_append(&json, "{\"name\":") &&
                ch_json_append_string(&json, name) &&
                ch_json_append(&json, ",\"address\":") &&
                ch_json_append_string(&json, address) &&
                ch_json_append(&json, ",\"protocol\":") &&
                ch_json_append_string(&json, protocol) &&
                ch_json_append(&json, ",\"geo\":{}");
            free(name);
            free(address);
            free(protocol);
            if (okay) okay = ch_json_append(&json, "}");
        }
        if (okay) okay = ch_json_append(&json, "]}");
    }
    if (okay) okay = ch_json_append(&json, "]}");
    free(profile_name);
    if (!okay) {
        ch_json_dispose(&json);
        goto out_of_memory_without_profile;
    }
    return ch_json_take(&json);

out_of_memory:
    free(profile_name);
out_of_memory_without_profile:
    ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "encode servers payload");
    return NULL;
}

char *ch_config_profile_payload_json(const ch_config *config,
                                     const char *profile_name,
                                     ch_error *error) {
    char *json = NULL;
    const ch_config_table *profile;
    ch_error_clear(error);
    if (config == NULL) return ch_strdup("{}");
    profile = select_profile(config, profile_name);
    if (profile == NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "profile %s not found",
                     profile_name == NULL ? "" : profile_name);
        return NULL;
    }
    if (ch_config_table_json(profile, &json, error) != CH_OK) return NULL;
    return json;
}

static ch_status request_optional_string(const char *request_json,
                                         const char *key,
                                         char **out_value,
                                         ch_error *error) {
    *out_value = NULL;
    if (request_json == NULL || request_json[0] == '\0') return CH_OK;
    ch_json_value *root = ch_json_parse(request_json, strlen(request_json),
                                        error);
    if (root == NULL) return error == NULL ? CH_ERROR_PARSE : error->code;
    if (ch_json_value_type(root) != CH_JSON_OBJECT) {
        ch_json_value_destroy(root);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "request must be a JSON object");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    const ch_json_value *value = ch_json_object_get(root, key);
    if (value == NULL) {
        ch_json_value_destroy(root);
        return CH_OK;
    }
    const char *text = ch_json_string_value(value);
    if (text == NULL) {
        ch_json_value_destroy(root);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "%s must be a string", key);
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_value = ch_strdup(text);
    ch_json_value_destroy(root);
    if (*out_value == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy request %s", key);
        return CH_ERROR_OUT_OF_MEMORY;
    }
    return CH_OK;
}

char *ch_config_query_payload_json(const ch_config *config,
                                   const char *fallback_profile,
                                   const char *operation,
                                   const char *request_json,
                                   ch_error *error) {
    char *requested = NULL;
    ch_status status = request_optional_string(
        request_json, "profile", &requested, error);
    if (status != CH_OK) return NULL;
    const char *profile = requested == NULL || requested[0] == '\0' ?
        fallback_profile : requested;
    if (config != NULL && profile != NULL && profile[0] != '\0' &&
        !ch_config_has_profile(config, profile)) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "profile %s not found",
                     profile);
        free(requested);
        return NULL;
    }
    char *result = NULL;
    if (strcmp(operation, "servers") == 0) {
        result = ch_config_servers_payload_json(config, profile, error);
    } else if (strcmp(operation, "rules") == 0) {
        result = ch_config_collection_payload_json(
            config, profile, "rule", "rules", 1, 0, error);
    } else if (strcmp(operation, "policy_groups") == 0) {
        result = ch_config_policy_snapshot_json(config, profile, error);
    } else if (strcmp(operation, "rule_sets") == 0) {
        result = ch_config_collection_payload_json(
            config, profile, "rule_set", "rule_sets", 0, 1, error);
    } else if (strcmp(operation, "config") == 0) {
        result = ch_config_profile_payload_json(config, profile, error);
    } else if (strcmp(operation, "dns") == 0) {
        result = ch_config_dns_payload_json(config, profile, error);
    } else if (strcmp(operation, "config_settings") == 0) {
        result = ch_config_settings_payload_json(config, profile, error);
    } else if (strcmp(operation, "conditioner") == 0) {
        result = ch_config_conditioner_payload_json(config, profile, error);
    } else if (strcmp(operation, "developer_settings") == 0) {
        result = ch_config_developer_settings_payload_json(config, error);
    } else if (strcmp(operation, "rule_subscriptions") == 0) {
        result = ch_config_subscriptions_payload_json(config, profile, error);
    } else if (strcmp(operation, "rules_persistence") == 0) {
        result = ch_config_collection_payload_json(
            config, profile, "rule", "rules", 0, 0, error);
    } else if (strcmp(operation, "policy_groups_persistence") == 0) {
        result = ch_config_collection_payload_json(
            config, profile, "policy_group", "policy_groups", 0, 0, error);
    } else if (strcmp(operation, "rule_sets_persistence") == 0) {
        result = ch_config_collection_payload_json(
            config, profile, "rule_set", "rule_sets", 0, 0, error);
    } else if (strcmp(operation, "rule_subscriptions_persistence") == 0) {
        result = ch_config_collection_payload_json(
            config, profile, "rule_subscription", "subscriptions", 0, 0,
            error);
    } else if (strcmp(operation, "policy_group_selection") == 0) {
        result = ch_config_policy_selection_payload_json(
            config, profile, request_json, error);
    } else {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "unknown configuration payload operation");
    }
    free(requested);
    return result;
}

ch_status ch_rule_explain_request_json(const ch_config *config,
                                       const char *fallback_profile,
                                       const char *request_json,
                                       char **out_json,
                                       ch_error *error) {
    ch_error_clear(error);
    if (request_json == NULL || out_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "route explanation request and output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_json = NULL;
    ch_json_value *root = ch_json_parse(request_json, strlen(request_json), error);
    if (root == NULL) return error == NULL ? CH_ERROR_PARSE : error->code;
    if (ch_json_value_type(root) != CH_JSON_OBJECT) {
        ch_json_value_destroy(root);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "route explanation request must be a JSON object");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    const ch_json_value *profile_value = ch_json_object_get(root, "profile");
    const ch_json_value *network_value = ch_json_object_get(root, "network");
    const ch_json_value *target_value = ch_json_object_get(root, "target");
    const ch_json_value *source_value = ch_json_object_get(root, "source");
    const char *profile = profile_value == NULL ? fallback_profile :
        ch_json_string_value(profile_value);
    const char *network = ch_json_string_value(network_value);
    const char *target = ch_json_string_value(target_value);
    const char *source = source_value == NULL ? "" : ch_json_string_value(source_value);
    if (profile == NULL || network == NULL || target == NULL || source == NULL) {
        ch_json_value_destroy(root);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "profile, network, target, and source must be strings");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (profile[0] == '\0') profile = fallback_profile;
    ch_rule_match_context context = {
        .network = network,
        .target = target,
        .source = source
    };
    ch_status status = ch_rule_explain_config_json(config, profile, &context,
                                                   out_json, error);
    ch_json_value_destroy(root);
    return status;
}

int ch_config_has_profile(const ch_config *config, const char *name) {
    if (config == NULL || name == NULL) return 0;
    size_t count = ch_config_profile_count(config);
    for (size_t index = 0U; index < count; ++index) {
        char *candidate = NULL;
        ch_error ignored;
        int matches = ch_config_table_get_string(ch_config_profile_at(config, index),
                                                  "name", &candidate, &ignored) == CH_OK &&
                      strcmp(candidate, name) == 0;
        free(candidate);
        if (matches) return 1;
    }
    return 0;
}

char *ch_json_request_string(const char *request_json, const char *key,
                             ch_error *error) {
    ch_json_value *root;
    const ch_json_value *value;
    const char *text;
    char *copy;
    ch_error_clear(error);
    if (request_json == NULL || key == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "request JSON and key are required");
        return NULL;
    }
    root = ch_json_parse(request_json, strlen(request_json), error);
    if (root == NULL) return NULL;
    value = ch_json_object_get(root, key);
    text = ch_json_string_value(value);
    if (text == NULL || text[0] == '\0') {
        ch_json_value_destroy(root);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "%s is required", key);
        return NULL;
    }
    copy = ch_strdup(text);
    ch_json_value_destroy(root);
    if (copy == NULL) ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy request %s", key);
    return copy;
}
