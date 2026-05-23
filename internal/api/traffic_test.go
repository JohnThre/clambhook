package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/events"
	"github.com/JohnThre/clambhook/internal/traffic"
)

func TestHandleTrafficSnapshot(t *testing.T) {
	store, err := traffic.NewStore(config.TrafficConfig{
		Enabled:       true,
		HistoryLimit:  10,
		HistoryMaxAge: config.Duration(time.Hour),
		HistoryPath:   filepath.Join(t.TempDir(), "traffic-history.json"),
	}, nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	store.ApplyEvent(events.Event{TsNs: 1, Type: events.TypeConnectionOpened, Data: events.ConnectionOpenedData{ConnID: "c1"}})
	store.ApplyEvent(events.Event{TsNs: 2, Type: events.TypeConnectionDialing, Data: events.ConnectionDialingData{ConnID: "c1", Target: "example.com:443"}})

	s := &Server{traffic: store}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/traffic?state=active&limit=1", nil)
	rec := httptest.NewRecorder()

	s.handleTraffic(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got traffic.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Connections) != 1 || got.Connections[0].Target != "example.com:443" {
		t.Fatalf("snapshot = %+v", got)
	}
}

func TestHandleTrafficDisabledReturnsEmptySnapshot(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/traffic", nil)
	rec := httptest.NewRecorder()

	s.handleTraffic(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got traffic.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Connections) != 0 || got.Summary.HistoryPersisted {
		t.Fatalf("snapshot = %+v", got)
	}
}
