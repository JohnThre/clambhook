package vless

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/clambhook/clambhook/internal/chain"
	"github.com/clambhook/clambhook/internal/protocol"
	"github.com/clambhook/clambhook/internal/protocol/v2ray"
)

// TestChain_TwoVLESSHops_TCP verifies a real chain of two VLESS-over-TLS
// hops to a plain TCP echo server. Lives in the vless package to reuse the
// existing self-signed cert / TLS-listener helpers (newTestCert, tlsListener)
// and to access internal constants (version, cmdTCP, atyp codes).
//
// Topology:
//   client → VLESS-hop-1 (TLS) → VLESS-hop-2 (TLS) → echo target
//
// The two hops use the same UUID for simplicity (servers don't validate it
// here); the TLS certs are self-signed via newTestCert, and the client is
// configured with skip_cert_verify=true to accept them.
//
// This exercises a scenario where each hop's DialThrough layers a fresh TLS
// handshake over an already-chained stream — the case most likely to expose
// interface-contract bugs (e.g., if the netConnAdapter fails to satisfy
// something tls.Client needs).
func TestChain_TwoVLESSHops_TCP(t *testing.T) {
	// Final target: plain TCP echo server.
	echoLn := tcpListener(t)
	defer echoLn.Close()
	echoDone := make(chan struct{})
	go func() {
		defer close(echoDone)
		runEchoOnce(echoLn)
	}()

	// Each VLESS hop: TLS listener + relay handler.
	cert, _ := newTestCert(t)

	hop2Done := make(chan error, 1)
	hop2Ln := tlsListener(t, cert)
	defer hop2Ln.Close()
	go func() { hop2Done <- runVLESSRelayOnce(hop2Ln) }()

	hop1Done := make(chan error, 1)
	hop1Ln := tlsListener(t, cert)
	defer hop1Ln.Close()
	go func() { hop1Done <- runVLESSRelayOnce(hop1Ln) }()

	// Client chain: [VLESS@hop1, VLESS@hop2] → echoLn.
	c := &chain.Chain{
		Name: "vless-vless",
		Nodes: []protocol.Server{
			{
				Name:     "hop1",
				Address:  hop1Ln.Addr().String(),
				Protocol: "vless",
				Settings: map[string]any{
					"uuid":             testUUID,
					"sni":              "example.com",
					"skip_cert_verify": true,
				},
			},
			{
				Name:     "hop2",
				Address:  hop2Ln.Addr().String(),
				Protocol: "vless",
				Settings: map[string]any{
					"uuid":             testUUID,
					"sni":              "example.com",
					"skip_cert_verify": true,
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

	payload := []byte("vless-vless-chain-round-trip")
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

func runEchoOnce(ln net.Listener) {
	c, err := ln.Accept()
	if err != nil {
		return
	}
	defer c.Close()
	_, _ = io.Copy(c, c)
}

// runVLESSRelayOnce handles one client: TLS server handshake → read VLESS
// request header → dial the decoded target as raw TCP → write VLESS response
// header → bridge TLS↔TCP in both directions.
func runVLESSRelayOnce(ln net.Listener) error {
	c, err := ln.Accept()
	if err != nil {
		return fmt.Errorf("accept: %w", err)
	}
	defer c.Close()

	// tls.Listen already returns *tls.Conn from Accept, but the handshake
	// is deferred to the first Read — force it now so handshake failures
	// surface here instead of mid-bridge.
	if tc, ok := c.(*tls.Conn); ok {
		if err := tc.Handshake(); err != nil {
			return fmt.Errorf("tls handshake: %w", err)
		}
	}

	target, err := readVLESSRequestAndDial(c)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer target.Close()

	// VLESS server response: ver(0x00) + addon_len(0x00). The client's
	// readResponse consumes exactly these two bytes lazily on first Read.
	if _, err := c.Write([]byte{0x00, 0x00}); err != nil {
		return fmt.Errorf("write response header: %w", err)
	}

	// Bridge both directions concurrently; first side to error tears down
	// the other by closing both legs.
	errc := make(chan error, 2)
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			_ = target.Close()
			_ = c.Close()
		})
	}
	go func() {
		_, err := io.Copy(target, c)
		closeBoth()
		errc <- err
	}()
	go func() {
		_, err := io.Copy(c, target)
		closeBoth()
		errc <- err
	}()

	var firstErr error
	for i := 0; i < 2; i++ {
		if err := <-errc; err != nil && firstErr == nil && !isBenignErr(err) {
			firstErr = err
		}
	}
	return firstErr
}

// readVLESSRequestAndDial reads a VLESS request header from r (following the
// same wire format as encodeRequest at header.go:32) and dials the decoded
// target as a raw TCP connection.
func readVLESSRequestAndDial(r io.Reader) (net.Conn, error) {
	// Fixed prefix: ver(1) + uuid(16) + addon_len(1) + cmd(1) + port(2) + atyp(1) = 22 bytes
	fixed := make([]byte, 22)
	if _, err := io.ReadFull(r, fixed); err != nil {
		return nil, fmt.Errorf("read fixed: %w", err)
	}
	if fixed[0] != version {
		return nil, fmt.Errorf("bad version %#x", fixed[0])
	}
	if fixed[17] != 0x00 {
		return nil, fmt.Errorf("nonzero addon_len %d (test server only accepts flow=none)", fixed[17])
	}
	if fixed[18] != cmdTCP {
		return nil, fmt.Errorf("cmd = %#x, want TCP", fixed[18])
	}
	port := binary.BigEndian.Uint16(fixed[19:21])
	atyp := fixed[21]

	// Read variable-length address per ATYP.
	var host string
	switch atyp {
	case v2ray.ATYPIPv4:
		b := make([]byte, 4)
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, err
		}
		host = net.IP(b).String()
	case v2ray.ATYPIPv6:
		b := make([]byte, 16)
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, err
		}
		host = net.IP(b).String()
	case v2ray.ATYPDomain:
		var lb [1]byte
		if _, err := io.ReadFull(r, lb[:]); err != nil {
			return nil, err
		}
		b := make([]byte, int(lb[0]))
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, err
		}
		host = string(b)
	default:
		return nil, fmt.Errorf("bad atyp %#x", atyp)
	}

	target := net.JoinHostPort(host, strconv.Itoa(int(port)))
	return net.Dial("tcp", target)
}

func isBenignErr(err error) bool {
	if err == nil || err == io.EOF {
		return true
	}
	msg := err.Error()
	return bytes.Contains([]byte(msg), []byte("closed network connection")) ||
		bytes.Contains([]byte(msg), []byte("broken pipe")) ||
		bytes.Contains([]byte(msg), []byte("connection reset by peer")) ||
		bytes.Contains([]byte(msg), []byte("EOF"))
}
