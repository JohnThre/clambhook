package trojan

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/clambhook/clambhook/internal/protocol"
)

// packetConn carries UDP datagrams over a trojan-flavored TLS stream.
//
// Wire format (per datagram, inside the TLS stream):
//
//	+------+----------+----------+----------+---------+----------+
//	| ATYP | DST.ADDR | DST.PORT |  Length  |  CRLF   | Payload  |
//	+------+----------+----------+----------+---------+----------+
//	|   1  | Variable |     2    |     2    | X'0D0A' | Variable |
//	+------+----------+----------+----------+---------+----------+
//
// DST.ADDR/PORT in each frame is the *target peer* of that datagram — so a
// single TLS session carries traffic to many different UDP destinations.
type packetConn struct {
	tls *tls.Conn

	// tls.Conn allows concurrent Read+Write, but not concurrent Reads or
	// concurrent Writes. The framing is inherently message-boundary-aware,
	// so we serialize each direction to keep frames intact.
	readMu  sync.Mutex
	writeMu sync.Mutex
}

// Protocol implements protocol.PacketConn.
func (p *packetConn) Protocol() string { return "trojan" }

// maxUDPPayload bounds the size of a datagram we accept from the remote side.
// 64KB is the theoretical UDP maximum; in practice most networks cap at
// ~1472 bytes. Larger frames almost certainly indicate a desync.
const maxUDPPayload = 65535

// ReadFrom reads one framed datagram from the TLS stream.
//
// If the datagram is longer than len(buf), it is truncated to fit (matching
// net.PacketConn.ReadFrom semantics: "If the buffer is too small, the packet
// is truncated and the remainder discarded").
func (p *packetConn) ReadFrom(buf []byte) (int, net.Addr, error) {
	p.readMu.Lock()
	defer p.readMu.Unlock()

	host, port, err := readAddr(p.tls)
	if err != nil {
		return 0, nil, err
	}

	var lb [2]byte
	if _, err := io.ReadFull(p.tls, lb[:]); err != nil {
		return 0, nil, fmt.Errorf("trojan: read udp length: %w", err)
	}
	length := int(binary.BigEndian.Uint16(lb[:]))
	if length > maxUDPPayload {
		return 0, nil, fmt.Errorf("trojan: udp payload length %d exceeds max", length)
	}

	var crlf [2]byte
	if _, err := io.ReadFull(p.tls, crlf[:]); err != nil {
		return 0, nil, fmt.Errorf("trojan: read udp CRLF: %w", err)
	}
	if crlf != [2]byte{'\r', '\n'} {
		return 0, nil, fmt.Errorf("trojan: bad udp CRLF %v", crlf)
	}

	addr := packetAddr{host: host, port: int(port)}

	if length <= len(buf) {
		if _, err := io.ReadFull(p.tls, buf[:length]); err != nil {
			return 0, nil, fmt.Errorf("trojan: read udp payload: %w", err)
		}
		return length, addr, nil
	}

	// Payload larger than caller's buffer — read into scratch and truncate.
	scratch := make([]byte, length)
	if _, err := io.ReadFull(p.tls, scratch); err != nil {
		return 0, nil, fmt.Errorf("trojan: read udp payload: %w", err)
	}
	return copy(buf, scratch), addr, nil
}

// WriteTo frames one datagram and writes it to the TLS stream.
func (p *packetConn) WriteTo(payload []byte, addr net.Addr) (int, error) {
	if len(payload) > maxUDPPayload {
		return 0, fmt.Errorf("trojan: udp payload %d exceeds max", len(payload))
	}
	host, portStr, err := net.SplitHostPort(addr.String())
	if err != nil {
		return 0, fmt.Errorf("trojan: parse addr %q: %w", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		return 0, fmt.Errorf("trojan: bad port %q", portStr)
	}
	addrBytes, err := encodeAddr(net.JoinHostPort(host, portStr))
	if err != nil {
		return 0, err
	}

	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	// Assemble the frame in one buffer so a single TLS write carries the
	// whole datagram — two partial writes would still be valid TLS, but this
	// keeps on-the-wire record boundaries aligned with datagram boundaries.
	frame := make([]byte, 0, len(addrBytes)+2+2+len(payload))
	frame = append(frame, addrBytes...)
	frame = binary.BigEndian.AppendUint16(frame, uint16(len(payload)))
	frame = append(frame, '\r', '\n')
	frame = append(frame, payload...)

	if _, err := p.tls.Write(frame); err != nil {
		return 0, fmt.Errorf("trojan: write udp frame: %w", err)
	}
	return len(payload), nil
}

// Close tears down the underlying TLS session.
func (p *packetConn) Close() error { return p.tls.Close() }

// LocalAddr returns the local end of the TLS stream (not a UDP socket).
func (p *packetConn) LocalAddr() net.Addr { return p.tls.LocalAddr() }

// SetDeadline / SetReadDeadline / SetWriteDeadline delegate to the TLS conn
// so callers can cancel blocked I/O.
func (p *packetConn) SetDeadline(t time.Time) error      { return p.tls.SetDeadline(t) }
func (p *packetConn) SetReadDeadline(t time.Time) error  { return p.tls.SetReadDeadline(t) }
func (p *packetConn) SetWriteDeadline(t time.Time) error { return p.tls.SetWriteDeadline(t) }

// packetAddr is the net.Addr type returned from ReadFrom. Host may be an IP
// literal or a domain name; the stringer produces a form suitable for
// feeding back into WriteTo.
type packetAddr struct {
	host string
	port int
}

func (a packetAddr) Network() string { return "udp" }
func (a packetAddr) String() string {
	return net.JoinHostPort(a.host, strconv.Itoa(a.port))
}

// compile-time guards: packetConn is a net.PacketConn and a
// protocol.PacketConn; the trojan dialer is a protocol.PacketDialer.
// An accidental signature drift (missing return type, renamed method) now
// fails at build time rather than at runtime.
var (
	_ net.PacketConn       = (*packetConn)(nil)
	_ protocol.PacketConn   = (*packetConn)(nil)
	_ protocol.PacketDialer = (*dialer)(nil)
)
