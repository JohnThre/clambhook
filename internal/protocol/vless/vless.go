// Package vless implements the VLESS outbound client, wrapped in TLS.
//
// VLESS has no built-in encryption — security is delegated entirely to the
// TLS layer beneath it. The wire format is a tiny plaintext header on top of
// the TLS stream, followed by raw application bytes in both directions.
//
// Scope of this v1 implementation:
//   - Transport: TCP + TLS only (no WebSocket/gRPC/Reality/mKCP/QUIC).
//   - Flow: "none" only (no xtls-rprx-vision, no xudp, no Mux).
//   - UDP: single-target per session, framed as [addr][len][payload].
package vless

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/clambhook/clambhook/internal/protocol"
)

func init() {
	protocol.Register("vless", func(server protocol.Server) (protocol.Dialer, error) {
		cfg, err := parseConfig(server)
		if err != nil {
			return nil, err
		}
		return &dialer{server: server, cfg: cfg}, nil
	})
}

type dialer struct {
	server protocol.Server
	cfg    config
}

func (d *dialer) Protocol() string { return "vless" }

func (d *dialer) Dial(ctx context.Context, network, address string) (protocol.Conn, error) {
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", d.server.Address)
	if err != nil {
		return nil, fmt.Errorf("vless: dial %s: %w", d.server.Address, err)
	}
	tlsConn, err := d.handshake(ctx, raw, cmdTCP, address)
	if err != nil {
		return nil, err
	}
	return &conn{Conn: tlsConn}, nil
}

func (d *dialer) DialThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (protocol.Conn, error) {
	tlsConn, err := d.handshake(ctx, &netConnAdapter{rwc: underlying}, cmdTCP, address)
	if err != nil {
		return nil, err
	}
	return &conn{Conn: tlsConn}, nil
}

// DialPacket opens a VLESS single-target UDP session. The per-session target
// comes from address — per-datagram destinations (WriteTo's addr argument)
// are ignored, matching Trojan's simplification.
func (d *dialer) DialPacket(ctx context.Context, address string) (protocol.PacketConn, error) {
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", d.server.Address)
	if err != nil {
		return nil, fmt.Errorf("vless: dial %s: %w", d.server.Address, err)
	}
	tlsConn, err := d.handshake(ctx, raw, cmdUDP, address)
	if err != nil {
		return nil, err
	}
	return &packetConn{tls: tlsConn, target: address}, nil
}

// DialPacketThrough opens a VLESS UDP session over an already-chained stream.
func (d *dialer) DialPacketThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (protocol.PacketConn, error) {
	tlsConn, err := d.handshake(ctx, &netConnAdapter{rwc: underlying}, cmdUDP, address)
	if err != nil {
		return nil, err
	}
	return &packetConn{tls: tlsConn, target: address}, nil
}

// handshake runs TLS over raw, then writes the VLESS request header. Shared
// by TCP and UDP dial paths (cmd differs).
func (d *dialer) handshake(ctx context.Context, raw net.Conn, cmd byte, address string) (*tls.Conn, error) {
	tlsConn := tls.Client(raw, &tls.Config{
		ServerName:         d.cfg.sni,
		NextProtos:         d.cfg.alpn,
		InsecureSkipVerify: d.cfg.skipVerify,
		MinVersion:         tls.VersionTLS12,
	})

	if err := tlsConn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, fmt.Errorf("vless: tls handshake: %w", err)
	}

	header, err := encodeRequest(d.cfg.uuid, cmd, address)
	if err != nil {
		tlsConn.Close()
		return nil, err
	}
	if _, err := tlsConn.Write(header); err != nil {
		tlsConn.Close()
		return nil, fmt.Errorf("vless: write header: %w", err)
	}
	return tlsConn, nil
}

// conn wraps a post-handshake TLS connection and strips the VLESS response
// header on the first Read. The header is small (2 bytes + optional addons)
// and is consumed lazily so Dial() doesn't block on server data.
type conn struct {
	*tls.Conn
	readOnce sync.Once
	readErr  error
}

func (c *conn) Protocol() string { return "vless" }

func (c *conn) Read(p []byte) (int, error) {
	c.readOnce.Do(func() {
		c.readErr = readResponse(c.Conn)
	})
	if c.readErr != nil {
		return 0, c.readErr
	}
	return c.Conn.Read(p)
}

// netConnAdapter promotes an io.ReadWriteCloser to net.Conn so tls.Client can
// wrap a chained connection. Mirrors trojan/shadowsocks — a fourth protocol
// sharing this pattern would be the right moment to extract; until then,
// duplication is cheaper than coupling.
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

func (dummyAddr) Network() string { return "vless-chain" }
func (dummyAddr) String() string  { return "chained" }

// Compile-time guards.
var (
	_ protocol.Dialer       = (*dialer)(nil)
	_ protocol.PacketDialer = (*dialer)(nil)
	_ protocol.Conn         = (*conn)(nil)
	_ net.Conn              = (*conn)(nil)
)
