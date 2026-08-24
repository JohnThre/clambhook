#include "protocol_shadowsocks.h"

#include <errno.h>
#include <limits.h>
#include <openssl/evp.h>
#include <openssl/hmac.h>
#include <openssl/rand.h>
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

#include "clambhook/protocol.h"
#include "clambhook/socks.h"
#include "cnet.h"
#include "internal.h"

#define CH_SS_MAX_CHUNK 0x3fffU
#define CH_SS_TAG_SIZE 16U
#define CH_SS_NONCE_SIZE 12U
#define CH_SS_LENGTH_FRAME_SIZE (2U + CH_SS_TAG_SIZE)

typedef struct ch_ss_pump {
    ch_ss_cipher cipher;
    uint8_t master_key[32];
    uint8_t write_subkey[32];
    uint8_t write_nonce[CH_SS_NONCE_SIZE];
    int network_descriptor;
    int local_descriptor;
} ch_ss_pump;

static void ch_ss_close(int *descriptor) {
    if (descriptor == NULL || *descriptor < 0) return;
    (void)shutdown(*descriptor, SHUT_RDWR);
    (void)close(*descriptor);
    *descriptor = -1;
}

static ssize_t ch_ss_send(int descriptor, const void *bytes, size_t length) {
#ifdef MSG_NOSIGNAL
    return send(descriptor, bytes, length, MSG_NOSIGNAL);
#else
    return send(descriptor, bytes, length, 0);
#endif
}

static int ch_ss_send_all(int descriptor, const void *bytes, size_t length) {
    const uint8_t *cursor = bytes;
    while (length > 0U) {
        ssize_t written = ch_ss_send(descriptor, cursor, length);
        if (written < 0 && errno == EINTR) continue;
        if (written <= 0) return 0;
        cursor += (size_t)written;
        length -= (size_t)written;
    }
    return 1;
}

ch_status ch_ss_cipher_from_name(const char *name, ch_ss_cipher *out_cipher,
                                 ch_error *error) {
    ch_error_clear(error);
    if (name == NULL || out_cipher == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "shadowsocks method and output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    memset(out_cipher, 0, sizeof(*out_cipher));
    if (strcmp(name, "aes-128-gcm") == 0) {
        out_cipher->method = CH_SS_AES_128_GCM;
        out_cipher->key_size = 16U;
        out_cipher->salt_size = 16U;
        return CH_OK;
    }
    if (strcmp(name, "aes-256-gcm") == 0) {
        if (!cnet_aes256gcm_available()) {
            ch_error_set(error, CH_ERROR_UNSUPPORTED,
                         "shadowsocks: aes-256-gcm requires hardware AES; "
                         "use chacha20-ietf-poly1305 instead");
            return CH_ERROR_UNSUPPORTED;
        }
        out_cipher->method = CH_SS_AES_256_GCM;
        out_cipher->key_size = 32U;
        out_cipher->salt_size = 32U;
        return CH_OK;
    }
    if (strcmp(name, "chacha20-ietf-poly1305") == 0) {
        out_cipher->method = CH_SS_CHACHA20_IETF_POLY1305;
        out_cipher->key_size = 32U;
        out_cipher->salt_size = 32U;
        return CH_OK;
    }
    static const char *const legacy[] = {
        "rc4-md5", "aes-128-cfb", "aes-192-cfb", "aes-256-cfb",
        "aes-128-ctr", "aes-192-ctr", "aes-256-ctr", "chacha20",
        "chacha20-ietf", "salsa20", "camellia-128-cfb",
        "camellia-192-cfb", "camellia-256-cfb", "bf-cfb"
    };
    for (size_t index = 0U; index < sizeof(legacy) / sizeof(legacy[0]);
         ++index) {
        if (strcmp(name, legacy[index]) == 0) {
            ch_error_set(error, CH_ERROR_UNSUPPORTED,
                         "shadowsocks: legacy stream cipher %s is insecure "
                         "and not supported", name);
            return CH_ERROR_UNSUPPORTED;
        }
    }
    ch_error_set(error, CH_ERROR_UNSUPPORTED,
                 "shadowsocks: unknown method %s", name);
    return CH_ERROR_UNSUPPORTED;
}

ch_status ch_ss_evp_bytes_to_key(const uint8_t *password,
                                 size_t password_length, size_t key_length,
                                 uint8_t *out_key, ch_error *error) {
    ch_error_clear(error);
    if ((password == NULL && password_length > 0U) || out_key == NULL ||
        key_length == 0U || key_length > 32U) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "invalid Shadowsocks key derivation input");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    uint8_t previous[EVP_MAX_MD_SIZE];
    unsigned int previous_length = 0U;
    size_t produced = 0U;
    while (produced < key_length) {
        EVP_MD_CTX *context = EVP_MD_CTX_new();
        unsigned int digest_length = 0U;
        uint8_t digest[EVP_MAX_MD_SIZE];
        int ok = context != NULL &&
            EVP_DigestInit_ex(context, EVP_md5(), NULL) == 1 &&
            (previous_length == 0U || EVP_DigestUpdate(
                context, previous, previous_length) == 1) &&
            (password_length == 0U || EVP_DigestUpdate(
                context, password, password_length) == 1) &&
            EVP_DigestFinal_ex(context, digest, &digest_length) == 1;
        EVP_MD_CTX_free(context);
        if (!ok || digest_length != 16U) {
            ch_error_set(error, CH_ERROR_INTERNAL,
                         "shadowsocks MD5 key derivation failed");
            return CH_ERROR_INTERNAL;
        }
        memcpy(previous, digest, digest_length);
        previous_length = digest_length;
        size_t remaining = key_length - produced;
        size_t copy = remaining < (size_t)digest_length ? remaining :
                      (size_t)digest_length;
        memcpy(out_key + produced, digest, copy);
        produced += copy;
    }
    return CH_OK;
}

static ch_status ch_ss_hmac_sha1(const uint8_t *key, size_t key_length,
                                 const uint8_t *data, size_t data_length,
                                 uint8_t output[20], ch_error *error) {
    if (key_length > (size_t)INT_MAX) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "Shadowsocks HMAC key is too long");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    unsigned int length = 0U;
    if (HMAC(EVP_sha1(), key, (int)key_length, data, data_length,
             output, &length) == NULL || length != 20U) {
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "Shadowsocks HKDF-SHA1 failed");
        return CH_ERROR_INTERNAL;
    }
    return CH_OK;
}

ch_status ch_ss_hkdf_sha1(const uint8_t *secret, size_t secret_length,
                          const uint8_t *salt, size_t salt_length,
                          const uint8_t *info, size_t info_length,
                          uint8_t *out_key, size_t key_length,
                          ch_error *error) {
    ch_error_clear(error);
    if ((secret == NULL && secret_length > 0U) ||
        (salt == NULL && salt_length > 0U) ||
        (info == NULL && info_length > 0U) || out_key == NULL ||
        key_length == 0U || key_length > 255U * 20U ||
        info_length > 128U) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "invalid Shadowsocks HKDF input");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    uint8_t prk[20];
    ch_status status = ch_ss_hmac_sha1(salt, salt_length, secret,
                                       secret_length, prk, error);
    if (status != CH_OK) return status;
    uint8_t previous[20];
    size_t previous_length = 0U;
    size_t produced = 0U;
    uint8_t counter = 1U;
    while (produced < key_length) {
        uint8_t input[20U + 128U + 1U];
        if (previous_length > 0U) memcpy(input, previous, previous_length);
        if (info_length > 0U) {
            memcpy(input + previous_length, info, info_length);
        }
        input[previous_length + info_length] = counter;
        status = ch_ss_hmac_sha1(prk, sizeof(prk), input,
                                 previous_length + info_length + 1U,
                                 previous, error);
        if (status != CH_OK) return status;
        previous_length = sizeof(previous);
        size_t remaining = key_length - produced;
        size_t copy = remaining < sizeof(previous) ? remaining :
                      sizeof(previous);
        memcpy(out_key + produced, previous, copy);
        produced += copy;
        ++counter;
    }
    return CH_OK;
}

void ch_ss_nonce_increment(uint8_t nonce[12]) {
    if (nonce == NULL) return;
    for (size_t index = 0U; index < CH_SS_NONCE_SIZE; ++index) {
        ++nonce[index];
        if (nonce[index] != 0U) return;
    }
}

static int ch_ss_encrypt(const ch_ss_cipher *cipher, const uint8_t *key,
                         const uint8_t *nonce, const uint8_t *plaintext,
                         size_t plaintext_length, uint8_t *ciphertext,
                         uint8_t tag[CH_SS_TAG_SIZE]) {
    switch (cipher->method) {
        case CH_SS_AES_128_GCM:
            return cnet_aes128gcm_encrypt(key, nonce, plaintext,
                                          plaintext_length, NULL, 0U,
                                          ciphertext, tag);
        case CH_SS_AES_256_GCM:
            return cnet_aes256gcm_encrypt(key, nonce, plaintext,
                                          plaintext_length, NULL, 0U,
                                          ciphertext, tag);
        case CH_SS_CHACHA20_IETF_POLY1305:
            return cnet_chacha20poly1305_encrypt(key, nonce, plaintext,
                                                 plaintext_length, NULL, 0U,
                                                 ciphertext, tag);
    }
    return CNET_ERR_INIT;
}

static int ch_ss_decrypt(const ch_ss_cipher *cipher, const uint8_t *key,
                         const uint8_t *nonce, const uint8_t *ciphertext,
                         size_t ciphertext_length,
                         const uint8_t tag[CH_SS_TAG_SIZE],
                         uint8_t *plaintext) {
    switch (cipher->method) {
        case CH_SS_AES_128_GCM:
            return cnet_aes128gcm_decrypt(key, nonce, ciphertext,
                                          ciphertext_length, NULL, 0U, tag,
                                          plaintext);
        case CH_SS_AES_256_GCM:
            return cnet_aes256gcm_decrypt(key, nonce, ciphertext,
                                          ciphertext_length, NULL, 0U, tag,
                                          plaintext);
        case CH_SS_CHACHA20_IETF_POLY1305:
            return cnet_chacha20poly1305_decrypt(
                key, nonce, ciphertext, ciphertext_length, NULL, 0U, tag,
                plaintext);
    }
    return CNET_ERR_INIT;
}

ch_status ch_ss_encrypt_datagram(const ch_ss_cipher *cipher,
                                 const uint8_t *master_key,
                                 const char *target,
                                 const uint8_t *payload,
                                 size_t payload_length,
                                 uint8_t **out_frame,
                                 size_t *out_frame_length,
                                 ch_error *error) {
    ch_error_clear(error);
    if (cipher == NULL || master_key == NULL || target == NULL ||
        (payload == NULL && payload_length > 0U) || out_frame == NULL ||
        out_frame_length == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "invalid Shadowsocks UDP datagram input");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_frame = NULL;
    *out_frame_length = 0U;
    uint8_t *address = NULL;
    size_t address_length = 0U;
    ch_status status = ch_socks_encode_address(
        target, &address, &address_length, error);
    if (status != CH_OK) return status;
    if (cipher->salt_size > 65535U || address_length >
        65535U - cipher->salt_size || CH_SS_TAG_SIZE >
        65535U - cipher->salt_size - address_length || payload_length >
        65535U - cipher->salt_size - address_length - CH_SS_TAG_SIZE) {
        free(address);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "Shadowsocks UDP datagram is too large");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    size_t plaintext_length = address_length + payload_length;
    size_t frame_length = cipher->salt_size + plaintext_length +
                          CH_SS_TAG_SIZE;
    uint8_t *frame = malloc(frame_length);
    uint8_t *plaintext = malloc(plaintext_length == 0U ? 1U :
                                plaintext_length);
    if (frame == NULL || plaintext == NULL) {
        free(frame); free(plaintext); free(address);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate Shadowsocks UDP datagram");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    memcpy(plaintext, address, address_length);
    if (payload_length > 0U) memcpy(plaintext + address_length, payload,
                                    payload_length);
    free(address);
    uint8_t subkey[32];
    static const uint8_t info[] = "ss-subkey";
    if (RAND_bytes(frame, (int)cipher->salt_size) != 1) {
        status = CH_ERROR_INTERNAL;
        ch_error_set(error, status,
                     "generate Shadowsocks UDP salt failed");
    } else {
        status = ch_ss_hkdf_sha1(
            master_key, cipher->key_size, frame, cipher->salt_size, info,
            sizeof(info) - 1U, subkey, cipher->key_size, error);
    }
    uint8_t nonce[CH_SS_NONCE_SIZE] = {0};
    if (status == CH_OK && ch_ss_encrypt(
            cipher, subkey, nonce, plaintext, plaintext_length,
            frame + cipher->salt_size,
            frame + cipher->salt_size + plaintext_length) != CNET_OK) {
        status = CH_ERROR_INTERNAL;
        ch_error_set(error, status,
                     "encrypt Shadowsocks UDP datagram failed");
    }
    free(plaintext);
    if (status != CH_OK) {
        free(frame);
        return status;
    }
    *out_frame = frame;
    *out_frame_length = frame_length;
    return CH_OK;
}

ch_status ch_ss_decrypt_datagram(const ch_ss_cipher *cipher,
                                 const uint8_t *master_key,
                                 const uint8_t *frame,
                                 size_t frame_length,
                                 char **out_source,
                                 uint8_t **out_payload,
                                 size_t *out_payload_length,
                                 ch_error *error) {
    ch_error_clear(error);
    if (cipher == NULL || master_key == NULL || frame == NULL ||
        out_source == NULL || out_payload == NULL ||
        out_payload_length == NULL || frame_length <
        cipher->salt_size + CH_SS_TAG_SIZE + 1U) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "invalid encrypted Shadowsocks UDP datagram");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_source = NULL;
    *out_payload = NULL;
    *out_payload_length = 0U;
    size_t plaintext_length = frame_length - cipher->salt_size -
                              CH_SS_TAG_SIZE;
    uint8_t *plaintext = malloc(plaintext_length);
    if (plaintext == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate Shadowsocks UDP plaintext");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    uint8_t subkey[32];
    static const uint8_t info[] = "ss-subkey";
    ch_status status = ch_ss_hkdf_sha1(
        master_key, cipher->key_size, frame, cipher->salt_size, info,
        sizeof(info) - 1U, subkey, cipher->key_size, error);
    uint8_t nonce[CH_SS_NONCE_SIZE] = {0};
    if (status != CH_OK || ch_ss_decrypt(
            cipher, subkey, nonce, frame + cipher->salt_size,
            plaintext_length, frame + frame_length - CH_SS_TAG_SIZE,
            plaintext) != CNET_OK) {
        free(plaintext);
        ch_error_set(error, CH_ERROR_PARSE,
                     "authenticate Shadowsocks UDP datagram failed");
        return CH_ERROR_PARSE;
    }
    char *host = NULL;
    uint16_t port = 0U;
    size_t consumed = 0U;
    status = ch_socks_decode_address(plaintext, plaintext_length, &host,
                                     &port, &consumed, error);
    if (status != CH_OK || consumed > plaintext_length) {
        if (status == CH_OK) {
            ch_error_set(error, CH_ERROR_PARSE,
                         "invalid Shadowsocks UDP source address");
        }
        free(host); free(plaintext);
        return status == CH_OK ? CH_ERROR_PARSE : status;
    }
    int ipv6 = strchr(host, ':') != NULL;
    size_t source_length = strlen(host) + 10U;
    char *source = malloc(source_length);
    size_t payload_length = plaintext_length - consumed;
    uint8_t *payload_copy = malloc(payload_length == 0U ? 1U :
                                   payload_length);
    if (source == NULL || payload_copy == NULL) {
        free(source); free(payload_copy); free(host); free(plaintext);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy Shadowsocks UDP result");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    (void)snprintf(source, source_length, ipv6 ? "[%s]:%u" : "%s:%u",
                   host, (unsigned int)port);
    if (payload_length > 0U) memcpy(payload_copy, plaintext + consumed,
                                    payload_length);
    free(host); free(plaintext);
    *out_source = source;
    *out_payload = payload_copy;
    *out_payload_length = payload_length;
    return CH_OK;
}

ch_status ch_ss_decrypt_length(const ch_ss_cipher *cipher,
                               const uint8_t *subkey, uint8_t nonce[12],
                               const uint8_t length_frame[18],
                               size_t *out_plaintext_length,
                               ch_error *error) {
    ch_error_clear(error);
    if (cipher == NULL || subkey == NULL || nonce == NULL ||
        length_frame == NULL || out_plaintext_length == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "invalid Shadowsocks length frame");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    uint8_t length_bytes[2];
    if (ch_ss_decrypt(cipher, subkey, nonce, length_frame, 2U,
                      length_frame + 2U, length_bytes) != CNET_OK) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "authenticate Shadowsocks length failed");
        return CH_ERROR_PARSE;
    }
    ch_ss_nonce_increment(nonce);
    size_t plaintext_length = ((size_t)length_bytes[0] << 8U) |
                              (size_t)length_bytes[1];
    if (plaintext_length == 0U || plaintext_length > CH_SS_MAX_CHUNK) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "invalid Shadowsocks chunk length");
        return CH_ERROR_PARSE;
    }
    *out_plaintext_length = plaintext_length;
    return CH_OK;
}

ch_status ch_ss_decrypt_payload(const ch_ss_cipher *cipher,
                                const uint8_t *subkey, uint8_t nonce[12],
                                const uint8_t *payload_frame,
                                size_t plaintext_length,
                                uint8_t *out_plaintext,
                                ch_error *error) {
    ch_error_clear(error);
    if (cipher == NULL || subkey == NULL || nonce == NULL ||
        payload_frame == NULL || plaintext_length == 0U ||
        plaintext_length > CH_SS_MAX_CHUNK || out_plaintext == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "invalid Shadowsocks payload frame");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (ch_ss_decrypt(cipher, subkey, nonce, payload_frame,
                      plaintext_length, payload_frame + plaintext_length,
                      out_plaintext) != CNET_OK) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "authenticate Shadowsocks payload failed");
        return CH_ERROR_PARSE;
    }
    ch_ss_nonce_increment(nonce);
    return CH_OK;
}

ch_status ch_ss_encrypt_chunk(const ch_ss_cipher *cipher,
                              const uint8_t *subkey, uint8_t nonce[12],
                              const uint8_t *plaintext,
                              size_t plaintext_length, uint8_t **out_frame,
                              size_t *out_frame_length, ch_error *error) {
    ch_error_clear(error);
    if (cipher == NULL || subkey == NULL || nonce == NULL ||
        plaintext == NULL || plaintext_length == 0U ||
        plaintext_length > CH_SS_MAX_CHUNK || out_frame == NULL ||
        out_frame_length == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "invalid Shadowsocks chunk");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_frame = NULL;
    *out_frame_length = 0U;
    size_t frame_length = CH_SS_LENGTH_FRAME_SIZE + plaintext_length +
                          CH_SS_TAG_SIZE;
    uint8_t *frame = malloc(frame_length);
    if (frame == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate Shadowsocks frame");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    uint8_t length_bytes[2] = {
        (uint8_t)(plaintext_length >> 8U),
        (uint8_t)(plaintext_length & 0xffU)
    };
    int encrypted = ch_ss_encrypt(cipher, subkey, nonce, length_bytes,
                                  sizeof(length_bytes), frame, frame + 2U);
    if (encrypted == CNET_OK) ch_ss_nonce_increment(nonce);
    if (encrypted == CNET_OK) {
        encrypted = ch_ss_encrypt(cipher, subkey, nonce, plaintext,
                                  plaintext_length,
                                  frame + CH_SS_LENGTH_FRAME_SIZE,
                                  frame + CH_SS_LENGTH_FRAME_SIZE +
                                      plaintext_length);
    }
    if (encrypted == CNET_OK) ch_ss_nonce_increment(nonce);
    if (encrypted != CNET_OK) {
        free(frame);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "encrypt Shadowsocks chunk failed");
        return CH_ERROR_INTERNAL;
    }
    *out_frame = frame;
    *out_frame_length = frame_length;
    return CH_OK;
}

ch_status ch_ss_decrypt_chunk(const ch_ss_cipher *cipher,
                              const uint8_t *subkey, uint8_t nonce[12],
                              const uint8_t length_frame[18],
                              const uint8_t *payload_frame,
                              size_t payload_frame_length,
                              uint8_t **out_plaintext,
                              size_t *out_plaintext_length,
                              ch_error *error) {
    ch_error_clear(error);
    if (cipher == NULL || subkey == NULL || nonce == NULL ||
        length_frame == NULL || payload_frame == NULL ||
        out_plaintext == NULL || out_plaintext_length == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "invalid encrypted Shadowsocks chunk");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_plaintext = NULL;
    *out_plaintext_length = 0U;
    size_t plaintext_length = 0U;
    ch_status status = ch_ss_decrypt_length(cipher, subkey, nonce,
                                            length_frame, &plaintext_length,
                                            error);
    if (status != CH_OK) return status;
    if (payload_frame_length != plaintext_length + CH_SS_TAG_SIZE) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "invalid Shadowsocks chunk length");
        return CH_ERROR_PARSE;
    }
    uint8_t *plaintext = malloc(plaintext_length);
    if (plaintext == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate Shadowsocks plaintext");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    status = ch_ss_decrypt_payload(cipher, subkey, nonce, payload_frame,
                                   plaintext_length, plaintext, error);
    if (status != CH_OK) {
        free(plaintext);
        return status;
    }
    *out_plaintext = plaintext;
    *out_plaintext_length = plaintext_length;
    return CH_OK;
}

static ch_status ch_ss_write_chunks(int descriptor, const ch_ss_cipher *cipher,
                                    const uint8_t *subkey, uint8_t nonce[12],
                                    const uint8_t *plaintext,
                                    size_t plaintext_length,
                                    ch_error *error) {
    while (plaintext_length > 0U) {
        size_t chunk_length = plaintext_length > CH_SS_MAX_CHUNK ?
                              CH_SS_MAX_CHUNK : plaintext_length;
        uint8_t *frame = NULL;
        size_t frame_length = 0U;
        ch_status status = ch_ss_encrypt_chunk(
            cipher, subkey, nonce, plaintext, chunk_length, &frame,
            &frame_length, error);
        if (status != CH_OK) return status;
        int written = ch_ss_send_all(descriptor, frame, frame_length);
        free(frame);
        if (!written) {
            ch_error_set(error, CH_ERROR_IO,
                         "write Shadowsocks frame failed: %s",
                         strerror(errno));
            return CH_ERROR_IO;
        }
        plaintext += chunk_length;
        plaintext_length -= chunk_length;
    }
    return CH_OK;
}

static int ch_ss_read_exact(ch_ss_pump *pump, void *bytes, size_t length) {
    uint8_t *cursor = bytes;
    while (length > 0U) {
        struct pollfd waits[2] = {
            {.fd = pump->network_descriptor, .events = POLLIN},
            {.fd = pump->local_descriptor, .events = 0}
        };
        int ready;
        do {
            ready = poll(waits, 2U, -1);
        } while (ready < 0 && errno == EINTR);
        if (ready < 0 || (waits[1].revents & (POLLHUP | POLLERR | POLLNVAL)) != 0) {
            return 0;
        }
        if ((waits[0].revents & (POLLIN | POLLHUP | POLLERR)) == 0) continue;
        ssize_t received = recv(pump->network_descriptor, cursor, length, 0);
        if (received < 0 && errno == EINTR) continue;
        if (received <= 0) return 0;
        cursor += (size_t)received;
        length -= (size_t)received;
    }
    return 1;
}

static void *ch_ss_outgoing_main(void *opaque) {
    ch_ss_pump *pump = opaque;
    uint8_t plaintext[32768];
    for (;;) {
        struct pollfd wait = {.fd = pump->local_descriptor, .events = POLLIN};
        int ready;
        do {
            ready = poll(&wait, 1U, -1);
        } while (ready < 0 && errno == EINTR);
        if (ready < 0 || (wait.revents & POLLNVAL) != 0) break;
        ssize_t received = recv(pump->local_descriptor, plaintext,
                                sizeof(plaintext), 0);
        if (received < 0 && errno == EINTR) continue;
        if (received <= 0) {
            if ((wait.revents & (POLLHUP | POLLERR)) != 0) {
                (void)shutdown(pump->network_descriptor, SHUT_RDWR);
            } else {
                (void)shutdown(pump->network_descriptor, SHUT_WR);
            }
            break;
        }
        ch_error error;
        if (ch_ss_write_chunks(pump->network_descriptor, &pump->cipher,
                               pump->write_subkey, pump->write_nonce,
                               plaintext, (size_t)received, &error) != CH_OK) {
            (void)shutdown(pump->network_descriptor, SHUT_RDWR);
            break;
        }
    }
    return NULL;
}

static void *ch_ss_pump_main(void *opaque) {
    ch_ss_pump *pump = opaque;
    pthread_t outgoing;
    int outgoing_started = pthread_create(&outgoing, NULL,
                                           ch_ss_outgoing_main, pump) == 0;
    uint8_t salt[32];
    uint8_t read_subkey[32];
    uint8_t read_nonce[CH_SS_NONCE_SIZE] = {0};
    static const uint8_t info[] = "ss-subkey";
    ch_error error;
    int healthy = outgoing_started && ch_ss_read_exact(
        pump, salt, pump->cipher.salt_size) &&
        ch_ss_hkdf_sha1(pump->master_key, pump->cipher.key_size, salt,
                        pump->cipher.salt_size, info, sizeof(info) - 1U,
                        read_subkey, pump->cipher.key_size, &error) == CH_OK;
    while (healthy) {
        uint8_t length_frame[CH_SS_LENGTH_FRAME_SIZE];
        if (!ch_ss_read_exact(pump, length_frame, sizeof(length_frame))) break;
        size_t plaintext_length = 0U;
        if (ch_ss_decrypt_length(&pump->cipher, read_subkey, read_nonce,
                                 length_frame, &plaintext_length,
                                 &error) != CH_OK) break;
        uint8_t payload_frame[CH_SS_MAX_CHUNK + CH_SS_TAG_SIZE];
        if (!ch_ss_read_exact(pump, payload_frame,
                              plaintext_length + CH_SS_TAG_SIZE)) break;
        uint8_t plaintext[CH_SS_MAX_CHUNK];
        if (ch_ss_decrypt_payload(&pump->cipher, read_subkey, read_nonce,
                                  payload_frame, plaintext_length, plaintext,
                                  &error) != CH_OK) break;
        if (!ch_ss_send_all(pump->local_descriptor, plaintext,
                            plaintext_length)) break;
    }
    (void)shutdown(pump->local_descriptor, SHUT_RDWR);
    (void)shutdown(pump->network_descriptor, SHUT_RDWR);
    if (outgoing_started) (void)pthread_join(outgoing, NULL);
    ch_ss_close(&pump->network_descriptor);
    ch_ss_close(&pump->local_descriptor);
    free(pump);
    return NULL;
}

static char *ch_ss_optional_string(const ch_config_table *table,
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

ch_status ch_protocol_shadowsocks_dial(const ch_config_table *server,
                                       int underlying_descriptor,
                                       const char *target,
                                       int *out_descriptor,
                                       ch_error *error) {
    const ch_config_table *settings = ch_config_table_get_table(server,
                                                                "settings");
    char *method = ch_ss_optional_string(settings, "method");
    char *password = ch_ss_optional_string(settings, "password");
    char *server_address = ch_ss_optional_string(server, "address");
    if (method == NULL || password == NULL || server_address == NULL) {
        free(method); free(password); free(server_address);
        ch_ss_close(&underlying_descriptor);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy Shadowsocks configuration");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (method[0] == '\0' || password[0] == '\0') {
        const char *missing = method[0] == '\0' ? "method" : "password";
        free(method); free(password); free(server_address);
        ch_ss_close(&underlying_descriptor);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "shadowsocks: %s is required", missing);
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ch_ss_cipher cipher;
    ch_status status = ch_ss_cipher_from_name(method, &cipher, error);
    free(method);
    if (status != CH_OK) {
        free(password); free(server_address);
        ch_ss_close(&underlying_descriptor);
        return status;
    }
    uint8_t master_key[32];
    status = ch_ss_evp_bytes_to_key((const uint8_t *)password,
                                    strlen(password), cipher.key_size,
                                    master_key, error);
    free(password);
    if (status != CH_OK) {
        free(server_address);
        ch_ss_close(&underlying_descriptor);
        return status;
    }
    if (underlying_descriptor < 0) {
        if (server_address[0] == '\0') {
            free(server_address);
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "shadowsocks server address is required");
            return CH_ERROR_INVALID_ARGUMENT;
        }
        status = ch_protocol_connect_tcp(server_address,
                                         &underlying_descriptor, error);
        if (status != CH_OK) {
            free(server_address);
            return status;
        }
    }
    free(server_address);
    uint8_t salt[32];
    if (RAND_bytes(salt, (int)cipher.salt_size) != 1 ||
        !ch_ss_send_all(underlying_descriptor, salt, cipher.salt_size)) {
        ch_ss_close(&underlying_descriptor);
        ch_error_set(error, CH_ERROR_IO,
                     "initialize Shadowsocks salt failed");
        return CH_ERROR_IO;
    }
    uint8_t write_subkey[32];
    static const uint8_t info[] = "ss-subkey";
    status = ch_ss_hkdf_sha1(master_key, cipher.key_size, salt,
                             cipher.salt_size, info, sizeof(info) - 1U,
                             write_subkey, cipher.key_size, error);
    uint8_t *address = NULL;
    size_t address_length = 0U;
    if (status == CH_OK) {
        status = ch_socks_encode_address(target, &address, &address_length,
                                         error);
    }
    uint8_t write_nonce[CH_SS_NONCE_SIZE] = {0};
    if (status == CH_OK) {
        status = ch_ss_write_chunks(underlying_descriptor, &cipher,
                                    write_subkey, write_nonce, address,
                                    address_length, error);
    }
    free(address);
    if (status != CH_OK) {
        ch_ss_close(&underlying_descriptor);
        return status;
    }
    int pair[2] = {-1, -1};
    if (socketpair(AF_UNIX, SOCK_STREAM, 0, pair) != 0) {
        ch_ss_close(&underlying_descriptor);
        ch_error_set(error, CH_ERROR_IO,
                     "create Shadowsocks relay stream: %s", strerror(errno));
        return CH_ERROR_IO;
    }
    ch_ss_pump *pump = calloc(1U, sizeof(*pump));
    if (pump == NULL) {
        ch_ss_close(&underlying_descriptor);
        ch_ss_close(&pair[0]); ch_ss_close(&pair[1]);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate Shadowsocks relay stream");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    pump->cipher = cipher;
    memcpy(pump->master_key, master_key, cipher.key_size);
    memcpy(pump->write_subkey, write_subkey, cipher.key_size);
    memcpy(pump->write_nonce, write_nonce, sizeof(write_nonce));
    pump->network_descriptor = underlying_descriptor;
    pump->local_descriptor = pair[1];
    pthread_attr_t attributes;
    int initialized = pthread_attr_init(&attributes) == 0;
    if (initialized) {
        (void)pthread_attr_setdetachstate(&attributes, PTHREAD_CREATE_DETACHED);
    }
    pthread_t thread;
    int started = initialized &&
        pthread_create(&thread, &attributes, ch_ss_pump_main, pump) == 0;
    if (initialized) (void)pthread_attr_destroy(&attributes);
    if (!started) {
        free(pump);
        ch_ss_close(&underlying_descriptor);
        ch_ss_close(&pair[0]); ch_ss_close(&pair[1]);
        ch_error_set(error, CH_ERROR_IO,
                     "start Shadowsocks relay stream failed");
        return CH_ERROR_IO;
    }
    *out_descriptor = pair[0];
    return CH_OK;
}
