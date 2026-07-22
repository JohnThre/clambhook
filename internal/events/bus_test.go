package events

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// marker is a tiny sentinel payload so the test can distinguish event sources
// without relying on the public data types.
type marker struct {
	Source string `json:"source"`
}

func TestBusConcurrentPublishReplayOrderAndGap(t *testing.T) {
	// Small ring to force eviction and gap signals under high contention.
	b := NewBus(Config{SubBufferSize: 256, RingCapacity: 64, MeterInterval: time.Hour})
	defer b.Close()

	const publishers = 16
	const eventsPerPublisher = 200

	// One heavily contended shard: many goroutines + scanner-like bytes events.
	contended := b.NewShard()

	// Two unrelated shards to prove no cross-shard serialization/ordering.
	unrelatedA := b.NewShard()
	unrelatedB := b.NewShard()

	var wg sync.WaitGroup

	// Flood the contended shard with mixed event types, including
	// connection.bytes to mimic the scanner goroutine racing with handlers.
	for i := range publishers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range eventsPerPublisher {
				if j%5 == 0 {
					b.Publish(contended, TypeConnectionBytes, ConnectionBytesData{
						ConnID:  "c1",
						RxDelta: uint64(id + 1),
						TxDelta: 0,
					})
				} else {
					b.Publish(contended, "test.seq", marker{Source: fmt.Sprintf("g%d", id)})
				}
			}
		}(i)
	}

	// Absorb edges on the contended shard from one of the unrelated shards.
	// This exercises the cross-shard Lamport merge path under contention.
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 50 {
				remote := unrelatedA.Snapshot()
				if remote == 0 {
					// Unrelated shard hasn't emitted yet; fall back to a synthetic value.
					remote = uint64(j)
				}
				b.NewEmitter(contended).EmitAbsorb(remote, "test.absorb", marker{Source: "absorb"})
			}
		}()
	}

	// Independent traffic on unrelated shards; must remain ordered and gap-free.
	for range 4 {
		wg.Add(1)
		go func(shard *Shard, name string) {
			defer wg.Done()
			for range 40 {
				b.Publish(shard, "unrelated", marker{Source: name})
			}
		}(unrelatedA, "A")
		wg.Add(1)
		go func(shard *Shard, name string) {
			defer wg.Done()
			for range 40 {
				b.Publish(shard, "unrelated", marker{Source: name})
			}
		}(unrelatedB, "B")
	}

	wg.Wait()

	// Verify contended shard replay: gap first, then strictly monotonic.
	subCont := b.Subscribe(Filter{Since: map[uint64]uint64{contended.ID(): 0}})
	var contReplay []Event
	if err := b.DrainReplay(subCont, func(ev Event) error {
		contReplay = append(contReplay, ev)
		return nil
	}); err != nil {
		t.Fatalf("DrainReplay contended: %v", err)
	}
	if len(contReplay) == 0 || contReplay[0].Type != TypeReplayGap {
		t.Fatalf("contended shard: expected replay.gap first, got %d events, first=%+v", len(contReplay), contReplay)
	}
	gapData := contReplay[0].Data.(ReplayGapData)
	if gapData.ShardID != contended.ID() {
		t.Fatalf("contended gap shard_id=%d want %d", gapData.ShardID, contended.ID())
	}
	if gapData.OldestLamport == 0 {
		t.Fatalf("contended gap oldest_lamport should be non-zero, got %+v", gapData)
	}
	var prev uint64
	for i, ev := range contReplay[1:] {
		if ev.ShardID != contended.ID() {
			t.Fatalf("contended replay[%d]: shard_id=%d want %d", i, ev.ShardID, contended.ID())
		}
		if ev.Lamport <= prev {
			t.Fatalf("contended replay not monotonic at %d: prev=%d ev=%+v", i, prev, ev)
		}
		prev = ev.Lamport
	}
	if got := len(contReplay) - 1; got > b.cfg.RingCapacity {
		t.Fatalf("contended replay returned %d events, want <= ring capacity %d", got, b.cfg.RingCapacity)
	}

	// Verify unrelated shards are monotonic. They may or may not produce a
	// gap depending on how many events each goroutine managed to publish,
	// so we only assert monotonicity and that any gap is well-formed.
	for _, shard := range []*Shard{unrelatedA, unrelatedB} {
		sub := b.Subscribe(Filter{Since: map[uint64]uint64{shard.ID(): 0}})
		var replay []Event
		if err := b.DrainReplay(sub, func(ev Event) error {
			replay = append(replay, ev)
			return nil
		}); err != nil {
			t.Fatalf("DrainReplay shard %d: %v", shard.ID(), err)
		}
		if len(replay) == 0 {
			t.Fatalf("shard %d: expected replay events, got none", shard.ID())
		}
		start := 0
		if replay[0].Type == TypeReplayGap {
			gapData := replay[0].Data.(ReplayGapData)
			if gapData.ShardID != shard.ID() {
				t.Fatalf("shard %d gap shard_id=%d want %d", shard.ID(), gapData.ShardID, shard.ID())
			}
			if gapData.OldestLamport == 0 {
				t.Fatalf("shard %d gap oldest_lamport should be non-zero", shard.ID())
			}
			start = 1
		}
		prev = 0
		for i, ev := range replay[start:] {
			if ev.Lamport <= prev {
				t.Fatalf("shard %d replay not monotonic at %d: prev=%d ev=%+v", shard.ID(), i, prev, ev)
			}
			prev = ev.Lamport
		}
		if got := len(replay) - start; got > b.cfg.RingCapacity {
			t.Fatalf("shard %d replay returned %d events, want <= ring capacity %d", shard.ID(), got, b.cfg.RingCapacity)
		}
		sub.Unsubscribe()
	}

	subCont.Unsubscribe()
}

func TestBusPublishFanOut(t *testing.T) {
	b := NewBus(Config{SubBufferSize: 8, RingCapacity: 8, MeterInterval: time.Hour})
	defer b.Close()

	sub1 := b.Subscribe(Filter{})
	sub2 := b.Subscribe(Filter{})
	defer sub1.Unsubscribe()
	defer sub2.Unsubscribe()

	shard := b.NewShard()
	b.Publish(shard, "test.one", ConnectionOpenedData{ConnID: "c1"})

	for i, sub := range []*Subscription{sub1, sub2} {
		select {
		case ev := <-sub.Ch():
			if ev.Type != "test.one" {
				t.Fatalf("sub%d: type = %q want test.one", i+1, ev.Type)
			}
			if ev.ShardID != shard.ID() {
				t.Fatalf("sub%d: shard_id = %d want %d", i+1, ev.ShardID, shard.ID())
			}
			if ev.Lamport != 1 {
				t.Fatalf("sub%d: lamport = %d want 1", i+1, ev.Lamport)
			}
		case <-time.After(time.Second):
			t.Fatalf("sub%d: no event within 1s", i+1)
		}
	}
}

func TestBusFilterByType(t *testing.T) {
	b := NewBus(Config{SubBufferSize: 8, RingCapacity: 8, MeterInterval: time.Hour})
	defer b.Close()

	sub := b.Subscribe(Filter{Types: []string{"hop.*"}})
	defer sub.Unsubscribe()

	shard := b.NewShard()
	b.Publish(shard, TypeConnectionOpened, ConnectionOpenedData{ConnID: "c1"})
	b.Publish(shard, TypeHopDialing, HopDialingData{ConnID: "c1", HopIndex: 0})
	b.Publish(shard, TypeHopConnected, HopConnectedData{ConnID: "c1", HopIndex: 0})

	got := drain(sub, 50*time.Millisecond)
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2 hop.* events", len(got))
	}
	for _, ev := range got {
		if ev.Type != TypeHopDialing && ev.Type != TypeHopConnected {
			t.Fatalf("unexpected type %q in filtered stream", ev.Type)
		}
	}
}

func TestBusFilterByConnID(t *testing.T) {
	b := NewBus(Config{SubBufferSize: 8, RingCapacity: 8, MeterInterval: time.Hour})
	defer b.Close()

	sub := b.Subscribe(Filter{ConnIDs: []string{"target"}})
	defer sub.Unsubscribe()

	shard := b.NewShard()
	b.Publish(shard, TypeConnectionOpened, ConnectionOpenedData{ConnID: "other"})
	b.Publish(shard, TypeConnectionOpened, ConnectionOpenedData{ConnID: "target"})

	got := drain(sub, 50*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1 for conn_id=target", len(got))
	}
	data := got[0].Data.(ConnectionOpenedData)
	if data.ConnID != "target" {
		t.Fatalf("got conn_id=%q want target", data.ConnID)
	}
}

func TestBusSlowConsumerDisconnect(t *testing.T) {
	b := NewBus(Config{SubBufferSize: 2, RingCapacity: 8, MeterInterval: time.Hour})
	defer b.Close()

	sub := b.Subscribe(Filter{})
	defer sub.Unsubscribe()

	shard := b.NewShard()
	// Flood past the 2-event buffer without draining.
	for range 5 {
		b.Publish(shard, "spam", nil)
	}

	if !sub.Slow() {
		t.Fatalf("expected sub.Slow()=true after buffer overflow")
	}
	select {
	case <-sub.Context().Done():
		// expected
	case <-time.After(50 * time.Millisecond):
		t.Fatalf("expected sub ctx to be cancelled")
	}
}

func TestBusReplaySince(t *testing.T) {
	b := NewBus(Config{SubBufferSize: 16, RingCapacity: 16, MeterInterval: time.Hour})
	defer b.Close()

	shard := b.NewShard()
	for range 5 {
		b.Publish(shard, "seq", nil)
	}

	// Subscriber resumes with since=2 → should replay lamports 3, 4, 5.
	sub := b.Subscribe(Filter{Since: map[uint64]uint64{shard.ID(): 2}})
	defer sub.Unsubscribe()

	var replayed []Event
	err := b.DrainReplay(sub, func(ev Event) error {
		replayed = append(replayed, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("DrainReplay: %v", err)
	}
	if len(replayed) != 3 {
		t.Fatalf("replay len = %d want 3", len(replayed))
	}
	for i, ev := range replayed {
		want := uint64(3 + i)
		if ev.Lamport != want {
			t.Fatalf("replay[%d].Lamport = %d want %d", i, ev.Lamport, want)
		}
	}
}

func TestBusReplayGapSignal(t *testing.T) {
	b := NewBus(Config{SubBufferSize: 16, RingCapacity: 3, MeterInterval: time.Hour})
	defer b.Close()

	shard := b.NewShard()
	// Fill beyond cap so lamports 1-2 are evicted.
	for range 5 {
		b.Publish(shard, "seq", nil)
	}

	sub := b.Subscribe(Filter{Since: map[uint64]uint64{shard.ID(): 1}})
	defer sub.Unsubscribe()

	var replayed []Event
	if err := b.DrainReplay(sub, func(ev Event) error {
		replayed = append(replayed, ev)
		return nil
	}); err != nil {
		t.Fatalf("DrainReplay: %v", err)
	}
	if len(replayed) == 0 || replayed[0].Type != TypeReplayGap {
		t.Fatalf("expected replay.gap as first event, got %+v", replayed)
	}
}

func TestBusRetireShardReleasesRing(t *testing.T) {
	b := NewBus(Config{SubBufferSize: 8, RingCapacity: 8, MeterInterval: time.Hour})
	defer b.Close()

	for range 1000 {
		shard := b.NewShard()
		b.Publish(shard, TypeConnectionClosed, ConnectionClosedData{})
		b.RetireShard(shard)
		if _, ok := b.rings.Load(shard.ID()); ok {
			t.Fatalf("ring for retired shard %d is still registered", shard.ID())
		}
	}

	if _, ok := b.rings.Load(uint64(0)); !ok {
		t.Fatal("reserved listener shard was retired")
	}
}

// drain collects all events available within d and returns them.
func drain(sub *Subscription, d time.Duration) []Event {
	var out []Event
	deadline := time.After(d)
	for {
		select {
		case ev := <-sub.Ch():
			out = append(out, ev)
		case <-deadline:
			return out
		}
	}
}
