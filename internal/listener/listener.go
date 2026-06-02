// Package listener provides local ingress listeners (SOCKS5, HTTP CONNECT,
// and TUN/packet stack) that accept client traffic and route it through a proxy
// chain.
package listener

import "context"

// Listener is a long-running local ingress. The engine owns a slice of these
// and drives their lifecycle from Engine.Start / Engine.Stop.
type Listener interface {
	// Start binds the local socket and begins accepting connections. It must
	// return quickly once the socket is bound; the accept loop runs in a
	// goroutine owned by the listener. The supplied context is the parent
	// cancellation signal — cancelling it causes the listener to drain.
	Start(ctx context.Context) error

	// Stop closes the underlying socket and waits for active handlers to
	// finish, up to a bounded grace period. Idempotent: calling Stop on an
	// already-stopped listener returns nil.
	Stop() error

	// Addr returns the bound address (useful after Start when the
	// configured address was ":0" or similar).
	Addr() string

	// Protocol returns a short identifier ("socks5", "http") for Status
	// reporting.
	Protocol() string

	// ActiveConns returns the count of handlers currently in flight.
	// Cheap (atomic load) — safe to call from a status-polling loop.
	ActiveConns() int64
}
