package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
	"github.com/JohnThre/clambhook/internal/events"
	"github.com/JohnThre/clambhook/internal/traffic"
)

func TestRulesEndpointReturnsActiveProfileRules(t *testing.T) {
	cfg := testServersConfig("A")
	cfg.Profiles[0].Rules = []config.RuleConfig{{
		Name:           "ads",
		Action:         "block",
		DomainSuffixes: []string{"ads.example.com"},
	}}
	srv := New(engine.New(cfg, nil), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/rules", nil)
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var resp struct {
		Profile string              `json:"profile"`
		Rules   []config.RuleConfig `json:"rules"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Profile != "A" || len(resp.Rules) != 1 || resp.Rules[0].Name != "ads" {
		t.Fatalf("rules response = %+v", resp)
	}
}

func TestDecisionsEndpointReturnsRuleDecisions(t *testing.T) {
	store, err := traffic.NewStore(config.TrafficConfig{
		Enabled:     true,
		HistoryPath: filepath.Join(t.TempDir(), "traffic.json"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	store.ApplyEvent(events.Event{
		TsNs: time.Now().UnixNano(),
		Type: events.TypeConnectionOpened,
		Data: events.ConnectionOpenedData{ConnID: "c1"},
	})
	store.ApplyEvent(events.Event{
		TsNs: time.Now().UnixNano(),
		Type: events.TypeRuleBlocked,
		Data: events.RuleDecisionData{
			ConnID:   "c1",
			RuleName: "ads",
			Action:   "block",
			Target:   "ads.example.com:443",
		},
	})

	srv := NewWithOptions(engine.New(testServersConfig("A"), nil), nil, Options{TrafficStore: store})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/decisions", nil)
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var resp struct {
		Decisions []traffic.Connection `json:"decisions"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Decisions) != 1 || resp.Decisions[0].RuleName != "ads" || resp.Decisions[0].RuleAction != "block" {
		t.Fatalf("decisions response = %+v", resp.Decisions)
	}
}
