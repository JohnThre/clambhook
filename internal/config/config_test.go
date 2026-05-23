package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestValidateRejectsActiveProfileTypo(t *testing.T) {
	cfg := validConfig()
	cfg.Active = "missing"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), `active profile "missing" not found`) {
		t.Fatalf("Validate error = %v, want active profile error", err)
	}
}

func TestValidateRejectsDuplicateNames(t *testing.T) {
	cfg := validConfig()
	cfg.Profiles = append(cfg.Profiles, cfg.Profiles[0])

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate profile name") {
		t.Fatalf("Validate error = %v, want duplicate profile name", err)
	}

	cfg = validConfig()
	cfg.Profiles[0].Chains = append(cfg.Profiles[0].Chains, cfg.Profiles[0].Chains[0])
	err = cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate chain name") {
		t.Fatalf("Validate error = %v, want duplicate chain name", err)
	}
}

func TestValidateRejectsBadListenerChainReference(t *testing.T) {
	cfg := validConfig()
	cfg.Profiles[0].Listen.SOCKS5Chain = "missing"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), `listen.socks5_chain references unknown chain "missing"`) {
		t.Fatalf("Validate error = %v, want unknown chain reference", err)
	}
}

func TestValidateRejectsBadListenAddress(t *testing.T) {
	cfg := validConfig()
	cfg.Profiles[0].Listen.SOCKS5 = "127.0.0.1"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "must be host:port") {
		t.Fatalf("Validate error = %v, want host:port error", err)
	}
}

func TestValidateRejectsBadTUNCIDR(t *testing.T) {
	cfg := validConfig()
	cfg.Profiles[0].Listen.SOCKS5 = ""
	cfg.Profiles[0].Listen.TUN = &TUNConfig{
		Enabled:   true,
		Chain:     "default",
		Addresses: []string{"not-a-cidr"},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "listen.tun.addresses[0]") {
		t.Fatalf("Validate error = %v, want TUN CIDR error", err)
	}
}

func TestValidateAllowsWireGuardWithoutTopLevelAddress(t *testing.T) {
	cfg := validConfig()
	cfg.Profiles[0].Chains[0].Servers[0] = ServerConfig{
		Name:     "wg",
		Protocol: "wireguard",
		Settings: map[string]any{
			"private_key": "placeholder",
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func validConfig() *Config {
	return &Config{
		Active: "default",
		Profiles: []Profile{{
			Name: "default",
			Listen: ListenConfig{
				SOCKS5: "127.0.0.1:1080",
			},
			API: APIConfig{
				Listen: "127.0.0.1:9090",
			},
			Chains: []ChainConfig{{
				Name: "default",
				Servers: []ServerConfig{{
					Name:     "server",
					Address:  "203.0.113.1:443",
					Protocol: "trojan",
					Settings: map[string]any{"password": "secret"},
				}},
			}},
		}},
	}
}
