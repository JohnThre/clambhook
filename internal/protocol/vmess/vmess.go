// Package vmess implements the VMess AEAD protocol (V2Fly AEAD spec) as an
// outbound-only client. The legacy MD5-authenticated handshake is not
// implemented — clients must use alter_id=0 and an AEAD data cipher
// (aes-128-gcm or chacha20-poly1305).
//
// Wire shape (client → server):
//
//	[ AuthID(16) | encLen(18) | nonce(8) | encHeader(N+16) ]
//	[ body chunk 1 ] [ body chunk 2 ] ...
//
// Each body chunk is [masked_len(2) | ciphertext+tag], with mask bytes pulled
// from a SHAKE-128 stream keyed by the per-request body IV (Option M).
//
// Scope for v1:
//   - Transport: raw TCP (tls=false) or TCP+TLS (default).
//   - Data ciphers: aes-128-gcm, chacha20-poly1305.
//   - UDP: single-target session, cmd=0x02, body chunks carry raw datagrams.
//   - Out of scope: GlobalPadding, AuthenticatedLength, Mux/XUDP, dynamic
//     port, response cmd handling, WebSocket/gRPC/mKCP transports.
package vmess

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/clambhook/clambhook/internal/protocol"
)

func init() {
	protocol.Register("vmess", func(server protocol.Server) (protocol.Dialer, error) {
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

func (d *dialer) Protocol() string { return "vmess" }

func (d *dialer) Dial(ctx context.Context, network, address string) (protocol.Conn, error) {
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", d.server.Address)
	if err != nil {
		return nil, fmt.Errorf("vmess: dial %s: %w", d.server.Address, err)
	}
	return d.handshake(ctx, raw, cmdTCP, address, false)
}

func (d *dialer) DialThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (protocol.Conn, error) {
	return d.handshake(ctx, &netConnAdapter{rwc: underlying}, cmdTCP, address, false)
}

func (d *dialer) DialPacket(ctx context.Context, address string) (protocol.PacketConn, error) {
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", d.server.Address)
	if err != nil {
		return nil, fmt.Errorf("vmess: dial %s: %w", d.server.Address, err)
	}
	inner, err := d.handshake(ctx, raw, cmdUDP, address, true)
	if err != nil {
		return nil, err
	}
	c := inner.(*conn)
	return &packetConn{inner: c, target: address}, nil
}

func (d *dialer) DialPacketThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (protocol.PacketConn, error) {
	inner, err := d.handshake(ctx, &netConnAdapter{rwc: underlying}, cmdUDP, address, true)
	if err != nil {
		return nil, err
	}
	c := inner.(*conn)
	return &packetConn{inner: c, target: address}, nil
}

// handshake wraps `raw` in TLS if configured, writes the AEAD-sealed request
// header, and returns a conn ready for body I/O. For UDP (asPacket=true) the
// caller post-processes into a packetConn; the underlying wire shape is
// identical — VMess UDP is just TCP with cmd=0x02.
func (d *dialer) handshake(ctx context.Context, raw net.Conn, cmd byte, address string, asPacket bool) (protocol.Conn, error) {
	var transport net.Conn = raw
	if d.cfg.useTLS {
		tlsConn := tls.Client(raw, &tls.Config{
			ServerName:         d.cfg.sni,
			NextProtos:         d.cfg.alpn,
			InsecureSkipVerify: d.cfg.skipVerify,
			MinVersion:         tls.VersionTLS12,
		})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			raw.Close()
			return nil, fmt.Errorf("vmess: tls handshake: %w", err)
		}
		transport = tlsConn
	}

	header, state, err := encodeRequest(d.cfg, cmd, address)
	if err != nil {
		transport.Close()
		return nil, err
	}
	if _, err := transport.Write(header); err != nil {
		transport.Close()
		return nil, fmt.Errorf("vmess: write header: %w", err)
	}

	c, err := newConn(transport, state, d.cfg.security)
	if err != nil {
		transport.Close()
		return nil, err
	}
	_ = asPacket // currently no branching — flag reserved for future XUDP work
	return c, nil
}

// netConnAdapter promotes an io.ReadWriteCloser to net.Conn so the TLS
// client (and the chunk codec, when it delegates to net.Conn deadlines) has
// a consistent surface. Mirrors trojan/shadowsocks/vless — see the note in
// those packages about extracting at the fourth user.
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

func (dummyAddr) Network() string { return "vmess-chain" }
func (dummyAddr) String() string  { return "chained" }

// Compile-time guards.
var (
	_ protocol.Dialer       = (*dialer)(nil)
	_ protocol.PacketDialer = (*dialer)(nil)
)
