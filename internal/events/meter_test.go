package events

import (
	"bytes"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMeteredReaderCountsBytes(t *testing.T) {
	src := strings.NewReader("hello world")
	var counter atomic.Uint64
	mr := NewMeteredReader(src, &counter)

	var sink bytes.Buffer
	n, err := io.Copy(&sink, mr)
	if err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	if n != 11 {
		t.Fatalf("copied %d bytes want 11", n)
	}
	if got := counter.Load(); got != 11 {
		t.Fatalf("counter = %d want 11", got)
	}
}

func TestScannerEmitsOnlyOnDelta(t *testing.T) {
	b := NewBus(Config{SubBufferSize: 16, RingCapacity: 8, MeterInterval: 20 * time.Millisecond})
	defer b.Close()

	sub := b.Subscribe(Filter{Types: []string{TypeConnectionBytes}})
	defer sub.Unsubscribe()

	shard := b.NewShard()
	meter := NewConnMeter("c1", shard)
	b.RegisterMeter(meter)

	// Two ticks of no activity → no events.
	time.Sleep(60 * time.Millisecond)
	if idle := drain(sub, 10*time.Millisecond); len(idle) != 0 {
		t.Fatalf("idle ticks produced %d bytes events, want 0", len(idle))
	}

	// Burst traffic, wait for one tick → one event.
	meter.Rx().Add(1024)
	meter.Tx().Add(512)

	time.Sleep(60 * time.Millisecond)
	events := drain(sub, 10*time.Millisecond)
	if len(events) == 0 {
		t.Fatalf("expected at least one bytes event, got 0")
	}
	first := events[0].Data.(ConnectionBytesData)
	if first.RxDelta != 1024 || first.TxDelta != 512 {
		t.Fatalf("deltas = rx=%d tx=%d want rx=1024 tx=512", first.RxDelta, first.TxDelta)
	}
	if first.RxTotal != 1024 || first.TxTotal != 512 {
		t.Fatalf("totals = rx=%d tx=%d want rx=1024 tx=512", first.RxTotal, first.TxTotal)
	}

	rx, tx := b.UnregisterMeter("c1")
	if rx != 1024 || tx != 512 {
		t.Fatalf("Unregister totals = rx=%d tx=%d want rx=1024 tx=512", rx, tx)
	}
}
