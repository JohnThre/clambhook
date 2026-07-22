package events

import (
	"sync"
	"sync/atomic"
)

// Shard is a per-connection Lamport counter. Every connection handler owns
// exactly one Shard; all events that connection emits share it. Emits are
// lock-free (single atomic increment) so the hot path has no contention
// across connections.
type Shard struct {
	id      uint64
	lamport atomic.Uint64

	// mu serializes Lamport assignment and ring insertion for this shard.
	// The bus holds it across Tick/Absorb + Ring.Append so that insertion
	// order in the replay buffer always matches per-shard Lamport order,
	// even when the scanner goroutine races with the connection handler.
	// Fan-out happens after releasing mu to keep live delivery non-blocking.
	mu sync.Mutex
}

// ID returns the shard's immutable identifier.
func (s *Shard) ID() uint64 { return s.id }

// Tick increments the shard's Lamport counter and returns the new value.
// This is the normal emit path: one event → one Tick.
func (s *Shard) Tick() uint64 {
	return s.lamport.Add(1)
}

// Absorb merges a remote Lamport value into this shard and returns the new
// local value: max(local, remote) + 1. Used at cross-shard causal edges so
// the receiving event's Lamport is strictly greater than the causing event's.
//
// Implemented as a CAS loop because two concurrent Absorbs must both reflect
// their remote inputs (a plain Load/Store would lose updates).
func (s *Shard) Absorb(remote uint64) uint64 {
	for {
		cur := s.lamport.Load()
		next := cur
		if remote > next {
			next = remote
		}
		next++
		if s.lamport.CompareAndSwap(cur, next) {
			return next
		}
	}
}

// Snapshot reads the current Lamport value without advancing it. Used by
// subscribers to record a resume cursor and by cross-shard edges to sample
// a parent shard's clock.
func (s *Shard) Snapshot() uint64 {
	return s.lamport.Load()
}
