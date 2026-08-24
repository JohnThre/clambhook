#include "protocol_vmess.h"

#include <arpa/inet.h>
#include <ctype.h>
#include <errno.h>
#include <limits.h>
#include <openssl/evp.h>
#include <openssl/hmac.h>
#include <openssl/rand.h>
#include <poll.h>
#include <pthread.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>

#include "clambhook/protocol.h"
#include "cnet.h"
#include "internal.h"
#include "protocol_internal.h"

#define CH_VMESS_MAX_CHUNK 16384U
#define CH_VMESS_TAG_SIZE 16U

static const uint8_t ch_vmess_magic[] =
    "c48619fe-8f02-49e0-b9e9-edf763e17e21";
static const uint8_t ch_vmess_kdf_salt[] = "VMess AEAD KDF";
static const uint8_t ch_vmess_auth_id_key_label[] = "AES Auth ID Encryption";
static const uint8_t ch_vmess_header_length_key_label[] =
    "VMess Header AEAD Key_Length";
static const uint8_t ch_vmess_header_length_iv_label[] =
    "VMess Header AEAD Nonce_Length";
static const uint8_t ch_vmess_header_payload_key_label[] =
    "VMess Header AEAD Key";
static const uint8_t ch_vmess_header_payload_iv_label[] =
    "VMess Header AEAD Nonce";
static const uint8_t ch_vmess_response_length_key_label[] =
    "AEAD Resp Header Len Key";
static const uint8_t ch_vmess_response_length_iv_label[] =
    "AEAD Resp Header Len IV";
static const uint8_t ch_vmess_response_payload_key_label[] =
    "AEAD Resp Header Key";
static const uint8_t ch_vmess_response_payload_iv_label[] =
    "AEAD Resp Header IV";

typedef struct ch_vmess_config {
    uint8_t uuid[16];
    uint8_t command_key[16];
    ch_vmess_security security;
    bool tls;
} ch_vmess_config;

typedef struct ch_vmess_pump {
    ch_vmess_security security;
    ch_vmess_session session;
    uint16_t write_counter;
    bool write_exhausted;
    int network_descriptor;
    int local_descriptor;
} ch_vmess_pump;

static void ch_vmess_close(int *descriptor) {
    if (descriptor == NULL || *descriptor < 0) return;
    (void)shutdown(*descriptor, SHUT_RDWR);
    (void)close(*descriptor);
    *descriptor = -1;
}

static ssize_t ch_vmess_send(int descriptor, const void *bytes,
                             size_t length) {
#ifdef MSG_NOSIGNAL
    return send(descriptor, bytes, length, MSG_NOSIGNAL);
#else
    return send(descriptor, bytes, length, 0);
#endif
}

static int ch_vmess_send_all(int descriptor, const void *bytes,
                             size_t length) {
    const uint8_t *cursor = bytes;
    while (length > 0U) {
        ssize_t written = ch_vmess_send(descriptor, cursor, length);
        if (written < 0 && errno == EINTR) continue;
        if (written <= 0) return 0;
        cursor += (size_t)written;
        length -= (size_t)written;
    }
    return 1;
}

static char *ch_vmess_optional_string(const ch_config_table *table,
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

static ch_status ch_vmess_digest(const EVP_MD *digest,
                                 const uint8_t *bytes, size_t length,
                                 uint8_t *out, unsigned int expected_length,
                                 ch_error *error) {
    EVP_MD_CTX *context = EVP_MD_CTX_new();
    unsigned int output_length = 0U;
    int ok = context != NULL &&
        EVP_DigestInit_ex(context, digest, NULL) == 1 &&
        (length == 0U || EVP_DigestUpdate(context, bytes, length) == 1) &&
        EVP_DigestFinal_ex(context, out, &output_length) == 1;
    EVP_MD_CTX_free(context);
    if (!ok || output_length != expected_length) {
        ch_error_set(error, CH_ERROR_INTERNAL, "VMESS digest failed");
        return CH_ERROR_INTERNAL;
    }
    return CH_OK;
}

ch_status ch_vmess_parse_uuid(const char *text, uint8_t out_uuid[16],
                              ch_error *error) {
    ch_error_clear(error);
    if (text == NULL || out_uuid == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "vmess: uuid is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    const char *start = text;
    while (*start != '\0' && isspace((unsigned char)*start)) ++start;
    const char *end = start + strlen(start);
    while (end > start && isspace((unsigned char)end[-1])) --end;
    char clean[33];
    size_t count = 0U;
    for (const char *cursor = start; cursor < end; ++cursor) {
        unsigned char character = (unsigned char)*cursor;
        if (character == '-') continue;
        if (!isxdigit(character) || count >= 32U) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "vmess: invalid uuid");
            return CH_ERROR_INVALID_ARGUMENT;
        }
        clean[count++] = (char)character;
    }
    if (count != 32U) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "vmess: invalid uuid");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    clean[32] = '\0';
    for (size_t index = 0U; index < 16U; ++index) {
        unsigned int value = 0U;
        for (size_t nibble = 0U; nibble < 2U; ++nibble) {
            unsigned char character = (unsigned char)clean[index * 2U + nibble];
            unsigned int digit = character >= '0' && character <= '9' ?
                (unsigned int)(character - '0') :
                (unsigned int)(tolower(character) - 'a' + 10);
            value = (value << 4U) | digit;
        }
        out_uuid[index] = (uint8_t)value;
    }
    return CH_OK;
}

ch_status ch_vmess_command_key(const uint8_t uuid[16], uint8_t out_key[16],
                               ch_error *error) {
    if (uuid == NULL || out_key == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "VMESS UUID and command key output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    uint8_t input[16U + sizeof(ch_vmess_magic) - 1U];
    memcpy(input, uuid, 16U);
    memcpy(input + 16U, ch_vmess_magic, sizeof(ch_vmess_magic) - 1U);
    return ch_vmess_digest(EVP_md5(), input, sizeof(input), out_key, 16U,
                            error);
}

static ch_status ch_vmess_hmac_sha256(const uint8_t *key, size_t key_length,
                                      const uint8_t *data,
                                      size_t data_length, uint8_t out[32],
                                      ch_error *error) {
    if (key_length > (size_t)INT_MAX) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "VMESS HMAC key is too long");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    unsigned int length = 0U;
    if (HMAC(EVP_sha256(), key, (int)key_length, data, data_length,
             out, &length) == NULL || length != 32U) {
        ch_error_set(error, CH_ERROR_INTERNAL, "VMESS HMAC failed");
        return CH_ERROR_INTERNAL;
    }
    return CH_OK;
}

static ch_status ch_vmess_hash_layer(const uint8_t *const *paths,
                                     const size_t *path_lengths,
                                     size_t depth, const uint8_t *data,
                                     size_t data_length, uint8_t out[32],
                                     ch_error *error) {
    if (depth == 0U) {
        return ch_vmess_hmac_sha256(
            ch_vmess_kdf_salt, sizeof(ch_vmess_kdf_salt) - 1U, data,
            data_length, out, error);
    }
    const uint8_t *label = paths[depth - 1U];
    size_t label_length = path_lengths[depth - 1U];
    uint8_t key_block[64];
    memset(key_block, 0, sizeof(key_block));
    if (label_length > sizeof(key_block)) {
        ch_status status = ch_vmess_hash_layer(
            paths, path_lengths, depth - 1U, label, label_length, key_block,
            error);
        if (status != CH_OK) return status;
    } else if (label_length > 0U) {
        memcpy(key_block, label, label_length);
    }
    uint8_t *inner_input = malloc(sizeof(key_block) + data_length);
    if (inner_input == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate VMESS KDF input");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    for (size_t index = 0U; index < sizeof(key_block); ++index) {
        inner_input[index] = key_block[index] ^ 0x36U;
    }
    if (data_length > 0U) {
        memcpy(inner_input + sizeof(key_block), data, data_length);
    }
    uint8_t inner[32];
    ch_status status = ch_vmess_hash_layer(
        paths, path_lengths, depth - 1U, inner_input,
        sizeof(key_block) + data_length, inner, error);
    free(inner_input);
    if (status != CH_OK) return status;
    uint8_t outer_input[64U + 32U];
    for (size_t index = 0U; index < sizeof(key_block); ++index) {
        outer_input[index] = key_block[index] ^ 0x5cU;
    }
    memcpy(outer_input + sizeof(key_block), inner, sizeof(inner));
    return ch_vmess_hash_layer(paths, path_lengths, depth - 1U,
                                outer_input, sizeof(outer_input), out, error);
}

ch_status ch_vmess_kdf(const uint8_t *key, size_t key_length,
                       const uint8_t *const *paths,
                       const size_t *path_lengths, size_t path_count,
                       uint8_t out_key[32], ch_error *error) {
    ch_error_clear(error);
    if ((key == NULL && key_length > 0U) || out_key == NULL ||
        (path_count > 0U && (paths == NULL || path_lengths == NULL)) ||
        path_count > 8U) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "invalid VMESS KDF input");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    return ch_vmess_hash_layer(paths, path_lengths, path_count, key,
                                key_length, out_key, error);
}

static ch_status ch_vmess_split_target(const char *target, char **out_host,
                                       uint16_t *out_port,
                                       ch_error *error) {
    *out_host = NULL;
    *out_port = 0U;
    if (target == NULL || target[0] == '\0') {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "vmess: target is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    const char *host_start = target;
    const char *host_end = NULL;
    const char *port_start = NULL;
    if (target[0] == '[') {
        host_start = target + 1;
        host_end = strchr(host_start, ']');
        if (host_end == NULL || host_end[1] != ':' || host_end[2] == '\0') {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "vmess: invalid bracketed target");
            return CH_ERROR_INVALID_ARGUMENT;
        }
        port_start = host_end + 2;
    } else {
        const char *separator = strrchr(target, ':');
        if (separator == NULL || separator == target || separator[1] == '\0' ||
            strchr(target, ':') != separator) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                         "vmess: target must be host:port");
            return CH_ERROR_INVALID_ARGUMENT;
        }
        host_end = separator;
        port_start = separator + 1;
    }
    errno = 0;
    char *end = NULL;
    long port = strtol(port_start, &end, 10);
    if (errno != 0 || end == port_start || *end != '\0' || port < 0L ||
        port > 65535L) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "vmess: invalid target port");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    size_t host_length = (size_t)(host_end - host_start);
    if (host_length == 0U || host_length > 255U) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "vmess: target host length is invalid");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    char *host = malloc(host_length + 1U);
    if (host == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate VMESS target host");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    memcpy(host, host_start, host_length);
    host[host_length] = '\0';
    *out_host = host;
    *out_port = (uint16_t)port;
    return CH_OK;
}

ch_status ch_vmess_encode_address(const char *target, uint8_t **out_address,
                                  size_t *out_address_length,
                                  ch_error *error) {
    ch_error_clear(error);
    if (out_address == NULL || out_address_length == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "VMESS address output is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_address = NULL;
    *out_address_length = 0U;
    char *host = NULL;
    uint16_t port = 0U;
    ch_status status = ch_vmess_split_target(target, &host, &port, error);
    if (status != CH_OK) return status;
    uint8_t raw[16];
    uint8_t type;
    size_t host_length;
    size_t prefix_length = 3U;
    if (inet_pton(AF_INET, host, raw) == 1) {
        type = 0x01U;
        host_length = 4U;
    } else if (inet_pton(AF_INET6, host, raw) == 1) {
        type = 0x03U;
        host_length = 16U;
    } else {
        type = 0x02U;
        host_length = strlen(host);
        prefix_length = 4U;
    }
    uint8_t *address = malloc(prefix_length + host_length);
    if (address == NULL) {
        free(host);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate VMESS address");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    address[0] = (uint8_t)(port >> 8U);
    address[1] = (uint8_t)(port & 0xffU);
    address[2] = type;
    size_t offset = 3U;
    if (type == 0x02U) {
        address[offset++] = (uint8_t)host_length;
        memcpy(address + offset, host, host_length);
    } else {
        memcpy(address + offset, raw, host_length);
    }
    free(host);
    *out_address = address;
    *out_address_length = prefix_length + host_length;
    return CH_OK;
}

static ch_status ch_vmess_md5(const uint8_t *bytes, size_t length,
                              uint8_t out[16], ch_error *error) {
    return ch_vmess_digest(EVP_md5(), bytes, length, out, 16U, error);
}

static ch_status ch_vmess_aead_key(ch_vmess_security security,
                                   const uint8_t key[16], uint8_t out[32],
                                   size_t *out_length, ch_error *error) {
    if (security == CH_VMESS_AES_128_GCM) {
        memcpy(out, key, 16U);
        *out_length = 16U;
        return CH_OK;
    }
    if (security == CH_VMESS_CHACHA20_POLY1305) {
        ch_status status = ch_vmess_md5(key, 16U, out, error);
        if (status == CH_OK) status = ch_vmess_md5(out, 16U, out + 16U,
                                                  error);
        if (status == CH_OK) *out_length = 32U;
        return status;
    }
    ch_error_set(error, CH_ERROR_UNSUPPORTED,
                 "vmess: unsupported body security");
    return CH_ERROR_UNSUPPORTED;
}

static int ch_vmess_aead_encrypt(ch_vmess_security security,
                                 const uint8_t *key, const uint8_t nonce[12],
                                 const uint8_t *plaintext,
                                 size_t plaintext_length,
                                 const uint8_t *aad, size_t aad_length,
                                 uint8_t *ciphertext, uint8_t tag[16]) {
    return security == CH_VMESS_AES_128_GCM ?
        cnet_aes128gcm_encrypt(key, nonce, plaintext, plaintext_length, aad,
                               aad_length, ciphertext, tag) :
        cnet_chacha20poly1305_encrypt(key, nonce, plaintext,
                                      plaintext_length, aad, aad_length,
                                      ciphertext, tag);
}

static int ch_vmess_aead_decrypt(ch_vmess_security security,
                                 const uint8_t *key, const uint8_t nonce[12],
                                 const uint8_t *ciphertext,
                                 size_t ciphertext_length,
                                 const uint8_t *aad, size_t aad_length,
                                 const uint8_t tag[16], uint8_t *plaintext) {
    return security == CH_VMESS_AES_128_GCM ?
        cnet_aes128gcm_decrypt(key, nonce, ciphertext, ciphertext_length, aad,
                               aad_length, tag, plaintext) :
        cnet_chacha20poly1305_decrypt(key, nonce, ciphertext,
                                      ciphertext_length, aad, aad_length, tag,
                                      plaintext);
}

static void ch_vmess_body_nonce(uint16_t counter, const uint8_t iv[16],
                                uint8_t nonce[12]) {
    nonce[0] = (uint8_t)(counter >> 8U);
    nonce[1] = (uint8_t)(counter & 0xffU);
    memcpy(nonce + 2U, iv + 2U, 10U);
}

ch_status ch_vmess_encrypt_chunk(ch_vmess_security security,
                                 const uint8_t key[16],
                                 const uint8_t iv[16], uint16_t *counter,
                                 bool *exhausted, const uint8_t *plaintext,
                                 size_t plaintext_length,
                                 uint8_t **out_frame,
                                 size_t *out_frame_length,
                                 ch_error *error) {
    ch_error_clear(error);
    if (key == NULL || iv == NULL || counter == NULL || exhausted == NULL ||
        plaintext == NULL || plaintext_length == 0U ||
        plaintext_length > CH_VMESS_MAX_CHUNK || out_frame == NULL ||
        out_frame_length == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "invalid VMESS body chunk");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (*exhausted) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "vmess: body nonce counter exhausted");
        return CH_ERROR_INVALID_STATE;
    }
    uint8_t aead_key[32];
    size_t aead_key_length = 0U;
    ch_status status = ch_vmess_aead_key(security, key, aead_key,
                                         &aead_key_length, error);
    (void)aead_key_length;
    if (status != CH_OK) return status;
    size_t encrypted_length = plaintext_length + CH_VMESS_TAG_SIZE;
    uint8_t *frame = malloc(2U + encrypted_length);
    if (frame == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate VMESS body frame");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    frame[0] = (uint8_t)(encrypted_length >> 8U);
    frame[1] = (uint8_t)(encrypted_length & 0xffU);
    uint8_t nonce[12];
    ch_vmess_body_nonce(*counter, iv, nonce);
    if (ch_vmess_aead_encrypt(security, aead_key, nonce, plaintext,
                              plaintext_length, NULL, 0U, frame + 2U,
                              frame + 2U + plaintext_length) != CNET_OK) {
        free(frame);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "encrypt VMESS body chunk failed");
        return CH_ERROR_INTERNAL;
    }
    ++*counter;
    if (*counter == 0U) *exhausted = true;
    *out_frame = frame;
    *out_frame_length = 2U + encrypted_length;
    return CH_OK;
}

ch_status ch_vmess_decrypt_chunk(ch_vmess_security security,
                                 const uint8_t key[16],
                                 const uint8_t iv[16], uint16_t *counter,
                                 bool *exhausted, const uint8_t *frame,
                                 size_t frame_length,
                                 uint8_t **out_plaintext,
                                 size_t *out_plaintext_length,
                                 ch_error *error) {
    ch_error_clear(error);
    if (key == NULL || iv == NULL || counter == NULL || exhausted == NULL ||
        frame == NULL || frame_length < 18U || out_plaintext == NULL ||
        out_plaintext_length == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "invalid encrypted VMESS chunk");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (*exhausted) {
        ch_error_set(error, CH_ERROR_INVALID_STATE,
                     "vmess: body nonce counter exhausted");
        return CH_ERROR_INVALID_STATE;
    }
    size_t encrypted_length = ((size_t)frame[0] << 8U) | (size_t)frame[1];
    if (encrypted_length < CH_VMESS_TAG_SIZE ||
        encrypted_length + 2U != frame_length) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "invalid VMESS body chunk length");
        return CH_ERROR_PARSE;
    }
    size_t plaintext_length = encrypted_length - CH_VMESS_TAG_SIZE;
    uint8_t *plaintext = malloc(plaintext_length == 0U ? 1U :
                                plaintext_length);
    if (plaintext == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate VMESS body plaintext");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    uint8_t aead_key[32];
    size_t aead_key_length = 0U;
    ch_status status = ch_vmess_aead_key(security, key, aead_key,
                                         &aead_key_length, error);
    (void)aead_key_length;
    uint8_t nonce[12];
    ch_vmess_body_nonce(*counter, iv, nonce);
    if (status != CH_OK || ch_vmess_aead_decrypt(
            security, aead_key, nonce, frame + 2U, plaintext_length, NULL, 0U,
            frame + 2U + plaintext_length, plaintext) != CNET_OK) {
        free(plaintext);
        ch_error_set(error, CH_ERROR_PARSE,
                     "authenticate VMESS body chunk failed");
        return CH_ERROR_PARSE;
    }
    ++*counter;
    if (*counter == 0U) *exhausted = true;
    *out_plaintext = plaintext;
    *out_plaintext_length = plaintext_length;
    return CH_OK;
}

static uint32_t ch_vmess_crc32(const uint8_t *bytes, size_t length) {
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

static uint32_t ch_vmess_fnv1a(const uint8_t *bytes, size_t length) {
    uint32_t hash = 2166136261U;
    for (size_t index = 0U; index < length; ++index) {
        hash ^= bytes[index];
        hash *= 16777619U;
    }
    return hash;
}

static ch_status ch_vmess_aes_ecb_encrypt(const uint8_t key[16],
                                          const uint8_t input[16],
                                          uint8_t output[16],
                                          ch_error *error) {
    EVP_CIPHER_CTX *context = EVP_CIPHER_CTX_new();
    int written = 0;
    int final_written = 0;
    int ok = context != NULL &&
        EVP_EncryptInit_ex(context, EVP_aes_128_ecb(), NULL, key, NULL) == 1 &&
        EVP_CIPHER_CTX_set_padding(context, 0) == 1 &&
        EVP_EncryptUpdate(context, output, &written, input, 16) == 1 &&
        EVP_EncryptFinal_ex(context, output + written, &final_written) == 1 &&
        written + final_written == 16;
    EVP_CIPHER_CTX_free(context);
    if (!ok) {
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "encrypt VMESS AuthID failed");
        return CH_ERROR_INTERNAL;
    }
    return CH_OK;
}

static ch_status ch_vmess_create_auth_id(const uint8_t command_key[16],
                                         uint8_t out_auth_id[16],
                                         ch_error *error) {
    uint8_t plaintext[16];
    uint64_t timestamp = (uint64_t)time(NULL);
    for (size_t index = 0U; index < 8U; ++index) {
        plaintext[7U - index] = (uint8_t)(timestamp >> (index * 8U));
    }
    if (RAND_bytes(plaintext + 8U, 4) != 1) {
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "generate VMESS AuthID random failed");
        return CH_ERROR_INTERNAL;
    }
    uint32_t checksum = ch_vmess_crc32(plaintext, 12U);
    plaintext[12] = (uint8_t)(checksum >> 24U);
    plaintext[13] = (uint8_t)(checksum >> 16U);
    plaintext[14] = (uint8_t)(checksum >> 8U);
    plaintext[15] = (uint8_t)checksum;
    const uint8_t *paths[] = {ch_vmess_auth_id_key_label};
    const size_t lengths[] = {sizeof(ch_vmess_auth_id_key_label) - 1U};
    uint8_t derived[32];
    ch_status status = ch_vmess_kdf(command_key, 16U, paths, lengths, 1U,
                                    derived, error);
    if (status != CH_OK) return status;
    return ch_vmess_aes_ecb_encrypt(derived, plaintext, out_auth_id, error);
}

static ch_status ch_vmess_derive(const uint8_t *key, size_t key_length,
                                 const uint8_t *const *paths,
                                 const size_t *path_lengths,
                                 size_t path_count, uint8_t *out,
                                 size_t out_length, ch_error *error) {
    uint8_t derived[32];
    ch_status status = ch_vmess_kdf(key, key_length, paths, path_lengths,
                                    path_count, derived, error);
    if (status == CH_OK) memcpy(out, derived, out_length);
    return status;
}

static ch_status ch_vmess_new_session(ch_vmess_session *session,
                                      ch_error *error) {
    uint8_t random[33];
    if (RAND_bytes(random, (int)sizeof(random)) != 1) {
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "generate VMESS session failed");
        return CH_ERROR_INTERNAL;
    }
    memcpy(session->request_body_key, random, 16U);
    memcpy(session->request_body_iv, random + 16U, 16U);
    session->response_header = random[32];
    uint8_t digest[32];
    ch_status status = ch_vmess_digest(
        EVP_sha256(), session->request_body_key, 16U, digest, 32U, error);
    if (status == CH_OK) memcpy(session->response_body_key, digest, 16U);
    if (status == CH_OK) status = ch_vmess_digest(
        EVP_sha256(), session->request_body_iv, 16U, digest, 32U, error);
    if (status == CH_OK) memcpy(session->response_body_iv, digest, 16U);
    return status;
}

static ch_status ch_vmess_encode_header(const ch_vmess_config *config,
                                        ch_vmess_session *session,
                                        const char *target,
                                        uint8_t **out_header,
                                        size_t *out_header_length,
                                        ch_error *error) {
    uint8_t *address = NULL;
    size_t address_length = 0U;
    ch_status status = ch_vmess_encode_address(target, &address,
                                               &address_length, error);
    if (status != CH_OK) return status;
    size_t body_length = 38U + address_length + 4U;
    uint8_t *body = malloc(body_length);
    if (body == NULL) {
        free(address);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate VMESS request header");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    size_t offset = 0U;
    body[offset++] = 0x01U;
    memcpy(body + offset, session->request_body_iv, 16U); offset += 16U;
    memcpy(body + offset, session->request_body_key, 16U); offset += 16U;
    body[offset++] = session->response_header;
    body[offset++] = 0x01U;
    body[offset++] = (uint8_t)config->security;
    body[offset++] = 0x00U;
    body[offset++] = 0x01U;
    memcpy(body + offset, address, address_length); offset += address_length;
    free(address);
    uint32_t checksum = ch_vmess_fnv1a(body, offset);
    body[offset++] = (uint8_t)(checksum >> 24U);
    body[offset++] = (uint8_t)(checksum >> 16U);
    body[offset++] = (uint8_t)(checksum >> 8U);
    body[offset++] = (uint8_t)checksum;

    uint8_t auth_id[16];
    status = ch_vmess_create_auth_id(config->command_key, auth_id, error);
    uint8_t connection_nonce[8];
    if (status == CH_OK && RAND_bytes(connection_nonce, 8) != 1) {
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "generate VMESS header nonce failed");
        status = CH_ERROR_INTERNAL;
    }
    uint8_t length_plain[2] = {
        (uint8_t)(body_length >> 8U), (uint8_t)body_length
    };
    const uint8_t *length_key_paths[] = {
        ch_vmess_header_length_key_label, auth_id, connection_nonce
    };
    const size_t length_key_lengths[] = {
        sizeof(ch_vmess_header_length_key_label) - 1U, 16U, 8U
    };
    const uint8_t *length_iv_paths[] = {
        ch_vmess_header_length_iv_label, auth_id, connection_nonce
    };
    const size_t length_iv_lengths[] = {
        sizeof(ch_vmess_header_length_iv_label) - 1U, 16U, 8U
    };
    const uint8_t *payload_key_paths[] = {
        ch_vmess_header_payload_key_label, auth_id, connection_nonce
    };
    const size_t payload_key_lengths[] = {
        sizeof(ch_vmess_header_payload_key_label) - 1U, 16U, 8U
    };
    const uint8_t *payload_iv_paths[] = {
        ch_vmess_header_payload_iv_label, auth_id, connection_nonce
    };
    const size_t payload_iv_lengths[] = {
        sizeof(ch_vmess_header_payload_iv_label) - 1U, 16U, 8U
    };
    uint8_t length_key[16], length_nonce[12];
    uint8_t payload_key[16], payload_nonce[12];
    if (status == CH_OK) status = ch_vmess_derive(
        config->command_key, 16U, length_key_paths, length_key_lengths, 3U,
        length_key, sizeof(length_key), error);
    if (status == CH_OK) status = ch_vmess_derive(
        config->command_key, 16U, length_iv_paths, length_iv_lengths, 3U,
        length_nonce, sizeof(length_nonce), error);
    if (status == CH_OK) status = ch_vmess_derive(
        config->command_key, 16U, payload_key_paths, payload_key_lengths, 3U,
        payload_key, sizeof(payload_key), error);
    if (status == CH_OK) status = ch_vmess_derive(
        config->command_key, 16U, payload_iv_paths, payload_iv_lengths, 3U,
        payload_nonce, sizeof(payload_nonce), error);
    size_t total = 16U + 18U + 8U + body_length + 16U;
    uint8_t *header = status == CH_OK ? malloc(total) : NULL;
    if (status == CH_OK && header == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate sealed VMESS header");
        status = CH_ERROR_OUT_OF_MEMORY;
    }
    if (status == CH_OK) {
        memcpy(header, auth_id, 16U);
        if (cnet_aes128gcm_encrypt(
                length_key, length_nonce, length_plain, sizeof(length_plain),
                auth_id, 16U, header + 16U, header + 18U) != CNET_OK ||
            cnet_aes128gcm_encrypt(
                payload_key, payload_nonce, body, body_length, auth_id, 16U,
                header + 42U, header + 42U + body_length) != CNET_OK) {
            free(header);
            header = NULL;
            ch_error_set(error, CH_ERROR_INTERNAL,
                         "seal VMESS request header failed");
            status = CH_ERROR_INTERNAL;
        } else {
            memcpy(header + 34U, connection_nonce, 8U);
            *out_header = header;
            *out_header_length = total;
        }
    }
    free(body);
    return status;
}

static int ch_vmess_read_exact(ch_vmess_pump *pump, void *bytes,
                               size_t length) {
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
        if (ready < 0 ||
            (waits[1].revents & (POLLHUP | POLLERR | POLLNVAL)) != 0) return 0;
        if ((waits[0].revents & (POLLIN | POLLHUP | POLLERR)) == 0) continue;
        ssize_t received = recv(pump->network_descriptor, cursor, length, 0);
        if (received < 0 && errno == EINTR) continue;
        if (received <= 0) return 0;
        cursor += (size_t)received;
        length -= (size_t)received;
    }
    return 1;
}

static ch_status ch_vmess_read_response_header(ch_vmess_pump *pump,
                                               ch_error *error) {
    const uint8_t *length_key_paths[] = {ch_vmess_response_length_key_label};
    const size_t length_key_lengths[] = {
        sizeof(ch_vmess_response_length_key_label) - 1U
    };
    const uint8_t *length_iv_paths[] = {ch_vmess_response_length_iv_label};
    const size_t length_iv_lengths[] = {
        sizeof(ch_vmess_response_length_iv_label) - 1U
    };
    uint8_t key[16], nonce[12];
    ch_status status = ch_vmess_derive(
        pump->session.response_body_key, 16U, length_key_paths,
        length_key_lengths, 1U, key, sizeof(key), error);
    if (status == CH_OK) status = ch_vmess_derive(
        pump->session.response_body_iv, 16U, length_iv_paths,
        length_iv_lengths, 1U, nonce, sizeof(nonce), error);
    uint8_t encrypted_length[18];
    if (status != CH_OK || !ch_vmess_read_exact(
            pump, encrypted_length, sizeof(encrypted_length))) {
        ch_error_set(error, CH_ERROR_IO,
                     "read VMESS response header length failed");
        return CH_ERROR_IO;
    }
    uint8_t plain_length[2];
    if (cnet_aes128gcm_decrypt(key, nonce, encrypted_length, 2U, NULL, 0U,
                               encrypted_length + 2U,
                               plain_length) != CNET_OK) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "authenticate VMESS response length failed");
        return CH_ERROR_PARSE;
    }
    size_t header_length = ((size_t)plain_length[0] << 8U) |
                           (size_t)plain_length[1];
    uint8_t *encrypted = malloc(header_length + 16U);
    uint8_t *plain = malloc(header_length == 0U ? 1U : header_length);
    if (encrypted == NULL || plain == NULL) {
        free(encrypted); free(plain);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate VMESS response header");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    const uint8_t *payload_key_paths[] = {
        ch_vmess_response_payload_key_label
    };
    const size_t payload_key_lengths[] = {
        sizeof(ch_vmess_response_payload_key_label) - 1U
    };
    const uint8_t *payload_iv_paths[] = {
        ch_vmess_response_payload_iv_label
    };
    const size_t payload_iv_lengths[] = {
        sizeof(ch_vmess_response_payload_iv_label) - 1U
    };
    status = ch_vmess_derive(
        pump->session.response_body_key, 16U, payload_key_paths,
        payload_key_lengths, 1U, key, sizeof(key), error);
    if (status == CH_OK) status = ch_vmess_derive(
        pump->session.response_body_iv, 16U, payload_iv_paths,
        payload_iv_lengths, 1U, nonce, sizeof(nonce), error);
    if (status != CH_OK || !ch_vmess_read_exact(
            pump, encrypted, header_length + 16U) ||
        cnet_aes128gcm_decrypt(key, nonce, encrypted, header_length, NULL, 0U,
                               encrypted + header_length, plain) != CNET_OK ||
        header_length < 1U || plain[0] != pump->session.response_header) {
        free(encrypted); free(plain);
        ch_error_set(error, CH_ERROR_PARSE,
                     "authenticate VMESS response header failed");
        return CH_ERROR_PARSE;
    }
    free(encrypted); free(plain);
    return CH_OK;
}

static ch_status ch_vmess_write_chunks(ch_vmess_pump *pump,
                                       const uint8_t *plaintext,
                                       size_t plaintext_length,
                                       ch_error *error) {
    while (plaintext_length > 0U) {
        size_t chunk_length = plaintext_length > CH_VMESS_MAX_CHUNK ?
                              CH_VMESS_MAX_CHUNK : plaintext_length;
        uint8_t *frame = NULL;
        size_t frame_length = 0U;
        ch_status status = ch_vmess_encrypt_chunk(
            pump->security, pump->session.request_body_key,
            pump->session.request_body_iv, &pump->write_counter,
            &pump->write_exhausted, plaintext, chunk_length, &frame,
            &frame_length, error);
        if (status != CH_OK) return status;
        int sent = ch_vmess_send_all(pump->network_descriptor, frame,
                                     frame_length);
        free(frame);
        if (!sent) {
            ch_error_set(error, CH_ERROR_IO,
                         "write VMESS body frame failed");
            return CH_ERROR_IO;
        }
        plaintext += chunk_length;
        plaintext_length -= chunk_length;
    }
    return CH_OK;
}

static void *ch_vmess_outgoing_main(void *opaque) {
    ch_vmess_pump *pump = opaque;
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
        if (ch_vmess_write_chunks(pump, plaintext, (size_t)received,
                                  &error) != CH_OK) {
            (void)shutdown(pump->network_descriptor, SHUT_RDWR);
            break;
        }
    }
    return NULL;
}

static void *ch_vmess_pump_main(void *opaque) {
    ch_vmess_pump *pump = opaque;
    pthread_t outgoing;
    int outgoing_started = pthread_create(&outgoing, NULL,
                                           ch_vmess_outgoing_main, pump) == 0;
    ch_error error;
    int healthy = outgoing_started &&
        ch_vmess_read_response_header(pump, &error) == CH_OK;
    uint16_t read_counter = 0U;
    bool read_exhausted = false;
    while (healthy) {
        uint8_t length_bytes[2];
        if (!ch_vmess_read_exact(pump, length_bytes, sizeof(length_bytes))) break;
        size_t encrypted_length = ((size_t)length_bytes[0] << 8U) |
                                  (size_t)length_bytes[1];
        if (encrypted_length < 16U) break;
        uint8_t *frame = malloc(2U + encrypted_length);
        if (frame == NULL) break;
        memcpy(frame, length_bytes, 2U);
        if (!ch_vmess_read_exact(pump, frame + 2U, encrypted_length)) {
            free(frame);
            break;
        }
        uint8_t *plaintext = NULL;
        size_t plaintext_length = 0U;
        ch_status status = ch_vmess_decrypt_chunk(
            pump->security, pump->session.response_body_key,
            pump->session.response_body_iv, &read_counter, &read_exhausted,
            frame, 2U + encrypted_length, &plaintext, &plaintext_length,
            &error);
        free(frame);
        if (status != CH_OK || !ch_vmess_send_all(
                pump->local_descriptor, plaintext, plaintext_length)) {
            free(plaintext);
            break;
        }
        free(plaintext);
    }
    (void)shutdown(pump->local_descriptor, SHUT_RDWR);
    (void)shutdown(pump->network_descriptor, SHUT_RDWR);
    if (outgoing_started) (void)pthread_join(outgoing, NULL);
    ch_vmess_close(&pump->network_descriptor);
    ch_vmess_close(&pump->local_descriptor);
    free(pump);
    return NULL;
}

static ch_status ch_vmess_parse_config(const ch_config_table *server,
                                       ch_vmess_config *config,
                                       ch_error *error) {
    memset(config, 0, sizeof(*config));
    const ch_config_table *settings = ch_config_table_get_table(server,
                                                                "settings");
    char *uuid = ch_vmess_optional_string(settings, "uuid");
    if (uuid == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy VMESS UUID");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_status status = ch_vmess_parse_uuid(uuid, config->uuid, error);
    free(uuid);
    if (status != CH_OK) return status;
    status = ch_vmess_command_key(config->uuid, config->command_key, error);
    if (status != CH_OK) return status;
    if (settings != NULL && ch_config_table_has(settings, "alter_id")) {
        int64_t alter_id = 0;
        if (ch_config_table_get_int(settings, "alter_id", &alter_id,
                                    error) != CH_OK || alter_id != 0) {
            ch_error_set(error, CH_ERROR_UNSUPPORTED,
                         "vmess: alter_id must be 0 (AEAD only)");
            return CH_ERROR_UNSUPPORTED;
        }
    }
    char *security = ch_vmess_optional_string(settings, "security");
    if (security != NULL && security[0] == '\0') {
        free(security);
        security = ch_vmess_optional_string(settings, "method");
    }
    if (security == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy VMESS security");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    char *start = security;
    while (*start != '\0' && isspace((unsigned char)*start)) ++start;
    char *end = start + strlen(start);
    while (end > start && isspace((unsigned char)end[-1])) --end;
    *end = '\0';
    for (char *cursor = start; *cursor != '\0'; ++cursor) {
        *cursor = (char)tolower((unsigned char)*cursor);
    }
    if (start[0] == '\0' || strcmp(start, "auto") == 0) {
        config->security = cnet_aes256gcm_available() ?
            CH_VMESS_AES_128_GCM : CH_VMESS_CHACHA20_POLY1305;
    } else if (strcmp(start, "aes-128-gcm") == 0) {
        config->security = CH_VMESS_AES_128_GCM;
    } else if (strcmp(start, "chacha20-poly1305") == 0) {
        config->security = CH_VMESS_CHACHA20_POLY1305;
    } else {
        ch_error_set(error, CH_ERROR_UNSUPPORTED,
                     "vmess: unsupported security %s", start);
        free(security);
        return CH_ERROR_UNSUPPORTED;
    }
    free(security);
    bool tls = false;
    ch_error ignored;
    if (settings != NULL) {
        (void)ch_config_table_get_bool(settings, "tls", &tls, &ignored);
    }
    config->tls = tls;
    return CH_OK;
}

ch_status ch_protocol_vmess_dial(const ch_config_table *server,
                                 int underlying_descriptor,
                                 const char *target,
                                 int *out_descriptor,
                                 ch_error *error) {
    ch_vmess_config config;
    ch_status status = ch_vmess_parse_config(server, &config, error);
    char *server_address = ch_vmess_optional_string(server, "address");
    if (status != CH_OK || server_address == NULL) {
        free(server_address);
        ch_vmess_close(&underlying_descriptor);
        if (status == CH_OK) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy VMESS server address");
            status = CH_ERROR_OUT_OF_MEMORY;
        }
        return status;
    }
    if (server_address[0] == '\0') {
        free(server_address);
        ch_vmess_close(&underlying_descriptor);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "vmess: server address is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    if (underlying_descriptor < 0) {
        status = ch_protocol_connect_tcp(server_address,
                                         &underlying_descriptor, error);
        if (status != CH_OK) {
            free(server_address);
            return status;
        }
    }
    const ch_config_table *settings = ch_config_table_get_table(server,
                                                                "settings");
    if (config.tls) {
        int tls_descriptor = -1;
        status = ch_protocol_tls_wrap(settings, server_address,
                                      underlying_descriptor, NULL, 0U,
                                      "vmess", &tls_descriptor, error);
        underlying_descriptor = status == CH_OK ? tls_descriptor : -1;
    }
    free(server_address);
    if (status != CH_OK) return status;
    ch_vmess_session session;
    status = ch_vmess_new_session(&session, error);
    uint8_t *header = NULL;
    size_t header_length = 0U;
    if (status == CH_OK) status = ch_vmess_encode_header(
        &config, &session, target, &header, &header_length, error);
    if (status == CH_OK && !ch_vmess_send_all(
            underlying_descriptor, header, header_length)) {
        ch_error_set(error, CH_ERROR_IO,
                     "write VMESS request header failed");
        status = CH_ERROR_IO;
    }
    free(header);
    if (status != CH_OK) {
        ch_vmess_close(&underlying_descriptor);
        return status;
    }
    int pair[2] = {-1, -1};
    if (socketpair(AF_UNIX, SOCK_STREAM, 0, pair) != 0) {
        ch_vmess_close(&underlying_descriptor);
        ch_error_set(error, CH_ERROR_IO,
                     "create VMESS relay stream failed: %s", strerror(errno));
        return CH_ERROR_IO;
    }
    ch_vmess_pump *pump = calloc(1U, sizeof(*pump));
    if (pump == NULL) {
        ch_vmess_close(&underlying_descriptor);
        ch_vmess_close(&pair[0]); ch_vmess_close(&pair[1]);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate VMESS relay stream");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    pump->security = config.security;
    pump->session = session;
    pump->network_descriptor = underlying_descriptor;
    pump->local_descriptor = pair[1];
    pthread_attr_t attributes;
    int initialized = pthread_attr_init(&attributes) == 0;
    if (initialized) {
        (void)pthread_attr_setdetachstate(&attributes, PTHREAD_CREATE_DETACHED);
    }
    pthread_t thread;
    int started = initialized &&
        pthread_create(&thread, &attributes, ch_vmess_pump_main, pump) == 0;
    if (initialized) (void)pthread_attr_destroy(&attributes);
    if (!started) {
        free(pump);
        ch_vmess_close(&underlying_descriptor);
        ch_vmess_close(&pair[0]); ch_vmess_close(&pair[1]);
        ch_error_set(error, CH_ERROR_IO,
                     "start VMESS relay stream failed");
        return CH_ERROR_IO;
    }
    *out_descriptor = pair[0];
    return CH_OK;
}
