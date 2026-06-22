//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/netip"
	"os"
	"sort"
	"strconv"
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
	viewModeNow viewMode = iota
	viewModeActivity
	viewModeLibrary
	viewModeSettings
	viewModeDeveloper
	viewModeNetwork
)

type filterToken struct {
	Key   string // "app", "domain", "country", "action", "port", "ip", or "" for plain text
	Value string
}

// tempRuleTTLOptions defines TTL choices for the pending rule dialog.
// Seconds=0 means a persistent (non-expiring) rule.
var tempRuleTTLOptions = []struct {
	Seconds int64
	Label   string
}{{0, "persist"}, {900, "15m"}, {3600, "1h"}, {28800, "8h"}, {86400, "24h"}}

type model struct {
	apiAddr string
	client  apiClient

	status        statusPayload
	profiles      profilesPayload
	servers       serversPayload
	policies      policyGroupsPayload
	subscriptions ruleSubscriptionsPayload
	traffic       trafficSnapshotPayload
	dev           developerStatusPayload
	devRows       []developerEntryPayload

	selectedProfile      int
	viewMode             viewMode
	trafficFilter        string
	searchText           string
	searchEditing        bool
	ruleTestInput        string
	ruleTestEditing      bool
	ruleTestResult       *ruleTestResponse
	ruleTestErr          string
	selectedTraffic      int
	selectedSuggestion   int
	selectedCleanup      int
	suggestionFocus      bool
	cleanupFocus         bool
	selectedDeveloper    int
	selectedDeveloperTab int
	selectedPolicyGroup  int
	selectedPolicyMember int
	policyFocus          bool
	pendingRule          *pendingRule
	pendingCleanup       *cleanupSuggestionPayload
	pendingRuleTTLIdx    int // index into tempRuleTTLOptions; 0 = 15m
	width                int
	height               int

	// Network view state.
	netTreeAppIdx     int
	netTreeDomainIdx  int
	netTreeCountryIdx int
	netTreeDepth      int // 0=app, 1=domain, 2=country
	netTreeExpanded   map[int]bool
	netDetailConnIdx  int

	// Token-based filter (shared between Activity and Network views).
	filterTokens  []filterToken
	filterEditing bool
	filterInput   string

	configImportInput   string
	configImportEditing bool
	configExportInput   string
	configExportEditing bool
	configActionMsg     string

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

type pendingRule struct {
	rulePayload
	ConnID  string
	Profile string
	Scope   string
}

type dashboardLoadedMsg struct {
	Status        statusPayload
	Profiles      profilesPayload
	Servers       serversPayload
	Policies      policyGroupsPayload
	Subscriptions ruleSubscriptionsPayload
	Traffic       trafficSnapshotPayload
	Developer     developerStatusPayload
	DevRows       []developerEntryPayload
	Err           error
}

type statusLoadedMsg struct {
	Status   statusPayload
	Policies policyGroupsPayload
	Traffic  trafficSnapshotPayload
	Err      error
}

type developerLoadedMsg struct {
	Status  developerStatusPayload
	Entries []developerEntryPayload
	Err     error
}

type developerExportedMsg struct {
	Path string
	Err  error
}

type actionDoneMsg struct {
	Err error
}

type ruleTestDoneMsg struct {
	Result ruleTestResponse
	Err    error
}

type policyGroupsDoneMsg struct {
	Policies policyGroupsPayload
	Err      error
}

type subscriptionsLoadedMsg struct {
	Subscriptions ruleSubscriptionsPayload
	Err           error
}

type configExportDoneMsg struct {
	Path string
	Err  error
}

type configImportDoneMsg struct {
	Result configImportResponse
	Err    error
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
		// Handle Settings import/export editing modes before global key dispatch.
		if m.viewMode == viewModeSettings {
			if m.configExportEditing {
				switch msg.String() {
				case "esc":
					m.configExportEditing = false
				case "enter":
					path := strings.TrimSpace(m.configExportInput)
					if path == "" {
						m.configActionMsg = "enter a file path"
					} else {
						m.configExportEditing = false
						return m, m.exportConfigCmd(path)
					}
				case "backspace", "ctrl+h":
					if len(m.configExportInput) > 0 {
						m.configExportInput = m.configExportInput[:len(m.configExportInput)-1]
					}
				default:
					if len(msg.Runes) > 0 {
						m.configExportInput += string(msg.Runes)
					}
				}
				return m, nil
			}
			if m.configImportEditing {
				switch msg.String() {
				case "esc":
					m.configImportEditing = false
				case "enter":
					path := strings.TrimSpace(m.configImportInput)
					if path == "" {
						m.configActionMsg = "enter a file path"
					} else {
						m.configImportEditing = false
						return m, m.importConfigCmd(path)
					}
				case "backspace", "ctrl+h":
					if len(m.configImportInput) > 0 {
						m.configImportInput = m.configImportInput[:len(m.configImportInput)-1]
					}
				default:
					if len(msg.Runes) > 0 {
						m.configImportInput += string(msg.Runes)
					}
				}
				return m, nil
			}
		}
		// Intercept "t" for latency test when in Library+policyFocus mode.
		if msg.String() == "t" && m.viewMode == viewModeLibrary && m.policyFocus {
			group, ok := m.selectedPolicyGroupPayload()
			if ok {
				return m, m.testPolicyGroupCmd(group.Name)
			}
			return m, nil
		}
		switch msg.String() {
		case "q", "ctrl+c":
			if m.cancelEvents != nil {
				m.cancelEvents()
			}
			return m, tea.Quit
		case "1":
			m.viewMode = viewModeNow
			return m, nil
		case "2", "t":
			m.viewMode = viewModeActivity
			return m, nil
		case "3", "l":
			m.viewMode = viewModeLibrary
			return m, nil
		case "4", "s":
			m.viewMode = viewModeSettings
			return m, nil
		case "5", "v":
			m.viewMode = viewModeDeveloper
			return m, m.loadDeveloperCmd()
		case "6":
			m.viewMode = viewModeNetwork
			return m, nil
		}
		if m.viewMode == viewModeNetwork {
			return m.updateNetworkView(msg)
		}
		if m.viewMode == viewModeDeveloper {
			switch msg.String() {
			case "r":
				return m, m.loadDeveloperCmd()
			case "up", "k":
				m.moveDeveloperSelection(-1)
				return m, nil
			case "down", "j":
				m.moveDeveloperSelection(1)
				return m, nil
			case "tab", "]":
				m.selectedDeveloperTab = (m.selectedDeveloperTab + 1) % len(developerDetailTabs)
				return m, nil
			case "[":
				m.selectedDeveloperTab = (m.selectedDeveloperTab + len(developerDetailTabs) - 1) % len(developerDetailTabs)
				return m, nil
			case "e":
				return m, m.exportDeveloperHARCmd()
			case "c":
				return m, m.actionCmd(m.client.clearDeveloperEntries)
			}
			return m, nil
		}
		if m.viewMode == viewModeActivity {
			if m.ruleTestEditing {
				switch msg.String() {
				case "esc":
					m.ruleTestEditing = false
					return m, nil
				case "enter":
					network, target, err := parseRuleTestInput(m.ruleTestInput)
					if err != nil {
						m.ruleTestErr = err.Error()
						return m, nil
					}
					m.ruleTestEditing = false
					m.ruleTestErr = ""
					return m, m.ruleTestCmd(network, target)
				case "backspace", "ctrl+h":
					if len(m.ruleTestInput) > 0 {
						m.ruleTestInput = m.ruleTestInput[:len(m.ruleTestInput)-1]
					}
				default:
					if len(msg.Runes) > 0 {
						m.ruleTestInput += string(msg.Runes)
					}
				}
				return m, nil
			}
			if m.filterEditing {
				switch msg.String() {
				case "esc":
					m.filterEditing = false
					m.filterInput = ""
				case "enter":
					m.filterTokens = parseFilterTokens(m.filterInput)
					m.filterEditing = false
					m.filterInput = ""
					m.clampTrafficSelection()
				case "backspace", "ctrl+h":
					if len(m.filterInput) > 0 {
						m.filterInput = m.filterInput[:len(m.filterInput)-1]
					} else if len(m.filterTokens) > 0 {
						m.filterTokens = m.filterTokens[:len(m.filterTokens)-1]
					}
				default:
					if len(msg.Runes) > 0 {
						m.filterInput += string(msg.Runes)
					}
				}
				m.clampTrafficSelection()
				return m, nil
			}
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
					return m, m.savePendingRuleCmd(rule)
				case "a":
					if m.pendingRule.ConnID == "" {
						m.errText = "allow is only available for connection-derived rules"
						return m, nil
					}
					m.pendingRule.Action = "allow"
					return m, nil
				case "b":
					m.pendingRule.Action = "block"
					return m, nil
				case "d":
					m.pendingRule.Action = "direct"
					return m, nil
				case "p":
					if conn, ok := m.selectedConnection(); ok {
						if conn.GroupName != "" {
							m.pendingRule.Action = "group:" + conn.GroupName
						} else if conn.ChainName != "" {
							m.pendingRule.Action = "chain:" + conn.ChainName
						}
					}
					return m, nil
				case "left":
					if m.pendingRuleTTLIdx > 0 {
						m.pendingRuleTTLIdx--
					}
					return m, nil
				case "right":
					if m.pendingRuleTTLIdx < len(tempRuleTTLOptions)-1 {
						m.pendingRuleTTLIdx++
					}
					return m, nil
				}
			}
			if m.pendingCleanup != nil {
				switch msg.String() {
				case "esc":
					m.pendingCleanup = nil
					return m, nil
				case "y":
					cleanup := *m.pendingCleanup
					m.pendingCleanup = nil
					return m, m.cleanupRuleCmd(cleanup)
				}
			}
			switch msg.String() {
			case "r":
				return m, m.loadDashboardCmd()
			case "/":
				m.filterEditing = true
				m.filterInput = ""
				return m, nil
			case "esc":
				m.searchText = ""
				m.trafficFilter = ""
				m.filterTokens = nil
				m.filterEditing = false
				m.filterInput = ""
				m.suggestionFocus = false
				m.cleanupFocus = false
				m.clampTrafficSelection()
				m.clampSuggestionSelection()
				m.clampCleanupSelection()
				return m, nil
			case "a":
				m.trafficFilter = ""
				m.suggestionFocus = false
				m.cleanupFocus = false
				m.clampTrafficSelection()
				return m, nil
			case "b":
				m.trafficFilter = "block"
				m.suggestionFocus = false
				m.cleanupFocus = false
				m.clampTrafficSelection()
				return m, nil
			case "d":
				m.trafficFilter = "direct"
				m.suggestionFocus = false
				m.cleanupFocus = false
				m.clampTrafficSelection()
				return m, nil
			case "p":
				m.trafficFilter = "proxy"
				m.suggestionFocus = false
				m.cleanupFocus = false
				m.clampTrafficSelection()
				return m, nil
			case "tab":
				m.advanceActivityFocus()
				return m, nil
			case "up", "k":
				if m.cleanupFocus {
					m.moveCleanupSelection(-1)
				} else if m.suggestionFocus {
					m.moveSuggestionSelection(-1)
				} else {
					m.moveTrafficSelection(-1)
				}
				return m, nil
			case "down", "j":
				if m.cleanupFocus {
					m.moveCleanupSelection(1)
				} else if m.suggestionFocus {
					m.moveSuggestionSelection(1)
				} else {
					m.moveTrafficSelection(1)
				}
				return m, nil
			case "c":
				cleanup, ok := m.selectedCleanupSuggestion()
				if !ok {
					m.errText = "select a cleanup suggestion before applying cleanup"
					return m, nil
				}
				m.pendingCleanup = &cleanup
				return m, nil
			case "enter":
				if m.cleanupFocus {
					cleanup, ok := m.selectedCleanupSuggestion()
					if !ok {
						m.errText = "select a cleanup suggestion before applying cleanup"
						return m, nil
					}
					m.pendingCleanup = &cleanup
					return m, nil
				}
				rule, ok := m.ruleDraftFromSelected()
				if !ok {
					m.errText = "select a connection with a host before creating a rule"
					return m, nil
				}
				m.pendingRule = &rule
				return m, nil
			case "n":
				if m.cleanupFocus {
					m.errText = "use c to apply the selected cleanup suggestion"
					return m, nil
				}
				rule, ok := m.ruleDraftFromSelected()
				if !ok {
					m.errText = "select a connection with a host before creating a rule"
					return m, nil
				}
				m.pendingRule = &rule
				return m, nil
			case "x":
				if m.ruleTestInput == "" {
					m.ruleTestInput = "tcp example.com:443"
				}
				m.ruleTestEditing = true
				m.ruleTestErr = ""
				return m, nil
			}
			return m, nil
		}
		switch msg.String() {
		case "c":
			if m.viewMode != viewModeNow {
				return m, nil
			}
			if m.status.Running {
				return m, nil
			}
			return m, m.actionCmd(m.client.connect)
		case "d":
			if m.viewMode != viewModeNow {
				return m, nil
			}
			if !m.status.Running {
				return m, nil
			}
			return m, m.actionCmd(m.client.disconnect)
		case "[":
			if m.viewMode != viewModeLibrary || m.policyFocus {
				return m, nil
			}
			return m, m.switchProfileCmd(-1)
		case "]":
			if m.viewMode != viewModeLibrary || m.policyFocus {
				return m, nil
			}
			return m, m.switchProfileCmd(1)
		case "tab":
			if m.viewMode != viewModeLibrary {
				return m, nil
			}
			m.policyFocus = !m.policyFocus
			m.clampPolicySelection()
			return m, nil
		case "up", "k":
			if m.viewMode != viewModeLibrary {
				return m, nil
			}
			if m.policyFocus {
				m.movePolicyMemberSelection(-1)
			} else {
				m.moveProfileSelection(-1)
			}
			return m, nil
		case "down", "j":
			if m.viewMode != viewModeLibrary {
				return m, nil
			}
			if m.policyFocus {
				m.movePolicyMemberSelection(1)
			} else {
				m.moveProfileSelection(1)
			}
			return m, nil
		case "f":
			if m.viewMode == viewModeLibrary {
				return m, m.refreshSubscriptionsCmd()
			}
		case "e":
			if m.viewMode == viewModeSettings {
				if m.configExportInput == "" {
					m.configExportInput = "clambhook-export.toml"
				}
				m.configExportEditing = true
				m.configActionMsg = ""
				return m, nil
			}
		case "i":
			if m.viewMode == viewModeSettings {
				m.configImportEditing = true
				m.configActionMsg = ""
				return m, nil
			}
		case "enter":
			if m.viewMode != viewModeLibrary {
				return m, nil
			}
			if m.policyFocus {
				return m, m.applySelectedPolicyCmd()
			}
			return m, m.switchSelectedProfileCmd()
		case "r":
			return m, m.loadDashboardCmd()
		case "n":
			m.viewMode = viewModeNetwork
			return m, nil
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
		m.policies = msg.Policies
		m.subscriptions = msg.Subscriptions
		m.traffic = msg.Traffic
		m.dev = msg.Developer
		m.devRows = msg.DevRows
		m.syncSelectedProfile()
		m.clampPolicySelection()
		m.clampTrafficSelection()
		m.clampCleanupSelection()
		m.clampSuggestionSelection()
		m.clampDeveloperSelection()
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
		m.policies = msg.Policies
		m.traffic = msg.Traffic
		m.syncSelectedProfile()
		m.clampPolicySelection()
		m.clampTrafficSelection()
		m.clampCleanupSelection()
		m.clampSuggestionSelection()
		return m, nil
	case developerLoadedMsg:
		if msg.Err != nil {
			m.apiOnline = false
			m.errText = msg.Err.Error()
			return m, nil
		}
		m.apiOnline = true
		m.errText = ""
		m.dev = msg.Status
		m.devRows = msg.Entries
		m.clampDeveloperSelection()
		return m, nil
	case developerExportedMsg:
		if msg.Err != nil {
			m.errText = msg.Err.Error()
			return m, nil
		}
		m.errText = "exported HAR to " + msg.Path
		return m, nil
	case actionDoneMsg:
		if msg.Err != nil {
			m.apiOnline = false
			m.errText = msg.Err.Error()
			return m, nil
		}
		return m, m.loadDashboardCmd()
	case ruleTestDoneMsg:
		if msg.Err != nil {
			m.ruleTestErr = msg.Err.Error()
			return m, nil
		}
		m.ruleTestResult = &msg.Result
		m.ruleTestErr = ""
		return m, nil
	case policyGroupsDoneMsg:
		if msg.Err != nil {
			m.errText = msg.Err.Error()
			return m, nil
		}
		m.errText = ""
		m.policies = msg.Policies
		m.clampPolicySelection()
		return m, nil
	case subscriptionsLoadedMsg:
		if msg.Err != nil {
			m.errText = msg.Err.Error()
			return m, nil
		}
		m.subscriptions = msg.Subscriptions
		return m, nil
	case configExportDoneMsg:
		if msg.Err != nil {
			m.configActionMsg = "export error: " + msg.Err.Error()
			return m, nil
		}
		m.configActionMsg = "exported to " + msg.Path
		return m, nil
	case configImportDoneMsg:
		if msg.Err != nil {
			m.configActionMsg = "import error: " + msg.Err.Error()
			return m, nil
		}
		m.configActionMsg = msg.Result.Message
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
	switch m.viewMode {
	case viewModeActivity:
		return m.activityView()
	case viewModeLibrary:
		return m.libraryView()
	case viewModeSettings:
		return m.settingsView()
	case viewModeDeveloper:
		return m.developerView()
	case viewModeNetwork:
		return m.networkView()
	}

	width := m.contentWidth()
	sections := []string{
		m.renderHeader("Now"),
	}
	if m.errText != "" {
		sections = append(sections, m.renderError(width))
	}
	sections = append(sections,
		m.renderConnectionSection(width),
		m.renderNowPolicySection(width),
		m.renderLiveTrafficSection(width),
		m.renderRecentDecisionsSection(width),
		m.renderFooter(
			"Keys: c connect  d disconnect  r refresh  2 activity  3 library  4 settings  5 developer  6 network  q quit",
			"Keys: c/d  r  2 act  3 lib  4 set  5 dev  6 net  q",
		),
	)
	return joinSections(sections)
}

func (m model) activityView() string {
	width := m.contentWidth()
	sections := []string{m.renderHeader("Activity")}
	if m.errText != "" {
		sections = append(sections, m.renderError(width))
	}
	sections = append(sections,
		m.renderTrafficDetailSection(width),
		m.renderLogsSection(width),
		m.renderFooter(
			"Keys: a all  b block  d direct  p proxy  / filter  x test route  tab focus  up/down select  enter/n rule  c cleanup  r refresh  1 now  3 library  6 network  q quit",
			"Keys: a/b/d/p  / filter  x test  tab  up/down  enter/n rule  c clean  r  1 now  3 lib  6 net  q",
		),
	)
	return joinSections(sections)
}

func (m model) renderLogsSection(width int) string {
	lines := make([]string, 0, m.logVisibleRows()+1)
	if len(m.logs) == 0 {
		lines = append(lines, emptyStateLines("No logs yet", "Connection and daemon events will appear here.", width)...)
	} else {
		if m.logScroll > 0 {
			lines = append(lines, subtleStyle.Render(fmt.Sprintf("  showing %d lines above tail", m.logScroll)))
		}
		for _, line := range m.visibleLogLines() {
			lines = append(lines, "  "+truncate(line, width-2))
		}
	}
	return renderSection("Logs", lines)
}

func (m model) libraryView() string {
	width := m.contentWidth()
	sections := []string{m.renderHeader("Library")}
	if m.errText != "" {
		sections = append(sections, m.renderError(width))
	}
	sections = append(sections,
		m.renderProfileListenerSections(width),
		m.renderPolicyGroupsSection(width),
		m.renderSubscriptionsSection(width),
		m.renderRuleHitsSection(width),
		m.renderServersSection(width),
		m.renderFooter(
			"Keys: tab policy focus  [ prev profile  ] next profile  up/down select  enter apply  t test latency  f refresh subs  r refresh  1 now  2 activity  6 network  q quit",
			"Keys: tab focus  [/] profile  up/down  enter  t test  f subs  r  1 now  2 act  6 net  q",
		),
	)
	return joinSections(sections)
}

func (m model) settingsView() string {
	width := m.contentWidth()
	sections := []string{m.renderHeader("Settings")}
	if m.errText != "" {
		sections = append(sections, m.renderError(width))
	}
	eventStatus := "enabled"
	if m.eventCtx.Err() != nil {
		eventStatus = "stopped"
	}
	lines := []string{
		truncate("  API endpoint  "+m.apiAddr, width),
		truncate("  API status    "+mapBool(m.apiOnline, "online", "offline"), width),
		truncate("  Event stream  "+eventStatus, width),
		subtleStyle.Render(truncate("  Edit daemon config in your TOML file or platform settings UI.", width)),
	}
	configLines := m.configImportExportLines(width)
	sections = append(sections,
		renderSection("Settings", lines),
		renderSection("Config Import / Export", configLines),
		m.renderFooter(
			"Keys: e export config  i import config  r refresh  1 now  2 activity  3 library  5 developer  q quit",
			"Keys: e export  i import  r  1 now  2 activity  3 library  5 dev  q",
		),
	)
	return joinSections(sections)
}

func (m model) developerView() string {
	width := m.contentWidth()
	sections := []string{m.renderHeader("Developer")}
	if m.errText != "" {
		sections = append(sections, m.renderError(width))
	}
	sections = append(sections,
		renderSection("Developer Mode", m.developerStatusLines(width)),
		renderSection("HTTP Inspector", m.developerEntryLines(width)),
		m.renderFooter(
			"Keys: up/down select  e export HAR  c clear  r refresh  1 now  2 activity  3 library  4 settings  q quit",
			"Keys: up/down  tab detail  e export  c clear  r  1 now  2 activity  q",
		),
	)
	return joinSections(sections)
}

func (m model) developerStatusLines(width int) []string {
	state := "disabled"
	if m.dev.Enabled {
		state = "enabled"
	}
	mitm := "off"
	if m.dev.MITMEnabled {
		mitm = "on"
	}
	noCache := "off"
	if m.dev.NoCacheEnabled {
		noCache = "on"
	}
	lines := []string{
		truncate(fmt.Sprintf("  State %s  MITM %s  No-cache %s  Captures %d/%d  Body cap %s",
			state, mitm, noCache, m.dev.CaptureCount, m.dev.CaptureLimit, formatBytes(uint64(maxInt64(0, m.dev.BodyLimitBytes)))), width),
	}
	if m.dev.CACertPath != "" {
		lines = append(lines, truncate("  CA "+m.dev.CACertPath, width))
	}
	if m.dev.CAFingerprintSHA256 != "" {
		lines = append(lines, subtleStyle.Render(truncate("  SHA256 "+m.dev.CAFingerprintSHA256, width)))
	}
	if m.dev.CANotBefore != "" || m.dev.CANotAfter != "" {
		lines = append(lines, subtleStyle.Render(truncate("  Valid "+emptyDash(m.dev.CANotBefore)+" -> "+emptyDash(m.dev.CANotAfter), width)))
	}
	if !m.dev.Enabled {
		lines = append(lines, subtleStyle.Render(truncate("  Enable [developer] in TOML to capture HTTP(S) transactions.", width)))
	}
	return lines
}

func (m model) developerEntryLines(width int) []string {
	if len(m.devRows) == 0 {
		return emptyStateLines("No captured requests", "HTTP proxy requests appear here when developer mode is enabled.", width)
	}
	lines := make([]string, 0)
	limit := m.developerVisibleRows()
	for i, entry := range firstDeveloperRows(m.devRows, limit) {
		prefix := " "
		if i == m.selectedDeveloper {
			prefix = "›"
		}
		status := "--"
		if entry.Status > 0 {
			status = strconv.Itoa(entry.Status)
		}
		lines = append(lines, truncate(fmt.Sprintf("%s %-6s %-3s %-7s %s",
			prefix,
			entry.Method,
			status,
			entry.Scheme,
			entry.URL,
		), width))
	}
	if len(m.devRows) > limit {
		lines = append(lines, subtleStyle.Render(fmt.Sprintf("  +%d more rows hidden by terminal height", len(m.devRows)-limit)))
	}
	if entry, ok := m.selectedDeveloperEntry(); ok {
		lines = append(lines, "")
		lines = append(lines, tableHeaderStyle.Render(truncate("  Request Detail", width)))
		lines = append(lines, truncate(fmt.Sprintf("  %s %s  Status %s  Profile %s  Chain %s", entry.Method, entry.URL, statusText(entry.Status), emptyDash(entry.Profile), emptyDash(entry.ChainName)), width))
		lines = append(lines, subtleStyle.Render(truncate("  "+developerTabsLine(m.selectedDeveloperTab), width)))
		lines = append(lines, developerDetailTabLines(entry, m.selectedDeveloperTab, width)...)
		if entry.Error != "" {
			lines = append(lines, errorStyle.Render(truncate("  Error "+entry.Error, width)))
		}
	}
	return lines
}

var developerDetailTabs = []string{"Headers", "Body", "JSON", "Cookies"}

func developerTabsLine(active int) string {
	parts := make([]string, 0, len(developerDetailTabs))
	for i, tab := range developerDetailTabs {
		if i == active {
			parts = append(parts, "["+tab+"]")
		} else {
			parts = append(parts, tab)
		}
	}
	return strings.Join(parts, "  ")
}

func developerDetailTabLines(entry developerEntryPayload, active, width int) []string {
	switch developerDetailTabs[active%len(developerDetailTabs)] {
	case "Headers":
		return developerHeaderLines(entry, width)
	case "Body":
		return developerBodyLines(entry, width)
	case "JSON":
		return developerJSONLines(entry, width)
	case "Cookies":
		return developerCookieLines(entry, width)
	default:
		return nil
	}
}

func developerHeaderLines(entry developerEntryPayload, width int) []string {
	lines := []string{subtleStyle.Render(truncate("  Request headers", width))}
	lines = append(lines, developerHeaderRows(entry.Request.Headers, width)...)
	lines = append(lines, subtleStyle.Render(truncate("  Response headers", width)))
	lines = append(lines, developerHeaderRows(entry.Response.Headers, width)...)
	return lines
}

func developerHeaderRows(headers []developerHeaderPayload, width int) []string {
	if len(headers) == 0 {
		return []string{subtleStyle.Render(truncate("    none", width))}
	}
	lines := make([]string, 0, minInt(len(headers), 8)+1)
	for _, header := range headers[:minInt(len(headers), 8)] {
		value := header.Value
		if header.Redacted {
			value += " redacted"
		}
		if header.Truncated {
			value += " truncated"
		}
		lines = append(lines, truncate(fmt.Sprintf("    %s: %s", header.Name, value), width))
	}
	if len(headers) > 8 {
		lines = append(lines, subtleStyle.Render(truncate(fmt.Sprintf("    +%d more", len(headers)-8), width)))
	}
	return lines
}

func developerBodyLines(entry developerEntryPayload, width int) []string {
	lines := developerOneBodyLines("Request", entry.Request.Body, width)
	lines = append(lines, developerOneBodyLines("Response", entry.Response.Body, width)...)
	return lines
}

func developerOneBodyLines(title string, body developerBodyPayload, width int) []string {
	mime := body.MimeType
	if mime == "" {
		mime = "unknown"
	}
	lines := []string{
		subtleStyle.Render(truncate(fmt.Sprintf("  %s body %s preview %s%s  %s %s", title, formatBytes(uint64(maxInt64(0, body.Size))), formatBytes(uint64(maxInt64(0, body.PreviewBytes))), truncSuffix(body.Truncated), mime, emptyDash(body.Encoding)), width)),
	}
	text := developerBodyText(body)
	if text != "" {
		for _, line := range previewLines(text, 3) {
			lines = append(lines, truncate("    "+line, width))
		}
	}
	return lines
}

func developerJSONLines(entry developerEntryPayload, width int) []string {
	lines := developerOneJSONLines("Request", entry.Request.Body, width)
	lines = append(lines, developerOneJSONLines("Response", entry.Response.Body, width)...)
	return lines
}

func developerOneJSONLines(title string, body developerBodyPayload, width int) []string {
	text := body.Preview
	if strings.TrimSpace(text) == "" {
		return []string{subtleStyle.Render(truncate("  "+title+" JSON none", width))}
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(text), "", "  "); err != nil {
		return []string{subtleStyle.Render(truncate("  "+title+" JSON invalid or unavailable", width))}
	}
	lines := []string{subtleStyle.Render(truncate("  "+title+" JSON", width))}
	for _, line := range previewLines(buf.String(), 6) {
		lines = append(lines, truncate("    "+line, width))
	}
	return lines
}

func developerCookieLines(entry developerEntryPayload, width int) []string {
	lines := []string{subtleStyle.Render(truncate("  Request cookies", width))}
	lines = append(lines, developerCookieRows(entry.Request.Cookies, width)...)
	lines = append(lines, subtleStyle.Render(truncate("  Response cookies", width)))
	lines = append(lines, developerCookieRows(entry.Response.Cookies, width)...)
	return lines
}

func developerCookieRows(cookies []developerCookiePayload, width int) []string {
	if len(cookies) == 0 {
		return []string{subtleStyle.Render(truncate("    none", width))}
	}
	lines := make([]string, 0, minInt(len(cookies), 8)+1)
	for _, cookie := range cookies[:minInt(len(cookies), 8)] {
		attrs := make([]string, 0, 4)
		if cookie.Domain != "" {
			attrs = append(attrs, "domain="+cookie.Domain)
		}
		if cookie.Path != "" {
			attrs = append(attrs, "path="+cookie.Path)
		}
		if cookie.Secure {
			attrs = append(attrs, "secure")
		}
		if cookie.HTTPOnly {
			attrs = append(attrs, "httponly")
		}
		value := cookie.Value
		if cookie.Redacted {
			value += " redacted"
		}
		if len(attrs) > 0 {
			value += "  " + strings.Join(attrs, " ")
		}
		lines = append(lines, truncate(fmt.Sprintf("    %s=%s", cookie.Name, value), width))
	}
	if len(cookies) > 8 {
		lines = append(lines, subtleStyle.Render(truncate(fmt.Sprintf("    +%d more", len(cookies)-8), width)))
	}
	return lines
}

func developerBodyText(body developerBodyPayload) string {
	if body.Preview != "" {
		return body.Preview
	}
	if body.PreviewBase64 != "" {
		return "[base64] " + body.PreviewBase64
	}
	return ""
}

func previewLines(text string, limit int) []string {
	raw := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(raw) > limit {
		raw = raw[:limit]
		raw = append(raw, "...")
	}
	return raw
}

func (m model) selectedDeveloperEntry() (developerEntryPayload, bool) {
	if len(m.devRows) == 0 {
		return developerEntryPayload{}, false
	}
	idx := m.selectedDeveloper
	if idx < 0 {
		idx = 0
	}
	if idx >= len(m.devRows) {
		idx = len(m.devRows) - 1
	}
	return m.devRows[idx], true
}

func (m *model) moveDeveloperSelection(delta int) {
	if len(m.devRows) == 0 {
		m.selectedDeveloper = 0
		return
	}
	m.selectedDeveloper = (m.selectedDeveloper + delta + len(m.devRows)) % len(m.devRows)
}

func (m *model) clampDeveloperSelection() {
	if len(m.devRows) == 0 {
		m.selectedDeveloper = 0
		return
	}
	if m.selectedDeveloper < 0 {
		m.selectedDeveloper = 0
	}
	if m.selectedDeveloper >= len(m.devRows) {
		m.selectedDeveloper = len(m.devRows) - 1
	}
}

func firstDeveloperRows(rows []developerEntryPayload, limit int) []developerEntryPayload {
	if limit > 0 && len(rows) > limit {
		return rows[:limit]
	}
	return rows
}

func (m model) developerVisibleRows() int {
	if m.height <= 0 {
		return 10
	}
	return clampInt(m.height-16, 3, 16)
}

func statusText(status int) string {
	if status <= 0 {
		return "--"
	}
	return strconv.Itoa(status)
}

func truncSuffix(truncated bool) string {
	if truncated {
		return " truncated"
	}
	return ""
}

func (m model) renderHeader(title string) string {
	width := m.contentWidth()
	left := fmt.Sprintf("clambhook %s · %s", version, title)
	right := "API " + m.apiAddr
	return headerStyle.Width(width).Render(fitLine(left, right, width))
}

func (m model) renderStatusSummary(width int) string {
	parts := []string{
		m.runningBadge(),
		m.apiBadge(),
		"Profile " + truncate(emptyDash(m.activeProfile()), maxInt(8, width/3)),
		fmt.Sprintf("%d active connections", m.activeConnections()),
	}
	if m.status.TunnelMode == "tun" {
		parts = append(parts, runningBadgeStyle.Render("TUN"))
	} else if m.status.TunnelMode == "proxy" {
		parts = append(parts, subtleStyle.Render("PROXY"))
	}
	line := strings.Join(parts, "  ")
	if lipgloss.Width(line) <= width {
		return line
	}
	return strings.Join([]string{
		strings.Join([]string{m.runningBadge(), m.apiBadge()}, "  "),
		"Profile " + truncate(emptyDash(m.activeProfile()), width-8),
		fmt.Sprintf("%d active connections", m.activeConnections()),
	}, "\n")
}

func (m model) renderConnectionSection(width int) string {
	lines := []string{
		m.renderStatusSummary(width),
		truncate(fmt.Sprintf("  Active profile %s  Connections %d", emptyDash(m.activeProfile()), m.activeConnections()), width),
	}
	return renderSection("Connection", lines)
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

func emptyStateLines(title, detail string, width int) []string {
	return []string{
		subtleStyle.Render(truncate("  "+title, width)),
		subtleStyle.Render(truncate("  "+detail, width)),
	}
}

func mapBool(value bool, yes, no string) string {
	if value {
		return yes
	}
	return no
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
		return emptyStateLines("No profiles yet", "Add or import a profile in the daemon config.", width)
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

func (m model) tunnelModeLine(width int) string {
	switch m.status.TunnelMode {
	case "tun":
		return runningBadgeStyle.Render(truncate("  FULL TUNNEL (TUN)", width))
	case "proxy":
		return stoppedBadgeStyle.Render(truncate("  PROXY (SOCKS5 / HTTP)", width))
	default:
		return ""
	}
}

func (m model) listenerLines(width int) []string {
	if len(m.status.Listeners) == 0 {
		return emptyStateLines("No listeners active", "Connect to start the configured listeners.", width)
	}
	lines := make([]string, 0, len(m.status.Listeners)+1)
	if modeLine := m.tunnelModeLine(width); modeLine != "" {
		lines = append(lines, modeLine)
	}
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
	return renderSection("Proxy Policies", m.serverLines(width))
}

func (m model) renderPolicyGroupsSection(width int) string {
	return renderSection("Policy Groups", m.policyGroupLines(width))
}

func (m model) renderSubscriptionsSection(width int) string {
	return renderSection("Rule Subscriptions", m.subscriptionLines(width))
}

func (m model) subscriptionLines(width int) []string {
	if len(m.subscriptions.Subscriptions) == 0 {
		return emptyStateLines("No rule subscriptions", "Add subscriptions to the active profile to block domains.", width)
	}
	lines := make([]string, 0, len(m.subscriptions.Subscriptions))
	for _, sub := range m.subscriptions.Subscriptions {
		state := "enabled"
		stateStyle := subtleStyle
		if sub.Disabled {
			state = "disabled"
		} else {
			stateStyle = activeLineStyle
		}
		counts := ""
		if sub.Status.EntryCount > 0 {
			counts = fmt.Sprintf("  %d entries", sub.Status.EntryCount)
		}
		errPart := ""
		if sub.Status.LastFetchErr != "" {
			errPart = "  err: " + truncate(sub.Status.LastFetchErr, 30)
		}
		line := fmt.Sprintf("  %-20s  %-8s  %s  %s%s%s",
			truncate(sub.Name, 20),
			sub.Action,
			sub.Format,
			state,
			counts,
			errPart,
		)
		lines = append(lines, stateStyle.Render(truncate(line, width)))
	}
	lines = append(lines, subtleStyle.Render(truncate("  f to refresh all", width)))
	return lines
}

func (m model) configImportExportLines(width int) []string {
	lines := []string{}
	if m.configExportEditing {
		hint := "  Export to file  " + m.configExportInput + "  (enter confirm, esc cancel)"
		lines = append(lines, selectedLineStyle.Render(truncate(hint, width)))
		return lines
	}
	if m.configImportEditing {
		hint := "  Import from file  " + m.configImportInput + "  (enter confirm, esc cancel)"
		lines = append(lines, selectedLineStyle.Render(truncate(hint, width)))
		return lines
	}
	if m.configActionMsg != "" {
		style := activeLineStyle
		if strings.HasPrefix(m.configActionMsg, "export error") || strings.HasPrefix(m.configActionMsg, "import error") {
			style = errorStyle
		}
		lines = append(lines, style.Render(truncate("  "+m.configActionMsg, width)))
	}
	lines = append(lines, subtleStyle.Render(truncate("  e to export active config  i to import a TOML file", width)))
	return lines
}

func (m model) renderNowPolicySection(width int) string {
	lines := make([]string, 0, 3)
	counts := m.actionCounts()
	lines = append(lines, truncate(fmt.Sprintf("  Mode Rule  Proxy %d  Direct %d  Block %d", counts["proxy"], counts["direct"], counts["block"]), width))
	if len(m.policies.Groups) > 0 {
		group := m.policies.Groups[0]
		reason := routeSelectionReason(group.SelectionReason)
		lines = append(lines, truncate(fmt.Sprintf("  Group %s  Policy %s  Selected %s", group.Name, policyModeText(group), emptyDash(selectedPolicyChain(group))), width))
		lines = append(lines, truncate(fmt.Sprintf("  Fallback %s  Reason %s  %s", fallbackStateText(group.SelectionReason == "no_healthy_fallback"), reason, policyGroupHealthText(group)), width))
	} else {
		lines = append(lines, truncate(fmt.Sprintf("  Group --  Static route  Selected %s", emptyDash(m.defaultRouteName())), width))
		lines = append(lines, truncate(fmt.Sprintf("  Fallback No  %s", routeCountText(len(m.servers.Chains))), width))
	}
	return renderSection("Route Control", lines)
}

func (m model) policyGroupLines(width int) []string {
	if len(m.policies.Groups) == 0 {
		return emptyStateLines("No policy groups", "Routes use static chains in this profile.", width)
	}
	lines := make([]string, 0)
	for gi, group := range m.policies.Groups {
		selected := selectedPolicyChain(group)
		prefix := " "
		if m.policyFocus && gi == m.selectedPolicyGroup {
			prefix = "›"
		}
		header := fmt.Sprintf("%s %s  %s  selected %s  %s",
			prefix,
			group.Name,
			policyModeText(group),
			emptyDash(selected),
			policyGroupHealthText(group),
		)
		if m.policyFocus && gi == m.selectedPolicyGroup {
			lines = append(lines, selectedLineStyle.Render(truncate(header, width)))
		} else {
			lines = append(lines, tableHeaderStyle.Render(truncate(header, width)))
		}
		for mi, chainName := range group.Chains {
			marker := " "
			if chainName == selected {
				marker = "*"
			}
			if m.policyFocus && gi == m.selectedPolicyGroup && mi == m.selectedPolicyMember {
				marker = ">"
			}
			result, ok := policyResultFor(group, chainName)
			line := fmt.Sprintf("  %s %-18s %s", marker, chainName, policyResultText(result, ok))
			if m.policyFocus && gi == m.selectedPolicyGroup && mi == m.selectedPolicyMember {
				lines = append(lines, activeLineStyle.Render(truncate(line, width)))
			} else if !ok || !result.Healthy {
				lines = append(lines, subtleStyle.Render(truncate(line, width)))
			} else {
				lines = append(lines, truncate(line, width))
			}
		}
	}
	return lines
}

func (m model) serverLines(width int) []string {
	if len(m.servers.Chains) == 0 {
		return emptyStateLines("No servers in this profile", "Add a chain and server to the active profile.", width)
	}
	lines := make([]string, 0)
	if width >= 92 {
		widths := serverColumnWidths(width)
		lines = append(lines, tableHeaderStyle.Render(tableRow([]string{"", "Name", "Protocol", "Address", "Location", "Chain"}, widths)))
		for _, ch := range m.servers.Chains {
			lines = append(lines, tableHeaderStyle.Render(truncate(policySummaryLine(ch, width), width)))
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
		lines = append(lines, tableHeaderStyle.Render(truncate(policySummaryLine(ch, width), width)))
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

func policySummaryLine(ch chainPayload, width int) string {
	hops := ch.HopCount
	if hops == 0 {
		hops = len(ch.Servers)
	}
	return truncate(fmt.Sprintf("  Policy %s  %d hops  %s", ch.Name, hops, udpSummary(ch.Capabilities)), width)
}

func udpSummary(caps protocolCapabilitiesPayload) string {
	if caps.UDP {
		if caps.UDPMode == "" {
			return "UDP supported"
		}
		return "UDP " + caps.UDPMode
	}
	if caps.UDPReason != "" {
		return "UDP unsupported: " + caps.UDPReason
	}
	return "UDP unsupported"
}

func policyModeText(group policyGroupPayload) string {
	mode := strings.TrimSpace(group.SelectionMode)
	if mode == "" {
		mode = strings.TrimSpace(group.Type)
	}
	if mode == "" {
		return "policy"
	}
	return strings.ReplaceAll(mode, "-", " ")
}

func selectedPolicyChain(group policyGroupPayload) string {
	if strings.TrimSpace(group.SelectedChain) != "" {
		return group.SelectedChain
	}
	if strings.TrimSpace(group.Selected) != "" {
		return group.Selected
	}
	if len(group.Chains) > 0 {
		return group.Chains[0]
	}
	return ""
}

func routeSelectionReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "--"
	}
	return strings.ReplaceAll(reason, "_", " ")
}

func fallbackStateText(fallback bool) string {
	if fallback {
		return "Yes (no healthy fallback)"
	}
	return "No"
}

func policyResultFor(group policyGroupPayload, chainName string) (policyProbeResultPayload, bool) {
	for _, result := range group.Results {
		if result.ChainName == chainName {
			return result, true
		}
	}
	return policyProbeResultPayload{}, false
}

func policyGroupHealthText(group policyGroupPayload) string {
	if len(group.Results) == 0 {
		return "Pending health"
	}
	selected := selectedPolicyChain(group)
	healthy := 0
	for _, result := range group.Results {
		if result.Healthy {
			healthy++
		}
	}
	if result, ok := policyResultFor(group, selected); ok && result.Healthy {
		return fmt.Sprintf("Healthy / %d/%d", healthy, len(group.Results))
	}
	return fmt.Sprintf("Fallback / %d/%d healthy", healthy, len(group.Results))
}

func policyResultText(result policyProbeResultPayload, ok bool) string {
	if !ok {
		return "pending"
	}
	if result.Healthy {
		parts := []string{"healthy"}
		if result.LatencyNs > 0 {
			parts = append(parts, formatDurationNs(result.LatencyNs))
		}
		if result.StatusCode > 0 {
			parts = append(parts, fmt.Sprintf("HTTP %d", result.StatusCode))
		}
		return strings.Join(parts, "  ")
	}
	if result.Error != "" {
		return "error  " + result.Error
	}
	return "unhealthy"
}

func (m model) renderLiveTrafficSection(width int) string {
	current := m.bandwidth.current()
	graphWidth := graphWidthFor(width)
	lines := []string{
		m.trafficSummaryLine(width),
		fmt.Sprintf("  Rx %-10s %s", formatRate(current.RxBps), m.bandwidth.graph(true, graphWidth)),
		fmt.Sprintf("  Tx %-10s %s", formatRate(current.TxBps), m.bandwidth.graph(false, graphWidth)),
	}
	if m.traffic.Summary.PersistError != "" {
		lines = append(lines, errorStyle.Render(truncate("  History: "+m.traffic.Summary.PersistError, width)))
	}
	return renderSection("Live Traffic", lines)
}

func (m model) renderRecentDecisionsSection(width int) string {
	if len(m.traffic.Connections) == 0 {
		return renderSection("Recent Decisions", emptyStateLines("No recent activity", "Connection decisions will appear here.", width))
	}
	limit := m.dashboardTrafficRows()
	lines := make([]string, 0, limit+1)
	for _, conn := range firstTrafficRows(m.traffic.Connections, limit) {
		lines = append(lines, recentDecisionLine(conn, width))
	}
	if len(m.traffic.Connections) > limit {
		lines = append(lines, subtleStyle.Render(fmt.Sprintf("  +%d more (press 2)", len(m.traffic.Connections)-limit)))
	}
	return renderSection("Recent Decisions", lines)
}

func (m model) renderTrafficDetailSection(width int) string {
	lines := []string{m.trafficSummaryLine(width)}
	if m.traffic.Summary.PersistError != "" {
		lines = append(lines, errorStyle.Render(truncate("  History: "+m.traffic.Summary.PersistError, width)))
	}
	lines = append(lines, m.monitorFilterLine(width))
	lines = append(lines, m.trafficBreakdownLines(width)...)
	if line := m.ruleTestLine(width); line != "" {
		lines = append(lines, line)
	}
	lines = append(lines, m.ruleHitLines(width)...)
	lines = append(lines, m.blockDecisionLines(width)...)
	lines = append(lines, m.cleanupSuggestionLines(width)...)
	lines = append(lines, m.ruleSuggestionLines(width)...)
	if m.pendingRule != nil {
		lines = append(lines, m.pendingRuleLine(width))
	}
	if m.pendingCleanup != nil {
		lines = append(lines, m.pendingCleanupLine(width))
	}
	rows := m.filteredTrafficConnections()
	if len(rows) == 0 {
		lines = append(lines, "")
		lines = append(lines, emptyStateLines("No matching activity", "Connection decisions appear here when traffic passes through clambhook.", width)...)
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
	return renderSection("Activity", lines)
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
	active := m.traffic.ProfileContext.Active
	if active == "" {
		active = m.activeProfile()
	}
	all := m.quickFilterCount("all", len(m.traffic.Connections))
	return truncate(fmt.Sprintf("  [%s] profile %s  all %d  proxy %d  direct %d  block %d  %s %q",
		filter,
		emptyDash(active),
		all,
		counts["proxy"],
		counts["direct"],
		counts["block"],
		prompt,
		search,
	), width)
}

func (m model) trafficBreakdownLines(width int) []string {
	var lines []string
	if line := breakdownSummaryLine("  Routes", m.traffic.Breakdowns.Actions, width); line != "" {
		lines = append(lines, line)
	}
	if line := breakdownSummaryLine("  Top chains", m.traffic.Breakdowns.Chains, width); line != "" {
		lines = append(lines, line)
	}
	if line := breakdownSummaryLine("  Top rules", m.traffic.Breakdowns.Rules, width); line != "" {
		lines = append(lines, line)
	}
	return lines
}

func breakdownSummaryLine(label string, rows []breakdownRowPayload, width int) string {
	if len(rows) == 0 {
		return ""
	}
	limit := minInt(4, len(rows))
	parts := make([]string, 0, limit)
	for _, row := range rows[:limit] {
		name := row.Label
		if name == "" {
			name = row.Key
		}
		parts = append(parts, fmt.Sprintf("%s %d", emptyDash(name), row.Count))
	}
	return truncate(label+"  "+strings.Join(parts, "  "), width)
}

func (m model) ruleTestLine(width int) string {
	if m.ruleTestEditing {
		line := fmt.Sprintf("  Test route  %s  (enter run, esc cancel)", m.ruleTestInput)
		if m.ruleTestErr != "" {
			line += "  " + m.ruleTestErr
		}
		return selectedLineStyle.Render(truncate(line, width))
	}
	if m.ruleTestErr != "" {
		return errorStyle.Render(truncate("  Test route  "+m.ruleTestErr, width))
	}
	if m.ruleTestResult == nil {
		return ""
	}
	result := *m.ruleTestResult
	decision := result.Decision
	action := strings.ToUpper(actionFamilyFromAction(decision.Action))
	parts := []string{
		fmt.Sprintf("  Test route  %s %s -> %s", decision.Network, decision.Target, action),
	}
	if decision.RuleName != "" {
		parts = append(parts, "rule "+decision.RuleName)
	} else if decision.Default {
		parts = append(parts, "default")
	}
	if decision.ChainName != "" {
		parts = append(parts, "chain "+decision.ChainName)
	}
	if result.Chain != nil {
		parts = append(parts, fmt.Sprintf("%d hops", result.Chain.HopCount), udpSummary(result.Chain.Capabilities))
	}
	return truncate(strings.Join(parts, "  "), width)
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

func (m model) blockDecisionLines(width int) []string {
	if len(m.traffic.BlockDecisions) == 0 {
		return nil
	}
	limit := minInt(3, len(m.traffic.BlockDecisions))
	parts := make([]string, 0, limit)
	for _, decision := range m.traffic.BlockDecisions[:limit] {
		target := decision.TargetHost
		if target == "" {
			target = decision.Target
		}
		parts = append(parts, fmt.Sprintf("%s/%s", emptyDash(target), emptyDash(decision.RuleName)))
	}
	return []string{truncate("  Recent blocks  "+strings.Join(parts, "  "), width)}
}

func (m model) cleanupSuggestionLines(width int) []string {
	if len(m.traffic.CleanupSuggestions) == 0 {
		return nil
	}
	limit := minInt(4, len(m.traffic.CleanupSuggestions))
	lines := []string{tableHeaderStyle.Render(truncate("  Rule cleanup", width))}
	for i, suggestion := range m.traffic.CleanupSuggestions[:limit] {
		prefix := " "
		if m.cleanupFocus && i == m.selectedCleanup {
			prefix = "›"
		}
		target := cleanupTarget(suggestion)
		lines = append(lines, subtleStyle.Render(truncate(fmt.Sprintf("%s %s  %s  %s",
			prefix,
			cleanupActionText(suggestion),
			emptyDash(target),
			suggestion.Message,
		), width)))
	}
	if len(m.traffic.CleanupSuggestions) > limit {
		lines = append(lines, subtleStyle.Render(fmt.Sprintf("  +%d more cleanup suggestions", len(m.traffic.CleanupSuggestions)-limit)))
	}
	return lines
}

func (m model) ruleSuggestionLines(width int) []string {
	if len(m.traffic.RuleSuggestions) == 0 {
		return nil
	}
	limit := minInt(4, len(m.traffic.RuleSuggestions))
	lines := []string{tableHeaderStyle.Render(truncate("  Suggested rules", width))}
	for i, suggestion := range m.traffic.RuleSuggestions[:limit] {
		prefix := " "
		if m.suggestionFocus && i == m.selectedSuggestion {
			prefix = "›"
		}
		match := ruleMatchText(suggestion.DraftRule)
		lines = append(lines, truncate(fmt.Sprintf("%s %s  %s  %s  %d hits  %s",
			prefix,
			strings.ToUpper(suggestion.Kind),
			suggestion.DraftRule.Action,
			match,
			suggestion.Count,
			suggestion.Reason,
		), width))
	}
	if len(m.traffic.RuleSuggestions) > limit {
		lines = append(lines, subtleStyle.Render(fmt.Sprintf("  +%d more suggested rules", len(m.traffic.RuleSuggestions)-limit)))
	}
	return lines
}

func (m model) pendingRuleLine(width int) string {
	rule := m.pendingRule
	if rule == nil {
		return ""
	}
	match := ruleMatchText(rule.rulePayload)
	keys := "y save, b/d/p action, esc cancel"
	if rule.ConnID != "" {
		keys = "y save, a allow, b/d/p action, esc cancel"
	}
	ttlStr := ""
	if rule.ConnID != "" && m.pendingRuleTTLIdx >= 0 && m.pendingRuleTTLIdx < len(tempRuleTTLOptions) {
		opts := make([]string, len(tempRuleTTLOptions))
		for i, opt := range tempRuleTTLOptions {
			if i == m.pendingRuleTTLIdx {
				opts[i] = "[" + opt.Label + "]"
			} else {
				opts[i] = opt.Label
			}
		}
		ttlStr = "  TTL: " + strings.Join(opts, " ") + "  (left/right)"
	}
	return selectedLineStyle.Render(truncate(fmt.Sprintf("  New rule: %s  %s  %s  (%s)%s", rule.Name, rule.Action, match, keys, ttlStr), width))
}

func (m model) pendingCleanupLine(width int) string {
	cleanup := m.pendingCleanup
	if cleanup == nil {
		return ""
	}
	return selectedLineStyle.Render(truncate(fmt.Sprintf("  Cleanup: %s %s  (y apply, esc cancel)",
		strings.ToLower(cleanupActionText(*cleanup)),
		emptyDash(cleanupTarget(*cleanup)),
	), width))
}

func cleanupTarget(suggestion cleanupSuggestionPayload) string {
	if suggestion.TargetRuleName != "" {
		return suggestion.TargetRuleName
	}
	return suggestion.RuleName
}

func cleanupActionText(suggestion cleanupSuggestionPayload) string {
	switch suggestion.Operation {
	case "move_rule_to_end":
		return "Move to end"
	default:
		return "Delete"
	}
}

func ruleMatchText(rule rulePayload) string {
	if len(rule.Domains) > 0 {
		return strings.Join(rule.Domains, ",")
	}
	if len(rule.DomainSuffixes) > 0 {
		return "*." + strings.Join(rule.DomainSuffixes, ",*.")
	}
	if len(rule.CIDRs) > 0 {
		return strings.Join(rule.CIDRs, ",")
	}
	if len(rule.DomainKeywords) > 0 {
		return "contains " + strings.Join(rule.DomainKeywords, ",")
	}
	return "any"
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
		if full && !m.suggestionFocus && i == m.selectedTraffic {
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

func (m model) selectedRuleSuggestion() (ruleSuggestionPayload, bool) {
	if len(m.traffic.RuleSuggestions) == 0 {
		return ruleSuggestionPayload{}, false
	}
	idx := m.selectedSuggestion
	if idx < 0 {
		idx = 0
	}
	if idx >= len(m.traffic.RuleSuggestions) {
		idx = len(m.traffic.RuleSuggestions) - 1
	}
	return m.traffic.RuleSuggestions[idx], true
}

func (m model) selectedCleanupSuggestion() (cleanupSuggestionPayload, bool) {
	if len(m.traffic.CleanupSuggestions) == 0 {
		return cleanupSuggestionPayload{}, false
	}
	idx := m.selectedCleanup
	if idx < 0 {
		idx = 0
	}
	if idx >= len(m.traffic.CleanupSuggestions) {
		idx = len(m.traffic.CleanupSuggestions) - 1
	}
	return m.traffic.CleanupSuggestions[idx], true
}

func (m *model) advanceActivityFocus() {
	hasCleanup := len(m.traffic.CleanupSuggestions) > 0
	hasSuggestions := len(m.traffic.RuleSuggestions) > 0
	switch {
	case !m.cleanupFocus && !m.suggestionFocus:
		if hasCleanup {
			m.cleanupFocus = true
			m.suggestionFocus = false
			m.clampCleanupSelection()
			return
		}
		if hasSuggestions {
			m.cleanupFocus = false
			m.suggestionFocus = true
			m.clampSuggestionSelection()
			return
		}
	case m.cleanupFocus:
		m.cleanupFocus = false
		if hasSuggestions {
			m.suggestionFocus = true
			m.clampSuggestionSelection()
			return
		}
	case m.suggestionFocus:
		m.suggestionFocus = false
	}
	m.cleanupFocus = false
	m.suggestionFocus = false
}

func (m *model) moveTrafficSelection(delta int) {
	rows := m.filteredTrafficConnections()
	if len(rows) == 0 {
		m.selectedTraffic = 0
		return
	}
	m.selectedTraffic = (m.selectedTraffic + delta + len(rows)) % len(rows)
}

func (m *model) moveSuggestionSelection(delta int) {
	if len(m.traffic.RuleSuggestions) == 0 {
		m.selectedSuggestion = 0
		m.suggestionFocus = false
		return
	}
	m.selectedSuggestion = (m.selectedSuggestion + delta + len(m.traffic.RuleSuggestions)) % len(m.traffic.RuleSuggestions)
}

func (m *model) moveCleanupSelection(delta int) {
	if len(m.traffic.CleanupSuggestions) == 0 {
		m.selectedCleanup = 0
		m.cleanupFocus = false
		return
	}
	m.selectedCleanup = (m.selectedCleanup + delta + len(m.traffic.CleanupSuggestions)) % len(m.traffic.CleanupSuggestions)
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

func (m *model) clampSuggestionSelection() {
	if len(m.traffic.RuleSuggestions) == 0 {
		m.selectedSuggestion = 0
		m.suggestionFocus = false
		return
	}
	if m.selectedSuggestion < 0 {
		m.selectedSuggestion = 0
	}
	if m.selectedSuggestion >= len(m.traffic.RuleSuggestions) {
		m.selectedSuggestion = len(m.traffic.RuleSuggestions) - 1
	}
}

func (m *model) clampCleanupSelection() {
	if len(m.traffic.CleanupSuggestions) == 0 {
		m.selectedCleanup = 0
		m.cleanupFocus = false
		return
	}
	if m.selectedCleanup < 0 {
		m.selectedCleanup = 0
	}
	if m.selectedCleanup >= len(m.traffic.CleanupSuggestions) {
		m.selectedCleanup = len(m.traffic.CleanupSuggestions) - 1
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
		truncate(fmt.Sprintf("  Host %s  Decision %s  Rule %s  Route %s  Profile %s", emptyDash(host), actionChip(conn), ruleLabel(conn), routeLabel(conn), emptyDash(conn.Profile)), width),
		truncate(routeControlDetailLine(conn), width),
		truncate(fmt.Sprintf("  Target %s  Network %s  App %s  Listener %s %s", emptyDash(conn.Target), emptyDash(conn.Network), emptyDash(conn.Application), conn.Listener.Protocol, conn.Listener.Addr), width),
		truncate(fmt.Sprintf("  Bytes %s down / %s up  Duration %s  Decision %s", formatBytes(conn.RxTotal), formatBytes(conn.TxTotal), formatDurationNs(conn.DurationNs), formatDurationNs(conn.DecisionNs)), width),
	}
	if host != "" {
		lines = append(lines, selectedLineStyle.Render(truncate("  Action enter/n create rule from connection", width)))
	}
	if conn.Geo.CountryCode != "" || conn.Geo.Country != "" || conn.Geo.City != "" {
		lines = append(lines, truncate(fmt.Sprintf("  Location %s %s %s", countryFlag(conn.Geo.CountryCode), conn.Geo.Country, conn.Geo.City), width))
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
	if len(m.traffic.RuleHits) > 0 {
		out := make([]ruleHit, 0, len(m.traffic.RuleHits))
		for _, hit := range m.traffic.RuleHits {
			out = append(out, ruleHit{Name: hit.RuleName, Action: hit.Action, Count: hit.Count})
		}
		sortRuleHits(out)
		return out
	}
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
	if len(m.traffic.QuickFilters) > 0 {
		for _, key := range []string{"proxy", "direct", "block"} {
			counts[key] = m.quickFilterCount(key, 0)
		}
		return counts
	}
	for _, conn := range m.traffic.Connections {
		counts[actionFamily(conn)]++
	}
	return counts
}

func (m model) quickFilterCount(key string, fallback int) int {
	for _, filter := range m.traffic.QuickFilters {
		if filter.Key == key {
			return filter.Count
		}
	}
	return fallback
}

func connectionMatchesSearch(conn trafficConnectionPayload, query string) bool {
	fields := []string{
		conn.Target,
		conn.TargetHost,
		conn.TargetPort,
		conn.RuleName,
		conn.RuleAction,
		conn.Profile,
		conn.ChainName,
		conn.Application,
		conn.Network,
		conn.Listener.Protocol,
		conn.Listener.Addr,
		conn.ClientAddr,
		conn.Geo.Country,
		conn.Geo.CountryCode,
		conn.Geo.City,
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

func ruleLabel(conn trafficConnectionPayload) string {
	if conn.RuleName != "" {
		return conn.RuleName
	}
	if conn.Default {
		return "default"
	}
	return emptyDash("")
}

func routeLabel(conn trafficConnectionPayload) string {
	switch {
	case conn.GroupName != "" && conn.ChainName != "":
		return conn.GroupName + " -> " + conn.ChainName
	case conn.GroupName != "":
		return conn.GroupName
	case conn.ChainName != "":
		return conn.ChainName
	case actionFamily(conn) == "direct":
		return "direct"
	case actionFamily(conn) == "block":
		return "blocked"
	default:
		return emptyDash("")
	}
}

func routeControlDetailLine(conn trafficConnectionPayload) string {
	control := conn.RouteControl
	mode := routeControlMode(control)
	decision := strings.ToUpper(routeControlDecision(conn))
	source := routeControlSource(conn)
	group := control.PolicyGroup
	if group == "" {
		group = conn.GroupName
	}
	selected := control.SelectedChain
	if selected == "" {
		selected = conn.ChainName
	}
	return fmt.Sprintf("  Route Control  Mode %s  Decision %s  Source %s  Group %s  Selected %s  Fallback %s",
		mode,
		decision,
		source,
		emptyDash(group),
		emptyDash(selected),
		fallbackStateText(control.Fallback),
	)
}

func routeControlMode(control routeControlPayload) string {
	mode := strings.TrimSpace(control.Mode)
	if mode == "" {
		return "Rule"
	}
	mode = strings.ReplaceAll(mode, "_", " ")
	return strings.ToUpper(mode[:1]) + mode[1:]
}

func routeControlDecision(conn trafficConnectionPayload) string {
	decision := strings.ToLower(strings.TrimSpace(conn.RouteControl.Decision))
	if decision != "" {
		return decision
	}
	return actionFamilyFromAction(conn.RuleAction)
}

func routeControlSource(conn trafficConnectionPayload) string {
	source := strings.TrimSpace(conn.RouteControl.Source)
	if source == "" {
		if conn.Default || conn.RouteControl.Default {
			source = "default"
		} else if conn.RuleName != "" {
			source = "profile_rule"
		}
	}
	if source == "" {
		return "--"
	}
	return strings.ReplaceAll(source, "_", " ")
}

func recentDecisionLine(conn trafficConnectionPayload, width int) string {
	parts := []string{
		actionChip(conn),
		emptyDash(recentDecisionTarget(conn)),
		"rule " + ruleLabel(conn),
		"route " + routeLabel(conn),
		fmt.Sprintf("%s down / %s up", formatBytes(conn.RxTotal), formatBytes(conn.TxTotal)),
		formatDurationNs(conn.DurationNs),
	}
	return truncate("  "+strings.Join(parts, "  "), width)
}

func recentDecisionTarget(conn trafficConnectionPayload) string {
	if conn.TargetHost != "" {
		return conn.TargetHost
	}
	if conn.Visibility != nil && conn.Visibility.Host != "" {
		return conn.Visibility.Host
	}
	return conn.Target
}

func actionFamily(conn trafficConnectionPayload) string {
	if decision := strings.ToLower(strings.TrimSpace(conn.RouteControl.Decision)); decision != "" {
		return actionFamilyFromAction(decision)
	}
	action := strings.ToLower(strings.TrimSpace(conn.RuleAction))
	return actionFamilyFromAction(action)
}

func actionFamilyFromAction(action string) string {
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "direct":
		return "direct"
	case "block", "reject":
		return "block"
	default:
		return "proxy"
	}
}

func parseRuleTestInput(input string) (network, target string, err error) {
	parts := strings.Fields(input)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("use: tcp host:port or udp host:port")
	}
	network = strings.ToLower(parts[0])
	if network != "tcp" && network != "udp" {
		return "", "", fmt.Errorf("network must be tcp or udp")
	}
	host, port := splitHostPortLoose(parts[1])
	if host == "" || port == "" {
		return "", "", fmt.Errorf("target must be host:port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", "", fmt.Errorf("target port must be between 1 and 65535")
	}
	return network, parts[1], nil
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

func (m model) ruleDraftFromSelected() (pendingRule, bool) {
	if m.suggestionFocus {
		suggestion, ok := m.selectedRuleSuggestion()
		if !ok {
			return pendingRule{}, false
		}
		return pendingRule{rulePayload: suggestion.DraftRule}, true
	}
	conn, ok := m.selectedConnection()
	if !ok {
		return pendingRule{}, false
	}
	host := connectionHost(conn)
	if host == "" {
		return pendingRule{}, false
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
	return pendingRule{
		rulePayload: rule,
		ConnID:      conn.ConnID,
		Profile:     conn.Profile,
		Scope:       "auto",
	}, true
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
	case "group":
		if conn.GroupName != "" {
			return "group:" + conn.GroupName
		}
	}
	if conn.GroupName != "" {
		return "group:" + conn.GroupName
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
		policies, err := client.policyGroups()
		if err != nil {
			return dashboardLoadedMsg{Err: err}
		}
		subs, err := client.ruleSubscriptions("")
		if err != nil {
			subs = ruleSubscriptionsPayload{}
		}
		traffic, err := client.traffic()
		if err != nil {
			return dashboardLoadedMsg{Err: err}
		}
		dev, devRows, err := client.developer()
		if err != nil {
			return dashboardLoadedMsg{Err: err}
		}
		return dashboardLoadedMsg{Status: status, Profiles: profiles, Servers: servers, Policies: policies, Subscriptions: subs, Traffic: traffic, Developer: dev, DevRows: devRows}
	}
}

func (m model) loadStatusCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		status, err := client.status()
		if err != nil {
			return statusLoadedMsg{Err: err}
		}
		policies, err := client.policyGroups()
		if err != nil {
			return statusLoadedMsg{Err: err}
		}
		traffic, err := client.traffic()
		return statusLoadedMsg{Status: status, Policies: policies, Traffic: traffic, Err: err}
	}
}

func (m model) actionCmd(fn func() error) tea.Cmd {
	return func() tea.Msg {
		return actionDoneMsg{Err: fn()}
	}
}

func (m model) savePendingRuleCmd(rule pendingRule) tea.Cmd {
	ttlIdx := m.pendingRuleTTLIdx
	return m.actionCmd(func() error {
		if ttlIdx >= 0 && ttlIdx < len(tempRuleTTLOptions) && tempRuleTTLOptions[ttlIdx].Seconds > 0 && rule.ConnID != "" {
			return m.client.createTemporaryRuleFromConnection(
				rule.ConnID, rule.Profile, rule.Name, rule.Action, rule.Scope,
				tempRuleTTLOptions[ttlIdx].Seconds,
			)
		}
		if rule.ConnID != "" {
			return m.client.createRuleFromConnection(createRuleFromConnectionRequest{
				ConnID:  rule.ConnID,
				Profile: rule.Profile,
				Name:    rule.Name,
				Action:  rule.Action,
				Scope:   rule.Scope,
			})
		}
		return m.client.createRule(rule.rulePayload)
	})
}

func (m model) cleanupRuleCmd(cleanup cleanupSuggestionPayload) tea.Cmd {
	return m.actionCmd(func() error {
		return m.client.cleanupRule(cleanupRuleRequest{
			Profile:        cleanup.Profile,
			Kind:           cleanup.Kind,
			RuleName:       cleanup.RuleName,
			TargetRuleName: cleanupTarget(cleanup),
			Operation:      cleanup.Operation,
		})
	})
}

func (m model) ruleTestCmd(network, target string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		result, err := client.testRule(network, target)
		return ruleTestDoneMsg{Result: result, Err: err}
	}
}

func (m model) testPolicyGroupCmd(group string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		policies, err := client.testPolicyGroup(group)
		return policyGroupsDoneMsg{Policies: policies, Err: err}
	}
}

func (m model) refreshSubscriptionsCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		if err := client.refreshRuleSubscriptions("", nil); err != nil {
			return subscriptionsLoadedMsg{Err: err}
		}
		subs, err := client.ruleSubscriptions("")
		return subscriptionsLoadedMsg{Subscriptions: subs, Err: err}
	}
}

func (m model) exportConfigCmd(path string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		toml, err := client.exportConfig()
		if err != nil {
			return configExportDoneMsg{Err: err}
		}
		if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
			return configExportDoneMsg{Err: err}
		}
		return configExportDoneMsg{Path: path}
	}
}

func (m model) importConfigCmd(path string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		data, err := os.ReadFile(path)
		if err != nil {
			return configImportDoneMsg{Err: err}
		}
		result, err := client.importConfig(string(data))
		return configImportDoneMsg{Result: result, Err: err}
	}
}

func (m model) applySelectedPolicyCmd() tea.Cmd {
	group, chainName, ok := m.selectedPolicyChainName()
	if !ok {
		return nil
	}
	client := m.client
	profile := m.activeProfile()
	if strings.EqualFold(group.Type, "select") || strings.EqualFold(group.SelectionMode, "manual") {
		return func() tea.Msg {
			policies, err := client.selectPolicyGroup(profile, group.Name, chainName)
			return policyGroupsDoneMsg{Policies: policies, Err: err}
		}
	}
	return func() tea.Msg {
		policies, err := client.testPolicyGroup(group.Name)
		return policyGroupsDoneMsg{Policies: policies, Err: err}
	}
}

func (m model) loadDeveloperCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		status, entries, err := client.developer()
		return developerLoadedMsg{Status: status, Entries: entries, Err: err}
	}
}

func (m model) exportDeveloperHARCmd() tea.Cmd {
	client := m.client
	path := fmt.Sprintf("clambhook-%s.har", time.Now().Format("20060102-150405"))
	return func() tea.Msg {
		err := client.exportDeveloperHAR(path)
		return developerExportedMsg{Path: path, Err: err}
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

func (m *model) movePolicyMemberSelection(delta int) {
	group, ok := m.selectedPolicyGroupPayload()
	if !ok || len(group.Chains) == 0 {
		m.selectedPolicyMember = 0
		return
	}
	m.selectedPolicyMember = (m.selectedPolicyMember + delta + len(group.Chains)) % len(group.Chains)
}

func (m *model) clampPolicySelection() {
	if len(m.policies.Groups) == 0 {
		m.selectedPolicyGroup = 0
		m.selectedPolicyMember = 0
		m.policyFocus = false
		return
	}
	if m.selectedPolicyGroup < 0 {
		m.selectedPolicyGroup = 0
	}
	if m.selectedPolicyGroup >= len(m.policies.Groups) {
		m.selectedPolicyGroup = len(m.policies.Groups) - 1
	}
	group := m.policies.Groups[m.selectedPolicyGroup]
	if len(group.Chains) == 0 {
		m.selectedPolicyMember = 0
		return
	}
	if m.selectedPolicyMember < 0 {
		m.selectedPolicyMember = 0
	}
	if m.selectedPolicyMember >= len(group.Chains) {
		m.selectedPolicyMember = len(group.Chains) - 1
	}
}

func (m model) selectedPolicyGroupPayload() (policyGroupPayload, bool) {
	if len(m.policies.Groups) == 0 {
		return policyGroupPayload{}, false
	}
	idx := m.selectedPolicyGroup
	if idx < 0 {
		idx = 0
	}
	if idx >= len(m.policies.Groups) {
		idx = len(m.policies.Groups) - 1
	}
	return m.policies.Groups[idx], true
}

func (m model) selectedPolicyChainName() (policyGroupPayload, string, bool) {
	group, ok := m.selectedPolicyGroupPayload()
	if !ok || len(group.Chains) == 0 {
		return policyGroupPayload{}, "", false
	}
	idx := m.selectedPolicyMember
	if idx < 0 {
		idx = 0
	}
	if idx >= len(group.Chains) {
		idx = len(group.Chains) - 1
	}
	return group, group.Chains[idx], true
}

func (m model) activeProfile() string {
	if m.profiles.Active != "" {
		return m.profiles.Active
	}
	return m.status.Profile
}

func (m model) defaultRouteName() string {
	if len(m.servers.Chains) > 0 {
		return m.servers.Chains[0].Name
	}
	for _, conn := range m.traffic.Connections {
		if conn.ChainName != "" {
			return conn.ChainName
		}
	}
	return ""
}

func routeCountText(count int) string {
	switch count {
	case 0:
		return "0 routes"
	case 1:
		return "1 route"
	default:
		return fmt.Sprintf("%d routes", count)
	}
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

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// parseFilterTokens splits a filter input string into typed tokens.
// Recognized keys: app, domain, country, action, port, ip.
// Bare words (no key:) become a plain-text search token.
func parseFilterTokens(input string) []filterToken {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}
	parts := strings.Fields(input)
	tokens := make([]filterToken, 0, len(parts))
	for _, part := range parts {
		colon := strings.IndexByte(part, ':')
		if colon > 0 {
			key := strings.ToLower(part[:colon])
			value := part[colon+1:]
			switch key {
			case "app", "domain", "country", "action", "port", "ip":
				tokens = append(tokens, filterToken{Key: key, Value: value})
				continue
			}
		}
		tokens = append(tokens, filterToken{Key: "", Value: part})
	}
	return tokens
}

// filterTokenAction returns the action value from tokens, or "".
func filterTokenAction(tokens []filterToken) string {
	for _, t := range tokens {
		if t.Key == "action" {
			return t.Value
		}
	}
	return ""
}

// filterTokenText returns the concatenation of plain-text tokens.
func filterTokenText(tokens []filterToken) string {
	var parts []string
	for _, t := range tokens {
		if t.Key == "" {
			parts = append(parts, t.Value)
		}
	}
	return strings.Join(parts, " ")
}

// filterTokenByKey returns the value for a specific key, or "".
func filterTokenByKey(tokens []filterToken, key string) string {
	for _, t := range tokens {
		if t.Key == key {
			return t.Value
		}
	}
	return ""
}

// filteredTraffic fetches traffic from the API with the current filter tokens.
func (m model) filteredTraffic() (trafficSnapshotPayload, error) {
	return m.client.trafficWithFilters(
		filterTokenAction(m.filterTokens),
		filterTokenByKey(m.filterTokens, "app"),
		filterTokenByKey(m.filterTokens, "domain"),
		filterTokenByKey(m.filterTokens, "country"),
		filterTokenByKey(m.filterTokens, "port"),
		filterTokenText(m.filterTokens),
	)
}

// networkView renders the Little Snitch-style hierarchy view.
func (m model) networkView() string {
	width := m.contentWidth()
	sections := []string{m.renderHeader("Network")}
	if m.errText != "" {
		sections = append(sections, m.renderError(width))
	}
	sections = append(sections,
		m.renderNetworkTreeSection(width),
		m.renderFooter(
			"Keys: up/down move  right/left expand/collapse  enter select  a/b/d action  / filter  r refresh  1 now  2 activity  3 library  q quit",
			"Keys: up/down  right/left  enter  a/b/d  / filter  r  1 now  2 act  3 lib  q",
		),
	)
	return joinSections(sections)
}

func (m model) renderNetworkTreeSection(width int) string {
	hierarchy := m.traffic.NetworkHierarchy
	if len(hierarchy) == 0 {
		return renderSection("Network", emptyStateLines("No connections yet", "Live app/domain/country hierarchy will appear here.", width))
	}

	lines := []string{}

	// Render filter bar.
	filterLine := m.renderFilterBar(width)
	if filterLine != "" {
		lines = append(lines, filterLine)
	}

	halfWidth := width / 2
	if halfWidth < 20 {
		halfWidth = 20
	}

	// Left: tree. Right: detail.
	treeLines := m.renderNetworkTree(halfWidth)
	detailLines := m.renderNetworkDetail(halfWidth)

	maxLines := len(treeLines)
	if len(detailLines) > maxLines {
		maxLines = len(detailLines)
	}

	for i := 0; i < maxLines; i++ {
		left := ""
		right := ""
		if i < len(treeLines) {
			left = treeLines[i]
		}
		if i < len(detailLines) {
			right = detailLines[i]
		}
		lines = append(lines, fmt.Sprintf("%-*s  %s", halfWidth, left, right))
	}

	return renderSection("Network", lines)
}

func (m model) renderFilterBar(width int) string {
	var parts []string
	for _, t := range m.filterTokens {
		var label string
		if t.Key != "" {
			label = t.Key + ":" + t.Value
		} else {
			label = t.Value
		}
		style := subtleStyle
		switch t.Key {
		case "action":
			if t.Value == "block" {
				style = errorStyle
			} else if t.Value == "direct" {
				style = activeLineStyle
			}
		case "country":
			style = onlineBadgeStyle
		}
		parts = append(parts, style.Render("["+label+"]"))
	}
	if m.filterEditing {
		parts = append(parts, selectedLineStyle.Render(m.filterInput+"_"))
	} else if len(parts) == 0 {
		return ""
	}
	return "  Filter: " + strings.Join(parts, " ")
}

func (m model) renderNetworkTree(width int) []string {
	hierarchy := m.traffic.NetworkHierarchy
	lines := []string{}
	if m.netTreeExpanded == nil {
		m.netTreeExpanded = make(map[int]bool)
	}

	for i, app := range hierarchy {
		prefix := "  "
		cursor := " "
		if i == m.netTreeAppIdx && m.netTreeDepth == 0 {
			cursor = ">"
		}
		expanded := m.netTreeExpanded[i]
		arrow := "+"
		if expanded {
			arrow = "-"
		}
		if len(app.Domains) > 0 {
			lines = append(lines, truncate(fmt.Sprintf("%s%s %s %s  %dc  %s",
				cursor, arrow, prefix, app.Application, app.ConnCount, formatBytes(app.RxTotal+app.TxTotal)), width))
		} else {
			lines = append(lines, truncate(fmt.Sprintf("%s  %s %s  %dc  %s",
				cursor, prefix, app.Application, app.ConnCount, formatBytes(app.RxTotal+app.TxTotal)), width))
		}

		if expanded {
			for j, domain := range app.Domains {
				dPrefix := "    "
				dCursor := " "
				if i == m.netTreeAppIdx && j == m.netTreeDomainIdx && m.netTreeDepth == 1 {
					dCursor = ">"
				}
				dArrow := "+"
				dExpanded := m.netTreeExpanded[i*1000+j+1]
				if dExpanded {
					dArrow = "-"
				}
				if len(domain.Countries) > 0 {
					lines = append(lines, truncate(fmt.Sprintf("%s%s %s %s  %dc  %s",
						dCursor, dArrow, dPrefix, domain.Domain, domain.ConnCount, formatBytes(domain.RxTotal+domain.TxTotal)), width))
				} else {
					lines = append(lines, truncate(fmt.Sprintf("%s  %s %s  %dc  %s",
						dCursor, dPrefix, domain.Domain, domain.ConnCount, formatBytes(domain.RxTotal+domain.TxTotal)), width))
				}

				if dExpanded {
					for k, country := range domain.Countries {
						cPrefix := "      "
						cCursor := " "
						if i == m.netTreeAppIdx && j == m.netTreeDomainIdx && k == m.netTreeCountryIdx && m.netTreeDepth == 2 {
							cCursor = ">"
						}
						lines = append(lines, truncate(fmt.Sprintf("%s  %s %s  %dc", cCursor, cPrefix, country.Code, country.ConnCount), width))
					}
				}
			}
		}
	}

	return lines
}

func (m model) renderNetworkDetail(width int) []string {
	hierarchy := m.traffic.NetworkHierarchy
	if m.netTreeAppIdx < 0 || m.netTreeAppIdx >= len(hierarchy) {
		return []string{subtleStyle.Render("Select a node to see details")}
	}
	app := hierarchy[m.netTreeAppIdx]

	lines := []string{tableHeaderStyle.Render("Details")}

	switch m.netTreeDepth {
	case 0:
		lines = append(lines,
			fmt.Sprintf("App:      %s", app.Application),
			fmt.Sprintf("Conns:    %d (active: %d)", app.ConnCount, app.ActiveCount),
			fmt.Sprintf("Bytes:    %s", formatBytes(app.RxTotal+app.TxTotal)),
			fmt.Sprintf("Domains:  %d", len(app.Domains)),
		)
	case 1:
		if m.netTreeDomainIdx >= 0 && m.netTreeDomainIdx < len(app.Domains) {
			domain := app.Domains[m.netTreeDomainIdx]
			lines = append(lines,
				fmt.Sprintf("Domain:   %s", domain.Domain),
				fmt.Sprintf("Conns:    %d", domain.ConnCount),
				fmt.Sprintf("Bytes:    %s", formatBytes(domain.RxTotal+domain.TxTotal)),
				fmt.Sprintf("Countries: %d", len(domain.Countries)),
			)
		}
	case 2:
		if m.netTreeDomainIdx >= 0 && m.netTreeDomainIdx < len(app.Domains) {
			domain := app.Domains[m.netTreeDomainIdx]
			if m.netTreeCountryIdx >= 0 && m.netTreeCountryIdx < len(domain.Countries) {
				country := domain.Countries[m.netTreeCountryIdx]
				lines = append(lines,
					fmt.Sprintf("Country:  %s (%s)", country.Name, country.Code),
					fmt.Sprintf("Conns:    %d", country.ConnCount),
					fmt.Sprintf("Bytes:    %s", formatBytes(country.RxTotal+country.TxTotal)),
				)
			}
		}
	}

	// Show matching connections.
	conns := m.connectionsForNetworkNode()
	if len(conns) > 0 {
		lines = append(lines, "", tableHeaderStyle.Render("Connections"))
		limit := minInt(6, len(conns))
		for i, conn := range conns[:limit] {
			cursor := " "
			if i == m.netDetailConnIdx {
				cursor = ">"
			}
			lines = append(lines, truncate(fmt.Sprintf("%s %s  %s  %s",
				cursor, conn.Target, conn.RuleAction, conn.RuleName), width))
		}
		if len(conns) > limit {
			lines = append(lines, subtleStyle.Render(fmt.Sprintf("  +%d more", len(conns)-limit)))
		}
	}

	return lines
}

// connectionsForNetworkNode returns connections matching the selected tree node.
func (m model) connectionsForNetworkNode() []trafficConnectionPayload {
	hierarchy := m.traffic.NetworkHierarchy
	if m.netTreeAppIdx < 0 || m.netTreeAppIdx >= len(hierarchy) {
		return nil
	}
	app := hierarchy[m.netTreeAppIdx]
	var result []trafficConnectionPayload
	for _, conn := range m.traffic.Connections {
		if !strings.EqualFold(conn.Application, app.Application) {
			continue
		}
		if m.netTreeDepth >= 1 && m.netTreeDomainIdx >= 0 && m.netTreeDomainIdx < len(app.Domains) {
			domain := app.Domains[m.netTreeDomainIdx]
			host := strings.ToLower(conn.TargetHost)
			if !strings.HasSuffix(host, domain.Domain) {
				continue
			}
		}
		if m.netTreeDepth >= 2 && m.netTreeDomainIdx >= 0 && m.netTreeDomainIdx < len(app.Domains) {
			domain := app.Domains[m.netTreeDomainIdx]
			if m.netTreeCountryIdx >= 0 && m.netTreeCountryIdx < len(domain.Countries) {
				country := domain.Countries[m.netTreeCountryIdx]
				if !strings.EqualFold(conn.Geo.CountryCode, country.Code) {
					continue
				}
			}
		}
		result = append(result, conn)
	}
	return result
}

// updateNetworkView handles key events for the network view.
func (m model) updateNetworkView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.filterEditing {
		switch msg.String() {
		case "esc":
			m.filterEditing = false
			m.filterInput = ""
		case "enter":
			m.filterTokens = parseFilterTokens(m.filterInput)
			m.filterEditing = false
			m.filterInput = ""
			return m, m.loadDashboardCmd()
		case "backspace", "ctrl+h":
			if len(m.filterInput) > 0 {
				m.filterInput = m.filterInput[:len(m.filterInput)-1]
			} else if len(m.filterTokens) > 0 {
				m.filterTokens = m.filterTokens[:len(m.filterTokens)-1]
			}
		default:
			if len(msg.Runes) > 0 {
				m.filterInput += string(msg.Runes)
			}
		}
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
			return m, m.savePendingRuleCmd(rule)
		case "b":
			m.pendingRule.Action = "block"
			return m, nil
		case "d":
			m.pendingRule.Action = "direct"
			return m, nil
		case "left":
			if m.pendingRuleTTLIdx > 0 {
				m.pendingRuleTTLIdx--
			}
			return m, nil
		case "right":
			if m.pendingRuleTTLIdx < len(tempRuleTTLOptions)-1 {
				m.pendingRuleTTLIdx++
			}
			return m, nil
		}
		return m, nil
	}

	hierarchy := m.traffic.NetworkHierarchy
	if m.netTreeExpanded == nil {
		m.netTreeExpanded = make(map[int]bool)
	}

	switch msg.String() {
	case "r":
		return m, m.loadDashboardCmd()
	case "/":
		m.filterEditing = true
		m.filterInput = ""
		return m, nil
	case "esc":
		m.filterTokens = nil
		m.filterEditing = false
		m.filterInput = ""
		return m, nil
	case "up", "k":
		if m.netTreeDepth == 0 {
			if m.netTreeAppIdx > 0 {
				m.netTreeAppIdx--
			}
		} else if m.netTreeDepth == 1 {
			if m.netTreeDomainIdx > 0 {
				m.netTreeDomainIdx--
			} else {
				m.netTreeDepth = 0
			}
		} else if m.netTreeDepth == 2 {
			if m.netTreeCountryIdx > 0 {
				m.netTreeCountryIdx--
			} else {
				m.netTreeDepth = 1
			}
		}
		return m, nil
	case "down", "j":
		if m.netTreeDepth == 0 {
			if m.netTreeAppIdx < len(hierarchy)-1 {
				m.netTreeAppIdx++
			}
		} else if m.netTreeDepth == 1 {
			if m.netTreeAppIdx < len(hierarchy) && m.netTreeDomainIdx < len(hierarchy[m.netTreeAppIdx].Domains)-1 {
				m.netTreeDomainIdx++
			}
		} else if m.netTreeDepth == 2 {
			if m.netTreeAppIdx < len(hierarchy) && m.netTreeDomainIdx < len(hierarchy[m.netTreeAppIdx].Domains) {
				domain := hierarchy[m.netTreeAppIdx].Domains[m.netTreeDomainIdx]
				if m.netTreeCountryIdx < len(domain.Countries)-1 {
					m.netTreeCountryIdx++
				}
			}
		}
		return m, nil
	case "right", "enter":
		if m.netTreeDepth == 0 && m.netTreeAppIdx < len(hierarchy) {
			if len(hierarchy[m.netTreeAppIdx].Domains) > 0 {
				m.netTreeExpanded[m.netTreeAppIdx] = true
				m.netTreeDepth = 1
				m.netTreeDomainIdx = 0
			}
		} else if m.netTreeDepth == 1 && m.netTreeAppIdx < len(hierarchy) {
			if m.netTreeDomainIdx < len(hierarchy[m.netTreeAppIdx].Domains) {
				domain := hierarchy[m.netTreeAppIdx].Domains[m.netTreeDomainIdx]
				if len(domain.Countries) > 0 {
					m.netTreeExpanded[m.netTreeAppIdx*1000+m.netTreeDomainIdx+1] = true
					m.netTreeDepth = 2
					m.netTreeCountryIdx = 0
				} else if msg.String() == "enter" {
					return m.ruleDraftFromNetworkNode()
				}
			}
		} else if msg.String() == "enter" {
			return m.ruleDraftFromNetworkNode()
		}
		return m, nil
	case "left":
		if m.netTreeDepth == 2 {
			if m.netTreeAppIdx < len(hierarchy) && m.netTreeDomainIdx < len(hierarchy[m.netTreeAppIdx].Domains) {
				m.netTreeExpanded[m.netTreeAppIdx*1000+m.netTreeDomainIdx+1] = false
			}
			m.netTreeDepth = 1
		} else if m.netTreeDepth == 1 {
			if m.netTreeAppIdx < len(hierarchy) {
				m.netTreeExpanded[m.netTreeAppIdx] = false
			}
			m.netTreeDepth = 0
		}
		return m, nil
	case "b":
		return m.ruleDraftFromNetworkNode("block")
	case "d":
		return m.ruleDraftFromNetworkNode("direct")
	case "a":
		return m.ruleDraftFromNetworkNode("allow")
	}
	return m, nil
}

// ruleDraftFromNetworkNode creates a pending rule from the selected network node.
func (m model) ruleDraftFromNetworkNode(action ...string) (tea.Model, tea.Cmd) {
	hierarchy := m.traffic.NetworkHierarchy
	if m.netTreeAppIdx < 0 || m.netTreeAppIdx >= len(hierarchy) {
		m.errText = "select a node before creating a rule"
		return m, nil
	}
	app := hierarchy[m.netTreeAppIdx]
	act := "block"
	if len(action) > 0 && action[0] != "" {
		act = action[0]
	}

	var domain string
	if m.netTreeDepth >= 1 && m.netTreeDomainIdx >= 0 && m.netTreeDomainIdx < len(app.Domains) {
		domain = app.Domains[m.netTreeDomainIdx].Domain
	}

	rule := pendingRule{
		rulePayload: rulePayload{
			Name:   fmt.Sprintf("NET-%s-%s", strings.ToUpper(act), app.Application),
			Action: act,
		},
		Profile: m.traffic.ProfileContext.Active,
	}
	if domain != "" {
		rule.rulePayload.DomainSuffixes = []string{domain}
		rule.Name = fmt.Sprintf("NET-%s-%s", strings.ToUpper(act), domain)
	}
	m.pendingRule = &rule
	m.pendingRuleTTLIdx = 0
	return m, nil
}

// renderRuleHitsSection renders rule usage stats in the Library view.
func (m model) renderRuleHitsSection(width int) string {
	hits := m.traffic.RuleHits
	if len(hits) == 0 {
		return ""
	}
	limit := minInt(10, len(hits))
	lines := []string{tableHeaderStyle.Render(truncate("  Rule Activity", width))}
	for _, hit := range hits[:limit] {
		lastHit := ""
		if hit.LastHitTsNs > 0 {
			lastHit = formatAge(time.Unix(0, hit.LastHitTsNs))
		}
		tempTag := ""
		if hit.Temporary {
			tempTag = " (temp)"
		}
		lines = append(lines, truncate(fmt.Sprintf("  %s  %s  %d hits  %s  %s%s",
			emptyDash(hit.RuleName),
			hit.Action,
			hit.Count,
			formatBytes(hit.RxTotal+hit.TxTotal),
			lastHit,
			tempTag,
		), width))
	}
	if len(hits) > limit {
		lines = append(lines, subtleStyle.Render(fmt.Sprintf("  +%d more rules", len(hits)-limit)))
	}
	return renderSection("Rule Activity", lines)
}

// formatAge returns a human-readable age string.
func formatAge(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
