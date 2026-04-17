package protocol

import (
	"context"
	"io"
	"net"
)

// Conn represents an established protocol connection.
type Conn interface {
	net.Conn
	Protocol() string
}

// PacketConn represents an established UDP-carrying protocol connection.
// The ReadFrom/WriteTo addresses are the remote UDP peer (target) for each
// datagram — framed inside the protocol's transport, not a raw UDP socket.
type PacketConn interface {
	net.PacketConn
	Protocol() string
}

// Dialer creates protocol connections.
type Dialer interface {
	// Dial establishes a connection through this protocol.
	Dial(ctx context.Context, network, address string) (Conn, error)

	// DialThrough establishes a connection using an existing connection
	// as the transport layer. This enables chain proxy support.
	DialThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (Conn, error)

	// Protocol returns the protocol name.
	Protocol() string
}

// PacketDialer is optionally implemented by dialers that carry UDP. Callers
// detect support with a type assertion (errors.As / ok-form) — this keeps
// TCP-only protocols free of boilerplate "not supported" stubs and makes
// missing support loud and local at the use site.
//
//	if pd, ok := dialer.(PacketDialer); ok {
//	    return pd.DialPacket(ctx, target)
//	}
//	return nil, fmt.Errorf("%s: UDP not supported", dialer.Protocol())
type PacketDialer interface {
	// DialPacket opens a new UDP-carrying session. address is advisory —
	// some protocols (e.g. trojan) place an address in the opening frame
	// for compatibility but don't use it for routing; per-datagram
	// destinations come from WriteTo.
	DialPacket(ctx context.Context, address string) (PacketConn, error)

	// DialPacketThrough opens a UDP-carrying session using an existing
	// connection as transport. Used when the final hop of a chain offers
	// UDP over a stream already tunneled by the previous hops.
	DialPacketThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (PacketConn, error)
}

// Server represents a remote server endpoint.
type Server struct {
	Name     string
	Address  string
	Protocol string
	Settings map[string]any
}

// DialerFactory creates a Dialer from server configuration.
type DialerFactory func(server Server) (Dialer, error)
