package events

import (
	"io"
	"sync/atomic"
	"time"
)

// ConnMeter tracks rx/tx byte totals for one connection. The counters are
// atomic so the hot path (meteredReader.Read) has zero lock acquisition.
//
// lastRx / lastTx / lastTickAt are touched only by the bus scanner
// goroutine and so need no synchronization — the scanner owns them.
type ConnMeter struct {
	ConnID string
	Shard  *Shard

	rx, tx atomic.Uint64

	lastRx, lastTx uint64
	lastTickAt     time.Time
	StartedAt      time.Time
}

// NewConnMeter constructs a meter anchored to the given connection ID and
// shard. StartedAt and lastTickAt are set to now so the first bytes event's
// IntervalNs reflects the time since accept.
func NewConnMeter(connID string, shard *Shard) *ConnMeter {
	now := time.Now()
	return &ConnMeter{
		ConnID:     connID,
		Shard:      shard,
		StartedAt:  now,
		lastTickAt: now,
	}
}

// Rx exposes the rx counter pointer for use in meteredReader. Returning a
// pointer (rather than a Load) lets the reader do a lock-free Add per Read.
func (m *ConnMeter) Rx() *atomic.Uint64 { return &m.rx }

// Tx exposes the tx counter pointer. Symmetric to Rx().
func (m *ConnMeter) Tx() *atomic.Uint64 { return &m.tx }

// RxTotal returns the current rx total. Safe for any goroutine.
func (m *ConnMeter) RxTotal() uint64 { return m.rx.Load() }

// TxTotal returns the current tx total. Safe for any goroutine.
func (m *ConnMeter) TxTotal() uint64 { return m.tx.Load() }

// MeteredReader wraps an io.Reader and atomically adds each Read's byte
// count to counter. Use it to instrument a relay: wrap each direction's
// source reader, point to the matching rx/tx counter.
//
// The wrapper adds nothing else — no buffering, no copy. The io.Copy loop
// in relay() stays zero-overhead apart from the single Add.
type MeteredReader struct {
	src     io.Reader
	counter *atomic.Uint64
}

// NewMeteredReader returns a reader that increments counter by n after
// each successful Read of n bytes.
func NewMeteredReader(src io.Reader, counter *atomic.Uint64) *MeteredReader {
	return &MeteredReader{src: src, counter: counter}
}

// Read implements io.Reader.
func (r *MeteredReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	if n > 0 && r.counter != nil {
		r.counter.Add(uint64(n))
	}
	return n, err
}
