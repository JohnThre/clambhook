// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "dns_quic.h"

#include <arpa/inet.h>
#include <limits.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include <openssl/err.h>
#include <openssl/opensslv.h>
#include <openssl/ssl.h>

#include "internal.h"

#if OPENSSL_VERSION_NUMBER >= 0x30200000L && !defined(OPENSSL_NO_QUIC)
#include <openssl/quic.h>
#define CH_DNS_HAVE_OPENSSL_QUIC 1
#else
#define CH_DNS_HAVE_OPENSSL_QUIC 0
#endif

#define CH_DNS_QUIC_MTU 1350U
#define CH_DNS_QUIC_BIO_BUFFER (256U * 1024U)

bool ch_dns_quic_available(void) {
    return CH_DNS_HAVE_OPENSSL_QUIC != 0;
}

#if CH_DNS_HAVE_OPENSSL_QUIC
typedef struct ch_dns_quic_transport {
    const ch_dns_proxy_options *options;
    ch_packet_connection *packet;
    char *send_target;
    BIO *network_bio;
    SSL_CTX *ssl_context;
    SSL *connection;
    uint64_t deadline_milliseconds;
} ch_dns_quic_transport;

static uint64_t ch_dns_quic_now_milliseconds(void) {
    struct timespec now = {0};
    if (clock_gettime(CLOCK_MONOTONIC, &now) != 0) return 0U;
    uint64_t seconds = now.tv_sec < 0 ? 0U : (uint64_t)now.tv_sec;
    return seconds * UINT64_C(1000) + (uint64_t)now.tv_nsec / UINT64_C(1000000);
}

static int ch_dns_quic_remaining(const ch_dns_quic_transport *transport) {
    uint64_t now = ch_dns_quic_now_milliseconds();
    if (now >= transport->deadline_milliseconds) return 0;
    uint64_t remaining = transport->deadline_milliseconds - now;
    return remaining > (uint64_t)INT_MAX ? INT_MAX : (int)remaining;
}

static void ch_dns_quic_ssl_error(ch_error *error, const char *operation) {
    unsigned long code = ERR_peek_last_error();
    char detail[192];
    if (code == 0UL) {
        (void)snprintf(detail, sizeof(detail), "connection closed");
    } else {
        ERR_error_string_n(code, detail, sizeof(detail));
    }
    ch_error_set(error, CH_ERROR_IO, "dns DoQ %s: %s", operation, detail);
}

static ch_status ch_dns_quic_drain(ch_dns_quic_transport *transport,
                                   ch_error *error) {
    uint8_t datagram[65536];
    for (;;) {
        size_t pending = BIO_ctrl_pending(transport->network_bio);
        if (pending == 0U) return CH_OK;
        if (pending > sizeof(datagram)) {
            ch_error_set(error, CH_ERROR_IO,
                         "dns DoQ generated an oversized QUIC datagram");
            return CH_ERROR_IO;
        }
        size_t length = 0U;
        if (BIO_read_ex(transport->network_bio, datagram, sizeof(datagram),
                        &length) != 1 || length == 0U) {
            ch_dns_quic_ssl_error(error, "read outbound datagram");
            return CH_ERROR_IO;
        }
        ch_status status = ch_packet_connection_send(
            transport->packet, transport->send_target, datagram, length,
            error);
        if (status != CH_OK) return status;
    }
}

static int ch_dns_quic_event_wait(ch_dns_quic_transport *transport) {
    int remaining = ch_dns_quic_remaining(transport);
    if (remaining <= 0) return 0;
    struct timeval timeout = {0};
    int infinite = 0;
    if (SSL_get_event_timeout(transport->connection, &timeout, &infinite) != 1 ||
        infinite != 0) {
        return remaining;
    }
    uint64_t milliseconds = (uint64_t)timeout.tv_sec * UINT64_C(1000) +
        ((uint64_t)timeout.tv_usec + UINT64_C(999)) / UINT64_C(1000);
    if (milliseconds > (uint64_t)remaining) return remaining;
    return milliseconds > (uint64_t)INT_MAX ? INT_MAX : (int)milliseconds;
}

static ch_status ch_dns_quic_pump(ch_dns_quic_transport *transport,
                                  bool wait_for_network, ch_error *error) {
    if (SSL_handle_events(transport->connection) != 1) {
        ch_dns_quic_ssl_error(error, "handle events");
        return CH_ERROR_IO;
    }
    ch_status status = ch_dns_quic_drain(transport, error);
    if (status != CH_OK) return status;
    int wait_milliseconds = wait_for_network ?
        ch_dns_quic_event_wait(transport) : 0;
    if (wait_for_network && wait_milliseconds == 0 &&
        ch_dns_quic_remaining(transport) == 0) {
        ch_error_set(error, CH_ERROR_IO, "dns DoQ exchange timed out");
        return CH_ERROR_IO;
    }
    uint8_t datagram[65536];
    size_t length = 0U;
    char *source = NULL;
    ch_error receive_error;
    status = ch_packet_connection_receive_timeout(
        transport->packet, datagram, sizeof(datagram), &length, &source,
        wait_milliseconds, &receive_error);
    free(source);
    if (status == CH_OK) {
        size_t written = 0U;
        if (length == 0U ||
            BIO_write_ex(transport->network_bio, datagram, length,
                         &written) != 1 || written != length) {
            ch_dns_quic_ssl_error(error, "inject inbound datagram");
            return CH_ERROR_IO;
        }
    } else if (status != CH_ERROR_NOT_FOUND) {
        if (error != NULL) *error = receive_error;
        return status;
    } else {
        ERR_clear_error();
    }
    if (SSL_handle_events(transport->connection) != 1) {
        ch_dns_quic_ssl_error(error, "process inbound datagram");
        return CH_ERROR_IO;
    }
    return ch_dns_quic_drain(transport, error);
}

static bool ch_dns_quic_retryable(SSL *ssl, int result,
                                  bool *out_wait_for_network) {
    int ssl_error = SSL_get_error(ssl, result);
    *out_wait_for_network = ssl_error == SSL_ERROR_WANT_READ;
    return ssl_error == SSL_ERROR_WANT_READ ||
        ssl_error == SSL_ERROR_WANT_WRITE;
}

static ch_status ch_dns_quic_connect(ch_dns_quic_transport *transport,
                                     ch_error *error) {
    for (;;) {
        ERR_clear_error();
        int result = SSL_connect(transport->connection);
        if (result == 1) return CH_OK;
        bool wait_for_network = false;
        if (!ch_dns_quic_retryable(transport->connection, result,
                                   &wait_for_network)) {
            ch_dns_quic_ssl_error(error, "handshake");
            return CH_ERROR_IO;
        }
        ch_status status = ch_dns_quic_pump(
            transport, wait_for_network, error);
        if (status != CH_OK) return status;
    }
}

static ch_status ch_dns_quic_write(ch_dns_quic_transport *transport,
                                   SSL *stream, const uint8_t *bytes,
                                   size_t length, ch_error *error) {
    for (;;) {
        size_t written = 0U;
        ERR_clear_error();
        int result = SSL_write_ex(stream, bytes, length, &written);
        if (result == 1 && written == length) break;
        bool wait_for_network = false;
        if (!ch_dns_quic_retryable(stream, result, &wait_for_network)) {
            ch_dns_quic_ssl_error(error, "write query");
            return CH_ERROR_IO;
        }
        ch_status status = ch_dns_quic_pump(
            transport, wait_for_network, error);
        if (status != CH_OK) return status;
    }
    for (;;) {
        ERR_clear_error();
        int result = SSL_stream_conclude(stream, 0U);
        if (result == 1) return CH_OK;
        bool wait_for_network = false;
        if (!ch_dns_quic_retryable(stream, result, &wait_for_network)) {
            ch_dns_quic_ssl_error(error, "conclude query stream");
            return CH_ERROR_IO;
        }
        ch_status status = ch_dns_quic_pump(
            transport, wait_for_network, error);
        if (status != CH_OK) return status;
    }
}

static ch_status ch_dns_quic_read_exact(ch_dns_quic_transport *transport,
                                        SSL *stream, uint8_t *bytes,
                                        size_t length, ch_error *error) {
    size_t offset = 0U;
    while (offset < length) {
        size_t received = 0U;
        ERR_clear_error();
        int result = SSL_read_ex(stream, bytes + offset, length - offset,
                                 &received);
        if (result == 1 && received > 0U) {
            offset += received;
            continue;
        }
        bool wait_for_network = false;
        if (!ch_dns_quic_retryable(stream, result, &wait_for_network)) {
            ch_dns_quic_ssl_error(error, "read response");
            return CH_ERROR_IO;
        }
        ch_status status = ch_dns_quic_pump(
            transport, wait_for_network, error);
        if (status != CH_OK) return status;
    }
    return CH_OK;
}

static ch_status ch_dns_quic_expect_eof(ch_dns_quic_transport *transport,
                                        SSL *stream, ch_error *error) {
    uint8_t trailing = 0U;
    for (;;) {
        size_t received = 0U;
        ERR_clear_error();
        int result = SSL_read_ex(stream, &trailing, 1U, &received);
        if (result == 1 && received > 0U) {
            ch_error_set(error, CH_ERROR_PARSE,
                         "dns DoQ response has trailing bytes");
            return CH_ERROR_PARSE;
        }
        int ssl_error = SSL_get_error(stream, result);
        if (ssl_error == SSL_ERROR_ZERO_RETURN) return CH_OK;
        bool wait_for_network = ssl_error == SSL_ERROR_WANT_READ;
        if (ssl_error != SSL_ERROR_WANT_READ &&
            ssl_error != SSL_ERROR_WANT_WRITE) {
            ch_dns_quic_ssl_error(error, "finish response stream");
            return CH_ERROR_IO;
        }
        ch_status status = ch_dns_quic_pump(
            transport, wait_for_network, error);
        if (status != CH_OK) return status;
    }
}

static void ch_dns_quic_transport_clear(ch_dns_quic_transport *transport) {
    if (transport->connection != NULL) {
        (void)SSL_shutdown_ex(transport->connection,
                              SSL_SHUTDOWN_FLAG_RAPID,
                              NULL, 0U);
        SSL_free(transport->connection);
    }
    BIO_free(transport->network_bio);
    SSL_CTX_free(transport->ssl_context);
    ch_packet_connection_close(transport->packet);
    free(transport->send_target);
    memset(transport, 0, sizeof(*transport));
}

static ch_status ch_dns_quic_transport_create(
    ch_dns_quic_transport *transport,
    const ch_dns_proxy_options *options,
    const char *target,
    const char *server_name,
    const char *const *bootstrap_ips,
    size_t bootstrap_ip_count,
    unsigned int timeout_milliseconds,
    ch_error *error) {
    memset(transport, 0, sizeof(*transport));
    transport->options = options;
    uint64_t now = ch_dns_quic_now_milliseconds();
    transport->deadline_milliseconds = now + timeout_milliseconds;
    ch_status status = options->packet_dial(
        "udp", target, bootstrap_ips, bootstrap_ip_count,
        &transport->packet, &transport->send_target,
        options->dial_context, error);
    if (status != CH_OK) return status;
    BIO *ssl_bio = NULL;
    if (BIO_new_bio_dgram_pair(&ssl_bio, CH_DNS_QUIC_BIO_BUFFER,
                               &transport->network_bio,
                               CH_DNS_QUIC_BIO_BUFFER) != 1 ||
        BIO_dgram_set_no_trunc(ssl_bio, 1) != 1 ||
        BIO_dgram_set_no_trunc(transport->network_bio, 1) != 1 ||
        BIO_dgram_set_mtu(ssl_bio, CH_DNS_QUIC_MTU) != 1 ||
        BIO_dgram_set_caps(transport->network_bio,
                           BIO_DGRAM_CAP_HANDLES_DST_ADDR |
                           BIO_DGRAM_CAP_PROVIDES_SRC_ADDR) != 1) {
        BIO_free(ssl_bio);
        ch_dns_quic_ssl_error(error, "create datagram bridge");
        return CH_ERROR_IO;
    }
    BIO_ADDR *peer = BIO_ADDR_new();
    BIO_ADDR *local = BIO_ADDR_new();
    struct in_addr dummy_address;
    if (peer == NULL || local == NULL ||
        inet_pton(AF_INET, "192.0.2.1", &dummy_address) != 1 ||
        BIO_ADDR_rawmake(peer, AF_INET, &dummy_address,
                         sizeof(dummy_address), 853U) != 1 ||
        BIO_ADDR_copy(local, peer) != 1 ||
        BIO_dgram_set0_local_addr(transport->network_bio, local) != 1) {
        BIO_ADDR_free(peer);
        BIO_ADDR_free(local);
        BIO_free(ssl_bio);
        ch_dns_quic_ssl_error(error, "configure datagram bridge address");
        return CH_ERROR_IO;
    }
    local = NULL; /* Ownership transferred to the network BIO. */
    transport->ssl_context = SSL_CTX_new(OSSL_QUIC_client_method());
    if (transport->ssl_context == NULL) {
        BIO_ADDR_free(peer);
        BIO_free(ssl_bio);
        ch_dns_quic_ssl_error(error, "create context");
        return CH_ERROR_IO;
    }
    if (options->insecure_skip_verify) {
        SSL_CTX_set_verify(transport->ssl_context, SSL_VERIFY_NONE, NULL);
    } else {
        SSL_CTX_set_verify(transport->ssl_context, SSL_VERIFY_PEER, NULL);
#if defined(__ANDROID__)
        int trust_loaded = SSL_CTX_load_verify_locations(
            transport->ssl_context, NULL, "/system/etc/security/cacerts");
        if (trust_loaded != 1) ERR_clear_error();
#else
        int trust_loaded = 0;
#endif
        if (trust_loaded != 1 &&
            SSL_CTX_set_default_verify_paths(transport->ssl_context) != 1) {
            BIO_ADDR_free(peer);
            BIO_free(ssl_bio);
            ch_dns_quic_ssl_error(error, "load trust store");
            return CH_ERROR_IO;
        }
    }
    transport->connection = SSL_new(transport->ssl_context);
    static const unsigned char alpn[] = {3U, 'd', 'o', 'q'};
    if (transport->connection == NULL ||
        SSL_set_blocking_mode(transport->connection, 0) != 1 ||
        SSL_set_alpn_protos(transport->connection, alpn,
                            sizeof(alpn)) != 0 ||
        SSL_set1_initial_peer_addr(transport->connection, peer) != 1 ||
        (server_name[0] != '\0' &&
         (SSL_set_tlsext_host_name(transport->connection, server_name) != 1 ||
          (!options->insecure_skip_verify &&
           SSL_set1_host(transport->connection, server_name) != 1)))) {
        BIO_ADDR_free(peer);
        BIO_free(ssl_bio);
        ch_dns_quic_ssl_error(error, "configure connection");
        return CH_ERROR_IO;
    }
    BIO_ADDR_free(peer);
    SSL_set_bio(transport->connection, ssl_bio, ssl_bio);
    return ch_dns_quic_connect(transport, error);
}
#endif

ch_status ch_dns_quic_exchange(
    const ch_dns_proxy_options *options,
    const char *target,
    const char *server_name,
    const char *const *bootstrap_ips,
    size_t bootstrap_ip_count,
    unsigned int timeout_milliseconds,
    const uint8_t *query,
    size_t query_length,
    uint8_t **out_response,
    size_t *out_response_length,
    ch_error *error) {
#if !CH_DNS_HAVE_OPENSSL_QUIC
    (void)options;
    (void)target;
    (void)server_name;
    (void)bootstrap_ips;
    (void)bootstrap_ip_count;
    (void)timeout_milliseconds;
    (void)query;
    (void)query_length;
    (void)out_response;
    (void)out_response_length;
    ch_error_set(error, CH_ERROR_UNSUPPORTED,
                 "native DNS-over-QUIC requires OpenSSL 3.2 or later");
    return CH_ERROR_UNSUPPORTED;
#else
    if (options == NULL || options->packet_dial == NULL || target == NULL ||
        server_name == NULL || query == NULL || query_length < 12U ||
        query_length > UINT16_MAX || out_response == NULL ||
        out_response_length == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "invalid DNS-over-QUIC exchange");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_response = NULL;
    *out_response_length = 0U;
    uint8_t *request = malloc(query_length + 2U);
    if (request == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate DNS-over-QUIC request");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    request[0] = (uint8_t)(query_length >> 8U);
    request[1] = (uint8_t)query_length;
    memcpy(request + 2U, query, query_length);
    request[2] = 0U;
    request[3] = 0U;
    ch_dns_quic_transport transport;
    ch_status status = ch_dns_quic_transport_create(
        &transport, options, target, server_name, bootstrap_ips,
        bootstrap_ip_count, timeout_milliseconds, error);
    if (status != CH_OK) goto cleanup;
    status = ch_dns_quic_write(&transport, transport.connection, request,
                               query_length + 2U, error);
    uint8_t length_bytes[2] = {0};
    if (status == CH_OK) {
        status = ch_dns_quic_read_exact(&transport, transport.connection,
                                        length_bytes,
                                        sizeof(length_bytes), error);
    }
    size_t response_length = (size_t)length_bytes[0] * 256U +
        (size_t)length_bytes[1];
    if (status == CH_OK && response_length < 12U) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "dns DoQ response is too short");
        status = CH_ERROR_PARSE;
    }
    uint8_t *response = status == CH_OK ? malloc(response_length) : NULL;
    if (status == CH_OK && response == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate DNS-over-QUIC response");
        status = CH_ERROR_OUT_OF_MEMORY;
    }
    if (status == CH_OK) {
        status = ch_dns_quic_read_exact(&transport, transport.connection,
                                        response,
                                        response_length, error);
    }
    if (status == CH_OK) {
        status = ch_dns_quic_expect_eof(&transport, transport.connection,
                                        error);
    }
    if (status == CH_OK) {
        status = ch_dns_validate_response(request + 2U, query_length,
                                          response, response_length, error);
    }
    if (status == CH_OK) {
        response[0] = query[0];
        response[1] = query[1];
        *out_response = response;
        *out_response_length = response_length;
        response = NULL;
    }
    free(response);

cleanup:
    if (transport.packet != NULL || transport.connection != NULL ||
        transport.network_bio != NULL || transport.ssl_context != NULL ||
        transport.send_target != NULL) {
        ch_dns_quic_transport_clear(&transport);
    }
    free(request);
    return status;
#endif
}
