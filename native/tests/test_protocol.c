#include "test.h"

#include <arpa/inet.h>
#include <errno.h>
#include <openssl/evp.h>
#include <openssl/rsa.h>
#include <openssl/ssl.h>
#include <openssl/x509.h>
#include <pthread.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

#include "clambhook/config.h"
#include "clambhook/protocol.h"

typedef struct protocol_tls_server {
    int descriptor;
    uint16_t port;
    pthread_t thread;
    SSL_CTX *context;
    uint8_t *expected_header;
    size_t expected_header_length;
    uint16_t relay_port;
    int success;
} protocol_tls_server;

typedef struct protocol_tls_relay {
    SSL *ssl;
    int descriptor;
} protocol_tls_relay;

static int protocol_test_send_all(int descriptor, const void *bytes,
                                  size_t length) {
    const uint8_t *cursor = bytes;
    while (length > 0U) {
        ssize_t written = send(descriptor, cursor, length, 0);
        if (written < 0 && errno == EINTR) continue;
        if (written <= 0) return 0;
        cursor += (size_t)written;
        length -= (size_t)written;
    }
    return 1;
}

static int protocol_test_receive_exact(int descriptor, void *bytes,
                                       size_t length) {
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

static int protocol_test_ssl_read_exact(SSL *ssl, void *bytes, size_t length) {
    uint8_t *cursor = bytes;
    while (length > 0U) {
        int amount = length > 16384U ? 16384 : (int)length;
        int received = SSL_read(ssl, cursor, amount);
        if (received <= 0) return 0;
        cursor += (size_t)received;
        length -= (size_t)received;
    }
    return 1;
}

static int protocol_test_ssl_write_all(SSL *ssl, const void *bytes,
                                       size_t length) {
    const uint8_t *cursor = bytes;
    while (length > 0U) {
        int amount = length > 16384U ? 16384 : (int)length;
        int written = SSL_write(ssl, cursor, amount);
        if (written <= 0) return 0;
        cursor += (size_t)written;
        length -= (size_t)written;
    }
    return 1;
}

static int protocol_test_connect_loopback(uint16_t port) {
    int descriptor = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
    if (descriptor < 0) return -1;
    struct sockaddr_in address = {
        .sin_family = AF_INET,
        .sin_port = htons(port),
        .sin_addr = {.s_addr = htonl(INADDR_LOOPBACK)}
    };
    if (connect(descriptor, (struct sockaddr *)&address,
                (socklen_t)sizeof(address)) != 0) {
        (void)close(descriptor);
        return -1;
    }
    return descriptor;
}

static void *protocol_test_tls_to_socket(void *opaque) {
    protocol_tls_relay *relay = opaque;
    uint8_t bytes[32768];
    for (;;) {
        int received = SSL_read(relay->ssl, bytes, (int)sizeof(bytes));
        if (received <= 0 ||
            !protocol_test_send_all(relay->descriptor, bytes,
                                    (size_t)received)) break;
    }
    (void)shutdown(relay->descriptor, SHUT_WR);
    return NULL;
}

static int protocol_test_relay_tls(SSL *ssl, uint16_t port) {
    int descriptor = protocol_test_connect_loopback(port);
    if (descriptor < 0) return 0;
    protocol_tls_relay relay = {.ssl = ssl, .descriptor = descriptor};
    pthread_t outgoing;
    if (pthread_create(&outgoing, NULL, protocol_test_tls_to_socket,
                       &relay) != 0) {
        (void)close(descriptor);
        return 0;
    }
    int success = 1;
    uint8_t bytes[32768];
    for (;;) {
        ssize_t received = recv(descriptor, bytes, sizeof(bytes), 0);
        if (received < 0 && errno == EINTR) continue;
        if (received <= 0) break;
        if (!protocol_test_ssl_write_all(ssl, bytes, (size_t)received)) {
            success = 0;
            break;
        }
    }
    (void)shutdown(descriptor, SHUT_RDWR);
    (void)pthread_join(outgoing, NULL);
    (void)close(descriptor);
    return success;
}

static SSL_CTX *protocol_test_tls_context(void) {
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
            name, "CN", MBSTRING_ASC, (const unsigned char *)"localhost",
            -1, -1, 0) != 1 ||
        X509_set_issuer_name(certificate, name) != 1 ||
        X509_sign(certificate, key, EVP_sha256()) <= 0) goto cleanup;
    context = SSL_CTX_new(TLS_server_method());
    if (context == NULL || SSL_CTX_use_certificate(context, certificate) != 1 ||
        SSL_CTX_use_PrivateKey(context, key) != 1 ||
        SSL_CTX_check_private_key(context) != 1) {
        SSL_CTX_free(context);
        context = NULL;
    }

cleanup:
    X509_free(certificate);
    EVP_PKEY_free(key);
    EVP_PKEY_CTX_free(key_context);
    return context;
}

static void *protocol_test_tls_server_main(void *opaque) {
    protocol_tls_server *server = opaque;
    int client;
    do {
        client = accept(server->descriptor, NULL, NULL);
    } while (client < 0 && errno == EINTR);
    if (client < 0) return NULL;
    SSL *ssl = SSL_new(server->context);
    uint8_t *header = malloc(server->expected_header_length);
    if (ssl != NULL && header != NULL && SSL_set_fd(ssl, client) == 1 &&
        SSL_accept(ssl) == 1 &&
        protocol_test_ssl_read_exact(ssl, header,
                                     server->expected_header_length) &&
        memcmp(header, server->expected_header,
               server->expected_header_length) == 0) {
        if (server->relay_port != 0U) {
            server->success = protocol_test_relay_tls(ssl,
                                                      server->relay_port);
        } else {
            uint8_t payload[128];
            int received = SSL_read(ssl, payload, (int)sizeof(payload));
            if (received > 0 &&
                protocol_test_ssl_write_all(ssl, payload,
                                            (size_t)received)) {
                server->success = 1;
            }
        }
    }
    free(header);
    if (ssl != NULL) {
        (void)SSL_shutdown(ssl);
        SSL_free(ssl);
    }
    (void)close(client);
    return NULL;
}

static int protocol_test_tls_server_start(protocol_tls_server *server,
                                          uint8_t *expected_header,
                                          size_t expected_header_length,
                                          uint16_t relay_port) {
    memset(server, 0, sizeof(*server));
    server->descriptor = -1;
    server->expected_header = expected_header;
    server->expected_header_length = expected_header_length;
    server->relay_port = relay_port;
    server->context = protocol_test_tls_context();
    if (server->context == NULL) return 0;
    server->descriptor = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
    if (server->descriptor < 0) return 0;
    struct sockaddr_in address = {
        .sin_family = AF_INET,
        .sin_port = 0,
        .sin_addr = {.s_addr = htonl(INADDR_LOOPBACK)}
    };
    if (bind(server->descriptor, (struct sockaddr *)&address,
             (socklen_t)sizeof(address)) != 0 ||
        listen(server->descriptor, 1) != 0) return 0;
    socklen_t address_length = (socklen_t)sizeof(address);
    if (getsockname(server->descriptor, (struct sockaddr *)&address,
                    &address_length) != 0) return 0;
    server->port = ntohs(address.sin_port);
    return pthread_create(&server->thread, NULL,
                          protocol_test_tls_server_main, server) == 0;
}

static void protocol_test_tls_server_stop(protocol_tls_server *server) {
    (void)pthread_join(server->thread, NULL);
    if (server->descriptor >= 0) (void)close(server->descriptor);
    SSL_CTX_free(server->context);
}

static void protocol_test_trojan_stream(void) {
    ch_error error;
    uint8_t *expected_header = NULL;
    size_t expected_header_length = 0U;
    CH_TEST_ASSERT(ch_protocol_trojan_header(
        "secret", "destination.example:443", &expected_header,
        &expected_header_length, &error) == CH_OK);
    protocol_tls_server server;
    CH_TEST_ASSERT(protocol_test_tls_server_start(
        &server, expected_header, expected_header_length, 0U));
    char document[1024];
    (void)snprintf(
        document, sizeof(document),
        "active = \"local\"\n"
        "[[profile]]\nname = \"local\"\n"
        "[[profile.chain]]\nname = \"default\"\n"
        "[[profile.chain.server]]\n"
        "address = \"127.0.0.1:%u\"\n"
        "protocol = \"trojan\"\n"
        "[profile.chain.server.settings]\n"
        "password = \"secret\"\n"
        "sni = \"localhost\"\n"
        "skip_cert_verify = true\n"
        "alpn = [\"http/1.1\"]\n",
        (unsigned int)server.port);
    ch_config *config = NULL;
    CH_TEST_ASSERT(ch_config_parse(document, NULL, &config, &error) == CH_OK);
    const ch_config_table *profile = ch_config_active_profile(config);
    const ch_config_array *chains = ch_config_table_get_array(profile, "chain");
    const ch_config_table *chain = ch_config_array_get_table(chains, 0U);
    int stream = -1;
    CH_TEST_ASSERT(ch_protocol_chain_dial(chain, "tcp",
                                          "destination.example:443", &stream,
                                          &error) == CH_OK);
    static const char payload[] = "native-trojan-echo";
    char echoed[sizeof(payload)];
    CH_TEST_ASSERT(protocol_test_send_all(stream, payload, sizeof(payload)));
    CH_TEST_ASSERT(protocol_test_receive_exact(stream, echoed, sizeof(echoed)));
    CH_TEST_ASSERT(memcmp(payload, echoed, sizeof(payload)) == 0);
    (void)shutdown(stream, SHUT_RDWR);
    (void)close(stream);
    protocol_test_tls_server_stop(&server);
    CH_TEST_ASSERT(server.success == 1);
    ch_config_free(config);
    free(expected_header);
}

static void protocol_test_trojan_chain(void) {
    ch_error error;
    uint8_t *inner_header = NULL;
    size_t inner_header_length = 0U;
    CH_TEST_ASSERT(ch_protocol_trojan_header(
        "inner-secret", "destination.example:443", &inner_header,
        &inner_header_length, &error) == CH_OK);
    protocol_tls_server inner;
    CH_TEST_ASSERT(protocol_test_tls_server_start(
        &inner, inner_header, inner_header_length, 0U));
    char inner_address[64];
    (void)snprintf(inner_address, sizeof(inner_address), "127.0.0.1:%u",
                   (unsigned int)inner.port);
    uint8_t *outer_header = NULL;
    size_t outer_header_length = 0U;
    CH_TEST_ASSERT(ch_protocol_trojan_header(
        "outer-secret", inner_address, &outer_header, &outer_header_length,
        &error) == CH_OK);
    protocol_tls_server outer;
    CH_TEST_ASSERT(protocol_test_tls_server_start(
        &outer, outer_header, outer_header_length, inner.port));

    char document[1600];
    (void)snprintf(
        document, sizeof(document),
        "active = \"local\"\n"
        "[[profile]]\nname = \"local\"\n"
        "[[profile.chain]]\nname = \"nested\"\n"
        "[[profile.chain.server]]\n"
        "address = \"127.0.0.1:%u\"\n"
        "protocol = \"trojan\"\n"
        "[profile.chain.server.settings]\n"
        "password = \"outer-secret\"\n"
        "sni = \"localhost\"\n"
        "skip_cert_verify = true\n"
        "[[profile.chain.server]]\n"
        "address = \"127.0.0.1:%u\"\n"
        "protocol = \"clambback\"\n"
        "[profile.chain.server.settings]\n"
        "password = \"inner-secret\"\n"
        "sni = \"localhost\"\n"
        "skip_cert_verify = true\n",
        (unsigned int)outer.port, (unsigned int)inner.port);
    ch_config *config = NULL;
    CH_TEST_ASSERT(ch_config_parse(document, NULL, &config, &error) == CH_OK);
    const ch_config_table *profile = ch_config_active_profile(config);
    const ch_config_array *chains = ch_config_table_get_array(profile, "chain");
    const ch_config_table *chain = ch_config_array_get_table(chains, 0U);
    int stream = -1;
    CH_TEST_ASSERT(ch_protocol_chain_dial(chain, "tcp",
                                          "destination.example:443", &stream,
                                          &error) == CH_OK);
    static const char payload[] = "nested-trojan-clambback";
    char echoed[sizeof(payload)];
    CH_TEST_ASSERT(protocol_test_send_all(stream, payload, sizeof(payload)));
    CH_TEST_ASSERT(protocol_test_receive_exact(stream, echoed, sizeof(echoed)));
    CH_TEST_ASSERT(memcmp(payload, echoed, sizeof(payload)) == 0);
    (void)shutdown(stream, SHUT_RDWR);
    (void)close(stream);
    protocol_test_tls_server_stop(&inner);
    protocol_test_tls_server_stop(&outer);
    CH_TEST_ASSERT(inner.success == 1);
    CH_TEST_ASSERT(outer.success == 1);
    ch_config_free(config);
    free(outer_header);
    free(inner_header);
}

void ch_test_protocol(void) {
    ch_error error;
    uint8_t *header = NULL;
    size_t length = 0U;
    CH_TEST_ASSERT(ch_protocol_trojan_header("secret", "1.2.3.4:80",
                                            &header, &length, &error) == CH_OK);
    static const char expected_hash[] =
        "95c7fbca92ac5083afda62a564a3d014fc3b72c9140e3cb99ea6bf12";
    static const uint8_t expected_suffix[] = {
        '\r','\n',0x01U,0x01U,1U,2U,3U,4U,0U,80U,'\r','\n'
    };
    CH_TEST_ASSERT(length == 68U);
    CH_TEST_ASSERT(memcmp(header, expected_hash, 56U) == 0);
    CH_TEST_ASSERT(memcmp(header + 56U, expected_suffix,
                          sizeof(expected_suffix)) == 0);
    free(header);
    header = NULL;
    CH_TEST_ASSERT(ch_protocol_trojan_header("secret", "[2001:db8::1]:443",
                                            &header, &length, &error) == CH_OK);
    CH_TEST_ASSERT(length == 80U);
    CH_TEST_ASSERT(header[58] == 0x01U);
    CH_TEST_ASSERT(header[59] == 0x04U);
    free(header);
    CH_TEST_ASSERT(ch_protocol_trojan_header("", "example.com:443",
                                            &header, &length, &error) ==
                   CH_ERROR_INVALID_ARGUMENT);
    protocol_test_trojan_stream();
    protocol_test_trojan_chain();
}
