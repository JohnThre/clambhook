//go:build unix

package cnet

import (
	"bytes"
	"fmt"
	"testing"
)

func TestAES128GCMRejectsIncorrectNonceLength(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, 16)
	plaintext := []byte("payload")
	validNonce := bytes.Repeat([]byte{0x22}, 12)
	ciphertext, tag, err := AES128GCMEncrypt(key, validNonce, plaintext, nil)
	if err != nil {
		t.Fatalf("setup encrypt: %v", err)
	}

	for _, nonceSize := range []int{11, 13} {
		t.Run(fmt.Sprintf("nonce_%d", nonceSize), func(t *testing.T) {
			nonce := bytes.Repeat([]byte{0x22}, nonceSize)
			if _, _, err := AES128GCMEncrypt(key, nonce, plaintext, nil); err == nil {
				t.Fatal("encrypt accepted incorrect nonce length")
			}
			if _, err := AES128GCMDecrypt(key, nonce, ciphertext, nil, tag); err == nil {
				t.Fatal("decrypt accepted incorrect nonce length")
			}
		})
	}
}

func TestAES256GCMRejectsNon256BitKeys(t *testing.T) {
	nonce := bytes.Repeat([]byte{0x22}, 12)
	plaintext := []byte("payload")
	tag := make([]byte, 16)

	for _, keySize := range []int{16, 24} {
		t.Run(fmt.Sprintf("key_%d", keySize), func(t *testing.T) {
			key := bytes.Repeat([]byte{0x11}, keySize)
			want := fmt.Sprintf("aes256gcm: key size %d, want 32", keySize)

			if _, _, err := AES256GCMEncrypt(key, nonce, plaintext, nil); err == nil {
				t.Fatal("encrypt accepted non-256-bit key")
			} else if err.Error() != want {
				t.Fatalf("encrypt error = %q, want %q", err, want)
			}
			if _, err := AES256GCMDecrypt(key, nonce, nil, nil, tag); err == nil {
				t.Fatal("decrypt accepted non-256-bit key")
			} else if err.Error() != want {
				t.Fatalf("decrypt error = %q, want %q", err, want)
			}
		})
	}
}
