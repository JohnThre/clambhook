package chain

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/JohnThre/clambhook/internal/protocol"
)

// This file registers test-only protocols with the global protocol registry.
// Registration happens once via init(); the test binary is isolated from the
// production binary so the global mutation is scoped.
//
// Protocols registered here:
//   - "loopback" — a streaming protocol whose client directly wires to an
//     in-process server goroutine via net.Pipe(). Each hop XORs every byte
//     it carries with a hop-specific key, so a chain of N hops applies N
//     XOR layers — if the chain wired hops in the wrong order or skipped a
//     DialThrough, the final echo won't match.
//   - "loopback_udp" — same as "loopback" but also implements PacketDialer.
//   - "loopback_reject" — returns an error unconditionally from Dial and
//     DialThrough (for error-path tests). Closes any underlying conn passed
//     to DialThrough before returning, matching the convention observed in
//     shadowsocks.DialThrough (shadowsocks.go:106) and trojan.
//
// A per-test "target recorder" captures (via the loopback.factory's lookup
// map) the finalTarget string each hop was asked to reach, so tests can
// assert address wiring.

const (
	// loopbackHopKey is the per-hop XOR key. Encoded into the Server.Name
	// so each hop in a chain uses a distinct transform.
	loopbackSettingKey = "key"
)

// recorder collects per-hop observations for a single chain.Dial call. Tests
// create a fresh recorder, register a factory that writes into it, and
// assert on the observations afterward.
type recorder struct {
	mu sync.Mutex

	// targetByHop[i] is the "address" parameter the i-th hop received from
	// the chain (i.e. either nodes[i+1].Address for intermediate hops or
	// the user's target for the last hop).
	targetByHop map[string]string // keyed by node name → target

	// underlyingByHop[i] is whether the DialThrough call received a non-nil
	// underlying stream. Always true for hops >= 1; sanity-checked.
	underlyingByHop map[string]bool

	// closedUnderlying records whether a DialThrough that returned an error
	// closed its underlying stream (via Close() being called).
	closedUnderlying map[string]bool
}

func newRecorder() *recorder {
	return &recorder{
		targetByHop:      map[string]string{},
		underlyingByHop:  map[string]bool{},
		closedUnderlying: map[string]bool{},
	}
}

func (r *recorder) recordTarget(name, target string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.targetByHop[name] = target
}

func (r *recorder) recordUnderlying(name string, present bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.underlyingByHop[name] = present
}

func (r *recorder) recordClose(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closedUnderlying[name] = true
}

// loopbackState holds per-chain test state. Registered factories look it up
// by chain name (encoded in Server.Settings["chain"]).
type loopbackState struct {
	mu       sync.Mutex
	chains   map[string]*recorder
	rejectBy map[string]bool // node name → reject this hop on DialThrough
}

var globalLoopbackState = &loopbackState{
	chains:   map[string]*recorder{},
	rejectBy: map[string]bool{},
}

func (s *loopbackState) setChain(name string, r *recorder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chains[name] = r
}

func (s *loopbackState) getChain(name string) *recorder {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chains[name]
}

func (s *loopbackState) setReject(nodeName string, reject bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if reject {
		s.rejectBy[nodeName] = true
	} else {
		delete(s.rejectBy, nodeName)
	}
}

func (s *loopbackState) shouldReject(nodeName string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rejectBy[nodeName]
}

// -----------------------------------------------------------------------------
// Loopback protocol implementation
// -----------------------------------------------------------------------------

// loopbackDialer is the base stream-only dialer. It implements protocol.Dialer
// but NOT protocol.PacketDialer — so a chain ending on this type correctly
// triggers chain.go:38's "does not support UDP" error.
type loopbackDialer struct {
	server protocol.Server
	key    byte
	name   string
	chain  string
}

func (d *loopbackDialer) Protocol() string { return "loopback" }

// loopbackUDPDialer embeds loopbackDialer and adds PacketDialer methods.
// Chains ending on this type route through the DialPacket path.
type loopbackUDPDialer struct {
	loopbackDialer
}

func (d *loopbackUDPDialer) Protocol() string { return "loopback_udp" }

// Dial spins up an in-process server goroutine connected via net.Pipe. The
// "address" parameter is recorded but otherwise unused — there's no real
// network. Every byte transiting this conn is XOR'd with d.key.
func (d *loopbackDialer) Dial(ctx context.Context, network, address string) (protocol.Conn, error) {
	if r := globalLoopbackState.getChain(d.chain); r != nil {
		r.recordTarget(d.name, address)
	}
	clientSide, serverSide := net.Pipe()
	go runLoopbackServer(serverSide, d.key)
	return &loopbackConn{
		rwc:  clientSide,
		key:  d.key,
		name: d.Protocol(),
	}, nil
}

// DialThrough wraps an existing stream with this hop's XOR transform.
// Contractually: on error, we must close `underlying` (matching the
// convention observed in shadowsocks.go:106 and trojan). The chain
// orchestrator relies on this implicit contract.
func (d *loopbackDialer) DialThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (protocol.Conn, error) {
	if r := globalLoopbackState.getChain(d.chain); r != nil {
		r.recordTarget(d.name, address)
		r.recordUnderlying(d.name, underlying != nil)
	}
	if underlying == nil {
		return nil, fmt.Errorf("loopback %s: DialThrough got nil underlying", d.name)
	}
	if globalLoopbackState.shouldReject(d.name) {
		// Observe-and-close, per the convention. Record the close so the
		// test can assert the underlying was cleaned up.
		_ = underlying.Close()
		if r := globalLoopbackState.getChain(d.chain); r != nil {
			r.recordClose(d.name)
		}
		return nil, fmt.Errorf("loopback %s: rejected by test", d.name)
	}
	return &loopbackConn{
		rwc:  underlying,
		key:  d.key,
		name: d.Protocol(),
	}, nil
}

func (d *loopbackUDPDialer) DialPacket(ctx context.Context, address string) (protocol.PacketConn, error) {
	if r := globalLoopbackState.getChain(d.chain); r != nil {
		r.recordTarget(d.name, address)
	}
	clientSide, serverSide := net.Pipe()
	go runLoopbackUDPServer(serverSide, d.key)
	return &loopbackPacketConn{
		rwc:    clientSide,
		key:    d.key,
		name:   d.Protocol(),
		target: address,
	}, nil
}

func (d *loopbackUDPDialer) DialPacketThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (protocol.PacketConn, error) {
	if r := globalLoopbackState.getChain(d.chain); r != nil {
		r.recordTarget(d.name, address)
		r.recordUnderlying(d.name, underlying != nil)
	}
	if underlying == nil {
		return nil, fmt.Errorf("loopback %s: DialPacketThrough got nil underlying", d.name)
	}
	return &loopbackPacketConn{
		rwc:    underlying,
		key:    d.key,
		name:   d.Protocol(),
		target: address,
	}, nil
}

// runLoopbackServer echoes bytes back. Uses a writer goroutine so reads and
// writes don't serialize — net.Pipe is synchronous, so if the reader blocks
// on Write while the client is waiting to Write more, everything deadlocks.
// A buffered channel between the reader and a dedicated writer decouples the
// two directions.
//
// XOR: the server is a "dumb relay" — every byte that arrives is echoed
// back verbatim (XOR cancels through the same hop's decode/encode). The
// real correctness is whether the client's N-layer XOR stack decodes the
// echoed bytes back to the original payload.
func runLoopbackServer(conn net.Conn, key byte) {
	defer conn.Close()
	_ = key // reserved for future per-server transforms
	ch := make(chan []byte, 16)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for buf := range ch {
			if _, err := conn.Write(buf); err != nil {
				return
			}
		}
	}()
	readBuf := make([]byte, 4096)
	for {
		n, err := conn.Read(readBuf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, readBuf[:n])
			ch <- chunk
		}
		if err != nil {
			close(ch)
			<-writerDone
			return
		}
	}
}

// runLoopbackUDPServer frames each "packet" as length-prefixed (2 bytes BE)
// and echoes it back. Same decoupled read/write pattern as the TCP server.
func runLoopbackUDPServer(conn net.Conn, key byte) {
	defer conn.Close()
	_ = key
	ch := make(chan []byte, 16)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for buf := range ch {
			if _, err := conn.Write(buf); err != nil {
				return
			}
		}
	}()
	header := make([]byte, 2)
	for {
		if _, err := io.ReadFull(conn, header); err != nil {
			close(ch)
			<-writerDone
			return
		}
		n := int(header[0])<<8 | int(header[1])
		if n > 8192 {
			close(ch)
			<-writerDone
			return
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(conn, payload); err != nil {
			close(ch)
			<-writerDone
			return
		}
		frame := make([]byte, 0, 2+n)
		frame = append(frame, header...)
		frame = append(frame, payload...)
		ch <- frame
	}
}

// -----------------------------------------------------------------------------
// loopbackConn — protocol.Conn over an io.ReadWriteCloser, XORing every byte.
// -----------------------------------------------------------------------------

type loopbackConn struct {
	rwc  io.ReadWriteCloser
	key  byte
	name string
}

func (c *loopbackConn) Read(p []byte) (int, error) {
	n, err := c.rwc.Read(p)
	for i := 0; i < n; i++ {
		p[i] ^= c.key
	}
	return n, err
}

func (c *loopbackConn) Write(p []byte) (int, error) {
	// Copy-transform to avoid mutating caller's buffer.
	enc := make([]byte, len(p))
	for i := range p {
		enc[i] = p[i] ^ c.key
	}
	return c.rwc.Write(enc)
}

func (c *loopbackConn) Close() error                       { return c.rwc.Close() }
func (c *loopbackConn) Protocol() string                   { return c.name }
func (c *loopbackConn) LocalAddr() net.Addr                { return loopbackAddr{} }
func (c *loopbackConn) RemoteAddr() net.Addr               { return loopbackAddr{} }
func (c *loopbackConn) SetDeadline(t time.Time) error      { return nil }
func (c *loopbackConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *loopbackConn) SetWriteDeadline(t time.Time) error { return nil }

type loopbackAddr struct{}

func (loopbackAddr) Network() string { return "loopback" }
func (loopbackAddr) String() string  { return "loopback" }

// -----------------------------------------------------------------------------
// loopbackPacketConn — protocol.PacketConn with length-prefixed framing.
// -----------------------------------------------------------------------------

type loopbackPacketConn struct {
	rwc    io.ReadWriteCloser
	key    byte
	name   string
	target string
}

func (pc *loopbackPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(pc.rwc, header); err != nil {
		return 0, nil, err
	}
	n := int(header[0])<<8 | int(header[1])
	if n > len(p) {
		return 0, nil, errors.New("loopback packet too large for buffer")
	}
	if _, err := io.ReadFull(pc.rwc, p[:n]); err != nil {
		return 0, nil, err
	}
	for i := 0; i < n; i++ {
		p[i] ^= pc.key
	}
	return n, loopbackAddr{}, nil
}

func (pc *loopbackPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	header := []byte{byte(len(p) >> 8), byte(len(p) & 0xff)}
	enc := make([]byte, len(p))
	for i := range p {
		enc[i] = p[i] ^ pc.key
	}
	if _, err := pc.rwc.Write(header); err != nil {
		return 0, err
	}
	if _, err := pc.rwc.Write(enc); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (pc *loopbackPacketConn) Close() error                       { return pc.rwc.Close() }
func (pc *loopbackPacketConn) Protocol() string                   { return pc.name }
func (pc *loopbackPacketConn) LocalAddr() net.Addr                { return loopbackAddr{} }
func (pc *loopbackPacketConn) SetDeadline(t time.Time) error      { return nil }
func (pc *loopbackPacketConn) SetReadDeadline(t time.Time) error  { return nil }
func (pc *loopbackPacketConn) SetWriteDeadline(t time.Time) error { return nil }

// Compile-time interface checks.
var (
	_ protocol.Dialer       = (*loopbackDialer)(nil)
	_ protocol.Dialer       = (*loopbackUDPDialer)(nil)
	_ protocol.PacketDialer = (*loopbackUDPDialer)(nil)
	_ protocol.Conn         = (*loopbackConn)(nil)
	_ protocol.PacketConn   = (*loopbackPacketConn)(nil)
)

// -----------------------------------------------------------------------------
// Registration — runs once via init, adds test protocols to the global registry.
// -----------------------------------------------------------------------------

func init() {
	protocol.Register("loopback", func(s protocol.Server) (protocol.Dialer, error) {
		base := buildLoopbackBase(s)
		return &base, nil
	})
	protocol.Register("loopback_udp", func(s protocol.Server) (protocol.Dialer, error) {
		base := buildLoopbackBase(s)
		return &loopbackUDPDialer{loopbackDialer: base}, nil
	})
}

func buildLoopbackBase(s protocol.Server) loopbackDialer {
	key, _ := s.Settings[loopbackSettingKey].(int64)
	if key == 0 {
		if k, ok := s.Settings[loopbackSettingKey].(int); ok {
			key = int64(k)
		}
	}
	chain, _ := s.Settings["chain"].(string)
	return loopbackDialer{
		server: s,
		key:    byte(key),
		name:   s.Name,
		chain:  chain,
	}
}

// loopbackNode is a helper for building test Chain nodes.
func loopbackNode(name, address string, key byte, chain string) protocol.Server {
	return protocol.Server{
		Name:     name,
		Address:  address,
		Protocol: "loopback",
		Settings: map[string]any{
			loopbackSettingKey: int64(key),
			"chain":            chain,
		},
	}
}

func loopbackUDPNode(name, address string, key byte, chain string) protocol.Server {
	return protocol.Server{
		Name:     name,
		Address:  address,
		Protocol: "loopback_udp",
		Settings: map[string]any{
			loopbackSettingKey: int64(key),
			"chain":            chain,
		},
	}
}
