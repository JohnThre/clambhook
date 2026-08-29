// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "test.h"

#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <openssl/evp.h>
#include <openssl/opensslv.h>
#include <openssl/rsa.h>
#include <openssl/ssl.h>
#include <openssl/x509.h>
#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

#include "clambhook/config.h"
#include "clambhook/dns.h"
#include "clambhook/protocol.h"
#include "internal.h"

#if OPENSSL_VERSION_NUMBER >= 0x30500000L && !defined(OPENSSL_NO_QUIC)
#include <openssl/quic.h>
#define DNS_TEST_HAVE_QUIC_SERVER 1
#else
#define DNS_TEST_HAVE_QUIC_SERVER 0
#endif

#if OPENSSL_VERSION_NUMBER >= 0x30200000L && !defined(OPENSSL_NO_QUIC)
#define DNS_TEST_HAVE_QUIC_CLIENT 1
#else
#define DNS_TEST_HAVE_QUIC_CLIENT 0
#endif

static const uint8_t dns_test_query[] = {
    0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00,
    0x00, 0x00, 0x00, 0x00, 0x07, 'e', 'x', 'a',
    'm', 'p', 'l', 'e', 0x03, 'c', 'o', 'm', 0x00,
    0x00, 0x01, 0x00, 0x01
};

typedef enum dns_test_server_kind {
    DNS_TEST_DOH = 1,
    DNS_TEST_DOT = 2,
    DNS_TEST_DOQ = 3
} dns_test_server_kind;

typedef struct dns_test_server {
    int descriptor;
    uint16_t port;
    pthread_t thread;
    SSL_CTX *tls;
    dns_test_server_kind kind;
    int success;
} dns_test_server;

typedef struct dns_test_dialer {
    ch_dns_route_action action;
    int route_calls;
    int dial_calls;
    int packet_dial_calls;
    int fail_dial;
} dns_test_dialer;

static int dns_test_ssl_read_exact(SSL *ssl, uint8_t *bytes, size_t length) {
    while (length > 0U) {
        int amount = length > 16384U ? 16384 : (int)length;
        int received = SSL_read(ssl, bytes, amount);
        if (received <= 0) return 0;
        bytes += (size_t)received;
        length -= (size_t)received;
    }
    return 1;
}

static int dns_test_ssl_write_all(SSL *ssl, const uint8_t *bytes,
                                  size_t length) {
    while (length > 0U) {
        int amount = length > 16384U ? 16384 : (int)length;
        int written = SSL_write(ssl, bytes, amount);
        if (written <= 0) return 0;
        bytes += (size_t)written;
        length -= (size_t)written;
    }
    return 1;
}

static int dns_test_select_doq_alpn(SSL *ssl, const unsigned char **out,
                                    unsigned char *out_length,
                                    const unsigned char *input,
                                    unsigned int input_length, void *context) {
    (void)ssl;
    (void)context;
    static const unsigned char doq[] = {3U, 'd', 'o', 'q'};
    return SSL_select_next_proto((unsigned char **)out, out_length,
                                 doq, sizeof(doq), input, input_length) ==
        OPENSSL_NPN_NEGOTIATED ? SSL_TLSEXT_ERR_OK :
                                SSL_TLSEXT_ERR_ALERT_FATAL;
}

static SSL_CTX *dns_test_tls_context(dns_test_server_kind kind) {
    EVP_PKEY_CTX *key_context = EVP_PKEY_CTX_new_id(EVP_PKEY_RSA, NULL);
    EVP_PKEY *key = NULL;
    X509 *certificate = NULL;
    SSL_CTX *context = NULL;
    if (key_context == NULL || EVP_PKEY_keygen_init(key_context) <= 0 ||
        EVP_PKEY_CTX_set_rsa_keygen_bits(key_context, 2048) <= 0 ||
        EVP_PKEY_keygen(key_context, &key) <= 0) goto cleanup;
    certificate = X509_new();
    if (certificate == NULL || X509_set_version(certificate, 2L) != 1 ||
        ASN1_INTEGER_set(X509_get_serialNumber(certificate), 1L) != 1 ||
        X509_gmtime_adj(X509_get_notBefore(certificate), -60L) == NULL ||
        X509_gmtime_adj(X509_get_notAfter(certificate), 3600L) == NULL ||
        X509_set_pubkey(certificate, key) != 1) goto cleanup;
    X509_NAME *name = X509_get_subject_name(certificate);
    if (name == NULL || X509_NAME_add_entry_by_txt(
            name, "CN", MBSTRING_ASC,
            (const unsigned char *)"localhost", -1, -1, 0) != 1 ||
        X509_set_issuer_name(certificate, name) != 1 ||
        X509_sign(certificate, key, EVP_sha256()) <= 0) goto cleanup;
    if (kind == DNS_TEST_DOQ) {
#if DNS_TEST_HAVE_QUIC_SERVER
        context = SSL_CTX_new(OSSL_QUIC_server_method());
#endif
    } else {
        context = SSL_CTX_new(TLS_server_method());
    }
    if (context == NULL ||
        (kind != DNS_TEST_DOQ &&
         SSL_CTX_set_min_proto_version(context, TLS1_2_VERSION) != 1) ||
        SSL_CTX_use_certificate(context, certificate) != 1 ||
        SSL_CTX_use_PrivateKey(context, key) != 1 ||
        SSL_CTX_check_private_key(context) != 1) {
        SSL_CTX_free(context);
        context = NULL;
    } else if (kind == DNS_TEST_DOQ) {
        SSL_CTX_set_alpn_select_cb(context, dns_test_select_doq_alpn, NULL);
    }

cleanup:
    X509_free(certificate);
    EVP_PKEY_free(key);
    EVP_PKEY_CTX_free(key_context);
    return context;
}

static size_t dns_test_headers_end(const uint8_t *bytes, size_t length) {
    for (size_t index = 3U; index < length; ++index) {
        if (bytes[index - 3U] == '\r' && bytes[index - 2U] == '\n' &&
            bytes[index - 1U] == '\r' && bytes[index] == '\n') {
            return index + 1U;
        }
    }
    return 0U;
}

static int dns_test_handle_dot(SSL *ssl) {
    uint8_t length_bytes[2];
    uint8_t query[CH_DNS_MAX_MESSAGE];
    if (!dns_test_ssl_read_exact(ssl, length_bytes, sizeof(length_bytes))) {
        return 0;
    }
    size_t length = (size_t)length_bytes[0] * 256U + length_bytes[1];
    if (length != sizeof(dns_test_query) ||
        !dns_test_ssl_read_exact(ssl, query, length) ||
        memcmp(query, dns_test_query, length) != 0) {
        return 0;
    }
    uint8_t response[sizeof(dns_test_query)];
    memcpy(response, dns_test_query, sizeof(response));
    response[2] |= 0x80U;
    return dns_test_ssl_write_all(ssl, length_bytes, sizeof(length_bytes)) &&
        dns_test_ssl_write_all(ssl, response, sizeof(response));
}

#if DNS_TEST_HAVE_QUIC_SERVER
static int dns_test_handle_doq(SSL *ssl) {
    uint8_t length_bytes[2];
    uint8_t query[CH_DNS_MAX_MESSAGE];
    if (!dns_test_ssl_read_exact(ssl, length_bytes, sizeof(length_bytes))) {
        return 0;
    }
    size_t length = (size_t)length_bytes[0] * 256U + length_bytes[1];
    if (length != sizeof(dns_test_query) ||
        !dns_test_ssl_read_exact(ssl, query, length) ||
        query[0] != 0U || query[1] != 0U ||
        memcmp(query + 2U, dns_test_query + 2U, length - 2U) != 0) {
        return 0;
    }
    uint8_t response[sizeof(dns_test_query)];
    memcpy(response, query, sizeof(response));
    response[2] |= 0x80U;
    return dns_test_ssl_write_all(ssl, length_bytes, sizeof(length_bytes)) &&
        dns_test_ssl_write_all(ssl, response, sizeof(response)) &&
        SSL_stream_conclude(ssl, 0U) == 1;
}
#endif

static int dns_test_handle_doh(SSL *ssl) {
    uint8_t request[8192];
    size_t length = 0U;
    size_t headers_end = 0U;
    while (length < sizeof(request) && headers_end == 0U) {
        int received = SSL_read(ssl, request + length,
                                (int)(sizeof(request) - length));
        if (received <= 0) return 0;
        length += (size_t)received;
        headers_end = dns_test_headers_end(request, length);
    }
    if (headers_end == 0U) return 0;
    char *headers = malloc(headers_end + 1U);
    if (headers == NULL) return 0;
    memcpy(headers, request, headers_end);
    headers[headers_end] = '\0';
    const char *content_length = strstr(headers, "Content-Length: ");
    int valid_headers = strncmp(headers, "POST /dns-query HTTP/", 21U) == 0 &&
        strstr(headers, "Content-Type: application/dns-message") != NULL &&
        strstr(headers, "Accept: application/dns-message") != NULL &&
        content_length != NULL;
    unsigned long declared = content_length == NULL ? 0UL :
        strtoul(content_length + strlen("Content-Length: "), NULL, 10);
    free(headers);
    if (!valid_headers || declared != sizeof(dns_test_query) ||
        headers_end > SIZE_MAX - sizeof(dns_test_query)) {
        return 0;
    }
    size_t required = headers_end + sizeof(dns_test_query);
    if (required > sizeof(request) ||
        (length < required &&
         !dns_test_ssl_read_exact(ssl, request + length,
                                  required - length)) ||
        memcmp(request + headers_end, dns_test_query,
               sizeof(dns_test_query)) != 0) {
        return 0;
    }
    uint8_t response[sizeof(dns_test_query)];
    memcpy(response, dns_test_query, sizeof(response));
    response[2] |= 0x80U;
    char response_headers[256];
    int header_length = snprintf(
        response_headers, sizeof(response_headers),
        "HTTP/1.1 200 OK\r\n"
        "Content-Type: application/dns-message\r\n"
        "Content-Length: %zu\r\n"
        "Connection: close\r\n\r\n", sizeof(response));
    return header_length > 0 && (size_t)header_length < sizeof(response_headers) &&
        dns_test_ssl_write_all(ssl, (const uint8_t *)response_headers,
                               (size_t)header_length) &&
        dns_test_ssl_write_all(ssl, response, sizeof(response));
}

static void *dns_test_server_main(void *opaque) {
    dns_test_server *server = opaque;
#if DNS_TEST_HAVE_QUIC_SERVER
    if (server->kind == DNS_TEST_DOQ) {
        SSL *listener = SSL_new_listener(server->tls, 0U);
        SSL *connection = NULL;
        if (listener != NULL && SSL_set_fd(listener, server->descriptor) == 1 &&
            SSL_listen(listener) == 1) {
            connection = SSL_accept_connection(listener, 0U);
        }
        if (connection != NULL) {
            server->success = dns_test_handle_doq(connection);
            (void)SSL_shutdown_ex(connection, SSL_SHUTDOWN_FLAG_RAPID,
                                  NULL, 0U);
            SSL_free(connection);
        }
        SSL_free(listener);
        return NULL;
    }
#endif
    int client;
    do {
        client = accept(server->descriptor, NULL, NULL);
    } while (client < 0 && errno == EINTR);
    if (client < 0) return NULL;
    SSL *ssl = SSL_new(server->tls);
    if (ssl != NULL && SSL_set_fd(ssl, client) == 1 &&
        SSL_accept(ssl) == 1) {
        server->success = server->kind == DNS_TEST_DOH ?
            dns_test_handle_doh(ssl) : dns_test_handle_dot(ssl);
    }
    if (ssl != NULL) {
        (void)SSL_shutdown(ssl);
        SSL_free(ssl);
    }
    (void)close(client);
    return NULL;
}

static int dns_test_server_start(dns_test_server *server,
                                 dns_test_server_kind kind) {
    memset(server, 0, sizeof(*server));
    server->descriptor = -1;
    server->kind = kind;
    server->tls = dns_test_tls_context(kind);
    if (server->tls == NULL) return 0;
    server->descriptor = socket(AF_INET,
        kind == DNS_TEST_DOQ ? SOCK_DGRAM : SOCK_STREAM,
        kind == DNS_TEST_DOQ ? IPPROTO_UDP : IPPROTO_TCP);
    if (server->descriptor < 0) goto failure;
    struct sockaddr_in address = {
        .sin_family = AF_INET,
        .sin_port = 0,
        .sin_addr = {.s_addr = htonl(INADDR_LOOPBACK)}
    };
    if (bind(server->descriptor, (struct sockaddr *)&address,
             (socklen_t)sizeof(address)) != 0 ||
        (kind != DNS_TEST_DOQ && listen(server->descriptor, 1) != 0)) {
        goto failure;
    }
    socklen_t address_length = (socklen_t)sizeof(address);
    if (getsockname(server->descriptor, (struct sockaddr *)&address,
                    &address_length) != 0) goto failure;
    server->port = ntohs(address.sin_port);
    if (pthread_create(&server->thread, NULL, dns_test_server_main,
                       server) != 0) goto failure;
    return 1;

failure:
    if (server->descriptor >= 0) (void)close(server->descriptor);
    SSL_CTX_free(server->tls);
    server->descriptor = -1;
    server->tls = NULL;
    return 0;
}

static void dns_test_server_stop(dns_test_server *server) {
    (void)shutdown(server->descriptor, SHUT_RDWR);
    (void)close(server->descriptor);
    (void)pthread_join(server->thread, NULL);
    SSL_CTX_free(server->tls);
    server->descriptor = -1;
    server->tls = NULL;
}

static ch_status dns_test_route(const char *network, const char *target,
                                ch_dns_route_action *out_action,
                                void *context, ch_error *error) {
    (void)network;
    (void)target;
    ch_error_clear(error);
    dns_test_dialer *dialer = context;
    ++dialer->route_calls;
    *out_action = dialer->action;
    return CH_OK;
}

static ch_status dns_test_stream_dial(
    const char *network, const char *target,
    const char *const *bootstrap_ips, size_t bootstrap_ip_count,
    int *out_descriptor, void *context, ch_error *error) {
    dns_test_dialer *dialer = context;
    ++dialer->dial_calls;
    *out_descriptor = -1;
    if (strcmp(network, "tcp") != 0 || dialer->fail_dial) {
        ch_error_set(error, CH_ERROR_IO, "injected dns dial failure");
        return CH_ERROR_IO;
    }
    const char *address = target;
    char *bootstrap_target = NULL;
    if (bootstrap_ip_count > 0U) {
        const char *separator = strrchr(target, ':');
        if (separator == NULL || separator[1] == '\0') {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "test dns target has no port");
            return CH_ERROR_INVALID_ARGUMENT;
        }
        bool ipv6 = strchr(bootstrap_ips[0], ':') != NULL;
        size_t capacity = strlen(bootstrap_ips[0]) + strlen(separator + 1) +
            (ipv6 ? 4U : 2U);
        bootstrap_target = malloc(capacity);
        if (bootstrap_target == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "allocate test bootstrap target");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        (void)snprintf(bootstrap_target, capacity,
                       ipv6 ? "[%s]:%s" : "%s:%s", bootstrap_ips[0],
                       separator + 1);
        address = bootstrap_target;
    }
    ch_status status = ch_protocol_connect_tcp(address, out_descriptor, error);
    free(bootstrap_target);
    return status;
}

static ch_status dns_test_packet_dial(
    const char *network, const char *target,
    const char *const *bootstrap_ips, size_t bootstrap_ip_count,
    ch_packet_connection **out_connection, char **out_send_target,
    void *context, ch_error *error) {
    dns_test_dialer *dialer = context;
    ++dialer->packet_dial_calls;
    *out_connection = NULL;
    *out_send_target = NULL;
    if (strcmp(network, "udp") != 0 || dialer->fail_dial) {
        ch_error_set(error, CH_ERROR_IO,
                     "injected dns packet dial failure");
        return CH_ERROR_IO;
    }
    const char *separator = strrchr(target, ':');
    if (separator == NULL || separator[1] == '\0') {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "test dns packet target has no port");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (bootstrap_ip_count == 0U) {
        *out_send_target = ch_strdup(target);
    } else {
        bool ipv6 = strchr(bootstrap_ips[0], ':') != NULL;
        size_t capacity = strlen(bootstrap_ips[0]) +
            strlen(separator + 1U) + (ipv6 ? 4U : 2U);
        *out_send_target = malloc(capacity);
        if (*out_send_target != NULL) {
            (void)snprintf(*out_send_target, capacity,
                           ipv6 ? "[%s]:%s" : "%s:%s",
                           bootstrap_ips[0], separator + 1U);
        }
    }
    if (*out_send_target == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate test DNS packet target");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_status status = ch_protocol_direct_packet_dial(out_connection, error);
    if (status != CH_OK) {
        free(*out_send_target);
        *out_send_target = NULL;
    }
    return status;
}

static ch_dns_proxy_options dns_test_options(dns_test_dialer *dialer) {
    ch_dns_proxy_options options = {
        .route = dns_test_route,
        .stream_dial = dns_test_stream_dial,
        .packet_dial = dns_test_packet_dial,
        .dial_context = dialer,
        .insecure_skip_verify = true
    };
    return options;
}

static void dns_test_wire_helpers(void) {
    ch_error error;
    CH_TEST_ASSERT(ch_dns_question_end(dns_test_query,
                                      sizeof(dns_test_query)) ==
                   sizeof(dns_test_query));
    uint8_t response[sizeof(dns_test_query)];
    memcpy(response, dns_test_query, sizeof(response));
    response[2] |= 0x80U;
    CH_TEST_ASSERT(ch_dns_validate_response(
        dns_test_query, sizeof(dns_test_query), response, sizeof(response),
        &error) == CH_OK);
    response[0] ^= 0xffU;
    CH_TEST_ASSERT(ch_dns_validate_response(
        dns_test_query, sizeof(dns_test_query), response, sizeof(response),
        &error) == CH_ERROR_PARSE);
    response[0] ^= 0xffU;
    response[13] = 'z';
    CH_TEST_ASSERT(ch_dns_validate_response(
        dns_test_query, sizeof(dns_test_query), response, sizeof(response),
        &error) == CH_ERROR_PARSE);
    uint8_t compressed[sizeof(dns_test_query)];
    memcpy(compressed, dns_test_query, sizeof(compressed));
    compressed[12] = 0xc0U;
    CH_TEST_ASSERT(ch_dns_question_end(compressed, sizeof(compressed)) == 0U);
    uint8_t *servfail = NULL;
    size_t servfail_length = 0U;
    CH_TEST_ASSERT(ch_dns_servfail(
        dns_test_query, sizeof(dns_test_query), &servfail, &servfail_length,
        &error) == CH_OK);
    CH_TEST_ASSERT(servfail_length == sizeof(dns_test_query));
    CH_TEST_ASSERT((servfail[2] & 0x80U) != 0U);
    CH_TEST_ASSERT((servfail[3] & 0x0fU) == 0x02U);
    CH_TEST_ASSERT(memcmp(servfail + 6U, "\0\0\0\0\0\0", 6U) == 0);
    free(servfail);
}

static void dns_test_configuration_contract(void) {
    const char *control_d_document =
        "active = \"default\"\n"
        "[[profile]]\n"
        "name = \"default\"\n"
        "[profile.dns]\n"
        "enabled = true\n"
        "[[profile.dns.upstream]]\n"
        "protocol = \"controld\"\n"
        "resolver = \"abc123\"\n";
    ch_config *config = NULL;
    ch_error error;
    dns_test_dialer dialer = {.action = CH_DNS_ROUTE_DIRECT};
    ch_dns_proxy_options options = dns_test_options(&dialer);
    CH_TEST_ASSERT(ch_config_parse(control_d_document, NULL, &config,
                                   &error) == CH_OK);
    ch_dns_proxy *proxy = ch_dns_proxy_create(config, NULL, &options, &error);
    CH_TEST_ASSERT(proxy != NULL);
    CH_TEST_ASSERT(ch_dns_proxy_upstream_count(proxy) == 1U);
    CH_TEST_ASSERT_STRING("controld:abc123",
                          ch_dns_proxy_upstream_name(proxy, 0U));
    ch_dns_proxy_destroy(proxy);
    ch_config_free(config);

    const char *bootstrap_guard_document =
        "[[profile]]\n"
        "name = \"default\"\n"
        "[profile.dns]\n"
        "enabled = true\n"
        "[[profile.dns.upstream]]\n"
        "protocol = \"dot\"\n"
        "address = \"dns.example:853\"\n";
    config = NULL;
    dialer.route_calls = 0;
    CH_TEST_ASSERT(ch_config_parse(bootstrap_guard_document, NULL, &config,
                                   &error) == CH_OK);
    proxy = ch_dns_proxy_create(config, NULL, &options, &error);
    CH_TEST_ASSERT(proxy == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "needs bootstrap_ips") != NULL);
    CH_TEST_ASSERT(dialer.route_calls == 1);
    ch_config_free(config);

    const char *doq_document =
        "[[profile]]\n"
        "name = \"default\"\n"
        "[profile.dns]\n"
        "enabled = true\n"
        "[[profile.dns.upstream]]\n"
        "protocol = \"doq\"\n"
        "address = \"127.0.0.1:853\"\n";
    config = NULL;
    CH_TEST_ASSERT(ch_config_parse(doq_document, NULL, &config, &error) ==
                   CH_OK);
    proxy = ch_dns_proxy_create(config, NULL, &options, &error);
#if DNS_TEST_HAVE_QUIC_CLIENT
    CH_TEST_ASSERT(proxy != NULL);
    CH_TEST_ASSERT_STRING("doq:127.0.0.1",
                          ch_dns_proxy_upstream_name(proxy, 0U));
    ch_dns_proxy_destroy(proxy);
#else
    CH_TEST_ASSERT(proxy == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_UNSUPPORTED);
#endif
    ch_config_free(config);

    const char *invalid_bootstrap_document =
        "[[profile]]\n"
        "name = \"default\"\n"
        "[profile.dns]\n"
        "enabled = true\n"
        "[[profile.dns.upstream]]\n"
        "protocol = \"doh\"\n"
        "url = \"https://dns.example/dns-query\"\n"
        "bootstrap_ips = [\"not-an-ip\"]\n";
    config = NULL;
    CH_TEST_ASSERT(ch_config_parse(invalid_bootstrap_document, NULL, &config,
                                   &error) == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(config == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "dns.bootstrap_ips") != NULL);
}

static void dns_test_failure_servfail(void) {
    const char *document =
        "[[profile]]\n"
        "name = \"default\"\n"
        "[profile.dns]\n"
        "enabled = true\n"
        "timeout = \"200ms\"\n"
        "[[profile.dns.upstream]]\n"
        "name = \"broken\"\n"
        "protocol = \"dot\"\n"
        "address = \"127.0.0.1:853\"\n"
        "server_name = \"localhost\"\n";
    ch_config *config = NULL;
    ch_error error;
    dns_test_dialer dialer = {
        .action = CH_DNS_ROUTE_DIRECT,
        .fail_dial = 1
    };
    ch_dns_proxy_options options = dns_test_options(&dialer);
    CH_TEST_ASSERT(ch_config_parse(document, NULL, &config, &error) == CH_OK);
    ch_dns_proxy *proxy = ch_dns_proxy_create(config, NULL, &options, &error);
    CH_TEST_ASSERT(proxy != NULL);
    uint8_t *response = NULL;
    size_t response_length = 0U;
    CH_TEST_ASSERT(ch_dns_proxy_exchange(
        proxy, dns_test_query, sizeof(dns_test_query), &response,
        &response_length, &error) == CH_ERROR_IO);
    CH_TEST_ASSERT(response != NULL);
    CH_TEST_ASSERT(response_length == sizeof(dns_test_query));
    CH_TEST_ASSERT((response[3] & 0x0fU) == 0x02U);
    CH_TEST_ASSERT(strstr(error.message, "broken") != NULL);
    free(response);
    ch_dns_proxy_destroy(proxy);
    ch_config_free(config);
}

static void dns_test_encrypted_exchange(dns_test_server_kind kind) {
    dns_test_server server;
    CH_TEST_ASSERT(dns_test_server_start(&server, kind));
    char document[1024];
    if (kind == DNS_TEST_DOH) {
        (void)snprintf(
            document, sizeof(document),
            "[[profile]]\n"
            "name = \"default\"\n"
            "[profile.dns]\n"
            "enabled = true\n"
            "timeout = \"2s\"\n"
            "[[profile.dns.upstream]]\n"
            "protocol = \"doh\"\n"
            "url = \"https://localhost:%u/dns-query\"\n"
            "bootstrap_ips = [\"127.0.0.1\"]\n",
            (unsigned int)server.port);
    } else if (kind == DNS_TEST_DOT) {
        (void)snprintf(
            document, sizeof(document),
            "[[profile]]\n"
            "name = \"default\"\n"
            "[profile.dns]\n"
            "enabled = true\n"
            "timeout = \"2s\"\n"
            "[[profile.dns.upstream]]\n"
            "protocol = \"dot\"\n"
            "address = \"127.0.0.1:%u\"\n"
            "server_name = \"localhost\"\n",
            (unsigned int)server.port);
    } else {
        (void)snprintf(
            document, sizeof(document),
            "[[profile]]\n"
            "name = \"default\"\n"
            "[profile.dns]\n"
            "enabled = true\n"
            "timeout = \"4s\"\n"
            "[[profile.dns.upstream]]\n"
            "protocol = \"doq\"\n"
            "address = \"127.0.0.1:%u\"\n"
            "server_name = \"localhost\"\n",
            (unsigned int)server.port);
    }
    ch_config *config = NULL;
    ch_error error;
    dns_test_dialer dialer = {.action = CH_DNS_ROUTE_DIRECT};
    ch_dns_proxy_options options = dns_test_options(&dialer);
    ch_status parse_status = ch_config_parse(document, NULL, &config, &error);
    ch_dns_proxy *proxy = parse_status == CH_OK ?
        ch_dns_proxy_create(config, NULL, &options, &error) : NULL;
    uint8_t *response = NULL;
    size_t response_length = 0U;
    ch_status exchange_status = proxy == NULL ? error.code :
        ch_dns_proxy_exchange(proxy, dns_test_query, sizeof(dns_test_query),
                              &response, &response_length, &error);
    ch_dns_proxy_destroy(proxy);
    ch_config_free(config);
    dns_test_server_stop(&server);
    CH_TEST_ASSERT(parse_status == CH_OK);
    CH_TEST_ASSERT(exchange_status == CH_OK);
    CH_TEST_ASSERT(server.success);
    CH_TEST_ASSERT(kind == DNS_TEST_DOQ ? dialer.packet_dial_calls == 1 :
                                         dialer.dial_calls == 1);
    CH_TEST_ASSERT(response_length == sizeof(dns_test_query));
    CH_TEST_ASSERT(response != NULL && (response[2] & 0x80U) != 0U);
    free(response);
}

void ch_test_dns(void) {
    dns_test_wire_helpers();
    dns_test_configuration_contract();
    dns_test_failure_servfail();
    dns_test_encrypted_exchange(DNS_TEST_DOT);
    dns_test_encrypted_exchange(DNS_TEST_DOH);
#if DNS_TEST_HAVE_QUIC_SERVER
    dns_test_encrypted_exchange(DNS_TEST_DOQ);
#endif
}
