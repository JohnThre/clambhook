package wireguard

import (
	"fmt"
	"strings"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// wgInstance holds a live WireGuard device and its userspace netstack.
// Owned by a dialer for the daemon's lifetime; one instance per WG
// server entry in the TOML config.
type wgInstance struct {
	dev       *device.Device
	tnet      *netstack.Net
	name      string // log prefix for diagnostics
	closeHook func() error
}

// newInstance brings up a fresh netstack-backed WireGuard device from cfg.
// Returns when device.Up() succeeds; does NOT wait for the first peer
// handshake (that latency lands on the first Dial through the tunnel).
func newInstance(cfg *config, serverName string) (*wgInstance, error) {
	tunDev, tnet, err := netstack.CreateNetTUN(cfg.addresses, cfg.dns, cfg.mtu)
	if err != nil {
		return nil, fmt.Errorf("wireguard: create netstack tun: %w", err)
	}

	// Logger is non-nilable: device.NewDevice will panic if passed nil.
	// Prefix with the server name so logs from multiple WG instances are
	// distinguishable in stderr (wireguard-go's default writer).
	logger := device.NewLogger(cfg.logLevel, fmt.Sprintf("(wireguard:%s) ", serverName))

	dev := device.NewDevice(tunDev, conn.NewDefaultBind(), logger)

	uapi := buildUAPIConfig(cfg)
	if err := dev.IpcSet(uapi); err != nil {
		dev.Close()
		return nil, fmt.Errorf("wireguard: ipcset: %w", err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("wireguard: device up: %w", err)
	}

	return &wgInstance{dev: dev, tnet: tnet, name: serverName}, nil
}

// Close tears down the device. The underlying TUN, bind, and worker
// goroutines are released. wireguard-go's device.Close() returns no
// error — we keep an error return for symmetry with io.Closer.
func (i *wgInstance) Close() error {
	var err error
	if i.closeHook != nil {
		err = i.closeHook()
		i.closeHook = nil
	}
	if i.dev != nil {
		i.dev.Close()
		i.dev = nil
	}
	return err
}

// buildUAPIConfig serializes cfg into the newline-separated key=value
// blob that device.IpcSet expects. Order is significant: each
// `public_key=` line begins a new peer block, and subsequent
// preshared_key/endpoint/allowed_ip/persistent_keepalive_interval lines
// apply to the most recent peer. Get this wrong and IpcSet silently
// merges peers — there's no validation past the parser.
//
// `replace_allowed_ips=true` is set per peer so the AllowedIPs list is
// authoritative; without it IpcSet would *append* on subsequent calls.
// We don't reload yet, but the explicit form is documentation-as-code.
func buildUAPIConfig(cfg *config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "private_key=%s\n", cfg.privateKeyHex)
	for _, p := range cfg.peers {
		fmt.Fprintf(&b, "public_key=%s\n", p.publicKeyHex)
		if p.presharedKeyHex != "" {
			fmt.Fprintf(&b, "preshared_key=%s\n", p.presharedKeyHex)
		}
		fmt.Fprintf(&b, "endpoint=%s\n", p.endpoint)
		if p.keepalive > 0 {
			fmt.Fprintf(&b, "persistent_keepalive_interval=%d\n", p.keepalive)
		}
		fmt.Fprintf(&b, "replace_allowed_ips=true\n")
		for _, ip := range p.allowedIPs {
			fmt.Fprintf(&b, "allowed_ip=%s\n", ip)
		}
	}
	return b.String()
}
