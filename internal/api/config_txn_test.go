package api

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
	_ "github.com/JohnThre/clambhook/internal/protocol/tor"
)

// TestConcurrentRuleCreatesRetainAllEdits fires many simultaneous rule-append
// requests. Each is a load-modify-validate-write-reload transaction against the
// same config file; without a serializing lock the appends race and later
// writers overwrite earlier ones, so the final config keeps only a fraction of
// the rules. With the transaction lock every append must observe the prior
// writes, so all of them survive on disk and in the engine.
func TestConcurrentRuleCreatesRetainAllEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	cfg := testRuleCreateConfig()
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	srv := NewWithOptions(engine.New(cfg, nil), nil, Options{ConfigPath: path})

	const n = 25
	start := make(chan struct{})
	codes := make([]int, n)
	failBodies := make([]string, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func() {
			defer wg.Done()
			body := fmt.Sprintf(`{"rule":{"name":"rule-%02d","action":"block","domains":["b%02d.example.com"]}}`, i, i)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/rules", bytes.NewReader([]byte(body)))
			rec := httptest.NewRecorder()
			<-start
			srv.server.Handler.ServeHTTP(rec, req)
			codes[i] = rec.Code
			failBodies[i] = rec.Body.String()
		}()
	}
	close(start)
	wg.Wait()

	for i, code := range codes {
		if code != http.StatusOK {
			t.Fatalf("request %d status = %d body=%q, want 200", i, code, failBodies[i])
		}
	}

	want := make(map[string]bool, n)
	for i := range n {
		want[fmt.Sprintf("rule-%02d", i)] = true
	}

	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("load persisted config: %v", err)
	}
	diskProfile, err := reloaded.ActiveProfile()
	if err != nil {
		t.Fatal(err)
	}
	assertAllRuleNames(t, "disk", diskProfile.Rules, want)

	engineProfile, err := srv.engine.Config().ActiveProfile()
	if err != nil {
		t.Fatal(err)
	}
	assertAllRuleNames(t, "engine", engineProfile.Rules, want)
}

// TestConcurrentDistinctSectionEditsRetainAll fires simultaneous edits that
// each touch a different config section (rules, DNS, listen settings). Because
// every mutation rewrites the whole config file, without serialization the last
// writer clobbers the others' sections. The transaction lock guarantees each
// edit is layered onto the previous one, so all three survive.
func TestConcurrentDistinctSectionEditsRetainAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	cfg := testRuleCreateConfig()
	cfg.Profiles[0].Listen.SOCKS5 = "127.0.0.1:1080"
	cfg.Profiles[0].Listen.SOCKS5Chain = "proxy"
	if _, err := config.WriteAtomicWithBackup(path, cfg); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	srv := NewWithOptions(engine.New(cfg, nil), nil, Options{ConfigPath: path})

	edits := []struct {
		method string
		route  string
		body   string
	}{
		{http.MethodPost, "/api/v1/rules", `{"rule":{"name":"edit-rule","action":"block","domains":["blocked.example.com"]}}`},
		{http.MethodPut, "/api/v1/dns", `{"enabled":true,"timeout":"3s","upstreams":[{"name":"cf","protocol":"doh","url":"https://cloudflare-dns.com/dns-query"}]}`},
		{http.MethodPut, "/api/v1/config/settings", `{"listen":{"socks5":"127.0.0.1:11080","socks5_chain":"proxy"}}`},
	}

	start := make(chan struct{})
	codes := make([]int, len(edits))
	bodies := make([]string, len(edits))
	var wg sync.WaitGroup
	wg.Add(len(edits))
	for i, e := range edits {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(e.method, e.route, bytes.NewReader([]byte(e.body)))
			rec := httptest.NewRecorder()
			<-start
			srv.server.Handler.ServeHTTP(rec, req)
			codes[i] = rec.Code
			bodies[i] = rec.Body.String()
		}()
	}
	close(start)
	wg.Wait()

	for i, code := range codes {
		if code != http.StatusOK {
			t.Fatalf("edit %d (%s %s) status = %d body=%q, want 200", i, edits[i].method, edits[i].route, code, bodies[i])
		}
	}

	assertAllSections := func(label string, profile *config.Profile) {
		t.Helper()
		hasRule := false
		for _, r := range profile.Rules {
			if r.Name == "edit-rule" {
				hasRule = true
			}
		}
		if !hasRule {
			t.Fatalf("%s: rule edit lost; rules=%v", label, ruleNamesOf(profile.Rules))
		}
		if !profile.DNS.Enabled || len(profile.DNS.Upstreams) != 1 || profile.DNS.Upstreams[0].Name != "cf" {
			t.Fatalf("%s: dns edit lost; dns=%+v", label, profile.DNS)
		}
		if profile.Listen.SOCKS5 != "127.0.0.1:11080" {
			t.Fatalf("%s: listen edit lost; socks5=%q", label, profile.Listen.SOCKS5)
		}
	}

	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("load persisted config: %v", err)
	}
	diskProfile, err := reloaded.ActiveProfile()
	if err != nil {
		t.Fatal(err)
	}
	assertAllSections("disk", diskProfile)

	engineProfile, err := srv.engine.Config().ActiveProfile()
	if err != nil {
		t.Fatal(err)
	}
	assertAllSections("engine", engineProfile)
}

func assertAllRuleNames(t *testing.T, label string, rules []config.RuleConfig, want map[string]bool) {
	t.Helper()
	if len(rules) != len(want) {
		t.Fatalf("%s: got %d rules, want %d (rules=%v)", label, len(rules), len(want), ruleNamesOf(rules))
	}
	got := make(map[string]bool, len(rules))
	for _, r := range rules {
		got[r.Name] = true
	}
	for name := range want {
		if !got[name] {
			t.Fatalf("%s: missing rule %q; got %v", label, name, ruleNamesOf(rules))
		}
	}
}

func ruleNamesOf(rules []config.RuleConfig) []string {
	names := make([]string, len(rules))
	for i, r := range rules {
		names[i] = r.Name
	}
	return names
}
