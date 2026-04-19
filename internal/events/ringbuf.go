package events

import "sync"

// Ring is a fixed-capacity circular buffer of events for one shard. Used to
// replay missed events to a reconnecting subscriber. Overwrites the oldest
// entry on overflow; callers detect gaps via the oldest retained Lamport.
//
// A sync.Mutex guards the slice — replays are rare compared to appends, but
// an append + a concurrent Since walk must not tear. RWMutex would be
// tempting but append writes every 2 fields, so read-heavy optimization is
// not worth the added complexity.
type Ring struct {
	mu    sync.Mutex
	buf   []Event
	head  int  // index of next write slot
	size  int  // number of valid entries (≤ len(buf))
	capn  int  // cached cap, avoids repeated len(buf) calls
	total uint64 // lifetime append count (for debugging / metrics)
}

// NewRing allocates a ring with the given capacity. Panics on cap ≤ 0 so
// misconfiguration is caught at startup rather than silently dropping.
func NewRing(capacity int) *Ring {
	if capacity <= 0 {
		panic("events.NewRing: capacity must be > 0")
	}
	return &Ring{
		buf:  make([]Event, capacity),
		capn: capacity,
	}
}

// Append records an event. When full, the oldest entry is overwritten.
func (r *Ring) Append(ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.buf[r.head] = ev
	r.head = (r.head + 1) % r.capn
	if r.size < r.capn {
		r.size++
	}
	r.total++
}

// Since returns all buffered events with Lamport > since, in order.
// okAll reports whether `since` is still within the retained range; if false,
// the subscriber has missed events and should emit a replay.gap.
//
// Allocates a new slice so the caller can iterate without holding the lock.
func (r *Ring) Since(since uint64) (events []Event, okAll bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.size == 0 {
		// Empty ring — nothing missed, nothing to replay.
		return nil, true
	}

	// Walk from the oldest slot to the newest, preserving insertion order.
	oldestIdx := (r.head - r.size + r.capn) % r.capn
	oldestLamport := r.buf[oldestIdx].Lamport

	// No gap iff the subscriber's cursor is at or after the event just
	// before our oldest retained entry. I.e., we have event `since+1` or
	// they already saw everything we dropped.
	okAll = oldestLamport <= since+1

	out := make([]Event, 0, r.size)
	for i := 0; i < r.size; i++ {
		idx := (oldestIdx + i) % r.capn
		ev := r.buf[idx]
		if ev.Lamport > since {
			out = append(out, ev)
		}
	}
	return out, okAll
}

// OldestLamport returns the Lamport of the oldest retained event, or 0 if
// the ring is empty. Used to build replay.gap payloads.
func (r *Ring) OldestLamport() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size == 0 {
		return 0
	}
	oldestIdx := (r.head - r.size + r.capn) % r.capn
	return r.buf[oldestIdx].Lamport
}

// Len reports the number of events currently stored.
func (r *Ring) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.size
}
