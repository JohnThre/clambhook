#ifndef CLAMBHOOK_PROTOCOL_VMESS_H
#define CLAMBHOOK_PROTOCOL_VMESS_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "clambhook/config.h"
#include "clambhook/error.h"

typedef enum ch_vmess_security {
    CH_VMESS_AES_128_GCM = 0x03,
    CH_VMESS_CHACHA20_POLY1305 = 0x04
} ch_vmess_security;

typedef struct ch_vmess_session {
    uint8_t request_body_key[16];
    uint8_t request_body_iv[16];
    uint8_t response_body_key[16];
    uint8_t response_body_iv[16];
    uint8_t response_header;
} ch_vmess_session;

ch_status ch_vmess_parse_uuid(const char *text, uint8_t out_uuid[16],
                              ch_error *error);
ch_status ch_vmess_command_key(const uint8_t uuid[16], uint8_t out_key[16],
                               ch_error *error);
ch_status ch_vmess_kdf(const uint8_t *key, size_t key_length,
                       const uint8_t *const *paths,
                       const size_t *path_lengths, size_t path_count,
                       uint8_t out_key[32], ch_error *error);
ch_status ch_vmess_encode_address(const char *target, uint8_t **out_address,
                                  size_t *out_address_length,
                                  ch_error *error);
ch_status ch_vmess_encrypt_chunk(ch_vmess_security security,
                                 const uint8_t key[16],
                                 const uint8_t iv[16], uint16_t *counter,
                                 bool *exhausted, const uint8_t *plaintext,
                                 size_t plaintext_length,
                                 uint8_t **out_frame,
                                 size_t *out_frame_length,
                                 ch_error *error);
ch_status ch_vmess_decrypt_chunk(ch_vmess_security security,
                                 const uint8_t key[16],
                                 const uint8_t iv[16], uint16_t *counter,
                                 bool *exhausted, const uint8_t *frame,
                                 size_t frame_length,
                                 uint8_t **out_plaintext,
                                 size_t *out_plaintext_length,
                                 ch_error *error);

ch_status ch_protocol_vmess_dial(const ch_config_table *server,
                                 int underlying_descriptor,
                                 const char *target,
                                 int *out_descriptor,
                                 ch_error *error);

#endif
