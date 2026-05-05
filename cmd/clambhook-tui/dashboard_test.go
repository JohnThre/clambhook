package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/clambhook/clambhook/internal/events"
)

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
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
}
