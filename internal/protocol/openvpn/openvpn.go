// Package openvpn implements a native OpenVPN 2.6+ client that plugs
// into clambhook's protocol.Dialer interface.
//
// Scope is deliberately narrow (see plan file): UDP transport only, AEAD
// data-channel ciphers only (AES-256-GCM and ChaCha20-Poly1305), TLS 1.2+
// with tls-ekm, PKI + optional --auth-user-pass. Explicitly NOT supported:
// CBC+HMAC mode (legacy), TCP transport, tls-auth / tls-crypt /
// tls-crypt-v2 control-channel wrapping, compression, renegotiation.
//
// Layering (bottom → top):
//
//	net.UDPConn                              (device.go: udp field)
//	  └─ udpReadLoop demuxes by opcode       (device.go)
//	       ├─ reliable (packet IDs, ACKs)    (reliable.go)
//	       │    └─ control (fragments TLS)   (control.go)
//	       │         └─ crypto/tls.Client    (handshake.go)
//	       │              └─ key_method=2 + PUSH_REPLY (handshake.go)
//	       └─ dataChannel (AEAD seal/open)   (data.go)
//	            ├─ tunToUDP goroutine        (netstack.go)
//	            └─ writeToTUN                (device.go)
//	                 └─ netstack.Net (gVisor) (device.go)
//	                      └─ dialer.Dial → TCP over the tunnel
//
// Like WireGuard, OpenVPN is a layer-3 VPN and therefore must be the
// entry (or only) hop in a chain. DialThrough is declined with a
// descriptive error.
package openvpn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"

	"github.com/JohnThre/clambhook/internal/protocol"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
)

func init() {
	protocol.Register("openvpn", func(server protocol.Server) (protocol.Dialer, error) {
		cfg, err := parseConfig(server)
		if err != nil {
			return nil, err
		}
		return &dialer{server: server, cfg: cfg}, nil
	})
}

var newOpenVPNInstance = newInstance

type dialer struct {
	server protocol.Server
	cfg    *config

	mu      sync.Mutex
	once    sync.Once
	inst    *instance
	onceErr error
	closed  bool
}

// instance lazily brings up the VPN. It's guarded by sync.Once so the
// first Dial pays the handshake cost and every subsequent Dial reuses
// the same tunnel. If bring-up fails, the error is latched — the user
// needs to fix their config and restart the daemon; silently retrying
// would mask problems.
func (d *dialer) instance(ctx context.Context) (*instance, error) {
	d.once.Do(func() {
		d.mu.Lock()
		closed := d.closed
		d.mu.Unlock()
		if closed {
			return
		}

		inst, err := newOpenVPNInstance(ctx, d.cfg)
		d.mu.Lock()
		if d.closed {
			d.mu.Unlock()
			if inst != nil {
				_ = inst.Close()
			}
			return
		}
		if err != nil {
			d.onceErr = err
			d.mu.Unlock()
			return
		}
		d.inst = inst
		d.mu.Unlock()
	})
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, errors.New("openvpn: dialer closed")
	}
	return d.inst, d.onceErr
}

func (d *dialer) Protocol() string { return "openvpn" }

// Close tears down the reusable OpenVPN session owned by this dialer.
// It is safe to call before the first Dial; future dials fail clearly.
func (d *dialer) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	inst := d.inst
	d.inst = nil
	d.mu.Unlock()

	if inst != nil {
		return inst.Close()
	}
	return nil
}

// Dial opens a TCP connection to address across the VPN tunnel. The
// first call drives the OpenVPN handshake; subsequent calls reuse the
// same tunnel.
//
// Hostnames resolve through the in-tunnel DNS (set by PUSH_REPLY). If no
// DNS was pushed, the netstack will refuse hostname lookups and the
// caller must dial by IP.
func (d *dialer) Dial(ctx context.Context, network, address string) (protocol.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("openvpn: unsupported network %q", network)
	}
	inst, err := d.instance(ctx)
	if err != nil {
		return nil, err
	}

	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("openvpn: bad address %q: %w", address, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("openvpn: bad port %q", portStr)
	}

	ip, err := resolveTunnel(ctx, inst, host)
	if err != nil {
		return nil, err
	}

	tcp, err := inst.tnet.DialContextTCPAddrPort(ctx, netip.AddrPortFrom(ip, uint16(port)))
	if err != nil {
		return nil, fmt.Errorf("openvpn: dial %s: %w", address, err)
	}
	return &ovpnConn{TCPConn: tcp}, nil
}

// DialThrough is declined: OpenVPN, like WireGuard, wraps IP traffic in
// UDP datagrams and cannot run inside another protocol's stream.
// Surface a clear error rather than letting the user hit a deeper
// mystery failure.
func (d *dialer) DialThrough(_ context.Context, underlying io.ReadWriteCloser, _ string) (protocol.Conn, error) {
	_ = underlying
	return nil, errors.New("openvpn: cannot tunnel OpenVPN inside another stream protocol (place it as a single-hop chain or as the chain's entry hop)")
}

// resolveTunnel maps host (IP literal or hostname) to a netip.Addr
// reachable via the tunnel. Hostnames are resolved by the in-tunnel DNS
// so we don't leak the lookup to the local resolver — same reason
// WireGuard uses inst.tnet.LookupContextHost.
func resolveTunnel(ctx context.Context, inst *instance, host string) (netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip, nil
	}
	addrs, err := inst.tnet.LookupContextHost(ctx, host)
	if err != nil || len(addrs) == 0 {
		return netip.Addr{}, fmt.Errorf("openvpn: resolve %q: %w", host, err)
	}
	ip, err := netip.ParseAddr(addrs[0])
	if err != nil {
		return netip.Addr{}, fmt.Errorf("openvpn: bad resolved addr %q: %w", addrs[0], err)
	}
	return ip, nil
}

// ovpnConn wraps gVisor's TCP conn. gonet.TCPConn already satisfies
// net.Conn; we just add Protocol().
type ovpnConn struct {
	*gonet.TCPConn
}

func (c *ovpnConn) Protocol() string { return "openvpn" }

var (
	_ protocol.Dialer = (*dialer)(nil)
	_ protocol.Conn   = (*ovpnConn)(nil)
	_ net.Conn        = (*ovpnConn)(nil)
)
