// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "api_server.h"

#include <arpa/inet.h>
#include <ctype.h>
#include <errno.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>

#include <llhttp.h>
#include <openssl/evp.h>
#include <sodium.h>

#include "clambhook/json.h"
#include "clambhook/license_json.h"
#include "internal.h"

#define CH_API_MAX_REQUEST_BYTES (1024U * 1024U)
#define CH_API_MAX_CONFIG_TRANSFER_BYTES (4U * 1024U * 1024U)
#define CH_API_MAX_LICENSE_BYTES (8U * 1024U * 1024U)
#define CH_API_LICENSE_CACHE_MILLISECONDS UINT64_C(10000)

typedef struct ch_api_client ch_api_client;

struct ch_api_server {
    uv_loop_t *loop;
    ch_runtime *runtime;
    uv_tcp_t listener;
    char *auth_token;
    char *license_path;
    uint64_t license_cache_expires;
    int license_cache_valid;
    int license_cache_allowed;
    char *address;
    char bind_host[INET6_ADDRSTRLEN];
    int bind_port;
    int wildcard_bind;
    ch_api_client *clients;
    size_t client_count;
    int closing;
    int listener_closed;
};

struct ch_api_client {
    uv_tcp_t stream;
    ch_api_server *server;
    llhttp_t parser;
    llhttp_settings_t settings;
    ch_json_buffer url;
    ch_json_buffer field;
    ch_json_buffer value;
    ch_json_buffer body;
    char *authorization;
    char *host;
    char *origin;
    char *connection;
    char *upgrade;
    char *websocket_key;
    char *websocket_version;
    int responded;
    int websocket;
    int websocket_initialized;
    int websocket_write_pending;
    uint64_t websocket_next_sequence;
    uint64_t websocket_ticks;
    char *websocket_filters;
    uv_timer_t websocket_timer;
    int websocket_timer_initialized;
    int websocket_timer_closed;
    int stream_closed;
    int unlinked;
    ch_api_client *next;
};

typedef struct ch_api_write {
    uv_write_t request;
    char *bytes;
    int close_after;
} ch_api_write;

static int ch_ascii_equal(const char *left, const char *right) {
    return left != NULL && right != NULL && strcasecmp(left, right) == 0;
}

static int ch_parse_port(const char *text, int *port) {
    if (text == NULL || text[0] == '\0') return 0;
    unsigned value = 0U;
    for (const unsigned char *cursor = (const unsigned char *)text; *cursor != 0U; ++cursor) {
        if (!isdigit(*cursor)) return 0;
        if (value > 6553U || (value == 6553U && *cursor > (unsigned char)'5')) return 0;
        value = value * 10U + (unsigned)(*cursor - (unsigned char)'0');
    }
    *port = (int)value;
    return 1;
}

static int ch_split_authority(
    const char *authority,
    char host[INET6_ADDRSTRLEN],
    int *port,
    int *has_port
) {
    if (authority == NULL || authority[0] == '\0') return 0;
    const char *host_start = authority;
    const char *host_end = NULL;
    const char *port_text = NULL;
    if (authority[0] == '[') {
        host_start = authority + 1;
        host_end = strchr(host_start, ']');
        if (host_end == NULL) return 0;
        if (host_end[1] == ':') port_text = host_end + 2;
        else if (host_end[1] != '\0') return 0;
    } else {
        const char *separator = strchr(authority, ':');
        if (separator != NULL) {
            if (strchr(separator + 1, ':') != NULL) return 0;
            host_end = separator;
            port_text = separator + 1;
        } else {
            host_end = authority + strlen(authority);
        }
    }
    size_t length = (size_t)(host_end - host_start);
    if (length == 0U || length >= INET6_ADDRSTRLEN) return 0;
    memcpy(host, host_start, length);
    host[length] = '\0';
    *has_port = port_text != NULL;
    *port = 0;
    return port_text == NULL || ch_parse_port(port_text, port);
}

static int ch_api_is_loopback_name(const char *host) {
    if (strcasecmp(host, "localhost") == 0) return 1;
    struct in_addr parsed;
    if (uv_inet_pton(AF_INET, host, &parsed) == 0) {
        return ((const unsigned char *)&parsed)[0] == 127U;
    }
    struct in6_addr parsed_v6;
    static const struct in6_addr loopback = IN6ADDR_LOOPBACK_INIT;
    return uv_inet_pton(AF_INET6, host, &parsed_v6) == 0 &&
        memcmp(&parsed_v6, &loopback, sizeof(parsed_v6)) == 0;
}

int ch_api_is_loopback_host(const char *authority) {
    char host[INET6_ADDRSTRLEN];
    int port = 0;
    int has_port = 0;
    return ch_split_authority(authority, host, &port, &has_port) &&
        ch_api_is_loopback_name(host);
}

int ch_api_is_license_gated_request(const char *method, const char *path) {
    if (method == NULL || path == NULL) return 0;
    if (strcmp(path, "/api/v1/disconnect") == 0) return 0;
    if (strcmp(method, "DELETE") == 0 &&
        strncmp(path, "/api/v1/rules/temporary/", 24U) == 0) return 0;
    return strcmp(method, "POST") == 0 || strcmp(method, "PUT") == 0 ||
        strcmp(method, "DELETE") == 0;
}

static char *ch_api_read_license_snapshot(const char *path,
                                          ch_error *error) {
    FILE *file = fopen(path, "rb");
    if (file == NULL) {
        ch_error_set(error, CH_ERROR_IO, "read license snapshot %s: %s",
                     path, strerror(errno));
        return NULL;
    }
    ch_json_buffer snapshot;
    ch_json_init(&snapshot);
    char chunk[4096];
    int okay = 1;
    for (;;) {
        size_t count = fread(chunk, 1U, sizeof(chunk), file);
        if (count > 0U) {
            if (snapshot.length > CH_API_MAX_LICENSE_BYTES - count ||
                !ch_json_append_bytes(&snapshot, chunk, count)) {
                okay = 0;
                ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                             "license snapshot exceeds 8 MiB");
                break;
            }
        }
        if (count < sizeof(chunk)) {
            if (ferror(file)) {
                okay = 0;
                ch_error_set(error, CH_ERROR_IO,
                             "read license snapshot %s: %s", path,
                             strerror(errno));
            }
            break;
        }
    }
    (void)fclose(file);
    char *result = okay ? ch_json_take(&snapshot) : NULL;
    ch_json_dispose(&snapshot);
    if (okay && result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate license snapshot");
    }
    return result;
}

static ch_status ch_api_license_allowed(ch_api_server *server, int *allowed,
                                        ch_error *error) {
    *allowed = 0;
    if (server->license_path == NULL || server->license_path[0] == '\0') {
        *allowed = 1;
        return CH_OK;
    }
    uv_update_time(server->loop);
    uint64_t now = uv_now(server->loop);
    if (server->license_cache_valid && now < server->license_cache_expires) {
        *allowed = server->license_cache_allowed;
        return CH_OK;
    }
    char *snapshot = ch_api_read_license_snapshot(server->license_path,
                                                   error);
    if (snapshot == NULL) return error->code;
    bool evaluated = false;
    ch_status status = ch_license_can_use_snapshot_json(
        snapshot, 0, &evaluated, error);
    free(snapshot);
    if (status != CH_OK) return status;
    server->license_cache_allowed = evaluated ? 1 : 0;
    server->license_cache_valid = 1;
    server->license_cache_expires = now + CH_API_LICENSE_CACHE_MILLISECONDS;
    *allowed = server->license_cache_allowed;
    return CH_OK;
}

static int ch_api_host_allowed(const ch_api_server *server, const char *authority) {
    if (server->wildcard_bind) return authority != NULL && authority[0] != '\0';
    char host[INET6_ADDRSTRLEN];
    int port = 0;
    int has_port = 0;
    if (!ch_split_authority(authority, host, &port, &has_port)) return 0;
    if (!has_port) port = 80;
    if (port != server->bind_port) return 0;
    return ch_api_is_loopback_name(host) || strcasecmp(host, server->bind_host) == 0;
}

static int ch_api_origin_allowed(const ch_api_server *server, const char *origin) {
    if (origin == NULL || origin[0] == '\0') return 1;
    const char *authority = NULL;
    if (strncasecmp(origin, "http://", 7U) == 0) authority = origin + 7;
    else if (strncasecmp(origin, "https://", 8U) == 0) authority = origin + 8;
    else return 0;
    if (strpbrk(authority, "/?#") != NULL) return 0;
    char host[INET6_ADDRSTRLEN];
    int port = 0;
    int has_port = 0;
    if (!ch_split_authority(authority, host, &port, &has_port)) return 0;
    return ch_api_is_loopback_name(host) ||
        (!server->wildcard_bind && strcasecmp(host, server->bind_host) == 0);
}

static int ch_api_hex_digit(char value) {
    if (value >= '0' && value <= '9') return value - '0';
    if (value >= 'a' && value <= 'f') return value - 'a' + 10;
    if (value >= 'A' && value <= 'F') return value - 'A' + 10;
    return -1;
}

static char *ch_api_query_decode(const char *text, size_t length,
                                 ch_error *error) {
    char *decoded = malloc(length + 1U);
    if (decoded == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate decoded query parameter");
        return NULL;
    }
    size_t output = 0U;
    for (size_t index = 0U; index < length; ++index) {
        if (text[index] == '+') {
            decoded[output++] = ' ';
        } else if (text[index] == '%') {
            if (index + 2U >= length) goto malformed;
            int high = ch_api_hex_digit(text[index + 1U]);
            int low = ch_api_hex_digit(text[index + 2U]);
            if (high < 0 || low < 0 || (high == 0 && low == 0)) {
                goto malformed;
            }
            decoded[output++] = (char)((high << 4) | low);
            index += 2U;
        } else {
            decoded[output++] = text[index];
        }
    }
    decoded[output] = '\0';
    return decoded;

malformed:
    free(decoded);
    ch_error_set(error, CH_ERROR_PARSE,
                 "malformed URL query encoding");
    return NULL;
}

static char *ch_api_path_decode(const char *text, size_t length,
                                ch_error *error) {
    char *decoded = malloc(length + 1U);
    if (decoded == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate decoded path segment");
        return NULL;
    }
    size_t output = 0U;
    for (size_t index = 0U; index < length; ++index) {
        if (text[index] == '%') {
            if (index + 2U >= length) goto malformed;
            int high = ch_api_hex_digit(text[index + 1U]);
            int low = ch_api_hex_digit(text[index + 2U]);
            if (high < 0 || low < 0 || (high == 0 && low == 0)) {
                goto malformed;
            }
            decoded[output++] = (char)((high << 4) | low);
            index += 2U;
        } else {
            decoded[output++] = text[index];
        }
    }
    decoded[output] = '\0';
    return decoded;

malformed:
    free(decoded);
    ch_error_set(error, CH_ERROR_PARSE, "malformed URL path encoding");
    return NULL;
}

static char *ch_api_path_id_request_json(const char *path,
                                         const char *prefix,
                                         ch_error *error) {
    size_t prefix_length = strlen(prefix);
    if (strncmp(path, prefix, prefix_length) != 0 ||
        path[prefix_length] == '\0' || strchr(path + prefix_length, '/') !=
        NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND,
                     "resource path not found");
        return NULL;
    }
    char *id = ch_api_path_decode(path + prefix_length,
                                  strlen(path + prefix_length), error);
    if (id == NULL) return NULL;
    ch_json_buffer request;
    ch_json_init(&request);
    int okay = ch_json_append(&request, "{\"id\":") &&
        ch_json_append_string(&request, id) && ch_json_append(&request, "}");
    free(id);
    char *result = okay ? ch_json_take(&request) : NULL;
    ch_json_dispose(&request);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode resource id");
    }
    return result;
}

static char *ch_api_path_action_request_json(const char *path,
                                             const char *prefix,
                                             const char *suffix,
                                             const char *body,
                                             ch_error *error) {
    size_t prefix_length = strlen(prefix);
    size_t suffix_length = strlen(suffix);
    size_t path_length = strlen(path);
    if (path_length <= prefix_length + suffix_length ||
        strncmp(path, prefix, prefix_length) != 0 ||
        strcmp(path + path_length - suffix_length, suffix) != 0) {
        ch_error_set(error, CH_ERROR_NOT_FOUND,
                     "resource path not found");
        return NULL;
    }
    size_t id_length = path_length - prefix_length - suffix_length;
    if (memchr(path + prefix_length, '/', id_length) != NULL) {
        ch_error_set(error, CH_ERROR_NOT_FOUND,
                     "resource path not found");
        return NULL;
    }
    char *id = ch_api_path_decode(path + prefix_length, id_length, error);
    if (id == NULL) return NULL;
    const char *document = body == NULL || body[0] == '\0' ? "{}" : body;
    ch_json_value *request = ch_json_parse(document, strlen(document), error);
    if (request == NULL) {
        free(id);
        return NULL;
    }
    if (ch_json_value_type(request) != CH_JSON_OBJECT) {
        ch_json_value_destroy(request);
        free(id);
        ch_error_set(error, CH_ERROR_PARSE,
                     "request body must be a JSON object");
        return NULL;
    }
    ch_json_value *id_value = ch_json_value_new_string(id);
    free(id);
    ch_status status = id_value == NULL ? CH_ERROR_OUT_OF_MEMORY :
        ch_json_object_set(request, "id", id_value, error);
    if (status != CH_OK) {
        ch_json_value_destroy(id_value);
        ch_json_value_destroy(request);
        if (status == CH_ERROR_OUT_OF_MEMORY) {
            ch_error_set(error, status, "encode resource identifier");
        }
        return NULL;
    }
    ch_json_buffer encoded;
    ch_json_init(&encoded);
    int okay = ch_json_append_value(&encoded, request);
    ch_json_value_destroy(request);
    char *result = okay ? ch_json_take(&encoded) : NULL;
    ch_json_dispose(&encoded);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode resource request");
    }
    return result;
}

char *ch_api_profile_request_json(const char *url, ch_error *error) {
    ch_error_clear(error);
    const char *query = url == NULL ? NULL : strchr(url, '?');
    char *profile = NULL;
    if (query != NULL) {
        ++query;
        while (*query != '\0' && *query != '#') {
            const char *end = query + strcspn(query, "&#");
            const char *separator = memchr(query, '=', (size_t)(end - query));
            const char *key_end = separator == NULL ? end : separator;
            char *key = ch_api_query_decode(
                query, (size_t)(key_end - query), error);
            if (key == NULL) return NULL;
            if (profile == NULL && strcmp(key, "profile") == 0) {
                const char *value = separator == NULL ? end : separator + 1U;
                profile = ch_api_query_decode(
                    value, (size_t)(end - value), error);
            }
            free(key);
            if (error != NULL && error->code != CH_OK) {
                free(profile);
                return NULL;
            }
            query = *end == '&' ? end + 1U : end;
        }
    }
    ch_json_buffer request;
    ch_json_init(&request);
    int okay = ch_json_append(&request, "{");
    if (okay && profile != NULL) {
        okay = ch_json_append(&request, "\"profile\":") &&
            ch_json_append_string(&request, profile);
    }
    if (okay) okay = ch_json_append(&request, "}");
    free(profile);
    char *result = okay ? ch_json_take(&request) : NULL;
    ch_json_dispose(&request);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode profile query");
    }
    return result;
}

static int ch_api_traffic_query_key(const char *key) {
    static const char *const keys[] = {
        "state", "action", "profile", "rule", "country", "port",
        "query", "app", "domain", "process", "network", "limit",
        "offset"
    };
    for (size_t index = 0U; index < sizeof(keys) / sizeof(keys[0]); ++index) {
        if (strcmp(key, keys[index]) == 0) return (int)index;
    }
    return -1;
}

static int ch_api_nonnegative_integer(const char *value) {
    if (value == NULL || value[0] == '\0') return 0;
    for (const unsigned char *cursor = (const unsigned char *)value;
         *cursor != 0U; ++cursor) {
        if (!isdigit(*cursor)) return 0;
    }
    return 1;
}

char *ch_api_traffic_request_json(const char *url, ch_error *error) {
    ch_error_clear(error);
    ch_json_buffer request;
    ch_json_init(&request);
    int okay = ch_json_append(&request, "{");
    unsigned int seen = 0U;
    size_t written = 0U;
    const char *query = url == NULL ? NULL : strchr(url, '?');
    if (query != NULL) {
        ++query;
        while (okay && *query != '\0' && *query != '#') {
            const char *end = query + strcspn(query, "&#");
            const char *separator = memchr(query, '=', (size_t)(end - query));
            const char *key_end = separator == NULL ? end : separator;
            char *key = ch_api_query_decode(
                query, (size_t)(key_end - query), error);
            const char *value_start = separator == NULL ? end : separator + 1U;
            char *value = key == NULL ? NULL : ch_api_query_decode(
                value_start, (size_t)(end - value_start), error);
            if (key == NULL || value == NULL) {
                free(key);
                free(value);
                okay = 0;
                break;
            }
            int key_index = ch_api_traffic_query_key(key);
            unsigned int bit = key_index < 0 ? 0U : 1U << (unsigned int)key_index;
            if (key_index >= 0 && (seen & bit) == 0U) {
                int numeric = strcmp(key, "limit") == 0 ||
                    strcmp(key, "offset") == 0;
                if (numeric && !ch_api_nonnegative_integer(value)) {
                    ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                                 "invalid %s", key);
                    okay = 0;
                } else {
                    okay = (written == 0U || ch_json_append(&request, ",")) &&
                        ch_json_append_string(&request, key) &&
                        ch_json_append(&request, ":") &&
                        (numeric ? ch_json_append(&request, value) :
                                   ch_json_append_string(&request, value));
                    ++written;
                    seen |= bit;
                }
            }
            free(key);
            free(value);
            query = *end == '&' ? end + 1U : end;
        }
    }
    if (okay) okay = ch_json_append(&request, "}");
    char *result = okay ? ch_json_take(&request) : NULL;
    ch_json_dispose(&request);
    if (result == NULL && (error == NULL || error->code == CH_OK)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode traffic query");
    }
    return result;
}

static char *ch_api_trim(char *text) {
    if (text == NULL) return NULL;
    char *start = text;
    while (*start != '\0' && isspace((unsigned char)*start)) ++start;
    char *end = start + strlen(start);
    while (end > start && isspace((unsigned char)end[-1])) --end;
    *end = '\0';
    return start;
}

static int ch_api_events_append_strings(ch_json_value *array, char *value,
                                        ch_error *error) {
    char *cursor = value;
    while (cursor != NULL) {
        char *comma = strchr(cursor, ',');
        if (comma != NULL) *comma = '\0';
        char *item = ch_api_trim(cursor);
        if (item[0] != '\0') {
            ch_json_value *string = ch_json_value_new_string(item);
            if (string == NULL ||
                ch_json_array_append(array, string, error) != CH_OK) {
                ch_json_value_destroy(string);
                if (error == NULL || error->code == CH_OK) {
                    ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                                 "append event filter");
                }
                return 0;
            }
        }
        cursor = comma == NULL ? NULL : comma + 1U;
    }
    return 1;
}

char *ch_api_developer_entries_request_json(const char *url,
                                            ch_error *error) {
    ch_error_clear(error);
    ch_json_value *root = ch_json_value_new_object();
    if (root == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate developer entry filter");
        return NULL;
    }
    unsigned int seen = 0U;
    const char *query = url == NULL ? NULL : strchr(url, '?');
    int okay = 1;
    if (query != NULL) {
        ++query;
        while (okay && *query != '\0' && *query != '#') {
            const char *end = query + strcspn(query, "&#");
            const char *separator = memchr(query, '=', (size_t)(end - query));
            const char *key_end = separator == NULL ? end : separator;
            char *key = ch_api_query_decode(
                query, (size_t)(key_end - query), error);
            const char *value_start = separator == NULL ? end : separator + 1U;
            char *value = key == NULL ? NULL : ch_api_query_decode(
                value_start, (size_t)(end - value_start), error);
            if (key == NULL || value == NULL) {
                free(key);
                free(value);
                okay = 0;
                break;
            }
            static const char *const keys[] = {
                "method", "status_min", "status_max", "host", "scheme",
                "content_type", "q", "error_only", "limit"
            };
            int key_index = -1;
            for (size_t index = 0U;
                 index < sizeof(keys) / sizeof(keys[0]); ++index) {
                if (strcmp(key, keys[index]) == 0) {
                    key_index = (int)index;
                    break;
                }
            }
            unsigned int bit = key_index < 0 ? 0U :
                1U << (unsigned int)key_index;
            if (key_index >= 0 && (seen & bit) == 0U) {
                ch_json_value *member = NULL;
                const char *output_key = key;
                if (key_index == 0) {
                    member = ch_json_value_new_array();
                    okay = member != NULL && ch_api_events_append_strings(
                        member, value, error);
                    output_key = "methods";
                } else if (key_index == 1 || key_index == 2 ||
                           key_index == 8) {
                    char *number_end = NULL;
                    unsigned long long number = strtoull(
                        value, &number_end, 10);
                    if (!ch_api_nonnegative_integer(value) ||
                        number_end == value || *number_end != '\0' ||
                        number > (unsigned long long)INT64_MAX) {
                        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                                     "invalid %s", key);
                        okay = 0;
                    } else {
                        member = ch_json_value_new_int64((int64_t)number);
                    }
                } else if (key_index == 7) {
                    bool enabled;
                    if (strcasecmp(value, "1") == 0 ||
                        strcasecmp(value, "true") == 0 ||
                        strcasecmp(value, "yes") == 0 ||
                        strcasecmp(value, "on") == 0) {
                        enabled = true;
                    } else if (strcasecmp(value, "0") == 0 ||
                               strcasecmp(value, "false") == 0 ||
                               strcasecmp(value, "no") == 0 ||
                               strcasecmp(value, "off") == 0) {
                        enabled = false;
                    } else {
                        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                                     "invalid error_only");
                        okay = 0;
                        enabled = false;
                    }
                    if (okay) member = ch_json_value_new_bool(enabled);
                } else {
                    member = ch_json_value_new_string(value);
                    if (key_index == 6) output_key = "query";
                }
                if (okay && (member == NULL || ch_json_object_set(
                        root, output_key, member, error) != CH_OK)) {
                    ch_json_value_destroy(member);
                    okay = 0;
                }
                if (okay) seen |= bit;
            }
            free(key);
            free(value);
            query = *end == '&' ? end + 1U : end;
        }
    }
    ch_json_buffer encoded;
    ch_json_init(&encoded);
    if (okay) okay = ch_json_append_value(&encoded, root);
    ch_json_value_destroy(root);
    char *result = okay ? ch_json_take(&encoded) : NULL;
    ch_json_dispose(&encoded);
    if (result == NULL && (error == NULL || error->code == CH_OK)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode developer entry filter");
    }
    return result;
}

static int ch_api_events_append_cursors(ch_json_value *array, char *value,
                                        ch_error *error) {
    char *cursor = value;
    while (cursor != NULL) {
        char *comma = strchr(cursor, ',');
        if (comma != NULL) *comma = '\0';
        char *item = ch_api_trim(cursor);
        if (item[0] != '\0') {
            char *separator = strchr(item, ':');
            if (separator == NULL || strchr(separator + 1U, ':') != NULL) {
                ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                             "invalid since value %s: expected shard:lamport",
                             item);
                return 0;
            }
            *separator = '\0';
            char *shard_text = ch_api_trim(item);
            char *lamport_text = ch_api_trim(separator + 1U);
            char *shard_end = NULL;
            char *lamport_end = NULL;
            unsigned long long shard = strtoull(shard_text, &shard_end, 10);
            unsigned long long lamport = strtoull(
                lamport_text, &lamport_end, 10);
            if (shard_text[0] == '\0' || lamport_text[0] == '\0' ||
                shard_end == shard_text || *shard_end != '\0' ||
                lamport_end == lamport_text || *lamport_end != '\0' ||
                shard > (unsigned long long)INT64_MAX ||
                lamport > (unsigned long long)INT64_MAX) {
                ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                             "invalid since cursor");
                return 0;
            }
            ch_json_value *object = ch_json_value_new_object();
            ch_json_value *shard_value = ch_json_value_new_int64(
                (int64_t)shard);
            ch_json_value *lamport_value = ch_json_value_new_int64(
                (int64_t)lamport);
            int cursor_okay = object != NULL && shard_value != NULL &&
                lamport_value != NULL;
            if (cursor_okay) {
                cursor_okay = ch_json_object_set(
                    object, "shard_id", shard_value, error) == CH_OK;
                if (cursor_okay) shard_value = NULL;
            }
            if (cursor_okay) {
                cursor_okay = ch_json_object_set(
                    object, "lamport", lamport_value, error) == CH_OK;
                if (cursor_okay) lamport_value = NULL;
            }
            if (cursor_okay) {
                cursor_okay = ch_json_array_append(array, object, error) ==
                    CH_OK;
                if (cursor_okay) object = NULL;
            }
            if (!cursor_okay) {
                ch_json_value_destroy(shard_value);
                ch_json_value_destroy(lamport_value);
                ch_json_value_destroy(object);
                if (error == NULL || error->code == CH_OK) {
                    ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                                 "append event cursor");
                }
                return 0;
            }
        }
        cursor = comma == NULL ? NULL : comma + 1U;
    }
    return 1;
}

char *ch_api_events_request_json(const char *url, ch_error *error) {
    ch_error_clear(error);
    ch_json_value *root = ch_json_value_new_object();
    ch_json_value *types = ch_json_value_new_array();
    ch_json_value *conn_ids = ch_json_value_new_array();
    ch_json_value *since = ch_json_value_new_array();
    if (root == NULL || types == NULL || conn_ids == NULL || since == NULL) {
        ch_json_value_destroy(root); ch_json_value_destroy(types);
        ch_json_value_destroy(conn_ids); ch_json_value_destroy(since);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate event filters");
        return NULL;
    }
    const char *query = url == NULL ? NULL : strchr(url, '?');
    int okay = 1;
    if (query != NULL) {
        ++query;
        while (okay && *query != '\0' && *query != '#') {
            const char *end = query + strcspn(query, "&#");
            const char *separator = memchr(query, '=', (size_t)(end - query));
            const char *key_end = separator == NULL ? end : separator;
            char *key = ch_api_query_decode(
                query, (size_t)(key_end - query), error);
            const char *value_start = separator == NULL ? end : separator + 1U;
            char *value = key == NULL ? NULL : ch_api_query_decode(
                value_start, (size_t)(end - value_start), error);
            if (key == NULL || value == NULL) {
                free(key); free(value); okay = 0; break;
            }
            if (strcmp(key, "types") == 0) {
                okay = ch_api_events_append_strings(types, value, error);
            } else if (strcmp(key, "conn_id") == 0) {
                okay = ch_api_events_append_strings(conn_ids, value, error);
            } else if (strcmp(key, "since") == 0) {
                okay = ch_api_events_append_cursors(since, value, error);
            } else if (strcmp(key, "after") == 0) {
                char *number_end = NULL;
                unsigned long long number = strtoull(value, &number_end, 10);
                if (!ch_api_nonnegative_integer(value) ||
                    number_end == value || *number_end != '\0' ||
                    number > (unsigned long long)INT64_MAX) {
                    ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                                 "invalid after sequence");
                    okay = 0;
                } else {
                    ch_json_value *after = ch_json_value_new_int64(
                        (int64_t)number);
                    if (after == NULL || ch_json_object_set(
                            root, "after_sequence", after, error) != CH_OK) {
                        ch_json_value_destroy(after);
                        okay = 0;
                    }
                }
            } else if (strcmp(key, "limit") == 0) {
                char *number_end = NULL;
                unsigned long long number = strtoull(value, &number_end, 10);
                if (!ch_api_nonnegative_integer(value) ||
                    number_end == value || *number_end != '\0' ||
                    number == 0U || number > 4096U) {
                    ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                                 "invalid event limit");
                    okay = 0;
                } else {
                    ch_json_value *limit = ch_json_value_new_int64(
                        (int64_t)number);
                    if (limit == NULL || ch_json_object_set(
                            root, "limit", limit, error) != CH_OK) {
                        ch_json_value_destroy(limit);
                        okay = 0;
                    }
                }
            }
            free(key); free(value);
            query = *end == '&' ? end + 1U : end;
        }
    }
    if (okay && ch_json_array_size(types) > 0U) {
        okay = ch_json_object_set(root, "types", types, error) == CH_OK;
        if (okay) types = NULL;
    }
    if (okay && ch_json_array_size(conn_ids) > 0U) {
        okay = ch_json_object_set(root, "conn_ids", conn_ids, error) ==
            CH_OK;
        if (okay) conn_ids = NULL;
    }
    if (okay && ch_json_array_size(since) > 0U) {
        okay = ch_json_object_set(root, "since", since, error) == CH_OK;
        if (okay) since = NULL;
    }
    ch_json_buffer encoded;
    ch_json_init(&encoded);
    if (okay) okay = ch_json_append_value(&encoded, root);
    char *result = okay ? ch_json_take(&encoded) : NULL;
    ch_json_dispose(&encoded);
    ch_json_value_destroy(root); ch_json_value_destroy(types);
    ch_json_value_destroy(conn_ids); ch_json_value_destroy(since);
    if (result == NULL && (error == NULL || error->code == CH_OK)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode event filters");
    }
    return result;
}

static int ch_token_equal(const char *configured, const char *authorization) {
    if (configured == NULL || configured[0] == '\0') return 1;
    static const char prefix[] = "Bearer ";
    if (authorization == NULL || strncmp(authorization, prefix, sizeof(prefix) - 1U) != 0) return 0;
    const char *provided = authorization + sizeof(prefix) - 1U;
    size_t configured_length = strlen(configured);
    return configured_length == strlen(provided) &&
        sodium_memcmp(configured, provided, configured_length) == 0;
}

static void ch_api_server_maybe_free(ch_api_server *server) {
    if (server->closing && server->listener_closed && server->client_count == 0U) {
        free(server->auth_token);
        free(server->license_path);
        free(server->address);
        free(server);
    }
}

static void ch_api_client_unlink(ch_api_client *client) {
    if (client->unlinked) return;
    ch_api_client **cursor = &client->server->clients;
    while (*cursor != NULL) {
        if (*cursor == client) {
            *cursor = client->next;
            --client->server->client_count;
            client->unlinked = 1;
            return;
        }
        cursor = &(*cursor)->next;
    }
}

static void ch_api_client_maybe_free(ch_api_client *client) {
    if (!client->stream_closed ||
        (client->websocket_timer_initialized &&
         !client->websocket_timer_closed)) return;
    ch_api_server *server = client->server;
    ch_api_client_unlink(client);
    ch_json_dispose(&client->url);
    ch_json_dispose(&client->field);
    ch_json_dispose(&client->value);
    ch_json_dispose(&client->body);
    free(client->authorization);
    free(client->host);
    free(client->origin);
    free(client->connection);
    free(client->upgrade);
    free(client->websocket_key);
    free(client->websocket_version);
    free(client->websocket_filters);
    free(client);
    ch_api_server_maybe_free(server);
}

static void ch_api_client_closed(uv_handle_t *handle) {
    ch_api_client *client = handle->data;
    client->stream_closed = 1;
    ch_api_client_maybe_free(client);
}

static void ch_api_websocket_timer_closed(uv_handle_t *handle) {
    ch_api_client *client = handle->data;
    client->websocket_timer_closed = 1;
    ch_api_client_maybe_free(client);
}

static void ch_api_close_client(ch_api_client *client) {
    if (client->websocket_timer_initialized &&
        !uv_is_closing((uv_handle_t *)&client->websocket_timer)) {
        (void)uv_timer_stop(&client->websocket_timer);
        uv_close((uv_handle_t *)&client->websocket_timer,
                 ch_api_websocket_timer_closed);
    }
    if (!uv_is_closing((uv_handle_t *)&client->stream)) {
        (void)uv_read_stop((uv_stream_t *)&client->stream);
        uv_close((uv_handle_t *)&client->stream, ch_api_client_closed);
    }
}

static void ch_api_write_finished(uv_write_t *request, int status) {
    ch_api_write *write = request->data;
    ch_api_client *client = request->handle->data;
    int close_after = write->close_after;
    free(write->bytes);
    free(write);
    client->websocket_write_pending = 0;
    if (status < 0 || close_after) ch_api_close_client(client);
}

static const char *ch_api_reason(int status) {
    switch (status) {
        case 200: return "OK";
        case 400: return "Bad Request";
        case 401: return "Unauthorized";
        case 403: return "Forbidden";
        case 404: return "Not Found";
        case 405: return "Method Not Allowed";
        case 409: return "Conflict";
        default: return "Internal Server Error";
    }
}

static void ch_api_respond_with_headers(ch_api_client *client, int status,
                                        const char *type,
                                        const char *content_disposition,
                                        const char *body) {
    if (client->responded) return;
    client->responded = 1;
    (void)uv_read_stop((uv_stream_t *)&client->stream);
    if (type == NULL) type = "text/plain; charset=utf-8";
    if (body == NULL) body = "";
    size_t body_length = strlen(body);
    const char *disposition_header = content_disposition == NULL ? "" :
        "Content-Disposition: ";
    const char *disposition_value = content_disposition == NULL ? "" :
        content_disposition;
    const char *disposition_end = content_disposition == NULL ? "" : "\r\n";
    int header_length = snprintf(
        NULL, 0,
        "HTTP/1.1 %d %s\r\nContent-Type: %s\r\n%s%s%s"
        "Content-Length: %zu\r\nCache-Control: no-store\r\n"
        "Connection: close\r\n\r\n",
        status, ch_api_reason(status), type, disposition_header,
        disposition_value, disposition_end, body_length);
    if (header_length < 0 || (size_t)header_length > SIZE_MAX - body_length - 1U) {
        ch_api_close_client(client);
        return;
    }
    ch_api_write *write = calloc(1U, sizeof(*write));
    size_t total = (size_t)header_length + body_length;
    if (write == NULL || (write->bytes = malloc(total + 1U)) == NULL) {
        free(write);
        ch_api_close_client(client);
        return;
    }
    write->close_after = 1;
    (void)snprintf(
        write->bytes, (size_t)header_length + 1U,
        "HTTP/1.1 %d %s\r\nContent-Type: %s\r\n%s%s%s"
        "Content-Length: %zu\r\nCache-Control: no-store\r\n"
        "Connection: close\r\n\r\n",
        status, ch_api_reason(status), type, disposition_header,
        disposition_value, disposition_end, body_length);
    memcpy(write->bytes + header_length, body, body_length);
    write->bytes[total] = '\0';
    uv_buf_t buffer = uv_buf_init(write->bytes, (unsigned int)total);
    write->request.data = write;
    if (uv_write(&write->request, (uv_stream_t *)&client->stream, &buffer, 1U, ch_api_write_finished) != 0) {
        free(write->bytes);
        free(write);
        ch_api_close_client(client);
    }
}

static void ch_api_respond(ch_api_client *client, int status,
                           const char *type, const char *body) {
    ch_api_respond_with_headers(client, status, type, NULL, body);
}

static void ch_api_runtime_error(ch_api_client *client, ch_status status, const ch_error *error) {
    ch_json_buffer json;
    ch_json_init(&json);
    (void)ch_json_append(&json, "{\"error\":");
    (void)ch_json_append_string(&json, error->message);
    (void)ch_json_append(&json, "}");
    char *body = ch_json_take(&json);
    if (body == NULL) {
        ch_api_respond(client, 500, NULL, "internal error\n");
        return;
    }
    int http_status = status == CH_ERROR_NOT_FOUND ? 404 :
        (status == CH_ERROR_INVALID_ARGUMENT ||
         status == CH_ERROR_INVALID_STATE || status == CH_ERROR_PARSE ?
            400 : 500);
    ch_api_respond(client, http_status, "application/json", body);
    free(body);
}

static int ch_api_header_has_token(const char *header, const char *token) {
    if (header == NULL || token == NULL) return 0;
    const char *cursor = header;
    while (*cursor != '\0') {
        while (*cursor == ',' || isspace((unsigned char)*cursor)) ++cursor;
        const char *end = cursor;
        while (*end != '\0' && *end != ',') ++end;
        const char *trimmed = end;
        while (trimmed > cursor && isspace((unsigned char)trimmed[-1])) {
            --trimmed;
        }
        size_t length = (size_t)(trimmed - cursor);
        if (strlen(token) == length && strncasecmp(cursor, token, length) ==
            0) return 1;
        cursor = *end == ',' ? end + 1U : end;
    }
    return 0;
}

static int ch_api_websocket_frame(ch_json_buffer *output, uint8_t opcode,
                                  const char *payload, size_t length) {
    uint8_t header[10];
    size_t header_length = 2U;
    header[0] = (uint8_t)(0x80U | (opcode & 0x0fU));
    if (length <= 125U) {
        header[1] = (uint8_t)length;
    } else if (length <= UINT16_MAX) {
        header[1] = 126U;
        header[2] = (uint8_t)(length >> 8U);
        header[3] = (uint8_t)length;
        header_length = 4U;
    } else {
        header[1] = 127U;
        uint64_t encoded = (uint64_t)length;
        for (size_t index = 0U; index < 8U; ++index) {
            header[2U + index] = (uint8_t)(encoded >>
                (unsigned int)((7U - index) * 8U));
        }
        header_length = 10U;
    }
    return ch_json_append_bytes(output, (const char *)header,
                                header_length) &&
        (length == 0U || ch_json_append_bytes(output, payload, length));
}

static int ch_api_websocket_write(ch_api_client *client, char *bytes,
                                  size_t length) {
    ch_api_write *write = calloc(1U, sizeof(*write));
    if (write == NULL) {
        free(bytes);
        ch_api_close_client(client);
        return 0;
    }
    write->bytes = bytes;
    write->close_after = 0;
    write->request.data = write;
    uv_buf_t buffer = uv_buf_init(bytes, (unsigned int)length);
    client->websocket_write_pending = 1;
    if (uv_write(&write->request, (uv_stream_t *)&client->stream, &buffer,
                 1U, ch_api_write_finished) != 0) {
        client->websocket_write_pending = 0;
        free(write->bytes);
        free(write);
        ch_api_close_client(client);
        return 0;
    }
    return 1;
}

static char *ch_api_websocket_poll_request(ch_api_client *client,
                                           ch_error *error) {
    ch_json_value *request = ch_json_parse(
        client->websocket_filters, strlen(client->websocket_filters), error);
    if (request == NULL) return NULL;
    if (client->websocket_initialized) {
        (void)ch_json_object_remove(request, "since");
    }
    ch_json_value *sequence = ch_json_value_new_int64(
        (int64_t)client->websocket_next_sequence);
    if (sequence == NULL || ch_json_object_set(
            request, "after_sequence", sequence, error) != CH_OK) {
        ch_json_value_destroy(sequence);
        ch_json_value_destroy(request);
        return NULL;
    }
    ch_json_buffer encoded;
    ch_json_init(&encoded);
    int okay = ch_json_append_value(&encoded, request);
    ch_json_value_destroy(request);
    char *result = okay ? ch_json_take(&encoded) : NULL;
    ch_json_dispose(&encoded);
    if (result == NULL && (error == NULL || error->code == CH_OK)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode WebSocket event request");
    }
    return result;
}

static void ch_api_websocket_poll(uv_timer_t *timer) {
    ch_api_client *client = timer->data;
    if (!client->websocket || client->websocket_write_pending ||
        uv_is_closing((uv_handle_t *)&client->stream)) return;
    ++client->websocket_ticks;
    ch_error error;
    char *request = ch_api_websocket_poll_request(client, &error);
    char *response = NULL;
    ch_status status = request == NULL ? error.code : ch_runtime_query(
        client->server->runtime, "events", request, &response, &error);
    free(request);
    if (status != CH_OK || response == NULL) {
        free(response);
        ch_api_close_client(client);
        return;
    }
    ch_json_value *root = ch_json_parse(response, strlen(response), &error);
    free(response);
    if (root == NULL || ch_json_value_type(root) != CH_JSON_OBJECT) {
        ch_json_value_destroy(root);
        ch_api_close_client(client);
        return;
    }
    int64_t next = -1;
    const ch_json_value *next_value = ch_json_object_get(
        root, "next_sequence");
    const ch_json_value *events = ch_json_object_get(root, "events");
    if (!ch_json_int64_value(next_value, &next) || next < 0 ||
        events == NULL || ch_json_value_type(events) != CH_JSON_ARRAY) {
        ch_json_value_destroy(root);
        ch_api_close_client(client);
        return;
    }
    client->websocket_next_sequence = (uint64_t)next;
    client->websocket_initialized = 1;
    ch_json_buffer frames;
    ch_json_init(&frames);
    int okay = 1;
    if (!ch_json_bool_value(ch_json_object_get(root, "complete"), true)) {
        static const char gap[] =
            "{\"shard_id\":0,\"lamport\":0,\"ts_ns\":0,"
            "\"type\":\"replay.gap\",\"data\":{}}";
        okay = ch_api_websocket_frame(&frames, 0x1U, gap,
                                      sizeof(gap) - 1U);
    }
    size_t count = ch_json_array_size(events);
    for (size_t index = 0U; okay && index < count; ++index) {
        ch_json_buffer event;
        ch_json_init(&event);
        okay = ch_json_append_value(&event, ch_json_array_get(events,
                                                              index));
        char *event_json = okay ? ch_json_take(&event) : NULL;
        ch_json_dispose(&event);
        if (event_json == NULL) {
            okay = 0;
        } else {
            okay = ch_api_websocket_frame(&frames, 0x1U, event_json,
                                           strlen(event_json));
        }
        free(event_json);
    }
    if (okay && client->websocket_ticks % 300U == 0U) {
        okay = ch_api_websocket_frame(&frames, 0x9U, "", 0U);
    }
    ch_json_value_destroy(root);
    size_t frame_length = frames.length;
    char *frame_bytes = okay && frame_length > 0U ? ch_json_take(&frames) :
        NULL;
    ch_json_dispose(&frames);
    if (!okay) {
        free(frame_bytes);
        ch_api_close_client(client);
    } else if (frame_bytes != NULL) {
        (void)ch_api_websocket_write(client, frame_bytes, frame_length);
    }
}

static int ch_api_websocket_accept(ch_api_client *client,
                                   const char *filters) {
    if (!ch_api_header_has_token(client->connection, "upgrade") ||
        !ch_ascii_equal(client->upgrade, "websocket") ||
        !ch_ascii_equal(client->websocket_version, "13") ||
        client->websocket_key == NULL ||
        strlen(client->websocket_key) > 128U) {
        ch_api_respond(client, 400, NULL,
                       "invalid WebSocket upgrade\n");
        return 0;
    }
    unsigned char decoded[32];
    int decoded_length = EVP_DecodeBlock(
        decoded, (const unsigned char *)client->websocket_key,
        (int)strlen(client->websocket_key));
    size_t key_length = strlen(client->websocket_key);
    while (key_length > 0U && client->websocket_key[key_length - 1U] == '=') {
        --decoded_length;
        --key_length;
    }
    if (decoded_length != 16) {
        ch_api_respond(client, 400, NULL, "invalid WebSocket key\n");
        return 0;
    }
    static const char guid[] = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11";
    size_t source_length = strlen(client->websocket_key) + sizeof(guid) - 1U;
    char *source = malloc(source_length + 1U);
    unsigned char digest[EVP_MAX_MD_SIZE];
    size_t digest_length = 0U;
    if (source == NULL) {
        ch_api_respond(client, 500, NULL, "internal error\n");
        return 0;
    }
    (void)snprintf(source, source_length + 1U, "%s%s",
                   client->websocket_key, guid);
    int digested = EVP_Q_digest(NULL, "SHA1", NULL, source, source_length,
                                digest, &digest_length);
    free(source);
    unsigned char accept[64];
    int accept_length = digested && digest_length == 20U ?
        EVP_EncodeBlock(accept, digest, (int)digest_length) : -1;
    if (accept_length <= 0) {
        ch_api_respond(client, 500, NULL, "internal error\n");
        return 0;
    }
    accept[accept_length] = '\0';
    char *baseline_json = NULL;
    ch_error baseline_error;
    ch_status baseline_status = ch_runtime_query(
        client->server->runtime, "events", "{}", &baseline_json,
        &baseline_error);
    ch_json_value *baseline = baseline_status == CH_OK ? ch_json_parse(
        baseline_json, strlen(baseline_json), &baseline_error) : NULL;
    int64_t baseline_sequence = -1;
    free(baseline_json);
    if (baseline == NULL || !ch_json_int64_value(
            ch_json_object_get(baseline, "next_sequence"),
            &baseline_sequence) || baseline_sequence < 0) {
        ch_json_value_destroy(baseline);
        ch_api_respond(client, 500, NULL, "internal error\n");
        return 0;
    }
    ch_json_value_destroy(baseline);
    int response_length = snprintf(
        NULL, 0,
        "HTTP/1.1 101 Switching Protocols\r\n"
        "Upgrade: websocket\r\nConnection: Upgrade\r\n"
        "Sec-WebSocket-Accept: %s\r\nCache-Control: no-store\r\n\r\n",
        (const char *)accept);
    char *response = response_length < 0 ? NULL :
        malloc((size_t)response_length + 1U);
    char *filter_copy = ch_strdup(filters);
    if (response == NULL || filter_copy == NULL ||
        uv_timer_init(client->server->loop, &client->websocket_timer) != 0) {
        free(response); free(filter_copy);
        ch_api_respond(client, 500, NULL, "internal error\n");
        return 0;
    }
    client->websocket_timer_initialized = 1;
    client->websocket_timer.data = client;
    (void)snprintf(
        response, (size_t)response_length + 1U,
        "HTTP/1.1 101 Switching Protocols\r\n"
        "Upgrade: websocket\r\nConnection: Upgrade\r\n"
        "Sec-WebSocket-Accept: %s\r\nCache-Control: no-store\r\n\r\n",
        (const char *)accept);
    client->responded = 1;
    client->websocket = 1;
    client->websocket_next_sequence = (uint64_t)baseline_sequence;
    client->websocket_filters = filter_copy;
    (void)uv_read_stop((uv_stream_t *)&client->stream);
    if (uv_timer_start(&client->websocket_timer, ch_api_websocket_poll,
                       100U, 100U) != 0) {
        free(response);
        ch_api_close_client(client);
        return 0;
    }
    if (!ch_api_websocket_write(client, response,
                                (size_t)response_length)) {
        ch_api_close_client(client);
        return 0;
    }
    return 1;
}

static void ch_api_route(ch_api_client *client) {
    if (!ch_api_host_allowed(client->server, client->host) ||
        !ch_api_origin_allowed(client->server, client->origin)) {
        ch_api_respond(client, 403, NULL, "forbidden host or origin\n");
        return;
    }
    if (!ch_token_equal(client->server->auth_token, client->authorization)) {
        ch_api_respond(client, 401, NULL, "unauthorized\n");
        return;
    }
    const char *method = llhttp_method_name((llhttp_method_t)llhttp_get_method(&client->parser));
    const char *url = client->url.data == NULL ? "" : client->url.data;
    char *path = strndup(url, strcspn(url, "?"));
    if (path == NULL) {
        ch_api_respond(client, 500, NULL, "internal error\n");
        return;
    }
    if (ch_api_is_license_gated_request(method, path) &&
        client->server->license_path != NULL &&
        client->server->license_path[0] != '\0') {
        ch_error license_error;
        int allowed = 0;
        ch_status license_status = ch_api_license_allowed(
            client->server, &allowed, &license_error);
        if (license_status != CH_OK || !allowed) {
            ch_api_respond(client, 403, "application/json",
                           license_status == CH_OK ?
                               "{\"error\":\"license required\"}\n" :
                               "{\"error\":\"license unavailable\"}\n");
            free(path);
            return;
        }
    }
    if (strcmp(method, "GET") == 0 &&
        strcmp(path, "/api/v1/events/snapshot") == 0) {
        ch_error event_error;
        char *filters = ch_api_events_request_json(url, &event_error);
        char *snapshot = NULL;
        ch_status event_status = filters == NULL ? event_error.code :
            ch_runtime_query(client->server->runtime, "events", filters,
                             &snapshot, &event_error);
        if (event_status == CH_OK) {
            ch_api_respond(client, 200, "application/json", snapshot);
        } else {
            ch_api_runtime_error(client, event_status, &event_error);
        }
        free(snapshot);
        free(filters);
        free(path);
        return;
    }
    if (strcmp(method, "GET") == 0 &&
        strcmp(path, "/api/v1/events") == 0) {
        ch_error event_error;
        char *filters = ch_api_events_request_json(url, &event_error);
        if (filters == NULL) {
            ch_api_runtime_error(client, event_error.code, &event_error);
        } else {
            (void)ch_api_websocket_accept(client, filters);
        }
        free(filters);
        free(path);
        return;
    }
    char *json = NULL;
    const char *response_type = "application/json";
    const char *content_disposition = NULL;
    int config_transfer = 0;
    int persistence_required = 0;
    int conflict_on_invalid_state = 0;
    ch_error error;
    ch_status status;
    char *profile_request = ch_api_profile_request_json(url, &error);
    if (profile_request == NULL) {
        ch_api_runtime_error(client, error.code, &error);
        free(path);
        return;
    }
    char *traffic_request = NULL;
    if (strcmp(path, "/api/v1/traffic") == 0 ||
        strcmp(path, "/api/v1/decisions") == 0) {
        traffic_request = ch_api_traffic_request_json(url, &error);
        if (traffic_request == NULL) {
            ch_api_runtime_error(client, error.code, &error);
            free(profile_request);
            free(path);
            return;
        }
    }
    if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/status") == 0) {
        status = ch_runtime_query(client->server->runtime, "status", "{}", &json, &error);
    } else if (strcmp(method, "POST") == 0 &&
               strcmp(path, "/api/v1/outline/review") == 0) {
        status = ch_runtime_query(
            client->server->runtime, "outline_review",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "POST") == 0 &&
               strcmp(path, "/api/v1/outline/import") == 0) {
        persistence_required = 1;
        status = ch_runtime_mutate(
            client->server->runtime, "outline_import",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "POST") == 0 &&
               strcmp(path, "/api/v1/outline/refresh") == 0) {
        persistence_required = 1;
        conflict_on_invalid_state = 1;
        status = ch_runtime_mutate(
            client->server->runtime, "outline_refresh",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/profiles") == 0) {
        status = ch_runtime_query(client->server->runtime, "profiles", "{}", &json, &error);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/servers") == 0) {
        status = ch_runtime_query(client->server->runtime, "servers", profile_request, &json, &error);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/rules") == 0) {
        status = ch_runtime_query(client->server->runtime, "rules", profile_request, &json, &error);
    } else if (strcmp(method, "GET") == 0 &&
               strcmp(path, "/api/v1/traffic") == 0) {
        status = ch_runtime_query(
            client->server->runtime, "traffic_filter", traffic_request,
            &json, &error);
    } else if (strcmp(method, "GET") == 0 &&
               strcmp(path, "/api/v1/decisions") == 0) {
        status = ch_runtime_query(client->server->runtime, "decisions",
                                  traffic_request, &json, &error);
    } else if (strcmp(method, "GET") == 0 &&
               strcmp(path, "/api/v1/rules/temporary") == 0) {
        status = ch_runtime_query(client->server->runtime,
                                  "temporary_rules", profile_request,
                                  &json, &error);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/policy-groups") == 0) {
        status = ch_runtime_query(client->server->runtime, "policy_groups", profile_request, &json, &error);
    } else if (strcmp(method, "POST") == 0 &&
               strcmp(path, "/api/v1/policy-groups/test") == 0) {
        conflict_on_invalid_state = 1;
        status = ch_runtime_mutate(
            client->server->runtime, "test_policy_groups",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/rule-sets") == 0) {
        status = ch_runtime_query(client->server->runtime, "rule_sets", profile_request, &json, &error);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/dns") == 0) {
        status = ch_runtime_query(client->server->runtime, "dns", profile_request, &json, &error);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/config/settings") == 0) {
        status = ch_runtime_query(client->server->runtime, "config_settings", profile_request, &json, &error);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/conditioner") == 0) {
        status = ch_runtime_query(client->server->runtime, "conditioner", profile_request, &json, &error);
    } else if (strcmp(method, "GET") == 0 &&
               strcmp(path, "/api/v1/developer/status") == 0) {
        status = ch_runtime_query(client->server->runtime,
                                  "developer_status", "{}", &json,
                                  &error);
    } else if (strcmp(method, "GET") == 0 &&
               strcmp(path, "/api/v1/developer/ca.pem") == 0) {
        response_type = "application/x-pem-file";
        content_disposition =
            "attachment; filename=\"clambhook-developer-ca.pem\"";
        status = ch_runtime_query(client->server->runtime,
                                  "developer_ca", "{}", &json, &error);
    } else if (strcmp(method, "GET") == 0 &&
               strcmp(path,
                      "/api/v1/developer/breakpoints/pending") == 0) {
        status = ch_runtime_query(
            client->server->runtime, "developer_pending_breakpoints", "{}",
            &json, &error);
    } else if (strcmp(method, "GET") == 0 &&
               strcmp(path, "/api/v1/developer/entries") == 0) {
        char *request = ch_api_developer_entries_request_json(url, &error);
        if (request == NULL) {
            ch_api_runtime_error(client, error.code, &error);
            free(profile_request);
            free(path);
            return;
        }
        status = ch_runtime_query(client->server->runtime,
                                  "developer_entries", request, &json,
                                  &error);
        free(request);
    } else if (strcmp(method, "GET") == 0 &&
               strcmp(path, "/api/v1/developer/har") == 0) {
        content_disposition = "attachment; filename=\"clambhook.har\"";
        status = ch_runtime_query(client->server->runtime, "developer_har",
                                  "{}", &json, &error);
    } else if (strcmp(method, "GET") == 0 &&
               strncmp(path, "/api/v1/developer/entries/", 26U) == 0) {
        static const char entry_prefix[] = "/api/v1/developer/entries/";
        size_t path_length = strlen(path);
        static const char curl_suffix[] = "/curl";
        bool curl = path_length > sizeof(entry_prefix) - 1U +
                sizeof(curl_suffix) - 1U &&
            strcmp(path + path_length - (sizeof(curl_suffix) - 1U),
                   curl_suffix) == 0;
        char *request = curl ? ch_api_path_action_request_json(
            path, entry_prefix, curl_suffix, NULL, &error) :
            ch_api_path_id_request_json(path, entry_prefix, &error);
        if (request == NULL) {
            ch_api_runtime_error(client, error.code, &error);
            free(profile_request);
            free(path);
            return;
        }
        status = ch_runtime_query(
            client->server->runtime,
            curl ? "developer_entry_curl" : "developer_entry", request,
            &json, &error);
        free(request);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/developer/settings") == 0) {
        status = ch_runtime_query(client->server->runtime, "developer_settings", "{}", &json, &error);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/developer/map-rules") == 0) {
        status = ch_runtime_query(client->server->runtime, "developer_map_rules", "{}", &json, &error);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/developer/breakpoint-rules") == 0) {
        status = ch_runtime_query(client->server->runtime, "developer_breakpoint_rules", "{}", &json, &error);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/developer/rewrite-rules") == 0) {
        status = ch_runtime_query(client->server->runtime, "developer_rewrite_rules", "{}", &json, &error);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/rule-subscriptions") == 0) {
        status = ch_runtime_query(client->server->runtime, "rule_subscriptions", profile_request, &json, &error);
    } else if (strcmp(method, "GET") == 0 &&
               strcmp(path, "/api/v1/prompts/pending") == 0) {
        status = ch_runtime_query(client->server->runtime,
                                  "pending_prompts", "{}", &json, &error);
    } else if (strcmp(method, "GET") == 0 &&
               strcmp(path, "/api/v1/prompts/decisions") == 0) {
        status = ch_runtime_query(client->server->runtime,
                                  "silent_decisions", "{}", &json, &error);
    } else if (strcmp(method, "PUT") == 0 && strcmp(path, "/api/v1/dns") == 0) {
        persistence_required = 1;
        status = ch_runtime_mutate(
            client->server->runtime, "update_dns",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "PUT") == 0 && strcmp(path, "/api/v1/config/settings") == 0) {
        persistence_required = 1;
        status = ch_runtime_mutate(
            client->server->runtime, "update_config_settings",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "PUT") == 0 && strcmp(path, "/api/v1/conditioner") == 0) {
        persistence_required = 1;
        status = ch_runtime_mutate(
            client->server->runtime, "update_conditioner",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "DELETE") == 0 &&
               strcmp(path, "/api/v1/developer/entries") == 0) {
        status = ch_runtime_mutate(client->server->runtime,
                                   "clear_developer_entries", "{}", &json,
                                   &error);
    } else if (strcmp(method, "POST") == 0 &&
               strcmp(path, "/api/v1/developer/curl/import") == 0) {
        status = ch_runtime_query(
            client->server->runtime, "developer_curl_import",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "POST") == 0 &&
               (strcmp(path, "/api/v1/developer/send") == 0 ||
                strcmp(path, "/api/v1/developer/repeat") == 0)) {
        status = ch_runtime_developer_request(
            client->server->runtime,
            strcmp(path, "/api/v1/developer/repeat") == 0,
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "POST") == 0 &&
               strcmp(path,
                      "/api/v1/developer/ca/regenerate") == 0) {
        status = ch_runtime_mutate(
            client->server->runtime, "regenerate_developer_ca", "{}",
            &json, &error);
    } else if (strcmp(method, "POST") == 0 &&
               strncmp(path, "/api/v1/developer/breakpoints/", 30U) == 0) {
        char *request = ch_api_path_action_request_json(
            path, "/api/v1/developer/breakpoints/", "/resolve",
            client->body.data, &error);
        if (request == NULL) {
            ch_api_runtime_error(client, error.code, &error);
            free(traffic_request);
            free(profile_request);
            free(path);
            return;
        }
        status = ch_runtime_mutate(
            client->server->runtime, "resolve_developer_breakpoint", request,
            &json, &error);
        free(request);
    } else if ((strcmp(method, "PUT") == 0 ||
                strcmp(method, "POST") == 0) &&
               strcmp(path, "/api/v1/rules") == 0) {
        persistence_required = 1;
        status = ch_runtime_mutate(
            client->server->runtime,
            strcmp(method, "PUT") == 0 ? "replace_rules" : "create_rule",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "PUT") == 0 &&
               strcmp(path, "/api/v1/policy-groups") == 0) {
        persistence_required = 1;
        status = ch_runtime_mutate(
            client->server->runtime, "replace_policy_groups",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "PUT") == 0 &&
               strcmp(path, "/api/v1/rule-sets") == 0) {
        persistence_required = 1;
        status = ch_runtime_mutate(
            client->server->runtime, "replace_rule_sets",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "PUT") == 0 &&
               strcmp(path, "/api/v1/rule-subscriptions") == 0) {
        persistence_required = 1;
        status = ch_runtime_mutate(
            client->server->runtime, "replace_rule_subscriptions",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "PUT") == 0 &&
               strcmp(path, "/api/v1/policy-groups/selection") == 0) {
        persistence_required = 1;
        status = ch_runtime_mutate(
            client->server->runtime, "select_policy_group",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "PUT") == 0 &&
               strcmp(path, "/api/v1/developer/settings") == 0) {
        persistence_required = 1;
        status = ch_runtime_mutate(
            client->server->runtime, "update_developer_settings",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "PUT") == 0 &&
               strcmp(path, "/api/v1/developer/map-rules") == 0) {
        persistence_required = 1;
        status = ch_runtime_mutate(
            client->server->runtime, "replace_developer_map_rules",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "PUT") == 0 &&
               strcmp(path, "/api/v1/developer/breakpoint-rules") == 0) {
        persistence_required = 1;
        status = ch_runtime_mutate(
            client->server->runtime, "replace_developer_breakpoint_rules",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "PUT") == 0 &&
               strcmp(path, "/api/v1/developer/rewrite-rules") == 0) {
        persistence_required = 1;
        status = ch_runtime_mutate(
            client->server->runtime, "replace_developer_rewrite_rules",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "DELETE") == 0 &&
               (strncmp(path, "/api/v1/developer/map-rules/", 28U) == 0 ||
                strncmp(path, "/api/v1/developer/breakpoint-rules/", 35U) == 0 ||
                strncmp(path, "/api/v1/developer/rewrite-rules/", 32U) == 0)) {
        const char *prefix = strncmp(
            path, "/api/v1/developer/map-rules/", 28U) == 0 ?
            "/api/v1/developer/map-rules/" :
            (strncmp(path, "/api/v1/developer/breakpoint-rules/", 35U) == 0 ?
                "/api/v1/developer/breakpoint-rules/" :
                "/api/v1/developer/rewrite-rules/");
        const char *operation = prefix[18] == 'm' ?
            "delete_developer_map_rule" :
            (prefix[18] == 'b' ? "delete_developer_breakpoint_rule" :
                                "delete_developer_rewrite_rule");
        char *request = ch_api_path_id_request_json(path, prefix, &error);
        if (request == NULL) {
            ch_api_runtime_error(client, error.code, &error);
            free(profile_request);
            free(path);
            return;
        }
        persistence_required = 1;
        status = ch_runtime_mutate(client->server->runtime, operation,
                                   request, &json, &error);
        free(request);
    } else if (strcmp(method, "POST") == 0 &&
               (strcmp(path, "/api/v1/rule-sets/refresh") == 0 ||
                strcmp(path, "/api/v1/rule-subscriptions/refresh") == 0)) {
        persistence_required = 1;
        status = ch_runtime_mutate(
            client->server->runtime,
            strcmp(path, "/api/v1/rule-sets/refresh") == 0 ?
                "refresh_rule_sets" : "refresh_rule_subscriptions",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "POST") == 0 &&
               (strcmp(path, "/api/v1/rules/test") == 0 ||
                strcmp(path, "/api/v1/routes/explain") == 0)) {
        status = ch_runtime_query(client->server->runtime, "test_rule",
                                  client->body.data == NULL ? "{}" : client->body.data,
                                  &json, &error);
    } else if (strcmp(method, "POST") == 0 &&
               strcmp(path, "/api/v1/rules/from-connection") == 0) {
        persistence_required = 1;
        status = ch_runtime_mutate(
            client->server->runtime, "create_rule_from_connection",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "POST") == 0 &&
               strcmp(path,
                      "/api/v1/rules/temporary/from-connection") == 0) {
        status = ch_runtime_mutate(
            client->server->runtime,
            "create_temporary_rule_from_connection",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "POST") == 0 &&
               strncmp(path, "/api/v1/prompts/decisions/", 26U) == 0) {
        char *request = ch_api_path_action_request_json(
            path, "/api/v1/prompts/decisions/", "/promote",
            client->body.data, &error);
        if (request == NULL) {
            ch_api_runtime_error(client, error.code, &error);
            free(traffic_request);
            free(profile_request);
            free(path);
            return;
        }
        persistence_required = strstr(request, "\"scope\":\"forever\"") !=
            NULL;
        status = ch_runtime_mutate(client->server->runtime,
                                   "promote_silent_decision", request,
                                   &json, &error);
        free(request);
    } else if (strcmp(method, "POST") == 0 &&
               strncmp(path, "/api/v1/prompts/", 16U) == 0) {
        char *request = ch_api_path_action_request_json(
            path, "/api/v1/prompts/", "/resolve", client->body.data,
            &error);
        if (request == NULL) {
            ch_api_runtime_error(client, error.code, &error);
            free(traffic_request);
            free(profile_request);
            free(path);
            return;
        }
        persistence_required = strstr(request, "\"scope\":\"forever\"") !=
            NULL;
        status = ch_runtime_mutate(client->server->runtime,
                                   "resolve_prompt", request, &json,
                                   &error);
        free(request);
    } else if (strcmp(method, "POST") == 0 &&
               strcmp(path, "/api/v1/rules/cleanup") == 0) {
        persistence_required = 1;
        status = ch_runtime_mutate(
            client->server->runtime, "cleanup_rule_from_traffic",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "DELETE") == 0 &&
               strncmp(path, "/api/v1/rules/temporary/", 24U) == 0) {
        static const char prefix[] = "/api/v1/rules/temporary/";
        char *request = ch_api_path_id_request_json(path, prefix, &error);
        if (request == NULL) {
            ch_api_runtime_error(client, error.code, &error);
            free(traffic_request);
            free(profile_request);
            free(path);
            return;
        }
        status = ch_runtime_mutate(
            client->server->runtime, "remove_temporary_rule", request,
            &json, &error);
        free(request);
    } else if (strcmp(method, "GET") == 0 &&
               strcmp(path, "/api/v1/config/export") == 0) {
        config_transfer = 1;
        response_type = "text/plain; charset=utf-8";
        content_disposition = "attachment; filename=\"clambhook.toml\"";
        status = ch_runtime_query(client->server->runtime, "config_export",
                                  "{}", &json, &error);
    } else if (strcmp(method, "POST") == 0 &&
               strcmp(path, "/api/v1/config/import") == 0) {
        config_transfer = 1;
        status = ch_runtime_mutate(
            client->server->runtime, "config_import",
            client->body.data == NULL ? "" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "PUT") == 0 &&
               strcmp(path, "/api/v1/profiles/active") == 0) {
        status = ch_runtime_mutate(
            client->server->runtime, "persist_active_profile",
            client->body.data == NULL ? "{}" : client->body.data,
            &json, &error);
    } else if (strcmp(method, "POST") == 0 && strcmp(path, "/api/v1/connect") == 0) {
        status = ch_runtime_mutate(client->server->runtime, "connect", client->body.data, &json, &error);
    } else if (strcmp(method, "POST") == 0 && strcmp(path, "/api/v1/disconnect") == 0) {
        status = ch_runtime_mutate(client->server->runtime, "disconnect", client->body.data, &json, &error);
    } else {
        int known = strcmp(path, "/api/v1/status") == 0 || strcmp(path, "/api/v1/profiles") == 0 ||
            strcmp(path, "/api/v1/servers") == 0 || strcmp(path, "/api/v1/rules") == 0 ||
            strcmp(path, "/api/v1/traffic") == 0 ||
            strcmp(path, "/api/v1/decisions") == 0 ||
            strcmp(path, "/api/v1/events") == 0 ||
            strcmp(path, "/api/v1/events/snapshot") == 0 ||
            strcmp(path, "/api/v1/policy-groups") == 0 || strcmp(path, "/api/v1/rule-sets") == 0 ||
            strcmp(path, "/api/v1/policy-groups/test") == 0 ||
            strcmp(path, "/api/v1/dns") == 0 ||
            strcmp(path, "/api/v1/config/settings") == 0 ||
            strcmp(path, "/api/v1/conditioner") == 0 ||
            strcmp(path, "/api/v1/developer/status") == 0 ||
            strcmp(path, "/api/v1/developer/ca.pem") == 0 ||
            strcmp(path, "/api/v1/developer/ca/regenerate") == 0 ||
            strcmp(path, "/api/v1/developer/breakpoints/pending") == 0 ||
            strncmp(path, "/api/v1/developer/breakpoints/", 30U) == 0 ||
            strcmp(path, "/api/v1/developer/entries") == 0 ||
            strncmp(path, "/api/v1/developer/entries/", 26U) == 0 ||
            strcmp(path, "/api/v1/developer/har") == 0 ||
            strcmp(path, "/api/v1/developer/curl/import") == 0 ||
            strcmp(path, "/api/v1/developer/send") == 0 ||
            strcmp(path, "/api/v1/developer/repeat") == 0 ||
            strcmp(path, "/api/v1/rule-subscriptions") == 0 ||
            strcmp(path, "/api/v1/prompts/pending") == 0 ||
            strcmp(path, "/api/v1/prompts/decisions") == 0 ||
            strncmp(path, "/api/v1/prompts/", 16U) == 0 ||
            strcmp(path, "/api/v1/policy-groups/selection") == 0 ||
            strcmp(path, "/api/v1/developer/settings") == 0 ||
            strcmp(path, "/api/v1/developer/map-rules") == 0 ||
            strcmp(path, "/api/v1/developer/breakpoint-rules") == 0 ||
            strcmp(path, "/api/v1/developer/rewrite-rules") == 0 ||
            strcmp(path, "/api/v1/rule-sets/refresh") == 0 ||
            strcmp(path, "/api/v1/rule-subscriptions/refresh") == 0 ||
            strcmp(path, "/api/v1/rules/test") == 0 || strcmp(path, "/api/v1/routes/explain") == 0 ||
            strcmp(path, "/api/v1/rules/from-connection") == 0 ||
            strcmp(path, "/api/v1/rules/temporary") == 0 ||
            strcmp(path, "/api/v1/rules/temporary/from-connection") == 0 ||
            strcmp(path, "/api/v1/rules/cleanup") == 0 ||
            strncmp(path, "/api/v1/rules/temporary/", 24U) == 0 ||
            strcmp(path, "/api/v1/config/export") == 0 ||
            strcmp(path, "/api/v1/config/import") == 0 ||
            strcmp(path, "/api/v1/outline/review") == 0 ||
            strcmp(path, "/api/v1/outline/import") == 0 ||
            strcmp(path, "/api/v1/outline/refresh") == 0 ||
            strcmp(path, "/api/v1/profiles/active") == 0 ||
            strcmp(path, "/api/v1/connect") == 0 || strcmp(path, "/api/v1/disconnect") == 0;
        ch_api_respond(client, known ? 405 : 404, NULL, known ? "method not allowed\n" : "not found\n");
        free(traffic_request);
        free(profile_request);
        free(path);
        return;
    }
    free(traffic_request);
    free(profile_request);
    free(path);
    if (status != CH_OK) {
        if ((config_transfer || persistence_required ||
             conflict_on_invalid_state) &&
            status == CH_ERROR_INVALID_STATE) {
            ch_json_buffer body;
            ch_json_init(&body);
            int okay = ch_json_append(&body, "{\"error\":") &&
                ch_json_append_string(&body, error.message) &&
                ch_json_append(&body, "}");
            char *encoded = okay ? ch_json_take(&body) : NULL;
            ch_json_dispose(&body);
            ch_api_respond(client, 409, "application/json",
                           encoded == NULL ?
                               "{\"error\":\"conflict\"}" : encoded);
            free(encoded);
        } else {
            ch_api_runtime_error(client, status, &error);
        }
    } else {
        ch_api_respond_with_headers(client, 200, response_type,
                                    content_disposition, json);
    }
    ch_string_free(json);
}

static int ch_api_append(ch_json_buffer *buffer, const char *at, size_t length) {
    if (length > CH_API_MAX_REQUEST_BYTES || buffer->length > CH_API_MAX_REQUEST_BYTES - length) return HPE_USER;
    return ch_json_append_bytes(buffer, at, length) ? 0 : HPE_USER;
}

static int ch_api_on_url(llhttp_t *parser, const char *at, size_t length) {
    return ch_api_append(&((ch_api_client *)parser->data)->url, at, length);
}

static int ch_api_on_field(llhttp_t *parser, const char *at, size_t length) {
    return ch_api_append(&((ch_api_client *)parser->data)->field, at, length);
}

static int ch_api_on_value(llhttp_t *parser, const char *at, size_t length) {
    return ch_api_append(&((ch_api_client *)parser->data)->value, at, length);
}

static int ch_api_on_value_complete(llhttp_t *parser) {
    ch_api_client *client = parser->data;
    const char *field = client->field.data == NULL ? "" : client->field.data;
    const char *value = client->value.data == NULL ? "" : client->value.data;
    char **destination = NULL;
    if (ch_ascii_equal(field, "authorization")) destination = &client->authorization;
    else if (ch_ascii_equal(field, "host")) destination = &client->host;
    else if (ch_ascii_equal(field, "origin")) destination = &client->origin;
    else if (ch_ascii_equal(field, "connection")) destination = &client->connection;
    else if (ch_ascii_equal(field, "upgrade")) destination = &client->upgrade;
    else if (ch_ascii_equal(field, "sec-websocket-key")) destination = &client->websocket_key;
    else if (ch_ascii_equal(field, "sec-websocket-version")) destination = &client->websocket_version;
    if (destination != NULL) {
        free(*destination);
        *destination = ch_strdup(value);
        if (*destination == NULL) return HPE_USER;
    }
    ch_json_dispose(&client->field);
    ch_json_dispose(&client->value);
    ch_json_init(&client->field);
    ch_json_init(&client->value);
    return 0;
}

static int ch_api_on_body(llhttp_t *parser, const char *at, size_t length) {
    ch_api_client *client = parser->data;
    const char *url = client->url.data == NULL ? "" : client->url.data;
    static const char import_path[] = "/api/v1/config/import";
    size_t import_length = sizeof(import_path) - 1U;
    int is_import = strncmp(url, import_path, import_length) == 0 &&
        (url[import_length] == '\0' || url[import_length] == '?');
    size_t limit = is_import ? CH_API_MAX_CONFIG_TRANSFER_BYTES :
        CH_API_MAX_REQUEST_BYTES;
    if (memchr(at, '\0', length) != NULL || length > limit ||
        client->body.length > limit - length) {
        return HPE_USER;
    }
    return ch_json_append_bytes(&client->body, at, length) ? 0 : HPE_USER;
}

static int ch_api_on_complete(llhttp_t *parser) {
    ch_api_route(parser->data);
    return 0;
}

static void ch_api_alloc(uv_handle_t *handle, size_t suggested, uv_buf_t *buffer) {
    (void)handle;
    size_t size = suggested == 0U ? 4096U : suggested;
    buffer->base = malloc(size);
    buffer->len = buffer->base == NULL ? 0U : size;
}

static void ch_api_read(uv_stream_t *stream, ssize_t count, const uv_buf_t *buffer) {
    ch_api_client *client = stream->data;
    if (count > 0) {
        llhttp_errno_t result = llhttp_execute(&client->parser, buffer->base, (size_t)count);
        if (result != HPE_OK && !client->responded) {
            ch_api_respond(client, 400, NULL, "invalid HTTP request\n");
        }
    } else if (count < 0) {
        ch_api_close_client(client);
    }
    free(buffer->base);
}

static void ch_api_accept(uv_stream_t *listener, int status) {
    if (status < 0) return;
    ch_api_server *server = listener->data;
    ch_api_client *client = calloc(1U, sizeof(*client));
    if (client == NULL) return;
    client->server = server;
    ch_json_init(&client->url);
    ch_json_init(&client->field);
    ch_json_init(&client->value);
    ch_json_init(&client->body);
    llhttp_settings_init(&client->settings);
    client->settings.on_url = ch_api_on_url;
    client->settings.on_header_field = ch_api_on_field;
    client->settings.on_header_value = ch_api_on_value;
    client->settings.on_header_value_complete = ch_api_on_value_complete;
    client->settings.on_body = ch_api_on_body;
    client->settings.on_message_complete = ch_api_on_complete;
    llhttp_init(&client->parser, HTTP_REQUEST, &client->settings);
    client->parser.data = client;
    if (uv_tcp_init(server->loop, &client->stream) != 0) {
        free(client);
        return;
    }
    client->stream.data = client;
    client->next = server->clients;
    server->clients = client;
    ++server->client_count;
    if (uv_accept(listener, (uv_stream_t *)&client->stream) != 0 ||
        uv_read_start((uv_stream_t *)&client->stream, ch_api_alloc, ch_api_read) != 0) {
        ch_api_close_client(client);
    }
}

static ch_status ch_api_parse_address(
    const char *address,
    struct sockaddr_storage *socket_address,
    char **rendered,
    ch_error *error
) {
    char *copy = ch_strdup(address == NULL ? "" : address);
    if (copy == NULL) return CH_ERROR_OUT_OF_MEMORY;
    char *host = copy;
    char *port_text = NULL;
    if (copy[0] == '[') {
        char *end = strchr(copy, ']');
        if (end != NULL && end[1] == ':') { *end = '\0'; host = copy + 1; port_text = end + 2; }
    } else {
        char *separator = strrchr(copy, ':');
        if (separator != NULL) { *separator = '\0'; port_text = separator + 1; }
    }
    char *port_end = NULL;
    long port = port_text == NULL ? -1L : strtol(port_text, &port_end, 10);
    if (port < 0L || port > 65535L || port_end == port_text || *port_end != '\0') {
        free(copy); ch_error_set(error, CH_ERROR_PARSE, "invalid API listen address"); return CH_ERROR_PARSE;
    }
    if (strcmp(host, "localhost") == 0) host = "127.0.0.1";
    int result = uv_ip4_addr(host, (int)port, (struct sockaddr_in *)socket_address);
    if (result != 0) result = uv_ip6_addr(host, (int)port, (struct sockaddr_in6 *)socket_address);
    if (result != 0) {
        free(copy); ch_error_set(error, CH_ERROR_PARSE, "invalid API listen host"); return CH_ERROR_PARSE;
    }
    *rendered = ch_strdup(address);
    free(copy);
    return *rendered == NULL ? CH_ERROR_OUT_OF_MEMORY : CH_OK;
}

static ch_status ch_api_capture_bound_address(ch_api_server *server, ch_error *error) {
    struct sockaddr_storage address;
    int address_length = (int)sizeof(address);
    int result = uv_tcp_getsockname(
        &server->listener, (struct sockaddr *)&address, &address_length
    );
    if (result != 0) {
        ch_error_set(error, CH_ERROR_IO, "read bound API address: %s", uv_strerror(result));
        return CH_ERROR_IO;
    }
    char host[INET6_ADDRSTRLEN];
    int port = 0;
    int wildcard = 0;
    if (address.ss_family == AF_INET) {
        const struct sockaddr_in *ipv4 = (const struct sockaddr_in *)&address;
        result = uv_ip4_name(ipv4, host, sizeof(host));
        port = (int)ntohs(ipv4->sin_port);
        wildcard = ipv4->sin_addr.s_addr == htonl(INADDR_ANY);
    } else if (address.ss_family == AF_INET6) {
        const struct sockaddr_in6 *ipv6 = (const struct sockaddr_in6 *)&address;
        static const struct in6_addr unspecified = IN6ADDR_ANY_INIT;
        result = uv_ip6_name(ipv6, host, sizeof(host));
        port = (int)ntohs(ipv6->sin6_port);
        wildcard = memcmp(&ipv6->sin6_addr, &unspecified, sizeof(unspecified)) == 0;
    } else {
        ch_error_set(error, CH_ERROR_INTERNAL, "bound API address has an unknown family");
        return CH_ERROR_INTERNAL;
    }
    if (result != 0) {
        ch_error_set(error, CH_ERROR_IO, "format bound API address: %s", uv_strerror(result));
        return CH_ERROR_IO;
    }
    ch_json_buffer rendered;
    ch_json_init(&rendered);
    int ok = address.ss_family == AF_INET6
        ? ch_json_append_format(&rendered, "[%s]:%d", host, port)
        : ch_json_append_format(&rendered, "%s:%d", host, port);
    char *text = ok ? ch_json_take(&rendered) : NULL;
    ch_json_dispose(&rendered);
    if (text == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "format bound API address");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    free(server->address);
    server->address = text;
    (void)snprintf(server->bind_host, sizeof(server->bind_host), "%s", host);
    server->bind_port = port;
    server->wildcard_bind = wildcard;
    return CH_OK;
}

static void ch_api_startup_listener_closed(uv_handle_t *handle) {
    int *closed = handle->data;
    *closed = 1;
}

ch_api_server *ch_api_server_start(
    uv_loop_t *loop,
    ch_runtime *runtime,
    const char *address,
    const char *auth_token,
    const char *license_path,
    ch_error *error
) {
    ch_error_clear(error);
    if (loop == NULL || runtime == NULL || address == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "loop, runtime, and API address are required");
        return NULL;
    }
    ch_api_server *server = calloc(1U, sizeof(*server));
    if (server == NULL) { ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate API server"); return NULL; }
    server->loop = loop;
    server->runtime = runtime;
    server->auth_token = ch_strdup(auth_token == NULL ? "" : auth_token);
    server->license_path = ch_strdup(license_path == NULL ? "" : license_path);
    struct sockaddr_storage socket_address;
    ch_status parsed = ch_api_parse_address(address, &socket_address, &server->address, error);
    if (server->auth_token == NULL || server->license_path == NULL ||
        parsed != CH_OK) {
        free(server->auth_token); free(server->license_path);
        free(server->address); free(server); return NULL;
    }
    if (!ch_api_is_loopback_host(address) && server->auth_token[0] == '\0') {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "non-loopback API listen requires an authentication token");
        free(server->auth_token); free(server->license_path);
        free(server->address); free(server); return NULL;
    }
    int result = uv_tcp_init(loop, &server->listener);
    if (result == 0) server->listener.data = server;
    if (result == 0) result = uv_tcp_bind(&server->listener, (const struct sockaddr *)&socket_address, 0U);
    if (result == 0) result = uv_listen((uv_stream_t *)&server->listener, 128, ch_api_accept);
    ch_status capture_status = result == 0
        ? ch_api_capture_bound_address(server, error)
        : CH_ERROR_IO;
    if (result != 0 || capture_status != CH_OK) {
        if (result != 0) {
            ch_error_set(error, CH_ERROR_IO, "bind API listener %s: %s", address, uv_strerror(result));
        }
        if (server->listener.loop != NULL) {
            int closed = 0;
            server->listener.data = &closed;
            uv_close((uv_handle_t *)&server->listener, ch_api_startup_listener_closed);
            while (!closed) (void)uv_run(loop, UV_RUN_NOWAIT);
        }
        free(server->auth_token); free(server->license_path);
        free(server->address); free(server); return NULL;
    }
    return server;
}

static void ch_api_listener_closed(uv_handle_t *handle) {
    ch_api_server *server = handle->data;
    server->listener_closed = 1;
    ch_api_server_maybe_free(server);
}

void ch_api_server_stop(ch_api_server *server) {
    if (server == NULL || server->closing) return;
    server->closing = 1;
    for (ch_api_client *client = server->clients; client != NULL; client = client->next) {
        ch_api_close_client(client);
    }
    if (!uv_is_closing((uv_handle_t *)&server->listener)) {
        uv_close((uv_handle_t *)&server->listener, ch_api_listener_closed);
    }
}

const char *ch_api_server_address(const ch_api_server *server) {
    return server == NULL ? "" : server->address;
}
