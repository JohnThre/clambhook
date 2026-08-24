#include "test.h"

#include <arpa/inet.h>
#include <errno.h>
#include <openssl/core_names.h>
#include <openssl/evp.h>
#include <openssl/params.h>
#include <openssl/rsa.h>
#include <openssl/ssl.h>
#include <openssl/x509.h>
#include <pthread.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>

#include "clambhook/config.h"
#include "clambhook/protocol.h"
#include "clambhook/socks.h"
#include "cnet.h"
#include "protocol_shadowtls.h"
#include "protocol_shadowsocks.h"
#include "protocol_vmess.h"

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

typedef struct protocol_tor_server {
    int descriptor;
    uint16_t port;
    pthread_t thread;
    int require_auth;
    const char *expected_user;
    const char *expected_password;
    const char *expected_host;
    uint16_t expected_port;
    int success;
} protocol_tor_server;

typedef struct protocol_vmess_server {
    int descriptor;
    uint16_t port;
    pthread_t thread;
    ch_vmess_security security;
    int success;
} protocol_vmess_server;

typedef struct protocol_test_hmac {
    EVP_MAC *algorithm;
    EVP_MAC_CTX *context;
} protocol_test_hmac;

typedef struct protocol_stls_server {
    int descriptor;
    uint16_t port;
    pthread_t thread;
    SSL_CTX *context;
    const char *password;
    int success;
} protocol_stls_server;

typedef struct protocol_stls_relay {
    int client_descriptor;
    int handshake_descriptor;
    const char *password;
    pthread_mutex_t mutex;
    uint8_t server_random[32];
    int random_ready;
    int success;
} protocol_stls_relay;

typedef struct protocol_stls_tls_worker {
    int descriptor;
    SSL_CTX *context;
    int success;
} protocol_stls_tls_worker;

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

static uint32_t protocol_test_crc32(const uint8_t *bytes, size_t length) {
    uint32_t crc = UINT32_MAX;
    for (size_t index = 0U; index < length; ++index) {
        crc ^= bytes[index];
        for (unsigned int bit = 0U; bit < 8U; ++bit) {
            uint32_t mask = (uint32_t)-(int32_t)(crc & 1U);
            crc = (crc >> 1U) ^ (0xedb88320U & mask);
        }
    }
    return ~crc;
}

static uint32_t protocol_test_fnv1a(const uint8_t *bytes, size_t length) {
    uint32_t hash = 2166136261U;
    for (size_t index = 0U; index < length; ++index) {
        hash ^= bytes[index];
        hash *= 16777619U;
    }
    return hash;
}

static int protocol_test_sha256_16(const uint8_t input[16],
                                   uint8_t output[16]) {
    uint8_t digest[32];
    unsigned int length = 0U;
    int ok = EVP_Digest(input, 16U, digest, &length, EVP_sha256(), NULL) == 1 &&
             length == sizeof(digest);
    if (ok) memcpy(output, digest, 16U);
    return ok;
}

static int protocol_test_aes128_ecb_decrypt(const uint8_t key[16],
                                            const uint8_t input[16],
                                            uint8_t output[16]) {
    EVP_CIPHER_CTX *context = EVP_CIPHER_CTX_new();
    int written = 0;
    int final_written = 0;
    int ok = context != NULL &&
        EVP_DecryptInit_ex(context, EVP_aes_128_ecb(), NULL, key, NULL) == 1 &&
        EVP_CIPHER_CTX_set_padding(context, 0) == 1 &&
        EVP_DecryptUpdate(context, output, &written, input, 16) == 1 &&
        EVP_DecryptFinal_ex(context, output + written, &final_written) == 1 &&
        written + final_written == 16;
    EVP_CIPHER_CTX_free(context);
    return ok;
}

static void protocol_test_hmac_free(protocol_test_hmac *mac) {
    EVP_MAC_CTX_free(mac->context);
    EVP_MAC_free(mac->algorithm);
    memset(mac, 0, sizeof(*mac));
}

static int protocol_test_hmac_init(protocol_test_hmac *mac,
                                   const char *password,
                                   const uint8_t random[32],
                                   const char *suffix) {
    memset(mac, 0, sizeof(*mac));
    mac->algorithm = EVP_MAC_fetch(NULL, "HMAC", NULL);
    mac->context = mac->algorithm == NULL ? NULL :
                   EVP_MAC_CTX_new(mac->algorithm);
    char digest[] = "SHA1";
    OSSL_PARAM params[] = {
        OSSL_PARAM_construct_utf8_string(OSSL_MAC_PARAM_DIGEST, digest, 0U),
        OSSL_PARAM_construct_end()
    };
    int ok = mac->context != NULL && EVP_MAC_init(
        mac->context, (const uint8_t *)password, strlen(password), params) == 1 &&
        EVP_MAC_update(mac->context, random, 32U) == 1 &&
        (suffix[0] == '\0' || EVP_MAC_update(
            mac->context, (const uint8_t *)suffix, strlen(suffix)) == 1);
    if (!ok) protocol_test_hmac_free(mac);
    return ok;
}

static int protocol_test_hmac_add(protocol_test_hmac *mac,
                                  const uint8_t *payload,
                                  size_t payload_length,
                                  uint8_t signature[4],
                                  int chain_signature) {
    if (EVP_MAC_update(mac->context, payload, payload_length) != 1) return 0;
    EVP_MAC_CTX *copy = EVP_MAC_CTX_dup(mac->context);
    uint8_t sum[20];
    size_t sum_length = 0U;
    int ok = copy != NULL && EVP_MAC_final(
        copy, sum, &sum_length, sizeof(sum)) == 1 && sum_length == sizeof(sum);
    EVP_MAC_CTX_free(copy);
    if (!ok) return 0;
    memcpy(signature, sum, 4U);
    return !chain_signature || EVP_MAC_update(
        mac->context, signature, 4U) == 1;
}

static int protocol_test_hmac_verify(protocol_test_hmac *mac,
                                     const uint8_t *payload,
                                     size_t payload_length,
                                     const uint8_t signature[4],
                                     int chain_signature) {
    uint8_t expected[4];
    return protocol_test_hmac_add(mac, payload, payload_length, expected,
                                  chain_signature) &&
           CRYPTO_memcmp(expected, signature, sizeof(expected)) == 0;
}

static int protocol_test_shadowtls_xor_key(const char *password,
                                           const uint8_t random[32],
                                           uint8_t output[32]) {
    EVP_MD_CTX *context = EVP_MD_CTX_new();
    unsigned int length = 0U;
    int ok = context != NULL && EVP_DigestInit_ex(
        context, EVP_sha256(), NULL) == 1 && EVP_DigestUpdate(
        context, password, strlen(password)) == 1 && EVP_DigestUpdate(
        context, random, 32U) == 1 && EVP_DigestFinal_ex(
        context, output, &length) == 1 && length == 32U;
    EVP_MD_CTX_free(context);
    return ok;
}

static void protocol_test_shadowtls_xor(uint8_t *bytes, size_t length,
                                        const uint8_t key[32]) {
    for (size_t index = 0U; index < length; ++index) {
        bytes[index] ^= key[index % 32U];
    }
}

static int protocol_test_vmess_derive(
    const uint8_t *key, size_t key_length, const char *label,
    const uint8_t *extra_one, size_t extra_one_length,
    const uint8_t *extra_two, size_t extra_two_length,
    uint8_t *output, size_t output_length) {
    const uint8_t *paths[3] = {(const uint8_t *)label, extra_one, extra_two};
    size_t lengths[3] = {strlen(label), extra_one_length, extra_two_length};
    size_t count = extra_two != NULL ? 3U : extra_one != NULL ? 2U : 1U;
    uint8_t derived[32];
    ch_error error;
    if (output_length > sizeof(derived) ||
        ch_vmess_kdf(key, key_length, paths, lengths, count, derived,
                     &error) != CH_OK) return 0;
    memcpy(output, derived, output_length);
    return 1;
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

static void *protocol_test_tor_server_main(void *opaque) {
    protocol_tor_server *server = opaque;
    int client;
    do {
        client = accept(server->descriptor, NULL, NULL);
    } while (client < 0 && errno == EINTR);
    if (client < 0) return NULL;
    uint8_t greeting[2];
    int healthy = protocol_test_receive_exact(client, greeting,
                                               sizeof(greeting)) &&
                  greeting[0] == 0x05U && greeting[1] > 0U;
    uint8_t methods[255];
    if (healthy) {
        healthy = protocol_test_receive_exact(client, methods, greeting[1]);
    }
    uint8_t selected = server->require_auth ? 0x02U : 0x00U;
    int offered = 0;
    for (size_t index = 0U; healthy && index < (size_t)greeting[1]; ++index) {
        if (methods[index] == selected) offered = 1;
    }
    uint8_t method_reply[2] = {0x05U, offered ? selected : 0xffU};
    healthy = healthy && protocol_test_send_all(client, method_reply,
                                                 sizeof(method_reply)) &&
              offered;
    if (healthy && server->require_auth) {
        uint8_t auth_header[2];
        healthy = protocol_test_receive_exact(client, auth_header,
                                               sizeof(auth_header)) &&
                  auth_header[0] == 0x01U;
        uint8_t user[255];
        if (healthy) {
            healthy = protocol_test_receive_exact(client, user,
                                                   auth_header[1]);
        }
        uint8_t password_length = 0U;
        uint8_t password[255];
        if (healthy) {
            healthy = protocol_test_receive_exact(client, &password_length,
                                                   1U) &&
                protocol_test_receive_exact(client, password,
                                             password_length) &&
                strlen(server->expected_user) == (size_t)auth_header[1] &&
                memcmp(user, server->expected_user, auth_header[1]) == 0 &&
                strlen(server->expected_password) ==
                    (size_t)password_length &&
                memcmp(password, server->expected_password,
                       password_length) == 0;
        }
        uint8_t auth_reply[2] = {0x01U, healthy ? 0x00U : 0x01U};
        healthy = protocol_test_send_all(client, auth_reply,
                                         sizeof(auth_reply)) && healthy;
    }
    uint8_t request[4];
    if (healthy) {
        healthy = protocol_test_receive_exact(client, request,
                                               sizeof(request)) &&
                  request[0] == 0x05U && request[1] == 0x01U &&
                  request[2] == 0x00U;
    }
    char host[256];
    memset(host, 0, sizeof(host));
    if (healthy && request[3] == CH_SOCKS_ATYP_IPV4) {
        uint8_t address[4];
        healthy = protocol_test_receive_exact(client, address,
                                               sizeof(address));
        if (healthy) {
            (void)snprintf(host, sizeof(host), "%u.%u.%u.%u",
                           (unsigned int)address[0],
                           (unsigned int)address[1],
                           (unsigned int)address[2],
                           (unsigned int)address[3]);
        }
    } else if (healthy && request[3] == CH_SOCKS_ATYP_DOMAIN) {
        uint8_t length = 0U;
        healthy = protocol_test_receive_exact(client, &length, 1U) &&
                  length > 0U &&
                  protocol_test_receive_exact(client, host, length);
        host[length] = '\0';
    } else if (healthy) {
        healthy = 0;
    }
    uint8_t port_bytes[2];
    if (healthy) {
        healthy = protocol_test_receive_exact(client, port_bytes,
                                               sizeof(port_bytes));
    }
    uint16_t port = healthy ?
        (uint16_t)(((uint16_t)port_bytes[0] << 8U) | port_bytes[1]) : 0U;
    healthy = healthy && strcmp(host, server->expected_host) == 0 &&
              port == server->expected_port;
    uint8_t connect_reply[10] = {
        0x05U, healthy ? 0x00U : 0x01U, 0x00U, CH_SOCKS_ATYP_IPV4,
        0U, 0U, 0U, 0U, 0U, 0U
    };
    healthy = protocol_test_send_all(client, connect_reply,
                                     sizeof(connect_reply)) && healthy;
    if (healthy) {
        uint8_t payload[128];
        ssize_t received = recv(client, payload, sizeof(payload), 0);
        healthy = received > 0 && protocol_test_send_all(
            client, payload, (size_t)received);
    }
    server->success = healthy;
    (void)shutdown(client, SHUT_RDWR);
    (void)close(client);
    return NULL;
}

static int protocol_test_tor_server_start(protocol_tor_server *server,
                                          int require_auth,
                                          const char *expected_host,
                                          uint16_t expected_port) {
    memset(server, 0, sizeof(*server));
    server->descriptor = -1;
    server->require_auth = require_auth;
    server->expected_user = "circuit";
    server->expected_password = "profile-1";
    server->expected_host = expected_host;
    server->expected_port = expected_port;
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
                          protocol_test_tor_server_main, server) == 0;
}

static void protocol_test_tor_server_stop(protocol_tor_server *server) {
    (void)pthread_join(server->thread, NULL);
    if (server->descriptor >= 0) (void)close(server->descriptor);
}

static void protocol_test_tor_streams(void) {
    static const char onion[] =
        "duckduckgogg42xjoc72x3sjasowoarfbgcmvfimaftt6twagswzczad.onion";
    for (int authenticated = 0; authenticated <= 1; ++authenticated) {
        const char *host = authenticated ? onion : "example.com";
        uint16_t target_port = authenticated ? 80U : 443U;
        protocol_tor_server server;
        CH_TEST_ASSERT(protocol_test_tor_server_start(
            &server, authenticated, host, target_port));
        char document[1200];
        (void)snprintf(
            document, sizeof(document),
            "active = \"local\"\n"
            "[[profile]]\nname = \"local\"\n"
            "[[profile.chain]]\nname = \"tor\"\n"
            "[[profile.chain.server]]\n"
            "address = \"127.0.0.1:%u\"\n"
            "protocol = \"tor\"\n"
            "%s",
            (unsigned int)server.port,
            authenticated ?
                "[profile.chain.server.settings]\n"
                "isolation_user = \"circuit\"\n"
                "isolation_pass = \"profile-1\"\n" : "");
        ch_error error;
        ch_config *config = NULL;
        CH_TEST_ASSERT(ch_config_parse(document, NULL, &config,
                                       &error) == CH_OK);
        const ch_config_table *profile = ch_config_active_profile(config);
        const ch_config_array *chains = ch_config_table_get_array(profile,
                                                                  "chain");
        const ch_config_table *chain = ch_config_array_get_table(chains, 0U);
        char target[320];
        (void)snprintf(target, sizeof(target), "%s:%u", host,
                       (unsigned int)target_port);
        int stream = -1;
        CH_TEST_ASSERT(ch_protocol_chain_dial(chain, "tcp", target, &stream,
                                              &error) == CH_OK);
        static const char payload[] = "native-tor-socks5-echo";
        char echoed[sizeof(payload)];
        CH_TEST_ASSERT(protocol_test_send_all(stream, payload,
                                              sizeof(payload)));
        CH_TEST_ASSERT(protocol_test_receive_exact(stream, echoed,
                                                   sizeof(echoed)));
        CH_TEST_ASSERT(memcmp(payload, echoed, sizeof(payload)) == 0);
        (void)shutdown(stream, SHUT_RDWR);
        (void)close(stream);
        protocol_test_tor_server_stop(&server);
        CH_TEST_ASSERT(server.success == 1);
        ch_config_free(config);
    }
}

static void protocol_test_tor_after_trojan(void) {
    static const char onion[] =
        "duckduckgogg42xjoc72x3sjasowoarfbgcmvfimaftt6twagswzczad.onion";
    protocol_tor_server tor;
    CH_TEST_ASSERT(protocol_test_tor_server_start(&tor, 1, onion, 80U));
    char tor_address[64];
    (void)snprintf(tor_address, sizeof(tor_address), "127.0.0.1:%u",
                   (unsigned int)tor.port);
    ch_error error;
    uint8_t *outer_header = NULL;
    size_t outer_header_length = 0U;
    CH_TEST_ASSERT(ch_protocol_trojan_header(
        "outer-secret", tor_address, &outer_header, &outer_header_length,
        &error) == CH_OK);
    protocol_tls_server outer;
    CH_TEST_ASSERT(protocol_test_tls_server_start(
        &outer, outer_header, outer_header_length, tor.port));
    char document[1600];
    (void)snprintf(
        document, sizeof(document),
        "active = \"local\"\n"
        "[[profile]]\nname = \"local\"\n"
        "[[profile.chain]]\nname = \"trojan-tor\"\n"
        "[[profile.chain.server]]\n"
        "address = \"127.0.0.1:%u\"\n"
        "protocol = \"trojan\"\n"
        "[profile.chain.server.settings]\n"
        "password = \"outer-secret\"\n"
        "sni = \"localhost\"\n"
        "skip_cert_verify = true\n"
        "[[profile.chain.server]]\n"
        "address = \"127.0.0.1:%u\"\n"
        "protocol = \"tor\"\n"
        "[profile.chain.server.settings]\n"
        "isolation_user = \"circuit\"\n"
        "isolation_pass = \"profile-1\"\n",
        (unsigned int)outer.port, (unsigned int)tor.port);
    ch_config *config = NULL;
    CH_TEST_ASSERT(ch_config_parse(document, NULL, &config, &error) == CH_OK);
    const ch_config_table *profile = ch_config_active_profile(config);
    const ch_config_array *chains = ch_config_table_get_array(profile, "chain");
    const ch_config_table *chain = ch_config_array_get_table(chains, 0U);
    char target[320];
    (void)snprintf(target, sizeof(target), "%s:80", onion);
    int stream = -1;
    CH_TEST_ASSERT(ch_protocol_chain_dial(chain, "tcp", target, &stream,
                                          &error) == CH_OK);
    static const char payload[] = "trojan-to-tor-echo";
    char echoed[sizeof(payload)];
    CH_TEST_ASSERT(protocol_test_send_all(stream, payload, sizeof(payload)));
    CH_TEST_ASSERT(protocol_test_receive_exact(stream, echoed,
                                               sizeof(echoed)));
    CH_TEST_ASSERT(memcmp(payload, echoed, sizeof(payload)) == 0);
    (void)shutdown(stream, SHUT_RDWR);
    (void)close(stream);
    protocol_test_tor_server_stop(&tor);
    protocol_test_tls_server_stop(&outer);
    CH_TEST_ASSERT(tor.success == 1);
    CH_TEST_ASSERT(outer.success == 1);
    ch_config_free(config);
    free(outer_header);
}

static void *protocol_test_vmess_server_main(void *opaque) {
    protocol_vmess_server *server = opaque;
    int client;
    do {
        client = accept(server->descriptor, NULL, NULL);
    } while (client < 0 && errno == EINTR);
    if (client < 0) return NULL;

    static const uint8_t uuid[16] = {
        0xb8U,0x31U,0x38U,0x1dU,0x63U,0x24U,0x4dU,0x53U,
        0xadU,0x4fU,0x8cU,0xdaU,0x48U,0xb3U,0x08U,0x11U
    };
    uint8_t command_key[16];
    ch_error error;
    int healthy = ch_vmess_command_key(uuid, command_key, &error) == CH_OK;
    uint8_t prefix[42];
    healthy = healthy && protocol_test_receive_exact(client, prefix,
                                                      sizeof(prefix));
    const uint8_t *auth_id = prefix;
    const uint8_t *encrypted_length = prefix + 16U;
    const uint8_t *connection_nonce = prefix + 34U;

    uint8_t auth_key[16];
    uint8_t auth_plain[16] = {0};
    healthy = healthy && protocol_test_vmess_derive(
        command_key, sizeof(command_key), "AES Auth ID Encryption",
        NULL, 0U, NULL, 0U, auth_key, sizeof(auth_key)) &&
        protocol_test_aes128_ecb_decrypt(auth_key, auth_id, auth_plain);
    uint64_t timestamp = 0U;
    for (size_t index = 0U; index < 8U; ++index) {
        timestamp = (timestamp << 8U) | auth_plain[index];
    }
    uint32_t auth_checksum =
        ((uint32_t)auth_plain[12] << 24U) |
        ((uint32_t)auth_plain[13] << 16U) |
        ((uint32_t)auth_plain[14] << 8U) | (uint32_t)auth_plain[15];
    time_t now = time(NULL);
    uint64_t now_seconds = now >= (time_t)0 ? (uint64_t)now : 0U;
    healthy = healthy && auth_checksum == protocol_test_crc32(auth_plain, 12U) &&
        timestamp + 10U >= now_seconds && timestamp <= now_seconds + 10U;

    uint8_t length_key[16], length_nonce[12], plain_length[2] = {0};
    healthy = healthy && protocol_test_vmess_derive(
        command_key, sizeof(command_key), "VMess Header AEAD Key_Length",
        auth_id, 16U, connection_nonce, 8U, length_key,
        sizeof(length_key)) && protocol_test_vmess_derive(
        command_key, sizeof(command_key), "VMess Header AEAD Nonce_Length",
        auth_id, 16U, connection_nonce, 8U, length_nonce,
        sizeof(length_nonce)) && cnet_aes128gcm_decrypt(
        length_key, length_nonce, encrypted_length, 2U, auth_id, 16U,
        encrypted_length + 2U, plain_length) == CNET_OK;
    size_t body_length = healthy ?
        ((size_t)plain_length[0] << 8U) | (size_t)plain_length[1] : 0U;
    healthy = healthy && body_length >= 46U;
    uint8_t *encrypted_body = healthy ? malloc(body_length + 16U) : NULL;
    uint8_t *body = healthy ? malloc(body_length) : NULL;
    healthy = healthy && encrypted_body != NULL && body != NULL &&
        protocol_test_receive_exact(client, encrypted_body,
                                    body_length + 16U);
    uint8_t payload_key[16], payload_nonce[12];
    healthy = healthy && protocol_test_vmess_derive(
        command_key, sizeof(command_key), "VMess Header AEAD Key",
        auth_id, 16U, connection_nonce, 8U, payload_key,
        sizeof(payload_key)) && protocol_test_vmess_derive(
        command_key, sizeof(command_key), "VMess Header AEAD Nonce",
        auth_id, 16U, connection_nonce, 8U, payload_nonce,
        sizeof(payload_nonce)) && cnet_aes128gcm_decrypt(
        payload_key, payload_nonce, encrypted_body, body_length, auth_id,
        16U, encrypted_body + body_length, body) == CNET_OK;
    size_t checksum_offset = body_length >= 4U ? body_length - 4U : 0U;
    uint32_t body_checksum = healthy ?
        ((uint32_t)body[checksum_offset] << 24U) |
        ((uint32_t)body[checksum_offset + 1U] << 16U) |
        ((uint32_t)body[checksum_offset + 2U] << 8U) |
        (uint32_t)body[checksum_offset + 3U] : 0U;
    uint8_t expected_security = (uint8_t)server->security;
    healthy = healthy && body[0] == 0x01U && body[34] == 0x01U &&
        body[35] == expected_security && body[36] == 0x00U &&
        body[37] == 0x01U && body[38] == 0x01U && body[39] == 0xbbU &&
        body[40] == 0x02U && body[41] == 11U &&
        checksum_offset == 53U &&
        memcmp(body + 42U, "example.com", 11U) == 0 &&
        body_checksum == protocol_test_fnv1a(body, checksum_offset);

    uint8_t response_key[16], response_iv[16];
    healthy = healthy && protocol_test_sha256_16(body + 17U, response_key) &&
        protocol_test_sha256_16(body + 1U, response_iv);
    uint8_t response_length_key[16], response_length_nonce[12];
    uint8_t response_payload_key[16], response_payload_nonce[12];
    healthy = healthy && protocol_test_vmess_derive(
        response_key, sizeof(response_key), "AEAD Resp Header Len Key",
        NULL, 0U, NULL, 0U, response_length_key,
        sizeof(response_length_key)) && protocol_test_vmess_derive(
        response_iv, sizeof(response_iv), "AEAD Resp Header Len IV",
        NULL, 0U, NULL, 0U, response_length_nonce,
        sizeof(response_length_nonce)) && protocol_test_vmess_derive(
        response_key, sizeof(response_key), "AEAD Resp Header Key",
        NULL, 0U, NULL, 0U, response_payload_key,
        sizeof(response_payload_key)) && protocol_test_vmess_derive(
        response_iv, sizeof(response_iv), "AEAD Resp Header IV",
        NULL, 0U, NULL, 0U, response_payload_nonce,
        sizeof(response_payload_nonce));
    uint8_t response[38];
    static const uint8_t response_length[2] = {0x00U, 0x04U};
    uint8_t response_plain[4] = {0};
    if (healthy) response_plain[0] = body[33];
    healthy = healthy && cnet_aes128gcm_encrypt(
        response_length_key, response_length_nonce, response_length,
        sizeof(response_length), NULL, 0U, response, response + 2U) ==
        CNET_OK && cnet_aes128gcm_encrypt(
        response_payload_key, response_payload_nonce, response_plain,
        sizeof(response_plain), NULL, 0U, response + 18U,
        response + 22U) == CNET_OK && protocol_test_send_all(
        client, response, sizeof(response));

    uint8_t frame_length_bytes[2];
    healthy = healthy && protocol_test_receive_exact(
        client, frame_length_bytes, sizeof(frame_length_bytes));
    size_t frame_payload_length = healthy ?
        ((size_t)frame_length_bytes[0] << 8U) |
        (size_t)frame_length_bytes[1] : 0U;
    uint8_t *frame = healthy ? malloc(frame_payload_length + 2U) : NULL;
    healthy = healthy && frame != NULL && frame_payload_length >= 16U;
    if (healthy) {
        memcpy(frame, frame_length_bytes, 2U);
        healthy = protocol_test_receive_exact(client, frame + 2U,
                                              frame_payload_length);
    }
    uint16_t read_counter = 0U;
    bool read_exhausted = false;
    uint8_t *plaintext = NULL;
    size_t plaintext_length = 0U;
    healthy = healthy && ch_vmess_decrypt_chunk(
        server->security, body + 17U, body + 1U, &read_counter,
        &read_exhausted, frame, frame_payload_length + 2U, &plaintext,
        &plaintext_length, &error) == CH_OK;
    uint16_t write_counter = 0U;
    bool write_exhausted = false;
    uint8_t *echo_frame = NULL;
    size_t echo_frame_length = 0U;
    healthy = healthy && ch_vmess_encrypt_chunk(
        server->security, response_key, response_iv, &write_counter,
        &write_exhausted, plaintext, plaintext_length, &echo_frame,
        &echo_frame_length, &error) == CH_OK && protocol_test_send_all(
        client, echo_frame, echo_frame_length);
    server->success = healthy;
    free(echo_frame);
    free(plaintext);
    free(frame);
    free(body);
    free(encrypted_body);
    (void)shutdown(client, SHUT_RDWR);
    (void)close(client);
    return NULL;
}

static int protocol_test_vmess_server_start(protocol_vmess_server *server,
                                             ch_vmess_security security) {
    memset(server, 0, sizeof(*server));
    server->descriptor = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
    server->security = security;
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
                          protocol_test_vmess_server_main, server) == 0;
}

static void protocol_test_vmess_server_stop(protocol_vmess_server *server) {
    (void)pthread_join(server->thread, NULL);
    if (server->descriptor >= 0) (void)close(server->descriptor);
}

static void protocol_test_vmess_streams(void) {
    static const ch_vmess_security methods[] = {
        CH_VMESS_AES_128_GCM, CH_VMESS_CHACHA20_POLY1305
    };
    static const char *const names[] = {
        "aes-128-gcm", "chacha20-poly1305"
    };
    for (size_t index = 0U; index < sizeof(methods) / sizeof(methods[0]);
         ++index) {
        protocol_vmess_server server;
        CH_TEST_ASSERT(protocol_test_vmess_server_start(&server,
                                                        methods[index]));
        char document[1400];
        (void)snprintf(
            document, sizeof(document),
            "active = \"local\"\n"
            "[[profile]]\nname = \"local\"\n"
            "[[profile.chain]]\nname = \"vmess\"\n"
            "[[profile.chain.server]]\n"
            "address = \"127.0.0.1:%u\"\n"
            "protocol = \"vmess\"\n"
            "[profile.chain.server.settings]\n"
            "uuid = \"b831381d-6324-4d53-ad4f-8cda48b30811\"\n"
            "alter_id = 0\nsecurity = \"%s\"\n",
            (unsigned int)server.port, names[index]);
        ch_error error;
        ch_config *config = NULL;
        CH_TEST_ASSERT(ch_config_parse(document, NULL, &config,
                                       &error) == CH_OK);
        const ch_config_table *profile = ch_config_active_profile(config);
        const ch_config_array *chains = ch_config_table_get_array(profile,
                                                                  "chain");
        const ch_config_table *chain = ch_config_array_get_table(chains, 0U);
        int stream = -1;
        CH_TEST_ASSERT(ch_protocol_chain_dial(
            chain, "tcp", "example.com:443", &stream, &error) == CH_OK);
        static const char payload[] = "native-vmess-aead-echo";
        char echoed[sizeof(payload)];
        CH_TEST_ASSERT(protocol_test_send_all(stream, payload,
                                              sizeof(payload)));
        CH_TEST_ASSERT(protocol_test_receive_exact(stream, echoed,
                                                   sizeof(echoed)));
        CH_TEST_ASSERT(memcmp(payload, echoed, sizeof(payload)) == 0);
        (void)shutdown(stream, SHUT_RDWR);
        (void)close(stream);
        protocol_test_vmess_server_stop(&server);
        CH_TEST_ASSERT(server.success == 1);
        ch_config_free(config);
    }
}

static void *protocol_test_stls_tls_main(void *opaque) {
    protocol_stls_tls_worker *worker = opaque;
    SSL *ssl = SSL_new(worker->context);
    if (ssl != NULL && SSL_set_fd(ssl, worker->descriptor) == 1 &&
        SSL_accept(ssl) == 1) {
        worker->success = 1;
        uint8_t discard[256];
        while (SSL_read(ssl, discard, (int)sizeof(discard)) > 0) {
        }
    }
    SSL_free(ssl);
    (void)close(worker->descriptor);
    return NULL;
}

static void *protocol_test_stls_relay_main(void *opaque) {
    protocol_stls_relay *relay = opaque;
    protocol_test_hmac handshake_mac;
    memset(&handshake_mac, 0, sizeof(handshake_mac));
    uint8_t xor_key[32];
    int has_random = 0;
    relay->success = 1;
    for (;;) {
        uint8_t header[5];
        if (!protocol_test_receive_exact(relay->handshake_descriptor, header,
                                         sizeof(header))) break;
        size_t body_length = ((size_t)header[3] << 8U) | (size_t)header[4];
        uint8_t *body = malloc(body_length == 0U ? 1U : body_length);
        if (body == NULL || !protocol_test_receive_exact(
                relay->handshake_descriptor, body, body_length)) {
            free(body);
            relay->success = 0;
            break;
        }
        if (header[0] == 22U && body_length > 38U && body[0] == 2U) {
            memcpy(relay->server_random, body + 6U, 32U);
            has_random = ch_shadowtls_server_hello_tls13(body, body_length) &&
                protocol_test_hmac_init(&handshake_mac, relay->password,
                                        relay->server_random, "") &&
                protocol_test_shadowtls_xor_key(
                    relay->password, relay->server_random, xor_key);
            (void)pthread_mutex_lock(&relay->mutex);
            relay->random_ready = has_random;
            (void)pthread_mutex_unlock(&relay->mutex);
            if (!has_random) relay->success = 0;
        }
        uint8_t *wire = body;
        size_t wire_length = body_length;
        if (header[0] == 23U && has_random) {
            wire = malloc(body_length + 4U);
            if (wire == NULL) {
                free(body);
                relay->success = 0;
                break;
            }
            memcpy(wire + 4U, body, body_length);
            protocol_test_shadowtls_xor(wire + 4U, body_length, xor_key);
            if (!protocol_test_hmac_add(&handshake_mac, wire + 4U,
                                        body_length, wire, 0)) {
                free(wire);
                free(body);
                relay->success = 0;
                break;
            }
            wire_length += 4U;
            header[3] = (uint8_t)(wire_length >> 8U);
            header[4] = (uint8_t)wire_length;
        }
        int sent = protocol_test_send_all(relay->client_descriptor, header,
                                          sizeof(header)) &&
                   protocol_test_send_all(relay->client_descriptor, wire,
                                          wire_length);
        if (wire != body) free(wire);
        free(body);
        if (!sent) {
            relay->success = 0;
            break;
        }
    }
    protocol_test_hmac_free(&handshake_mac);
    return NULL;
}

static int protocol_test_stls_client_hello(const uint8_t *body,
                                           size_t body_length,
                                           const char *password) {
    if (body_length < 71U || body[0] != 1U || body[38] != 32U) return 0;
    uint8_t expected[4];
    ch_error error;
    return ch_shadowtls_signature(password, body, body_length, expected,
                                  &error) == CH_OK &&
           CRYPTO_memcmp(expected, body + 67U, sizeof(expected)) == 0;
}

static int protocol_test_stls_write_data(int descriptor,
                                         protocol_test_hmac *mac,
                                         const uint8_t *payload,
                                         size_t payload_length) {
    size_t body_length = 4U + payload_length;
    if (body_length > 65535U) return 0;
    uint8_t *frame = malloc(9U + payload_length);
    if (frame == NULL) return 0;
    frame[0] = 23U;
    frame[1] = 3U;
    frame[2] = 3U;
    frame[3] = (uint8_t)(body_length >> 8U);
    frame[4] = (uint8_t)body_length;
    int ok = protocol_test_hmac_add(mac, payload, payload_length, frame + 5U,
                                    1);
    if (ok) memcpy(frame + 9U, payload, payload_length);
    if (ok) ok = protocol_test_send_all(descriptor, frame,
                                        9U + payload_length);
    free(frame);
    return ok;
}

static void *protocol_test_stls_server_main(void *opaque) {
    protocol_stls_server *server = opaque;
    int client;
    do {
        client = accept(server->descriptor, NULL, NULL);
    } while (client < 0 && errno == EINTR);
    if (client < 0) return NULL;
    int handshake_pair[2] = {-1, -1};
    int healthy = socketpair(AF_UNIX, SOCK_STREAM, 0, handshake_pair) == 0;
    protocol_stls_tls_worker worker = {
        .descriptor = healthy ? handshake_pair[1] : -1,
        .context = server->context
    };
    pthread_t tls_thread;
    int tls_started = healthy && pthread_create(
        &tls_thread, NULL, protocol_test_stls_tls_main, &worker) == 0;
    protocol_stls_relay relay = {
        .client_descriptor = client,
        .handshake_descriptor = healthy ? handshake_pair[0] : -1,
        .password = server->password
    };
    healthy = tls_started && pthread_mutex_init(&relay.mutex, NULL) == 0;
    pthread_t relay_thread;
    int relay_started = healthy && pthread_create(
        &relay_thread, NULL, protocol_test_stls_relay_main, &relay) == 0;
    healthy = healthy && relay_started;
    int client_hello_valid = 0;
    uint8_t *first_payload = NULL;
    size_t first_payload_length = 0U;
    while (healthy) {
        uint8_t header[5];
        if (!protocol_test_receive_exact(client, header, sizeof(header))) {
            healthy = 0;
            break;
        }
        size_t body_length = ((size_t)header[3] << 8U) | (size_t)header[4];
        uint8_t *body = malloc(body_length == 0U ? 1U : body_length);
        if (body == NULL || !protocol_test_receive_exact(
                client, body, body_length)) {
            free(body);
            healthy = 0;
            break;
        }
        if (header[0] == 22U && body_length > 0U && body[0] == 1U) {
            client_hello_valid = protocol_test_stls_client_hello(
                body, body_length, server->password);
            healthy = healthy && client_hello_valid;
        }
        int random_ready;
        uint8_t random[32];
        (void)pthread_mutex_lock(&relay.mutex);
        random_ready = relay.random_ready;
        memcpy(random, relay.server_random, sizeof(random));
        (void)pthread_mutex_unlock(&relay.mutex);
        int data_frame = 0;
        if (header[0] == 23U && body_length >= 4U && random_ready) {
            protocol_test_hmac trial;
            if (protocol_test_hmac_init(&trial, server->password, random,
                                        "C")) {
                data_frame = protocol_test_hmac_verify(
                    &trial, body + 4U, body_length - 4U, body, 1);
                protocol_test_hmac_free(&trial);
            }
        }
        if (data_frame) {
            first_payload_length = body_length - 4U;
            first_payload = malloc(first_payload_length == 0U ? 1U :
                                   first_payload_length);
            if (first_payload == NULL) {
                healthy = 0;
            } else {
                memcpy(first_payload, body + 4U, first_payload_length);
            }
            free(body);
            break;
        }
        healthy = protocol_test_send_all(handshake_pair[0], header,
                                         sizeof(header)) &&
                  protocol_test_send_all(handshake_pair[0], body,
                                         body_length);
        free(body);
    }
    (void)shutdown(handshake_pair[0], SHUT_RDWR);
    if (relay_started) (void)pthread_join(relay_thread, NULL);
    if (tls_started) (void)pthread_join(tls_thread, NULL);
    if (handshake_pair[0] >= 0) (void)close(handshake_pair[0]);
    if (relay_started) {
        healthy = healthy && relay.success && worker.success;
    }
    uint8_t random[32];
    (void)pthread_mutex_lock(&relay.mutex);
    memcpy(random, relay.server_random, sizeof(random));
    (void)pthread_mutex_unlock(&relay.mutex);
    protocol_test_hmac response_mac;
    memset(&response_mac, 0, sizeof(response_mac));
    healthy = healthy && client_hello_valid && first_payload != NULL &&
        protocol_test_hmac_init(&response_mac, server->password, random, "S") &&
        protocol_test_stls_write_data(client, &response_mac, first_payload,
                                      first_payload_length);
    protocol_test_hmac_free(&response_mac);
    free(first_payload);
    if (relay_started || healthy) (void)pthread_mutex_destroy(&relay.mutex);
    server->success = healthy;
    (void)shutdown(client, SHUT_RDWR);
    (void)close(client);
    return NULL;
}

static int protocol_test_stls_server_start(protocol_stls_server *server) {
    memset(server, 0, sizeof(*server));
    server->descriptor = -1;
    server->password = "shadowtls-native-secret";
    server->context = protocol_test_tls_context();
    if (server->context == NULL || SSL_CTX_set_min_proto_version(
            server->context, TLS1_3_VERSION) != 1 ||
        SSL_CTX_set_max_proto_version(server->context, TLS1_3_VERSION) != 1) {
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
                          protocol_test_stls_server_main, server) == 0;
}

static void protocol_test_stls_server_stop(protocol_stls_server *server) {
    (void)pthread_join(server->thread, NULL);
    if (server->descriptor >= 0) (void)close(server->descriptor);
    SSL_CTX_free(server->context);
}

static void protocol_test_shadowtls_stream(void) {
    protocol_stls_server server;
    CH_TEST_ASSERT(protocol_test_stls_server_start(&server));
    char document[1200];
    (void)snprintf(
        document, sizeof(document),
        "active = \"local\"\n"
        "[[profile]]\nname = \"local\"\n"
        "[[profile.chain]]\nname = \"shadowtls\"\n"
        "[[profile.chain.server]]\n"
        "address = \"127.0.0.1:%u\"\nprotocol = \"shadowtls\"\n"
        "[profile.chain.server.settings]\n"
        "password = \"shadowtls-native-secret\"\nversion = 3\n"
        "sni = \"handshake.invalid\"\nskip_cert_verify = true\n",
        (unsigned int)server.port);
    ch_error error;
    ch_config *config = NULL;
    CH_TEST_ASSERT(ch_config_parse(document, NULL, &config, &error) == CH_OK);
    const ch_config_table *profile = ch_config_active_profile(config);
    const ch_config_array *chains = ch_config_table_get_array(profile, "chain");
    const ch_config_table *chain = ch_config_array_get_table(chains, 0U);
    const ch_config_array *servers = ch_config_table_get_array(chain, "server");
    const ch_config_table *server_table = ch_config_array_get_table(servers, 0U);
    int stream = -1;
    CH_TEST_ASSERT(ch_protocol_shadowtls_dial(
        server_table, -1, &stream, &error) == CH_OK);
    static const char payload[] = "native-shadowtls-v3-echo";
    char echoed[sizeof(payload)];
    CH_TEST_ASSERT(protocol_test_send_all(stream, payload, sizeof(payload)));
    CH_TEST_ASSERT(protocol_test_receive_exact(stream, echoed, sizeof(echoed)));
    CH_TEST_ASSERT(memcmp(payload, echoed, sizeof(payload)) == 0);
    (void)shutdown(stream, SHUT_RDWR);
    (void)close(stream);
    protocol_test_stls_server_stop(&server);
    CH_TEST_ASSERT(server.success == 1);
    ch_config_free(config);
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

static void protocol_test_vmess_vectors(void) {
    ch_error error;
    uint8_t uuid[16];
    static const uint8_t expected_uuid[16] = {
        0xb8U,0x31U,0x38U,0x1dU,0x63U,0x24U,0x4dU,0x53U,
        0xadU,0x4fU,0x8cU,0xdaU,0x48U,0xb3U,0x08U,0x11U
    };
    CH_TEST_ASSERT(ch_vmess_parse_uuid(
        "  b831381d-6324-4d53-ad4f-8cda48b30811\t", uuid,
        &error) == CH_OK);
    CH_TEST_ASSERT(memcmp(uuid, expected_uuid, sizeof(uuid)) == 0);
    CH_TEST_ASSERT(ch_vmess_parse_uuid(
        "b831381d-6324-4d53-ad4f-8cda48b3 0811", uuid,
        &error) == CH_ERROR_INVALID_ARGUMENT);

    static const uint8_t kdf_key[] = "some-command-key";
    static const uint8_t path_a[] = "label-a";
    static const uint8_t path_b[] = "label-b";
    const uint8_t *paths[] = {path_a, path_b};
    const size_t path_lengths[] = {
        sizeof(path_a) - 1U, sizeof(path_b) - 1U
    };
    static const uint8_t expected_kdf[32] = {
        0xa6U,0xd8U,0x8fU,0x82U,0x6bU,0xa2U,0xc4U,0x6dU,
        0x47U,0x6bU,0xb2U,0xd1U,0x03U,0x86U,0xe5U,0x09U,
        0xa8U,0xabU,0xa9U,0x0fU,0x9aU,0x89U,0xa3U,0xb6U,
        0xbfU,0x9aU,0x6dU,0x14U,0x34U,0xe2U,0xb5U,0xc9U
    };
    uint8_t derived[32];
    CH_TEST_ASSERT(ch_vmess_kdf(
        kdf_key, sizeof(kdf_key) - 1U, paths, path_lengths, 2U, derived,
        &error) == CH_OK);
    CH_TEST_ASSERT(memcmp(derived, expected_kdf, sizeof(derived)) == 0);

    static const char *const targets[] = {
        "1.2.3.4:80", "example.com:443", "[::1]:53"
    };
    static const uint8_t ipv4[] = {0x00U,0x50U,0x01U,1U,2U,3U,4U};
    static const uint8_t domain[] = {
        0x01U,0xbbU,0x02U,11U,'e','x','a','m','p','l','e','.','c','o','m'
    };
    static const uint8_t ipv6[] = {
        0x00U,0x35U,0x03U,0U,0U,0U,0U,0U,0U,0U,0U,
        0U,0U,0U,0U,0U,0U,0U,1U
    };
    const uint8_t *expected_addresses[] = {ipv4, domain, ipv6};
    const size_t expected_lengths[] = {
        sizeof(ipv4), sizeof(domain), sizeof(ipv6)
    };
    for (size_t index = 0U; index < 3U; ++index) {
        uint8_t *address = NULL;
        size_t address_length = 0U;
        CH_TEST_ASSERT(ch_vmess_encode_address(
            targets[index], &address, &address_length, &error) == CH_OK);
        CH_TEST_ASSERT(address_length == expected_lengths[index]);
        CH_TEST_ASSERT(memcmp(address, expected_addresses[index],
                              address_length) == 0);
        free(address);
    }
}

static void protocol_test_shadowtls_vectors(void) {
    uint8_t hello[74] = {0x01U,0U,0U,0U,0x03U,0x03U};
    for (size_t index = 0U; index < 32U; ++index) {
        hello[6U + index] = (uint8_t)index;
    }
    hello[38] = 32U;
    hello[71] = 0xaaU;
    hello[72] = 0xbbU;
    hello[73] = 0xccU;
    uint8_t signature[4];
    ch_error error;
    static const uint8_t expected[] = {0x41U,0x63U,0x94U,0x46U};
    CH_TEST_ASSERT(ch_shadowtls_signature(
        "hunter2", hello, sizeof(hello), signature, &error) == CH_OK);
    CH_TEST_ASSERT(memcmp(signature, expected, sizeof(expected)) == 0);
    memcpy(hello + 67U, signature, sizeof(signature));
    CH_TEST_ASSERT(ch_shadowtls_signature(
        "hunter2", hello, sizeof(hello), signature, &error) == CH_OK);
    CH_TEST_ASSERT(memcmp(signature, expected, sizeof(expected)) == 0);

    uint8_t server_hello[52] = {
        0x02U,0U,0U,0U,0x03U,0x03U
    };
    server_hello[38] = 0U;
    server_hello[39] = 0x13U;
    server_hello[40] = 0x01U;
    server_hello[41] = 0U;
    server_hello[42] = 0U;
    server_hello[43] = 6U;
    server_hello[44] = 0U;
    server_hello[45] = 43U;
    server_hello[46] = 0U;
    server_hello[47] = 2U;
    server_hello[48] = 0x03U;
    server_hello[49] = 0x04U;
    CH_TEST_ASSERT(ch_shadowtls_server_hello_tls13(server_hello, 50U));
    server_hello[49] = 0x03U;
    CH_TEST_ASSERT(!ch_shadowtls_server_hello_tls13(server_hello, 50U));

    static const char invalid_chain[] =
        "active = \"local\"\n"
        "[[profile]]\nname = \"local\"\n"
        "[[profile.chain]]\nname = \"invalid\"\n"
        "[[profile.chain.server]]\n"
        "address = \"127.0.0.1:1\"\nprotocol = \"shadowtls\"\n"
        "[profile.chain.server.settings]\n"
        "password = \"secret\"\nsni = \"localhost\"\n"
        "skip_cert_verify = true\n";
    ch_config *config = NULL;
    CH_TEST_ASSERT(ch_config_parse(invalid_chain, NULL, &config,
                                   &error) == CH_OK);
    const ch_config_table *profile = ch_config_active_profile(config);
    const ch_config_array *chains = ch_config_table_get_array(profile, "chain");
    const ch_config_table *chain = ch_config_array_get_table(chains, 0U);
    int stream = -1;
    CH_TEST_ASSERT(ch_protocol_chain_dial(
        chain, "tcp", "example.com:443", &stream,
        &error) == CH_ERROR_INVALID_ARGUMENT);
    ch_config_free(config);
}

static void protocol_test_vmess_frames(void) {
    static const ch_vmess_security methods[] = {
        CH_VMESS_AES_128_GCM, CH_VMESS_CHACHA20_POLY1305
    };
    uint8_t key[16];
    uint8_t iv[16];
    memset(key, 0x42, sizeof(key));
    memset(iv, 0xa5, sizeof(iv));
    static const uint8_t plaintext[] = "native-vmess-body-frame";
    for (size_t index = 0U; index < sizeof(methods) / sizeof(methods[0]);
         ++index) {
        ch_error error;
        uint16_t write_counter = 0U;
        bool write_exhausted = false;
        uint8_t *frame = NULL;
        size_t frame_length = 0U;
        CH_TEST_ASSERT(ch_vmess_encrypt_chunk(
            methods[index], key, iv, &write_counter, &write_exhausted,
            plaintext, sizeof(plaintext), &frame, &frame_length,
            &error) == CH_OK);
        CH_TEST_ASSERT(write_counter == 1U && !write_exhausted);
        uint16_t read_counter = 0U;
        bool read_exhausted = false;
        uint8_t *decrypted = NULL;
        size_t decrypted_length = 0U;
        CH_TEST_ASSERT(ch_vmess_decrypt_chunk(
            methods[index], key, iv, &read_counter, &read_exhausted, frame,
            frame_length, &decrypted, &decrypted_length, &error) == CH_OK);
        CH_TEST_ASSERT(decrypted_length == sizeof(plaintext));
        CH_TEST_ASSERT(memcmp(decrypted, plaintext, sizeof(plaintext)) == 0);
        free(decrypted);
        frame[frame_length - 1U] ^= 0x01U;
        read_counter = 0U;
        CH_TEST_ASSERT(ch_vmess_decrypt_chunk(
            methods[index], key, iv, &read_counter, &read_exhausted, frame,
            frame_length, &decrypted, &decrypted_length,
            &error) == CH_ERROR_PARSE);
        free(frame);

        write_counter = UINT16_MAX;
        write_exhausted = false;
        CH_TEST_ASSERT(ch_vmess_encrypt_chunk(
            methods[index], key, iv, &write_counter, &write_exhausted,
            plaintext, sizeof(plaintext), &frame, &frame_length,
            &error) == CH_OK);
        CH_TEST_ASSERT(write_counter == 0U && write_exhausted);
        free(frame);
        CH_TEST_ASSERT(ch_vmess_encrypt_chunk(
            methods[index], key, iv, &write_counter, &write_exhausted,
            plaintext, sizeof(plaintext), &frame, &frame_length,
            &error) == CH_ERROR_INVALID_STATE);
    }
    ch_error error;
    uint16_t counter = 0U;
    uint8_t *frame = NULL;
    size_t frame_length = 0U;
    CH_TEST_ASSERT(ch_vmess_encrypt_chunk(
        CH_VMESS_AES_128_GCM, key, iv, &counter, NULL, plaintext,
        sizeof(plaintext), &frame, &frame_length,
        &error) == CH_ERROR_INVALID_ARGUMENT);
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
    protocol_test_tor_streams();
    protocol_test_tor_after_trojan();
    protocol_test_vmess_vectors();
    protocol_test_vmess_frames();
    protocol_test_vmess_streams();
    protocol_test_shadowtls_vectors();
    protocol_test_shadowtls_stream();
}
