package mobile

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/subscription"
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

func TestTunnelNetworkSettingsJSONIncludesHTTPProxy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clambhook.toml")
	if err := os.WriteFile(path, []byte(`
active = "default"

[[profile]]
name = "default"

  [profile.listen]
  http = "127.0.0.1:18080"
  http_chain = "proxy"

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
	if payload.HTTPProxy == nil || payload.HTTPProxy.Host != "127.0.0.1" || payload.HTTPProxy.Port != 18080 {
		t.Fatalf("http proxy = %#v, want 127.0.0.1:18080", payload.HTTPProxy)
	}
	if payload.HTTPSProxy == nil || *payload.HTTPSProxy != *payload.HTTPProxy {
		t.Fatalf("https proxy = %#v, want %#v", payload.HTTPSProxy, payload.HTTPProxy)
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

func TestRuleSubscriptionMobileBridgeRefreshesAndReportsGeneratedRules(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ads.example.com\n"))
	}))
	defer source.Close()

	path := writeTunnelTestConfig(t)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Profiles[0].RuleSubscriptions = []config.RuleSubscriptionConfig{{
		Name: "ads",
		URL:  source.URL,
	}}
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write subscription config: %v", err)
	}

	rawRefresh, err := RefreshRuleSubscriptionsJSON(path, "", `[]`)
	if err != nil {
		t.Fatalf("RefreshRuleSubscriptionsJSON: %v", err)
	}
	var refresh subscription.StatusPayload
	if err := json.Unmarshal([]byte(rawRefresh), &refresh); err != nil {
		t.Fatal(err)
	}
	if len(refresh.Subscriptions) != 1 || refresh.Subscriptions[0].DomainCount != 1 || refresh.Subscriptions[0].LastError != "" {
		t.Fatalf("refresh = %+v", refresh)
	}

	rawDashboard, err := TunnelConfigDashboardJSON(path)
	if err != nil {
		t.Fatalf("TunnelConfigDashboardJSON: %v", err)
	}
	var dashboard dashboardPayload
	if err := json.Unmarshal([]byte(rawDashboard), &dashboard); err != nil {
		t.Fatal(err)
	}
	if len(dashboard.Rules.GeneratedRules) != 1 || dashboard.Rules.GeneratedRules[0].Name != "subscription:ads:domains" {
		t.Fatalf("generated rules = %#v", dashboard.Rules.GeneratedRules)
	}
	if len(dashboard.RuleSubscriptions.Subscriptions) != 1 || dashboard.RuleSubscriptions.Subscriptions[0].Name != "ads" {
		t.Fatalf("rule subscriptions = %+v", dashboard.RuleSubscriptions)
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

func TestCreateTunnelProfileConfigJSONAcceptsTypedSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	req := map[string]any{
		"profile_name":   "phone",
		"chain_name":     "proxy",
		"server_name":    "exit",
		"server_address": "example.invalid:443",
		"protocol":       "shadowsocks",
		"settings": map[string]any{
			"method":   "chacha20-ietf-poly1305",
			"password": "secret",
		},
		"replace": true,
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
	if got := payload.Servers.Chains[0].Servers[0]; got.Name != "exit" || got.Protocol != "shadowsocks" {
		t.Fatalf("server = %+v", got)
	}
}

func TestCreateTunnelProfileConfigJSONNormalizesNestedJSONNumbers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	validKey := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	req := map[string]any{
		"profile_name":   "phone",
		"chain_name":     "proxy",
		"server_name":    "wg",
		"server_address": "1.2.3.4:51820",
		"protocol":       "wireguard",
		"settings": map[string]any{
			"private_key": validKey,
			"addresses":   []any{"10.0.0.2/32"},
			"mtu":         1280,
			"peers": []any{
				map[string]any{
					"public_key":           validKey,
					"endpoint":             "1.2.3.4:51820",
					"allowed_ips":          []any{"0.0.0.0/0"},
					"persistent_keepalive": 25,
				},
			},
		},
		"replace": true,
	}
	rawReq, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateTunnelProfileConfigJSON(path, string(rawReq)); err != nil {
		t.Fatalf("CreateTunnelProfileConfigJSON: %v", err)
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

func TestValidateUsableTunnelConfigRejectsActiveProfileWithoutUDPSupport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	if err := os.WriteFile(path, []byte(`
active = "default"

[[profile]]
name = "default"

  [profile.listen.tun]
  enabled = true

  [[profile.chain]]
  name = "proxy"

    [[profile.chain.server]]
    name = "tor"
    address = "127.0.0.1:9050"
    protocol = "tor"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	err := ValidateUsableTunnelConfig(path)
	if err == nil {
		t.Fatalf("ValidateUsableTunnelConfig error = %v, want UDP support error", err)
	}
	lower := strings.ToLower(err.Error())
	if !strings.Contains(lower, "does not support udp") && !strings.Contains(lower, "does not carry udp") {
		t.Fatalf("ValidateUsableTunnelConfig error = %v, want UDP support error", err)
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

func TestTunnelImportReviewJSONSummarizesImportedProfiles(t *testing.T) {
	raw, err := TunnelImportReviewJSON(reviewImportConfig())
	if err != nil {
		t.Fatalf("TunnelImportReviewJSON: %v", err)
	}
	var payload tunnelImportReviewPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ActiveProfile != "imported" {
		t.Fatalf("active profile = %q, want imported", payload.ActiveProfile)
	}
	if len(payload.Profiles) != 2 {
		t.Fatalf("profiles = %#v, want 2 rows", payload.Profiles)
	}
	if got := payload.Profiles[0]; got.Name != "imported" || got.ServerCount != 1 || got.Protocols[0] != "shadowsocks" {
		t.Fatalf("first profile = %#v", got)
	}
}

func TestApplyReviewedTunnelImportMergesWithoutChangingActiveProfile(t *testing.T) {
	path := writeMultiProfileTunnelTestConfig(t)
	request := reviewedTunnelImportRequest{
		ImportText: reviewImportConfig(),
		Profiles: []reviewedTunnelImportProfile{{
			SourceName: "imported",
			TargetName: "imported-sg",
		}},
	}
	rawReq, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateReviewedTunnelImportJSON(path, string(rawReq)); err != nil {
		t.Fatalf("ValidateReviewedTunnelImportJSON: %v", err)
	}
	if err := ApplyReviewedTunnelImportJSON(path, string(rawReq)); err != nil {
		t.Fatalf("ApplyReviewedTunnelImportJSON: %v", err)
	}

	rawDashboard, err := TunnelConfigDashboardJSON(path)
	if err != nil {
		t.Fatalf("TunnelConfigDashboardJSON: %v", err)
	}
	var payload dashboardPayload
	if err := json.Unmarshal([]byte(rawDashboard), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Profiles.Active != "default" {
		t.Fatalf("active profile = %q, want default", payload.Profiles.Active)
	}
	if !containsString(payload.Profiles.Profiles, "imported-sg") {
		t.Fatalf("profiles = %#v, want imported-sg", payload.Profiles.Profiles)
	}
}

func TestApplyReviewedTunnelImportReplacesPlaceholderAndActivates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	if err := os.WriteFile(path, []byte(`
active = "default"

[[profile]]
name = "default"

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
	request := reviewedTunnelImportRequest{
		ImportText: reviewImportConfig(),
		Profiles: []reviewedTunnelImportProfile{{
			SourceName: "backup-import",
			TargetName: "phone-backup",
		}},
		ActivateProfile: "phone-backup",
	}
	rawReq, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyReviewedTunnelImportJSON(path, string(rawReq)); err != nil {
		t.Fatalf("ApplyReviewedTunnelImportJSON: %v", err)
	}
	rawDashboard, err := TunnelConfigDashboardJSON(path)
	if err != nil {
		t.Fatalf("TunnelConfigDashboardJSON: %v", err)
	}
	var payload dashboardPayload
	if err := json.Unmarshal([]byte(rawDashboard), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Profiles.Active != "phone-backup" || len(payload.Profiles.Profiles) != 1 {
		t.Fatalf("profiles = %+v, want only active phone-backup", payload.Profiles)
	}
}

func TestValidateReviewedTunnelImportRejectsExistingProfileName(t *testing.T) {
	path := writeMultiProfileTunnelTestConfig(t)
	request := reviewedTunnelImportRequest{
		ImportText: reviewImportConfig(),
		Profiles: []reviewedTunnelImportProfile{{
			SourceName: "imported",
			TargetName: "backup",
		}},
	}
	rawReq, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	err = ValidateReviewedTunnelImportJSON(path, string(rawReq))
	if err == nil || !strings.Contains(err.Error(), `profile "backup" already exists`) {
		t.Fatalf("ValidateReviewedTunnelImportJSON error = %v, want collision", err)
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

func reviewImportConfig() string {
	return `
active = "imported"

[[profile]]
name = "imported"

  [[profile.chain]]
  name = "proxy"

    [[profile.chain.server]]
    name = "exit"
    address = "import.example.invalid:443"
    protocol = "shadowsocks"

      [profile.chain.server.settings]
      method = "chacha20-ietf-poly1305"
      password = "secret"

[[profile]]
name = "backup-import"

  [[profile.chain]]
  name = "proxy"

    [[profile.chain.server]]
    name = "backup"
    address = "backup-import.example.invalid:443"
    protocol = "shadowsocks"

      [profile.chain.server.settings]
      method = "chacha20-ietf-poly1305"
      password = "secret"
`
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
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
