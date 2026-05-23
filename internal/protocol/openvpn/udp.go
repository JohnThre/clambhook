package openvpn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/JohnThre/clambhook/internal/protocol"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
)

// DialPacket opens a UDP socket inside the OpenVPN tunnel. Like WireGuard,
// OpenVPN is layer-3: the packet session binds an ephemeral source port on
// the tunnel address, and each WriteTo destination is routed by the userspace
// netstack.
func (d *dialer) DialPacket(ctx context.Context, address string) (protocol.PacketConn, error) {
	_ = address
	inst, err := d.instance(ctx)
	if err != nil {
		return nil, err
	}
	if len(inst.addresses) == 0 {
		return nil, errors.New("openvpn: no interior address configured")
	}
	src := netip.AddrPortFrom(inst.addresses[0], 0)
	udp, err := inst.tnet.ListenUDPAddrPort(src)
	if err != nil {
		return nil, fmt.Errorf("openvpn: listen udp: %w", err)
	}
	return &ovpnPacketConn{udp: udp, inst: inst}, nil
}

// DialPacketThrough is declined for the same reason as DialThrough:
// OpenVPN expects a UDP-bound transport and cannot be nested inside a stream.
func (d *dialer) DialPacketThrough(_ context.Context, underlying io.ReadWriteCloser, _ string) (protocol.PacketConn, error) {
	_ = underlying
	return nil, errors.New("openvpn: cannot tunnel OpenVPN inside another protocol; place it as a single-hop chain")
}

type ovpnPacketConn struct {
	udp  *gonet.UDPConn
	inst *instance
}

func (p *ovpnPacketConn) Protocol() string { return "openvpn" }

func (p *ovpnPacketConn) WriteTo(payload []byte, addr net.Addr) (int, error) {
	host, portStr, err := net.SplitHostPort(addr.String())
	if err != nil {
		return 0, fmt.Errorf("openvpn: bad target addr %q: %w", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("openvpn: bad target port %q", portStr)
	}
	ip, err := resolveTunnel(context.Background(), p.inst, host)
	if err != nil {
		return 0, err
	}
	return p.udp.WriteTo(payload, &net.UDPAddr{IP: ip.AsSlice(), Port: port})
}

func (p *ovpnPacketConn) ReadFrom(buf []byte) (int, net.Addr, error) {
	return p.udp.ReadFrom(buf)
}

func (p *ovpnPacketConn) Close() error                       { return p.udp.Close() }
func (p *ovpnPacketConn) LocalAddr() net.Addr                { return p.udp.LocalAddr() }
func (p *ovpnPacketConn) SetDeadline(t time.Time) error      { return p.udp.SetDeadline(t) }
func (p *ovpnPacketConn) SetReadDeadline(t time.Time) error  { return p.udp.SetReadDeadline(t) }
func (p *ovpnPacketConn) SetWriteDeadline(t time.Time) error { return p.udp.SetWriteDeadline(t) }

var (
	_ protocol.PacketDialer = (*dialer)(nil)
	_ protocol.PacketConn   = (*ovpnPacketConn)(nil)
	_ net.PacketConn        = (*ovpnPacketConn)(nil)
)
