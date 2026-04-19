package chain

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/clambhook/clambhook/internal/protocol"
)

// These tests exercise the chain orchestration in chain.go using a test-only
// "loopback" protocol (see loopback_test.go). Because chain.Chain builds
// dialers via protocol.NewDialer — which reads the global registry — the
// loopback protocol registers itself via init() in loopback_test.go. Test
// binary isolation makes this safe.
//
// Each test creates a fresh chain-scoped recorder so per-hop assertions
// don't bleed between tests.

// -----------------------------------------------------------------------------
// TCP chain orchestration
// -----------------------------------------------------------------------------

func TestChain_EmptyFails(t *testing.T) {
	c := &Chain{Name: "empty", Nodes: nil}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := c.Dial(ctx, "tcp", "example.com:443")
	if err == nil {
		t.Fatal("expected error for empty chain, got nil")
	}
	if !strings.Contains(err.Error(), "no nodes") {
		t.Errorf("error %q missing 'no nodes'", err)
	}
}

// TestChain_SingleHopTCP: one-node chain. first.Dial is called directly with
// the user's target as the address. No DialThrough.
func TestChain_SingleHopTCP(t *testing.T) {
	chainName := "single-tcp"
	r := newRecorder()
	globalLoopbackState.setChain(chainName, r)

	c := &Chain{
		Name: chainName,
		Nodes: []protocol.Server{
			loopbackNode("A", "unused-addr:0", 0x11, chainName),
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := c.Dial(ctx, "tcp", "final.target:443")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	// Assert single hop saw the user's target address directly.
	if got := r.targetByHop["A"]; got != "final.target:443" {
		t.Errorf("hop A target = %q, want final.target:443", got)
	}

	// Sanity: data round-trips.
	payload := []byte("hello single hop")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	reply := make([]byte, len(payload))
	n, err := conn.Read(reply)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(reply[:n], payload) {
		t.Errorf("echo mismatch: got %q want %q", reply[:n], payload)
	}
}

// TestChain_ThreeHopsTCP: three-node chain. Verifies each hop received the
// correct downstream address (nodes[i+1].Address for middle hops, final
// user target for last), and that the full stack round-trips data intact.
func TestChain_ThreeHopsTCP(t *testing.T) {
	chainName := "three-tcp"
	r := newRecorder()
	globalLoopbackState.setChain(chainName, r)

	// Node addresses encode WHERE each node lives. The chain passes each
	// hop the address of the NEXT hop (A gets B's address as its dial
	// target; B gets C's; C gets the user's final target).
	c := &Chain{
		Name: chainName,
		Nodes: []protocol.Server{
			loopbackNode("A", "addr.A:1111", 0x01, chainName),
			loopbackNode("B", "addr.B:2222", 0x02, chainName),
			loopbackNode("C", "addr.C:3333", 0x03, chainName),
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := c.Dial(ctx, "tcp", "final.target:443")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	cases := map[string]string{
		"A": "addr.B:2222",      // A.Dial → B.Address
		"B": "addr.C:3333",      // B.DialThrough → C.Address
		"C": "final.target:443", // C.DialThrough → user target
	}
	for hop, want := range cases {
		if got := r.targetByHop[hop]; got != want {
			t.Errorf("hop %s target = %q, want %q", hop, got, want)
		}
	}

	// Hops B and C must have received non-nil underlying streams.
	for _, hop := range []string{"B", "C"} {
		if !r.underlyingByHop[hop] {
			t.Errorf("hop %s did not receive underlying stream", hop)
		}
	}

	// Data round-trips through the full three-layer stack.
	payload := []byte("three-hop-echo")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	reply := make([]byte, len(payload))
	n, err := conn.Read(reply)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(reply[:n], payload) {
		t.Errorf("echo mismatch: got %q want %q", reply[:n], payload)
	}
}

// TestChain_ThreeHopsTCP_ErrorAtMiddleHop: middle hop rejects DialThrough.
// Verifies (a) error message wraps hop index and (b) the previous hop's
// connection was closed (by convention — the protocol implementation is
// expected to Close(underlying) on failure, matching shadowsocks.go:106).
func TestChain_ThreeHopsTCP_ErrorAtMiddleHop(t *testing.T) {
	chainName := "error-mid"
	r := newRecorder()
	globalLoopbackState.setChain(chainName, r)

	globalLoopbackState.setReject("B", true)
	defer globalLoopbackState.setReject("B", false)

	c := &Chain{
		Name: chainName,
		Nodes: []protocol.Server{
			loopbackNode("A", "addr.A:1111", 0x01, chainName),
			loopbackNode("B", "addr.B:2222", 0x02, chainName),
			loopbackNode("C", "addr.C:3333", 0x03, chainName),
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := c.Dial(ctx, "tcp", "final.target:443")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Chain wraps with "node %d dial".
	if !strings.Contains(err.Error(), `chain "error-mid" node 1 dial`) {
		t.Errorf("error %q missing 'node 1 dial' prefix", err)
	}

	// The rejecting hop (B) closed its underlying stream — this is the
	// convention chain.go:105 relies on (no explicit conn.Close there).
	if !r.closedUnderlying["B"] {
		t.Error("hop B did not close its underlying stream on DialThrough error")
	}
}

// TestChain_FirstHopDialFails: first-hop Dial fails. Chain must return an
// error wrapped with "node 0 dial". We trigger a failure by registering an
// always-erroring "reject" protocol for the first node.
func TestChain_FirstHopDialFails(t *testing.T) {
	// Register a one-off "reject_dial" factory that errors on Dial.
	protocol.Register("reject_dial", func(s protocol.Server) (protocol.Dialer, error) {
		return &rejectingDialer{name: s.Name}, nil
	})

	c := &Chain{
		Name: "first-fail",
		Nodes: []protocol.Server{
			{Name: "A", Address: "unused:0", Protocol: "reject_dial"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := c.Dial(ctx, "tcp", "target:443")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), `chain "first-fail" node 0 dial`) {
		t.Errorf("error %q missing 'node 0 dial' prefix", err)
	}
}

type rejectingDialer struct{ name string }

func (d *rejectingDialer) Protocol() string { return "reject_dial" }
func (d *rejectingDialer) Dial(ctx context.Context, network, address string) (protocol.Conn, error) {
	return nil, errors.New("simulated dial failure")
}
func (d *rejectingDialer) DialThrough(ctx context.Context, u io.ReadWriteCloser, address string) (protocol.Conn, error) {
	if u != nil {
		_ = u.Close()
	}
	return nil, errors.New("simulated dial-through failure")
}

// TestChain_UnknownProtocolFails: chain node references an unregistered
// protocol. Factory lookup at chain.go:94 must fail and close any already-
// established conns (here: just the first hop's conn).
func TestChain_UnknownProtocolFails(t *testing.T) {
	chainName := "unknown-proto"
	r := newRecorder()
	globalLoopbackState.setChain(chainName, r)

	c := &Chain{
		Name: chainName,
		Nodes: []protocol.Server{
			loopbackNode("A", "next:1111", 0x01, chainName),
			{Name: "B", Address: "unused:0", Protocol: "no_such_protocol"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := c.Dial(ctx, "tcp", "target:443")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown protocol") {
		t.Errorf("error %q missing 'unknown protocol'", err)
	}
	if !strings.Contains(err.Error(), "node 1") {
		t.Errorf("error %q missing 'node 1'", err)
	}
}

// -----------------------------------------------------------------------------
// UDP (PacketDialer) chain orchestration
// -----------------------------------------------------------------------------

// TestChain_SingleHopUDP: single-node chain where the node supports UDP.
// chain.DialPacket dials directly without any stream chaining.
func TestChain_SingleHopUDP(t *testing.T) {
	chainName := "single-udp"
	r := newRecorder()
	globalLoopbackState.setChain(chainName, r)

	c := &Chain{
		Name: chainName,
		Nodes: []protocol.Server{
			loopbackUDPNode("A", "unused:0", 0x10, chainName),
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	pc, err := c.DialPacket(ctx, "dns.target:53")
	if err != nil {
		t.Fatalf("DialPacket: %v", err)
	}
	defer pc.Close()

	if got := r.targetByHop["A"]; got != "dns.target:53" {
		t.Errorf("hop A target = %q, want dns.target:53", got)
	}

	payload := []byte{0xde, 0xad, 0xbe, 0xef}
	if _, err := pc.WriteTo(payload, &net.UDPAddr{IP: net.ParseIP("8.8.8.8"), Port: 53}); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	buf := make([]byte, 16)
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if !bytes.Equal(buf[:n], payload) {
		t.Errorf("udp echo mismatch: got %x want %x", buf[:n], payload)
	}
}

// TestChain_MultiHopUDP_FinalHopSupportsPacket: chain of 2 hops where the
// final hop is PacketDialer. Intermediate hops tunnel the final hop's
// stream-framed UDP session.
func TestChain_MultiHopUDP_FinalHopSupportsPacket(t *testing.T) {
	chainName := "multi-udp"
	r := newRecorder()
	globalLoopbackState.setChain(chainName, r)

	c := &Chain{
		Name: chainName,
		Nodes: []protocol.Server{
			loopbackNode("A", "addr.A:1111", 0x01, chainName),
			loopbackUDPNode("B", "addr.B:2222", 0x02, chainName),
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	pc, err := c.DialPacket(ctx, "dns.target:53")
	if err != nil {
		t.Fatalf("DialPacket: %v", err)
	}
	defer pc.Close()

	// A (stream hop) got B.Address as its dial target.
	// B (final/UDP hop) got the user's UDP target.
	if got := r.targetByHop["A"]; got != "addr.B:2222" {
		t.Errorf("hop A target = %q, want addr.B:2222", got)
	}
	if got := r.targetByHop["B"]; got != "dns.target:53" {
		t.Errorf("hop B target = %q, want dns.target:53", got)
	}

	payload := []byte{0xca, 0xfe, 0xba, 0xbe}
	if _, err := pc.WriteTo(payload, &net.UDPAddr{IP: net.ParseIP("1.1.1.1"), Port: 53}); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	buf := make([]byte, 16)
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if !bytes.Equal(buf[:n], payload) {
		t.Errorf("udp echo mismatch: got %x want %x", buf[:n], payload)
	}
}

// TestChain_MultiHopUDP_FinalHopNoPacketSupport: chain where the final hop
// is a stream-only "loopback" (not "loopback_udp"). chain.DialPacket must
// return a clean structured error naming the offending protocol.
func TestChain_MultiHopUDP_FinalHopNoPacketSupport(t *testing.T) {
	chainName := "no-udp-final"
	r := newRecorder()
	globalLoopbackState.setChain(chainName, r)

	c := &Chain{
		Name: chainName,
		Nodes: []protocol.Server{
			loopbackNode("A", "addr.A:1111", 0x01, chainName),
			loopbackNode("B", "addr.B:2222", 0x02, chainName), // stream-only
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := c.DialPacket(ctx, "dns.target:53")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), `protocol "loopback" does not support UDP`) {
		t.Errorf("error %q missing protocol-UDP-unsupported message", err)
	}
}
