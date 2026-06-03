package subscription

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JohnThre/clambhook/internal/config"
)

func TestParsePlainHostsAndCIDRs(t *testing.T) {
	body := []byte(`
# comment
example.com
0.0.0.0 ads.example.com tracker.example.com
10.0.0.0/8
192.0.2.10
https://cdn.example.net/path
`)
	got, err := Parse(body, FormatAuto)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Format != FormatHosts {
		t.Fatalf("format = %q, want hosts", got.Format)
	}
	wantDomains := []string{"ads.example.com", "cdn.example.net", "example.com", "tracker.example.com"}
	if strings.Join(got.DomainSuffixes, ",") != strings.Join(wantDomains, ",") {
		t.Fatalf("domains = %#v, want %#v", got.DomainSuffixes, wantDomains)
	}
	wantCIDRs := []string{"10.0.0.0/8", "192.0.2.10/32"}
	if strings.Join(got.CIDRs, ",") != strings.Join(wantCIDRs, ",") {
		t.Fatalf("cidrs = %#v, want %#v", got.CIDRs, wantCIDRs)
	}
}

func TestParseAdblockCommonHostFilters(t *testing.T) {
	body := []byte(`
! comment
||ads.example.com^
@@||allowed.example.com^
example.org##.ad
/regex/
|https://track.example.net/path
`)
	got, err := Parse(body, FormatAuto)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Format != FormatAdblock {
		t.Fatalf("format = %q, want adblock", got.Format)
	}
	want := []string{"ads.example.com", "track.example.net"}
	if strings.Join(got.DomainSuffixes, ",") != strings.Join(want, ",") {
		t.Fatalf("domains = %#v, want %#v", got.DomainSuffixes, want)
	}
	if got.Skipped != 1 {
		t.Fatalf("skipped = %d, want 1", got.Skipped)
	}
}

func TestCachedRulesMaterializeAfterManualRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clambhook.toml")
	profile := config.Profile{
		Name: "default",
		Rules: []config.RuleConfig{{
			Name:   "manual",
			Action: "direct",
			CIDRs:  []string{"10.0.0.0/8"},
		}},
		RuleSubscriptions: []config.RuleSubscriptionConfig{{
			Name:   "ads",
			URL:    "https://lists.example.invalid/ads.txt",
			Action: "reject",
		}},
	}
	sub := profile.RuleSubscriptions[0]
	if err := WriteCache(path, profile.Name, sub, Cache{
		Format:         FormatPlain,
		Action:         ActionReject,
		DomainSuffixes: []string{"ads.example.com"},
		CIDRs:          []string{"192.0.2.0/24"},
	}); err != nil {
		t.Fatalf("WriteCache: %v", err)
	}
	manual, generated, effective := EffectiveRules(path, &profile)
	if len(manual) != 1 || manual[0].Name != "manual" {
		t.Fatalf("manual = %#v", manual)
	}
	if len(generated) != 2 {
		t.Fatalf("generated = %#v, want 2 rules", generated)
	}
	if generated[0].Name != "subscription:ads:domains" || generated[0].Action != "reject" {
		t.Fatalf("domain rule = %#v", generated[0])
	}
	if len(effective) != 3 || effective[0].Name != "manual" || effective[1].Name != "subscription:ads:domains" {
		t.Fatalf("effective ordering = %#v", effective)
	}
}

func TestRefreshProfileKeepsPreviousCacheOnFetchFailure(t *testing.T) {
	var fail bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte("ads.example.com\n"))
	}))
	defer srv.Close()

	cfg := &config.Config{
		Path:   filepath.Join(t.TempDir(), "clambhook.toml"),
		Active: "default",
		Profiles: []config.Profile{{
			Name: "default",
			RuleSubscriptions: []config.RuleSubscriptionConfig{{
				Name: "ads",
				URL:  srv.URL,
			}},
		}},
	}
	payload, err := RefreshProfile(context.Background(), cfg, "", nil, srv.Client())
	if err != nil {
		t.Fatalf("RefreshProfile: %v", err)
	}
	if payload.Subscriptions[0].DomainCount != 1 || payload.Subscriptions[0].LastError != "" {
		t.Fatalf("refresh payload = %+v", payload.Subscriptions[0])
	}
	fail = true
	payload, err = RefreshProfile(context.Background(), cfg, "", nil, srv.Client())
	if err != nil {
		t.Fatalf("RefreshProfile after failure: %v", err)
	}
	st := payload.Subscriptions[0]
	if !st.Cached || st.DomainCount != 1 || st.LastError == "" {
		data, _ := json.Marshal(st)
		t.Fatalf("status after failure = %s", data)
	}
}
