//go:build unix

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/events"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestEventsURLSubscribesToConnectionAndLogEvents(t *testing.T) {
	c := newAPIClientFromBaseURL("http://127.0.0.1:9090")

	if got := c.eventsURL(); !strings.Contains(got, "types=connection.*,rule.*,hop.*,log.*") {
		t.Fatalf("eventsURL() = %q, want connection/rule/hop/log event filters", got)
	}
}

func TestCountryFlag(t *testing.T) {
	if got := countryFlag("GB"); got != "🇬🇧" {
		t.Fatalf("countryFlag(GB) = %q, want 🇬🇧", got)
	}
	if got := countryFlag(""); got != "--" {
		t.Fatalf("countryFlag(empty) = %q, want --", got)
	}
}

func TestBandwidthSeriesKeepsLatestSamplesAndFormatsRates(t *testing.T) {
	var series bandwidthSeries
	for i := 0; i < 65; i++ {
		series.add(bandwidthSample{RxBps: float64(i), TxBps: float64(i * 2)})
	}

	if len(series.Samples) != bandwidthSampleLimit {
		t.Fatalf("samples = %d, want %d", len(series.Samples), bandwidthSampleLimit)
	}
	if series.Samples[0].RxBps != 5 {
		t.Fatalf("first sample rx = %.0f, want 5", series.Samples[0].RxBps)
	}
	if got := formatRate(500); got != "500 B/s" {
		t.Fatalf("formatRate(500) = %q", got)
	}
	if got := formatRate(1536); got != "1.5 KB/s" {
		t.Fatalf("formatRate(1536) = %q", got)
	}
	if got := formatRate(2 * 1024 * 1024); got != "2.0 MB/s" {
		t.Fatalf("formatRate(2MiB) = %q", got)
	}
}

func TestDashboardAppliesConnectionBytesToAggregateGraph(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.applyEvent(events.Event{
		Type: events.TypeConnectionBytes,
		Data: map[string]any{
			"rx_delta":    float64(2048),
			"tx_delta":    float64(1024),
			"interval_ns": float64(time.Second),
		},
	})

	if len(m.bandwidth.Samples) != 1 {
		t.Fatalf("samples = %d, want 1", len(m.bandwidth.Samples))
	}
	sample := m.bandwidth.Samples[0]
	if sample.RxBps != 2048 || sample.TxBps != 1024 {
		t.Fatalf("sample = %+v, want rx=2048 tx=1024", sample)
	}
}

func TestDashboardAppliesLogLineEventsWithCap(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	for i := 0; i < maxLogLines+5; i++ {
		m.applyEvent(events.Event{
			Type: events.TypeLogLine,
			Data: map[string]any{"line": fmt.Sprintf("line-%d", i)},
		})
	}

	if len(m.logs) != maxLogLines {
		t.Fatalf("logs = %d, want %d", len(m.logs), maxLogLines)
	}
	if m.logs[0] != "line-5" {
		t.Fatalf("first retained log = %q, want line-5", m.logs[0])
	}
	if m.logs[len(m.logs)-1] != fmt.Sprintf("line-%d", maxLogLines+4) {
		t.Fatalf("last retained log = %q", m.logs[len(m.logs)-1])
	}
}

func TestNowViewIncludesStatusAndGraph(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.apiOnline = true
	m.status = statusPayload{
		Running: true,
		Profile: "B",
		Listeners: []listenerStatusPayload{{
			Protocol:    "socks5",
			Addr:        "127.0.0.1:1080",
			ActiveConns: 2,
		}},
	}
	m.servers = serversPayload{
		Profile: "B",
		Chains: []chainPayload{{
			Name: "default",
			Servers: []serverPayload{{
				Name:     "london",
				Address:  "81.2.69.142:443",
				Protocol: "trojan",
				Geo: locationPayload{
					Country:     "United Kingdom",
					CountryCode: "GB",
					City:        "London",
				},
			}},
		}},
	}
	m.policies = policyGroupsPayload{
		Profile: "B",
		Groups: []policyGroupPayload{{
			Name:            "auto",
			Type:            "url-test",
			Chains:          []string{"default", "backup"},
			SelectedChain:   "default",
			SelectionReason: "lowest_latency",
			Results: []policyProbeResultPayload{{
				ChainName:  "default",
				Healthy:    true,
				LatencyNs:  int64(42 * time.Millisecond),
				StatusCode: 204,
			}},
		}},
	}
	m.traffic = trafficSnapshotPayload{
		Summary: trafficSummaryPayload{
			ActiveConnections: 2,
			RxBps:             2048,
			TxBps:             1024,
			RxTotal:           4096,
			TxTotal:           2048,
		},
		Connections: []trafficConnectionPayload{{
			Target:     "example.com:443",
			TargetHost: "example.com",
			RuleName:   "web",
			RuleAction: "group",
			GroupName:  "auto",
			ChainName:  "default",
			RouteControl: routeControlPayload{
				Mode:            "rule",
				Decision:        "proxy",
				Source:          "profile_rule",
				RuleName:        "web",
				RuleNumber:      1,
				PolicyGroup:     "auto",
				SelectedChain:   "default",
				SelectionReason: "lowest_latency",
			},
			RxTotal: 4096,
			TxTotal: 2048,
		}},
	}
	m.bandwidth.add(bandwidthSample{RxBps: 2048, TxBps: 1024})

	view := m.View()
	for _, want := range []string{"Now", "Connection", "RUNNING", "B", "Route Control", "Mode Rule", "Proxy 1", "Direct 0", "Block 0", "auto", "url test", "Selected default", "Fallback No", "Live Traffic", "Rx", "Tx", "Recent Decisions", "PROXY", "example.com", "web", "auto -> default"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	for _, hidden := range []string{"socks5", "🇬🇧", "london", "trojan"} {
		if strings.Contains(view, hidden) {
			t.Fatalf("now view should not render library detail %q:\n%s", hidden, view)
		}
	}

	m.viewMode = viewModeLibrary
	view = m.View()
	for _, want := range []string{"Library", "socks5", "🇬🇧", "london", "trojan"} {
		if !strings.Contains(view, want) {
			t.Fatalf("library view missing %q:\n%s", want, view)
		}
	}
}

func TestNowViewShowsStaticPolicySummaryWithoutPolicyGroups(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.apiOnline = true
	m.status = statusPayload{Running: true, Profile: "A"}
	m.servers = serversPayload{
		Profile: "A",
		Chains:  []chainPayload{{Name: "default"}},
	}

	view := m.View()
	for _, want := range []string{"Route Control", "Mode Rule", "Static route", "Selected default", "Fallback No", "1 route"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestActivityViewToggleAndRenderLogs(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.apiOnline = true
	m.appendLogLine("api listening on 127.0.0.1:9090")

	updated, _ := m.Update(keyMsg("2"))
	m = updated.(model)

	if m.viewMode != viewModeActivity {
		t.Fatalf("viewMode = %v, want activity", m.viewMode)
	}
	view := m.View()
	for _, want := range []string{"Activity", "Logs", "api listening on 127.0.0.1:9090", "1 now"} {
		if !strings.Contains(view, want) {
			t.Fatalf("activity view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Servers") {
		t.Fatalf("activity view should not render library sections:\n%s", view)
	}
}

func TestActivityModeSelectionKeysDoNotMoveProfileSelection(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.viewMode = viewModeActivity
	m.height = 6
	m.profiles = profilesPayload{Profiles: []string{"A", "B", "C"}, Active: "B"}
	m.syncSelectedProfile()
	for i := 0; i < 10; i++ {
		m.appendLogLine(fmt.Sprintf("line-%d", i))
	}

	updated, _ := m.Update(keyMsg("up"))
	m = updated.(model)
	if m.selectedProfile != 1 {
		t.Fatalf("selectedProfile = %d, want 1", m.selectedProfile)
	}

	updated, _ = m.Update(keyMsg("down"))
	m = updated.(model)
	if m.selectedProfile != 1 {
		t.Fatalf("selectedProfile after down = %d, want 1", m.selectedProfile)
	}
}

func TestParseRuleTestInput(t *testing.T) {
	network, target, err := parseRuleTestInput("udp 1.1.1.1:53")
	if err != nil {
		t.Fatalf("parseRuleTestInput: %v", err)
	}
	if network != "udp" || target != "1.1.1.1:53" {
		t.Fatalf("parsed = %q %q", network, target)
	}
	for _, input := range []string{"icmp 1.1.1.1:53", "tcp example.com", "udp example.com:bad"} {
		if _, _, err := parseRuleTestInput(input); err == nil {
			t.Fatalf("parseRuleTestInput(%q) returned nil error", input)
		}
	}
}

func TestLibraryViewShowsPolicyUDPCapability(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.viewMode = viewModeLibrary
	m.servers = serversPayload{
		Profile: "A",
		Chains: []chainPayload{{
			Name:     "proxy",
			HopCount: 2,
			Capabilities: protocolCapabilitiesPayload{
				UDP:       false,
				UDPMode:   "unsupported",
				UDPReason: "final protocol cannot carry UDP through an upstream chain",
			},
			Servers: []serverPayload{{
				Name:     "exit",
				Address:  "203.0.113.10:443",
				Protocol: "shadowsocks",
			}},
		}},
	}

	view := m.View()
	for _, want := range []string{"Proxy Policies", "Policy proxy", "2 hops", "UDP unsupported"} {
		if !strings.Contains(view, want) {
			t.Fatalf("library view missing %q:\n%s", want, view)
		}
	}
}

func TestLibraryViewShowsPolicyGroupHealthAndFallback(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.viewMode = viewModeLibrary
	m.policies = policyGroupsPayload{
		Profile: "A",
		Groups: []policyGroupPayload{{
			Name:          "auto",
			Type:          "url-test",
			Chains:        []string{"proxy", "backup"},
			SelectedChain: "proxy",
			Results: []policyProbeResultPayload{
				{ChainName: "proxy", Healthy: false, Error: "timeout"},
				{ChainName: "backup", Healthy: true, LatencyNs: int64(30 * time.Millisecond), StatusCode: 204},
			},
		}},
	}

	view := m.View()
	for _, want := range []string{"Policy Groups", "auto", "selected proxy", "Fallback / 1/2 healthy", "backup", "30ms", "timeout"} {
		if !strings.Contains(view, want) {
			t.Fatalf("library view missing %q:\n%s", want, view)
		}
	}
}

func TestLibraryPolicySelectionSendsSelectionRequest(t *testing.T) {
	var got struct {
		Profile string `json:"profile"`
		Group   string `json:"group"`
		Chain   string `json:"chain"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/policy-groups/selection" || r.Method != http.MethodPut {
			t.Fatalf("request = %s %s, want PUT policy selection", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"policy_groups":{"profile":"A","groups":[{"name":"manual","type":"select","chains":["proxy","backup"],"selected_chain":"backup","selection_mode":"manual"}]}}`))
	}))
	defer srv.Close()

	m := newModel("127.0.0.1:9090")
	m.client = newAPIClientFromBaseURL(srv.URL)
	m.viewMode = viewModeLibrary
	m.profiles = profilesPayload{Active: "A"}
	m.policyFocus = true
	m.selectedPolicyMember = 1
	m.policies = policyGroupsPayload{Profile: "A", Groups: []policyGroupPayload{{
		Name:          "manual",
		Type:          "select",
		Chains:        []string{"proxy", "backup"},
		SelectedChain: "proxy",
		SelectionMode: "manual",
	}}}

	_, cmd := m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("enter returned nil command")
	}
	msg := cmd()
	done, ok := msg.(policyGroupsDoneMsg)
	if !ok {
		t.Fatalf("message = %T, want policyGroupsDoneMsg", msg)
	}
	if done.Err != nil {
		t.Fatalf("selection error: %v", done.Err)
	}
	if got.Profile != "A" || got.Group != "manual" || got.Chain != "backup" {
		t.Fatalf("request = %+v", got)
	}
	if len(done.Policies.Groups) != 1 || done.Policies.Groups[0].SelectedChain != "backup" {
		t.Fatalf("policies = %+v", done.Policies)
	}
}

func TestWindowSizeLimitsRenderedLogLines(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.viewMode = viewModeActivity
	m.height = 7
	for i := 0; i < 10; i++ {
		m.appendLogLine(fmt.Sprintf("entry-%02d", i))
	}

	view := m.View()
	for _, want := range []string{"entry-08", "entry-09"} {
		if !strings.Contains(view, want) {
			t.Fatalf("log view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "entry-07") {
		t.Fatalf("log view rendered too many rows:\n%s", view)
	}
}

func TestBandwidthGraphUsesRequestedWidth(t *testing.T) {
	var series bandwidthSeries
	for i := 1; i <= 5; i++ {
		series.add(bandwidthSample{RxBps: float64(i), TxBps: float64(i * 2)})
	}

	if got := series.graph(true, 8); lipgloss.Width(got) != 8 {
		t.Fatalf("graph width = %d, want 8 (%q)", lipgloss.Width(got), got)
	}
	if got := series.graph(false, 3); lipgloss.Width(got) != 3 {
		t.Fatalf("small graph width = %d, want 3 (%q)", lipgloss.Width(got), got)
	}
}

func TestDashboardClipsTrafficPreviewAtSmallHeight(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.height = 20
	m.traffic.Summary.ActiveConnections = 5
	for i := 0; i < 5; i++ {
		m.traffic.Connections = append(m.traffic.Connections, trafficConnectionPayload{
			State:      "open",
			Target:     fmt.Sprintf("target-%02d.example:443", i),
			RxTotal:    uint64(1024 * (i + 1)),
			TxTotal:    uint64(512 * (i + 1)),
			DurationNs: int64((i + 1)) * int64(time.Second),
		})
	}

	view := m.View()
	for _, want := range []string{"Recent Decisions", "target-00.example:443", "target-01.example:443", "+3 more (press 2)"} {
		if !strings.Contains(view, want) {
			t.Fatalf("dashboard traffic preview missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "target-02.example:443") {
		t.Fatalf("dashboard rendered more traffic rows than expected:\n%s", view)
	}
}

func TestNarrowDashboardLinesFitWidth(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.width = 44
	m.apiOnline = true
	m.status = statusPayload{
		Running: true,
		Profile: "very-long-profile-name-for-terminal",
		Listeners: []listenerStatusPayload{{
			Protocol:    "socks5",
			Addr:        "127.0.0.1:1080",
			ActiveConns: 2,
		}},
	}
	m.profiles = profilesPayload{
		Profiles: []string{"very-long-profile-name-for-terminal", "🇺🇸 backup-profile-with-long-name"},
		Active:   "very-long-profile-name-for-terminal",
	}
	m.servers = serversPayload{
		Chains: []chainPayload{{
			Name: "very-long-chain-name",
			Servers: []serverPayload{{
				Name:     "long-london-server-name",
				Address:  "very-long-hostname.example.test:443",
				Protocol: "trojan",
				Geo: locationPayload{
					Country:     "United Kingdom",
					CountryCode: "GB",
					City:        "London",
				},
			}},
		}},
	}
	m.bandwidth.add(bandwidthSample{RxBps: 2048, TxBps: 1024})

	view := m.View()
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > m.width {
			t.Fatalf("line width = %d, want <= %d:\n%s\nfull view:\n%s", got, m.width, line, view)
		}
	}
}

func TestTrafficModeRefreshKeyReturnsCommand(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.viewMode = viewModeActivity

	_, cmd := m.Update(keyMsg("r"))
	if cmd == nil {
		t.Fatal("traffic view r key returned nil command")
	}
}

func TestTrafficMonitorFiltersAndCreatesRuleDraft(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.viewMode = viewModeActivity
	m.traffic.Connections = []trafficConnectionPayload{
		{Target: "ads.example.com:443", TargetHost: "ads.example.com", RuleAction: "block", RuleName: "ads"},
		{Target: "example.com:443", TargetHost: "example.com", ChainName: "proxy"},
	}

	updated, _ := m.Update(keyMsg("b"))
	m = updated.(model)
	if rows := m.filteredTrafficConnections(); len(rows) != 1 || rows[0].TargetHost != "ads.example.com" {
		t.Fatalf("filtered rows = %+v", rows)
	}

	updated, _ = m.Update(keyMsg("n"))
	m = updated.(model)
	if m.pendingRule == nil || m.pendingRule.Action != "block" || len(m.pendingRule.Domains) != 1 || m.pendingRule.Domains[0] != "ads.example.com" {
		t.Fatalf("pending rule = %+v", m.pendingRule)
	}
}

func TestTrafficRefreshCommandsUseTokenFilters(t *testing.T) {
	var trafficQueries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/traffic" {
			trafficQueries = append(trafficQueries, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	m := newModel("127.0.0.1:9090")
	m.client = newAPIClientFromBaseURL(srv.URL)
	m.filterTokens = []filterToken{
		{Key: "action", Value: "block"},
		{Key: "app", Value: "Example App"},
		{Key: "domain", Value: "api.example.com"},
		{Key: "country", Value: "GB"},
		{Key: "port", Value: "443"},
		{Value: "search & more"},
	}

	if msg := m.loadDashboardCmd()(); msg.(dashboardLoadedMsg).Err != nil {
		t.Fatalf("loadDashboardCmd() error = %v", msg.(dashboardLoadedMsg).Err)
	}
	if msg := m.loadStatusCmd()(); msg.(statusLoadedMsg).Err != nil {
		t.Fatalf("loadStatusCmd() error = %v", msg.(statusLoadedMsg).Err)
	}
	if len(trafficQueries) != 2 {
		t.Fatalf("traffic requests = %d, want 2", len(trafficQueries))
	}
	for _, rawQuery := range trafficQueries {
		values := make(map[string]string)
		for _, part := range strings.Split(rawQuery, "&") {
			pair := strings.SplitN(part, "=", 2)
			if len(pair) == 2 {
				values[pair[0]] = pair[1]
			}
		}
		for key, want := range map[string]string{
			"action":  "block",
			"app":     "Example+App",
			"country": "GB",
			"domain":  "api.example.com",
			"limit":   "200",
			"port":    "443",
			"query":   "search+%26+more",
		} {
			if got := values[key]; got != want {
				t.Fatalf("%s query value = %q, want %q (raw query %q)", key, got, want, rawQuery)
			}
		}
	}
}

func TestTrafficMonitorCreatesRuleDraftFromSuggestion(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.viewMode = viewModeActivity
	m.traffic.Connections = []trafficConnectionPayload{
		{Target: "example.com:443", TargetHost: "example.com", ChainName: "proxy"},
	}
	m.traffic.RuleSuggestions = []ruleSuggestionPayload{{
		Kind:   "domain_suffix",
		Action: "block",
		Count:  3,
		DraftRule: rulePayload{
			Name:           "block-example-com",
			Action:         "block",
			DomainSuffixes: []string{"example.com"},
			Ports:          []int{443},
			Networks:       []string{"tcp"},
		},
		Reason: "Observed 3 connections across 2 subdomains.",
	}}

	updated, _ := m.Update(keyMsg("tab"))
	m = updated.(model)
	if !m.suggestionFocus {
		t.Fatal("suggestionFocus = false, want true")
	}
	updated, _ = m.Update(keyMsg("n"))
	m = updated.(model)
	if m.pendingRule == nil || m.pendingRule.Name != "block-example-com" || len(m.pendingRule.DomainSuffixes) != 1 || m.pendingRule.DomainSuffixes[0] != "example.com" {
		t.Fatalf("pending rule = %+v", m.pendingRule)
	}
}

func TestTrafficMonitorSavesConnectionRuleFromConnectionEndpoint(t *testing.T) {
	var requests []string
	var gotReq createRuleFromConnectionRequest
	var decodeErr error
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/api/v1/rules/from-connection" {
			decodeErr = json.NewDecoder(r.Body).Decode(&gotReq)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := newModel("127.0.0.1:9090")
	m.client = newAPIClientFromBaseURL(srv.URL)
	m.viewMode = viewModeActivity
	m.traffic.Connections = []trafficConnectionPayload{{
		ConnID:     "c1",
		Profile:    "Work",
		Target:     "ads.example.com:443",
		TargetHost: "ads.example.com",
		RuleAction: "block",
		RuleName:   "ads",
	}}

	updated, _ := m.Update(keyMsg("n"))
	m = updated.(model)
	if m.pendingRule == nil || m.pendingRule.ConnID != "c1" {
		t.Fatalf("pending rule = %+v", m.pendingRule)
	}
	updated, _ = m.Update(keyMsg("a"))
	m = updated.(model)
	if m.pendingRule == nil || m.pendingRule.Action != "allow" {
		t.Fatalf("pending rule after allow = %+v", m.pendingRule)
	}
	_, cmd := m.Update(keyMsg("y"))
	if cmd == nil {
		t.Fatal("save returned nil command")
	}
	_ = cmd()

	if decodeErr != nil {
		t.Fatalf("decode request: %v", decodeErr)
	}
	if len(requests) != 1 || requests[0] != "POST /api/v1/rules/from-connection" {
		t.Fatalf("requests = %v, want rules/from-connection", requests)
	}
	if gotReq.ConnID != "c1" || gotReq.Profile != "Work" || gotReq.Name != "block-ads-example-com" || gotReq.Action != "allow" || gotReq.Scope != "auto" || gotReq.Position != "append" {
		t.Fatalf("request = %+v", gotReq)
	}
}

func TestTrafficMonitorAppliesCleanupSuggestion(t *testing.T) {
	var requests []string
	var gotReq cleanupRuleRequest
	var decodeErr error
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/api/v1/rules/cleanup" {
			decodeErr = json.NewDecoder(r.Body).Decode(&gotReq)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := newModel("127.0.0.1:9090")
	m.client = newAPIClientFromBaseURL(srv.URL)
	m.viewMode = viewModeActivity
	m.traffic.CleanupSuggestions = []cleanupSuggestionPayload{{
		Kind:           "unused_in_history",
		Profile:        "Work",
		RuleName:       "old-rule",
		TargetRuleName: "old-rule",
		Operation:      "delete_rule",
		Message:        "No recent traffic-history entries matched this rule.",
	}}

	updated, _ := m.Update(keyMsg("c"))
	m = updated.(model)
	if m.pendingCleanup == nil || m.pendingCleanup.RuleName != "old-rule" {
		t.Fatalf("pending cleanup = %+v", m.pendingCleanup)
	}
	_, cmd := m.Update(keyMsg("y"))
	if cmd == nil {
		t.Fatal("cleanup returned nil command")
	}
	_ = cmd()

	if decodeErr != nil {
		t.Fatalf("decode request: %v", decodeErr)
	}
	if len(requests) != 1 || requests[0] != "POST /api/v1/rules/cleanup" {
		t.Fatalf("requests = %v, want cleanup endpoint", requests)
	}
	if gotReq.Profile != "Work" || gotReq.Kind != "unused_in_history" || gotReq.RuleName != "old-rule" || gotReq.TargetRuleName != "old-rule" || gotReq.Operation != "delete_rule" {
		t.Fatalf("request = %+v", gotReq)
	}
}

func TestTrafficMonitorSavesSuggestionRuleThroughRulesEndpoint(t *testing.T) {
	var requests []string
	var gotReq createRuleRequest
	var decodeErr error
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/api/v1/rules" {
			decodeErr = json.NewDecoder(r.Body).Decode(&gotReq)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := newModel("127.0.0.1:9090")
	m.client = newAPIClientFromBaseURL(srv.URL)
	m.viewMode = viewModeActivity
	m.traffic.RuleSuggestions = []ruleSuggestionPayload{{
		DraftRule: rulePayload{Name: "block-api", Action: "block", Domains: []string{"api.example.com"}},
	}}

	updated, _ := m.Update(keyMsg("tab"))
	m = updated.(model)
	updated, _ = m.Update(keyMsg("n"))
	m = updated.(model)
	_, cmd := m.Update(keyMsg("y"))
	if cmd == nil {
		t.Fatal("save returned nil command")
	}
	_ = cmd()

	if decodeErr != nil {
		t.Fatalf("decode request: %v", decodeErr)
	}
	if len(requests) != 1 || requests[0] != "POST /api/v1/rules" {
		t.Fatalf("requests = %v, want rules endpoint", requests)
	}
	if gotReq.Rule.Name != "block-api" || gotReq.Rule.Action != "block" || len(gotReq.Rule.Domains) != 1 || gotReq.Rule.Domains[0] != "api.example.com" {
		t.Fatalf("request = %+v", gotReq)
	}
}

func TestTrafficMonitorSearchMatchesRuleAndHost(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.traffic.Connections = []trafficConnectionPayload{
		{TargetHost: "ads.example.com", RuleName: "ads"},
		{TargetHost: "example.org", RuleName: "default"},
	}
	m.searchText = "ads"

	rows := m.filteredTrafficConnections()
	if len(rows) != 1 || rows[0].TargetHost != "ads.example.com" {
		t.Fatalf("filtered rows = %+v", rows)
	}
}

func TestTrafficMonitorRendersBackendAnalytics(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.viewMode = viewModeActivity
	m.width = 120
	m.traffic.ProfileContext = profileContextPayload{Active: "Work", Profiles: []string{"Work", "Home"}}
	m.traffic.QuickFilters = []quickFilterPayload{
		{Key: "all", Label: "All", Count: 9},
		{Key: "proxy", Label: "Proxy", Count: 4},
		{Key: "direct", Label: "Direct", Count: 2},
		{Key: "block", Label: "Block", Count: 3},
	}
	m.traffic.RuleHits = []ruleHitPayload{{RuleName: "ads", Action: "block", Count: 3}}
	m.traffic.BlockDecisions = []blockDecisionPayload{{TargetHost: "ads.example.com", RuleName: "ads", Action: "block"}}
	m.traffic.CleanupSuggestions = []cleanupSuggestionPayload{{RuleName: "old-rule", TargetRuleName: "old-rule", Operation: "delete_rule", Message: "No recent traffic-history entries matched this rule."}}
	m.traffic.RuleSuggestions = []ruleSuggestionPayload{{
		Kind:      "exact_host",
		Action:    "block",
		Count:     2,
		DraftRule: rulePayload{Name: "block-api", Action: "block", Domains: []string{"api.example.com"}},
		Reason:    "Observed 2 matching connections.",
	}}
	m.traffic.Breakdowns = trafficBreakdownsPayload{
		Actions: []breakdownRowPayload{{Key: "block", Label: "Block", Count: 3}},
		Chains:  []breakdownRowPayload{{Key: "proxy", Label: "proxy", Count: 4}},
		Rules:   []breakdownRowPayload{{Key: "ads", Label: "ads", Count: 3}},
	}
	m.traffic.Connections = []trafficConnectionPayload{{
		ConnID:     "c1",
		Profile:    "Work",
		Target:     "ads.example.com:443",
		TargetHost: "ads.example.com",
		RuleAction: "block",
		RuleName:   "ads",
		RouteControl: routeControlPayload{
			Mode:       "rule",
			Decision:   "block",
			Source:     "profile_rule",
			RuleName:   "ads",
			RuleNumber: 1,
		},
	}}

	view := m.View()
	for _, want := range []string{"profile Work", "all 9", "proxy 4", "direct 2", "block 3", "Routes", "Top chains", "Top rules", "Rule hits", "ads/block 3", "Recent blocks", "Route Control", "Mode Rule", "Decision BLOCK", "Source profile rule", "Fallback No", "Rule cleanup", "Delete", "Action enter/n create rule from connection", "Suggested rules", "api.example.com"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestDeveloperViewRendersStatusAndCaptures(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.viewMode = viewModeDeveloper
	m.dev = developerStatusPayload{
		Enabled:             true,
		MITMEnabled:         true,
		CaptureLimit:        200,
		BodyLimitBytes:      65536,
		CACertPath:          "/tmp/clambhook-ca.pem",
		CAFingerprintSHA256: "ABCDEF",
		CaptureCount:        1,
	}
	m.devRows = []developerEntryPayload{{
		Method:  "GET",
		URL:     "https://example.com/api",
		Scheme:  "https",
		Status:  200,
		Profile: "Work",
		Request: developerMessagePayload{Body: developerBodyPayload{Size: 10, PreviewBytes: 10}},
	}}

	view := m.View()
	for _, want := range []string{"Developer", "MITM on", "clambhook-ca.pem", "GET", "https://example.com/api", "Request Detail"} {
		if !strings.Contains(view, want) {
			t.Fatalf("developer view missing %q:\n%s", want, view)
		}
	}
}

func TestProfileListRendersEmojiNamesAndActiveMarker(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.viewMode = viewModeLibrary
	m.profiles = profilesPayload{
		Profiles: []string{"🇺🇸 US Fast", "🔒 Double Hop", "🔐 ClambBack"},
		Active:   "🔒 Double Hop",
	}
	m.status = statusPayload{Profile: "🔒 Double Hop"}
	m.syncSelectedProfile()

	view := m.View()
	for _, want := range []string{"Profiles", "  🇺🇸 US Fast", "● 🔒 Double Hop", "  🔐 ClambBack"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestProfileSelectionMovesAndWraps(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.viewMode = viewModeLibrary
	m.profiles = profilesPayload{
		Profiles: []string{"A", "B", "C"},
		Active:   "B",
	}
	m.syncSelectedProfile()

	if m.selectedProfile != 1 {
		t.Fatalf("selectedProfile = %d, want 1", m.selectedProfile)
	}

	updated, _ := m.Update(keyMsg("down"))
	m = updated.(model)
	if m.selectedProfile != 2 {
		t.Fatalf("after down selectedProfile = %d, want 2", m.selectedProfile)
	}

	updated, _ = m.Update(keyMsg("down"))
	m = updated.(model)
	if m.selectedProfile != 0 {
		t.Fatalf("after wrap down selectedProfile = %d, want 0", m.selectedProfile)
	}

	updated, _ = m.Update(keyMsg("up"))
	m = updated.(model)
	if m.selectedProfile != 2 {
		t.Fatalf("after wrap up selectedProfile = %d, want 2", m.selectedProfile)
	}
}

func TestEnterSwitchesSelectedInactiveProfile(t *testing.T) {
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"running":true,"profile":"B"}`))
	}))
	defer srv.Close()

	m := newModel("127.0.0.1:9090")
	m.viewMode = viewModeLibrary
	m.client = newAPIClientFromBaseURL(srv.URL)
	m.status = statusPayload{Profile: "A"}
	m.profiles = profilesPayload{Profiles: []string{"A", "B"}, Active: "A"}
	m.selectedProfile = 1

	_, cmd := m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("enter returned nil command for inactive selected profile")
	}
	_ = cmd()

	if len(requests) == 0 || requests[0] != "PUT /api/v1/profiles/active" {
		t.Fatalf("first request = %v, want PUT /api/v1/profiles/active", requests)
	}
}

func TestEnterOnActiveProfileDoesNotCallAPI(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.viewMode = viewModeLibrary
	m.status = statusPayload{Profile: "A"}
	m.profiles = profilesPayload{Profiles: []string{"A", "B"}, Active: "A"}
	m.selectedProfile = 0

	_, cmd := m.Update(keyMsg("enter"))
	if cmd != nil {
		t.Fatal("enter returned command for already-active selected profile")
	}
}

func TestDashboardLoadRealignsSelectedProfileToActive(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.selectedProfile = 0

	updated, _ := m.Update(dashboardLoadedMsg{
		Status:   statusPayload{Profile: "C"},
		Profiles: profilesPayload{Profiles: []string{"A", "B", "C"}, Active: "C"},
		Servers:  serversPayload{},
	})
	m = updated.(model)

	if m.selectedProfile != 2 {
		t.Fatalf("selectedProfile = %d, want 2", m.selectedProfile)
	}
}

func TestKeyActionsCallExpectedAPIEndpoints(t *testing.T) {
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"running":true,"profile":"B"}`))
	}))
	defer srv.Close()

	cases := []struct {
		key  string
		want string
	}{
		{key: "c", want: "POST /api/v1/connect"},
		{key: "d", want: "POST /api/v1/disconnect"},
		{key: "]", want: "PUT /api/v1/profiles/active"},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			requests = nil
			m := newModel("127.0.0.1:9090")
			m.client = newAPIClientFromBaseURL(srv.URL)
			m.status = statusPayload{Profile: "A", Running: tc.key == "d"}
			m.profiles = profilesPayload{Profiles: []string{"A", "B"}, Active: "A"}
			if tc.key == "]" {
				m.viewMode = viewModeLibrary
			}

			_, cmd := m.Update(keyMsg(tc.key))
			if cmd == nil {
				t.Fatalf("Update(%q) returned nil command", tc.key)
			}
			_ = cmd()

			if len(requests) == 0 || requests[0] != tc.want {
				t.Fatalf("first request = %v, want %q", requests, tc.want)
			}
		})
	}
}

func keyMsg(key string) tea.KeyMsg {
	switch key {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
}
