package vmess

import (
	"fmt"
	"net"
	"time"

	"github.com/JohnThre/clambhook/internal/protocol"
)

// packetConn layers UDP-semantics on top of a VMess body stream opened with
// cmd=0x02. Each body chunk carries exactly one datagram — the AEAD codec
// already provides length delimitation, so we don't need extra framing.
//
// Single-target per session, matching VLESS: the target address comes from
// DialPacket and is echoed back to callers of ReadFrom. Per-datagram target
// routing (XUDP / PacketAddr) is not implemented.
type packetConn struct {
	inner  *conn
	target string
}

func (p *packetConn) Protocol() string { return "vmess" }

// maxUDPPayload matches the VMess body-chunk cap. Any UDP datagram larger
// than this would already be rejected by the IP layer in practice (the MTU
// cap is ~1472 bytes).
const maxUDPPayload = maxChunkPlaintext

func (p *packetConn) ReadFrom(buf []byte) (int, net.Addr, error) {
	// Surface the first chunk as one datagram. If the caller's buffer is
	// smaller than the chunk, truncate (net.PacketConn.ReadFrom semantics).
	n, err := p.inner.Read(buf)
	if err != nil {
		return 0, nil, err
	}
	return n, packetAddr{target: p.target}, nil
}

func (p *packetConn) WriteTo(payload []byte, _ net.Addr) (int, error) {
	if len(payload) == 0 {
		return 0, nil
	}
	if len(payload) > maxUDPPayload {
		return 0, fmt.Errorf("vmess: udp payload %d exceeds max", len(payload))
	}
	return p.inner.Write(payload)
}

func (p *packetConn) Close() error                       { return p.inner.Close() }
func (p *packetConn) LocalAddr() net.Addr                { return p.inner.LocalAddr() }
func (p *packetConn) SetDeadline(t time.Time) error      { return p.inner.SetDeadline(t) }
func (p *packetConn) SetReadDeadline(t time.Time) error  { return p.inner.SetReadDeadline(t) }
func (p *packetConn) SetWriteDeadline(t time.Time) error { return p.inner.SetWriteDeadline(t) }

type packetAddr struct{ target string }

func (a packetAddr) Network() string { return "udp" }
func (a packetAddr) String() string  { return a.target }

// Compile-time guards.
var (
	_ net.PacketConn      = (*packetConn)(nil)
	_ protocol.PacketConn = (*packetConn)(nil)
)
