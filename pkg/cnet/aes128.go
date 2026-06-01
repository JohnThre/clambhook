//go:build unix

package cnet

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
)

// AES-128-GCM lives in pure Go (crypto/aes + crypto/cipher) because libsodium
// intentionally omits 128-bit AES-GCM. Go's stdlib implementation uses AES-NI
// / ARM Crypto Extensions when available, so performance is comparable to
// the libsodium-backed AES-256-GCM in pkg/cnet/cnet.go.
//
// API mirrors the AES256GCMEncrypt/Decrypt/Available shape so the rest of the
// codebase can dispatch on cipher choice without caring where the impl lives.

// AES128GCMEncrypt encrypts plaintext using AES-128-GCM with detached tag.
func AES128GCMEncrypt(key, nonce, plaintext, aad []byte) (ciphertext, tag []byte, err error) {
	gcm, err := newAES128GCM(key)
	if err != nil {
		return nil, nil, err
	}
	// Seal returns ciphertext||tag concatenated; split for detached-tag API.
	sealed := gcm.Seal(nil, nonce, plaintext, aad)
	tagSize := gcm.Overhead()
	return sealed[:len(sealed)-tagSize], sealed[len(sealed)-tagSize:], nil
}

// AES128GCMDecrypt decrypts ciphertext using AES-128-GCM with detached tag.
func AES128GCMDecrypt(key, nonce, ciphertext, aad, tag []byte) (plaintext []byte, err error) {
	gcm, err := newAES128GCM(key)
	if err != nil {
		return nil, err
	}
	// Recombine ciphertext||tag for the stdlib Open call.
	sealed := make([]byte, 0, len(ciphertext)+len(tag))
	sealed = append(sealed, ciphertext...)
	sealed = append(sealed, tag...)
	return gcm.Open(nil, nonce, sealed, aad)
}

// AES128GCMAvailable reports whether AES-128-GCM can run. Always true on
// platforms where Go runs — the stdlib has a software fallback when hardware
// AES isn't present. Kept as a function for API symmetry with AES256GCMAvailable.
func AES128GCMAvailable() bool { return true }

func newAES128GCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 16 {
		return nil, fmt.Errorf("aes128gcm: key size %d, want 16", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes128gcm: new cipher: %w", err)
	}
	return cipher.NewGCM(block)
}
