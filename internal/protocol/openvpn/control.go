package openvpn

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
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

	// Read-side context and deadline. The base context is usually the
	// handshake context so that parent cancellation interrupts a blocked
	// reliable recv; SetReadDeadline narrows it further.
	rctx      context.Context
	rdlMu     sync.Mutex
	rdeadline time.Time

	// Write-side context and deadline, symmetrical to the read side.
	wctx      context.Context
	wdlMu     sync.Mutex
	wdeadline time.Time
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

// Read implements io.Reader for the TLS state machine. It honours both the
// base context (usually the handshake context) and any read deadline set
// via SetReadDeadline / SetDeadline, returning os.ErrDeadlineExceeded
// when the deadline expires.
func (c *control) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()

	if c.rerr != nil && c.rbuf.Len() == 0 {
		return 0, c.rerr
	}
	for c.rbuf.Len() == 0 {
		ctx, cancel := c.readContext()
		pkt, err := c.r.recv(ctx)
		cancel()
		if err != nil {
			c.rerr = err
			if c.rctx.Err() == nil && context.Cause(ctx) == os.ErrDeadlineExceeded {
				return 0, os.ErrDeadlineExceeded
			}
			return 0, err
		}
		if pkt.opcode != OpcodeControlV1 {
			continue
		}
		if len(pkt.payload) == 0 {
			continue
		}
		c.rbuf.Write(pkt.payload)
	}
	return c.rbuf.Read(p)
}

func (c *control) readContext() (context.Context, context.CancelFunc) {
	c.rdlMu.Lock()
	dl := c.rdeadline
	c.rdlMu.Unlock()
	return contextWithDeadline(c.rctx, dl, os.ErrDeadlineExceeded)
}

// Write implements io.Writer for the TLS state machine. It fragments the
// input into CONTROL_V1 packets and honours the write context plus any
// write deadline.
func (c *control) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n := tlsFragmentSize
		if n > len(p) {
			n = len(p)
		}
		chunk := append([]byte(nil), p[:n]...)

		ctx, cancel := c.writeContext()
		err := c.r.send(ctx, OpcodeControlV1, chunk)
		cancel()
		if err != nil {
			if c.wctx.Err() == nil && context.Cause(ctx) == os.ErrDeadlineExceeded {
				return written, os.ErrDeadlineExceeded
			}
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

func (c *control) writeContext() (context.Context, context.CancelFunc) {
	c.wdlMu.Lock()
	dl := c.wdeadline
	c.wdlMu.Unlock()
	return contextWithDeadline(c.wctx, dl, os.ErrDeadlineExceeded)
}

// contextWithDeadline returns parent unchanged when dl is zero or when the
// parent already has an earlier deadline. Otherwise it returns a child
// context that expires at dl with the supplied cause (e.g.
// os.ErrDeadlineExceeded). The caller must invoke the returned CancelFunc.
func contextWithDeadline(parent context.Context, dl time.Time, cause error) (context.Context, context.CancelFunc) {
	if dl.IsZero() {
		return parent, func() {}
	}
	if err := parent.Err(); err != nil {
		return parent, func() {}
	}
	if pdl, ok := parent.Deadline(); ok && pdl.Before(dl) {
		return parent, func() {}
	}
	return context.WithDeadlineCause(parent, dl, cause)
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
func (c *control) SetDeadline(t time.Time) error {
	c.SetReadDeadline(t)
	c.SetWriteDeadline(t)
	return nil
}
func (c *control) SetReadDeadline(t time.Time) error {
	c.rdlMu.Lock()
	c.rdeadline = t
	c.rdlMu.Unlock()
	return nil
}
func (c *control) SetWriteDeadline(t time.Time) error {
	c.wdlMu.Lock()
	c.wdeadline = t
	c.wdlMu.Unlock()
	return nil
}

type controlAddr struct{}

func (controlAddr) Network() string { return "openvpn-control" }
func (controlAddr) String() string  { return "openvpn" }

var _ net.Conn = (*control)(nil)
var _ io.ReadWriteCloser = (*control)(nil)
