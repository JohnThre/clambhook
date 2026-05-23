package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
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

func TestHandleTrafficUsesSwappedStore(t *testing.T) {
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

	s := &Server{}
	s.SetTrafficStore(store)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/traffic", nil)
	rec := httptest.NewRecorder()

	s.handleTraffic(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got traffic.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Connections) != 1 || got.Connections[0].ConnID != "c1" {
		t.Fatalf("snapshot = %+v", got)
	}
}

func TestServerStartAddrAndShutdown(t *testing.T) {
	srv := New(engine.New(testAuthConfig(), nil), nil)
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if srv.Addr() == "" {
		t.Fatal("Addr is empty after Start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestSetActiveProfileRejectsOversizedBody(t *testing.T) {
	srv := New(engine.New(testAuthConfig(), nil), nil)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/profiles/active",
		strings.NewReader(strings.Repeat("x", maxJSONRequestBytes+1)))
	rec := httptest.NewRecorder()

	srv.handleSetActiveProfile(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%q, want 400", rec.Code, rec.Body.String())
	}
}
