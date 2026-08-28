// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: Apache-2.0

#ifndef CNET_H
#define CNET_H

#include <stddef.h>
#include <stdint.h>

/* Return codes for cnet_* functions. */
#define CNET_OK                0
#define CNET_ERR_AUTH         -1  /* AEAD authentication failure on decrypt. */
#define CNET_ERR_AES_UNAVAIL  -2  /* Retained for source compatibility. */
#define CNET_ERR_INIT         -3  /* OpenSSL cipher/context initialization failure. */

/*
 * AEAD ciphers (AES-128-GCM, AES-256-GCM and ChaCha20-Poly1305-IETF).
 *
 * Fixed sizes:
 *   key    = 16 bytes for AES-128-GCM, 32 bytes for the other ciphers
 *   nonce  = 12 bytes
 *   tag    = 16 bytes (detached; not appended to ciphertext)
 *   ciphertext buffer: >= pt_len bytes
 *   plaintext  buffer: >= ct_len bytes
 *
 * Aliasing is permitted: ciphertext and plaintext may point to the same
 * buffer for in-place encryption/decryption.
 *
 * Returns CNET_OK on success and CNET_ERR_AUTH when decrypt authentication
 * fails. OpenSSL supplies constant-time software fallbacks when hardware
 * acceleration is unavailable.
 */

int cnet_aes128gcm_encrypt(const uint8_t *key, const uint8_t *nonce,
                           const uint8_t *plaintext, size_t pt_len,
                           const uint8_t *aad, size_t aad_len,
                           uint8_t *ciphertext, uint8_t *tag);

int cnet_aes128gcm_decrypt(const uint8_t *key, const uint8_t *nonce,
                           const uint8_t *ciphertext, size_t ct_len,
                           const uint8_t *aad, size_t aad_len,
                           const uint8_t *tag,
                           uint8_t *plaintext);

/* OpenSSL provides a constant-time software fallback when hardware AES is absent. */
int cnet_aes128gcm_available(void);

int cnet_aes256gcm_encrypt(const uint8_t *key, const uint8_t *nonce,
                           const uint8_t *plaintext, size_t pt_len,
                           const uint8_t *aad, size_t aad_len,
                           uint8_t *ciphertext, uint8_t *tag);

int cnet_aes256gcm_decrypt(const uint8_t *key, const uint8_t *nonce,
                           const uint8_t *ciphertext, size_t ct_len,
                           const uint8_t *aad, size_t aad_len,
                           const uint8_t *tag,
                           uint8_t *plaintext);

/* Returns 1 when the linked OpenSSL provider exposes AES-256-GCM. */
int cnet_aes256gcm_available(void);

int cnet_chacha20poly1305_encrypt(const uint8_t *key, const uint8_t *nonce,
                                  const uint8_t *plaintext, size_t pt_len,
                                  const uint8_t *aad, size_t aad_len,
                                  uint8_t *ciphertext, uint8_t *tag);

int cnet_chacha20poly1305_decrypt(const uint8_t *key, const uint8_t *nonce,
                                  const uint8_t *ciphertext, size_t ct_len,
                                  const uint8_t *aad, size_t aad_len,
                                  const uint8_t *tag,
                                  uint8_t *plaintext);

/* SHA-224 hash (used by Trojan protocol) */
void cnet_sha224(const uint8_t *data, size_t len, uint8_t *hash);

#endif /* CNET_H */
