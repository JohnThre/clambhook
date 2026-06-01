package mobile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JohnThre/clambhook/internal/config"
)

func TestTunnelNetworkSettingsJSONAppliesMobileDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clambhook.toml")
	if err := os.WriteFile(path, []byte(`
active = "default"

[[profile]]
name = "default"

  [[profile.chain]]
  name = "proxy"

    [[profile.chain.server]]
    name = "example"
    address = "example.invalid:443"
    protocol = "shadowsocks"

      [profile.chain.server.settings]
      method = "chacha20-ietf-poly1305"
      password = "secret"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	raw, err := TunnelNetworkSettingsJSON(path)
	if err != nil {
		t.Fatal(err)
	}

	var payload tunnelNetworkSettings
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.MTU != defaultTunnelMTU {
		t.Fatalf("MTU = %d, want %d", payload.MTU, defaultTunnelMTU)
	}
	if payload.RemoteAddress != "example.invalid" {
		t.Fatalf("remote address = %q, want example.invalid", payload.RemoteAddress)
	}
	if len(payload.IPv4) != 1 || payload.IPv4[0].Address != "198.18.0.1" || payload.IPv4[0].PrefixLen != 30 {
		t.Fatalf("unexpected IPv4 settings: %#v", payload.IPv4)
	}
	if len(payload.IPv6) != 1 || payload.IPv6[0].Address != "fd7a:636c:616d::1" || payload.IPv6[0].PrefixLen != 64 {
		t.Fatalf("unexpected IPv6 settings: %#v", payload.IPv6)
	}
	if len(payload.IncludedRoutes) != 2 || payload.IncludedRoutes[0] != "0.0.0.0/0" || payload.IncludedRoutes[1] != "::/0" {
		t.Fatalf("unexpected included routes: %#v", payload.IncludedRoutes)
	}
}

func TestTunnelConfigDashboardAndRuleReplacement(t *testing.T) {
	path := writeTunnelTestConfig(t)

	if err := ValidateTunnelConfig(path); err != nil {
		t.Fatalf("ValidateTunnelConfig: %v", err)
	}

	rules := []config.RuleConfig{{
		Name:           "ads",
		Action:         "block",
		DomainSuffixes: []string{"ads.example.com"},
		Networks:       []string{"tcp"},
	}}
	rawRules, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReplaceTunnelRulesJSON(path, "default", string(rawRules)); err != nil {
		t.Fatalf("ReplaceTunnelRulesJSON: %v", err)
	}

	rawDashboard, err := TunnelConfigDashboardJSON(path)
	if err != nil {
		t.Fatalf("TunnelConfigDashboardJSON: %v", err)
	}
	var payload dashboardPayload
	if err := json.Unmarshal([]byte(rawDashboard), &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload.Rules.Rules; len(got) != 1 || got[0].Name != "ads" || got[0].Action != "block" {
		t.Fatalf("rules = %#v", got)
	}
	if payload.Status.Running {
		t.Fatalf("dashboard status running = true, want false")
	}
}

func TestCreateTunnelProfileConfigJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	req := map[string]any{
		"profile_name":   "phone",
		"chain_name":     "proxy",
		"server_name":    "exit",
		"server_address": "example.invalid:443",
		"protocol":       "shadowsocks",
		"settings_toml":  "method = \"chacha20-ietf-poly1305\"\npassword = \"secret\"\n",
		"replace":        true,
	}
	rawReq, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateTunnelProfileConfigJSON(path, string(rawReq)); err != nil {
		t.Fatalf("CreateTunnelProfileConfigJSON: %v", err)
	}
	rawDashboard, err := TunnelConfigDashboardJSON(path)
	if err != nil {
		t.Fatalf("TunnelConfigDashboardJSON: %v", err)
	}
	var payload dashboardPayload
	if err := json.Unmarshal([]byte(rawDashboard), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Profiles.Active != "phone" || len(payload.Profiles.Profiles) != 1 {
		t.Fatalf("profiles = %+v", payload.Profiles)
	}
	if got := payload.Servers.Chains[0].Servers[0]; got.Name != "exit" || got.Protocol != "shadowsocks" {
		t.Fatalf("server = %+v", got)
	}
}

func TestValidateUsableTunnelConfigRejectsPlaceholder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	if err := os.WriteFile(path, []byte(`
active = "default"

[[profile]]
name = "default"

  [profile.listen.tun]
  enabled = true
  mtu = 1500
  routes = ["0.0.0.0/0", "::/0"]
  exclude_cidrs = ["127.0.0.0/8", "::1/128"]

  [[profile.chain]]
  name = "proxy"

    [[profile.chain.server]]
    name = "replace-me"
    address = "proxy.example.com:443"
    protocol = "shadowsocks"

      [profile.chain.server.settings]
      method = "chacha20-ietf-poly1305"
      password = "replace-with-secret"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	err := ValidateUsableTunnelConfig(path)
	if err == nil || !strings.Contains(err.Error(), "placeholder profile") {
		t.Fatalf("ValidateUsableTunnelConfig error = %v, want placeholder error", err)
	}
}

func TestValidateUsableTunnelConfigAcceptsRealProfile(t *testing.T) {
	path := writeTunnelTestConfig(t)

	if err := ValidateUsableTunnelConfig(path); err != nil {
		t.Fatalf("ValidateUsableTunnelConfig: %v", err)
	}
}

func TestSetActiveTunnelProfileConfig(t *testing.T) {
	path := writeMultiProfileTunnelTestConfig(t)

	if err := SetActiveTunnelProfileConfig(path, "backup"); err != nil {
		t.Fatalf("SetActiveTunnelProfileConfig: %v", err)
	}

	rawDashboard, err := TunnelConfigDashboardJSON(path)
	if err != nil {
		t.Fatalf("TunnelConfigDashboardJSON: %v", err)
	}
	var payload dashboardPayload
	if err := json.Unmarshal([]byte(rawDashboard), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Profiles.Active != "backup" || payload.Status.Profile != "backup" {
		t.Fatalf("active profile = %+v status=%+v, want backup", payload.Profiles, payload.Status)
	}
}

func TestSetActiveTunnelProfileConfigRejectsUnknownProfile(t *testing.T) {
	path := writeMultiProfileTunnelTestConfig(t)

	err := SetActiveTunnelProfileConfig(path, "missing")
	if err == nil || !strings.Contains(err.Error(), "profile \"missing\" not found") {
		t.Fatalf("SetActiveTunnelProfileConfig error = %v, want missing profile error", err)
	}

	rawDashboard, dashErr := TunnelConfigDashboardJSON(path)
	if dashErr != nil {
		t.Fatalf("TunnelConfigDashboardJSON: %v", dashErr)
	}
	var payload dashboardPayload
	if err := json.Unmarshal([]byte(rawDashboard), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Profiles.Active != "default" {
		t.Fatalf("active profile = %q, want default", payload.Profiles.Active)
	}
}

func writeTunnelTestConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	if err := os.WriteFile(path, []byte(`
active = "default"

[[profile]]
name = "default"

  [[profile.chain]]
  name = "proxy"

    [[profile.chain.server]]
    name = "example"
    address = "example.invalid:443"
    protocol = "shadowsocks"

      [profile.chain.server.settings]
      method = "chacha20-ietf-poly1305"
      password = "secret"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeMultiProfileTunnelTestConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	if err := os.WriteFile(path, []byte(`
active = "default"

[[profile]]
name = "default"

  [[profile.chain]]
  name = "proxy"

    [[profile.chain.server]]
    name = "primary"
    address = "primary.example.invalid:443"
    protocol = "shadowsocks"

      [profile.chain.server.settings]
      method = "chacha20-ietf-poly1305"
      password = "secret"

[[profile]]
name = "backup"

  [[profile.chain]]
  name = "proxy"

    [[profile.chain.server]]
    name = "secondary"
    address = "secondary.example.invalid:443"
    protocol = "shadowsocks"

      [profile.chain.server.settings]
      method = "chacha20-ietf-poly1305"
      password = "secret"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
