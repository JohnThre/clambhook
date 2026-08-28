// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package shadowtls

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/tls"
	"io"
	"testing"
)

// buildTestTLSConfig mirrors the dialer's handshake config: it must force
// X25519 and honour Rand so the two passes are reproducible.
func buildTestTLSConfig(r io.Reader) *tls.Config {
	return &tls.Config{
		Rand:                   r,
		ServerName:             "handshake.local",
		InsecureSkipVerify:     true,
		MinVersion:             tls.VersionTLS13,
		MaxVersion:             tls.VersionTLS13,
		CurvePreferences:       []tls.CurveID{tls.X25519},
		SessionTicketsDisabled: true,
	}
}

// TestTwoPassProducesSignedSessionID is the core regression guard: it verifies
// that the pass-2 ClientHello crypto/tls actually marshals carries a
// legacy_session_id whose 4-byte tail is the correct ShadowTLS v3 HMAC. This
// depends on go.mod's `godebug cryptocustomrand=1`; if that regresses, this
// test fails rather than silently shipping unauthenticated handshakes.
func TestTwoPassProducesSignedSessionID(t *testing.T) {
	const password = "two-pass-secret"

	for i := 0; i < 32; i++ {
		_, pass2Rand, err := prepareSignedHandshake(context.Background(), nil, password, buildTestTLSConfig)
		if err != nil {
			t.Fatalf("prepareSignedHandshake: %v", err)
		}

		ch, err := captureClientHello(context.Background(), buildTestTLSConfig, pass2Rand.(*replayRand))
		if err != nil {
			t.Fatalf("capture pass-2 client hello: %v", err)
		}

		sessionID := ch[chSessionIDStart : chSessionIDStart+tlsSessionIDSize]
		gotTail := sessionID[tlsSessionIDSize-hmacSize:]

		// Recompute the expected HMAC the way a ShadowTLS server does.
		mac := hmac.New(sha1.New, []byte(password))
		mac.Write(ch[:chSessionIDTail])
		mac.Write([]byte{0, 0, 0, 0})
		mac.Write(ch[chSessionIDTail+hmacSize:])
		want := mac.Sum(nil)[:hmacSize]

		if !hmac.Equal(gotTail, want) {
			t.Fatalf("iter %d: session id tail = %x, want %x", i, gotTail, want)
		}
	}
}

// TestReplayRandDeterministic verifies the recorded main stream is identical
// across passes (1-byte probe reads must not shift it).
func TestReplayRandDeterministic(t *testing.T) {
	var seed [32]byte
	for i := range seed {
		seed[i] = byte(i)
	}

	a := newReplayRand(seed, nil)
	b := newReplayRand(seed, nil)

	// Interleave a probe (1-byte) read into b; it must not affect the main
	// stream recorded in served.
	buf := make([]byte, 100)
	if _, err := a.Read(buf); err != nil {
		t.Fatal(err)
	}
	var probe [1]byte
	_, _ = b.Read(probe[:])
	_, _ = b.Read(probe[:])
	bufB := make([]byte, 100)
	if _, err := b.Read(bufB); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(a.served, b.served) {
		t.Fatal("main stream diverged after probe reads")
	}
}
