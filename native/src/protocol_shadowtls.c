// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "protocol_shadowtls.h"

#include <arpa/inet.h>
#include <errno.h>
#include <limits.h>
#include <openssl/core_names.h>
#include <openssl/crypto.h>
#include <openssl/err.h>
#include <openssl/evp.h>
#include <openssl/hmac.h>
#include <openssl/params.h>
#include <openssl/provider.h>
#include <openssl/rand.h>
#include <openssl/ssl.h>
#include <openssl/x509_vfy.h>
#include <poll.h>
#include <pthread.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/socket.h>
#include <unistd.h>

#include "clambhook/protocol.h"
#include "internal.h"

#define CH_STLS_TLS_HEADER_SIZE 5U
#define CH_STLS_RANDOM_SIZE 32U
#define CH_STLS_HMAC_SIZE 4U
#define CH_STLS_FRAME_HEADER_SIZE 9U
#define CH_STLS_MAX_CHUNK 16384U
#define CH_STLS_ENTROPY_SIZE 65536U
#define CH_STLS_NONCE_SIZE 64U
#define CH_STLS_HANDSHAKE_RECORD 22U
#define CH_STLS_APPLICATION_RECORD 23U
#define CH_STLS_ALERT_RECORD 21U
#define CH_STLS_SERVER_HELLO 2U

typedef struct ch_stls_mac {
    EVP_MAC *algorithm;
    EVP_MAC_CTX *context;
} ch_stls_mac;

typedef struct ch_stls_entropy {
    uint8_t *public_bytes;
    uint8_t *private_bytes;
    uint8_t public_nonce[CH_STLS_NONCE_SIZE];
    uint8_t private_nonce[CH_STLS_NONCE_SIZE];
} ch_stls_entropy;

typedef struct ch_stls_tls {
    OSSL_LIB_CTX *library_context;
    OSSL_PROVIDER *provider;
    SSL_CTX *context;
    SSL *ssl;
} ch_stls_tls;

typedef struct ch_stls_config {
    char *password;
    char *sni;
    bool skip_verify;
    uint8_t *alpn;
    size_t alpn_length;
} ch_stls_config;

typedef struct ch_stls_pump {
    int network_descriptor;
    int local_descriptor;
    ch_stls_mac write_mac;
    ch_stls_mac read_mac;
    ch_stls_mac ignore_mac;
    bool has_ignore_mac;
} ch_stls_pump;

static void ch_stls_close(int *descriptor) {
    if (descriptor == NULL || *descriptor < 0) return;
    (void)shutdown(*descriptor, SHUT_RDWR);
    (void)close(*descriptor);
    *descriptor = -1;
}

static ssize_t ch_stls_send(int descriptor, const void *bytes,
                            size_t length) {
#ifdef MSG_NOSIGNAL
    return send(descriptor, bytes, length, MSG_NOSIGNAL);
#else
    return send(descriptor, bytes, length, 0);
#endif
}

static int ch_stls_send_all(int descriptor, const void *bytes,
                            size_t length) {
    const uint8_t *cursor = bytes;
    while (length > 0U) {
        ssize_t written = ch_stls_send(descriptor, cursor, length);
        if (written < 0 && errno == EINTR) continue;
        if (written <= 0) return 0;
        cursor += (size_t)written;
        length -= (size_t)written;
    }
    return 1;
}

static int ch_stls_receive_exact(int descriptor, void *bytes,
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

static char *ch_stls_optional_string(const ch_config_table *table,
                                      const char *key) {
    char *value = NULL;
    ch_error ignored;
    if (table == NULL || ch_config_table_get_string(
            table, key, &value, &ignored) != CH_OK) {
        free(value);
        return ch_strdup("");
    }
    return value;
}

static void ch_stls_mac_free(ch_stls_mac *mac) {
    if (mac == NULL) return;
    EVP_MAC_CTX_free(mac->context);
    EVP_MAC_free(mac->algorithm);
    memset(mac, 0, sizeof(*mac));
}

static ch_status ch_stls_mac_init(ch_stls_mac *mac, const char *password,
                                  const uint8_t server_random[32],
                                  const char *suffix, ch_error *error) {
    memset(mac, 0, sizeof(*mac));
    mac->algorithm = EVP_MAC_fetch(NULL, "HMAC", NULL);
    mac->context = mac->algorithm == NULL ? NULL :
                   EVP_MAC_CTX_new(mac->algorithm);
    char digest[] = "SHA1";
    OSSL_PARAM params[] = {
        OSSL_PARAM_construct_utf8_string(OSSL_MAC_PARAM_DIGEST, digest, 0U),
        OSSL_PARAM_construct_end()
    };
    size_t password_length = strlen(password);
    if (mac->context == NULL || EVP_MAC_init(
            mac->context, (const uint8_t *)password, password_length,
            params) != 1 || EVP_MAC_update(
            mac->context, server_random, CH_STLS_RANDOM_SIZE) != 1 ||
        (suffix != NULL && suffix[0] != '\0' && EVP_MAC_update(
            mac->context, (const uint8_t *)suffix, strlen(suffix)) != 1)) {
        ch_stls_mac_free(mac);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "initialize ShadowTLS HMAC failed");
        return CH_ERROR_INTERNAL;
    }
    return CH_OK;
}

static int ch_stls_mac_sum(const ch_stls_mac *mac, const uint8_t *bytes,
                            size_t length, uint8_t output[20]) {
    EVP_MAC_CTX *copy = mac == NULL || mac->context == NULL ? NULL :
                        EVP_MAC_CTX_dup(mac->context);
    size_t output_length = 0U;
    int ok = copy != NULL &&
        (length == 0U || EVP_MAC_update(copy, bytes, length) == 1) &&
        EVP_MAC_final(copy, output, &output_length, 20U) == 1 &&
        output_length == 20U;
    EVP_MAC_CTX_free(copy);
    return ok;
}

static int ch_stls_mac_verify(ch_stls_mac *mac, const uint8_t *bytes,
                               size_t length, const uint8_t expected[4],
                               bool chain_signature) {
    uint8_t sum[20];
    int valid = ch_stls_mac_sum(mac, bytes, length, sum) &&
                CRYPTO_memcmp(sum, expected, CH_STLS_HMAC_SIZE) == 0;
    if (!valid || EVP_MAC_update(mac->context, bytes, length) != 1) return 0;
    if (chain_signature && EVP_MAC_update(
            mac->context, sum, CH_STLS_HMAC_SIZE) != 1) return 0;
    return 1;
}

static int ch_stls_mac_add(ch_stls_mac *mac, const uint8_t *bytes,
                            size_t length, uint8_t signature[4]) {
    uint8_t sum[20];
    if (!ch_stls_mac_sum(mac, bytes, length, sum) ||
        EVP_MAC_update(mac->context, bytes, length) != 1 ||
        EVP_MAC_update(mac->context, sum, CH_STLS_HMAC_SIZE) != 1) return 0;
    memcpy(signature, sum, CH_STLS_HMAC_SIZE);
    return 1;
}

static ch_status ch_stls_sha256(const uint8_t *first, size_t first_length,
                                const uint8_t *second, size_t second_length,
                                uint8_t output[32], ch_error *error) {
    EVP_MD_CTX *context = EVP_MD_CTX_new();
    unsigned int length = 0U;
    int ok = context != NULL &&
        EVP_DigestInit_ex(context, EVP_sha256(), NULL) == 1 &&
        (first_length == 0U || EVP_DigestUpdate(
            context, first, first_length) == 1) &&
        (second_length == 0U || EVP_DigestUpdate(
            context, second, second_length) == 1) &&
        EVP_DigestFinal_ex(context, output, &length) == 1 && length == 32U;
    EVP_MD_CTX_free(context);
    if (!ok) {
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "derive ShadowTLS SHA-256 value failed");
        return CH_ERROR_INTERNAL;
    }
    return CH_OK;
}

static ch_status ch_stls_expand(const uint8_t seed[32], uint8_t domain,
                                uint8_t *output, size_t output_length,
                                ch_error *error) {
    uint64_t counter = 0U;
    size_t offset = 0U;
    while (offset < output_length) {
        uint8_t input[41];
        memcpy(input, seed, 32U);
        input[32] = domain;
        for (size_t index = 0U; index < 8U; ++index) {
            input[33U + index] = (uint8_t)(counter >> (index * 8U));
        }
        uint8_t digest[32];
        ch_status status = ch_stls_sha256(input, sizeof(input), NULL, 0U,
                                          digest, error);
        if (status != CH_OK) return status;
        size_t amount = output_length - offset;
        if (amount > sizeof(digest)) amount = sizeof(digest);
        memcpy(output + offset, digest, amount);
        offset += amount;
        ++counter;
    }
    return CH_OK;
}

static void ch_stls_entropy_free(ch_stls_entropy *entropy) {
    if (entropy == NULL) return;
    free(entropy->public_bytes);
    free(entropy->private_bytes);
    memset(entropy, 0, sizeof(*entropy));
}

static ch_status ch_stls_entropy_init(ch_stls_entropy *entropy,
                                      const uint8_t seed[32],
                                      ch_error *error) {
    memset(entropy, 0, sizeof(*entropy));
    entropy->public_bytes = malloc(CH_STLS_ENTROPY_SIZE);
    entropy->private_bytes = malloc(CH_STLS_ENTROPY_SIZE);
    if (entropy->public_bytes == NULL || entropy->private_bytes == NULL) {
        ch_stls_entropy_free(entropy);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate ShadowTLS deterministic entropy");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_status status = ch_stls_expand(
        seed, 1U, entropy->public_bytes, CH_STLS_ENTROPY_SIZE, error);
    if (status == CH_OK) status = ch_stls_expand(
        seed, 2U, entropy->private_bytes, CH_STLS_ENTROPY_SIZE, error);
    if (status == CH_OK) status = ch_stls_expand(
        seed, 3U, entropy->public_nonce, sizeof(entropy->public_nonce), error);
    if (status == CH_OK) status = ch_stls_expand(
        seed, 4U, entropy->private_nonce, sizeof(entropy->private_nonce), error);
    if (status != CH_OK) ch_stls_entropy_free(entropy);
    return status;
}

static void ch_stls_tls_free(ch_stls_tls *tls) {
    if (tls == NULL) return;
    SSL_free(tls->ssl);
    SSL_CTX_free(tls->context);
    OSSL_PROVIDER_unload(tls->provider);
    if (tls->library_context != NULL) {
        OPENSSL_thread_stop_ex(tls->library_context);
        OSSL_LIB_CTX_free(tls->library_context);
    }
    memset(tls, 0, sizeof(*tls));
}

static int ch_stls_random_context(OSSL_LIB_CTX *library_context,
                                  EVP_RAND_CTX *random_context,
                                  uint8_t *entropy, size_t entropy_length,
                                  uint8_t *nonce, size_t nonce_length) {
    unsigned int strength = 256U;
    OSSL_PARAM params[] = {
        OSSL_PARAM_construct_uint(OSSL_RAND_PARAM_STRENGTH, &strength),
        OSSL_PARAM_construct_octet_string(OSSL_RAND_PARAM_TEST_ENTROPY,
                                          entropy, entropy_length),
        OSSL_PARAM_construct_octet_string(OSSL_RAND_PARAM_TEST_NONCE,
                                          nonce, nonce_length),
        OSSL_PARAM_construct_end()
    };
    (void)library_context;
    return random_context != NULL &&
        EVP_RAND_uninstantiate(random_context) == 1 &&
        EVP_RAND_instantiate(random_context, strength, 0, NULL, 0U,
                             params) == 1;
}

static ch_status ch_stls_tls_configure(ch_stls_tls *tls,
                                       const ch_stls_entropy *entropy,
                                       const ch_stls_config *config,
                                       ch_error *error) {
    memset(tls, 0, sizeof(*tls));
    tls->library_context = OSSL_LIB_CTX_new();
    tls->provider = tls->library_context == NULL ? NULL :
                    OSSL_PROVIDER_load(tls->library_context, "default");
    int random_ready = tls->provider != NULL && RAND_set_DRBG_type(
        tls->library_context, "TEST-RAND", NULL, NULL, NULL) == 1;
    EVP_RAND_CTX *public_random = random_ready ?
        RAND_get0_public(tls->library_context) : NULL;
    EVP_RAND_CTX *private_random = random_ready ?
        RAND_get0_private(tls->library_context) : NULL;
    random_ready = random_ready && ch_stls_random_context(
        tls->library_context, public_random, entropy->public_bytes,
        CH_STLS_ENTROPY_SIZE, (uint8_t *)entropy->public_nonce,
        sizeof(entropy->public_nonce)) && ch_stls_random_context(
        tls->library_context, private_random, entropy->private_bytes,
        CH_STLS_ENTROPY_SIZE, (uint8_t *)entropy->private_nonce,
        sizeof(entropy->private_nonce));
    tls->context = random_ready ? SSL_CTX_new_ex(
        tls->library_context, NULL, TLS_client_method()) : NULL;
    int ok = tls->context != NULL && SSL_CTX_set_min_proto_version(
        tls->context, TLS1_3_VERSION) == 1 &&
        SSL_CTX_set_max_proto_version(tls->context, TLS1_3_VERSION) == 1 &&
        SSL_CTX_set1_groups_list(tls->context, "X25519") == 1;
    if (ok) {
        (void)SSL_CTX_set_options(tls->context, SSL_OP_NO_TICKET);
        SSL_CTX_set_verify(tls->context, config->skip_verify ? SSL_VERIFY_NONE :
                           SSL_VERIFY_PEER, NULL);
        if (!config->skip_verify) {
            ok = SSL_CTX_set_default_verify_paths(tls->context) == 1;
        }
    }
    tls->ssl = ok ? SSL_new(tls->context) : NULL;
    if (tls->ssl != NULL && !config->skip_verify) {
        X509_VERIFY_PARAM *parameters = SSL_get0_param(tls->ssl);
        uint8_t parsed_ip[16];
        int is_ip = inet_pton(AF_INET, config->sni, parsed_ip) == 1 ||
                    inet_pton(AF_INET6, config->sni, parsed_ip) == 1;
        ok = parameters != NULL && (is_ip ?
            X509_VERIFY_PARAM_set1_ip_asc(parameters, config->sni) == 1 :
            X509_VERIFY_PARAM_set1_host(parameters, config->sni, 0U) == 1);
    }
    if (tls->ssl != NULL && config->sni[0] != '\0') {
        uint8_t parsed_ip[16];
        if (inet_pton(AF_INET, config->sni, parsed_ip) != 1 &&
            inet_pton(AF_INET6, config->sni, parsed_ip) != 1) {
            ok = ok && SSL_set_tlsext_host_name(tls->ssl, config->sni) == 1;
        }
    }
    if (tls->ssl != NULL && config->alpn_length > 0U) {
        ok = ok && config->alpn_length <= (size_t)UINT_MAX &&
            SSL_set_alpn_protos(tls->ssl, config->alpn,
                                (unsigned int)config->alpn_length) == 0;
    }
    BIO *read_bio = tls->ssl == NULL ? NULL : BIO_new(BIO_s_mem());
    BIO *write_bio = tls->ssl == NULL ? NULL : BIO_new(BIO_s_mem());
    if (!ok || tls->ssl == NULL || read_bio == NULL || write_bio == NULL) {
        BIO_free(read_bio);
        BIO_free(write_bio);
        ch_stls_tls_free(tls);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "configure isolated ShadowTLS TLS 1.3 context failed");
        return CH_ERROR_INTERNAL;
    }
    SSL_set_bio(tls->ssl, read_bio, write_bio);
    SSL_set_connect_state(tls->ssl);
    return CH_OK;
}

static int ch_stls_client_hello_session(const uint8_t *client_hello,
                                        size_t client_hello_length,
                                        size_t *out_session_offset,
                                        size_t *out_session_length) {
    const size_t length_offset = 1U + 3U + 2U + CH_STLS_RANDOM_SIZE;
    if (client_hello == NULL || client_hello_length <= length_offset ||
        client_hello[0] != 0x01U) return 0;
    size_t session_length = client_hello[length_offset];
    size_t session_offset = length_offset + 1U;
    if (session_length != 32U || session_offset > client_hello_length ||
        session_length > client_hello_length - session_offset) return 0;
    *out_session_offset = session_offset;
    *out_session_length = session_length;
    return 1;
}

ch_status ch_shadowtls_signature(const char *password,
                                 const uint8_t *client_hello,
                                 size_t client_hello_length,
                                 uint8_t signature[4],
                                 ch_error *error) {
    ch_error_clear(error);
    size_t session_offset = 0U;
    size_t session_length = 0U;
    if (password == NULL || password[0] == '\0' || signature == NULL ||
        !ch_stls_client_hello_session(client_hello, client_hello_length,
                                      &session_offset, &session_length)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "invalid ShadowTLS ClientHello signature input");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    uint8_t *copy = malloc(client_hello_length);
    if (copy == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate ShadowTLS ClientHello signature input");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    memcpy(copy, client_hello, client_hello_length);
    memset(copy + session_offset + session_length - CH_STLS_HMAC_SIZE, 0,
           CH_STLS_HMAC_SIZE);
    unsigned int output_length = 0U;
    uint8_t output[EVP_MAX_MD_SIZE];
    int ok = strlen(password) <= (size_t)INT_MAX && HMAC(
        EVP_sha1(), password, (int)strlen(password), copy,
        client_hello_length, output, &output_length) != NULL &&
        output_length >= CH_STLS_HMAC_SIZE;
    free(copy);
    if (!ok) {
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "compute ShadowTLS ClientHello signature failed");
        return CH_ERROR_INTERNAL;
    }
    memcpy(signature, output, CH_STLS_HMAC_SIZE);
    return CH_OK;
}

bool ch_shadowtls_server_hello_tls13(const uint8_t *hello,
                                     size_t length) {
    const size_t session_length_offset = 1U + 3U + 2U + CH_STLS_RANDOM_SIZE;
    if (hello == NULL || length <= session_length_offset ||
        hello[0] != CH_STLS_SERVER_HELLO) return false;
    size_t offset = session_length_offset;
    size_t session_length = hello[offset++];
    if (session_length > length - offset) return false;
    offset += session_length;
    if (length - offset < 5U) return false;
    offset += 3U;
    size_t extensions_length = ((size_t)hello[offset] << 8U) |
                               (size_t)hello[offset + 1U];
    offset += 2U;
    if (extensions_length > length - offset) return false;
    size_t end = offset + extensions_length;
    while (end - offset >= 4U) {
        uint16_t type = (uint16_t)(((uint16_t)hello[offset] << 8U) |
                                   hello[offset + 1U]);
        size_t extension_length = ((size_t)hello[offset + 2U] << 8U) |
                                  (size_t)hello[offset + 3U];
        offset += 4U;
        if (extension_length > end - offset) return false;
        if (type == 43U) {
            return extension_length == 2U && hello[offset] == 0x03U &&
                   hello[offset + 1U] == 0x04U;
        }
        offset += extension_length;
    }
    return false;
}

static int ch_stls_drain_bio(SSL *ssl, uint8_t **out_bytes,
                              size_t *out_length) {
    BIO *write_bio = SSL_get_wbio(ssl);
    size_t pending = write_bio == NULL ? 0U : (size_t)BIO_ctrl_pending(
        write_bio);
    if (pending == 0U) {
        *out_bytes = NULL;
        *out_length = 0U;
        return 1;
    }
    uint8_t *bytes = malloc(pending);
    if (bytes == NULL) return 0;
    size_t offset = 0U;
    while (offset < pending) {
        size_t amount = pending - offset;
        if (amount > (size_t)INT_MAX) amount = (size_t)INT_MAX;
        int received = BIO_read(write_bio, bytes + offset, (int)amount);
        if (received <= 0) {
            free(bytes);
            return 0;
        }
        offset += (size_t)received;
    }
    *out_bytes = bytes;
    *out_length = pending;
    return 1;
}

static int ch_stls_extract_client_hello(const uint8_t *record,
                                        size_t record_length,
                                        const uint8_t **out_hello,
                                        size_t *out_hello_length) {
    if (record == NULL || record_length < CH_STLS_TLS_HEADER_SIZE ||
        record[0] != CH_STLS_HANDSHAKE_RECORD) return 0;
    size_t payload_length = ((size_t)record[3] << 8U) | (size_t)record[4];
    if (payload_length + CH_STLS_TLS_HEADER_SIZE != record_length ||
        payload_length < 4U || record[5] != 0x01U) return 0;
    size_t hello_length = ((size_t)record[6] << 16U) |
                          ((size_t)record[7] << 8U) | (size_t)record[8];
    if (hello_length + 4U != payload_length) return 0;
    *out_hello = record + CH_STLS_TLS_HEADER_SIZE;
    *out_hello_length = payload_length;
    return 1;
}

static ch_status ch_stls_capture_client_hello(
    ch_stls_tls *tls, uint8_t **out_record, size_t *out_record_length,
    const uint8_t **out_hello, size_t *out_hello_length, ch_error *error) {
    int result = SSL_do_handshake(tls->ssl);
    int ssl_error = SSL_get_error(tls->ssl, result);
    uint8_t *record = NULL;
    size_t record_length = 0U;
    if (result == 1 || ssl_error != SSL_ERROR_WANT_READ ||
        !ch_stls_drain_bio(tls->ssl, &record, &record_length) ||
        !ch_stls_extract_client_hello(record, record_length, out_hello,
                                      out_hello_length)) {
        free(record);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "capture isolated ShadowTLS ClientHello failed");
        return CH_ERROR_INTERNAL;
    }
    *out_record = record;
    *out_record_length = record_length;
    return CH_OK;
}

static size_t ch_stls_find_bytes(const uint8_t *haystack,
                                 size_t haystack_length,
                                 const uint8_t *needle,
                                 size_t needle_length, size_t *out_offset) {
    size_t matches = 0U;
    if (needle_length == 0U || needle_length > haystack_length) return 0U;
    for (size_t offset = 0U; offset <= haystack_length - needle_length;
         ++offset) {
        if (memcmp(haystack + offset, needle, needle_length) == 0) {
            if (matches == 0U) *out_offset = offset;
            ++matches;
        }
    }
    return matches;
}

static ch_status ch_stls_patch_entropy(ch_stls_entropy *entropy,
                                       const uint8_t session_id[32],
                                       const uint8_t signature[4],
                                       ch_error *error) {
    uint8_t *buffers[] = {
        entropy->public_bytes, entropy->private_bytes,
        entropy->public_nonce, entropy->private_nonce
    };
    const size_t lengths[] = {
        CH_STLS_ENTROPY_SIZE, CH_STLS_ENTROPY_SIZE,
        sizeof(entropy->public_nonce), sizeof(entropy->private_nonce)
    };
    size_t total_matches = 0U;
    size_t match_buffer = 0U;
    size_t match_offset = 0U;
    for (size_t index = 0U; index < 4U; ++index) {
        size_t offset = 0U;
        size_t matches = ch_stls_find_bytes(
            buffers[index], lengths[index], session_id, 32U, &offset);
        if (matches > 0U) {
            match_buffer = index;
            match_offset = offset;
        }
        total_matches += matches;
    }
    if (total_matches != 1U) {
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "ShadowTLS session id was not uniquely sourced from isolated entropy");
        return CH_ERROR_INTERNAL;
    }
    memcpy(buffers[match_buffer] + match_offset + 28U, signature, 4U);
    return CH_OK;
}

static ch_status ch_stls_xor_key(const char *password,
                                 const uint8_t server_random[32],
                                 uint8_t output[32], ch_error *error) {
    return ch_stls_sha256((const uint8_t *)password, strlen(password),
                          server_random, CH_STLS_RANDOM_SIZE, output, error);
}

static void ch_stls_xor(uint8_t *bytes, size_t length,
                        const uint8_t key[32]) {
    for (size_t index = 0U; index < length; ++index) {
        bytes[index] ^= key[index % 32U];
    }
}

static int ch_stls_client_hellos_match(const uint8_t *first,
                                       const uint8_t *second,
                                       size_t length,
                                       size_t signature_offset) {
    if (memcmp(first, second, signature_offset) != 0) return 0;
    size_t suffix = signature_offset + CH_STLS_HMAC_SIZE;
    return suffix <= length && memcmp(first + suffix, second + suffix,
                                      length - suffix) == 0;
}

static ch_status ch_stls_handshake(const ch_stls_config *config,
                                   int network_descriptor,
                                   uint8_t server_random[32],
                                   ch_stls_mac *out_ignore_mac,
                                   ch_error *error) {
    uint8_t seed[32];
    if (RAND_bytes(seed, (int)sizeof(seed)) != 1) {
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "generate ShadowTLS handshake seed failed");
        return CH_ERROR_INTERNAL;
    }
    ch_stls_entropy pass_one_entropy;
    ch_status status = ch_stls_entropy_init(&pass_one_entropy, seed, error);
    ch_stls_tls pass_one;
    memset(&pass_one, 0, sizeof(pass_one));
    if (status == CH_OK) status = ch_stls_tls_configure(
        &pass_one, &pass_one_entropy, config, error);
    uint8_t *first_record = NULL;
    size_t first_record_length = 0U;
    const uint8_t *first_hello = NULL;
    size_t first_hello_length = 0U;
    if (status == CH_OK) status = ch_stls_capture_client_hello(
        &pass_one, &first_record, &first_record_length, &first_hello,
        &first_hello_length, error);
    size_t session_offset = 0U;
    size_t session_length = 0U;
    if (status == CH_OK && !ch_stls_client_hello_session(
            first_hello, first_hello_length, &session_offset,
            &session_length)) {
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "locate ShadowTLS ClientHello session id failed");
        status = CH_ERROR_INTERNAL;
    }
    uint8_t signature[4];
    if (status == CH_OK) status = ch_shadowtls_signature(
        config->password, first_hello, first_hello_length, signature, error);
    ch_stls_tls_free(&pass_one);

    ch_stls_entropy pass_two_entropy;
    memset(&pass_two_entropy, 0, sizeof(pass_two_entropy));
    if (status == CH_OK) status = ch_stls_entropy_init(
        &pass_two_entropy, seed, error);
    if (status == CH_OK) status = ch_stls_patch_entropy(
        &pass_two_entropy, first_hello + session_offset, signature, error);
    ch_stls_tls pass_two;
    memset(&pass_two, 0, sizeof(pass_two));
    if (status == CH_OK) status = ch_stls_tls_configure(
        &pass_two, &pass_two_entropy, config, error);
    uint8_t *second_record = NULL;
    size_t second_record_length = 0U;
    const uint8_t *second_hello = NULL;
    size_t second_hello_length = 0U;
    if (status == CH_OK) status = ch_stls_capture_client_hello(
        &pass_two, &second_record, &second_record_length, &second_hello,
        &second_hello_length, error);
    size_t signature_offset = CH_STLS_TLS_HEADER_SIZE + session_offset + 28U;
    if (status == CH_OK && (first_record_length != second_record_length ||
        first_hello_length != second_hello_length ||
        !ch_stls_client_hellos_match(
            first_record, second_record, first_record_length,
            signature_offset) || CRYPTO_memcmp(
            second_hello + session_offset + 28U, signature, 4U) != 0)) {
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "ShadowTLS signed ClientHello replay diverged");
        status = CH_ERROR_INTERNAL;
    }
    if (status == CH_OK && !ch_stls_send_all(
            network_descriptor, second_record, second_record_length)) {
        ch_error_set(error, CH_ERROR_IO,
                     "write ShadowTLS ClientHello failed");
        status = CH_ERROR_IO;
    }
    free(first_record);
    free(second_record);
    ch_stls_entropy_free(&pass_one_entropy);
    if (status != CH_OK) {
        ch_stls_tls_free(&pass_two);
        ch_stls_entropy_free(&pass_two_entropy);
        return status;
    }

    bool has_server_random = false;
    bool server_is_tls13 = false;
    bool authorized = false;
    uint8_t xor_key[32];
    ch_stls_mac handshake_mac;
    memset(&handshake_mac, 0, sizeof(handshake_mac));
    for (;;) {
        int result = SSL_do_handshake(pass_two.ssl);
        uint8_t *outgoing = NULL;
        size_t outgoing_length = 0U;
        if (!ch_stls_drain_bio(pass_two.ssl, &outgoing, &outgoing_length)) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "drain ShadowTLS TLS handshake output failed");
            status = CH_ERROR_OUT_OF_MEMORY;
        } else if (outgoing_length > 0U && !ch_stls_send_all(
                network_descriptor, outgoing, outgoing_length)) {
            ch_error_set(error, CH_ERROR_IO,
                         "write ShadowTLS TLS handshake record failed");
            status = CH_ERROR_IO;
        }
        free(outgoing);
        if (status != CH_OK) break;
        if (result == 1) break;
        int ssl_error = SSL_get_error(pass_two.ssl, result);
        if (ssl_error != SSL_ERROR_WANT_READ) {
            unsigned long detail = ERR_get_error();
            ch_error_set(error, CH_ERROR_IO,
                         "ShadowTLS TLS 1.3 handshake failed: %s",
                         detail == 0UL ? "TLS error" :
                         ERR_reason_error_string(detail));
            status = CH_ERROR_IO;
            break;
        }
        uint8_t header[CH_STLS_TLS_HEADER_SIZE];
        if (!ch_stls_receive_exact(network_descriptor, header,
                                    sizeof(header))) {
            ch_error_set(error, CH_ERROR_IO,
                         "read ShadowTLS TLS handshake record failed");
            status = CH_ERROR_IO;
            break;
        }
        size_t body_length = ((size_t)header[3] << 8U) | (size_t)header[4];
        uint8_t *body = malloc(body_length == 0U ? 1U : body_length);
        bool allocation_failed = body == NULL;
        if (allocation_failed || !ch_stls_receive_exact(
                network_descriptor, body, body_length)) {
            free(body);
            ch_error_set(error, allocation_failed ? CH_ERROR_OUT_OF_MEMORY :
                         CH_ERROR_IO,
                         "read ShadowTLS TLS handshake payload failed");
            status = allocation_failed ? CH_ERROR_OUT_OF_MEMORY : CH_ERROR_IO;
            break;
        }
        if (header[0] == CH_STLS_HANDSHAKE_RECORD &&
            body_length > 1U + 3U + 2U + CH_STLS_RANDOM_SIZE &&
            body[0] == CH_STLS_SERVER_HELLO) {
            memcpy(server_random, body + 1U + 3U + 2U,
                   CH_STLS_RANDOM_SIZE);
            server_is_tls13 = ch_shadowtls_server_hello_tls13(
                body, body_length);
            status = ch_stls_xor_key(config->password, server_random,
                                     xor_key, error);
            if (status == CH_OK) status = ch_stls_mac_init(
                &handshake_mac, config->password, server_random, "", error);
            has_server_random = status == CH_OK;
        }
        if (status == CH_OK && header[0] == CH_STLS_APPLICATION_RECORD &&
            has_server_random) {
            if (body_length <= CH_STLS_HMAC_SIZE || !ch_stls_mac_verify(
                    &handshake_mac, body + CH_STLS_HMAC_SIZE,
                    body_length - CH_STLS_HMAC_SIZE, body, false)) {
                ch_error_set(error, CH_ERROR_PARSE,
                             "authenticate ShadowTLS handshake record failed");
                status = CH_ERROR_PARSE;
            } else {
                memmove(body, body + CH_STLS_HMAC_SIZE,
                        body_length - CH_STLS_HMAC_SIZE);
                body_length -= CH_STLS_HMAC_SIZE;
                ch_stls_xor(body, body_length, xor_key);
                header[3] = (uint8_t)(body_length >> 8U);
                header[4] = (uint8_t)body_length;
                authorized = true;
            }
        }
        BIO *read_bio = SSL_get_rbio(pass_two.ssl);
        int body_int = body_length > (size_t)INT_MAX ? -1 : (int)body_length;
        if (status == CH_OK && (BIO_write(
                read_bio, header, (int)sizeof(header)) != (int)sizeof(header) ||
            body_int < 0 || BIO_write(read_bio, body, body_int) != body_int)) {
            ch_error_set(error, CH_ERROR_IO,
                         "feed ShadowTLS TLS handshake record failed");
            status = CH_ERROR_IO;
        }
        free(body);
        if (status != CH_OK) break;
    }
    if (status == CH_OK && (!has_server_random || !server_is_tls13 ||
        !authorized || SSL_version(pass_two.ssl) != TLS1_3_VERSION ||
        (!config->skip_verify && SSL_get_verify_result(
            pass_two.ssl) != X509_V_OK))) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "ShadowTLS server authentication failed");
        status = CH_ERROR_PARSE;
    }
    if (status == CH_OK) {
        *out_ignore_mac = handshake_mac;
        memset(&handshake_mac, 0, sizeof(handshake_mac));
    }
    ch_stls_mac_free(&handshake_mac);
    ch_stls_tls_free(&pass_two);
    ch_stls_entropy_free(&pass_two_entropy);
    return status;
}

static void ch_stls_config_free(ch_stls_config *config) {
    if (config == NULL) return;
    free(config->password);
    free(config->sni);
    free(config->alpn);
    memset(config, 0, sizeof(*config));
}

static ch_status ch_stls_default_sni(const char *address, char **out_sni,
                                     ch_error *error) {
    const char *start = address;
    const char *end = NULL;
    if (address == NULL || address[0] == '\0') {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "shadowtls: server address is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (address[0] == '[') {
        start = address + 1;
        end = strchr(start, ']');
        if (end == NULL || end[1] != ':' || end[2] == '\0') {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "shadowtls: invalid server address");
            return CH_ERROR_INVALID_ARGUMENT;
        }
    } else {
        end = strrchr(address, ':');
        if (end == NULL || end == address || end[1] == '\0' ||
            strchr(address, ':') != end) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "shadowtls: invalid server address");
            return CH_ERROR_INVALID_ARGUMENT;
        }
    }
    size_t length = (size_t)(end - start);
    char *sni = malloc(length + 1U);
    if (sni == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy ShadowTLS server name");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    memcpy(sni, start, length);
    sni[length] = '\0';
    *out_sni = sni;
    return CH_OK;
}

static ch_status ch_stls_parse_alpn(const ch_config_table *settings,
                                    uint8_t **out_alpn,
                                    size_t *out_alpn_length,
                                    ch_error *error) {
    *out_alpn = NULL;
    *out_alpn_length = 0U;
    const ch_config_array *array = ch_config_table_get_array(settings, "alpn");
    size_t count = ch_config_array_count(array);
    size_t wire_length = 0U;
    for (size_t index = 0U; index < count; ++index) {
        char *value = NULL;
        ch_status status = ch_config_array_get_string(array, index, &value,
                                                      error);
        size_t length = value == NULL ? 0U : strlen(value);
        if (status != CH_OK || length == 0U || length > 255U ||
            wire_length > 65535U - length - 1U) {
            free(value);
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "shadowtls: ALPN entries must contain 1 to 255 bytes");
            return CH_ERROR_INVALID_ARGUMENT;
        }
        wire_length += length + 1U;
        free(value);
    }
    if (wire_length == 0U) return CH_OK;
    uint8_t *wire = malloc(wire_length);
    if (wire == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate ShadowTLS ALPN");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    size_t offset = 0U;
    for (size_t index = 0U; index < count; ++index) {
        char *value = NULL;
        if (ch_config_array_get_string(array, index, &value, error) != CH_OK) {
            free(value);
            free(wire);
            return error->code;
        }
        size_t length = strlen(value);
        wire[offset++] = (uint8_t)length;
        memcpy(wire + offset, value, length);
        offset += length;
        free(value);
    }
    *out_alpn = wire;
    *out_alpn_length = wire_length;
    return CH_OK;
}

static ch_status ch_stls_parse_config(const ch_config_table *server,
                                      ch_stls_config *config,
                                      char **out_server_address,
                                      ch_error *error) {
    memset(config, 0, sizeof(*config));
    *out_server_address = NULL;
    const ch_config_table *settings = ch_config_table_get_table(server,
                                                                "settings");
    config->password = ch_stls_optional_string(settings, "password");
    char *configured_sni = ch_stls_optional_string(settings, "sni");
    char *server_address = ch_stls_optional_string(server, "address");
    if (config->password == NULL || configured_sni == NULL ||
        server_address == NULL) {
        free(configured_sni);
        free(server_address);
        ch_stls_config_free(config);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy ShadowTLS configuration");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (config->password[0] == '\0') {
        free(configured_sni);
        free(server_address);
        ch_stls_config_free(config);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "shadowtls: password is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ch_status status = CH_OK;
    if (settings != NULL && ch_config_table_has(settings, "version")) {
        int64_t integer = 0;
        ch_error ignored;
        if (ch_config_table_get_int(settings, "version", &integer,
                                    &ignored) == CH_OK) {
            if (integer != 3) status = CH_ERROR_UNSUPPORTED;
        } else {
            char *version = NULL;
            if (ch_config_table_get_string(settings, "version", &version,
                                           &ignored) == CH_OK) {
                if (version[0] != '\0' && strcmp(version, "3") != 0) {
                    status = CH_ERROR_UNSUPPORTED;
                }
            } else {
                double real = 0.0;
                if (ch_config_table_get_double(settings, "version", &real,
                                               &ignored) != CH_OK ||
                    real != 3.0) status = CH_ERROR_UNSUPPORTED;
            }
            free(version);
        }
        if (status != CH_OK) {
            ch_error_set(error, CH_ERROR_UNSUPPORTED,
                         "shadowtls: only version 3 is supported");
        }
    }
    if (status == CH_OK && configured_sni[0] != '\0') {
        config->sni = configured_sni;
        configured_sni = NULL;
    } else if (status == CH_OK) {
        status = ch_stls_default_sni(server_address, &config->sni, error);
    }
    free(configured_sni);
    ch_error ignored;
    if (status == CH_OK && settings != NULL) {
        (void)ch_config_table_get_bool(settings, "skip_cert_verify",
                                       &config->skip_verify, &ignored);
        status = ch_stls_parse_alpn(settings, &config->alpn,
                                    &config->alpn_length, error);
    }
    if (status != CH_OK) {
        free(server_address);
        ch_stls_config_free(config);
        return status;
    }
    *out_server_address = server_address;
    return CH_OK;
}

static int ch_stls_write_frame(ch_stls_pump *pump, const uint8_t *payload,
                                size_t payload_length) {
    if (payload_length == 0U || payload_length > CH_STLS_MAX_CHUNK) return 0;
    uint8_t *frame = malloc(CH_STLS_FRAME_HEADER_SIZE + payload_length);
    if (frame == NULL) return 0;
    frame[0] = CH_STLS_APPLICATION_RECORD;
    frame[1] = 0x03U;
    frame[2] = 0x03U;
    size_t wire_length = CH_STLS_HMAC_SIZE + payload_length;
    frame[3] = (uint8_t)(wire_length >> 8U);
    frame[4] = (uint8_t)wire_length;
    int ok = ch_stls_mac_add(&pump->write_mac, payload, payload_length,
                              frame + CH_STLS_TLS_HEADER_SIZE);
    if (ok) memcpy(frame + CH_STLS_FRAME_HEADER_SIZE, payload,
                   payload_length);
    if (ok) ok = ch_stls_send_all(pump->network_descriptor, frame,
                                  CH_STLS_FRAME_HEADER_SIZE + payload_length);
    free(frame);
    return ok;
}

static void *ch_stls_outgoing_main(void *opaque) {
    ch_stls_pump *pump = opaque;
    uint8_t bytes[32768];
    for (;;) {
        ssize_t received = recv(pump->local_descriptor, bytes,
                                sizeof(bytes), 0);
        if (received < 0 && errno == EINTR) continue;
        if (received <= 0) break;
        const uint8_t *cursor = bytes;
        size_t remaining = (size_t)received;
        while (remaining > 0U) {
            size_t amount = remaining > CH_STLS_MAX_CHUNK ?
                            CH_STLS_MAX_CHUNK : remaining;
            if (!ch_stls_write_frame(pump, cursor, amount)) {
                remaining = 0U;
                received = -1;
                break;
            }
            cursor += amount;
            remaining -= amount;
        }
        if (received < 0) break;
    }
    (void)shutdown(pump->network_descriptor, SHUT_RDWR);
    return NULL;
}

static void ch_stls_send_alert(int descriptor) {
    uint8_t alert[31] = {CH_STLS_ALERT_RECORD, 0x03U, 0x03U, 0U, 26U};
    if (RAND_bytes(alert + CH_STLS_TLS_HEADER_SIZE,
                   (int)(sizeof(alert) - CH_STLS_TLS_HEADER_SIZE)) == 1) {
        (void)ch_stls_send_all(descriptor, alert, sizeof(alert));
    }
}

static void *ch_stls_pump_main(void *opaque) {
    ch_stls_pump *pump = opaque;
    pthread_t outgoing;
    int outgoing_started = pthread_create(&outgoing, NULL,
                                           ch_stls_outgoing_main, pump) == 0;
    int healthy = outgoing_started;
    while (healthy) {
        uint8_t header[CH_STLS_TLS_HEADER_SIZE];
        if (!ch_stls_receive_exact(pump->network_descriptor, header,
                                    sizeof(header))) break;
        size_t body_length = ((size_t)header[3] << 8U) | (size_t)header[4];
        uint8_t *body = malloc(body_length == 0U ? 1U : body_length);
        if (body == NULL || !ch_stls_receive_exact(
                pump->network_descriptor, body, body_length)) {
            free(body);
            break;
        }
        if (header[0] == CH_STLS_ALERT_RECORD) {
            free(body);
            break;
        }
        if (header[0] != CH_STLS_APPLICATION_RECORD || header[1] != 0x03U ||
            header[2] != 0x03U || body_length < CH_STLS_HMAC_SIZE) {
            free(body);
            ch_stls_send_alert(pump->network_descriptor);
            break;
        }
        const uint8_t *payload = body + CH_STLS_HMAC_SIZE;
        size_t payload_length = body_length - CH_STLS_HMAC_SIZE;
        if (pump->has_ignore_mac && ch_stls_mac_verify(
                &pump->ignore_mac, payload, payload_length, body, false)) {
            free(body);
            continue;
        }
        if (pump->has_ignore_mac) {
            ch_stls_mac_free(&pump->ignore_mac);
            pump->has_ignore_mac = false;
        }
        if (!ch_stls_mac_verify(&pump->read_mac, payload, payload_length,
                                 body, true) ||
            !ch_stls_send_all(pump->local_descriptor, payload,
                              payload_length)) {
            free(body);
            ch_stls_send_alert(pump->network_descriptor);
            break;
        }
        free(body);
    }
    (void)shutdown(pump->local_descriptor, SHUT_RDWR);
    (void)shutdown(pump->network_descriptor, SHUT_RDWR);
    if (outgoing_started) (void)pthread_join(outgoing, NULL);
    ch_stls_close(&pump->network_descriptor);
    ch_stls_close(&pump->local_descriptor);
    ch_stls_mac_free(&pump->write_mac);
    ch_stls_mac_free(&pump->read_mac);
    ch_stls_mac_free(&pump->ignore_mac);
    free(pump);
    return NULL;
}

ch_status ch_protocol_shadowtls_dial(const ch_config_table *server,
                                     int underlying_descriptor,
                                     int *out_descriptor,
                                     ch_error *error) {
    ch_error_clear(error);
    if (server == NULL || out_descriptor == NULL) {
        ch_stls_close(&underlying_descriptor);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "ShadowTLS server and output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_descriptor = -1;
    ch_stls_config config;
    char *server_address = NULL;
    ch_status status = ch_stls_parse_config(
        server, &config, &server_address, error);
    if (status != CH_OK) {
        ch_stls_close(&underlying_descriptor);
        return status;
    }
    if (underlying_descriptor < 0) {
        status = ch_protocol_connect_tcp(server_address,
                                         &underlying_descriptor, error);
    }
    free(server_address);
    if (status != CH_OK) {
        ch_stls_config_free(&config);
        return status;
    }
    uint8_t server_random[32];
    ch_stls_mac ignore_mac;
    memset(&ignore_mac, 0, sizeof(ignore_mac));
    status = ch_stls_handshake(&config, underlying_descriptor, server_random,
                               &ignore_mac, error);
    if (status != CH_OK) {
        ch_stls_mac_free(&ignore_mac);
        ch_stls_config_free(&config);
        ch_stls_close(&underlying_descriptor);
        return status;
    }
    int pair[2] = {-1, -1};
    if (socketpair(AF_UNIX, SOCK_STREAM, 0, pair) != 0) {
        ch_stls_mac_free(&ignore_mac);
        ch_stls_config_free(&config);
        ch_stls_close(&underlying_descriptor);
        ch_error_set(error, CH_ERROR_IO,
                     "create ShadowTLS relay stream failed: %s",
                     strerror(errno));
        return CH_ERROR_IO;
    }
    ch_stls_pump *pump = calloc(1U, sizeof(*pump));
    if (pump == NULL) {
        ch_stls_mac_free(&ignore_mac);
        ch_stls_config_free(&config);
        ch_stls_close(&underlying_descriptor);
        ch_stls_close(&pair[0]);
        ch_stls_close(&pair[1]);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate ShadowTLS relay stream");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    pump->network_descriptor = underlying_descriptor;
    pump->local_descriptor = pair[1];
    pump->ignore_mac = ignore_mac;
    pump->has_ignore_mac = true;
    memset(&ignore_mac, 0, sizeof(ignore_mac));
    status = ch_stls_mac_init(&pump->write_mac, config.password,
                              server_random, "C", error);
    if (status == CH_OK) status = ch_stls_mac_init(
        &pump->read_mac, config.password, server_random, "S", error);
    ch_stls_config_free(&config);
    pthread_attr_t attributes;
    int initialized = status == CH_OK && pthread_attr_init(&attributes) == 0;
    if (initialized) {
        (void)pthread_attr_setdetachstate(&attributes, PTHREAD_CREATE_DETACHED);
    }
    pthread_t thread;
    int started = initialized && pthread_create(
        &thread, &attributes, ch_stls_pump_main, pump) == 0;
    if (initialized) (void)pthread_attr_destroy(&attributes);
    if (!started) {
        ch_stls_mac_free(&pump->write_mac);
        ch_stls_mac_free(&pump->read_mac);
        ch_stls_mac_free(&pump->ignore_mac);
        free(pump);
        ch_stls_close(&underlying_descriptor);
        ch_stls_close(&pair[0]);
        ch_stls_close(&pair[1]);
        if (status == CH_OK) {
            ch_error_set(error, CH_ERROR_IO,
                         "start ShadowTLS relay stream failed");
            status = CH_ERROR_IO;
        }
        return status;
    }
    *out_descriptor = pair[0];
    return CH_OK;
}
