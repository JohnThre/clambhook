package tor

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
)

// closeTrackingConn wraps an io.ReadWriteCloser and records how many times
// Close was invoked, so a test can assert that DialThrough takes ownership
// of the underlying stream on its error paths.
type closeTrackingConn struct {
	io.ReadWriteCloser
	mu     sync.Mutex
	closes int
}

func (c *closeTrackingConn) Close() error {
	c.mu.Lock()
	c.closes++
	c.mu.Unlock()
	return c.ReadWriteCloser.Close()
}

func (c *closeTrackingConn) closeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closes
}

// TestDialThroughClosesUnderlyingOnHandshakeError proves that when the
// SOCKS5 CONNECT is rejected, DialThrough closes the underlying stream it
// was handed instead of leaking the prior chain hop's socket.
func TestDialThroughClosesUnderlyingOnHandshakeError(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		// 0x05 = connection refused: a well-formed rejection reply that
		// drives socks5Connect to return an error after a clean handshake.
		fakeSocks5Server(t, server, fakeSocks5Opts{connectReply: 0x05})
		close(done)
	}()

	tracked := &closeTrackingConn{ReadWriteCloser: client}
	d := &dialer{cfg: config{socksAddr: "unused:0"}}
	c, err := d.DialThrough(context.Background(), tracked, "example.com:443")
	if err == nil {
		if c != nil {
			c.Close()
		}
		t.Fatal("DialThrough succeeded, want handshake error")
	}
	if c != nil {
		t.Fatalf("DialThrough returned non-nil conn on error: %v", c)
	}
	if got := tracked.closeCount(); got != 1 {
		t.Fatalf("underlying Close count = %d, want 1", got)
	}
	<-done
}
