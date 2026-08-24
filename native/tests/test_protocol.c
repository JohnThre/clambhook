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
#include "clambhook/socks.h"
#include "protocol_shadowsocks.h"

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

typedef struct protocol_ss_server {
    int descriptor;
    uint16_t port;
    pthread_t thread;
    ch_ss_cipher cipher;
    const char *password;
    const char *expected_host;
    uint16_t expected_port;
    uint16_t relay_port;
    int success;
} protocol_ss_server;

typedef struct protocol_ss_relay {
    int client_descriptor;
    int target_descriptor;
    ch_ss_cipher cipher;
    uint8_t subkey[32];
    uint8_t nonce[12];
} protocol_ss_relay;

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

static int protocol_test_ss_read_chunk(int descriptor,
                                       const ch_ss_cipher *cipher,
                                       const uint8_t *subkey,
                                       uint8_t nonce[12],
                                       uint8_t **out_plaintext,
                                       size_t *out_plaintext_length) {
    uint8_t length_frame[18];
    ch_error error;
    if (!protocol_test_receive_exact(descriptor, length_frame,
                                     sizeof(length_frame))) return 0;
    size_t plaintext_length = 0U;
    if (ch_ss_decrypt_length(cipher, subkey, nonce, length_frame,
                             &plaintext_length, &error) != CH_OK) return 0;
    uint8_t *payload_frame = malloc(plaintext_length + 16U);
    uint8_t *plaintext = malloc(plaintext_length);
    if (payload_frame == NULL || plaintext == NULL ||
        !protocol_test_receive_exact(descriptor, payload_frame,
                                     plaintext_length + 16U) ||
        ch_ss_decrypt_payload(cipher, subkey, nonce, payload_frame,
                              plaintext_length, plaintext, &error) != CH_OK) {
        free(payload_frame);
        free(plaintext);
        return 0;
    }
    free(payload_frame);
    *out_plaintext = plaintext;
    *out_plaintext_length = plaintext_length;
    return 1;
}

static void *protocol_test_ss_to_socket(void *opaque) {
    protocol_ss_relay *relay = opaque;
    for (;;) {
        uint8_t *plaintext = NULL;
        size_t plaintext_length = 0U;
        if (!protocol_test_ss_read_chunk(
                relay->client_descriptor, &relay->cipher, relay->subkey,
                relay->nonce, &plaintext, &plaintext_length)) {
            free(plaintext);
            break;
        }
        int sent = protocol_test_send_all(relay->target_descriptor, plaintext,
                                          plaintext_length);
        free(plaintext);
        if (!sent) break;
    }
    (void)shutdown(relay->target_descriptor, SHUT_WR);
    return NULL;
}

static int protocol_test_ss_relay(int client, const ch_ss_cipher *cipher,
                                  const uint8_t *client_subkey,
                                  const uint8_t client_nonce[12],
                                  const uint8_t *server_subkey,
                                  uint16_t relay_port) {
    int target = protocol_test_connect_loopback(relay_port);
    if (target < 0) return 0;
    protocol_ss_relay relay = {
        .client_descriptor = client,
        .target_descriptor = target,
        .cipher = *cipher
    };
    memcpy(relay.subkey, client_subkey, cipher->key_size);
    memcpy(relay.nonce, client_nonce, sizeof(relay.nonce));
    pthread_t outgoing;
    if (pthread_create(&outgoing, NULL, protocol_test_ss_to_socket,
                       &relay) != 0) {
        (void)close(target);
        return 0;
    }
    uint8_t server_nonce[12] = {0};
    int success = 1;
    uint8_t plaintext[32768];
    for (;;) {
        ssize_t received = recv(target, plaintext, sizeof(plaintext), 0);
        if (received < 0 && errno == EINTR) continue;
        if (received <= 0) break;
        size_t offset = 0U;
        while (offset < (size_t)received) {
            size_t chunk = (size_t)received - offset;
            if (chunk > 0x3fffU) chunk = 0x3fffU;
            uint8_t *frame = NULL;
            size_t frame_length = 0U;
            ch_error error;
            if (ch_ss_encrypt_chunk(cipher, server_subkey, server_nonce,
                                    plaintext + offset, chunk, &frame,
                                    &frame_length, &error) != CH_OK ||
                !protocol_test_send_all(client, frame, frame_length)) {
                free(frame);
                success = 0;
                break;
            }
            free(frame);
            offset += chunk;
        }
        if (!success) break;
    }
    (void)shutdown(target, SHUT_RDWR);
    (void)shutdown(client, SHUT_RDWR);
    (void)pthread_join(outgoing, NULL);
    (void)close(target);
    return success;
}

static void *protocol_test_ss_server_main(void *opaque) {
    protocol_ss_server *server = opaque;
    int client;
    do {
        client = accept(server->descriptor, NULL, NULL);
    } while (client < 0 && errno == EINTR);
    if (client < 0) return NULL;
    uint8_t master_key[32];
    uint8_t client_salt[32];
    uint8_t client_subkey[32];
    uint8_t client_nonce[12] = {0};
    static const uint8_t info[] = "ss-subkey";
    ch_error error;
    int healthy = ch_ss_evp_bytes_to_key(
        (const uint8_t *)server->password, strlen(server->password),
        server->cipher.key_size, master_key, &error) == CH_OK &&
        protocol_test_receive_exact(client, client_salt,
                                    server->cipher.salt_size) &&
        ch_ss_hkdf_sha1(master_key, server->cipher.key_size, client_salt,
                        server->cipher.salt_size, info, sizeof(info) - 1U,
                        client_subkey, server->cipher.key_size,
                        &error) == CH_OK;
    uint8_t *address = NULL;
    size_t address_length = 0U;
    if (healthy) {
        healthy = protocol_test_ss_read_chunk(
            client, &server->cipher, client_subkey, client_nonce, &address,
            &address_length);
    }
    if (healthy) {
        char *host = NULL;
        uint16_t port = 0U;
        size_t consumed = 0U;
        healthy = ch_socks_decode_address(address, address_length, &host,
                                           &port, &consumed, &error) == CH_OK &&
                  consumed == address_length &&
                  strcmp(host, server->expected_host) == 0 &&
                  port == server->expected_port;
        free(host);
    }
    free(address);
    uint8_t server_salt[32];
    for (size_t index = 0U; index < server->cipher.salt_size; ++index) {
        server_salt[index] = (uint8_t)(index + 1U);
    }
    uint8_t server_subkey[32];
    uint8_t server_nonce[12] = {0};
    if (healthy) {
        healthy = protocol_test_send_all(client, server_salt,
                                          server->cipher.salt_size) &&
            ch_ss_hkdf_sha1(master_key, server->cipher.key_size, server_salt,
                            server->cipher.salt_size, info,
                            sizeof(info) - 1U, server_subkey,
                            server->cipher.key_size, &error) == CH_OK;
    }
    if (healthy && server->relay_port != 0U) {
        healthy = protocol_test_ss_relay(
            client, &server->cipher, client_subkey, client_nonce,
            server_subkey, server->relay_port);
    } else if (healthy) {
        uint8_t *payload = NULL;
        size_t payload_length = 0U;
        healthy = protocol_test_ss_read_chunk(
            client, &server->cipher, client_subkey, client_nonce, &payload,
            &payload_length);
        uint8_t *frame = NULL;
        size_t frame_length = 0U;
        if (healthy) {
            healthy = ch_ss_encrypt_chunk(
                &server->cipher, server_subkey, server_nonce, payload,
                payload_length, &frame, &frame_length, &error) == CH_OK &&
                protocol_test_send_all(client, frame, frame_length);
        }
        free(frame);
        free(payload);
    }
    server->success = healthy;
    (void)shutdown(client, SHUT_RDWR);
    (void)close(client);
    return NULL;
}

static int protocol_test_ss_server_start(protocol_ss_server *server,
                                         const char *method,
                                         const char *password,
                                         const char *expected_host,
                                         uint16_t expected_port,
                                         uint16_t relay_port) {
    memset(server, 0, sizeof(*server));
    server->descriptor = -1;
    server->password = password;
    server->expected_host = expected_host;
    server->expected_port = expected_port;
    server->relay_port = relay_port;
    ch_error error;
    if (ch_ss_cipher_from_name(method, &server->cipher, &error) != CH_OK) {
        return 0;
    }
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
    socklen_t length = (socklen_t)sizeof(address);
    if (getsockname(server->descriptor, (struct sockaddr *)&address,
                    &length) != 0) return 0;
    server->port = ntohs(address.sin_port);
    return pthread_create(&server->thread, NULL,
                          protocol_test_ss_server_main, server) == 0;
}

static void protocol_test_ss_server_stop(protocol_ss_server *server) {
    (void)pthread_join(server->thread, NULL);
    if (server->descriptor >= 0) (void)close(server->descriptor);
}

static void protocol_test_shadowsocks_kdf(void) {
    ch_error error;
    uint8_t master[16];
    static const uint8_t md5_foo[16] = {
        0xacU,0xbdU,0x18U,0xdbU,0x4cU,0xc2U,0xf8U,0x5cU,
        0xedU,0xefU,0x65U,0x4fU,0xccU,0xc4U,0xa4U,0xd8U
    };
    CH_TEST_ASSERT(ch_ss_evp_bytes_to_key((const uint8_t *)"foo", 3U,
                                          sizeof(master), master,
                                          &error) == CH_OK);
    CH_TEST_ASSERT(memcmp(master, md5_foo, sizeof(master)) == 0);
    static const uint8_t ikm[11] = {
        0x0bU,0x0bU,0x0bU,0x0bU,0x0bU,0x0bU,0x0bU,0x0bU,
        0x0bU,0x0bU,0x0bU
    };
    static const uint8_t salt[13] = {
        0x00U,0x01U,0x02U,0x03U,0x04U,0x05U,0x06U,
        0x07U,0x08U,0x09U,0x0aU,0x0bU,0x0cU
    };
    static const uint8_t info[10] = {
        0xf0U,0xf1U,0xf2U,0xf3U,0xf4U,0xf5U,0xf6U,0xf7U,0xf8U,0xf9U
    };
    static const uint8_t expected[42] = {
        0x08U,0x5aU,0x01U,0xeaU,0x1bU,0x10U,0xf3U,0x69U,
        0x33U,0x06U,0x8bU,0x56U,0xefU,0xa5U,0xadU,0x81U,
        0xa4U,0xf1U,0x4bU,0x82U,0x2fU,0x5bU,0x09U,0x15U,
        0x68U,0xa9U,0xcdU,0xd4U,0xf1U,0x55U,0xfdU,0xa2U,
        0xc2U,0x2eU,0x42U,0x24U,0x78U,0xd3U,0x05U,0xf3U,
        0xf8U,0x96U
    };
    uint8_t derived[42];
    CH_TEST_ASSERT(ch_ss_hkdf_sha1(ikm, sizeof(ikm), salt, sizeof(salt),
                                   info, sizeof(info), derived,
                                   sizeof(derived), &error) == CH_OK);
    CH_TEST_ASSERT(memcmp(derived, expected, sizeof(expected)) == 0);
    uint8_t nonce[12] = {0xffU, 0U};
    ch_ss_nonce_increment(nonce);
    CH_TEST_ASSERT(nonce[0] == 0U && nonce[1] == 1U);
}

static void protocol_test_shadowsocks_frames(void) {
    static const char *const methods[] = {
        "aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305"
    };
    for (size_t index = 0U; index < sizeof(methods) / sizeof(methods[0]);
         ++index) {
        ch_error error;
        ch_ss_cipher cipher;
        CH_TEST_ASSERT(ch_ss_cipher_from_name(methods[index], &cipher,
                                              &error) == CH_OK);
        uint8_t key[32];
        memset(key, 0xa5, sizeof(key));
        uint8_t write_nonce[12] = {0};
        uint8_t *frame = NULL;
        size_t frame_length = 0U;
        static const uint8_t plaintext[] = "shadowsocks-frame";
        CH_TEST_ASSERT(ch_ss_encrypt_chunk(
            &cipher, key, write_nonce, plaintext, sizeof(plaintext), &frame,
            &frame_length, &error) == CH_OK);
        CH_TEST_ASSERT(write_nonce[0] == 2U);
        uint8_t read_nonce[12] = {0};
        uint8_t *decrypted = NULL;
        size_t decrypted_length = 0U;
        CH_TEST_ASSERT(ch_ss_decrypt_chunk(
            &cipher, key, read_nonce, frame, frame + 18U,
            frame_length - 18U, &decrypted, &decrypted_length,
            &error) == CH_OK);
        CH_TEST_ASSERT(decrypted_length == sizeof(plaintext));
        CH_TEST_ASSERT(memcmp(decrypted, plaintext, sizeof(plaintext)) == 0);
        free(decrypted);
        frame[frame_length - 1U] ^= 0x01U;
        memset(read_nonce, 0, sizeof(read_nonce));
        CH_TEST_ASSERT(ch_ss_decrypt_chunk(
            &cipher, key, read_nonce, frame, frame + 18U,
            frame_length - 18U, &decrypted, &decrypted_length,
            &error) == CH_ERROR_PARSE);
        free(frame);
    }
}

static void protocol_test_shadowsocks_streams(void) {
    static const char *const methods[] = {
        "aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305"
    };
    for (size_t index = 0U; index < sizeof(methods) / sizeof(methods[0]);
         ++index) {
        protocol_ss_server server;
        CH_TEST_ASSERT(protocol_test_ss_server_start(
            &server, methods[index], "secret", "example.com", 443U, 0U));
        char document[1024];
        (void)snprintf(
            document, sizeof(document),
            "active = \"local\"\n"
            "[[profile]]\nname = \"local\"\n"
            "[[profile.chain]]\nname = \"default\"\n"
            "[[profile.chain.server]]\n"
            "address = \"127.0.0.1:%u\"\n"
            "protocol = \"shadowsocks\"\n"
            "[profile.chain.server.settings]\n"
            "method = \"%s\"\n"
            "password = \"secret\"\n",
            (unsigned int)server.port, methods[index]);
        ch_error error;
        ch_config *config = NULL;
        CH_TEST_ASSERT(ch_config_parse(document, NULL, &config,
                                       &error) == CH_OK);
        const ch_config_table *profile = ch_config_active_profile(config);
        const ch_config_array *chains = ch_config_table_get_array(profile,
                                                                  "chain");
        const ch_config_table *chain = ch_config_array_get_table(chains, 0U);
        int stream = -1;
        CH_TEST_ASSERT(ch_protocol_chain_dial(chain, "tcp",
                                              "example.com:443", &stream,
                                              &error) == CH_OK);
        static const char payload[] = "native-shadowsocks-echo";
        char echoed[sizeof(payload)];
        CH_TEST_ASSERT(protocol_test_send_all(stream, payload,
                                              sizeof(payload)));
        CH_TEST_ASSERT(protocol_test_receive_exact(stream, echoed,
                                                   sizeof(echoed)));
        CH_TEST_ASSERT(memcmp(payload, echoed, sizeof(payload)) == 0);
        (void)shutdown(stream, SHUT_RDWR);
        (void)close(stream);
        protocol_test_ss_server_stop(&server);
        CH_TEST_ASSERT(server.success == 1);
        ch_config_free(config);
    }
}

static void protocol_test_shadowsocks_chain(void) {
    const char *method = "chacha20-ietf-poly1305";
    protocol_ss_server inner;
    CH_TEST_ASSERT(protocol_test_ss_server_start(
        &inner, method, "inner-secret", "example.com", 443U, 0U));
    protocol_ss_server outer;
    CH_TEST_ASSERT(protocol_test_ss_server_start(
        &outer, method, "outer-secret", "127.0.0.1", inner.port,
        inner.port));
    char document[1600];
    (void)snprintf(
        document, sizeof(document),
        "active = \"local\"\n"
        "[[profile]]\nname = \"local\"\n"
        "[[profile.chain]]\nname = \"nested\"\n"
        "[[profile.chain.server]]\n"
        "address = \"127.0.0.1:%u\"\n"
        "protocol = \"shadowsocks\"\n"
        "[profile.chain.server.settings]\n"
        "method = \"%s\"\npassword = \"outer-secret\"\n"
        "[[profile.chain.server]]\n"
        "address = \"127.0.0.1:%u\"\n"
        "protocol = \"shadowsocks\"\n"
        "[profile.chain.server.settings]\n"
        "method = \"%s\"\npassword = \"inner-secret\"\n",
        (unsigned int)outer.port, method, (unsigned int)inner.port, method);
    ch_error error;
    ch_config *config = NULL;
    CH_TEST_ASSERT(ch_config_parse(document, NULL, &config, &error) == CH_OK);
    const ch_config_table *profile = ch_config_active_profile(config);
    const ch_config_array *chains = ch_config_table_get_array(profile, "chain");
    const ch_config_table *chain = ch_config_array_get_table(chains, 0U);
    int stream = -1;
    CH_TEST_ASSERT(ch_protocol_chain_dial(chain, "tcp", "example.com:443",
                                          &stream, &error) == CH_OK);
    static const char payload[] = "nested-native-shadowsocks";
    char echoed[sizeof(payload)];
    CH_TEST_ASSERT(protocol_test_send_all(stream, payload, sizeof(payload)));
    CH_TEST_ASSERT(protocol_test_receive_exact(stream, echoed,
                                               sizeof(echoed)));
    CH_TEST_ASSERT(memcmp(payload, echoed, sizeof(payload)) == 0);
    (void)shutdown(stream, SHUT_RDWR);
    (void)close(stream);
    protocol_test_ss_server_stop(&inner);
    protocol_test_ss_server_stop(&outer);
    CH_TEST_ASSERT(inner.success == 1);
    CH_TEST_ASSERT(outer.success == 1);
    ch_config_free(config);
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
    protocol_test_shadowsocks_kdf();
    protocol_test_shadowsocks_frames();
    protocol_test_shadowsocks_streams();
    protocol_test_shadowsocks_chain();
}
