package reality

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// TestClient_EmitsWellFormedClientHello drives Client() against a
// net.Pipe peer. It cannot complete the handshake (the peer speaks no
// TLS), so we only assert the wire-level shape of the ClientHello —
// specifically, that Reality's session_id stuffing landed at the right
// byte offset. The crypto content of the session_id is covered by
// session_test.go's round-trip test.
func TestClient_EmitsWellFormedClientHello(t *testing.T) {
	cliSide, srvSide := net.Pipe()
	defer cliSide.Close()
	defer srvSide.Close()

	// Garbage X25519 pub — we don't complete the handshake, so it's fine.
	// (An invalid X25519 point would be rejected at NewPublicKey, so use
	// a canonical random 32-byte value which X25519 will accept.)
	var pubkey [32]byte
	rand.Read(pubkey[:])

	opts := Options{
		PublicKey:   pubkey,
		ServerName:  "www.microsoft.com",
		Fingerprint: "chrome",
		ShortID:     [8]byte{0xa1, 0xb2, 0xc3, 0xd4},
	}

	clientDone := make(chan error, 1)
	go func() {
		_, err := Client(context.Background(), cliSide, opts)
		clientDone <- err
	}()

	// Read the first TLS record (record header + payload).
	srvSide.SetReadDeadline(time.Now().Add(3 * time.Second))
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(srvSide, hdr); err != nil {
		t.Fatalf("read record header: %v", err)
	}
	if hdr[0] != 0x16 {
		t.Fatalf("expected handshake record (0x16), got 0x%02x", hdr[0])
	}
	recordLen := binary.BigEndian.Uint16(hdr[3:5])
	payload := make([]byte, recordLen)
	if _, err := io.ReadFull(srvSide, payload); err != nil {
		t.Fatalf("read record payload: %v", err)
	}

	// Handshake message: [type(1) | len(3) | body...]
	// ClientHello body: [legacy_version(2) | random(32) | sid_len(1) | sid(N) | ...]
	if payload[0] != 0x01 {
		t.Fatalf("expected ClientHello (type 0x01), got 0x%02x", payload[0])
	}
	sidLen := payload[sessionIDOffset-1] // byte just before sessionIDOffset
	if sidLen != 32 {
		t.Fatalf("expected 32-byte session_id, got %d (fingerprint emitted an unexpected session_id length)", sidLen)
	}
	sid := payload[sessionIDOffset : sessionIDOffset+32]

	// After Reality stuffing, the session_id is 32 bytes of AES-GCM output
	// (ciphertext ‖ tag). All-zero means Reality did nothing; equal to
	// hello.Random[:32] means the plaintext plumbing broke. Just assert
	// non-zero and that the field is distinct from the record's random.
	if bytes.Equal(sid, make([]byte, 32)) {
		t.Fatal("session_id is all zeros — Reality did not stuff it")
	}
	random := payload[6:38]
	if bytes.Equal(sid, random) {
		t.Fatal("session_id equals client_random — stuffing collided")
	}

	// Unblock the goroutine and let it error out cleanly.
	srvSide.Close()
	select {
	case <-clientDone:
	case <-time.After(2 * time.Second):
		t.Error("Client() did not return after peer close")
	}
}

func TestClient_RejectsUnknownFingerprint(t *testing.T) {
	var pubkey [32]byte
	rand.Read(pubkey[:])
	_, err := Client(context.Background(), nil, Options{
		PublicKey:   pubkey,
		ServerName:  "x",
		Fingerprint: "bogus",
	})
	if err == nil {
		t.Error("expected error for unknown fingerprint")
	}
}
