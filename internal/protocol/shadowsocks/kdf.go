package shadowsocks

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
)

// hkdfSHA1 implements HKDF (RFC 5869) with SHA-1 as the underlying hash.
// SHA-1 is acceptable here: HKDF security relies on HMAC's PRF properties,
// not collision resistance, so the cryptanalytic weaknesses in SHA-1 don't
// affect this construction. The Shadowsocks AEAD spec mandates SHA-1.
//
// The Shadowsocks per-connection subkey is derived as:
//
//	subkey = hkdfSHA1(masterKey, salt, []byte("ss-subkey"), keyLen)
func hkdfSHA1(secret, salt, info []byte, keyLen int) []byte {
	// Extract: PRK = HMAC-SHA1(salt, secret).
	mac := hmac.New(sha1.New, salt)
	mac.Write(secret)
	prk := mac.Sum(nil)

	// Expand: T(N) = HMAC-SHA1(PRK, T(N-1) || info || N), T(0) = empty.
	out := make([]byte, 0, keyLen)
	var prev []byte
	for i := byte(1); len(out) < keyLen; i++ {
		mac = hmac.New(sha1.New, prk)
		mac.Write(prev)
		mac.Write(info)
		mac.Write([]byte{i})
		prev = mac.Sum(nil)
		out = append(out, prev...)
	}
	return out[:keyLen]
}

// evpBytesToKey reproduces OpenSSL's EVP_BytesToKey with MD5, no salt, iter=1.
// Shadowsocks (legacy AEAD-2018) uses this to stretch a user-supplied ASCII
// password into a fixed-size master key:
//
//	d_1 = MD5(password)
//	d_i = MD5(d_{i-1} || password)
//	masterKey = (d_1 || d_2 || ...)[:keyLen]
//
// MD5 is used here strictly for compatibility with the OpenSSL-derived legacy
// SS spec; it is *not* a security-critical hash in this construction (the
// password itself is the secret, not the digest).
func evpBytesToKey(password []byte, keyLen int) []byte {
	out := make([]byte, 0, keyLen)
	var prev []byte
	for len(out) < keyLen {
		h := md5.New()
		h.Write(prev)
		h.Write(password)
		prev = h.Sum(nil)
		out = append(out, prev...)
	}
	return out[:keyLen]
}
