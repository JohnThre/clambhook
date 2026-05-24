package trojanwire

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/JohnThre/clambhook/internal/protocol"
)

const MaxUDPPayload = 65535

type PacketConn struct {
	tls  *tls.Conn
	name string

	readMu  sync.Mutex
	writeMu sync.Mutex
}

func (p *PacketConn) Protocol() string { return p.name }

func (p *PacketConn) ReadFrom(buf []byte) (int, net.Addr, error) {
	p.readMu.Lock()
	defer p.readMu.Unlock()

	host, port, err := ReadAddr(p.name, p.tls)
	if err != nil {
		return 0, nil, err
	}

	var lb [2]byte
	if _, err := io.ReadFull(p.tls, lb[:]); err != nil {
		return 0, nil, fmt.Errorf("%s: read udp length: %w", p.name, err)
	}
	length := int(binary.BigEndian.Uint16(lb[:]))
	if length > MaxUDPPayload {
		return 0, nil, fmt.Errorf("%s: udp payload length %d exceeds max", p.name, length)
	}

	var crlf [2]byte
	if _, err := io.ReadFull(p.tls, crlf[:]); err != nil {
		return 0, nil, fmt.Errorf("%s: read udp CRLF: %w", p.name, err)
	}
	if crlf != [2]byte{'\r', '\n'} {
		return 0, nil, fmt.Errorf("%s: bad udp CRLF %v", p.name, crlf)
	}

	addr := packetAddr{host: host, port: int(port)}

	if length <= len(buf) {
		if _, err := io.ReadFull(p.tls, buf[:length]); err != nil {
			return 0, nil, fmt.Errorf("%s: read udp payload: %w", p.name, err)
		}
		return length, addr, nil
	}

	scratch := make([]byte, length)
	if _, err := io.ReadFull(p.tls, scratch); err != nil {
		return 0, nil, fmt.Errorf("%s: read udp payload: %w", p.name, err)
	}
	return copy(buf, scratch), addr, nil
}

func (p *PacketConn) WriteTo(payload []byte, addr net.Addr) (int, error) {
	if len(payload) > MaxUDPPayload {
		return 0, fmt.Errorf("%s: udp payload %d exceeds max", p.name, len(payload))
	}
	host, portStr, err := net.SplitHostPort(addr.String())
	if err != nil {
		return 0, fmt.Errorf("%s: parse addr %q: %w", p.name, addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		return 0, fmt.Errorf("%s: bad port %q", p.name, portStr)
	}
	addrBytes, err := EncodeAddr(p.name, net.JoinHostPort(host, portStr))
	if err != nil {
		return 0, err
	}

	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	frame := make([]byte, 0, len(addrBytes)+2+2+len(payload))
	frame = append(frame, addrBytes...)
	frame = binary.BigEndian.AppendUint16(frame, uint16(len(payload)))
	frame = append(frame, '\r', '\n')
	frame = append(frame, payload...)

	if _, err := p.tls.Write(frame); err != nil {
		return 0, fmt.Errorf("%s: write udp frame: %w", p.name, err)
	}
	return len(payload), nil
}

func (p *PacketConn) Close() error { return p.tls.Close() }

func (p *PacketConn) LocalAddr() net.Addr { return p.tls.LocalAddr() }

func (p *PacketConn) SetDeadline(t time.Time) error      { return p.tls.SetDeadline(t) }
func (p *PacketConn) SetReadDeadline(t time.Time) error  { return p.tls.SetReadDeadline(t) }
func (p *PacketConn) SetWriteDeadline(t time.Time) error { return p.tls.SetWriteDeadline(t) }

type packetAddr struct {
	host string
	port int
}

func (a packetAddr) Network() string { return "udp" }
func (a packetAddr) String() string {
	return net.JoinHostPort(a.host, strconv.Itoa(a.port))
}

var (
	_ net.PacketConn      = (*PacketConn)(nil)
	_ protocol.PacketConn = (*PacketConn)(nil)
)
