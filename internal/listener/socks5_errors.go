package listener

import (
	"context"
	"errors"
	"net"
	"syscall"
)

// replyCodeForDialErr maps a chain.Dial error into a SOCKS5 reply code
// (RFC 1928 §6).
//
// We map the common cases that clients actually branch on (refused,
// unreachable, timeout) and fall through to general failure for everything
// else. The leak surface (an attacker probing through the SOCKS port can
// distinguish "host exists" from "host doesn't") is acceptable here because
// the listener is meant to bind to loopback; operators who expose it more
// widely should revisit.
func replyCodeForDialErr(err error) byte {
	if err == nil {
		return repSuccess
	}
	if errors.Is(err, ErrRouteBlocked) || errors.Is(err, ErrRouteRejected) {
		return repConnNotAllowed
	}

	// Timeout / cancellation: most commonly surfaces as a TTL expired from
	// the client's perspective — the connection took too long end-to-end.
	if errors.Is(err, context.DeadlineExceeded) {
		return repTTLExpired
	}

	// DNS resolution failure — "no such host".
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return repHostUnreach
	}

	// Syscall-level errors from the TCP layer (direct first-hop dial).
	var sysErr syscall.Errno
	if errors.As(err, &sysErr) {
		switch sysErr {
		case syscall.ECONNREFUSED:
			return repConnRefused
		case syscall.ENETUNREACH:
			return repNetworkUnreach
		case syscall.EHOSTUNREACH:
			return repHostUnreach
		}
	}

	// net.OpError wraps most dial errors; also check for "i/o timeout".
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Timeout() {
		return repTTLExpired
	}

	return repGeneralFailure
}
