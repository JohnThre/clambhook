package mobile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/JohnThre/clambhook/internal/config"
)

func TestModulesJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `active = "default"

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
name = "m"
enabled = true
script = "function onRequest(req){ $done(req); }"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got, err := ModulesJSON(path)
	if err != nil {
		t.Fatalf("ModulesJSON: %v", err)
	}
	var payload struct {
		Modules []config.ModuleConfig `json:"modules"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Modules) != 1 || payload.Modules[0].Name != "m" {
		t.Fatalf("unexpected modules: %+v", payload.Modules)
	}
}

func TestReplaceModulesJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `active = "default"

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
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	modules := []config.ModuleConfig{{
		Name:    "m",
		Enabled: true,
		Script:  "function onRequest(req){ $done(req); }",
	}}
	data, _ := json.Marshal(modules)
	if err := ReplaceModulesJSON(path, string(data)); err != nil {
		t.Fatalf("ReplaceModulesJSON: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(cfg.Modules) != 1 || cfg.Modules[0].Name != "m" {
		t.Fatalf("unexpected modules after replace: %+v", cfg.Modules)
	}
}
