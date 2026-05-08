package tunnelcore

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/clambhook/clambhook/internal/protocol"
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
)

var (
	registerMobileTestProtocolOnce sync.Once
	mobileTestTCPAccepts           chan net.Conn
	mobileTestPacketConns          chan *mobileTestPacketConn
)

func registerMobileTestProtocol() {
	registerMobileTestProtocolOnce.Do(func() {
		protocol.Register("mobile_test_tcp", func(server protocol.Server) (protocol.Dialer, error) {
			return mobileTestDialer{}, nil
		})
	})
}

type mobileTestDialer struct{}

func (mobileTestDialer) Protocol() string { return "mobile_test_tcp" }

func (mobileTestDialer) Dial(ctx context.Context, network, address string) (protocol.Conn, error) {
	client, remote := net.Pipe()
	select {
	case mobileTestTCPAccepts <- remote:
	case <-ctx.Done():
		_ = client.Close()
		_ = remote.Close()
		return nil, ctx.Err()
	}
	return testProtocolConn{Conn: client}, nil
}

func (mobileTestDialer) DialThrough(context.Context, io.ReadWriteCloser, string) (protocol.Conn, error) {
	panic("not used")
}

func (mobileTestDialer) DialPacket(ctx context.Context, address string) (protocol.PacketConn, error) {
	pc := newMobileTestPacketConn()
	select {
	case mobileTestPacketConns <- pc:
	case <-ctx.Done():
		_ = pc.Close()
		return nil, ctx.Err()
	}
	return pc, nil
}

func (mobileTestDialer) DialPacketThrough(context.Context, io.ReadWriteCloser, string) (protocol.PacketConn, error) {
	panic("not used")
}

type testProtocolConn struct {
	net.Conn
}

func (testProtocolConn) Protocol() string { return "mobile_test_tcp" }

func TestManagerPacketTunnelRoutesTCPThroughActiveChain(t *testing.T) {
	registerMobileTestProtocol()
	mobileTestTCPAccepts = make(chan net.Conn, 1)

	mgr, err := NewManager(`
active = "default"

[[profile]]
name = "default"

  [[profile.chain]]
  name = "direct"

    [[profile.chain.server]]
    name = "proxy"
    address = "127.0.0.1:10000"
    protocol = "mobile_test_tcp"
`)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mgr.Stop()

	clientStack, clientNIC := newPacketClientStack(t, net.IPv4(10, 255, 0, 2).To4())
	defer func() {
		clientStack.Close()
		clientStack.Wait()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pumpClientPacketsToManager(ctx, t, clientNIC, mgr)
	pumpManagerPacketsToClient(ctx, t, mgr, clientNIC)

	tcpConn, err := gonet.DialContextTCP(
		ctx,
		clientStack,
		tcpip.FullAddress{
			NIC:  1,
			Addr: tcpip.AddrFrom4([4]byte{93, 184, 216, 34}),
			Port: 443,
		},
		ipv4.ProtocolNumber,
	)
	if err != nil {
		t.Fatalf("DialContextTCP through packet tunnel: %v", err)
	}
	defer tcpConn.Close()

	remote := receiveRemoteConn(t)
	defer remote.Close()

	if _, err := tcpConn.Write([]byte("ping")); err != nil {
		t.Fatalf("client write: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(remote, buf); err != nil {
		t.Fatalf("remote read: %v", err)
	}
	if got := string(buf); got != "ping" {
		t.Fatalf("remote got %q, want ping", got)
	}

	if _, err := remote.Write([]byte("pong")); err != nil {
		t.Fatalf("remote write: %v", err)
	}
	if _, err := io.ReadFull(tcpConn, buf); err != nil {
		t.Fatalf("client read: %v", err)
	}
	if got := string(buf); got != "pong" {
		t.Fatalf("client got %q, want pong", got)
	}
}

func TestManagerPacketTunnelRoutesUDPThroughActiveChain(t *testing.T) {
	registerMobileTestProtocol()
	mobileTestPacketConns = make(chan *mobileTestPacketConn, 1)

	mgr, err := NewManager(`
active = "default"

[[profile]]
name = "default"

  [[profile.chain]]
  name = "direct"

    [[profile.chain.server]]
    name = "proxy"
    address = "127.0.0.1:10000"
    protocol = "mobile_test_tcp"
`)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mgr.Stop()

	clientStack, clientNIC := newPacketClientStack(t, net.IPv4(10, 255, 0, 2).To4())
	defer func() {
		clientStack.Close()
		clientStack.Wait()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pumpClientPacketsToManager(ctx, t, clientNIC, mgr)
	pumpManagerPacketsToClient(ctx, t, mgr, clientNIC)

	udpConn, err := gonet.DialUDP(
		clientStack,
		nil,
		&tcpip.FullAddress{
			NIC:  1,
			Addr: tcpip.AddrFrom4([4]byte{8, 8, 8, 8}),
			Port: 53,
		},
		ipv4.ProtocolNumber,
	)
	if err != nil {
		t.Fatalf("DialUDP through packet tunnel: %v", err)
	}
	defer udpConn.Close()

	if _, err := udpConn.Write([]byte("query")); err != nil {
		t.Fatalf("client UDP write: %v", err)
	}

	remote := receiveRemotePacketConn(t)
	written := remote.receiveWrite(t)
	if got := string(written.payload); got != "query" {
		t.Fatalf("remote UDP got %q, want query", got)
	}
	if got := written.addr.String(); got != "8.8.8.8:53" {
		t.Fatalf("remote UDP addr = %q, want 8.8.8.8:53", got)
	}

	remote.sendFromChain(t, []byte("answer"), &net.UDPAddr{
		IP:   net.IPv4(8, 8, 8, 8),
		Port: 53,
	})
	buf := make([]byte, 32)
	n, addr, err := udpConn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("client UDP read: %v", err)
	}
	if got := string(buf[:n]); got != "answer" {
		t.Fatalf("client UDP got %q, want answer", got)
	}
	if got := addr.String(); got != "8.8.8.8:53" {
		t.Fatalf("client UDP addr = %q, want 8.8.8.8:53", got)
	}
}

func receiveRemoteConn(t *testing.T) net.Conn {
	t.Helper()
	select {
	case conn := <-mobileTestTCPAccepts:
		return conn
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for chain Dial")
		return nil
	}
}

func receiveRemotePacketConn(t *testing.T) *mobileTestPacketConn {
	t.Helper()
	select {
	case conn := <-mobileTestPacketConns:
		return conn
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for chain DialPacket")
		return nil
	}
}

type mobileTestPacketConn struct {
	toChain   chan mobileTestDatagram
	fromChain chan mobileTestDatagram
	closed    chan struct{}
}

type mobileTestDatagram struct {
	payload []byte
	addr    net.Addr
}

func newMobileTestPacketConn() *mobileTestPacketConn {
	return &mobileTestPacketConn{
		toChain:   make(chan mobileTestDatagram, 16),
		fromChain: make(chan mobileTestDatagram, 16),
		closed:    make(chan struct{}),
	}
}

func (pc *mobileTestPacketConn) Protocol() string { return "mobile_test_tcp" }

func (pc *mobileTestPacketConn) ReadFrom(buf []byte) (int, net.Addr, error) {
	select {
	case dg := <-pc.fromChain:
		return copy(buf, dg.payload), dg.addr, nil
	case <-pc.closed:
		return 0, nil, net.ErrClosed
	}
}

func (pc *mobileTestPacketConn) WriteTo(buf []byte, addr net.Addr) (int, error) {
	payload := append([]byte(nil), buf...)
	select {
	case pc.toChain <- mobileTestDatagram{payload: payload, addr: addr}:
		return len(buf), nil
	case <-pc.closed:
		return 0, net.ErrClosed
	case <-time.After(2 * time.Second):
		return 0, errors.New("timed out writing test datagram")
	}
}

func (pc *mobileTestPacketConn) Close() error {
	select {
	case <-pc.closed:
	default:
		close(pc.closed)
	}
	return nil
}

func (pc *mobileTestPacketConn) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (pc *mobileTestPacketConn) SetDeadline(time.Time) error      { return nil }
func (pc *mobileTestPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (pc *mobileTestPacketConn) SetWriteDeadline(time.Time) error { return nil }
func (pc *mobileTestPacketConn) receiveWrite(t *testing.T) mobileTestDatagram {
	t.Helper()
	select {
	case dg := <-pc.toChain:
		return dg
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for UDP datagram written to chain")
		return mobileTestDatagram{}
	}
}

func (pc *mobileTestPacketConn) sendFromChain(t *testing.T, payload []byte, addr net.Addr) {
	t.Helper()
	select {
	case pc.fromChain <- mobileTestDatagram{payload: append([]byte(nil), payload...), addr: addr}:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out sending UDP datagram from chain")
	}
}

func newPacketClientStack(t *testing.T, addr net.IP) (*stack.Stack, *channel.Endpoint) {
	t.Helper()

	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	ep := channel.New(1024, 1500, "")
	if err := s.CreateNIC(1, ep); err != nil {
		t.Fatalf("CreateNIC: %s", err)
	}
	protocolAddr := tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddrFromSlice(addr).WithPrefix(),
	}
	if err := s.AddProtocolAddress(1, protocolAddr, stack.AddressProperties{}); err != nil {
		t.Fatalf("AddProtocolAddress: %s", err)
	}
	s.SetRouteTable([]tcpip.Route{
		{Destination: header.IPv4EmptySubnet, NIC: 1},
		{Destination: header.IPv6EmptySubnet, NIC: 1},
	})
	return s, ep
}

func pumpClientPacketsToManager(ctx context.Context, t *testing.T, ep *channel.Endpoint, mgr *Manager) {
	t.Helper()
	go func() {
		for {
			pkt := ep.ReadContext(ctx)
			if pkt == nil {
				return
			}
			data := packetBytes(pkt)
			pkt.DecRef()
			if err := mgr.InjectPacket(data); err != nil {
				return
			}
		}
	}()
}

func pumpManagerPacketsToClient(ctx context.Context, t *testing.T, mgr *Manager, ep *channel.Endpoint) {
	t.Helper()
	go func() {
		for {
			packet, err := mgr.ReadPacket(100)
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					continue
				}
			}
			injectPacket(ep, packet)
		}
	}()
}

func packetBytes(pkt *stack.PacketBuffer) []byte {
	view := pkt.ToView()
	defer view.Release()
	return append([]byte(nil), view.AsSlice()...)
}

func injectPacket(ep *channel.Endpoint, packet []byte) {
	netProto, ok := networkProtocol(packet)
	if !ok {
		return
	}
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
		Payload: buffer.MakeWithData(packet),
	})
	defer pkt.DecRef()
	ep.InjectInbound(netProto, pkt)
}

func networkProtocol(packet []byte) (tcpip.NetworkProtocolNumber, bool) {
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
