package openvpn

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/clambhook/clambhook/internal/protocol"
)

// TestOpenvpnDialThroughRejected guards the "OpenVPN can't ride a chained
// stream" invariant — same contract WireGuard upholds. If someone ever
// tries to make OpenVPN chainable, they'll have to change this test
// alongside the underlying framing, which is the right moment to think
// twice about the consequences.
func TestOpenvpnDialThroughRejected(t *testing.T) {
	d := &dialer{cfg: &config{}}
	_, err := d.DialThrough(context.Background(), nopCloser{}, "example.com:80")
	if err == nil {
		t.Fatal("expected DialThrough to reject")
	}
}

// TestReliableInOrderDelivery feeds out-of-order control packets through
// handleIncoming and verifies recv() returns them in packet-ID order.
// This is the reliable layer's single most load-bearing behaviour: TLS
// records depend on byte-stream ordering, so reordering would break
// handshakes.
func TestReliableInOrderDelivery(t *testing.T) {
	// Can't easily construct a reliable without a live UDP socket, but we
	// can drive handleIncoming directly with manually-crafted packets
	// once we stub out the retransmit goroutine.
	r := &reliable{
		pending:      make(map[packetID]*outstanding),
		reorder:      make(map[packetID]*controlPacket),
		deliver:      make(chan *controlPacket, 16),
		nextExpected: 1,
		done:         make(chan struct{}),
	}
	// Don't start the retransmit loop — no connection.
	defer close(r.done)

	var lsid sessionID
	for i := range lsid {
		lsid[i] = byte(i + 1)
	}

	mkpkt := func(pid packetID, body string) []byte {
		buf, _ := encodeControl(&controlPacket{
			opcode:         OpcodeControlV1,
			localSessionID: lsid,
			packetID:       pid,
			payload:        []byte(body),
		})
		return buf
	}

	// Arrive out of order: 2, 3, 1.
	if err := r.handleIncoming(mkpkt(2, "second")); err != nil {
		t.Fatal(err)
	}
	if err := r.handleIncoming(mkpkt(3, "third")); err != nil {
		t.Fatal(err)
	}
	// Until 1 arrives, nothing should be delivered.
	select {
	case p := <-r.deliver:
		t.Fatalf("unexpected early delivery: %q", p.payload)
	default:
	}
	if err := r.handleIncoming(mkpkt(1, "first")); err != nil {
		t.Fatal(err)
	}

	// Now all three should pop out in order.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, want := range []string{"first", "second", "third"} {
		p, err := r.recv(ctx)
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if string(p.payload) != want {
			t.Fatalf("got %q, want %q", p.payload, want)
		}
	}
}

// TestReliableDedup confirms duplicates are not redelivered. Out-of-order
// delivery plus duplicates is what we'd see on a lossy path where the
// peer retransmits a packet whose ACK was lost in flight.
func TestReliableDedup(t *testing.T) {
	r := &reliable{
		pending:      make(map[packetID]*outstanding),
		reorder:      make(map[packetID]*controlPacket),
		deliver:      make(chan *controlPacket, 16),
		nextExpected: 1,
		done:         make(chan struct{}),
	}
	defer close(r.done)

	var lsid sessionID
	for i := range lsid {
		lsid[i] = byte(i + 1)
	}
	mk := func(pid packetID) []byte {
		buf, _ := encodeControl(&controlPacket{
			opcode:         OpcodeControlV1,
			localSessionID: lsid,
			packetID:       pid,
			payload:        []byte("x"),
		})
		return buf
	}
	if err := r.handleIncoming(mk(1)); err != nil {
		t.Fatal(err)
	}
	if err := r.handleIncoming(mk(1)); err != nil { // dup
		t.Fatal(err)
	}
	if err := r.handleIncoming(mk(2)); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen := 0
	for {
		_, err := r.recv(context.WithoutCancel(ctx))
		if err != nil {
			break
		}
		seen++
		if seen == 2 {
			// Verify no third delivery is waiting.
			select {
			case <-r.deliver:
				t.Fatal("duplicate was redelivered")
			default:
			}
			break
		}
	}
	if seen != 2 {
		t.Fatalf("delivered %d packets, expected 2", seen)
	}
}

type nopCloser struct{}

func (nopCloser) Read(p []byte) (int, error)  { return 0, io.EOF }
func (nopCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopCloser) Close() error                { return nil }

// Compile-time guard: the dialer satisfies protocol.Dialer. If someone
// changes the interface, the build breaks here rather than at runtime.
var _ protocol.Dialer = (*dialer)(nil)

// Unused helper just to keep errors import alive once we add more tests.
var _ = errors.New
