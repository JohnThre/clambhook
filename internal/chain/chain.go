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
