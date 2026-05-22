package shadowsocks

import (
	"strings"
	"testing"

	"github.com/JohnThre/clambhook/pkg/cnet"
)

func TestCipherByNameAES128(t *testing.T) {
	spec, err := cipherByName("aes-128-gcm")
	if err != nil {
		t.Fatal(err)
	}
	if spec.keySize != 16 || spec.saltSize != 16 || spec.nonceSize != 12 || spec.tagSize != 16 {
		t.Errorf("aes-128-gcm sizes wrong: %+v", spec)
	}
}

func TestCipherByNameAES256(t *testing.T) {
	spec, err := cipherByName("aes-256-gcm")
	if err != nil {
		// On a host without hardware AES, the error is the expected outcome.
		if !cnet.AES256GCMAvailable() {
			if !strings.Contains(err.Error(), "chacha20-ietf-poly1305") {
				t.Errorf("error should suggest chacha20 fallback, got: %v", err)
			}
			return
		}
		t.Fatal(err)
	}
	if spec.keySize != 32 || spec.saltSize != 32 {
		t.Errorf("aes-256-gcm sizes wrong: %+v", spec)
	}
}

func TestCipherByNameChaCha20(t *testing.T) {
	spec, err := cipherByName("chacha20-ietf-poly1305")
	if err != nil {
		t.Fatal(err)
	}
	if spec.keySize != 32 || spec.saltSize != 32 {
		t.Errorf("chacha20 sizes wrong: %+v", spec)
	}
}

func TestCipherByNameRejectsLegacy(t *testing.T) {
	for _, name := range []string{"rc4-md5", "aes-256-cfb", "chacha20", "salsa20"} {
		t.Run(name, func(t *testing.T) {
			_, err := cipherByName(name)
			if err == nil {
				t.Error("expected error")
			}
			if !strings.Contains(err.Error(), "insecure") {
				t.Errorf("error should call out insecurity, got: %v", err)
			}
		})
	}
}

func TestCipherByNameRejectsUnknown(t *testing.T) {
	_, err := cipherByName("totally-made-up")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown method") {
		t.Errorf("error should say unknown method, got: %v", err)
	}
}

// TestCipherEncryptDecryptRoundTrip exercises the function pointers wired
// into each cipherSpec to confirm the dispatch table matches the underlying
// AEAD primitives correctly.
func TestCipherEncryptDecryptRoundTrip(t *testing.T) {
	for _, name := range []string{"aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305"} {
		t.Run(name, func(t *testing.T) {
			spec, err := cipherByName(name)
			if err != nil {
				t.Skipf("cipher unavailable: %v", err)
			}
			key := make([]byte, spec.keySize)
			nonce := make([]byte, spec.nonceSize)
			pt := []byte("hello shadowsocks")

			ct, tag, err := spec.encrypt(key, nonce, pt, nil)
			if err != nil {
				t.Fatalf("encrypt: %v", err)
			}
			got, err := spec.decrypt(key, nonce, ct, nil, tag)
			if err != nil {
				t.Fatalf("decrypt: %v", err)
			}
			if string(got) != string(pt) {
				t.Errorf("round-trip mismatch: got %q want %q", got, pt)
			}
		})
	}
}
