#include "api_server.h"

#include <arpa/inet.h>
#include <ctype.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>

#include <llhttp.h>
#include <sodium.h>

#include "internal.h"

#define CH_API_MAX_REQUEST_BYTES (1024U * 1024U)
#define CH_API_MAX_CONFIG_TRANSFER_BYTES (4U * 1024U * 1024U)

typedef struct ch_api_client ch_api_client;

struct ch_api_server {
    uv_loop_t *loop;
    ch_runtime *runtime;
    uv_tcp_t listener;
    char *auth_token;
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
    int responded;
    ch_api_client *next;
};

typedef struct ch_api_write {
    uv_write_t request;
    char *bytes;
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
        free(server->address);
        free(server);
    }
}

static void ch_api_client_unlink(ch_api_client *client) {
    ch_api_client **cursor = &client->server->clients;
    while (*cursor != NULL) {
        if (*cursor == client) {
            *cursor = client->next;
            --client->server->client_count;
            return;
        }
        cursor = &(*cursor)->next;
    }
}

static void ch_api_client_closed(uv_handle_t *handle) {
    ch_api_client *client = handle->data;
    ch_api_server *server = client->server;
    ch_api_client_unlink(client);
    ch_json_dispose(&client->url);
    ch_json_dispose(&client->field);
    ch_json_dispose(&client->value);
    ch_json_dispose(&client->body);
    free(client->authorization);
    free(client->host);
    free(client->origin);
    free(client);
    ch_api_server_maybe_free(server);
}

static void ch_api_close_client(ch_api_client *client) {
    if (!uv_is_closing((uv_handle_t *)&client->stream)) {
        (void)uv_read_stop((uv_stream_t *)&client->stream);
        uv_close((uv_handle_t *)&client->stream, ch_api_client_closed);
    }
}

static void ch_api_write_finished(uv_write_t *request, int status) {
    (void)status;
    ch_api_write *write = request->data;
    ch_api_client *client = request->handle->data;
    free(write->bytes);
    free(write);
    ch_api_close_client(client);
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
    char *json = NULL;
    const char *response_type = "application/json";
    const char *content_disposition = NULL;
    int config_transfer = 0;
    int persistence_required = 0;
    ch_error error;
    ch_status status;
    char *profile_request = ch_api_profile_request_json(url, &error);
    if (profile_request == NULL) {
        ch_api_runtime_error(client, error.code, &error);
        free(path);
        return;
    }
    if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/status") == 0) {
        status = ch_runtime_query(client->server->runtime, "status", "{}", &json, &error);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/profiles") == 0) {
        status = ch_runtime_query(client->server->runtime, "profiles", "{}", &json, &error);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/servers") == 0) {
        status = ch_runtime_query(client->server->runtime, "servers", profile_request, &json, &error);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/rules") == 0) {
        status = ch_runtime_query(client->server->runtime, "rules", profile_request, &json, &error);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/policy-groups") == 0) {
        status = ch_runtime_query(client->server->runtime, "policy_groups", profile_request, &json, &error);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/rule-sets") == 0) {
        status = ch_runtime_query(client->server->runtime, "rule_sets", profile_request, &json, &error);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/dns") == 0) {
        status = ch_runtime_query(client->server->runtime, "dns", profile_request, &json, &error);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/config/settings") == 0) {
        status = ch_runtime_query(client->server->runtime, "config_settings", profile_request, &json, &error);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/conditioner") == 0) {
        status = ch_runtime_query(client->server->runtime, "conditioner", profile_request, &json, &error);
    } else if (strcmp(method, "GET") == 0 && strcmp(path, "/api/v1/rule-subscriptions") == 0) {
        status = ch_runtime_query(client->server->runtime, "rule_subscriptions", profile_request, &json, &error);
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
    } else if (strcmp(method, "POST") == 0 &&
               (strcmp(path, "/api/v1/rules/test") == 0 ||
                strcmp(path, "/api/v1/routes/explain") == 0)) {
        status = ch_runtime_query(client->server->runtime, "test_rule",
                                  client->body.data == NULL ? "{}" : client->body.data,
                                  &json, &error);
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
            strcmp(path, "/api/v1/policy-groups") == 0 || strcmp(path, "/api/v1/rule-sets") == 0 ||
            strcmp(path, "/api/v1/dns") == 0 ||
            strcmp(path, "/api/v1/config/settings") == 0 ||
            strcmp(path, "/api/v1/conditioner") == 0 ||
            strcmp(path, "/api/v1/rule-subscriptions") == 0 ||
            strcmp(path, "/api/v1/rules/test") == 0 || strcmp(path, "/api/v1/routes/explain") == 0 ||
            strcmp(path, "/api/v1/config/export") == 0 ||
            strcmp(path, "/api/v1/config/import") == 0 ||
            strcmp(path, "/api/v1/profiles/active") == 0 ||
            strcmp(path, "/api/v1/connect") == 0 || strcmp(path, "/api/v1/disconnect") == 0;
        ch_api_respond(client, known ? 405 : 404, NULL, known ? "method not allowed\n" : "not found\n");
        free(profile_request);
        free(path);
        return;
    }
    free(profile_request);
    free(path);
    if (status != CH_OK) {
        if ((config_transfer || persistence_required) &&
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
    struct sockaddr_storage socket_address;
    ch_status parsed = ch_api_parse_address(address, &socket_address, &server->address, error);
    if (server->auth_token == NULL || parsed != CH_OK) {
        free(server->auth_token); free(server->address); free(server); return NULL;
    }
    if (!ch_api_is_loopback_host(address) && server->auth_token[0] == '\0') {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "non-loopback API listen requires an authentication token");
        free(server->auth_token); free(server->address); free(server); return NULL;
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
        free(server->auth_token); free(server->address); free(server); return NULL;
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
