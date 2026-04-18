package shadowsocks

import (
	"io"
	"net"
	"time"
)

// netConnAdapter promotes an io.ReadWriteCloser to net.Conn. When the
// Shadowsocks dialer is asked to tunnel through a chained proxy, the
// underlying transport is an io.ReadWriteCloser from the previous hop —
// but ssConn wants net.Conn semantics (deadlines, addresses). If the
// underlying rwc is already a net.Conn, delegate; otherwise return
// zero values so the conn stays usable.
//
// This is intentionally a local copy of the same pattern in
// internal/protocol/trojan/trojan.go (lines 285-326). A third protocol
// sharing the pattern is the right moment to extract it to a shared
// package; until then, duplication is cheaper than coupling.
type netConnAdapter struct {
	rwc io.ReadWriteCloser
}

func (a *netConnAdapter) Read(p []byte) (int, error)  { return a.rwc.Read(p) }
func (a *netConnAdapter) Write(p []byte) (int, error) { return a.rwc.Write(p) }
func (a *netConnAdapter) Close() error                { return a.rwc.Close() }

func (a *netConnAdapter) LocalAddr() net.Addr {
	if nc, ok := a.rwc.(net.Conn); ok {
		return nc.LocalAddr()
	}
	return dummyAddr{}
}

func (a *netConnAdapter) RemoteAddr() net.Addr {
	if nc, ok := a.rwc.(net.Conn); ok {
		return nc.RemoteAddr()
	}
	return dummyAddr{}
}

func (a *netConnAdapter) SetDeadline(t time.Time) error {
	if nc, ok := a.rwc.(net.Conn); ok {
		return nc.SetDeadline(t)
	}
	return nil
}

func (a *netConnAdapter) SetReadDeadline(t time.Time) error {
	if nc, ok := a.rwc.(net.Conn); ok {
		return nc.SetReadDeadline(t)
	}
	return nil
}

func (a *netConnAdapter) SetWriteDeadline(t time.Time) error {
	if nc, ok := a.rwc.(net.Conn); ok {
		return nc.SetWriteDeadline(t)
	}
	return nil
}

type dummyAddr struct{}

func (dummyAddr) Network() string { return "shadowsocks-chain" }
func (dummyAddr) String() string  { return "chained" }
