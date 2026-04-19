package events

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Config tunes bus storage + scheduling. Zero values use sensible defaults.
type Config struct {
	// SubBufferSize is the buffered-channel depth per subscriber. A
	// subscriber that can't keep up and fills this buffer is dropped.
	// Default: 256.
	SubBufferSize int

	// RingCapacity is the per-shard replay buffer size. Events older than
	// this are evicted, which forces subscribers with stale Since cursors
	// to receive a replay.gap signal. Default: 512.
	RingCapacity int

	// MeterInterval is the cadence at which the scanner goroutine emits
	// per-connection bandwidth events. Default: 1s.
	MeterInterval time.Duration
}

// DefaultConfig returns the documented default tuning.
func DefaultConfig() Config {
	return Config{
		SubBufferSize: 256,
		RingCapacity:  512,
		MeterInterval: time.Second,
	}
}

// fill replaces any zero field with the default.
func (c Config) fill() Config {
	d := DefaultConfig()
	if c.SubBufferSize <= 0 {
		c.SubBufferSize = d.SubBufferSize
	}
	if c.RingCapacity <= 0 {
		c.RingCapacity = d.RingCapacity
	}
	if c.MeterInterval <= 0 {
		c.MeterInterval = d.MeterInterval
	}
	return c
}

// nextShardID is package-global so shard IDs are unique even across bus
// restarts within a single process. Shard 0 is reserved for the listener-
// level events the bus itself emits (listener.* in the future); real shards
// start at 1.
var nextShardID atomic.Uint64

// Bus is the central event pub-sub. Lifetime is process-scoped: the daemon
// creates one in main, threads it through engine → listeners → api, and
// calls Close on shutdown.
type Bus struct {
	cfg Config

	mu     sync.RWMutex
	subs   map[uint64]*Subscription
	nextID atomic.Uint64

	rings  sync.Map // uint64 shardID → *Ring
	meters sync.Map // string connID → *ConnMeter

	// listenerShard is reserved shard 0, used for engine/listener-level
	// events that aren't tied to a connection (e.g., a future
	// listener.started event). Kept for consistency; always allocated so
	// `rings.Load(0)` is never nil.
	listenerShard *Shard

	scannerCtx    context.Context
	scannerCancel context.CancelFunc
	scannerDone   chan struct{}

	closed atomic.Bool
}

// NewBus constructs a bus with the given config (zero-filled from
// DefaultConfig). The scanner goroutine is started immediately; Close stops
// it and cancels all subscriptions.
func NewBus(cfg Config) *Bus {
	cfg = cfg.fill()
	ctx, cancel := context.WithCancel(context.Background())
	b := &Bus{
		cfg:           cfg,
		subs:          make(map[uint64]*Subscription),
		listenerShard: &Shard{id: 0},
		scannerCtx:    ctx,
		scannerCancel: cancel,
		scannerDone:   make(chan struct{}),
	}
	// Pre-register shard 0's ring so listener-level emits never hit a nil.
	b.rings.Store(uint64(0), NewRing(cfg.RingCapacity))

	go b.scannerLoop()
	return b
}

// NewShard allocates a fresh shard with its own ring buffer. Returned
// shards have IDs starting at 1 (shard 0 is reserved).
func (b *Bus) NewShard() *Shard {
	id := nextShardID.Add(1)
	s := &Shard{id: id}
	b.rings.Store(id, NewRing(b.cfg.RingCapacity))
	return s
}

// Publish records an event at Lamport = shard.Tick() and broadcasts to all
// matching subscribers. If a subscriber's buffered channel is full, its ctx
// is cancelled so the WS handler can close the connection with a "slow
// consumer" status — dropping events silently would corrupt per-shard
// Lamport continuity on that subscriber.
//
// The shard parameter determines which Lamport counter and which ring
// buffer the event lives in. Callers typically use Emitter.Emit instead of
// this method directly.
func (b *Bus) Publish(shard *Shard, eventType string, data any) {
	if b.closed.Load() {
		return
	}
	ev := Event{
		ShardID: shard.ID(),
		Lamport: shard.Tick(),
		TsNs:    time.Now().UnixNano(),
		Type:    eventType,
		Data:    data,
	}
	b.deliver(ev)
}

// publishPreTicked broadcasts an event whose Lamport was already assigned
// (e.g., after Absorb). Internal helper for EmitAbsorb and replay-gap
// synthesis.
func (b *Bus) publishPreTicked(ev Event) {
	if b.closed.Load() {
		return
	}
	b.deliver(ev)
}

// deliver appends to the shard ring and fans out to subscribers.
func (b *Bus) deliver(ev Event) {
	if r, ok := b.rings.Load(ev.ShardID); ok {
		r.(*Ring).Append(ev)
	}

	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, sub := range b.subs {
		if !sub.filter.Match(ev) {
			continue
		}
		select {
		case sub.ch <- ev:
		default:
			// Subscriber is slow — cancel their context so the WS handler
			// closes with StatusPolicyViolation (1008). We do NOT close
			// sub.ch here; the handler goroutine will drain its own
			// resources.
			sub.markSlow()
		}
	}
}

// Emit implements the Emitter contract for a (bus, shard) pair. Creates
// the closure only once per connection so the hot path is allocation-free.
type busEmitter struct {
	bus   *Bus
	shard *Shard
}

func (e *busEmitter) Shard() *Shard { return e.shard }

func (e *busEmitter) Emit(eventType string, data any) {
	e.bus.Publish(e.shard, eventType, data)
}

func (e *busEmitter) EmitAbsorb(remote uint64, eventType string, data any) {
	if e.bus.closed.Load() {
		return
	}
	ev := Event{
		ShardID: e.shard.ID(),
		Lamport: e.shard.Absorb(remote),
		TsNs:    time.Now().UnixNano(),
		Type:    eventType,
		Data:    data,
	}
	e.bus.publishPreTicked(ev)
}

// NewEmitter returns an Emitter bound to the given shard. Every connection
// handler creates one at accept time.
func (b *Bus) NewEmitter(shard *Shard) Emitter {
	return &busEmitter{bus: b, shard: shard}
}

// PublishListener emits an engine/daemon-level event on the reserved
// shard 0 — for things like config reloads that aren't tied to any single
// connection. No-op when the bus is nil so callers don't need to null-check.
func (b *Bus) PublishListener(eventType string, data any) {
	if b == nil {
		return
	}
	b.Publish(b.listenerShard, eventType, data)
}

// Subscription is an open subscriber's view of the bus. It owns a buffered
// channel and a context; cancelling the context tears down the subscription.
type Subscription struct {
	id     uint64
	bus    *Bus
	filter Filter
	ch     chan Event
	ctx    context.Context
	cancel context.CancelFunc

	slow atomic.Bool // set when dropped due to slow consumer
}

// Ch returns the delivery channel. Closed after the subscription is
// unsubscribed or the bus closes — range-loop friendly.
func (s *Subscription) Ch() <-chan Event { return s.ch }

// Context returns the subscription's cancellation context. WS handlers
// should select on this to detect slow-consumer eviction and bus shutdown.
func (s *Subscription) Context() context.Context { return s.ctx }

// Slow reports whether the subscription was dropped for exceeding the
// buffered-channel depth. The WS handler uses this to pick a close code.
func (s *Subscription) Slow() bool { return s.slow.Load() }

// Unsubscribe removes the subscription from the bus and closes its channel.
// Safe to call multiple times.
func (s *Subscription) Unsubscribe() {
	s.bus.removeSub(s.id)
}

// markSlow is called by the bus on buffer overflow.
func (s *Subscription) markSlow() {
	s.slow.Store(true)
	s.cancel()
}

// Subscribe registers a new subscription against the given filter and
// returns it. The caller must eventually call Unsubscribe (typically via
// defer in the WS handler) to free bus resources.
//
// Replay is NOT performed here — callers should first call DrainReplay to
// push historical events into the WebSocket, then range over Ch() for live
// updates. Splitting the two phases keeps the bus mutex-critical section
// short.
func (b *Bus) Subscribe(filter Filter) *Subscription {
	id := b.nextID.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	sub := &Subscription{
		id:     id,
		bus:    b,
		filter: filter,
		ch:     make(chan Event, b.cfg.SubBufferSize),
		ctx:    ctx,
		cancel: cancel,
	}
	b.mu.Lock()
	b.subs[id] = sub
	b.mu.Unlock()
	return sub
}

// removeSub drops a subscription. Idempotent.
func (b *Bus) removeSub(id uint64) {
	b.mu.Lock()
	sub, ok := b.subs[id]
	if ok {
		delete(b.subs, id)
	}
	b.mu.Unlock()
	if ok {
		sub.cancel()
		close(sub.ch)
	}
}

// DrainReplay produces historical events from per-shard ring buffers. For
// each shard mentioned in filter.Since, the bus walks that ring from
// lamport > since[shard] and yields matching events in Lamport order. If
// the since cursor precedes the ring's oldest retained Lamport, a
// replay.gap event is yielded first for that shard.
//
// This is a one-shot drain: after it returns, the caller should switch to
// live streaming via sub.Ch(). Because ring appends happen concurrently,
// the last few events of each shard may also appear on sub.Ch() — that's
// fine, subscribers are expected to deduplicate by (shard_id, lamport).
func (b *Bus) DrainReplay(sub *Subscription, yield func(Event) error) error {
	for shardID, since := range sub.filter.Since {
		r, ok := b.rings.Load(shardID)
		if !ok {
			// Unknown shard — emit a gap so the subscriber knows we have
			// no history for it.
			ev := Event{
				ShardID: shardID,
				Lamport: 0,
				TsNs:    time.Now().UnixNano(),
				Type:    TypeReplayGap,
				Data:    ReplayGapData{ShardID: shardID, OldestLamport: 0},
			}
			if err := yield(ev); err != nil {
				return err
			}
			continue
		}
		ring := r.(*Ring)
		missed, ok := ring.Since(since)
		if !ok {
			gap := Event{
				ShardID: shardID,
				Lamport: 0,
				TsNs:    time.Now().UnixNano(),
				Type:    TypeReplayGap,
				Data:    ReplayGapData{ShardID: shardID, OldestLamport: ring.OldestLamport()},
			}
			if err := yield(gap); err != nil {
				return err
			}
		}
		for _, ev := range missed {
			if !sub.filter.Match(ev) {
				continue
			}
			if err := yield(ev); err != nil {
				return err
			}
		}
	}
	return nil
}

// RegisterMeter adds a ConnMeter to the scanner's scan set. Called from
// the listener accept path once a conn_id has been assigned.
func (b *Bus) RegisterMeter(m *ConnMeter) {
	b.meters.Store(m.ConnID, m)
}

// UnregisterMeter removes a ConnMeter. Called from the listener's defer
// path when the handler goroutine returns. Returns the final rx/tx totals
// so the caller can include them in the connection.closed event.
func (b *Bus) UnregisterMeter(connID string) (rxTotal, txTotal uint64) {
	v, ok := b.meters.LoadAndDelete(connID)
	if !ok {
		return 0, 0
	}
	m := v.(*ConnMeter)
	return m.rx.Load(), m.tx.Load()
}

// scannerLoop is the single global goroutine that emits connection.bytes
// events. Runs until Bus.Close. O(N) per tick where N = active connections;
// cheaper than per-connection tickers for any N > 1.
func (b *Bus) scannerLoop() {
	defer close(b.scannerDone)
	tick := time.NewTicker(b.cfg.MeterInterval)
	defer tick.Stop()

	for {
		select {
		case <-b.scannerCtx.Done():
			return
		case now := <-tick.C:
			b.scanOnce(now)
		}
	}
}

// scanOnce walks all registered meters and emits a connection.bytes event
// for any whose rx or tx delta is nonzero. Idle connections stay silent.
func (b *Bus) scanOnce(now time.Time) {
	b.meters.Range(func(_, v any) bool {
		m := v.(*ConnMeter)
		rx := m.rx.Load()
		tx := m.tx.Load()
		rxDelta := rx - m.lastRx
		txDelta := tx - m.lastTx
		if rxDelta == 0 && txDelta == 0 {
			return true
		}
		intervalNs := now.Sub(m.lastTickAt).Nanoseconds()
		m.lastRx = rx
		m.lastTx = tx
		m.lastTickAt = now

		b.Publish(m.Shard, TypeConnectionBytes, ConnectionBytesData{
			ConnID:     m.ConnID,
			RxDelta:    rxDelta,
			TxDelta:    txDelta,
			RxTotal:    rx,
			TxTotal:    tx,
			IntervalNs: intervalNs,
		})
		return true
	})
}

// Close stops the scanner, cancels all active subscriptions, and marks the
// bus closed so further publishes become no-ops. Safe to call multiple
// times.
func (b *Bus) Close() {
	if !b.closed.CompareAndSwap(false, true) {
		return
	}
	b.scannerCancel()
	<-b.scannerDone

	b.mu.Lock()
	subs := make([]*Subscription, 0, len(b.subs))
	for _, s := range b.subs {
		subs = append(subs, s)
	}
	b.subs = nil
	b.mu.Unlock()

	for _, s := range subs {
		s.cancel()
		close(s.ch)
	}
	log.Printf("events: bus closed (released %d subscriptions)", len(subs))
}
