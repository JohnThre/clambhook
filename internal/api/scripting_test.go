package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
	"github.com/JohnThre/clambhook/internal/scripting"
)

func writeTestModulesConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestModulesAPI(t *testing.T) {
	dir := t.TempDir()
	path := writeTestModulesConfig(t, dir, testConfigWithModule("mod1", "function onRequest(req){ $done(req); }"))
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	scr, err := scripting.NewManager(cfg.Modules, filepath.Join(dir, "scripting"))
	if err != nil {
		t.Fatalf("scripting manager: %v", err)
	}
	srv := NewWithOptions(engine.New(cfg, nil), nil, Options{
		ConfigPath: path,
		Scripting:  scr,
	})

	// GET modules
	req := httptest.NewRequest("GET", "/api/v1/modules", nil)
	rec := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET modules: %d", rec.Code)
	}
	var got modulesPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode modules: %v", err)
	}
	if len(got.Modules) != 1 {
		t.Fatalf("modules len = %d, want 1", len(got.Modules))
	}
	if got.Modules[0].Name != "mod1" {
		t.Fatalf("module name = %q, want mod1", got.Modules[0].Name)
	}
	if !got.Modules[0].HasRequest {
		t.Fatal("expected HasRequest true")
	}

	// PUT modules
	newModules := []config.ModuleConfig{{
		Name:    "mod2",
		Enabled: true,
		Script:  "function onRequest(req){ req.headers['X-T'] = '1'; $done(req); }",
	}}
	body, _ := json.Marshal(replaceModulesRequest{Modules: newModules})
	req = httptest.NewRequest("PUT", "/api/v1/modules", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT modules: %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode modules: %v", err)
	}
	if len(got.Modules) != 1 || got.Modules[0].Name != "mod2" {
		t.Fatalf("unexpected modules after PUT: %+v", got.Modules)
	}

	// Verify persisted.
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if len(reloaded.Modules) != 1 || reloaded.Modules[0].Name != "mod2" {
		t.Fatalf("config not persisted: %+v", reloaded.Modules)
	}
}

func testConfigWithModule(name, script string) string {
	return `active = "default"

[[profile]]
name = "default"

  [profile.listen]
  socks5 = "127.0.0.1:0"

  [profile.api]
  listen = "127.0.0.1:0"

  [[profile.chain]]
  name = "ss"

    [[profile.chain.server]]
    name = "ss"
    address = "127.0.0.1:12345"
    protocol = "shadowsocks"

      [profile.chain.server.settings]
      method = "aes-128-gcm"
      password = "pass"

[[module]]
name = "` + name + `"
enabled = true
script = """` + script + `"""
`
}
