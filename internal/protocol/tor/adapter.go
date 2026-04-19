package tor

import (
	"io"
	"net"
	"time"
)

// netConnAdapter promotes an io.ReadWriteCloser to net.Conn. The tor
// dialer wraps the final stream in a *conn (net.Conn), and when DialThrough
// is handed an io.ReadWriteCloser from a prior chain hop, we need to
// present it as a net.Conn so the returned protocol.Conn keeps its
// contract.
//
// This is a third local copy of the same pattern in trojan/trojan.go and
// shadowsocks/adapter.go. The shadowsocks copy notes that a third user is
// the right moment to extract to a shared package — but scoping that
// refactor alongside two new protocols is more churn than it's worth.
// Extracting belongs in its own commit.
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

func (dummyAddr) Network() string { return "tor-chain" }
func (dummyAddr) String() string  { return "chained" }
