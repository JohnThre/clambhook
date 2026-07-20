package shadowsocks

import (
	"context"
	"io"
	"sync"
	"testing"
)

// declineTracker wraps an io.ReadWriteCloser and records how many times
// Close is called, so the test can assert DialPacketThrough honors the
// ownership contract (protocol.go): on error the implementation MUST close
// the passed underlying, exactly once.
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

// TestDialPacketThroughClosesUnderlying proves the Shadowsocks UDP decline
// path takes ownership of the passed underlying and closes it exactly once,
// rather than leaking the prior chain hop's socket. Fails on the old
// behavior (`_ = underlying`), which left it open (closeCount == 0).
func TestDialPacketThroughClosesUnderlying(t *testing.T) {
	d := &dialer{cfg: config{method: "chacha20-ietf-poly1305"}}
	tracked := &declineTracker{}

	pc, err := d.DialPacketThrough(context.Background(), tracked, "example.com:443")
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

// TestDialPacketThroughToleratesNilUnderlying guards the defensive nil
// check: a nil underlying must still yield the decline error, not a panic.
func TestDialPacketThroughToleratesNilUnderlying(t *testing.T) {
	d := &dialer{cfg: config{method: "chacha20-ietf-poly1305"}}
	if _, err := d.DialPacketThrough(context.Background(), nil, "example.com:443"); err == nil {
		t.Error("DialPacketThrough(nil): expected error, got nil")
	}
}
