#ifndef CNET_H
#define CNET_H

#include <stddef.h>
#include <stdint.h>

/* Packet processing */
int cnet_process_packet(const uint8_t *in, size_t in_len,
                        uint8_t *out, size_t out_cap, size_t *out_len);

/* AES-256-GCM encrypt/decrypt */
int cnet_aes256gcm_encrypt(const uint8_t *key, const uint8_t *nonce,
                           const uint8_t *plaintext, size_t pt_len,
                           const uint8_t *aad, size_t aad_len,
                           uint8_t *ciphertext, uint8_t *tag);

int cnet_aes256gcm_decrypt(const uint8_t *key, const uint8_t *nonce,
                           const uint8_t *ciphertext, size_t ct_len,
                           const uint8_t *aad, size_t aad_len,
                           const uint8_t *tag,
                           uint8_t *plaintext);

/* ChaCha20-Poly1305 encrypt/decrypt */
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

/* Buffer pool for zero-copy packet handling */
typedef struct cnet_buf {
    uint8_t *data;
    size_t   len;
    size_t   cap;
} cnet_buf_t;

cnet_buf_t *cnet_buf_alloc(size_t cap);
void        cnet_buf_free(cnet_buf_t *buf);

#endif /* CNET_H */
