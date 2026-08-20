package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/developer"
	"github.com/JohnThre/clambhook/internal/engine"
)

func TestDeveloperRewriteRulesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	cfg := testDeveloperSettingsConfig(t)
	cfg.Developer.Enabled = true
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	dev, err := developer.NewManager(cfg.Developer)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	srv := NewWithOptions(engine.New(cfg, nil), nil, Options{ConfigPath: path, Developer: dev})

	// PUT a rewrite rule.
	rule := config.DeveloperRewriteRuleConfig{
		ID:      "rw-1",
		Name:    "add auth",
		Enabled: true,
		Stage:   "request",
		Match:   config.DeveloperMatchConfig{Host: "api.example.com", PathPrefix: "/v1/"},
		Ops: []config.DeveloperRewriteOp{
			{Target: "header", Action: "add", Field: "X-Test", Value: "dev"},
		},
	}
	body, err := json.Marshal(map[string]any{"rules": []config.DeveloperRewriteRuleConfig{rule}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/developer/rewrite-rules", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT rewrite-rules: status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var resp developerRulesPersistenceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Developer.RewriteRules) != 1 || resp.Developer.RewriteRules[0].ID != "rw-1" {
		t.Fatalf("persisted response rewrite rules = %+v", resp.Developer.RewriteRules)
	}

	// GET reflects the persisted rule.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/developer/rewrite-rules", nil)
	rec = httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET rewrite-rules: status = %d, want 200", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	rules, ok := got["rules"].([]any)
	if !ok || len(rules) != 1 {
		t.Fatalf("GET rewrite-rules rules = %v", got)
	}

	// The live manager must reflect the persisted rule (Reconfigure ran).
	if dev.ConfigSnapshot().Enabled {
		if got := dev.HasRequestRewrite(httptest.NewRequest(http.MethodGet, "http://api.example.com/v1/x", nil)); !got {
			t.Error("live manager HasRequestRewrite = false after PUT, want true")
		}
	}

	// DELETE removes the rule.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/developer/rewrite-rules/rw-1", nil)
	rec = httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE rewrite-rules: status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var delResp developerRulesPersistenceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &delResp); err != nil {
		t.Fatal(err)
	}
	if len(delResp.Developer.RewriteRules) != 0 {
		t.Fatalf("after DELETE, rewrite rules = %+v, want empty", delResp.Developer.RewriteRules)
	}

	// Reloaded config on disk reflects the deletion.
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("load persisted config: %v", err)
	}
	if len(reloaded.Developer.RewriteRules) != 0 {
		t.Fatalf("persisted config rewrite rules = %+v, want empty", reloaded.Developer.RewriteRules)
	}
}

func TestDeveloperRewriteRulesRejectsInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	cfg := testDeveloperSettingsConfig(t)
	cfg.Developer.Enabled = true
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	dev, err := developer.NewManager(cfg.Developer)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	srv := NewWithOptions(engine.New(cfg, nil), nil, Options{ConfigPath: path, Developer: dev})

	// A status op on a request-only stage must fail validation.
	rule := config.DeveloperRewriteRuleConfig{
		ID:      "rw-bad",
		Enabled: true,
		Stage:   "request",
		Ops:     []config.DeveloperRewriteOp{{Target: "status", Action: "set", Value: "204"}},
	}
	body, err := json.Marshal(map[string]any{"rules": []config.DeveloperRewriteRuleConfig{rule}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/developer/rewrite-rules", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT invalid rewrite-rules: status = %d body=%q, want 400", rec.Code, rec.Body.String())
	}
}

func TestDeveloperRewriteRulesRequiresConfigPath(t *testing.T) {
	cfg := testDeveloperSettingsConfig(t)
	cfg.Developer.Enabled = true
	dev, err := developer.NewManager(cfg.Developer)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	srv := NewWithOptions(engine.New(cfg, nil), nil, Options{Developer: dev}) // no ConfigPath

	req := httptest.NewRequest(http.MethodPut, "/api/v1/developer/rewrite-rules",
		bytes.NewReader([]byte(`{"rules":[]}`)))
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("PUT without config path: status = %d, want 409", rec.Code)
	}
}
