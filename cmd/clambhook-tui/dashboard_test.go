package main

import (
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

	if got := c.eventsURL(); !strings.Contains(got, "types=connection.*,log.*") {
		t.Fatalf("eventsURL() = %q, want connection and log event filters", got)
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

func TestDashboardViewIncludesStatusServersAndGraph(t *testing.T) {
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
	m.bandwidth.add(bandwidthSample{RxBps: 2048, TxBps: 1024})

	view := m.View()
	for _, want := range []string{"RUNNING", "B", "socks5", "🇬🇧", "london", "trojan", "Rx", "Tx"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestLogViewToggleAndRender(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.apiOnline = true
	m.appendLogLine("api listening on 127.0.0.1:9090")

	updated, _ := m.Update(keyMsg("l"))
	m = updated.(model)

	if m.viewMode != viewModeLogs {
		t.Fatalf("viewMode = %v, want logs", m.viewMode)
	}
	view := m.View()
	for _, want := range []string{"Logs", "api listening on 127.0.0.1:9090", "l dashboard"} {
		if !strings.Contains(view, want) {
			t.Fatalf("log view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Servers") {
		t.Fatalf("log view should not render dashboard sections:\n%s", view)
	}
}

func TestLogModeScrollKeysDoNotMoveProfileSelection(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.viewMode = viewModeLogs
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
	if m.logScroll != 1 {
		t.Fatalf("logScroll after up = %d, want 1", m.logScroll)
	}

	updated, _ = m.Update(keyMsg("down"))
	m = updated.(model)
	if m.selectedProfile != 1 {
		t.Fatalf("selectedProfile after down = %d, want 1", m.selectedProfile)
	}
	if m.logScroll != 0 {
		t.Fatalf("logScroll after down = %d, want 0", m.logScroll)
	}
}

func TestWindowSizeLimitsRenderedLogLines(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.viewMode = viewModeLogs
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
	for _, want := range []string{"target-00.example:443", "target-01.example:443", "+3 more (press t)"} {
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
	m.viewMode = viewModeTraffic

	_, cmd := m.Update(keyMsg("r"))
	if cmd == nil {
		t.Fatal("traffic view r key returned nil command")
	}
}

func TestProfileListRendersEmojiNamesAndActiveMarker(t *testing.T) {
	m := newModel("127.0.0.1:9090")
	m.profiles = profilesPayload{
		Profiles: []string{"🇺🇸 US Fast", "🔒 Double Hop", "🎭 Reality"},
		Active:   "🔒 Double Hop",
	}
	m.status = statusPayload{Profile: "🔒 Double Hop"}
	m.syncSelectedProfile()

	view := m.View()
	for _, want := range []string{"Profiles", "  🇺🇸 US Fast", "● 🔒 Double Hop", "  🎭 Reality"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestProfileSelectionMovesAndWraps(t *testing.T) {
	m := newModel("127.0.0.1:9090")
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
