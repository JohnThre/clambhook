package tunnelcore

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/clambhook/clambhook/internal/chain"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

const (
	packetNICID       tcpip.NICID = 1
	packetMTU                     = 1500
	packetQueueLength             = 1024
)

var ErrNoPacket = errors.New("tunnelcore: no packet available")

type packetTunnel struct {
	ctx    context.Context
	cancel context.CancelFunc
	chain  *chain.Chain
	stack  *stack.Stack
	linkEP *channel.Endpoint
}

func newPacketTunnel(ch *chain.Chain) (*packetTunnel, error) {
	ctx, cancel := context.WithCancel(context.Background())
	t := &packetTunnel{
		ctx:    ctx,
		cancel: cancel,
		chain:  ch,
		stack: stack.New(stack.Options{
			NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol},
			TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
		}),
		linkEP: channel.New(packetQueueLength, packetMTU, ""),
	}

	if err := t.stack.CreateNIC(packetNICID, t.linkEP); err != nil {
		cancel()
		return nil, errors.New(err.String())
	}
	if err := t.stack.SetPromiscuousMode(packetNICID, true); err != nil {
		cancel()
		return nil, errors.New(err.String())
	}
	if err := t.stack.SetSpoofing(packetNICID, true); err != nil {
		cancel()
		return nil, errors.New(err.String())
	}
	t.stack.SetRouteTable([]tcpip.Route{
		{Destination: header.IPv4EmptySubnet, NIC: packetNICID},
		{Destination: header.IPv6EmptySubnet, NIC: packetNICID},
	})

	tcpForwarder := tcp.NewForwarder(t.stack, 0, 1024, t.handleTCP)
	t.stack.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpForwarder.HandlePacket)
	udpForwarder := udp.NewForwarder(t.stack, t.handleUDP)
	t.stack.SetTransportProtocolHandler(udp.ProtocolNumber, udpForwarder.HandlePacket)

	return t, nil
}

func (t *packetTunnel) Close() {
	t.cancel()
	t.linkEP.Close()
	t.stack.Close()
	t.stack.Wait()
}

func (t *packetTunnel) InjectPacket(packet []byte) error {
	netProto, ok := packetNetworkProtocol(packet)
	if !ok {
		return errors.New("tunnelcore: unsupported packet family")
	}
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
		Payload: buffer.MakeWithData(packet),
	})
	defer pkt.DecRef()
	t.linkEP.InjectInbound(netProto, pkt)
	return nil
}

func (t *packetTunnel) ReadPacket(timeoutMillis int) ([]byte, error) {
	ctx := t.ctx
	var cancel context.CancelFunc
	if timeoutMillis > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutMillis)*time.Millisecond)
		defer cancel()
	}
	pkt := t.linkEP.ReadContext(ctx)
	if pkt == nil {
		return nil, ErrNoPacket
	}
	defer pkt.DecRef()
	view := pkt.ToView()
	defer view.Release()
	return append([]byte(nil), view.AsSlice()...), nil
}

func (t *packetTunnel) handleTCP(req *tcp.ForwarderRequest) {
	id := req.ID()
	target := packetTargetAddress(id.LocalAddress, id.LocalPort)

	remote, err := t.chain.Dial(t.ctx, "tcp", target)
	if err != nil {
		req.Complete(true)
		return
	}

	var wq waiter.Queue
	ep, tcpErr := req.CreateEndpoint(&wq)
	if tcpErr != nil {
		_ = remote.Close()
		req.Complete(true)
		return
	}
	req.Complete(false)

	local := gonet.NewTCPConn(&wq, ep)
	go proxyTCP(local, remote)
}

func (t *packetTunnel) handleUDP(req *udp.ForwarderRequest) {
	id := req.ID()
	target := packetUDPAddr(id.LocalAddress, id.LocalPort)

	remote, err := t.chain.DialPacket(t.ctx, target.String())
	if err != nil {
		return
	}

	var wq waiter.Queue
	ep, udpErr := req.CreateEndpoint(&wq)
	if udpErr != nil {
		_ = remote.Close()
		return
	}

	local := gonet.NewUDPConn(&wq, ep)
	go proxyUDP(local, remote, target)
}

func proxyTCP(left io.ReadWriteCloser, right io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(left, right)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(right, left)
		done <- struct{}{}
	}()
	<-done
	_ = left.Close()
	_ = right.Close()
}

func proxyUDP(local *gonet.UDPConn, remote net.PacketConn, target net.Addr) {
	done := make(chan struct{}, 2)
	go func() {
		buf := make([]byte, packetMTU)
		for {
			n, _, err := local.ReadFrom(buf)
			if err != nil {
				break
			}
			if _, err := remote.WriteTo(buf[:n], target); err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	go func() {
		buf := make([]byte, packetMTU)
		for {
			n, _, err := remote.ReadFrom(buf)
			if err != nil {
				break
			}
			if _, err := local.Write(buf[:n]); err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	<-done
	_ = local.Close()
	_ = remote.Close()
}

func packetTargetAddress(addr tcpip.Address, port uint16) string {
	return net.JoinHostPort(packetIP(addr).String(), strconv.Itoa(int(port)))
}

func packetUDPAddr(addr tcpip.Address, port uint16) *net.UDPAddr {
	return &net.UDPAddr{
		IP:   packetIP(addr),
		Port: int(port),
	}
}

func packetIP(addr tcpip.Address) net.IP {
	return net.IP(append([]byte(nil), addr.AsSlice()...))
}

func packetNetworkProtocol(packet []byte) (tcpip.NetworkProtocolNumber, bool) {
	if len(packet) == 0 {
		return 0, false
	}
	switch packet[0] >> 4 {
	case 4:
		return ipv4.ProtocolNumber, true
	case 6:
		return ipv6.ProtocolNumber, true
	default:
		return 0, false
	}
}
