package vless

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/JohnThre/clambhook/internal/protocol"
)

// packetConn carries UDP datagrams over a VLESS-over-TLS stream.
//
// Wire format after the response header (per datagram, both directions):
//
//	[ len (2B BE) ][ payload ]
//
// VLESS v1 of this client uses *single-target* UDP: the destination is fixed
// in the opening request header (cmd=0x02). Per-datagram address fields
// (which some servers expect when Mux/XUDP is enabled) are out of scope.
// WriteTo's `addr` argument is ignored; ReadFrom reports the target baked
// in at Dial time.
type packetConn struct {
	stream net.Conn // outer transport: either *tls.Conn or *utls.UConn (Reality)
	target string   // the per-session target set by DialPacket

	// The outer transport allows concurrent Read+Write but not concurrent
	// Reads or concurrent Writes. Framing is message-boundary-aware, so
	// serialize each direction to keep frames intact.
	readMu   sync.Mutex
	writeMu  sync.Mutex
	respOnce sync.Once
	respErr  error
}

func (p *packetConn) Protocol() string { return "vless" }

// maxUDPPayload bounds accepted datagram size. 64KB is the theoretical UDP
// maximum; anything larger almost certainly indicates a desync.
const maxUDPPayload = 65535

func (p *packetConn) ReadFrom(buf []byte) (int, net.Addr, error) {
	// The VLESS response header prefixes the entire session (not each
	// datagram) — consume it lazily on first ReadFrom so DialPacket can
	// return without blocking on server I/O.
	p.respOnce.Do(func() { p.respErr = readResponse(p.stream) })
	if p.respErr != nil {
		return 0, nil, p.respErr
	}

	p.readMu.Lock()
	defer p.readMu.Unlock()

	var lb [2]byte
	if _, err := io.ReadFull(p.stream, lb[:]); err != nil {
		return 0, nil, fmt.Errorf("vless: read udp length: %w", err)
	}
	length := int(binary.BigEndian.Uint16(lb[:]))
	if length == 0 || length > maxUDPPayload {
		return 0, nil, fmt.Errorf("vless: invalid udp length %d", length)
	}

	addr := packetAddr{target: p.target}

	if length <= len(buf) {
		if _, err := io.ReadFull(p.stream, buf[:length]); err != nil {
			return 0, nil, fmt.Errorf("vless: read udp payload: %w", err)
		}
		return length, addr, nil
	}

	// Payload larger than caller's buffer — read into scratch and truncate,
	// matching net.PacketConn.ReadFrom semantics.
	scratch := make([]byte, length)
	if _, err := io.ReadFull(p.stream, scratch); err != nil {
		return 0, nil, fmt.Errorf("vless: read udp payload: %w", err)
	}
	return copy(buf, scratch), addr, nil
}

// WriteTo writes one length-prefixed datagram. addr is ignored — VLESS
// single-target UDP puts the destination in the opening header.
func (p *packetConn) WriteTo(payload []byte, addr net.Addr) (int, error) {
	if len(payload) == 0 {
		return 0, nil
	}
	if len(payload) > maxUDPPayload {
		return 0, fmt.Errorf("vless: udp payload %d exceeds max", len(payload))
	}

	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	frame := make([]byte, 0, 2+len(payload))
	frame = binary.BigEndian.AppendUint16(frame, uint16(len(payload)))
	frame = append(frame, payload...)
	if _, err := p.stream.Write(frame); err != nil {
		return 0, fmt.Errorf("vless: write udp frame: %w", err)
	}
	return len(payload), nil
}

func (p *packetConn) Close() error        { return p.stream.Close() }
func (p *packetConn) LocalAddr() net.Addr { return p.stream.LocalAddr() }
func (p *packetConn) SetDeadline(t time.Time) error {
	return p.stream.SetDeadline(t)
}
func (p *packetConn) SetReadDeadline(t time.Time) error  { return p.stream.SetReadDeadline(t) }
func (p *packetConn) SetWriteDeadline(t time.Time) error { return p.stream.SetWriteDeadline(t) }

// packetAddr reports the session-wide target. Every datagram on this
// packetConn has the same remote; it's set by DialPacket.
type packetAddr struct {
	target string
}

func (a packetAddr) Network() string { return "udp" }
func (a packetAddr) String() string  { return a.target }

// Compile-time guards.
var (
	_ net.PacketConn      = (*packetConn)(nil)
	_ protocol.PacketConn = (*packetConn)(nil)
)
