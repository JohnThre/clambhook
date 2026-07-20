//go:build unix

package cnet

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex literal: %v", err)
	}
	return b
}

func TestSHA224(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "empty string",
			input:  "",
			expect: "d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f",
		},
		{
			name:   "abc",
			input:  "abc",
			expect: "23097d223405d8228642a477bda255b32aadbce4bda0b3f7e36c9da7",
		},
		{
			name:   "448-bit message",
			input:  "abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq",
			expect: "75388b16512776cc5dba5da1fd890150b0c6455cb4f58b1952522525",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := hex.EncodeToString(SHA224([]byte(tc.input)))
			if got != tc.expect {
				t.Errorf("SHA224(%q)\n  got  %s\n  want %s", tc.input, got, tc.expect)
			}
		})
	}
}

// TestAES128GCM uses NIST SP 800-38D Test Case 4: 128-bit key, 96-bit IV,
// 60-byte plaintext, 20-byte AAD. Verifies the new AES-128 path against the
// canonical NIST vector (and validates the cgo bridge handles AAD correctly).
func TestAES128GCM(t *testing.T) {
	if !AES128GCMAvailable() {
		t.Skip("AES-128-GCM not available on this host (no AES-NI / ARM Crypto)")
	}

	key := mustHex(t, "feffe9928665731c6d6a8f9467308308")
	nonce := mustHex(t, "cafebabefacedbaddecaf888")
	aad := mustHex(t, "feedfacedeadbeeffeedfacedeadbeefabaddad2")
	plaintext := mustHex(t,
		"d9313225f88406e5a55909c5aff5269a"+
			"86a7a9531534f7da2e4c303d8a318a72"+
			"1c3c0c95956809532fcf0e2449a6b525"+
			"b16aedf5aa0de657ba637b39")
	wantCT := mustHex(t,
		"42831ec2217774244b7221b784d0d49c"+
			"e3aa212f2c02a4e035c17e2329aca12e"+
			"21d514b25466931c7d8f6a5aac84aa05"+
			"1ba30b396a0aac973d58e091")
	wantTag := mustHex(t, "5bc94fbc3221a5db94fae95ae7121a47")

	ct, tag, err := AES128GCMEncrypt(key, nonce, plaintext, aad)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !bytes.Equal(ct, wantCT) {
		t.Errorf("ciphertext mismatch\n  got  %x\n  want %x", ct, wantCT)
	}
	if !bytes.Equal(tag, wantTag) {
		t.Errorf("tag mismatch\n  got  %x\n  want %x", tag, wantTag)
	}

	pt, err := AES128GCMDecrypt(key, nonce, ct, aad, tag)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Errorf("round-trip plaintext mismatch\n  got  %x\n  want %x", pt, plaintext)
	}
}

func TestAES128GCMTamper(t *testing.T) {
	if !AES128GCMAvailable() {
		t.Skip("AES-128-GCM not available on this host")
	}
	key := bytes.Repeat([]byte{0xaa}, 16)
	nonce := bytes.Repeat([]byte{0xbb}, 12)
	plaintext := []byte("sensitive payload")

	ct, tag, err := AES128GCMEncrypt(key, nonce, plaintext, nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	ct[0] ^= 1
	if _, err := AES128GCMDecrypt(key, nonce, ct, nil, tag); err == nil {
		t.Fatal("decrypt unexpectedly succeeded on tampered ciphertext")
	}
}

// TestAES256GCM uses NIST SP 800-38D Test Case 16: 256-bit key, 96-bit IV,
// 64-byte plaintext, no AAD.
func TestAES256GCM(t *testing.T) {
	if !AES256GCMAvailable() {
		t.Skip("AES-256-GCM not available on this host (no AES-NI / ARM Crypto)")
	}

	key := mustHex(t, "feffe9928665731c6d6a8f9467308308feffe9928665731c6d6a8f9467308308")
	nonce := mustHex(t, "cafebabefacedbaddecaf888")
	plaintext := mustHex(t,
		"d9313225f88406e5a55909c5aff5269a"+
			"86a7a9531534f7da2e4c303d8a318a72"+
			"1c3c0c95956809532fcf0e2449a6b525"+
			"b16aedf5aa0de657ba637b391aafd255")
	wantCT := mustHex(t,
		"522dc1f099567d07f47f37a32a84427d"+
			"643a8cdcbfe5c0c97598a2bd2555d1aa"+
			"8cb08e48590dbb3da7b08b1056828838"+
			"c5f61e6393ba7a0abcc9f662898015ad")
	wantTag := mustHex(t, "b094dac5d93471bdec1a502270e3cc6c")

	ct, tag, err := AES256GCMEncrypt(key, nonce, plaintext, nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !bytes.Equal(ct, wantCT) {
		t.Errorf("ciphertext mismatch\n  got  %x\n  want %x", ct, wantCT)
	}
	if !bytes.Equal(tag, wantTag) {
		t.Errorf("tag mismatch\n  got  %x\n  want %x", tag, wantTag)
	}

	pt, err := AES256GCMDecrypt(key, nonce, ct, nil, tag)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Errorf("round-trip plaintext mismatch\n  got  %x\n  want %x", pt, plaintext)
	}
}

func TestAES256GCMTamper(t *testing.T) {
	if !AES256GCMAvailable() {
		t.Skip("AES-256-GCM not available on this host")
	}
	key := bytes.Repeat([]byte{0xaa}, 32)
	nonce := bytes.Repeat([]byte{0xbb}, 12)
	plaintext := []byte("sensitive payload")

	ct, tag, err := AES256GCMEncrypt(key, nonce, plaintext, nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	ct[0] ^= 1
	if _, err := AES256GCMDecrypt(key, nonce, ct, nil, tag); err == nil {
		t.Fatal("decrypt unexpectedly succeeded on tampered ciphertext")
	}
}

// TestChaCha20Poly1305RFC7539 verifies against RFC 7539 section 2.8.2.
func TestChaCha20Poly1305RFC7539(t *testing.T) {
	key := mustHex(t, "808182838485868788898a8b8c8d8e8f909192939495969798999a9b9c9d9e9f")
	nonce := mustHex(t, "070000004041424344454647")
	aad := mustHex(t, "50515253c0c1c2c3c4c5c6c7")
	plaintext := []byte("Ladies and Gentlemen of the class of '99: If I could offer you only one tip for the future, sunscreen would be it.")
	wantCT := mustHex(t,
		"d31a8d34648e60db7b86afbc53ef7ec2"+
			"a4aded51296e08fea9e2b5a736ee62d6"+
			"3dbea45e8ca9671282fafb69da92728b"+
			"1a71de0a9e060b2905d6a5b67ecd3b36"+
			"92ddbd7f2d778b8c9803aee328091b58"+
			"fab324e4fad675945585808b4831d7bc"+
			"3ff4def08e4b7a9de576d26586cec64b"+
			"6116")
	wantTag := mustHex(t, "1ae10b594f09e26a7e902ecbd0600691")

	ct, tag, err := ChaCha20Poly1305Encrypt(key, nonce, plaintext, aad)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !bytes.Equal(ct, wantCT) {
		t.Errorf("ciphertext mismatch\n  got  %x\n  want %x", ct, wantCT)
	}
	if !bytes.Equal(tag, wantTag) {
		t.Errorf("tag mismatch\n  got  %x\n  want %x", tag, wantTag)
	}

	pt, err := ChaCha20Poly1305Decrypt(key, nonce, ct, aad, tag)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Errorf("round-trip plaintext mismatch")
	}
}

func TestChaCha20Poly1305Tamper(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, 32)
	nonce := bytes.Repeat([]byte{0x22}, 12)
	plaintext := []byte("sensitive payload")

	ct, tag, err := ChaCha20Poly1305Encrypt(key, nonce, plaintext, nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	ct[0] ^= 1
	if _, err := ChaCha20Poly1305Decrypt(key, nonce, ct, nil, tag); err == nil {
		t.Fatal("decrypt unexpectedly succeeded on tampered ciphertext")
	}
}

// TestAEADInvalidLengths verifies that every cgo/purego AEAD entrypoint rejects
// wrong-length key/nonce/tag inputs with an error instead of dereferencing
// &slice[0] on an empty or short slice (which would panic / corrupt memory).
// The table is valid under both the default cgo build and `-tags purego`,
// since both implementations must honor the same key=32/nonce=12/tag=16
// contract.
func TestAEADInvalidLengths(t *testing.T) {
	const (
		keySize   = 32
		nonceSize = 12
		tagSize   = 16
	)
	goodKey := bytes.Repeat([]byte{0x01}, keySize)
	goodNonce := bytes.Repeat([]byte{0x02}, nonceSize)
	pt := []byte("payload")

	type sizes struct{ key, nonce, tag int }
	bad := []struct {
		name string
		sizes
	}{
		{"empty key", sizes{0, nonceSize, tagSize}},
		{"short key", sizes{keySize - 1, nonceSize, tagSize}},
		{"long key", sizes{keySize + 1, nonceSize, tagSize}},
		{"empty nonce", sizes{keySize, 0, tagSize}},
		{"short nonce", sizes{keySize, nonceSize - 1, tagSize}},
		{"long nonce", sizes{keySize, nonceSize + 1, tagSize}},
		{"empty tag", sizes{keySize, nonceSize, 0}},
		{"short tag", sizes{keySize, nonceSize, tagSize - 1}},
		{"long tag", sizes{keySize, nonceSize, tagSize + 1}},
	}

	type cipher struct {
		name    string
		encrypt func(key, nonce, plaintext, aad []byte) ([]byte, []byte, error)
		decrypt func(key, nonce, ciphertext, aad, tag []byte) ([]byte, error)
	}
	ciphers := []cipher{
		{"aes256gcm", AES256GCMEncrypt, AES256GCMDecrypt},
		{"chacha20poly1305", ChaCha20Poly1305Encrypt, ChaCha20Poly1305Decrypt},
	}

	for _, c := range ciphers {
		for _, tc := range bad {
			key := bytes.Repeat([]byte{0x01}, tc.key)
			nonce := bytes.Repeat([]byte{0x02}, tc.nonce)
			tag := bytes.Repeat([]byte{0x03}, tc.tag)

			// Encrypt ignores tag length (it produces the tag), so only run
			// encrypt cases where the key or nonce is the invalid dimension.
			if tc.key != keySize || tc.nonce != nonceSize {
				t.Run(c.name+"/encrypt/"+tc.name, func(t *testing.T) {
					if _, _, err := c.encrypt(key, nonce, pt, nil); err == nil {
						t.Fatalf("%s encrypt accepted bad input (key=%d nonce=%d)",
							c.name, tc.key, tc.nonce)
					}
				})
			}
			t.Run(c.name+"/decrypt/"+tc.name, func(t *testing.T) {
				if _, err := c.decrypt(key, nonce, pt, nil, tag); err == nil {
					t.Fatalf("%s decrypt accepted bad input (key=%d nonce=%d tag=%d)",
						c.name, tc.key, tc.nonce, tc.tag)
				}
			})
		}
	}

	// Sanity: the good lengths must still succeed, so the rejections above are
	// about length and not a blanket failure.
	for _, c := range ciphers {
		if c.name == "aes256gcm" && !AES256GCMAvailable() {
			continue
		}
		ct, tag, err := c.encrypt(goodKey, goodNonce, pt, nil)
		if err != nil {
			t.Fatalf("%s encrypt with valid lengths failed: %v", c.name, err)
		}
		if _, err := c.decrypt(goodKey, goodNonce, ct, nil, tag); err != nil {
			t.Fatalf("%s decrypt with valid lengths failed: %v", c.name, err)
		}
	}
}

// TestAEADEmptyPlaintext exercises the empty-plaintext / empty-ciphertext path,
// which must not dereference &slice[0] on the zero-length buffer. The tag is a
// known-answer vector (all-zero key + nonce) computed from Go's crypto/aes and
// x/crypto/chacha20poly1305, so it holds identically under the cgo and purego
// builds — pinning cgo-vs-purego equivalence on the empty input.
func TestAEADEmptyPlaintext(t *testing.T) {
	key := make([]byte, 32)
	nonce := make([]byte, 12)

	cases := []struct {
		name    string
		avail   func() bool
		encrypt func(key, nonce, plaintext, aad []byte) ([]byte, []byte, error)
		decrypt func(key, nonce, ciphertext, aad, tag []byte) ([]byte, error)
		wantTag string
	}{
		{"aes256gcm", AES256GCMAvailable, AES256GCMEncrypt, AES256GCMDecrypt,
			"530f8afbc74536b9a963b4f1c4cb738b"},
		{"chacha20poly1305", func() bool { return true },
			ChaCha20Poly1305Encrypt, ChaCha20Poly1305Decrypt,
			"4eb972c9a8fb3a1b382bb4d36f5ffad1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.avail() {
				t.Skipf("%s not available on this host", tc.name)
			}
			ct, tag, err := tc.encrypt(key, nonce, nil, nil)
			if err != nil {
				t.Fatalf("encrypt empty: %v", err)
			}
			if len(ct) != 0 {
				t.Errorf("ciphertext for empty plaintext: got %d bytes, want 0", len(ct))
			}
			if got := mustHex(t, tc.wantTag); !bytes.Equal(tag, got) {
				t.Errorf("tag mismatch\n  got  %x\n  want %s", tag, tc.wantTag)
			}
			pt, err := tc.decrypt(key, nonce, nil, nil, tag)
			if err != nil {
				t.Fatalf("decrypt empty: %v", err)
			}
			if len(pt) != 0 {
				t.Errorf("plaintext for empty ciphertext: got %d bytes, want 0", len(pt))
			}
		})
	}
}
