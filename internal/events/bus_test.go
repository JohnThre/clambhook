package events

import (
	"testing"
	"time"
)

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
	for i := 0; i < 5; i++ {
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
	for i := 0; i < 5; i++ {
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
	for i := 0; i < 5; i++ {
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
