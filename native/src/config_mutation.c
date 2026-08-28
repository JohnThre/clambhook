#include "internal.h"

#include <ctype.h>
#include <math.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>

#include "clambhook/config.h"
#include "clambhook/json.h"

static char *mutation_trimmed_copy(const char *value) {
    if (value == NULL) return NULL;
    while (*value != '\0' && isspace((unsigned char)*value) != 0) ++value;
    const char *end = value + strlen(value);
    while (end > value && isspace((unsigned char)end[-1]) != 0) --end;
    size_t length = (size_t)(end - value);
    char *copy = malloc(length + 1U);
    if (copy != NULL) {
        memcpy(copy, value, length);
        copy[length] = '\0';
    }
    return copy;
}

static ch_status mutation_set_owned(ch_json_value *object, const char *key,
                                    ch_json_value *value, ch_error *error) {
    if (value == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate configuration value");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_status status = ch_json_object_set(object, key, value, error);
    if (status != CH_OK) ch_json_value_destroy(value);
    return status;
}

static ch_status mutation_set_clone(ch_json_value *object, const char *key,
                                    const ch_json_value *value,
                                    ch_error *error) {
    return mutation_set_owned(object, key, ch_json_value_clone(value), error);
}

static ch_json_value *mutation_ensure_object(ch_json_value *parent,
                                             const char *key,
                                             ch_error *error) {
    ch_json_value *value = ch_json_object_get_mutable(parent, key);
    if (value != NULL) {
        if (ch_json_value_type(value) != CH_JSON_OBJECT) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "%s must be an object", key);
            return NULL;
        }
        return value;
    }
    ch_json_value *created = ch_json_value_new_object();
    if (created == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate %s configuration", key);
        return NULL;
    }
    if (ch_json_object_set(parent, key, created, error) != CH_OK) {
        ch_json_value_destroy(created);
        return NULL;
    }
    return created;
}

static ch_status mutation_type_error(const char *field, const char *type,
                                     ch_error *error) {
    ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "%s must be %s", field,
                 type);
    return CH_ERROR_INVALID_ARGUMENT;
}

static ch_status mutation_build_dns(const ch_json_value *request,
                                    ch_json_value **out_dns,
                                    ch_error *error) {
    *out_dns = NULL;
    if (request == NULL || ch_json_value_type(request) != CH_JSON_OBJECT) {
        return mutation_type_error("dns", "an object", error);
    }
    ch_json_value *dns = ch_json_value_new_object();
    if (dns == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate DNS configuration");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    const ch_json_value *enabled = ch_json_object_get(request, "enabled");
    if (enabled != NULL && ch_json_value_type(enabled) != CH_JSON_BOOL &&
        ch_json_value_type(enabled) != CH_JSON_NULL) {
        ch_json_value_destroy(dns);
        return mutation_type_error("enabled", "a boolean", error);
    }
    if (mutation_set_owned(
            dns, "enabled",
            ch_json_value_new_bool(enabled == NULL ? false :
                ch_json_bool_value(enabled, false)), error) != CH_OK) {
        ch_json_value_destroy(dns);
        return error->code;
    }
    const ch_json_value *timeout = ch_json_object_get(request, "timeout");
    if (timeout != NULL && ch_json_value_type(timeout) != CH_JSON_NULL) {
        const char *text = ch_json_string_value(timeout);
        if (text == NULL) {
            ch_json_value_destroy(dns);
            return mutation_type_error("timeout", "a string", error);
        }
        char *trimmed = mutation_trimmed_copy(text);
        if (trimmed == NULL) {
            ch_json_value_destroy(dns);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy DNS timeout");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        if (trimmed[0] != '\0' && mutation_set_owned(
                dns, "timeout", ch_json_value_new_string(trimmed), error) !=
                CH_OK) {
            free(trimmed);
            ch_json_value_destroy(dns);
            return error->code;
        }
        free(trimmed);
    }
    const ch_json_value *upstreams = ch_json_object_get(request, "upstreams");
    if (upstreams != NULL && ch_json_value_type(upstreams) != CH_JSON_NULL &&
        ch_json_value_type(upstreams) != CH_JSON_ARRAY) {
        ch_json_value_destroy(dns);
        return mutation_type_error("upstreams", "an array", error);
    }
    ch_json_value *owned_upstreams = upstreams == NULL ||
        ch_json_value_type(upstreams) == CH_JSON_NULL ?
        ch_json_value_new_array() : ch_json_value_clone(upstreams);
    if (mutation_set_owned(dns, "upstream", owned_upstreams, error) != CH_OK) {
        ch_json_value_destroy(dns);
        return error->code;
    }
    *out_dns = dns;
    return CH_OK;
}

static ch_status mutation_apply_conditioner(ch_json_value *profile,
                                            const ch_json_value *request,
                                            ch_error *error) {
    ch_json_value *conditioner = mutation_ensure_object(
        profile, "conditioner", error);
    if (conditioner == NULL) return error->code;
    const char *numeric_fields[] = {
        "download_kbps", "upload_kbps", "loss_percent"
    };
    const ch_json_value *enabled = ch_json_object_get(request, "enabled");
    if (enabled != NULL && ch_json_value_type(enabled) != CH_JSON_NULL) {
        if (ch_json_value_type(enabled) != CH_JSON_BOOL) {
            return mutation_type_error("enabled", "a boolean", error);
        }
        if (mutation_set_clone(conditioner, "enabled", enabled, error) !=
            CH_OK) return error->code;
    }
    for (size_t index = 0U; index < 3U; ++index) {
        const char *field = numeric_fields[index];
        const ch_json_value *value = ch_json_object_get(request, field);
        if (value == NULL || ch_json_value_type(value) == CH_JSON_NULL) continue;
        if (ch_json_value_type(value) != CH_JSON_NUMBER) {
            return mutation_type_error(field, "a number", error);
        }
        double number = ch_json_number_value(value, 0.0);
        if (number < 0.0 || (index == 2U && number > 100.0) ||
            (index < 2U && floor(number) != number)) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         index == 2U ?
                            "loss_percent must be between 0 and 100" :
                            "%s must be a non-negative integer",
                         field);
            return CH_ERROR_INVALID_ARGUMENT;
        }
        if (mutation_set_clone(conditioner, field, value, error) != CH_OK) {
            return error->code;
        }
    }
    const char *duration_fields[] = {"latency", "jitter"};
    for (size_t index = 0U; index < 2U; ++index) {
        const char *field = duration_fields[index];
        const ch_json_value *value = ch_json_object_get(request, field);
        if (value == NULL || ch_json_value_type(value) == CH_JSON_NULL) continue;
        const char *text = ch_json_string_value(value);
        if (text == NULL) return mutation_type_error(field, "a string", error);
        char *trimmed = mutation_trimmed_copy(text);
        if (trimmed == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy %s", field);
            return CH_ERROR_OUT_OF_MEMORY;
        }
        if (trimmed[0] == '\0') {
            (void)ch_json_object_remove(conditioner, field);
        } else {
            int64_t duration = 0;
            ch_status status = ch_config_parse_duration_ns(trimmed, &duration,
                                                           error);
            if (status != CH_OK || duration < 0) {
                if (status == CH_OK) {
                    ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                                 "%s must not be negative", field);
                }
                free(trimmed);
                return status == CH_OK ? CH_ERROR_INVALID_ARGUMENT : status;
            }
            status = mutation_set_owned(
                conditioner, field, ch_json_value_new_string(trimmed), error);
            if (status != CH_OK) {
                free(trimmed);
                return status;
            }
        }
        free(trimmed);
    }
    return CH_OK;
}

static ch_status mutation_set_trimmed_string(ch_json_value *object,
                                             const ch_json_value *request,
                                             const char *key,
                                             ch_error *error) {
    const ch_json_value *value = ch_json_object_get(request, key);
    if (value == NULL || ch_json_value_type(value) == CH_JSON_NULL) return CH_OK;
    const char *text = ch_json_string_value(value);
    if (text == NULL) return mutation_type_error(key, "a string", error);
    char *trimmed = mutation_trimmed_copy(text);
    if (trimmed == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy %s", key);
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_status status = mutation_set_owned(
        object, key, ch_json_value_new_string(trimmed), error);
    free(trimmed);
    return status;
}

static ch_status mutation_apply_tun(ch_json_value *listen,
                                    const ch_json_value *request,
                                    ch_error *error) {
    if (ch_json_value_type(request) != CH_JSON_OBJECT) {
        return mutation_type_error("tun", "an object", error);
    }
    ch_json_value *tun = ch_json_value_new_object();
    if (tun == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate TUN configuration");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    const ch_json_value *enabled = ch_json_object_get(request, "enabled");
    if (enabled != NULL && ch_json_value_type(enabled) != CH_JSON_NULL &&
        ch_json_value_type(enabled) != CH_JSON_BOOL) {
        ch_json_value_destroy(tun);
        return mutation_type_error("tun.enabled", "a boolean", error);
    }
    if (mutation_set_owned(
            tun, "enabled", ch_json_value_new_bool(
                enabled == NULL ? false : ch_json_bool_value(enabled, false)),
            error) != CH_OK) {
        ch_json_value_destroy(tun);
        return error->code;
    }
    const char *string_fields[] = {"name", "chain"};
    for (size_t index = 0U; index < 2U; ++index) {
        ch_status status = mutation_set_trimmed_string(
            tun, request, string_fields[index], error);
        if (status != CH_OK) {
            ch_json_value_destroy(tun);
            return status;
        }
    }
    const ch_json_value *mtu = ch_json_object_get(request, "mtu");
    if (mtu != NULL && ch_json_value_type(mtu) != CH_JSON_NULL) {
        double number = ch_json_number_value(mtu, -1.0);
        if (ch_json_value_type(mtu) != CH_JSON_NUMBER || number < 0.0 ||
            floor(number) != number) {
            ch_json_value_destroy(tun);
            return mutation_type_error("tun.mtu", "a non-negative integer",
                                       error);
        }
        if (mutation_set_clone(tun, "mtu", mtu, error) != CH_OK) {
            ch_json_value_destroy(tun);
            return error->code;
        }
    }
    const char *array_fields[] = {"addresses", "routes", "exclude_cidrs"};
    for (size_t index = 0U; index < 3U; ++index) {
        const ch_json_value *value = ch_json_object_get(
            request, array_fields[index]);
        if (value == NULL || ch_json_value_type(value) == CH_JSON_NULL) continue;
        if (ch_json_value_type(value) != CH_JSON_ARRAY) {
            ch_json_value_destroy(tun);
            return mutation_type_error(array_fields[index], "an array", error);
        }
        if (mutation_set_clone(tun, array_fields[index], value, error) != CH_OK) {
            ch_json_value_destroy(tun);
            return error->code;
        }
    }
    ch_status status = mutation_set_owned(listen, "tun", tun, error);
    return status;
}

static ch_status mutation_apply_config_settings(ch_json_value *root,
                                                ch_json_value *profile,
                                                const ch_json_value *request,
                                                ch_error *error) {
    const ch_json_value *listen_request = ch_json_object_get(request, "listen");
    if (listen_request != NULL &&
        ch_json_value_type(listen_request) != CH_JSON_NULL) {
        if (ch_json_value_type(listen_request) != CH_JSON_OBJECT) {
            return mutation_type_error("listen", "an object", error);
        }
        ch_json_value *listen = mutation_ensure_object(profile, "listen",
                                                       error);
        if (listen == NULL) return error->code;
        const char *fields[] = {
            "socks5", "socks5_chain", "http", "http_chain"
        };
        for (size_t index = 0U; index < 4U; ++index) {
            ch_status status = mutation_set_trimmed_string(
                listen, listen_request, fields[index], error);
            if (status != CH_OK) return status;
        }
        const ch_json_value *tun = ch_json_object_get(listen_request, "tun");
        if (tun != NULL && ch_json_value_type(tun) != CH_JSON_NULL) {
            ch_status status = mutation_apply_tun(listen, tun, error);
            if (status != CH_OK) return status;
        }
    }
    const ch_json_value *dns_request = ch_json_object_get(request, "dns");
    if (dns_request != NULL && ch_json_value_type(dns_request) != CH_JSON_NULL) {
        ch_json_value *dns = NULL;
        ch_status status = mutation_build_dns(dns_request, &dns, error);
        if (status != CH_OK) return status;
        status = mutation_set_owned(profile, "dns", dns, error);
        if (status != CH_OK) return status;
    }
    const ch_json_value *triggers = ch_json_object_get(
        request, "network_triggers");
    if (triggers != NULL && ch_json_value_type(triggers) != CH_JSON_NULL) {
        if (ch_json_value_type(triggers) != CH_JSON_ARRAY) {
            return mutation_type_error("network_triggers", "an array", error);
        }
        ch_status status = mutation_set_clone(profile, "network_trigger",
                                              triggers, error);
        if (status != CH_OK) return status;
    }
    const ch_json_value *prompt_request = ch_json_object_get(request, "prompt");
    if (prompt_request != NULL &&
        ch_json_value_type(prompt_request) != CH_JSON_NULL) {
        if (ch_json_value_type(prompt_request) != CH_JSON_OBJECT) {
            return mutation_type_error("prompt", "an object", error);
        }
        ch_json_value *prompt = mutation_ensure_object(root, "prompt", error);
        if (prompt == NULL) return error->code;
        const char *bool_fields[] = {"enabled", "default_allow"};
        for (size_t index = 0U; index < 2U; ++index) {
            const char *field = bool_fields[index];
            const ch_json_value *value = ch_json_object_get(prompt_request,
                                                            field);
            if (value == NULL || ch_json_value_type(value) == CH_JSON_NULL) {
                continue;
            }
            if (ch_json_value_type(value) != CH_JSON_BOOL) {
                return mutation_type_error(field, "a boolean", error);
            }
            if (mutation_set_clone(prompt, field, value, error) != CH_OK) {
                return error->code;
            }
        }
        const ch_json_value *timeout = ch_json_object_get(
            prompt_request, "timeout_seconds");
        if (timeout != NULL && ch_json_value_type(timeout) != CH_JSON_NULL) {
            double number = ch_json_number_value(timeout, -1.0);
            if (ch_json_value_type(timeout) != CH_JSON_NUMBER || number < 0.0 ||
                floor(number) != number) {
                return mutation_type_error(
                    "timeout_seconds", "a non-negative integer", error);
            }
            if (mutation_set_clone(prompt, "timeout_seconds", timeout,
                                   error) != CH_OK) return error->code;
        }
        ch_status status = mutation_set_trimmed_string(
            prompt, prompt_request, "silent_mode", error);
        if (status != CH_OK) return status;
    }
    return CH_OK;
}

static ch_status mutation_replace_collection(ch_json_value *profile,
                                             const ch_json_value *request,
                                             const char *request_key,
                                             const char *config_key,
                                             ch_error *error) {
    const ch_json_value *collection = ch_json_object_get(request, request_key);
    if (collection == NULL || ch_json_value_type(collection) == CH_JSON_NULL) {
        (void)ch_json_object_remove(profile, config_key);
        return CH_OK;
    }
    if (ch_json_value_type(collection) != CH_JSON_ARRAY) {
        return mutation_type_error(request_key, "an array", error);
    }
    if (ch_json_array_size(collection) == 0U) {
        (void)ch_json_object_remove(profile, config_key);
        return CH_OK;
    }
    for (size_t index = 0U; index < ch_json_array_size(collection); ++index) {
        if (ch_json_value_type(ch_json_array_get(collection, index)) !=
            CH_JSON_OBJECT) {
            return mutation_type_error(request_key,
                                       "an array of objects", error);
        }
    }
    return mutation_set_clone(profile, config_key, collection, error);
}

static ch_status mutation_create_rule(ch_json_value *profile,
                                      const ch_json_value *request,
                                      ch_error *error) {
    const ch_json_value *position = ch_json_object_get(request, "position");
    bool prepend = false;
    if (position != NULL && ch_json_value_type(position) != CH_JSON_NULL) {
        const char *text = ch_json_string_value(position);
        char *trimmed = text == NULL ? NULL : mutation_trimmed_copy(text);
        if (text == NULL) {
            return mutation_type_error("position", "a string", error);
        }
        if (trimmed == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy rule position");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        bool valid = trimmed[0] == '\0' || strcmp(trimmed, "append") == 0 ||
            strcmp(trimmed, "prepend") == 0;
        prepend = strcmp(trimmed, "prepend") == 0;
        free(trimmed);
        if (!valid) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "position must be append or prepend");
            return CH_ERROR_INVALID_ARGUMENT;
        }
    }
    const ch_json_value *rule = ch_json_object_get(request, "rule");
    if (rule == NULL || ch_json_value_type(rule) != CH_JSON_OBJECT) {
        return mutation_type_error("rule", "an object", error);
    }
    ch_json_value *rules = ch_json_object_get_mutable(profile, "rule");
    if (rules == NULL) {
        rules = ch_json_value_new_array();
        if (rules == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "allocate rules configuration");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        ch_status status = ch_json_object_set(profile, "rule", rules, error);
        if (status != CH_OK) {
            ch_json_value_destroy(rules);
            return status;
        }
    } else if (ch_json_value_type(rules) != CH_JSON_ARRAY) {
        return mutation_type_error("rule", "an array", error);
    }
    ch_json_value *copy = ch_json_value_clone(rule);
    if (copy == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy rule");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (prepend && ch_json_array_size(rules) > 0U) {
        ch_json_value *ordered = ch_json_value_new_array();
        if (ordered == NULL) {
            ch_json_value_destroy(copy);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "allocate prepended rule collection");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        ch_status status = ch_json_array_append(ordered, copy, error);
        if (status != CH_OK) {
            ch_json_value_destroy(copy);
            ch_json_value_destroy(ordered);
            return status;
        }
        for (size_t index = 0U; index < ch_json_array_size(rules); ++index) {
            ch_json_value *existing = ch_json_value_clone(
                ch_json_array_get(rules, index));
            if (existing == NULL || ch_json_array_append(
                    ordered, existing, error) != CH_OK) {
                ch_json_value_destroy(existing);
                ch_json_value_destroy(ordered);
                if (error == NULL || error->code == CH_OK) {
                    ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                                 "copy prepended rule collection");
                }
                return error == NULL ? CH_ERROR_OUT_OF_MEMORY : error->code;
            }
        }
        status = ch_json_object_set(profile, "rule", ordered, error);
        if (status != CH_OK) ch_json_value_destroy(ordered);
        return status;
    }
    ch_status status = ch_json_array_append(rules, copy, error);
    if (status != CH_OK) ch_json_value_destroy(copy);
    return status;
}

static ch_status mutation_select_policy_group(ch_json_value *profile,
                                              const ch_json_value *request,
                                              ch_error *error) {
    const ch_json_value *group_value = ch_json_object_get(request, "group");
    const ch_json_value *chain_value = ch_json_object_get(request, "chain");
    const char *group_text = ch_json_string_value(group_value);
    const char *chain_text = ch_json_string_value(chain_value);
    if (group_text == NULL || chain_text == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "group and chain are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    char *group_name = mutation_trimmed_copy(group_text);
    char *chain_name = mutation_trimmed_copy(chain_text);
    if (group_name == NULL || chain_name == NULL) {
        free(group_name);
        free(chain_name);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy policy group selection");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (group_name[0] == '\0' || chain_name[0] == '\0') {
        free(group_name);
        free(chain_name);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "group and chain are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ch_json_value *groups = ch_json_object_get_mutable(profile, "policy_group");
    ch_json_value *group = NULL;
    for (size_t index = 0U; index < ch_json_array_size(groups); ++index) {
        ch_json_value *candidate = ch_json_array_get_mutable(groups, index);
        const char *name = ch_json_string_value(
            ch_json_object_get(candidate, "name"));
        if (name != NULL && strcmp(name, group_name) == 0) {
            group = candidate;
            break;
        }
    }
    if (group == NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND,
                     "policy group %s not found", group_name);
        free(group_name);
        free(chain_name);
        return CH_ERROR_NOT_FOUND;
    }
    const char *type = ch_json_string_value(ch_json_object_get(group, "type"));
    if (type == NULL || strcasecmp(type, "select") != 0) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "policy group %s is %s, not select", group_name,
                     type == NULL ? "" : type);
        free(group_name);
        free(chain_name);
        return CH_ERROR_INVALID_ARGUMENT;
    }
    const ch_json_value *chains = ch_json_object_get(group, "chains");
    bool member = false;
    for (size_t index = 0U; index < ch_json_array_size(chains); ++index) {
        const char *candidate = ch_json_string_value(
            ch_json_array_get(chains, index));
        if (candidate != NULL && strcmp(candidate, chain_name) == 0) {
            member = true;
            break;
        }
    }
    if (!member) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "policy group %s has no member chain %s", group_name,
                     chain_name);
        free(group_name);
        free(chain_name);
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ch_status status = mutation_set_owned(
        group, "selected", ch_json_value_new_string(chain_name), error);
    free(group_name);
    free(chain_name);
    return status;
}

static ch_status mutation_select_profile(ch_json_value *root,
                                         const ch_json_value *request,
                                         const char *fallback_profile,
                                         ch_json_value **out_profile,
                                         ch_error *error) {
    const char *requested = fallback_profile;
    const ch_json_value *profile_value = ch_json_object_get(request, "profile");
    if (profile_value != NULL && ch_json_value_type(profile_value) !=
        CH_JSON_NULL) {
        requested = ch_json_string_value(profile_value);
        if (requested == NULL) {
            return mutation_type_error("profile", "a string", error);
        }
    }
    char *trimmed = mutation_trimmed_copy(requested == NULL ? "" : requested);
    if (trimmed == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy profile name");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (trimmed[0] == '\0') {
        free(trimmed);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "profile name is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ch_json_value *profiles = ch_json_object_get_mutable(root, "profile");
    for (size_t index = 0U; index < ch_json_array_size(profiles); ++index) {
        ch_json_value *profile = ch_json_array_get_mutable(profiles, index);
        const char *name = ch_json_string_value(ch_json_object_get(profile,
                                                                   "name"));
        if (name != NULL && strcmp(name, trimmed) == 0) {
            free(trimmed);
            *out_profile = profile;
            return CH_OK;
        }
    }
    ch_error_set(error, CH_ERROR_NOT_FOUND, "profile %s not found", trimmed);
    free(trimmed);
    return CH_ERROR_NOT_FOUND;
}

static int mutation_append_toml_string(ch_json_buffer *output,
                                       const char *value) {
    static const char hex[] = "0123456789abcdef";
    if (!ch_json_append(output, "\"")) return 0;
    for (const unsigned char *cursor = (const unsigned char *)value;
         *cursor != 0U; ++cursor) {
        const char *escaped = NULL;
        switch (*cursor) {
            case '\b': escaped = "\\b"; break;
            case '\t': escaped = "\\t"; break;
            case '\n': escaped = "\\n"; break;
            case '\f': escaped = "\\f"; break;
            case '\r': escaped = "\\r"; break;
            case '"': escaped = "\\\""; break;
            case '\\': escaped = "\\\\"; break;
            default: break;
        }
        if (escaped != NULL) {
            if (!ch_json_append(output, escaped)) return 0;
        } else if (*cursor < 0x20U || *cursor == 0x7fU) {
            char encoded[7] = {
                '\\', 'u', '0', '0', hex[*cursor >> 4U],
                hex[*cursor & 0x0fU], '\0'
            };
            if (!ch_json_append(output, encoded)) return 0;
        } else {
            char character[2] = {(char)*cursor, '\0'};
            if (!ch_json_append(output, character)) return 0;
        }
    }
    return ch_json_append(output, "\"");
}

static int mutation_append_toml_key(ch_json_buffer *output,
                                    const char *key) {
    if (key == NULL || key[0] == '\0') return 0;
    bool bare = true;
    for (const unsigned char *cursor = (const unsigned char *)key;
         *cursor != 0U; ++cursor) {
        if (!(isalnum(*cursor) != 0 || *cursor == '_' || *cursor == '-')) {
            bare = false;
            break;
        }
    }
    return bare ? ch_json_append(output, key) :
        mutation_append_toml_string(output, key);
}

static int mutation_append_toml_value(ch_json_buffer *output,
                                      const ch_json_value *value,
                                      ch_error *error) {
    switch (ch_json_value_type(value)) {
        case CH_JSON_BOOL:
            return ch_json_append(output, ch_json_bool_value(value, false) ?
                                  "true" : "false");
        case CH_JSON_NUMBER: {
            double number = ch_json_number_value(value, 0.0);
            double integer = 0.0;
            if (modf(number, &integer) == 0.0 && fabs(number) <= 9.0e15) {
                return ch_json_append_format(output, "%.0f", number);
            }
            return ch_json_append_format(output, "%.17g", number);
        }
        case CH_JSON_STRING:
            return mutation_append_toml_string(
                output, ch_json_string_value(value));
        case CH_JSON_ARRAY:
            if (!ch_json_append(output, "[")) return 0;
            for (size_t index = 0U; index < ch_json_array_size(value); ++index) {
                const ch_json_value *item = ch_json_array_get(value, index);
                if (ch_json_value_type(item) == CH_JSON_OBJECT ||
                    ch_json_value_type(item) == CH_JSON_NULL ||
                    (index > 0U && !ch_json_append(output, ", ")) ||
                    !mutation_append_toml_value(output, item, error)) {
                    ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                                 "TOML value arrays must contain scalars");
                    return 0;
                }
            }
            return ch_json_append(output, "]");
        case CH_JSON_NULL:
        case CH_JSON_OBJECT:
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "configuration contains an unsupported TOML value");
            return 0;
    }
    return 0;
}

static bool mutation_is_object_array(const ch_json_value *value,
                                     ch_error *error) {
    if (ch_json_value_type(value) != CH_JSON_ARRAY ||
        ch_json_array_size(value) == 0U) return false;
    bool objects = ch_json_value_type(ch_json_array_get(value, 0U)) ==
        CH_JSON_OBJECT;
    for (size_t index = 1U; index < ch_json_array_size(value); ++index) {
        bool next_object = ch_json_value_type(ch_json_array_get(value, index)) ==
            CH_JSON_OBJECT;
        if (objects != next_object) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "configuration arrays must not mix tables and values");
            return false;
        }
    }
    return objects;
}

static int mutation_append_path(ch_json_buffer *output,
                                const char *const *path, size_t depth) {
    for (size_t index = 0U; index < depth; ++index) {
        if ((index > 0U && !ch_json_append(output, ".")) ||
            !mutation_append_toml_key(output, path[index])) return 0;
    }
    return 1;
}

static int mutation_render_table(ch_json_buffer *output,
                                 const ch_json_value *object,
                                 const char **path, size_t depth,
                                 ch_error *error) {
    if (object == NULL || ch_json_value_type(object) != CH_JSON_OBJECT ||
        depth > 64U) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "configuration table is invalid or too deeply nested");
        return 0;
    }
    size_t count = ch_json_object_size(object);
    for (size_t index = 0U; index < count; ++index) {
        const ch_json_value *value = ch_json_object_value(object, index);
        ch_json_type type = ch_json_value_type(value);
        if (type == CH_JSON_NULL || type == CH_JSON_OBJECT ||
            mutation_is_object_array(value, error)) continue;
        if (error != NULL && error->code != CH_OK) return 0;
        if (!mutation_append_toml_key(
                output, ch_json_object_key(object, index)) ||
            !ch_json_append(output, " = ") ||
            !mutation_append_toml_value(output, value, error) ||
            !ch_json_append(output, "\n")) return 0;
    }
    for (size_t index = 0U; index < count; ++index) {
        const char *key = ch_json_object_key(object, index);
        const ch_json_value *value = ch_json_object_value(object, index);
        if (ch_json_value_type(value) != CH_JSON_OBJECT) continue;
        path[depth] = key;
        if (!ch_json_append(output, "\n[") ||
            !mutation_append_path(output, path, depth + 1U) ||
            !ch_json_append(output, "]\n") ||
            !mutation_render_table(output, value, path, depth + 1U, error)) {
            return 0;
        }
    }
    for (size_t index = 0U; index < count; ++index) {
        const char *key = ch_json_object_key(object, index);
        const ch_json_value *value = ch_json_object_value(object, index);
        if (!mutation_is_object_array(value, error)) {
            if (error != NULL && error->code != CH_OK) return 0;
            continue;
        }
        path[depth] = key;
        for (size_t item = 0U; item < ch_json_array_size(value); ++item) {
            if (!ch_json_append(output, "\n[[") ||
                !mutation_append_path(output, path, depth + 1U) ||
                !ch_json_append(output, "]]\n") ||
                !mutation_render_table(output, ch_json_array_get(value, item),
                                       path, depth + 1U, error)) {
                return 0;
            }
        }
    }
    return 1;
}

ch_status ch_config_render_document_json(const ch_json_value *root,
                                         char **out_toml,
                                         ch_error *error) {
    *out_toml = NULL;
    ch_json_buffer output;
    ch_json_init(&output);
    const char *path[65] = {0};
    if (!mutation_render_table(&output, root, path, 0U, error)) {
        ch_json_dispose(&output);
        if (error == NULL || error->code == CH_OK) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "render TOML configuration");
        }
        return error == NULL ? CH_ERROR_INTERNAL : error->code;
    }
    *out_toml = ch_json_take(&output);
    if (*out_toml == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "render TOML configuration");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    return CH_OK;
}

ch_status ch_config_mutate_document_json(const ch_config *config,
                                         const char *fallback_profile,
                                         const char *operation,
                                         const char *request_json,
                                         char **out_toml,
                                         ch_error *error) {
    ch_error_clear(error);
    if (config == NULL || operation == NULL || request_json == NULL ||
        out_toml == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "config mutation inputs are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_toml = NULL;
    char *root_json = NULL;
    ch_status status = ch_config_table_json(ch_config_root(config), &root_json,
                                            error);
    if (status != CH_OK) return status;
    ch_json_value *root = ch_json_parse(root_json, strlen(root_json), error);
    free(root_json);
    if (root == NULL) return error->code;
    ch_json_value *request = ch_json_parse(request_json, strlen(request_json),
                                           error);
    if (request == NULL) {
        ch_json_value_destroy(root);
        return error->code;
    }
    if (ch_json_value_type(request) != CH_JSON_OBJECT) {
        ch_json_value_destroy(request);
        ch_json_value_destroy(root);
        return mutation_type_error("request", "an object", error);
    }
    ch_json_value *profile = NULL;
    status = mutation_select_profile(root, request, fallback_profile, &profile,
                                     error);
    if (status == CH_OK) {
        char *active = mutation_trimmed_copy(
            fallback_profile == NULL ? "" : fallback_profile);
        if (active == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy active profile");
            status = CH_ERROR_OUT_OF_MEMORY;
        } else if (active[0] != '\0') {
            status = mutation_set_owned(
                root, "active", ch_json_value_new_string(active), error);
        }
        free(active);
    }
    if (status == CH_OK && strcmp(operation, "update_dns") == 0) {
        ch_json_value *dns = NULL;
        status = mutation_build_dns(request, &dns, error);
        if (status == CH_OK) {
            status = mutation_set_owned(profile, "dns", dns, error);
        }
    } else if (status == CH_OK &&
               strcmp(operation, "update_conditioner") == 0) {
        status = mutation_apply_conditioner(profile, request, error);
    } else if (status == CH_OK &&
               strcmp(operation, "update_config_settings") == 0) {
        status = mutation_apply_config_settings(root, profile, request, error);
    } else if (status == CH_OK && strcmp(operation, "replace_rules") == 0) {
        status = mutation_replace_collection(profile, request, "rules", "rule",
                                             error);
    } else if (status == CH_OK && strcmp(operation, "create_rule") == 0) {
        status = mutation_create_rule(profile, request, error);
    } else if (status == CH_OK &&
               strcmp(operation, "replace_policy_groups") == 0) {
        status = mutation_replace_collection(
            profile, request, "policy_groups", "policy_group", error);
    } else if (status == CH_OK &&
               strcmp(operation, "replace_rule_sets") == 0) {
        status = mutation_replace_collection(
            profile, request, "rule_sets", "rule_set", error);
    } else if (status == CH_OK &&
               strcmp(operation, "replace_rule_subscriptions") == 0) {
        status = mutation_replace_collection(
            profile, request, "subscriptions", "rule_subscription", error);
    } else if (status == CH_OK &&
               strcmp(operation, "select_policy_group") == 0) {
        status = mutation_select_policy_group(profile, request, error);
    } else if (status == CH_OK) {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "unknown configuration mutation operation");
        status = CH_ERROR_UNSUPPORTED;
    }
    if (status == CH_OK) {
        status = ch_config_render_document_json(root, out_toml, error);
    }
    if (status == CH_OK) {
        ch_config *validated = NULL;
        status = ch_config_parse(*out_toml, ch_config_source_path(config),
                                 &validated, error);
        ch_config_free(validated);
        if (status != CH_OK) {
            free(*out_toml);
            *out_toml = NULL;
        }
    }
    ch_json_value_destroy(request);
    ch_json_value_destroy(root);
    return status;
}
