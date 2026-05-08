//go:build ios

package cnet

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"

	"golang.org/x/crypto/chacha20poly1305"
)

func SHA224(data []byte) []byte {
	hash := sha256.Sum224(data)
	return hash[:]
}

func AES256GCMEncrypt(key, nonce, plaintext, aad []byte) (ciphertext, tag []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	sealed := aead.Seal(nil, nonce, plaintext, aad)
	tagStart := len(sealed) - aead.Overhead()
	return sealed[:tagStart], sealed[tagStart:], nil
}

func AES256GCMDecrypt(key, nonce, ciphertext, aad, tag []byte) (plaintext []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sealed := append(append([]byte(nil), ciphertext...), tag...)
	return aead.Open(nil, nonce, sealed, aad)
}

func AES256GCMAvailable() bool {
	return true
}

func ChaCha20Poly1305Encrypt(key, nonce, plaintext, aad []byte) (ciphertext, tag []byte, err error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, nil, err
	}
	sealed := aead.Seal(nil, nonce, plaintext, aad)
	tagStart := len(sealed) - aead.Overhead()
	return sealed[:tagStart], sealed[tagStart:], nil
}

func ChaCha20Poly1305Decrypt(key, nonce, ciphertext, aad, tag []byte) (plaintext []byte, err error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	sealed := append(append([]byte(nil), ciphertext...), tag...)
	return aead.Open(nil, nonce, sealed, aad)
}

func ProcessPacket(in []byte) ([]byte, error) {
	return nil, nil
}
