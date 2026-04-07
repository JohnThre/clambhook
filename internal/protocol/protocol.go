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

// Server represents a remote server endpoint.
type Server struct {
	Name     string
	Address  string
	Protocol string
	Settings map[string]any
}

// DialerFactory creates a Dialer from server configuration.
type DialerFactory func(server Server) (Dialer, error)
