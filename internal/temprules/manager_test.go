package temprules

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/rules"
)

func knownChains(names ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}
	return set
}

// compiles reads the internal recompilation counter under lock.
func (m *Manager) compiles() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.compileCount
}

func TestDecideMatchesTemporaryRule(t *testing.T) {
	m := New()
	if _, err := m.Create(CreateRequest{
		Profile: "home",
		Rule:    config.RuleConfig{Name: "temp-block", Action: rules.ActionBlock, Domains: []string{"ads.example.com"}},
		TTL:     time.Hour,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	decision, ok, err := m.Decide("home", "default", "tcp", "ads.example.com:443", "", "", "", knownChains("default"), nil)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !ok {
		t.Fatalf("expected temporary rule to match")
	}
	if decision.Action != rules.ActionBlock || decision.RuleName != "temp-block" {
		t.Fatalf("decision = %+v, want block temp-block", decision)
	}
	if decision.Explanation.Source != "temporary_rule" {
		t.Fatalf("explanation source = %q, want temporary_rule", decision.Explanation.Source)
	}

	// Non-matching target falls through.
	if _, ok, err := m.Decide("home", "default", "tcp", "example.org:443", "", "", "", knownChains("default"), nil); err != nil || ok {
		t.Fatalf("unexpected match for example.org: ok=%v err=%v", ok, err)
	}

	// Other profiles do not see this rule.
	if _, ok, err := m.Decide("work", "default", "tcp", "ads.example.com:443", "", "", "", knownChains("default"), nil); err != nil || ok {
		t.Fatalf("rule leaked into other profile: ok=%v err=%v", ok, err)
	}
}

func TestDecideExpiredRuleDisappears(t *testing.T) {
	m := New()
	if _, err := m.Create(CreateRequest{
		Profile: "home",
		Rule:    config.RuleConfig{Name: "temp-block", Action: rules.ActionBlock, Domains: []string{"ads.example.com"}},
		TTL:     20 * time.Millisecond,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, ok, err := m.Decide("home", "default", "tcp", "ads.example.com:443", "", "", "", knownChains("default"), nil); err != nil || !ok {
		t.Fatalf("expected match before expiry: ok=%v err=%v", ok, err)
	}

	time.Sleep(40 * time.Millisecond)

	if _, ok, err := m.Decide("home", "default", "tcp", "ads.example.com:443", "", "", "", knownChains("default"), nil); err != nil || ok {
		t.Fatalf("expected no match after expiry: ok=%v err=%v", ok, err)
	}
	if got := m.Snapshot("home"); len(got) != 0 {
		t.Fatalf("expired rule still present: %+v", got)
	}
}

func TestDecideReusesCompiledEngine(t *testing.T) {
	m := New()
	if _, err := m.Create(CreateRequest{
		Profile: "home",
		Rule:    config.RuleConfig{Name: "temp-block", Action: rules.ActionBlock, Domains: []string{"ads.example.com"}},
		TTL:     time.Hour,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	chains := knownChains("default")

	// First Decide compiles once.
	if _, _, err := m.Decide("home", "default", "tcp", "ads.example.com:443", "", "", "", chains, nil); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	after := m.compiles()
	if after != 1 {
		t.Fatalf("compile count after first Decide = %d, want 1", after)
	}

	// Repeated unchanged Decide calls must not recompile.
	for range 500 {
		if _, ok, err := m.Decide("home", "default", "tcp", "ads.example.com:443", "", "", "", chains, nil); err != nil || !ok {
			t.Fatalf("Decide reuse: ok=%v err=%v", ok, err)
		}
	}
	if got := m.compiles(); got != 1 {
		t.Fatalf("compile count after reuse = %d, want 1 (recompiled on unchanged hot path)", got)
	}

	// A mutation forces exactly one more compile.
	if _, err := m.Create(CreateRequest{
		Profile: "home",
		Rule:    config.RuleConfig{Name: "temp-two", Action: rules.ActionBlock, Domains: []string{"tracker.example.com"}},
		TTL:     time.Hour,
	}); err != nil {
		t.Fatalf("Create second: %v", err)
	}
	if _, _, err := m.Decide("home", "default", "tcp", "ads.example.com:443", "", "", "", chains, nil); err != nil {
		t.Fatalf("Decide after mutation: %v", err)
	}
	if got := m.compiles(); got != 2 {
		t.Fatalf("compile count after mutation = %d, want 2", got)
	}
}

func TestDecideRecompilesOnChangedChainContext(t *testing.T) {
	m := New()
	if _, err := m.Create(CreateRequest{
		Profile: "home",
		Rule:    config.RuleConfig{Name: "temp-route", Action: "chain:corp", Domains: []string{"api.example.com"}},
		TTL:     time.Hour,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// corp chain is unknown: compile must fail closed.
	if _, ok, err := m.Decide("home", "default", "tcp", "api.example.com:443", "", "", "", knownChains("default"), nil); err == nil {
		t.Fatalf("expected error for unknown chain, got ok=%v", ok)
	}

	// Once corp exists, the changed context recompiles and matches.
	decision, ok, err := m.Decide("home", "default", "tcp", "api.example.com:443", "", "", "", knownChains("default", "corp"), nil)
	if err != nil || !ok {
		t.Fatalf("expected match with corp chain: ok=%v err=%v", ok, err)
	}
	if decision.Action != rules.ActionChain || decision.ChainName != "corp" {
		t.Fatalf("decision = %+v, want chain corp", decision)
	}
}

func TestConcurrentCreateDeleteDecideRaceClean(t *testing.T) {
	m := New()
	chains := knownChains("default")
	var stop atomic.Bool
	var wg sync.WaitGroup

	// Readers hammer Decide on the unchanged hot path.
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				_, _, _ = m.Decide("home", "default", "tcp", "ads.example.com:443", "", "", "", chains, nil)
			}
		}()
	}

	// Writers churn the rule set.
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				created, err := m.Create(CreateRequest{
					Profile: "home",
					Rule:    config.RuleConfig{Name: "temp-block", Action: rules.ActionBlock, Domains: []string{"ads.example.com"}},
					TTL:     5 * time.Millisecond,
				})
				if err != nil {
					continue
				}
				m.Delete(created.ID)
				m.Snapshot("home")
			}
		}()
	}

	time.Sleep(200 * time.Millisecond)
	stop.Store(true)
	wg.Wait()
}

func BenchmarkDecideReusesCompiled(b *testing.B) {
	m := New()
	if _, err := m.Create(CreateRequest{
		Profile: "home",
		Rule:    config.RuleConfig{Name: "temp-block", Action: rules.ActionBlock, Domains: []string{"ads.example.com"}},
		TTL:     time.Hour,
	}); err != nil {
		b.Fatalf("Create: %v", err)
	}
	chains := knownChains("default")
	if _, _, err := m.Decide("home", "default", "tcp", "ads.example.com:443", "", "", "", chains, nil); err != nil {
		b.Fatalf("warmup Decide: %v", err)
	}
	before := m.compiles()

	b.ResetTimer()
	for range b.N {
		if _, _, err := m.Decide("home", "default", "tcp", "ads.example.com:443", "", "", "", chains, nil); err != nil {
			b.Fatalf("Decide: %v", err)
		}
	}
	b.StopTimer()
	if got := m.compiles(); got != before {
		b.Fatalf("recompiled during benchmark: before=%d after=%d", before, got)
	}
}
