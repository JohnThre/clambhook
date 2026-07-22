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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JohnThre/clambhook/internal/chain"
	"github.com/JohnThre/clambhook/internal/events"
	"github.com/JohnThre/clambhook/internal/policy"
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
	tunNICID                    tcpip.NICID = 1
	tunQueueSize                            = 1024
	tunMaxTCPInFlight                       = 1024
	tunDialTimeout                          = 30 * time.Second
	tunUDPIdleTimeout                       = 2 * time.Minute
	tunUDPPollInterval                      = 30 * time.Second
	// tunMaxWriteConsecutiveErrors is the number of transient WritePacket
	// errors the stack-to-writer loop tolerates before giving up. It bounds
	// the cost of a permanently broken writer without exiting on a single
	// hiccup.
	tunMaxWriteConsecutiveErrors = 10
	// tunWriteTransientBackoff is the base delay between consecutive failed
	// writes. It doubles up to tunWriteTransientMaxBackoff.
	tunWriteTransientBackoff     = 5 * time.Millisecond
	tunWriteTransientMaxBackoff  = 250 * time.Millisecond
)

// PacketWriter receives raw IP packets emitted by the userspace packet stack.
// Platform adapters implement it with a kernel TUN device or an embedded
// platform callback.
type PacketWriter interface {
	WritePacket([]byte) error
}

// PacketStack translates raw IP packets into clambhook's stream/datagram route
// planner. It is platform-neutral; callers own device setup and routing.
type PacketStack struct {
	opts    TUNOptions
	ch      *chain.Chain
	planner RoutePlanner
	writer  PacketWriter

	active atomic.Int64

	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	linkEP *channel.Endpoint
	stack  *stack.Stack
	wg     sync.WaitGroup
}

func NewPacketStack(opts TUNOptions, ch *chain.Chain, planner RoutePlanner, writer PacketWriter) *PacketStack {
	return &PacketStack{opts: opts, ch: ch, planner: planner, writer: writer}
}

func (s *PacketStack) Protocol() string { return "tun" }

func (s *PacketStack) Addr() string { return s.opts.name() }

func (s *PacketStack) ActiveConns() int64 {
	if s == nil {
		return 0
	}
	return s.active.Load()
}

func (s *PacketStack) PolicySnapshot(profile string) policy.Snapshot {
	if s == nil || s.opts.PolicyManager == nil {
		return policy.Snapshot{Profile: profile}
	}
	return s.opts.PolicyManager.Snapshot(profile)
}

func (s *PacketStack) Start(parent context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stack != nil {
		return errors.New("tun: packet stack already started")
	}
	if s.writer == nil {
		return errors.New("tun: nil packet writer")
	}
	if s.ch == nil && s.planner == nil {
		return errors.New("tun: nil router")
	}
	if s.ch != nil {
		if err := s.ch.CheckPacketSupport(); err != nil {
			return fmt.Errorf("tun: %w", err)
		}
	} else if s.planner != nil {
		s.opts.ChainName = s.planner.DefaultChainName()
	}

	mtu := s.opts.mtu()
	stk, linkEP, err := s.newStack(mtu, tunAddresses(s.opts))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(parent)
	s.ctx = ctx
	s.cancel = cancel
	s.stack = stk
	s.linkEP = linkEP

	if s.opts.PolicyManager != nil {
		s.opts.PolicyManager.Start(ctx)
	}
	s.wg.Add(1)
	go s.stackToWriterLoop(ctx, linkEP)
	return nil
}

func (s *PacketStack) Stop() error {
	s.mu.Lock()
	if s.stack == nil {
		dnsProxy := s.opts.DNSProxy
		policyManager := s.opts.PolicyManager
		s.mu.Unlock()
		var errs []error
		if dnsProxy != nil {
			if err := dnsProxy.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if policyManager != nil {
			if err := policyManager.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}
	cancel := s.cancel
	stk := s.stack
	linkEP := s.linkEP
	s.ctx = nil
	s.cancel = nil
	s.stack = nil
	s.linkEP = nil
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if stk != nil {
		stk.Close()
	}
	if linkEP != nil {
		linkEP.Close()
	}

	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(stopGrace):
		log.Printf("tun: packet stack stop grace period expired; abandoning in-flight handlers")
	}
	var errs []error
	if s.opts.DNSProxy != nil {
		if err := s.opts.DNSProxy.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.opts.PolicyManager != nil {
		if err := s.opts.PolicyManager.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *PacketStack) InjectPacket(pkt []byte) error {
	s.mu.Lock()
	linkEP := s.linkEP
	s.mu.Unlock()
	if linkEP == nil {
		return errors.New("tun: packet stack is not running")
	}
	injectPacket(linkEP, pkt)
	return nil
}

func (s *PacketStack) currentContext() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctx
}

func (s *PacketStack) newStack(mtu int, addresses []string) (*stack.Stack, *channel.Endpoint, error) {
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

	tcpForwarder := gtcp.NewForwarder(stk, 0, tunMaxTCPInFlight, s.handleTCPForward)
	stk.SetTransportProtocolHandler(gtcp.ProtocolNumber, tcpForwarder.HandlePacket)

	udpForwarder := gudp.NewForwarder(stk, s.handleUDPForward)
	stk.SetTransportProtocolHandler(gudp.ProtocolNumber, udpForwarder.HandlePacket)

	return stk, linkEP, nil
}

func injectPacket(linkEP *channel.Endpoint, pkt []byte) {
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

func (s *PacketStack) stackToWriterLoop(ctx context.Context, linkEP *channel.Endpoint) {
	defer s.wg.Done()

	consecutive := 0
	backoff := tunWriteTransientBackoff
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
		out := append([]byte(nil), view.AsSlice()...)
		err := s.writer.WritePacket(out)
		view.Release()
		if err == nil {
			consecutive = 0
			backoff = tunWriteTransientBackoff
			continue
		}

		if ctx.Err() != nil || errors.Is(err, os.ErrClosed) {
			return
		}
		consecutive++
		if consecutive >= tunMaxWriteConsecutiveErrors {
			log.Printf("tun: write: %v (giving up after %d consecutive errors)", err, consecutive)
			return
		}
		log.Printf("tun: write: %v (consecutive errors %d/%d, backing off %v)", err, consecutive, tunMaxWriteConsecutiveErrors, backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		if backoff < tunWriteTransientMaxBackoff {
			backoff *= 2
			if backoff > tunWriteTransientMaxBackoff {
				backoff = tunWriteTransientMaxBackoff
			}
		}
	}
}

func (s *PacketStack) handleTCPForward(req *gtcp.ForwarderRequest) {
	id := req.ID()
	target, err := idTarget(id)
	if err != nil {
		req.Complete(true)
		return
	}

	ctx := s.currentContext()
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
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.handleTCPFlow(ctx, local, id, target)
	}()
}

func (s *PacketStack) handleTCPFlow(ctx context.Context, local *gonet.TCPConn, id stack.TransportEndpointID, target string) {
	defer local.Close()
	s.active.Add(1)
	defer s.active.Add(-1)

	ce := newConnEvents(s.opts.EventBus, events.ListenerInfo{
		Protocol: s.Protocol(),
		Addr:     s.Addr(),
	}, s.opts.ProfileName, idClientAddr(id), s.opts.ChainName)
	ce.emitOpened()

	planCtx := ce.attach(ctx)
	if s.shouldProxyDNS("tcp", target) {
		s.handleDNSTCPFlow(planCtx, local, target, ce)
		return
	}

	source := idClientAddr(id)
	plan, dialCtx, cancel, err := s.planFlow(planCtx, "tcp", target, source)
	if err != nil {
		log.Printf("tun tcp: route plan %s failed: %v", target, err)
		ce.emitClosed(events.ReasonError)
		return
	}
	defer cancel()
	ce.emitRuleDecision(plan)
	ce.emitDialingPlan(plan)
	if plan.Action == RouteActionBlock || plan.Action == RouteActionReject {
		ce.emitClosed(routeCloseReason(plan.Action))
		return
	}

	remote, err := plan.Dial(dialCtx, "tcp", target)
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

func (s *PacketStack) handleUDPForward(req *gudp.ForwarderRequest) {
	id := req.ID()
	target, err := idTarget(id)
	if err != nil {
		return
	}

	ctx := s.currentContext()
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
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.handleUDPFlow(ctx, local, id, target)
	}()
}

func (s *PacketStack) handleUDPFlow(ctx context.Context, local *gonet.UDPConn, id stack.TransportEndpointID, target string) {
	defer local.Close()
	s.active.Add(1)
	defer s.active.Add(-1)

	ce := newConnEvents(s.opts.EventBus, events.ListenerInfo{
		Protocol: s.Protocol(),
		Addr:     s.Addr(),
	}, s.opts.ProfileName, idClientAddr(id), s.opts.ChainName)
	ce.emitOpened()

	planCtx := ce.attach(ctx)
	if s.shouldProxyDNS("udp", target) {
		s.handleDNSUDPFlow(planCtx, local, target, ce)
		return
	}

	source := idClientAddr(id)
	plan, dialCtx, cancel, err := s.planFlow(planCtx, "udp", target, source)
	if err != nil {
		log.Printf("tun udp: route plan %s failed: %v", target, err)
		ce.emitClosed(events.ReasonError)
		return
	}
	defer cancel()
	if plan.Port == "53" {
		plan.Visibility = events.VisibilityInfo{Kind: "dns", Host: plan.Host, Port: plan.Port}
	}
	ce.emitRuleDecision(plan)
	ce.emitDialingPlan(plan)
	if plan.Action == RouteActionBlock || plan.Action == RouteActionReject {
		ce.emitClosed(routeCloseReason(plan.Action))
		return
	}
	if plan.DialPacket == nil {
		log.Printf("tun udp: route %s does not support UDP", target)
		ce.emitClosed(events.ReasonError)
		return
	}

	chainPC, err := plan.DialPacket(dialCtx, target)
	if err != nil {
		log.Printf("tun udp: chain dial %s failed: %v", target, err)
		ce.emitClosed(events.ReasonError)
		return
	}
	defer chainPC.Close()

	ce.emitEstablished()
	var dnsObserved bool
	relayErr := relayUDP(ctx, local, chainPC, target, ce.rxCounter(), ce.txCounter(), func(payload []byte) {
		if dnsObserved || plan.Port != "53" {
			return
		}
		if info, ok := dnsVisibilityFromPacket(payload); ok {
			dnsObserved = true
			ce.emitVisibility(info)
		}
	})
	ce.emitClosed(classifyClose(ctx, relayErr))
}

func (s *PacketStack) shouldProxyDNS(network, target string) bool {
	if s.opts.DNSProxy == nil {
		return false
	}
	_, port := splitTrafficTarget(target)
	return port == "53" && (network == "tcp" || network == "udp")
}

func (s *PacketStack) dnsPlan(network, target string) RoutePlan {
	host, port := splitTrafficTarget(target)
	return RoutePlan{
		Profile:      s.opts.ProfileName,
		Action:       RouteActionDirect,
		Target:       target,
		Host:         host,
		Port:         port,
		Network:      network,
		Visibility:   events.VisibilityInfo{Kind: "dns", Host: host, Port: port},
		RouteControl: staticRouteControl(RouteActionDirect, ""),
	}
}

func (s *PacketStack) handleDNSUDPFlow(ctx context.Context, local *gonet.UDPConn, target string, ce *connEvents) {
	plan := s.dnsPlan("udp", target)
	ce.emitRuleDecision(plan)
	ce.emitDialingPlan(plan)
	ce.emitEstablished()

	var dnsObserved bool
	var lastActivity atomic.Int64
	lastActivity.Store(time.Now().UnixNano())
	idle := func() bool {
		return time.Since(time.Unix(0, lastActivity.Load())) > tunUDPIdleTimeout
	}
	buf := make([]byte, 65535)
	for {
		_ = local.SetReadDeadline(time.Now().Add(tunUDPPollInterval))
		n, _, err := local.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				ce.emitClosed(events.ReasonShutdown)
				return
			}
			if isTimeout(err) {
				if idle() {
					ce.emitClosed(events.ReasonClientEOF)
					return
				}
				continue
			}
			log.Printf("tun dns udp: read %s: %v", target, err)
			ce.emitClosed(events.ReasonError)
			return
		}
		if n == 0 {
			continue
		}
		lastActivity.Store(time.Now().UnixNano())
		if tx := ce.txCounter(); tx != nil {
			tx.Add(uint64(n))
		}
		query := append([]byte(nil), buf[:n]...)
		if !dnsObserved {
			if info, ok := dnsVisibilityFromPacket(query); ok {
				dnsObserved = true
				ce.emitVisibility(info)
			}
		}
		resp, err := s.opts.DNSProxy.Exchange(ctx, query)
		if err != nil {
			log.Printf("tun dns udp: exchange %s: %v", target, err)
		}
		if len(resp) == 0 {
			continue
		}
		if rx := ce.rxCounter(); rx != nil {
			rx.Add(uint64(len(resp)))
		}
		if _, err := local.Write(resp); err != nil {
			log.Printf("tun dns udp: write %s: %v", target, err)
			ce.emitClosed(events.ReasonError)
			return
		}
	}
}

func (s *PacketStack) handleDNSTCPFlow(ctx context.Context, local *gonet.TCPConn, target string, ce *connEvents) {
	plan := s.dnsPlan("tcp", target)
	ce.emitRuleDecision(plan)
	ce.emitDialingPlan(plan)
	ce.emitEstablished()

	var dnsObserved bool
	for {
		_ = local.SetReadDeadline(time.Now().Add(tunUDPIdleTimeout))
		query, wireLen, err := readDNSStreamFrame(local)
		if err != nil {
			if ctx.Err() != nil {
				ce.emitClosed(events.ReasonShutdown)
				return
			}
			if errors.Is(err, io.EOF) || isTimeout(err) || errors.Is(err, net.ErrClosed) {
				ce.emitClosed(events.ReasonClientEOF)
				return
			}
			log.Printf("tun dns tcp: read %s: %v", target, err)
			ce.emitClosed(events.ReasonError)
			return
		}
		if tx := ce.txCounter(); tx != nil {
			tx.Add(uint64(wireLen))
		}
		if !dnsObserved {
			if info, ok := dnsVisibilityFromPacket(query); ok {
				dnsObserved = true
				ce.emitVisibility(info)
			}
		}
		resp, err := s.opts.DNSProxy.Exchange(ctx, query)
		if err != nil {
			log.Printf("tun dns tcp: exchange %s: %v", target, err)
		}
		if len(resp) == 0 {
			continue
		}
		_ = local.SetWriteDeadline(time.Now().Add(tunUDPIdleTimeout))
		if err := writeDNSStreamFrame(local, resp); err != nil {
			log.Printf("tun dns tcp: write %s: %v", target, err)
			ce.emitClosed(events.ReasonError)
			return
		}
		if rx := ce.rxCounter(); rx != nil {
			rx.Add(uint64(len(resp) + 2))
		}
	}
}

// planFlow lets route planning use the full handler lifetime, then creates the
// bounded context used only for the outbound dial. This ordering is shared by
// TCP and UDP TUN flows so a prompt cannot consume the dial budget.
func (s *PacketStack) planFlow(ctx context.Context, network, target, source string) (RoutePlan, context.Context, context.CancelFunc, error) {
	plan, err := s.planWithSource(ctx, network, target, source)
	if err != nil {
		return RoutePlan{}, nil, nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, tunDialTimeout)
	return plan, dialCtx, cancel, nil
}

func (s *PacketStack) planWithSource(ctx context.Context, network, target, source string) (RoutePlan, error) {
	if s.planner != nil {
		return PlanRoute(ctx, s.planner, network, target, source)
	}
	plan := RoutePlan{
		Profile:      s.opts.ProfileName,
		Action:       RouteActionChain,
		ChainName:    s.opts.ChainName,
		Target:       target,
		Network:      network,
		Source:       source,
		RouteControl: staticRouteControl(RouteActionChain, s.opts.ChainName),
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return s.ch.Dial(ctx, network, address)
		},
		DialPacket: func(ctx context.Context, address string) (net.PacketConn, error) {
			return s.ch.DialPacket(ctx, address)
		},
	}
	plan.Host, plan.Port = splitTrafficTarget(target)
	if s.ch != nil {
		plan.Hops = s.ch.HopInfo()
	}
	return plan, nil
}

func relayUDP(ctx context.Context, local *gonet.UDPConn, chainPC net.PacketConn, target string, rxCounter, txCounter *atomic.Uint64, observePayload func([]byte)) error {
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
				if observePayload != nil {
					observePayload(buf[:n])
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

func readDNSStreamFrame(r io.Reader) ([]byte, int, error) {
	var lenBuf [2]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, 0, err
	}
	n := int(lenBuf[0])<<8 | int(lenBuf[1])
	if n == 0 {
		return nil, 2, errors.New("dns tcp: empty message")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, 2 + n, err
	}
	return buf, 2 + n, nil
}

func writeDNSStreamFrame(w io.Writer, msg []byte) error {
	if len(msg) == 0 || len(msg) > 65535 {
		return fmt.Errorf("dns tcp: message length %d out of range", len(msg))
	}
	var lenBuf [2]byte
	lenBuf[0] = byte(len(msg) >> 8)
	lenBuf[1] = byte(len(msg))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := w.Write(msg)
	return err
}

func dnsVisibilityFromPacket(payload []byte) (events.VisibilityInfo, bool) {
	if len(payload) < 12 {
		return events.VisibilityInfo{}, false
	}
	qdCount := int(payload[4])<<8 | int(payload[5])
	if qdCount < 1 {
		return events.VisibilityInfo{}, false
	}
	offset := 12
	labels := make([]string, 0, 4)
	for {
		if offset >= len(payload) {
			return events.VisibilityInfo{}, false
		}
		n := int(payload[offset])
		offset++
		if n == 0 {
			break
		}
		// Compression pointers are not expected in the question name. Avoid
		// following them here so malformed packets cannot create loops.
		if n&0xc0 != 0 || n > 63 || offset+n > len(payload) {
			return events.VisibilityInfo{}, false
		}
		labels = append(labels, string(payload[offset:offset+n]))
		offset += n
	}
	if len(labels) == 0 || offset+4 > len(payload) {
		return events.VisibilityInfo{}, false
	}
	qtype := uint16(payload[offset])<<8 | uint16(payload[offset+1])
	return events.VisibilityInfo{
		Kind:      "dns",
		Host:      strings.ToLower(strings.Join(labels, ".")),
		Port:      "53",
		QueryType: dnsQueryTypeName(qtype),
	}, true
}

func dnsQueryTypeName(qtype uint16) string {
	switch qtype {
	case 1:
		return "A"
	case 2:
		return "NS"
	case 5:
		return "CNAME"
	case 15:
		return "MX"
	case 16:
		return "TXT"
	case 28:
		return "AAAA"
	case 33:
		return "SRV"
	case 65:
		return "HTTPS"
	default:
		return fmt.Sprintf("TYPE%d", qtype)
	}
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
