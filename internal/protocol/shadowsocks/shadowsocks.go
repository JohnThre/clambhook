// Package shadowsocks implements the legacy Shadowsocks AEAD-2018 protocol
// (https://shadowsocks.org/doc/aead.html). It supports three cipher methods:
// aes-128-gcm, aes-256-gcm, and chacha20-ietf-poly1305.
//
// Shadowsocks-2022 (xchacha20-2022, aes-2022-*, PSKs) and pluggable obfuscators
// (simple-obfs, v2ray-plugin) are out of scope for this package.
//
// Wire format (per connection, each direction independently):
//
//	[salt(saltSize)] then chunked stream:
//	  chunk 1: [enc_len(2 BE) || len_tag(16)] [enc(req_header) || hdr_tag(16)]
//	  chunk n (n>1): [enc_len(2 BE) || len_tag(16)] [enc(payload) || payload_tag(16)]
//
// The first outbound chunk carries a SOCKS5-style address triple
// (ATYP || ADDR || PORT); subsequent chunks are raw application data.
// Inbound chunks are all raw application data (the server doesn't echo
// an address).
package shadowsocks

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/JohnThre/clambhook/internal/protocol"
	"github.com/JohnThre/clambhook/internal/socks"
)

var ssSubkeyInfo = []byte("ss-subkey")

func init() {
	protocol.Register("shadowsocks", func(server protocol.Server) (protocol.Dialer, error) {
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

type config struct {
	method    string
	password  string
	spec      *cipherSpec
	masterKey []byte
}

// parseConfig extracts and validates Shadowsocks-specific settings from the
// shared Server struct. Keeps the same shape as trojan's parseConfig (which
// uses the ok-form of type assertion with type-aware defaults).
func parseConfig(s protocol.Server) (config, error) {
	var c config

	method, _ := s.Settings["method"].(string)
	if method == "" {
		return c, errors.New("shadowsocks: method is required")
	}
	c.method = method

	password, _ := s.Settings["password"].(string)
	if password == "" {
		return c, errors.New("shadowsocks: password is required")
	}
	c.password = password

	spec, err := cipherByName(method)
	if err != nil {
		return c, err
	}
	c.spec = spec

	// Master key derivation is fixed per-config, so do it once and cache.
	c.masterKey = evpBytesToKey([]byte(password), spec.keySize)

	return c, nil
}

func (d *dialer) Protocol() string { return "shadowsocks" }

func (d *dialer) Dial(ctx context.Context, network, address string) (protocol.Conn, error) {
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", d.server.Address)
	if err != nil {
		return nil, fmt.Errorf("shadowsocks: dial %s: %w", d.server.Address, err)
	}
	ss, err := d.handshake(raw, address)
	if err != nil {
		raw.Close()
		return nil, err
	}
	return ss, nil
}

func (d *dialer) DialThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (protocol.Conn, error) {
	ss, err := d.handshake(&netConnAdapter{rwc: underlying}, address)
	if err != nil {
		underlying.Close()
		return nil, err
	}
	return ss, nil
}

// handshake sets up the outbound half of the Shadowsocks session:
//  1. Generate a fresh CSPRNG salt (saltSize bytes for this cipher).
//  2. Write the salt to rwc as the very first bytes of the connection.
//  3. Derive the write subkey via HKDF-SHA1(masterKey, salt, "ss-subkey").
//  4. Build the streamWriter and push the [ATYP||ADDR||PORT] address triple
//     as the first chunk — the exit server uses this to route traffic.
//
// The inbound half (reading the server's salt and building the streamReader)
// is deferred to first Read — avoids blocking Dial() on data the server may
// not send until it sees our request.
func (d *dialer) handshake(rwc io.ReadWriteCloser, address string) (*ssConn, error) {
	salt := make([]byte, d.cfg.spec.saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("shadowsocks: generate salt: %w", err)
	}
	if _, err := rwc.Write(salt); err != nil {
		return nil, fmt.Errorf("shadowsocks: write salt: %w", err)
	}

	subkey := hkdfSHA1(d.cfg.masterKey, salt, ssSubkeyInfo, d.cfg.spec.keySize)
	sw := newStreamWriter(rwc, d.cfg.spec, subkey)

	addrBytes, err := socks.EncodeAddr(address)
	if err != nil {
		return nil, fmt.Errorf("shadowsocks: encode target: %w", err)
	}
	if _, err := sw.Write(addrBytes); err != nil {
		return nil, fmt.Errorf("shadowsocks: write request header: %w", err)
	}

	return &ssConn{
		rwc:       rwc,
		cfg:       &d.cfg,
		sw:        sw,
		writeSalt: salt,
	}, nil
}

// ssConn is a net.Conn that transparently applies Shadowsocks AEAD framing.
// The write side is wired up during handshake; the read side is lazily
// initialized on first Read to avoid blocking Dial on server-side I/O.
type ssConn struct {
	rwc io.ReadWriteCloser
	cfg *config

	writeSalt []byte
	sw        *streamWriter

	readOnce sync.Once
	readErr  error
	sr       *streamReader
}

func (c *ssConn) Protocol() string { return "shadowsocks" }

func (c *ssConn) Read(p []byte) (int, error) {
	c.readOnce.Do(func() {
		salt := make([]byte, c.cfg.spec.saltSize)
		if _, err := io.ReadFull(c.rwc, salt); err != nil {
			c.readErr = fmt.Errorf("shadowsocks: read remote salt: %w", err)
			return
		}
		subkey := hkdfSHA1(c.cfg.masterKey, salt, ssSubkeyInfo, c.cfg.spec.keySize)
		c.sr = newStreamReader(c.rwc, c.cfg.spec, subkey)
	})
	if c.readErr != nil {
		return 0, c.readErr
	}
	return c.sr.Read(p)
}

func (c *ssConn) Write(p []byte) (int, error) {
	return c.sw.Write(p)
}

func (c *ssConn) Close() error { return c.rwc.Close() }

// LocalAddr/RemoteAddr/SetDeadline* delegate to rwc when it happens to be
// a net.Conn. When rwc is a raw TCP conn from Dial, this works natively.
// When rwc is a netConnAdapter wrapping a chained io.ReadWriteCloser, the
// adapter itself already handles the delegation.
func (c *ssConn) LocalAddr() net.Addr {
	if nc, ok := c.rwc.(net.Conn); ok {
		return nc.LocalAddr()
	}
	return dummyAddr{}
}

func (c *ssConn) RemoteAddr() net.Addr {
	if nc, ok := c.rwc.(net.Conn); ok {
		return nc.RemoteAddr()
	}
	return dummyAddr{}
}

func (c *ssConn) SetDeadline(t time.Time) error {
	if nc, ok := c.rwc.(net.Conn); ok {
		return nc.SetDeadline(t)
	}
	return nil
}

func (c *ssConn) SetReadDeadline(t time.Time) error {
	if nc, ok := c.rwc.(net.Conn); ok {
		return nc.SetReadDeadline(t)
	}
	return nil
}

func (c *ssConn) SetWriteDeadline(t time.Time) error {
	if nc, ok := c.rwc.(net.Conn); ok {
		return nc.SetWriteDeadline(t)
	}
	return nil
}

// Compile-time guards.
var (
	_ protocol.Dialer = (*dialer)(nil)
	_ protocol.Conn   = (*ssConn)(nil)
	_ net.Conn        = (*ssConn)(nil)
)
