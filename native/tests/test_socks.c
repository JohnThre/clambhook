// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "test.h"

#include <stdint.h>
#include <stdlib.h>

#include "clambhook/socks.h"

static void round_trip(const char *address, const char *expected_host, uint16_t expected_port, uint8_t atyp) {
    ch_error error;
    uint8_t *encoded = NULL;
    size_t encoded_length = 0U;
    CH_TEST_ASSERT(
        ch_socks_encode_address(address, &encoded, &encoded_length, &error) == CH_OK
    );
    CH_TEST_ASSERT(encoded != NULL && encoded_length >= 3U && encoded[0] == atyp);
    char *host = NULL;
    uint16_t port = 0U;
    size_t consumed = 0U;
    CH_TEST_ASSERT(
        ch_socks_decode_address(
            encoded,
            encoded_length,
            &host,
            &port,
            &consumed,
            &error
        ) == CH_OK
    );
    CH_TEST_ASSERT_STRING(expected_host, host);
    CH_TEST_ASSERT(port == expected_port && consumed == encoded_length);
    free(host);
    ch_bytes_free(encoded);
}

void ch_test_socks(void) {
    round_trip("example.com:443", "example.com", 443U, CH_SOCKS_ATYP_DOMAIN);
    round_trip("192.0.2.4:53", "192.0.2.4", 53U, CH_SOCKS_ATYP_IPV4);
    round_trip("[2001:db8::1]:8443", "2001:db8::1", 8443U, CH_SOCKS_ATYP_IPV6);

    ch_error error;
    uint8_t *encoded = NULL;
    size_t encoded_length = 0U;
    CH_TEST_ASSERT(
        ch_socks_encode_address("missing-port", &encoded, &encoded_length, &error) == CH_ERROR_PARSE
    );
    const uint8_t empty_domain[] = {CH_SOCKS_ATYP_DOMAIN, 0U, 0U, 80U};
    char *host = NULL;
    uint16_t port = 0U;
    size_t consumed = 0U;
    CH_TEST_ASSERT(
        ch_socks_decode_address(
            empty_domain,
            sizeof(empty_domain),
            &host,
            &port,
            &consumed,
            &error
        ) == CH_ERROR_PARSE
    );
    CH_TEST_ASSERT_STRING("socks: empty domain", error.message);
}
