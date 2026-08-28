// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_PROTOCOL_SHADOWSOCKS_H
#define CLAMBHOOK_PROTOCOL_SHADOWSOCKS_H

#include <stddef.h>
#include <stdint.h>

#include "clambhook/config.h"
#include "clambhook/error.h"

typedef enum ch_ss_method {
    CH_SS_AES_128_GCM = 1,
    CH_SS_AES_256_GCM = 2,
    CH_SS_CHACHA20_IETF_POLY1305 = 3
} ch_ss_method;

typedef struct ch_ss_cipher {
    ch_ss_method method;
    size_t key_size;
    size_t salt_size;
} ch_ss_cipher;

ch_status ch_ss_cipher_from_name(const char *name, ch_ss_cipher *out_cipher,
                                 ch_error *error);
ch_status ch_ss_evp_bytes_to_key(const uint8_t *password,
                                 size_t password_length, size_t key_length,
                                 uint8_t *out_key, ch_error *error);
ch_status ch_ss_hkdf_sha1(const uint8_t *secret, size_t secret_length,
                          const uint8_t *salt, size_t salt_length,
                          const uint8_t *info, size_t info_length,
                          uint8_t *out_key, size_t key_length,
                          ch_error *error);
void ch_ss_nonce_increment(uint8_t nonce[12]);
ch_status ch_ss_decrypt_length(const ch_ss_cipher *cipher,
                               const uint8_t *subkey, uint8_t nonce[12],
                               const uint8_t length_frame[18],
                               size_t *out_plaintext_length,
                               ch_error *error);
ch_status ch_ss_decrypt_payload(const ch_ss_cipher *cipher,
                                const uint8_t *subkey, uint8_t nonce[12],
                                const uint8_t *payload_frame,
                                size_t plaintext_length,
                                uint8_t *out_plaintext,
                                ch_error *error);
ch_status ch_ss_encrypt_chunk(const ch_ss_cipher *cipher,
                              const uint8_t *subkey, uint8_t nonce[12],
                              const uint8_t *plaintext,
                              size_t plaintext_length, uint8_t **out_frame,
                              size_t *out_frame_length, ch_error *error);
ch_status ch_ss_decrypt_chunk(const ch_ss_cipher *cipher,
                              const uint8_t *subkey, uint8_t nonce[12],
                              const uint8_t length_frame[18],
                              const uint8_t *payload_frame,
                              size_t payload_frame_length,
                              uint8_t **out_plaintext,
                              size_t *out_plaintext_length,
                              ch_error *error);

ch_status ch_ss_encrypt_datagram(const ch_ss_cipher *cipher,
                                 const uint8_t *master_key,
                                 const char *target,
                                 const uint8_t *payload,
                                 size_t payload_length,
                                 uint8_t **out_frame,
                                 size_t *out_frame_length,
                                 ch_error *error);
ch_status ch_ss_decrypt_datagram(const ch_ss_cipher *cipher,
                                 const uint8_t *master_key,
                                 const uint8_t *frame,
                                 size_t frame_length,
                                 char **out_source,
                                 uint8_t **out_payload,
                                 size_t *out_payload_length,
                                 ch_error *error);

ch_status ch_protocol_shadowsocks_dial(const ch_config_table *server,
                                       int underlying_descriptor,
                                       const char *target,
                                       int *out_descriptor,
                                       ch_error *error);

#endif
