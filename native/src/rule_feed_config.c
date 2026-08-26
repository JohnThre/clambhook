#include "internal.h"

#include <ctype.h>
#include <stdlib.h>
#include <string.h>

#include "clambhook/config.h"
#include "clambhook/json.h"
#include "clambhook/rule_feed.h"

typedef struct feed_refresh_error {
    char *name;
    char *message;
} feed_refresh_error;

static char *feed_config_trimmed_copy(const char *raw) {
    if (raw == NULL) return NULL;
    while (*raw != '\0' && isspace((unsigned char)*raw)) ++raw;
    const char *end = raw + strlen(raw);
    while (end > raw && isspace((unsigned char)end[-1])) --end;
    size_t length = (size_t)(end - raw);
    char *copy = malloc(length + 1U);
    if (copy != NULL) {
        memcpy(copy, raw, length);
        copy[length] = '\0';
    }
    return copy;
}

static char *feed_config_string_or(const ch_config_table *table,
                                   const char *key, const char *fallback) {
    char *value = NULL;
    ch_error ignored;
    if (table == NULL || !ch_config_table_has(table, key) ||
        ch_config_table_get_string(table, key, &value, &ignored) != CH_OK) {
        free(value);
        return ch_strdup(fallback);
    }
    return value;
}

static int feed_config_bool_or(const ch_config_table *table,
                               const char *key, int fallback) {
    bool value = false;
    ch_error ignored;
    return table != NULL && ch_config_table_has(table, key) &&
        ch_config_table_get_bool(table, key, &value, &ignored) == CH_OK ?
        (value ? 1 : 0) : fallback;
}

static void feed_refresh_errors_clear(feed_refresh_error *errors,
                                      size_t count) {
    for (size_t index = 0U; index < count; ++index) {
        free(errors[index].name);
        free(errors[index].message);
    }
    free(errors);
}

static ch_status feed_refresh_error_add(feed_refresh_error **errors,
                                        size_t *count, const char *name,
                                        const char *message,
                                        ch_error *error) {
    if (*count == SIZE_MAX / sizeof(**errors)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "rule feed refresh error list is too large");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    feed_refresh_error *grown = realloc(
        *errors, (*count + 1U) * sizeof(**errors));
    if (grown == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "grow rule feed refresh error list");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    *errors = grown;
    grown[*count].name = ch_strdup(name);
    grown[*count].message = ch_strdup(message);
    if (grown[*count].name == NULL || grown[*count].message == NULL) {
        free(grown[*count].name);
        free(grown[*count].message);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy rule feed refresh error");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ++*count;
    return CH_OK;
}

static ch_status feed_request_names(const ch_json_value *request,
                                    char ***out_names, size_t *out_count,
                                    ch_error *error) {
    *out_names = NULL;
    *out_count = 0U;
    const ch_json_value *names = ch_json_object_get(request, "names");
    if (names == NULL) return CH_OK;
    if (ch_json_value_type(names) != CH_JSON_ARRAY) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "rule feed names must be an array");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    size_t count = ch_json_array_size(names);
    if (count == 0U) return CH_OK;
    char **result = calloc(count, sizeof(*result));
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate selected rule feed names");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    for (size_t index = 0U; index < count; ++index) {
        const char *raw = ch_json_string_value(ch_json_array_get(names,
                                                                 index));
        result[index] = feed_config_trimmed_copy(raw);
        if (result[index] == NULL || result[index][0] == '\0') {
            for (size_t prior = 0U; prior <= index; ++prior) {
                free(result[prior]);
            }
            free(result);
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "rule feed name must not be empty");
            return CH_ERROR_INVALID_ARGUMENT;
        }
    }
    *out_names = result;
    *out_count = count;
    return CH_OK;
}

static void feed_names_clear(char **names, size_t count) {
    for (size_t index = 0U; index < count; ++index) free(names[index]);
    free(names);
}

static int feed_name_selected(char *const *names, size_t count,
                              const char *candidate, unsigned char *found) {
    if (count == 0U) return 1;
    int selected = 0;
    for (size_t index = 0U; index < count; ++index) {
        if (strcmp(names[index], candidate) == 0) {
            found[index] = 1U;
            selected = 1;
        }
    }
    return selected;
}

static ch_status feed_config_networks(const ch_config_table *table,
                                      char ***out_networks,
                                      size_t *out_count, ch_error *error) {
    *out_networks = NULL;
    *out_count = 0U;
    const ch_config_array *array = ch_config_table_get_array(table,
                                                             "networks");
    size_t count = ch_config_array_count(array);
    if (count == 0U) return CH_OK;
    char **networks = calloc(count, sizeof(*networks));
    if (networks == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate rule feed networks");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    for (size_t index = 0U; index < count; ++index) {
        if (ch_config_array_get_string(array, index, &networks[index],
                                       error) != CH_OK) {
            feed_names_clear(networks, index);
            return error == NULL ? CH_ERROR_PARSE : error->code;
        }
    }
    *out_networks = networks;
    *out_count = count;
    return CH_OK;
}

static char *feed_refresh_response_with_errors(
    char *base, ch_rule_feed_kind kind, const feed_refresh_error *errors,
    size_t error_count, ch_error *error) {
    if (error_count == 0U) return base;
    ch_json_value *root = ch_json_parse(base, strlen(base), error);
    free(base);
    if (root == NULL) return NULL;
    const char *array_key = kind == CH_RULE_FEED_RULE_SET ? "statuses" :
        "subscriptions";
    ch_json_value *rows = ch_json_object_get_mutable(root, array_key);
    for (size_t row = 0U; row < ch_json_array_size(rows); ++row) {
        ch_json_value *item = ch_json_array_get_mutable(rows, row);
        const char *name = ch_json_string_value(ch_json_object_get(item,
                                                                   "name"));
        for (size_t index = 0U; name != NULL && index < error_count; ++index) {
            if (strcmp(name, errors[index].name) != 0) continue;
            ch_json_value *message = ch_json_value_new_string(
                errors[index].message);
            if (message == NULL || ch_json_object_set(
                    item, "last_error", message, error) != CH_OK) {
                ch_json_value_destroy(message);
                ch_json_value_destroy(root);
                return NULL;
            }
        }
    }
    ch_json_buffer output;
    ch_json_init(&output);
    int okay = ch_json_append_value(&output, root);
    ch_json_value_destroy(root);
    char *result = okay ? ch_json_take(&output) : NULL;
    ch_json_dispose(&output);
    if (result == NULL && (error == NULL || error->code == CH_OK)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode rule feed refresh response");
    }
    return result;
}

char *ch_config_refresh_rule_feeds_json(
    const ch_config *config, const char *fallback_profile,
    ch_rule_feed_kind kind, const char *request_json, ch_error *error) {
    ch_error_clear(error);
    if (config == NULL || ch_config_source_path(config) == NULL ||
        ch_config_source_path(config)[0] == '\0') {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "rule feed refresh requires daemon config path");
        return NULL;
    }
    const char *json = request_json == NULL || request_json[0] == '\0' ?
        "{}" : request_json;
    ch_json_value *request = ch_json_parse(json, strlen(json), error);
    if (request == NULL) return NULL;
    if (ch_json_value_type(request) != CH_JSON_OBJECT) {
        ch_json_value_destroy(request);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "rule feed refresh request must be an object");
        return NULL;
    }
    const ch_json_value *profile_value = ch_json_object_get(request,
                                                             "profile");
    char *requested_profile = NULL;
    ch_status status = CH_OK;
    if (profile_value == NULL) {
        requested_profile = ch_strdup("");
    } else {
        const char *profile_text = ch_json_string_value(profile_value);
        if (profile_text == NULL) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "rule feed profile must be a string");
            status = CH_ERROR_INVALID_ARGUMENT;
        } else {
            requested_profile = feed_config_trimmed_copy(profile_text);
        }
    }
    if (status == CH_OK && requested_profile == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy rule feed profile");
        status = CH_ERROR_OUT_OF_MEMORY;
    }
    char **names = NULL;
    size_t name_count = 0U;
    if (status == CH_OK) {
        status = feed_request_names(request, &names, &name_count, error);
    }
    const char *selected_profile = requested_profile != NULL &&
        requested_profile[0] != '\0' ? requested_profile : fallback_profile;
    const ch_config_table *profile = status == CH_OK ?
        ch_config_profile_named(config, selected_profile) : NULL;
    if (status == CH_OK && profile == NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "profile %s not found",
                     selected_profile == NULL ? "" : selected_profile);
        status = CH_ERROR_NOT_FOUND;
    }
    char *profile_name = profile == NULL ? NULL :
        feed_config_string_or(profile, "name", "");
    const char *config_key = kind == CH_RULE_FEED_RULE_SET ? "rule_set" :
        "rule_subscription";
    const char *operation = kind == CH_RULE_FEED_RULE_SET ? "rule_sets" :
        "rule_subscriptions";
    const ch_config_array *feeds = profile == NULL ? NULL :
        ch_config_table_get_array(profile, config_key);
    unsigned char *found = name_count == 0U ? NULL : calloc(name_count, 1U);
    if (status == CH_OK && name_count > 0U && found == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate selected rule feed matches");
        status = CH_ERROR_OUT_OF_MEMORY;
    }
    feed_refresh_error *refresh_errors = NULL;
    size_t refresh_error_count = 0U;
    size_t feed_count = ch_config_array_count(feeds);
    for (size_t index = 0U; status == CH_OK && index < feed_count; ++index) {
        const ch_config_table *table = ch_config_array_get_table(feeds, index);
        char *name = feed_config_string_or(table, "name", "");
        char *url = feed_config_string_or(table, "url", "");
        char *format = feed_config_string_or(table, "format", "auto");
        char *action = feed_config_string_or(table, "action", "block");
        if (name == NULL || url == NULL || format == NULL || action == NULL) {
            free(name); free(url); free(format); free(action);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy rule feed configuration");
            status = CH_ERROR_OUT_OF_MEMORY;
            break;
        }
        if (!feed_name_selected(names, name_count, name, found) ||
            feed_config_bool_or(table, "disabled", 0) ||
            (kind == CH_RULE_FEED_RULE_SET && url[0] == '\0')) {
            free(name); free(url); free(format); free(action);
            continue;
        }
        char **networks = NULL;
        size_t network_count = 0U;
        status = feed_config_networks(table, &networks, &network_count,
                                      error);
        if (status == CH_OK) {
            ch_rule_feed_refresh_options options = {
                .config_path = ch_config_source_path(config),
                .kind = kind,
                .profile = profile_name,
                .name = name,
                .url = url,
                .format = format[0] == '\0' ? "auto" : format,
                .action = action[0] == '\0' ? "block" : action,
                .networks = (const char *const *)networks,
                .network_count = network_count
            };
            ch_error refresh_error;
            ch_status refresh_status = ch_rule_feed_refresh(&options,
                                                             &refresh_error);
            if (refresh_status != CH_OK) {
                status = feed_refresh_error_add(
                    &refresh_errors, &refresh_error_count, name,
                    refresh_error.message, error);
            }
        }
        feed_names_clear(networks, network_count);
        free(name); free(url); free(format); free(action);
    }
    for (size_t index = 0U; status == CH_OK && index < name_count; ++index) {
        if (found[index] == 0U) {
            ch_error_set(error, CH_ERROR_NOT_FOUND,
                         "%s %s not found",
                         kind == CH_RULE_FEED_RULE_SET ? "rule set" :
                            "rule subscription", names[index]);
            status = CH_ERROR_NOT_FOUND;
        }
    }
    char *base = status == CH_OK ? ch_config_query_payload_json(
        config, selected_profile, operation, request_json, error) : NULL;
    char *result = base == NULL ? NULL : feed_refresh_response_with_errors(
        base, kind, refresh_errors, refresh_error_count, error);
    feed_refresh_errors_clear(refresh_errors, refresh_error_count);
    free(found);
    free(profile_name);
    feed_names_clear(names, name_count);
    free(requested_profile);
    ch_json_value_destroy(request);
    return result;
}
