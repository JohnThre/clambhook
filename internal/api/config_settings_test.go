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
	_ "github.com/JohnThre/clambhook/internal/protocol/shadowsocks"
)

func TestConfigSettingsEndpointReturnsActiveProfileSettings(t *testing.T) {
	cfg := testServersConfig("A")
	cfg.Profiles[0].Listen.SOCKS5 = "127.0.0.1:1080"
	cfg.Profiles[0].Listen.SOCKS5Chain = "a-default"
	cfg.Profiles[0].Listen.HTTP = "127.0.0.1:8080"
	cfg.Profiles[0].Listen.HTTPChain = "a-default"
	cfg.Profiles[0].Listen.TUN = &config.TUNConfig{
		Enabled:      true,
		Name:         "utun",
		Chain:        "a-default",
		MTU:          1500,
		Routes:       []string{"0.0.0.0/0", "::/0"},
		ExcludeCIDRs: []string{"127.0.0.0/8"},
	}
	cfg.Profiles[0].Chains[0].Servers[0].Settings = map[string]any{
		"method":   "chacha20-ietf-poly1305",
		"password": "secret",
	}
	cfg.Profiles[0].DNS = config.DNSConfig{
		Enabled: true,
		Timeout: config.Duration(4 * time.Second),
		Upstreams: []config.DNSUpstreamConfig{{
			Name:     "cf",
			Protocol: "doh",
			URL:      "https://cloudflare-dns.com/dns-query",
		}},
	}
	srv := New(engine.New(cfg, nil), nil)

	resp := getConfigSettings(t, srv, "/api/v1/config/settings")

	if resp.Profile != "A" || resp.Listen.SOCKS5 != "127.0.0.1:1080" || resp.Listen.HTTP != "127.0.0.1:8080" {
		t.Fatalf("settings response = %+v", resp)
	}
	if !resp.Listen.TUN.Enabled || resp.Listen.TUN.Chain != "a-default" || resp.Listen.TUN.MTU != 1500 {
		t.Fatalf("tun response = %+v", resp.Listen.TUN)
	}
	if !resp.DNS.Enabled || len(resp.DNS.Upstreams) != 1 || resp.DNS.Upstreams[0].Name != "cf" {
		t.Fatalf("dns response = %+v", resp.DNS)
	}
}

func TestConfigSettingsUpdatePersistsBackupAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	cfg := testServersConfig("A")
	cfg.Profiles[0].Listen.SOCKS5 = "127.0.0.1:1080"
	cfg.Profiles[0].Listen.SOCKS5Chain = "a-default"
	cfg.Profiles[0].Listen.HTTP = "127.0.0.1:8080"
	cfg.Profiles[0].Listen.HTTPChain = "a-default"
	cfg.Profiles[0].Chains[0].Servers[0].Settings = map[string]any{
		"method":   "chacha20-ietf-poly1305",
		"password": "secret",
	}
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	srv := NewWithOptions(engine.New(cfg, nil), nil, Options{ConfigPath: path})
	body := []byte(`{
		"listen": {
			"socks5": "127.0.0.1:1180",
			"http": "127.0.0.1:18080",
			"tun": {
				"enabled": true,
				"name": "utun",
				"chain": "a-default",
				"mtu": 1500,
				"addresses": ["198.18.0.1/30"],
				"routes": ["0.0.0.0/0", "::/0"],
				"exclude_cidrs": ["127.0.0.0/8"]
			}
		},
		"dns": {
			"enabled": true,
			"timeout": "3s",
			"upstreams": [{"name":"cf","protocol":"doh","url":"https://cloudflare-dns.com/dns-query"}]
		}
	}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var resp configSettingsAPIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
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
	if profile.Listen.SOCKS5 != "127.0.0.1:1180" || profile.Listen.HTTP != "127.0.0.1:18080" {
		t.Fatalf("persisted listen = %+v", profile.Listen)
	}
	if profile.Listen.TUN == nil || !profile.Listen.TUN.Enabled || profile.Listen.TUN.Name != "utun" || len(profile.Listen.TUN.Routes) != 2 {
		t.Fatalf("persisted tun = %+v", profile.Listen.TUN)
	}
	if !profile.DNS.Enabled || profile.DNS.Timeout.Std() != 3*time.Second || len(profile.DNS.Upstreams) != 1 {
		t.Fatalf("persisted dns = %+v", profile.DNS)
	}
	if got := srv.engine.Config().Profiles[0].Listen.HTTP; got != "127.0.0.1:18080" {
		t.Fatalf("engine http listen after reload = %q", got)
	}
}

func TestConfigSettingsUpdateRejectsInvalidListenAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	cfg := testServersConfig("A")
	cfg.Profiles[0].Listen.SOCKS5Chain = "a-default"
	cfg.Profiles[0].Chains[0].Servers[0].Settings = map[string]any{
		"method":   "chacha20-ietf-poly1305",
		"password": "secret",
	}
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	srv := NewWithOptions(engine.New(cfg, nil), nil, Options{ConfigPath: path})
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/config/settings",
		bytes.NewReader([]byte(`{"listen":{"socks5":"not an address"}}`)),
	)
	rec := httptest.NewRecorder()

	srv.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%q, want 400", rec.Code, rec.Body.String())
	}
}

type configSettingsAPIResponse struct {
	Profile    string                      `json:"profile"`
	Listen     configSettingsListenPayload `json:"listen"`
	DNS        config.DNSConfig            `json:"dns"`
	BackupPath string                      `json:"backup_path"`
}

func getConfigSettings(t *testing.T, srv *Server, path string) configSettingsAPIResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var resp configSettingsAPIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return resp
}
