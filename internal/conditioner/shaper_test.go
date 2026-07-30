package conditioner

import (
	"net"
	"sync"
	"testing"
	"time"
)

// fakeConn is an in-memory net.Conn that echoes writes into a read buffer so
// tests can drive Read/Write without a real socket.
type fakeConn struct {
	mu  sync.Mutex
	buf []byte
}

func (c *fakeConn) Read(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.buf) == 0 {
		return 0, nil
	}
	n := copy(b, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}

func (c *fakeConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf = append(c.buf, b...)
	return len(b), nil
}

func (c *fakeConn) Close() error                       { return nil }
func (c *fakeConn) LocalAddr() net.Addr                { return dummyAddr{} }
func (c *fakeConn) RemoteAddr() net.Addr               { return dummyAddr{} }
func (c *fakeConn) SetDeadline(t time.Time) error      { return nil }
func (c *fakeConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *fakeConn) SetWriteDeadline(t time.Time) error { return nil }

type dummyAddr struct{}

func (dummyAddr) Network() string { return "tcp" }
func (dummyAddr) String() string  { return "127.0.0.1:0" }

// fakePacketConn is an in-memory net.PacketConn that records written payloads.
type fakePacketConn struct {
	mu      sync.Mutex
	written int
	inbox   [][]byte
}

func (c *fakePacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.inbox) == 0 {
		return 0, dummyAddr{}, nil
	}
	n := copy(b, c.inbox[0])
	c.inbox = c.inbox[1:]
	return n, dummyAddr{}, nil
}

func (c *fakePacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.written++
	return len(b), nil
}

func (c *fakePacketConn) Close() error                       { return nil }
func (c *fakePacketConn) LocalAddr() net.Addr                { return dummyAddr{} }
func (c *fakePacketConn) SetDeadline(t time.Time) error      { return nil }
func (c *fakePacketConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *fakePacketConn) SetWriteDeadline(t time.Time) error { return nil }

func (c *fakePacketConn) writes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.written
}

func TestDisabledIsPassthrough(t *testing.T) {
	s := New(Config{Enabled: false, DownloadKbps: 10})
	base := &fakeConn{}
	if got := s.WrapConn(base); got != base {
		t.Fatalf("disabled shaper must return the original conn unchanged")
	}
	basePkt := &fakePacketConn{}
	if got := s.WrapPacketConn(basePkt); got != basePkt {
		t.Fatalf("disabled shaper must return the original packet conn unchanged")
	}
}

func TestEnabledButNoParamsIsPassthrough(t *testing.T) {
	s := New(Config{Enabled: true})
	base := &fakeConn{}
	if got := s.WrapConn(base); got != base {
		t.Fatalf("enabled shaper with no params must be a passthrough")
	}
}

func TestBandwidthCapLimitsThroughput(t *testing.T) {
	// ~524 kbps => 65536 bytes/sec, so the burst equals minBurst (64 KiB).
	// The first burst is admitted immediately; the second chunk must wait
	// for a full refill (~1s), giving a measurable, bounded delay.
	s := New(Config{Enabled: true, UploadKbps: 524})
	conn := s.WrapConn(&fakeConn{})

	payload := make([]byte, minBurst*2) // exceeds a single burst
	start := time.Now()
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	elapsed := time.Since(start)
	// The second chunk waits for tokens to refill; assert a conservative
	// lower bound well under the true ~1s so the test is not flaky.
	if elapsed < 500*time.Millisecond {
		t.Fatalf("expected bandwidth cap to slow the write, took %v", elapsed)
	}
}

func TestLatencyAddsDelay(t *testing.T) {
	s := New(Config{Enabled: true, Latency: 40 * time.Millisecond})
	conn := s.WrapConn(&fakeConn{})
	start := time.Now()
	if _, err := conn.Write([]byte("hi")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Fatalf("expected latency delay, took %v", elapsed)
	}
}

func TestPacketLossFullDrop(t *testing.T) {
	s := New(Config{Enabled: true, LossPercent: 100})
	base := &fakePacketConn{}
	conn := s.WrapPacketConn(base)
	for i := 0; i < 20; i++ {
		if _, err := conn.WriteTo([]byte("x"), dummyAddr{}); err != nil {
			t.Fatalf("writeto: %v", err)
		}
	}
	if base.writes() != 0 {
		t.Fatalf("100%% loss must drop all datagrams, got %d delivered", base.writes())
	}
}

func TestPacketLossZeroDeliversAll(t *testing.T) {
	s := New(Config{Enabled: true, LossPercent: 0, Latency: time.Millisecond})
	base := &fakePacketConn{}
	conn := s.WrapPacketConn(base)
	for i := 0; i < 20; i++ {
		if _, err := conn.WriteTo([]byte("x"), dummyAddr{}); err != nil {
			t.Fatalf("writeto: %v", err)
		}
	}
	if base.writes() != 20 {
		t.Fatalf("0%% loss must deliver every datagram, got %d", base.writes())
	}
}

func TestUpdateSwapsConfigForNewConns(t *testing.T) {
	s := New(Config{Enabled: false})
	base := &fakeConn{}
	if got := s.WrapConn(base); got != base {
		t.Fatalf("expected passthrough before update")
	}
	s.Update(Config{Enabled: true, Latency: 10 * time.Millisecond})
	if got := s.WrapConn(&fakeConn{}); got == nil {
		t.Fatalf("expected wrapped conn after update")
	}
	if _, ok := s.WrapConn(&fakeConn{}).(*shapedConn); !ok {
		t.Fatalf("expected *shapedConn after enabling")
	}
}

func TestConcurrentReadWrite(t *testing.T) {
	s := New(Config{Enabled: true, DownloadKbps: 1000, UploadKbps: 1000})
	conn := s.WrapConn(&fakeConn{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			buf := make([]byte, 128)
			for j := 0; j < 50; j++ {
				_, _ = conn.Write(buf)
			}
		}()
		go func() {
			defer wg.Done()
			buf := make([]byte, 128)
			for j := 0; j < 50; j++ {
				_, _ = conn.Read(buf)
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent read/write deadlocked or was too slow")
	}
}

func TestNilShaperWrapsAreSafe(t *testing.T) {
	var s *Shaper
	base := &fakeConn{}
	if got := s.WrapConn(base); got != base {
		t.Fatalf("nil shaper must passthrough conns")
	}
	basePkt := &fakePacketConn{}
	if got := s.WrapPacketConn(basePkt); got != basePkt {
		t.Fatalf("nil shaper must passthrough packet conns")
	}
}
