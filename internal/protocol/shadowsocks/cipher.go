package shadowsocks

import (
	"fmt"

	"github.com/clambhook/clambhook/pkg/cnet"
)

// cipherSpec captures the parameters and primitive AEAD operations for one
// Shadowsocks AEAD method. The dialer and stream framing layers are
// cipher-agnostic — they just call spec.encrypt / spec.decrypt with the
// per-direction subkey and counter nonce.
//
// All current SS-AEAD-2018 methods share nonceSize=12 and tagSize=16; only
// keySize and saltSize vary. We carry the constants on the struct anyway so
// a future cipher with different nonce/tag sizes wouldn't need protocol-layer
// changes.
type cipherSpec struct {
	name      string
	keySize   int
	saltSize  int
	nonceSize int
	tagSize   int
	encrypt   aeadEncryptFn
	decrypt   aeadDecryptFn
}

type aeadEncryptFn func(key, nonce, plaintext, aad []byte) (ct, tag []byte, err error)
type aeadDecryptFn func(key, nonce, ciphertext, aad, tag []byte) (pt []byte, err error)

// cipherByName resolves a Shadowsocks method name to its cipherSpec.
// AES variants check hardware availability and return a clear, actionable
// error pointing users at ChaCha20-Poly1305 if the host can't do AES-GCM.
// Legacy stream ciphers are explicitly rejected — silently treating an
// unrecognized method as "unsupported" would let users misconfigure with
// an insecure cipher and never know.
func cipherByName(name string) (*cipherSpec, error) {
	switch name {
	case "aes-128-gcm":
		// AES-128 lives in pure Go (stdlib has a software fallback), so we
		// don't gate on AES128GCMAvailable — it's always true.
		return &cipherSpec{
			name:      name,
			keySize:   16,
			saltSize:  16,
			nonceSize: 12,
			tagSize:   16,
			encrypt:   cnet.AES128GCMEncrypt,
			decrypt:   cnet.AES128GCMDecrypt,
		}, nil
	case "aes-256-gcm":
		if !cnet.AES256GCMAvailable() {
			return nil, fmt.Errorf("shadowsocks: aes-256-gcm requires hardware AES (AES-NI / ARM Crypto); use chacha20-ietf-poly1305 instead")
		}
		return &cipherSpec{
			name:      name,
			keySize:   32,
			saltSize:  32,
			nonceSize: 12,
			tagSize:   16,
			encrypt:   cnet.AES256GCMEncrypt,
			decrypt:   cnet.AES256GCMDecrypt,
		}, nil
	case "chacha20-ietf-poly1305":
		return &cipherSpec{
			name:      name,
			keySize:   32,
			saltSize:  32,
			nonceSize: 12,
			tagSize:   16,
			encrypt:   cnet.ChaCha20Poly1305Encrypt,
			decrypt:   cnet.ChaCha20Poly1305Decrypt,
		}, nil
	case "rc4-md5", "aes-128-cfb", "aes-192-cfb", "aes-256-cfb",
		"aes-128-ctr", "aes-192-ctr", "aes-256-ctr",
		"chacha20", "chacha20-ietf", "salsa20",
		"camellia-128-cfb", "camellia-192-cfb", "camellia-256-cfb",
		"bf-cfb":
		return nil, fmt.Errorf("shadowsocks: legacy stream cipher %q is insecure and not supported; use aes-128-gcm, aes-256-gcm, or chacha20-ietf-poly1305", name)
	default:
		return nil, fmt.Errorf("shadowsocks: unknown method %q (supported: aes-128-gcm, aes-256-gcm, chacha20-ietf-poly1305)", name)
	}
}
