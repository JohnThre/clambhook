package mobile

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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

func TestMobileHTTPProxyConfigDisablesPacketAndSOCKSListeners(t *testing.T) {
	cfg := &config.Config{
		Path:   "/tmp/clambhook.toml",
		Active: "default",
		Profiles: []config.Profile{{
			Name: "default",
			Listen: config.ListenConfig{
				SOCKS5:      "127.0.0.1:1080",
				SOCKS5Chain: "proxy",
				HTTP:        "127.0.0.1:18080",
				HTTPChain:   "proxy",
				TUN:         &config.TUNConfig{Enabled: true},
			},
			Chains: []config.ChainConfig{{
				Name: "proxy",
				Servers: []config.ServerConfig{{
					Name:     "example",
					Address:  "example.invalid:443",
					Protocol: "shadowsocks",
					Settings: map[string]any{
						"method":   "chacha20-ietf-poly1305",
						"password": "secret",
					},
				}},
			}},
		}},
	}

	proxyCfg := mobileHTTPProxyConfig(cfg)
	if proxyCfg == nil {
		t.Fatal("mobileHTTPProxyConfig returned nil")
	}
	profile, err := proxyCfg.ActiveProfile()
	if err != nil {
		t.Fatal(err)
	}
	if profile.Listen.HTTP != "127.0.0.1:18080" || profile.Listen.HTTPChain != "proxy" {
		t.Fatalf("http listener = %+v", profile.Listen)
	}
	if profile.Listen.TUN != nil || profile.Listen.SOCKS5 != "" || profile.Listen.SOCKS5Chain != "" {
		t.Fatalf("unexpected non-http listeners in proxy config: %+v", profile.Listen)
	}
}

func TestLoadTunnelConfigDisablesDeveloperCapture(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clambhook.toml")
	if err := os.WriteFile(path, []byte(`
active = "default"

[developer]
enabled = true
mitm_enabled = true
capture_limit = 10
body_limit_bytes = 1024

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

	cfg, err := loadTunnelConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Developer.Enabled {
		t.Fatalf("developer enabled = true, want false")
	}
	if cfg.Developer.BodyLimitBytes != config.DefaultDeveloperConfig().BodyLimitBytes {
		t.Fatalf("body limit = %d, want mobile default disabled config", cfg.Developer.BodyLimitBytes)
	}
}

func TestTunnelNetworkSettingsJSONIncludesDNSServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clambhook.toml")
	if err := os.WriteFile(path, []byte(`
active = "default"

[[profile]]
name = "default"

  [profile.dns]
  enabled = true

    [[profile.dns.upstream]]
    name = "cloudflare"
    protocol = "doh"
    url = "https://cloudflare-dns.com/dns-query"

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
	if len(payload.DNSServers) != 2 || payload.DNSServers[0] != "198.18.0.1" || payload.DNSServers[1] != "fd7a:636c:616d::1" {
		t.Fatalf("dns servers = %#v, want tunnel addresses", payload.DNSServers)
	}
}

func TestTunnelConfigDashboardJSONIncludesNetworkSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clambhook.toml")
	if err := os.WriteFile(path, []byte(`
active = "default"

[[profile]]
name = "default"

  [profile.listen]
  http = "127.0.0.1:18080"
  http_chain = "proxy"

  [profile.listen.tun]
  enabled = true
  mtu = 1400
  routes = ["10.0.0.0/8"]
  exclude_cidrs = ["10.1.0.0/16"]

  [profile.dns]
  enabled = true

    [[profile.dns.upstream]]
    name = "cloudflare"
    protocol = "doh"
    url = "https://cloudflare-dns.com/dns-query"

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

	rawDashboard, err := TunnelConfigDashboardJSON(path)
	if err != nil {
		t.Fatalf("TunnelConfigDashboardJSON: %v", err)
	}
	var payload dashboardPayload
	if err := json.Unmarshal([]byte(rawDashboard), &payload); err != nil {
		t.Fatal(err)
	}
	settings := payload.NetworkSettings
	if settings.MTU != 1400 {
		t.Fatalf("MTU = %d, want 1400", settings.MTU)
	}
	if len(settings.DNSServers) != 2 || settings.DNSServers[0] != "198.18.0.1" || settings.DNSServers[1] != "fd7a:636c:616d::1" {
		t.Fatalf("dns servers = %#v, want tunnel addresses", settings.DNSServers)
	}
	if len(settings.IncludedRoutes) != 1 || settings.IncludedRoutes[0] != "10.0.0.0/8" {
		t.Fatalf("included routes = %#v, want 10.0.0.0/8", settings.IncludedRoutes)
	}
	if len(settings.ExcludedRoutes) != 1 || settings.ExcludedRoutes[0] != "10.1.0.0/16" {
		t.Fatalf("excluded routes = %#v, want 10.1.0.0/16", settings.ExcludedRoutes)
	}
	if settings.HTTPProxy == nil || settings.HTTPProxy.Host != "127.0.0.1" || settings.HTTPProxy.Port != 18080 {
		t.Fatalf("http proxy = %#v, want 127.0.0.1:18080", settings.HTTPProxy)
	}
	if !payload.DNS.Enabled || payload.DNS.Strategy != "encrypted" || len(payload.DNS.Upstreams) != 1 {
		t.Fatalf("dns = %+v, want encrypted DNS snapshot", payload.DNS)
	}
	if len(payload.DNS.UpstreamRoutes) != 1 || payload.DNS.UpstreamRoutes[0].Target != "cloudflare-dns.com:443" {
		t.Fatalf("dns upstream routes = %+v, want cloudflare target", payload.DNS.UpstreamRoutes)
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

func TestTunnelConfigRuleTestJSONExplainsPolicyGroupSelection(t *testing.T) {
	path := writeTunnelTestConfig(t)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Profiles[0].Chains = append(cfg.Profiles[0].Chains, config.ChainConfig{
		Name: "backup",
		Servers: []config.ServerConfig{{
			Name:     "backup",
			Address:  "backup.example.invalid:443",
			Protocol: "shadowsocks",
			Settings: map[string]any{
				"method":   "chacha20-ietf-poly1305",
				"password": "secret",
			},
		}},
	})
	cfg.Profiles[0].PolicyGroups = []config.PolicyGroupConfig{{
		Name:     "manual",
		Type:     "select",
		Chains:   []string{"proxy", "backup"},
		Selected: "backup",
	}}
	cfg.Profiles[0].Rules = []config.RuleConfig{{
		Name:           "grouped",
		Action:         "group:manual",
		DomainSuffixes: []string{"example.com"},
	}}
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write rule test config: %v", err)
	}

	rawTest, err := TestRuleJSON(path, "", "tcp", "www.example.com:443", "")
	if err != nil {
		t.Fatalf("TestRuleJSON: %v", err)
	}
	var payload ruleTestResponse
	if err := json.Unmarshal([]byte(rawTest), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Profile != "default" || payload.Decision.RuleName != "grouped" || payload.Decision.ChainName != "backup" {
		t.Fatalf("rule test payload = %+v", payload)
	}
	if payload.Chain == nil || payload.Chain.Name != "backup" || len(payload.Hops) != 1 || payload.Hops[0].Name != "backup" {
		t.Fatalf("route chain = %+v hops=%+v", payload.Chain, payload.Hops)
	}
}

func TestTunnelConfigDashboardJSONIncludesPolicyGroups(t *testing.T) {
	path := writeTunnelTestConfig(t)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Profiles[0].Chains = append(cfg.Profiles[0].Chains, config.ChainConfig{
		Name: "backup",
		Servers: []config.ServerConfig{{
			Name:     "backup",
			Address:  "backup.example.invalid:443",
			Protocol: "shadowsocks",
			Settings: map[string]any{
				"method":   "chacha20-ietf-poly1305",
				"password": "secret",
			},
		}},
	})
	cfg.Profiles[0].PolicyGroups = []config.PolicyGroupConfig{{
		Name:   "auto",
		Type:   "url-test",
		Chains: []string{"proxy", "backup"},
	}}
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write policy group config: %v", err)
	}

	rawDashboard, err := TunnelConfigDashboardJSON(path)
	if err != nil {
		t.Fatalf("TunnelConfigDashboardJSON: %v", err)
	}
	var payload dashboardPayload
	if err := json.Unmarshal([]byte(rawDashboard), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.PolicyGroups.Profile != "default" || len(payload.PolicyGroups.Groups) != 1 {
		t.Fatalf("policy groups = %+v", payload.PolicyGroups)
	}
	group := payload.PolicyGroups.Groups[0]
	if group.Name != "auto" || group.SelectedChain != "proxy" || len(group.Chains) != 2 {
		t.Fatalf("policy group = %+v", group)
	}
}

func TestTunnelRuntimeDashboardJSONIncludesLivePolicyGroups(t *testing.T) {
	path := writeTunnelTestConfig(t)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Profiles[0].PolicyGroups = []config.PolicyGroupConfig{{
		Name:   "auto",
		Type:   "url-test",
		Chains: []string{"proxy"},
	}}
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write policy group config: %v", err)
	}
	runtime := NewTunnelRuntime(discardPacketWriter{})
	if err := runtime.Start(path); err != nil {
		t.Fatalf("runtime Start: %v", err)
	}
	defer runtime.Stop()

	rawDashboard, err := runtime.DashboardJSON()
	if err != nil {
		t.Fatalf("DashboardJSON: %v", err)
	}
	var payload dashboardPayload
	if err := json.Unmarshal([]byte(rawDashboard), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.PolicyGroups.Profile != "default" || len(payload.PolicyGroups.Groups) != 1 {
		t.Fatalf("policy groups = %+v", payload.PolicyGroups)
	}
	if got := payload.PolicyGroups.Groups[0].SelectedChain; got != "proxy" {
		t.Fatalf("selected chain = %q, want proxy", got)
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
	_, port, _ := net.SplitHostPort(source.Listener.Addr().String())
	cfg.Profiles[0].RuleSubscriptions = []config.RuleSubscriptionConfig{{
		Name: "ads",
		URL:  "http://93.184.216.34:" + port + "/",
	}}
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write subscription config: %v", err)
	}

	// The public mobile helper does not accept a custom client, so we call
	// subscription.RefreshProfile directly with a transport that dials the
	// local test server. The config is already written, so dashboard output
	// is exercised end-to-end below.
	dialAddr := source.Listener.Addr().String()
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, dialAddr)
			},
		},
	}
	refresh, err := subscription.RefreshProfile(context.Background(), cfg, "", nil, client)
	if err != nil {
		t.Fatalf("RefreshProfile: %v", err)
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

func TestValidateSupportDemoProfile(t *testing.T) {
	path := strings.TrimSpace(os.Getenv("CLAMBHOOK_SUPPORT_DEMO_CONFIG"))
	if path == "" {
		t.Skip("set CLAMBHOOK_SUPPORT_DEMO_CONFIG to validate the rendered support demo profile")
	}
	if err := ValidateUsableTunnelConfig(path); err != nil {
		t.Fatalf("ValidateUsableTunnelConfig(%q): %v", path, err)
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

func writeSelectPolicyGroupTestConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	if err := os.WriteFile(path, []byte(`
active = "default"

[[profile]]
name = "default"

  [[profile.policy_group]]
  name = "manual"
  type = "select"
  chains = ["proxy", "backup"]
  selected = "proxy"

  [[profile.chain]]
  name = "proxy"

    [[profile.chain.server]]
    name = "example"
    address = "example.invalid:443"
    protocol = "shadowsocks"

      [profile.chain.server.settings]
      method = "chacha20-ietf-poly1305"
      password = "secret"

  [[profile.chain]]
  name = "backup"

    [[profile.chain.server]]
    name = "backup"
    address = "backup.invalid:443"
    protocol = "shadowsocks"

      [profile.chain.server.settings]
      method = "chacha20-ietf-poly1305"
      password = "secret"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func dashboardSelectedChain(t *testing.T, runtime *TunnelRuntime, group string) string {
	t.Helper()
	raw, err := runtime.DashboardJSON()
	if err != nil {
		t.Fatalf("DashboardJSON: %v", err)
	}
	var payload dashboardPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}
	for _, g := range payload.PolicyGroups.Groups {
		if g.Name == group {
			return g.SelectedChain
		}
	}
	t.Fatalf("policy group %q not found in dashboard %+v", group, payload.PolicyGroups)
	return ""
}

// TestSelectPolicyGroupIsRaceCleanWithConcurrentReaders drives SelectPolicyGroup
// against concurrent DashboardJSON/ProfilesJSON/RulesJSON reads. Before the deep
// copy fix, SelectPolicyGroup shallow-mutated the live config's policy-group
// backing array outside the mutex, which the race detector flags here.
func TestSelectPolicyGroupIsRaceCleanWithConcurrentReaders(t *testing.T) {
	path := writeSelectPolicyGroupTestConfig(t)
	runtime := NewTunnelRuntime(discardPacketWriter{})
	if err := runtime.Start(path); err != nil {
		t.Fatalf("runtime Start: %v", err)
	}
	defer runtime.Stop()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	reader := func(read func() (string, error)) {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := read(); err != nil {
				t.Errorf("reader: %v", err)
				return
			}
		}
	}

	wg.Add(3)
	go reader(runtime.DashboardJSON)
	go reader(runtime.ProfilesJSON)
	go reader(runtime.RulesJSON)

	chains := []string{"proxy", "backup"}
	for i := 0; i < 12; i++ {
		if err := runtime.SelectPolicyGroup("default", "manual", chains[i%2]); err != nil {
			t.Fatalf("SelectPolicyGroup: %v", err)
		}
	}

	close(stop)
	wg.Wait()

	if got := dashboardSelectedChain(t, runtime, "manual"); got != "backup" {
		t.Fatalf("selected chain = %q, want backup", got)
	}
}

func TestSelectPolicyGroupRuntimeMatchesPersistedSelection(t *testing.T) {
	persistedPath := writeSelectPolicyGroupTestConfig(t)
	rawPersisted, err := SelectPolicyGroupJSON(persistedPath, "", " manual ", " backup ")
	if err != nil {
		t.Fatalf("SelectPolicyGroupJSON: %v", err)
	}
	var persisted struct {
		Groups []struct {
			Name          string `json:"name"`
			SelectedChain string `json:"selected_chain"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(rawPersisted), &persisted); err != nil {
		t.Fatalf("decode persisted policy groups: %v", err)
	}
	if len(persisted.Groups) != 1 || persisted.Groups[0].Name != "manual" {
		t.Fatalf("persisted policy groups = %+v", persisted.Groups)
	}

	runtime := NewTunnelRuntime(discardPacketWriter{})
	if err := runtime.Start(writeSelectPolicyGroupTestConfig(t)); err != nil {
		t.Fatalf("runtime Start: %v", err)
	}
	defer runtime.Stop()
	if err := runtime.SelectPolicyGroup("", " manual ", " backup "); err != nil {
		t.Fatalf("runtime SelectPolicyGroup: %v", err)
	}
	if got, want := dashboardSelectedChain(t, runtime, "manual"), persisted.Groups[0].SelectedChain; got != want {
		t.Fatalf("runtime selected chain = %q, persisted selected chain = %q", got, want)
	}
}

// TestSelectPolicyGroupValidationFailurePreservesLiveState verifies validation
// runs against a private candidate. The intentionally invalid live config lets
// selection reach engine.ValidateConfig; the old shallow copy changed Selected
// before validation rejected the candidate.
func TestSelectPolicyGroupValidationFailurePreservesLiveState(t *testing.T) {
	cfg, err := config.Load(writeSelectPolicyGroupTestConfig(t))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.Profiles[0].PolicyGroups[0].Timeout = -1
	runtime := NewTunnelRuntime(discardPacketWriter{})
	runtime.cfg = cfg

	statusBefore, err := runtime.StatusJSON()
	if err != nil {
		t.Fatalf("StatusJSON before: %v", err)
	}
	dashboardBefore, err := runtime.DashboardJSON()
	if err != nil {
		t.Fatalf("DashboardJSON before: %v", err)
	}
	if got := dashboardSelectedChain(t, runtime, "manual"); got != "proxy" {
		t.Fatalf("selected chain before = %q, want proxy", got)
	}

	err = runtime.SelectPolicyGroup("default", "manual", "backup")
	if err == nil || !strings.Contains(err.Error(), "timeout must be >= 0") {
		t.Fatalf("SelectPolicyGroup error = %v, want validation failure", err)
	}

	statusAfter, err := runtime.StatusJSON()
	if err != nil {
		t.Fatalf("StatusJSON after: %v", err)
	}
	dashboardAfter, err := runtime.DashboardJSON()
	if err != nil {
		t.Fatalf("DashboardJSON after: %v", err)
	}
	if statusAfter != statusBefore {
		t.Fatalf("status changed after validation failure: before=%s after=%s", statusBefore, statusAfter)
	}
	var beforePayload, afterPayload dashboardPayload
	if err := json.Unmarshal([]byte(dashboardBefore), &beforePayload); err != nil {
		t.Fatalf("decode dashboard before: %v", err)
	}
	if err := json.Unmarshal([]byte(dashboardAfter), &afterPayload); err != nil {
		t.Fatalf("decode dashboard after: %v", err)
	}
	// Dashboard traffic snapshots are timestamped at read time; normalize that
	// volatile field before comparing all observable dashboard state.
	beforePayload.Traffic.UpdatedTsNs = 0
	afterPayload.Traffic.UpdatedTsNs = 0
	if !reflect.DeepEqual(afterPayload, beforePayload) {
		t.Fatalf("dashboard changed after validation failure: before=%+v after=%+v", beforePayload, afterPayload)
	}
	if got := dashboardSelectedChain(t, runtime, "manual"); got != "proxy" {
		t.Fatalf("selected chain after = %q, want proxy preserved", got)
	}
}
