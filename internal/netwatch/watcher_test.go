package netwatch

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestMatchesTrigger(t *testing.T) {
	t.Parallel()
	info := NetworkInfo{InterfaceName: "en0", SSID: "HomeNet", IsWiFi: true}
	cases := []struct {
		name  string
		ssid  string
		iface string
		want  bool
	}{
		{"both empty is never a match", "", "", false},
		{"ssid exact", "HomeNet", "", true},
		{"ssid case-insensitive", "homenet", "", true},
		{"ssid trimmed", "  HomeNet  ", "", true},
		{"ssid mismatch", "OtherNet", "", false},
		{"iface exact", "", "en0", true},
		{"iface case-insensitive", "", "EN0", true},
		{"iface mismatch", "", "en1", false},
		{"both match", "HomeNet", "en0", true},
		{"ssid matches iface not", "HomeNet", "en1", false},
		{"iface matches ssid not", "OtherNet", "en0", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := info.MatchesTrigger(tc.ssid, tc.iface); got != tc.want {
				t.Fatalf("MatchesTrigger(%q, %q) = %v, want %v", tc.ssid, tc.iface, got, tc.want)
			}
		})
	}
}

// MatchesTrigger must not match an empty observed SSID against a wildcard-less
// SSID trigger even when the interface is unknown. This guards the macOS 14+
// "SSID withheld" path where SSID is empty but the interface is still reported.
func TestMatchesTriggerEmptyObservedSSID(t *testing.T) {
	t.Parallel()
	info := NetworkInfo{InterfaceName: "en0", SSID: "", IsWiFi: true}
	if info.MatchesTrigger("HomeNet", "") {
		t.Fatal("empty observed SSID must not match a named SSID trigger")
	}
	if !info.MatchesTrigger("", "en0") {
		t.Fatal("interface-only trigger must still match when SSID is withheld")
	}
}

func recvWithin(t *testing.T, ch <-chan NetworkInfo, d time.Duration) (NetworkInfo, bool) {
	t.Helper()
	select {
	case info, ok := <-ch:
		return info, ok
	case <-time.After(d):
		t.Fatalf("timed out after %s waiting for network event", d)
		return NetworkInfo{}, false
	}
}

// Watch emits the first observation immediately, without waiting a full poll
// interval.
func TestWatchImmediateEmit(t *testing.T) {
	t.Parallel()
	want := NetworkInfo{InterfaceName: "en0", SSID: "HomeNet", IsWiFi: true}
	// A large interval proves the first emit does not wait for a tick.
	w := newWithSource(time.Hour, func() (NetworkInfo, error) {
		return want, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got, ok := recvWithin(t, w.Watch(ctx), time.Second)
	if !ok {
		t.Fatal("channel closed before first emit")
	}
	if got != want {
		t.Fatalf("first emit = %+v, want %+v", got, want)
	}
}

// Watch emits only when the observed network changes; repeated identical
// observations are suppressed.
func TestWatchEmitsOnlyOnChange(t *testing.T) {
	t.Parallel()
	a := NetworkInfo{InterfaceName: "en0", SSID: "A", IsWiFi: true}
	b := NetworkInfo{InterfaceName: "en0", SSID: "B", IsWiFi: true}
	var mu sync.Mutex
	calls := 0
	src := func() (NetworkInfo, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls <= 3 {
			return a, nil
		}
		return b, nil
	}
	w := newWithSource(time.Millisecond, src)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := w.Watch(ctx)

	first, ok := recvWithin(t, ch, time.Second)
	if !ok || first != a {
		t.Fatalf("first emit = %+v (ok=%v), want %+v", first, ok, a)
	}
	// The next value received must be b: the duplicate a observations between
	// polls 1-3 must have been suppressed rather than re-emitted.
	second, ok := recvWithin(t, ch, time.Second)
	if !ok || second != b {
		t.Fatalf("second emit = %+v (ok=%v), want %+v", second, ok, b)
	}
}

// Watch surfaces errors to logging and keeps polling rather than emitting a
// zero value or closing the channel.
func TestWatchSkipsErrors(t *testing.T) {
	t.Parallel()
	want := NetworkInfo{InterfaceName: "en0", IsWiFi: true}
	var mu sync.Mutex
	calls := 0
	src := func() (NetworkInfo, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls <= 2 {
			return NetworkInfo{}, errors.New("probe failed")
		}
		return want, nil
	}
	w := newWithSource(time.Millisecond, src)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got, ok := recvWithin(t, w.Watch(ctx), time.Second)
	if !ok {
		t.Fatal("channel closed while errors were transient")
	}
	if got != want {
		t.Fatalf("emit = %+v, want %+v (errors must be skipped)", got, want)
	}
}

// Cancelling the context stops the watcher and closes the channel.
func TestWatchCancellationClosesChannel(t *testing.T) {
	t.Parallel()
	fixed := NetworkInfo{InterfaceName: "en0", SSID: "A", IsWiFi: true}
	w := newWithSource(time.Millisecond, func() (NetworkInfo, error) {
		return fixed, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	ch := w.Watch(ctx)

	if _, ok := recvWithin(t, ch, time.Second); !ok {
		t.Fatal("channel closed before first emit")
	}
	cancel()

	// After cancellation the channel must eventually close. Drain any buffered
	// or in-flight change emissions until the close is observed.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // channel closed as required
			}
		case <-deadline:
			t.Fatal("channel not closed after cancellation")
		}
	}
}
