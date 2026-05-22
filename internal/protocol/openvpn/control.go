package openvpn

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// control sits between the reliable UDP layer and crypto/tls.Client.
// Crucially, it implements io.ReadWriter so tls.Client(control, ...) can
// just drive the handshake as if talking to any other stream.
//
// OpenVPN fragments TLS records across multiple P_CONTROL_V1 packets
// because TLS record sizes (up to 16 KiB) exceed what fits in a UDP
// datagram. Fragmentation is transparent: the reliable layer guarantees
// in-order delivery, and Read() just concatenates payloads into a single
// byte stream that the TLS state machine can parse.
type control struct {
	r *reliable

	// Read-side state: a bytes.Buffer stores un-read bytes pulled from the
	// reliable layer. Read() fills from this buffer; when empty, blocks
	// on reliable.recv() to pull the next P_CONTROL_V1 payload.
	rmu  sync.Mutex
	rbuf bytes.Buffer
	rerr error
	rctx context.Context

	// Write-side state — no buffer needed; fragmentation happens inline.
	wctx context.Context
}

// tlsFragmentSize is the max payload bytes we put into one P_CONTROL_V1.
// The real limit comes from the datagram size: 1500 (Ethernet MTU) minus
// IP(20..40) + UDP(8) + OpenVPN control overhead (~40) leaves ~1400. We
// pick 1200 as a conservative round number so we survive a PPPoE link or
// similar on the path.
const tlsFragmentSize = 1200

// newControl wires a control channel on top of an already-initialised
// reliable. The caller owns the reliable's lifecycle; control does not
// close it on error.
func newControl(r *reliable, ctx context.Context) *control {
	return &control{r: r, rctx: ctx, wctx: ctx}
}

// hardResetClient drives the initial HARD_RESET_CLIENT_V2 / HARD_RESET_SERVER_V2
// exchange. Must complete before any TLS record is sent. The server's
// reply carries an ACK of our HARD_RESET, which the reliable layer
// retires transparently; we just need to receive the matching
// HARD_RESET_SERVER_V2 packet to know the server is alive.
//
// Returns once the handshake is pinned: local + remote session IDs are
// set, the server's HARD_RESET is ACKed, and the channel is ready for
// TLS records.
func (c *control) hardResetClient(ctx context.Context) error {
	// HARD_RESET packets carry no payload — all the interesting bits
	// (session IDs) are in the control header itself.
	if err := c.r.send(ctx, OpcodeControlHardResetClientV2, nil); err != nil {
		return fmt.Errorf("openvpn: send HARD_RESET_CLIENT_V2: %w", err)
	}
	// The server's HARD_RESET_SERVER_V2 must arrive before we can TLS. It
	// can take a bit on lossy links, so give it the full handshake budget.
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		pkt, err := c.r.recv(deadline)
		if err != nil {
			return fmt.Errorf("openvpn: recv HARD_RESET_SERVER_V2: %w", err)
		}
		if pkt.opcode == OpcodeControlHardResetServerV2 {
			// Immediately ACK it, standalone — the TLS ClientHello won't
			// follow for a couple hundred microseconds, and we don't want
			// the server retransmitting in that window.
			_ = c.r.sendAck()
			return nil
		}
		// Any other control packet at this point is unexpected; keep
		// looping in case stray traffic slips through. A stricter reader
		// would fail hard here, but tolerating noise keeps us robust
		// against late retransmits from a previous (aborted) session.
	}
}

// Read implements io.Reader for the TLS state machine. Blocks until
// bytes are available; returns io.EOF when the reliable layer closes.
func (c *control) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()

	if c.rerr != nil && c.rbuf.Len() == 0 {
		return 0, c.rerr
	}
	for c.rbuf.Len() == 0 {
		pkt, err := c.r.recv(c.rctx)
		if err != nil {
			c.rerr = err
			return 0, err
		}
		if pkt.opcode != OpcodeControlV1 {
			// HARD_RESET_SERVER_V2 may arrive here on unlucky timing (the
			// server retransmitted after we already started the TLS
			// handshake). Silently drop — it's already been ACKed via
			// reliable.handleIncoming.
			continue
		}
		if len(pkt.payload) == 0 {
			// Empty CONTROL_V1 — shouldn't happen in practice but is legal
			// per the wire format. Ignore.
			continue
		}
		c.rbuf.Write(pkt.payload)
	}
	return c.rbuf.Read(p)
}

// Write implements io.Writer for the TLS state machine. Fragments the
// input into CONTROL_V1 packets of at most tlsFragmentSize bytes.
//
// TLS writes records (≤16 KiB) in one call to the underlying writer.
// OpenVPN's reliable layer delivers them in order, so fragmenting
// transparently is correct: the receiving peer's TLS layer sees one
// unbroken byte stream.
func (c *control) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n := tlsFragmentSize
		if n > len(p) {
			n = len(p)
		}
		// Defensive copy: reliable retains the payload buffer until ACK,
		// and the caller (tls.Conn) may reuse its record buffer.
		chunk := append([]byte(nil), p[:n]...)
		if err := c.r.send(c.wctx, OpcodeControlV1, chunk); err != nil {
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

// Close is a no-op: control doesn't own the reliable layer (the caller
// does) and crypto/tls doesn't require Close on the underlying conn for
// correctness. Satisfies io.Closer in case a future caller wants to pass
// control to a type that expects it.
func (c *control) Close() error { return nil }

// net.Conn satisfaction. tls.Client takes a net.Conn, not just io.ReadWriter,
// so we round out the interface with no-op address + deadline methods.
// Deadlines would be useful eventually (right now the wider handshake
// timeout in context.WithTimeout is what actually bounds the wait), but
// wiring them through to the reliable layer adds complexity we don't
// need for the handshake path.

func (c *control) LocalAddr() net.Addr                { return controlAddr{} }
func (c *control) RemoteAddr() net.Addr               { return controlAddr{} }
func (c *control) SetDeadline(t time.Time) error      { return nil }
func (c *control) SetReadDeadline(t time.Time) error  { return nil }
func (c *control) SetWriteDeadline(t time.Time) error { return nil }

type controlAddr struct{}

func (controlAddr) Network() string { return "openvpn-control" }
func (controlAddr) String() string  { return "openvpn" }

var _ net.Conn = (*control)(nil)
var _ io.ReadWriteCloser = (*control)(nil)
