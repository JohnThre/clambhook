package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
	_ "github.com/JohnThre/clambhook/internal/protocol/shadowsocks"
)

func conditionerTestConfig() *config.Config {
	cfg := testServersConfig("A")
	cfg.Profiles[0].Chains[0].Servers[0].Settings = map[string]any{
		"method":   "chacha20-ietf-poly1305",
		"password": "secret",
	}
	return cfg
}

func TestConditionerGetReturnsProfileSnapshot(t *testing.T) {
	cfg := conditionerTestConfig()
	cfg.Profiles[0].Conditioner = config.ConditionerConfig{
		Enabled:      true,
		DownloadKbps: 2048,
		UploadKbps:   1024,
		Latency:      config.Duration(50_000_000), // 50ms
		LossPercent:  1.5,
	}
	srv := New(engine.New(cfg, nil), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/conditioner", nil)
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var resp conditionerPayload
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Profile != "A" || !resp.Enabled || resp.DownloadKbps != 2048 || resp.UploadKbps != 1024 {
		t.Fatalf("snapshot = %+v", resp)
	}
	if resp.Latency != "50ms" || resp.LossPercent != 1.5 {
		t.Fatalf("latency/loss = %s/%v", resp.Latency, resp.LossPercent)
	}
}

func TestConditionerPutPersistsAndAppliesLive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	cfg := conditionerTestConfig()
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	eng := engine.New(cfg, nil)
	srv := NewWithOptions(eng, nil, Options{ConfigPath: path})

	body := []byte(`{
		"enabled": true,
		"download_kbps": 512,
		"upload_kbps": 256,
		"latency": "40ms",
		"jitter": "10ms",
		"loss_percent": 2.5
	}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/conditioner", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var resp conditionerPayload
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.BackupPath == "" {
		t.Fatalf("backup_path empty in response %+v", resp)
	}
	if !resp.Enabled || resp.DownloadKbps != 512 || resp.UploadKbps != 256 || resp.Latency != "40ms" || resp.Jitter != "10ms" || resp.LossPercent != 2.5 {
		t.Fatalf("response = %+v", resp)
	}

	// Persisted to disk.
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("load persisted config: %v", err)
	}
	profile, err := reloaded.ActiveProfile()
	if err != nil {
		t.Fatal(err)
	}
	c := profile.Conditioner
	if !c.Enabled || c.DownloadKbps != 512 || c.UploadKbps != 256 || c.LossPercent != 2.5 {
		t.Fatalf("persisted conditioner = %+v", c)
	}
	if c.Latency.Std().String() != "40ms" || c.Jitter.Std().String() != "10ms" {
		t.Fatalf("persisted latency/jitter = %s/%s", c.Latency.Std(), c.Jitter.Std())
	}

	// Applied live to the running engine config.
	engineProfile, err := eng.Config().ActiveProfile()
	if err != nil {
		t.Fatal(err)
	}
	if !engineProfile.Conditioner.Enabled || engineProfile.Conditioner.DownloadKbps != 512 {
		t.Fatalf("engine conditioner after PUT = %+v", engineProfile.Conditioner)
	}
}

func TestConditionerPutPartialLeavesOtherFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	cfg := conditionerTestConfig()
	cfg.Profiles[0].Conditioner = config.ConditionerConfig{
		Enabled:      true,
		DownloadKbps: 1000,
		UploadKbps:   500,
	}
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	srv := NewWithOptions(engine.New(cfg, nil), nil, Options{ConfigPath: path})

	// Only toggle enabled off; bandwidth must be preserved.
	body := []byte(`{"enabled": false}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/conditioner", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	var resp conditionerPayload
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Enabled {
		t.Fatalf("enabled should be false, got %+v", resp)
	}
	if resp.DownloadKbps != 1000 || resp.UploadKbps != 500 {
		t.Fatalf("partial PUT dropped bandwidth: %+v", resp)
	}
}

func TestConditionerPutUnknownProfile404(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	cfg := conditionerTestConfig()
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	srv := NewWithOptions(engine.New(cfg, nil), nil, Options{ConfigPath: path})

	body := []byte(`{"profile":"missing","enabled":true}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/conditioner", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%q, want 404", rec.Code, rec.Body.String())
	}
}

func TestConditionerPutRejectsBadLossPercent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	cfg := conditionerTestConfig()
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	srv := NewWithOptions(engine.New(cfg, nil), nil, Options{ConfigPath: path})

	body := []byte(`{"loss_percent": 150}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/conditioner", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%q, want 400", rec.Code, rec.Body.String())
	}
}
