package mobile

import (
	"encoding/json"
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
