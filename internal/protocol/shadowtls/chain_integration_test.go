// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package shadowtls

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"hash"
	"io"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/chain"
	"github.com/JohnThre/clambhook/internal/protocol"
)

const testPassword = "chain-integration-secret"

// TestChain_SingleShadowTLSHop_Echo drives a real crypto/tls 1.3 handshake
// (with the client-side SessionID hijack) against an in-process ShadowTLS
// server that relays the handshake to a genuine TLS 1.3 handshake server, then
// echoes at the data stage. This exercises chain.Dial + the full handshake +
// data-stage framing end to end.
func TestChain_SingleShadowTLSHop_Echo(t *testing.T) {
	ln := tcpListener(t)
	defer ln.Close()
	go runShadowTLSEchoServerOnce(t, ln, testPassword)

	c := &chain.Chain{
		Name: "stls",
		Nodes: []protocol.Server{
			{
				Name:     "stls-entry",
				Address:  ln.Addr().String(),
				Protocol: "shadowtls",
				Settings: map[string]any{
					"password":         testPassword,
					"sni":              "handshake.invalid",
					"skip_cert_verify": true,
				},
			},
		},
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := c.Dial(ctx, "tcp", "example.invalid:443")
	if err != nil {
		t.Fatalf("chain Dial: %v", err)
	}
	defer conn.Close()

	assertEcho(t, conn)
}

// TestDialThrough_Echo verifies the non-final-hop path (DialThrough) that
// chain.go uses for intermediate hops: ShadowTLS wraps an existing transport
// and returns a working byte stream.
func TestDialThrough_Echo(t *testing.T) {
	ln := tcpListener(t)
	defer ln.Close()
	go runShadowTLSEchoServerOnce(t, ln, testPassword)

	underlying, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	d, err := protocol.NewDialer(protocol.Server{
		Address:  ln.Addr().String(),
		Protocol: "shadowtls",
		Settings: map[string]any{
			"password":         testPassword,
			"sni":              "handshake.invalid",
			"skip_cert_verify": true,
		},
	})
	if err != nil {
		t.Fatalf("NewDialer: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := d.DialThrough(ctx, underlying, "example.invalid:443")
	if err != nil {
		t.Fatalf("DialThrough: %v", err)
	}
	defer conn.Close()

	assertEcho(t, conn)
}

// TestDialThrough_ClosesUnderlyingOnError verifies the ownership contract:
// when the handshake fails, DialThrough must close the underlying transport.
func TestDialThrough_ClosesUnderlyingOnError(t *testing.T) {
	// A server that accepts then immediately closes — the TLS handshake fails.
	ln := tcpListener(t)
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		conn.Close()
	}()

	underlying, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	tracker := &closeTracker{Conn: underlying}

	d, err := protocol.NewDialer(protocol.Server{
		Address:  ln.Addr().String(),
		Protocol: "shadowtls",
		Settings: map[string]any{"password": testPassword, "sni": "x.invalid", "skip_cert_verify": true},
	})
	if err != nil {
		t.Fatalf("NewDialer: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := d.DialThrough(ctx, tracker, "example.invalid:443"); err == nil {
		t.Fatal("expected handshake error")
	}
	if !tracker.closed() {
		t.Fatal("DialThrough did not close underlying on error")
	}
}

func assertEcho(t *testing.T, conn net.Conn) {
	t.Helper()
	payload := []byte("shadowtls-chain-round-trip-payload")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("echo mismatch:\n got %q\nwant %q", got, payload)
	}
}

// -----------------------------------------------------------------------------
// In-process ShadowTLS v3 server (test-only), ported from the reference server
// stage logic. Relays the TLS 1.3 handshake to a genuine handshake server, then
// switches to the authenticated data stage and echoes.
// -----------------------------------------------------------------------------

func runShadowTLSEchoServerOnce(t *testing.T, ln net.Listener, password string) {
	raw, err := ln.Accept()
	if err != nil {
		return
	}
	defer raw.Close()

	// Genuine TLS 1.3 handshake server reached over an in-memory pipe.
	hsLocal, hsRemote := net.Pipe()
	defer hsLocal.Close()
	tlsSrv := tls.Server(hsRemote, testTLSConfig(t))
	go func() {
		_ = tlsSrv.HandshakeContext(context.Background())
		// Keep the handshake side readable so any trailing records drain.
		_, _ = io.Copy(io.Discard, tlsSrv)
	}()

	srvRandomCh := make(chan []byte, 1)
	var randomOnce sync.Once

	// hs -> client: capture ServerRandom, XOR+HMAC-wrap ApplicationData.
	go func() {
		var (
			hmacWrite hash.Hash
			writeKey  []byte
		)
		for {
			frame, err := extractFrameConn(hsLocal)
			if err != nil {
				return
			}
			if len(frame) > serverRandomIndex+tlsRandomSize && frame[0] == recordHandshake && frame[tlsHeaderSize] == handshakeServerHello {
				sr := make([]byte, tlsRandomSize)
				copy(sr, frame[serverRandomIndex:serverRandomIndex+tlsRandomSize])
				hmacWrite = hmac.New(sha1.New, []byte(password))
				hmacWrite.Write(sr)
				writeKey = kdf(password, sr)
				randomOnce.Do(func() { srvRandomCh <- sr })
			}
			if frame[0] == recordApplicationData && hmacWrite != nil {
				xorSlice(frame[tlsHeaderSize:], writeKey)
				hmacWrite.Write(frame[tlsHeaderSize:])
				sum := hmacWrite.Sum(nil)[:hmacSize]
				out := make([]byte, 0, tlsHmacHeaderSize+len(frame)-tlsHeaderSize)
				out = append(out, frame[:tlsHeaderSize]...)
				binary.BigEndian.PutUint16(out[3:], uint16(len(frame)-tlsHeaderSize+hmacSize))
				out = append(out, sum...)
				out = append(out, frame[tlsHeaderSize:]...)
				if _, err := raw.Write(out); err != nil {
					return
				}
			} else {
				if _, err := raw.Write(frame); err != nil {
					return
				}
			}
		}
	}()

	// client -> hs: forward until a data frame authenticates with
	// HMAC_ServerRandomC; that frame is the first data-stage payload.
	var serverRandom []byte
	hmacVerify := (hash.Hash)(nil)
	var firstPayload []byte
	for {
		frame, err := extractFrameConn(raw)
		if err != nil {
			return
		}
		if frame[0] == recordApplicationData {
			if serverRandom == nil {
				select {
				case serverRandom = <-srvRandomCh:
				case <-time.After(5 * time.Second):
					return
				}
			}
			base := hmac.New(sha1.New, []byte(password))
			base.Write(serverRandom)
			base.Write([]byte("C"))
			if len(frame) > tlsHmacHeaderSize {
				trial := cloneHMAC(base, serverRandom, password, "C")
				trial.Write(frame[tlsHmacHeaderSize:])
				if hmac.Equal(trial.Sum(nil)[:hmacSize], frame[tlsHeaderSize:tlsHmacHeaderSize]) {
					// Establish the data-stage verifier state and stash payload.
					hmacVerify = cloneHMAC(base, serverRandom, password, "C")
					hmacVerify.Write(frame[tlsHmacHeaderSize:])
					hmacVerify.Write(frame[tlsHeaderSize:tlsHmacHeaderSize])
					firstPayload = append([]byte(nil), frame[tlsHmacHeaderSize:]...)
					break
				}
			}
		}
		if _, err := hsLocal.Write(frame); err != nil {
			return
		}
	}

	// Data stage: echo. Build a server-side stConn (add=S, verify=C) but the
	// first payload was already consumed above, so echo it, then bridge.
	hmacAdd := hmac.New(sha1.New, []byte(password))
	hmacAdd.Write(serverRandom)
	hmacAdd.Write([]byte("S"))

	server := newStConn(raw, hmacAdd, hmacVerify, nil)
	if len(firstPayload) > 0 {
		if _, err := server.Write(firstPayload); err != nil {
			return
		}
	}
	_, _ = io.Copy(server, server)
}

// extractFrameConn reads one whole TLS record (header + body) from conn.
func extractFrameConn(conn net.Conn) ([]byte, error) {
	var header [tlsHeaderSize]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return nil, err
	}
	length := int(binary.BigEndian.Uint16(header[3:tlsHeaderSize]))
	frame := make([]byte, tlsHeaderSize+length)
	copy(frame, header[:])
	if _, err := io.ReadFull(conn, frame[tlsHeaderSize:]); err != nil {
		return nil, err
	}
	return frame, nil
}

func cloneHMAC(_ hash.Hash, serverRandom []byte, password, suffix string) hash.Hash {
	h := hmac.New(sha1.New, []byte(password))
	h.Write(serverRandom)
	h.Write([]byte(suffix))
	return h
}

func tcpListener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

func testTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "handshake.invalid"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"handshake.invalid"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
	}
}

type closeTracker struct {
	net.Conn
	mu     sync.Mutex
	didClo bool
}

func (c *closeTracker) Close() error {
	c.mu.Lock()
	c.didClo = true
	c.mu.Unlock()
	return c.Conn.Close()
}

func (c *closeTracker) closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.didClo
}
