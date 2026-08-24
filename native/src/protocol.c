#include "clambhook/protocol.h"

#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <netdb.h>
#include <openssl/err.h>
#include <openssl/ssl.h>
#include <poll.h>
#include <pthread.h>
#include <signal.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <unistd.h>

#include "clambhook/socks.h"
#include "cnet.h"
#include "internal.h"
#include "protocol_shadowsocks.h"
#include "protocol_tor.h"

#define CH_PROTOCOL_DIAL_TIMEOUT_MS 30000
#define CH_PROTOCOL_BUFFER_SIZE 32768U

typedef struct ch_tls_pump {
    SSL_CTX *context;
    SSL *ssl;
    int network_descriptor;
    int local_descriptor;
} ch_tls_pump;

static pthread_once_t ch_protocol_sigpipe_once = PTHREAD_ONCE_INIT;

static void ch_protocol_ignore_sigpipe(void) {
    (void)signal(SIGPIPE, SIG_IGN);
}

static void ch_protocol_close(int *descriptor) {
    if (descriptor == NULL || *descriptor < 0) return;
    (void)shutdown(*descriptor, SHUT_RDWR);
    (void)close(*descriptor);
    *descriptor = -1;
}

static void ch_protocol_socket_timeout(int descriptor, unsigned int milliseconds) {
    struct timeval timeout = {
        .tv_sec = (time_t)(milliseconds / 1000U),
        .tv_usec = (suseconds_t)((milliseconds % 1000U) * 1000U)
    };
    (void)setsockopt(descriptor, SOL_SOCKET, SO_RCVTIMEO, &timeout,
                     (socklen_t)sizeof(timeout));
    (void)setsockopt(descriptor, SOL_SOCKET, SO_SNDTIMEO, &timeout,
                     (socklen_t)sizeof(timeout));
#ifdef SO_NOSIGPIPE
    int enabled = 1;
    (void)setsockopt(descriptor, SOL_SOCKET, SO_NOSIGPIPE, &enabled,
                     (socklen_t)sizeof(enabled));
#endif
}

static ch_status ch_protocol_split_target(const char *target, char **out_host,
                                          char **out_service,
                                          ch_error *error) {
    *out_host = NULL;
    *out_service = NULL;
    if (target == NULL || target[0] == '\0') {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "dial target is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    const char *host_start = target;
    const char *host_end;
    const char *service_start;
    if (target[0] == '[') {
        host_start = target + 1;
        host_end = strchr(host_start, ']');
        if (host_end == NULL || host_end[1] != ':' || host_end[2] == '\0') {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "invalid bracketed dial target");
            return CH_ERROR_INVALID_ARGUMENT;
        }
        service_start = host_end + 2;
    } else {
        const char *separator = strrchr(target, ':');
        if (separator == NULL || separator == target || separator[1] == '\0' ||
            strchr(target, ':') != separator) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "dial target must be host:port");
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
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy dial target");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    memcpy(*out_host, host_start, host_length);
    (*out_host)[host_length] = '\0';
    return CH_OK;
}

ch_status ch_protocol_connect_tcp(const char *target, int *out_descriptor,
                                  ch_error *error) {
    ch_error_clear(error);
    if (out_descriptor == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "output descriptor is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_descriptor = -1;
    char *host = NULL;
    char *service = NULL;
    ch_status status = ch_protocol_split_target(target, &host, &service, error);
    if (status != CH_OK) return status;
    struct addrinfo hints;
    memset(&hints, 0, sizeof(hints));
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_STREAM;
    hints.ai_protocol = IPPROTO_TCP;
    struct addrinfo *addresses = NULL;
    int lookup = getaddrinfo(host, service, &hints, &addresses);
    free(host);
    free(service);
    if (lookup != 0) {
        ch_error_set(error, CH_ERROR_IO, "resolve dial target: %s",
                     gai_strerror(lookup));
        return CH_ERROR_IO;
    }
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
        int flags = fcntl(descriptor, F_GETFL, 0);
        if (flags < 0 || fcntl(descriptor, F_SETFL, flags | O_NONBLOCK) != 0) {
            saved_error = errno;
            ch_protocol_close(&descriptor);
            continue;
        }
        int connected = connect(descriptor, candidate->ai_addr,
                                candidate->ai_addrlen) == 0;
        if (!connected && errno == EINPROGRESS) {
            struct pollfd wait = {.fd = descriptor, .events = POLLOUT};
            int ready;
            do {
                ready = poll(&wait, 1U, CH_PROTOCOL_DIAL_TIMEOUT_MS);
            } while (ready < 0 && errno == EINTR);
            if (ready > 0) {
                int socket_error = 0;
                socklen_t length = (socklen_t)sizeof(socket_error);
                if (getsockopt(descriptor, SOL_SOCKET, SO_ERROR, &socket_error,
                               &length) == 0 && socket_error == 0) {
                    connected = 1;
                } else {
                    saved_error = socket_error == 0 ? errno : socket_error;
                }
            } else {
                saved_error = ready == 0 ? ETIMEDOUT : errno;
            }
        } else if (!connected) {
            saved_error = errno;
        }
        if (connected) {
            (void)fcntl(descriptor, F_SETFL, flags);
            break;
        }
        ch_protocol_close(&descriptor);
    }
    freeaddrinfo(addresses);
    if (descriptor < 0) {
        ch_error_set(error, CH_ERROR_IO, "dial %s: %s", target,
                     strerror(saved_error));
        return CH_ERROR_IO;
    }
    ch_protocol_socket_timeout(descriptor, 0U);
    *out_descriptor = descriptor;
    return CH_OK;
}

ch_status ch_protocol_trojan_header(const char *password, const char *target,
                                    uint8_t **out_header,
                                    size_t *out_header_length,
                                    ch_error *error) {
    ch_error_clear(error);
    if (password == NULL || password[0] == '\0' || target == NULL ||
        out_header == NULL || out_header_length == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "trojan password, target, and output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_header = NULL;
    *out_header_length = 0U;
    uint8_t *address = NULL;
    size_t address_length = 0U;
    ch_status status = ch_socks_encode_address(target, &address,
                                               &address_length, error);
    if (status != CH_OK) return status;
    if (address_length > SIZE_MAX - 61U) {
        free(address);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate Trojan header");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    size_t total = 56U + 2U + 1U + address_length + 2U;
    uint8_t *header = malloc(total);
    if (header == NULL) {
        free(address);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate Trojan header");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    uint8_t digest[28];
    static const char hex[] = "0123456789abcdef";
    cnet_sha224((const uint8_t *)password, strlen(password), digest);
    for (size_t index = 0U; index < sizeof(digest); ++index) {
        header[index * 2U] = (uint8_t)hex[digest[index] >> 4U];
        header[index * 2U + 1U] = (uint8_t)hex[digest[index] & 0x0fU];
    }
    size_t offset = 56U;
    header[offset++] = '\r';
    header[offset++] = '\n';
    header[offset++] = 0x01U;
    memcpy(header + offset, address, address_length);
    offset += address_length;
    header[offset++] = '\r';
    header[offset] = '\n';
    free(address);
    *out_header = header;
    *out_header_length = total;
    return CH_OK;
}

static int ch_protocol_set_nonblocking(int descriptor) {
    int flags = fcntl(descriptor, F_GETFL, 0);
    return flags >= 0 && fcntl(descriptor, F_SETFL, flags | O_NONBLOCK) == 0;
}

static ssize_t ch_protocol_local_send(int descriptor, const void *bytes,
                                      size_t length) {
#ifdef MSG_NOSIGNAL
    return send(descriptor, bytes, length, MSG_NOSIGNAL);
#else
    return send(descriptor, bytes, length, 0);
#endif
}

static void *ch_protocol_tls_pump_main(void *opaque) {
    ch_tls_pump *pump = opaque;
    uint8_t outgoing[CH_PROTOCOL_BUFFER_SIZE];
    uint8_t incoming[CH_PROTOCOL_BUFFER_SIZE];
    size_t outgoing_offset = 0U;
    size_t outgoing_length = 0U;
    size_t incoming_offset = 0U;
    size_t incoming_length = 0U;
    bool local_read_open = true;
    bool tls_read_open = true;
    bool write_wants_read = false;
    bool read_wants_write = false;
    bool shutdown_started = false;

    if (!ch_protocol_set_nonblocking(pump->local_descriptor) ||
        !ch_protocol_set_nonblocking(pump->network_descriptor)) {
        local_read_open = false;
        tls_read_open = false;
    }
    while (local_read_open || tls_read_open || outgoing_length > 0U ||
           incoming_length > 0U) {
        struct pollfd waits[2] = {
            {.fd = pump->local_descriptor, .events = 0},
            {.fd = pump->network_descriptor, .events = 0}
        };
        if (local_read_open && outgoing_length == 0U) waits[0].events |= POLLIN;
        if (incoming_length > 0U) waits[0].events |= POLLOUT;
        if (tls_read_open && incoming_length == 0U && !read_wants_write) {
            waits[1].events |= POLLIN;
        }
        if (read_wants_write || (outgoing_length > 0U && !write_wants_read)) {
            waits[1].events |= POLLOUT;
        }
        if (write_wants_read) waits[1].events |= POLLIN;

        int ready;
        int wait_timeout = SSL_pending(pump->ssl) > 0 ? 0 : -1;
        do {
            ready = poll(waits, 2U, wait_timeout);
        } while (ready < 0 && errno == EINTR);
        if (ready < 0) break;
        if ((waits[0].revents & POLLNVAL) != 0 ||
            (waits[1].revents & POLLNVAL) != 0) break;

        if (incoming_length > 0U &&
            (waits[0].revents & (POLLOUT | POLLERR | POLLHUP)) != 0) {
            ssize_t sent = ch_protocol_local_send(
                pump->local_descriptor, incoming + incoming_offset,
                incoming_length);
            if (sent > 0) {
                incoming_offset += (size_t)sent;
                incoming_length -= (size_t)sent;
                if (incoming_length == 0U) incoming_offset = 0U;
            } else if (sent == 0 || (errno != EINTR && errno != EAGAIN &&
                                     errno != EWOULDBLOCK)) {
                local_read_open = false;
                incoming_length = 0U;
            }
        }
        if (local_read_open && outgoing_length == 0U &&
            (waits[0].revents & (POLLIN | POLLERR | POLLHUP)) != 0) {
            ssize_t received = recv(pump->local_descriptor, outgoing,
                                    sizeof(outgoing), 0);
            if (received > 0) {
                outgoing_offset = 0U;
                outgoing_length = (size_t)received;
                write_wants_read = false;
            } else if (received == 0 || (errno != EINTR && errno != EAGAIN &&
                                         errno != EWOULDBLOCK)) {
                local_read_open = false;
            }
        }

        bool may_write = outgoing_length > 0U &&
            ((!write_wants_read &&
              (waits[1].revents & (POLLOUT | POLLERR | POLLHUP)) != 0) ||
             (write_wants_read &&
              (waits[1].revents & (POLLIN | POLLERR | POLLHUP)) != 0));
        if (may_write) {
            int amount = outgoing_length > (size_t)INT_MAX ? INT_MAX :
                         (int)outgoing_length;
            int written = SSL_write(pump->ssl, outgoing + outgoing_offset,
                                    amount);
            if (written > 0) {
                outgoing_offset += (size_t)written;
                outgoing_length -= (size_t)written;
                write_wants_read = false;
                if (outgoing_length == 0U) outgoing_offset = 0U;
            } else {
                int ssl_error = SSL_get_error(pump->ssl, written);
                if (ssl_error == SSL_ERROR_WANT_READ) {
                    write_wants_read = true;
                } else if (ssl_error != SSL_ERROR_WANT_WRITE) {
                    outgoing_length = 0U;
                    tls_read_open = false;
                    local_read_open = false;
                    (void)shutdown(pump->local_descriptor, SHUT_RDWR);
                }
            }
        }

        bool may_read = tls_read_open && incoming_length == 0U &&
            (SSL_pending(pump->ssl) > 0 ||
             (!read_wants_write &&
              (waits[1].revents & (POLLIN | POLLERR | POLLHUP)) != 0) ||
             (read_wants_write &&
              (waits[1].revents & (POLLOUT | POLLERR | POLLHUP)) != 0));
        if (may_read) {
            int received = SSL_read(pump->ssl, incoming, (int)sizeof(incoming));
            if (received > 0) {
                incoming_offset = 0U;
                incoming_length = (size_t)received;
                read_wants_write = false;
            } else {
                int ssl_error = SSL_get_error(pump->ssl, received);
                if (ssl_error == SSL_ERROR_WANT_WRITE) {
                    read_wants_write = true;
                } else if (ssl_error != SSL_ERROR_WANT_READ) {
                    tls_read_open = false;
                    (void)shutdown(pump->local_descriptor, SHUT_WR);
                }
            }
        }

        if (!local_read_open && outgoing_length == 0U && !shutdown_started) {
            shutdown_started = true;
            (void)SSL_shutdown(pump->ssl);
        }
        if (!tls_read_open && incoming_length == 0U) {
            (void)shutdown(pump->local_descriptor, SHUT_WR);
            if (!local_read_open || (waits[0].revents & POLLHUP) != 0) break;
        }
        if ((waits[0].revents & POLLHUP) != 0 && outgoing_length == 0U &&
            incoming_length == 0U) break;
    }

    (void)SSL_shutdown(pump->ssl);
    SSL_free(pump->ssl);
    SSL_CTX_free(pump->context);
    ch_protocol_close(&pump->network_descriptor);
    ch_protocol_close(&pump->local_descriptor);
    free(pump);
    return NULL;
}

static char *ch_protocol_optional_string(const ch_config_table *table,
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

static ch_status ch_protocol_tls_configure(SSL_CTX *context, SSL *ssl,
                                           const ch_config_table *settings,
                                           const char *server_address,
                                           ch_error *error) {
    char *sni = ch_protocol_optional_string(settings, "sni");
    if (sni == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy Trojan SNI");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (sni[0] == '\0') {
        free(sni);
        char *service = NULL;
        if (ch_protocol_split_target(server_address, &sni, &service, error) != CH_OK) {
            free(service);
            return error == NULL ? CH_ERROR_INVALID_ARGUMENT : error->code;
        }
        free(service);
    }
    bool skip_verify = false;
    ch_error ignored;
    if (settings != NULL) {
        (void)ch_config_table_get_bool(settings, "skip_cert_verify",
                                       &skip_verify, &ignored);
    }
    if (skip_verify) {
        SSL_CTX_set_verify(context, SSL_VERIFY_NONE, NULL);
    } else {
        SSL_CTX_set_verify(context, SSL_VERIFY_PEER, NULL);
        if (SSL_CTX_set_default_verify_paths(context) != 1) {
            free(sni);
            ch_error_set(error, CH_ERROR_IO,
                         "trojan load default certificate authorities failed");
            return CH_ERROR_IO;
        }
        X509_VERIFY_PARAM *parameters = SSL_get0_param(ssl);
        uint8_t address[16];
        if (inet_pton(AF_INET, sni, address) == 1 ||
            inet_pton(AF_INET6, sni, address) == 1) {
            if (X509_VERIFY_PARAM_set1_ip_asc(parameters, sni) != 1) {
                free(sni);
                ch_error_set(error, CH_ERROR_IO,
                             "trojan configure certificate IP verification failed");
                return CH_ERROR_IO;
            }
        } else if (X509_VERIFY_PARAM_set1_host(parameters, sni, 0U) != 1) {
            free(sni);
            ch_error_set(error, CH_ERROR_IO,
                         "trojan configure certificate hostname verification failed");
            return CH_ERROR_IO;
        }
    }
    uint8_t parsed_ip[16];
    if (sni[0] != '\0' && inet_pton(AF_INET, sni, parsed_ip) != 1 &&
        inet_pton(AF_INET6, sni, parsed_ip) != 1 &&
        SSL_set_tlsext_host_name(ssl, sni) != 1) {
        free(sni);
        ch_error_set(error, CH_ERROR_IO, "trojan configure SNI failed");
        return CH_ERROR_IO;
    }
    free(sni);

    const ch_config_array *alpn = settings == NULL ? NULL :
                                  ch_config_table_get_array(settings, "alpn");
    size_t count = ch_config_array_count(alpn);
    if (count == 0U) return CH_OK;
    size_t wire_length = 0U;
    for (size_t index = 0U; index < count; ++index) {
        char *value = NULL;
        if (ch_config_array_get_string(alpn, index, &value, error) != CH_OK ||
            value == NULL || value[0] == '\0' || strlen(value) > 255U ||
            wire_length > 65535U - strlen(value) - 1U) {
            free(value);
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "trojan ALPN entries must contain 1 to 255 bytes");
            return CH_ERROR_INVALID_ARGUMENT;
        }
        wire_length += strlen(value) + 1U;
        free(value);
    }
    uint8_t *wire = malloc(wire_length);
    if (wire == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate Trojan ALPN");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    size_t offset = 0U;
    for (size_t index = 0U; index < count; ++index) {
        char *value = NULL;
        if (ch_config_array_get_string(alpn, index, &value, error) != CH_OK) {
            free(wire);
            free(value);
            return error == NULL ? CH_ERROR_INVALID_ARGUMENT : error->code;
        }
        size_t length = strlen(value);
        wire[offset++] = (uint8_t)length;
        memcpy(wire + offset, value, length);
        offset += length;
        free(value);
    }
    int configured = SSL_set_alpn_protos(ssl, wire, (unsigned int)wire_length);
    free(wire);
    if (configured != 0) {
        ch_error_set(error, CH_ERROR_IO, "trojan configure ALPN failed");
        return CH_ERROR_IO;
    }
    return CH_OK;
}

static ch_status ch_protocol_ssl_write_all(SSL *ssl, const uint8_t *bytes,
                                           size_t length, ch_error *error) {
    while (length > 0U) {
        int amount = length > (size_t)INT_MAX ? INT_MAX : (int)length;
        int written = SSL_write(ssl, bytes, amount);
        if (written <= 0) {
            unsigned long detail = ERR_get_error();
            ch_error_set(error, CH_ERROR_IO, "trojan write header failed: %s",
                         detail == 0UL ? "TLS I/O error" :
                         ERR_reason_error_string(detail));
            return CH_ERROR_IO;
        }
        bytes += (size_t)written;
        length -= (size_t)written;
    }
    return CH_OK;
}

static ch_status ch_protocol_trojan_dial(const ch_config_table *server,
                                         int underlying_descriptor,
                                         const char *target,
                                         int *out_descriptor,
                                         ch_error *error) {
    const ch_config_table *settings = ch_config_table_get_table(server, "settings");
    char *password = ch_protocol_optional_string(settings, "password");
    char *server_address = ch_protocol_optional_string(server, "address");
    if (password == NULL || server_address == NULL) {
        free(password);
        free(server_address);
        ch_protocol_close(&underlying_descriptor);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy Trojan server configuration");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (password[0] == '\0') {
        free(password);
        free(server_address);
        ch_protocol_close(&underlying_descriptor);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "trojan password is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (underlying_descriptor < 0) {
        if (server_address[0] == '\0') {
            free(password);
            free(server_address);
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "trojan server address is required");
            return CH_ERROR_INVALID_ARGUMENT;
        }
        ch_status status = ch_protocol_connect_tcp(server_address,
                                                   &underlying_descriptor,
                                                   error);
        if (status != CH_OK) {
            free(password);
            free(server_address);
            return status;
        }
    }
    (void)pthread_once(&ch_protocol_sigpipe_once, ch_protocol_ignore_sigpipe);
    ch_protocol_socket_timeout(underlying_descriptor,
                               CH_PROTOCOL_DIAL_TIMEOUT_MS);
    SSL_CTX *context = SSL_CTX_new(TLS_client_method());
    if (context == NULL || SSL_CTX_set_min_proto_version(
            context, TLS1_2_VERSION) != 1) {
        SSL_CTX_free(context);
        free(password);
        free(server_address);
        ch_protocol_close(&underlying_descriptor);
        ch_error_set(error, CH_ERROR_IO, "create Trojan TLS context failed");
        return CH_ERROR_IO;
    }
    SSL *ssl = SSL_new(context);
    if (ssl == NULL || SSL_set_fd(ssl, underlying_descriptor) != 1) {
        SSL_free(ssl);
        SSL_CTX_free(context);
        free(password);
        free(server_address);
        ch_protocol_close(&underlying_descriptor);
        ch_error_set(error, CH_ERROR_IO, "create Trojan TLS stream failed");
        return CH_ERROR_IO;
    }
    ch_status status = ch_protocol_tls_configure(context, ssl, settings,
                                                 server_address, error);
    free(server_address);
    if (status != CH_OK) {
        SSL_free(ssl);
        SSL_CTX_free(context);
        free(password);
        ch_protocol_close(&underlying_descriptor);
        return status;
    }
    if (SSL_connect(ssl) != 1) {
        unsigned long detail = ERR_get_error();
        ch_error_set(error, CH_ERROR_IO, "trojan TLS handshake failed: %s",
                     detail == 0UL ? "TLS I/O error" :
                     ERR_reason_error_string(detail));
        SSL_free(ssl);
        SSL_CTX_free(context);
        free(password);
        ch_protocol_close(&underlying_descriptor);
        return CH_ERROR_IO;
    }
    uint8_t *header = NULL;
    size_t header_length = 0U;
    status = ch_protocol_trojan_header(password, target, &header,
                                       &header_length, error);
    free(password);
    if (status == CH_OK) {
        status = ch_protocol_ssl_write_all(ssl, header, header_length, error);
    }
    free(header);
    if (status != CH_OK) {
        SSL_free(ssl);
        SSL_CTX_free(context);
        ch_protocol_close(&underlying_descriptor);
        return status;
    }
    ch_protocol_socket_timeout(underlying_descriptor, 0U);
    int pair[2] = {-1, -1};
    if (socketpair(AF_UNIX, SOCK_STREAM, 0, pair) != 0) {
        SSL_free(ssl);
        SSL_CTX_free(context);
        ch_protocol_close(&underlying_descriptor);
        ch_error_set(error, CH_ERROR_IO, "create Trojan relay stream: %s",
                     strerror(errno));
        return CH_ERROR_IO;
    }
    ch_tls_pump *pump = calloc(1U, sizeof(*pump));
    if (pump == NULL) {
        SSL_free(ssl);
        SSL_CTX_free(context);
        ch_protocol_close(&underlying_descriptor);
        ch_protocol_close(&pair[0]);
        ch_protocol_close(&pair[1]);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate Trojan relay stream");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    pump->context = context;
    pump->ssl = ssl;
    pump->network_descriptor = underlying_descriptor;
    pump->local_descriptor = pair[1];
    pthread_attr_t attributes;
    int initialized = pthread_attr_init(&attributes) == 0;
    if (initialized) {
        (void)pthread_attr_setdetachstate(&attributes, PTHREAD_CREATE_DETACHED);
    }
    pthread_t thread;
    int started = initialized &&
        pthread_create(&thread, &attributes, ch_protocol_tls_pump_main, pump) == 0;
    if (initialized) (void)pthread_attr_destroy(&attributes);
    if (!started) {
        free(pump);
        SSL_free(ssl);
        SSL_CTX_free(context);
        ch_protocol_close(&underlying_descriptor);
        ch_protocol_close(&pair[0]);
        ch_protocol_close(&pair[1]);
        ch_error_set(error, CH_ERROR_IO, "start Trojan relay stream failed");
        return CH_ERROR_IO;
    }
    *out_descriptor = pair[0];
    return CH_OK;
}

ch_status ch_protocol_chain_dial(const ch_config_table *chain,
                                 const char *network, const char *target,
                                 int *out_descriptor, ch_error *error) {
    ch_error_clear(error);
    if (chain == NULL || network == NULL || target == NULL ||
        out_descriptor == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "chain, network, target, and output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_descriptor = -1;
    if (strcasecmp(network, "tcp") != 0) {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "native encrypted chains currently support TCP only");
        return CH_ERROR_UNSUPPORTED;
    }
    const ch_config_array *servers = ch_config_table_get_array(chain, "server");
    size_t count = ch_config_array_count(servers);
    if (count == 0U) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "native chain requires at least one server");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    int descriptor = -1;
    for (size_t index = 0U; index < count; ++index) {
        const ch_config_table *server = ch_config_array_get_table(servers, index);
        char *protocol = ch_protocol_optional_string(server, "protocol");
        if (protocol == NULL) {
            ch_protocol_close(&descriptor);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy chain protocol");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        const char *hop_target = target;
        char *next_address = NULL;
        if (index + 1U < count) {
            const ch_config_table *next = ch_config_array_get_table(servers,
                                                                    index + 1U);
            next_address = ch_protocol_optional_string(next, "address");
            if (next_address == NULL || next_address[0] == '\0') {
                free(next_address);
                free(protocol);
                ch_protocol_close(&descriptor);
                ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                             "chained server address is required");
                return CH_ERROR_INVALID_ARGUMENT;
            }
            hop_target = next_address;
        }
        ch_status status;
        if (strcasecmp(protocol, "direct") == 0 && count == 1U) {
            status = ch_protocol_connect_tcp(target, &descriptor, error);
        } else if (strcasecmp(protocol, "trojan") == 0 ||
                   strcasecmp(protocol, "clambback") == 0) {
            int tunneled = -1;
            status = ch_protocol_trojan_dial(server, descriptor, hop_target,
                                             &tunneled, error);
            descriptor = status == CH_OK ? tunneled : -1;
        } else if (strcasecmp(protocol, "shadowsocks") == 0) {
            int tunneled = -1;
            status = ch_protocol_shadowsocks_dial(
                server, descriptor, hop_target, &tunneled, error);
            descriptor = status == CH_OK ? tunneled : -1;
        } else if (strcasecmp(protocol, "tor") == 0) {
            int tunneled = -1;
            status = ch_protocol_tor_dial(server, descriptor, hop_target,
                                          &tunneled, error);
            descriptor = status == CH_OK ? tunneled : -1;
        } else {
            ch_protocol_close(&descriptor);
            ch_error_set(error, CH_ERROR_UNSUPPORTED,
                         "native protocol %s is not ported yet", protocol);
            status = CH_ERROR_UNSUPPORTED;
        }
        free(next_address);
        free(protocol);
        if (status != CH_OK) {
            ch_protocol_close(&descriptor);
            return status;
        }
    }
    *out_descriptor = descriptor;
    return CH_OK;
}
