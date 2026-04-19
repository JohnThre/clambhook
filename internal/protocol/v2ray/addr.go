// Package v2ray holds wire-format helpers shared by V2Ray-family protocols
// (VLESS, VMess). These protocols use SOCKS5-inspired address encoding —
// ATYP || ADDR || PORT — but with different ATYP values than SOCKS5 itself,
// which is why this codec lives separately from internal/socks.
//
//	V2Ray ATYP:   0x01 IPv4,  0x02 Domain, 0x03 IPv6
//	SOCKS5 ATYP:  0x01 IPv4,  0x03 Domain, 0x04 IPv6
//
// Mixing the two would corrupt frames silently — the byte values happen to
// overlap enough to parse as a different address type rather than erroring.
package v2ray

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
)

const (
	ATYPIPv4   byte = 0x01
	ATYPDomain byte = 0x02
	ATYPIPv6   byte = 0x03
)

// EncodeAddr parses "host:port" and returns the addressing bytes used in
// V2Ray-family request headers and per-datagram frames:
//
//	ATYP (1) | ADDR (variable) | PORT (2, big-endian)
//
// ATYP selection rules:
//   - host parses as an IPv4 literal → ATYPIPv4 (4 raw bytes)
//   - host parses as an IPv6 literal → ATYPIPv6 (16 raw bytes)
//   - otherwise host is treated as a domain → ATYPDomain (1-byte length + bytes)
//
// Preferring ATYPDomain for hostnames avoids a local DNS lookup on the
// client, letting the exit server resolve the name in its own network
// context. This prevents a DNS leak — one of the main reasons users run
// traffic through a proxy in the first place.
//
// Note the argument order in V2Ray headers differs from SOCKS5: the wire
// format actually puts PORT *before* ATYP||ADDR. EncodeAddr only produces
// the ATYP||ADDR||PORT triple; protocol-specific header builders are
// responsible for any field reordering they need.
func EncodeAddr(address string) ([]byte, error) {
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("v2ray: split host/port %q: %w", address, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		return nil, fmt.Errorf("v2ray: invalid port %q", portStr)
	}

	out := make([]byte, 0, 1+len(host)+2)
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			out = append(out, ATYPIPv4)
			out = append(out, v4...)
		} else {
			out = append(out, ATYPIPv6)
			out = append(out, ip.To16()...)
		}
	} else {
		if len(host) == 0 || len(host) > 255 {
			return nil, fmt.Errorf("v2ray: domain length %d out of range", len(host))
		}
		out = append(out, ATYPDomain, byte(len(host)))
		out = append(out, host...)
	}

	out = append(out, byte(port>>8), byte(port))
	return out, nil
}

// ReadAddr reads an ATYP|ADDR|PORT triple from r, returning host and port.
// Used when parsing inbound V2Ray frames (e.g. UDP datagrams).
func ReadAddr(r io.Reader) (host string, port uint16, err error) {
	var atyp [1]byte
	if _, err = io.ReadFull(r, atyp[:]); err != nil {
		return "", 0, fmt.Errorf("v2ray: read atyp: %w", err)
	}
	switch atyp[0] {
	case ATYPIPv4:
		var b [4]byte
		if _, err = io.ReadFull(r, b[:]); err != nil {
			return "", 0, fmt.Errorf("v2ray: read ipv4: %w", err)
		}
		host = net.IP(b[:]).String()
	case ATYPIPv6:
		var b [16]byte
		if _, err = io.ReadFull(r, b[:]); err != nil {
			return "", 0, fmt.Errorf("v2ray: read ipv6: %w", err)
		}
		host = net.IP(b[:]).String()
	case ATYPDomain:
		var lb [1]byte
		if _, err = io.ReadFull(r, lb[:]); err != nil {
			return "", 0, fmt.Errorf("v2ray: read domain len: %w", err)
		}
		if lb[0] == 0 {
			return "", 0, errors.New("v2ray: empty domain")
		}
		b := make([]byte, int(lb[0]))
		if _, err = io.ReadFull(r, b); err != nil {
			return "", 0, fmt.Errorf("v2ray: read domain: %w", err)
		}
		host = string(b)
	default:
		return "", 0, fmt.Errorf("v2ray: unsupported atyp %#x", atyp[0])
	}
	var pb [2]byte
	if _, err = io.ReadFull(r, pb[:]); err != nil {
		return "", 0, fmt.Errorf("v2ray: read port: %w", err)
	}
	port = uint16(pb[0])<<8 | uint16(pb[1])
	return host, port, nil
}
