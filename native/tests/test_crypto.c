// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "test.h"

#include <stdint.h>
#include <string.h>

#include "cnet.h"

static int from_hex(const char *hex, uint8_t *output, size_t capacity) {
    size_t length = strlen(hex);
    if (length % 2U != 0U || length / 2U > capacity) return -1;
    for (size_t index = 0U; index < length / 2U; ++index) {
        unsigned value = 0U;
        if (sscanf(hex + index * 2U, "%2x", &value) != 1) return -1;
        output[index] = (uint8_t)value;
    }
    return (int)(length / 2U);
}

void ch_test_crypto(void) {
    uint8_t digest[28];
    static const uint8_t expected_sha224[28] = {
        0x23,0x09,0x7d,0x22,0x34,0x05,0xd8,0x22,0x86,0x42,0xa4,0x77,0xbd,0xa2,
        0x55,0xb3,0x2a,0xad,0xbc,0xe4,0xbd,0xa0,0xb3,0xf7,0xe3,0x6c,0x9d,0xa7
    };
    cnet_sha224((const uint8_t *)"abc", 3U, digest);
    CH_TEST_ASSERT(memcmp(digest, expected_sha224, sizeof(digest)) == 0);

    uint8_t key[16], nonce[12], aad[20], plaintext[60], expected_ciphertext[60], expected_tag[16];
    CH_TEST_ASSERT(from_hex("feffe9928665731c6d6a8f9467308308", key, sizeof(key)) == 16);
    CH_TEST_ASSERT(from_hex("cafebabefacedbaddecaf888", nonce, sizeof(nonce)) == 12);
    CH_TEST_ASSERT(from_hex("feedfacedeadbeeffeedfacedeadbeefabaddad2", aad, sizeof(aad)) == 20);
    CH_TEST_ASSERT(from_hex(
        "d9313225f88406e5a55909c5aff5269a86a7a9531534f7da2e4c303d8a318a72"
        "1c3c0c95956809532fcf0e2449a6b525b16aedf5aa0de657ba637b39",
        plaintext, sizeof(plaintext)) == 60);
    CH_TEST_ASSERT(from_hex(
        "42831ec2217774244b7221b784d0d49ce3aa212f2c02a4e035c17e2329aca12e"
        "21d514b25466931c7d8f6a5aac84aa051ba30b396a0aac973d58e091",
        expected_ciphertext, sizeof(expected_ciphertext)) == 60);
    CH_TEST_ASSERT(from_hex("5bc94fbc3221a5db94fae95ae7121a47", expected_tag, sizeof(expected_tag)) == 16);
    uint8_t ciphertext[60], tag[16], recovered[60];
    CH_TEST_ASSERT(cnet_aes128gcm_encrypt(
        key, nonce, plaintext, sizeof(plaintext), aad, sizeof(aad), ciphertext, tag
    ) == CNET_OK);
    CH_TEST_ASSERT(memcmp(ciphertext, expected_ciphertext, sizeof(ciphertext)) == 0);
    CH_TEST_ASSERT(memcmp(tag, expected_tag, sizeof(tag)) == 0);
    CH_TEST_ASSERT(cnet_aes128gcm_decrypt(
        key, nonce, ciphertext, sizeof(ciphertext), aad, sizeof(aad), tag, recovered
    ) == CNET_OK);
    CH_TEST_ASSERT(memcmp(recovered, plaintext, sizeof(recovered)) == 0);
    tag[0] ^= 1U;
    CH_TEST_ASSERT(cnet_aes128gcm_decrypt(
        key, nonce, ciphertext, sizeof(ciphertext), aad, sizeof(aad), tag, recovered
    ) == CNET_ERR_AUTH);
}
