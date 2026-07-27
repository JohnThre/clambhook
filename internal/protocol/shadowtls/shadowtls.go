// Package shadowtls implements the ShadowTLS v3 client transport
// (https://github.com/ihciah/shadow-tls/blob/master/docs/protocol-v3-en.md).
//
// ShadowTLS is a transport-obfuscation layer, not an addressed proxy: the
// client performs a genuine TLS 1.3 handshake with a real "handshake server"
// (relayed by the ShadowTLS server), then switches to its own authenticated
// application-data framing over the same TCP stream. It carries no target
// address of its own — the real destination is spoken by an inner protocol
// (typically Shadowsocks) layered on top.
//
// In this codebase ShadowTLS is therefore a stream-carrier hop that is never
// the final hop of a chain: it implements Dial + DialThrough and returns a
// protocol.Conn whose Read/Write apply ShadowTLS data framing, over which the
// next hop (shadowsocks, trojan, ...) runs its own DialThrough carrying the
// real target. A typical chain is [shadowtls -> shadowsocks].
//
// Only v3 is implemented, in strict mode: the handshake server must negotiate
// TLS 1.3. Legacy v1/v2 are out of scope.
package shadowtls

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/tls"
	"fmt"
	"io"
	"net"

	"github.com/JohnThre/clambhook/internal/protocol"
)

func init() {
	protocol.Register("shadowtls", func(server protocol.Server) (protocol.Dialer, error) {
		cfg, err := parseConfig(server)
		if err != nil {
			return nil, err
		}
		return &dialer{server: server, cfg: cfg}, nil
	})
	protocol.RegisterCapabilities("shadowtls", shadowtlsCapabilities())
}

type dialer struct {
	server protocol.Server
	cfg    config
}

func (d *dialer) Protocol() string { return "shadowtls" }

func (d *dialer) Capabilities() protocol.Capabilities { return shadowtlsCapabilities() }

// shadowtlsCapabilities reports ShadowTLS as a transparent stream carrier: it
// forwards raw bytes, so it can carry both TCP and UDP-over-stream for a later
// hop. It is never itself the final hop, so it does not implement PacketDialer;
// runtime chain validation remains authoritative for UDP.
func shadowtlsCapabilities() protocol.Capabilities {
	return protocol.Capabilities{
		TCP:     true,
		UDP:     true,
		UDPMode: protocol.UDPModeStream,
	}
}

// Dial opens a TCP connection to the ShadowTLS server and performs the v3
// handshake. address is ignored: ShadowTLS carries no destination of its own.
func (d *dialer) Dial(ctx context.Context, network, address string) (protocol.Conn, error) {
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", d.server.Address)
	if err != nil {
		return nil, fmt.Errorf("shadowtls: dial %s: %w", d.server.Address, err)
	}
	conn, err := d.handshake(ctx, raw)
	if err != nil {
		raw.Close()
		return nil, err
	}
	return conn, nil
}

// DialThrough performs the v3 handshake over an existing chained transport.
// Ownership contract: on error, underlying is closed.
func (d *dialer) DialThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (protocol.Conn, error) {
	conn, err := d.handshake(ctx, &netConnAdapter{rwc: underlying})
	if err != nil {
		underlying.Close()
		return nil, err
	}
	return conn, nil
}

// handshake drives the TLS 1.3 handshake with an HMAC-signed session id
// (injected via the deterministic two-pass Rand mechanism), validates the
// ShadowTLS authentication through the read-side hijackConn, and returns a
// data-stage connection bound to the raw transport.
func (d *dialer) handshake(ctx context.Context, raw net.Conn) (*stConn, error) {
	build := func(r io.Reader) *tls.Config {
		return &tls.Config{
			Rand:               r,
			ServerName:         d.cfg.sni,
			NextProtos:         d.cfg.alpn,
			InsecureSkipVerify: d.cfg.skipVerify,
			// v3 strict mode requires the handshake server to speak TLS 1.3.
			MinVersion: tls.VersionTLS13,
			MaxVersion: tls.VersionTLS13,
			// Force plain X25519: post-quantum hybrids draw key material in a
			// way that is not reproducible across the two deterministic passes,
			// and their larger key share would break the session-id injection.
			CurvePreferences: []tls.CurveID{tls.X25519},
			// Session resumption would change ClientHello structure across
			// passes and add a PSK; keep every handshake a fresh full one.
			SessionTicketsDisabled: true,
		}
	}

	hc, pass2Rand, err := prepareSignedHandshake(ctx, raw, d.cfg.password, build)
	if err != nil {
		return nil, err
	}

	tlsConn := tls.Client(hc, build(pass2Rand))
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("shadowtls: tls handshake: %w", err)
	}
	if !hc.isTLS13 {
		return nil, fmt.Errorf("shadowtls: handshake server did not negotiate TLS 1.3")
	}
	if !hc.authorized {
		return nil, fmt.Errorf("shadowtls: server authentication failed (traffic may be hijacked)")
	}

	hmacAdd := hmac.New(sha1.New, []byte(d.cfg.password))
	hmacAdd.Write(hc.serverRandom)
	hmacAdd.Write([]byte("C"))

	hmacVerify := hmac.New(sha1.New, []byte(d.cfg.password))
	hmacVerify.Write(hc.serverRandom)
	hmacVerify.Write([]byte("S"))

	// Data stage runs directly over the raw transport, independent of the TLS
	// library. hc.readHMAC (HMAC_ServerRandom) filters residual handshake
	// frames the server may still emit after the client switches.
	return newStConn(raw, hmacAdd, hmacVerify, hc.readHMAC), nil
}

var (
	_ protocol.Dialer = (*dialer)(nil)
	_ protocol.Conn   = (*stConn)(nil)
	_ net.Conn        = (*stConn)(nil)
)
