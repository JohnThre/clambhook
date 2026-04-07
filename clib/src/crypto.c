#include "cnet.h"
#include <string.h>

int cnet_aes256gcm_encrypt(const uint8_t *key, const uint8_t *nonce,
                           const uint8_t *plaintext, size_t pt_len,
                           const uint8_t *aad, size_t aad_len,
                           uint8_t *ciphertext, uint8_t *tag) {
    (void)key;
    (void)nonce;
    (void)plaintext;
    (void)pt_len;
    (void)aad;
    (void)aad_len;
    (void)ciphertext;
    (void)tag;
    return 0;
}

int cnet_aes256gcm_decrypt(const uint8_t *key, const uint8_t *nonce,
                           const uint8_t *ciphertext, size_t ct_len,
                           const uint8_t *aad, size_t aad_len,
                           const uint8_t *tag,
                           uint8_t *plaintext) {
    (void)key;
    (void)nonce;
    (void)ciphertext;
    (void)ct_len;
    (void)aad;
    (void)aad_len;
    (void)tag;
    (void)plaintext;
    return 0;
}

int cnet_chacha20poly1305_encrypt(const uint8_t *key, const uint8_t *nonce,
                                  const uint8_t *plaintext, size_t pt_len,
                                  const uint8_t *aad, size_t aad_len,
                                  uint8_t *ciphertext, uint8_t *tag) {
    (void)key;
    (void)nonce;
    (void)plaintext;
    (void)pt_len;
    (void)aad;
    (void)aad_len;
    (void)ciphertext;
    (void)tag;
    return 0;
}

int cnet_chacha20poly1305_decrypt(const uint8_t *key, const uint8_t *nonce,
                                  const uint8_t *ciphertext, size_t ct_len,
                                  const uint8_t *aad, size_t aad_len,
                                  const uint8_t *tag,
                                  uint8_t *plaintext) {
    (void)key;
    (void)nonce;
    (void)ciphertext;
    (void)ct_len;
    (void)aad;
    (void)aad_len;
    (void)tag;
    (void)plaintext;
    return 0;
}

void cnet_sha224(const uint8_t *data, size_t len, uint8_t *hash) {
    (void)data;
    (void)len;
    memset(hash, 0, 28);
}
