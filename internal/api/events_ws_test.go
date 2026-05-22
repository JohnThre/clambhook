package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/events"
	"github.com/coder/websocket"
)

// mountEventsServer starts an httptest.Server whose single route is
// /api/v1/events bound to the given bus.
func mountEventsServer(t *testing.T, bus *events.Bus) (wsURL string) {
	t.Helper()
	s := &Server{bus: bus}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/events", s.handleEvents)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/events"
}

func dialWS(t *testing.T, url string) (*websocket.Conn, context.Context, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		cancel()
		t.Fatalf("ws dial: %v", err)
	}
	return c, ctx, cancel
}

func readOneEvent(t *testing.T, ctx context.Context, c *websocket.Conn) events.Event {
	t.Helper()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	var ev events.Event
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return ev
}

func TestEventsWSLiveStream(t *testing.T) {
	bus := events.NewBus(events.Config{
		SubBufferSize: 64,
		RingCapacity:  64,
		MeterInterval: time.Hour, // scanner idle — keeps the test deterministic
	})
	defer bus.Close()

	url := mountEventsServer(t, bus)
	c, ctx, cancel := dialWS(t, url)
	defer cancel()
	defer c.CloseNow()

	// Wait briefly for Subscribe to register before publishing.
	time.Sleep(30 * time.Millisecond)

	shard := bus.NewShard()
	bus.Publish(shard, events.TypeConnectionOpened, events.ConnectionOpenedData{
		ConnID:     "c1",
		ClientAddr: "127.0.0.1:1234",
	})

	ev := readOneEvent(t, ctx, c)
	if ev.Type != events.TypeConnectionOpened {
		t.Fatalf("got type %q want %q", ev.Type, events.TypeConnectionOpened)
	}
	if ev.ShardID != shard.ID() {
		t.Fatalf("shard_id = %d want %d", ev.ShardID, shard.ID())
	}
	if ev.Lamport != 1 {
		t.Fatalf("lamport = %d want 1", ev.Lamport)
	}
	_ = c.Close(websocket.StatusNormalClosure, "")
}

func TestEventsWSReplayThenLive(t *testing.T) {
	bus := events.NewBus(events.Config{
		SubBufferSize: 64,
		RingCapacity:  64,
		MeterInterval: time.Hour,
	})
	defer bus.Close()

	shard := bus.NewShard()
	for i := 0; i < 3; i++ {
		bus.Publish(shard, "seq", events.ConnectionOpenedData{ConnID: "c1"})
	}

	url := mountEventsServer(t, bus) + "?since=" +
		strconv.FormatUint(shard.ID(), 10) + ":1"
	c, ctx, cancel := dialWS(t, url)
	defer cancel()
	defer c.CloseNow()

	// Replay delivers lamports 2, 3.
	got1 := readOneEvent(t, ctx, c)
	got2 := readOneEvent(t, ctx, c)
	if got1.Lamport != 2 || got2.Lamport != 3 {
		t.Fatalf("replay lamports = %d,%d want 2,3", got1.Lamport, got2.Lamport)
	}

	bus.Publish(shard, "seq", events.ConnectionOpenedData{ConnID: "c1"})
	live := readOneEvent(t, ctx, c)
	if live.Lamport != 4 {
		t.Fatalf("live lamport = %d want 4", live.Lamport)
	}
	_ = c.Close(websocket.StatusNormalClosure, "")
}

func TestEventsWSTypeFilter(t *testing.T) {
	bus := events.NewBus(events.Config{
		SubBufferSize: 64,
		RingCapacity:  64,
		MeterInterval: time.Hour,
	})
	defer bus.Close()

	url := mountEventsServer(t, bus) + "?types=hop.*"
	c, ctx, cancel := dialWS(t, url)
	defer cancel()
	defer c.CloseNow()

	time.Sleep(30 * time.Millisecond)

	shard := bus.NewShard()
	// This event is filtered out.
	bus.Publish(shard, events.TypeConnectionOpened, events.ConnectionOpenedData{ConnID: "c1"})
	// This event matches hop.*.
	bus.Publish(shard, events.TypeHopDialing, events.HopDialingData{ConnID: "c1", HopIndex: 0})

	ev := readOneEvent(t, ctx, c)
	if ev.Type != events.TypeHopDialing {
		t.Fatalf("got type %q want hop.dialing", ev.Type)
	}
	_ = c.Close(websocket.StatusNormalClosure, "")
}

func TestParseEventsFilter(t *testing.T) {
	cases := []struct {
		name  string
		query map[string][]string
		want  events.Filter
		err   bool
	}{
		{
			name:  "empty",
			query: map[string][]string{},
			want:  events.Filter{},
		},
		{
			name:  "types with wildcard",
			query: map[string][]string{"types": {"hop.*,connection.opened"}},
			want:  events.Filter{Types: []string{"hop.*", "connection.opened"}},
		},
		{
			name: "conn_id repeatable",
			query: map[string][]string{
				"conn_id": {"a,b", "c"},
			},
			want: events.Filter{ConnIDs: []string{"a", "b", "c"}},
		},
		{
			name:  "since parse",
			query: map[string][]string{"since": {"1:42,3:7"}},
			want:  events.Filter{Since: map[uint64]uint64{1: 42, 3: 7}},
		},
		{
			name:  "malformed since",
			query: map[string][]string{"since": {"not-a-number"}},
			err:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseEventsFilter(tc.query)
			if tc.err {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if !filterEqual(got, tc.want) {
				t.Fatalf("filter mismatch:\n got=%+v\nwant=%+v", got, tc.want)
			}
		})
	}
}

func filterEqual(a, b events.Filter) bool {
	if !strSliceEqual(a.Types, b.Types) {
		return false
	}
	if !strSliceEqual(a.ConnIDs, b.ConnIDs) {
		return false
	}
	if len(a.Since) != len(b.Since) {
		return false
	}
	for k, v := range a.Since {
		if b.Since[k] != v {
			return false
		}
	}
	return true
}

func strSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
