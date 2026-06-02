package trojanwire

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

	"github.com/JohnThre/clambhook/internal/protocol"
	"github.com/JohnThre/clambhook/pkg/cnet"
)

const (
	CmdConnect      = 0x01
	CmdUDPAssociate = 0x03
	ATYPIPv4        = 0x01
	ATYPDomain      = 0x03
	ATYPIPv6        = 0x04
)

type Config struct {
	Password        string
	PasswordHashHex [56]byte
	SNI             string
	ALPN            []string
	SkipVerify      bool
}

type Dialer struct {
	name   string
	server protocol.Server
	cfg    Config
}

func NewDialer(name string, server protocol.Server) (protocol.Dialer, error) {
	cfg, err := ParseConfig(name, server)
	if err != nil {
		return nil, err
	}
	return &Dialer{name: name, server: server, cfg: cfg}, nil
}

func ParseConfig(name string, s protocol.Server) (Config, error) {
	var c Config

	pw, _ := s.Settings["password"].(string)
	if pw == "" {
		return c, fmt.Errorf("%s: password is required", name)
	}
	c.Password = pw

	sum := cnet.SHA224([]byte(pw))
	hex.Encode(c.PasswordHashHex[:], sum)

	if sni, ok := s.Settings["sni"].(string); ok && sni != "" {
		c.SNI = sni
	} else {
		host, _, err := net.SplitHostPort(s.Address)
		if err != nil {
			return c, fmt.Errorf("%s: invalid server address %q: %w", name, s.Address, err)
		}
		c.SNI = host
	}

	if raw, ok := s.Settings["alpn"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && s != "" {
				c.ALPN = append(c.ALPN, s)
			}
		}
	}

	if v, ok := s.Settings["skip_cert_verify"].(bool); ok {
		c.SkipVerify = v
	}

	return c, nil
}

func (d *Dialer) Protocol() string { return d.name }

func (d *Dialer) Capabilities() protocol.Capabilities {
	return Capabilities()
}

func Capabilities() protocol.Capabilities {
	return protocol.Capabilities{
		TCP:     true,
		UDP:     true,
		UDPMode: protocol.UDPModeStream,
	}
}

func (d *Dialer) Dial(ctx context.Context, network, address string) (protocol.Conn, error) {
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", d.server.Address)
	if err != nil {
		return nil, fmt.Errorf("%s: dial %s: %w", d.name, d.server.Address, err)
	}
	tlsConn, err := d.handshake(ctx, raw, CmdConnect, address)
	if err != nil {
		return nil, err
	}
	return &Conn{Conn: tlsConn, name: d.name}, nil
}

func (d *Dialer) DialThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (protocol.Conn, error) {
	// This wire protocol always speaks TLS, even when nested inside another
	// encrypted proxy. The exit-side server expects a fresh TLS handshake.
	tlsConn, err := d.handshake(ctx, &netConnAdapter{rwc: underlying, name: d.name}, CmdConnect, address)
	if err != nil {
		return nil, err
	}
	return &Conn{Conn: tlsConn, name: d.name}, nil
}

func (d *Dialer) DialPacket(ctx context.Context, address string) (protocol.PacketConn, error) {
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", d.server.Address)
	if err != nil {
		return nil, fmt.Errorf("%s: dial %s: %w", d.name, d.server.Address, err)
	}
	if address == "" {
		address = "0.0.0.0:0"
	}
	tlsConn, err := d.handshake(ctx, raw, CmdUDPAssociate, address)
	if err != nil {
		return nil, err
	}
	return &PacketConn{tls: tlsConn, name: d.name}, nil
}

func (d *Dialer) DialPacketThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (protocol.PacketConn, error) {
	if address == "" {
		address = "0.0.0.0:0"
	}
	tlsConn, err := d.handshake(ctx, &netConnAdapter{rwc: underlying, name: d.name}, CmdUDPAssociate, address)
	if err != nil {
		return nil, err
	}
	return &PacketConn{tls: tlsConn, name: d.name}, nil
}

func (d *Dialer) handshake(ctx context.Context, raw net.Conn, cmd byte, address string) (*tls.Conn, error) {
	tlsConn := tls.Client(raw, &tls.Config{
		ServerName:         d.cfg.SNI,
		NextProtos:         d.cfg.ALPN,
		InsecureSkipVerify: d.cfg.SkipVerify,
		MinVersion:         tls.VersionTLS12,
	})

	if err := tlsConn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, fmt.Errorf("%s: tls handshake: %w", d.name, err)
	}

	header, err := EncodeHeader(d.name, d.cfg.PasswordHashHex, cmd, address)
	if err != nil {
		tlsConn.Close()
		return nil, err
	}
	if _, err := tlsConn.Write(header); err != nil {
		tlsConn.Close()
		return nil, fmt.Errorf("%s: write header: %w", d.name, err)
	}

	return tlsConn, nil
}

func EncodeHeader(name string, hashHex [56]byte, cmd byte, address string) ([]byte, error) {
	addr, err := EncodeAddr(name, address)
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

func EncodeAddr(name, address string) ([]byte, error) {
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("%s: split host/port %q: %w", name, address, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		return nil, fmt.Errorf("%s: invalid port %q", name, portStr)
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
			return nil, fmt.Errorf("%s: domain length %d out of range", name, len(host))
		}
		out = append(out, ATYPDomain, byte(len(host)))
		out = append(out, host...)
	}

	out = append(out, byte(port>>8), byte(port))
	return out, nil
}

func ReadAddr(name string, r io.Reader) (host string, port uint16, err error) {
	var atyp [1]byte
	if _, err = io.ReadFull(r, atyp[:]); err != nil {
		return "", 0, fmt.Errorf("%s: read atyp: %w", name, err)
	}
	switch atyp[0] {
	case ATYPIPv4:
		var b [4]byte
		if _, err = io.ReadFull(r, b[:]); err != nil {
			return "", 0, fmt.Errorf("%s: read ipv4: %w", name, err)
		}
		host = net.IP(b[:]).String()
	case ATYPIPv6:
		var b [16]byte
		if _, err = io.ReadFull(r, b[:]); err != nil {
			return "", 0, fmt.Errorf("%s: read ipv6: %w", name, err)
		}
		host = net.IP(b[:]).String()
	case ATYPDomain:
		var lb [1]byte
		if _, err = io.ReadFull(r, lb[:]); err != nil {
			return "", 0, fmt.Errorf("%s: read domain len: %w", name, err)
		}
		if lb[0] == 0 {
			return "", 0, fmt.Errorf("%s: %w", name, errors.New("empty domain"))
		}
		b := make([]byte, int(lb[0]))
		if _, err = io.ReadFull(r, b); err != nil {
			return "", 0, fmt.Errorf("%s: read domain: %w", name, err)
		}
		host = string(b)
	default:
		return "", 0, fmt.Errorf("%s: unsupported atyp %#x", name, atyp[0])
	}
	var pb [2]byte
	if _, err = io.ReadFull(r, pb[:]); err != nil {
		return "", 0, fmt.Errorf("%s: read port: %w", name, err)
	}
	port = uint16(pb[0])<<8 | uint16(pb[1])
	return host, port, nil
}

type Conn struct {
	*tls.Conn
	name string
}

func (c *Conn) Protocol() string { return c.name }

type netConnAdapter struct {
	rwc  io.ReadWriteCloser
	name string
}

func (a *netConnAdapter) Read(p []byte) (int, error)  { return a.rwc.Read(p) }
func (a *netConnAdapter) Write(p []byte) (int, error) { return a.rwc.Write(p) }
func (a *netConnAdapter) Close() error                { return a.rwc.Close() }

func (a *netConnAdapter) LocalAddr() net.Addr {
	if nc, ok := a.rwc.(net.Conn); ok {
		return nc.LocalAddr()
	}
	return dummyAddr{name: a.name}
}

func (a *netConnAdapter) RemoteAddr() net.Addr {
	if nc, ok := a.rwc.(net.Conn); ok {
		return nc.RemoteAddr()
	}
	return dummyAddr{name: a.name}
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

type dummyAddr struct{ name string }

func (a dummyAddr) Network() string { return a.name + "-chain" }
func (a dummyAddr) String() string  { return "chained" }

var (
	_ protocol.Dialer       = (*Dialer)(nil)
	_ protocol.PacketDialer = (*Dialer)(nil)
	_ protocol.Conn         = (*Conn)(nil)
	_ net.Conn              = (*Conn)(nil)
)
