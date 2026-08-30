// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "internal.h"

#include <ctype.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "clambhook/config.h"
#include "clambhook/json.h"

typedef struct import_strings {
    char **items;
    size_t count;
    size_t capacity;
} import_strings;

static char *import_trimmed_copy(const char *raw) {
    if (raw == NULL) return NULL;
    while (*raw != '\0' && isspace((unsigned char)*raw) != 0) ++raw;
    const char *end = raw + strlen(raw);
    while (end > raw && isspace((unsigned char)end[-1]) != 0) --end;
    size_t length = (size_t)(end - raw);
    char *copy = malloc(length + 1U);
    if (copy != NULL) {
        memcpy(copy, raw, length);
        copy[length] = '\0';
    }
    return copy;
}

static void import_strings_clear(import_strings *strings) {
    if (strings == NULL) return;
    for (size_t index = 0U; index < strings->count; ++index) {
        free(strings->items[index]);
    }
    free(strings->items);
    memset(strings, 0, sizeof(*strings));
}

static int import_string_compare(const void *left, const void *right) {
    const char *const *first = left;
    const char *const *second = right;
    return strcmp(*first, *second);
}

static ch_status import_strings_add_unique(import_strings *strings,
                                           const char *raw,
                                           int lowercase,
                                           ch_error *error) {
    char *value = import_trimmed_copy(raw);
    if (value == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy imported profile value");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (lowercase != 0) {
        for (char *cursor = value; *cursor != '\0'; ++cursor) {
            *cursor = (char)tolower((unsigned char)*cursor);
        }
    }
    if (value[0] == '\0') {
        free(value);
        return CH_OK;
    }
    for (size_t index = 0U; index < strings->count; ++index) {
        if (strcmp(strings->items[index], value) == 0) {
            free(value);
            return CH_OK;
        }
    }
    if (strings->count == strings->capacity) {
        size_t capacity = strings->capacity == 0U ? 4U :
            strings->capacity * 2U;
        if (capacity < strings->capacity ||
            capacity > SIZE_MAX / sizeof(*strings->items)) {
            free(value);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "grow imported profile values");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        char **grown = realloc(strings->items,
                               capacity * sizeof(*strings->items));
        if (grown == NULL) {
            free(value);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "grow imported profile values");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        strings->items = grown;
        strings->capacity = capacity;
    }
    strings->items[strings->count++] = value;
    return CH_OK;
}

static char *import_optional_string(const ch_config_table *table,
                                    const char *key) {
    char *value = NULL;
    ch_error ignored;
    if (table == NULL || ch_config_table_get_string(
            table, key, &value, &ignored) != CH_OK) {
        free(value);
        return ch_strdup("");
    }
    return value;
}

ch_status ch_config_import_review_json(const char *import_toml,
                                       char **out_json,
                                       ch_error *error) {
    ch_error_clear(error);
    if (import_toml == NULL || out_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "import TOML and review output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_json = NULL;
    char *trimmed = import_trimmed_copy(import_toml);
    if (trimmed == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy import TOML");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (trimmed[0] == '\0') {
        free(trimmed);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "import text is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ch_config *config = NULL;
    ch_status status = ch_config_parse(trimmed, NULL, &config, error);
    free(trimmed);
    if (status != CH_OK) return status;

    char *active = import_optional_string(ch_config_root(config), "active");
    ch_json_buffer output;
    ch_json_init(&output);
    int okay = active != NULL &&
        ch_json_append(&output, "{\"active_profile\":") &&
        ch_json_append_string(&output, active) &&
        ch_json_append(&output, ",\"profiles\":[");
    size_t profile_count = ch_config_profile_count(config);
    for (size_t index = 0U; okay && index < profile_count; ++index) {
        const ch_config_table *profile = ch_config_profile_at(config, index);
        char *name = import_optional_string(profile, "name");
        const ch_config_array *chains = ch_config_table_get_array(profile,
                                                                  "chain");
        const ch_config_array *rules = ch_config_table_get_array(profile,
                                                                 "rule");
        import_strings protocols = {0};
        size_t server_count = 0U;
        for (size_t chain_index = 0U;
             chain_index < ch_config_array_count(chains); ++chain_index) {
            const ch_config_table *chain = ch_config_array_get_table(
                chains, chain_index);
            const ch_config_array *servers = ch_config_table_get_array(
                chain, "server");
            server_count += ch_config_array_count(servers);
            for (size_t server_index = 0U;
                 status == CH_OK &&
                 server_index < ch_config_array_count(servers);
                 ++server_index) {
                const ch_config_table *server = ch_config_array_get_table(
                    servers, server_index);
                char *protocol = import_optional_string(server, "protocol");
                if (protocol == NULL) {
                    status = CH_ERROR_OUT_OF_MEMORY;
                    ch_error_set(error, status,
                                 "copy imported server protocol");
                } else {
                    status = import_strings_add_unique(
                        &protocols, protocol, 1, error);
                }
                free(protocol);
            }
        }
        if (protocols.count > 1U) {
            qsort(protocols.items, protocols.count, sizeof(*protocols.items),
                  import_string_compare);
        }
        okay = status == CH_OK && name != NULL &&
            (index == 0U || ch_json_append(&output, ",")) &&
            ch_json_append(&output, "{\"name\":") &&
            ch_json_append_string(&output, name) &&
            ch_json_append_format(
                &output,
                ",\"chain_count\":%zu,\"server_count\":%zu,"
                "\"rule_count\":%zu,\"protocols\":[",
                ch_config_array_count(chains), server_count,
                ch_config_array_count(rules));
        for (size_t protocol_index = 0U;
             okay && protocol_index < protocols.count; ++protocol_index) {
            okay = (protocol_index == 0U || ch_json_append(&output, ",")) &&
                ch_json_append_string(&output,
                                      protocols.items[protocol_index]);
        }
        if (okay) okay = ch_json_append(&output, "]}");
        free(name);
        import_strings_clear(&protocols);
    }
    if (okay && status == CH_OK) okay = ch_json_append(&output, "]}");
    free(active);
    ch_config_free(config);
    if (!okay || status != CH_OK) {
        ch_json_dispose(&output);
        if (status == CH_OK) {
            status = CH_ERROR_OUT_OF_MEMORY;
            ch_error_set(error, status, "encode import review");
        }
        return status;
    }
    *out_json = ch_json_take(&output);
    if (*out_json == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "encode import review");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    return CH_OK;
}

static const ch_json_value *import_profile_named(const ch_json_value *profiles,
                                                 const char *name) {
    for (size_t index = 0U; index < ch_json_array_size(profiles); ++index) {
        const ch_json_value *profile = ch_json_array_get(profiles, index);
        const char *candidate = ch_json_string_value(
            ch_json_object_get(profile, "name"));
        if (candidate != NULL && strcmp(candidate, name) == 0) return profile;
    }
    return NULL;
}

static int import_profile_is_placeholder(const ch_json_value *profiles) {
    if (ch_json_array_size(profiles) != 1U) return 0;
    const ch_json_value *profile = ch_json_array_get(profiles, 0U);
    const char *name = ch_json_string_value(ch_json_object_get(profile,
                                                               "name"));
    const ch_json_value *chains = ch_json_object_get(profile, "chain");
    if (name == NULL || strcmp(name, "default") != 0 ||
        ch_json_array_size(chains) != 1U) return 0;
    const ch_json_value *servers = ch_json_object_get(
        ch_json_array_get(chains, 0U), "server");
    if (ch_json_array_size(servers) != 1U) return 0;
    const ch_json_value *server = ch_json_array_get(servers, 0U);
    const char *server_name = ch_json_string_value(
        ch_json_object_get(server, "name"));
    const char *address = ch_json_string_value(
        ch_json_object_get(server, "address"));
    return (server_name != NULL && strcmp(server_name, "replace-me") == 0) ||
        (address != NULL && strstr(address, "proxy.example.com") != NULL);
}

static ch_status import_set_owned(ch_json_value *object, const char *key,
                                  ch_json_value *value, ch_error *error) {
    if (value == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate imported configuration value");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_status status = ch_json_object_set(object, key, value, error);
    if (status != CH_OK) ch_json_value_destroy(value);
    return status;
}

ch_status ch_config_merge_reviewed_import_document(const ch_config *current,
                                                   const char *request_json,
                                                   char **out_toml,
                                                   ch_error *error) {
    ch_error_clear(error);
    if (current == NULL || request_json == NULL || out_toml == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "current config, reviewed request, and output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_toml = NULL;
    ch_json_value *request = ch_json_parse(request_json, strlen(request_json), error);
    if (request == NULL || ch_json_value_type(request) != CH_JSON_OBJECT) {
        ch_json_value_destroy(request);
        if (error->code == CH_OK) ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                                               "reviewed import request must be an object");
        return error->code == CH_OK ? CH_ERROR_INVALID_ARGUMENT : error->code;
    }
    const char *import_text = ch_json_string_value(ch_json_object_get(request, "import_text"));
    const ch_json_value *selection = ch_json_object_get(request, "profiles");
    if (import_text == NULL || import_text[0] == '\0' ||
        ch_json_value_type(selection) != CH_JSON_ARRAY || ch_json_array_size(selection) == 0U) {
        ch_json_value_destroy(request);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     import_text == NULL || import_text[0] == '\0' ?
                     "import text is required" : "select at least one profile");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ch_config *imported = NULL;
    char *current_json = NULL, *import_json = NULL, *document = NULL;
    ch_json_value *root = NULL, *import_root = NULL;
    import_strings selected_names = {0};
    ch_status status = ch_config_parse(import_text, NULL, &imported, error);
    if (status == CH_OK) status = ch_config_table_json(ch_config_root(current), &current_json, error);
    if (status == CH_OK) status = ch_config_table_json(ch_config_root(imported), &import_json, error);
    if (status == CH_OK) {
        root = ch_json_parse(current_json, strlen(current_json), error);
        import_root = ch_json_parse(import_json, strlen(import_json), error);
        if (root == NULL || import_root == NULL) status = error->code;
    }
    ch_json_value *current_profiles = status == CH_OK ? ch_json_object_get_mutable(root, "profile") : NULL;
    const ch_json_value *import_profiles = status == CH_OK ? ch_json_object_get(import_root, "profile") : NULL;
    if (status == CH_OK && (ch_json_value_type(current_profiles) != CH_JSON_ARRAY ||
                            ch_json_value_type(import_profiles) != CH_JSON_ARRAY)) {
        ch_error_set(error, CH_ERROR_PARSE, "imported and current profiles must be arrays");
        status = CH_ERROR_PARSE;
    }
    int placeholder = status == CH_OK ? import_profile_is_placeholder(current_profiles) : 0;
    ch_json_value *next_profiles = placeholder ? ch_json_value_new_array() : current_profiles;
    int next_profiles_owned = placeholder;
    if (status == CH_OK && next_profiles == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate imported profile selection");
        status = CH_ERROR_OUT_OF_MEMORY;
    }
    for (size_t index = 0U; status == CH_OK && index < ch_json_array_size(selection); ++index) {
        const ch_json_value *row = ch_json_array_get(selection, index);
        const char *source_raw = ch_json_string_value(ch_json_object_get(row, "source_name"));
        const char *target_raw = ch_json_string_value(ch_json_object_get(row, "target_name"));
        char *source = import_trimmed_copy(source_raw);
        char *target = import_trimmed_copy(target_raw);
        if (source == NULL || target == NULL || source[0] == '\0' || target[0] == '\0' ||
            source_raw == NULL || target_raw == NULL || strcmp(target, target_raw) != 0) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "profile %zu source_name and trimmed target_name are required", index);
            status = CH_ERROR_INVALID_ARGUMENT;
        }
        for (size_t prior = 0U; status == CH_OK && prior < selected_names.count; ++prior)
            if (strcmp(selected_names.items[prior], target) == 0) {
                ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "profile %s: duplicate target name", target);
                status = CH_ERROR_INVALID_ARGUMENT;
            }
        if (status == CH_OK && !placeholder && import_profile_named(current_profiles, target) != NULL) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "profile %s already exists", target);
            status = CH_ERROR_INVALID_ARGUMENT;
        }
        const ch_json_value *source_profile = status == CH_OK ? import_profile_named(import_profiles, source) : NULL;
        if (status == CH_OK && source_profile == NULL) {
            ch_error_set(error, CH_ERROR_NOT_FOUND, "import profile %s not found", source);
            status = CH_ERROR_NOT_FOUND;
        }
        ch_json_value *copy = status == CH_OK ? ch_json_value_clone(source_profile) : NULL;
        if (status == CH_OK && copy == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy imported profile");
            status = CH_ERROR_OUT_OF_MEMORY;
        }
        if (status == CH_OK) status = import_set_owned(copy, "name", ch_json_value_new_string(target), error);
        int appended = 0;
        if (status == CH_OK) { status = ch_json_array_append(next_profiles, copy, error); appended = status == CH_OK; }
        if (!appended) ch_json_value_destroy(copy);
        if (status == CH_OK) status = import_strings_add_unique(&selected_names, target, 0, error);
        free(source); free(target);
    }
    if (status == CH_OK && placeholder) {
        status = import_set_owned(root, "profile", next_profiles, error);
        next_profiles = NULL; next_profiles_owned = 0;
    }
    const ch_json_value *active_value = ch_json_object_get(request, "activate_profile");
    const char *active_raw = active_value == NULL ? "" : ch_json_string_value(active_value);
    char *active = import_trimmed_copy(active_raw);
    if (status == CH_OK && active == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "activate_profile must be a string");
        status = CH_ERROR_INVALID_ARGUMENT;
    }
    bool active_selected = active != NULL && active[0] == '\0';
    for (size_t index = 0U; active != NULL && active[0] != '\0' && index < selected_names.count; ++index)
        if (strcmp(active, selected_names.items[index]) == 0) active_selected = true;
    if (status == CH_OK && !active_selected) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "activate_profile was not selected");
        status = CH_ERROR_INVALID_ARGUMENT;
    }
    const char *current_active = ch_json_string_value(ch_json_object_get(root, "active"));
    if (status == CH_OK && active[0] != '\0')
        status = import_set_owned(root, "active", ch_json_value_new_string(active), error);
    else if (status == CH_OK && (placeholder || current_active == NULL || current_active[0] == '\0'))
        status = import_set_owned(root, "active", ch_json_value_new_string(selected_names.items[0]), error);
    free(active);
    if (status == CH_OK) status = ch_config_render_document_json(root, &document, error);
    ch_config *validated = NULL;
    if (status == CH_OK) status = ch_config_parse(document, NULL, &validated, error);
    ch_config_free(validated);
    if (status == CH_OK) { *out_toml = document; document = NULL; }
    if (next_profiles_owned != 0 && next_profiles != NULL) ch_json_value_destroy(next_profiles);
    import_strings_clear(&selected_names); ch_json_value_destroy(import_root);
    ch_json_value_destroy(root); free(document); free(import_json); free(current_json);
    ch_config_free(imported); ch_json_value_destroy(request);
    return status;
}

ch_status ch_config_apply_reviewed_import_file(const char *path,
                                               const char *request_json,
                                               ch_error *error) {
    ch_error_clear(error);
    if (path == NULL || path[0] == '\0' || request_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "config path and reviewed import request are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ch_json_value *request = ch_json_parse(request_json,
                                           strlen(request_json), error);
    if (request == NULL) return error->code;
    if (ch_json_value_type(request) != CH_JSON_OBJECT) {
        ch_json_value_destroy(request);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "reviewed import request must be an object");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    const char *import_text = ch_json_string_value(
        ch_json_object_get(request, "import_text"));
    const ch_json_value *selection = ch_json_object_get(request, "profiles");
    if (import_text == NULL || import_text[0] == '\0' ||
        ch_json_value_type(selection) != CH_JSON_ARRAY ||
        ch_json_array_size(selection) == 0U) {
        ch_json_value_destroy(request);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     import_text == NULL || import_text[0] == '\0' ?
                        "import text is required" :
                        "select at least one profile");
        return CH_ERROR_INVALID_ARGUMENT;
    }

    ch_config *current = NULL;
    ch_config *imported = NULL;
    ch_config *validated = NULL;
    char *current_json = NULL;
    char *import_json = NULL;
    char *document = NULL;
    ch_json_value *root = NULL;
    ch_json_value *import_root = NULL;
    import_strings selected_names = {0};
    ch_status status = ch_config_parse(import_text, NULL, &imported, error);
    if (status == CH_OK) status = ch_config_load(path, &current, error);
    if (status == CH_OK) status = ch_config_table_json(
        ch_config_root(current), &current_json, error);
    if (status == CH_OK) status = ch_config_table_json(
        ch_config_root(imported), &import_json, error);
    if (status == CH_OK) {
        root = ch_json_parse(current_json, strlen(current_json), error);
        import_root = ch_json_parse(import_json, strlen(import_json), error);
        if (root == NULL || import_root == NULL) status = error->code;
    }
    ch_json_value *current_profiles = status == CH_OK ?
        ch_json_object_get_mutable(root, "profile") : NULL;
    const ch_json_value *import_profiles = status == CH_OK ?
        ch_json_object_get(import_root, "profile") : NULL;
    if (status == CH_OK &&
        (ch_json_value_type(current_profiles) != CH_JSON_ARRAY ||
         ch_json_value_type(import_profiles) != CH_JSON_ARRAY)) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "imported and current profiles must be arrays");
        status = CH_ERROR_PARSE;
    }
    int placeholder = status == CH_OK ?
        import_profile_is_placeholder(current_profiles) : 0;
    ch_json_value *next_profiles = placeholder ? ch_json_value_new_array() :
        current_profiles;
    int next_profiles_owned = placeholder;
    if (status == CH_OK && next_profiles == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate imported profile selection");
        status = CH_ERROR_OUT_OF_MEMORY;
    }

    for (size_t index = 0U; status == CH_OK &&
         index < ch_json_array_size(selection); ++index) {
        const ch_json_value *row = ch_json_array_get(selection, index);
        const char *source_raw = ch_json_string_value(
            ch_json_object_get(row, "source_name"));
        const char *target_raw = ch_json_string_value(
            ch_json_object_get(row, "target_name"));
        char *source = import_trimmed_copy(source_raw);
        char *target = import_trimmed_copy(target_raw);
        if (source == NULL || target == NULL) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "profile %zu source_name and target_name are required",
                         index);
            status = CH_ERROR_INVALID_ARGUMENT;
        } else if (source[0] == '\0') {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "profile %zu: source_name is required", index);
            status = CH_ERROR_INVALID_ARGUMENT;
        } else if (target[0] == '\0') {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "profile %s: target_name is required", source);
            status = CH_ERROR_INVALID_ARGUMENT;
        } else if (strcmp(target, target_raw) != 0) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "profile %s: target_name must not have surrounding whitespace",
                         source);
            status = CH_ERROR_INVALID_ARGUMENT;
        }
        for (size_t prior = 0U; status == CH_OK &&
             prior < selected_names.count; ++prior) {
            if (strcmp(selected_names.items[prior], target) == 0) {
                ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                             "profile %s: duplicate target name", target);
                status = CH_ERROR_INVALID_ARGUMENT;
            }
        }
        if (status == CH_OK && !placeholder &&
            import_profile_named(current_profiles, target) != NULL) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "profile %s already exists", target);
            status = CH_ERROR_INVALID_ARGUMENT;
        }
        const ch_json_value *source_profile = status == CH_OK ?
            import_profile_named(import_profiles, source) : NULL;
        if (status == CH_OK && source_profile == NULL) {
            ch_error_set(error, CH_ERROR_NOT_FOUND,
                         "import profile %s not found", source);
            status = CH_ERROR_NOT_FOUND;
        }
        ch_json_value *copy = status == CH_OK ?
            ch_json_value_clone(source_profile) : NULL;
        if (status == CH_OK && copy == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy imported profile");
            status = CH_ERROR_OUT_OF_MEMORY;
        }
        if (status == CH_OK) status = import_set_owned(
            copy, "name", ch_json_value_new_string(target), error);
        int appended = 0;
        if (status == CH_OK) {
            status = ch_json_array_append(next_profiles, copy, error);
            appended = status == CH_OK;
        }
        if (!appended) ch_json_value_destroy(copy);
        if (status == CH_OK) status = import_strings_add_unique(
            &selected_names, target, 0, error);
        free(source);
        free(target);
    }
    if (status == CH_OK && placeholder) {
        status = import_set_owned(root, "profile", next_profiles, error);
        next_profiles = NULL;
        next_profiles_owned = 0;
    }

    const ch_json_value *active_value = ch_json_object_get(request,
                                                           "activate_profile");
    const char *active_raw = active_value == NULL ? "" :
        ch_json_string_value(active_value);
    char *active = import_trimmed_copy(active_raw);
    if (status == CH_OK && active == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "activate_profile must be a string");
        status = CH_ERROR_INVALID_ARGUMENT;
    } else if (status == CH_OK && strcmp(active, active_raw) != 0) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "activate_profile must not have surrounding whitespace");
        status = CH_ERROR_INVALID_ARGUMENT;
    }
    int active_selected = active != NULL && active[0] == '\0';
    for (size_t index = 0U; active != NULL && active[0] != '\0' &&
         index < selected_names.count; ++index) {
        if (strcmp(active, selected_names.items[index]) == 0) {
            active_selected = 1;
            break;
        }
    }
    if (status == CH_OK && !active_selected) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "activate_profile %s was not selected", active);
        status = CH_ERROR_INVALID_ARGUMENT;
    }
    const char *current_active = ch_json_string_value(
        ch_json_object_get(root, "active"));
    if (status == CH_OK && active[0] != '\0') {
        status = import_set_owned(root, "active",
                                  ch_json_value_new_string(active), error);
    } else if (status == CH_OK &&
               (placeholder || current_active == NULL ||
                current_active[0] == '\0')) {
        status = import_set_owned(
            root, "active",
            ch_json_value_new_string(selected_names.items[0]), error);
    }
    free(active);
    if (status == CH_OK) {
        status = ch_config_render_document_json(root, &document, error);
    }
    if (status == CH_OK) {
        status = ch_config_parse(document, path, &validated, error);
    }
    if (status == CH_OK) {
        status = ch_config_write_atomic_document(path, document, NULL, error);
    }

    if (next_profiles_owned != 0 && next_profiles != NULL) {
        ch_json_value_destroy(next_profiles);
    }
    import_strings_clear(&selected_names);
    ch_json_value_destroy(import_root);
    ch_json_value_destroy(root);
    free(document);
    free(import_json);
    free(current_json);
    ch_config_free(validated);
    ch_config_free(imported);
    ch_config_free(current);
    ch_json_value_destroy(request);
    return status;
}
