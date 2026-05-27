// Package events provides a pub-sub event bus with per-connection Lamport
// clocks for real-time lifecycle and bandwidth reporting to frontends.
//
// The bus streams typed events to WebSocket subscribers. Each emitting
// connection owns a *Shard with its own atomic Lamport counter so the hot
// path (many concurrent handler goroutines) is contention-free. Events
// carry (shard_id, lamport) so subscribers can reconstruct causal order
// without the bus ever touching a global atomic.
package events

import (
	"context"
)

// Event is the wire-level envelope delivered to every subscriber.
//
// Data is held as `any` so publishers allocate the payload once and the bus
// stores it in its per-shard ring buffer without re-marshaling. JSON
// marshaling happens exactly once per WebSocket write.
type Event struct {
	ShardID uint64 `json:"shard_id"`
	Lamport uint64 `json:"lamport"`
	TsNs    int64  `json:"ts_ns"`
	Type    string `json:"type"`
	Data    any    `json:"data,omitempty"`
}

// Event type constants. Dotted hierarchy lets subscribers filter with
// trailing-`*` prefix patterns (e.g. "hop.*", "connection.*").
const (
	TypeConnectionOpened      = "connection.opened"
	TypeConnectionDialing     = "connection.dialing"
	TypeHopDialing            = "hop.dialing"
	TypeHopConnected          = "hop.connected"
	TypeHopError              = "hop.error"
	TypeConnectionEstablished = "connection.established"
	TypeConnectionBytes       = "connection.bytes"
	TypeConnectionClosed      = "connection.closed"
	TypeRuleMatched           = "rule.matched"
	TypeRuleDirect            = "rule.direct"
	TypeRuleBlocked           = "rule.blocked"

	// Engine/daemon-level events. Emitted on shard 0 via Bus.PublishListener.
	TypeConfigReloaded     = "config.reloaded"
	TypeConfigReloadFailed = "config.reload_failed"
	TypeLogLine            = "log.line"

	// TypeReplayGap is emitted once when a subscriber's Since cursor points
	// further back than the ring buffer retains. The subscriber should treat
	// it as a signal that history is incomplete and re-sync UI state.
	TypeReplayGap = "replay.gap"
)

// ListenerInfo identifies the listener that accepted a connection.
type ListenerInfo struct {
	Protocol string `json:"protocol"`
	Addr     string `json:"addr"`
}

// HopInfo describes one node in a proxy chain.
type HopInfo struct {
	Index    int    `json:"index"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
}

// ConnectionOpenedData is emitted when a listener accepts a new client.
type ConnectionOpenedData struct {
	ConnID     string       `json:"conn_id"`
	Listener   ListenerInfo `json:"listener"`
	ClientAddr string       `json:"client_addr"`
	ChainName  string       `json:"chain_name,omitempty"`
}

// ConnectionDialingData is emitted before the chain dial begins.
type ConnectionDialingData struct {
	ConnID      string    `json:"conn_id"`
	Target      string    `json:"target"`
	TargetHost  string    `json:"target_host,omitempty"`
	TargetPort  string    `json:"target_port,omitempty"`
	Network     string    `json:"network,omitempty"`
	Application string    `json:"application,omitempty"`
	RuleName    string    `json:"rule_name,omitempty"`
	RuleAction  string    `json:"rule_action,omitempty"`
	ChainName   string    `json:"chain_name,omitempty"`
	DecisionNs  int64     `json:"decision_ns,omitempty"`
	Hops        []HopInfo `json:"hops"`
}

// RuleDecisionData records the routing decision made for a connection.
type RuleDecisionData struct {
	ConnID     string `json:"conn_id"`
	RuleName   string `json:"rule_name,omitempty"`
	Action     string `json:"action"`
	ChainName  string `json:"chain_name,omitempty"`
	Target     string `json:"target"`
	TargetHost string `json:"target_host,omitempty"`
	TargetPort string `json:"target_port,omitempty"`
	Network    string `json:"network,omitempty"`
	ElapsedNs  int64  `json:"elapsed_ns,omitempty"`
}

// HopDialingData is emitted per hop as the chain dial progresses.
type HopDialingData struct {
	ConnID   string `json:"conn_id"`
	HopIndex int    `json:"hop_index"`
	HopName  string `json:"hop_name"`
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
}

// HopConnectedData records a successful hop dial and elapsed time.
type HopConnectedData struct {
	ConnID    string `json:"conn_id"`
	HopIndex  int    `json:"hop_index"`
	ElapsedNs int64  `json:"elapsed_ns"`
}

// HopErrorData records a hop-dial failure.
type HopErrorData struct {
	ConnID   string `json:"conn_id"`
	HopIndex int    `json:"hop_index"`
	Error    string `json:"error"`
}

// ConnectionEstablishedData is emitted after the end-to-end chain is
// up and the client has been notified.
type ConnectionEstablishedData struct {
	ConnID      string `json:"conn_id"`
	TotalDialNs int64  `json:"total_dial_ns"`
}

// ConnectionBytesData is emitted periodically while the connection is live.
// Deltas are per-tick; totals are lifetime-to-date.
type ConnectionBytesData struct {
	ConnID     string `json:"conn_id"`
	RxDelta    uint64 `json:"rx_delta"`
	TxDelta    uint64 `json:"tx_delta"`
	RxTotal    uint64 `json:"rx_total"`
	TxTotal    uint64 `json:"tx_total"`
	IntervalNs int64  `json:"interval_ns"`
}

// ConnectionClosedData is emitted when the handler goroutine returns.
type ConnectionClosedData struct {
	ConnID     string `json:"conn_id"`
	Reason     string `json:"reason"`
	DurationNs int64  `json:"duration_ns"`
	RxTotal    uint64 `json:"rx_total"`
	TxTotal    uint64 `json:"tx_total"`
}

// ReplayGapData signals a since-cursor that precedes the ring buffer.
type ReplayGapData struct {
	ShardID       uint64 `json:"shard_id"`
	OldestLamport uint64 `json:"oldest_lamport"`
}

// ConfigReloadedData is emitted after the daemon successfully reloads a
// config file from disk (file watcher or explicit trigger).
type ConfigReloadedData struct {
	Path string `json:"path"`
}

// ConfigReloadFailedData is emitted when a config reload attempt fails —
// either parse error from disk or the engine rejected the new config.
type ConfigReloadFailedData struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// LogLineData carries one newline-delimited daemon log line.
type LogLineData struct {
	Line string `json:"line"`
}

// Close reasons used by the listener teardown path.
const (
	ReasonClientEOF = "client_eof"
	ReasonRemoteEOF = "remote_eof"
	ReasonError     = "error"
	ReasonShutdown  = "shutdown"
)

// Emitter is the interface publishers see. A handler goroutine gets an
// Emitter at accept time and calls Emit for each event on its shard.
//
// The chain package reads an Emitter from the request context via
// EmitterFrom — it never touches the Bus directly.
type Emitter interface {
	// Shard returns the shard this emitter writes to.
	Shard() *Shard

	// Emit publishes an event on the emitter's shard. The bus fills in
	// ShardID, Lamport, and TsNs; the caller provides Type and Data.
	Emit(eventType string, data any)

	// EmitAbsorb is like Emit but first merges a remote Lamport value into
	// the shard (max(local,remote)+1) to preserve a cross-shard causal
	// edge. Use when an event depends on something that happened elsewhere.
	EmitAbsorb(remoteLamport uint64, eventType string, data any)
}

// Filter selects a subset of events for a subscriber.
type Filter struct {
	// Types is a comma-expanded list of exact event types or trailing-*
	// prefix patterns (e.g., "hop.*"). Empty means no filtering by type.
	Types []string

	// ConnIDs scopes to events whose Data carries one of the listed
	// connection IDs. Empty means all connections. The bus uses best-effort
	// extraction via a type switch on known payload structs; unknown
	// payload types bypass the filter and are delivered.
	ConnIDs []string

	// Since[shard_id] = last lamport seen for that shard. On subscribe,
	// the bus replays each shard's ring from lamport > Since[shard_id],
	// then joins the live stream. Empty means no replay, live-only.
	Since map[uint64]uint64
}

// matchType reports whether a given event type satisfies the filter's type
// list. Empty list matches everything.
func (f Filter) matchType(eventType string) bool {
	if len(f.Types) == 0 {
		return true
	}
	for _, pat := range f.Types {
		if pat == eventType {
			return true
		}
		// Trailing-* wildcard: "hop.*" matches "hop.dialing".
		if n := len(pat); n > 0 && pat[n-1] == '*' {
			prefix := pat[:n-1]
			if len(eventType) >= len(prefix) && eventType[:len(prefix)] == prefix {
				return true
			}
		}
	}
	return false
}

// matchConnID checks whether the event's payload (if any) references one of
// the scoped connection IDs. Events without a conn_id (listener/engine-level)
// pass through when ConnIDs is empty; otherwise they are filtered out unless
// the caller explicitly asked for shard 0 replay.
func (f Filter) matchConnID(ev Event) bool {
	if len(f.ConnIDs) == 0 {
		return true
	}
	id := extractConnID(ev.Data)
	if id == "" {
		return false
	}
	for _, want := range f.ConnIDs {
		if want == id {
			return true
		}
	}
	return false
}

// Match is the composite predicate used by the bus fan-out path.
func (f Filter) Match(ev Event) bool {
	return f.matchType(ev.Type) && f.matchConnID(ev)
}

// extractConnID pulls a conn_id from a known payload struct. Returns "" if
// the payload type isn't recognized or has no conn_id.
func extractConnID(data any) string {
	switch d := data.(type) {
	case ConnectionOpenedData:
		return d.ConnID
	case ConnectionDialingData:
		return d.ConnID
	case HopDialingData:
		return d.ConnID
	case HopConnectedData:
		return d.ConnID
	case HopErrorData:
		return d.ConnID
	case ConnectionEstablishedData:
		return d.ConnID
	case ConnectionBytesData:
		return d.ConnID
	case ConnectionClosedData:
		return d.ConnID
	case RuleDecisionData:
		return d.ConnID
	}
	return ""
}

// ctxKey is a package-private type so ctx values can't collide with keys
// from other packages.
type ctxKey int

const (
	emitterKey ctxKey = iota
	connIDKey
)

// WithEmitter stores an Emitter on the context so downstream code (chain,
// protocol dialers) can emit events without holding a direct Bus reference.
func WithEmitter(ctx context.Context, e Emitter) context.Context {
	return context.WithValue(ctx, emitterKey, e)
}

// EmitterFrom retrieves the Emitter from a context. Returns (nil, false)
// when no emitter is attached — callers should treat that as "events
// disabled" and simply not emit.
func EmitterFrom(ctx context.Context) (Emitter, bool) {
	e, ok := ctx.Value(emitterKey).(Emitter)
	return e, ok
}

// WithConnID stores the connection ID on the context for downstream use
// (chain hop emits need conn_id in their payload).
func WithConnID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, connIDKey, id)
}

// ConnIDFrom retrieves the connection ID. Returns "" when none is set.
func ConnIDFrom(ctx context.Context) string {
	if id, ok := ctx.Value(connIDKey).(string); ok {
		return id
	}
	return ""
}
