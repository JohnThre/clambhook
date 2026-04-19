package vmess

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

// newBodyAEAD returns the AEAD used for per-chunk sealing/opening of VMess
// body data (after the header). For AES-128-GCM the key is the raw 16-byte
// reqKey; for ChaCha20-Poly1305, VMess mandates a re-derivation that expands
// the 16B key into a 32B one (see chacha20Key below).
func newBodyAEAD(security byte, key []byte) (cipher.AEAD, error) {
	switch security {
	case securityAES128GCM:
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, fmt.Errorf("vmess: aes cipher: %w", err)
		}
		return cipher.NewGCM(block)
	case securityChaCha20Poly1305:
		return chacha20poly1305.New(chacha20Key(key))
	default:
		return nil, fmt.Errorf("vmess: unsupported security %#x", security)
	}
}

// chacha20Key expands a 16-byte VMess body key into the 32-byte ChaCha20-
// Poly1305 key per the V2Fly convention:
//
//	left  = MD5(key)
//	right = MD5(left)
//	out   = left || right
//
// This is intentionally not a real KDF — it was chosen in V2Ray's pre-AEAD
// era to stretch the 16-byte credential up to ChaCha20's 32-byte key size.
// It's deterministic and bijective, which is all VMess needs here.
func chacha20Key(key []byte) []byte {
	left := md5.Sum(key)
	right := md5.Sum(left[:])
	out := make([]byte, 32)
	copy(out, left[:])
	copy(out[16:], right[:])
	return out
}
