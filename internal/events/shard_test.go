package events

import (
	"sync"
	"testing"
)

func TestShardTickMonotonic(t *testing.T) {
	s := &Shard{id: 7}
	prev := uint64(0)
	for i := 0; i < 1000; i++ {
		cur := s.Tick()
		if cur <= prev {
			t.Fatalf("Tick not monotonic: prev=%d cur=%d", prev, cur)
		}
		prev = cur
	}
	if got := s.Snapshot(); got != prev {
		t.Fatalf("Snapshot after 1000 Ticks: got %d want %d", got, prev)
	}
}

func TestShardAbsorbAdvancesBeyondRemote(t *testing.T) {
	cases := []struct {
		name          string
		start, remote uint64
		wantAtLeast   uint64
	}{
		{"remote_ahead", 3, 10, 11},
		{"local_ahead", 10, 3, 11},
		{"equal", 5, 5, 6},
		{"from_zero", 0, 0, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Shard{id: 1}
			s.lamport.Store(c.start)
			got := s.Absorb(c.remote)
			if got < c.wantAtLeast {
				t.Fatalf("Absorb(%d) on start=%d: got %d, want >= %d",
					c.remote, c.start, got, c.wantAtLeast)
			}
		})
	}
}

// TestShardConcurrentTickAbsorb hammers a shard with many concurrent Ticks
// and Absorbs. Correctness property: after N operations, Snapshot() must
// be at least N (every operation advances the counter by at least 1).
func TestShardConcurrentTickAbsorb(t *testing.T) {
	s := &Shard{id: 42}
	const goroutines = 16
	const opsPerG = 1000

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerG; i++ {
				if i%3 == 0 {
					// Remote Lamport values that sometimes leap ahead.
					s.Absorb(uint64(g*1000 + i))
				} else {
					s.Tick()
				}
			}
		}()
	}
	wg.Wait()

	total := uint64(goroutines * opsPerG)
	if got := s.Snapshot(); got < total {
		t.Fatalf("after %d concurrent ops, Snapshot=%d < %d", total, got, total)
	}
}
