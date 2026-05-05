package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/clambhook/clambhook/internal/events"
	"github.com/coder/websocket"
)

const (
	bandwidthSampleLimit = 60
	graphWidth           = 30
	refreshInterval      = 2 * time.Second
	reconnectInterval    = 2 * time.Second
)

type model struct {
	apiAddr string
	client  apiClient

	status   statusPayload
	profiles profilesPayload
	servers  serversPayload

	bandwidth bandwidthSeries
	apiOnline bool
	errText   string

	eventsCh     chan events.Event
	eventErrCh   chan error
	eventCtx     context.Context
	cancelEvents context.CancelFunc
}

type bandwidthSample struct {
	RxBps float64
	TxBps float64
}

type bandwidthSeries struct {
	Samples []bandwidthSample
}

type dashboardLoadedMsg struct {
	Status   statusPayload
	Profiles profilesPayload
	Servers  serversPayload
	Err      error
}

type statusLoadedMsg struct {
	Status statusPayload
	Err    error
}

type actionDoneMsg struct {
	Err error
}

type eventMsg struct {
	Event events.Event
}

type eventErrMsg struct {
	Err error
}

type reconnectEventsMsg struct{}
type eventStreamStartedMsg struct{}
type tickMsg time.Time

func newModel(apiAddr string) model {
	ctx, cancel := context.WithCancel(context.Background())
	return model{
		apiAddr:      apiAddr,
		client:       newAPIClient(apiAddr),
		eventsCh:     make(chan events.Event, 32),
		eventErrCh:   make(chan error, 2),
		eventCtx:     ctx,
		cancelEvents: cancel,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.loadDashboardCmd(),
		m.startEventStreamCmd(),
		waitEventCmd(m.eventsCh, m.eventErrCh),
		tickCmd(),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			if m.cancelEvents != nil {
				m.cancelEvents()
			}
			return m, tea.Quit
		case "c":
			if m.status.Running {
				return m, nil
			}
			return m, m.actionCmd(m.client.connect)
		case "d":
			if !m.status.Running {
				return m, nil
			}
			return m, m.actionCmd(m.client.disconnect)
		case "[":
			return m, m.switchProfileCmd(-1)
		case "]":
			return m, m.switchProfileCmd(1)
		case "r":
			return m, m.loadDashboardCmd()
		}
	case dashboardLoadedMsg:
		if msg.Err != nil {
			m.apiOnline = false
			m.errText = msg.Err.Error()
			return m, nil
		}
		m.apiOnline = true
		m.errText = ""
		m.status = msg.Status
		m.profiles = msg.Profiles
		m.servers = msg.Servers
		return m, nil
	case statusLoadedMsg:
		if msg.Err != nil {
			m.apiOnline = false
			m.errText = msg.Err.Error()
			return m, nil
		}
		m.apiOnline = true
		m.errText = ""
		m.status = msg.Status
		return m, nil
	case actionDoneMsg:
		if msg.Err != nil {
			m.apiOnline = false
			m.errText = msg.Err.Error()
			return m, nil
		}
		return m, m.loadDashboardCmd()
	case eventMsg:
		m.applyEvent(msg.Event)
		return m, waitEventCmd(m.eventsCh, m.eventErrCh)
	case eventErrMsg:
		if msg.Err != nil && m.eventCtx.Err() == nil {
			m.errText = "events: " + msg.Err.Error()
			return m, reconnectEventsCmd()
		}
		return m, nil
	case reconnectEventsMsg:
		return m, tea.Batch(m.startEventStreamCmd(), waitEventCmd(m.eventsCh, m.eventErrCh))
	case eventStreamStartedMsg:
		return m, nil
	case tickMsg:
		return m, tea.Batch(m.loadStatusCmd(), tickCmd())
	}
	return m, nil
}

func (m model) View() string {
	var b strings.Builder

	apiState := "offline"
	if m.apiOnline {
		apiState = "online"
	}
	runState := "STOPPED"
	if m.status.Running {
		runState = "RUNNING"
	}

	fmt.Fprintf(&b, "clambhook %s  API %s (%s)\n", version, m.apiAddr, apiState)
	fmt.Fprintf(&b, "Status: %s  Profile: %s\n", runState, emptyDash(m.status.Profile))
	if m.errText != "" {
		fmt.Fprintf(&b, "Error: %s\n", m.errText)
	}

	b.WriteString("\nListeners\n")
	if len(m.status.Listeners) == 0 {
		b.WriteString("  -- none active\n")
	} else {
		for _, l := range m.status.Listeners {
			fmt.Fprintf(&b, "  %-7s %-21s %d active\n", l.Protocol, l.Addr, l.ActiveConns)
		}
	}

	b.WriteString("\nServers\n")
	if len(m.servers.Chains) == 0 {
		b.WriteString("  -- no servers in active profile\n")
	} else {
		for _, ch := range m.servers.Chains {
			for _, server := range ch.Servers {
				fmt.Fprintf(&b, "  %s %-16s %-11s %-22s %-18s %s\n",
					countryFlag(server.Geo.CountryCode),
					truncate(server.Name, 16),
					server.Protocol,
					truncate(server.Address, 22),
					truncate(serverLocation(server), 18),
					ch.Name,
				)
			}
		}
	}

	current := m.bandwidth.current()
	b.WriteString("\nBandwidth\n")
	fmt.Fprintf(&b, "  Rx %-10s %s\n", formatRate(current.RxBps), m.bandwidth.graph(true))
	fmt.Fprintf(&b, "  Tx %-10s %s\n", formatRate(current.TxBps), m.bandwidth.graph(false))

	b.WriteString("\nKeys: c connect  d disconnect  [ prev profile  ] next profile  r refresh  q quit\n")
	return b.String()
}

func (m model) loadDashboardCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		status, err := client.status()
		if err != nil {
			return dashboardLoadedMsg{Err: err}
		}
		profiles, err := client.profiles()
		if err != nil {
			return dashboardLoadedMsg{Err: err}
		}
		servers, err := client.servers()
		if err != nil {
			return dashboardLoadedMsg{Err: err}
		}
		return dashboardLoadedMsg{Status: status, Profiles: profiles, Servers: servers}
	}
}

func (m model) loadStatusCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		status, err := client.status()
		return statusLoadedMsg{Status: status, Err: err}
	}
}

func (m model) actionCmd(fn func() error) tea.Cmd {
	return func() tea.Msg {
		return actionDoneMsg{Err: fn()}
	}
}

func (m model) switchProfileCmd(delta int) tea.Cmd {
	next, ok := m.profileAt(delta)
	if !ok {
		return nil
	}
	return m.actionCmd(func() error {
		return m.client.setActiveProfile(next)
	})
}

func (m model) profileAt(delta int) (string, bool) {
	if len(m.profiles.Profiles) == 0 {
		return "", false
	}
	active := m.profiles.Active
	if active == "" {
		active = m.status.Profile
	}
	idx := 0
	for i, name := range m.profiles.Profiles {
		if name == active {
			idx = i
			break
		}
	}
	next := (idx + delta + len(m.profiles.Profiles)) % len(m.profiles.Profiles)
	return m.profiles.Profiles[next], true
}

func (m model) startEventStreamCmd() tea.Cmd {
	ctx := m.eventCtx
	client := m.client
	eventsCh := m.eventsCh
	errCh := m.eventErrCh
	return func() tea.Msg {
		go streamEvents(ctx, client, eventsCh, errCh)
		return eventStreamStartedMsg{}
	}
}

func streamEvents(ctx context.Context, client apiClient, eventsCh chan<- events.Event, errCh chan<- error) {
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	c, _, err := websocket.Dial(dialCtx, client.eventsURL(), nil)
	cancel()
	if err != nil {
		sendEventErr(ctx, errCh, err)
		return
	}
	defer c.CloseNow()

	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			if ctx.Err() == nil {
				sendEventErr(ctx, errCh, err)
			}
			return
		}
		var ev events.Event
		if err := json.Unmarshal(data, &ev); err != nil {
			sendEventErr(ctx, errCh, err)
			return
		}
		select {
		case eventsCh <- ev:
		case <-ctx.Done():
			return
		}
	}
}

func sendEventErr(ctx context.Context, errCh chan<- error, err error) {
	select {
	case errCh <- err:
	case <-ctx.Done():
	default:
	}
}

func waitEventCmd(eventsCh <-chan events.Event, errCh <-chan error) tea.Cmd {
	return func() tea.Msg {
		select {
		case ev := <-eventsCh:
			return eventMsg{Event: ev}
		case err := <-errCh:
			return eventErrMsg{Err: err}
		}
	}
}

func reconnectEventsCmd() tea.Cmd {
	return tea.Tick(reconnectInterval, func(time.Time) tea.Msg {
		return reconnectEventsMsg{}
	})
}

func tickCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m *model) applyEvent(ev events.Event) {
	if ev.Type != events.TypeConnectionBytes {
		return
	}
	rxDelta, okRx := eventNumber(ev.Data, "rx_delta")
	txDelta, okTx := eventNumber(ev.Data, "tx_delta")
	intervalNs, okInterval := eventNumber(ev.Data, "interval_ns")
	if !okRx || !okTx || !okInterval || intervalNs <= 0 {
		return
	}
	seconds := intervalNs / float64(time.Second)
	m.bandwidth.add(bandwidthSample{
		RxBps: rxDelta / seconds,
		TxBps: txDelta / seconds,
	})
}

func eventNumber(data any, key string) (float64, bool) {
	switch d := data.(type) {
	case map[string]any:
		v, ok := d[key]
		if !ok {
			return 0, false
		}
		switch n := v.(type) {
		case float64:
			return n, true
		case int:
			return float64(n), true
		case int64:
			return float64(n), true
		case uint64:
			return float64(n), true
		}
	case events.ConnectionBytesData:
		switch key {
		case "rx_delta":
			return float64(d.RxDelta), true
		case "tx_delta":
			return float64(d.TxDelta), true
		case "interval_ns":
			return float64(d.IntervalNs), true
		}
	}
	return 0, false
}

func (s *bandwidthSeries) add(sample bandwidthSample) {
	if len(s.Samples) >= bandwidthSampleLimit {
		copy(s.Samples, s.Samples[1:])
		s.Samples[len(s.Samples)-1] = sample
		return
	}
	s.Samples = append(s.Samples, sample)
}

func (s bandwidthSeries) current() bandwidthSample {
	if len(s.Samples) == 0 {
		return bandwidthSample{}
	}
	return s.Samples[len(s.Samples)-1]
}

func (s bandwidthSeries) graph(rx bool) string {
	if len(s.Samples) == 0 {
		return strings.Repeat(" ", graphWidth)
	}
	start := 0
	if len(s.Samples) > graphWidth {
		start = len(s.Samples) - graphWidth
	}
	values := make([]float64, 0, len(s.Samples)-start)
	var maxValue float64
	for _, sample := range s.Samples[start:] {
		v := sample.TxBps
		if rx {
			v = sample.RxBps
		}
		values = append(values, v)
		if v > maxValue {
			maxValue = v
		}
	}
	if maxValue <= 0 {
		return strings.Repeat("▁", len(values))
	}
	const bars = "▁▂▃▄▅▆▇█"
	var b strings.Builder
	for _, v := range values {
		idx := int(math.Ceil((v/maxValue)*float64(len([]rune(bars)))) - 1)
		if idx < 0 {
			idx = 0
		}
		if idx >= len([]rune(bars)) {
			idx = len([]rune(bars)) - 1
		}
		b.WriteRune([]rune(bars)[idx])
	}
	return b.String()
}

func countryFlag(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' {
		return "--"
	}
	return string([]rune{
		0x1F1E6 + rune(code[0]-'A'),
		0x1F1E6 + rune(code[1]-'A'),
	})
}

func formatRate(bytesPerSecond float64) string {
	if bytesPerSecond < 1024 {
		return fmt.Sprintf("%.0f B/s", bytesPerSecond)
	}
	if bytesPerSecond < 1024*1024 {
		return fmt.Sprintf("%.1f KB/s", bytesPerSecond/1024)
	}
	return fmt.Sprintf("%.1f MB/s", bytesPerSecond/(1024*1024))
}

func serverLocation(server serverPayload) string {
	if server.GeoError != "" {
		return "geo error"
	}
	if server.Geo.City != "" && server.Geo.Country != "" {
		return server.Geo.City + ", " + server.Geo.Country
	}
	if server.Geo.Country != "" {
		return server.Geo.Country
	}
	return "--"
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "--"
	}
	return s
}

func truncate(s string, max int) string {
	if len([]rune(s)) <= max {
		return s
	}
	r := []rune(s)
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}
