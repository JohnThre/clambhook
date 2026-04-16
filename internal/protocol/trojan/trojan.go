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
	cmdConnect = 0x01
	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04
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
	return d.handshake(ctx, raw, address)
}

func (d *dialer) DialThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (protocol.Conn, error) {
	// Trojan always speaks TLS — even when nested inside another encrypted proxy,
	// the exit-side server expects a fresh TLS handshake.
	return d.handshake(ctx, &netConnAdapter{rwc: underlying}, address)
}

// handshake runs TLS over raw, then writes the Trojan request header targeting
// address. The returned conn is the post-handshake TLS session.
func (d *dialer) handshake(ctx context.Context, raw net.Conn, address string) (protocol.Conn, error) {
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

	header, err := encodeHeader(d.cfg.passwordHashHex, address)
	if err != nil {
		tlsConn.Close()
		return nil, err
	}
	if _, err := tlsConn.Write(header); err != nil {
		tlsConn.Close()
		return nil, fmt.Errorf("trojan: write header: %w", err)
	}

	return &conn{Conn: tlsConn}, nil
}

// encodeHeader builds: hex(SHA224(password)) | CRLF | CMD | ATYP | ADDR | PORT | CRLF
func encodeHeader(hashHex [56]byte, address string) ([]byte, error) {
	addr, err := encodeAddress(address)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, 56+2+len(addr)+2)
	out = append(out, hashHex[:]...)
	out = append(out, '\r', '\n')
	out = append(out, addr...)
	out = append(out, '\r', '\n')
	return out, nil
}

// encodeAddress parses "host:port" and returns the Trojan request body:
//
//	CMD (1) | ATYP (1) | ADDR (variable) | PORT (2, big-endian)
//
// CMD is always 0x01 (CONNECT) in this implementation.
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
//
func encodeAddress(address string) ([]byte, error) {
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("trojan: split host/port %q: %w", address, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("trojan: invalid port %q", portStr)
	}

	out := make([]byte, 0, 1+1+1+len(host)+2)
	out = append(out, cmdConnect)

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
