package traffic

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/events"
	"github.com/JohnThre/clambhook/internal/geo"
)

func TestStoreAggregatesAndPersistsClosedHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic-history.json")
	store, err := NewStore(config.TrafficConfig{
		Enabled:       true,
		HistoryLimit:  10,
		HistoryMaxAge: config.Duration(time.Hour),
		HistoryPath:   path,
	}, func(address string) (*geo.Location, error) {
		return &geo.Location{Country: "United States", CountryCode: "US", City: "New York"}, nil
	})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	store.ApplyEvent(events.Event{TsNs: 1, Type: events.TypeConnectionOpened, Data: events.ConnectionOpenedData{
		ConnID:     "c1",
		Listener:   events.ListenerInfo{Protocol: "socks5", Addr: "127.0.0.1:1080"},
		ClientAddr: "127.0.0.1:50000",
		ChainName:  "default",
	}})
	store.ApplyEvent(events.Event{TsNs: 2, Type: events.TypeConnectionDialing, Data: events.ConnectionDialingData{
		ConnID:     "c1",
		Target:     "example.com:443",
		TargetHost: "example.com",
		TargetPort: "443",
		Visibility: events.VisibilityInfo{
			Kind:   "http_connect",
			Method: "CONNECT",
			Scheme: "https",
			Host:   "example.com",
			Port:   "443",
		},
		Hops: []events.HopInfo{{
			Index:    0,
			Name:     "exit",
			Protocol: "trojan",
			Address:  "proxy.example:443",
		}},
	}})
	store.ApplyEvent(events.Event{TsNs: 3, Type: events.TypeHopConnected, Data: events.HopConnectedData{
		ConnID:    "c1",
		HopIndex:  0,
		ElapsedNs: int64(50 * time.Millisecond),
	}})
	store.ApplyEvent(events.Event{TsNs: 4, Type: events.TypeConnectionEstablished, Data: events.ConnectionEstablishedData{
		ConnID:      "c1",
		TotalDialNs: int64(60 * time.Millisecond),
	}})
	store.ApplyEvent(events.Event{TsNs: 5, Type: events.TypeConnectionBytes, Data: events.ConnectionBytesData{
		ConnID:     "c1",
		RxDelta:    2048,
		TxDelta:    1024,
		RxTotal:    2048,
		TxTotal:    1024,
		IntervalNs: int64(time.Second),
	}})

	live := store.Snapshot("active", 0)
	if got := len(live.Connections); got != 1 {
		t.Fatalf("active connections = %d, want 1", got)
	}
	conn := live.Connections[0]
	if conn.Application != "HTTPS" || conn.RxBps != 2048 || conn.TxBps != 1024 {
		t.Fatalf("live connection = %+v", conn)
	}
	if conn.Geo.CountryCode != "US" {
		t.Fatalf("geo = %+v", conn.Geo)
	}
	if conn.Visibility == nil || conn.Visibility.Kind != "http_connect" || conn.Visibility.Host != "example.com" {
		t.Fatalf("visibility = %+v", conn.Visibility)
	}
	if len(conn.Timeline) < 4 {
		t.Fatalf("timeline = %#v, want lifecycle events", conn.Timeline)
	}

	store.ApplyEvent(events.Event{TsNs: time.Now().UnixNano(), Type: events.TypeConnectionClosed, Data: events.ConnectionClosedData{
		ConnID:     "c1",
		Reason:     events.ReasonClientEOF,
		DurationNs: int64(2 * time.Second),
		RxTotal:    2048,
		TxTotal:    1024,
	}})

	closed := store.Snapshot("closed", 0)
	if got := len(closed.Connections); got != 1 {
		t.Fatalf("closed connections = %d, want 1", got)
	}
	if closed.Connections[0].State != StateClosed || closed.Connections[0].CloseReason != events.ReasonClientEOF {
		t.Fatalf("closed connection = %+v", closed.Connections[0])
	}

	reloaded, err := NewStore(config.TrafficConfig{
		Enabled:       true,
		HistoryLimit:  10,
		HistoryMaxAge: config.Duration(time.Hour),
		HistoryPath:   path,
	}, nil)
	if err != nil {
		t.Fatalf("reload NewStore: %v", err)
	}
	if got := len(reloaded.Snapshot("closed", 0).Connections); got != 1 {
		t.Fatalf("reloaded closed connections = %d, want 1", got)
	}
}

func TestSnapshotWithOptionsFiltersAndBuildsMonitorAnalytics(t *testing.T) {
	store, err := NewStore(config.TrafficConfig{
		Enabled:       true,
		HistoryLimit:  10,
		HistoryMaxAge: config.Duration(time.Hour),
		HistoryPath:   filepath.Join(t.TempDir(), "traffic-history.json"),
	}, nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	base := time.Now().UnixNano()
	store.ApplyEvent(events.Event{TsNs: base + 1, Type: events.TypeConnectionOpened, Data: events.ConnectionOpenedData{
		ConnID:  "c1",
		Profile: "Work",
	}})
	store.ApplyEvent(events.Event{TsNs: base + 2, Type: events.TypeRuleBlocked, Data: events.RuleDecisionData{
		ConnID:     "c1",
		Profile:    "Work",
		RuleName:   "ads",
		Action:     "block",
		Target:     "ads.example.com:443",
		TargetHost: "ads.example.com",
		TargetPort: "443",
		Network:    "tcp",
	}})
	store.ApplyEvent(events.Event{TsNs: base + 3, Type: events.TypeConnectionClosed, Data: events.ConnectionClosedData{
		ConnID: "c1",
		Reason: events.ReasonRouteBlocked,
	}})
	store.ApplyEvent(events.Event{TsNs: base + 4, Type: events.TypeConnectionOpened, Data: events.ConnectionOpenedData{
		ConnID:  "c2",
		Profile: "Home",
	}})
	store.ApplyEvent(events.Event{TsNs: base + 5, Type: events.TypeRuleMatched, Data: events.RuleDecisionData{
		ConnID:     "c2",
		Profile:    "Home",
		Action:     "chain",
		ChainName:  "proxy",
		Target:     "example.com:443",
		TargetHost: "example.com",
		TargetPort: "443",
		Default:    true,
	}})
	store.ApplyEvent(events.Event{TsNs: base + 6, Type: events.TypeConnectionOpened, Data: events.ConnectionOpenedData{
		ConnID:  "c3",
		Profile: "Work",
	}})
	store.ApplyEvent(events.Event{TsNs: base + 7, Type: events.TypeRuleMatched, Data: events.RuleDecisionData{
		ConnID:     "c3",
		Profile:    "Work",
		Action:     "chain",
		ChainName:  "proxy",
		Target:     "api.example.com:443",
		TargetHost: "api.example.com",
		TargetPort: "443",
		Network:    "tcp",
	}})

	snapshot := store.SnapshotWithOptions(SnapshotOptions{
		State:         "all",
		Limit:         20,
		Action:        "block",
		Profile:       "Work",
		ActiveProfile: "Work",
		Profiles:      []string{"Work", "Home"},
		Rules: []config.RuleConfig{
			{Name: "ads", Action: "block", DomainSuffixes: []string{"ads.example.com"}},
			{Name: "unused", Action: "direct", Domains: []string{"unused.example.com"}},
		},
	})

	if len(snapshot.Connections) != 1 || snapshot.Connections[0].ConnID != "c1" {
		t.Fatalf("filtered connections = %+v", snapshot.Connections)
	}
	if snapshot.ProfileContext.Active != "Work" || len(snapshot.QuickFilters) == 0 {
		t.Fatalf("context/filters = %+v %+v", snapshot.ProfileContext, snapshot.QuickFilters)
	}
	var sawAdsHit bool
	for _, hit := range snapshot.RuleHits {
		if hit.RuleName == "ads" && hit.Action == "block" {
			sawAdsHit = true
		}
	}
	if !sawAdsHit {
		t.Fatalf("rule hits = %+v", snapshot.RuleHits)
	}
	if len(snapshot.BlockDecisions) != 1 || snapshot.BlockDecisions[0].CloseReason != events.ReasonRouteBlocked {
		t.Fatalf("block decisions = %+v", snapshot.BlockDecisions)
	}
	if len(snapshot.CleanupSuggestions) == 0 {
		t.Fatalf("cleanup suggestions empty")
	}
	if len(snapshot.RuleSuggestions) != 1 {
		t.Fatalf("rule suggestions = %+v, want one uncovered Work host", snapshot.RuleSuggestions)
	}
	suggestion := snapshot.RuleSuggestions[0]
	if suggestion.Kind != "exact_host" || suggestion.Action != "chain:proxy" || len(suggestion.DraftRule.Domains) != 1 || suggestion.DraftRule.Domains[0] != "api.example.com" {
		t.Fatalf("rule suggestion = %+v", suggestion)
	}
	if len(suggestion.DraftRule.Ports) != 1 || suggestion.DraftRule.Ports[0] != 443 || len(suggestion.DraftRule.Networks) != 1 || suggestion.DraftRule.Networks[0] != "tcp" {
		t.Fatalf("rule suggestion match scope = %+v", suggestion.DraftRule)
	}
}

func TestRuleSuggestionsIncludeConservativeDomainSuffixes(t *testing.T) {
	store, err := NewStore(config.TrafficConfig{
		Enabled:       true,
		HistoryLimit:  10,
		HistoryMaxAge: config.Duration(time.Hour),
		HistoryPath:   filepath.Join(t.TempDir(), "traffic-history.json"),
	}, nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	base := time.Now().UnixNano()
	rows := []struct {
		id     string
		target string
		host   string
	}{
		{"c1", "api.example.com:443", "api.example.com"},
		{"c2", "cdn.example.com:443", "cdn.example.com"},
		{"c3", "img.example.com:443", "img.example.com"},
	}
	for i, row := range rows {
		store.ApplyEvent(events.Event{TsNs: base + int64(i*2+1), Type: events.TypeConnectionOpened, Data: events.ConnectionOpenedData{
			ConnID:  row.id,
			Profile: "Work",
		}})
		store.ApplyEvent(events.Event{TsNs: base + int64(i*2+2), Type: events.TypeRuleMatched, Data: events.RuleDecisionData{
			ConnID:     row.id,
			Profile:    "Work",
			Action:     "direct",
			Target:     row.target,
			TargetHost: row.host,
			TargetPort: "443",
			Network:    "tcp",
		}})
	}

	snapshot := store.SnapshotWithOptions(SnapshotOptions{State: "all", ActiveProfile: "Work"})
	var suffix *RuleSuggestion
	for i := range snapshot.RuleSuggestions {
		if snapshot.RuleSuggestions[i].Kind == "domain_suffix" {
			suffix = &snapshot.RuleSuggestions[i]
			break
		}
	}
	if suffix == nil {
		t.Fatalf("rule suggestions = %+v, want domain suffix suggestion", snapshot.RuleSuggestions)
	}
	if suffix.DraftRule.Action != "direct" || len(suffix.DraftRule.DomainSuffixes) != 1 || suffix.DraftRule.DomainSuffixes[0] != "example.com" {
		t.Fatalf("suffix suggestion = %+v", *suffix)
	}
	if suffix.Count != 3 || suffix.Confidence != "low" {
		t.Fatalf("suffix suggestion confidence/count = %+v", *suffix)
	}
}

func TestRuleSuggestionsSuppressEffectiveRuleCoverage(t *testing.T) {
	store, err := NewStore(config.TrafficConfig{
		Enabled:       true,
		HistoryLimit:  10,
		HistoryMaxAge: config.Duration(time.Hour),
		HistoryPath:   filepath.Join(t.TempDir(), "traffic-history.json"),
	}, nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	store.ApplyEvent(events.Event{TsNs: 1, Type: events.TypeConnectionOpened, Data: events.ConnectionOpenedData{
		ConnID:  "c1",
		Profile: "Work",
	}})
	store.ApplyEvent(events.Event{TsNs: 2, Type: events.TypeRuleMatched, Data: events.RuleDecisionData{
		ConnID:     "c1",
		Profile:    "Work",
		Action:     "block",
		Target:     "ads.example.com:443",
		TargetHost: "ads.example.com",
		TargetPort: "443",
		Network:    "tcp",
	}})

	snapshot := store.SnapshotWithOptions(SnapshotOptions{
		State:         "all",
		ActiveProfile: "Work",
		EffectiveRules: []config.RuleConfig{{
			Name:           "subscription-ads",
			Action:         "block",
			DomainSuffixes: []string{"example.com"},
		}},
	})
	if len(snapshot.RuleSuggestions) != 0 {
		t.Fatalf("rule suggestions = %+v, want covered host suppressed", snapshot.RuleSuggestions)
	}
}

func TestCleanupSuggestionsRespectRuleScope(t *testing.T) {
	rules := []config.RuleConfig{
		{Name: "guest-api", Action: "block", Domains: []string{"api.example.com"}, SourceCIDRs: []string{"10.0.0.0/8"}},
		{Name: "corp-api", Action: "block", Domains: []string{"api.example.com"}, SourceCIDRs: []string{"192.168.0.0/16"}},
		{Name: "ads-a", Action: "block", RuleSets: []string{"ads-a"}},
		{Name: "ads-b", Action: "block", RuleSets: []string{"ads-b"}},
		{Name: "exact-direct", Action: "direct", Domains: []string{"cdn.example.com"}},
		{Name: "suffix-block", Action: "block", DomainSuffixes: []string{"example.com"}},
		{Name: "scoped-exact", Action: "block", Domains: []string{"api.work.example.com"}, SourceCIDRs: []string{"10.0.0.0/8"}, Networks: []string{"tcp"}, Ports: []int{443}},
		{Name: "scoped-suffix", Action: "block", DomainSuffixes: []string{"work.example.com"}, SourceCIDRs: []string{"10.0.0.0/8"}, Networks: []string{"tcp"}, Ports: []int{443}},
	}

	suggestions := buildCleanupSuggestions("Work", rules, nil)

	if hasCleanupKindForRule(suggestions, "duplicate_matcher", "corp-api") {
		t.Fatalf("cleanup suggestions = %+v, different source_cidrs should not duplicate", suggestions)
	}
	if hasCleanupKindForRule(suggestions, "duplicate_matcher", "ads-b") {
		t.Fatalf("cleanup suggestions = %+v, different rule_sets should not duplicate", suggestions)
	}
	if hasCleanupKindForRule(suggestions, "shadowed_exact_match", "suffix-block") {
		t.Fatalf("cleanup suggestions = %+v, different actions should not shadow", suggestions)
	}
	if !hasCleanupKindForRule(suggestions, "shadowed_exact_match", "scoped-suffix") {
		t.Fatalf("cleanup suggestions = %+v, want scoped suffix shadow suggestion", suggestions)
	}
	shadow := cleanupForRule(suggestions, "shadowed_exact_match", "scoped-suffix")
	if shadow == nil || shadow.Operation != "delete_rule" || shadow.TargetRuleName != "scoped-exact" {
		t.Fatalf("shadow cleanup = %+v, want delete scoped-exact", shadow)
	}
}

func TestCleanupSuggestionsMoveBroadFirstRuleToEnd(t *testing.T) {
	rules := []config.RuleConfig{
		{Name: "final", Action: "direct"},
		{Name: "ads", Action: "block", Domains: []string{"ads.example.com"}},
	}

	suggestions := buildCleanupSuggestions("Work", rules, nil)

	broad := cleanupForRule(suggestions, "broad_match", "final")
	if broad == nil || broad.Operation != "move_rule_to_end" || broad.TargetRuleName != "final" {
		t.Fatalf("broad cleanup = %+v, want move final to end", broad)
	}
	if hasCleanupKindForRule(suggestions, "unused_in_history", "final") {
		t.Fatalf("cleanup suggestions = %+v, first broad rule should not get delete suggestion", suggestions)
	}
}

func TestStoreReconfigureDisabledStopsRecording(t *testing.T) {
	store, err := NewStore(config.TrafficConfig{
		Enabled:       true,
		HistoryLimit:  10,
		HistoryMaxAge: config.Duration(time.Hour),
		HistoryPath:   filepath.Join(t.TempDir(), "traffic-history.json"),
	}, nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Reconfigure(config.TrafficConfig{Enabled: false, HistoryLimit: 10, HistoryMaxAge: config.Duration(time.Hour)}); err != nil {
		t.Fatalf("Reconfigure: %v", err)
	}
	store.ApplyEvent(events.Event{TsNs: 1, Type: events.TypeConnectionOpened, Data: events.ConnectionOpenedData{ConnID: "c1"}})
	if got := len(store.Snapshot("all", 0).Connections); got != 0 {
		t.Fatalf("connections after disabled recording = %d, want 0", got)
	}
}

func hasCleanupKindForRule(suggestions []CleanupSuggestion, kind, ruleName string) bool {
	return cleanupForRule(suggestions, kind, ruleName) != nil
}

func cleanupForRule(suggestions []CleanupSuggestion, kind, ruleName string) *CleanupSuggestion {
	for _, suggestion := range suggestions {
		if suggestion.Kind == kind && suggestion.RuleName == ruleName {
			return &suggestion
		}
	}
	return nil
}

func TestManagerEnablesAndDisablesStoreOnReconfigure(t *testing.T) {
	mgr, err := NewManager(config.TrafficConfig{Enabled: false}, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if mgr.Store() != nil {
		t.Fatal("Store is non-nil for disabled initial config")
	}

	bus := events.NewBus(events.Config{MeterInterval: time.Hour})
	defer bus.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx, bus)
	defer mgr.Stop()

	if err := mgr.Reconfigure(config.TrafficConfig{
		Enabled:       true,
		HistoryLimit:  10,
		HistoryMaxAge: config.Duration(time.Hour),
		HistoryPath:   filepath.Join(t.TempDir(), "traffic-history.json"),
	}); err != nil {
		t.Fatalf("enable Reconfigure: %v", err)
	}
	if mgr.Store() == nil {
		t.Fatal("Store is nil after enabling traffic")
	}

	shard := bus.NewShard()
	bus.NewEmitter(shard).Emit(events.TypeConnectionOpened, events.ConnectionOpenedData{ConnID: "c1"})
	waitForConnections(t, mgr.Store(), 1)

	if err := mgr.Reconfigure(config.TrafficConfig{Enabled: false}); err != nil {
		t.Fatalf("disable Reconfigure: %v", err)
	}
	if mgr.Store() != nil {
		t.Fatal("Store is non-nil after disabling traffic")
	}
}

func TestManagerReconfigureUpdatesExistingStore(t *testing.T) {
	pathA := filepath.Join(t.TempDir(), "a.json")
	pathB := filepath.Join(t.TempDir(), "b.json")
	mgr, err := NewManager(config.TrafficConfig{
		Enabled:       true,
		HistoryLimit:  10,
		HistoryMaxAge: config.Duration(time.Hour),
		HistoryPath:   pathA,
	}, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := mgr.Reconfigure(config.TrafficConfig{
		Enabled:       true,
		HistoryLimit:  20,
		HistoryMaxAge: config.Duration(2 * time.Hour),
		HistoryPath:   pathB,
	}); err != nil {
		t.Fatalf("Reconfigure: %v", err)
	}

	snapshot := mgr.Store().Snapshot("all", 0)
	if snapshot.Summary.HistoryLimit != 20 || snapshot.Summary.HistoryPath != pathB {
		t.Fatalf("summary = %+v, want limit/path update", snapshot.Summary)
	}
}

func waitForConnections(t *testing.T, store *Store, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := len(store.Snapshot("all", 0).Connections); got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("connections = %d, want %d", len(store.Snapshot("all", 0).Connections), want)
}
