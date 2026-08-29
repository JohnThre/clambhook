// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "clambhook/listener.h"

#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
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

#include <llhttp.h>
#include <openssl/ssl.h>
#include <sodium.h>

#include "clambhook/protocol.h"
#include "clambhook/developer.h"
#include "clambhook/socks.h"
#include "developer_internal.h"
#include "internal.h"

#define CH_LISTENER_DEFAULT_HANDSHAKE_MS 30000U
#define CH_HTTP_MAX_HEADER_BYTES (1024U * 1024U)
#define CH_HTTP_MAX_BODY_BYTES (256U * 1024U * 1024U)
#define CH_SOCKS_UDP_MAX_PACKET 65507U
#define CH_SOCKS_UDP_READER_POLL_MS 500

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
    ch_proxy_packet_dial_callback packet_dial;
    void *dial_context;
    ch_proxy_flow_bytes_callback flow_bytes;
    ch_proxy_flow_close_callback flow_close;
    void *flow_context;
    ch_developer_manager *developer;
    char *profile_name;
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
    uint64_t flow_id;
    ch_developer_capture *developer_capture;
    ch_listener_connection *next;
};

typedef struct ch_relay_direction {
    int source;
    int destination;
    ch_proxy_listener *listener;
    uint64_t flow_id;
    int incoming;
    ch_developer_capture *developer_capture;
} ch_relay_direction;

typedef struct ch_socks_udp_association ch_socks_udp_association;

typedef struct ch_socks_udp_session {
    ch_socks_udp_association *association;
    ch_packet_connection *packet;
    uint64_t flow_id;
    char key[CH_PROXY_ROUTE_SESSION_KEY_SIZE];
    pthread_t reader_thread;
    int reader_started;
    struct ch_socks_udp_session *next;
} ch_socks_udp_session;

struct ch_socks_udp_association {
    ch_proxy_listener *listener;
    int relay;
    pthread_mutex_t mutex;
    int stopping;
    struct sockaddr_storage client;
    socklen_t client_length;
    ch_socks_udp_session *sessions;
};

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

static char *ch_listener_format_address(const struct sockaddr *address,
                                        socklen_t length) {
    char host[1025];
    char service[32];
    if (getnameinfo(address, length, host, sizeof(host), service,
                    sizeof(service), NI_NUMERICHOST | NI_NUMERICSERV) != 0) {
        return NULL;
    }
    int ipv6 = address->sa_family == AF_INET6;
    size_t needed = strlen(host) + strlen(service) + (ipv6 ? 4U : 2U);
    char *result = malloc(needed);
    if (result != NULL) {
        (void)snprintf(result, needed, ipv6 ? "[%s]:%s" : "%s:%s", host,
                       service);
    }
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
            !ch_listener_send_all(direction->destination, buffer,
                                  (size_t)received)) break;
        if (direction->listener->flow_bytes != NULL &&
            direction->flow_id != 0U) {
            direction->listener->flow_bytes(
                direction->flow_id,
                direction->incoming ? (uint64_t)received : 0U,
                direction->incoming ? 0U : (uint64_t)received,
                direction->listener->flow_context);
        }
        if (direction->developer_capture != NULL) {
            if (direction->incoming) {
                ch_developer_capture_response(
                    direction->developer_capture, buffer, (size_t)received);
            } else {
                ch_developer_capture_request_body(
                    direction->developer_capture, buffer, (size_t)received);
            }
        }
    }
    (void)shutdown(direction->destination, SHUT_WR);
    return NULL;
}

static void ch_listener_relay(ch_listener_connection *connection) {
    int client = connection->client_descriptor;
    int remote = connection->remote_descriptor;
    ch_relay_direction outgoing = {
        .source = client,
        .destination = remote,
        .listener = connection->listener,
        .flow_id = connection->flow_id,
        .developer_capture = connection->developer_capture
    };
    ch_relay_direction incoming = {
        .source = remote,
        .destination = client,
        .listener = connection->listener,
        .flow_id = connection->flow_id,
        .incoming = 1,
        .developer_capture = connection->developer_capture
    };
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
    if (route->flow_id != 0U) connection->flow_id = route->flow_id;
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

static int ch_socks_write_bound_reply(int descriptor, uint8_t reply,
                                      int bound_descriptor) {
    if (bound_descriptor < 0) return ch_socks_write_reply(descriptor, reply);
    struct sockaddr_storage address;
    socklen_t length = (socklen_t)sizeof(address);
    if (getsockname(bound_descriptor, (struct sockaddr *)&address,
                    &length) != 0) {
        return ch_socks_write_reply(descriptor, 0x01U);
    }
    uint8_t response[22] = {0x05U, reply, 0x00U};
    size_t response_length;
    if (address.ss_family == AF_INET) {
        const struct sockaddr_in *ipv4 = (const struct sockaddr_in *)&address;
        response[3] = 0x01U;
        memcpy(response + 4U, &ipv4->sin_addr, 4U);
        memcpy(response + 8U, &ipv4->sin_port, 2U);
        response_length = 10U;
    } else if (address.ss_family == AF_INET6) {
        const struct sockaddr_in6 *ipv6 = (const struct sockaddr_in6 *)&address;
        response[3] = 0x04U;
        memcpy(response + 4U, &ipv6->sin6_addr, 16U);
        memcpy(response + 20U, &ipv6->sin6_port, 2U);
        response_length = 22U;
    } else {
        return ch_socks_write_reply(descriptor, 0x01U);
    }
    return ch_listener_send_all(descriptor, response, response_length);
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

static uint16_t ch_listener_address_port(const struct sockaddr *address) {
    if (address->sa_family == AF_INET) {
        return ntohs(((const struct sockaddr_in *)address)->sin_port);
    }
    if (address->sa_family == AF_INET6) {
        return ntohs(((const struct sockaddr_in6 *)address)->sin6_port);
    }
    return 0U;
}

static int ch_listener_same_ip(const struct sockaddr *left,
                               const struct sockaddr *right) {
    if (left->sa_family != right->sa_family) return 0;
    if (left->sa_family == AF_INET) {
        const struct sockaddr_in *left_ipv4 =
            (const struct sockaddr_in *)left;
        const struct sockaddr_in *right_ipv4 =
            (const struct sockaddr_in *)right;
        return memcmp(&left_ipv4->sin_addr, &right_ipv4->sin_addr,
                      sizeof(left_ipv4->sin_addr)) == 0;
    }
    if (left->sa_family == AF_INET6) {
        const struct sockaddr_in6 *left_ipv6 =
            (const struct sockaddr_in6 *)left;
        const struct sockaddr_in6 *right_ipv6 =
            (const struct sockaddr_in6 *)right;
        return left_ipv6->sin6_scope_id == right_ipv6->sin6_scope_id &&
            memcmp(&left_ipv6->sin6_addr, &right_ipv6->sin6_addr,
                   sizeof(left_ipv6->sin6_addr)) == 0;
    }
    return 0;
}

static int ch_listener_same_endpoint(const struct sockaddr *left,
                                     const struct sockaddr *right) {
    return ch_listener_same_ip(left, right) &&
        ch_listener_address_port(left) == ch_listener_address_port(right);
}

static int ch_socks_udp_open(int client, ch_error *error) {
    struct sockaddr_storage local;
    socklen_t length = (socklen_t)sizeof(local);
    if (getsockname(client, (struct sockaddr *)&local, &length) != 0) {
        ch_error_set(error, CH_ERROR_IO, "read SOCKS5 local address: %s",
                     strerror(errno));
        return -1;
    }
    int descriptor = socket(local.ss_family, SOCK_DGRAM, IPPROTO_UDP);
    if (descriptor < 0) {
        ch_error_set(error, CH_ERROR_IO, "open SOCKS5 UDP relay: %s",
                     strerror(errno));
        return -1;
    }
    if (local.ss_family == AF_INET) {
        ((struct sockaddr_in *)&local)->sin_port = 0U;
        length = (socklen_t)sizeof(struct sockaddr_in);
    } else if (local.ss_family == AF_INET6) {
        ((struct sockaddr_in6 *)&local)->sin6_port = 0U;
        length = (socklen_t)sizeof(struct sockaddr_in6);
    } else {
        (void)close(descriptor);
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "unsupported SOCKS5 UDP address family");
        return -1;
    }
    if (bind(descriptor, (struct sockaddr *)&local, length) != 0) {
        ch_error_set(error, CH_ERROR_IO, "bind SOCKS5 UDP relay: %s",
                     strerror(errno));
        (void)close(descriptor);
        return -1;
    }
    return descriptor;
}

static uint16_t ch_socks_requested_udp_port(const char *target) {
    const char *separator = target == NULL ? NULL : strrchr(target, ':');
    if (separator == NULL || separator[1] == '\0') return 0U;
    char *end = NULL;
    unsigned long port = strtoul(separator + 1, &end, 10);
    return end != separator + 1 && *end == '\0' && port <= 65535UL ?
        (uint16_t)port : 0U;
}

static int ch_socks_udp_send_response(
    int relay,
    const struct sockaddr *client_address,
    socklen_t client_length,
    const char *source,
    const uint8_t *payload,
    size_t payload_length) {
    ch_error error;
    uint8_t *encoded_source = NULL;
    size_t encoded_source_length = 0U;
    if (ch_socks_encode_address(source, &encoded_source,
                                &encoded_source_length, &error) != CH_OK ||
        encoded_source_length > CH_SOCKS_UDP_MAX_PACKET - 3U ||
        payload_length > CH_SOCKS_UDP_MAX_PACKET - 3U -
                             encoded_source_length) {
        free(encoded_source);
        return 0;
    }
    size_t response_length = 3U + encoded_source_length + payload_length;
    uint8_t *response = malloc(response_length);
    if (response == NULL) {
        free(encoded_source);
        return 0;
    }
    response[0] = 0U;
    response[1] = 0U;
    response[2] = 0U;
    memcpy(response + 3U, encoded_source, encoded_source_length);
    if (payload_length > 0U) {
        memcpy(response + 3U + encoded_source_length, payload,
               payload_length);
    }
    free(encoded_source);
    ssize_t sent;
    do {
        sent = sendto(relay, response, response_length, 0, client_address,
                      client_length);
    } while (sent < 0 && errno == EINTR);
    free(response);
    return sent == (ssize_t)response_length;
}

static void *ch_socks_udp_session_reader(void *opaque) {
    ch_socks_udp_session *session = opaque;
    ch_socks_udp_association *association = session->association;
    uint8_t response[CH_SOCKS_UDP_MAX_PACKET];
    for (;;) {
        pthread_mutex_lock(&association->mutex);
        int stopping = association->stopping;
        pthread_mutex_unlock(&association->mutex);
        if (stopping) break;
        char *source = NULL;
        size_t response_length = 0U;
        ch_error error;
        ch_status status = ch_packet_connection_receive_timeout(
            session->packet, response, sizeof(response), &response_length,
            &source, CH_SOCKS_UDP_READER_POLL_MS, &error);
        if (status == CH_ERROR_NOT_FOUND) continue;
        if (status != CH_OK) {
            free(source);
            break;
        }
        struct sockaddr_storage client;
        socklen_t client_length;
        pthread_mutex_lock(&association->mutex);
        stopping = association->stopping;
        client_length = association->client_length;
        if (client_length > 0U) {
            memcpy(&client, &association->client, client_length);
        }
        pthread_mutex_unlock(&association->mutex);
        if (!stopping && client_length > 0U) {
            int delivered = ch_socks_udp_send_response(
                association->relay, (const struct sockaddr *)&client,
                client_length, source, response, response_length);
            if (delivered && association->listener->flow_bytes != NULL &&
                session->flow_id != 0U) {
                association->listener->flow_bytes(
                    session->flow_id, (uint64_t)response_length, 0U,
                    association->listener->flow_context);
            }
        }
        free(source);
    }
    return NULL;
}

static ch_socks_udp_session *ch_socks_udp_session_get(
    ch_socks_udp_association *association,
    const char *key,
    ch_packet_connection *packet,
    uint64_t flow_id) {
    for (ch_socks_udp_session *session = association->sessions;
         session != NULL; session = session->next) {
        if (strcmp(session->key, key) == 0) {
            ch_packet_connection_close(packet);
            if (association->listener->flow_close != NULL && flow_id != 0U) {
                association->listener->flow_close(
                    flow_id, "UDP session reused",
                    association->listener->flow_context);
            }
            return session;
        }
    }
    ch_socks_udp_session *session = calloc(1U, sizeof(*session));
    if (session == NULL) {
        ch_packet_connection_close(packet);
        if (association->listener->flow_close != NULL && flow_id != 0U) {
            association->listener->flow_close(
                flow_id, "UDP session allocation failed",
                association->listener->flow_context);
        }
        return NULL;
    }
    session->association = association;
    session->packet = packet;
    session->flow_id = flow_id;
    (void)snprintf(session->key, sizeof(session->key), "%s", key);
    session->next = association->sessions;
    association->sessions = session;
    return session;
}

static int ch_socks_udp_session_start(ch_socks_udp_session *session) {
    if (session->reader_started) return 1;
    if (pthread_create(&session->reader_thread, NULL,
                       ch_socks_udp_session_reader, session) != 0) {
        return 0;
    }
    session->reader_started = 1;
    return 1;
}

static void ch_socks_udp_association_clear(
    ch_socks_udp_association *association) {
    pthread_mutex_lock(&association->mutex);
    association->stopping = 1;
    pthread_mutex_unlock(&association->mutex);
    ch_socks_udp_session *session = association->sessions;
    while (session != NULL) {
        ch_socks_udp_session *next = session->next;
        if (session->reader_started) {
            (void)pthread_join(session->reader_thread, NULL);
        }
        ch_packet_connection_close(session->packet);
        if (association->listener->flow_close != NULL &&
            session->flow_id != 0U) {
            association->listener->flow_close(
                session->flow_id, "closed",
                association->listener->flow_context);
        }
        free(session);
        session = next;
    }
    (void)pthread_mutex_destroy(&association->mutex);
}

static void ch_socks_udp_associate(ch_listener_connection *connection,
                                   const char *requested_client) {
    ch_proxy_listener *listener = connection->listener;
    int client = connection->client_descriptor;
    if (listener->packet_dial == NULL) {
        (void)ch_socks_write_reply(client, 0x07U);
        return;
    }
    ch_error error;
    int relay = ch_socks_udp_open(client, &error);
    if (relay < 0) {
        (void)ch_socks_write_reply(client, 0x01U);
        return;
    }
    pthread_mutex_lock(&listener->mutex);
    connection->remote_descriptor = relay;
    pthread_mutex_unlock(&listener->mutex);
    ch_socks_udp_association association;
    memset(&association, 0, sizeof(association));
    association.listener = listener;
    association.relay = relay;
    if (pthread_mutex_init(&association.mutex, NULL) != 0) {
        (void)ch_socks_write_reply(client, 0x01U);
        return;
    }
    if (!ch_socks_write_bound_reply(client, 0x00U, relay)) {
        ch_socks_udp_association_clear(&association);
        return;
    }
    ch_listener_clear_timeout(client);
    struct sockaddr_storage tcp_peer;
    socklen_t tcp_peer_length = (socklen_t)sizeof(tcp_peer);
    if (getpeername(client, (struct sockaddr *)&tcp_peer,
                    &tcp_peer_length) != 0) {
        ch_socks_udp_association_clear(&association);
        return;
    }
    uint16_t requested_port = ch_socks_requested_udp_port(requested_client);
    uint8_t request[CH_SOCKS_UDP_MAX_PACKET];
    for (;;) {
        struct pollfd wait[2] = {
            {.fd = client, .events = POLLIN},
            {.fd = relay, .events = POLLIN}
        };
        int ready;
        do {
            ready = poll(wait, 2U, -1);
        } while (ready < 0 && errno == EINTR);
        if (ready <= 0 || (wait[0].revents & (POLLERR | POLLHUP | POLLNVAL)) != 0) {
            break;
        }
        if ((wait[0].revents & POLLIN) != 0) {
            uint8_t ignored;
            ssize_t control = recv(client, &ignored, 1U, 0);
            if (control <= 0) break;
        }
        if ((wait[1].revents & POLLIN) == 0) continue;
        struct sockaddr_storage udp_client;
        socklen_t udp_client_length = (socklen_t)sizeof(udp_client);
        ssize_t request_length;
        do {
            request_length = recvfrom(
                relay, request, sizeof(request), 0,
                (struct sockaddr *)&udp_client, &udp_client_length);
        } while (request_length < 0 && errno == EINTR);
        if (request_length < 4) continue;
        if (!ch_listener_same_ip((const struct sockaddr *)&tcp_peer,
                                 (const struct sockaddr *)&udp_client)) {
            continue;
        }
        if (association.client_length == 0U) {
            if (requested_port != 0U && ch_listener_address_port(
                    (const struct sockaddr *)&udp_client) != requested_port) {
                continue;
            }
            pthread_mutex_lock(&association.mutex);
            memcpy(&association.client, &udp_client, udp_client_length);
            association.client_length = udp_client_length;
            pthread_mutex_unlock(&association.mutex);
        } else if (!ch_listener_same_endpoint(
                       (const struct sockaddr *)&association.client,
                       (const struct sockaddr *)&udp_client)) {
            continue;
        }
        if (request[0] != 0U || request[1] != 0U || request[2] != 0U) {
            continue;
        }
        char *target_host = NULL;
        uint16_t target_port = 0U;
        size_t address_length = 0U;
        if (ch_socks_decode_address(
                request + 3U, (size_t)request_length - 3U, &target_host,
                &target_port, &address_length, &error) != CH_OK ||
            address_length > (size_t)request_length - 3U) {
            free(target_host);
            continue;
        }
        int target_ipv6 = strchr(target_host, ':') != NULL;
        size_t target_length = strlen(target_host) + 10U;
        char *target = malloc(target_length);
        if (target == NULL) {
            free(target_host);
            continue;
        }
        (void)snprintf(target, target_length,
                       target_ipv6 ? "[%s]:%u" : "%s:%u", target_host,
                       (unsigned int)target_port);
        free(target_host);
        char *source = ch_listener_format_address(
            (const struct sockaddr *)&udp_client, udp_client_length);
        if (source == NULL) {
            free(target);
            continue;
        }
        ch_proxy_route route;
        memset(&route, 0, sizeof(route));
        ch_packet_connection *packet = NULL;
        ch_status status = listener->packet_dial(
            "udp", target, source, &route, &packet, listener->dial_context,
            &error);
        free(source);
        if (route.action == 0) route.action = CH_PROXY_ROUTE_CONNECT;
        size_t payload_offset = 3U + address_length;
        ch_socks_udp_session *session = NULL;
        if (status == CH_OK && route.action == CH_PROXY_ROUTE_CONNECT &&
            packet != NULL) {
            if (route.session_key[0] == '\0') {
                (void)snprintf(route.session_key, sizeof(route.session_key),
                               "target:%s", target);
            }
            session = ch_socks_udp_session_get(&association,
                                                route.session_key, packet,
                                                route.flow_id);
            packet = NULL;
            if (session == NULL) status = CH_ERROR_OUT_OF_MEMORY;
        }
        if (status == CH_OK && session != NULL) {
            size_t payload_length = (size_t)request_length - payload_offset;
            status = ch_packet_connection_send(
                session->packet, target, request + payload_offset,
                payload_length, &error);
            if (status == CH_OK && listener->flow_bytes != NULL &&
                session->flow_id != 0U) {
                listener->flow_bytes(session->flow_id, 0U,
                                     (uint64_t)payload_length,
                                     listener->flow_context);
            }
            if (status == CH_OK && !ch_socks_udp_session_start(session)) {
                status = CH_ERROR_INTERNAL;
            }
        }
        ch_packet_connection_close(packet);
        free(target);
    }
    ch_socks_udp_association_clear(&association);
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
    if (command == 0x03U) {
        ch_socks_udp_associate(connection, target);
        free(target);
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
    ch_listener_relay(connection);
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

typedef struct ch_http_io {
    int descriptor;
    SSL *tls;
    uint8_t *unread;
    size_t unread_length;
} ch_http_io;

typedef struct ch_http_parser_context {
    llhttp_t parser;
    llhttp_settings_t settings;
    ch_developer_http_message message;
    ch_json_buffer url;
    ch_json_buffer header_name;
    ch_json_buffer header_value;
    size_t header_bytes;
    bool reading_value;
    bool complete;
    bool response;
} ch_http_parser_context;

typedef struct ch_http_prefix_bio {
    int descriptor;
    uint8_t *prefix;
    size_t prefix_length;
    size_t prefix_offset;
} ch_http_prefix_bio;

static BIO_METHOD *ch_http_prefix_bio_method = NULL;
static pthread_once_t ch_http_prefix_bio_once = PTHREAD_ONCE_INIT;

static int ch_http_prefix_bio_create(BIO *bio) {
    BIO_set_init(bio, 1);
    BIO_set_shutdown(bio, 0);
    BIO_set_data(bio, NULL);
    return 1;
}

static int ch_http_prefix_bio_destroy(BIO *bio) {
    if (bio == NULL) return 0;
    ch_http_prefix_bio *state = BIO_get_data(bio);
    if (state != NULL) {
        free(state->prefix);
        free(state);
    }
    BIO_set_data(bio, NULL);
    BIO_set_init(bio, 0);
    return 1;
}

static int ch_http_prefix_bio_read(BIO *bio, char *output, int length) {
    ch_http_prefix_bio *state = BIO_get_data(bio);
    if (state == NULL || output == NULL || length <= 0) return 0;
    BIO_clear_retry_flags(bio);
    size_t remaining = state->prefix_length - state->prefix_offset;
    if (remaining > 0U) {
        size_t copied = remaining < (size_t)length ? remaining :
                                                     (size_t)length;
        memcpy(output, state->prefix + state->prefix_offset, copied);
        state->prefix_offset += copied;
        return (int)copied;
    }
    ssize_t received;
    do {
        received = recv(state->descriptor, output, (size_t)length, 0);
    } while (received < 0 && errno == EINTR);
    if (received < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) {
        BIO_set_retry_read(bio);
    }
    return received > (ssize_t)INT_MAX ? INT_MAX : (int)received;
}

static int ch_http_prefix_bio_write(BIO *bio, const char *input, int length) {
    ch_http_prefix_bio *state = BIO_get_data(bio);
    if (state == NULL || input == NULL || length <= 0) return 0;
    BIO_clear_retry_flags(bio);
    ssize_t written;
    do {
        written = send(state->descriptor, input, (size_t)length,
#ifdef MSG_NOSIGNAL
                       MSG_NOSIGNAL
#else
                       0
#endif
        );
    } while (written < 0 && errno == EINTR);
    if (written < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) {
        BIO_set_retry_write(bio);
    }
    return written > (ssize_t)INT_MAX ? INT_MAX : (int)written;
}

static long ch_http_prefix_bio_ctrl(BIO *bio, int command, long number,
                                    void *pointer) {
    (void)number;
    (void)pointer;
    ch_http_prefix_bio *state = BIO_get_data(bio);
    switch (command) {
        case BIO_CTRL_FLUSH:
            return 1L;
        case BIO_CTRL_PENDING:
            return state == NULL ? 0L :
                (long)(state->prefix_length - state->prefix_offset);
        case BIO_CTRL_GET_CLOSE:
            return BIO_get_shutdown(bio);
        case BIO_CTRL_SET_CLOSE:
            BIO_set_shutdown(bio, (int)number);
            return 1L;
        default:
            return 0L;
    }
}

static void ch_http_prefix_bio_initialize(void) {
    ch_http_prefix_bio_method = BIO_meth_new(
        BIO_TYPE_SOURCE_SINK | 0x80, "clambhook prefixed socket");
    if (ch_http_prefix_bio_method == NULL) return;
    if (BIO_meth_set_create(ch_http_prefix_bio_method,
                            ch_http_prefix_bio_create) != 1 ||
        BIO_meth_set_destroy(ch_http_prefix_bio_method,
                             ch_http_prefix_bio_destroy) != 1 ||
        BIO_meth_set_read(ch_http_prefix_bio_method,
                          ch_http_prefix_bio_read) != 1 ||
        BIO_meth_set_write(ch_http_prefix_bio_method,
                           ch_http_prefix_bio_write) != 1 ||
        BIO_meth_set_ctrl(ch_http_prefix_bio_method,
                          ch_http_prefix_bio_ctrl) != 1) {
        BIO_meth_free(ch_http_prefix_bio_method);
        ch_http_prefix_bio_method = NULL;
    }
}

static BIO *ch_http_prefix_bio_new(int descriptor, const uint8_t *prefix,
                                   size_t prefix_length) {
    (void)pthread_once(&ch_http_prefix_bio_once,
                       ch_http_prefix_bio_initialize);
    if (ch_http_prefix_bio_method == NULL) return NULL;
    BIO *bio = BIO_new(ch_http_prefix_bio_method);
    ch_http_prefix_bio *state = bio == NULL ? NULL :
        calloc(1U, sizeof(*state));
    if (state != NULL && prefix_length > 0U) {
        state->prefix = malloc(prefix_length);
        if (state->prefix != NULL) {
            memcpy(state->prefix, prefix, prefix_length);
            state->prefix_length = prefix_length;
        }
    }
    if (state == NULL || (prefix_length > 0U && state->prefix == NULL)) {
        free(state);
        BIO_free(bio);
        return NULL;
    }
    state->descriptor = descriptor;
    BIO_set_data(bio, state);
    return bio;
}

static ssize_t ch_http_io_read(ch_http_io *io, uint8_t *buffer,
                               size_t length) {
    if (io->tls != NULL) {
        int limited = length > (size_t)INT_MAX ? INT_MAX : (int)length;
        int result = SSL_read(io->tls, buffer, limited);
        if (result > 0) return result;
        int ssl_error = SSL_get_error(io->tls, result);
        if (ssl_error == SSL_ERROR_ZERO_RETURN) return 0;
        errno = ssl_error == SSL_ERROR_WANT_READ ||
            ssl_error == SSL_ERROR_WANT_WRITE ? EINTR : EIO;
        return -1;
    }
    return recv(io->descriptor, buffer, length, 0);
}

static void ch_http_io_dispose(ch_http_io *io) {
    if (io == NULL) return;
    free(io->unread);
    io->unread = NULL;
    io->unread_length = 0U;
}

static bool ch_http_io_store_unread(ch_http_io *io, const char *bytes,
                                    size_t length) {
    uint8_t *copy = NULL;
    if (length > 0U) {
        copy = malloc(length);
        if (copy == NULL) return false;
        memcpy(copy, bytes, length);
    }
    free(io->unread);
    io->unread = copy;
    io->unread_length = length;
    return true;
}

static bool ch_http_io_write_all(ch_http_io *io, const uint8_t *bytes,
                                 size_t length) {
    size_t offset = 0U;
    while (offset < length) {
        ssize_t written;
        if (io->tls != NULL) {
            int limited = length - offset > (size_t)INT_MAX ? INT_MAX :
                                                           (int)(length - offset);
            int result = SSL_write(io->tls, bytes + offset, limited);
            if (result > 0) {
                written = result;
            } else {
                int ssl_error = SSL_get_error(io->tls, result);
                if (ssl_error == SSL_ERROR_WANT_READ ||
                    ssl_error == SSL_ERROR_WANT_WRITE) continue;
                return false;
            }
        } else {
            written = send(io->descriptor, bytes + offset, length - offset,
#ifdef MSG_NOSIGNAL
                           MSG_NOSIGNAL
#else
                           0
#endif
            );
            if (written < 0 && errno == EINTR) continue;
        }
        if (written <= 0) return false;
        offset += (size_t)written;
    }
    return true;
}

static bool ch_http_message_header_add(ch_developer_http_message *message,
                                       const char *name,
                                       const char *value) {
    ch_developer_http_header *next = realloc(
        message->headers,
        (message->header_count + 1U) * sizeof(*message->headers));
    if (next == NULL) return false;
    message->headers = next;
    ch_developer_http_header *header = &next[message->header_count];
    memset(header, 0, sizeof(*header));
    header->name = ch_strdup(name == NULL ? "" : name);
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

static const char *ch_http_message_header(
    const ch_developer_http_message *message, const char *name) {
    for (size_t index = 0U; index < message->header_count; ++index) {
        if (message->headers[index].name != NULL &&
            strcasecmp(message->headers[index].name, name) == 0) {
            return message->headers[index].value;
        }
    }
    return NULL;
}

static void ch_http_message_header_remove(
    ch_developer_http_message *message, const char *name) {
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

static bool ch_http_parser_finalize_header(ch_http_parser_context *context) {
    if (context->header_name.length == 0U) return true;
    char *name = ch_json_take(&context->header_name);
    char *value = ch_json_take(&context->header_value);
    ch_json_init(&context->header_name);
    ch_json_init(&context->header_value);
    bool okay = name != NULL && value != NULL &&
        ch_http_message_header_add(&context->message, name, value);
    free(name);
    free(value);
    context->reading_value = false;
    return okay;
}

static int ch_http_parser_url(llhttp_t *parser, const char *at,
                              size_t length) {
    ch_http_parser_context *context = parser->data;
    return ch_json_append_bytes(&context->url, at, length) ? 0 : HPE_USER;
}

static int ch_http_parser_header_field(llhttp_t *parser, const char *at,
                                       size_t length) {
    ch_http_parser_context *context = parser->data;
    if (context->reading_value && !ch_http_parser_finalize_header(context)) {
        return HPE_USER;
    }
    context->header_bytes += length;
    if (context->header_bytes > CH_HTTP_MAX_HEADER_BYTES) return HPE_USER;
    return ch_json_append_bytes(&context->header_name, at, length) ? 0 :
                                                                    HPE_USER;
}

static int ch_http_parser_header_value(llhttp_t *parser, const char *at,
                                       size_t length) {
    ch_http_parser_context *context = parser->data;
    context->reading_value = true;
    context->header_bytes += length;
    if (context->header_bytes > CH_HTTP_MAX_HEADER_BYTES) return HPE_USER;
    return ch_json_append_bytes(&context->header_value, at, length) ? 0 :
                                                                     HPE_USER;
}

static int ch_http_parser_headers_complete(llhttp_t *parser) {
    ch_http_parser_context *context = parser->data;
    if (!ch_http_parser_finalize_header(context)) return HPE_USER;
    if (context->response) {
        context->message.status = (int)parser->status_code;
    } else {
        const char *method = llhttp_method_name((llhttp_method_t)parser->method);
        context->message.method = ch_strdup(method == NULL ? "" : method);
        if (context->message.method == NULL) return HPE_USER;
    }
    return 0;
}

static int ch_http_parser_body(llhttp_t *parser, const char *at,
                               size_t length) {
    ch_http_parser_context *context = parser->data;
    if (length > CH_HTTP_MAX_BODY_BYTES - context->message.body_length) {
        return HPE_USER;
    }
    uint8_t *next = realloc(context->message.body,
                            context->message.body_length + length);
    if (next == NULL && length > 0U) return HPE_USER;
    context->message.body = next;
    if (length > 0U) {
        memcpy(next + context->message.body_length, at, length);
        context->message.body_length += length;
        context->message.body_set = true;
    }
    return 0;
}

static int ch_http_parser_message_complete(llhttp_t *parser) {
    ch_http_parser_context *context = parser->data;
    if (context->response && context->message.status >= 100 &&
        context->message.status < 200 && context->message.status != 101) {
        ch_developer_http_message_clear(&context->message);
        context->header_bytes = 0U;
        context->reading_value = false;
        return 0;
    }
    context->complete = true;
    llhttp_pause(parser);
    return 0;
}

static void ch_http_parser_dispose(ch_http_parser_context *context) {
    ch_json_dispose(&context->url);
    ch_json_dispose(&context->header_name);
    ch_json_dispose(&context->header_value);
    ch_developer_http_message_clear(&context->message);
}

static bool ch_http_message_derive_request(
    ch_developer_http_message *message, char *url,
    const char *scheme_hint, const char *authority_hint) {
    message->url = url;
    const char *host_header = ch_http_message_header(message, "Host");
    const char *authority = host_header == NULL || host_header[0] == '\0' ?
        authority_hint : host_header;
    if (url != NULL && (strncasecmp(url, "http://", 7U) == 0 ||
                        strncasecmp(url, "https://", 8U) == 0)) {
        const char *start = strstr(url, "://") + 3U;
        const char *slash = strchr(start, '/');
        const char *question = strchr(start, '?');
        const char *end = slash == NULL ? question :
            (question == NULL || slash < question ? slash : question);
        size_t host_length = end == NULL ? strlen(start) :
                                             (size_t)(end - start);
        message->host = strndup(start, host_length);
        message->path = ch_strdup(end == NULL ? "/" : end);
        return message->host != NULL && message->path != NULL;
    }
    if (authority == NULL || authority[0] == '\0') return false;
    message->host = ch_strdup(authority);
    message->path = ch_strdup(url == NULL || url[0] == '\0' ? "/" : url);
    ch_json_buffer absolute;
    ch_json_init(&absolute);
    bool okay = ch_json_append(&absolute,
                               scheme_hint == NULL ? "http" : scheme_hint) &&
        ch_json_append(&absolute, "://") &&
        ch_json_append(&absolute, authority) &&
        ch_json_append(&absolute, message->path);
    char *full = okay ? ch_json_take(&absolute) : NULL;
    ch_json_dispose(&absolute);
    free(message->url);
    message->url = full;
    return message->host != NULL && message->path != NULL && full != NULL;
}

static ch_status ch_http_parse_message(
    ch_http_io *io, llhttp_type_t type,
    const uint8_t *initial, size_t initial_length,
    const char *request_method_hint,
    const char *scheme_hint, const char *authority_hint,
    ch_developer_http_message *out, ch_error *error) {
    ch_http_parser_context context;
    memset(&context, 0, sizeof(context));
    context.response = type == HTTP_RESPONSE;
    ch_json_init(&context.url);
    ch_json_init(&context.header_name);
    ch_json_init(&context.header_value);
    llhttp_settings_init(&context.settings);
    context.settings.on_url = ch_http_parser_url;
    context.settings.on_header_field = ch_http_parser_header_field;
    context.settings.on_header_value = ch_http_parser_header_value;
    context.settings.on_headers_complete = ch_http_parser_headers_complete;
    context.settings.on_body = ch_http_parser_body;
    context.settings.on_message_complete = ch_http_parser_message_complete;
    llhttp_init(&context.parser, type, &context.settings);
    if (context.response && request_method_hint != NULL &&
        strcasecmp(request_method_hint, "HEAD") == 0) {
        context.parser.method = HTTP_HEAD;
    }
    context.parser.data = &context;
    llhttp_errno_t parse_status = HPE_OK;
    if (initial_length > 0U) {
        parse_status = llhttp_execute(
            &context.parser, (const char *)initial, initial_length);
        if (parse_status == HPE_PAUSED && context.complete) {
            const char *position = llhttp_get_error_pos(&context.parser);
            const char *end = (const char *)initial + initial_length;
            if (position != NULL && position >= (const char *)initial &&
                position <= end &&
                !ch_http_io_store_unread(io, position,
                                         (size_t)(end - position))) {
                parse_status = HPE_USER;
            }
        }
    }
    if (!context.complete && parse_status == HPE_OK &&
        io->unread_length > 0U) {
        uint8_t *unread = io->unread;
        size_t unread_length = io->unread_length;
        io->unread = NULL;
        io->unread_length = 0U;
        parse_status = llhttp_execute(
            &context.parser, (const char *)unread, unread_length);
        if (parse_status == HPE_PAUSED && context.complete) {
            const char *position = llhttp_get_error_pos(&context.parser);
            const char *end = (const char *)unread + unread_length;
            if (position != NULL && position >= (const char *)unread &&
                position <= end &&
                !ch_http_io_store_unread(io, position,
                                         (size_t)(end - position))) {
                parse_status = HPE_USER;
            }
        }
        free(unread);
    }
    uint8_t buffer[32768];
    while (!context.complete && parse_status == HPE_OK) {
        ssize_t received = ch_http_io_read(io, buffer, sizeof(buffer));
        if (received < 0 && errno == EINTR) continue;
        if (received < 0) {
            ch_error_set(error, CH_ERROR_IO, "read HTTP message: %s",
                         strerror(errno));
            ch_http_parser_dispose(&context);
            return CH_ERROR_IO;
        }
        if (received == 0) {
            parse_status = llhttp_finish(&context.parser);
            break;
        }
        parse_status = llhttp_execute(&context.parser, (const char *)buffer,
                                      (size_t)received);
        if (parse_status == HPE_PAUSED && context.complete) {
            const char *position = llhttp_get_error_pos(&context.parser);
            const char *end = (const char *)buffer + received;
            if (position != NULL && position >= (const char *)buffer &&
                position <= end &&
                !ch_http_io_store_unread(io, position,
                                         (size_t)(end - position))) {
                parse_status = HPE_USER;
            }
        }
    }
    if (parse_status == HPE_PAUSED && context.complete) parse_status = HPE_OK;
    if (parse_status != HPE_OK || !context.complete) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "parse HTTP message: %s",
                     llhttp_errno_name(parse_status));
        ch_http_parser_dispose(&context);
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (!context.response) {
        char *url = ch_json_take(&context.url);
        if (url == NULL || !ch_http_message_derive_request(
                &context.message, url, scheme_hint, authority_hint)) {
            free(url);
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "HTTP request target is invalid");
            ch_http_parser_dispose(&context);
            return CH_ERROR_INVALID_ARGUMENT;
        }
    }
    if (ch_http_message_header(&context.message, "Content-Length") != NULL ||
        ch_http_message_header(&context.message, "Transfer-Encoding") != NULL) {
        context.message.body_set = true;
    }
    *out = context.message;
    memset(&context.message, 0, sizeof(context.message));
    ch_http_parser_dispose(&context);
    return CH_OK;
}

static const char *ch_http_reason(int status) {
    switch (status) {
        case 100: return "Continue";
        case 200: return "OK";
        case 201: return "Created";
        case 202: return "Accepted";
        case 204: return "No Content";
        case 206: return "Partial Content";
        case 301: return "Moved Permanently";
        case 302: return "Found";
        case 304: return "Not Modified";
        case 307: return "Temporary Redirect";
        case 308: return "Permanent Redirect";
        case 400: return "Bad Request";
        case 401: return "Unauthorized";
        case 403: return "Forbidden";
        case 404: return "Not Found";
        case 407: return "Proxy Authentication Required";
        case 408: return "Request Timeout";
        case 409: return "Conflict";
        case 413: return "Content Too Large";
        case 429: return "Too Many Requests";
        case 500: return "Internal Server Error";
        case 502: return "Bad Gateway";
        case 503: return "Service Unavailable";
        case 504: return "Gateway Timeout";
        default: return "Status";
    }
}

static void ch_http_strip_hop_headers(ch_developer_http_message *message) {
    static const char *const names[] = {
        "Connection", "Proxy-Connection", "Keep-Alive",
        "Proxy-Authenticate", "Proxy-Authorization", "TE", "Trailer",
        "Transfer-Encoding", "Upgrade"
    };
    for (size_t index = 0U; index < sizeof(names) / sizeof(names[0]); ++index) {
        ch_http_message_header_remove(message, names[index]);
    }
}

static char *ch_http_serialize_message(ch_developer_http_message *message,
                                       bool response,
                                       size_t *out_length) {
    ch_http_strip_hop_headers(message);
    if (message->body_set || message->body_length > 0U) {
        char length[32];
        (void)snprintf(length, sizeof(length), "%zu", message->body_length);
        ch_http_message_header_remove(message, "Content-Length");
        if (!ch_http_message_header_add(message, "Content-Length", length)) {
            return NULL;
        }
    }
    ch_json_buffer output;
    ch_json_init(&output);
    bool okay;
    if (response) {
        okay = ch_json_append_format(
            &output, "HTTP/1.1 %d %s\r\n", message->status,
            ch_http_reason(message->status));
    } else {
        const char *path = message->path == NULL || message->path[0] == '\0' ?
            "/" : message->path;
        okay = ch_json_append_format(
            &output, "%s %s HTTP/1.1\r\n",
            message->method == NULL ? "GET" : message->method, path);
    }
    for (size_t index = 0U; okay && index < message->header_count; ++index) {
        const char *name = message->headers[index].name;
        const char *value = message->headers[index].value;
        if (name == NULL || name[0] == '\0' || strchr(name, '\r') != NULL ||
            strchr(name, '\n') != NULL || (value != NULL &&
            (strchr(value, '\r') != NULL || strchr(value, '\n') != NULL))) {
            okay = false;
            break;
        }
        okay = ch_json_append_format(&output, "%s: %s\r\n", name,
                                     value == NULL ? "" : value);
    }
    okay = okay && ch_json_append(&output, "\r\n") &&
        (message->body_length == 0U || ch_json_append_bytes(
            &output, (const char *)message->body, message->body_length));
    *out_length = okay ? output.length : 0U;
    char *result = okay ? ch_json_take(&output) : NULL;
    ch_json_dispose(&output);
    return result;
}

static char *ch_http_message_headers_raw(
    const ch_developer_http_message *message, size_t *out_length) {
    ch_json_buffer output;
    ch_json_init(&output);
    bool okay = true;
    for (size_t index = 0U; okay && index < message->header_count; ++index) {
        const char *name = message->headers[index].name;
        const char *value = message->headers[index].value;
        okay = name != NULL && strchr(name, '\r') == NULL &&
            strchr(name, '\n') == NULL &&
            (value == NULL || (strchr(value, '\r') == NULL &&
                               strchr(value, '\n') == NULL)) &&
            ch_json_append_format(&output, "%s: %s\r\n", name,
                                  value == NULL ? "" : value);
    }
    *out_length = okay ? output.length : 0U;
    char *result = okay ? ch_json_take(&output) : NULL;
    ch_json_dispose(&output);
    return result;
}

static ch_developer_capture *ch_http_begin_message_capture(
    ch_listener_connection *connection,
    const ch_developer_http_message *message,
    const ch_proxy_route *route,
    const char *scheme) {
    if (connection->listener->developer == NULL) return NULL;
    size_t headers_length = 0U;
    char *headers = ch_http_message_headers_raw(message, &headers_length);
    char *client_address = ch_listener_socket_address(
        connection->client_descriptor, 1);
    ch_developer_capture *capture = NULL;
    if (headers != NULL && client_address != NULL) {
        ch_developer_capture_metadata metadata = {
            .flow_id = route == NULL ? 0U : route->flow_id,
            .profile = connection->listener->profile_name,
            .client_address = client_address,
            .chain_name = route == NULL ? "map-local" : route->chain_name,
            .method = message->method,
            .url = message->url,
            .scheme = scheme,
            .host = message->host,
            .request_headers = headers,
            .request_headers_length = headers_length
        };
        ch_error error;
        capture = ch_developer_capture_begin(
            connection->listener->developer, &metadata, &error);
        if (capture != NULL && message->body_length > 0U) {
            ch_developer_capture_request_body(
                capture, message->body, message->body_length);
        }
    }
    free(headers);
    free(client_address);
    return capture;
}

static char *ch_http_scheme(const ch_developer_http_message *message) {
    if (message->url == NULL) return NULL;
    const char *separator = strstr(message->url, "://");
    if (separator == NULL) return NULL;
    return strndup(message->url, (size_t)(separator - message->url));
}

static char *ch_http_target_for_message(
    const ch_developer_http_message *message, const char *scheme) {
    return ch_http_authority_target(
        message->host, scheme != NULL && strcasecmp(scheme, "https") == 0 ?
                           "443" : "80");
}

static char *ch_http_target_host(const char *target) {
    if (target == NULL) return NULL;
    if (target[0] == '[') {
        const char *end = strchr(target, ']');
        return end == NULL ? NULL : strndup(target + 1U,
                                            (size_t)(end - target - 1U));
    }
    const char *separator = strrchr(target, ':');
    return separator == NULL ? ch_strdup(target) :
        strndup(target, (size_t)(separator - target));
}

static SSL *ch_http_upstream_tls(int descriptor, const char *host,
                                 SSL_CTX **out_context, ch_error *error) {
    *out_context = SSL_CTX_new(TLS_client_method());
    if (*out_context == NULL ||
        SSL_CTX_set_min_proto_version(*out_context, TLS1_2_VERSION) != 1 ||
        SSL_CTX_set_default_verify_paths(*out_context) != 1) {
        SSL_CTX_free(*out_context);
        *out_context = NULL;
        ch_error_set(error, CH_ERROR_IO,
                     "initialize upstream TLS context");
        return NULL;
    }
    SSL_CTX_set_verify(*out_context, SSL_VERIFY_PEER, NULL);
    SSL *tls = SSL_new(*out_context);
    X509_VERIFY_PARAM *verify = tls == NULL ? NULL : SSL_get0_param(tls);
    uint8_t address[16];
    bool okay = tls != NULL && verify != NULL &&
        SSL_set_fd(tls, descriptor) == 1 &&
        SSL_set_tlsext_host_name(tls, host) == 1;
    if (okay) {
        okay = (inet_pton(AF_INET, host, address) == 1 ||
                inet_pton(AF_INET6, host, address) == 1) ?
            X509_VERIFY_PARAM_set1_ip_asc(verify, host) == 1 :
            X509_VERIFY_PARAM_set1_host(verify, host, 0U) == 1;
    }
    if (okay) okay = SSL_connect(tls) == 1;
    if (!okay) {
        SSL_free(tls);
        SSL_CTX_free(*out_context);
        *out_context = NULL;
        ch_error_set(error, CH_ERROR_IO,
                     "establish verified upstream TLS connection");
        return NULL;
    }
    return tls;
}

static bool ch_http_send_synthetic(ch_http_io *client, int status,
                                   const char *body) {
    ch_developer_http_message response;
    memset(&response, 0, sizeof(response));
    response.status = status;
    response.body_length = strlen(body == NULL ? "" : body);
    response.body = response.body_length == 0U ? NULL :
        (uint8_t *)ch_strdup(body);
    response.body_set = true;
    bool initialized = (response.body != NULL || response.body_length == 0U) &&
        ch_http_message_header_add(
            &response, "Content-Type", "text/plain; charset=utf-8") &&
        ch_http_message_header_add(&response, "Connection", "close");
    size_t length = 0U;
    char *serialized = initialized ? ch_http_serialize_message(
        &response, true, &length) : NULL;
    bool okay = serialized != NULL && ch_http_io_write_all(
        client, (const uint8_t *)serialized, length);
    free(serialized);
    ch_developer_http_message_clear(&response);
    return okay;
}

static void ch_http_flow_bytes(ch_listener_connection *connection,
                               uint64_t rx, uint64_t tx) {
    if (connection->listener->flow_bytes != NULL &&
        connection->flow_id != 0U) {
        connection->listener->flow_bytes(
            connection->flow_id, rx, tx,
            connection->listener->flow_context);
    }
}

static bool ch_http_forward_message(
    ch_listener_connection *connection,
    ch_http_io *client,
    ch_developer_http_message *request,
    const ch_proxy_route *existing_route,
    int existing_remote,
    ch_error *error) {
    ch_developer_http_result request_result;
    ch_status status = ch_developer_process_request(
        connection->listener->developer, request, &request_result, error);
    if (status != CH_OK) {
        (void)ch_http_send_synthetic(client, 502, "Developer tooling failed");
        return false;
    }
    if (request_result.drop) {
        (void)ch_http_send_synthetic(client, 403, "Dropped by breakpoint");
        ch_developer_http_result_clear(&request_result);
        return true;
    }
    if (request_result.local_response) {
        char *capture_scheme = ch_http_scheme(request);
        ch_developer_capture *capture = ch_http_begin_message_capture(
            connection, request, NULL,
            capture_scheme == NULL ? "http" : capture_scheme);
        free(capture_scheme);
        size_t response_length = 0U;
        char *response = ch_http_serialize_message(
            &request_result.message, true, &response_length);
        bool okay = response != NULL && ch_http_io_write_all(
            client, (const uint8_t *)response, response_length);
        if (capture != NULL && response != NULL) {
            ch_developer_capture_response(
                capture, (const uint8_t *)response, response_length);
            ch_developer_capture_finish(capture,
                                        okay ? NULL : "write response failed");
        }
        free(response);
        ch_developer_http_result_clear(&request_result);
        return okay;
    }
    ch_developer_http_message *outgoing = &request_result.message;
    char *scheme = ch_http_scheme(outgoing);
    char *target = ch_http_target_for_message(outgoing, scheme);
    if (scheme == NULL || target == NULL ||
        (strcasecmp(scheme, "http") != 0 &&
         strcasecmp(scheme, "https") != 0)) {
        free(scheme);
        free(target);
        ch_developer_http_result_clear(&request_result);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "unsupported forwarded URL");
        (void)ch_http_send_synthetic(client, 400, "Invalid forwarded URL");
        return false;
    }
    ch_proxy_route route_storage;
    const ch_proxy_route *route = existing_route;
    int remote = existing_remote;
    if (route == NULL || request_result.remote_url != NULL) {
        if (remote >= 0 && remote != connection->remote_descriptor) {
            ch_listener_close_descriptor(&remote);
        }
        if (connection->remote_descriptor >= 0 &&
            request_result.remote_url != NULL) {
            pthread_mutex_lock(&connection->listener->mutex);
            int stale = connection->remote_descriptor;
            connection->remote_descriptor = -1;
            pthread_mutex_unlock(&connection->listener->mutex);
            ch_listener_close_descriptor(&stale);
        }
        status = ch_listener_dial(connection, target, &route_storage, error);
        route = &route_storage;
        remote = connection->remote_descriptor;
    }
    if (status != CH_OK || route == NULL ||
        route->action != CH_PROXY_ROUTE_CONNECT || remote < 0) {
        (void)ch_http_send_synthetic(client, 502, "Bad Gateway");
        free(scheme);
        free(target);
        ch_developer_http_result_clear(&request_result);
        return false;
    }
    SSL_CTX *upstream_context = NULL;
    SSL *upstream_tls = NULL;
    if (strcasecmp(scheme, "https") == 0) {
        char *host = ch_http_target_host(target);
        upstream_tls = host == NULL ? NULL : ch_http_upstream_tls(
            remote, host, &upstream_context, error);
        free(host);
        if (upstream_tls == NULL) {
            (void)ch_http_send_synthetic(client, 502, "TLS upstream failed");
            free(scheme);
            free(target);
            ch_developer_http_result_clear(&request_result);
            return false;
        }
    }
    ch_http_io upstream = {.descriptor = remote, .tls = upstream_tls};
    ch_developer_capture *capture = ch_http_begin_message_capture(
        connection, outgoing, route, scheme);
    size_t request_length = 0U;
    char *serialized_request = ch_http_serialize_message(
        outgoing, false, &request_length);
    if (serialized_request == NULL || !ch_http_io_write_all(
            &upstream, (const uint8_t *)serialized_request, request_length)) {
        if (capture != NULL) {
            ch_developer_capture_finish(capture,
                                        "write request to upstream failed");
        }
        free(serialized_request);
        SSL_free(upstream_tls);
        SSL_CTX_free(upstream_context);
        free(scheme);
        free(target);
        ch_developer_http_result_clear(&request_result);
        return false;
    }
    ch_http_flow_bytes(connection, 0U, (uint64_t)request_length);
    free(serialized_request);
    ch_developer_http_message response_message;
    memset(&response_message, 0, sizeof(response_message));
    status = ch_http_parse_message(
        &upstream, HTTP_RESPONSE, NULL, 0U, outgoing->method, NULL, NULL,
        &response_message, error);
    ch_http_io_dispose(&upstream);
    if (status != CH_OK) {
        if (capture != NULL) {
            ch_developer_capture_finish(capture,
                                        "read response from upstream failed");
        }
        SSL_free(upstream_tls);
        SSL_CTX_free(upstream_context);
        free(scheme);
        free(target);
        ch_developer_http_result_clear(&request_result);
        return false;
    }
    ch_developer_http_result response_result;
    status = ch_developer_process_response(
        connection->listener->developer, outgoing, &response_message,
        &response_result, error);
    ch_developer_http_message_clear(&response_message);
    if (status != CH_OK) {
        if (capture != NULL) {
            ch_developer_capture_finish(capture,
                                        "apply response tooling failed");
        }
        SSL_free(upstream_tls);
        SSL_CTX_free(upstream_context);
        free(scheme);
        free(target);
        ch_developer_http_result_clear(&request_result);
        return false;
    }
    if (response_result.drop) {
        if (capture != NULL) ch_developer_capture_finish(capture, NULL);
        (void)ch_http_send_synthetic(client, 403,
                                     "Dropped by breakpoint");
        ch_developer_http_result_clear(&response_result);
        SSL_free(upstream_tls);
        SSL_CTX_free(upstream_context);
        free(scheme);
        free(target);
        ch_developer_http_result_clear(&request_result);
        return true;
    }
    size_t response_length = 0U;
    char *serialized_response = ch_http_serialize_message(
        &response_result.message, true, &response_length);
    bool okay = serialized_response != NULL && ch_http_io_write_all(
        client, (const uint8_t *)serialized_response, response_length);
    if (serialized_response != NULL) {
        ch_http_flow_bytes(connection, (uint64_t)response_length, 0U);
        if (capture != NULL) {
            ch_developer_capture_response(
                capture, (const uint8_t *)serialized_response,
                response_length);
        }
    }
    if (capture != NULL) {
        ch_developer_capture_finish(capture,
                                    okay ? NULL : "write response failed");
    }
    free(serialized_response);
    ch_developer_http_result_clear(&response_result);
    SSL_free(upstream_tls);
    SSL_CTX_free(upstream_context);
    free(scheme);
    free(target);
    ch_developer_http_result_clear(&request_result);
    return okay;
}

static void ch_http_close_remote_transaction(
    ch_listener_connection *connection, const char *reason) {
    pthread_mutex_lock(&connection->listener->mutex);
    int remote = connection->remote_descriptor;
    connection->remote_descriptor = -1;
    pthread_mutex_unlock(&connection->listener->mutex);
    ch_listener_close_descriptor(&remote);
    if (connection->listener->flow_close != NULL &&
        connection->flow_id != 0U) {
        connection->listener->flow_close(
            connection->flow_id, reason,
            connection->listener->flow_context);
        connection->flow_id = 0U;
    }
}

static bool ch_http_handle_mitm(
    ch_listener_connection *connection,
    const ch_proxy_route *initial_route,
    const char *target,
    const uint8_t *early_data,
    size_t early_data_length) {
    char *host = ch_http_target_host(target);
    ch_error error;
    SSL_CTX *server_context = NULL;
    ch_status status = host == NULL ? CH_ERROR_INVALID_ARGUMENT :
        ch_developer_tls_server_context(
            connection->listener->developer, host, &server_context, &error);
    if (status != CH_OK) {
        free(host);
        SSL_CTX_free(server_context);
        return false;
    }
    static const char connected[] =
        "HTTP/1.1 200 Connection established\r\n"
        "Proxy-Agent: clambhook\r\n\r\n";
    if (!ch_listener_send_all(connection->client_descriptor, connected,
                              sizeof(connected) - 1U)) {
        free(host);
        SSL_CTX_free(server_context);
        return false;
    }
    SSL *client_tls = SSL_new(server_context);
    BIO *read_bio = client_tls == NULL ? NULL : ch_http_prefix_bio_new(
        connection->client_descriptor, early_data, early_data_length);
    BIO *write_bio = client_tls == NULL ? NULL : BIO_new_socket(
        connection->client_descriptor, BIO_NOCLOSE);
    if (client_tls == NULL || read_bio == NULL || write_bio == NULL) {
        BIO_free(read_bio);
        BIO_free(write_bio);
        SSL_free(client_tls);
        SSL_CTX_free(server_context);
        free(host);
        return false;
    }
    SSL_set0_rbio(client_tls, read_bio);
    SSL_set0_wbio(client_tls, write_bio);
    if (SSL_accept(client_tls) != 1) {
        SSL_free(client_tls);
        SSL_CTX_free(server_context);
        free(host);
        return false;
    }
    ch_http_io client_io = {
        .descriptor = connection->client_descriptor,
        .tls = client_tls
    };
    const ch_proxy_route *route = initial_route;
    int remote = connection->remote_descriptor;
    bool handled = false;
    for (;;) {
        ch_developer_http_message request;
        memset(&request, 0, sizeof(request));
        status = ch_http_parse_message(
            &client_io, HTTP_REQUEST, NULL, 0U, NULL, "https", target,
            &request, &error);
        if (status != CH_OK) {
            ch_developer_http_message_clear(&request);
            break;
        }
        bool close_after = false;
        const char *connection_header = ch_http_message_header(
            &request, "Connection");
        if (connection_header != NULL &&
            strcasecmp(connection_header, "close") == 0) close_after = true;
        bool okay = ch_http_forward_message(
            connection, &client_io, &request, route, remote, &error);
        ch_developer_http_message_clear(&request);
        handled = handled || okay;
        ch_http_close_remote_transaction(
            connection, okay ? "HTTP transaction completed" :
                               "HTTP transaction failed");
        route = NULL;
        remote = -1;
        if (!okay || close_after) break;
    }
    (void)SSL_shutdown(client_tls);
    ch_http_io_dispose(&client_io);
    SSL_free(client_tls);
    SSL_CTX_free(server_context);
    free(host);
    return handled;
}

static void ch_http_finish_capture(ch_listener_connection *connection,
                                   const char *error_message) {
    if (connection->developer_capture == NULL) return;
    ch_developer_capture_finish(connection->developer_capture, error_message);
    connection->developer_capture = NULL;
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
    if (!is_connect) {
        uri[-1] = ' ';
        version[-1] = ' ';
        ch_http_io client_io = {.descriptor = client};
        ch_developer_http_message parsed_request;
        memset(&parsed_request, 0, sizeof(parsed_request));
        ch_error parse_error;
        ch_status parse_status = ch_http_parse_message(
            &client_io, HTTP_REQUEST, (const uint8_t *)request, length,
            NULL, "http", NULL, &parsed_request, &parse_error);
        free(target);
        free(request);
        ch_http_io_dispose(&client_io);
        if (parse_status != CH_OK) {
            ch_http_status(client, 400, "Bad Request", 0);
            return;
        }
        ch_listener_clear_timeout(client);
        ch_error forward_error;
        (void)ch_http_forward_message(
            connection, &client_io, &parsed_request, NULL, -1,
            &forward_error);
        ch_developer_http_message_clear(&parsed_request);
        return;
    }
    ch_proxy_route route;
    ch_error error;
    ch_status status = ch_listener_dial(connection, target, &route, &error);
    if (status != CH_OK) {
        free(target);
        ch_http_status(client, 502, "Bad Gateway", 0);
        free(request);
        return;
    }
    if (route.action != CH_PROXY_ROUTE_CONNECT) {
        free(target);
        ch_http_status(client, 403, "Forbidden", 0);
        free(request);
        return;
    }
    ch_listener_clear_timeout(client);
    if (is_connect && connection->listener->developer != NULL) {
        char *mitm_host = ch_http_target_host(target);
        bool should_mitm = mitm_host != NULL && ch_developer_should_mitm(
            connection->listener->developer, mitm_host);
        free(mitm_host);
        if (should_mitm) {
            (void)ch_http_handle_mitm(
                connection, &route, target,
                (const uint8_t *)body_start, buffered_body_length);
            free(target);
            free(request);
            return;
        }
    }
    if (!is_connect && connection->listener->developer != NULL) {
        size_t host_length = 0U;
        const char *host_value = ch_http_header_value(
            line_end + 2U, header_end, "Host", &host_length);
        char *host = host_value == NULL ? ch_strdup(target) :
            strndup(host_value, host_length);
        char *client_address = ch_listener_socket_address(client, 1);
        if (host != NULL && client_address != NULL) {
            ch_developer_capture_metadata metadata = {
                .flow_id = route.flow_id,
                .profile = connection->listener->profile_name,
                .client_address = client_address,
                .chain_name = route.chain_name,
                .method = method,
                .url = uri,
                .scheme = "http",
                .host = host,
                .request_headers = line_end + 2U,
                .request_headers_length =
                    (size_t)(header_end + 2U - (line_end + 2U))
            };
            ch_error capture_error;
            connection->developer_capture = ch_developer_capture_begin(
                connection->listener->developer, &metadata, &capture_error);
        }
        free(client_address);
        free(host);
    }
    free(target);
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
                                  strlen(forward_header))) {
            free(forward_header);
            ch_http_finish_capture(connection, "write request headers failed");
            free(request);
            return;
        }
        free(forward_header);
        if (buffered_body_length > 0U) {
            if (!ch_listener_send_all(connection->remote_descriptor, body_start,
                                      buffered_body_length)) {
                ch_http_finish_capture(connection, "write request body failed");
                free(request);
                return;
            }
            ch_developer_capture_request_body(
                connection->developer_capture, (const uint8_t *)body_start,
                buffered_body_length);
        }
    }
    free(request);
    ch_listener_relay(connection);
    ch_http_finish_capture(connection, NULL);
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
    ch_http_finish_capture(
        connection, "connection closed before capture completed");
    pthread_mutex_lock(&connection->listener->mutex);
    int remote = connection->remote_descriptor;
    int client = connection->client_descriptor;
    connection->remote_descriptor = -1;
    connection->client_descriptor = -1;
    pthread_mutex_unlock(&connection->listener->mutex);
    ch_listener_close_descriptor(&remote);
    ch_listener_close_descriptor(&client);
    if (connection->listener->flow_close != NULL &&
        connection->flow_id != 0U) {
        connection->listener->flow_close(
            connection->flow_id, "closed",
            connection->listener->flow_context);
    }
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
    listener->packet_dial = options->packet_dial;
    listener->dial_context = options->dial_context;
    listener->flow_bytes = options->flow_bytes;
    listener->flow_close = options->flow_close;
    listener->flow_context = options->flow_context;
    listener->developer = options->developer;
    listener->profile_name = ch_strdup(
        options->profile_name == NULL ? "" : options->profile_name);
    if (listener->configured_address == NULL || listener->username == NULL ||
        listener->password == NULL || listener->profile_name == NULL ||
        pthread_mutex_init(&listener->mutex, NULL) != 0) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "initialize proxy listener");
        free(listener->configured_address);
        free(listener->username);
        free(listener->password);
        free(listener->profile_name);
        free(listener);
        return NULL;
    }
    if (pthread_cond_init(&listener->condition, NULL) != 0) {
        pthread_mutex_destroy(&listener->mutex);
        free(listener->configured_address);
        free(listener->username);
        free(listener->password);
        free(listener->profile_name);
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
        free(listener->profile_name);
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
    free(listener->profile_name);
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
