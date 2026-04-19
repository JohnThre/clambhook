package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/clambhook/clambhook/internal/events"
	"github.com/coder/websocket"
)

const (
	// wsPingInterval is how often the server sends WS control pings to
	// keep the connection alive and detect dead peers.
	wsPingInterval = 30 * time.Second

	// wsWriteTimeout bounds each frame write so a stuck peer can't pin a
	// goroutine forever.
	wsWriteTimeout = 10 * time.Second
)

// handleEvents is the /api/v1/events WebSocket endpoint. Subscribers connect
// with optional query params `types`, `conn_id`, `since`; the server drains
// any ring-buffered history first, then live-streams events until either
// side closes or the bus shuts down.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if s.bus == nil {
		http.Error(w, "events disabled", http.StatusNotImplemented)
		return
	}

	filter, err := parseEventsFilter(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// InsecureSkipVerify disables the same-origin check. The daemon binds
	// to 127.0.0.1 so any caller is already local; skipping origin lets
	// browser-based TUIs connect without a preflight dance.
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("api: ws accept: %v", err)
		return
	}
	defer c.CloseNow()

	sub := s.bus.Subscribe(filter)
	defer sub.Unsubscribe()

	// One cancel covers client-side close (CloseRead), slow-consumer
	// eviction (sub.Context), bus shutdown, and our own write errors.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// CloseRead drains the peer's read side in a background goroutine so we
	// notice a client close promptly — without it, the handler only wakes
	// up on the next periodic ping failure.
	ctx = c.CloseRead(ctx)

	// Pump replay events first. If the peer closes mid-drain the write
	// fails and we bail out; that's the desired behavior.
	if err := s.bus.DrainReplay(sub, func(ev events.Event) error {
		return writeEvent(ctx, c, ev)
	}); err != nil {
		return
	}

	// Ping loop runs concurrently with the live fan-out so a silent
	// connection (no events, no client pings) still gets a keepalive.
	pingCtx, cancelPing := context.WithCancel(ctx)
	defer cancelPing()
	go pingLoop(pingCtx, c)

	// Live stream.
	for {
		select {
		case ev, ok := <-sub.Ch():
			if !ok {
				// Bus closed — send StatusGoingAway and return.
				_ = c.Close(websocket.StatusGoingAway, "bus closed")
				return
			}
			if err := writeEvent(ctx, c, ev); err != nil {
				return
			}
		case <-sub.Context().Done():
			// Slow-consumer eviction. Close with PolicyViolation so the
			// client knows it can reconnect with a since cursor.
			_ = c.Close(websocket.StatusPolicyViolation, "slow consumer")
			return
		case <-ctx.Done():
			// Client went away or server is shutting down.
			return
		}
	}
}

// writeEvent serializes ev as JSON and sends it as a single text frame.
// Uses a short write timeout so a stuck peer can't pin this goroutine.
func writeEvent(ctx context.Context, c *websocket.Conn, ev events.Event) error {
	buf, err := json.Marshal(ev)
	if err != nil {
		// Should be impossible — all payloads are plain structs. Log and
		// drop the event so one malformed payload doesn't kill the stream.
		log.Printf("api: marshal event: %v", err)
		return nil
	}
	wctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer cancel()
	return c.Write(wctx, websocket.MessageText, buf)
}

// pingLoop keeps the connection alive. Returns when ctx is cancelled or a
// ping fails. We don't need to close the conn on failure — the main
// handler's write loop will notice the broken pipe on the next event.
func pingLoop(ctx context.Context, c *websocket.Conn) {
	t := time.NewTicker(wsPingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// parseEventsFilter builds a Filter from URL query params.
//
// Syntax:
//
//	types=connection.*,hop.connected   comma-separated, trailing-* wildcard
//	conn_id=abc-123                    repeatable; matches any payload conn_id
//	since=1:42,3:7                     per-shard last-lamport for replay
//
// Unknown params are ignored so clients can supply hints we don't yet
// understand without triggering 400.
func parseEventsFilter(q map[string][]string) (events.Filter, error) {
	f := events.Filter{}

	if raw := firstQuery(q, "types"); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				f.Types = append(f.Types, t)
			}
		}
	}

	// conn_id is repeatable — both ?conn_id=a&conn_id=b and
	// ?conn_id=a,b are accepted.
	if vals, ok := q["conn_id"]; ok {
		for _, v := range vals {
			for _, id := range strings.Split(v, ",") {
				id = strings.TrimSpace(id)
				if id != "" {
					f.ConnIDs = append(f.ConnIDs, id)
				}
			}
		}
	}

	if raw := firstQuery(q, "since"); raw != "" {
		since := make(map[uint64]uint64)
		for _, pair := range strings.Split(raw, ",") {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			parts := strings.SplitN(pair, ":", 2)
			if len(parts) != 2 {
				return f, badFilterErr("since", pair, "expected shard:lamport")
			}
			shard, err := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 64)
			if err != nil {
				return f, badFilterErr("since", pair, "shard must be uint64")
			}
			lam, err := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 64)
			if err != nil {
				return f, badFilterErr("since", pair, "lamport must be uint64")
			}
			since[shard] = lam
		}
		if len(since) > 0 {
			f.Since = since
		}
	}

	return f, nil
}

func firstQuery(q map[string][]string, key string) string {
	if v, ok := q[key]; ok && len(v) > 0 {
		return v[0]
	}
	return ""
}

type filterError struct {
	field, value, reason string
}

func (e *filterError) Error() string {
	return "invalid " + e.field + " value " + strconv.Quote(e.value) + ": " + e.reason
}

func badFilterErr(field, value, reason string) error {
	return &filterError{field: field, value: value, reason: reason}
}
