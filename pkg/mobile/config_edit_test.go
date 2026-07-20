package mobile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/traffic"
)

func decodeMobileRules(t *testing.T, raw string) rulesPayload {
	t.Helper()
	var payload rulesPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode rules payload: %v", err)
	}
	return payload
}

func dashboardRules(t *testing.T, path string) []config.RuleConfig {
	t.Helper()
	raw, err := TunnelConfigDashboardJSON(path)
	if err != nil {
		t.Fatalf("TunnelConfigDashboardJSON: %v", err)
	}
	var payload dashboardPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}
	return payload.Rules.Rules
}

func TestAppendTunnelRuleJSONPersistsAndReloads(t *testing.T) {
	path := writeTunnelTestConfig(t)

	ruleJSON := `{"name":"block-example-com","action":"block","domains":["example.com"]}`
	raw, err := AppendTunnelRuleJSON(path, "", ruleJSON)
	if err != nil {
		t.Fatalf("AppendTunnelRuleJSON: %v", err)
	}
	payload := decodeMobileRules(t, raw)
	if payload.Profile != "default" || len(payload.Rules) != 1 || payload.Rules[0].Name != "block-example-com" {
		t.Fatalf("append payload = %+v", payload)
	}

	// A fresh load must observe the persisted rule (reload semantics).
	persisted := dashboardRules(t, path)
	if len(persisted) != 1 || persisted[0].Name != "block-example-com" || persisted[0].Action != "block" {
		t.Fatalf("persisted rules = %+v", persisted)
	}
}

func TestAppendConnectionRuleDerivesAndDedupesName(t *testing.T) {
	path := writeTunnelTestConfig(t)
	conn := traffic.Connection{
		Profile:    "default",
		TargetHost: "api.example.com",
		RuleAction: "chain",
		ChainName:  "proxy",
	}

	raw, err := appendConnectionRuleToTunnelConfig(path, "", conn, "", "allow", "auto")
	if err != nil {
		t.Fatalf("appendConnectionRuleToTunnelConfig: %v", err)
	}
	first := decodeMobileRules(t, raw)
	if len(first.Rules) != 1 {
		t.Fatalf("rules = %+v, want one", first.Rules)
	}
	rule := first.Rules[0]
	if rule.Name != "allow-api-example-com" || rule.Action != "chain:proxy" ||
		len(rule.Domains) != 1 || rule.Domains[0] != "api.example.com" {
		t.Fatalf("derived rule = %+v", rule)
	}

	// A second identical connection rule must not collide on the persisted name.
	raw, err = appendConnectionRuleToTunnelConfig(path, "", conn, "", "allow", "auto")
	if err != nil {
		t.Fatalf("appendConnectionRuleToTunnelConfig (second): %v", err)
	}
	second := decodeMobileRules(t, raw)
	if len(second.Rules) != 2 || second.Rules[1].Name != "allow-api-example-com-2" {
		t.Fatalf("deduped rules = %+v", second.Rules)
	}

	persisted := dashboardRules(t, path)
	if len(persisted) != 2 {
		t.Fatalf("persisted rules = %+v, want two", persisted)
	}
}

func TestApplyTunnelCleanupDeletesMatchedRuleAndPersists(t *testing.T) {
	path := writeTunnelTestConfig(t)
	if _, err := AppendTunnelRuleJSON(path, "", `{"name":"old","action":"block","domains":["old.example.com"]}`); err != nil {
		t.Fatalf("seed old rule: %v", err)
	}
	if _, err := AppendTunnelRuleJSON(path, "", `{"name":"keep","action":"block","domains":["keep.example.com"]}`); err != nil {
		t.Fatalf("seed keep rule: %v", err)
	}

	suggestions := []traffic.CleanupSuggestion{{
		Kind:           "unused_in_history",
		RuleName:       "old",
		TargetRuleName: "old",
		Operation:      "delete_rule",
	}}
	raw, err := applyTunnelCleanupToConfig(path, "", suggestions, "unused_in_history", "old", "old", "delete_rule")
	if err != nil {
		t.Fatalf("applyTunnelCleanupToConfig: %v", err)
	}
	payload := decodeMobileRules(t, raw)
	if len(payload.Rules) != 1 || payload.Rules[0].Name != "keep" {
		t.Fatalf("cleanup payload = %+v", payload)
	}

	persisted := dashboardRules(t, path)
	if len(persisted) != 1 || persisted[0].Name != "keep" {
		t.Fatalf("persisted rules = %+v, want only keep", persisted)
	}
}

func TestApplyTunnelCleanupRejectsStaleSuggestion(t *testing.T) {
	path := writeTunnelTestConfig(t)
	if _, err := AppendTunnelRuleJSON(path, "", `{"name":"old","action":"block","domains":["old.example.com"]}`); err != nil {
		t.Fatalf("seed old rule: %v", err)
	}

	// No live suggestion matches: the request is a domain error, not a crash,
	// and the rule must remain persisted.
	_, err := applyTunnelCleanupToConfig(path, "", nil, "unused_in_history", "old", "old", "delete_rule")
	if err == nil {
		t.Fatal("applyTunnelCleanupToConfig returned nil error for stale suggestion")
	}
	if err.Error() != "cleanup suggestion is stale" {
		t.Fatalf("error = %q, want stale rejection", err.Error())
	}

	persisted := dashboardRules(t, path)
	if len(persisted) != 1 || persisted[0].Name != "old" {
		t.Fatalf("persisted rules = %+v, rule must survive stale cleanup", persisted)
	}
}

// writePreservationTunnelTestConfig writes a config with a non-default
// [developer] section and a non-TUN secondary profile. Neither profile declares
// a [listen.tun] stanza on disk, so the mobile TUN behavior is expected to come
// from the in-memory runtime overlay, never from persisted edits.
func writePreservationTunnelTestConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	if err := os.WriteFile(path, []byte(`
active = "default"

[developer]
enabled = true
mitm_enabled = true
capture_limit = 42
body_limit_bytes = 12345
ssl_decrypt_hosts = ["*.internal.example.com"]

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
    name = "primary"
    address = "primary.example.invalid:443"
    protocol = "shadowsocks"

      [profile.chain.server.settings]
      method = "chacha20-ietf-poly1305"
      password = "secret"

  [[profile.chain]]
  name = "backup"

    [[profile.chain.server]]
    name = "fallback"
    address = "fallback.example.invalid:443"
    protocol = "shadowsocks"

      [profile.chain.server.settings]
      method = "chacha20-ietf-poly1305"
      password = "secret"

[[profile]]
name = "secondary"

  [[profile.chain]]
  name = "proxy"

    [[profile.chain.server]]
    name = "sec"
    address = "sec.example.invalid:443"
    protocol = "shadowsocks"

      [profile.chain.server.settings]
      method = "chacha20-ietf-poly1305"
      password = "secret"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertDeveloperSectionPreserved(t *testing.T, dev config.DeveloperConfig) {
	t.Helper()
	if !dev.Enabled {
		t.Fatalf("developer.enabled = false, want true (user setting must survive the edit)")
	}
	if !dev.MITMEnabled {
		t.Fatalf("developer.mitm_enabled = false, want true")
	}
	if dev.CaptureLimit != 42 {
		t.Fatalf("developer.capture_limit = %d, want 42", dev.CaptureLimit)
	}
	if dev.BodyLimitBytes != 12345 {
		t.Fatalf("developer.body_limit_bytes = %d, want 12345", dev.BodyLimitBytes)
	}
	if !reflect.DeepEqual(dev.SSLDecryptHosts, []string{"*.internal.example.com"}) {
		t.Fatalf("developer.ssl_decrypt_hosts = %#v, want [*.internal.example.com]", dev.SSLDecryptHosts)
	}
}

func assertProfileNotTUNInjected(t *testing.T, cfg *config.Config, name string) {
	t.Helper()
	profile, ok := cfg.ProfileByName(name)
	if !ok {
		t.Fatalf("profile %q missing from persisted config", name)
	}
	if profile.Listen.TUN != nil {
		t.Fatalf("profile %q persisted with injected TUN stanza %+v, want none", name, profile.Listen.TUN)
	}
}

// assertRuntimeOverlayEnablesTUN confirms the in-memory runtime load still
// force-enables TUN with mobile defaults on every profile and disables developer
// capture, even though none of that is persisted to disk.
func assertRuntimeOverlayEnablesTUN(t *testing.T, path string) {
	t.Helper()
	cfg, err := loadTunnelConfig(path)
	if err != nil {
		t.Fatalf("loadTunnelConfig (runtime overlay): %v", err)
	}
	for i := range cfg.Profiles {
		tun := cfg.Profiles[i].Listen.TUN
		if tun == nil || !tun.Enabled {
			t.Fatalf("runtime profile %q TUN = %+v, want enabled overlay", cfg.Profiles[i].Name, tun)
		}
		if tun.MTU != defaultTunnelMTU {
			t.Fatalf("runtime profile %q MTU = %d, want %d", cfg.Profiles[i].Name, tun.MTU, defaultTunnelMTU)
		}
		if !reflect.DeepEqual(tun.Routes, defaultTunnelRoutes) {
			t.Fatalf("runtime profile %q routes = %#v, want %#v", cfg.Profiles[i].Name, tun.Routes, defaultTunnelRoutes)
		}
	}
	if cfg.Developer.Enabled {
		t.Fatalf("runtime developer capture enabled, want mobile-safety disabled overlay")
	}
}

func TestAppendTunnelRulePreservesDeveloperAndSecondaryProfile(t *testing.T) {
	path := writePreservationTunnelTestConfig(t)

	if _, err := AppendTunnelRuleJSON(path, "default", `{"name":"block-ads","action":"block","domains":["ads.example.com"]}`); err != nil {
		t.Fatalf("AppendTunnelRuleJSON: %v", err)
	}

	// Inspect the persisted bytes directly, bypassing the runtime overlay.
	persisted, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load persisted: %v", err)
	}

	// The [developer] section must not be replaced with mobile defaults.
	assertDeveloperSectionPreserved(t, persisted.Developer)

	// Only the targeted rule changed on the default profile.
	def, ok := persisted.ProfileByName("default")
	if !ok {
		t.Fatal("default profile missing after edit")
	}
	if len(def.Rules) != 1 || def.Rules[0].Name != "block-ads" {
		t.Fatalf("default rules = %+v, want only block-ads", def.Rules)
	}

	// No TUN/default routes injected into any persisted profile.
	assertProfileNotTUNInjected(t, persisted, "default")
	assertProfileNotTUNInjected(t, persisted, "secondary")

	// The non-TUN secondary profile is untouched by an edit that targeted default.
	sec, ok := persisted.ProfileByName("secondary")
	if !ok {
		t.Fatal("secondary profile missing after edit")
	}
	if len(sec.Rules) != 0 {
		t.Fatalf("secondary rules = %+v, want none", sec.Rules)
	}
	if len(sec.Chains) != 1 || sec.Chains[0].Name != "proxy" ||
		len(sec.Chains[0].Servers) != 1 || sec.Chains[0].Servers[0].Name != "sec" {
		t.Fatalf("secondary chains mutated: %+v", sec.Chains)
	}

	// Runtime still enables required mobile TUN behavior as an overlay.
	assertRuntimeOverlayEnablesTUN(t, path)
}

func TestSelectPolicyGroupJSONPreservesDeveloperAndSecondaryProfile(t *testing.T) {
	path := writePreservationTunnelTestConfig(t)

	if _, err := SelectPolicyGroupJSON(path, "default", "manual", "backup"); err != nil {
		t.Fatalf("SelectPolicyGroupJSON: %v", err)
	}

	persisted, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load persisted: %v", err)
	}

	assertDeveloperSectionPreserved(t, persisted.Developer)

	// Only the targeted policy group selection changed.
	def, ok := persisted.ProfileByName("default")
	if !ok {
		t.Fatal("default profile missing after edit")
	}
	if len(def.PolicyGroups) != 1 || def.PolicyGroups[0].Name != "manual" || def.PolicyGroups[0].Selected != "backup" {
		t.Fatalf("default policy groups = %+v, want manual selected=backup", def.PolicyGroups)
	}

	assertProfileNotTUNInjected(t, persisted, "default")
	assertProfileNotTUNInjected(t, persisted, "secondary")

	sec, ok := persisted.ProfileByName("secondary")
	if !ok {
		t.Fatal("secondary profile missing after edit")
	}
	if len(sec.PolicyGroups) != 0 || len(sec.Rules) != 0 {
		t.Fatalf("secondary mutated: groups=%+v rules=%+v", sec.PolicyGroups, sec.Rules)
	}

	assertRuntimeOverlayEnablesTUN(t, path)
}
