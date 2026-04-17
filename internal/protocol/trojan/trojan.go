package trojan

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/clambhook/clambhook/internal/protocol"
	"github.com/clambhook/clambhook/pkg/cnet"
)

func init() {
	protocol.Register("trojan", func(server protocol.Server) (protocol.Dialer, error) {
		cfg, err := parseConfig(server)
		if err != nil {
			return nil, err
		}
		return &dialer{server: server, cfg: cfg}, nil
	})
}

const (
	cmdConnect      = 0x01
	cmdUDPAssociate = 0x03
	atypIPv4        = 0x01
	atypDomain      = 0x03
	atypIPv6        = 0x04
)

type dialer struct {
	server protocol.Server
	cfg    config
}

type config struct {
	password        string
	passwordHashHex [56]byte
	sni             string
	alpn            []string
	skipVerify      bool
}

func parseConfig(s protocol.Server) (config, error) {
	var c config

	pw, _ := s.Settings["password"].(string)
	if pw == "" {
		return c, errors.New("trojan: password is required")
	}
	c.password = pw

	sum := cnet.SHA224([]byte(pw))
	hex.Encode(c.passwordHashHex[:], sum)

	if sni, ok := s.Settings["sni"].(string); ok && sni != "" {
		c.sni = sni
	} else {
		host, _, err := net.SplitHostPort(s.Address)
		if err != nil {
			return c, fmt.Errorf("trojan: invalid server address %q: %w", s.Address, err)
		}
		c.sni = host
	}

	if raw, ok := s.Settings["alpn"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && s != "" {
				c.alpn = append(c.alpn, s)
			}
		}
	}

	if v, ok := s.Settings["skip_cert_verify"].(bool); ok {
		c.skipVerify = v
	}

	return c, nil
}

func (d *dialer) Protocol() string { return "trojan" }

func (d *dialer) Dial(ctx context.Context, network, address string) (protocol.Conn, error) {
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", d.server.Address)
	if err != nil {
		return nil, fmt.Errorf("trojan: dial %s: %w", d.server.Address, err)
	}
	tlsConn, err := d.handshake(ctx, raw, cmdConnect, address)
	if err != nil {
		return nil, err
	}
	return &conn{Conn: tlsConn}, nil
}

func (d *dialer) DialThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (protocol.Conn, error) {
	// Trojan always speaks TLS — even when nested inside another encrypted proxy,
	// the exit-side server expects a fresh TLS handshake.
	tlsConn, err := d.handshake(ctx, &netConnAdapter{rwc: underlying}, cmdConnect, address)
	if err != nil {
		return nil, err
	}
	return &conn{Conn: tlsConn}, nil
}

// DialPacket opens a trojan UDP_ASSOCIATE session. address is the
// placeholder target written into the opening header — trojan servers
// don't use it for routing UDP (per-datagram destinations ride in the
// frame). If the caller passes "" we use "0.0.0.0:0".
func (d *dialer) DialPacket(ctx context.Context, address string) (protocol.PacketConn, error) {
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", d.server.Address)
	if err != nil {
		return nil, fmt.Errorf("trojan: dial %s: %w", d.server.Address, err)
	}
	if address == "" {
		address = "0.0.0.0:0"
	}
	tlsConn, err := d.handshake(ctx, raw, cmdUDPAssociate, address)
	if err != nil {
		return nil, err
	}
	return &packetConn{tls: tlsConn}, nil
}

// DialPacketThrough opens a trojan UDP_ASSOCIATE session over an existing
// tunneled stream (inner hop of a chain).
func (d *dialer) DialPacketThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (protocol.PacketConn, error) {
	if address == "" {
		address = "0.0.0.0:0"
	}
	tlsConn, err := d.handshake(ctx, &netConnAdapter{rwc: underlying}, cmdUDPAssociate, address)
	if err != nil {
		return nil, err
	}
	return &packetConn{tls: tlsConn}, nil
}

// handshake runs TLS over raw, then writes the Trojan request header targeting
// address with the given CMD. The returned tls.Conn is the post-handshake
// session — callers (Dial / DialPacket) wrap it in the appropriate type.
func (d *dialer) handshake(ctx context.Context, raw net.Conn, cmd byte, address string) (*tls.Conn, error) {
	tlsConn := tls.Client(raw, &tls.Config{
		ServerName:         d.cfg.sni,
		NextProtos:         d.cfg.alpn,
		InsecureSkipVerify: d.cfg.skipVerify,
		MinVersion:         tls.VersionTLS12,
	})

	if err := tlsConn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, fmt.Errorf("trojan: tls handshake: %w", err)
	}

	header, err := encodeHeader(d.cfg.passwordHashHex, cmd, address)
	if err != nil {
		tlsConn.Close()
		return nil, err
	}
	if _, err := tlsConn.Write(header); err != nil {
		tlsConn.Close()
		return nil, fmt.Errorf("trojan: write header: %w", err)
	}

	return tlsConn, nil
}

// encodeHeader builds: hex(SHA224(password)) | CRLF | CMD | ATYP | ADDR | PORT | CRLF
func encodeHeader(hashHex [56]byte, cmd byte, address string) ([]byte, error) {
	addr, err := encodeAddr(address)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, 56+2+1+len(addr)+2)
	out = append(out, hashHex[:]...)
	out = append(out, '\r', '\n')
	out = append(out, cmd)
	out = append(out, addr...)
	out = append(out, '\r', '\n')
	return out, nil
}

// encodeAddr parses "host:port" and returns the addressing bytes used in
// Trojan request headers and UDP datagram frames:
//
//	ATYP (1) | ADDR (variable) | PORT (2, big-endian)
//
// ATYP selection rules:
//   - If host parses as an IPv4 literal → atypIPv4 (4 raw bytes)
//   - If host parses as an IPv6 literal → atypIPv6 (16 raw bytes)
//   - Otherwise, treat host as a domain → atypDomain (1-byte length prefix + bytes)
//
// Why this matters: preferring atypDomain for hostnames avoids a local DNS
// lookup on the client, letting the exit server resolve the name in its own
// network context. This prevents a DNS leak — one of the main reasons users
// run traffic through a proxy in the first place.
func encodeAddr(address string) ([]byte, error) {
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("trojan: split host/port %q: %w", address, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		return nil, fmt.Errorf("trojan: invalid port %q", portStr)
	}

	out := make([]byte, 0, 1+len(host)+2)
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			out = append(out, atypIPv4)
			out = append(out, v4...)
		} else {
			out = append(out, atypIPv6)
			out = append(out, ip.To16()...)
		}
	} else {
		if len(host) == 0 || len(host) > 255 {
			return nil, fmt.Errorf("trojan: domain length %d out of range", len(host))
		}
		out = append(out, atypDomain, byte(len(host)))
		out = append(out, host...)
	}

	out = append(out, byte(port>>8), byte(port))
	return out, nil
}

// readAddr reads an ATYP|ADDR|PORT triple from r, returning host:port.
// Used when parsing incoming trojan UDP datagram frames.
func readAddr(r io.Reader) (host string, port uint16, err error) {
	var atyp [1]byte
	if _, err = io.ReadFull(r, atyp[:]); err != nil {
		return "", 0, fmt.Errorf("trojan: read atyp: %w", err)
	}
	switch atyp[0] {
	case atypIPv4:
		var b [4]byte
		if _, err = io.ReadFull(r, b[:]); err != nil {
			return "", 0, fmt.Errorf("trojan: read ipv4: %w", err)
		}
		host = net.IP(b[:]).String()
	case atypIPv6:
		var b [16]byte
		if _, err = io.ReadFull(r, b[:]); err != nil {
			return "", 0, fmt.Errorf("trojan: read ipv6: %w", err)
		}
		host = net.IP(b[:]).String()
	case atypDomain:
		var lb [1]byte
		if _, err = io.ReadFull(r, lb[:]); err != nil {
			return "", 0, fmt.Errorf("trojan: read domain len: %w", err)
		}
		if lb[0] == 0 {
			return "", 0, errors.New("trojan: empty domain")
		}
		b := make([]byte, int(lb[0]))
		if _, err = io.ReadFull(r, b); err != nil {
			return "", 0, fmt.Errorf("trojan: read domain: %w", err)
		}
		host = string(b)
	default:
		return "", 0, fmt.Errorf("trojan: unsupported atyp %#x", atyp[0])
	}
	var pb [2]byte
	if _, err = io.ReadFull(r, pb[:]); err != nil {
		return "", 0, fmt.Errorf("trojan: read port: %w", err)
	}
	port = uint16(pb[0])<<8 | uint16(pb[1])
	return host, port, nil
}

type conn struct {
	*tls.Conn
}

func (c *conn) Protocol() string { return "trojan" }

// netConnAdapter promotes an io.ReadWriteCloser to net.Conn so tls.Client can
// wrap a chained connection. The address and deadline methods are best-effort:
// if the underlying connection happens to be a net.Conn, we delegate; otherwise
// we return zero values.
type netConnAdapter struct {
	rwc io.ReadWriteCloser
}

func (a *netConnAdapter) Read(p []byte) (int, error)  { return a.rwc.Read(p) }
func (a *netConnAdapter) Write(p []byte) (int, error) { return a.rwc.Write(p) }
func (a *netConnAdapter) Close() error                { return a.rwc.Close() }

func (a *netConnAdapter) LocalAddr() net.Addr {
	if nc, ok := a.rwc.(net.Conn); ok {
		return nc.LocalAddr()
	}
	return dummyAddr{}
}

func (a *netConnAdapter) RemoteAddr() net.Addr {
	if nc, ok := a.rwc.(net.Conn); ok {
		return nc.RemoteAddr()
	}
	return dummyAddr{}
}

func (a *netConnAdapter) SetDeadline(t time.Time) error {
	if nc, ok := a.rwc.(net.Conn); ok {
		return nc.SetDeadline(t)
	}
	return nil
}

func (a *netConnAdapter) SetReadDeadline(t time.Time) error {
	if nc, ok := a.rwc.(net.Conn); ok {
		return nc.SetReadDeadline(t)
	}
	return nil
}

func (a *netConnAdapter) SetWriteDeadline(t time.Time) error {
	if nc, ok := a.rwc.(net.Conn); ok {
		return nc.SetWriteDeadline(t)
	}
	return nil
}

type dummyAddr struct{}

func (dummyAddr) Network() string { return "trojan-chain" }
func (dummyAddr) String() string  { return "chained" }
