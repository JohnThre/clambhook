//go:build unix

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/JohnThre/clambhook/internal/events"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/coder/websocket"
)

const (
	bandwidthSampleLimit = 60
	defaultGraphWidth    = 30
	defaultViewWidth     = 100
	minViewWidth         = 32
	maxLogLines          = 200
	defaultLogViewHeight = 24
	refreshInterval      = 2 * time.Second
	reconnectInterval    = 2 * time.Second
)

var (
	headerStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("24"))
	sectionStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
	tableHeaderStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("244"))
	subtleStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	errorStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	footerStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	activeLineStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	selectedLineStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("229"))
	runningBadgeStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("22")).Background(lipgloss.Color("42")).Padding(0, 1)
	stoppedBadgeStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("236")).Background(lipgloss.Color("250")).Padding(0, 1)
	onlineBadgeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	offlineBadgeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
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
	trafficFilter   string
	searchText      string
	searchEditing   bool
	selectedTraffic int
	pendingRule     *rulePayload
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
			if m.searchEditing {
				switch msg.String() {
				case "esc", "enter":
					m.searchEditing = false
				case "backspace", "ctrl+h":
					if len(m.searchText) > 0 {
						m.searchText = m.searchText[:len(m.searchText)-1]
					}
				default:
					if len(msg.Runes) > 0 {
						m.searchText += string(msg.Runes)
					}
				}
				m.clampTrafficSelection()
				return m, nil
			}
			if m.pendingRule != nil {
				switch msg.String() {
				case "esc":
					m.pendingRule = nil
					return m, nil
				case "y":
					rule := *m.pendingRule
					m.pendingRule = nil
					return m, m.actionCmd(func() error {
						return m.client.createRule(rule)
					})
				case "b":
					m.pendingRule.Action = "block"
					return m, nil
				case "d":
					m.pendingRule.Action = "direct"
					return m, nil
				case "p":
					if conn, ok := m.selectedConnection(); ok && conn.ChainName != "" {
						m.pendingRule.Action = "chain:" + conn.ChainName
					}
					return m, nil
				}
			}
			switch msg.String() {
			case "r":
				return m, m.loadDashboardCmd()
			case "/":
				m.searchEditing = true
				return m, nil
			case "esc":
				m.searchText = ""
				m.trafficFilter = ""
				m.clampTrafficSelection()
				return m, nil
			case "a":
				m.trafficFilter = ""
				m.clampTrafficSelection()
				return m, nil
			case "b":
				m.trafficFilter = "block"
				m.clampTrafficSelection()
				return m, nil
			case "d":
				m.trafficFilter = "direct"
				m.clampTrafficSelection()
				return m, nil
			case "p":
				m.trafficFilter = "proxy"
				m.clampTrafficSelection()
				return m, nil
			case "up", "k":
				m.moveTrafficSelection(-1)
				return m, nil
			case "down", "j":
				m.moveTrafficSelection(1)
				return m, nil
			case "n":
				rule, ok := m.ruleDraftFromSelected()
				if !ok {
					m.errText = "select a connection with a host before creating a rule"
					return m, nil
				}
				m.pendingRule = &rule
				return m, nil
			}
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
		m.clampTrafficSelection()
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
		m.clampTrafficSelection()
		return m, nil
	case actionDoneMsg:
		if msg.Err != nil {
			m.apiOnline = false
			m.errText = msg.Err.Error()
			return m, nil
		}
		return m, m.loadDashboardCmd()
	case eventMsg:
		needsRefresh := m.applyEvent(msg.Event)
		if needsRefresh {
			return m, tea.Batch(waitEventCmd(m.eventsCh, m.eventErrCh), m.loadStatusCmd())
		}
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

	width := m.contentWidth()
	sections := []string{
		m.renderHeader("Dashboard"),
		m.renderStatusSummary(width),
	}
	if m.errText != "" {
		sections = append(sections, m.renderError(width))
	}
	sections = append(sections,
		m.renderProfileListenerSections(width),
		m.renderServersSection(width),
		m.renderBandwidthSection(width),
		m.renderTrafficPreviewSection(width),
		m.renderFooter(
			"Keys: c connect  d disconnect  [ prev profile  ] next profile  enter switch  t traffic  l logs  r refresh  q quit",
			"Keys: c/d  [/] profile  enter  t traffic  l logs  r refresh  q quit",
		),
	)
	return joinSections(sections)
}

func (m model) logView() string {
	width := m.contentWidth()
	sections := []string{m.renderHeader("Logs")}
	if m.errText != "" {
		sections = append(sections, m.renderError(width))
	}

	lines := make([]string, 0, m.logVisibleRows()+1)
	if len(m.logs) == 0 {
		lines = append(lines, subtleStyle.Render("  -- no logs yet"))
	} else {
		if m.logScroll > 0 {
			lines = append(lines, subtleStyle.Render(fmt.Sprintf("  showing %d lines above tail", m.logScroll)))
		}
		for _, line := range m.visibleLogLines() {
			lines = append(lines, "  "+truncate(line, width-2))
		}
	}

	sections = append(sections,
		renderSection("Logs", lines),
		m.renderFooter(
			"Keys: l dashboard  up/k scroll  down/j scroll  end tail  q quit",
			"Keys: l dashboard  up/down  end tail  q quit",
		),
	)
	return joinSections(sections)
}

func (m model) trafficView() string {
	width := m.contentWidth()
	sections := []string{m.renderHeader("Monitor")}
	if m.errText != "" {
		sections = append(sections, m.renderError(width))
	}
	sections = append(sections,
		m.renderTrafficDetailSection(width),
		m.renderFooter(
			"Keys: a all  b block  d direct  p proxy  / search  up/down select  n new rule  r refresh  t dashboard  q quit",
			"Keys: a/b/d/p  / search  up/down  n rule  r refresh  t dash  q",
		),
	)
	return joinSections(sections)
}

func (m model) renderHeader(title string) string {
	width := m.contentWidth()
	left := fmt.Sprintf("clambhook %s · %s", version, title)
	right := "API " + m.apiAddr
	return headerStyle.Width(width).Render(fitLine(left, right, width))
}

func (m model) renderStatusSummary(width int) string {
	line := strings.Join([]string{
		m.runningBadge(),
		m.apiBadge(),
		"Profile " + truncate(emptyDash(m.activeProfile()), maxInt(8, width/3)),
		fmt.Sprintf("%d active connections", m.activeConnections()),
	}, "  ")
	if lipgloss.Width(line) <= width {
		return line
	}
	return strings.Join([]string{
		strings.Join([]string{m.runningBadge(), m.apiBadge()}, "  "),
		"Profile " + truncate(emptyDash(m.activeProfile()), width-8),
		fmt.Sprintf("%d active connections", m.activeConnections()),
	}, "\n")
}

func (m model) runningBadge() string {
	if m.status.Running {
		return runningBadgeStyle.Render("RUNNING")
	}
	return stoppedBadgeStyle.Render("STOPPED")
}

func (m model) apiBadge() string {
	if m.apiOnline {
		return onlineBadgeStyle.Render("API online")
	}
	return offlineBadgeStyle.Render("API offline")
}

func (m model) renderError(width int) string {
	return errorStyle.Render(truncate("Error: "+m.errText, width))
}

func (m model) renderProfileListenerSections(width int) string {
	if width >= 84 {
		profileWidth := clampInt(width/3, 24, 36)
		listenerWidth := width - profileWidth - 2
		profiles := lipgloss.NewStyle().Width(profileWidth).Render(renderSection("Profiles", m.profileLines(profileWidth)))
		listeners := lipgloss.NewStyle().Width(listenerWidth).Render(renderSection("Listeners", m.listenerLines(listenerWidth)))
		return lipgloss.JoinHorizontal(lipgloss.Top, profiles, "  ", listeners)
	}
	return renderSection("Profiles", m.profileLines(width)) + "\n\n" + renderSection("Listeners", m.listenerLines(width))
}

func (m model) profileLines(width int) []string {
	if len(m.profiles.Profiles) == 0 {
		return []string{subtleStyle.Render("  -- no profiles")}
	}
	active := m.activeProfile()
	lines := make([]string, 0, len(m.profiles.Profiles))
	for i, profile := range m.profiles.Profiles {
		marker := " "
		style := lipgloss.NewStyle()
		styled := false
		switch {
		case profile == active:
			marker = "●"
			style = activeLineStyle
			styled = true
		case i == m.selectedProfile:
			marker = "›"
			style = selectedLineStyle
			styled = true
		}
		line := fmt.Sprintf("%s %s", marker, truncate(profile, width-2))
		if styled {
			line = style.Render(line)
		}
		lines = append(lines, line)
	}
	return lines
}

func (m model) listenerLines(width int) []string {
	if len(m.status.Listeners) == 0 {
		return []string{subtleStyle.Render("  -- none active")}
	}
	lines := make([]string, 0, len(m.status.Listeners))
	for _, l := range m.status.Listeners {
		if width < 54 {
			line := fmt.Sprintf("  %s %s (%d)", l.Protocol, l.Addr, l.ActiveConns)
			lines = append(lines, truncate(line, width))
			continue
		}
		addrWidth := maxInt(12, width-24)
		line := fmt.Sprintf("  %s %s %s",
			cell(l.Protocol, 7),
			cell(l.Addr, addrWidth),
			cell(fmt.Sprintf("%d active", l.ActiveConns), 10),
		)
		lines = append(lines, truncate(line, width))
	}
	return lines
}

func (m model) renderServersSection(width int) string {
	return renderSection("Servers", m.serverLines(width))
}

func (m model) serverLines(width int) []string {
	if len(m.servers.Chains) == 0 {
		return []string{subtleStyle.Render("  -- no servers in active profile")}
	}
	lines := make([]string, 0)
	if width >= 92 {
		widths := serverColumnWidths(width)
		lines = append(lines, tableHeaderStyle.Render(tableRow([]string{"", "Name", "Protocol", "Address", "Location", "Chain"}, widths)))
		for _, ch := range m.servers.Chains {
			for _, server := range ch.Servers {
				lines = append(lines, tableRow([]string{
					countryFlag(server.Geo.CountryCode),
					server.Name,
					server.Protocol,
					server.Address,
					serverLocation(server),
					ch.Name,
				}, widths))
			}
		}
		return lines
	}

	for _, ch := range m.servers.Chains {
		for _, server := range ch.Servers {
			first := fmt.Sprintf("  %s %s · %s · %s",
				countryFlag(server.Geo.CountryCode),
				server.Name,
				server.Protocol,
				server.Address,
			)
			second := fmt.Sprintf("     %s · %s", serverLocation(server), ch.Name)
			lines = append(lines, truncate(first, width), subtleStyle.Render(truncate(second, width)))
		}
	}
	return lines
}

func (m model) renderBandwidthSection(width int) string {
	current := m.bandwidth.current()
	graphWidth := graphWidthFor(width)
	lines := []string{
		fmt.Sprintf("  Rx %-10s %s", formatRate(current.RxBps), m.bandwidth.graph(true, graphWidth)),
		fmt.Sprintf("  Tx %-10s %s", formatRate(current.TxBps), m.bandwidth.graph(false, graphWidth)),
	}
	return renderSection("Bandwidth", lines)
}

func (m model) renderTrafficPreviewSection(width int) string {
	lines := []string{m.trafficSummaryLine(width)}
	if len(m.traffic.Connections) == 0 {
		lines = append(lines, subtleStyle.Render("  -- no traffic history"))
		return renderSection("Traffic", lines)
	}

	limit := m.dashboardTrafficRows()
	lines = append(lines, m.trafficRows(width, limit, false)...)
	if len(m.traffic.Connections) > limit {
		lines = append(lines, subtleStyle.Render(fmt.Sprintf("  +%d more (press t)", len(m.traffic.Connections)-limit)))
	}
	return renderSection("Traffic", lines)
}

func (m model) renderTrafficDetailSection(width int) string {
	lines := []string{m.trafficSummaryLine(width)}
	if m.traffic.Summary.PersistError != "" {
		lines = append(lines, errorStyle.Render(truncate("  History: "+m.traffic.Summary.PersistError, width)))
	}
	lines = append(lines, m.monitorFilterLine(width))
	lines = append(lines, m.ruleHitLines(width)...)
	if m.pendingRule != nil {
		lines = append(lines, m.pendingRuleLine(width))
	}
	rows := m.filteredTrafficConnections()
	if len(rows) == 0 {
		lines = append(lines, "", subtleStyle.Render("  -- no traffic history"))
	} else {
		limit := m.trafficVisibleRows()
		lines = append(lines, "", tableHeaderStyle.Render(trafficHeaderRow(width)))
		lines = append(lines, m.trafficRowsFor(rows, width, limit, true)...)
		if len(rows) > limit {
			lines = append(lines, subtleStyle.Render(fmt.Sprintf("  +%d more rows hidden by terminal height", len(rows)-limit)))
		}
		lines = append(lines, "")
		lines = append(lines, m.selectedConnectionDetailLines(width)...)
	}
	return renderSection("Monitor", lines)
}

func (m model) trafficSummaryLine(width int) string {
	sum := m.traffic.Summary
	return truncate(fmt.Sprintf("  Active %d  Down %s  Up %s  Total down %s  Total up %s",
		sum.ActiveConnections,
		formatRate(sum.RxBps),
		formatRate(sum.TxBps),
		formatBytes(sum.RxTotal),
		formatBytes(sum.TxTotal),
	), width)
}

func (m model) monitorFilterLine(width int) string {
	counts := m.actionCounts()
	filter := m.trafficFilter
	if filter == "" {
		filter = "all"
	}
	search := m.searchText
	if search == "" {
		search = "--"
	}
	prompt := "search"
	if m.searchEditing {
		prompt = "typing"
	}
	return truncate(fmt.Sprintf("  [%s] all %d  proxy %d  direct %d  block %d  %s %q",
		filter,
		len(m.traffic.Connections),
		counts["proxy"],
		counts["direct"],
		counts["block"],
		prompt,
		search,
	), width)
}

func (m model) ruleHitLines(width int) []string {
	hits := m.ruleHits()
	if len(hits) == 0 {
		return nil
	}
	limit := 4
	if len(hits) < limit {
		limit = len(hits)
	}
	parts := make([]string, 0, limit)
	for _, hit := range hits[:limit] {
		parts = append(parts, fmt.Sprintf("%s/%s %d", emptyDash(hit.Name), hit.Action, hit.Count))
	}
	return []string{truncate("  Rule hits  "+strings.Join(parts, "  "), width)}
}

func (m model) pendingRuleLine(width int) string {
	rule := m.pendingRule
	if rule == nil {
		return ""
	}
	match := strings.Join(rule.Domains, ",")
	if match == "" {
		match = strings.Join(rule.CIDRs, ",")
	}
	return selectedLineStyle.Render(truncate(fmt.Sprintf("  New rule: %s  %s  %s  (y save, b/d/p action, esc cancel)", rule.Name, rule.Action, match), width))
}

func (m model) trafficRows(width, limit int, full bool) []string {
	return m.trafficRowsFor(m.traffic.Connections, width, limit, full)
}

func (m model) trafficRowsFor(connections []trafficConnectionPayload, width, limit int, full bool) []string {
	rows := firstTrafficRows(connections, limit)
	out := make([]string, 0, len(rows))
	wide := width >= 92
	widths := trafficColumnWidths(width)
	for i, conn := range rows {
		prefix := " "
		if full && i == m.selectedTraffic {
			prefix = "›"
		}
		if wide && full {
			out = append(out, prefix+tableRowNoIndent([]string{
				actionChip(conn),
				emptyDash(conn.Application),
				emptyDash(conn.Target),
				conn.Listener.Protocol + " " + conn.Listener.Addr,
				formatBytes(conn.RxTotal),
				formatBytes(conn.TxTotal),
				formatDurationNs(conn.DurationNs),
				trafficPath(conn),
			}, widths))
			continue
		}
		if wide {
			out = append(out, truncate(fmt.Sprintf("%s %-7s %-7s %-28s down %-10s up %-10s %s",
				prefix,
				truncate(actionChip(conn), 7),
				truncate(emptyDash(conn.Application), 7),
				truncate(emptyDash(conn.Target), 28),
				formatBytes(conn.RxTotal),
				formatBytes(conn.TxTotal),
				formatDurationNs(conn.DurationNs),
			), width))
			continue
		}
		out = append(out, truncate(fmt.Sprintf("%s %s %s  %s down / %s up  %s",
			prefix,
			actionChip(conn),
			emptyDash(conn.Target),
			formatBytes(conn.RxTotal),
			formatBytes(conn.TxTotal),
			formatDurationNs(conn.DurationNs),
		), width))
	}
	return out
}

func (m model) filteredTrafficConnections() []trafficConnectionPayload {
	query := strings.ToLower(strings.TrimSpace(m.searchText))
	out := make([]trafficConnectionPayload, 0, len(m.traffic.Connections))
	for _, conn := range m.traffic.Connections {
		if m.trafficFilter != "" && actionFamily(conn) != m.trafficFilter {
			continue
		}
		if query != "" && !connectionMatchesSearch(conn, query) {
			continue
		}
		out = append(out, conn)
	}
	return out
}

func (m model) selectedConnection() (trafficConnectionPayload, bool) {
	rows := m.filteredTrafficConnections()
	if len(rows) == 0 {
		return trafficConnectionPayload{}, false
	}
	idx := m.selectedTraffic
	if idx < 0 {
		idx = 0
	}
	if idx >= len(rows) {
		idx = len(rows) - 1
	}
	return rows[idx], true
}

func (m *model) moveTrafficSelection(delta int) {
	rows := m.filteredTrafficConnections()
	if len(rows) == 0 {
		m.selectedTraffic = 0
		return
	}
	m.selectedTraffic = (m.selectedTraffic + delta + len(rows)) % len(rows)
}

func (m *model) clampTrafficSelection() {
	rows := m.filteredTrafficConnections()
	if len(rows) == 0 {
		m.selectedTraffic = 0
		return
	}
	if m.selectedTraffic < 0 {
		m.selectedTraffic = 0
	}
	if m.selectedTraffic >= len(rows) {
		m.selectedTraffic = len(rows) - 1
	}
}

func (m model) selectedConnectionDetailLines(width int) []string {
	conn, ok := m.selectedConnection()
	if !ok {
		return nil
	}
	host := connectionHost(conn)
	lines := []string{
		tableHeaderStyle.Render(truncate("  Host Detail", width)),
		truncate(fmt.Sprintf("  Host %s  Action %s  Rule %s  Chain %s", emptyDash(host), actionChip(conn), emptyDash(conn.RuleName), emptyDash(conn.ChainName)), width),
		truncate(fmt.Sprintf("  Target %s  Network %s  App %s  Listener %s %s", emptyDash(conn.Target), emptyDash(conn.Network), emptyDash(conn.Application), conn.Listener.Protocol, conn.Listener.Addr), width),
		truncate(fmt.Sprintf("  Bytes %s down / %s up  Duration %s  Decision %s", formatBytes(conn.RxTotal), formatBytes(conn.TxTotal), formatDurationNs(conn.DurationNs), formatDurationNs(conn.DecisionNs)), width),
	}
	if conn.Visibility != nil {
		lines = append(lines, truncate(fmt.Sprintf("  Visibility %s %s %s %s", conn.Visibility.Kind, conn.Visibility.Method, conn.Visibility.Host, conn.Visibility.Path), width))
	}
	if len(conn.Timeline) > 0 {
		last := conn.Timeline[len(conn.Timeline)-1]
		lines = append(lines, truncate(fmt.Sprintf("  Last %s %s", emptyDash(last.Title), last.Detail), width))
	}
	return lines
}

type ruleHit struct {
	Name   string
	Action string
	Count  int
}

func (m model) ruleHits() []ruleHit {
	index := map[string]*ruleHit{}
	for _, conn := range m.traffic.Connections {
		if conn.RuleName == "" && conn.RuleAction == "" {
			continue
		}
		name := conn.RuleName
		if name == "" {
			name = "default"
		}
		action := actionFamily(conn)
		key := name + "\x00" + action
		hit := index[key]
		if hit == nil {
			hit = &ruleHit{Name: name, Action: action}
			index[key] = hit
		}
		hit.Count++
	}
	out := make([]ruleHit, 0, len(index))
	for _, hit := range index {
		out = append(out, *hit)
	}
	sortRuleHits(out)
	return out
}

func (m model) actionCounts() map[string]int {
	counts := map[string]int{"proxy": 0, "direct": 0, "block": 0}
	for _, conn := range m.traffic.Connections {
		counts[actionFamily(conn)]++
	}
	return counts
}

func connectionMatchesSearch(conn trafficConnectionPayload, query string) bool {
	fields := []string{
		conn.Target,
		conn.TargetHost,
		conn.TargetPort,
		conn.RuleName,
		conn.RuleAction,
		conn.ChainName,
		conn.Application,
		conn.Network,
		conn.Listener.Protocol,
		conn.Listener.Addr,
		conn.ClientAddr,
	}
	if conn.Visibility != nil {
		fields = append(fields, conn.Visibility.Kind, conn.Visibility.Method, conn.Visibility.Host, conn.Visibility.Path, conn.Visibility.QueryType)
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}

func actionChip(conn trafficConnectionPayload) string {
	switch actionFamily(conn) {
	case "direct":
		return "DIRECT"
	case "block":
		return "BLOCK"
	default:
		return "PROXY"
	}
}

func actionFamily(conn trafficConnectionPayload) string {
	action := strings.ToLower(strings.TrimSpace(conn.RuleAction))
	switch action {
	case "direct":
		return "direct"
	case "block", "reject":
		return "block"
	default:
		return "proxy"
	}
}

func sortRuleHits(hits []ruleHit) {
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Count == hits[j].Count {
			if hits[i].Name == hits[j].Name {
				return hits[i].Action < hits[j].Action
			}
			return hits[i].Name < hits[j].Name
		}
		return hits[i].Count > hits[j].Count
	})
}

func (m model) ruleDraftFromSelected() (rulePayload, bool) {
	conn, ok := m.selectedConnection()
	if !ok {
		return rulePayload{}, false
	}
	host := connectionHost(conn)
	if host == "" {
		return rulePayload{}, false
	}
	rule := rulePayload{
		Name:   ruleNameForHost(host, actionFamily(conn)),
		Action: ruleActionForConnection(conn),
	}
	if addr, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		if addr.Is6() {
			rule.CIDRs = []string{addr.String() + "/128"}
		} else {
			rule.CIDRs = []string{addr.String() + "/32"}
		}
	} else {
		rule.Domains = []string{host}
	}
	return rule, true
}

func connectionHost(conn trafficConnectionPayload) string {
	if conn.TargetHost != "" {
		return normalizeRuleHost(conn.TargetHost)
	}
	if conn.Visibility != nil && conn.Visibility.Host != "" {
		return normalizeRuleHost(conn.Visibility.Host)
	}
	host, _ := splitHostPortLoose(conn.Target)
	return normalizeRuleHost(host)
}

func ruleActionForConnection(conn trafficConnectionPayload) string {
	action := strings.ToLower(strings.TrimSpace(conn.RuleAction))
	switch action {
	case "direct", "block", "reject":
		return action
	case "chain":
		if conn.ChainName != "" {
			return "chain:" + conn.ChainName
		}
	}
	if conn.ChainName != "" {
		return "chain:" + conn.ChainName
	}
	return "direct"
}

func ruleNameForHost(host, action string) string {
	name := strings.ToLower(strings.TrimSpace(host))
	name = strings.Trim(name, "[]")
	replacer := strings.NewReplacer(".", "-", ":", "-", "_", "-", " ", "-")
	name = replacer.Replace(name)
	name = strings.Trim(name, "-")
	if name == "" {
		name = "connection"
	}
	return action + "-" + name
}

func normalizeRuleHost(host string) string {
	host = strings.TrimSpace(host)
	host = strings.Trim(host, "[]")
	host = strings.TrimSuffix(host, ".")
	return strings.ToLower(host)
}

func splitHostPortLoose(target string) (string, string) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", ""
	}
	if i := strings.LastIndex(target, ":"); i > 0 && i < len(target)-1 {
		return target[:i], target[i+1:]
	}
	return target, ""
}

func (m model) renderFooter(full, compact string) string {
	width := m.contentWidth()
	text := full
	if lipgloss.Width(text) > width {
		text = compact
	}
	return footerStyle.Render(truncate(text, width))
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

func (m *model) applyEvent(ev events.Event) bool {
	if ev.Type == events.TypeLogLine {
		if line, ok := eventLogLine(ev.Data); ok {
			m.appendLogLine(line)
		}
		return false
	}
	if ev.Type != events.TypeConnectionBytes {
		return strings.HasPrefix(ev.Type, "connection.") || strings.HasPrefix(ev.Type, "rule.") || strings.HasPrefix(ev.Type, "hop.")
	}
	rxDelta, okRx := eventNumber(ev.Data, "rx_delta")
	txDelta, okTx := eventNumber(ev.Data, "tx_delta")
	intervalNs, okInterval := eventNumber(ev.Data, "interval_ns")
	if !okRx || !okTx || !okInterval || intervalNs <= 0 {
		return false
	}
	seconds := intervalNs / float64(time.Second)
	m.bandwidth.add(bandwidthSample{
		RxBps: rxDelta / seconds,
		TxBps: txDelta / seconds,
	})
	if connID, ok := eventString(ev.Data, "conn_id"); ok {
		m.applyTrafficBytes(connID, rxDelta/seconds, txDelta/seconds, rxDelta, txDelta)
	}
	return false
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
	rows := height - 5
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

func (s bandwidthSeries) graph(rx bool, width int) string {
	if width <= 0 {
		width = defaultGraphWidth
	}
	if len(s.Samples) == 0 {
		return strings.Repeat(" ", width)
	}
	start := 0
	if len(s.Samples) > width {
		start = len(s.Samples) - width
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
		return padLeft(strings.Repeat("▁", len(values)), width)
	}
	const bars = "▁▂▃▄▅▆▇█"
	barRunes := []rune(bars)
	var b strings.Builder
	for _, v := range values {
		idx := int(math.Ceil((v/maxValue)*float64(len(barRunes))) - 1)
		if idx < 0 {
			idx = 0
		}
		if idx >= len(barRunes) {
			idx = len(barRunes) - 1
		}
		b.WriteRune(barRunes[idx])
	}
	return padLeft(b.String(), width)
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
	rows := height - 10
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
	if max <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	var b strings.Builder
	for _, r := range s {
		next := b.String() + string(r)
		if lipgloss.Width(next) > max-1 {
			break
		}
		b.WriteRune(r)
	}
	return b.String() + "…"
}

func (m model) contentWidth() int {
	if m.width <= 0 {
		return defaultViewWidth
	}
	if m.width < minViewWidth {
		return minViewWidth
	}
	return m.width
}

func (m model) activeConnections() int64 {
	var total int64
	for _, listener := range m.status.Listeners {
		total += listener.ActiveConns
	}
	if total == 0 && m.traffic.Summary.ActiveConnections > 0 {
		return int64(m.traffic.Summary.ActiveConnections)
	}
	return total
}

func (m model) dashboardTrafficRows() int {
	limit := 6
	if m.height <= 0 {
		return limit
	}
	rows := m.height - 24
	if rows < 2 {
		rows = 2
	}
	if rows > limit {
		return limit
	}
	return rows
}

func renderSection(title string, lines []string) string {
	if len(lines) == 0 {
		lines = []string{subtleStyle.Render("  --")}
	}
	return sectionStyle.Render(title) + "\n" + strings.Join(lines, "\n")
}

func joinSections(sections []string) string {
	out := make([]string, 0, len(sections))
	for _, section := range sections {
		if strings.TrimSpace(section) != "" {
			out = append(out, section)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n\n") + "\n"
}

func fitLine(left, right string, width int) string {
	if width <= 0 {
		return left
	}
	right = truncate(right, maxInt(0, width/2))
	left = truncate(left, width)
	spaces := width - lipgloss.Width(left) - lipgloss.Width(right)
	if spaces < 1 {
		return truncate(left+" "+right, width)
	}
	return left + strings.Repeat(" ", spaces) + right
}

func graphWidthFor(width int) int {
	graphWidth := width - 18
	if graphWidth <= 0 {
		return 8
	}
	return clampInt(graphWidth, 8, 60)
}

func serverColumnWidths(width int) []int {
	available := width - 2 - 5
	flagWidth := 4
	protocolWidth := 11
	addressWidth := 22
	locationWidth := 18
	chainWidth := 14
	nameWidth := available - flagWidth - protocolWidth - addressWidth - locationWidth - chainWidth
	if nameWidth < 12 {
		nameWidth = 12
	}
	return []int{flagWidth, nameWidth, protocolWidth, addressWidth, locationWidth, chainWidth}
}

func trafficHeaderRow(width int) string {
	if width < 92 {
		return truncate("  Action Target  Down / Up  Duration", width)
	}
	return tableRow([]string{"Action", "App", "Target", "Listener", "Down", "Up", "Duration", "Path"}, trafficColumnWidths(width))
}

func trafficColumnWidths(width int) []int {
	available := width - 2 - 7
	stateWidth := 7
	appWidth := 8
	downWidth := 10
	upWidth := 10
	durationWidth := 8
	remaining := available - stateWidth - appWidth - downWidth - upWidth - durationWidth
	if remaining < 36 {
		remaining = 36
	}
	targetWidth := remaining * 40 / 100
	listenerWidth := remaining * 30 / 100
	pathWidth := remaining - targetWidth - listenerWidth
	return []int{stateWidth, appWidth, targetWidth, listenerWidth, downWidth, upWidth, durationWidth, pathWidth}
}

func tableRow(cells []string, widths []int) string {
	return "  " + tableRowNoIndent(cells, widths)
}

func tableRowNoIndent(cells []string, widths []int) string {
	parts := make([]string, 0, len(widths))
	for i, width := range widths {
		cellValue := ""
		if i < len(cells) {
			cellValue = cells[i]
		}
		parts = append(parts, cell(cellValue, width))
	}
	return strings.Join(parts, " ")
}

func cell(s string, width int) string {
	return padRight(truncate(s, width), width)
}

func padRight(s string, width int) string {
	if width <= 0 {
		return ""
	}
	padding := width - lipgloss.Width(s)
	if padding <= 0 {
		return s
	}
	return s + strings.Repeat(" ", padding)
}

func padLeft(s string, width int) string {
	if width <= 0 {
		return ""
	}
	padding := width - lipgloss.Width(s)
	if padding <= 0 {
		return s
	}
	return strings.Repeat(" ", padding) + s
}

func clampInt(n, min, max int) int {
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
