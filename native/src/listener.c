#include "clambhook/listener.h"

#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <netdb.h>
#include <poll.h>
#include <pthread.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <sys/types.h>
#include <unistd.h>

#include <sodium.h>

#include "internal.h"

#define CH_LISTENER_DEFAULT_HANDSHAKE_MS 30000U
#define CH_HTTP_MAX_HEADER_BYTES (1024U * 1024U)

typedef struct ch_listener_connection ch_listener_connection;

struct ch_proxy_listener {
    ch_proxy_listener_protocol protocol;
    char *configured_address;
    char *bound_address;
    int authentication_required;
    char *username;
    char *password;
    size_t maximum_connections;
    unsigned int handshake_timeout_milliseconds;
    ch_proxy_dial_callback dial;
    void *dial_context;
    int descriptor;
    pthread_t accept_thread;
    int accept_thread_started;
    pthread_mutex_t mutex;
    pthread_cond_t condition;
    int stopping;
    size_t active_connections;
    ch_listener_connection *connections;
};

struct ch_listener_connection {
    ch_proxy_listener *listener;
    int client_descriptor;
    int remote_descriptor;
    ch_listener_connection *next;
};

typedef struct ch_relay_direction {
    int source;
    int destination;
} ch_relay_direction;

static void ch_listener_close_descriptor(int *descriptor) {
    if (descriptor == NULL || *descriptor < 0) return;
    (void)shutdown(*descriptor, SHUT_RDWR);
    (void)close(*descriptor);
    *descriptor = -1;
}

static void ch_listener_disable_sigpipe(int descriptor) {
#ifdef SO_NOSIGPIPE
    int enabled = 1;
    (void)setsockopt(descriptor, SOL_SOCKET, SO_NOSIGPIPE, &enabled,
                     (socklen_t)sizeof(enabled));
#else
    (void)descriptor;
#endif
}

static ssize_t ch_listener_send(int descriptor, const void *bytes, size_t length) {
#ifdef MSG_NOSIGNAL
    return send(descriptor, bytes, length, MSG_NOSIGNAL);
#else
    return send(descriptor, bytes, length, 0);
#endif
}

static int ch_listener_send_all(int descriptor, const void *bytes, size_t length) {
    const uint8_t *cursor = bytes;
    while (length > 0U) {
        ssize_t written = ch_listener_send(descriptor, cursor, length);
        if (written < 0 && errno == EINTR) continue;
        if (written <= 0) return 0;
        cursor += (size_t)written;
        length -= (size_t)written;
    }
    return 1;
}

static int ch_listener_receive_exact(int descriptor, void *bytes, size_t length) {
    uint8_t *cursor = bytes;
    while (length > 0U) {
        ssize_t received = recv(descriptor, cursor, length, 0);
        if (received < 0 && errno == EINTR) continue;
        if (received <= 0) return 0;
        cursor += (size_t)received;
        length -= (size_t)received;
    }
    return 1;
}

static void ch_listener_set_timeout(int descriptor, unsigned int milliseconds) {
    struct timeval timeout = {
        .tv_sec = (time_t)(milliseconds / 1000U),
        .tv_usec = (suseconds_t)((milliseconds % 1000U) * 1000U)
    };
    (void)setsockopt(descriptor, SOL_SOCKET, SO_RCVTIMEO, &timeout,
                     (socklen_t)sizeof(timeout));
    (void)setsockopt(descriptor, SOL_SOCKET, SO_SNDTIMEO, &timeout,
                     (socklen_t)sizeof(timeout));
}

static void ch_listener_clear_timeout(int descriptor) {
    ch_listener_set_timeout(descriptor, 0U);
}

static int ch_listener_constant_equal(const char *left, const char *right) {
    if (left == NULL) left = "";
    if (right == NULL) right = "";
    size_t left_length = strlen(left);
    size_t right_length = strlen(right);
    if (left_length != right_length) return 0;
    return sodium_memcmp(left, right, left_length) == 0;
}

static char *ch_listener_socket_address(int descriptor, int peer) {
    struct sockaddr_storage address;
    socklen_t length = (socklen_t)sizeof(address);
    int status = peer ? getpeername(descriptor, (struct sockaddr *)&address, &length) :
                        getsockname(descriptor, (struct sockaddr *)&address, &length);
    if (status != 0) return ch_strdup("");
    char host[INET6_ADDRSTRLEN];
    char service[6];
    if (getnameinfo((struct sockaddr *)&address, length, host, sizeof(host),
                    service, sizeof(service), NI_NUMERICHOST | NI_NUMERICSERV) != 0) {
        return ch_strdup("");
    }
    int ipv6 = address.ss_family == AF_INET6;
    size_t needed = strlen(host) + strlen(service) + (ipv6 ? 4U : 2U);
    char *result = malloc(needed);
    if (result == NULL) return NULL;
    (void)snprintf(result, needed, ipv6 ? "[%s]:%s" : "%s:%s", host, service);
    return result;
}

static ch_status ch_listener_split_address(const char *address,
                                           char **out_host,
                                           char **out_service,
                                           ch_error *error) {
    *out_host = NULL;
    *out_service = NULL;
    if (address == NULL || address[0] == '\0') {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "listener address is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    const char *host_start = address;
    const char *host_end;
    const char *service_start;
    if (address[0] == '[') {
        host_start = address + 1;
        host_end = strchr(host_start, ']');
        if (host_end == NULL || host_end[1] != ':' || host_end[2] == '\0') {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "invalid bracketed listener address");
            return CH_ERROR_INVALID_ARGUMENT;
        }
        service_start = host_end + 2;
    } else {
        const char *separator = strrchr(address, ':');
        if (separator == NULL || separator[1] == '\0') {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "listener address must be host:port");
            return CH_ERROR_INVALID_ARGUMENT;
        }
        host_end = separator;
        service_start = separator + 1;
    }
    size_t host_length = (size_t)(host_end - host_start);
    *out_host = malloc(host_length + 1U);
    *out_service = ch_strdup(service_start);
    if (*out_host == NULL || *out_service == NULL) {
        free(*out_host);
        free(*out_service);
        *out_host = NULL;
        *out_service = NULL;
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy listener address");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    memcpy(*out_host, host_start, host_length);
    (*out_host)[host_length] = '\0';
    return CH_OK;
}

static int ch_listener_open_socket(const char *address, char **bound_address,
                                   ch_error *error) {
    char *host = NULL;
    char *service = NULL;
    if (ch_listener_split_address(address, &host, &service, error) != CH_OK) return -1;
    struct addrinfo hints;
    memset(&hints, 0, sizeof(hints));
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_STREAM;
    hints.ai_protocol = IPPROTO_TCP;
    hints.ai_flags = AI_PASSIVE;
    struct addrinfo *addresses = NULL;
    int lookup = getaddrinfo(host[0] == '\0' ? NULL : host, service, &hints, &addresses);
    free(host);
    free(service);
    if (lookup != 0) {
        ch_error_set(error, CH_ERROR_IO, "resolve listener address: %s", gai_strerror(lookup));
        return -1;
    }
    int descriptor = -1;
    int saved_error = EADDRNOTAVAIL;
    for (const struct addrinfo *candidate = addresses; candidate != NULL;
         candidate = candidate->ai_next) {
        descriptor = socket(candidate->ai_family, candidate->ai_socktype,
                            candidate->ai_protocol);
        if (descriptor < 0) {
            saved_error = errno;
            continue;
        }
        int enabled = 1;
        (void)setsockopt(descriptor, SOL_SOCKET, SO_REUSEADDR, &enabled,
                         (socklen_t)sizeof(enabled));
        ch_listener_disable_sigpipe(descriptor);
        if (bind(descriptor, candidate->ai_addr, candidate->ai_addrlen) == 0 &&
            listen(descriptor, 128) == 0) {
            break;
        }
        saved_error = errno;
        (void)close(descriptor);
        descriptor = -1;
    }
    freeaddrinfo(addresses);
    if (descriptor < 0) {
        ch_error_set(error, CH_ERROR_IO, "listen %s: %s", address, strerror(saved_error));
        return -1;
    }
    *bound_address = ch_listener_socket_address(descriptor, 0);
    if (*bound_address == NULL) {
        (void)close(descriptor);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy bound listener address");
        return -1;
    }
    return descriptor;
}

static void *ch_listener_relay_direction(void *context) {
    ch_relay_direction *direction = context;
    uint8_t buffer[32768];
    for (;;) {
        ssize_t received = recv(direction->source, buffer, sizeof(buffer), 0);
        if (received < 0 && errno == EINTR) continue;
        if (received <= 0 ||
            !ch_listener_send_all(direction->destination, buffer, (size_t)received)) break;
    }
    (void)shutdown(direction->destination, SHUT_WR);
    return NULL;
}

static void ch_listener_relay(int client, int remote) {
    ch_relay_direction outgoing = {.source = client, .destination = remote};
    ch_relay_direction incoming = {.source = remote, .destination = client};
    pthread_t incoming_thread;
    int incoming_started = pthread_create(&incoming_thread, NULL,
                                           ch_listener_relay_direction, &incoming) == 0;
    (void)ch_listener_relay_direction(&outgoing);
    if (incoming_started) {
        (void)pthread_join(incoming_thread, NULL);
    } else {
        (void)shutdown(remote, SHUT_RDWR);
    }
}

static ch_status ch_listener_dial(ch_listener_connection *connection,
                                  const char *target,
                                  ch_proxy_route *route,
                                  ch_error *error) {
    ch_proxy_listener *listener = connection->listener;
    char *source = ch_listener_socket_address(connection->client_descriptor, 1);
    if (source == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy client address");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    int remote = -1;
    memset(route, 0, sizeof(*route));
    ch_status status = listener->dial("tcp", target, source, route, &remote,
                                      listener->dial_context, error);
    free(source);
    if (status != CH_OK) {
        ch_listener_close_descriptor(&remote);
        return status;
    }
    if (route->action == 0) route->action = CH_PROXY_ROUTE_CONNECT;
    if (route->action != CH_PROXY_ROUTE_CONNECT) {
        ch_listener_close_descriptor(&remote);
        return CH_OK;
    }
    if (remote < 0) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "proxy dial callback returned no connection");
        return CH_ERROR_INVALID_STATE;
    }
    ch_listener_disable_sigpipe(remote);
    pthread_mutex_lock(&listener->mutex);
    connection->remote_descriptor = remote;
    pthread_mutex_unlock(&listener->mutex);
    return CH_OK;
}

static int ch_socks_write_reply(int descriptor, uint8_t reply) {
    uint8_t bytes[] = {0x05U, reply, 0x00U, 0x01U, 0x00U, 0x00U,
                       0x00U, 0x00U, 0x00U, 0x00U};
    return ch_listener_send_all(descriptor, bytes, sizeof(bytes));
}

static int ch_socks_negotiate(ch_listener_connection *connection) {
    ch_proxy_listener *listener = connection->listener;
    int descriptor = connection->client_descriptor;
    uint8_t greeting[2];
    if (!ch_listener_receive_exact(descriptor, greeting, sizeof(greeting)) ||
        greeting[0] != 0x05U || greeting[1] == 0U) return 0;
    uint8_t methods[255];
    size_t method_count = greeting[1];
    if (!ch_listener_receive_exact(descriptor, methods, method_count)) return 0;
    uint8_t wanted = listener->authentication_required ? 0x02U : 0x00U;
    int offered = 0;
    for (size_t index = 0U; index < method_count; ++index) {
        if (methods[index] == wanted) offered = 1;
    }
    uint8_t selection[] = {0x05U, offered ? wanted : 0xffU};
    if (!ch_listener_send_all(descriptor, selection, sizeof(selection)) || !offered) return 0;
    if (!listener->authentication_required) return 1;
    uint8_t header[2];
    if (!ch_listener_receive_exact(descriptor, header, sizeof(header)) || header[0] != 0x01U) return 0;
    char username[256];
    char password[256];
    size_t username_length = header[1];
    if (!ch_listener_receive_exact(descriptor, username, username_length)) return 0;
    username[username_length] = '\0';
    uint8_t password_length;
    if (!ch_listener_receive_exact(descriptor, &password_length, 1U) ||
        !ch_listener_receive_exact(descriptor, password, password_length)) return 0;
    password[password_length] = '\0';
    int accepted = ch_listener_constant_equal(username, listener->username) &
                   ch_listener_constant_equal(password, listener->password);
    uint8_t response[] = {0x01U, accepted ? 0x00U : 0x01U};
    return ch_listener_send_all(descriptor, response, sizeof(response)) && accepted;
}

static char *ch_socks_read_target(int descriptor, uint8_t *command,
                                  uint8_t *reply) {
    uint8_t header[4];
    *reply = 0x01U;
    if (!ch_listener_receive_exact(descriptor, header, sizeof(header)) ||
        header[0] != 0x05U) return NULL;
    *command = header[1];
    char host[256];
    memset(host, 0, sizeof(host));
    if (header[3] == 0x01U) {
        uint8_t address[4];
        if (!ch_listener_receive_exact(descriptor, address, sizeof(address)) ||
            inet_ntop(AF_INET, address, host, sizeof(host)) == NULL) return NULL;
    } else if (header[3] == 0x04U) {
        uint8_t address[16];
        if (!ch_listener_receive_exact(descriptor, address, sizeof(address)) ||
            inet_ntop(AF_INET6, address, host, sizeof(host)) == NULL) return NULL;
    } else if (header[3] == 0x03U) {
        uint8_t length;
        if (!ch_listener_receive_exact(descriptor, &length, 1U) || length == 0U ||
            !ch_listener_receive_exact(descriptor, host, length)) return NULL;
        host[length] = '\0';
    } else {
        *reply = 0x08U;
        return NULL;
    }
    uint8_t port_bytes[2];
    if (!ch_listener_receive_exact(descriptor, port_bytes, sizeof(port_bytes))) return NULL;
    unsigned int port = (unsigned int)port_bytes[0] * 256U + (unsigned int)port_bytes[1];
    int ipv6 = header[3] == 0x04U;
    size_t needed = strlen(host) + 10U;
    char *target = malloc(needed);
    if (target == NULL) return NULL;
    (void)snprintf(target, needed, ipv6 ? "[%s]:%u" : "%s:%u", host, port);
    return target;
}

static void ch_socks_handle(ch_listener_connection *connection) {
    int client = connection->client_descriptor;
    if (!ch_socks_negotiate(connection)) return;
    uint8_t command = 0U;
    uint8_t reply = 0x01U;
    char *target = ch_socks_read_target(client, &command, &reply);
    if (target == NULL) {
        (void)ch_socks_write_reply(client, reply);
        return;
    }
    if (command != 0x01U) {
        (void)ch_socks_write_reply(client, 0x07U);
        free(target);
        return;
    }
    ch_proxy_route route;
    ch_error error;
    ch_status status = ch_listener_dial(connection, target, &route, &error);
    free(target);
    if (status != CH_OK) {
        (void)ch_socks_write_reply(client, 0x01U);
        return;
    }
    if (route.action != CH_PROXY_ROUTE_CONNECT) {
        (void)ch_socks_write_reply(client, 0x02U);
        return;
    }
    if (!ch_socks_write_reply(client, 0x00U)) return;
    ch_listener_clear_timeout(client);
    ch_listener_relay(client, connection->remote_descriptor);
}

static const char *ch_http_header_value(const char *headers,
                                        const char *header_end,
                                        const char *name,
                                        size_t *value_length) {
    size_t name_length = strlen(name);
    const char *line = headers;
    while (line < header_end) {
        const char *end = strstr(line, "\r\n");
        if (end == NULL || end > header_end) break;
        if ((size_t)(end - line) > name_length &&
            strncasecmp(line, name, name_length) == 0 && line[name_length] == ':') {
            const char *value = line + name_length + 1U;
            while (value < end && (*value == ' ' || *value == '\t')) ++value;
            const char *trimmed_end = end;
            while (trimmed_end > value &&
                   (trimmed_end[-1] == ' ' || trimmed_end[-1] == '\t')) --trimmed_end;
            *value_length = (size_t)(trimmed_end - value);
            return value;
        }
        line = end + 2;
    }
    return NULL;
}

static int ch_http_authorized(const ch_proxy_listener *listener,
                              const char *headers,
                              const char *header_end) {
    if (!listener->authentication_required) return 1;
    size_t length = 0U;
    const char *value = ch_http_header_value(headers, header_end,
                                             "Proxy-Authorization", &length);
    static const char prefix[] = "Basic ";
    if (value == NULL || length <= sizeof(prefix) - 1U ||
        strncasecmp(value, prefix, sizeof(prefix) - 1U) != 0) return 0;
    value += sizeof(prefix) - 1U;
    length -= sizeof(prefix) - 1U;
    size_t decoded_capacity = length + 1U;
    unsigned char *decoded = malloc(decoded_capacity);
    if (decoded == NULL) return 0;
    size_t decoded_length = 0U;
    int decoded_ok = sodium_base642bin(decoded, decoded_capacity,
        value, length, NULL, &decoded_length, NULL,
        sodium_base64_VARIANT_ORIGINAL) == 0;
    size_t expected_length = strlen(listener->username) + 1U + strlen(listener->password);
    char *expected = malloc(expected_length + 1U);
    if (expected == NULL) {
        free(decoded);
        return 0;
    }
    (void)snprintf(expected, expected_length + 1U, "%s:%s",
                   listener->username, listener->password);
    int accepted = decoded_ok && decoded_length == expected_length &&
        sodium_memcmp(decoded, expected, expected_length) == 0;
    sodium_memzero(decoded, decoded_capacity);
    sodium_memzero(expected, expected_length + 1U);
    free(decoded);
    free(expected);
    return accepted;
}

static void ch_http_status(int descriptor, int status, const char *reason,
                           int authenticate) {
    char response[512];
    int length = snprintf(response, sizeof(response),
        "HTTP/1.1 %d %s\r\nConnection: close\r\n%sContent-Length: 0\r\n\r\n",
        status, reason, authenticate ? "Proxy-Authenticate: Basic realm=\"clambhook\"\r\n" : "");
    if (length > 0 && (size_t)length < sizeof(response)) {
        (void)ch_listener_send_all(descriptor, response, (size_t)length);
    }
}

static char *ch_http_authority_target(const char *authority,
                                      const char *default_port) {
    if (authority == NULL || authority[0] == '\0') return NULL;
    if (authority[0] == '[') {
        const char *closing = strchr(authority, ']');
        if (closing == NULL) return NULL;
        if (closing[1] == ':' && closing[2] != '\0') return ch_strdup(authority);
        if (closing[1] != '\0') return NULL;
        size_t needed = strlen(authority) + strlen(default_port) + 2U;
        char *result = malloc(needed);
        if (result != NULL) (void)snprintf(result, needed, "%s:%s", authority, default_port);
        return result;
    }
    const char *colon = strrchr(authority, ':');
    if (colon != NULL && strchr(authority, ':') == colon && colon[1] != '\0') {
        return ch_strdup(authority);
    }
    if (colon != NULL) return NULL;
    size_t needed = strlen(authority) + strlen(default_port) + 2U;
    char *result = malloc(needed);
    if (result != NULL) (void)snprintf(result, needed, "%s:%s", authority, default_port);
    return result;
}

static int ch_http_header_is_proxy_only(const char *line, size_t length) {
    static const char authorization[] = "Proxy-Authorization:";
    static const char connection[] = "Proxy-Connection:";
    return (length >= sizeof(authorization) - 1U &&
            strncasecmp(line, authorization, sizeof(authorization) - 1U) == 0) ||
           (length >= sizeof(connection) - 1U &&
            strncasecmp(line, connection, sizeof(connection) - 1U) == 0);
}

static char *ch_http_forward_header(const char *method, const char *path,
                                    const char *version,
                                    const char *headers,
                                    const char *header_end) {
    ch_json_buffer output;
    ch_json_init(&output);
    if (!ch_json_append_format(&output, "%s %s %s\r\n", method, path, version)) {
        ch_json_dispose(&output);
        return NULL;
    }
    const char *line = headers;
    while (line < header_end) {
        const char *end = strstr(line, "\r\n");
        if (end == NULL || end > header_end) break;
        size_t length = (size_t)(end - line);
        if (length > 0U && !ch_http_header_is_proxy_only(line, length) &&
            !ch_json_append_format(&output, "%.*s\r\n", (int)length, line)) {
            ch_json_dispose(&output);
            return NULL;
        }
        line = end + 2;
    }
    if (!ch_json_append(&output, "\r\n")) {
        ch_json_dispose(&output);
        return NULL;
    }
    return ch_json_take(&output);
}

static void ch_http_handle(ch_listener_connection *connection) {
    int client = connection->client_descriptor;
    size_t capacity = 8192U;
    size_t length = 0U;
    char *request = malloc(capacity + 1U);
    if (request == NULL) return;
    char *header_end = NULL;
    while (header_end == NULL && length < CH_HTTP_MAX_HEADER_BYTES) {
        if (length == capacity) {
            size_t next_capacity = capacity > CH_HTTP_MAX_HEADER_BYTES / 2U ?
                CH_HTTP_MAX_HEADER_BYTES : capacity * 2U;
            char *next = realloc(request, next_capacity + 1U);
            if (next == NULL) break;
            request = next;
            capacity = next_capacity;
        }
        ssize_t received = recv(client, request + length, capacity - length, 0);
        if (received < 0 && errno == EINTR) continue;
        if (received <= 0) break;
        length += (size_t)received;
        request[length] = '\0';
        header_end = strstr(request, "\r\n\r\n");
    }
    if (header_end == NULL) {
        ch_http_status(client, 400, "Bad Request", 0);
        free(request);
        return;
    }
    const char *body_start = header_end + 4;
    size_t buffered_body_length = length - (size_t)(body_start - request);
    char *line_end = strstr(request, "\r\n");
    if (line_end == NULL) {
        ch_http_status(client, 400, "Bad Request", 0);
        free(request);
        return;
    }
    *line_end = '\0';
    char *method = request;
    char *uri = strchr(method, ' ');
    if (uri == NULL) goto bad_request;
    *uri++ = '\0';
    char *version = strchr(uri, ' ');
    if (version == NULL) goto bad_request;
    *version++ = '\0';
    if (strncmp(version, "HTTP/1.", 7U) != 0) goto bad_request;
    *line_end = '\r';
    if (!ch_http_authorized(connection->listener, line_end + 2, header_end)) {
        ch_http_status(client, 407, "Proxy Authentication Required", 1);
        free(request);
        return;
    }
    int is_connect = strcasecmp(method, "CONNECT") == 0;
    char *target = NULL;
    const char *path = NULL;
    if (is_connect) {
        target = ch_http_authority_target(uri, "443");
    } else if (strncasecmp(uri, "http://", 7U) == 0) {
        const char *authority = uri + 7;
        path = strchr(authority, '/');
        size_t authority_length = path == NULL ? strlen(authority) : (size_t)(path - authority);
        char *authority_copy = malloc(authority_length + 1U);
        if (authority_copy != NULL) {
            memcpy(authority_copy, authority, authority_length);
            authority_copy[authority_length] = '\0';
            target = ch_http_authority_target(authority_copy, "80");
            free(authority_copy);
        }
        if (path == NULL) path = "/";
    }
    if (target == NULL) goto bad_request_restored;
    ch_proxy_route route;
    ch_error error;
    ch_status status = ch_listener_dial(connection, target, &route, &error);
    free(target);
    if (status != CH_OK) {
        ch_http_status(client, 502, "Bad Gateway", 0);
        free(request);
        return;
    }
    if (route.action != CH_PROXY_ROUTE_CONNECT) {
        ch_http_status(client, 403, "Forbidden", 0);
        free(request);
        return;
    }
    ch_listener_clear_timeout(client);
    if (is_connect) {
        static const char connected[] =
            "HTTP/1.1 200 Connection established\r\nProxy-Agent: clambhook\r\n\r\n";
        if (!ch_listener_send_all(client, connected, sizeof(connected) - 1U) ||
            (buffered_body_length > 0U &&
             !ch_listener_send_all(connection->remote_descriptor, body_start,
                                   buffered_body_length))) {
            free(request);
            return;
        }
    } else {
        *line_end = '\0';
        char *forward_header = ch_http_forward_header(method, path, version,
                                                       line_end + 2, header_end);
        *line_end = '\r';
        if (forward_header == NULL ||
            !ch_listener_send_all(connection->remote_descriptor, forward_header,
                                  strlen(forward_header)) ||
            (buffered_body_length > 0U &&
             !ch_listener_send_all(connection->remote_descriptor, body_start,
                                   buffered_body_length))) {
            free(forward_header);
            free(request);
            return;
        }
        free(forward_header);
    }
    free(request);
    ch_listener_relay(client, connection->remote_descriptor);
    return;

bad_request:
    *line_end = '\r';
bad_request_restored:
    ch_http_status(client, 400, "Bad Request", 0);
    free(request);
}

static void ch_listener_connection_remove(ch_listener_connection *connection) {
    ch_proxy_listener *listener = connection->listener;
    pthread_mutex_lock(&listener->mutex);
    ch_listener_connection **cursor = &listener->connections;
    while (*cursor != NULL && *cursor != connection) cursor = &(*cursor)->next;
    if (*cursor == connection) *cursor = connection->next;
    if (listener->active_connections > 0U) --listener->active_connections;
    pthread_cond_broadcast(&listener->condition);
    pthread_mutex_unlock(&listener->mutex);
}

static void *ch_listener_connection_main(void *context) {
    ch_listener_connection *connection = context;
    unsigned int timeout = connection->listener->handshake_timeout_milliseconds;
    ch_listener_set_timeout(connection->client_descriptor, timeout);
    if (connection->listener->protocol == CH_PROXY_LISTENER_SOCKS5) {
        ch_socks_handle(connection);
    } else {
        ch_http_handle(connection);
    }
    pthread_mutex_lock(&connection->listener->mutex);
    int remote = connection->remote_descriptor;
    int client = connection->client_descriptor;
    connection->remote_descriptor = -1;
    connection->client_descriptor = -1;
    pthread_mutex_unlock(&connection->listener->mutex);
    ch_listener_close_descriptor(&remote);
    ch_listener_close_descriptor(&client);
    ch_listener_connection_remove(connection);
    free(connection);
    return NULL;
}

static void *ch_listener_accept_main(void *context) {
    ch_proxy_listener *listener = context;
    pthread_mutex_lock(&listener->mutex);
    int accept_descriptor = listener->descriptor;
    pthread_mutex_unlock(&listener->mutex);
    for (;;) {
        int client = accept(accept_descriptor, NULL, NULL);
        if (client < 0) {
            if (errno == EINTR) continue;
            pthread_mutex_lock(&listener->mutex);
            int stopping = listener->stopping;
            pthread_mutex_unlock(&listener->mutex);
            if (stopping || errno == EBADF || errno == EINVAL) break;
            struct pollfd wait = {.fd = accept_descriptor, .events = POLLIN};
            (void)poll(&wait, 1U, 100);
            continue;
        }
        ch_listener_disable_sigpipe(client);
        pthread_mutex_lock(&listener->mutex);
        while (!listener->stopping && listener->maximum_connections > 0U &&
               listener->active_connections >= listener->maximum_connections) {
            pthread_cond_wait(&listener->condition, &listener->mutex);
        }
        if (listener->stopping) {
            pthread_mutex_unlock(&listener->mutex);
            (void)close(client);
            break;
        }
        ch_listener_connection *connection = calloc(1U, sizeof(*connection));
        if (connection == NULL) {
            pthread_mutex_unlock(&listener->mutex);
            (void)close(client);
            continue;
        }
        connection->listener = listener;
        connection->client_descriptor = client;
        connection->remote_descriptor = -1;
        connection->next = listener->connections;
        listener->connections = connection;
        ++listener->active_connections;
        pthread_mutex_unlock(&listener->mutex);
        pthread_attr_t attributes;
        int initialized = pthread_attr_init(&attributes) == 0;
        if (initialized) {
            (void)pthread_attr_setdetachstate(&attributes, PTHREAD_CREATE_DETACHED);
        }
        pthread_t thread;
        int created = initialized &&
            pthread_create(&thread, &attributes, ch_listener_connection_main, connection) == 0;
        if (initialized) (void)pthread_attr_destroy(&attributes);
        if (!created) {
            ch_listener_close_descriptor(&connection->client_descriptor);
            ch_listener_connection_remove(connection);
            free(connection);
        }
    }
    return NULL;
}

ch_proxy_listener *ch_proxy_listener_start(const ch_proxy_listener_options *options,
                                           ch_error *error) {
    ch_error_clear(error);
    if (options == NULL || options->address == NULL || options->dial == NULL ||
        (options->protocol != CH_PROXY_LISTENER_SOCKS5 &&
         options->protocol != CH_PROXY_LISTENER_HTTP)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "valid listener options, address, and dial callback are required");
        return NULL;
    }
    ch_proxy_listener *listener = calloc(1U, sizeof(*listener));
    if (listener == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate proxy listener");
        return NULL;
    }
    listener->descriptor = -1;
    listener->protocol = options->protocol;
    listener->configured_address = ch_strdup(options->address);
    listener->username = ch_strdup(options->username == NULL ? "" : options->username);
    listener->password = ch_strdup(options->password == NULL ? "" : options->password);
    listener->authentication_required = options->authentication_required != 0;
    listener->maximum_connections = options->maximum_connections;
    listener->handshake_timeout_milliseconds = options->handshake_timeout_milliseconds == 0U ?
        CH_LISTENER_DEFAULT_HANDSHAKE_MS : options->handshake_timeout_milliseconds;
    listener->dial = options->dial;
    listener->dial_context = options->dial_context;
    if (listener->configured_address == NULL || listener->username == NULL ||
        listener->password == NULL || pthread_mutex_init(&listener->mutex, NULL) != 0) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "initialize proxy listener");
        free(listener->configured_address);
        free(listener->username);
        free(listener->password);
        free(listener);
        return NULL;
    }
    if (pthread_cond_init(&listener->condition, NULL) != 0) {
        pthread_mutex_destroy(&listener->mutex);
        free(listener->configured_address);
        free(listener->username);
        free(listener->password);
        free(listener);
        ch_error_set(error, CH_ERROR_INTERNAL, "initialize proxy listener condition");
        return NULL;
    }
    listener->descriptor = ch_listener_open_socket(options->address,
                                                    &listener->bound_address, error);
    if (listener->descriptor < 0 ||
        pthread_create(&listener->accept_thread, NULL,
                       ch_listener_accept_main, listener) != 0) {
        if (listener->descriptor >= 0 && error != NULL && error->code == CH_OK) {
            ch_error_set(error, CH_ERROR_INTERNAL, "start proxy listener thread");
        }
        ch_listener_close_descriptor(&listener->descriptor);
        pthread_cond_destroy(&listener->condition);
        pthread_mutex_destroy(&listener->mutex);
        free(listener->configured_address);
        free(listener->bound_address);
        free(listener->username);
        free(listener->password);
        free(listener);
        return NULL;
    }
    listener->accept_thread_started = 1;
    return listener;
}

void ch_proxy_listener_stop(ch_proxy_listener *listener) {
    if (listener == NULL) return;
    pthread_mutex_lock(&listener->mutex);
    listener->stopping = 1;
    int descriptor = listener->descriptor;
    listener->descriptor = -1;
    for (ch_listener_connection *connection = listener->connections;
         connection != NULL; connection = connection->next) {
        (void)shutdown(connection->client_descriptor, SHUT_RDWR);
        if (connection->remote_descriptor >= 0) {
            (void)shutdown(connection->remote_descriptor, SHUT_RDWR);
        }
    }
    pthread_cond_broadcast(&listener->condition);
    pthread_mutex_unlock(&listener->mutex);
    if (descriptor >= 0) {
        (void)shutdown(descriptor, SHUT_RDWR);
        (void)close(descriptor);
    }
    if (listener->accept_thread_started) {
        (void)pthread_join(listener->accept_thread, NULL);
    }
    pthread_mutex_lock(&listener->mutex);
    while (listener->active_connections > 0U) {
        pthread_cond_wait(&listener->condition, &listener->mutex);
    }
    pthread_mutex_unlock(&listener->mutex);
    pthread_cond_destroy(&listener->condition);
    pthread_mutex_destroy(&listener->mutex);
    sodium_memzero(listener->password, strlen(listener->password));
    free(listener->configured_address);
    free(listener->bound_address);
    free(listener->username);
    free(listener->password);
    free(listener);
}

const char *ch_proxy_listener_address(const ch_proxy_listener *listener) {
    return listener == NULL ? NULL : listener->bound_address;
}

const char *ch_proxy_listener_protocol_name(const ch_proxy_listener *listener) {
    if (listener == NULL) return "";
    return listener->protocol == CH_PROXY_LISTENER_SOCKS5 ? "socks5" : "http";
}

size_t ch_proxy_listener_active_connections(ch_proxy_listener *listener) {
    if (listener == NULL) return 0U;
    pthread_mutex_lock(&listener->mutex);
    size_t active = listener->active_connections;
    pthread_mutex_unlock(&listener->mutex);
    return active;
}
