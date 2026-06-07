package rules

import "testing"

func TestDecideFirstMatchingRule(t *testing.T) {
	engine, err := Compile([]Rule{
		{Name: "ads", Action: ActionBlock, DomainSuffixes: []string{"ads.example.com"}},
		{Name: "corp", Action: "chain:corp", Domains: []string{"api.example.com"}, Ports: []int{443}},
	}, "default", map[string]struct{}{"default": {}, "corp": {}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	got := engine.Decide("tcp", "api.example.com:443")
	if got.RuleName != "corp" || got.RuleNumber != 2 || got.Action != ActionChain || got.ChainName != "corp" {
		t.Fatalf("decision = %+v, want corp chain", got)
	}

	got = engine.Decide("tcp", "cdn.ads.example.com:443")
	if got.RuleName != "ads" || got.RuleNumber != 1 || got.Action != ActionBlock {
		t.Fatalf("decision = %+v, want ads block", got)
	}
}

func TestDecideFallsBackToDefaultChain(t *testing.T) {
	engine, err := Compile(nil, "default", map[string]struct{}{"default": {}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	got := engine.Decide("tcp", "example.org:80")
	if !got.Default || got.RuleNumber != 1 || got.Action != ActionChain || got.ChainName != "default" {
		t.Fatalf("decision = %+v, want default chain", got)
	}
}

func TestCompileRejectsUnknownChain(t *testing.T) {
	_, err := Compile([]Rule{{Name: "missing", Action: "chain:missing"}}, "default", map[string]struct{}{"default": {}}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCIDRAndNetworkMatch(t *testing.T) {
	engine, err := Compile([]Rule{
		{Name: "local-dns", Action: ActionDirect, CIDRs: []string{"10.0.0.0/8"}, Ports: []int{53}, Networks: []string{"udp"}},
	}, "default", map[string]struct{}{"default": {}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	got := engine.Decide("udp", "10.1.2.3:53")
	if got.RuleName != "local-dns" || got.Action != ActionDirect {
		t.Fatalf("decision = %+v, want direct local-dns", got)
	}

	got = engine.Decide("tcp", "10.1.2.3:53")
	if !got.Default || got.RuleNumber != 2 {
		t.Fatalf("decision = %+v, want default for TCP", got)
	}
}

func TestDecidePolicyGroupRule(t *testing.T) {
	engine, err := Compile([]Rule{
		{Name: "auto", Action: "group:auto", DomainSuffixes: []string{"example.com"}},
	}, "default", map[string]struct{}{"default": {}}, map[string]struct{}{"auto": {}})
	if err != nil {
		t.Fatal(err)
	}

	got := engine.Decide("tcp", "api.example.com:443")
	if got.RuleName != "auto" || got.Action != ActionGroup || got.GroupName != "auto" || got.ChainName != "" {
		t.Fatalf("decision = %+v, want auto group", got)
	}
}

func TestCompileRejectsUnknownPolicyGroup(t *testing.T) {
	_, err := Compile([]Rule{{Name: "missing", Action: "group:missing"}}, "default", map[string]struct{}{"default": {}}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDecideRuleSetMatch(t *testing.T) {
	engine, err := CompileWithRuleSets([]Rule{
		{Name: "ads", Action: ActionBlock, RuleSets: []string{"ads"}},
	}, "default", map[string]struct{}{"default": {}}, nil, map[string]RuleSet{
		"ads": {
			DomainSuffixes: []string{"ads.example.com"},
			CIDRs:          []string{"203.0.113.0/24"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got := engine.Decide("tcp", "cdn.ads.example.com:443")
	if got.RuleName != "ads" || got.Action != ActionBlock {
		t.Fatalf("decision = %+v, want ads block", got)
	}
	got = engine.Decide("tcp", "203.0.113.10:443")
	if got.RuleName != "ads" || got.Action != ActionBlock {
		t.Fatalf("decision = %+v, want ads CIDR block", got)
	}
	got = engine.Decide("tcp", "example.org:443")
	if !got.Default {
		t.Fatalf("decision = %+v, want default", got)
	}
}

func TestDecideSourceCIDRMatch(t *testing.T) {
	engine, err := Compile([]Rule{
		{Name: "guest", Action: ActionBlock, Domains: []string{"internal.example.com"}, SourceCIDRs: []string{"10.10.0.0/16"}},
	}, "default", map[string]struct{}{"default": {}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	got := engine.DecideWithSource("tcp", "internal.example.com:443", "10.10.2.3:51512")
	if got.RuleName != "guest" || got.Source != "10.10.2.3:51512" || got.Action != ActionBlock {
		t.Fatalf("decision = %+v, want source-scoped block", got)
	}
	got = engine.DecideWithSource("tcp", "internal.example.com:443", "10.20.2.3:51512")
	if !got.Default {
		t.Fatalf("decision = %+v, want default outside source CIDR", got)
	}
}

func TestCompileRejectsRuleSetWithInlineDestinationMatchers(t *testing.T) {
	_, err := CompileWithRuleSets([]Rule{
		{Name: "ambiguous", Action: ActionBlock, RuleSets: []string{"ads"}, Domains: []string{"example.com"}},
	}, "default", map[string]struct{}{"default": {}}, nil, map[string]RuleSet{"ads": {Domains: []string{"ads.example.com"}}})
	if err == nil {
		t.Fatal("expected error")
	}
}
