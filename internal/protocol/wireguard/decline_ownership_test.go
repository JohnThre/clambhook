package wireguard

import (
	"context"
	"io"
	"sync"
	"testing"
)

// declineTracker wraps an io.ReadWriteCloser and records how many times
// Close is called, so tests can assert the decline paths honor the
// DialThrough/DialPacketThrough ownership contract (protocol.go): on
// error the implementation MUST close the passed underlying, exactly once.
type declineTracker struct {
	mu     sync.Mutex
	closes int
}

func (t *declineTracker) Read(p []byte) (int, error)  { return 0, io.EOF }
func (t *declineTracker) Write(p []byte) (int, error) { return len(p), nil }

func (t *declineTracker) Close() error {
	t.mu.Lock()
	t.closes++
	t.mu.Unlock()
	return nil
}

func (t *declineTracker) closeCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closes
}

// TestDialThroughClosesUnderlying proves the WireGuard stream decline path
// takes ownership of the passed underlying and closes it exactly once,
// rather than leaking the prior chain hop's socket. Fails on the old
// behavior (`_ = underlying`), which left it open (closeCount == 0).
func TestDialThroughClosesUnderlying(t *testing.T) {
	d := &dialer{cfg: config{}}
	tracked := &declineTracker{}

	c, err := d.DialThrough(context.Background(), tracked, "1.2.3.4:80")
	if err == nil {
		if c != nil {
			c.Close()
		}
		t.Fatal("DialThrough: expected decline error, got nil")
	}
	if c != nil {
		t.Fatalf("DialThrough returned non-nil conn on decline: %v", c)
	}
	if got := tracked.closeCount(); got != 1 {
		t.Fatalf("underlying Close count = %d, want 1 (no leak, no double-close)", got)
	}
}

// TestDialPacketThroughClosesUnderlying proves the WireGuard packet decline
// path closes the passed underlying exactly once.
func TestDialPacketThroughClosesUnderlying(t *testing.T) {
	d := &dialer{cfg: config{}}
	tracked := &declineTracker{}

	pc, err := d.DialPacketThrough(context.Background(), tracked, "1.2.3.4:80")
	if err == nil {
		if pc != nil {
			pc.Close()
		}
		t.Fatal("DialPacketThrough: expected decline error, got nil")
	}
	if pc != nil {
		t.Fatalf("DialPacketThrough returned non-nil conn on decline: %v", pc)
	}
	if got := tracked.closeCount(); got != 1 {
		t.Fatalf("underlying Close count = %d, want 1 (no leak, no double-close)", got)
	}
}

// TestDeclinePathsTolerateNilUnderlying guards the defensive nil check:
// callers that (contrary to the chain contract) hand a nil underlying must
// still get the decline error, not a panic.
func TestDeclinePathsTolerateNilUnderlying(t *testing.T) {
	d := &dialer{cfg: config{}}
	if _, err := d.DialThrough(context.Background(), nil, "1.2.3.4:80"); err == nil {
		t.Error("DialThrough(nil): expected error, got nil")
	}
	if _, err := d.DialPacketThrough(context.Background(), nil, ""); err == nil {
		t.Error("DialPacketThrough(nil): expected error, got nil")
	}
}
