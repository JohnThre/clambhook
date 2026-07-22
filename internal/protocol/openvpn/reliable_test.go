package openvpn

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/protocol"
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
	r := stubReliable(t)

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

	// Arrive out of order: 1, 2, 0 (a 0-based conformant peer's first
	// control packet is id 0).
	if err := r.handleIncoming(mkpkt(1, "second")); err != nil {
		t.Fatal(err)
	}
	if err := r.handleIncoming(mkpkt(2, "third")); err != nil {
		t.Fatal(err)
	}
	// Until 0 arrives, nothing should be delivered.
	select {
	case p := <-r.deliver:
		t.Fatalf("unexpected early delivery: %q", p.payload)
	default:
	}
	if err := r.handleIncoming(mkpkt(0, "first")); err != nil {
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
	r := stubReliable(t)

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
	if err := r.handleIncoming(mk(0)); err != nil {
		t.Fatal(err)
	}
	if err := r.handleIncoming(mk(0)); err != nil { // dup
		t.Fatal(err)
	}
	if err := r.handleIncoming(mk(1)); err != nil {
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

// stubReliable returns a minimal reliable for handleIncoming unit tests.
// It uses the 0-based packet-id window that matches OpenVPN upstream.
func stubReliable(t *testing.T) *reliable {
	t.Helper()
	return &reliable{
		pending:      make(map[packetID]*outstanding),
		reorder:      make(map[packetID]*controlPacket),
		deliver:      make(chan *controlPacket, 16),
		nextExpected: 0,
		done:         make(chan struct{}),
	}
}

// newTestReliablePair creates two reliable instances whose UDP sockets are
// connected to each other on 127.0.0.1. A background goroutine feeds each
// socket's incoming datagrams into its reliable's handleIncoming, matching
// the instance's udpReadLoop. The returned cleanup function must be called.
func newTestReliablePair(t *testing.T) (clientRel, serverRel *reliable, cleanup func()) {
	t.Helper()

	clientConn, serverConn, err := dialUDPConnectedPair()
	if err != nil {
		t.Fatalf("create connected udp pair: %v", err)
	}

	clientRel, err = newReliable(clientConn)
	if err != nil {
		_ = clientConn.Close()
		_ = serverConn.Close()
		t.Fatalf("new client reliable: %v", err)
	}
	serverRel, err = newReliable(serverConn)
	if err != nil {
		_ = clientRel.close()
		_ = clientConn.Close()
		_ = serverConn.Close()
		t.Fatalf("new server reliable: %v", err)
	}

	stopC := startReliableReadLoop(clientRel)
	stopS := startReliableReadLoop(serverRel)
	cleanup = func() {
		stopC()
		stopS()
		_ = clientRel.close()
		_ = serverRel.close()
		_ = clientConn.Close()
		_ = serverConn.Close()
	}
	return clientRel, serverRel, cleanup
}

// dialUDPConnectedPair returns two *net.UDPConn endpoints connected to each
// other. We first bind ephemeral ports to discover free pairs, then close
// those listeners and re-dial connected sockets aimed at each other.
func dialUDPConnectedPair() (client, server *net.UDPConn, err error) {
	caddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
	cListener, err := net.ListenUDP("udp", caddr)
	if err != nil {
		return nil, nil, err
	}
	cLocal := cListener.LocalAddr().(*net.UDPAddr)
	_ = cListener.Close()

	saddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
	sListener, err := net.ListenUDP("udp", saddr)
	if err != nil {
		return nil, nil, err
	}
	sLocal := sListener.LocalAddr().(*net.UDPAddr)
	_ = sListener.Close()

	client, err = net.DialUDP("udp", cLocal, sLocal)
	if err != nil {
		return nil, nil, err
	}
	server, err = net.DialUDP("udp", sLocal, cLocal)
	if err != nil {
		_ = client.Close()
		return nil, nil, err
	}
	return client, server, nil
}

// startReliableReadLoop mirrors the instance udpReadLoop for a standalone
// reliable in a test: it copies UDP datagrams into handleIncoming until the
// underlying connection is closed.
func startReliableReadLoop(r *reliable) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		buf := make([]byte, 2048)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			n, err := r.conn.Read(buf)
			if err != nil {
				return
			}
			pkt := append([]byte(nil), buf[:n]...)
			_ = r.handleIncoming(pkt)
		}
	}()
	return cancel
}

// TestReliableZeroBasedPacketIDsConformantPeer models an upstream OpenVPN
// peer: its first outgoing control packet (HARD_RESET_SERVER_V2) has
// packet-id 0 and it expects our first control packet to be id 0 as well.
func TestReliableZeroBasedPacketIDsConformantPeer(t *testing.T) {
	client, server, cleanup := newTestReliablePair(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Client sends HARD_RESET_CLIENT_V2; the first packet id must be 0.
	if err := client.send(ctx, OpcodeControlHardResetClientV2, nil); err != nil {
		t.Fatalf("client send: %v", err)
	}
	got, err := server.recv(ctx)
	if err != nil {
		t.Fatalf("server recv reset: %v", err)
	}
	if got.opcode != OpcodeControlHardResetClientV2 {
		t.Fatalf("server got opcode %d, want HARD_RESET_CLIENT_V2", got.opcode)
	}
	if got.packetID != 0 {
		t.Fatalf("client first packet id = %d, want 0", got.packetID)
	}

	// Server replies with its own id-0 HARD_RESET_SERVER_V2. A 1-based
	// receive window would drop this as a duplicate.
	if err := server.send(ctx, OpcodeControlHardResetServerV2, nil); err != nil {
		t.Fatalf("server send: %v", err)
	}
	got, err = client.recv(ctx)
	if err != nil {
		t.Fatalf("client recv server reset: %v", err)
	}
	if got.opcode != OpcodeControlHardResetServerV2 {
		t.Fatalf("client got opcode %d, want HARD_RESET_SERVER_V2", got.opcode)
	}
	if got.packetID != 0 {
		t.Fatalf("server first packet id seen = %d, want 0", got.packetID)
	}
}

// TestReliableRecvInterruptedByContextCancel proves that a recv() blocked
// waiting for the next in-order packet returns immediately when its context
// is cancelled.
func TestReliableRecvInterruptedByContextCancel(t *testing.T) {
	client, _, cleanup := newTestReliablePair(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	_, err := client.recv(ctx)
	if err == nil {
		t.Fatal("recv returned nil, expected context error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("recv err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("recv took %s to cancel, expected < 1s", elapsed)
	}
}

// TestControlReadDeadlineInterruptsReliableRecv checks that a read deadline
// on the control channel is forwarded to the reliable layer, so a stalled
// peer cannot hang the TLS handshake indefinitely.
func TestControlReadDeadlineInterruptsReliableRecv(t *testing.T) {
	client, _, cleanup := newTestReliablePair(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctrl := newControl(client, ctx)

	// The remote peer never sends; the read deadline should fire first.
	_ = ctrl.SetReadDeadline(time.Now().Add(200 * time.Millisecond))

	buf := make([]byte, 16)
	start := time.Now()
	_, err := ctrl.Read(buf)
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Read err = %v, want os.ErrDeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Read took %s, expected < 1s", elapsed)
	}
}

// TestControlWriteDeadlineInterruptsReliableSend checks that a write
// deadline in the past causes Write to return a deadline error rather than
// blocking. This is the deterministic half of the deadline contract.
func TestControlWriteDeadlineInterruptsReliableSend(t *testing.T) {
	client, _, cleanup := newTestReliablePair(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctrl := newControl(client, ctx)

	_ = ctrl.SetWriteDeadline(time.Now().Add(-time.Second))

	start := time.Now()
	_, err := ctrl.Write([]byte("tls record fragment"))
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Write err = %v, want os.ErrDeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Write took %s, expected < 1s", elapsed)
	}
}

// TestControlReadUsesHandshakeContext proves that control.Read uses the
// context passed at construction, so parent cancellation interrupts a
// blocked read even without an explicit deadline.
func TestControlReadUsesHandshakeContext(t *testing.T) {
	client, _, cleanup := newTestReliablePair(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	ctrl := newControl(client, ctx)

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	buf := make([]byte, 16)
	start := time.Now()
	_, err := ctrl.Read(buf)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Read err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Read took %s to cancel, expected < 1s", elapsed)
	}
}
