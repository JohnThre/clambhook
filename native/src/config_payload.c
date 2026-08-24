#include "internal.h"

#include <stdlib.h>
#include <string.h>

#include "clambhook/config.h"
#include "clambhook/json.h"
#include "clambhook/rules.h"

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
    }
    return ch_config_active_profile(config);
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
    if (profile == NULL ||
        ch_config_table_get_string(profile, "name", &profile_name, error) != CH_OK) {
        profile_name = ch_strdup(fallback_profile == NULL ? "default" : fallback_profile);
        ch_error_clear(error);
    }
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
    if (profile == NULL || ch_config_table_json(profile, &json, error) != CH_OK) return NULL;
    return json;
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
