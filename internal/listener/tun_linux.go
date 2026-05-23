//go:build linux

package listener

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JohnThre/clambhook/internal/chain"
	"github.com/JohnThre/clambhook/internal/events"
	"golang.zx2c4.com/wireguard/tun"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
	gtcp "gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	gudp "gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

const (
	tunNICID           tcpip.NICID = 1
	tunQueueSize                   = 1024
	tunMaxTCPInFlight              = 1024
	tunDialTimeout                 = 30 * time.Second
	tunUDPIdleTimeout              = 2 * time.Minute
	tunUDPPollInterval             = 30 * time.Second
	defaultTUNMTU                  = 1500
)

// TUN is a Linux device-wide ingress listener backed by a kernel TUN device
// and a userspace gVisor TCP/IP stack.
type TUN struct {
	opts TUNOptions
	ch   *chain.Chain

	active atomic.Int64

	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	dev      tun.Device
	linkEP   *channel.Endpoint
	stack    *stack.Stack
	routeMgr *linuxRouteManager
	wg       sync.WaitGroup
}

// NewTUN constructs a Linux TUN listener. Start owns privileged setup.
func NewTUN(opts TUNOptions, ch *chain.Chain) Listener {
	return &TUN{opts: opts, ch: ch}
}

func TUNSupported() bool { return true }

func (t *TUN) Protocol() string { return "tun" }

func (o TUNOptions) mtu() int {
	if o.MTU > 0 {
		return o.MTU
	}
	return defaultTUNMTU
}

func (t *TUN) Addr() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dev != nil {
		if name, err := t.dev.Name(); err == nil && name != "" {
			return name
		}
	}
	return t.opts.name()
}

func (t *TUN) ActiveConns() int64 { return t.active.Load() }

func (t *TUN) Start(parent context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.dev != nil {
		return errors.New("tun: already started")
	}
	if t.ch == nil {
		return errors.New("tun: nil chain")
	}
	if err := t.ch.CheckPacketSupport(); err != nil {
		return fmt.Errorf("tun: %w", err)
	}

	mtu := t.opts.mtu()
	dev, err := tun.CreateTUN(t.opts.name(), mtu)
	if err != nil {
		return fmt.Errorf("tun create %s: %w", t.opts.name(), err)
	}
	name, err := dev.Name()
	if err != nil {
		_ = dev.Close()
		return fmt.Errorf("tun name: %w", err)
	}

	routeMgr := newLinuxRouteManager(name, mtu, t.opts, t.ch)
	if err := routeMgr.Setup(parent); err != nil {
		_ = routeMgr.Cleanup(context.Background())
		_ = dev.Close()
		return fmt.Errorf("tun route setup: %w", err)
	}

	stk, linkEP, err := t.newStack(mtu, tunAddresses(t.opts))
	if err != nil {
		_ = routeMgr.Cleanup(context.Background())
		_ = dev.Close()
		return err
	}

	ctx, cancel := context.WithCancel(parent)
	t.ctx = ctx
	t.cancel = cancel
	t.dev = dev
	t.linkEP = linkEP
	t.stack = stk
	t.routeMgr = routeMgr

	t.wg.Add(2)
	go t.tunToStackLoop(ctx, dev, linkEP, mtu)
	go t.stackToTunLoop(ctx, dev, linkEP)

	log.Printf("tun listener started on %s (mtu=%d chain=%q)", name, mtu, t.opts.ChainName)
	return nil
}

func (t *TUN) Stop() error {
	t.mu.Lock()
	if t.dev == nil {
		t.mu.Unlock()
		return nil
	}
	cancel := t.cancel
	dev := t.dev
	linkEP := t.linkEP
	stk := t.stack
	routeMgr := t.routeMgr
	t.ctx = nil
	t.cancel = nil
	t.dev = nil
	t.linkEP = nil
	t.stack = nil
	t.routeMgr = nil
	t.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	var errs []error
	if routeMgr != nil {
		if err := routeMgr.Cleanup(context.Background()); err != nil {
			errs = append(errs, err)
		}
	}
	if stk != nil {
		stk.Close()
	}
	if linkEP != nil {
		linkEP.Close()
	}
	if dev != nil {
		if err := dev.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			errs = append(errs, err)
		}
	}

	done := make(chan struct{})
	go func() { t.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(stopGrace):
		log.Printf("tun: stop grace period expired; abandoning in-flight handlers")
	}

	return errors.Join(errs...)
}

func (t *TUN) currentContext() context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ctx
}

func (t *TUN) newStack(mtu int, addresses []string) (*stack.Stack, *channel.Endpoint, error) {
	stk := stack.New(stack.Options{
		NetworkProtocols: []stack.NetworkProtocolFactory{
			ipv4.NewProtocol,
			ipv6.NewProtocol,
		},
		TransportProtocols: []stack.TransportProtocolFactory{
			gtcp.NewProtocol,
			gudp.NewProtocol,
			icmp.NewProtocol4,
			icmp.NewProtocol6,
		},
		HandleLocal: true,
	})

	sackEnabled := tcpip.TCPSACKEnabled(true)
	if err := stk.SetTransportProtocolOption(gtcp.ProtocolNumber, &sackEnabled); err != nil {
		return nil, nil, fmt.Errorf("tun: enable TCP SACK: %s", err)
	}

	linkEP := channel.New(tunQueueSize, uint32(mtu), "")
	if err := stk.CreateNIC(tunNICID, linkEP); err != nil {
		return nil, nil, fmt.Errorf("tun: create gvisor NIC: %s", err)
	}
	if err := stk.SetPromiscuousMode(tunNICID, true); err != nil {
		return nil, nil, fmt.Errorf("tun: enable promiscuous mode: %s", err)
	}
	if err := stk.SetSpoofing(tunNICID, true); err != nil {
		return nil, nil, fmt.Errorf("tun: enable spoofing: %s", err)
	}

	for _, raw := range addresses {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("tun: invalid address %q: %w", raw, err)
		}
		var proto tcpip.NetworkProtocolNumber
		if prefix.Addr().Is4() {
			proto = ipv4.ProtocolNumber
		} else {
			proto = ipv6.ProtocolNumber
		}
		tcpipErr := stk.AddProtocolAddress(tunNICID, tcpip.ProtocolAddress{
			Protocol: proto,
			AddressWithPrefix: tcpip.AddressWithPrefix{
				Address:   tcpip.AddrFromSlice(prefix.Addr().AsSlice()),
				PrefixLen: prefix.Bits(),
			},
		}, stack.AddressProperties{})
		if tcpipErr != nil {
			return nil, nil, fmt.Errorf("tun: add stack address %q: %s", raw, tcpipErr)
		}
	}

	stk.AddRoute(tcpip.Route{Destination: header.IPv4EmptySubnet, NIC: tunNICID})
	stk.AddRoute(tcpip.Route{Destination: header.IPv6EmptySubnet, NIC: tunNICID})

	tcpForwarder := gtcp.NewForwarder(stk, 0, tunMaxTCPInFlight, t.handleTCPForward)
	stk.SetTransportProtocolHandler(gtcp.ProtocolNumber, tcpForwarder.HandlePacket)

	udpForwarder := gudp.NewForwarder(stk, t.handleUDPForward)
	stk.SetTransportProtocolHandler(gudp.ProtocolNumber, udpForwarder.HandlePacket)

	return stk, linkEP, nil
}

func (t *TUN) tunToStackLoop(ctx context.Context, dev tun.Device, linkEP *channel.Endpoint, mtu int) {
	defer t.wg.Done()

	batchSize := dev.BatchSize()
	if batchSize < 1 {
		batchSize = 1
	}
	bufs := make([][]byte, batchSize)
	sizes := make([]int, batchSize)
	for i := range bufs {
		bufs[i] = make([]byte, mtu+128)
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := dev.Read(bufs, sizes, 0)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, os.ErrClosed) {
				return
			}
			log.Printf("tun: read: %v", err)
			return
		}
		for i := 0; i < n; i++ {
			t.injectPacket(linkEP, bufs[i][:sizes[i]])
		}
	}
}

func (t *TUN) injectPacket(linkEP *channel.Endpoint, pkt []byte) {
	if len(pkt) == 0 {
		return
	}
	var proto tcpip.NetworkProtocolNumber
	switch pkt[0] >> 4 {
	case 4:
		proto = header.IPv4ProtocolNumber
	case 6:
		proto = header.IPv6ProtocolNumber
	default:
		return
	}
	linkEP.InjectInbound(proto, stack.NewPacketBuffer(stack.PacketBufferOptions{
		Payload: buffer.MakeWithData(pkt),
	}))
}

func (t *TUN) stackToTunLoop(ctx context.Context, dev tun.Device, linkEP *channel.Endpoint) {
	defer t.wg.Done()

	for {
		pkt := linkEP.ReadContext(ctx)
		if pkt == nil {
			return
		}
		view := pkt.ToView()
		pkt.DecRef()
		if view.Size() == 0 {
			view.Release()
			continue
		}
		_, err := dev.Write([][]byte{view.AsSlice()}, 0)
		view.Release()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, os.ErrClosed) {
				return
			}
			log.Printf("tun: write: %v", err)
			return
		}
	}
}

func (t *TUN) handleTCPForward(req *gtcp.ForwarderRequest) {
	id := req.ID()
	target, err := idTarget(id)
	if err != nil {
		req.Complete(true)
		return
	}

	ctx := t.currentContext()
	if ctx == nil || ctx.Err() != nil {
		req.Complete(true)
		return
	}

	var wq waiter.Queue
	ep, tcpErr := req.CreateEndpoint(&wq)
	if tcpErr != nil {
		req.Complete(true)
		return
	}
	req.Complete(false)

	local := gonet.NewTCPConn(&wq, ep)
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		t.handleTCPFlow(ctx, local, id, target)
	}()
}

func (t *TUN) handleTCPFlow(ctx context.Context, local *gonet.TCPConn, id stack.TransportEndpointID, target string) {
	defer local.Close()
	t.active.Add(1)
	defer t.active.Add(-1)

	ce := newConnEvents(t.opts.EventBus, events.ListenerInfo{
		Protocol: t.Protocol(),
		Addr:     t.Addr(),
	}, idClientAddr(id), t.opts.ChainName)
	ce.emitOpened()

	dialCtx, cancel := context.WithTimeout(ctx, tunDialTimeout)
	defer cancel()
	dialCtx = ce.attach(dialCtx)

	var hops []events.HopInfo
	if t.ch != nil {
		hops = t.ch.HopInfo()
	}
	ce.emitDialingNetwork("tcp", target, hops)

	remote, err := t.ch.Dial(dialCtx, "tcp", target)
	if err != nil {
		log.Printf("tun tcp: chain dial %s failed: %v", target, err)
		ce.emitClosed(events.ReasonError)
		return
	}
	defer remote.Close()

	ce.emitEstablished()

	stopCh := make(chan struct{})
	defer close(stopCh)
	go func() {
		select {
		case <-ctx.Done():
			_ = local.Close()
			_ = remote.Close()
		case <-stopCh:
		}
	}()

	relayErr := relay(local, remote, ce.rxCounter(), ce.txCounter())
	ce.emitClosed(classifyClose(ctx, relayErr))
}

func (t *TUN) handleUDPForward(req *gudp.ForwarderRequest) {
	id := req.ID()
	target, err := idTarget(id)
	if err != nil {
		return
	}

	ctx := t.currentContext()
	if ctx == nil || ctx.Err() != nil {
		return
	}

	var wq waiter.Queue
	ep, tcpErr := req.CreateEndpoint(&wq)
	if tcpErr != nil {
		log.Printf("tun udp: create endpoint for %s: %s", target, tcpErr)
		return
	}

	local := gonet.NewUDPConn(&wq, ep)
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		t.handleUDPFlow(ctx, local, id, target)
	}()
}

func (t *TUN) handleUDPFlow(ctx context.Context, local *gonet.UDPConn, id stack.TransportEndpointID, target string) {
	defer local.Close()
	t.active.Add(1)
	defer t.active.Add(-1)

	ce := newConnEvents(t.opts.EventBus, events.ListenerInfo{
		Protocol: t.Protocol(),
		Addr:     t.Addr(),
	}, idClientAddr(id), t.opts.ChainName)
	ce.emitOpened()

	dialCtx, cancel := context.WithTimeout(ctx, tunDialTimeout)
	defer cancel()
	dialCtx = ce.attach(dialCtx)

	var hops []events.HopInfo
	if t.ch != nil {
		hops = t.ch.HopInfo()
	}
	ce.emitDialingNetwork("udp", target, hops)

	chainPC, err := t.ch.DialPacket(dialCtx, target)
	if err != nil {
		log.Printf("tun udp: chain dial %s failed: %v", target, err)
		ce.emitClosed(events.ReasonError)
		return
	}
	defer chainPC.Close()

	ce.emitEstablished()
	relayErr := relayUDP(ctx, local, chainPC, target, ce.rxCounter(), ce.txCounter())
	ce.emitClosed(classifyClose(ctx, relayErr))
}

func relayUDP(ctx context.Context, local *gonet.UDPConn, chainPC net.PacketConn, target string, rxCounter, txCounter *atomic.Uint64) error {
	flowCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var lastActivity atomic.Int64
	lastActivity.Store(time.Now().UnixNano())
	bump := func() { lastActivity.Store(time.Now().UnixNano()) }
	idle := func() bool {
		return time.Since(time.Unix(0, lastActivity.Load())) > tunUDPIdleTimeout
	}

	errCh := make(chan error, 2)
	go func() {
		buf := make([]byte, 65535)
		for {
			_ = local.SetReadDeadline(time.Now().Add(tunUDPPollInterval))
			n, _, err := local.ReadFrom(buf)
			if err != nil {
				if flowCtx.Err() != nil {
					errCh <- nil
					return
				}
				if isTimeout(err) {
					if idle() {
						errCh <- nil
						return
					}
					continue
				}
				errCh <- normalizeRelayErr(err)
				return
			}
			if n > 0 {
				if txCounter != nil {
					txCounter.Add(uint64(n))
				}
				bump()
				if _, err := chainPC.WriteTo(buf[:n], stringAddr{network: "udp", address: target}); err != nil {
					errCh <- normalizeRelayErr(err)
					return
				}
			}
		}
	}()

	go func() {
		buf := make([]byte, 65535)
		for {
			_ = chainPC.SetReadDeadline(time.Now().Add(tunUDPPollInterval))
			n, _, err := chainPC.ReadFrom(buf)
			if err != nil {
				if flowCtx.Err() != nil {
					errCh <- nil
					return
				}
				if isTimeout(err) {
					if idle() {
						errCh <- nil
						return
					}
					continue
				}
				errCh <- normalizeRelayErr(err)
				return
			}
			if n > 0 {
				if rxCounter != nil {
					rxCounter.Add(uint64(n))
				}
				bump()
				if _, err := local.Write(buf[:n]); err != nil {
					errCh <- normalizeRelayErr(err)
					return
				}
			}
		}
	}()

	err := <-errCh
	cancel()
	_ = local.Close()
	_ = chainPC.Close()
	if err == nil {
		err = <-errCh
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

type stringAddr struct {
	network string
	address string
}

func (a stringAddr) Network() string { return a.network }
func (a stringAddr) String() string  { return a.address }

func idTarget(id stack.TransportEndpointID) (string, error) {
	return endpointAddress(id.LocalAddress, id.LocalPort)
}

func idClientAddr(id stack.TransportEndpointID) string {
	addr, err := endpointAddress(id.RemoteAddress, id.RemotePort)
	if err != nil {
		return ""
	}
	return addr
}

func endpointAddress(addr tcpip.Address, port uint16) (string, error) {
	a := addr
	ip, ok := netip.AddrFromSlice(a.AsSlice())
	if !ok {
		return "", fmt.Errorf("bad address %q", addr)
	}
	return netip.AddrPortFrom(ip, port).String(), nil
}

func tunAddresses(opts TUNOptions) []string {
	if len(opts.Addresses) > 0 {
		return opts.Addresses
	}
	return []string{
		"198.18.0.1/30",
		"fd7a:636c:616d::1/64",
	}
}

var _ Listener = (*TUN)(nil)
