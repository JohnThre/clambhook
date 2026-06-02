// Package wireguard implements WireGuard support via the official
// golang.zx2c4.com/wireguard library and its tun/netstack package
// (a userspace gVisor TCP/IP stack).
//
// WireGuard is a Layer 3 VPN — it exchanges encrypted IP packets over
// UDP — but clambhook's Dialer/PacketDialer interfaces expect L4
// streams and datagrams. The userspace netstack bridges this: it owns
// a virtual NIC that the WireGuard device drains, and it exposes
// DialContextTCPAddrPort / ListenUDPAddrPort which look identical to
// stdlib net.Conn / net.PacketConn from the caller's side.
//
// Single-hop only: WireGuard cannot ride a chained TCP stream because
// its bind expects raw UDP datagrams. DialThrough / DialPacketThrough
// return a structured error that names the constraint, mirroring the
// shadowsocks-UDP convention (internal/protocol/shadowsocks/udp.go).
package wireguard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"

	"golang.zx2c4.com/wireguard/device"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"

	"github.com/JohnThre/clambhook/internal/protocol"
)

func init() {
	protocol.Register("wireguard", func(server protocol.Server) (protocol.Dialer, error) {
		cfg, err := parseConfig(server)
		if err != nil {
			return nil, err
		}
		return &dialer{server: server, cfg: cfg}, nil
	})
	protocol.RegisterCapabilities("wireguard", wireguardCapabilities())
}

var newWireGuardInstance = newInstance

type peerConfig struct {
	publicKeyHex    string
	presharedKeyHex string // empty if absent
	endpoint        string // "host:port", passed verbatim to IpcSet
	allowedIPs      []string
	keepalive       int // seconds, 0 = disabled
}

type config struct {
	privateKeyHex string
	addresses     []netip.Addr // local interior addrs (TUN-side IPs)
	dns           []netip.Addr // optional, used by tnet.LookupContextHost
	mtu           int          // default device.DefaultMTU (1420)
	logLevel      int          // device.LogLevel{Silent,Error,Verbose}
	peers         []peerConfig
}

func (d *dialer) Capabilities() protocol.Capabilities {
	return wireguardCapabilities()
}

func wireguardCapabilities() protocol.Capabilities {
	return protocol.Capabilities{
		TCP:       true,
		UDP:       true,
		UDPMode:   protocol.UDPModeNative,
		UDPReason: "WireGuard must be used as a single-hop chain",
	}
}

// parseConfig extracts and validates WireGuard settings from the shared
// Server struct. Style mirrors trojan/shadowsocks parseConfig (typed
// asserts via ok-form, error-on-missing for required fields).
func parseConfig(s protocol.Server) (config, error) {
	var c config

	pkB64, _ := s.Settings["private_key"].(string)
	if pkB64 == "" {
		return c, errors.New("wireguard: private_key is required")
	}
	pkHex, err := keyToHex(pkB64)
	if err != nil {
		return c, fmt.Errorf("wireguard: private_key: %w", err)
	}
	c.privateKeyHex = pkHex

	rawAddrs, ok := s.Settings["addresses"].([]any)
	if !ok || len(rawAddrs) == 0 {
		return c, errors.New("wireguard: addresses is required (at least one CIDR)")
	}
	for _, v := range rawAddrs {
		s, ok := v.(string)
		if !ok || s == "" {
			return c, errors.New("wireguard: addresses entries must be CIDR strings")
		}
		prefix, err := netip.ParsePrefix(s)
		if err != nil {
			return c, fmt.Errorf("wireguard: invalid address %q: %w", s, err)
		}
		c.addresses = append(c.addresses, prefix.Addr())
	}

	if rawDNS, ok := s.Settings["dns"].([]any); ok {
		for _, v := range rawDNS {
			s, ok := v.(string)
			if !ok || s == "" {
				return c, errors.New("wireguard: dns entries must be IP strings")
			}
			ip, err := netip.ParseAddr(s)
			if err != nil {
				return c, fmt.Errorf("wireguard: invalid dns %q: %w", s, err)
			}
			c.dns = append(c.dns, ip)
		}
	}

	c.mtu = device.DefaultMTU
	if v, ok := s.Settings["mtu"].(int64); ok && v > 0 {
		c.mtu = int(v)
	}

	c.logLevel = device.LogLevelError
	if v, ok := s.Settings["log_level"].(string); ok {
		switch v {
		case "silent":
			c.logLevel = device.LogLevelSilent
		case "error":
			c.logLevel = device.LogLevelError
		case "verbose":
			c.logLevel = device.LogLevelVerbose
		default:
			return c, fmt.Errorf("wireguard: invalid log_level %q (want silent|error|verbose)", v)
		}
	}

	peersRaw, ok := peersList(s.Settings["peers"])
	if !ok || len(peersRaw) == 0 {
		return c, errors.New("wireguard: at least one peer is required")
	}
	for i, pm := range peersRaw {
		pc, err := parsePeer(pm)
		if err != nil {
			return c, fmt.Errorf("wireguard: peer %d: %w", i, err)
		}
		c.peers = append(c.peers, pc)
	}

	// Coherence check: if the TOML supplied a top-level address, it must
	// match the first peer's endpoint. Otherwise the TUI would display one
	// address while traffic actually goes somewhere else.
	if s.Address != "" && s.Address != c.peers[0].endpoint {
		return c, fmt.Errorf("wireguard: server.address %q does not match peers[0].endpoint %q",
			s.Address, c.peers[0].endpoint)
	}

	return c, nil
}

// peersList normalizes the two shapes BurntSushi/toml may produce for
// `[[settings.peers]]`: typically `[]map[string]any`, but if the destination
// type erases that to `any` we may see `[]any` of maps. Accept both.
func peersList(v any) ([]map[string]any, bool) {
	switch t := v.(type) {
	case []map[string]any:
		return t, true
	case []any:
		out := make([]map[string]any, 0, len(t))
		for _, e := range t {
			m, ok := e.(map[string]any)
			if !ok {
				return nil, false
			}
			out = append(out, m)
		}
		return out, true
	default:
		return nil, false
	}
}

func parsePeer(pm map[string]any) (peerConfig, error) {
	var pc peerConfig

	pubB64, _ := pm["public_key"].(string)
	if pubB64 == "" {
		return pc, errors.New("public_key is required")
	}
	pubHex, err := keyToHex(pubB64)
	if err != nil {
		return pc, fmt.Errorf("public_key: %w", err)
	}
	pc.publicKeyHex = pubHex

	if pskB64, ok := pm["preshared_key"].(string); ok && pskB64 != "" {
		pskHex, err := keyToHex(pskB64)
		if err != nil {
			return pc, fmt.Errorf("preshared_key: %w", err)
		}
		pc.presharedKeyHex = pskHex
	}

	pc.endpoint, _ = pm["endpoint"].(string)
	if pc.endpoint == "" {
		return pc, errors.New("endpoint is required")
	}

	rawIPs, ok := pm["allowed_ips"].([]any)
	if !ok || len(rawIPs) == 0 {
		return pc, errors.New("allowed_ips is required (at least one CIDR)")
	}
	for _, v := range rawIPs {
		s, ok := v.(string)
		if !ok || s == "" {
			return pc, errors.New("allowed_ips entries must be CIDR strings")
		}
		if _, err := netip.ParsePrefix(s); err != nil {
			return pc, fmt.Errorf("allowed_ips %q: %w", s, err)
		}
		pc.allowedIPs = append(pc.allowedIPs, s)
	}

	if v, ok := pm["persistent_keepalive"].(int64); ok {
		if v < 0 || v > 65535 {
			return pc, fmt.Errorf("persistent_keepalive %d out of range [0,65535]", v)
		}
		pc.keepalive = int(v)
	}

	return pc, nil
}

// dialer holds the WireGuard configuration and a lazily-initialized device
// instance. The instance is created on first Dial via sync.Once because
// many TOML entries may declare WG servers that are never actually used —
// no point burning a netstack and a handshake on cold configs.
type dialer struct {
	server protocol.Server
	cfg    config

	mu   sync.Mutex
	once sync.Once
	inst *wgInstance
	err  error

	closed bool
}

func (d *dialer) Protocol() string { return "wireguard" }

// instance lazily brings up the WireGuard device. Subsequent calls return
// the cached instance (or the cached startup error).
func (d *dialer) instance() (*wgInstance, error) {
	d.once.Do(func() {
		d.mu.Lock()
		closed := d.closed
		d.mu.Unlock()
		if closed {
			return
		}

		inst, err := newWireGuardInstance(&d.cfg, d.server.Name)

		d.mu.Lock()
		if d.closed {
			d.mu.Unlock()
			if inst != nil {
				_ = inst.Close()
			}
			return
		}
		d.inst = inst
		d.err = err
		d.mu.Unlock()
	})
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, errors.New("wireguard: dialer closed")
	}
	return d.inst, d.err
}

// Close tears down the reusable WireGuard instance owned by this dialer.
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

// Dial opens a TCP connection to address through the WireGuard tunnel.
// Hostnames are resolved via tnet.LookupContextHost (DNS in-tunnel) so
// the lookup can't leak to the local resolver — same rationale as the
// SOCKS5 ATYPDomain preference in internal/socks/addr.go.
func (d *dialer) Dial(ctx context.Context, network, address string) (protocol.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("wireguard: unsupported network %q", network)
	}
	inst, err := d.instance()
	if err != nil {
		return nil, err
	}

	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("wireguard: bad address %q: %w", address, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("wireguard: bad port %q", portStr)
	}

	ip, err := resolveTunnel(ctx, inst, host)
	if err != nil {
		return nil, err
	}

	tcpConn, err := inst.tnet.DialContextTCPAddrPort(ctx, netip.AddrPortFrom(ip, uint16(port)))
	if err != nil {
		return nil, fmt.Errorf("wireguard: dial %s: %w", address, err)
	}
	return &wgConn{TCPConn: tcpConn}, nil
}

// DialThrough is declined: WireGuard's UDP-bound transport can't ride
// another protocol's stream without explicit framing, which is out of
// scope for v1. Surfacing a structured error steers users at valid
// chain configurations rather than failing mysteriously deeper down.
func (d *dialer) DialThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (protocol.Conn, error) {
	_ = underlying
	return nil, errors.New("wireguard: cannot tunnel WireGuard inside another stream protocol (place it as a single-hop chain or as the chain's entry hop)")
}

// resolveTunnel turns host (literal IP or hostname) into a netip.Addr
// reachable via the tunnel. Literal IPs short-circuit the lookup.
func resolveTunnel(ctx context.Context, inst *wgInstance, host string) (netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip, nil
	}
	addrs, err := inst.tnet.LookupContextHost(ctx, host)
	if err != nil || len(addrs) == 0 {
		return netip.Addr{}, fmt.Errorf("wireguard: resolve %q: %w", host, err)
	}
	ip, err := netip.ParseAddr(addrs[0])
	if err != nil {
		return netip.Addr{}, fmt.Errorf("wireguard: bad resolved addr %q: %w", addrs[0], err)
	}
	return ip, nil
}

// wgConn is the protocol.Conn wrapper around gVisor's TCP conn.
// gonet.TCPConn already implements net.Conn (LocalAddr/RemoteAddr/
// deadlines), so we only need to add Protocol().
type wgConn struct {
	*gonet.TCPConn
}

func (c *wgConn) Protocol() string { return "wireguard" }

// Compile-time guards.
var (
	_ protocol.Dialer       = (*dialer)(nil)
	_ protocol.PacketDialer = (*dialer)(nil)
	_ protocol.Conn         = (*wgConn)(nil)
	_ net.Conn              = (*wgConn)(nil)
)
