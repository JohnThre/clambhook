package chain

import (
	"context"
	"fmt"

	"github.com/clambhook/clambhook/internal/protocol"
)

// Chain represents an ordered sequence of protocol hops.
type Chain struct {
	Name  string
	Nodes []protocol.Server
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
	lastDialer, err := protocol.NewDialer(last)
	if err != nil {
		return nil, fmt.Errorf("chain %q last hop: %w", c.Name, err)
	}
	pd, ok := lastDialer.(protocol.PacketDialer)
	if !ok {
		return nil, fmt.Errorf("chain %q: protocol %q does not support UDP", c.Name, last.Protocol)
	}

	// Single-hop: dial directly as UDP.
	if len(c.Nodes) == 1 {
		return pd.DialPacket(ctx, address)
	}

	// Multi-hop: stream-tunnel through all prior hops, then layer UDP on top.
	first, err := protocol.NewDialer(c.Nodes[0])
	if err != nil {
		return nil, fmt.Errorf("chain %q node 0: %w", c.Name, err)
	}
	conn, err := first.Dial(ctx, "tcp", c.Nodes[1].Address)
	if err != nil {
		return nil, fmt.Errorf("chain %q node 0 dial: %w", c.Name, err)
	}
	for i := 1; i < len(c.Nodes)-1; i++ {
		dialer, err := protocol.NewDialer(c.Nodes[i])
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("chain %q node %d: %w", c.Name, i, err)
		}
		conn, err = dialer.DialThrough(ctx, conn, c.Nodes[i+1].Address)
		if err != nil {
			return nil, fmt.Errorf("chain %q node %d dial: %w", c.Name, i, err)
		}
	}
	return pd.DialPacketThrough(ctx, conn, address)
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

	conn, err := first.Dial(ctx, network, target)
	if err != nil {
		return nil, fmt.Errorf("chain %q node 0 dial: %w", c.Name, err)
	}

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

		conn, err = dialer.DialThrough(ctx, conn, nextTarget)
		if err != nil {
			return nil, fmt.Errorf("chain %q node %d dial: %w", c.Name, i, err)
		}
	}

	return conn, nil
}
