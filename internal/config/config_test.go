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

func TestLoadRules(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	data := []byte(`
active = "default"

[[profile]]
name = "default"

  [[profile.chain]]
  name = "default"

    [[profile.chain.server]]
    name = "exit"
    address = "203.0.113.10:443"
    protocol = "trojan"

      [profile.chain.server.settings]
      password = "secret"

  [[profile.rule]]
  name = "ads"
  action = "block"
  domain_suffixes = ["ads.example.com"]
  ports = [80, 443]
  networks = ["tcp"]
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
	if len(profile.Rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(profile.Rules))
	}
	rule := profile.Rules[0]
	if rule.Name != "ads" || rule.Action != "block" || rule.DomainSuffixes[0] != "ads.example.com" {
		t.Fatalf("rule = %+v", rule)
	}
}

func TestLoadDNSConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	data := []byte(`
active = "default"

[[profile]]
name = "default"

  [profile.dns]
  enabled = true
  timeout = "3s"

    [[profile.dns.upstream]]
    name = "cloudflare"
    protocol = "doh"
    url = "https://cloudflare-dns.com/dns-query"
    server_name = "cloudflare-dns.com"
    bootstrap_ips = ["1.1.1.1", "2606:4700:4700::1111"]

    [[profile.dns.upstream]]
    name = "quad9"
    protocol = "dot"
    address = "9.9.9.9:853"

    [[profile.dns.upstream]]
    name = "adguard"
    protocol = "doq"
    address = "94.140.14.14:853"

  [[profile.chain]]
  name = "default"

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
	if !profile.DNS.Enabled {
		t.Fatal("DNS.Enabled = false, want true")
	}
	if profile.DNS.Timeout.Std().String() != "3s" {
		t.Fatalf("DNS.Timeout = %s, want 3s", profile.DNS.Timeout.Std())
	}
	if len(profile.DNS.Upstreams) != 3 {
		t.Fatalf("DNS.Upstreams = %d, want 3", len(profile.DNS.Upstreams))
	}
	if got := profile.DNS.Upstreams[0].URL; got != "https://cloudflare-dns.com/dns-query" {
		t.Fatalf("first upstream URL = %q", got)
	}
	if got := profile.DNS.Upstreams[0].BootstrapIPs; len(got) != 2 || got[0] != "1.1.1.1" {
		t.Fatalf("bootstrap IPs = %#v", got)
	}
}

func TestValidateRejectsBadDNSConfig(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{
			name: "enabled without upstreams",
			edit: func(cfg *Config) {
				cfg.Profiles[0].DNS.Enabled = true
			},
			want: "at least one upstream",
		},
		{
			name: "bad protocol",
			edit: func(cfg *Config) {
				cfg.Profiles[0].DNS = DNSConfig{
					Enabled: true,
					Upstreams: []DNSUpstreamConfig{{
						Protocol: "udp",
						Address:  "1.1.1.1:53",
					}},
				}
			},
			want: "must be doh, dot, or doq",
		},
		{
			name: "doh requires https",
			edit: func(cfg *Config) {
				cfg.Profiles[0].DNS = DNSConfig{
					Enabled: true,
					Upstreams: []DNSUpstreamConfig{{
						Protocol: "doh",
						URL:      "http://dns.example/dns-query",
					}},
				}
			},
			want: "must use https",
		},
		{
			name: "dot requires address",
			edit: func(cfg *Config) {
				cfg.Profiles[0].DNS = DNSConfig{
					Enabled: true,
					Upstreams: []DNSUpstreamConfig{{
						Protocol: "dot",
						URL:      "https://dns.example/dns-query",
					}},
				}
			},
			want: "address is required for dot",
		},
		{
			name: "bad bootstrap",
			edit: func(cfg *Config) {
				cfg.Profiles[0].DNS = DNSConfig{
					Enabled: true,
					Upstreams: []DNSUpstreamConfig{{
						Protocol:     "doq",
						Address:      "dns.example:853",
						BootstrapIPs: []string{"not-an-ip"},
					}},
				}
			},
			want: "bootstrap_ips[0]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.edit(cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestLoadRuleSubscriptions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	data := []byte(`
active = "default"

[[profile]]
name = "default"

  [[profile.chain]]
  name = "default"

    [[profile.chain.server]]
    name = "exit"
    address = "203.0.113.10:443"
    protocol = "trojan"

      [profile.chain.server.settings]
      password = "secret"

  [[profile.rule_subscription]]
  name = "ads"
  url = "https://lists.example.invalid/ads.txt"
  format = "adblock"
  action = "reject"
  networks = ["tcp"]
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Path != path {
		t.Fatalf("Path = %q, want %q", cfg.Path, path)
	}
	profile, err := cfg.ActiveProfile()
	if err != nil {
		t.Fatalf("ActiveProfile: %v", err)
	}
	if len(profile.RuleSubscriptions) != 1 {
		t.Fatalf("rule subscriptions = %d, want 1", len(profile.RuleSubscriptions))
	}
	sub := profile.RuleSubscriptions[0]
	if sub.Name != "ads" || sub.Format != "adblock" || sub.Action != "reject" || sub.Networks[0] != "tcp" {
		t.Fatalf("subscription = %+v", sub)
	}
}

func TestValidateRejectsBadRuleChainReference(t *testing.T) {
	cfg := validConfig()
	cfg.Profiles[0].Rules = []RuleConfig{{Name: "missing", Action: "chain:missing"}}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), `action references unknown chain "missing"`) {
		t.Fatalf("Validate error = %v, want unknown rule chain reference", err)
	}
}

func TestValidateRejectsBadRuleSubscription(t *testing.T) {
	cfg := validConfig()
	cfg.Profiles[0].RuleSubscriptions = []RuleSubscriptionConfig{{
		Name:   "ads",
		URL:    "file:///tmp/ads.txt",
		Format: "yaml",
		Action: "direct",
	}}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate error = nil, want rule subscription errors")
	}
	for _, want := range []string{"valid http or https URL", "format", "action"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Validate error = %v, want %q", err, want)
		}
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
