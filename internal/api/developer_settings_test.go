package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/developer"
	"github.com/JohnThre/clambhook/internal/engine"
	_ "github.com/JohnThre/clambhook/internal/protocol/shadowsocks"
)

func TestDeveloperSettingsDefaultsHideCAPaths(t *testing.T) {
	cfg := testDeveloperSettingsConfig(t)
	srv := New(engine.New(cfg, nil), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/settings", nil)
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if _, ok := resp["ca_key_path"]; ok {
		t.Fatalf("settings response exposed ca_key_path: %+v", resp)
	}
	if _, ok := resp["ca_cert_path"]; ok {
		t.Fatalf("settings response exposed ca_cert_path: %+v", resp)
	}
	if enabled, _ := resp["mitm_enabled"].(bool); enabled {
		t.Fatalf("mitm_enabled = true, want false")
	}
}

func TestDeveloperSettingsRejectsHTTPSCaptureWithoutAck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	cfg := testDeveloperSettingsConfig(t)
	cfg.Developer.Enabled = true
	cfg.Developer.MITMEnabled = false
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	dev, err := developer.NewManager(cfg.Developer)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	srv := NewWithOptions(engine.New(cfg, nil), nil, Options{ConfigPath: path, Developer: dev})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/developer/settings", bytes.NewReader([]byte(`{"mitm_enabled":true}`)))
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%q, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "https_capture_ack") {
		t.Fatalf("body = %q, want ack error", rec.Body.String())
	}
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("load persisted config: %v", err)
	}
	if reloaded.Developer.MITMEnabled {
		t.Fatal("MITMEnabled persisted true without acknowledgement")
	}
}

func TestDeveloperSettingsPersistsHTTPSCaptureWithAck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	cfg := testDeveloperSettingsConfig(t)
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	dev, err := developer.NewManager(cfg.Developer)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	srv := NewWithOptions(engine.New(cfg, nil), nil, Options{ConfigPath: path, Developer: dev})
	body := []byte(`{
		"enabled": true,
		"mitm_enabled": true,
		"https_capture_ack": true,
		"redact_query_params": [" Access_Token ", "SECRET"]
	}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/developer/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var resp developerSettingsPayload
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Enabled || !resp.MITMEnabled || resp.BackupPath == "" {
		t.Fatalf("settings response = %+v", resp)
	}
	if got := strings.Join(resp.RedactQueryParams, ","); got != "access_token,secret" {
		t.Fatalf("redact query params = %q", got)
	}
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("load persisted config: %v", err)
	}
	if !reloaded.Developer.Enabled || !reloaded.Developer.MITMEnabled {
		t.Fatalf("persisted developer config = %+v", reloaded.Developer)
	}
	if !srv.developerManager().MITMEnabled() {
		t.Fatal("live developer manager MITMEnabled = false, want true")
	}
}

func TestDeveloperSettingsEnablingCaptureClearsStaleHTTPSCapture(t *testing.T) {
	current := config.DefaultDeveloperConfig()
	current.Enabled = false
	current.MITMEnabled = true
	enabled := true

	next, err := applyDeveloperSettingsUpdate(current, updateDeveloperSettingsRequest{Enabled: &enabled})
	if err != nil {
		t.Fatalf("applyDeveloperSettingsUpdate: %v", err)
	}
	if !next.Enabled || next.MITMEnabled {
		t.Fatalf("next = %+v, want enabled with HTTPS capture off", next)
	}
}

func TestDeveloperSettingsRequiresAckForStaleHTTPSCaptureFlag(t *testing.T) {
	current := config.DefaultDeveloperConfig()
	current.Enabled = false
	current.MITMEnabled = true
	enabled := true
	mitmEnabled := true

	_, err := applyDeveloperSettingsUpdate(current, updateDeveloperSettingsRequest{
		Enabled:     &enabled,
		MITMEnabled: &mitmEnabled,
	})
	if err == nil || !strings.Contains(err.Error(), "https_capture_ack") {
		t.Fatalf("applyDeveloperSettingsUpdate error = %v, want acknowledgement error", err)
	}
}

func testDeveloperSettingsConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg := testServersConfig("A")
	cfg.Developer = config.DefaultDeveloperConfig()
	cfg.Developer.CACertPath = filepath.Join(dir, "ca.pem")
	cfg.Developer.CAKeyPath = filepath.Join(dir, "ca-key.pem")
	cfg.Profiles[0].Chains[0].Servers[0].Settings = map[string]any{
		"method":   "chacha20-ietf-poly1305",
		"password": "secret",
	}
	return cfg
}
