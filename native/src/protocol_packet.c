#include "clambhook/protocol.h"

#include <arpa/inet.h>
#include <errno.h>
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
#include <unistd.h>

#include "internal.h"
#include "protocol_shadowsocks.h"

#define CH_PACKET_MAX_WIRE_SIZE 65507U

typedef enum ch_packet_kind {
    CH_PACKET_DIRECT = 1,
    CH_PACKET_SHADOWSOCKS = 2
} ch_packet_kind;

struct ch_packet_connection {
    ch_packet_kind kind;
    int ipv4_descriptor;
    int ipv6_descriptor;
    ch_ss_cipher cipher;
    uint8_t master_key[32];
    pthread_mutex_t send_mutex;
    pthread_mutex_t receive_mutex;
};

static void ch_packet_close_descriptor(int *descriptor) {
    if (descriptor == NULL || *descriptor < 0) return;
    (void)shutdown(*descriptor, SHUT_RDWR);
    (void)close(*descriptor);
    *descriptor = -1;
}

static char *ch_packet_optional_string(const ch_config_table *table,
                                       const char *key) {
    char *value = NULL;
    ch_error ignored;
    if (table == NULL || !ch_config_table_has(table, key) ||
        ch_config_table_get_string(table, key, &value, &ignored) != CH_OK) {
        free(value);
        return NULL;
    }
    return value;
}

static ch_status ch_packet_split_target(const char *target, char **out_host,
                                        char **out_service,
                                        ch_error *error) {
    *out_host = NULL;
    *out_service = NULL;
    if (target == NULL || target[0] == '\0') {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "packet target is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    const char *host_start = target;
    const char *host_end = NULL;
    const char *service_start = NULL;
    if (target[0] == '[') {
        host_start = target + 1;
        host_end = strchr(host_start, ']');
        if (host_end == NULL || host_end[1] != ':' || host_end[2] == '\0') {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "invalid bracketed packet target");
            return CH_ERROR_INVALID_ARGUMENT;
        }
        service_start = host_end + 2;
    } else {
        const char *separator = strrchr(target, ':');
        if (separator == NULL || separator == target || separator[1] == '\0' ||
            strchr(target, ':') != separator) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "packet target must be host:port");
            return CH_ERROR_INVALID_ARGUMENT;
        }
        host_end = separator;
        service_start = separator + 1;
    }
    size_t host_length = (size_t)(host_end - host_start);
    char *host = malloc(host_length + 1U);
    char *service = ch_strdup(service_start);
    if (host == NULL || service == NULL) {
        free(host);
        free(service);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy packet target");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    memcpy(host, host_start, host_length);
    host[host_length] = '\0';
    *out_host = host;
    *out_service = service;
    return CH_OK;
}

static ch_status ch_packet_resolve(const char *target, int connected,
                                   struct addrinfo **out_addresses,
                                   ch_error *error) {
    char *host = NULL;
    char *service = NULL;
    ch_status status = ch_packet_split_target(target, &host, &service, error);
    if (status != CH_OK) return status;
    struct addrinfo hints;
    memset(&hints, 0, sizeof(hints));
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_DGRAM;
    hints.ai_protocol = IPPROTO_UDP;
    if (!connected) hints.ai_flags = AI_ADDRCONFIG;
    struct addrinfo *addresses = NULL;
    int lookup = getaddrinfo(host, service, &hints, &addresses);
    free(host);
    free(service);
    if (lookup != 0) {
        ch_error_set(error, CH_ERROR_IO, "resolve packet target: %s",
                     gai_strerror(lookup));
        return CH_ERROR_IO;
    }
    *out_addresses = addresses;
    return CH_OK;
}

static ch_status ch_packet_format_source(const struct sockaddr *address,
                                         socklen_t address_length,
                                         char **out_source,
                                         ch_error *error) {
    char host[1025];
    char service[32];
    int result = getnameinfo(address, address_length, host, sizeof(host),
                             service, sizeof(service),
                             NI_NUMERICHOST | NI_NUMERICSERV);
    if (result != 0) {
        ch_error_set(error, CH_ERROR_IO, "format UDP source: %s",
                     gai_strerror(result));
        return CH_ERROR_IO;
    }
    int ipv6 = address->sa_family == AF_INET6;
    size_t length = strlen(host) + strlen(service) + (ipv6 ? 4U : 2U);
    char *source = malloc(length);
    if (source == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy UDP source");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    (void)snprintf(source, length, ipv6 ? "[%s]:%s" : "%s:%s", host,
                   service);
    *out_source = source;
    return CH_OK;
}

static ch_packet_connection *ch_packet_allocate(ch_packet_kind kind,
                                                ch_error *error) {
    ch_packet_connection *connection = calloc(1U, sizeof(*connection));
    if (connection == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate packet connection");
        return NULL;
    }
    connection->kind = kind;
    connection->ipv4_descriptor = -1;
    connection->ipv6_descriptor = -1;
    if (pthread_mutex_init(&connection->send_mutex, NULL) != 0) {
        free(connection);
        ch_error_set(error, CH_ERROR_IO, "initialize packet send lock");
        return NULL;
    }
    if (pthread_mutex_init(&connection->receive_mutex, NULL) != 0) {
        (void)pthread_mutex_destroy(&connection->send_mutex);
        free(connection);
        ch_error_set(error, CH_ERROR_IO, "initialize packet receive lock");
        return NULL;
    }
    return connection;
}

static ch_status ch_packet_connect_server(ch_packet_connection *connection,
                                          const char *target,
                                          ch_error *error) {
    struct addrinfo *addresses = NULL;
    ch_status status = ch_packet_resolve(target, 1, &addresses, error);
    if (status != CH_OK) return status;
    int descriptor = -1;
    int saved_error = EHOSTUNREACH;
    for (const struct addrinfo *candidate = addresses; candidate != NULL;
         candidate = candidate->ai_next) {
        descriptor = socket(candidate->ai_family, candidate->ai_socktype,
                            candidate->ai_protocol);
        if (descriptor < 0) {
            saved_error = errno;
            continue;
        }
        if (connect(descriptor, candidate->ai_addr,
                    candidate->ai_addrlen) == 0) {
            break;
        }
        saved_error = errno;
        ch_packet_close_descriptor(&descriptor);
    }
    freeaddrinfo(addresses);
    if (descriptor < 0) {
        ch_error_set(error, CH_ERROR_IO, "connect UDP server %s: %s", target,
                     strerror(saved_error));
        return CH_ERROR_IO;
    }
    connection->ipv4_descriptor = descriptor;
    return CH_OK;
}

ch_status ch_protocol_chain_dial_packet(const ch_config_table *chain,
                                        ch_packet_connection **out_connection,
                                        ch_error *error) {
    ch_error_clear(error);
    if (chain == NULL || out_connection == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "packet chain and output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_connection = NULL;
    const ch_config_array *servers = ch_config_table_get_array(chain, "server");
    size_t count = ch_config_array_count(servers);
    if (count != 1U) {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "native packet chains currently require one server");
        return CH_ERROR_UNSUPPORTED;
    }
    const ch_config_table *server = ch_config_array_get_table(servers, 0U);
    char *protocol = ch_packet_optional_string(server, "protocol");
    if (protocol == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "packet chain protocol is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ch_packet_kind kind;
    if (strcasecmp(protocol, "direct") == 0) {
        kind = CH_PACKET_DIRECT;
    } else if (strcasecmp(protocol, "shadowsocks") == 0) {
        kind = CH_PACKET_SHADOWSOCKS;
    } else {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "native packet protocol %s is not ported yet", protocol);
        free(protocol);
        return CH_ERROR_UNSUPPORTED;
    }
    free(protocol);
    ch_packet_connection *connection = ch_packet_allocate(kind, error);
    if (connection == NULL) return error == NULL ? CH_ERROR_OUT_OF_MEMORY :
                                                  error->code;
    if (kind == CH_PACKET_SHADOWSOCKS) {
        const ch_config_table *settings = ch_config_table_get_table(
            server, "settings");
        char *method = ch_packet_optional_string(settings, "method");
        char *password = ch_packet_optional_string(settings, "password");
        char *address = ch_packet_optional_string(server, "address");
        if (method == NULL || password == NULL || address == NULL ||
            password[0] == '\0' || address[0] == '\0') {
            free(method); free(password); free(address);
            ch_packet_connection_close(connection);
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "Shadowsocks packet method, password, and address "
                         "are required");
            return CH_ERROR_INVALID_ARGUMENT;
        }
        ch_status status = ch_ss_cipher_from_name(method, &connection->cipher,
                                                  error);
        if (status == CH_OK) {
            status = ch_ss_evp_bytes_to_key(
                (const uint8_t *)password, strlen(password),
                connection->cipher.key_size, connection->master_key, error);
        }
        if (status == CH_OK) {
            status = ch_packet_connect_server(connection, address, error);
        }
        free(method); free(password); free(address);
        if (status != CH_OK) {
            ch_packet_connection_close(connection);
            return status;
        }
    }
    *out_connection = connection;
    return CH_OK;
}

ch_status ch_protocol_direct_packet_dial(
    ch_packet_connection **out_connection,
    ch_error *error) {
    ch_error_clear(error);
    if (out_connection == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "direct packet output is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_connection = ch_packet_allocate(CH_PACKET_DIRECT, error);
    if (*out_connection == NULL) {
        return error == NULL ? CH_ERROR_OUT_OF_MEMORY : error->code;
    }
    return CH_OK;
}

static ch_status ch_packet_send_direct(ch_packet_connection *connection,
                                       const char *target,
                                       const uint8_t *payload,
                                       size_t payload_length,
                                       ch_error *error) {
    struct addrinfo *addresses = NULL;
    ch_status status = ch_packet_resolve(target, 0, &addresses, error);
    if (status != CH_OK) return status;
    int saved_error = EHOSTUNREACH;
    int sent = 0;
    for (const struct addrinfo *candidate = addresses; candidate != NULL;
         candidate = candidate->ai_next) {
        int *descriptor = candidate->ai_family == AF_INET6 ?
            &connection->ipv6_descriptor : &connection->ipv4_descriptor;
        if (*descriptor < 0) {
            *descriptor = socket(candidate->ai_family, candidate->ai_socktype,
                                 candidate->ai_protocol);
            if (*descriptor < 0) {
                saved_error = errno;
                continue;
            }
        }
        ssize_t written;
        do {
            written = sendto(*descriptor, payload, payload_length, 0,
                             candidate->ai_addr, candidate->ai_addrlen);
        } while (written < 0 && errno == EINTR);
        if (written == (ssize_t)payload_length) {
            sent = 1;
            break;
        }
        saved_error = written < 0 ? errno : EMSGSIZE;
    }
    freeaddrinfo(addresses);
    if (!sent) {
        ch_error_set(error, CH_ERROR_IO, "send UDP packet to %s: %s", target,
                     strerror(saved_error));
        return CH_ERROR_IO;
    }
    return CH_OK;
}

ch_status ch_packet_connection_send(ch_packet_connection *connection,
                                    const char *target,
                                    const uint8_t *payload,
                                    size_t payload_length,
                                    ch_error *error) {
    ch_error_clear(error);
    if (connection == NULL || target == NULL ||
        (payload == NULL && payload_length > 0U) ||
        payload_length > CH_PACKET_MAX_WIRE_SIZE) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "invalid packet send input");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    (void)pthread_mutex_lock(&connection->send_mutex);
    ch_status status;
    if (connection->kind == CH_PACKET_DIRECT) {
        status = ch_packet_send_direct(connection, target, payload,
                                       payload_length, error);
    } else {
        uint8_t *frame = NULL;
        size_t frame_length = 0U;
        status = ch_ss_encrypt_datagram(
            &connection->cipher, connection->master_key, target, payload,
            payload_length, &frame, &frame_length, error);
        if (status == CH_OK && frame_length > CH_PACKET_MAX_WIRE_SIZE) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "encrypted UDP packet is too large");
            status = CH_ERROR_INVALID_ARGUMENT;
        }
        if (status == CH_OK) {
            ssize_t written;
            do {
                written = send(connection->ipv4_descriptor, frame,
                               frame_length, 0);
            } while (written < 0 && errno == EINTR);
            if (written != (ssize_t)frame_length) {
                ch_error_set(error, CH_ERROR_IO,
                             "send Shadowsocks UDP packet: %s",
                             written < 0 ? strerror(errno) :
                                           "short datagram write");
                status = CH_ERROR_IO;
            }
        }
        free(frame);
    }
    (void)pthread_mutex_unlock(&connection->send_mutex);
    return status;
}

static ch_status ch_packet_receive_direct(ch_packet_connection *connection,
                                          uint8_t *buffer,
                                          size_t buffer_capacity,
                                          size_t *out_length,
                                          char **out_source,
                                          int timeout_milliseconds,
                                          ch_error *error) {
    struct pollfd descriptors[2];
    nfds_t count = 0U;
    if (connection->ipv4_descriptor >= 0) {
        descriptors[count].fd = connection->ipv4_descriptor;
        descriptors[count].events = POLLIN;
        descriptors[count].revents = 0;
        ++count;
    }
    if (connection->ipv6_descriptor >= 0) {
        descriptors[count].fd = connection->ipv6_descriptor;
        descriptors[count].events = POLLIN;
        descriptors[count].revents = 0;
        ++count;
    }
    if (count == 0U) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "send a direct UDP packet before receiving");
        return CH_ERROR_INVALID_STATE;
    }
    int ready;
    do {
        ready = poll(descriptors, count, timeout_milliseconds);
    } while (ready < 0 && errno == EINTR);
    if (ready == 0) {
        ch_error_set(error, CH_ERROR_NOT_FOUND, "UDP receive timed out");
        return CH_ERROR_NOT_FOUND;
    }
    if (ready < 0) {
        ch_error_set(error, CH_ERROR_IO, "wait for UDP packet: %s",
                     strerror(errno));
        return CH_ERROR_IO;
    }
    int descriptor = -1;
    for (nfds_t index = 0U; index < count; ++index) {
        if ((descriptors[index].revents & POLLIN) != 0) {
            descriptor = descriptors[index].fd;
            break;
        }
    }
    if (descriptor < 0) {
        ch_error_set(error, CH_ERROR_IO, "UDP packet socket closed");
        return CH_ERROR_IO;
    }
    struct sockaddr_storage source_address;
    struct iovec vector = {.iov_base = buffer, .iov_len = buffer_capacity};
    struct msghdr message;
    memset(&message, 0, sizeof(message));
    message.msg_name = &source_address;
    message.msg_namelen = (socklen_t)sizeof(source_address);
    message.msg_iov = &vector;
    message.msg_iovlen = 1U;
    ssize_t length;
    do {
        length = recvmsg(descriptor, &message, 0);
    } while (length < 0 && errno == EINTR);
    if (length < 0) {
        ch_error_set(error, CH_ERROR_IO, "receive UDP packet: %s",
                     strerror(errno));
        return CH_ERROR_IO;
    }
    if ((message.msg_flags & MSG_TRUNC) != 0 ||
        (size_t)length > buffer_capacity) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "UDP receive buffer is too small");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ch_status status = ch_packet_format_source(
        (const struct sockaddr *)&source_address, message.msg_namelen,
        out_source, error);
    if (status == CH_OK) *out_length = (size_t)length;
    return status;
}

ch_status ch_packet_connection_receive(ch_packet_connection *connection,
                                       uint8_t *buffer,
                                       size_t buffer_capacity,
                                       size_t *out_length,
                                       char **out_source,
                                       ch_error *error) {
    return ch_packet_connection_receive_timeout(
        connection, buffer, buffer_capacity, out_length, out_source, -1,
        error);
}

ch_status ch_packet_connection_receive_timeout(
    ch_packet_connection *connection,
    uint8_t *buffer,
    size_t buffer_capacity,
    size_t *out_length,
    char **out_source,
    int timeout_milliseconds,
    ch_error *error) {
    ch_error_clear(error);
    if (connection == NULL || buffer == NULL || buffer_capacity == 0U ||
        out_length == NULL || out_source == NULL || timeout_milliseconds < -1) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "invalid packet receive input");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_length = 0U;
    *out_source = NULL;
    (void)pthread_mutex_lock(&connection->receive_mutex);
    ch_status status;
    if (connection->kind == CH_PACKET_DIRECT) {
        status = ch_packet_receive_direct(connection, buffer, buffer_capacity,
                                          out_length, out_source,
                                          timeout_milliseconds, error);
    } else {
        uint8_t frame[CH_PACKET_MAX_WIRE_SIZE];
        struct pollfd wait = {
            .fd = connection->ipv4_descriptor,
            .events = POLLIN
        };
        int ready;
        do {
            ready = poll(&wait, 1U, timeout_milliseconds);
        } while (ready < 0 && errno == EINTR);
        ssize_t frame_length = -1;
        if (ready > 0 && (wait.revents & POLLIN) != 0) {
            do {
                frame_length = recv(connection->ipv4_descriptor, frame,
                                    sizeof(frame), 0);
            } while (frame_length < 0 && errno == EINTR);
        }
        if (ready == 0) {
            ch_error_set(error, CH_ERROR_NOT_FOUND,
                         "Shadowsocks UDP receive timed out");
            status = CH_ERROR_NOT_FOUND;
        } else if (ready < 0 || frame_length < 0) {
            ch_error_set(error, CH_ERROR_IO,
                         "receive Shadowsocks UDP packet: %s",
                         strerror(errno));
            status = CH_ERROR_IO;
        } else {
            uint8_t *payload = NULL;
            size_t payload_length = 0U;
            status = ch_ss_decrypt_datagram(
                &connection->cipher, connection->master_key, frame,
                (size_t)frame_length, out_source, &payload, &payload_length,
                error);
            if (status == CH_OK && payload_length > buffer_capacity) {
                free(*out_source);
                *out_source = NULL;
                ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                             "UDP receive buffer is too small");
                status = CH_ERROR_INVALID_ARGUMENT;
            }
            if (status == CH_OK) {
                memcpy(buffer, payload, payload_length);
                *out_length = payload_length;
            }
            free(payload);
        }
    }
    (void)pthread_mutex_unlock(&connection->receive_mutex);
    return status;
}

void ch_packet_connection_close(ch_packet_connection *connection) {
    if (connection == NULL) return;
    ch_packet_close_descriptor(&connection->ipv4_descriptor);
    ch_packet_close_descriptor(&connection->ipv6_descriptor);
    (void)pthread_mutex_destroy(&connection->receive_mutex);
    (void)pthread_mutex_destroy(&connection->send_mutex);
    memset(connection->master_key, 0, sizeof(connection->master_key));
    free(connection);
}
