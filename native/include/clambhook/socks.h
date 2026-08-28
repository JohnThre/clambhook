// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_SOCKS_H
#define CLAMBHOOK_SOCKS_H

#include <stddef.h>
#include <stdint.h>

#include "clambhook/error.h"

#ifdef __cplusplus
extern "C" {
#endif

enum {
    CH_SOCKS_ATYP_IPV4 = 0x01,
    CH_SOCKS_ATYP_DOMAIN = 0x03,
    CH_SOCKS_ATYP_IPV6 = 0x04
};

ch_status ch_socks_encode_address(
    const char *address,
    uint8_t **encoded,
    size_t *encoded_length,
    ch_error *error
);

ch_status ch_socks_decode_address(
    const uint8_t *encoded,
    size_t encoded_length,
    char **host,
    uint16_t *port,
    size_t *consumed,
    ch_error *error
);

void ch_bytes_free(uint8_t *bytes);

#ifdef __cplusplus
}
#endif

#endif
