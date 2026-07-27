// Package vmess implements the VMESS-AEAD client protocol as used by v2ray,
// xray, and sing-box. It speaks the modern AEAD request header (alterId 0 only)
// over raw TCP or TCP+TLS, with an aes-128-gcm or chacha20-poly1305 chunked
// body stream.
//
// Out of scope: legacy MD5-authenticated headers (alterId > 0), the CFB/none
// body ciphers, and non-TCP transports (WebSocket, gRPC, HTTP/2).
//
// Wire format (client → server), per connection:
//
//	authID(16) || sealed_len(2+16) || conn_nonce(8) || sealed_header(hdr+16)
//	then chunk stream: [len(2 BE) || AEAD(payload)||tag(16)] ...
//
// The plaintext request header carries the version, per-connection body key/IV,
// response-auth byte, options, security selector, command, VMESS address
// triple, and an FNV1a checksum. Response body keys are derived from the request
// keys via SHA-256 so both sides agree without extra negotiation.
package vmess

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strconv"

	"github.com/JohnThre/clambhook/internal/protocol"
)

func init() {
	protocol.Register("vmess", func(server protocol.Server) (protocol.Dialer, error) {
		cfg, err := parseConfig(server)
		if err != nil {
			return nil, err
		}
		return &dialer{server: server, cfg: cfg}, nil
	})
	protocol.RegisterCapabilities("vmess", vmessCapabilities())
}

type dialer struct {
	server protocol.Server
	cfg    config
}

func (d *dialer) Protocol() string { return "vmess" }

func (d *dialer) Capabilities() protocol.Capabilities { return vmessCapabilities() }

func vmessCapabilities() protocol.Capabilities {
	return protocol.Capabilities{
		TCP:     true,
		UDP:     true,
		UDPMode: protocol.UDPModeStream,
	}
}

func (d *dialer) Dial(ctx context.Context, network, address string) (protocol.Conn, error) {
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", d.server.Address)
	if err != nil {
		return nil, fmt.Errorf("vmess: dial %s: %w", d.server.Address, err)
	}
	transport, err := d.maybeTLS(ctx, raw)
	if err != nil {
		raw.Close()
		return nil, err
	}
	conn, err := d.handshake(transport, cmdTCP, address)
	if err != nil {
		transport.Close()
		return nil, err
	}
	return conn, nil
}

func (d *dialer) DialThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (protocol.Conn, error) {
	transport, err := d.maybeTLS(ctx, &netConnAdapter{rwc: underlying})
	if err != nil {
		underlying.Close()
		return nil, err
	}
	conn, err := d.handshake(transport, cmdTCP, address)
	if err != nil {
		transport.Close()
		return nil, err
	}
	return conn, nil
}

func (d *dialer) DialPacket(ctx context.Context, address string) (protocol.PacketConn, error) {
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", d.server.Address)
	if err != nil {
		return nil, fmt.Errorf("vmess: dial %s: %w", d.server.Address, err)
	}
	transport, err := d.maybeTLS(ctx, raw)
	if err != nil {
		raw.Close()
		return nil, err
	}
	pc, err := d.handshakePacket(transport, address)
	if err != nil {
		transport.Close()
		return nil, err
	}
	return pc, nil
}

func (d *dialer) DialPacketThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (protocol.PacketConn, error) {
	transport, err := d.maybeTLS(ctx, &netConnAdapter{rwc: underlying})
	if err != nil {
		underlying.Close()
		return nil, err
	}
	pc, err := d.handshakePacket(transport, address)
	if err != nil {
		transport.Close()
		return nil, err
	}
	return pc, nil
}

// maybeTLS wraps raw in a TLS client when the config requests it, otherwise
// returns raw unchanged.
func (d *dialer) maybeTLS(ctx context.Context, raw net.Conn) (net.Conn, error) {
	if !d.cfg.tls {
		return raw, nil
	}
	tlsConn := tls.Client(raw, &tls.Config{
		ServerName:         d.cfg.sni,
		NextProtos:         d.cfg.alpn,
		InsecureSkipVerify: d.cfg.skipVerify,
		MinVersion:         tls.VersionTLS12,
	})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("vmess: tls handshake: %w", err)
	}
	return tlsConn, nil
}

// handshake generates a session, writes the AEAD request header + returns a conn
// whose write side is ready. The response header is verified lazily on Read.
func (d *dialer) handshake(transport net.Conn, cmd byte, address string) (*vmessConn, error) {
	sess, err := newSession()
	if err != nil {
		return nil, err
	}
	header, err := encodeRequestHeader(&d.cfg, sess, cmd, address)
	if err != nil {
		return nil, err
	}
	if _, err := transport.Write(header); err != nil {
		return nil, fmt.Errorf("vmess: write request header: %w", err)
	}
	cw, err := newChunkWriter(transport, d.cfg.security, sess.requestBodyKey[:], sess.requestBodyIV[:])
	if err != nil {
		return nil, err
	}
	return &vmessConn{rwc: transport, sess: sess, cfg: &d.cfg, cw: cw}, nil
}

func (d *dialer) handshakePacket(transport net.Conn, address string) (*vmessPacketConn, error) {
	conn, err := d.handshake(transport, cmdUDP, address)
	if err != nil {
		return nil, err
	}
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("vmess: split udp target %q: %w", address, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("vmess: invalid udp port %q: %w", portStr, err)
	}
	return &vmessPacketConn{conn: conn, target: packetAddr{host: host, port: port}}, nil
}

var (
	_ protocol.Dialer       = (*dialer)(nil)
	_ protocol.PacketDialer = (*dialer)(nil)
	_ protocol.Conn         = (*vmessConn)(nil)
	_ net.Conn              = (*vmessConn)(nil)
	_ protocol.PacketConn   = (*vmessPacketConn)(nil)
)
