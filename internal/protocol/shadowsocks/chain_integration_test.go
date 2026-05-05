package shadowsocks

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/clambhook/clambhook/internal/chain"
	"github.com/clambhook/clambhook/internal/protocol"
	"github.com/clambhook/clambhook/internal/socks"
)

// TestChain_TwoSSHops_TCP verifies that the chain orchestrator routes traffic
// correctly through two real Shadowsocks hops to a plain TCP echo server.
// Lives in the shadowsocks package for access to unexported crypto primitives
// (evpBytesToKey, hkdfSHA1, newStreamReader/Writer) needed to implement the
// relay-mode fake server.
//
// Topology:
//
//	client → SS-hop-1 → SS-hop-2 → echo target
//
// The two SS relays use different passwords, so each hop decrypts a distinct
// layer. A byte-for-byte round-trip of a non-trivial payload proves the full
// chain — including chain.go's DialThrough wiring — works with real AEAD
// crypto.
func TestChain_TwoSSHops_TCP(t *testing.T) {
	method := "chacha20-ietf-poly1305"
	spec, err := cipherByName(method)
	if err != nil {
		t.Skipf("cipher unavailable: %v", err)
	}

	// Final target: a plain TCP echo server.
	echoLn := tcpListener(t)
	defer echoLn.Close()
	echoDone := make(chan struct{})
	go func() {
		defer close(echoDone)
		runEchoOnce(echoLn)
	}()

	// Two SS relay servers, each with its own password.
	hop2Done := make(chan error, 1)
	hop2Ln := tcpListener(t)
	defer hop2Ln.Close()
	go func() { hop2Done <- runSSRelayOnce(hop2Ln, "pass-hop-2", method, spec) }()

	hop1Done := make(chan error, 1)
	hop1Ln := tcpListener(t)
	defer hop1Ln.Close()
	go func() { hop1Done <- runSSRelayOnce(hop1Ln, "pass-hop-1", method, spec) }()

	// Client-side chain: [SS@hop1, SS@hop2] → echoLn.
	c := &chain.Chain{
		Name: "ss-ss",
		Nodes: []protocol.Server{
			{
				Name:     "hop1",
				Address:  hop1Ln.Addr().String(),
				Protocol: "shadowsocks",
				Settings: map[string]any{
					"method":   method,
					"password": "pass-hop-1",
				},
			},
			{
				Name:     "hop2",
				Address:  hop2Ln.Addr().String(),
				Protocol: "shadowsocks",
				Settings: map[string]any{
					"method":   method,
					"password": "pass-hop-2",
				},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := c.Dial(ctx, "tcp", echoLn.Addr().String())
	if err != nil {
		t.Fatalf("chain Dial: %v", err)
	}

	payload := []byte("hello-ss-ss-chain-round-trip")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("echo mismatch:\n got %q\nwant %q", got, payload)
	}

	// Closing the client conn propagates through the chain and unblocks the
	// relays' io.Copy loops.
	_ = conn.Close()

	for i, ch := range []chan error{hop1Done, hop2Done} {
		select {
		case err := <-ch:
			if err != nil {
				t.Errorf("hop %d relay: %v", i+1, err)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("hop %d relay did not finish", i+1)
		}
	}
	<-echoDone
}

// -----------------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------------

func tcpListener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

// runEchoOnce accepts one connection and echoes bytes back until the client
// closes. Standard bidirectional TCP echo — used as the "final target" that
// the chain traffic ultimately reaches.
func runEchoOnce(ln net.Listener) {
	c, err := ln.Accept()
	if err != nil {
		return
	}
	defer c.Close()
	_, _ = io.Copy(c, c)
}

// runSSRelayOnce implements a single-connection Shadowsocks relay: accepts an
// SS client, decodes its target address, dials that target as plain TCP, and
// bridges bytes bidirectionally (decrypting client→target, re-encrypting
// target→client). This is what a real SS server does — simplified for tests
// to one connection and one target.
//
// Returns the first error encountered, or nil on clean shutdown.
func runSSRelayOnce(ln net.Listener, password, method string, spec *cipherSpec) error {
	conn, err := ln.Accept()
	if err != nil {
		return fmt.Errorf("accept: %w", err)
	}
	defer conn.Close()

	masterKey := evpBytesToKey([]byte(password), spec.keySize)

	// Read the client's salt, then decrypt chunks using the client's subkey.
	clientSalt := make([]byte, spec.saltSize)
	if _, err := io.ReadFull(conn, clientSalt); err != nil {
		return fmt.Errorf("read client salt: %w", err)
	}
	clientSubkey := hkdfSHA1(masterKey, clientSalt, ssSubkeyInfo, spec.keySize)
	sr := newStreamReader(conn, spec, clientSubkey)

	// First bytes: the target address. socks.ReadAddr reads exactly the
	// ATYP|ADDR|PORT triple; subsequent reads from sr return tunneled payload.
	host, port, err := socks.ReadAddr(sr)
	if err != nil {
		return fmt.Errorf("read addr: %w", err)
	}

	target, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		return fmt.Errorf("dial target: %w", err)
	}
	defer target.Close()

	// Set up the response direction: fresh salt, re-encrypt bytes from target.
	serverSalt := make([]byte, spec.saltSize)
	if _, err := rand.Read(serverSalt); err != nil {
		return fmt.Errorf("gen server salt: %w", err)
	}
	if _, err := conn.Write(serverSalt); err != nil {
		return fmt.Errorf("write server salt: %w", err)
	}
	serverSubkey := hkdfSHA1(masterKey, serverSalt, ssSubkeyInfo, spec.keySize)
	sw := newStreamWriter(conn, spec, serverSubkey)

	// Bridge in both directions concurrently. Each Copy terminates when its
	// source closes; the first Copy to finish returns, we close the target
	// and the pair naturally unwinds.
	errc := make(chan error, 2)
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			_ = target.Close()
			_ = conn.Close()
		})
	}
	go func() {
		_, err := io.Copy(target, sr) // client → target (decrypted)
		closeBoth()
		errc <- err
	}()
	go func() {
		_, err := io.Copy(sw, target) // target → client (re-encrypted)
		closeBoth()
		errc <- err
	}()

	// Wait for both halves to terminate. Return the first non-nil,
	// non-expected error (EOF / closed-conn noise is benign).
	var firstErr error
	for i := 0; i < 2; i++ {
		if err := <-errc; err != nil && firstErr == nil && !isBenignErr(err) {
			firstErr = err
		}
	}
	return firstErr
}

// isBenignErr recognizes errors that just signal "the other side closed" — we
// don't want those to fail the test.
func isBenignErr(err error) bool {
	if err == nil || err == io.EOF {
		return true
	}
	// Raw-TCP closes surface as net.ErrClosed or "use of closed network
	// connection"; match on message to avoid importing net internal errors.
	msg := err.Error()
	return containsAny(msg,
		"closed network connection",
		"broken pipe",
		"connection reset by peer",
		"EOF",
	)
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if bytes.Contains([]byte(s), []byte(sub)) {
			return true
		}
	}
	return false
}
