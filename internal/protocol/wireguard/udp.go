package wireguard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"

	"github.com/JohnThre/clambhook/internal/protocol"
)

// DialPacket opens a UDP-carrying session through the WireGuard tunnel.
// The address argument is ignored — like trojan and shadowsocks UDP, the
// per-datagram destination comes from WriteTo's addr argument. We bind
// an ephemeral source port on the first interior address.
func (d *dialer) DialPacket(ctx context.Context, address string) (protocol.PacketConn, error) {
	_ = address
	inst, err := d.instance()
	if err != nil {
		return nil, err
	}
	if len(d.cfg.addresses) == 0 {
		return nil, errors.New("wireguard: no interior address configured")
	}
	src := netip.AddrPortFrom(d.cfg.addresses[0], 0)
	udp, err := inst.tnet.ListenUDPAddrPort(src)
	if err != nil {
		return nil, fmt.Errorf("wireguard: listen udp: %w", err)
	}
	return &wgPacketConn{udp: udp, inst: inst}, nil
}

// DialPacketThrough is declined for the same reason as DialThrough:
// WireGuard expects a UDP-bound transport. See wireguard.go:DialThrough.
func (d *dialer) DialPacketThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (protocol.PacketConn, error) {
	_ = underlying
	return nil, errors.New("wireguard: cannot tunnel WireGuard inside another protocol; place it as a single-hop chain")
}

// wgPacketConn wraps a netstack UDP socket. We override WriteTo because
// gonet.UDPConn.WriteTo type-asserts addr to *net.UDPAddr unconditionally
// (gonet.go:664) — passing the SOCKS5 listener's addrForWrite would
// panic. So we resolve the host (in-tunnel DNS for hostnames) and
// reconstruct a concrete *net.UDPAddr.
type wgPacketConn struct {
	udp  *gonet.UDPConn
	inst *wgInstance
}

func (p *wgPacketConn) Protocol() string { return "wireguard" }

func (p *wgPacketConn) WriteTo(payload []byte, addr net.Addr) (int, error) {
	host, portStr, err := net.SplitHostPort(addr.String())
	if err != nil {
		return 0, fmt.Errorf("wireguard: bad target addr %q: %w", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("wireguard: bad target port %q", portStr)
	}
	ip, err := resolveTunnel(context.Background(), p.inst, host)
	if err != nil {
		return 0, err
	}
	return p.udp.WriteTo(payload, &net.UDPAddr{IP: ip.AsSlice(), Port: port})
}

func (p *wgPacketConn) ReadFrom(buf []byte) (int, net.Addr, error) {
	return p.udp.ReadFrom(buf)
}

// Close on a gonet.UDPConn while another goroutine sits in ReadFrom
// unblocks the reader with a gVisor-specific error class — it does NOT
// satisfy errors.Is(err, net.ErrClosed). The SOCKS5 UDP relay
// (internal/listener/socks5_udp.go) uses an explicit context cancel
// for shutdown so this asymmetry doesn't bite us in practice; flagging
// for the next person who tries to use a portable close pattern here.
func (p *wgPacketConn) Close() error                       { return p.udp.Close() }
func (p *wgPacketConn) LocalAddr() net.Addr                { return p.udp.LocalAddr() }
func (p *wgPacketConn) SetDeadline(t time.Time) error      { return p.udp.SetDeadline(t) }
func (p *wgPacketConn) SetReadDeadline(t time.Time) error  { return p.udp.SetReadDeadline(t) }
func (p *wgPacketConn) SetWriteDeadline(t time.Time) error { return p.udp.SetWriteDeadline(t) }

var (
	_ protocol.PacketConn = (*wgPacketConn)(nil)
	_ net.PacketConn      = (*wgPacketConn)(nil)
)
