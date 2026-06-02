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
	//
	// Ownership contract on error: if DialThrough returns a non-nil error,
	// the implementation MUST have closed underlying. On success, ownership
	// transfers to the returned Conn (whose Close is responsible for
	// closing the whole chain). Callers — chain.Chain in particular — rely
	// on this so they don't need to track underlying separately across the
	// DialThrough boundary.
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
	// some protocols (e.g. trojan/clambback) place an address in the opening frame
	// for compatibility but don't use it for routing; per-datagram
	// destinations come from WriteTo.
	DialPacket(ctx context.Context, address string) (PacketConn, error)

	// DialPacketThrough opens a UDP-carrying session using an existing
	// connection as transport. Used when the final hop of a chain offers
	// UDP over a stream already tunneled by the previous hops.
	//
	// Same ownership contract as Dialer.DialThrough: on error, the
	// implementation MUST close underlying.
	DialPacketThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (PacketConn, error)
}

const (
	UDPModeNative      = "native"
	UDPModeStream      = "stream"
	UDPModeUnsupported = "unsupported"
)

// Capabilities describes local protocol support without opening network
// sockets. UDPMode is native when UDP must be sent directly to the server,
// stream when the protocol can carry UDP through an upstream TCP tunnel, and
// unsupported when UDP is unavailable.
type Capabilities struct {
	TCP       bool   `json:"tcp"`
	UDP       bool   `json:"udp"`
	UDPMode   string `json:"udp_mode"`
	UDPReason string `json:"udp_reason,omitempty"`
}

// CapabilityReporter lets protocol implementations surface constraints that
// cannot be inferred from interface shape alone.
type CapabilityReporter interface {
	Capabilities() Capabilities
}

// DialerCapabilities returns conservative local capabilities for a dialer.
func DialerCapabilities(d Dialer) Capabilities {
	if d == nil {
		return Capabilities{UDPMode: UDPModeUnsupported, UDPReason: "dialer is not configured"}
	}
	if reporter, ok := d.(CapabilityReporter); ok {
		caps := reporter.Capabilities()
		return normalizeCapabilities(caps)
	}
	caps := Capabilities{TCP: true}
	if _, ok := d.(PacketDialer); ok {
		caps.UDP = true
		caps.UDPMode = UDPModeStream
	} else {
		caps.UDPMode = UDPModeUnsupported
		caps.UDPReason = "protocol does not support UDP"
	}
	return caps
}

func normalizeCapabilities(caps Capabilities) Capabilities {
	if caps.UDPMode == "" {
		if caps.UDP {
			caps.UDPMode = UDPModeStream
		} else {
			caps.UDPMode = UDPModeUnsupported
		}
	}
	if !caps.UDP {
		caps.UDPMode = UDPModeUnsupported
		if caps.UDPReason == "" {
			caps.UDPReason = "protocol does not support UDP"
		}
	}
	return caps
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
