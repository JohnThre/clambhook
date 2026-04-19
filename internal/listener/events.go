package listener

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/clambhook/clambhook/internal/events"
	"github.com/google/uuid"
)

// connEvents bundles the per-connection event plumbing a listener needs.
// Listeners create one at accept time and thread it through the handler.
// When Bus is nil (tests without wiring), every method is a cheap no-op so
// existing unit tests don't require the events package.
type connEvents struct {
	bus       *events.Bus
	shard     *events.Shard
	meter     *events.ConnMeter
	emitter   events.Emitter
	connID    string
	startedAt time.Time
	dialStart time.Time

	// capture data needed by the connection.opened event so we can emit
	// once after allocation rather than plumbing fields through.
	listenerInfo events.ListenerInfo
	clientAddr   string
	chainName    string
}

// newConnEvents allocates a connection's event context: a unique ID, a
// Lamport shard, a byte meter. Registers the meter with the bus so the
// scanner includes it in periodic bandwidth emits. Returns nil when bus is
// nil — callers check for nil before invoking methods.
func newConnEvents(bus *events.Bus, li events.ListenerInfo, clientAddr, chainName string) *connEvents {
	if bus == nil {
		return nil
	}
	shard := bus.NewShard()
	connID := uuid.NewString()
	meter := events.NewConnMeter(connID, shard)
	bus.RegisterMeter(meter)

	return &connEvents{
		bus:          bus,
		shard:        shard,
		meter:        meter,
		emitter:      bus.NewEmitter(shard),
		connID:       connID,
		startedAt:    time.Now(),
		listenerInfo: li,
		clientAddr:   clientAddr,
		chainName:    chainName,
	}
}

// attach returns a context carrying this connection's emitter and ID so
// downstream code (chain.Dial, protocol dialers) can emit without holding
// a direct reference.
func (c *connEvents) attach(ctx context.Context) context.Context {
	if c == nil {
		return ctx
	}
	ctx = events.WithEmitter(ctx, c.emitter)
	ctx = events.WithConnID(ctx, c.connID)
	return ctx
}

// emitOpened fires connection.opened once the handler goroutine is up.
func (c *connEvents) emitOpened() {
	if c == nil {
		return
	}
	c.emitter.Emit(events.TypeConnectionOpened, events.ConnectionOpenedData{
		ConnID:     c.connID,
		Listener:   c.listenerInfo,
		ClientAddr: c.clientAddr,
		ChainName:  c.chainName,
	})
}

// emitDialing fires connection.dialing just before the chain dial begins.
// hops describes every node in the chain so subscribers see the full shape
// even if a mid-hop fails.
func (c *connEvents) emitDialing(target string, hops []events.HopInfo) {
	if c == nil {
		return
	}
	c.dialStart = time.Now()
	c.emitter.Emit(events.TypeConnectionDialing, events.ConnectionDialingData{
		ConnID: c.connID,
		Target: target,
		Hops:   hops,
	})
}

// emitEstablished fires connection.established after the client has been
// told the chain is up (SOCKS5 reply success / HTTP 200 Connection
// established). TotalDialNs is measured from the dialing emit.
func (c *connEvents) emitEstablished() {
	if c == nil {
		return
	}
	c.emitter.Emit(events.TypeConnectionEstablished, events.ConnectionEstablishedData{
		ConnID:      c.connID,
		TotalDialNs: time.Since(c.dialStart).Nanoseconds(),
	})
}

// emitClosed unregisters the meter and fires connection.closed with the
// final byte totals. reason is one of events.Reason*.
func (c *connEvents) emitClosed(reason string) {
	if c == nil {
		return
	}
	rx, tx := c.bus.UnregisterMeter(c.connID)
	c.emitter.Emit(events.TypeConnectionClosed, events.ConnectionClosedData{
		ConnID:     c.connID,
		Reason:     reason,
		DurationNs: time.Since(c.startedAt).Nanoseconds(),
		RxTotal:    rx,
		TxTotal:    tx,
	})
}

// rxCounter returns the rx atomic counter for wrapping in a MeteredReader,
// or nil when events are disabled (nil bus). Nil-safe via MeteredReader's
// own nil-counter guard.
func (c *connEvents) rxCounter() *atomic.Uint64 {
	if c == nil {
		return nil
	}
	return c.meter.Rx()
}

// txCounter returns the tx atomic counter for the opposite direction.
func (c *connEvents) txCounter() *atomic.Uint64 {
	if c == nil {
		return nil
	}
	return c.meter.Tx()
}

// classifyClose picks a close reason from the listener-visible signals:
//
//   - ctx cancelled → shutdown (engine tearing down)
//   - relay returned non-nil err → error
//   - otherwise → client_eof (normal end of transfer)
//
// We can't cheaply distinguish client_eof from remote_eof without threading
// direction back from the relay goroutines; in practice frontends display
// both identically so the distinction isn't worth the complexity today.
func classifyClose(ctx context.Context, relayErr error) string {
	if ctx.Err() != nil {
		return events.ReasonShutdown
	}
	if relayErr != nil {
		return events.ReasonError
	}
	return events.ReasonClientEOF
}
