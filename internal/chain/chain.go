package chain

import (
	"context"
	"fmt"
	"time"

	"github.com/JohnThre/clambhook/internal/events"
	"github.com/JohnThre/clambhook/internal/protocol"
)

// Chain represents an ordered sequence of protocol hops.
type Chain struct {
	Name  string
	Nodes []protocol.Server
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
// protocol.PacketDialer (trojan does; most others don't yet).
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
	first, err := protocol.NewDialer(c.Nodes[0])
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
		dialer, err := protocol.NewDialer(c.Nodes[i])
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
	_, err := c.packetDialerForLastHop()
	return err
}

func (c *Chain) packetDialerForLastHop() (protocol.PacketDialer, error) {
	if len(c.Nodes) == 0 {
		return nil, fmt.Errorf("chain %q: no nodes", c.Name)
	}
	last := c.Nodes[len(c.Nodes)-1]
	lastDialer, err := protocol.NewDialer(last)
	if err != nil {
		return nil, fmt.Errorf("chain %q last hop: %w", c.Name, err)
	}
	pd, ok := lastDialer.(protocol.PacketDialer)
	if !ok {
		return nil, fmt.Errorf("chain %q: protocol %q does not support UDP", c.Name, last.Protocol)
	}
	return pd, nil
}

// Dial connects through the entire chain to reach the final address.
func (c *Chain) Dial(ctx context.Context, network, address string) (protocol.Conn, error) {
	if len(c.Nodes) == 0 {
		return nil, fmt.Errorf("chain %q: no nodes", c.Name)
	}

	// First hop: direct dial to entry server.
	first, err := protocol.NewDialer(c.Nodes[0])
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
		dialer, err := protocol.NewDialer(c.Nodes[i])
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
