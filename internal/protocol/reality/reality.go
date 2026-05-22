// Package reality implements the XTLS Reality client handshake — an
// anti-censorship TLS camouflage that makes a tunneled connection
// indistinguishable from a normal browser HTTPS request to a popular
// decoy site. Reality is almost always composed with VLESS as the inner
// protocol; this package exposes both a standalone protocol.Dialer (for
// chained or bare use) and a Client() helper that VLESS calls directly
// when configured with security = "reality".
//
// Wire format reference: xray-core transport/internet/reality/reality.go.
// See handshake.go for the bit-level layout; session.go + verify.go hold
// the parts unit tests can exercise without a live server.
//
// Out of scope in v1: SpiderX (post-auth-failure decoy crawl) and
// xtls-rprx-vision flow. A failed server verification tears the
// connection down immediately — functionally correct but slightly more
// fingerprintable to active probers than xray's full behavior.
package reality

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/JohnThre/clambhook/internal/protocol"
)

func init() {
	protocol.Register("reality", func(server protocol.Server) (protocol.Dialer, error) {
		opts, err := ParseOptions(server)
		if err != nil {
			return nil, err
		}
		return &dialer{server: server, opts: opts}, nil
	})
}

type dialer struct {
	server protocol.Server
	opts   Options
}

func (d *dialer) Protocol() string { return "reality" }

// Dial opens a TCP connection to the configured Reality server and
// performs the Reality handshake over it. The address argument is
// unused — Reality is a transport, not a routed protocol; the inner
// protocol (usually VLESS) carries target addresses.
func (d *dialer) Dial(ctx context.Context, network, address string) (protocol.Conn, error) {
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", d.server.Address)
	if err != nil {
		return nil, fmt.Errorf("reality: dial %s: %w", d.server.Address, err)
	}
	inner, err := Client(ctx, raw, d.opts)
	if err != nil {
		raw.Close()
		return nil, err
	}
	return &conn{Conn: inner}, nil
}

// DialThrough runs Reality over an already-established connection from
// a prior hop. Reality does not carry an inner address, so address is
// ignored for the same reason as Dial.
func (d *dialer) DialThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (protocol.Conn, error) {
	inner, err := Client(ctx, &netConnAdapter{rwc: underlying}, d.opts)
	if err != nil {
		return nil, err
	}
	return &conn{Conn: inner}, nil
}

// conn wraps a post-Reality-handshake net.Conn as a protocol.Conn. It is
// just a Protocol-name tag over the underlying connection; all actual
// I/O is delegated to the embedded conn.
type conn struct {
	net.Conn
}

func (c *conn) Protocol() string { return "reality" }

// netConnAdapter promotes an io.ReadWriteCloser into a net.Conn so the
// handshake can call SetReadDeadline / SetWriteDeadline without
// reflecting on the underlying type. Copy of the same helper in
// vless/vless.go and trojan/trojan.go — the fourth copy is a good
// moment to extract, but that's a separate cleanup.
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

func (dummyAddr) Network() string { return "reality-chain" }
func (dummyAddr) String() string  { return "chained" }

var (
	_ protocol.Dialer = (*dialer)(nil)
	_ protocol.Conn   = (*conn)(nil)
)
