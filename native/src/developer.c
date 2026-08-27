#include "clambhook/developer.h"

#include <ctype.h>
#include <inttypes.h>
#include <pthread.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <time.h>

#include <sodium.h>

#include "clambhook/json.h"
#include "internal.h"

#define CH_DEVELOPER_MAX_HEADER_BYTES (1024U * 1024U)

typedef struct ch_developer_header {
    char *name;
    char *value;
    bool redacted;
    bool truncated;
} ch_developer_header;

typedef struct ch_developer_body {
    uint8_t *preview;
    size_t preview_length;
    uint64_t size;
    size_t limit;
    bool initialized;
} ch_developer_body;

typedef struct ch_developer_entry {
    char *identifier;
    char *connection_identifier;
    char *profile;
    char *client_address;
    char *chain_name;
    int64_t started_ns;
    int64_t finished_ns;
    char *method;
    char *url;
    char *scheme;
    char *host;
    int status;
    ch_developer_header *request_headers;
    size_t request_header_count;
    ch_developer_header *response_headers;
    size_t response_header_count;
    ch_developer_body request_body;
    ch_developer_body response_body;
    char *error_message;
} ch_developer_entry;

struct ch_developer_manager {
    pthread_mutex_t mutex;
    bool enabled;
    bool no_cache_enabled;
    size_t capture_limit;
    size_t body_limit;
    size_t header_value_limit;
    char **redact_headers;
    size_t redact_header_count;
    char **redact_query_params;
    size_t redact_query_param_count;
    ch_developer_entry **entries;
    size_t entry_count;
    uint64_t next_identifier;
};

struct ch_developer_capture {
    ch_developer_manager *manager;
    ch_developer_entry *entry;
    char **redact_headers;
    size_t redact_header_count;
    size_t header_value_limit;
    char *response_header_buffer;
    size_t response_header_length;
    size_t response_header_capacity;
    bool response_headers_complete;
};

static const char *const ch_developer_default_redact_headers[] = {
    "authorization", "proxy-authorization", "cookie", "set-cookie",
    "x-api-key", "api-key", "x-auth-token", "x-csrf-token",
    "x-xsrf-token", "csrf-token", "xsrf-token"
};

static const char *const ch_developer_default_redact_query_params[] = {
    "token", "access_token", "refresh_token", "id_token", "api_key"
};

static int64_t developer_now_ns(void) {
    struct timespec now;
    if (clock_gettime(CLOCK_REALTIME, &now) != 0) return 0;
    return (int64_t)now.tv_sec * INT64_C(1000000000) + (int64_t)now.tv_nsec;
}

static void developer_free_strings(char **values, size_t count) {
    if (values == NULL) return;
    for (size_t index = 0U; index < count; ++index) free(values[index]);
    free(values);
}

static char **developer_copy_static_strings(const char *const *values,
                                            size_t count) {
    char **copy = calloc(count, sizeof(*copy));
    if (copy == NULL && count > 0U) return NULL;
    for (size_t index = 0U; index < count; ++index) {
        copy[index] = ch_strdup(values[index]);
        if (copy[index] == NULL) {
            developer_free_strings(copy, count);
            return NULL;
        }
    }
    return copy;
}

static char *developer_trimmed_lower(const char *value) {
    const char *start = value == NULL ? "" : value;
    while (*start != '\0' && isspace((unsigned char)*start) != 0) ++start;
    const char *end = start + strlen(start);
    while (end > start && isspace((unsigned char)end[-1]) != 0) --end;
    size_t length = (size_t)(end - start);
    char *result = malloc(length + 1U);
    if (result == NULL) return NULL;
    for (size_t index = 0U; index < length; ++index) {
        result[index] = (char)tolower((unsigned char)start[index]);
    }
    result[length] = '\0';
    return result;
}

static char **developer_config_string_array(const ch_config_table *table,
                                            const char *key,
                                            const char *const *defaults,
                                            size_t default_count,
                                            size_t *out_count) {
    *out_count = 0U;
    const ch_config_array *array = ch_config_table_get_array(table, key);
    size_t count = ch_config_array_count(array);
    if (count == 0U) {
        char **copy = developer_copy_static_strings(defaults, default_count);
        if (copy != NULL) *out_count = default_count;
        return copy;
    }
    char **values = calloc(count, sizeof(*values));
    if (values == NULL) return NULL;
    size_t used = 0U;
    for (size_t index = 0U; index < count; ++index) {
        char *raw = NULL;
        ch_error ignored;
        if (ch_config_array_get_string(array, index, &raw, &ignored) != CH_OK) {
            free(raw);
            developer_free_strings(values, count);
            return NULL;
        }
        char *normalized = developer_trimmed_lower(raw);
        free(raw);
        if (normalized == NULL) {
            developer_free_strings(values, count);
            return NULL;
        }
        if (normalized[0] == '\0') {
            free(normalized);
            continue;
        }
        values[used++] = normalized;
    }
    if (used == 0U) {
        developer_free_strings(values, count);
        values = developer_copy_static_strings(defaults, default_count);
        if (values != NULL) *out_count = default_count;
        return values;
    }
    *out_count = used;
    return values;
}

static void developer_header_clear(ch_developer_header *header) {
    if (header == NULL) return;
    free(header->name);
    free(header->value);
}

static void developer_headers_clear(ch_developer_header *headers,
                                    size_t count) {
    for (size_t index = 0U; index < count; ++index) {
        developer_header_clear(&headers[index]);
    }
    free(headers);
}

static void developer_body_clear(ch_developer_body *body) {
    if (body == NULL) return;
    free(body->preview);
    memset(body, 0, sizeof(*body));
}

static void developer_entry_destroy(ch_developer_entry *entry) {
    if (entry == NULL) return;
    free(entry->identifier);
    free(entry->connection_identifier);
    free(entry->profile);
    free(entry->client_address);
    free(entry->chain_name);
    free(entry->method);
    free(entry->url);
    free(entry->scheme);
    free(entry->host);
    developer_headers_clear(entry->request_headers,
                            entry->request_header_count);
    developer_headers_clear(entry->response_headers,
                            entry->response_header_count);
    developer_body_clear(&entry->request_body);
    developer_body_clear(&entry->response_body);
    free(entry->error_message);
    free(entry);
}

static void developer_manager_clear_locked(ch_developer_manager *manager) {
    for (size_t index = 0U; index < manager->entry_count; ++index) {
        developer_entry_destroy(manager->entries[index]);
    }
    free(manager->entries);
    manager->entries = NULL;
    manager->entry_count = 0U;
}

ch_developer_manager *ch_developer_manager_create(ch_error *error) {
    ch_error_clear(error);
    ch_developer_manager *manager = calloc(1U, sizeof(*manager));
    if (manager == NULL || pthread_mutex_init(&manager->mutex, NULL) != 0) {
        free(manager);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "initialize developer capture manager");
        return NULL;
    }
    manager->capture_limit = CH_CONFIG_DEFAULT_DEVELOPER_CAPTURE_LIMIT;
    manager->body_limit = CH_CONFIG_DEFAULT_DEVELOPER_BODY_LIMIT_BYTES;
    manager->header_value_limit =
        CH_CONFIG_DEFAULT_DEVELOPER_HEADER_LIMIT_BYTES;
    manager->redact_headers = developer_copy_static_strings(
        ch_developer_default_redact_headers,
        sizeof(ch_developer_default_redact_headers) /
            sizeof(ch_developer_default_redact_headers[0]));
    manager->redact_header_count =
        sizeof(ch_developer_default_redact_headers) /
        sizeof(ch_developer_default_redact_headers[0]);
    manager->redact_query_params = developer_copy_static_strings(
        ch_developer_default_redact_query_params,
        sizeof(ch_developer_default_redact_query_params) /
            sizeof(ch_developer_default_redact_query_params[0]));
    manager->redact_query_param_count =
        sizeof(ch_developer_default_redact_query_params) /
        sizeof(ch_developer_default_redact_query_params[0]);
    if (manager->redact_headers == NULL ||
        manager->redact_query_params == NULL) {
        ch_developer_manager_destroy(manager);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "initialize developer redaction defaults");
        return NULL;
    }
    return manager;
}

void ch_developer_manager_destroy(ch_developer_manager *manager) {
    if (manager == NULL) return;
    pthread_mutex_lock(&manager->mutex);
    developer_manager_clear_locked(manager);
    developer_free_strings(manager->redact_headers,
                           manager->redact_header_count);
    developer_free_strings(manager->redact_query_params,
                           manager->redact_query_param_count);
    manager->redact_headers = NULL;
    manager->redact_query_params = NULL;
    pthread_mutex_unlock(&manager->mutex);
    pthread_mutex_destroy(&manager->mutex);
    free(manager);
}

static size_t developer_config_size(const ch_config_table *table,
                                    const char *key, size_t fallback) {
    int64_t value = 0;
    ch_error ignored;
    if (table == NULL ||
        ch_config_table_get_int(table, key, &value, &ignored) != CH_OK ||
        value <= 0) {
        return fallback;
    }
    return (uint64_t)value > (uint64_t)SIZE_MAX ? SIZE_MAX : (size_t)value;
}

ch_status ch_developer_manager_configure(ch_developer_manager *manager,
                                         const ch_config *config,
                                         ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer capture manager is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    const ch_config_table *developer = config == NULL ? NULL :
        ch_config_table_get_table(ch_config_root(config), "developer");
    bool enabled = false;
    bool no_cache_enabled = false;
    ch_error ignored;
    if (developer != NULL) {
        (void)ch_config_table_get_bool(developer, "enabled", &enabled,
                                      &ignored);
        (void)ch_config_table_get_bool(developer, "no_cache_enabled",
                                      &no_cache_enabled, &ignored);
    }
    size_t redact_header_count = 0U;
    char **redact_headers = developer_config_string_array(
        developer, "redact_headers", ch_developer_default_redact_headers,
        sizeof(ch_developer_default_redact_headers) /
            sizeof(ch_developer_default_redact_headers[0]),
        &redact_header_count);
    size_t redact_query_count = 0U;
    char **redact_query = developer_config_string_array(
        developer, "redact_query_params",
        ch_developer_default_redact_query_params,
        sizeof(ch_developer_default_redact_query_params) /
            sizeof(ch_developer_default_redact_query_params[0]),
        &redact_query_count);
    if (redact_headers == NULL || redact_query == NULL) {
        developer_free_strings(redact_headers, redact_header_count);
        developer_free_strings(redact_query, redact_query_count);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "configure developer redaction lists");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    size_t capture_limit = developer_config_size(
        developer, "capture_limit",
        CH_CONFIG_DEFAULT_DEVELOPER_CAPTURE_LIMIT);
    size_t body_limit = developer_config_size(
        developer, "body_limit_bytes",
        CH_CONFIG_DEFAULT_DEVELOPER_BODY_LIMIT_BYTES);
    size_t header_limit = developer_config_size(
        developer, "header_value_limit_bytes",
        CH_CONFIG_DEFAULT_DEVELOPER_HEADER_LIMIT_BYTES);

    pthread_mutex_lock(&manager->mutex);
    bool was_enabled = manager->enabled;
    manager->enabled = enabled;
    manager->no_cache_enabled = no_cache_enabled;
    manager->capture_limit = capture_limit;
    manager->body_limit = body_limit;
    manager->header_value_limit = header_limit;
    developer_free_strings(manager->redact_headers,
                           manager->redact_header_count);
    developer_free_strings(manager->redact_query_params,
                           manager->redact_query_param_count);
    manager->redact_headers = redact_headers;
    manager->redact_header_count = redact_header_count;
    manager->redact_query_params = redact_query;
    manager->redact_query_param_count = redact_query_count;
    if (!enabled || !was_enabled) {
        developer_manager_clear_locked(manager);
    } else {
        while (manager->entry_count > manager->capture_limit) {
            developer_entry_destroy(
                manager->entries[manager->entry_count - 1U]);
            --manager->entry_count;
        }
    }
    pthread_mutex_unlock(&manager->mutex);
    return CH_OK;
}

static bool developer_name_in_list(const char *name, char *const *values,
                                   size_t count) {
    for (size_t index = 0U; index < count; ++index) {
        if (strcasecmp(name, values[index]) == 0) return true;
    }
    return false;
}

static int developer_header_compare(const void *left, const void *right) {
    const ch_developer_header *a = left;
    const ch_developer_header *b = right;
    int order = strcasecmp(a->name, b->name);
    return order != 0 ? order : strcmp(a->value, b->value);
}

static bool developer_parse_headers(const char *bytes, size_t length,
                                    bool skip_first_line,
                                    char *const *redact_headers,
                                    size_t redact_header_count,
                                    size_t value_limit,
                                    ch_developer_header **out_headers,
                                    size_t *out_count) {
    *out_headers = NULL;
    *out_count = 0U;
    const char *cursor = bytes;
    const char *end = bytes + length;
    if (skip_first_line) {
        const char *line_end = NULL;
        for (const char *at = cursor; at + 1U < end; ++at) {
            if (at[0] == '\r' && at[1] == '\n') {
                line_end = at;
                break;
            }
        }
        if (line_end == NULL) return false;
        cursor = line_end + 2U;
    }
    while (cursor < end) {
        const char *line_end = NULL;
        for (const char *at = cursor; at + 1U < end; ++at) {
            if (at[0] == '\r' && at[1] == '\n') {
                line_end = at;
                break;
            }
        }
        if (line_end == NULL) line_end = end;
        if (line_end == cursor) break;
        const char *colon = memchr(cursor, ':', (size_t)(line_end - cursor));
        if (colon != NULL && colon > cursor) {
            const char *name_end = colon;
            while (name_end > cursor &&
                   isspace((unsigned char)name_end[-1]) != 0) --name_end;
            const char *value_start = colon + 1U;
            while (value_start < line_end &&
                   isspace((unsigned char)*value_start) != 0) ++value_start;
            const char *value_end = line_end;
            while (value_end > value_start &&
                   isspace((unsigned char)value_end[-1]) != 0) --value_end;
            size_t name_length = (size_t)(name_end - cursor);
            size_t value_length = (size_t)(value_end - value_start);
            char *name = malloc(name_length + 1U);
            if (name == NULL) goto fail;
            memcpy(name, cursor, name_length);
            name[name_length] = '\0';
            bool redacted = developer_name_in_list(
                name, redact_headers, redact_header_count);
            bool truncated = !redacted && value_length > value_limit;
            size_t stored_length = redacted ? strlen("[redacted]") :
                (truncated ? value_limit : value_length);
            char *value = malloc(stored_length + 1U);
            if (value == NULL) {
                free(name);
                goto fail;
            }
            if (redacted) {
                memcpy(value, "[redacted]", stored_length);
            } else if (stored_length > 0U) {
                memcpy(value, value_start, stored_length);
            }
            value[stored_length] = '\0';
            ch_developer_header *grown = realloc(
                *out_headers, (*out_count + 1U) * sizeof(**out_headers));
            if (grown == NULL) {
                free(name);
                free(value);
                goto fail;
            }
            *out_headers = grown;
            (*out_headers)[*out_count] = (ch_developer_header){
                .name = name,
                .value = value,
                .redacted = redacted,
                .truncated = truncated
            };
            ++*out_count;
        }
        cursor = line_end == end ? end : line_end + 2U;
    }
    if (*out_count > 1U) {
        qsort(*out_headers, *out_count, sizeof(**out_headers),
              developer_header_compare);
    }
    return true;

fail:
    developer_headers_clear(*out_headers, *out_count);
    *out_headers = NULL;
    *out_count = 0U;
    return false;
}

static void developer_body_initialize(ch_developer_body *body, size_t limit) {
    body->limit = limit;
    body->initialized = true;
}

static void developer_body_write(ch_developer_body *body,
                                 const uint8_t *bytes, size_t length) {
    if (body == NULL || !body->initialized || bytes == NULL || length == 0U) {
        return;
    }
    if (UINT64_MAX - body->size < (uint64_t)length) {
        body->size = UINT64_MAX;
    } else {
        body->size += (uint64_t)length;
    }
    size_t remaining = body->preview_length >= body->limit ? 0U :
        body->limit - body->preview_length;
    size_t copied = length < remaining ? length : remaining;
    if (copied == 0U) return;
    uint8_t *grown = realloc(body->preview, body->preview_length + copied);
    if (grown == NULL) return;
    body->preview = grown;
    memcpy(body->preview + body->preview_length, bytes, copied);
    body->preview_length += copied;
}

static char **developer_copy_string_list(char *const *values, size_t count) {
    char **copy = calloc(count, sizeof(*copy));
    if (copy == NULL && count > 0U) return NULL;
    for (size_t index = 0U; index < count; ++index) {
        copy[index] = ch_strdup(values[index]);
        if (copy[index] == NULL) {
            developer_free_strings(copy, count);
            return NULL;
        }
    }
    return copy;
}

static char *developer_redact_url(const char *url, char *const *names,
                                  size_t name_count) {
    const char *query = url == NULL ? NULL : strchr(url, '?');
    if (query == NULL || name_count == 0U) return ch_strdup(url == NULL ? "" : url);
    ch_json_buffer result;
    ch_json_init(&result);
    bool okay = ch_json_append_bytes(&result, url, (size_t)(query - url + 1U));
    const char *cursor = query + 1U;
    const char *fragment = strchr(cursor, '#');
    const char *query_end = fragment == NULL ? url + strlen(url) : fragment;
    bool changed = false;
    while (okay && cursor < query_end) {
        const char *separator = memchr(cursor, '&', (size_t)(query_end - cursor));
        const char *part_end = separator == NULL ? query_end : separator;
        const char *equals = memchr(cursor, '=', (size_t)(part_end - cursor));
        size_t name_length = (size_t)((equals == NULL ? part_end : equals) - cursor);
        char *name = malloc(name_length + 1U);
        if (name == NULL) {
            okay = false;
            break;
        }
        for (size_t index = 0U; index < name_length; ++index) {
            name[index] = (char)tolower((unsigned char)cursor[index]);
        }
        name[name_length] = '\0';
        bool redact = developer_name_in_list(name, names, name_count);
        free(name);
        if (redact && equals != NULL) {
            okay = ch_json_append_bytes(&result, cursor,
                                        (size_t)(equals - cursor + 1U)) &&
                ch_json_append(&result, "%5Bredacted%5D");
            changed = true;
        } else {
            okay = ch_json_append_bytes(&result, cursor,
                                        (size_t)(part_end - cursor));
        }
        if (okay && separator != NULL) okay = ch_json_append(&result, "&");
        cursor = separator == NULL ? query_end : separator + 1U;
    }
    if (okay && fragment != NULL) okay = ch_json_append(&result, fragment);
    char *encoded = okay ? ch_json_take(&result) : NULL;
    ch_json_dispose(&result);
    if (!changed && encoded != NULL) {
        free(encoded);
        return ch_strdup(url);
    }
    return encoded;
}

ch_developer_capture *ch_developer_capture_begin(
    ch_developer_manager *manager,
    const ch_developer_capture_metadata *metadata,
    ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL || metadata == NULL || metadata->method == NULL ||
        metadata->url == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer capture metadata is required");
        return NULL;
    }
    pthread_mutex_lock(&manager->mutex);
    if (!manager->enabled) {
        pthread_mutex_unlock(&manager->mutex);
        return NULL;
    }
    ch_developer_capture *capture = calloc(1U, sizeof(*capture));
    ch_developer_entry *entry = calloc(1U, sizeof(*entry));
    char identifier[64];
    uint64_t next_identifier = ++manager->next_identifier;
    (void)snprintf(identifier, sizeof(identifier), "dev-%" PRIu64,
                   next_identifier);
    char connection_identifier[64] = "";
    if (metadata->flow_id != 0U) {
        (void)snprintf(connection_identifier, sizeof(connection_identifier),
                       "conn-%" PRIu64, metadata->flow_id);
    }
    size_t redact_header_count = manager->redact_header_count;
    char **redact_headers = developer_copy_string_list(
        manager->redact_headers, redact_header_count);
    char *redacted_url = developer_redact_url(
        metadata->url, manager->redact_query_params,
        manager->redact_query_param_count);
    if (capture != NULL && entry != NULL) {
        entry->identifier = ch_strdup(identifier);
        entry->connection_identifier = ch_strdup(connection_identifier);
        entry->profile = ch_strdup(metadata->profile == NULL ? "" : metadata->profile);
        entry->client_address = ch_strdup(
            metadata->client_address == NULL ? "" : metadata->client_address);
        entry->chain_name = ch_strdup(
            metadata->chain_name == NULL ? "" : metadata->chain_name);
        entry->method = ch_strdup(metadata->method);
        entry->url = redacted_url;
        redacted_url = NULL;
        entry->scheme = ch_strdup(metadata->scheme == NULL ? "http" : metadata->scheme);
        entry->host = ch_strdup(metadata->host == NULL ? "" : metadata->host);
    }
    bool okay = capture != NULL && entry != NULL && redact_headers != NULL &&
        entry->identifier != NULL && entry->connection_identifier != NULL &&
        entry->profile != NULL && entry->client_address != NULL &&
        entry->chain_name != NULL && entry->method != NULL && entry->url != NULL &&
        entry->scheme != NULL && entry->host != NULL &&
        developer_parse_headers(
            metadata->request_headers == NULL ? "" : metadata->request_headers,
            metadata->request_headers_length, false, redact_headers,
            redact_header_count, manager->header_value_limit,
            &entry->request_headers, &entry->request_header_count);
    if (okay) {
        entry->started_ns = developer_now_ns();
        developer_body_initialize(&entry->request_body, manager->body_limit);
        capture->manager = manager;
        capture->entry = entry;
        capture->redact_headers = redact_headers;
        capture->redact_header_count = redact_header_count;
        capture->header_value_limit = manager->header_value_limit;
    }
    pthread_mutex_unlock(&manager->mutex);
    free(redacted_url);
    if (!okay) {
        developer_free_strings(redact_headers, redact_header_count);
        developer_entry_destroy(entry);
        free(capture);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "start developer capture");
        return NULL;
    }
    return capture;
}

void ch_developer_capture_request_body(ch_developer_capture *capture,
                                       const uint8_t *bytes, size_t length) {
    if (capture == NULL) return;
    developer_body_write(&capture->entry->request_body, bytes, length);
}

static bool developer_capture_response_headers(
    ch_developer_capture *capture, const uint8_t *bytes, size_t length,
    size_t *out_body_offset) {
    *out_body_offset = length;
    if (capture->response_headers_complete) {
        *out_body_offset = 0U;
        return true;
    }
    if (length > CH_DEVELOPER_MAX_HEADER_BYTES -
                     capture->response_header_length) {
        free(capture->entry->error_message);
        capture->entry->error_message = ch_strdup(
            "response headers exceed 1 MiB capture limit");
        return false;
    }
    size_t needed = capture->response_header_length + length;
    if (needed > capture->response_header_capacity) {
        size_t capacity = capture->response_header_capacity == 0U ? 8192U :
            capture->response_header_capacity;
        while (capacity < needed) {
            capacity = capacity > CH_DEVELOPER_MAX_HEADER_BYTES / 2U ?
                CH_DEVELOPER_MAX_HEADER_BYTES : capacity * 2U;
        }
        char *grown = realloc(capture->response_header_buffer, capacity + 1U);
        if (grown == NULL) return false;
        capture->response_header_buffer = grown;
        capture->response_header_capacity = capacity;
    }
    memcpy(capture->response_header_buffer + capture->response_header_length,
           bytes, length);
    capture->response_header_length += length;
    capture->response_header_buffer[capture->response_header_length] = '\0';
    char *header_end = strstr(capture->response_header_buffer, "\r\n\r\n");
    if (header_end == NULL) return true;
    size_t header_length = (size_t)(header_end -
                                     capture->response_header_buffer) + 2U;
    int status = 0;
    if (sscanf(capture->response_header_buffer, "HTTP/%*u.%*u %d", &status) ==
        1 && status >= 100 && status <= 999) {
        capture->entry->status = status;
    }
    if (!developer_parse_headers(
            capture->response_header_buffer, header_length, true,
            capture->redact_headers, capture->redact_header_count,
            capture->header_value_limit, &capture->entry->response_headers,
            &capture->entry->response_header_count)) {
        return false;
    }
    developer_body_initialize(&capture->entry->response_body,
                              capture->entry->request_body.limit);
    capture->response_headers_complete = true;
    size_t consumed = (size_t)(header_end - capture->response_header_buffer) +
        4U;
    if (capture->response_header_length > consumed) {
        developer_body_write(
            &capture->entry->response_body,
            (const uint8_t *)capture->response_header_buffer + consumed,
            capture->response_header_length - consumed);
    }
    *out_body_offset = length;
    return true;
}

void ch_developer_capture_response(ch_developer_capture *capture,
                                   const uint8_t *bytes, size_t length) {
    if (capture == NULL || bytes == NULL || length == 0U) return;
    if (capture->response_headers_complete) {
        developer_body_write(&capture->entry->response_body, bytes, length);
        return;
    }
    size_t body_offset = length;
    (void)developer_capture_response_headers(capture, bytes, length,
                                             &body_offset);
    if (capture->response_headers_complete && body_offset < length) {
        developer_body_write(&capture->entry->response_body,
                             bytes + body_offset, length - body_offset);
    }
}

static void developer_manager_add_locked(ch_developer_manager *manager,
                                         ch_developer_entry *entry) {
    if (!manager->enabled || manager->capture_limit == 0U) {
        developer_entry_destroy(entry);
        return;
    }
    size_t keep = manager->entry_count < manager->capture_limit - 1U ?
        manager->entry_count : manager->capture_limit - 1U;
    ch_developer_entry **next = calloc(keep + 1U, sizeof(*next));
    if (next == NULL) {
        developer_entry_destroy(entry);
        return;
    }
    next[0] = entry;
    if (keep > 0U) {
        memcpy(next + 1U, manager->entries, keep * sizeof(*next));
    }
    for (size_t index = keep; index < manager->entry_count; ++index) {
        developer_entry_destroy(manager->entries[index]);
    }
    free(manager->entries);
    manager->entries = next;
    manager->entry_count = keep + 1U;
}

void ch_developer_capture_finish(ch_developer_capture *capture,
                                 const char *error_message) {
    if (capture == NULL) return;
    capture->entry->finished_ns = developer_now_ns();
    if ((error_message == NULL || error_message[0] == '\0') &&
        !capture->response_headers_complete) {
        error_message = "response closed before headers completed";
    }
    if (error_message != NULL && error_message[0] != '\0' &&
        capture->entry->error_message == NULL) {
        capture->entry->error_message = ch_strdup(error_message);
    }
    pthread_mutex_lock(&capture->manager->mutex);
    developer_manager_add_locked(capture->manager, capture->entry);
    pthread_mutex_unlock(&capture->manager->mutex);
    developer_free_strings(capture->redact_headers,
                           capture->redact_header_count);
    free(capture->response_header_buffer);
    free(capture);
}

static bool developer_append_timestamp(ch_json_buffer *json,
                                       int64_t timestamp_ns) {
    time_t seconds = (time_t)(timestamp_ns / INT64_C(1000000000));
    long nanoseconds = (long)(timestamp_ns % INT64_C(1000000000));
    struct tm value;
    if (timestamp_ns <= 0 || gmtime_r(&seconds, &value) == NULL) {
        return ch_json_append_string(json, "0001-01-01T00:00:00Z");
    }
    char formatted[64];
    int length = snprintf(
        formatted, sizeof(formatted),
        "%04d-%02d-%02dT%02d:%02d:%02d.%09ldZ",
        value.tm_year + 1900, value.tm_mon + 1, value.tm_mday,
        value.tm_hour, value.tm_min, value.tm_sec, nanoseconds);
    return length > 0 && (size_t)length < sizeof(formatted) &&
        ch_json_append_string(json, formatted);
}

static bool developer_utf8_valid(const uint8_t *bytes, size_t length) {
    size_t index = 0U;
    while (index < length) {
        uint8_t first = bytes[index++];
        if (first <= 0x7fU) continue;
        unsigned int remaining;
        uint32_t codepoint;
        if (first >= 0xc2U && first <= 0xdfU) {
            remaining = 1U;
            codepoint = (uint32_t)(first & 0x1fU);
        } else if (first >= 0xe0U && first <= 0xefU) {
            remaining = 2U;
            codepoint = (uint32_t)(first & 0x0fU);
        } else if (first >= 0xf0U && first <= 0xf4U) {
            remaining = 3U;
            codepoint = (uint32_t)(first & 0x07U);
        } else {
            return false;
        }
        if (length - index < remaining) return false;
        for (unsigned int offset = 0U; offset < remaining; ++offset) {
            uint8_t next = bytes[index++];
            if ((next & 0xc0U) != 0x80U) return false;
            codepoint = (codepoint << 6U) | (uint32_t)(next & 0x3fU);
        }
        if ((remaining == 2U && codepoint >= 0xd800U && codepoint <= 0xdfffU) ||
            (remaining == 3U && codepoint > 0x10ffffU) ||
            (remaining == 2U && codepoint < 0x800U) ||
            (remaining == 3U && codepoint < 0x10000U)) return false;
    }
    return true;
}

static const char *developer_body_mime(const ch_developer_header *headers,
                                       size_t count) {
    for (size_t index = 0U; index < count; ++index) {
        if (!headers[index].redacted &&
            strcasecmp(headers[index].name, "Content-Type") == 0 &&
            headers[index].value[0] != '\0') {
            return headers[index].value;
        }
    }
    return "";
}

static bool developer_append_headers(ch_json_buffer *json,
                                     const ch_developer_header *headers,
                                     size_t count) {
    if (!ch_json_append(json, "[")) return false;
    for (size_t index = 0U; index < count; ++index) {
        if ((index > 0U && !ch_json_append(json, ",")) ||
            !ch_json_append(json, "{\"name\":") ||
            !ch_json_append_string(json, headers[index].name) ||
            !ch_json_append(json, ",\"value\":") ||
            !ch_json_append_string(json, headers[index].value) ||
            (headers[index].redacted &&
             !ch_json_append(json, ",\"redacted\":true")) ||
            (headers[index].truncated &&
             !ch_json_append(json, ",\"truncated\":true")) ||
            !ch_json_append(json, "}")) return false;
    }
    return ch_json_append(json, "]");
}

static char *developer_body_base64(const ch_developer_body *body) {
    size_t capacity = sodium_base64_encoded_len(
        body->preview_length, sodium_base64_VARIANT_ORIGINAL);
    char *encoded = malloc(capacity);
    if (encoded != NULL) {
        sodium_bin2base64(encoded, capacity, body->preview,
                          body->preview_length,
                          sodium_base64_VARIANT_ORIGINAL);
    }
    return encoded;
}

static bool developer_append_body(ch_json_buffer *json,
                                  const ch_developer_body *body,
                                  const ch_developer_header *headers,
                                  size_t header_count) {
    uint64_t size = body->initialized ? body->size : 0U;
    size_t preview_length = body->initialized ? body->preview_length : 0U;
    size_t limit = body->initialized ? body->limit : 0U;
    bool truncated = body->initialized && size > (uint64_t)preview_length;
    bool utf8 = developer_utf8_valid(body->preview, preview_length);
    if (!ch_json_append_format(
            json, "{\"size\":%" PRIu64 ",", size)) return false;
    if (preview_length > 0U) {
        if (utf8) {
            char *text = malloc(preview_length + 1U);
            if (text == NULL) return false;
            memcpy(text, body->preview, preview_length);
            text[preview_length] = '\0';
            bool okay = ch_json_append(json, "\"preview\":") &&
                ch_json_append_string(json, text) && ch_json_append(json, ",");
            free(text);
            if (!okay) return false;
        } else {
            char *encoded = developer_body_base64(body);
            if (encoded == NULL) return false;
            bool okay = ch_json_append(json, "\"preview_base64\":") &&
                ch_json_append_string(json, encoded) && ch_json_append(json, ",");
            free(encoded);
            if (!okay) return false;
        }
    }
    if (!ch_json_append_format(
            json,
            "\"preview_bytes\":%zu,\"truncated\":%s,"
            "\"truncated_after\":%zu",
            preview_length, truncated ? "true" : "false", limit)) return false;
    const char *mime = developer_body_mime(headers, header_count);
    if (mime[0] != '\0' &&
        (!ch_json_append(json, ",\"mime_type\":") ||
         !ch_json_append_string(json, mime))) return false;
    if (body->initialized &&
        (!ch_json_append(json, ",\"encoding\":") ||
         !ch_json_append_string(json, utf8 ? "utf8" : "base64"))) return false;
    return ch_json_append(json, "}");
}

static bool developer_append_message(ch_json_buffer *json,
                                     const ch_developer_header *headers,
                                     size_t header_count,
                                     const ch_developer_body *body) {
    if (!ch_json_append(json, "{")) return false;
    if (header_count > 0U &&
        (!ch_json_append(json, "\"headers\":") ||
         !developer_append_headers(json, headers, header_count) ||
         !ch_json_append(json, ","))) return false;
    return ch_json_append(json, "\"body\":") &&
        developer_append_body(json, body, headers, header_count) &&
        ch_json_append(json, "}");
}

static bool developer_append_entry(ch_json_buffer *json,
                                   const ch_developer_entry *entry) {
    if (!ch_json_append(json, "{\"id\":") ||
        !ch_json_append_string(json, entry->identifier)) return false;
#define APPEND_OPTIONAL_STRING(key, value) \
    do { \
        if ((value)[0] != '\0' && \
            (!ch_json_append(json, ",\"" key "\":") || \
             !ch_json_append_string(json, (value)))) return false; \
    } while (0)
    APPEND_OPTIONAL_STRING("conn_id", entry->connection_identifier);
    APPEND_OPTIONAL_STRING("profile", entry->profile);
    APPEND_OPTIONAL_STRING("client_addr", entry->client_address);
    APPEND_OPTIONAL_STRING("chain_name", entry->chain_name);
#undef APPEND_OPTIONAL_STRING
    if (!ch_json_append(json, ",\"started_at\":") ||
        !developer_append_timestamp(json, entry->started_ns) ||
        !ch_json_append(json, ",\"finished_at\":") ||
        !developer_append_timestamp(json, entry->finished_ns) ||
        !ch_json_append(json, ",\"method\":") ||
        !ch_json_append_string(json, entry->method) ||
        !ch_json_append(json, ",\"url\":") ||
        !ch_json_append_string(json, entry->url) ||
        !ch_json_append(json, ",\"scheme\":") ||
        !ch_json_append_string(json, entry->scheme) ||
        !ch_json_append(json, ",\"host\":") ||
        !ch_json_append_string(json, entry->host)) return false;
    if (entry->status != 0 &&
        !ch_json_append_format(json, ",\"status\":%d", entry->status)) {
        return false;
    }
    if (!ch_json_append(json, ",\"request\":") ||
        !developer_append_message(json, entry->request_headers,
                                  entry->request_header_count,
                                  &entry->request_body) ||
        !ch_json_append(json, ",\"response\":") ||
        !developer_append_message(json, entry->response_headers,
                                  entry->response_header_count,
                                  &entry->response_body)) return false;
    if (entry->error_message != NULL && entry->error_message[0] != '\0' &&
        (!ch_json_append(json, ",\"error\":") ||
         !ch_json_append_string(json, entry->error_message))) return false;
    return ch_json_append(json, "}");
}

char *ch_developer_status_json(ch_developer_manager *manager,
                               ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer capture manager is required");
        return NULL;
    }
    pthread_mutex_lock(&manager->mutex);
    ch_json_buffer json;
    ch_json_init(&json);
    bool okay = ch_json_append_format(
        &json,
        "{\"enabled\":%s,\"mitm_enabled\":false,"
        "\"no_cache_enabled\":%s,\"capture_limit\":%zu,"
        "\"body_limit_bytes\":%zu,\"header_value_limit_bytes\":%zu,"
        "\"capture_count\":%zu}",
        manager->enabled ? "true" : "false",
        manager->no_cache_enabled ? "true" : "false",
        manager->capture_limit, manager->body_limit,
        manager->header_value_limit, manager->entry_count);
    pthread_mutex_unlock(&manager->mutex);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode developer status");
    }
    return result;
}

typedef struct developer_filter {
    const ch_json_value *methods;
    int64_t status_min;
    int64_t status_max;
    const char *host;
    const char *scheme;
    const char *content_type;
    const char *query;
    bool error_only;
    size_t limit;
} developer_filter;

static bool developer_contains_case(const char *text, const char *needle) {
    if (needle == NULL || needle[0] == '\0') return true;
    if (text == NULL) return false;
    size_t needle_length = strlen(needle);
    for (const char *cursor = text; *cursor != '\0'; ++cursor) {
        if (strncasecmp(cursor, needle, needle_length) == 0) return true;
    }
    return false;
}

static bool developer_body_contains(const ch_developer_body *body,
                                    const char *query) {
    if (query == NULL || query[0] == '\0') return true;
    if (!developer_utf8_valid(body->preview, body->preview_length)) return false;
    char *text = malloc(body->preview_length + 1U);
    if (text == NULL) return false;
    memcpy(text, body->preview, body->preview_length);
    text[body->preview_length] = '\0';
    bool matches = developer_contains_case(text, query);
    free(text);
    return matches;
}

static bool developer_headers_contain(const ch_developer_header *headers,
                                      size_t count, const char *query) {
    for (size_t index = 0U; index < count; ++index) {
        if (developer_contains_case(headers[index].name, query) ||
            developer_contains_case(headers[index].value, query)) return true;
    }
    return false;
}

static bool developer_entry_matches(const ch_developer_entry *entry,
                                    const developer_filter *filter) {
    if (filter->methods != NULL &&
        ch_json_array_size(filter->methods) > 0U) {
        bool matched = false;
        for (size_t index = 0U;
             index < ch_json_array_size(filter->methods); ++index) {
            const char *method = ch_json_string_value(
                ch_json_array_get(filter->methods, index));
            if (method != NULL && strcasecmp(method, entry->method) == 0) {
                matched = true;
                break;
            }
        }
        if (!matched) return false;
    }
    if (filter->status_min > 0 || filter->status_max > 0) {
        if (entry->status == 0 ||
            (filter->status_min > 0 && entry->status < filter->status_min) ||
            (filter->status_max > 0 && entry->status > filter->status_max)) {
            return false;
        }
    }
    if (!developer_contains_case(entry->host, filter->host) ||
        (filter->scheme != NULL && filter->scheme[0] != '\0' &&
         strcasecmp(entry->scheme, filter->scheme) != 0) ||
        (filter->error_only && (entry->error_message == NULL ||
                               entry->error_message[0] == '\0'))) return false;
    if (filter->content_type != NULL && filter->content_type[0] != '\0' &&
        !developer_contains_case(
            developer_body_mime(entry->request_headers,
                                entry->request_header_count),
            filter->content_type) &&
        !developer_contains_case(
            developer_body_mime(entry->response_headers,
                                entry->response_header_count),
            filter->content_type)) return false;
    const char *query = filter->query;
    if (query == NULL || query[0] == '\0') return true;
    char status[32];
    (void)snprintf(status, sizeof(status), "%d", entry->status);
    return developer_contains_case(entry->method, query) ||
        developer_contains_case(entry->url, query) ||
        developer_contains_case(entry->host, query) ||
        developer_contains_case(entry->scheme, query) ||
        developer_contains_case(entry->chain_name, query) ||
        developer_contains_case(entry->profile, query) ||
        developer_contains_case(status, query) ||
        developer_contains_case(entry->error_message, query) ||
        developer_headers_contain(entry->request_headers,
                                  entry->request_header_count, query) ||
        developer_headers_contain(entry->response_headers,
                                  entry->response_header_count, query) ||
        developer_body_contains(&entry->request_body, query) ||
        developer_body_contains(&entry->response_body, query);
}

static bool developer_filter_parse(const char *request_json,
                                   ch_json_value **out_root,
                                   developer_filter *out_filter,
                                   ch_error *error) {
    const char *document = request_json == NULL || request_json[0] == '\0' ?
        "{}" : request_json;
    ch_json_value *root = ch_json_parse(document, strlen(document), error);
    if (root == NULL) return false;
    if (ch_json_value_type(root) != CH_JSON_OBJECT) {
        ch_json_value_destroy(root);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer entry filter must be an object");
        return false;
    }
    memset(out_filter, 0, sizeof(*out_filter));
    out_filter->limit = 200U;
    const ch_json_value *methods = ch_json_object_get(root, "methods");
    if (methods != NULL && ch_json_value_type(methods) != CH_JSON_ARRAY) {
        goto invalid;
    }
    for (size_t index = 0U; index < ch_json_array_size(methods); ++index) {
        if (ch_json_string_value(ch_json_array_get(methods, index)) == NULL) {
            goto invalid;
        }
    }
    const char *string_keys[] = {"host", "scheme", "content_type", "query"};
    const char **string_outputs[] = {
        &out_filter->host, &out_filter->scheme,
        &out_filter->content_type, &out_filter->query
    };
    for (size_t index = 0U; index < 4U; ++index) {
        const ch_json_value *value = ch_json_object_get(root, string_keys[index]);
        if (value != NULL &&
            (*string_outputs[index] = ch_json_string_value(value)) == NULL) {
            goto invalid;
        }
    }
    const char *number_keys[] = {"status_min", "status_max", "limit"};
    int64_t *number_outputs[] = {
        &out_filter->status_min, &out_filter->status_max, NULL
    };
    for (size_t index = 0U; index < 3U; ++index) {
        const ch_json_value *value = ch_json_object_get(root, number_keys[index]);
        if (value == NULL) continue;
        int64_t number = 0;
        if (!ch_json_int64_value(value, &number) || number < 0) goto invalid;
        if (index == 2U) {
            out_filter->limit = (uint64_t)number > (uint64_t)SIZE_MAX ?
                SIZE_MAX : (size_t)number;
        } else {
            *number_outputs[index] = number;
        }
    }
    const ch_json_value *error_only = ch_json_object_get(root, "error_only");
    if (error_only != NULL &&
        ch_json_value_type(error_only) != CH_JSON_BOOL) goto invalid;
    out_filter->error_only = ch_json_bool_value(error_only, false);
    out_filter->methods = methods;
    *out_root = root;
    return true;

invalid:
    ch_json_value_destroy(root);
    ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                 "invalid developer entry filter");
    return false;
}

char *ch_developer_entries_json(ch_developer_manager *manager,
                                const char *request_json,
                                ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer capture manager is required");
        return NULL;
    }
    ch_json_value *root = NULL;
    developer_filter filter;
    if (!developer_filter_parse(request_json, &root, &filter, error)) {
        return NULL;
    }
    pthread_mutex_lock(&manager->mutex);
    ch_json_buffer json;
    ch_json_init(&json);
    bool okay = ch_json_append(&json, "{\"entries\":[");
    size_t emitted = 0U;
    for (size_t index = 0U; okay && index < manager->entry_count; ++index) {
        if (!developer_entry_matches(manager->entries[index], &filter)) continue;
        if (filter.limit > 0U && emitted >= filter.limit) break;
        okay = (emitted == 0U || ch_json_append(&json, ",")) &&
            developer_append_entry(&json, manager->entries[index]);
        ++emitted;
    }
    okay = okay && ch_json_append(&json, "]}");
    pthread_mutex_unlock(&manager->mutex);
    ch_json_value_destroy(root);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode developer entries");
    }
    return result;
}

static ch_developer_entry *developer_find_entry_locked(
    ch_developer_manager *manager, const char *identifier) {
    for (size_t index = 0U; index < manager->entry_count; ++index) {
        if (strcmp(manager->entries[index]->identifier, identifier) == 0) {
            return manager->entries[index];
        }
    }
    return NULL;
}

char *ch_developer_entry_json(ch_developer_manager *manager,
                              const char *identifier,
                              ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL || identifier == NULL || identifier[0] == '\0') {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer entry identifier is required");
        return NULL;
    }
    pthread_mutex_lock(&manager->mutex);
    ch_developer_entry *entry = developer_find_entry_locked(manager, identifier);
    if (entry == NULL) {
        pthread_mutex_unlock(&manager->mutex);
        ch_error_set(error, CH_ERROR_NOT_FOUND,
                     "developer entry not found");
        return NULL;
    }
    ch_json_buffer json;
    ch_json_init(&json);
    bool okay = developer_append_entry(&json, entry);
    pthread_mutex_unlock(&manager->mutex);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode developer entry");
    }
    return result;
}

static bool developer_shell_quote(ch_json_buffer *output, const char *value) {
    if (!ch_json_append(output, "'")) return false;
    for (const char *cursor = value == NULL ? "" : value;
         *cursor != '\0'; ++cursor) {
        if (*cursor == '\'') {
            if (!ch_json_append(output, "'\\''")) return false;
        } else if (!ch_json_append_bytes(output, cursor, 1U)) {
            return false;
        }
    }
    return ch_json_append(output, "'");
}

char *ch_developer_entry_curl_json(ch_developer_manager *manager,
                                   const char *identifier,
                                   ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL || identifier == NULL || identifier[0] == '\0') {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer entry identifier is required");
        return NULL;
    }
    pthread_mutex_lock(&manager->mutex);
    ch_developer_entry *entry = developer_find_entry_locked(manager, identifier);
    if (entry == NULL) {
        pthread_mutex_unlock(&manager->mutex);
        ch_error_set(error, CH_ERROR_NOT_FOUND,
                     "developer entry not found");
        return NULL;
    }
    ch_json_buffer command;
    ch_json_init(&command);
    bool okay = ch_json_append(&command, "curl");
    if (okay && strcasecmp(entry->method, "GET") != 0) {
        okay = ch_json_append(&command, " -X ") &&
            developer_shell_quote(&command, entry->method);
    }
    ch_json_buffer redacted;
    ch_json_init(&redacted);
    size_t redacted_count = 0U;
    for (size_t index = 0U;
         okay && index < entry->request_header_count; ++index) {
        ch_developer_header *header = &entry->request_headers[index];
        if (header->redacted) {
            okay = (redacted_count == 0U || ch_json_append(&redacted, ", ")) &&
                ch_json_append(&redacted, header->name);
            ++redacted_count;
            continue;
        }
        if (strcasecmp(header->name, "Content-Length") == 0 ||
            strcasecmp(header->name, "Host") == 0) continue;
        ch_json_buffer joined;
        ch_json_init(&joined);
        bool joined_ok = ch_json_append(&joined, header->name) &&
            ch_json_append(&joined, ": ") &&
            ch_json_append(&joined, header->value);
        char *joined_value = joined_ok ? ch_json_take(&joined) : NULL;
        ch_json_dispose(&joined);
        okay = joined_value != NULL && ch_json_append(&command, " -H ") &&
            developer_shell_quote(&command, joined_value);
        free(joined_value);
    }
    if (okay && entry->request_body.preview_length > 0U &&
        developer_utf8_valid(entry->request_body.preview,
                             entry->request_body.preview_length)) {
        char *body = malloc(entry->request_body.preview_length + 1U);
        if (body == NULL) {
            okay = false;
        } else {
            memcpy(body, entry->request_body.preview,
                   entry->request_body.preview_length);
            body[entry->request_body.preview_length] = '\0';
            okay = ch_json_append(&command, " --data-raw ") &&
                developer_shell_quote(&command, body);
            free(body);
        }
    }
    okay = okay && ch_json_append(&command, " ") &&
        developer_shell_quote(&command, entry->url);
    if (okay && entry->request_body.size >
                    (uint64_t)entry->request_body.preview_length) {
        okay = ch_json_append(
            &command,
            "\n# warning: captured request body was truncated; supply the "
            "full body before sending");
    }
    if (okay && redacted_count > 0U) {
        okay = ch_json_append(&command, "\n# redacted headers omitted: ") &&
            ch_json_append_bytes(&command, redacted.data, redacted.length);
    }
    char *command_text = okay ? ch_json_take(&command) : NULL;
    ch_json_dispose(&command);
    ch_json_dispose(&redacted);
    ch_json_buffer json;
    ch_json_init(&json);
    okay = command_text != NULL && ch_json_append(&json, "{\"curl\":") &&
        ch_json_append_string(&json, command_text) && ch_json_append(&json, "}");
    free(command_text);
    pthread_mutex_unlock(&manager->mutex);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode developer cURL command");
    }
    return result;
}

static const char *developer_status_text(int status) {
    switch (status) {
        case 200: return "OK";
        case 201: return "Created";
        case 202: return "Accepted";
        case 204: return "No Content";
        case 301: return "Moved Permanently";
        case 302: return "Found";
        case 304: return "Not Modified";
        case 400: return "Bad Request";
        case 401: return "Unauthorized";
        case 403: return "Forbidden";
        case 404: return "Not Found";
        case 500: return "Internal Server Error";
        case 502: return "Bad Gateway";
        case 503: return "Service Unavailable";
        default: return "";
    }
}

static bool developer_append_har_body(ch_json_buffer *json,
                                      const ch_developer_body *body,
                                      const ch_developer_header *headers,
                                      size_t header_count,
                                      bool response) {
    const char *mime = developer_body_mime(headers, header_count);
    bool utf8 = developer_utf8_valid(body->preview, body->preview_length);
    char *text = NULL;
    if (body->preview_length > 0U) {
        if (utf8) {
            text = malloc(body->preview_length + 1U);
            if (text != NULL) {
                memcpy(text, body->preview, body->preview_length);
                text[body->preview_length] = '\0';
            }
        } else {
            text = developer_body_base64(body);
        }
        if (text == NULL) return false;
    } else {
        text = ch_strdup("");
        if (text == NULL) return false;
    }
    const char *encoding = body->initialized ?
        (utf8 ? "utf8" : "base64") : "";
    bool okay = ch_json_append(json, "{") &&
        (!response || ch_json_append_format(json, "\"size\":%" PRIu64 ",",
                                             body->size)) &&
        ch_json_append(json, "\"mimeType\":") &&
        ch_json_append_string(json, mime) &&
        ch_json_append(json, ",\"text\":") &&
        ch_json_append_string(json, text);
    if (okay && !utf8 && body->preview_length > 0U) {
        okay = ch_json_append(json, ",\"encoding\":\"base64\"");
    }
    okay = okay && ch_json_append(json, ",\"_clambhook\":{");
    if (okay && !response) {
        okay = ch_json_append_format(json, "\"size\":%" PRIu64 ",",
                                     body->size);
    }
    okay = okay && ch_json_append_format(
        json,
        "\"preview_bytes\":%zu,\"truncated\":%s,"
        "\"truncated_after\":%zu,\"encoding\":\"%s\"}}",
        body->preview_length,
        body->size > (uint64_t)body->preview_length ? "true" : "false",
        body->limit, encoding);
    free(text);
    return okay;
}

static bool developer_append_har_headers(ch_json_buffer *json,
                                         const ch_developer_header *headers,
                                         size_t count) {
    if (!ch_json_append(json, "[")) return false;
    for (size_t index = 0U; index < count; ++index) {
        if ((index > 0U && !ch_json_append(json, ",")) ||
            !ch_json_append(json, "{\"name\":") ||
            !ch_json_append_string(json, headers[index].name) ||
            !ch_json_append(json, ",\"value\":") ||
            !ch_json_append_string(json, headers[index].value) ||
            (headers[index].redacted &&
             !ch_json_append(json, ",\"_clambhook_redacted\":true")) ||
            (headers[index].truncated &&
             !ch_json_append(json, ",\"_clambhook_truncated\":true")) ||
            !ch_json_append(json, "}")) return false;
    }
    return ch_json_append(json, "]");
}

static bool developer_append_har_entry(ch_json_buffer *json,
                                       const ch_developer_entry *entry) {
    double duration = entry->finished_ns > entry->started_ns ?
        (double)(entry->finished_ns - entry->started_ns) / 1000000.0 : 0.0;
    if (!ch_json_append(json, "{\"startedDateTime\":") ||
        !developer_append_timestamp(json, entry->started_ns) ||
        !ch_json_append_format(json, ",\"time\":%.3f,\"request\":{", duration) ||
        !ch_json_append(json, "\"method\":") ||
        !ch_json_append_string(json, entry->method) ||
        !ch_json_append(json, ",\"url\":") ||
        !ch_json_append_string(json, entry->url) ||
        !ch_json_append(json, ",\"httpVersion\":\"HTTP/1.1\",\"headers\":") ||
        !developer_append_har_headers(json, entry->request_headers,
                                      entry->request_header_count) ||
        !ch_json_append(json, ",\"queryString\":[],\"cookies\":[],"
                             "\"headersSize\":-1,") ||
        !ch_json_append_format(json, "\"bodySize\":%" PRIu64 ",\"postData\":",
                               entry->request_body.size) ||
        !developer_append_har_body(json, &entry->request_body,
                                   entry->request_headers,
                                   entry->request_header_count, false) ||
        !ch_json_append(json, "},\"response\":{") ||
        !ch_json_append_format(json, "\"status\":%d,", entry->status) ||
        !ch_json_append(json, "\"statusText\":") ||
        !ch_json_append_string(json, developer_status_text(entry->status)) ||
        !ch_json_append(json, ",\"httpVersion\":\"HTTP/1.1\",\"headers\":") ||
        !developer_append_har_headers(json, entry->response_headers,
                                      entry->response_header_count) ||
        !ch_json_append(json, ",\"cookies\":[],\"content\":") ||
        !developer_append_har_body(json, &entry->response_body,
                                   entry->response_headers,
                                   entry->response_header_count, true) ||
        !ch_json_append(json, ",\"redirectURL\":\"\",\"headersSize\":-1,") ||
        !ch_json_append_format(json, "\"bodySize\":%" PRIu64 "},",
                               entry->response_body.size) ||
        !ch_json_append_format(
            json,
            "\"cache\":{},\"timings\":{\"blocked\":-1,\"dns\":-1,"
            "\"connect\":-1,\"ssl\":-1,\"send\":0,\"wait\":%.3f,"
            "\"receive\":0},\"_clambhook\":{\"id\":",
            duration) ||
        !ch_json_append_string(json, entry->identifier) ||
        !ch_json_append(json, ",\"conn_id\":") ||
        !ch_json_append_string(json, entry->connection_identifier) ||
        !ch_json_append(json, ",\"profile\":") ||
        !ch_json_append_string(json, entry->profile) ||
        !ch_json_append(json, ",\"chain_name\":") ||
        !ch_json_append_string(json, entry->chain_name) ||
        !ch_json_append(json, ",\"client_addr\":") ||
        !ch_json_append_string(json, entry->client_address) ||
        !ch_json_append(json, ",\"scheme\":") ||
        !ch_json_append_string(json, entry->scheme) ||
        !ch_json_append(json, ",\"host\":") ||
        !ch_json_append_string(json, entry->host) ||
        !ch_json_append(json, ",\"error\":") ||
        !ch_json_append_string(json, entry->error_message == NULL ? "" :
                               entry->error_message) ||
        !ch_json_append_format(
            json,
            ",\"request_body_truncated\":%s,"
            "\"response_body_truncated\":%s,"
            "\"request_preview_bytes\":%zu,"
            "\"response_preview_bytes\":%zu,"
            "\"request_truncated_after\":%zu,"
            "\"response_truncated_after\":%zu}}",
            entry->request_body.size >
                    (uint64_t)entry->request_body.preview_length ?
                "true" : "false",
            entry->response_body.size >
                    (uint64_t)entry->response_body.preview_length ?
                "true" : "false",
            entry->request_body.preview_length,
            entry->response_body.preview_length,
            entry->request_body.limit, entry->response_body.limit)) return false;
    return true;
}

char *ch_developer_har_json(ch_developer_manager *manager,
                            ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer capture manager is required");
        return NULL;
    }
    pthread_mutex_lock(&manager->mutex);
    ch_json_buffer json;
    ch_json_init(&json);
    bool okay = ch_json_append(
        &json,
        "{\"log\":{\"version\":\"1.2\",\"creator\":{"
        "\"name\":\"clambhook\",\"version\":\"dev\"},\"entries\":[");
    for (size_t offset = 0U; okay && offset < manager->entry_count; ++offset) {
        size_t index = manager->entry_count - offset - 1U;
        okay = (offset == 0U || ch_json_append(&json, ",")) &&
            developer_append_har_entry(&json, manager->entries[index]);
    }
    okay = okay && ch_json_append(&json, "]}}");
    pthread_mutex_unlock(&manager->mutex);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode developer HAR");
    }
    return result;
}

char *ch_developer_clear_json(ch_developer_manager *manager,
                              ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer capture manager is required");
        return NULL;
    }
    pthread_mutex_lock(&manager->mutex);
    developer_manager_clear_locked(manager);
    pthread_mutex_unlock(&manager->mutex);
    char *result = ch_strdup("{\"cleared\":true}");
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode developer clear response");
    }
    return result;
}
