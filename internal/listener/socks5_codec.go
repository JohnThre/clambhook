package listener

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
)

// SOCKS5 protocol constants (RFC 1928 + RFC 1929).
const (
	socks5Version  = 0x05
	userPassVer    = 0x01
	methodNoAuth   = 0x00
	methodUserPass = 0x02
	methodNone     = 0xFF

	cmdConnect      = 0x01
	cmdBind         = 0x02
	cmdUDPAssociate = 0x03

	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04

	repSuccess           = 0x00
	repGeneralFailure    = 0x01
	repConnNotAllowed    = 0x02
	repNetworkUnreach    = 0x03
	repHostUnreach       = 0x04
	repConnRefused       = 0x05
	repTTLExpired        = 0x06
	repCmdNotSupported   = 0x07
	repAddrTypNotSupport = 0x08
)

// readMethodSelection reads the initial client greeting and returns the
// methods offered by the client.
func readMethodSelection(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, fmt.Errorf("read greeting: %w", err)
	}
	if hdr[0] != socks5Version {
		return nil, fmt.Errorf("bad SOCKS version %#x", hdr[0])
	}
	n := int(hdr[1])
	if n == 0 {
		return nil, errors.New("empty method list")
	}
	methods := make([]byte, n)
	if _, err := io.ReadFull(r, methods); err != nil {
		return nil, fmt.Errorf("read methods: %w", err)
	}
	return methods, nil
}

// writeMethodSelection tells the client which method the server picked.
func writeMethodSelection(w io.Writer, method byte) error {
	_, err := w.Write([]byte{socks5Version, method})
	return err
}

// readUserPassAuth reads RFC 1929 username/password credentials.
func readUserPassAuth(r io.Reader) (string, string, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return "", "", fmt.Errorf("read auth header: %w", err)
	}
	if hdr[0] != userPassVer {
		return "", "", fmt.Errorf("bad user/pass version %#x", hdr[0])
	}
	ulen := int(hdr[1])
	user := make([]byte, ulen)
	if _, err := io.ReadFull(r, user); err != nil {
		return "", "", fmt.Errorf("read username: %w", err)
	}
	var plenBuf [1]byte
	if _, err := io.ReadFull(r, plenBuf[:]); err != nil {
		return "", "", fmt.Errorf("read plen: %w", err)
	}
	pass := make([]byte, int(plenBuf[0]))
	if _, err := io.ReadFull(r, pass); err != nil {
		return "", "", fmt.Errorf("read password: %w", err)
	}
	return string(user), string(pass), nil
}

// writeUserPassReply sends the RFC 1929 auth status back to the client.
// A non-zero status causes the client to close the connection.
func writeUserPassReply(w io.Writer, ok bool) error {
	status := byte(0x00)
	if !ok {
		status = 0x01
	}
	_, err := w.Write([]byte{userPassVer, status})
	return err
}

// request represents a parsed SOCKS5 request header.
type request struct {
	cmd  byte
	addr string // dotted IPv4 / bracketless IPv6 / domain — no port
	port uint16
}

// target formats addr:port for net.Dial. IPv6 literals are bracketed.
func (r request) target() string {
	if ip := net.ParseIP(r.addr); ip != nil && ip.To4() == nil {
		return "[" + r.addr + "]:" + strconv.Itoa(int(r.port))
	}
	return net.JoinHostPort(r.addr, strconv.Itoa(int(r.port)))
}

// readRequest reads the CONNECT/UDP request from the client.
func readRequest(r io.Reader) (request, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return request{}, fmt.Errorf("read request header: %w", err)
	}
	if hdr[0] != socks5Version {
		return request{}, fmt.Errorf("bad SOCKS version %#x", hdr[0])
	}
	// hdr[2] is RSV (0x00) — ignore per spec.

	var addr string
	switch hdr[3] {
	case atypIPv4:
		var buf [4]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return request{}, fmt.Errorf("read ipv4: %w", err)
		}
		addr = net.IP(buf[:]).String()
	case atypIPv6:
		var buf [16]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return request{}, fmt.Errorf("read ipv6: %w", err)
		}
		addr = net.IP(buf[:]).String()
	case atypDomain:
		var lenBuf [1]byte
		if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
			return request{}, fmt.Errorf("read domain length: %w", err)
		}
		if lenBuf[0] == 0 {
			return request{}, errors.New("empty domain")
		}
		buf := make([]byte, int(lenBuf[0]))
		if _, err := io.ReadFull(r, buf); err != nil {
			return request{}, fmt.Errorf("read domain: %w", err)
		}
		addr = string(buf)
	default:
		return request{cmd: hdr[1]}, fmt.Errorf("unsupported atyp %#x", hdr[3])
	}

	var portBuf [2]byte
	if _, err := io.ReadFull(r, portBuf[:]); err != nil {
		return request{}, fmt.Errorf("read port: %w", err)
	}
	return request{
		cmd:  hdr[1],
		addr: addr,
		port: binary.BigEndian.Uint16(portBuf[:]),
	}, nil
}

// udpHeader is a parsed SOCKS5 UDP datagram header (RFC 1928 §7).
//
// Wire format:
//
//	+----+------+------+----------+----------+----------+
//	|RSV | FRAG | ATYP | DST.ADDR | DST.PORT |   DATA   |
//	+----+------+------+----------+----------+----------+
//	| 2  |  1   |  1   | Variable |    2     | Variable |
//	+----+------+------+----------+----------+----------+
type udpHeader struct {
	addr string // dotted IPv4 / bare IPv6 / domain — no port
	port uint16
}

// parseUDPDatagram parses a SOCKS5-wrapped UDP datagram and returns the
// target address plus the payload (the payload slice aliases buf). It
// rejects fragmentation (FRAG != 0) — almost nothing uses it and
// reassembly is a separate codebase's worth of work.
func parseUDPDatagram(buf []byte) (udpHeader, []byte, error) {
	if len(buf) < 4 {
		return udpHeader{}, nil, fmt.Errorf("socks5 udp: header too short (%d)", len(buf))
	}
	// buf[0:2] = RSV (must be 0x0000, ignored in practice)
	if buf[2] != 0x00 {
		return udpHeader{}, nil, fmt.Errorf("socks5 udp: fragmentation unsupported (FRAG=%#x)", buf[2])
	}

	var h udpHeader
	idx := 4 // RSV(2) + FRAG(1) + ATYP(1)
	switch buf[3] {
	case atypIPv4:
		if len(buf) < idx+4+2 {
			return h, nil, fmt.Errorf("socks5 udp: short ipv4 addr")
		}
		h.addr = net.IP(buf[idx : idx+4]).String()
		idx += 4
	case atypIPv6:
		if len(buf) < idx+16+2 {
			return h, nil, fmt.Errorf("socks5 udp: short ipv6 addr")
		}
		h.addr = net.IP(buf[idx : idx+16]).String()
		idx += 16
	case atypDomain:
		if len(buf) <= idx {
			return h, nil, fmt.Errorf("socks5 udp: short domain length")
		}
		dlen := int(buf[idx])
		idx++
		if dlen == 0 || len(buf) < idx+dlen+2 {
			return h, nil, fmt.Errorf("socks5 udp: short domain")
		}
		h.addr = string(buf[idx : idx+dlen])
		idx += dlen
	default:
		return h, nil, fmt.Errorf("socks5 udp: unsupported ATYP %#x", buf[3])
	}
	h.port = uint16(buf[idx])<<8 | uint16(buf[idx+1])
	idx += 2
	return h, buf[idx:], nil
}

// encodeUDPDatagram prepends a SOCKS5 UDP header identifying addr as the
// source peer. For the chain→client direction, "addr" is the remote target
// that sent the reply.
func encodeUDPDatagram(addr string, port uint16, payload []byte) ([]byte, error) {
	out := []byte{0x00, 0x00, 0x00} // RSV RSV FRAG
	if ip := net.ParseIP(addr); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			out = append(out, atypIPv4)
			out = append(out, v4...)
		} else {
			out = append(out, atypIPv6)
			out = append(out, ip.To16()...)
		}
	} else {
		if len(addr) == 0 || len(addr) > 255 {
			return nil, fmt.Errorf("socks5 udp: domain length %d out of range", len(addr))
		}
		out = append(out, atypDomain, byte(len(addr)))
		out = append(out, addr...)
	}
	out = append(out, byte(port>>8), byte(port))
	out = append(out, payload...)
	return out, nil
}

// writeReply sends a SOCKS5 reply. bnd is the bound address we want to
// report (usually "0.0.0.0:0" for CONNECT — clients don't consult it).
func writeReply(w io.Writer, rep byte, bnd string) error {
	host, portStr, err := net.SplitHostPort(bnd)
	if err != nil {
		// Fall back to the required-but-ignored zero BND for CONNECT replies.
		host, portStr = "0.0.0.0", "0"
	}
	port, _ := strconv.Atoi(portStr)

	buf := []byte{socks5Version, rep, 0x00}

	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			buf = append(buf, atypIPv4)
			buf = append(buf, v4...)
		} else {
			buf = append(buf, atypIPv6)
			buf = append(buf, ip.To16()...)
		}
	} else {
		// Unknown host — report IPv4 zero. Clients don't rely on BND for CONNECT.
		buf = append(buf, atypIPv4, 0, 0, 0, 0)
	}

	buf = binary.BigEndian.AppendUint16(buf, uint16(port))
	_, err = w.Write(buf)
	return err
}
