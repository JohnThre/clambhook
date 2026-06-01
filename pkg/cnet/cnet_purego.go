//go:build !cgo || windows || purego

package cnet

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

// SHA224 computes the SHA-224 hash of data.
func SHA224(data []byte) []byte {
	sum := sha256.Sum224(data)
	return sum[:]
}

// AES256GCMEncrypt encrypts plaintext using AES-256-GCM.
func AES256GCMEncrypt(key, nonce, plaintext, aad []byte) (ciphertext, tag []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("aes256gcm: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("aes256gcm: new gcm: %w", err)
	}
	return sealDetached("aes256gcm", gcm, nonce, plaintext, aad)
}

// AES256GCMDecrypt decrypts ciphertext using AES-256-GCM.
func AES256GCMDecrypt(key, nonce, ciphertext, aad, tag []byte) (plaintext []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes256gcm: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("aes256gcm: new gcm: %w", err)
	}
	return openDetached("aes256gcm", gcm, nonce, ciphertext, aad, tag)
}

// AES256GCMAvailable reports whether AES-256-GCM can run on this host.
func AES256GCMAvailable() bool { return true }

// ChaCha20Poly1305Encrypt encrypts plaintext using ChaCha20-Poly1305-IETF.
func ChaCha20Poly1305Encrypt(key, nonce, plaintext, aad []byte) (ciphertext, tag []byte, err error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, nil, fmt.Errorf("chacha20poly1305: new cipher: %w", err)
	}
	return sealDetached("chacha20poly1305", aead, nonce, plaintext, aad)
}

// ChaCha20Poly1305Decrypt decrypts ciphertext using ChaCha20-Poly1305-IETF.
func ChaCha20Poly1305Decrypt(key, nonce, ciphertext, aad, tag []byte) (plaintext []byte, err error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("chacha20poly1305: new cipher: %w", err)
	}
	return openDetached("chacha20poly1305", aead, nonce, ciphertext, aad, tag)
}

// ProcessPacket preserves the current packet hook behavior. The C backend is a
// placeholder that returns an empty output, so the pure-Go fallback does too.
func ProcessPacket(_ []byte) ([]byte, error) {
	return []byte{}, nil
}

func sealDetached(name string, aead cipher.AEAD, nonce, plaintext, aad []byte) (ciphertext, tag []byte, err error) {
	if len(nonce) != aead.NonceSize() {
		return nil, nil, fmt.Errorf("%s: nonce size %d, want %d", name, len(nonce), aead.NonceSize())
	}
	sealed := aead.Seal(nil, nonce, plaintext, aad)
	tagSize := aead.Overhead()
	return sealed[:len(sealed)-tagSize], sealed[len(sealed)-tagSize:], nil
}

func openDetached(name string, aead cipher.AEAD, nonce, ciphertext, aad, tag []byte) ([]byte, error) {
	if len(nonce) != aead.NonceSize() {
		return nil, fmt.Errorf("%s: nonce size %d, want %d", name, len(nonce), aead.NonceSize())
	}
	sealed := make([]byte, 0, len(ciphertext)+len(tag))
	sealed = append(sealed, ciphertext...)
	sealed = append(sealed, tag...)
	plaintext, err := aead.Open(nil, nonce, sealed, aad)
	if err != nil {
		return nil, fmt.Errorf("%s: decrypt: %w", name, err)
	}
	return plaintext, nil
}
