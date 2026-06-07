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

func TestRulesEndpointReturnsRequestedProfileRules(t *testing.T) {
	cfg := testServersConfig("A")
	cfg.Profiles[0].Rules = []config.RuleConfig{{
		Name:    "a-rule",
		Action:  "direct",
		Domains: []string{"a.example.com"},
	}}
	cfg.Profiles[1].Rules = []config.RuleConfig{{
		Name:    "b-rule",
		Action:  "block",
		Domains: []string{"b.example.com"},
	}}
	srv := New(engine.New(cfg, nil), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/rules?profile=B", nil)
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
	if resp.Profile != "B" || len(resp.Rules) != 1 || resp.Rules[0].Name != "b-rule" {
		t.Fatalf("rules response = %+v, want profile B b-rule", resp)
	}
}

func TestRulesEndpointRejectsMissingProfile(t *testing.T) {
	srv := New(engine.New(testServersConfig("A"), nil), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/rules?profile=missing", nil)
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%q, want 404", rec.Code, rec.Body.String())
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
			RuleName   string `json:"rule_name"`
			RuleNumber int    `json:"rule_number"`
			Action     string `json:"action"`
			Network    string `json:"network"`
			Target     string `json:"target"`
		} `json:"decision"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Profile != "A" || resp.Decision.RuleName != "ads" || resp.Decision.RuleNumber != 1 || resp.Decision.Action != "block" || resp.Decision.Network != "tcp" {
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
			RuleNumber int    `json:"rule_number"`
			Action     string `json:"action"`
			ChainName  string `json:"chain_name"`
			Default    bool   `json:"default"`
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
	if resp.Decision.RuleNumber != 1 || resp.Decision.Action != "chain" || resp.Decision.ChainName != "proxy" || !resp.Decision.Default {
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

func TestReplaceRulesPersistsOrderedConfigWithBackupAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	cfg := testRuleCreateConfig()
	cfg.Profiles[0].Rules = []config.RuleConfig{{
		Name:    "old",
		Action:  "direct",
		Domains: []string{"old.example.com"},
	}}
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	srv := NewWithOptions(engine.New(cfg, nil), nil, Options{ConfigPath: path})
	body := []byte(`{"rules":[{"name":"first","action":"block","domains":["one.example.com"]},{"name":"second","action":"direct","domains":["two.example.com"]}]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/rules", bytes.NewReader(body))
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
	if resp.Profile != "A" || len(resp.Rules) != 2 || resp.Rules[0].Name != "first" || resp.Rules[1].Name != "second" {
		t.Fatalf("response = %+v", resp)
	}
	if resp.BackupPath == "" {
		t.Fatalf("backup_path empty in response %+v", resp)
	}
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("load persisted config: %v", err)
	}
	profile, err := reloaded.ActiveProfile()
	if err != nil {
		t.Fatal(err)
	}
	if len(profile.Rules) != 2 || profile.Rules[0].Name != "first" || profile.Rules[1].Name != "second" {
		t.Fatalf("persisted rules = %+v", profile.Rules)
	}
	if got := srv.engine.Config().Profiles[0].Rules; len(got) != 2 || got[0].Name != "first" || got[1].Name != "second" {
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

func TestRuleSubscriptionRefreshGeneratesEffectiveRules(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("||ads.example.com^\n192.0.2.0/24\n"))
	}))
	defer source.Close()

	path := filepath.Join(t.TempDir(), "clambhook.toml")
	cfg := testRuleCreateConfig()
	cfg.Profiles[0].RuleSubscriptions = []config.RuleSubscriptionConfig{{
		Name:   "ads",
		URL:    source.URL,
		Format: "auto",
		Action: "block",
	}}
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	srv := NewWithOptions(engine.New(cfg, nil), nil, Options{ConfigPath: path})

	refreshReq := httptest.NewRequest(http.MethodPost, "/api/v1/rule-subscriptions/refresh", bytes.NewReader([]byte(`{}`)))
	refreshRec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(refreshRec, refreshReq)
	if refreshRec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d body=%q, want 200", refreshRec.Code, refreshRec.Body.String())
	}
	var refreshResp struct {
		Profile       string `json:"profile"`
		Subscriptions []struct {
			Name        string `json:"name"`
			DomainCount int    `json:"domain_count"`
			CIDRCount   int    `json:"cidr_count"`
			LastError   string `json:"last_error"`
		} `json:"subscriptions"`
	}
	if err := json.NewDecoder(refreshRec.Body).Decode(&refreshResp); err != nil {
		t.Fatal(err)
	}
	if len(refreshResp.Subscriptions) != 1 || refreshResp.Subscriptions[0].DomainCount != 1 || refreshResp.Subscriptions[0].CIDRCount != 1 || refreshResp.Subscriptions[0].LastError != "" {
		t.Fatalf("refresh response = %+v", refreshResp)
	}

	rulesReq := httptest.NewRequest(http.MethodGet, "/api/v1/rules", nil)
	rulesRec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rulesRec, rulesReq)
	if rulesRec.Code != http.StatusOK {
		t.Fatalf("rules status = %d body=%q, want 200", rulesRec.Code, rulesRec.Body.String())
	}
	var rulesResp struct {
		Rules          []config.RuleConfig `json:"rules"`
		GeneratedRules []config.RuleConfig `json:"generated_rules"`
		EffectiveRules []config.RuleConfig `json:"effective_rules"`
	}
	if err := json.NewDecoder(rulesRec.Body).Decode(&rulesResp); err != nil {
		t.Fatal(err)
	}
	if len(rulesResp.Rules) != 0 || len(rulesResp.GeneratedRules) != 2 || len(rulesResp.EffectiveRules) != 2 {
		t.Fatalf("rules response = %+v", rulesResp)
	}

	testReq := httptest.NewRequest(http.MethodPost, "/api/v1/rules/test", bytes.NewReader([]byte(`{"network":"tcp","target":"cdn.ads.example.com:443"}`)))
	testRec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(testRec, testReq)
	if testRec.Code != http.StatusOK {
		t.Fatalf("rule test status = %d body=%q, want 200", testRec.Code, testRec.Body.String())
	}
	var testResp struct {
		Decision struct {
			RuleName   string `json:"rule_name"`
			RuleNumber int    `json:"rule_number"`
			Action     string `json:"action"`
		} `json:"decision"`
	}
	if err := json.NewDecoder(testRec.Body).Decode(&testResp); err != nil {
		t.Fatal(err)
	}
	if testResp.Decision.RuleName != "subscription:ads:domains" || testResp.Decision.RuleNumber != 1 || testResp.Decision.Action != "block" {
		t.Fatalf("rule test response = %+v", testResp.Decision)
	}
}

func TestRuleSetRefreshFeedsRouteExplain(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("||ads.example.com^\n203.0.113.0/24\n"))
	}))
	defer source.Close()

	path := filepath.Join(t.TempDir(), "clambhook.toml")
	cfg := testRuleCreateConfig()
	cfg.Profiles[0].RuleSets = []config.RuleSetConfig{{
		Name:   "ads",
		URL:    source.URL,
		Format: "auto",
	}}
	cfg.Profiles[0].Rules = []config.RuleConfig{{
		Name:        "guest-ads",
		Action:      "block",
		RuleSets:    []string{"ads"},
		SourceCIDRs: []string{"10.10.0.0/16"},
	}}
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	srv := NewWithOptions(engine.New(cfg, nil), nil, Options{ConfigPath: path})

	refreshReq := httptest.NewRequest(http.MethodPost, "/api/v1/rule-sets/refresh", bytes.NewReader([]byte(`{}`)))
	refreshRec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(refreshRec, refreshReq)
	if refreshRec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d body=%q, want 200", refreshRec.Code, refreshRec.Body.String())
	}
	var refreshResp struct {
		Profile  string                 `json:"profile"`
		RuleSets []config.RuleSetConfig `json:"rule_sets"`
		Statuses []struct {
			Name        string `json:"name"`
			DomainCount int    `json:"domain_count"`
			CIDRCount   int    `json:"cidr_count"`
			LastError   string `json:"last_error"`
		} `json:"statuses"`
	}
	if err := json.NewDecoder(refreshRec.Body).Decode(&refreshResp); err != nil {
		t.Fatal(err)
	}
	if refreshResp.Profile != "A" || len(refreshResp.RuleSets) != 1 || len(refreshResp.Statuses) != 1 || refreshResp.Statuses[0].DomainCount != 1 || refreshResp.Statuses[0].CIDRCount != 1 || refreshResp.Statuses[0].LastError != "" {
		t.Fatalf("refresh response = %+v", refreshResp)
	}

	explainReq := httptest.NewRequest(http.MethodPost, "/api/v1/routes/explain", bytes.NewReader([]byte(`{"network":"tcp","target":"cdn.ads.example.com:443","source":"10.10.1.2:52000"}`)))
	explainRec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(explainRec, explainReq)
	if explainRec.Code != http.StatusOK {
		t.Fatalf("explain status = %d body=%q, want 200", explainRec.Code, explainRec.Body.String())
	}
	var explainResp struct {
		Decision struct {
			RuleName string `json:"rule_name"`
			Action   string `json:"action"`
			Source   string `json:"source"`
		} `json:"decision"`
	}
	if err := json.NewDecoder(explainRec.Body).Decode(&explainResp); err != nil {
		t.Fatal(err)
	}
	if explainResp.Decision.RuleName != "guest-ads" || explainResp.Decision.Action != "block" || explainResp.Decision.Source != "10.10.1.2:52000" {
		t.Fatalf("explain response = %+v", explainResp.Decision)
	}

	missReq := httptest.NewRequest(http.MethodPost, "/api/v1/routes/explain", bytes.NewReader([]byte(`{"network":"tcp","target":"cdn.ads.example.com:443","source":"10.20.1.2:52000"}`)))
	missRec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(missRec, missReq)
	if missRec.Code != http.StatusOK {
		t.Fatalf("miss explain status = %d body=%q, want 200", missRec.Code, missRec.Body.String())
	}
	var missResp struct {
		Decision struct {
			RuleName string `json:"rule_name"`
			Default  bool   `json:"default"`
		} `json:"decision"`
	}
	if err := json.NewDecoder(missRec.Body).Decode(&missResp); err != nil {
		t.Fatal(err)
	}
	if missResp.Decision.RuleName != "" || !missResp.Decision.Default {
		t.Fatalf("miss explain response = %+v", missResp.Decision)
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
