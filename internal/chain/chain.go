package chain

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/JohnThre/clambhook/internal/events"
	"github.com/JohnThre/clambhook/internal/protocol"
)

// Chain represents an ordered sequence of protocol hops.
type Chain struct {
	Name  string
	Nodes []protocol.Server

	mu      sync.Mutex
	dialers []protocol.Dialer
	closed  bool
}

type dialerCloser interface {
	Close() error
}

func (c *Chain) dialerAt(idx int) (protocol.Dialer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil, fmt.Errorf("chain %q: closed", c.Name)
	}
	if idx < 0 || idx >= len(c.Nodes) {
		return nil, fmt.Errorf("chain %q: node %d out of range", c.Name, idx)
	}
	if c.dialers == nil {
		c.dialers = make([]protocol.Dialer, len(c.Nodes))
	}
	if c.dialers[idx] != nil {
		return c.dialers[idx], nil
	}

	dialer, err := protocol.NewDialer(c.Nodes[idx])
	if err != nil {
		return nil, err
	}
	c.dialers[idx] = dialer
	return dialer, nil
}

// Close releases reusable protocol dialers owned by this runtime chain.
// It is idempotent; after Close, future Dial/DialPacket calls fail.
func (c *Chain) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	dialers := c.dialers
	c.dialers = nil
	c.mu.Unlock()

	var errs []error
	for i, dialer := range dialers {
		closer, ok := dialer.(dialerCloser)
		if !ok || closer == nil {
			continue
		}
		if err := closer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("node %d %s: %w", i, dialer.Protocol(), err))
		}
	}
	return errors.Join(errs...)
}

// emitHopDialing publishes hop.dialing if an emitter is attached to the
// context. No-op when events are disabled (ctx has no emitter).
func emitHopDialing(ctx context.Context, idx int, node protocol.Server) {
	em, ok := events.EmitterFrom(ctx)
	if !ok {
		return
	}
	em.Emit(events.TypeHopDialing, events.HopDialingData{
		ConnID:   events.ConnIDFrom(ctx),
		HopIndex: idx,
		HopName:  node.Name,
		Protocol: node.Protocol,
		Address:  node.Address,
	})
}

// emitHopConnected publishes hop.connected with the elapsed dial time.
func emitHopConnected(ctx context.Context, idx int, start time.Time) {
	em, ok := events.EmitterFrom(ctx)
	if !ok {
		return
	}
	em.Emit(events.TypeHopConnected, events.HopConnectedData{
		ConnID:    events.ConnIDFrom(ctx),
		HopIndex:  idx,
		ElapsedNs: time.Since(start).Nanoseconds(),
	})
}

// emitHopError publishes hop.error for a failed dial.
func emitHopError(ctx context.Context, idx int, err error) {
	em, ok := events.EmitterFrom(ctx)
	if !ok {
		return
	}
	em.Emit(events.TypeHopError, events.HopErrorData{
		ConnID:   events.ConnIDFrom(ctx),
		HopIndex: idx,
		Error:    err.Error(),
	})
}

// DialPacket connects through the chain to open a UDP-carrying session.
//
// Intermediate hops must be streaming protocols — they just forward the TLS
// bytes of the UDP-carrying final hop. The final hop MUST implement
// protocol.PacketDialer (trojan and clambback do; most others don't yet).
//
// This is the "type-assertion" approach: we don't force every protocol to
// implement a no-op DialPacket. If the final hop doesn't support UDP, we
// return a structured error naming the hop — which becomes a SOCKS5 reply
// code of "command not supported" or "general failure" at the listener.
func (c *Chain) DialPacket(ctx context.Context, address string) (protocol.PacketConn, error) {
	if len(c.Nodes) == 0 {
		return nil, fmt.Errorf("chain %q: no nodes", c.Name)
	}

	last := c.Nodes[len(c.Nodes)-1]
	lastIdx := len(c.Nodes) - 1
	pd, err := c.packetDialerForLastHop()
	if err != nil {
		return nil, err
	}

	// Single-hop: dial directly as UDP.
	if len(c.Nodes) == 1 {
		emitHopDialing(ctx, 0, last)
		start := time.Now()
		pc, err := pd.DialPacket(ctx, address)
		if err != nil {
			emitHopError(ctx, 0, err)
			return nil, err
		}
		emitHopConnected(ctx, 0, start)
		return pc, nil
	}

	// Multi-hop: stream-tunnel through all prior hops, then layer UDP on top.
	first, err := c.dialerAt(0)
	if err != nil {
		return nil, fmt.Errorf("chain %q node 0: %w", c.Name, err)
	}
	emitHopDialing(ctx, 0, c.Nodes[0])
	firstStart := time.Now()
	conn, err := first.Dial(ctx, "tcp", c.Nodes[1].Address)
	if err != nil {
		emitHopError(ctx, 0, err)
		return nil, fmt.Errorf("chain %q node 0 dial: %w", c.Name, err)
	}
	emitHopConnected(ctx, 0, firstStart)

	for i := 1; i < len(c.Nodes)-1; i++ {
		dialer, err := c.dialerAt(i)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("chain %q node %d: %w", c.Name, i, err)
		}
		emitHopDialing(ctx, i, c.Nodes[i])
		hopStart := time.Now()
		conn, err = dialer.DialThrough(ctx, conn, c.Nodes[i+1].Address)
		if err != nil {
			emitHopError(ctx, i, err)
			return nil, fmt.Errorf("chain %q node %d dial: %w", c.Name, i, err)
		}
		emitHopConnected(ctx, i, hopStart)
	}

	emitHopDialing(ctx, lastIdx, last)
	lastStart := time.Now()
	pc, err := pd.DialPacketThrough(ctx, conn, address)
	if err != nil {
		emitHopError(ctx, lastIdx, err)
		return nil, err
	}
	emitHopConnected(ctx, lastIdx, lastStart)
	return pc, nil
}

// CheckPacketSupport validates that the chain can carry UDP without opening
// any network sockets. Device-wide TUN mode requires this so UDP flows don't
// silently disappear after the host route has already been redirected.
func (c *Chain) CheckPacketSupport() error {
	caps := c.Capabilities()
	if caps.UDP {
		return nil
	}
	if caps.UDPReason == "" {
		caps.UDPReason = "UDP is not supported"
	}
	return fmt.Errorf("chain %q: %s", c.Name, caps.UDPReason)
}

func (c *Chain) packetDialerForLastHop() (protocol.PacketDialer, error) {
	if len(c.Nodes) == 0 {
		return nil, fmt.Errorf("chain %q: no nodes", c.Name)
	}
	lastIdx := len(c.Nodes) - 1
	last := c.Nodes[lastIdx]
	lastDialer, err := c.dialerAt(lastIdx)
	if err != nil {
		return nil, fmt.Errorf("chain %q last hop: %w", c.Name, err)
	}
	pd, ok := lastDialer.(protocol.PacketDialer)
	if !ok {
		return nil, fmt.Errorf("chain %q: protocol %q does not support UDP", c.Name, last.Protocol)
	}
	return pd, nil
}

// Capabilities describes the whole chain's local routing support.
func (c *Chain) Capabilities() protocol.Capabilities {
	caps := protocol.Capabilities{
		TCP:     len(c.Nodes) > 0,
		UDPMode: protocol.UDPModeUnsupported,
	}
	if len(c.Nodes) == 0 {
		caps.UDPReason = "chain has no nodes"
		return caps
	}
	lastIdx := len(c.Nodes) - 1
	last := c.Nodes[lastIdx]
	lastDialer, err := c.dialerAt(lastIdx)
	if err != nil {
		caps.UDPReason = err.Error()
		return caps
	}
	lastCaps := protocol.DialerCapabilities(lastDialer)
	if !lastCaps.UDP {
		caps.UDPReason = fmt.Sprintf("protocol %q does not support UDP", last.Protocol)
		if lastCaps.UDPReason != "" {
			caps.UDPReason = lastCaps.UDPReason
		}
		return caps
	}
	if len(c.Nodes) == 1 {
		caps.UDP = true
		caps.UDPMode = lastCaps.UDPMode
		caps.UDPReason = lastCaps.UDPReason
		return caps
	}
	if lastCaps.UDPMode != protocol.UDPModeStream {
		caps.UDPReason = fmt.Sprintf("protocol %q cannot carry UDP through an upstream chain", last.Protocol)
		if lastCaps.UDPReason != "" {
			caps.UDPReason = lastCaps.UDPReason
		}
		return caps
	}
	caps.UDP = true
	caps.UDPMode = protocol.UDPModeStream
	return caps
}

// Dial connects through the entire chain to reach the final address.
func (c *Chain) Dial(ctx context.Context, network, address string) (protocol.Conn, error) {
	if len(c.Nodes) == 0 {
		return nil, fmt.Errorf("chain %q: no nodes", c.Name)
	}

	// First hop: direct dial to entry server.
	first, err := c.dialerAt(0)
	if err != nil {
		return nil, fmt.Errorf("chain %q node 0: %w", c.Name, err)
	}

	target := address
	if len(c.Nodes) > 1 {
		target = c.Nodes[1].Address
	}

	emitHopDialing(ctx, 0, c.Nodes[0])
	firstStart := time.Now()
	conn, err := first.Dial(ctx, network, target)
	if err != nil {
		emitHopError(ctx, 0, err)
		return nil, fmt.Errorf("chain %q node 0 dial: %w", c.Name, err)
	}
	emitHopConnected(ctx, 0, firstStart)

	// Subsequent hops: DialThrough the previous connection.
	for i := 1; i < len(c.Nodes); i++ {
		dialer, err := c.dialerAt(i)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("chain %q node %d: %w", c.Name, i, err)
		}

		nextTarget := address
		if i < len(c.Nodes)-1 {
			nextTarget = c.Nodes[i+1].Address
		}

		emitHopDialing(ctx, i, c.Nodes[i])
		hopStart := time.Now()
		conn, err = dialer.DialThrough(ctx, conn, nextTarget)
		if err != nil {
			emitHopError(ctx, i, err)
			return nil, fmt.Errorf("chain %q node %d dial: %w", c.Name, i, err)
		}
		emitHopConnected(ctx, i, hopStart)
	}

	return conn, nil
}

// HopInfo returns lightweight descriptors for each chain hop. Used by
// listeners to populate the `connection.dialing` event's `hops` field
// before the dial begins (so the subscriber sees the full chain shape
// even if a mid-hop fails).
func (c *Chain) HopInfo() []events.HopInfo {
	out := make([]events.HopInfo, len(c.Nodes))
	for i, n := range c.Nodes {
		out[i] = events.HopInfo{
			Index:    i,
			Name:     n.Name,
			Protocol: n.Protocol,
			Address:  n.Address,
		}
	}
	return out
}
