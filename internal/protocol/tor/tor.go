// Package tor implements a clambhook protocol that routes traffic through
// a locally-running tor daemon via its SOCKS5 port.
//
// This is NOT a native onion-routing client — clambhook does not implement
// the tor protocol (circuit construction, relay directory consensus, etc.).
// Instead, the user runs tor themselves (e.g. `brew install tor && tor`),
// and clambhook speaks SOCKS5 to it. This matches how tor is normally used
// as a proxy backend: tor's own documentation recommends pointing clients
// at its SOCKS5 port rather than reimplementing the protocol.
//
// Key properties:
//   - TCP-only. Tor does not carry UDP, so we intentionally do not implement
//     protocol.PacketDialer. The chain package type-asserts PacketDialer and
//     returns a clean SOCKS5 reply 0x07 when UDP is requested.
//   - .onion hosts work for free: internal/socks.EncodeAddr selects ATYPDomain
//     for any non-IP host, and tor is specifically designed to resolve those
//     names inside its own circuit.
//   - Stream isolation: set isolation_user / isolation_pass in TOML. Tor
//     treats each unique credential pair as a separate circuit token (see
//     torrc SOCKSPort IsolateSOCKSAuth, which is on by default).
package tor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/JohnThre/clambhook/internal/protocol"
)

func init() {
	protocol.Register("tor", func(server protocol.Server) (protocol.Dialer, error) {
		cfg, err := parseConfig(server)
		if err != nil {
			return nil, err
		}
		return &dialer{server: server, cfg: cfg}, nil
	})
	protocol.RegisterCapabilities("tor", torCapabilities())
}

type dialer struct {
	server protocol.Server
	cfg    config
}

type conn struct {
	net.Conn
}

func (*conn) Protocol() string { return "tor" }

// Compile-time interface assertion — keeps us from silently breaking
// protocol.Dialer if someone renames a method on the interface.
var _ protocol.Dialer = (*dialer)(nil)

func (d *dialer) Protocol() string { return "tor" }

func (d *dialer) Capabilities() protocol.Capabilities {
	return torCapabilities()
}

func torCapabilities() protocol.Capabilities {
	return protocol.Capabilities{
		TCP:       true,
		UDP:       false,
		UDPMode:   protocol.UDPModeUnsupported,
		UDPReason: "Tor does not carry UDP",
	}
}

func (d *dialer) Dial(ctx context.Context, network, address string) (protocol.Conn, error) {
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", d.cfg.socksAddr)
	if err != nil {
		return nil, fmt.Errorf("tor: dial SOCKS5 port %s: %w", d.cfg.socksAddr, err)
	}
	if err := d.handshake(ctx, raw, address); err != nil {
		raw.Close()
		return nil, err
	}
	return &conn{Conn: raw}, nil
}

func (d *dialer) DialThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (protocol.Conn, error) {
	adapter := &netConnAdapter{rwc: underlying}
	if err := d.handshake(ctx, adapter, address); err != nil {
		// The SOCKS5 handshake failed, so the tunnel to `address` never
		// came up and this stream is unusable. We take ownership of the
		// underlying conn the moment it's handed to us, so close it on
		// every error path rather than leaking the prior chain hop's
		// socket — mirroring how Dial closes its freshly dialed conn.
		_ = adapter.Close()
		return nil, err
	}
	return &conn{Conn: adapter}, nil
}

// handshake runs the SOCKS5 exchange. The passed nc carries the bytes to
// tor's SOCKS5 port (whether a freshly dialed TCP conn or a chained
// stream). On success, nc is positioned at the start of the tunnelled
// payload and the caller can treat it as a bidirectional stream to
// `address`.
//
// ctx only gates the dial itself (above) — the SOCKS5 handshake uses nc's
// read/write deadlines if the caller installs them. We don't install a
// default handshake timeout here because the chain/listener layer owns
// end-to-end deadlines and imposing another one would surprise callers
// who set their own via ctx upstream.
func (d *dialer) handshake(_ context.Context, nc net.Conn, address string) error {
	if address == "" {
		return errors.New("tor: target address is required")
	}
	return socks5Connect(nc, address, d.cfg.isolationUser, d.cfg.isolationPass)
}
