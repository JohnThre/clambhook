package events

import (
	"testing"
)

func mkEvent(lamport uint64) Event {
	return Event{ShardID: 1, Lamport: lamport, Type: "test"}
}

func TestRingAppendUnderCap(t *testing.T) {
	r := NewRing(5)
	for i := uint64(1); i <= 3; i++ {
		r.Append(mkEvent(i))
	}
	if r.Len() != 3 {
		t.Fatalf("Len after 3 appends: got %d want 3", r.Len())
	}
	got, okAll := r.Since(0)
	if !okAll {
		t.Fatalf("Since(0) with 3 events starting at L=1: should be complete (no gap)")
	}
	if len(got) != 3 {
		t.Fatalf("Since(0): got %d events want 3", len(got))
	}
	for i, ev := range got {
		if ev.Lamport != uint64(i+1) {
			t.Fatalf("got[%d].Lamport = %d, want %d", i, ev.Lamport, i+1)
		}
	}
}

func TestRingWrapAroundEviction(t *testing.T) {
	r := NewRing(3)
	for i := uint64(1); i <= 5; i++ {
		r.Append(mkEvent(i))
	}
	// Oldest retained should be L=3 (1, 2 were evicted).
	if got := r.OldestLamport(); got != 3 {
		t.Fatalf("OldestLamport after 5 appends to cap=3: got %d want 3", got)
	}
	if r.Len() != 3 {
		t.Fatalf("Len: got %d want 3", r.Len())
	}
	events, okAll := r.Since(2)
	// since=2, oldest=3, no gap (subscriber saw L=2, next is L=3 which we have).
	if !okAll {
		t.Fatalf("Since(2) with oldest=3: expected no gap")
	}
	if len(events) != 3 {
		t.Fatalf("Since(2): got %d events want 3", len(events))
	}
	if events[0].Lamport != 3 || events[2].Lamport != 5 {
		t.Fatalf("event order: got [%d..%d] want [3..5]",
			events[0].Lamport, events[2].Lamport)
	}
}

func TestRingGapSignaling(t *testing.T) {
	r := NewRing(3)
	for i := uint64(1); i <= 5; i++ {
		r.Append(mkEvent(i))
	}
	// Subscriber saw L=1. Ring retains L=3..5. L=2 was evicted → gap.
	_, okAll := r.Since(1)
	if okAll {
		t.Fatalf("Since(1) with oldest=3: expected gap, got okAll=true")
	}
}

func TestRingSinceAtOrAfterNewest(t *testing.T) {
	r := NewRing(3)
	r.Append(mkEvent(10))
	r.Append(mkEvent(11))

	events, okAll := r.Since(11)
	if !okAll {
		t.Fatalf("Since(11) with newest=11: expected no gap")
	}
	if len(events) != 0 {
		t.Fatalf("Since(11): expected empty, got %d events", len(events))
	}
}

func TestRingEmpty(t *testing.T) {
	r := NewRing(3)
	if got := r.OldestLamport(); got != 0 {
		t.Fatalf("OldestLamport empty: got %d want 0", got)
	}
	events, okAll := r.Since(100)
	if !okAll {
		t.Fatalf("Since on empty ring should be okAll=true (nothing missed)")
	}
	if len(events) != 0 {
		t.Fatalf("Since on empty: expected empty, got %d", len(events))
	}
}
