package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTUNConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	data := []byte(`
active = "default"

[[profile]]
name = "default"

  [profile.listen.tun]
  enabled = true
  name = "clambhook-test0"
  chain = "main"
  mtu = 1400
  addresses = ["198.18.0.1/30"]
  routes = ["0.0.0.0/1", "128.0.0.0/1"]
  exclude_cidrs = ["10.0.0.0/8"]

  [[profile.chain]]
  name = "main"

    [[profile.chain.server]]
    name = "exit"
    address = "203.0.113.10:443"
    protocol = "trojan"

      [profile.chain.server.settings]
      password = "secret"
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	profile, err := cfg.ActiveProfile()
	if err != nil {
		t.Fatalf("ActiveProfile: %v", err)
	}
	if profile.Listen.TUN == nil {
		t.Fatal("Listen.TUN is nil")
	}
	tun := profile.Listen.TUN
	if !tun.Enabled {
		t.Error("TUN.Enabled = false, want true")
	}
	if tun.Name != "clambhook-test0" || tun.Chain != "main" || tun.MTU != 1400 {
		t.Errorf("unexpected TUN config: %+v", tun)
	}
	if got := tun.Addresses; len(got) != 1 || got[0] != "198.18.0.1/30" {
		t.Errorf("Addresses = %#v", got)
	}
	if got := tun.ExcludeCIDRs; len(got) != 1 || got[0] != "10.0.0.0/8" {
		t.Errorf("ExcludeCIDRs = %#v", got)
	}
}
