package vmess

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/JohnThre/clambhook/internal/protocol"
	"github.com/JohnThre/clambhook/internal/protocol/v2ray"
)

// packetConn layers UDP-semantics on top of a VMess body stream. Legacy
// cmd=0x02 carries one raw datagram per body chunk. XUDP uses cmd=0x03
// (Mux) and prefixes each datagram with a mux-style frame carrying its
// destination address.
//
// Legacy mode is single-target per session, matching VLESS: the target
// address comes from DialPacket and is echoed back to callers of ReadFrom.
// XUDP mode uses WriteTo/ReadFrom addresses per datagram.
type packetConn struct {
	inner  *conn
	target string
	xudp   bool

	writeMu        sync.Mutex
	requestWritten bool
}

func (p *packetConn) Protocol() string { return "vmess" }

// maxUDPPayload matches the VMess body-chunk cap. Any UDP datagram larger
// than this would already be rejected by the IP layer in practice (the MTU
// cap is ~1472 bytes).
const maxUDPPayload = maxChunkPlaintext

func (p *packetConn) ReadFrom(buf []byte) (int, net.Addr, error) {
	if !p.xudp {
		chunk, err := p.inner.readChunk()
		if err != nil {
			return 0, nil, err
		}
		return copy(buf, chunk), packetAddr{target: p.target}, nil
	}

	for {
		frame, err := p.inner.readChunk()
		if err != nil {
			return 0, nil, err
		}
		payload, target, ok, err := decodeXUDPFrame(frame, p.target)
		if err != nil {
			return 0, nil, err
		}
		if !ok {
			continue
		}
		return copy(buf, payload), packetAddr{target: target}, nil
	}
}

func (p *packetConn) WriteTo(payload []byte, addr net.Addr) (int, error) {
	if len(payload) == 0 {
		return 0, nil
	}
	if len(payload) > maxUDPPayload {
		return 0, fmt.Errorf("vmess: udp payload %d exceeds max", len(payload))
	}
	if !p.xudp {
		if p.target == "" {
			return 0, fmt.Errorf("vmess: legacy UDP requires a per-session target; set packet_encoding=%q for per-datagram routing", packetEncodingXUDP)
		}
		if addr != nil && addr.String() != p.target {
			return 0, fmt.Errorf("vmess: legacy UDP session is bound to %s, got datagram for %s; set packet_encoding=%q for per-datagram routing", p.target, addr, packetEncodingXUDP)
		}
		return len(payload), p.inner.writeChunk(payload)
	}
	if addr == nil {
		if p.target == "" {
			return 0, errors.New("vmess: xudp write requires a destination address")
		}
		addr = packetAddr{target: p.target}
	}

	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	frame, err := encodeXUDPFrame(payload, addr, !p.requestWritten)
	if err != nil {
		return 0, err
	}
	if len(frame) > maxUDPPayload {
		return 0, fmt.Errorf("vmess: xudp frame %d exceeds max", len(frame))
	}
	if err := p.inner.writeChunk(frame); err != nil {
		return 0, err
	}
	p.requestWritten = true
	return len(payload), nil
}

func (p *packetConn) Close() error                       { return p.inner.Close() }
func (p *packetConn) LocalAddr() net.Addr                { return p.inner.LocalAddr() }
func (p *packetConn) SetDeadline(t time.Time) error      { return p.inner.SetDeadline(t) }
func (p *packetConn) SetReadDeadline(t time.Time) error  { return p.inner.SetReadDeadline(t) }
func (p *packetConn) SetWriteDeadline(t time.Time) error { return p.inner.SetWriteDeadline(t) }

type packetAddr struct{ target string }

func (a packetAddr) Network() string { return "udp" }
func (a packetAddr) String() string  { return a.target }

const (
	xudpStatusNew       byte = 0x01
	xudpStatusKeep      byte = 0x02
	xudpStatusEnd       byte = 0x03
	xudpStatusKeepAlive byte = 0x04

	xudpOptionData  byte = 0x01
	xudpOptionError byte = 0x02

	xudpNetworkUDP byte = 0x02
)

func encodeXUDPFrame(payload []byte, addr net.Addr, first bool) ([]byte, error) {
	if len(payload) > 0xffff {
		return nil, fmt.Errorf("vmess: xudp payload %d exceeds max", len(payload))
	}
	addrBytes, err := encodeXUDPAddr(addr)
	if err != nil {
		return nil, err
	}

	status := xudpStatusKeep
	if first {
		status = xudpStatusNew
	}
	frameLen := 5 + len(addrBytes) // session ID, status, option, network, address
	if frameLen > 0xffff {
		return nil, fmt.Errorf("vmess: xudp frame header %d exceeds max", frameLen)
	}

	frame := make([]byte, 0, 2+frameLen+2+len(payload))
	frame = binary.BigEndian.AppendUint16(frame, uint16(frameLen))
	frame = append(frame, 0x00, 0x00) // session ID; one UDP association per VMess mux stream
	frame = append(frame, status, xudpOptionData, xudpNetworkUDP)
	frame = append(frame, addrBytes...)
	frame = binary.BigEndian.AppendUint16(frame, uint16(len(payload)))
	frame = append(frame, payload...)
	return frame, nil
}

func decodeXUDPFrame(frame []byte, defaultTarget string) (payload []byte, target string, ok bool, err error) {
	if len(frame) < 8 {
		return nil, "", false, fmt.Errorf("vmess: xudp frame too short: %d", len(frame))
	}
	frameLen := int(binary.BigEndian.Uint16(frame[:2]))
	if frameLen < 4 {
		return nil, "", false, fmt.Errorf("vmess: xudp frame header too short: %d", frameLen)
	}
	if len(frame) < 2+frameLen+2 {
		return nil, "", false, fmt.Errorf("vmess: xudp frame truncated: header=%d total=%d", frameLen, len(frame))
	}

	status := frame[4]
	option := frame[5]
	if option&xudpOptionError != 0 {
		return nil, "", false, net.ErrClosed
	}
	switch status {
	case xudpStatusEnd:
		return nil, "", false, io.EOF
	case xudpStatusKeepAlive:
		if option&xudpOptionData == 0 {
			return nil, "", false, nil
		}
	case xudpStatusNew:
		return nil, "", false, errors.New("vmess: unexpected xudp new frame from server")
	case xudpStatusKeep:
	default:
		return nil, "", false, fmt.Errorf("vmess: unexpected xudp frame status %#x", status)
	}

	if frameLen > 4 {
		if frame[6] != xudpNetworkUDP {
			return nil, "", false, fmt.Errorf("vmess: xudp unsupported network %#x", frame[6])
		}
		target, err = readXUDPAddr(bytes.NewReader(frame[7 : 2+frameLen]))
		if err != nil {
			return nil, "", false, err
		}
	} else {
		target = defaultTarget
	}
	if target == "" {
		return nil, "", false, errors.New("vmess: xudp frame missing destination")
	}

	if option&xudpOptionData == 0 {
		return nil, "", false, nil
	}
	payloadOff := 2 + frameLen
	payloadLen := int(binary.BigEndian.Uint16(frame[payloadOff : payloadOff+2]))
	payloadOff += 2
	if payloadOff+payloadLen > len(frame) {
		return nil, "", false, fmt.Errorf("vmess: xudp payload truncated: payload=%d total=%d", payloadLen, len(frame))
	}
	return frame[payloadOff : payloadOff+payloadLen], target, true, nil
}

func encodeXUDPAddr(addr net.Addr) ([]byte, error) {
	if addr == nil {
		return nil, errors.New("vmess: xudp destination is nil")
	}
	host, portStr, err := net.SplitHostPort(addr.String())
	if err != nil {
		return nil, fmt.Errorf("vmess: xudp parse destination %q: %w", addr, err)
	}
	triple, err := v2ray.EncodeAddr(net.JoinHostPort(host, portStr))
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(triple))
	out = append(out, triple[len(triple)-2:]...)
	out = append(out, triple[:len(triple)-2]...)
	return out, nil
}

func readXUDPAddr(r *bytes.Reader) (string, error) {
	var pb [2]byte
	if _, err := io.ReadFull(r, pb[:]); err != nil {
		return "", fmt.Errorf("vmess: xudp read port: %w", err)
	}
	port := uint16(pb[0])<<8 | uint16(pb[1])

	var atyp [1]byte
	if _, err := io.ReadFull(r, atyp[:]); err != nil {
		return "", fmt.Errorf("vmess: xudp read atyp: %w", err)
	}
	var host string
	switch atyp[0] {
	case v2ray.ATYPIPv4:
		var b [4]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", fmt.Errorf("vmess: xudp read ipv4: %w", err)
		}
		host = net.IP(b[:]).String()
	case v2ray.ATYPIPv6:
		var b [16]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", fmt.Errorf("vmess: xudp read ipv6: %w", err)
		}
		host = net.IP(b[:]).String()
	case v2ray.ATYPDomain:
		var lb [1]byte
		if _, err := io.ReadFull(r, lb[:]); err != nil {
			return "", fmt.Errorf("vmess: xudp read domain length: %w", err)
		}
		if lb[0] == 0 {
			return "", errors.New("vmess: xudp empty domain")
		}
		b := make([]byte, int(lb[0]))
		if _, err := io.ReadFull(r, b); err != nil {
			return "", fmt.Errorf("vmess: xudp read domain: %w", err)
		}
		host = string(b)
	default:
		return "", fmt.Errorf("vmess: xudp unsupported atyp %#x", atyp[0])
	}
	if r.Len() != 0 {
		return "", fmt.Errorf("vmess: xudp address has %d trailing bytes", r.Len())
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

// Compile-time guards.
var (
	_ net.PacketConn      = (*packetConn)(nil)
	_ protocol.PacketConn = (*packetConn)(nil)
)
