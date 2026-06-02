package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
	"github.com/JohnThre/clambhook/internal/events"
	_ "github.com/JohnThre/clambhook/internal/protocol/tor"
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

func TestRuleTestEndpointEvaluatesRulesWithoutTraffic(t *testing.T) {
	cfg := testRuleCreateConfig()
	cfg.Profiles[0].Rules = []config.RuleConfig{{
		Name:           "ads",
		Action:         "block",
		DomainSuffixes: []string{"ads.example.com"},
		Networks:       []string{"tcp"},
	}}
	srv := New(engine.New(cfg, nil), nil)
	body := []byte(`{"network":"tcp","target":"cdn.ads.example.com:443"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rules/test", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var resp struct {
		Profile  string `json:"profile"`
		Decision struct {
			RuleName string `json:"rule_name"`
			Action   string `json:"action"`
			Network  string `json:"network"`
			Target   string `json:"target"`
		} `json:"decision"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Profile != "A" || resp.Decision.RuleName != "ads" || resp.Decision.Action != "block" || resp.Decision.Network != "tcp" {
		t.Fatalf("rule test response = %+v", resp)
	}
}

func TestRuleTestEndpointReturnsDefaultChainDetails(t *testing.T) {
	srv := New(engine.New(testRuleCreateConfig(), nil), nil)
	body := []byte(`{"network":"udp","target":"1.1.1.1:53"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rules/test", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var resp struct {
		Decision struct {
			Action    string `json:"action"`
			ChainName string `json:"chain_name"`
			Default   bool   `json:"default"`
		} `json:"decision"`
		Chain struct {
			Name         string `json:"name"`
			HopCount     int    `json:"hop_count"`
			Capabilities struct {
				UDP       bool   `json:"udp"`
				UDPMode   string `json:"udp_mode"`
				UDPReason string `json:"udp_reason"`
			} `json:"capabilities"`
		} `json:"chain"`
		Hops []serverPayload `json:"hops"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Decision.Action != "chain" || resp.Decision.ChainName != "proxy" || !resp.Decision.Default {
		t.Fatalf("decision = %+v, want default chain proxy", resp.Decision)
	}
	if resp.Chain.Name != "proxy" || resp.Chain.HopCount != 1 || len(resp.Hops) != 1 {
		t.Fatalf("chain details = %+v hops=%d", resp.Chain, len(resp.Hops))
	}
}

func TestCreateRulePersistsConfigWithBackupAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	cfg := testRuleCreateConfig()
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	srv := NewWithOptions(engine.New(cfg, nil), nil, Options{ConfigPath: path})
	body := []byte(`{"rule":{"name":"ads","action":"block","domains":["ads.example.com"]}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rules", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var resp struct {
		Profile    string              `json:"profile"`
		Rules      []config.RuleConfig `json:"rules"`
		BackupPath string              `json:"backup_path"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Profile != "A" || len(resp.Rules) != 1 || resp.Rules[0].Name != "ads" {
		t.Fatalf("response = %+v", resp)
	}
	if resp.BackupPath == "" {
		t.Fatalf("backup_path empty in response %+v", resp)
	}
	if _, err := config.Load(resp.BackupPath); err != nil {
		t.Fatalf("backup config not readable: %v", err)
	}
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("load persisted config: %v", err)
	}
	profile, err := reloaded.ActiveProfile()
	if err != nil {
		t.Fatal(err)
	}
	if len(profile.Rules) != 1 || profile.Rules[0].Domains[0] != "ads.example.com" {
		t.Fatalf("persisted rules = %+v", profile.Rules)
	}
	if got := srv.engine.Config().Profiles[0].Rules; len(got) != 1 || got[0].Name != "ads" {
		t.Fatalf("engine rules after reload = %+v", got)
	}
}

func TestCreateRuleRequiresConfigPath(t *testing.T) {
	srv := NewWithOptions(engine.New(testRuleCreateConfig(), nil), nil, Options{})
	body := []byte(`{"rule":{"name":"ads","action":"block","domains":["ads.example.com"]}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rules", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%q, want 409", rec.Code, rec.Body.String())
	}
}

func TestCreateRuleRejectsInvalidRule(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	cfg := testRuleCreateConfig()
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	srv := NewWithOptions(engine.New(cfg, nil), nil, Options{ConfigPath: path})
	body := []byte(`{"rule":{"name":"bad","action":"chain:missing","domains":["example.com"]}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rules", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%q, want 400", rec.Code, rec.Body.String())
	}
}

func testRuleCreateConfig() *config.Config {
	return &config.Config{
		Active: "A",
		Profiles: []config.Profile{{
			Name: "A",
			Chains: []config.ChainConfig{{
				Name: "proxy",
				Servers: []config.ServerConfig{{
					Name:     "tor",
					Address:  "127.0.0.1:9050",
					Protocol: "tor",
				}},
			}},
		}},
		Traffic: config.DefaultTrafficConfig(),
	}
}
