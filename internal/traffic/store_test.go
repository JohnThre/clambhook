package traffic

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/events"
	"github.com/JohnThre/clambhook/internal/geo"
)

func TestStoreAggregatesAndPersistsClosedHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic-history.json")
	store, err := NewStore(config.TrafficConfig{
		Enabled:       true,
		HistoryLimit:  10,
		HistoryMaxAge: config.Duration(time.Hour),
		HistoryPath:   path,
	}, func(address string) (*geo.Location, error) {
		return &geo.Location{Country: "United States", CountryCode: "US", City: "New York"}, nil
	})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	store.ApplyEvent(events.Event{TsNs: 1, Type: events.TypeConnectionOpened, Data: events.ConnectionOpenedData{
		ConnID:     "c1",
		Listener:   events.ListenerInfo{Protocol: "socks5", Addr: "127.0.0.1:1080"},
		ClientAddr: "127.0.0.1:50000",
		ChainName:  "default",
	}})
	store.ApplyEvent(events.Event{TsNs: 2, Type: events.TypeConnectionDialing, Data: events.ConnectionDialingData{
		ConnID:     "c1",
		Target:     "example.com:443",
		TargetHost: "example.com",
		TargetPort: "443",
		Hops: []events.HopInfo{{
			Index:    0,
			Name:     "exit",
			Protocol: "trojan",
			Address:  "proxy.example:443",
		}},
	}})
	store.ApplyEvent(events.Event{TsNs: 3, Type: events.TypeHopConnected, Data: events.HopConnectedData{
		ConnID:    "c1",
		HopIndex:  0,
		ElapsedNs: int64(50 * time.Millisecond),
	}})
	store.ApplyEvent(events.Event{TsNs: 4, Type: events.TypeConnectionEstablished, Data: events.ConnectionEstablishedData{
		ConnID:      "c1",
		TotalDialNs: int64(60 * time.Millisecond),
	}})
	store.ApplyEvent(events.Event{TsNs: 5, Type: events.TypeConnectionBytes, Data: events.ConnectionBytesData{
		ConnID:     "c1",
		RxDelta:    2048,
		TxDelta:    1024,
		RxTotal:    2048,
		TxTotal:    1024,
		IntervalNs: int64(time.Second),
	}})

	live := store.Snapshot("active", 0)
	if got := len(live.Connections); got != 1 {
		t.Fatalf("active connections = %d, want 1", got)
	}
	conn := live.Connections[0]
	if conn.Application != "HTTPS" || conn.RxBps != 2048 || conn.TxBps != 1024 {
		t.Fatalf("live connection = %+v", conn)
	}
	if conn.Geo.CountryCode != "US" {
		t.Fatalf("geo = %+v", conn.Geo)
	}

	store.ApplyEvent(events.Event{TsNs: time.Now().UnixNano(), Type: events.TypeConnectionClosed, Data: events.ConnectionClosedData{
		ConnID:     "c1",
		Reason:     events.ReasonClientEOF,
		DurationNs: int64(2 * time.Second),
		RxTotal:    2048,
		TxTotal:    1024,
	}})

	closed := store.Snapshot("closed", 0)
	if got := len(closed.Connections); got != 1 {
		t.Fatalf("closed connections = %d, want 1", got)
	}
	if closed.Connections[0].State != StateClosed || closed.Connections[0].CloseReason != events.ReasonClientEOF {
		t.Fatalf("closed connection = %+v", closed.Connections[0])
	}

	reloaded, err := NewStore(config.TrafficConfig{
		Enabled:       true,
		HistoryLimit:  10,
		HistoryMaxAge: config.Duration(time.Hour),
		HistoryPath:   path,
	}, nil)
	if err != nil {
		t.Fatalf("reload NewStore: %v", err)
	}
	if got := len(reloaded.Snapshot("closed", 0).Connections); got != 1 {
		t.Fatalf("reloaded closed connections = %d, want 1", got)
	}
}

func TestStoreReconfigureDisabledStopsRecording(t *testing.T) {
	store, err := NewStore(config.TrafficConfig{
		Enabled:       true,
		HistoryLimit:  10,
		HistoryMaxAge: config.Duration(time.Hour),
		HistoryPath:   filepath.Join(t.TempDir(), "traffic-history.json"),
	}, nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Reconfigure(config.TrafficConfig{Enabled: false, HistoryLimit: 10, HistoryMaxAge: config.Duration(time.Hour)}); err != nil {
		t.Fatalf("Reconfigure: %v", err)
	}
	store.ApplyEvent(events.Event{TsNs: 1, Type: events.TypeConnectionOpened, Data: events.ConnectionOpenedData{ConnID: "c1"}})
	if got := len(store.Snapshot("all", 0).Connections); got != 0 {
		t.Fatalf("connections after disabled recording = %d, want 0", got)
	}
}
