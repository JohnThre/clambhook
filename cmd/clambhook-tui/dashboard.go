package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/JohnThre/clambhook/internal/events"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"
)

const (
	bandwidthSampleLimit = 60
	graphWidth           = 30
	maxLogLines          = 200
	defaultLogViewHeight = 24
	refreshInterval      = 2 * time.Second
	reconnectInterval    = 2 * time.Second
)

type viewMode int

const (
	viewModeDashboard viewMode = iota
	viewModeLogs
	viewModeTraffic
)

type model struct {
	apiAddr string
	client  apiClient

	status   statusPayload
	profiles profilesPayload
	servers  serversPayload
	traffic  trafficSnapshotPayload

	selectedProfile int
	viewMode        viewMode
	width           int
	height          int

	bandwidth bandwidthSeries
	logs      []string
	logScroll int
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
	Traffic  trafficSnapshotPayload
	Err      error
}

type statusLoadedMsg struct {
	Status  statusPayload
	Traffic trafficSnapshotPayload
	Err     error
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
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.clampLogScroll()
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			if m.cancelEvents != nil {
				m.cancelEvents()
			}
			return m, tea.Quit
		case "l":
			if m.viewMode == viewModeLogs {
				m.viewMode = viewModeDashboard
			} else {
				m.viewMode = viewModeLogs
				m.logScroll = 0
			}
			return m, nil
		case "t":
			if m.viewMode == viewModeTraffic {
				m.viewMode = viewModeDashboard
			} else {
				m.viewMode = viewModeTraffic
			}
			return m, nil
		}
		if m.viewMode == viewModeTraffic {
			return m, nil
		}
		if m.viewMode == viewModeLogs {
			switch msg.String() {
			case "up", "k":
				m.scrollLogs(1)
				return m, nil
			case "down", "j":
				m.scrollLogs(-1)
				return m, nil
			case "end":
				m.logScroll = 0
				return m, nil
			}
			return m, nil
		}
		switch msg.String() {
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
		case "up", "k":
			m.moveProfileSelection(-1)
			return m, nil
		case "down", "j":
			m.moveProfileSelection(1)
			return m, nil
		case "enter":
			return m, m.switchSelectedProfileCmd()
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
		m.traffic = msg.Traffic
		m.syncSelectedProfile()
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
		m.traffic = msg.Traffic
		m.syncSelectedProfile()
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
	if m.viewMode == viewModeLogs {
		return m.logView()
	}
	if m.viewMode == viewModeTraffic {
		return m.trafficView()
	}

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

	b.WriteString("\nProfiles\n")
	if len(m.profiles.Profiles) == 0 {
		b.WriteString("  -- no profiles\n")
	} else {
		active := m.activeProfile()
		for i, profile := range m.profiles.Profiles {
			marker := " "
			switch {
			case profile == active:
				marker = "●"
			case i == m.selectedProfile:
				marker = "›"
			}
			fmt.Fprintf(&b, "%s %s\n", marker, profile)
		}
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

	b.WriteString("\nTraffic\n")
	if len(m.traffic.Connections) == 0 {
		b.WriteString("  -- no traffic history\n")
	} else {
		for _, conn := range firstTrafficRows(m.traffic.Connections, 6) {
			fmt.Fprintf(&b, "  %-7s %-5s %-24s down %-9s up %-9s %s\n",
				conn.State,
				emptyDash(conn.Application),
				truncate(emptyDash(conn.Target), 24),
				formatBytes(conn.RxTotal),
				formatBytes(conn.TxTotal),
				formatDurationNs(conn.DurationNs),
			)
		}
	}

	b.WriteString("\nKeys: c connect  d disconnect  [ prev profile  ] next profile  t traffic  l logs  r refresh  q quit\n")
	return b.String()
}

func (m model) logView() string {
	var b strings.Builder

	apiState := "offline"
	if m.apiOnline {
		apiState = "online"
	}

	fmt.Fprintf(&b, "clambhook %s  API %s (%s)\n", version, m.apiAddr, apiState)
	b.WriteString("Logs\n")
	if m.errText != "" {
		fmt.Fprintf(&b, "Error: %s\n", m.errText)
	}

	if len(m.logs) == 0 {
		b.WriteString("  -- no logs yet\n")
	} else {
		for _, line := range m.visibleLogLines() {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}

	b.WriteString("\nKeys: l dashboard  up/k scroll  down/j scroll  end tail  q quit\n")
	return b.String()
}

func (m model) trafficView() string {
	var b strings.Builder

	apiState := "offline"
	if m.apiOnline {
		apiState = "online"
	}
	fmt.Fprintf(&b, "clambhook %s  API %s (%s)\n", version, m.apiAddr, apiState)
	b.WriteString("Traffic\n")
	if m.errText != "" {
		fmt.Fprintf(&b, "Error: %s\n", m.errText)
	}

	sum := m.traffic.Summary
	fmt.Fprintf(&b, "Active %d  Down %s  Up %s  Total down %s  Total up %s\n",
		sum.ActiveConnections,
		formatRate(sum.RxBps),
		formatRate(sum.TxBps),
		formatBytes(sum.RxTotal),
		formatBytes(sum.TxTotal),
	)
	if sum.PersistError != "" {
		fmt.Fprintf(&b, "History: %s\n", sum.PersistError)
	}

	if len(m.traffic.Connections) == 0 {
		b.WriteString("\n  -- no traffic history\n")
	} else {
		b.WriteString("\n  State   App       Target                    Listener          Down       Up         Duration  Path\n")
		for _, conn := range firstTrafficRows(m.traffic.Connections, m.trafficVisibleRows()) {
			fmt.Fprintf(&b, "  %-7s %-9s %-25s %-17s %-10s %-10s %-9s %s\n",
				truncate(conn.State, 7),
				truncate(emptyDash(conn.Application), 9),
				truncate(emptyDash(conn.Target), 25),
				truncate(conn.Listener.Protocol+" "+conn.Listener.Addr, 17),
				formatBytes(conn.RxTotal),
				formatBytes(conn.TxTotal),
				formatDurationNs(conn.DurationNs),
				truncate(trafficPath(conn), 22),
			)
		}
	}

	b.WriteString("\nKeys: t dashboard  l logs  r refresh  q quit\n")
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
		traffic, err := client.traffic()
		if err != nil {
			return dashboardLoadedMsg{Err: err}
		}
		return dashboardLoadedMsg{Status: status, Profiles: profiles, Servers: servers, Traffic: traffic}
	}
}

func (m model) loadStatusCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		status, err := client.status()
		if err != nil {
			return statusLoadedMsg{Err: err}
		}
		traffic, err := client.traffic()
		return statusLoadedMsg{Status: status, Traffic: traffic, Err: err}
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

func (m model) switchSelectedProfileCmd() tea.Cmd {
	if len(m.profiles.Profiles) == 0 {
		return nil
	}
	if m.selectedProfile < 0 || m.selectedProfile >= len(m.profiles.Profiles) {
		return nil
	}
	next := m.profiles.Profiles[m.selectedProfile]
	if next == m.activeProfile() {
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
	active := m.activeProfile()
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

func (m *model) syncSelectedProfile() {
	if len(m.profiles.Profiles) == 0 {
		m.selectedProfile = 0
		return
	}
	active := m.activeProfile()
	for i, name := range m.profiles.Profiles {
		if name == active {
			m.selectedProfile = i
			return
		}
	}
	if m.selectedProfile < 0 || m.selectedProfile >= len(m.profiles.Profiles) {
		m.selectedProfile = 0
	}
}

func (m *model) moveProfileSelection(delta int) {
	if len(m.profiles.Profiles) == 0 {
		m.selectedProfile = 0
		return
	}
	m.selectedProfile = (m.selectedProfile + delta + len(m.profiles.Profiles)) % len(m.profiles.Profiles)
}

func (m model) activeProfile() string {
	if m.profiles.Active != "" {
		return m.profiles.Active
	}
	return m.status.Profile
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
	if ev.Type == events.TypeLogLine {
		if line, ok := eventLogLine(ev.Data); ok {
			m.appendLogLine(line)
		}
		return
	}
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
	if connID, ok := eventString(ev.Data, "conn_id"); ok {
		m.applyTrafficBytes(connID, rxDelta/seconds, txDelta/seconds, rxDelta, txDelta)
	}
}

func (m *model) applyTrafficBytes(connID string, rxBps, txBps, rxDelta, txDelta float64) {
	for i := range m.traffic.Connections {
		if m.traffic.Connections[i].ConnID != connID {
			continue
		}
		oldRxBps := m.traffic.Connections[i].RxBps
		oldTxBps := m.traffic.Connections[i].TxBps
		m.traffic.Connections[i].RxBps = rxBps
		m.traffic.Connections[i].TxBps = txBps
		m.traffic.Connections[i].RxTotal += uint64(rxDelta)
		m.traffic.Connections[i].TxTotal += uint64(txDelta)
		m.traffic.Summary.RxBps += rxBps - oldRxBps
		m.traffic.Summary.TxBps += txBps - oldTxBps
		m.traffic.Summary.RxTotal += uint64(rxDelta)
		m.traffic.Summary.TxTotal += uint64(txDelta)
		return
	}
}

func (m *model) appendLogLine(line string) {
	wasTailing := m.logScroll == 0
	if !wasTailing {
		m.logScroll++
	}
	m.logs = append(m.logs, line)
	if len(m.logs) > maxLogLines {
		m.logs = m.logs[len(m.logs)-maxLogLines:]
	}
	if wasTailing {
		m.logScroll = 0
		return
	}
	m.clampLogScroll()
}

func (m *model) scrollLogs(delta int) {
	m.logScroll += delta
	m.clampLogScroll()
}

func (m *model) clampLogScroll() {
	if m.logScroll < 0 {
		m.logScroll = 0
	}
	if maxScroll := m.maxLogScroll(); m.logScroll > maxScroll {
		m.logScroll = maxScroll
	}
}

func (m model) maxLogScroll() int {
	visible := m.logVisibleRows()
	if len(m.logs) <= visible {
		return 0
	}
	return len(m.logs) - visible
}

func (m model) visibleLogLines() []string {
	if len(m.logs) == 0 {
		return nil
	}
	visible := m.logVisibleRows()
	end := len(m.logs) - m.logScroll
	if end < 0 {
		end = 0
	}
	if end > len(m.logs) {
		end = len(m.logs)
	}
	start := end - visible
	if start < 0 {
		start = 0
	}
	return m.logs[start:end]
}

func (m model) logVisibleRows() int {
	height := m.height
	if height <= 0 {
		height = defaultLogViewHeight
	}
	rows := height - 4
	if m.errText != "" {
		rows--
	}
	if rows < 1 {
		return 1
	}
	return rows
}

func eventLogLine(data any) (string, bool) {
	switch d := data.(type) {
	case map[string]any:
		line, ok := d["line"].(string)
		return line, ok
	case events.LogLineData:
		return d.Line, true
	case *events.LogLineData:
		if d == nil {
			return "", false
		}
		return d.Line, true
	}
	return "", false
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

func eventString(data any, key string) (string, bool) {
	switch d := data.(type) {
	case map[string]any:
		v, ok := d[key].(string)
		return v, ok
	case events.ConnectionBytesData:
		if key == "conn_id" {
			return d.ConnID, true
		}
	}
	return "", false
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

func formatBytes(n uint64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	if n < 1024*1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
}

func formatDurationNs(ns int64) string {
	if ns <= 0 {
		return "--"
	}
	d := time.Duration(ns)
	if d < time.Second {
		return d.Truncate(time.Millisecond).String()
	}
	if d < time.Minute {
		return d.Truncate(time.Second).String()
	}
	return d.Truncate(time.Minute).String()
}

func firstTrafficRows(rows []trafficConnectionPayload, max int) []trafficConnectionPayload {
	if max <= 0 || len(rows) <= max {
		return rows
	}
	return rows[:max]
}

func (m model) trafficVisibleRows() int {
	height := m.height
	if height <= 0 {
		height = defaultLogViewHeight
	}
	rows := height - 8
	if m.errText != "" {
		rows--
	}
	if rows < 1 {
		return 1
	}
	return rows
}

func trafficPath(conn trafficConnectionPayload) string {
	if len(conn.Hops) == 0 {
		return emptyDash(conn.ChainName)
	}
	parts := make([]string, 0, len(conn.Hops))
	for _, hop := range conn.Hops {
		name := hop.Name
		if name == "" {
			name = hop.Protocol
		}
		if hop.State != "" {
			name += ":" + hop.State
		}
		parts = append(parts, name)
	}
	return strings.Join(parts, " > ")
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
