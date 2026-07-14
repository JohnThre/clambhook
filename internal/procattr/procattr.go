// Package procattr resolves the local process that owns a client connection,
// mapping a listener-visible source address (e.g. 127.0.0.1:54321) to the
// owning PID and executable path.
//
// Attribution only works for locally originated connections whose source is a
// real socket on this host — the primary local-proxy path (SOCKS5/HTTP
// listeners). TUN-forwarded flows carry the original application's LAN address
// rather than a local socket, so they are not attributable and Lookup returns
// ok=false. Unsupported platforms return ok=false as well.
package procattr

import (
	"net"
	"path/filepath"
	"strconv"
	"strings"
)

// Process identifies the local process that owns a connection's source socket.
type Process struct {
	PID  int    `json:"pid,omitempty"`
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
}

// Empty reports whether no process was attributed.
func (p Process) Empty() bool {
	return p.PID == 0 && p.Name == "" && p.Path == ""
}

// Lookup resolves the process that owns the socket bound to source for the
// given network ("tcp"/"udp"). ok is false when the connection cannot be
// attributed (unsupported platform, non-local source, or no matching socket).
func Lookup(network, source string) (Process, bool) {
	return lookup(network, source)
}

// localPort extracts the numeric port from a host:port source address.
func localPort(source string) (int, bool) {
	source = strings.TrimSpace(source)
	if source == "" {
		return 0, false
	}
	_, portStr, err := net.SplitHostPort(source)
	if err != nil {
		return 0, false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return 0, false
	}
	return port, true
}

// isUDP reports whether network denotes a UDP flow.
func isUDP(network string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(network)), "udp")
}

// baseName returns the executable's file name for display.
func baseName(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Base(path)
}
