// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package shadowtls

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"testing"
)

// buildClientHelloMsg constructs a minimal ClientHello handshake message (no
// record header): enough structure for signature() to locate the session id
// tail, and for a server to re-verify the HMAC the same way.
func buildClientHelloMsg() []byte {
	msg := []byte{handshakeClientHello, 0, 0, 0} // msg type + 3-byte length (unused here)
	msg = append(msg, 0x03, 0x03)                // legacy_version
	msg = append(msg, make([]byte, tlsRandomSize)...)
	for i := 0; i < tlsRandomSize; i++ {
		msg[6+i] = byte(i)
	}
	msg = append(msg, tlsSessionIDSize)                  // session id length
	msg = append(msg, make([]byte, tlsSessionIDSize)...) // session id (28 random + 4 hmac)
	msg = append(msg, 0xAA, 0xBB, 0xCC)                  // fake extensions tail
	return msg
}

func TestSignatureMatchesServerVerification(t *testing.T) {
	const password = "hunter2"
	msg := buildClientHelloMsg()

	got := signature(password, msg)

	// Reproduce v3 server-side verification: HMAC over the message with the 4
	// HMAC bytes zeroed. verifyClientHello on the server checks exactly this.
	mac := hmac.New(sha1.New, []byte(password))
	mac.Write(msg[:chSessionIDTail])
	mac.Write([]byte{0, 0, 0, 0})
	mac.Write(msg[chSessionIDTail+hmacSize:])
	want := mac.Sum(nil)[:hmacSize]

	if !hmac.Equal(got, want) {
		t.Fatalf("signature mismatch: got %x want %x", got, want)
	}
}

func TestServerHelloSupportsTLS13(t *testing.T) {
	// Build a ServerHello body (starting at msg type) with a supported_versions
	// extension (type 43) selecting TLS 1.3 (0x0304).
	build := func(selected uint16) []byte {
		var b []byte
		b = append(b, handshakeServerHello, 0, 0, 0) // type + 3-byte len
		b = append(b, 0x03, 0x03)                    // legacy_version
		b = append(b, make([]byte, tlsRandomSize)...)
		b = append(b, 0)           // session id length 0
		b = append(b, 0x13, 0x01)  // cipher suite
		b = append(b, 0x00)        // compression method
		ext := []byte{0, 43, 0, 2} // ext type 43, len 2
		ext = binary.BigEndian.AppendUint16(ext, selected)
		b = binary.BigEndian.AppendUint16(b, uint16(len(ext)))
		b = append(b, ext...)
		return b
	}

	if !serverHelloSupportsTLS13(build(0x0304)) {
		t.Error("expected TLS 1.3 detection")
	}
	if serverHelloSupportsTLS13(build(0x0303)) {
		t.Error("did not expect TLS 1.3 for 0x0303 selection")
	}
	if serverHelloSupportsTLS13([]byte{handshakeServerHello}) {
		t.Error("short frame must not report TLS 1.3")
	}
}

func TestKDFDeterministic(t *testing.T) {
	sr := make([]byte, tlsRandomSize)
	for i := range sr {
		sr[i] = byte(i)
	}
	a := kdf("pw", sr)
	b := kdf("pw", sr)
	if len(a) != 32 {
		t.Fatalf("kdf len = %d, want 32", len(a))
	}
	if !hmac.Equal(a, b) {
		t.Error("kdf not deterministic")
	}
	if hmac.Equal(a, kdf("other", sr)) {
		t.Error("kdf must depend on password")
	}
}

func TestXORSliceRoundTrip(t *testing.T) {
	key := kdf("pw", make([]byte, tlsRandomSize))
	data := []byte("the quick brown fox jumps over the lazy dog")
	orig := append([]byte(nil), data...)
	xorSlice(data, key)
	if string(data) == string(orig) {
		t.Fatal("xor produced identical bytes")
	}
	xorSlice(data, key)
	if string(data) != string(orig) {
		t.Fatalf("xor round-trip mismatch: %q", data)
	}
}
