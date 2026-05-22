package listener

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/events"
)

// newTestListenerWithBus builds a SOCKSv5 listener wired to the given bus
// and a net.Pipe-backed dial stub. Returns the bound address and a channel
// that delivers the server side of each stub dial so the test can drive
// remote-side traffic.
func newTestListenerWithBus(t *testing.T, bus *events.Bus) (addr string, remoteCh <-chan net.Conn) {
	t.Helper()
	ch := make(chan net.Conn, 4)
	s := &SOCKSv5{
		addr: "127.0.0.1:0",
		dial: stubDial(ch),
		opts: Options{EventBus: bus},
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop() })
	return s.Addr(), ch
}

// collectEvents drains sub.Ch() until the given predicate returns true for
// one of the received events, or the timeout expires.
func collectEvents(t *testing.T, sub *events.Subscription, timeout time.Duration, until func(events.Event) bool) []events.Event {
	t.Helper()
	var out []events.Event
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-sub.Ch():
			out = append(out, ev)
			if until(ev) {
				return out
			}
		case <-deadline:
			t.Fatalf("timeout after %v; collected %d events: %+v", timeout, len(out), typesOf(out))
		}
	}
}

func typesOf(evs []events.Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Type
	}
	return out
}

func TestSOCKS5EmitsLifecycleEvents(t *testing.T) {
	bus := events.NewBus(events.Config{
		SubBufferSize: 64,
		RingCapacity:  64,
		MeterInterval: 50 * time.Millisecond,
	})
	defer bus.Close()

	sub := bus.Subscribe(events.Filter{})
	defer sub.Unsubscribe()

	addr, remoteCh := newTestListenerWithBus(t, bus)

	// Client-side SOCKS5 handshake + CONNECT.
	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	defer client.Close()

	// Method selection: no-auth.
	if _, err := client.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(client, make([]byte, 2)); err != nil {
		t.Fatal(err)
	}

	// CONNECT 127.0.0.1:443.
	req := []byte{0x05, 0x01, 0x00, 0x01, 127, 0, 0, 1, 0x01, 0xbb}
	if _, err := client.Write(req); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(client, make([]byte, 10)); err != nil {
		t.Fatal(err)
	}

	remote := <-remoteCh

	// Exchange a few bytes to exercise the meter.
	const greeting = "hello remote"
	const response = "hi client"
	if _, err := client.Write([]byte(greeting)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(greeting))
	if _, err := io.ReadFull(remote, got); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Write([]byte(response)); err != nil {
		t.Fatal(err)
	}
	gotBack := make([]byte, len(response))
	if _, err := io.ReadFull(client, gotBack); err != nil {
		t.Fatal(err)
	}

	// Give the scanner at least one tick to emit bytes events.
	time.Sleep(120 * time.Millisecond)

	// Close the client side; wait for connection.closed.
	client.Close()
	remote.Close()

	evs := collectEvents(t, sub, 2*time.Second, func(e events.Event) bool {
		return e.Type == events.TypeConnectionClosed
	})

	// Verify the listener-level event sequence.
	want := []string{
		events.TypeConnectionOpened,
		events.TypeConnectionDialing,
		events.TypeConnectionEstablished,
	}
	matched := 0
	for _, ev := range evs {
		if matched < len(want) && ev.Type == want[matched] {
			matched++
		}
	}
	if matched != len(want) {
		t.Fatalf("expected lifecycle prefix %v, got stream %v", want, typesOf(evs))
	}

	// connection.bytes at least once, with nonzero totals.
	var sawBytes bool
	var lastBytesData events.ConnectionBytesData
	for _, ev := range evs {
		if ev.Type == events.TypeConnectionBytes {
			sawBytes = true
			lastBytesData = ev.Data.(events.ConnectionBytesData)
		}
	}
	if !sawBytes {
		t.Fatalf("expected at least one connection.bytes event, got %v", typesOf(evs))
	}
	// rx is bytes flowing remote→client (response), tx is bytes client→remote (greeting).
	if lastBytesData.RxTotal < uint64(len(response)) || lastBytesData.TxTotal < uint64(len(greeting)) {
		t.Fatalf("bytes totals too low: rx=%d tx=%d (want ≥%d, ≥%d)",
			lastBytesData.RxTotal, lastBytesData.TxTotal, len(response), len(greeting))
	}

	// connection.closed totals must match what we actually transferred.
	var closedData events.ConnectionClosedData
	for _, ev := range evs {
		if ev.Type == events.TypeConnectionClosed {
			closedData = ev.Data.(events.ConnectionClosedData)
		}
	}
	if closedData.RxTotal != uint64(len(response)) || closedData.TxTotal != uint64(len(greeting)) {
		t.Fatalf("closed totals: rx=%d tx=%d want rx=%d tx=%d",
			closedData.RxTotal, closedData.TxTotal, len(response), len(greeting))
	}
	if closedData.DurationNs <= 0 {
		t.Fatalf("closed duration_ns = %d, want > 0", closedData.DurationNs)
	}

	// Lamport monotonic within the shard (all non-zero-shard events).
	var prev uint64
	for _, ev := range evs {
		if ev.ShardID == 0 {
			continue
		}
		if ev.Lamport <= prev {
			t.Fatalf("lamport not monotonic: prev=%d got=%d (type=%s)",
				prev, ev.Lamport, ev.Type)
		}
		prev = ev.Lamport
	}
}

func TestSOCKS5EventsDisabledWhenNoBus(t *testing.T) {
	// Smoke test: listener should still work when EventBus is nil.
	remoteCh := make(chan net.Conn, 1)
	_, addr := newTestListener(t, nil, stubDial(remoteCh))

	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	defer client.Close()

	if _, err := client.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(client, make([]byte, 2)); err != nil {
		t.Fatal(err)
	}
}
