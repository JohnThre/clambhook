package api

import (
	"testing"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/prompt"
)

// TestPromptRuleFromPendingMatchGranularity covers the Little Snitch-style
// remembered-rule builder: an "allow" resolution routes the owning process
// through the profile's default chain, and the match-host/port/protocol toggles
// pin the rule to the connection's destination host, port, and network.
func TestPromptRuleFromPendingMatchGranularity(t *testing.T) {
	pending := prompt.Pending{
		Profile:     "default",
		Network:     "tcp",
		Target:      "example.com:443",
		TargetHost:  "example.com",
		TargetPort:  "443",
		ProcessName: "curl",
		ProcessPath: "/usr/bin/curl",
	}
	cfg := &config.Config{Profiles: []config.Profile{{Name: "default", Chains: []config.ChainConfig{{Name: "proxy"}}}}}

	rule, err := PromptRuleFromPending(pending, true, true, true, true, cfg)
	if err != nil {
		t.Fatalf("PromptRuleFromPending allow: %v", err)
	}
	if rule.Action != "chain:proxy" {
		t.Fatalf("allow action = %q, want chain:proxy", rule.Action)
	}
	if len(rule.Processes) != 1 || rule.Processes[0] != "/usr/bin/curl" {
		t.Fatalf("processes = %+v", rule.Processes)
	}
	if len(rule.Domains) != 1 || rule.Domains[0] != "example.com" {
		t.Fatalf("domains = %+v (matchHost)", rule.Domains)
	}
	if len(rule.Ports) != 1 || rule.Ports[0] != 443 {
		t.Fatalf("ports = %+v (matchPort)", rule.Ports)
	}
	if len(rule.Networks) != 1 || rule.Networks[0] != "tcp" {
		t.Fatalf("networks = %+v (matchProtocol)", rule.Networks)
	}

	// Block with no match flags: a process-only deny (no host/port/protocol pin).
	blockRule, err := PromptRuleFromPending(pending, false, false, false, false, cfg)
	if err != nil {
		t.Fatalf("PromptRuleFromPending block: %v", err)
	}
	if blockRule.Action != "block" {
		t.Fatalf("block action = %q, want block", blockRule.Action)
	}
	if len(blockRule.Domains) != 0 || len(blockRule.Ports) != 0 || len(blockRule.Networks) != 0 {
		t.Fatalf("block rule should be process-only: %+v", blockRule)
	}

	// No attributed process: cannot build a remembered rule.
	if _, err := PromptRuleFromPending(prompt.Pending{Profile: "default"}, true, false, false, false, cfg); err == nil {
		t.Fatalf("expected error for prompt with no attributed process")
	}
}
