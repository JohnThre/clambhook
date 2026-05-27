package rules

import "testing"

func TestDecideFirstMatchingRule(t *testing.T) {
	engine, err := Compile([]Rule{
		{Name: "ads", Action: ActionBlock, DomainSuffixes: []string{"ads.example.com"}},
		{Name: "corp", Action: "chain:corp", Domains: []string{"api.example.com"}, Ports: []int{443}},
	}, "default", map[string]struct{}{"default": {}, "corp": {}})
	if err != nil {
		t.Fatal(err)
	}

	got := engine.Decide("tcp", "api.example.com:443")
	if got.RuleName != "corp" || got.Action != ActionChain || got.ChainName != "corp" {
		t.Fatalf("decision = %+v, want corp chain", got)
	}

	got = engine.Decide("tcp", "cdn.ads.example.com:443")
	if got.RuleName != "ads" || got.Action != ActionBlock {
		t.Fatalf("decision = %+v, want ads block", got)
	}
}

func TestDecideFallsBackToDefaultChain(t *testing.T) {
	engine, err := Compile(nil, "default", map[string]struct{}{"default": {}})
	if err != nil {
		t.Fatal(err)
	}

	got := engine.Decide("tcp", "example.org:80")
	if !got.Default || got.Action != ActionChain || got.ChainName != "default" {
		t.Fatalf("decision = %+v, want default chain", got)
	}
}

func TestCompileRejectsUnknownChain(t *testing.T) {
	_, err := Compile([]Rule{{Name: "missing", Action: "chain:missing"}}, "default", map[string]struct{}{"default": {}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCIDRAndNetworkMatch(t *testing.T) {
	engine, err := Compile([]Rule{
		{Name: "local-dns", Action: ActionDirect, CIDRs: []string{"10.0.0.0/8"}, Ports: []int{53}, Networks: []string{"udp"}},
	}, "default", map[string]struct{}{"default": {}})
	if err != nil {
		t.Fatal(err)
	}

	got := engine.Decide("udp", "10.1.2.3:53")
	if got.RuleName != "local-dns" || got.Action != ActionDirect {
		t.Fatalf("decision = %+v, want direct local-dns", got)
	}

	got = engine.Decide("tcp", "10.1.2.3:53")
	if !got.Default {
		t.Fatalf("decision = %+v, want default for TCP", got)
	}
}
