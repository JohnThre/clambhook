package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
	"github.com/JohnThre/clambhook/internal/policy"
	"github.com/JohnThre/clambhook/internal/protocol"
)

type policyAPIDialer struct{}

func (d policyAPIDialer) Dial(ctx context.Context, network, address string) (protocol.Conn, error) {
	var nd net.Dialer
	conn, err := nd.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return policyAPIConn{Conn: conn}, nil
}

func (d policyAPIDialer) DialThrough(context.Context, io.ReadWriteCloser, string) (protocol.Conn, error) {
	return nil, io.ErrClosedPipe
}

func (d policyAPIDialer) Protocol() string { return "policy_api_direct" }

type policyAPIConn struct {
	net.Conn
}

func (c policyAPIConn) Protocol() string { return "policy_api_direct" }

func init() {
	protocol.Register("policy_api_direct", func(protocol.Server) (protocol.Dialer, error) {
		return policyAPIDialer{}, nil
	})
}

func TestPolicyGroupsEndpointReturnsIdleConfigSnapshot(t *testing.T) {
	cfg := testPolicyGroupConfig("https://probe.example/generate_204")
	srv := New(engine.New(cfg, nil), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/policy-groups", nil)
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var resp policy.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Profile != "A" || len(resp.Groups) != 1 || resp.Groups[0].Name != "auto" || resp.Groups[0].SelectedChain != "primary" {
		t.Fatalf("policy groups = %+v", resp)
	}
	if len(resp.Groups[0].Results) != 0 {
		t.Fatalf("results = %+v, want none for idle engine", resp.Groups[0].Results)
	}
}

func TestPolicyGroupsEndpointReturnsRequestedInactiveProfileSnapshot(t *testing.T) {
	cfg := testPolicyGroupConfig("https://probe.example/generate_204")
	cfg.Profiles = append(cfg.Profiles, config.Profile{
		Name: "B",
		Chains: []config.ChainConfig{
			policyAPIChain("b-primary"),
			policyAPIChain("b-backup"),
		},
		PolicyGroups: []config.PolicyGroupConfig{{
			Name:    "b-auto",
			Type:    policy.TypeURLTest,
			Chains:  []string{"b-primary", "b-backup"},
			TestURL: "https://b-probe.example/generate_204",
		}},
	})
	srv := New(engine.New(cfg, nil), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/policy-groups?profile=B", nil)
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var resp policy.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Profile != "B" || len(resp.Groups) != 1 || resp.Groups[0].Name != "b-auto" || resp.Groups[0].SelectedChain != "b-primary" {
		t.Fatalf("policy groups = %+v, want inactive profile B config snapshot", resp)
	}
	if len(resp.Groups[0].Results) != 0 {
		t.Fatalf("results = %+v, want no runtime results for inactive profile", resp.Groups[0].Results)
	}
}

func TestPolicyGroupsEndpointRejectsMissingProfile(t *testing.T) {
	srv := New(engine.New(testPolicyGroupConfig("https://probe.example/generate_204"), nil), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/policy-groups?profile=missing", nil)
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%q, want 404", rec.Code, rec.Body.String())
	}
}

func TestPolicyGroupSelectionPersistsSelectGroup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	cfg := testPolicyGroupConfig("https://probe.example/generate_204")
	cfg.Profiles[0].PolicyGroups = []config.PolicyGroupConfig{{
		Name:     "manual",
		Type:     policy.TypeSelect,
		Chains:   []string{"primary", "backup"},
		Selected: "primary",
	}}
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	srv := NewWithOptions(engine.New(cfg, nil), nil, Options{ConfigPath: path})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/policy-groups/selection", bytes.NewReader([]byte(`{"group":"manual","chain":"backup"}`)))
	rec := httptest.NewRecorder()

	srv.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var resp struct {
		Profile    string                 `json:"profile"`
		Group      string                 `json:"group"`
		Chain      string                 `json:"chain"`
		BackupPath string                 `json:"backup_path"`
		Groups     []policy.GroupSnapshot `json:"groups"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Profile != "A" || resp.Group != "manual" || resp.Chain != "backup" || resp.BackupPath == "" {
		t.Fatalf("response = %+v", resp)
	}
	if len(resp.Groups) != 1 || resp.Groups[0].SelectedChain != "backup" || resp.Groups[0].SelectionMode != "manual" {
		t.Fatalf("policy groups = %+v", resp.Groups)
	}
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("load persisted config: %v", err)
	}
	profile, err := reloaded.ActiveProfile()
	if err != nil {
		t.Fatal(err)
	}
	if got := profile.PolicyGroups[0].Selected; got != "backup" {
		t.Fatalf("persisted selected = %q, want backup", got)
	}
}

func TestPolicyGroupManualTestRequiresRunningEngine(t *testing.T) {
	cfg := testPolicyGroupConfig("https://probe.example/generate_204")
	srv := New(engine.New(cfg, nil), nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy-groups/test", bytes.NewReader([]byte(`{"group":"auto"}`)))
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%q, want 409", rec.Code, rec.Body.String())
	}
}

func TestPolicyGroupManualTestRunsProbes(t *testing.T) {
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			http.Error(w, "method must be HEAD", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer probe.Close()

	cfg := testPolicyGroupConfig(probe.URL)
	eng := engine.New(cfg, nil)
	if err := eng.Start(context.Background()); err != nil {
		t.Fatalf("engine start: %v", err)
	}
	defer eng.Stop()
	srv := New(eng, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy-groups/test", bytes.NewReader([]byte(`{"group":"auto"}`)))
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var resp policy.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Groups) != 1 || resp.Groups[0].SelectedChain == "" || len(resp.Groups[0].Results) != 2 {
		t.Fatalf("policy response = %+v", resp)
	}
	for _, result := range resp.Groups[0].Results {
		if !result.Healthy || result.StatusCode != http.StatusNoContent || result.LatencyNs <= 0 {
			t.Fatalf("probe result = %+v, want healthy 204 with latency", result)
		}
	}
}

func TestRuleTestEndpointReturnsPolicyGroupSelection(t *testing.T) {
	cfg := testPolicyGroupConfig("https://probe.example/generate_204")
	cfg.Profiles[0].Rules = []config.RuleConfig{{
		Name:           "auto",
		Action:         "group:auto",
		DomainSuffixes: []string{"example.com"},
	}}
	srv := New(engine.New(cfg, nil), nil)

	body := []byte(`{"network":"tcp","target":"api.example.com:443"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rules/test", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var resp struct {
		Decision struct {
			Action    string `json:"action"`
			GroupName string `json:"group_name"`
			ChainName string `json:"chain_name"`
		} `json:"decision"`
		Chain struct {
			Name string `json:"name"`
		} `json:"chain"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Decision.Action != "group" || resp.Decision.GroupName != "auto" || resp.Decision.ChainName != "primary" || resp.Chain.Name != "primary" {
		t.Fatalf("rule test response = %+v", resp)
	}
}

func testPolicyGroupConfig(testURL string) *config.Config {
	return &config.Config{
		Active: "A",
		Profiles: []config.Profile{{
			Name: "A",
			Listen: config.ListenConfig{
				SOCKS5: "127.0.0.1:0",
			},
			Chains: []config.ChainConfig{
				policyAPIChain("primary"),
				policyAPIChain("backup"),
			},
			PolicyGroups: []config.PolicyGroupConfig{{
				Name:    "auto",
				Type:    policy.TypeURLTest,
				Chains:  []string{"primary", "backup"},
				TestURL: testURL,
				Timeout: config.Duration(2 * 1_000_000_000),
			}},
		}},
		Traffic: config.DefaultTrafficConfig(),
	}
}

func policyAPIChain(name string) config.ChainConfig {
	return config.ChainConfig{
		Name: name,
		Servers: []config.ServerConfig{{
			Name:     name + "-server",
			Address:  "127.0.0.1:1",
			Protocol: "policy_api_direct",
		}},
	}
}
