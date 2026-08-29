// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "developer_internal.h"

#include <arpa/inet.h>
#include <ctype.h>
#include <errno.h>
#include <fcntl.h>
#include <fnmatch.h>
#include <inttypes.h>
#include <pthread.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>

#include <curl/curl.h>
#include <openssl/bio.h>
#include <openssl/err.h>
#include <openssl/evp.h>
#include <openssl/pem.h>
#include <openssl/rand.h>
#include <openssl/sha.h>
#include <openssl/x509.h>
#include <openssl/x509v3.h>
#include "clambhook/json.h"
#include "http_safety.h"
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

typedef struct ch_developer_pending {
    char *identifier;
    char *rule_identifier;
    char *rule_name;
    char *stage;
    int64_t created_ns;
    ch_developer_http_message request;
    ch_developer_http_message response;
    bool has_response;
    bool resolved;
    char *action;
    ch_developer_http_message edited_request;
    bool has_edited_request;
    ch_developer_http_message edited_response;
    bool has_edited_response;
    pthread_cond_t condition;
    struct ch_developer_pending *next;
} ch_developer_pending;

struct ch_developer_manager {
    pthread_mutex_t mutex;
    pthread_cond_t pending_drained;
    bool destroying;
    bool enabled;
    bool mitm_enabled;
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
    char **ssl_decrypt_hosts;
    size_t ssl_decrypt_host_count;
    char *ca_cert_path;
    char *ca_key_path;
    char *ca_pem;
    size_t ca_pem_length;
    X509 *ca_certificate;
    EVP_PKEY *ca_key;
    ch_json_value *map_rules;
    ch_json_value *breakpoint_rules;
    ch_json_value *rewrite_rules;
    ch_developer_pending *pending;
    uint64_t next_pending_identifier;
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

static void developer_openssl_error(ch_error *error, const char *action) {
    unsigned long code = ERR_peek_last_error();
    char detail[256] = {0};
    if (code != 0UL) ERR_error_string_n(code, detail, sizeof(detail));
    ch_error_set(error, CH_ERROR_IO, "%s%s%s", action,
                 detail[0] == '\0' ? "" : ": ", detail);
}

void ch_developer_http_message_clear(ch_developer_http_message *message) {
    if (message == NULL) return;
    free(message->method);
    free(message->url);
    free(message->host);
    free(message->path);
    for (size_t index = 0U; index < message->header_count; ++index) {
        free(message->headers[index].name);
        free(message->headers[index].value);
    }
    free(message->headers);
    free(message->body);
    memset(message, 0, sizeof(*message));
}

void ch_developer_http_result_clear(ch_developer_http_result *result) {
    if (result == NULL) return;
    free(result->rule_id);
    free(result->rule_name);
    free(result->kind);
    free(result->remote_url);
    ch_developer_http_message_clear(&result->message);
    memset(result, 0, sizeof(*result));
}

static bool developer_http_message_copy(
    ch_developer_http_message *destination,
    const ch_developer_http_message *source) {
    memset(destination, 0, sizeof(*destination));
    destination->status = source == NULL ? 0 : source->status;
    destination->body_set = source != NULL && source->body_set;
    if (source == NULL) return true;
#define COPY_MESSAGE_STRING(member) \
    do { \
        destination->member = ch_strdup(source->member == NULL ? "" : \
                                        source->member); \
        if (destination->member == NULL) goto failed; \
    } while (0)
    COPY_MESSAGE_STRING(method);
    COPY_MESSAGE_STRING(url);
    COPY_MESSAGE_STRING(host);
    COPY_MESSAGE_STRING(path);
#undef COPY_MESSAGE_STRING
    if (source->header_count > 0U) {
        destination->headers = calloc(source->header_count,
                                      sizeof(*destination->headers));
        if (destination->headers == NULL) goto failed;
        destination->header_count = source->header_count;
        for (size_t index = 0U; index < source->header_count; ++index) {
            destination->headers[index].name = ch_strdup(
                source->headers[index].name == NULL ? "" :
                                                     source->headers[index].name);
            destination->headers[index].value = ch_strdup(
                source->headers[index].value == NULL ? "" :
                                                      source->headers[index].value);
            if (destination->headers[index].name == NULL ||
                destination->headers[index].value == NULL) goto failed;
        }
    }
    if (source->body_length > 0U) {
        destination->body = malloc(source->body_length);
        if (destination->body == NULL) goto failed;
        memcpy(destination->body, source->body, source->body_length);
        destination->body_length = source->body_length;
    }
    return true;
failed:
    ch_developer_http_message_clear(destination);
    return false;
}

static void developer_pending_destroy(ch_developer_pending *pending) {
    if (pending == NULL) return;
    free(pending->identifier);
    free(pending->rule_identifier);
    free(pending->rule_name);
    free(pending->stage);
    free(pending->action);
    ch_developer_http_message_clear(&pending->request);
    ch_developer_http_message_clear(&pending->response);
    ch_developer_http_message_clear(&pending->edited_request);
    ch_developer_http_message_clear(&pending->edited_response);
    pthread_cond_destroy(&pending->condition);
    free(pending);
}

static void developer_pending_continue_locked(ch_developer_manager *manager) {
    for (ch_developer_pending *pending = manager->pending;
         pending != NULL; pending = pending->next) {
        if (!pending->resolved) {
            free(pending->action);
            pending->action = ch_strdup("continue");
            pending->resolved = true;
            pthread_cond_signal(&pending->condition);
        }
    }
}

static void developer_ca_clear_locked(ch_developer_manager *manager) {
    X509_free(manager->ca_certificate);
    EVP_PKEY_free(manager->ca_key);
    free(manager->ca_pem);
    free(manager->ca_cert_path);
    free(manager->ca_key_path);
    manager->ca_certificate = NULL;
    manager->ca_key = NULL;
    manager->ca_pem = NULL;
    manager->ca_pem_length = 0U;
    manager->ca_cert_path = NULL;
    manager->ca_key_path = NULL;
}

static void developer_rules_clear_locked(ch_developer_manager *manager) {
    ch_json_value_destroy(manager->map_rules);
    ch_json_value_destroy(manager->breakpoint_rules);
    ch_json_value_destroy(manager->rewrite_rules);
    manager->map_rules = NULL;
    manager->breakpoint_rules = NULL;
    manager->rewrite_rules = NULL;
}

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
    char **copy = calloc(count == 0U ? 1U : count, sizeof(*copy));
    if (copy == NULL) return NULL;
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
    if (pthread_cond_init(&manager->pending_drained, NULL) != 0) {
        pthread_mutex_destroy(&manager->mutex);
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
    manager->destroying = true;
    developer_pending_continue_locked(manager);
    while (manager->pending != NULL) {
        pthread_cond_wait(&manager->pending_drained, &manager->mutex);
    }
    developer_manager_clear_locked(manager);
    developer_free_strings(manager->redact_headers,
                           manager->redact_header_count);
    developer_free_strings(manager->redact_query_params,
                           manager->redact_query_param_count);
    developer_free_strings(manager->ssl_decrypt_hosts,
                           manager->ssl_decrypt_host_count);
    developer_rules_clear_locked(manager);
    developer_ca_clear_locked(manager);
    manager->redact_headers = NULL;
    manager->redact_query_params = NULL;
    pthread_mutex_unlock(&manager->mutex);
    pthread_cond_destroy(&manager->pending_drained);
    pthread_mutex_destroy(&manager->mutex);
    free(manager);
}

static size_t developer_config_size(const ch_config_table *table,
                                    const char *key, size_t fallback) {
    int64_t value = 0;
    ch_error ignored;
    if (table == NULL ||
        ch_config_table_get_int(table, key, &value, &ignored) != CH_OK ||
        value < 0) {
        return fallback;
    }
    return (uint64_t)value > (uint64_t)SIZE_MAX ? SIZE_MAX : (size_t)value;
}

static char *developer_config_string(const ch_config_table *table,
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

static ch_json_value *developer_config_rules(const ch_config_table *developer,
                                             const char *key,
                                             ch_error *error) {
    const ch_config_array *array = developer == NULL ? NULL :
        ch_config_table_get_array(developer, key);
    if (array == NULL || ch_config_array_count(array) == 0U) {
        ch_json_value *empty = ch_json_value_new_array();
        if (empty == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "allocate developer %s", key);
        }
        return empty;
    }
    char *json = NULL;
    if (ch_config_array_json(array, &json, error) != CH_OK) return NULL;
    ch_json_value *rules = ch_json_parse(json, strlen(json), error);
    free(json);
    return rules;
}

static bool developer_path_join(const char *left, const char *right,
                                char **out) {
    size_t left_length = strlen(left);
    size_t right_length = strlen(right);
    bool separator = left_length > 0U && left[left_length - 1U] != '/';
    if (left_length > SIZE_MAX - right_length - (separator ? 2U : 1U)) {
        return false;
    }
    char *path = malloc(left_length + right_length + (separator ? 2U : 1U));
    if (path == NULL) return false;
    (void)snprintf(path, left_length + right_length + (separator ? 2U : 1U),
                   separator ? "%s/%s" : "%s%s", left, right);
    *out = path;
    return true;
}

static char *developer_default_ca_path(const char *filename) {
    const char *base = getenv("XDG_CONFIG_HOME");
    char *owned_base = NULL;
    if (base == NULL || base[0] == '\0') {
        const char *home = getenv("HOME");
        if (home != NULL && home[0] != '\0' &&
            developer_path_join(home, ".config", &owned_base)) {
            base = owned_base;
        } else {
            base = "/tmp";
        }
    }
    char *directory = NULL;
    char *developer_directory = NULL;
    char *path = NULL;
    if (developer_path_join(base, "clambhook", &directory) &&
        developer_path_join(directory, "developer", &developer_directory)) {
        (void)developer_path_join(developer_directory, filename, &path);
    }
    free(owned_base);
    free(directory);
    free(developer_directory);
    return path;
}

static ch_status developer_resolve_ca_path(const ch_config *config,
                                           const char *configured,
                                           const char *filename,
                                           char **out,
                                           ch_error *error) {
    if (configured != NULL && configured[0] != '\0') {
        return ch_config_resolve_path(config, configured, out, error);
    }
    *out = developer_default_ca_path(filename);
    if (*out == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate developer CA path");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    return CH_OK;
}

static ch_status developer_mkdir_parents(const char *path, ch_error *error) {
    char *copy = ch_strdup(path);
    if (copy == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy developer CA path");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    char *separator = strrchr(copy, '/');
    if (separator == NULL) {
        free(copy);
        return CH_OK;
    }
    if (separator == copy) {
        free(copy);
        return CH_OK;
    }
    *separator = '\0';
    for (char *cursor = copy + 1; ; ++cursor) {
        if (*cursor != '/' && *cursor != '\0') continue;
        char saved = *cursor;
        *cursor = '\0';
        if (mkdir(copy, 0700) != 0 && errno != EEXIST) {
            int code = errno;
            free(copy);
            ch_error_set(error, CH_ERROR_IO,
                         "create developer CA directory: %s",
                         strerror(code));
            return CH_ERROR_IO;
        }
        *cursor = saved;
        if (saved == '\0') break;
    }
    free(copy);
    return CH_OK;
}

static ch_status developer_write_secret_file(const char *path,
                                             const uint8_t *bytes,
                                             size_t length,
                                             ch_error *error) {
    if (developer_mkdir_parents(path, error) != CH_OK) return error->code;
    size_t needed = strlen(path) + 32U;
    char *temporary = malloc(needed);
    if (temporary == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate developer CA temporary path");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    (void)snprintf(temporary, needed, "%s.tmp-%ld", path, (long)getpid());
    int descriptor = open(temporary, O_WRONLY | O_CREAT | O_TRUNC, 0600);
    if (descriptor < 0) {
        int code = errno;
        free(temporary);
        ch_error_set(error, CH_ERROR_IO, "open developer CA file: %s",
                     strerror(code));
        return CH_ERROR_IO;
    }
    size_t offset = 0U;
    while (offset < length) {
        ssize_t written = write(descriptor, bytes + offset, length - offset);
        if (written < 0 && errno == EINTR) continue;
        if (written <= 0) break;
        offset += (size_t)written;
    }
    int saved = errno;
    bool okay = offset == length && fsync(descriptor) == 0 &&
        close(descriptor) == 0 && rename(temporary, path) == 0;
    if (!okay) {
        (void)close(descriptor);
        (void)unlink(temporary);
        free(temporary);
        ch_error_set(error, CH_ERROR_IO, "write developer CA file: %s",
                     strerror(saved == 0 ? EIO : saved));
        return CH_ERROR_IO;
    }
    free(temporary);
    return CH_OK;
}

static ch_status developer_read_file(const char *path, uint8_t **out,
                                     size_t *out_length, ch_error *error) {
    *out = NULL;
    *out_length = 0U;
    FILE *file = fopen(path, "rb");
    if (file == NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "open %s: %s", path,
                     strerror(errno));
        return CH_ERROR_NOT_FOUND;
    }
    if (fseek(file, 0L, SEEK_END) != 0) {
        fclose(file);
        ch_error_set(error, CH_ERROR_IO, "measure %s", path);
        return CH_ERROR_IO;
    }
    long length = ftell(file);
    if (length < 0L || (uint64_t)length > UINT64_C(16) * 1024U * 1024U ||
        fseek(file, 0L, SEEK_SET) != 0) {
        fclose(file);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer CA file is too large");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    uint8_t *bytes = malloc((size_t)length + 1U);
    if (bytes == NULL) {
        fclose(file);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate developer CA file");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    size_t read_length = fread(bytes, 1U, (size_t)length, file);
    bool okay = read_length == (size_t)length && fclose(file) == 0;
    if (!okay) {
        free(bytes);
        ch_error_set(error, CH_ERROR_IO, "read %s", path);
        return CH_ERROR_IO;
    }
    bytes[read_length] = '\0';
    *out = bytes;
    *out_length = read_length;
    return CH_OK;
}

static bool developer_x509_extension(X509 *certificate, X509 *issuer,
                                     int identifier, const char *value) {
    X509V3_CTX context;
    X509V3_set_ctx(&context, issuer, certificate, NULL, NULL, 0);
    X509_EXTENSION *extension = X509V3_EXT_conf_nid(
        NULL, &context, identifier, (char *)value);
    if (extension == NULL) return false;
    bool okay = X509_add_ext(certificate, extension, -1) == 1;
    X509_EXTENSION_free(extension);
    return okay;
}

static EVP_PKEY *developer_generate_key(ch_error *error) {
    EVP_PKEY_CTX *context = EVP_PKEY_CTX_new_id(EVP_PKEY_EC, NULL);
    EVP_PKEY *key = NULL;
    bool okay = context != NULL && EVP_PKEY_keygen_init(context) > 0 &&
        EVP_PKEY_CTX_set_ec_paramgen_curve_nid(context,
                                               NID_X9_62_prime256v1) > 0 &&
        EVP_PKEY_keygen(context, &key) > 0;
    EVP_PKEY_CTX_free(context);
    if (!okay) {
        EVP_PKEY_free(key);
        developer_openssl_error(error, "generate developer CA key");
        return NULL;
    }
    return key;
}

static bool developer_random_serial(X509 *certificate) {
    uint8_t serial_bytes[16];
    if (RAND_bytes(serial_bytes, (int)sizeof(serial_bytes)) != 1) return false;
    serial_bytes[0] &= 0x7fU;
    BIGNUM *number = BN_bin2bn(serial_bytes, (int)sizeof(serial_bytes), NULL);
    ASN1_INTEGER *serial = number == NULL ? NULL : BN_to_ASN1_INTEGER(number,
                                                                      NULL);
    bool okay = serial != NULL && X509_set_serialNumber(certificate, serial) == 1;
    ASN1_INTEGER_free(serial);
    BN_free(number);
    return okay;
}

static ch_status developer_generate_ca(EVP_PKEY **out_key,
                                       X509 **out_certificate,
                                       char **out_cert_pem,
                                       size_t *out_cert_length,
                                       char **out_key_pem,
                                       size_t *out_key_length,
                                       ch_error *error) {
    *out_key = NULL;
    *out_certificate = NULL;
    *out_cert_pem = NULL;
    *out_key_pem = NULL;
    EVP_PKEY *key = developer_generate_key(error);
    X509 *certificate = key == NULL ? NULL : X509_new();
    X509_NAME *name = certificate == NULL ? NULL : X509_NAME_new();
    bool okay = key != NULL && certificate != NULL && name != NULL &&
        X509_set_version(certificate, 2L) == 1 &&
        developer_random_serial(certificate) &&
        X509_gmtime_adj(X509_getm_notBefore(certificate), -3600L) != NULL &&
        X509_gmtime_adj(X509_getm_notAfter(certificate),
                        5L * 365L * 24L * 60L * 60L) != NULL &&
        X509_set_pubkey(certificate, key) == 1 &&
        X509_NAME_add_entry_by_txt(
            name, "CN", MBSTRING_ASC,
            (const unsigned char *)"Clambhook Developer Mode CA", -1, -1,
            0) == 1 &&
        X509_set_subject_name(certificate, name) == 1 &&
        X509_set_issuer_name(certificate, name) == 1 &&
        developer_x509_extension(certificate, certificate,
                                 NID_basic_constraints,
                                 "critical,CA:TRUE,pathlen:0") &&
        developer_x509_extension(certificate, certificate, NID_key_usage,
                                 "critical,keyCertSign,cRLSign") &&
        developer_x509_extension(certificate, certificate,
                                 NID_subject_key_identifier, "hash") &&
        X509_sign(certificate, key, EVP_sha256()) > 0;
    X509_NAME_free(name);
    if (!okay) {
        EVP_PKEY_free(key);
        X509_free(certificate);
        if (error->code == CH_OK) {
            developer_openssl_error(error, "generate developer CA");
        }
        return error->code;
    }
    BIO *cert_bio = BIO_new(BIO_s_mem());
    BIO *key_bio = BIO_new(BIO_s_mem());
    okay = cert_bio != NULL && key_bio != NULL &&
        PEM_write_bio_X509(cert_bio, certificate) == 1 &&
        PEM_write_bio_PrivateKey(key_bio, key, NULL, NULL, 0, NULL, NULL) == 1;
    BUF_MEM *cert_memory = NULL;
    BUF_MEM *key_memory = NULL;
    if (okay) {
        BIO_get_mem_ptr(cert_bio, &cert_memory);
        BIO_get_mem_ptr(key_bio, &key_memory);
        okay = cert_memory != NULL && key_memory != NULL;
    }
    char *cert_pem = okay ? malloc(cert_memory->length + 1U) : NULL;
    char *key_pem = okay ? malloc(key_memory->length + 1U) : NULL;
    if (cert_pem == NULL || key_pem == NULL) okay = false;
    if (okay) {
        memcpy(cert_pem, cert_memory->data, cert_memory->length);
        cert_pem[cert_memory->length] = '\0';
        memcpy(key_pem, key_memory->data, key_memory->length);
        key_pem[key_memory->length] = '\0';
        *out_cert_length = cert_memory->length;
        *out_key_length = key_memory->length;
    }
    BIO_free(cert_bio);
    BIO_free(key_bio);
    if (!okay) {
        free(cert_pem);
        free(key_pem);
        EVP_PKEY_free(key);
        X509_free(certificate);
        if (error->code == CH_OK) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "encode developer CA");
        }
        return error->code;
    }
    *out_key = key;
    *out_certificate = certificate;
    *out_cert_pem = cert_pem;
    *out_key_pem = key_pem;
    return CH_OK;
}

static ch_status developer_load_ca_files(const char *cert_path,
                                         const char *key_path,
                                         X509 **out_certificate,
                                         EVP_PKEY **out_key,
                                         char **out_pem,
                                         size_t *out_pem_length,
                                         ch_error *error) {
    uint8_t *cert_bytes = NULL;
    uint8_t *key_bytes = NULL;
    size_t cert_length = 0U;
    size_t key_length = 0U;
    if (developer_read_file(cert_path, &cert_bytes, &cert_length, error) !=
            CH_OK ||
        developer_read_file(key_path, &key_bytes, &key_length, error) !=
            CH_OK) {
        free(cert_bytes);
        free(key_bytes);
        return error->code;
    }
    BIO *cert_bio = BIO_new_mem_buf(cert_bytes, (int)cert_length);
    BIO *key_bio = BIO_new_mem_buf(key_bytes, (int)key_length);
    X509 *certificate = cert_bio == NULL ? NULL :
        PEM_read_bio_X509(cert_bio, NULL, NULL, NULL);
    EVP_PKEY *key = key_bio == NULL ? NULL :
        PEM_read_bio_PrivateKey(key_bio, NULL, NULL, NULL);
    bool okay = certificate != NULL && key != NULL &&
        X509_check_private_key(certificate, key) == 1 &&
        X509_check_ca(certificate) > 0;
    BIO_free(cert_bio);
    BIO_free(key_bio);
    free(key_bytes);
    if (!okay) {
        free(cert_bytes);
        X509_free(certificate);
        EVP_PKEY_free(key);
        developer_openssl_error(error, "parse developer CA");
        return error->code;
    }
    *out_certificate = certificate;
    *out_key = key;
    *out_pem = (char *)cert_bytes;
    *out_pem_length = cert_length;
    return CH_OK;
}

static ch_status developer_create_ca_files(const char *cert_path,
                                           const char *key_path,
                                           X509 **out_certificate,
                                           EVP_PKEY **out_key,
                                           char **out_pem,
                                           size_t *out_pem_length,
                                           ch_error *error) {
    char *cert_pem = NULL;
    char *key_pem = NULL;
    size_t cert_length = 0U;
    size_t key_length = 0U;
    X509 *certificate = NULL;
    EVP_PKEY *key = NULL;
    ch_status status = developer_generate_ca(
        &key, &certificate, &cert_pem, &cert_length, &key_pem, &key_length,
        error);
    if (status == CH_OK) {
        status = developer_write_secret_file(
            cert_path, (const uint8_t *)cert_pem, cert_length, error);
    }
    if (status == CH_OK) {
        status = developer_write_secret_file(
            key_path, (const uint8_t *)key_pem, key_length, error);
    }
    OPENSSL_cleanse(key_pem, key_length);
    free(key_pem);
    if (status != CH_OK) {
        free(cert_pem);
        X509_free(certificate);
        EVP_PKEY_free(key);
        return status;
    }
    *out_certificate = certificate;
    *out_key = key;
    *out_pem = cert_pem;
    *out_pem_length = cert_length;
    return CH_OK;
}

static ch_status developer_prepare_ca(const ch_config *config,
                                      const ch_config_table *developer,
                                      X509 **out_certificate,
                                      EVP_PKEY **out_key,
                                      char **out_pem,
                                      size_t *out_pem_length,
                                      char **out_cert_path,
                                      char **out_key_path,
                                      ch_error *error) {
    char *configured_cert = developer_config_string(developer,
                                                    "ca_cert_path");
    char *configured_key = developer_config_string(developer,
                                                   "ca_key_path");
    if (configured_cert == NULL || configured_key == NULL) {
        free(configured_cert);
        free(configured_key);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy developer CA paths");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_status status = developer_resolve_ca_path(
        config, configured_cert, "clambhook-developer-ca.pem", out_cert_path,
        error);
    if (status == CH_OK) {
        status = developer_resolve_ca_path(
            config, configured_key, "clambhook-developer-ca-key.pem",
            out_key_path, error);
    }
    free(configured_cert);
    free(configured_key);
    if (status != CH_OK) return status;
    status = developer_load_ca_files(
        *out_cert_path, *out_key_path, out_certificate, out_key, out_pem,
        out_pem_length, error);
    if (status == CH_OK) return CH_OK;
    ch_error_clear(error);
    return developer_create_ca_files(
        *out_cert_path, *out_key_path, out_certificate, out_key, out_pem,
        out_pem_length, error);
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
    bool mitm_enabled = false;
    bool no_cache_enabled = false;
    ch_error ignored;
    if (developer != NULL) {
        (void)ch_config_table_get_bool(developer, "enabled", &enabled,
                                      &ignored);
        (void)ch_config_table_get_bool(developer, "mitm_enabled",
                                      &mitm_enabled, &ignored);
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
    size_t ssl_decrypt_host_count = 0U;
    char **ssl_decrypt_hosts = developer_config_string_array(
        developer, "ssl_decrypt_hosts", NULL, 0U,
        &ssl_decrypt_host_count);
    ch_json_value *map_rules = developer_config_rules(
        developer, "map_rule", error);
    ch_json_value *breakpoint_rules = error->code == CH_OK ?
        developer_config_rules(developer, "breakpoint_rule", error) : NULL;
    ch_json_value *rewrite_rules = error->code == CH_OK ?
        developer_config_rules(developer, "rewrite_rule", error) : NULL;
    if (redact_headers == NULL || redact_query == NULL ||
        ssl_decrypt_hosts == NULL || map_rules == NULL ||
        breakpoint_rules == NULL || rewrite_rules == NULL) {
        developer_free_strings(redact_headers, redact_header_count);
        developer_free_strings(redact_query, redact_query_count);
        developer_free_strings(ssl_decrypt_hosts, ssl_decrypt_host_count);
        ch_json_value_destroy(map_rules);
        ch_json_value_destroy(breakpoint_rules);
        ch_json_value_destroy(rewrite_rules);
        if (error->code == CH_OK) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "configure developer state");
        }
        return error->code;
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

    X509 *ca_certificate = NULL;
    EVP_PKEY *ca_key = NULL;
    char *ca_pem = NULL;
    size_t ca_pem_length = 0U;
    char *ca_cert_path = NULL;
    char *ca_key_path = NULL;
    if (enabled && mitm_enabled && developer_prepare_ca(
            config, developer, &ca_certificate, &ca_key, &ca_pem,
            &ca_pem_length, &ca_cert_path, &ca_key_path, error) != CH_OK) {
        developer_free_strings(redact_headers, redact_header_count);
        developer_free_strings(redact_query, redact_query_count);
        developer_free_strings(ssl_decrypt_hosts, ssl_decrypt_host_count);
        ch_json_value_destroy(map_rules);
        ch_json_value_destroy(breakpoint_rules);
        ch_json_value_destroy(rewrite_rules);
        free(ca_cert_path);
        free(ca_key_path);
        return error->code;
    }

    pthread_mutex_lock(&manager->mutex);
    bool was_enabled = manager->enabled;
    developer_pending_continue_locked(manager);
    manager->enabled = enabled;
    manager->mitm_enabled = enabled && mitm_enabled;
    manager->no_cache_enabled = no_cache_enabled;
    manager->capture_limit = capture_limit;
    manager->body_limit = body_limit;
    manager->header_value_limit = header_limit;
    developer_free_strings(manager->redact_headers,
                           manager->redact_header_count);
    developer_free_strings(manager->redact_query_params,
                           manager->redact_query_param_count);
    developer_free_strings(manager->ssl_decrypt_hosts,
                           manager->ssl_decrypt_host_count);
    developer_rules_clear_locked(manager);
    developer_ca_clear_locked(manager);
    manager->redact_headers = redact_headers;
    manager->redact_header_count = redact_header_count;
    manager->redact_query_params = redact_query;
    manager->redact_query_param_count = redact_query_count;
    manager->ssl_decrypt_hosts = ssl_decrypt_hosts;
    manager->ssl_decrypt_host_count = ssl_decrypt_host_count;
    manager->map_rules = map_rules;
    manager->breakpoint_rules = breakpoint_rules;
    manager->rewrite_rules = rewrite_rules;
    manager->ca_certificate = ca_certificate;
    manager->ca_key = ca_key;
    manager->ca_pem = ca_pem;
    manager->ca_pem_length = ca_pem_length;
    manager->ca_cert_path = ca_cert_path;
    manager->ca_key_path = ca_key_path;
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
    bool okay = ch_json_append_bytes(&result, url,
                                     (size_t)(query - url) + 1U);
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
                                        (size_t)(equals - cursor) + 1U) &&
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
    if (sscanf(capture->response_header_buffer, "HTTP/%*s %d", &status) ==
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
    static const char alphabet[] =
        "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    size_t length = body->preview_length;
    if (length > (SIZE_MAX / 4U) * 3U - 2U) return NULL;
    size_t encoded_length = ((length + 2U) / 3U) * 4U;
    char *encoded = malloc(encoded_length + 1U);
    if (encoded == NULL) return NULL;
    size_t input = 0U;
    size_t output = 0U;
    while (input + 3U <= length) {
        uint32_t value = (uint32_t)body->preview[input] << 16U |
            (uint32_t)body->preview[input + 1U] << 8U |
            (uint32_t)body->preview[input + 2U];
        encoded[output++] = alphabet[(value >> 18U) & 0x3fU];
        encoded[output++] = alphabet[(value >> 12U) & 0x3fU];
        encoded[output++] = alphabet[(value >> 6U) & 0x3fU];
        encoded[output++] = alphabet[value & 0x3fU];
        input += 3U;
    }
    size_t remaining = length - input;
    if (remaining > 0U) {
        uint32_t value = (uint32_t)body->preview[input] << 16U;
        if (remaining == 2U) {
            value |= (uint32_t)body->preview[input + 1U] << 8U;
        }
        encoded[output++] = alphabet[(value >> 18U) & 0x3fU];
        encoded[output++] = alphabet[(value >> 12U) & 0x3fU];
        encoded[output++] = remaining == 2U ?
            alphabet[(value >> 6U) & 0x3fU] : '=';
        encoded[output++] = '=';
    }
    encoded[output] = '\0';
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

static bool developer_x509_time_text(const ASN1_TIME *value,
                                     char output[32]) {
    struct tm time_value;
    memset(&time_value, 0, sizeof(time_value));
    if (value == NULL || ASN1_TIME_to_tm(value, &time_value) != 1) {
        output[0] = '\0';
        return false;
    }
    return strftime(output, 32U, "%Y-%m-%dT%H:%M:%SZ", &time_value) > 0U;
}

static bool developer_ca_fingerprint(X509 *certificate, char output[65]) {
    unsigned char digest[EVP_MAX_MD_SIZE];
    unsigned int length = 0U;
    if (certificate == NULL ||
        X509_digest(certificate, EVP_sha256(), digest, &length) != 1 ||
        length != SHA256_DIGEST_LENGTH) {
        output[0] = '\0';
        return false;
    }
    for (unsigned int index = 0U; index < length; ++index) {
        (void)snprintf(output + index * 2U, 3U, "%02X", digest[index]);
    }
    output[64] = '\0';
    return true;
}

static bool developer_append_status_locked(ch_developer_manager *manager,
                                           ch_json_buffer *json) {
    if (!ch_json_append_format(
            json,
            "{\"enabled\":%s,\"mitm_enabled\":%s,"
            "\"no_cache_enabled\":%s,\"capture_limit\":%zu,"
            "\"body_limit_bytes\":%zu,\"header_value_limit_bytes\":%zu,"
            "\"capture_count\":%zu",
            manager->enabled ? "true" : "false",
            manager->mitm_enabled && manager->ca_certificate != NULL ?
                "true" : "false",
            manager->no_cache_enabled ? "true" : "false",
            manager->capture_limit, manager->body_limit,
            manager->header_value_limit, manager->entry_count)) return false;
    if (manager->ca_certificate != NULL) {
        char fingerprint[65];
        char not_before[32];
        char not_after[32];
        (void)developer_ca_fingerprint(manager->ca_certificate, fingerprint);
        (void)developer_x509_time_text(
            X509_get0_notBefore(manager->ca_certificate), not_before);
        (void)developer_x509_time_text(
            X509_get0_notAfter(manager->ca_certificate), not_after);
        if (manager->ca_cert_path != NULL &&
            (!ch_json_append(json, ",\"ca_cert_path\":") ||
             !ch_json_append_string(json, manager->ca_cert_path))) return false;
        if (fingerprint[0] != '\0' &&
            (!ch_json_append(json, ",\"ca_fingerprint_sha256\":") ||
             !ch_json_append_string(json, fingerprint))) return false;
        if (not_before[0] != '\0' &&
            (!ch_json_append(json, ",\"ca_not_before\":") ||
             !ch_json_append_string(json, not_before))) return false;
        if (not_after[0] != '\0' &&
            (!ch_json_append(json, ",\"ca_not_after\":") ||
             !ch_json_append_string(json, not_after))) return false;
    }
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
    bool okay = developer_append_status_locked(manager, &json);
    pthread_mutex_unlock(&manager->mutex);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode developer status");
    }
    return result;
}

char *ch_developer_ca_pem(ch_developer_manager *manager,
                          size_t *out_length,
                          ch_error *error) {
    ch_error_clear(error);
    if (out_length != NULL) *out_length = 0U;
    if (manager == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer capture manager is required");
        return NULL;
    }
    pthread_mutex_lock(&manager->mutex);
    char *copy = NULL;
    if (manager->ca_pem != NULL && manager->ca_pem_length > 0U) {
        copy = malloc(manager->ca_pem_length + 1U);
        if (copy != NULL) {
            memcpy(copy, manager->ca_pem, manager->ca_pem_length);
            copy[manager->ca_pem_length] = '\0';
            if (out_length != NULL) *out_length = manager->ca_pem_length;
        }
    }
    bool available = manager->ca_pem != NULL;
    pthread_mutex_unlock(&manager->mutex);
    if (copy == NULL) {
        ch_error_set(error, available ? CH_ERROR_OUT_OF_MEMORY :
                                      CH_ERROR_NOT_FOUND,
                     available ? "copy developer CA certificate" :
                                 "developer MITM CA unavailable");
    }
    return copy;
}

char *ch_developer_regenerate_ca_json(ch_developer_manager *manager,
                                      ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer capture manager is required");
        return NULL;
    }
    pthread_mutex_lock(&manager->mutex);
    bool enabled = manager->enabled && manager->mitm_enabled;
    char *cert_path = ch_strdup(manager->ca_cert_path == NULL ? "" :
                                                       manager->ca_cert_path);
    char *key_path = ch_strdup(manager->ca_key_path == NULL ? "" :
                                                     manager->ca_key_path);
    pthread_mutex_unlock(&manager->mutex);
    if (!enabled || cert_path == NULL || key_path == NULL ||
        cert_path[0] == '\0' || key_path[0] == '\0') {
        free(cert_path);
        free(key_path);
        ch_error_set(error, enabled ? CH_ERROR_OUT_OF_MEMORY :
                                      CH_ERROR_INVALID_STATE,
                     enabled ? "copy developer CA paths" :
                               "developer MITM CA unavailable");
        return NULL;
    }
    X509 *certificate = NULL;
    EVP_PKEY *key = NULL;
    char *pem = NULL;
    size_t pem_length = 0U;
    if (developer_create_ca_files(cert_path, key_path, &certificate, &key,
                                  &pem, &pem_length, error) != CH_OK) {
        free(cert_path);
        free(key_path);
        return NULL;
    }
    pthread_mutex_lock(&manager->mutex);
    developer_ca_clear_locked(manager);
    manager->ca_certificate = certificate;
    manager->ca_key = key;
    manager->ca_pem = pem;
    manager->ca_pem_length = pem_length;
    manager->ca_cert_path = cert_path;
    manager->ca_key_path = key_path;
    ch_json_buffer json;
    ch_json_init(&json);
    bool okay = developer_append_status_locked(manager, &json);
    pthread_mutex_unlock(&manager->mutex);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode regenerated developer CA status");
    }
    return result;
}

bool ch_developer_should_mitm(ch_developer_manager *manager,
                              const char *host) {
    if (manager == NULL || host == NULL) return false;
    char *normalized = developer_trimmed_lower(host);
    if (normalized == NULL) return false;
    size_t length = strlen(normalized);
    if (length >= 2U && normalized[0] == '[' &&
        normalized[length - 1U] == ']') {
        memmove(normalized, normalized + 1U, length - 2U);
        normalized[length - 2U] = '\0';
    }
    pthread_mutex_lock(&manager->mutex);
    bool matches = manager->enabled && manager->mitm_enabled &&
        manager->ca_certificate != NULL;
    if (matches && manager->ssl_decrypt_host_count > 0U) {
        matches = false;
        for (size_t index = 0U;
             index < manager->ssl_decrypt_host_count; ++index) {
            if (fnmatch(manager->ssl_decrypt_hosts[index], normalized, 0) ==
                0) {
                matches = true;
                break;
            }
        }
    }
    pthread_mutex_unlock(&manager->mutex);
    free(normalized);
    return matches;
}

static ch_status developer_generate_leaf(X509 *issuer, EVP_PKEY *issuer_key,
                                         const char *host,
                                         X509 **out_certificate,
                                         EVP_PKEY **out_key,
                                         ch_error *error) {
    EVP_PKEY *key = developer_generate_key(error);
    X509 *certificate = key == NULL ? NULL : X509_new();
    X509_NAME *name = certificate == NULL ? NULL : X509_NAME_new();
    bool okay = key != NULL && certificate != NULL && name != NULL &&
        X509_set_version(certificate, 2L) == 1 &&
        developer_random_serial(certificate) &&
        X509_gmtime_adj(X509_getm_notBefore(certificate), -3600L) != NULL &&
        X509_gmtime_adj(X509_getm_notAfter(certificate),
                        48L * 60L * 60L) != NULL &&
        X509_set_pubkey(certificate, key) == 1 &&
        X509_NAME_add_entry_by_txt(
            name, "CN", MBSTRING_ASC, (const unsigned char *)host, -1, -1,
            0) == 1 &&
        X509_set_subject_name(certificate, name) == 1 &&
        X509_set_issuer_name(certificate, X509_get_subject_name(issuer)) == 1 &&
        developer_x509_extension(certificate, issuer, NID_basic_constraints,
                                 "critical,CA:FALSE") &&
        developer_x509_extension(certificate, issuer, NID_key_usage,
                                 "critical,digitalSignature") &&
        developer_x509_extension(certificate, issuer,
                                 NID_ext_key_usage, "serverAuth");
    uint8_t address[16];
    char subject_alt_name[1024];
    if (okay) {
        const char *kind = inet_pton(AF_INET, host, address) == 1 ||
            inet_pton(AF_INET6, host, address) == 1 ? "IP" : "DNS";
        int written = snprintf(subject_alt_name, sizeof(subject_alt_name),
                               "%s:%s", kind, host);
        okay = written > 0 && (size_t)written < sizeof(subject_alt_name) &&
            developer_x509_extension(certificate, issuer,
                                     NID_subject_alt_name, subject_alt_name) &&
            X509_sign(certificate, issuer_key, EVP_sha256()) > 0;
    }
    X509_NAME_free(name);
    if (!okay) {
        X509_free(certificate);
        EVP_PKEY_free(key);
        if (error->code == CH_OK) {
            developer_openssl_error(error,
                                    "generate developer TLS certificate");
        }
        return error->code;
    }
    *out_certificate = certificate;
    *out_key = key;
    return CH_OK;
}

ch_status ch_developer_tls_server_context(ch_developer_manager *manager,
                                          const char *host,
                                          SSL_CTX **out_context,
                                          ch_error *error) {
    ch_error_clear(error);
    if (out_context != NULL) *out_context = NULL;
    if (manager == NULL || host == NULL || host[0] == '\0' ||
        out_context == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer manager, TLS host, and output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (!ch_developer_should_mitm(manager, host)) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "developer MITM is disabled for host");
        return CH_ERROR_INVALID_STATE;
    }
    pthread_mutex_lock(&manager->mutex);
    X509 *issuer = manager->ca_certificate;
    EVP_PKEY *issuer_key = manager->ca_key;
    bool issuer_referenced = issuer != NULL && X509_up_ref(issuer) == 1;
    bool key_referenced = issuer_key != NULL &&
        EVP_PKEY_up_ref(issuer_key) == 1;
    pthread_mutex_unlock(&manager->mutex);
    if (!issuer_referenced || !key_referenced) {
        if (issuer_referenced) X509_free(issuer);
        if (key_referenced) EVP_PKEY_free(issuer_key);
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "developer MITM CA unavailable");
        return CH_ERROR_INVALID_STATE;
    }
    char *normalized = developer_trimmed_lower(host);
    if (normalized != NULL) {
        size_t length = strlen(normalized);
        if (length >= 2U && normalized[0] == '[' &&
            normalized[length - 1U] == ']') {
            memmove(normalized, normalized + 1U, length - 2U);
            normalized[length - 2U] = '\0';
        }
    }
    X509 *leaf = NULL;
    EVP_PKEY *leaf_key = NULL;
    ch_status status = normalized == NULL ? CH_ERROR_OUT_OF_MEMORY :
        developer_generate_leaf(issuer, issuer_key, normalized, &leaf,
                                &leaf_key, error);
    free(normalized);
    X509_free(issuer);
    EVP_PKEY_free(issuer_key);
    if (status != CH_OK) {
        if (error->code == CH_OK) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy developer TLS host");
        }
        return error->code;
    }
    SSL_CTX *context = SSL_CTX_new(TLS_server_method());
    bool okay = context != NULL &&
        SSL_CTX_set_min_proto_version(context, TLS1_2_VERSION) == 1 &&
        SSL_CTX_use_certificate(context, leaf) == 1 &&
        SSL_CTX_use_PrivateKey(context, leaf_key) == 1 &&
        SSL_CTX_check_private_key(context) == 1;
    X509_free(leaf);
    EVP_PKEY_free(leaf_key);
    if (!okay) {
        SSL_CTX_free(context);
        developer_openssl_error(error,
                                "create developer TLS server context");
        return error->code;
    }
    SSL_CTX_set_options(context, SSL_OP_NO_COMPRESSION);
    *out_context = context;
    return CH_OK;
}

static const char *developer_json_string(const ch_json_value *object,
                                         const char *key) {
    return ch_json_string_value(ch_json_object_get(object, key));
}

static bool developer_rule_matches(const ch_json_value *rule,
                                   const ch_developer_http_message *request) {
    if (!ch_json_bool_value(ch_json_object_get(rule, "enabled"), false)) {
        return false;
    }
    const ch_json_value *match = ch_json_object_get(rule, "match");
    if (ch_json_value_type(match) != CH_JSON_OBJECT) return true;
    const ch_json_value *methods = ch_json_object_get(match, "methods");
    if (ch_json_value_type(methods) == CH_JSON_ARRAY &&
        ch_json_array_size(methods) > 0U) {
        bool found = false;
        for (size_t index = 0U; index < ch_json_array_size(methods); ++index) {
            const char *method = ch_json_string_value(
                ch_json_array_get(methods, index));
            if (method != NULL && request->method != NULL &&
                strcasecmp(method, request->method) == 0) {
                found = true;
                break;
            }
        }
        if (!found) return false;
    }
    const char *host = developer_json_string(match, "host");
    if (host != NULL && host[0] != '\0' &&
        (request->host == NULL || strcasecmp(host, request->host) != 0)) {
        return false;
    }
    const char *path_prefix = developer_json_string(match, "path_prefix");
    if (path_prefix != NULL && path_prefix[0] != '\0' &&
        (request->path == NULL ||
         strncmp(request->path, path_prefix, strlen(path_prefix)) != 0)) {
        return false;
    }
    const char *contains = developer_json_string(match, "url_contains");
    return contains == NULL || contains[0] == '\0' ||
        (request->url != NULL && strstr(request->url, contains) != NULL);
}

static bool developer_stage_matches(const ch_json_value *rule,
                                    const char *stage) {
    const char *configured = developer_json_string(rule, "stage");
    return configured != NULL &&
        (strcmp(configured, "both") == 0 || strcmp(configured, stage) == 0);
}

static ssize_t developer_header_index(
    const ch_developer_http_message *message, const char *name) {
    for (size_t index = 0U; index < message->header_count; ++index) {
        if (message->headers[index].name != NULL &&
            strcasecmp(message->headers[index].name, name) == 0) {
            return (ssize_t)index;
        }
    }
    return -1;
}

static bool developer_header_add(ch_developer_http_message *message,
                                 const char *name, const char *value) {
    if (name == NULL || name[0] == '\0') return true;
    ch_developer_http_header *next = realloc(
        message->headers,
        (message->header_count + 1U) * sizeof(*message->headers));
    if (next == NULL) return false;
    message->headers = next;
    ch_developer_http_header *header = &next[message->header_count];
    memset(header, 0, sizeof(*header));
    header->name = ch_strdup(name);
    header->value = ch_strdup(value == NULL ? "" : value);
    if (header->name == NULL || header->value == NULL) {
        free(header->name);
        free(header->value);
        memset(header, 0, sizeof(*header));
        return false;
    }
    ++message->header_count;
    return true;
}

static void developer_header_remove(ch_developer_http_message *message,
                                    const char *name) {
    size_t output = 0U;
    for (size_t index = 0U; index < message->header_count; ++index) {
        if (message->headers[index].name != NULL &&
            strcasecmp(message->headers[index].name, name) == 0) {
            free(message->headers[index].name);
            free(message->headers[index].value);
            continue;
        }
        if (output != index) message->headers[output] = message->headers[index];
        ++output;
    }
    message->header_count = output;
}

static bool developer_header_set(ch_developer_http_message *message,
                                 const char *name, const char *value) {
    developer_header_remove(message, name);
    return developer_header_add(message, name, value);
}

static bool developer_body_set(ch_developer_http_message *message,
                               const uint8_t *body, size_t length) {
    uint8_t *copy = length == 0U ? NULL : malloc(length);
    if (copy == NULL && length > 0U) return false;
    if (length > 0U) memcpy(copy, body, length);
    free(message->body);
    message->body = copy;
    message->body_length = length;
    message->body_set = true;
    char content_length[32];
    (void)snprintf(content_length, sizeof(content_length), "%zu", length);
    return developer_header_set(message, "Content-Length", content_length);
}

static bool developer_body_replace(ch_developer_http_message *message,
                                   const char *needle,
                                   const char *replacement) {
    size_t needle_length = needle == NULL ? 0U : strlen(needle);
    size_t replacement_length = replacement == NULL ? 0U :
        strlen(replacement);
    if (needle_length == 0U) return true;
    size_t occurrences = 0U;
    for (size_t index = 0U; index + needle_length <= message->body_length;) {
        if (memcmp(message->body + index, needle, needle_length) == 0) {
            ++occurrences;
            index += needle_length;
        } else {
            ++index;
        }
    }
    if (occurrences == 0U) return true;
    if (replacement_length > needle_length && occurrences >
        (SIZE_MAX - message->body_length) /
            (replacement_length - needle_length)) return false;
    size_t next_length = replacement_length >= needle_length ?
        message->body_length + occurrences *
            (replacement_length - needle_length) :
        message->body_length - occurrences *
            (needle_length - replacement_length);
    uint8_t *next = next_length == 0U ? NULL : malloc(next_length);
    if (next == NULL && next_length > 0U) return false;
    size_t input = 0U;
    size_t output = 0U;
    while (input < message->body_length) {
        if (input + needle_length <= message->body_length &&
            memcmp(message->body + input, needle, needle_length) == 0) {
            if (replacement_length > 0U) {
                memcpy(next + output, replacement, replacement_length);
            }
            output += replacement_length;
            input += needle_length;
        } else {
            next[output++] = message->body[input++];
        }
    }
    bool okay = developer_body_set(message, next, next_length);
    free(next);
    return okay;
}

static bool developer_apply_rewrites(
    const ch_json_value *rules, const char *stage,
    const ch_developer_http_message *request,
    ch_developer_http_message *message) {
    for (size_t rule_index = 0U;
         rule_index < ch_json_array_size(rules); ++rule_index) {
        const ch_json_value *rule = ch_json_array_get(rules, rule_index);
        if (!developer_rule_matches(rule, request) ||
            !developer_stage_matches(rule, stage)) continue;
        const ch_json_value *operations = ch_json_object_get(rule, "op");
        if (ch_json_value_type(operations) != CH_JSON_ARRAY) {
            operations = ch_json_object_get(rule, "ops");
        }
        for (size_t operation_index = 0U;
             operation_index < ch_json_array_size(operations);
             ++operation_index) {
            const ch_json_value *operation = ch_json_array_get(
                operations, operation_index);
            const char *target = developer_json_string(operation, "target");
            const char *action = developer_json_string(operation, "action");
            const char *field = developer_json_string(operation, "field");
            const char *value = developer_json_string(operation, "value");
            const char *replacement = developer_json_string(operation,
                                                            "replace");
            if (target == NULL || action == NULL) continue;
            if (strcmp(target, "header") == 0) {
                if (strcmp(action, "add") == 0 &&
                    !developer_header_add(message, field, value)) return false;
                if (strcmp(action, "set") == 0 &&
                    !developer_header_set(message, field, value)) return false;
                if (strcmp(action, "remove") == 0) {
                    developer_header_remove(message, field);
                }
            } else if (strcmp(target, "body") == 0) {
                if (strcmp(action, "set") == 0 &&
                    !developer_body_set(message, (const uint8_t *)(
                        value == NULL ? "" : value),
                        strlen(value == NULL ? "" : value))) return false;
                if (strcmp(action, "replace") == 0 &&
                    !developer_body_replace(message, value, replacement)) {
                    return false;
                }
            } else if (strcmp(target, "status") == 0 &&
                       strcmp(action, "set") == 0 && value != NULL) {
                char *end = NULL;
                long status = strtol(value, &end, 10);
                if (end != value && *end == '\0' && status >= 100L &&
                    status <= 599L) message->status = (int)status;
            }
        }
    }
    return true;
}

static bool developer_apply_no_cache_request(
    ch_developer_http_message *message) {
    static const char *const conditional[] = {
        "If-Match", "If-None-Match", "If-Modified-Since",
        "If-Unmodified-Since", "If-Range"
    };
    for (size_t index = 0U;
         index < sizeof(conditional) / sizeof(conditional[0]); ++index) {
        developer_header_remove(message, conditional[index]);
    }
    return developer_header_set(message, "Cache-Control", "no-cache") &&
        developer_header_set(message, "Pragma", "no-cache");
}

static bool developer_apply_no_cache_response(
    ch_developer_http_message *message) {
    return developer_header_set(
               message, "Cache-Control",
               "no-store, no-cache, must-revalidate") &&
        developer_header_set(message, "Pragma", "no-cache") &&
        developer_header_set(message, "Expires", "0");
}

static bool developer_message_update_url(ch_developer_http_message *message,
                                         const char *url) {
    CURLU *parsed = curl_url();
    if (parsed == NULL || curl_url_set(parsed, CURLUPART_URL, url, 0U) !=
                              CURLUE_OK) {
        curl_url_cleanup(parsed);
        return false;
    }
    char *host = NULL;
    char *port = NULL;
    char *path = NULL;
    char *query = NULL;
    bool okay = curl_url_get(parsed, CURLUPART_HOST, &host, 0U) == CURLUE_OK &&
        curl_url_get(parsed, CURLUPART_PATH, &path, 0U) == CURLUE_OK;
    (void)curl_url_get(parsed, CURLUPART_PORT, &port, 0U);
    (void)curl_url_get(parsed, CURLUPART_QUERY, &query, 0U);
    ch_json_buffer authority;
    ch_json_init(&authority);
    ch_json_buffer request_path;
    ch_json_init(&request_path);
    okay = okay && ch_json_append(&authority, host) &&
        (port == NULL || (ch_json_append(&authority, ":") &&
                          ch_json_append(&authority, port))) &&
        ch_json_append(&request_path, path == NULL || path[0] == '\0' ?
                                         "/" : path) &&
        (query == NULL || (ch_json_append(&request_path, "?") &&
                           ch_json_append(&request_path, query)));
    char *authority_text = okay ? ch_json_take(&authority) : NULL;
    char *path_text = okay ? ch_json_take(&request_path) : NULL;
    char *url_copy = okay ? ch_strdup(url) : NULL;
    ch_json_dispose(&authority);
    ch_json_dispose(&request_path);
    curl_free(host);
    curl_free(port);
    curl_free(path);
    curl_free(query);
    curl_url_cleanup(parsed);
    if (authority_text == NULL || path_text == NULL || url_copy == NULL) {
        free(authority_text);
        free(path_text);
        free(url_copy);
        return false;
    }
    free(message->url);
    free(message->host);
    free(message->path);
    message->url = url_copy;
    message->host = authority_text;
    message->path = path_text;
    return developer_header_set(message, "Host", authority_text);
}

static char *developer_remote_map_url(
    const ch_json_value *rule, const ch_developer_http_message *request) {
    const char *remote_url = developer_json_string(rule, "remote_url");
    if (remote_url == NULL || request->url == NULL) return NULL;
    CURLU *remote = curl_url();
    CURLU *source = curl_url();
    if (remote == NULL || source == NULL ||
        curl_url_set(remote, CURLUPART_URL, remote_url, 0U) != CURLUE_OK ||
        curl_url_set(source, CURLUPART_URL, request->url, 0U) != CURLUE_OK) {
        curl_url_cleanup(remote);
        curl_url_cleanup(source);
        return NULL;
    }
    char *base_path = NULL;
    char *source_path = NULL;
    char *source_query = NULL;
    char *remote_query = NULL;
    (void)curl_url_get(remote, CURLUPART_PATH, &base_path, 0U);
    (void)curl_url_get(source, CURLUPART_PATH, &source_path, 0U);
    (void)curl_url_get(source, CURLUPART_QUERY, &source_query, 0U);
    (void)curl_url_get(remote, CURLUPART_QUERY, &remote_query, 0U);
    const ch_json_value *match = ch_json_object_get(rule, "match");
    const char *prefix = developer_json_string(match, "path_prefix");
    const char *suffix = source_path == NULL ? "/" : source_path;
    if (prefix != NULL && prefix[0] != '\0' &&
        strncmp(suffix, prefix, strlen(prefix)) == 0) {
        suffix += strlen(prefix);
    }
    ch_json_buffer path;
    ch_json_init(&path);
    const char *base = base_path == NULL || base_path[0] == '\0' ? "/" :
                                                                       base_path;
    bool base_is_root = strcmp(base, "/") == 0;
    bool okay = ch_json_append(&path, base_is_root ? "" : base) &&
        ((suffix[0] == '\0' || strcmp(suffix, "/") == 0) ||
         ((path.length == 0U || path.data[path.length - 1U] == '/' ||
           ch_json_append(&path, "/")) &&
          ch_json_append(&path, suffix[0] == '/' ? suffix + 1U : suffix)));
    if (path.length == 0U) okay = okay && ch_json_append(&path, "/");
    char *mapped_path = okay ? ch_json_take(&path) : NULL;
    ch_json_dispose(&path);
    if (mapped_path != NULL) {
        okay = curl_url_set(remote, CURLUPART_PATH, mapped_path, 0U) == CURLUE_OK;
    }
    if (okay && remote_query == NULL && source_query != NULL) {
        okay = curl_url_set(remote, CURLUPART_QUERY, source_query, 0U) ==
            CURLUE_OK;
    }
    char *mapped = NULL;
    if (okay) (void)curl_url_get(remote, CURLUPART_URL, &mapped, 0U);
    char *result = mapped == NULL ? NULL : ch_strdup(mapped);
    curl_free(mapped);
    free(mapped_path);
    curl_free(base_path);
    curl_free(source_path);
    curl_free(source_query);
    curl_free(remote_query);
    curl_url_cleanup(remote);
    curl_url_cleanup(source);
    return result;
}

static bool developer_safe_local_suffix(const char *path, char **out) {
    const char *end = path == NULL ? NULL : strchr(path, '?');
    size_t length = path == NULL ? 0U :
        (end == NULL ? strlen(path) : (size_t)(end - path));
    while (length > 0U && *path == '/') {
        ++path;
        --length;
    }
    char *copy = strndup(path == NULL ? "" : path, length);
    if (copy == NULL) return false;
    char *cursor = copy;
    while (*cursor != '\0') {
        char *separator = strchr(cursor, '/');
        if (separator != NULL) *separator = '\0';
        if (strcmp(cursor, "..") == 0) {
            free(copy);
            return false;
        }
        if (separator == NULL) break;
        *separator = '/';
        cursor = separator + 1U;
    }
    *out = copy;
    return true;
}

static ch_status developer_read_local_file(const char *path, uint8_t **out,
                                           size_t *out_length,
                                           ch_error *error) {
    *out = NULL;
    *out_length = 0U;
    FILE *file = fopen(path, "rb");
    if (file == NULL) {
        ch_error_set(error, CH_ERROR_IO, "read mapped file %s: %s", path,
                     strerror(errno));
        return CH_ERROR_IO;
    }
    if (fseek(file, 0L, SEEK_END) != 0) {
        fclose(file);
        ch_error_set(error, CH_ERROR_IO, "measure mapped file %s", path);
        return CH_ERROR_IO;
    }
    long measured = ftell(file);
    if (measured < 0L || (uint64_t)measured > UINT64_C(256) * 1024U * 1024U ||
        fseek(file, 0L, SEEK_SET) != 0) {
        fclose(file);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "mapped file exceeds 256 MiB");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    uint8_t *body = measured == 0L ? NULL : malloc((size_t)measured);
    if (body == NULL && measured > 0L) {
        fclose(file);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "read mapped file");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    bool okay = fread(body, 1U, (size_t)measured, file) ==
                    (size_t)measured &&
        fclose(file) == 0;
    if (!okay) {
        free(body);
        ch_error_set(error, CH_ERROR_IO, "read mapped file %s", path);
        return CH_ERROR_IO;
    }
    *out = body;
    *out_length = (size_t)measured;
    return CH_OK;
}

static const char *developer_content_type(const char *path,
                                          const uint8_t *body,
                                          size_t length) {
    const char *extension = path == NULL ? NULL : strrchr(path, '.');
    if (extension != NULL) {
        if (strcasecmp(extension, ".html") == 0 ||
            strcasecmp(extension, ".htm") == 0) return "text/html; charset=utf-8";
        if (strcasecmp(extension, ".json") == 0) return "application/json";
        if (strcasecmp(extension, ".css") == 0) return "text/css; charset=utf-8";
        if (strcasecmp(extension, ".js") == 0) return "text/javascript; charset=utf-8";
        if (strcasecmp(extension, ".txt") == 0) return "text/plain; charset=utf-8";
        if (strcasecmp(extension, ".svg") == 0) return "image/svg+xml";
        if (strcasecmp(extension, ".png") == 0) return "image/png";
        if (strcasecmp(extension, ".jpg") == 0 ||
            strcasecmp(extension, ".jpeg") == 0) return "image/jpeg";
    }
    bool text = length == 0U;
    for (size_t index = 0U; !text && index < length; ++index) {
        if (body[index] == 0U) return "application/octet-stream";
        text = index + 1U == length;
    }
    return text ? "text/plain; charset=utf-8" : "application/octet-stream";
}

static ch_status developer_apply_local_map(
    const ch_json_value *rule, const ch_developer_http_message *request,
    ch_developer_http_result *result, ch_error *error) {
    const char *configured = developer_json_string(rule, "local_path");
    if (configured == NULL || configured[0] == '\0') {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer local map path is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    char *path = ch_strdup(configured);
    struct stat information;
    if (path == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy developer local map path");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (stat(path, &information) == 0 && S_ISDIR(information.st_mode)) {
        char *suffix = NULL;
        char *joined = NULL;
        if (!developer_safe_local_suffix(request->path, &suffix) ||
            !developer_path_join(path, suffix == NULL ? "" : suffix,
                                 &joined)) {
            free(suffix);
            free(path);
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "unsafe developer local map path");
            return CH_ERROR_INVALID_ARGUMENT;
        }
        free(suffix);
        free(path);
        path = joined;
    }
    uint8_t *body = NULL;
    size_t body_length = 0U;
    ch_status status = developer_read_local_file(path, &body, &body_length,
                                                 error);
    if (status != CH_OK) {
        free(path);
        return status;
    }
    ch_developer_http_message_clear(&result->message);
    result->message.status = (int)ch_json_number_value(
        ch_json_object_get(rule, "status"), 200.0);
    if (result->message.status == 0) result->message.status = 200;
    const ch_json_value *headers = ch_json_object_get(rule, "headers");
    for (size_t index = 0U; index < ch_json_object_size(headers); ++index) {
        const char *name = ch_json_object_key(headers, index);
        const char *value = ch_json_string_value(
            ch_json_object_value(headers, index));
        if (name != NULL && value != NULL &&
            !developer_header_add(&result->message, name, value)) {
            free(path);
            free(body);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy developer local map headers");
            return CH_ERROR_OUT_OF_MEMORY;
        }
    }
    if (developer_header_index(&result->message, "Content-Type") < 0 &&
        !developer_header_add(&result->message, "Content-Type",
                              developer_content_type(path, body,
                                                     body_length))) {
        free(path);
        free(body);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "add developer local map content type");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    bool okay = developer_body_set(&result->message, body, body_length);
    free(body);
    free(path);
    if (!okay) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy developer local map body");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    result->local_response = true;
    return CH_OK;
}

static ch_status developer_apply_map(
    const ch_json_value *rules, const ch_developer_http_message *request,
    ch_developer_http_result *result, ch_error *error) {
    for (size_t index = 0U; index < ch_json_array_size(rules); ++index) {
        const ch_json_value *rule = ch_json_array_get(rules, index);
        if (!developer_rule_matches(rule, request)) continue;
        result->matched = true;
        const char *identifier = developer_json_string(rule, "id");
        const char *name = developer_json_string(rule, "name");
        const char *kind = developer_json_string(rule, "kind");
        result->rule_id = ch_strdup(identifier == NULL ? "" : identifier);
        result->rule_name = ch_strdup(name == NULL ? "" : name);
        result->kind = ch_strdup(kind == NULL ? "" : kind);
        if (result->rule_id == NULL || result->rule_name == NULL ||
            result->kind == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy developer map result");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        if (kind != NULL && strcmp(kind, "local") == 0) {
            return developer_apply_local_map(rule, request, result, error);
        }
        if (kind != NULL && strcmp(kind, "remote") == 0) {
            result->remote_url = developer_remote_map_url(rule, request);
            if (result->remote_url == NULL ||
                !developer_message_update_url(&result->message,
                                              result->remote_url)) {
                ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                             "invalid developer remote map URL");
                return CH_ERROR_INVALID_ARGUMENT;
            }
        }
        return CH_OK;
    }
    return CH_OK;
}

static bool developer_append_http_headers(
    ch_json_buffer *json, const ch_developer_http_message *message,
    char *const *redact_headers, size_t redact_header_count,
    size_t value_limit) {
    if (!ch_json_append(json, "[")) return false;
    for (size_t index = 0U; index < message->header_count; ++index) {
        const char *name = message->headers[index].name == NULL ? "" :
                                                            message->headers[index].name;
        const char *value = message->headers[index].value == NULL ? "" :
                                                              message->headers[index].value;
        bool redacted = developer_name_in_list(name, redact_headers,
                                               redact_header_count);
        size_t length = strlen(value);
        bool truncated = !redacted && length > value_limit;
        char *bounded = NULL;
        if (truncated) {
            bounded = strndup(value, value_limit);
            if (bounded == NULL) return false;
            value = bounded;
        } else if (redacted) {
            value = "[redacted]";
        }
        bool okay = (index == 0U || ch_json_append(json, ",")) &&
            ch_json_append(json, "{\"name\":") &&
            ch_json_append_string(json, name) &&
            ch_json_append(json, ",\"value\":") &&
            ch_json_append_string(json, value) &&
            (!redacted || ch_json_append(json, ",\"redacted\":true")) &&
            (!truncated || ch_json_append(json, ",\"truncated\":true")) &&
            ch_json_append(json, "}");
        free(bounded);
        if (!okay) return false;
    }
    return ch_json_append(json, "]");
}

static bool developer_append_http_message(
    ch_json_buffer *json, const ch_developer_http_message *message,
    char *const *redact_headers, size_t redact_header_count,
    size_t value_limit, bool response) {
    if (!ch_json_append(json, "{")) return false;
    bool field = false;
#define APPEND_MESSAGE_FIELD(key, value) \
    do { \
        if ((value) != NULL && (value)[0] != '\0') { \
            if ((field && !ch_json_append(json, ",")) || \
                !ch_json_append(json, "\"" key "\":") || \
                !ch_json_append_string(json, (value))) return false; \
            field = true; \
        } \
    } while (0)
    if (!response) {
        APPEND_MESSAGE_FIELD("method", message->method);
        APPEND_MESSAGE_FIELD("url", message->url);
    }
#undef APPEND_MESSAGE_FIELD
    if (response && message->status != 0) {
        if ((field && !ch_json_append(json, ",")) ||
            !ch_json_append_format(json, "\"status\":%d", message->status)) {
            return false;
        }
        field = true;
    }
    if (message->headers != NULL) {
        if ((field && !ch_json_append(json, ",")) ||
            !ch_json_append(json, "\"headers\":") ||
            !developer_append_http_headers(
                json, message, redact_headers, redact_header_count,
                value_limit)) return false;
        field = true;
    }
    if (message->body_set || message->body_length > 0U) {
        char *body = malloc(message->body_length + 1U);
        if (body == NULL) return false;
        for (size_t index = 0U; index < message->body_length; ++index) {
            body[index] = message->body[index] == 0U ? '?' :
                (char)message->body[index];
        }
        body[message->body_length] = '\0';
        bool okay = (!field || ch_json_append(json, ",")) &&
            ch_json_append(json, "\"body\":") &&
            ch_json_append_string(json, body) &&
            ch_json_append(json, ",\"body_set\":true");
        free(body);
        if (!okay) return false;
    }
    return ch_json_append(json, "}");
}

static bool developer_http_message_from_json(
    ch_developer_http_message *message, const ch_json_value *edit,
    ch_error *error) {
    if (edit == NULL || ch_json_value_type(edit) == CH_JSON_NULL) return true;
    if (ch_json_value_type(edit) != CH_JSON_OBJECT) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "breakpoint message must be an object");
        return false;
    }
    const char *method = developer_json_string(edit, "method");
    const char *url = developer_json_string(edit, "url");
    if (method != NULL && method[0] != '\0') {
        char *copy = ch_strdup(method);
        if (copy == NULL) goto memory;
        free(message->method);
        message->method = copy;
    }
    if (url != NULL && url[0] != '\0' &&
        !developer_message_update_url(message, url)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "breakpoint URL is invalid");
        return false;
    }
    const ch_json_value *status = ch_json_object_get(edit, "status");
    if (status != NULL && ch_json_value_type(status) != CH_JSON_NULL) {
        int64_t value = 0;
        if (!ch_json_int64_value(status, &value) || value < 100 ||
            value > 599) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "breakpoint status must be between 100 and 599");
            return false;
        }
        message->status = (int)value;
    }
    const ch_json_value *headers = ch_json_object_get(edit, "headers");
    if (headers != NULL && ch_json_value_type(headers) != CH_JSON_NULL) {
        if (ch_json_value_type(headers) != CH_JSON_ARRAY) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "breakpoint headers must be an array");
            return false;
        }
        for (size_t index = 0U; index < message->header_count; ++index) {
            free(message->headers[index].name);
            free(message->headers[index].value);
        }
        free(message->headers);
        message->headers = NULL;
        message->header_count = 0U;
        for (size_t index = 0U; index < ch_json_array_size(headers); ++index) {
            const ch_json_value *header = ch_json_array_get(headers, index);
            const char *name = developer_json_string(header, "name");
            const char *value = developer_json_string(header, "value");
            if (name != NULL && name[0] != '\0' &&
                !developer_header_add(message, name,
                                      value == NULL ? "" : value)) {
                goto memory;
            }
        }
    }
    const ch_json_value *body = ch_json_object_get(edit, "body");
    bool body_set = ch_json_bool_value(ch_json_object_get(edit, "body_set"),
                                      false);
    if (body != NULL && ch_json_value_type(body) != CH_JSON_NULL) {
        const char *text = ch_json_string_value(body);
        if (text == NULL) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "breakpoint body must be a string");
            return false;
        }
        if (!developer_body_set(message, (const uint8_t *)text,
                                strlen(text))) goto memory;
    } else if (body_set && !developer_body_set(message, NULL, 0U)) {
        goto memory;
    }
    return true;
memory:
    ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                 "copy breakpoint message");
    return false;
}

static ch_status developer_breakpoint_wait(
    ch_developer_manager *manager, const ch_json_value *rules,
    const char *stage, const ch_developer_http_message *request,
    const ch_developer_http_message *response,
    ch_developer_http_message *mutable_message, bool *drop,
    ch_error *error) {
    const ch_json_value *matched = NULL;
    for (size_t index = 0U; index < ch_json_array_size(rules); ++index) {
        const ch_json_value *rule = ch_json_array_get(rules, index);
        if (developer_rule_matches(rule, request) &&
            developer_stage_matches(rule, stage)) {
            matched = rule;
            break;
        }
    }
    if (matched == NULL) return CH_OK;
    ch_developer_pending *pending = calloc(1U, sizeof(*pending));
    if (pending == NULL || pthread_cond_init(&pending->condition, NULL) != 0) {
        free(pending);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate developer breakpoint");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    const char *rule_id = developer_json_string(matched, "id");
    const char *rule_name = developer_json_string(matched, "name");
    pending->rule_identifier = ch_strdup(rule_id == NULL ? "" : rule_id);
    pending->rule_name = ch_strdup(rule_name == NULL ? "" : rule_name);
    pending->stage = ch_strdup(stage);
    pending->created_ns = developer_now_ns();
    if (pending->rule_identifier == NULL || pending->rule_name == NULL ||
        pending->stage == NULL ||
        !developer_http_message_copy(&pending->request, request) ||
        (response != NULL &&
         !developer_http_message_copy(&pending->response, response))) {
        developer_pending_destroy(pending);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy developer breakpoint");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    pending->has_response = response != NULL;
    pthread_mutex_lock(&manager->mutex);
    if (manager->destroying) {
        pthread_mutex_unlock(&manager->mutex);
        developer_pending_destroy(pending);
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "developer capture manager is stopping");
        return CH_ERROR_INVALID_STATE;
    }
    char identifier[64];
    (void)snprintf(identifier, sizeof(identifier), "bp-%" PRIu64,
                   ++manager->next_pending_identifier);
    pending->identifier = ch_strdup(identifier);
    if (pending->identifier == NULL) {
        pthread_mutex_unlock(&manager->mutex);
        developer_pending_destroy(pending);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy developer breakpoint identifier");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    pending->next = manager->pending;
    manager->pending = pending;
    struct timespec deadline;
    (void)clock_gettime(CLOCK_REALTIME, &deadline);
    deadline.tv_sec += 30;
    while (!pending->resolved) {
        int wait_status = pthread_cond_timedwait(
            &pending->condition, &manager->mutex, &deadline);
        if (wait_status == ETIMEDOUT) break;
    }
    if (!pending->resolved) {
        pending->action = ch_strdup("continue");
        pending->resolved = true;
    }
    *drop = pending->action != NULL &&
        strcasecmp(pending->action, "drop") == 0;
    if (!*drop) {
        ch_developer_http_message *edit = strcmp(stage, "request") == 0 ?
            &pending->edited_request : &pending->edited_response;
        bool has_edit = strcmp(stage, "request") == 0 ?
            pending->has_edited_request : pending->has_edited_response;
        if (has_edit) {
            ch_developer_http_message_clear(mutable_message);
            *mutable_message = *edit;
            memset(edit, 0, sizeof(*edit));
        }
    }
    ch_developer_pending **cursor = &manager->pending;
    while (*cursor != NULL && *cursor != pending) cursor = &(*cursor)->next;
    if (*cursor == pending) *cursor = pending->next;
    if (manager->pending == NULL) {
        pthread_cond_broadcast(&manager->pending_drained);
    }
    pthread_mutex_unlock(&manager->mutex);
    developer_pending_destroy(pending);
    return CH_OK;
}

char *ch_developer_pending_breakpoints_json(ch_developer_manager *manager,
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
    bool okay = ch_json_append(&json, "{\"breakpoints\":[");
    size_t index = 0U;
    for (ch_developer_pending *pending = manager->pending;
         okay && pending != NULL; pending = pending->next, ++index) {
        okay = (index == 0U || ch_json_append(&json, ",")) &&
            ch_json_append(&json, "{\"id\":") &&
            ch_json_append_string(&json, pending->identifier) &&
            ch_json_append(&json, ",\"rule_id\":") &&
            ch_json_append_string(&json, pending->rule_identifier) &&
            (pending->rule_name[0] == '\0' ||
             (ch_json_append(&json, ",\"rule_name\":") &&
              ch_json_append_string(&json, pending->rule_name))) &&
            ch_json_append(&json, ",\"stage\":") &&
            ch_json_append_string(&json, pending->stage) &&
            ch_json_append(&json, ",\"created_at\":") &&
            developer_append_timestamp(&json, pending->created_ns) &&
            ch_json_append(&json, ",\"request\":") &&
            developer_append_http_message(
                &json, &pending->request, manager->redact_headers,
                manager->redact_header_count, manager->header_value_limit,
                false) &&
            (!pending->has_response ||
             (ch_json_append(&json, ",\"response\":") &&
              developer_append_http_message(
                  &json, &pending->response, manager->redact_headers,
                  manager->redact_header_count,
                  manager->header_value_limit, true))) &&
            ch_json_append(&json, "}");
    }
    okay = okay && ch_json_append(&json, "]}");
    pthread_mutex_unlock(&manager->mutex);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode pending developer breakpoints");
    }
    return result;
}

char *ch_developer_resolve_breakpoint_json(ch_developer_manager *manager,
                                            const char *identifier,
                                            const char *request_json,
                                            ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL || identifier == NULL || identifier[0] == '\0') {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer breakpoint identifier is required");
        return NULL;
    }
    const char *payload = request_json == NULL ? "{}" : request_json;
    ch_json_value *request = ch_json_parse(payload, strlen(payload), error);
    if (request == NULL) return NULL;
    if (ch_json_value_type(request) != CH_JSON_OBJECT) {
        ch_json_value_destroy(request);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "breakpoint resolution must be an object");
        return NULL;
    }
    const char *action = developer_json_string(request, "action");
    if (action == NULL || action[0] == '\0') action = "continue";
    if (strcasecmp(action, "continue") != 0 &&
        strcasecmp(action, "drop") != 0) {
        ch_json_value_destroy(request);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "breakpoint action must be continue or drop");
        return NULL;
    }
    pthread_mutex_lock(&manager->mutex);
    ch_developer_pending *pending = manager->pending;
    while (pending != NULL && strcmp(pending->identifier, identifier) != 0) {
        pending = pending->next;
    }
    if (pending == NULL || pending->resolved) {
        pthread_mutex_unlock(&manager->mutex);
        ch_json_value_destroy(request);
        ch_error_set(error, CH_ERROR_NOT_FOUND, "breakpoint not found");
        return NULL;
    }
    ch_developer_http_message_clear(&pending->edited_request);
    ch_developer_http_message_clear(&pending->edited_response);
    pending->has_edited_request = false;
    pending->has_edited_response = false;
    bool okay = developer_http_message_copy(
        &pending->edited_request, &pending->request) &&
        (!pending->has_response || developer_http_message_copy(
            &pending->edited_response, &pending->response));
    if (okay) {
        okay = developer_http_message_from_json(
            &pending->edited_request, ch_json_object_get(request, "request"),
            error);
    }
    if (okay && pending->has_response) {
        okay = developer_http_message_from_json(
            &pending->edited_response,
            ch_json_object_get(request, "response"), error);
    }
    if (okay) {
        pending->has_edited_request =
            ch_json_object_get(request, "request") != NULL;
        pending->has_edited_response = pending->has_response &&
            ch_json_object_get(request, "response") != NULL;
        pending->action = ch_strdup(action);
        okay = pending->action != NULL;
    }
    if (okay) {
        pending->resolved = true;
        pthread_cond_signal(&pending->condition);
    }
    pthread_mutex_unlock(&manager->mutex);
    ch_json_value_destroy(request);
    if (!okay) {
        if (error->code == CH_OK) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "resolve developer breakpoint");
        }
        return NULL;
    }
    return ch_strdup("{\"resolved\":true}");
}

ch_status ch_developer_process_request(
    ch_developer_manager *manager,
    const ch_developer_http_message *request,
    ch_developer_http_result *result,
    ch_error *error) {
    ch_error_clear(error);
    if (request == NULL || result == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer request and result are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    memset(result, 0, sizeof(*result));
    if (!developer_http_message_copy(&result->message, request)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy developer HTTP request");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (manager == NULL) return CH_OK;
    pthread_mutex_lock(&manager->mutex);
    bool enabled = manager->enabled;
    bool no_cache = manager->enabled && manager->no_cache_enabled;
    ch_json_value *rewrite_rules = ch_json_value_clone(
        manager->rewrite_rules);
    ch_json_value *breakpoint_rules = ch_json_value_clone(
        manager->breakpoint_rules);
    ch_json_value *map_rules = ch_json_value_clone(manager->map_rules);
    pthread_mutex_unlock(&manager->mutex);
    if (!enabled) {
        ch_json_value_destroy(rewrite_rules);
        ch_json_value_destroy(breakpoint_rules);
        ch_json_value_destroy(map_rules);
        return CH_OK;
    }
    if (rewrite_rules == NULL || breakpoint_rules == NULL ||
        map_rules == NULL) {
        ch_json_value_destroy(rewrite_rules);
        ch_json_value_destroy(breakpoint_rules);
        ch_json_value_destroy(map_rules);
        ch_developer_http_result_clear(result);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy developer HTTP rules");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (!developer_apply_rewrites(rewrite_rules, "request", request,
                                  &result->message)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "apply developer request rewrite");
        goto failed;
    }
    if (developer_breakpoint_wait(
            manager, breakpoint_rules, "request", &result->message, NULL,
            &result->message, &result->drop, error) != CH_OK) goto failed;
    if (!result->drop && no_cache) {
        if (!developer_apply_no_cache_request(&result->message)) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "apply developer request cache policy");
            goto failed;
        }
    }
    if (!result->drop && developer_apply_map(
            map_rules, &result->message, result, error) != CH_OK) goto failed;
    if (!result->drop && result->local_response && no_cache &&
        !developer_apply_no_cache_response(&result->message)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "apply developer local response cache policy");
        goto failed;
    }
    ch_json_value_destroy(rewrite_rules);
    ch_json_value_destroy(breakpoint_rules);
    ch_json_value_destroy(map_rules);
    return CH_OK;
failed:
    ch_json_value_destroy(rewrite_rules);
    ch_json_value_destroy(breakpoint_rules);
    ch_json_value_destroy(map_rules);
    ch_developer_http_result_clear(result);
    return error->code;
}

ch_status ch_developer_process_response(
    ch_developer_manager *manager,
    const ch_developer_http_message *request,
    const ch_developer_http_message *response,
    ch_developer_http_result *result,
    ch_error *error) {
    ch_error_clear(error);
    if (request == NULL || response == NULL || result == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer request, response, and result are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    memset(result, 0, sizeof(*result));
    if (!developer_http_message_copy(&result->message, response)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy developer HTTP response");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (manager == NULL) return CH_OK;
    pthread_mutex_lock(&manager->mutex);
    bool enabled = manager->enabled;
    bool no_cache = manager->enabled && manager->no_cache_enabled;
    ch_json_value *rewrite_rules = ch_json_value_clone(
        manager->rewrite_rules);
    ch_json_value *breakpoint_rules = ch_json_value_clone(
        manager->breakpoint_rules);
    pthread_mutex_unlock(&manager->mutex);
    if (!enabled) {
        ch_json_value_destroy(rewrite_rules);
        ch_json_value_destroy(breakpoint_rules);
        return CH_OK;
    }
    if (rewrite_rules == NULL || breakpoint_rules == NULL) {
        ch_json_value_destroy(rewrite_rules);
        ch_json_value_destroy(breakpoint_rules);
        ch_developer_http_result_clear(result);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy developer HTTP rules");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (!developer_apply_rewrites(rewrite_rules, "response", request,
                                  &result->message)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "apply developer response rewrite");
        goto failed;
    }
    if (developer_breakpoint_wait(
            manager, breakpoint_rules, "response", request,
            &result->message, &result->message, &result->drop,
            error) != CH_OK) goto failed;
    if (!result->drop && no_cache &&
        !developer_apply_no_cache_response(&result->message)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "apply developer response cache policy");
        goto failed;
    }
    ch_json_value_destroy(rewrite_rules);
    ch_json_value_destroy(breakpoint_rules);
    return CH_OK;
failed:
    ch_json_value_destroy(rewrite_rules);
    ch_json_value_destroy(breakpoint_rules);
    ch_developer_http_result_clear(result);
    return error->code;
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

typedef struct developer_outgoing_request {
    char *entry_id;
    char *method;
    char *url;
    char *body;
    bool body_set;
    ch_developer_header *headers;
    size_t header_count;
} developer_outgoing_request;

typedef struct developer_http_response {
    ch_developer_capture *capture;
    ch_json_buffer header_block;
    char *location;
    long status;
    bool response_started;
} developer_http_response;

static pthread_once_t developer_curl_once = PTHREAD_ONCE_INIT;
static CURLcode developer_curl_init_status = CURLE_OK;

static void developer_curl_initialize(void) {
    developer_curl_init_status = curl_global_init(CURL_GLOBAL_DEFAULT);
}

static void developer_outgoing_clear(developer_outgoing_request *request) {
    if (request == NULL) return;
    free(request->entry_id);
    free(request->method);
    free(request->url);
    free(request->body);
    developer_headers_clear(request->headers, request->header_count);
    memset(request, 0, sizeof(*request));
}

static char *developer_trim_copy(const char *value) {
    const char *start = value == NULL ? "" : value;
    while (*start != '\0' && isspace((unsigned char)*start) != 0) ++start;
    const char *end = start + strlen(start);
    while (end > start && isspace((unsigned char)end[-1]) != 0) --end;
    size_t length = (size_t)(end - start);
    char *copy = malloc(length + 1U);
    if (copy == NULL) return NULL;
    memcpy(copy, start, length);
    copy[length] = '\0';
    return copy;
}

static bool developer_token_valid(const char *token, size_t maximum) {
    static const char separators[] = "()<>@,;:\\\"/[]?={} \t";
    size_t length = token == NULL ? 0U : strlen(token);
    if (length == 0U || length > maximum) return false;
    for (size_t index = 0U; index < length; ++index) {
        unsigned char value = (unsigned char)token[index];
        if (value <= 0x20U || value >= 0x7fU ||
            strchr(separators, (int)value) != NULL) return false;
    }
    return true;
}

static bool developer_method_valid(const char *method) {
    return developer_token_valid(method, 32U);
}

static bool developer_header_valid(const char *name, const char *value) {
    if (!developer_token_valid(name, 256U) ||
        value == NULL || strlen(value) > CH_DEVELOPER_MAX_HEADER_BYTES) {
        return false;
    }
    for (const unsigned char *cursor = (const unsigned char *)value;
         *cursor != 0U; ++cursor) {
        if (*cursor == '\r' || *cursor == '\n' || *cursor == 0x7fU ||
            (*cursor < 0x20U && *cursor != '\t')) return false;
    }
    return strcasecmp(name, "Host") != 0 &&
        strcasecmp(name, "Content-Length") != 0 &&
        strcasecmp(name, "Transfer-Encoding") != 0 &&
        strcasecmp(name, "Connection") != 0 &&
        strcasecmp(name, "Proxy-Connection") != 0;
}

static bool developer_copy_json_string(char **target,
                                       const ch_json_value *object,
                                       const char *key, bool optional,
                                       ch_error *error) {
    const ch_json_value *value = ch_json_object_get(object, key);
    if (value == NULL || ch_json_value_type(value) == CH_JSON_NULL) {
        if (optional) return true;
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer request %s is required", key);
        return false;
    }
    const char *text = ch_json_string_value(value);
    if (text == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer request %s must be a string", key);
        return false;
    }
    *target = ch_strdup(text);
    if (*target == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy developer request %s", key);
        return false;
    }
    return true;
}

static bool developer_parse_outgoing(const char *request_json, bool repeat,
                                     developer_outgoing_request *out,
                                     ch_error *error) {
    memset(out, 0, sizeof(*out));
    const char *document = request_json == NULL || request_json[0] == '\0' ?
        "{}" : request_json;
    ch_json_value *root = ch_json_parse(document, strlen(document), error);
    if (root == NULL) return false;
    bool okay = ch_json_value_type(root) == CH_JSON_OBJECT;
    if (!okay) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer request must be a JSON object");
    }
    if (okay && repeat) {
        okay = developer_copy_json_string(&out->entry_id, root, "entry_id",
                                          false, error);
    }
    if (okay) {
        okay = developer_copy_json_string(&out->method, root, "method", true,
                                          error) &&
            developer_copy_json_string(&out->url, root, "url", true, error);
    }
    const ch_json_value *body = okay ? ch_json_object_get(root, "body") : NULL;
    if (okay && body != NULL && ch_json_value_type(body) != CH_JSON_NULL) {
        const char *text = ch_json_string_value(body);
        if (text == NULL) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "developer request body must be a string or null");
            okay = false;
        } else {
            out->body = ch_strdup(text);
            out->body_set = true;
            if (out->body == NULL) {
                ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                             "copy developer request body");
                okay = false;
            }
        }
    }
    const ch_json_value *headers = okay ?
        ch_json_object_get(root, "headers") : NULL;
    if (okay && headers != NULL &&
        ch_json_value_type(headers) != CH_JSON_NULL) {
        if (ch_json_value_type(headers) != CH_JSON_ARRAY ||
            ch_json_array_size(headers) > 1024U) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "developer request headers must be a bounded array");
            okay = false;
        }
    }
    size_t count = okay ? ch_json_array_size(headers) : 0U;
    if (okay && count > 0U) {
        out->headers = calloc(count, sizeof(*out->headers));
        if (out->headers == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "allocate developer request headers");
            okay = false;
        }
    }
    for (size_t index = 0U; okay && index < count; ++index) {
        const ch_json_value *header = ch_json_array_get(headers, index);
        const char *name = ch_json_string_value(
            ch_json_object_get(header, "name"));
        const char *value = ch_json_string_value(
            ch_json_object_get(header, "value"));
        if (ch_json_value_type(header) != CH_JSON_OBJECT ||
            !developer_header_valid(name, value)) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "developer request header %zu is invalid or restricted",
                         index);
            okay = false;
            break;
        }
        out->headers[index].name = ch_strdup(name);
        out->headers[index].value = ch_strdup(value);
        out->header_count = index + 1U;
        if (out->headers[index].name == NULL ||
            out->headers[index].value == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy developer request header");
            okay = false;
            break;
        }
    }
    ch_json_value_destroy(root);
    if (!okay) developer_outgoing_clear(out);
    return okay;
}

static bool developer_outgoing_set_default(char **target,
                                           const char *fallback,
                                           ch_error *error) {
    if (*target != NULL) {
        char *trimmed = developer_trim_copy(*target);
        free(*target);
        *target = trimmed;
    } else {
        *target = ch_strdup(fallback == NULL ? "" : fallback);
    }
    if (*target == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy developer request field");
        return false;
    }
    if ((*target)[0] == '\0' && fallback != NULL && fallback[0] != '\0') {
        free(*target);
        *target = ch_strdup(fallback);
        if (*target == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy developer request default");
            return false;
        }
    }
    return true;
}

static bool developer_copy_entry_request(ch_developer_manager *manager,
                                         developer_outgoing_request *request,
                                         ch_error *error) {
    pthread_mutex_lock(&manager->mutex);
    ch_developer_entry *entry = developer_find_entry_locked(
        manager, request->entry_id == NULL ? "" : request->entry_id);
    if (entry == NULL) {
        pthread_mutex_unlock(&manager->mutex);
        ch_error_set(error, CH_ERROR_NOT_FOUND, "developer entry not found");
        return false;
    }
    bool okay = true;
    if (request->method == NULL || request->method[0] == '\0') {
        free(request->method);
        request->method = ch_strdup(entry->method);
        okay = request->method != NULL;
    }
    if (okay && (request->url == NULL || request->url[0] == '\0')) {
        free(request->url);
        request->url = ch_strdup(entry->url);
        okay = request->url != NULL;
    }
    if (okay && !request->body_set) {
        if (entry->request_body.size >
            (uint64_t)entry->request_body.preview_length) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "captured request body is truncated; provide an override body");
            okay = false;
        } else if (!developer_utf8_valid(entry->request_body.preview,
                                         entry->request_body.preview_length)) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "captured request body is binary; provide an override body");
            okay = false;
        } else {
            request->body = malloc(entry->request_body.preview_length + 1U);
            if (request->body != NULL) {
                memcpy(request->body, entry->request_body.preview,
                       entry->request_body.preview_length);
                request->body[entry->request_body.preview_length] = '\0';
                request->body_set = true;
            } else {
                okay = false;
            }
        }
    }
    if (okay && request->header_count == 0U) {
        size_t count = 0U;
        for (size_t index = 0U; index < entry->request_header_count; ++index) {
            if (!entry->request_headers[index].redacted &&
                developer_header_valid(entry->request_headers[index].name,
                                       entry->request_headers[index].value)) {
                ++count;
            }
        }
        request->headers = calloc(count, sizeof(*request->headers));
        if (request->headers == NULL && count > 0U) okay = false;
        for (size_t index = 0U, output = 0U;
             okay && index < entry->request_header_count; ++index) {
            ch_developer_header *source = &entry->request_headers[index];
            if (source->redacted ||
                !developer_header_valid(source->name, source->value)) continue;
            request->headers[output].name = ch_strdup(source->name);
            request->headers[output].value = ch_strdup(source->value);
            request->header_count = output + 1U;
            if (request->headers[output].name == NULL ||
                request->headers[output].value == NULL) {
                okay = false;
                break;
            }
            ++output;
        }
    }
    pthread_mutex_unlock(&manager->mutex);
    if (!okay && error->code == CH_OK) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy captured developer request");
    }
    return okay;
}

static bool developer_http_is_redirect(long status) {
    return status == 301L || status == 302L || status == 303L ||
        status == 307L || status == 308L;
}

static void developer_http_response_reset(developer_http_response *response) {
    ch_json_dispose(&response->header_block);
    ch_json_init(&response->header_block);
    free(response->location);
    response->location = NULL;
    response->status = 0L;
    response->response_started = false;
}

static bool developer_http_set_location(developer_http_response *response,
                                        const char *value, size_t length) {
    while (length > 0U && (value[length - 1U] == '\r' ||
                           value[length - 1U] == '\n' ||
                           isspace((unsigned char)value[length - 1U]) != 0)) {
        --length;
    }
    while (length > 0U && isspace((unsigned char)*value) != 0) {
        ++value;
        --length;
    }
    char *copy = malloc(length + 1U);
    if (copy == NULL) return false;
    memcpy(copy, value, length);
    copy[length] = '\0';
    free(response->location);
    response->location = copy;
    return true;
}

static size_t developer_http_header(char *data, size_t size, size_t count,
                                    void *context) {
    developer_http_response *response = context;
    if (size != 0U && count > SIZE_MAX / size) return 0U;
    size_t length = size * count;
    if (length >= 5U && strncasecmp(data, "HTTP/", 5U) == 0) {
        ch_json_dispose(&response->header_block);
        ch_json_init(&response->header_block);
        response->status = 0L;
        char line[128];
        size_t copied = length < sizeof(line) - 1U ? length : sizeof(line) - 1U;
        memcpy(line, data, copied);
        line[copied] = '\0';
        (void)sscanf(line, "HTTP/%*s %ld", &response->status);
    }
    if (!ch_json_append_bytes(&response->header_block, data, length)) return 0U;
    const char *colon = memchr(data, ':', length);
    if (colon != NULL && (size_t)(colon - data) == 8U &&
        strncasecmp(data, "Location", 8U) == 0 &&
        !developer_http_set_location(response, colon + 1U,
                                     length - (size_t)(colon - data) - 1U)) {
        return 0U;
    }
    bool blank = (length == 2U && data[0] == '\r' && data[1] == '\n') ||
        (length == 1U && data[0] == '\n');
    if (blank && response->status >= 200L &&
        !developer_http_is_redirect(response->status)) {
        ch_developer_capture_response(
            response->capture, (const uint8_t *)response->header_block.data,
            response->header_block.length);
        response->response_started = true;
    }
    return length;
}

static size_t developer_http_write(char *data, size_t size, size_t count,
                                   void *context) {
    developer_http_response *response = context;
    if (size != 0U && count > SIZE_MAX / size) return 0U;
    size_t length = size * count;
    if (response->response_started) {
        ch_developer_capture_response(response->capture,
                                      (const uint8_t *)data, length);
    }
    return length;
}

static bool developer_add_curl_headers(const developer_outgoing_request *request,
                                       struct curl_slist **out,
                                       ch_json_buffer *raw,
                                       ch_error *error) {
    for (size_t index = 0U; index < request->header_count; ++index) {
        const ch_developer_header *header = &request->headers[index];
        int length = snprintf(NULL, 0, "%s: %s", header->name, header->value);
        char *line = length < 0 ? NULL : malloc((size_t)length + 1U);
        if (line == NULL) goto out_of_memory;
        (void)snprintf(line, (size_t)length + 1U, "%s: %s", header->name,
                       header->value);
        struct curl_slist *grown = curl_slist_append(*out, line);
        bool appended = ch_json_append(raw, line) && ch_json_append(raw, "\r\n");
        free(line);
        if (grown == NULL) goto out_of_memory;
        *out = grown;
        if (!appended) goto out_of_memory;
    }
    return true;

out_of_memory:
    ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                 "allocate developer HTTP headers");
    return false;
}

static char *developer_entry_wrapper_json(ch_developer_manager *manager,
                                          const char *identifier,
                                          ch_error *error) {
    pthread_mutex_lock(&manager->mutex);
    ch_developer_entry *entry = developer_find_entry_locked(manager, identifier);
    ch_json_buffer json;
    ch_json_init(&json);
    bool okay = entry != NULL && ch_json_append(&json, "{\"entry\":") &&
        developer_append_entry(&json, entry) && ch_json_append(&json, "}");
    pthread_mutex_unlock(&manager->mutex);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, entry == NULL ? CH_ERROR_NOT_FOUND :
                     CH_ERROR_OUT_OF_MEMORY,
                     entry == NULL ? "developer entry not found" :
                     "encode developer request result");
    }
    return result;
}

static char *developer_send_outgoing(ch_developer_manager *manager,
                                     developer_outgoing_request *request,
                                     ch_error *error) {
    if (!developer_outgoing_set_default(&request->method, "GET", error) ||
        !developer_outgoing_set_default(&request->url, "", error)) return NULL;
    if (!developer_method_valid(request->method)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer request method is invalid");
        return NULL;
    }
    if (request->url[0] == '\0') {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer request URL is required");
        return NULL;
    }
    if (request->body == NULL) {
        request->body = ch_strdup("");
        if (request->body == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "allocate developer request body");
            return NULL;
        }
    }
    pthread_mutex_lock(&manager->mutex);
    bool enabled = manager->enabled;
    pthread_mutex_unlock(&manager->mutex);
    if (!enabled) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "developer capture disabled");
        return NULL;
    }
    (void)pthread_once(&developer_curl_once, developer_curl_initialize);
    if (developer_curl_init_status != CURLE_OK) {
        ch_error_set(error, CH_ERROR_IO, "initialize developer HTTP client: %s",
                     curl_easy_strerror(developer_curl_init_status));
        return NULL;
    }
    ch_http_endpoint initial = {0};
    ch_http_endpoint endpoint = {0};
    ch_status status = ch_http_endpoint_prepare(
        request->url, "developer request", &initial, error);
    if (status == CH_OK) {
        status = ch_http_endpoint_prepare(request->url, "developer request",
                                          &endpoint, error);
    }
    if (status != CH_OK) {
        ch_http_endpoint_clear(&initial);
        return NULL;
    }
    struct curl_slist *headers = NULL;
    ch_json_buffer raw_headers;
    ch_json_init(&raw_headers);
    if (!developer_add_curl_headers(request, &headers, &raw_headers, error)) {
        curl_slist_free_all(headers);
        ch_json_dispose(&raw_headers);
        ch_http_endpoint_clear(&endpoint);
        ch_http_endpoint_clear(&initial);
        return NULL;
    }
    ch_developer_capture_metadata metadata = {
        .flow_id = 0U,
        .profile = "",
        .client_address = "",
        .chain_name = "repeat",
        .method = request->method,
        .url = endpoint.url,
        .scheme = endpoint.scheme,
        .host = endpoint.host,
        .request_headers = raw_headers.data,
        .request_headers_length = raw_headers.length
    };
    ch_developer_capture *capture = ch_developer_capture_begin(
        manager, &metadata, error);
    if (capture == NULL) {
        if (error->code == CH_OK) {
            ch_error_set(error, CH_ERROR_INVALID_STATE,
                         "developer capture disabled");
        }
        curl_slist_free_all(headers);
        ch_json_dispose(&raw_headers);
        ch_http_endpoint_clear(&endpoint);
        ch_http_endpoint_clear(&initial);
        return NULL;
    }
    ch_developer_capture_request_body(capture,
        (const uint8_t *)request->body, strlen(request->body));
    char *identifier = ch_strdup(capture->entry->identifier);
    developer_http_response response = {.capture = capture};
    ch_json_init(&response.header_block);
    char *current_method = ch_strdup(request->method);
    const char *current_body = request->body;
    char curl_error[CURL_ERROR_SIZE] = {0};
    ch_status request_status = identifier == NULL || current_method == NULL ?
        CH_ERROR_OUT_OF_MEMORY : CH_OK;
    const char *capture_error = NULL;
    struct timespec deadline_start;
    (void)clock_gettime(CLOCK_MONOTONIC, &deadline_start);
    for (unsigned redirect = 0U; request_status == CH_OK; ++redirect) {
        developer_http_response_reset(&response);
        curl_error[0] = '\0';
        CURL *curl = curl_easy_init();
        struct curl_slist *resolve = NULL;
        if (curl != NULL) resolve = curl_slist_append(resolve, endpoint.resolve);
        CURLcode code = curl != NULL && resolve != NULL ? CURLE_OK :
            CURLE_OUT_OF_MEMORY;
        struct timespec now;
        (void)clock_gettime(CLOCK_MONOTONIC, &now);
        int64_t elapsed_ms = (int64_t)(now.tv_sec - deadline_start.tv_sec) *
            INT64_C(1000) + (int64_t)(now.tv_nsec - deadline_start.tv_nsec) /
            INT64_C(1000000);
        long remaining_ms = elapsed_ms >= INT64_C(30000) ? 1L :
            (long)(INT64_C(30000) - elapsed_ms);
#define DEVELOPER_CURL_SET(option, value) do { \
    if (code == CURLE_OK) code = curl_easy_setopt(curl, (option), (value)); \
} while (0)
        DEVELOPER_CURL_SET(CURLOPT_URL, endpoint.url);
        DEVELOPER_CURL_SET(CURLOPT_HTTPHEADER, headers);
        DEVELOPER_CURL_SET(CURLOPT_RESOLVE, resolve);
        DEVELOPER_CURL_SET(CURLOPT_PROXY, "");
#if LIBCURL_VERSION_NUM >= 0x075500
        DEVELOPER_CURL_SET(CURLOPT_PROTOCOLS_STR, "http,https");
#else
        DEVELOPER_CURL_SET(CURLOPT_PROTOCOLS,
                           (long)(CURLPROTO_HTTP | CURLPROTO_HTTPS));
#endif
        DEVELOPER_CURL_SET(CURLOPT_FOLLOWLOCATION, 0L);
        DEVELOPER_CURL_SET(CURLOPT_TIMEOUT_MS, remaining_ms);
        DEVELOPER_CURL_SET(CURLOPT_CONNECTTIMEOUT_MS, remaining_ms);
        DEVELOPER_CURL_SET(CURLOPT_NOSIGNAL, 1L);
        DEVELOPER_CURL_SET(CURLOPT_FRESH_CONNECT, 1L);
        DEVELOPER_CURL_SET(CURLOPT_FORBID_REUSE, 1L);
        DEVELOPER_CURL_SET(CURLOPT_DNS_CACHE_TIMEOUT, 0L);
        DEVELOPER_CURL_SET(CURLOPT_USERAGENT, "clambhook/1");
        DEVELOPER_CURL_SET(CURLOPT_HEADERFUNCTION, developer_http_header);
        DEVELOPER_CURL_SET(CURLOPT_HEADERDATA, &response);
        DEVELOPER_CURL_SET(CURLOPT_WRITEFUNCTION, developer_http_write);
        DEVELOPER_CURL_SET(CURLOPT_WRITEDATA, &response);
        DEVELOPER_CURL_SET(CURLOPT_ERRORBUFFER, curl_error);
        if (current_body[0] != '\0') {
            DEVELOPER_CURL_SET(CURLOPT_POSTFIELDS, current_body);
            DEVELOPER_CURL_SET(CURLOPT_POSTFIELDSIZE_LARGE,
                               (curl_off_t)strlen(current_body));
        }
        DEVELOPER_CURL_SET(CURLOPT_CUSTOMREQUEST, current_method);
#undef DEVELOPER_CURL_SET
        if (code == CURLE_OK) code = curl_easy_perform(curl);
        long http_status = 0L;
        if (code == CURLE_OK) {
            code = curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE,
                                     &http_status);
        }
        curl_slist_free_all(resolve);
        curl_easy_cleanup(curl);
        if (code != CURLE_OK) {
            capture_error = curl_error[0] == '\0' ? curl_easy_strerror(code) :
                                                    curl_error;
            break;
        }
        if (!developer_http_is_redirect(http_status)) break;
        if (redirect >= 9U) {
            capture_error = "stopped after 10 redirects";
            break;
        }
        if (response.location == NULL || response.location[0] == '\0') {
            capture_error = "redirect has no location";
            break;
        }
        char *next_url = ch_http_resolve_redirect(endpoint.url,
                                                  response.location);
        ch_http_endpoint next = {0};
        request_status = next_url == NULL ? CH_ERROR_INVALID_ARGUMENT :
            ch_http_endpoint_prepare(next_url, "developer redirect", &next,
                                     error);
        curl_free(next_url);
        if (request_status == CH_OK &&
            !ch_http_endpoint_same_origin(&initial, &next)) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "developer redirect to a different origin is not allowed");
            request_status = CH_ERROR_INVALID_ARGUMENT;
        }
        if (request_status != CH_OK) {
            capture_error = error->message;
            ch_http_endpoint_clear(&next);
            break;
        }
        ch_http_endpoint_clear(&endpoint);
        endpoint = next;
        if ((http_status == 301L || http_status == 302L ||
             http_status == 303L) && strcasecmp(current_method, "GET") != 0 &&
            strcasecmp(current_method, "HEAD") != 0) {
            free(current_method);
            current_method = ch_strdup("GET");
            current_body = "";
            if (current_method == NULL) request_status = CH_ERROR_OUT_OF_MEMORY;
        }
    }
    if (request_status == CH_ERROR_OUT_OF_MEMORY && error->code == CH_OK) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate developer HTTP request");
        capture_error = error->message;
    }
    char captured_error[sizeof(error->message)];
    captured_error[0] = '\0';
    if (capture_error != NULL) {
        (void)snprintf(captured_error, sizeof(captured_error), "%s",
                       capture_error);
    }
    ch_developer_capture_finish(capture,
        captured_error[0] == '\0' ? NULL : captured_error);
    developer_http_response_reset(&response);
    ch_json_dispose(&response.header_block);
    free(current_method);
    curl_slist_free_all(headers);
    ch_json_dispose(&raw_headers);
    ch_http_endpoint_clear(&endpoint);
    ch_http_endpoint_clear(&initial);
    if (identifier == NULL) {
        free(identifier);
        return NULL;
    }
    /* Transport and redirect errors are captured in the returned entry, just
     * like an HTTP error status. Only malformed/unsafe requests fail the API. */
    ch_error_clear(error);
    char *result = developer_entry_wrapper_json(manager, identifier, error);
    free(identifier);
    return result;
}

char *ch_developer_send_json(ch_developer_manager *manager,
                             const char *request_json,
                             ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer capture manager is required");
        return NULL;
    }
    developer_outgoing_request request;
    if (!developer_parse_outgoing(request_json, false, &request, error)) {
        return NULL;
    }
    char *result = developer_send_outgoing(manager, &request, error);
    developer_outgoing_clear(&request);
    return result;
}

char *ch_developer_repeat_json(ch_developer_manager *manager,
                               const char *request_json,
                               ch_error *error) {
    ch_error_clear(error);
    if (manager == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "developer capture manager is required");
        return NULL;
    }
    developer_outgoing_request request;
    if (!developer_parse_outgoing(request_json, true, &request, error)) {
        return NULL;
    }
    char *result = NULL;
    if (developer_copy_entry_request(manager, &request, error)) {
        result = developer_send_outgoing(manager, &request, error);
    }
    developer_outgoing_clear(&request);
    return result;
}
