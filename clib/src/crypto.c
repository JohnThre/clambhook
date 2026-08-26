#include "cnet.h"
#include <limits.h>
#include <openssl/evp.h>
#include <stdlib.h>
#include <string.h>

int cnet_aes256gcm_available(void) {
    return EVP_aes_256_gcm() != NULL;
}

int cnet_aes128gcm_available(void) {
    return EVP_aes_128_gcm() != NULL;
}

static int cnet_aead_encrypt(const EVP_CIPHER *cipher,
                             const uint8_t *key, const uint8_t *nonce,
                             const uint8_t *plaintext, size_t pt_len,
                             const uint8_t *aad, size_t aad_len,
                             uint8_t *ciphertext, uint8_t *tag) {
    if (cipher == NULL || key == NULL || nonce == NULL || tag == NULL ||
        (pt_len > 0 && (plaintext == NULL || ciphertext == NULL)) ||
        (aad_len > 0 && aad == NULL) || pt_len > INT_MAX ||
        aad_len > INT_MAX) {
        return CNET_ERR_INIT;
    }
    EVP_CIPHER_CTX *ctx = EVP_CIPHER_CTX_new();
    if (ctx == NULL) return CNET_ERR_INIT;
    int written = 0;
    int final_written = 0;
    int ok = EVP_EncryptInit_ex(ctx, cipher, NULL, NULL, NULL) == 1 &&
             EVP_CIPHER_CTX_ctrl(ctx, EVP_CTRL_AEAD_SET_IVLEN, 12, NULL) == 1 &&
             EVP_EncryptInit_ex(ctx, NULL, NULL, key, nonce) == 1;
    if (ok && aad_len > 0) {
        ok = EVP_EncryptUpdate(ctx, NULL, &written, aad, (int)aad_len) == 1;
    }
    if (ok && pt_len > 0) {
        ok = EVP_EncryptUpdate(ctx, ciphertext, &written, plaintext,
                               (int)pt_len) == 1;
    }
    if (ok) {
        uint8_t scratch[16];
        uint8_t *output = pt_len > 0 ? ciphertext + written : scratch;
        ok = EVP_EncryptFinal_ex(ctx, output, &final_written) == 1 &&
             EVP_CIPHER_CTX_ctrl(ctx, EVP_CTRL_AEAD_GET_TAG, 16, tag) == 1;
    }
    EVP_CIPHER_CTX_free(ctx);
    return ok ? CNET_OK : CNET_ERR_INIT;
}

static int cnet_aead_decrypt(const EVP_CIPHER *cipher,
                             const uint8_t *key, const uint8_t *nonce,
                             const uint8_t *ciphertext, size_t ct_len,
                             const uint8_t *aad, size_t aad_len,
                             const uint8_t *tag, uint8_t *plaintext) {
    if (cipher == NULL || key == NULL || nonce == NULL || tag == NULL ||
        (ct_len > 0 && (ciphertext == NULL || plaintext == NULL)) ||
        (aad_len > 0 && aad == NULL) || ct_len > INT_MAX ||
        aad_len > INT_MAX) {
        return CNET_ERR_INIT;
    }
    EVP_CIPHER_CTX *ctx = EVP_CIPHER_CTX_new();
    if (ctx == NULL) return CNET_ERR_INIT;
    int written = 0;
    int final_written = 0;
    int ok = EVP_DecryptInit_ex(ctx, cipher, NULL, NULL, NULL) == 1 &&
             EVP_CIPHER_CTX_ctrl(ctx, EVP_CTRL_AEAD_SET_IVLEN, 12, NULL) == 1 &&
             EVP_DecryptInit_ex(ctx, NULL, NULL, key, nonce) == 1;
    if (ok && aad_len > 0) {
        ok = EVP_DecryptUpdate(ctx, NULL, &written, aad, (int)aad_len) == 1;
    }
    if (ok && ct_len > 0) {
        ok = EVP_DecryptUpdate(ctx, plaintext, &written, ciphertext,
                               (int)ct_len) == 1;
    }
    if (ok) {
        ok = EVP_CIPHER_CTX_ctrl(ctx, EVP_CTRL_AEAD_SET_TAG, 16,
                                 (void *)tag) == 1;
    }
    if (ok) {
        uint8_t scratch[16];
        uint8_t *output = ct_len > 0 ? plaintext + written : scratch;
        ok = EVP_DecryptFinal_ex(ctx, output, &final_written) == 1;
    }
    EVP_CIPHER_CTX_free(ctx);
    return ok ? CNET_OK : CNET_ERR_AUTH;
}

int cnet_aes128gcm_encrypt(const uint8_t *key, const uint8_t *nonce,
                           const uint8_t *plaintext, size_t pt_len,
                           const uint8_t *aad, size_t aad_len,
                           uint8_t *ciphertext, uint8_t *tag) {
    return cnet_aead_encrypt(EVP_aes_128_gcm(), key, nonce, plaintext,
                             pt_len, aad, aad_len, ciphertext, tag);
}

int cnet_aes128gcm_decrypt(const uint8_t *key, const uint8_t *nonce,
                           const uint8_t *ciphertext, size_t ct_len,
                           const uint8_t *aad, size_t aad_len,
                           const uint8_t *tag,
                           uint8_t *plaintext) {
    return cnet_aead_decrypt(EVP_aes_128_gcm(), key, nonce, ciphertext,
                             ct_len, aad, aad_len, tag, plaintext);
}

int cnet_aes256gcm_encrypt(const uint8_t *key, const uint8_t *nonce,
                           const uint8_t *plaintext, size_t pt_len,
                           const uint8_t *aad, size_t aad_len,
                           uint8_t *ciphertext, uint8_t *tag) {
    return cnet_aead_encrypt(EVP_aes_256_gcm(), key, nonce, plaintext,
                             pt_len, aad, aad_len, ciphertext, tag);
}

int cnet_aes256gcm_decrypt(const uint8_t *key, const uint8_t *nonce,
                           const uint8_t *ciphertext, size_t ct_len,
                           const uint8_t *aad, size_t aad_len,
                           const uint8_t *tag,
                           uint8_t *plaintext) {
    return cnet_aead_decrypt(EVP_aes_256_gcm(), key, nonce, ciphertext,
                             ct_len, aad, aad_len, tag, plaintext);
}

int cnet_chacha20poly1305_encrypt(const uint8_t *key, const uint8_t *nonce,
                                  const uint8_t *plaintext, size_t pt_len,
                                  const uint8_t *aad, size_t aad_len,
                                  uint8_t *ciphertext, uint8_t *tag) {
    return cnet_aead_encrypt(EVP_chacha20_poly1305(), key, nonce, plaintext,
                             pt_len, aad, aad_len, ciphertext, tag);
}

int cnet_chacha20poly1305_decrypt(const uint8_t *key, const uint8_t *nonce,
                                  const uint8_t *ciphertext, size_t ct_len,
                                  const uint8_t *aad, size_t aad_len,
                                  const uint8_t *tag,
                                  uint8_t *plaintext) {
    return cnet_aead_decrypt(EVP_chacha20_poly1305(), key, nonce, ciphertext,
                             ct_len, aad, aad_len, tag, plaintext);
}

/* SHA-256 round constants: first 32 bits of the fractional parts of
   the cube roots of the first 64 primes (2..311). */
static const uint32_t K256[64] = {
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5,
    0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
    0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc,
    0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7,
    0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
    0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3,
    0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5,
    0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
    0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
};

/* SHA-224 initial hash values: first 32 bits of the fractional parts of
   the square roots of the 23rd through 30th primes (83..127). */
static const uint32_t H224[8] = {
    0xc1059ed8, 0x367cd507, 0x3070dd17, 0xf70e5939,
    0xffc00b31, 0x68581511, 0x64f98fa7, 0xbefa4fa4,
};

#define ROTR(x, n) (((x) >> (n)) | ((x) << (32 - (n))))
#define CH(x, y, z)  (((x) & (y)) ^ (~(x) & (z)))
#define MAJ(x, y, z) (((x) & (y)) ^ ((x) & (z)) ^ ((y) & (z)))
#define EP0(x)  (ROTR(x, 2)  ^ ROTR(x, 13) ^ ROTR(x, 22))
#define EP1(x)  (ROTR(x, 6)  ^ ROTR(x, 11) ^ ROTR(x, 25))
#define SIG0(x) (ROTR(x, 7)  ^ ROTR(x, 18) ^ ((x) >> 3))
#define SIG1(x) (ROTR(x, 17) ^ ROTR(x, 19) ^ ((x) >> 10))

static void sha256_compress(uint32_t state[8], const uint8_t block[64]) {
    uint32_t w[64];
    uint32_t a, b, c, d, e, f, g, h;
    uint32_t t1, t2;
    int i;

    for (i = 0; i < 16; i++) {
        w[i] = ((uint32_t)block[i * 4]     << 24) |
               ((uint32_t)block[i * 4 + 1] << 16) |
               ((uint32_t)block[i * 4 + 2] <<  8) |
               ((uint32_t)block[i * 4 + 3]);
    }
    for (i = 16; i < 64; i++) {
        w[i] = SIG1(w[i - 2]) + w[i - 7] + SIG0(w[i - 15]) + w[i - 16];
    }

    a = state[0]; b = state[1]; c = state[2]; d = state[3];
    e = state[4]; f = state[5]; g = state[6]; h = state[7];

    for (i = 0; i < 64; i++) {
        t1 = h + EP1(e) + CH(e, f, g) + K256[i] + w[i];
        t2 = EP0(a) + MAJ(a, b, c);
        h = g; g = f; f = e; e = d + t1;
        d = c; c = b; b = a; a = t1 + t2;
    }

    state[0] += a; state[1] += b; state[2] += c; state[3] += d;
    state[4] += e; state[5] += f; state[6] += g; state[7] += h;
}

void cnet_sha224(const uint8_t *data, size_t len, uint8_t *hash) {
    uint32_t state[8];
    uint8_t block[64];
    size_t i, remaining, blocks;
    uint64_t bitlen;

    for (i = 0; i < 8; i++)
        state[i] = H224[i];

    bitlen = (uint64_t)len * 8;
    blocks = len / 64;

    for (i = 0; i < blocks; i++)
        sha256_compress(state, data + i * 64);

    remaining = len % 64;
    memset(block, 0, 64);
    if (remaining)
        memcpy(block, data + blocks * 64, remaining);

    block[remaining] = 0x80;

    if (remaining >= 56) {
        sha256_compress(state, block);
        memset(block, 0, 64);
    }

    block[56] = (uint8_t)(bitlen >> 56);
    block[57] = (uint8_t)(bitlen >> 48);
    block[58] = (uint8_t)(bitlen >> 40);
    block[59] = (uint8_t)(bitlen >> 32);
    block[60] = (uint8_t)(bitlen >> 24);
    block[61] = (uint8_t)(bitlen >> 16);
    block[62] = (uint8_t)(bitlen >>  8);
    block[63] = (uint8_t)(bitlen);
    sha256_compress(state, block);

    for (i = 0; i < 7; i++) {
        hash[i * 4]     = (uint8_t)(state[i] >> 24);
        hash[i * 4 + 1] = (uint8_t)(state[i] >> 16);
        hash[i * 4 + 2] = (uint8_t)(state[i] >>  8);
        hash[i * 4 + 3] = (uint8_t)(state[i]);
    }
}
