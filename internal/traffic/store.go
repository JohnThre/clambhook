// Package traffic maintains the daemon's metadata-only traffic history.
package traffic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/events"
	"github.com/JohnThre/clambhook/internal/geo"
	"github.com/JohnThre/clambhook/internal/temprules"
)

const (
	StateOpening = "opening"
	StateDialing = "dialing"
	StateActive  = "active"
	StateClosed  = "closed"

	historyVersion = 1
)

// GeoLookupFunc enriches target addresses with optional location metadata.
type GeoLookupFunc func(address string) (*geo.Location, error)

// Store records live connection metadata and a bounded recent closed history.
type Store struct {
	mu sync.RWMutex

	enabled       bool
	historyLimit  int
	historyMaxAge time.Duration
	historyPath   string
	geoLookup     GeoLookupFunc

	active      map[string]*Connection
	closed      []Connection // newest first
	persistErr  string
	lastSavedNs int64
}

// Listener identifies the local ingress that accepted a connection.
type Listener struct {
	Protocol string `json:"protocol"`
	Addr     string `json:"addr"`
}

// Hop describes one proxy hop in a connection path.
type Hop struct {
	Index     int    `json:"index"`
	Name      string `json:"name,omitempty"`
	Protocol  string `json:"protocol,omitempty"`
	Address   string `json:"address,omitempty"`
	State     string `json:"state,omitempty"`
	ElapsedNs int64  `json:"elapsed_ns,omitempty"`
	Error     string `json:"error,omitempty"`
}

// TimelineEvent is a compact connection lifecycle event for UI timelines.
type TimelineEvent struct {
	TsNs   int64  `json:"ts_ns"`
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
}

// Visibility describes metadata-only protocol visibility. It intentionally
// excludes payload bytes, headers, query strings, and response data.
type Visibility struct {
	Kind      string `json:"kind,omitempty"`
	Method    string `json:"method,omitempty"`
	Scheme    string `json:"scheme,omitempty"`
	Host      string `json:"host,omitempty"`
	Port      string `json:"port,omitempty"`
	Path      string `json:"path,omitempty"`
	QueryType string `json:"query_type,omitempty"`
}

// Connection is the API model exposed to end-user UIs. It intentionally
// contains connection metadata and counters only; payload bytes are not stored.
type Connection struct {
	ConnID       string            `json:"conn_id"`
	Profile      string            `json:"profile,omitempty"`
	State        string            `json:"state"`
	StartTsNs    int64             `json:"start_ts_ns"`
	UpdatedTsNs  int64             `json:"updated_ts_ns"`
	EndTsNs      int64             `json:"end_ts_ns,omitempty"`
	Listener     Listener          `json:"listener"`
	ClientAddr   string            `json:"client_addr,omitempty"`
	ChainName    string            `json:"chain_name,omitempty"`
	GroupName    string            `json:"group_name,omitempty"`
	RuleName     string            `json:"rule_name,omitempty"`
	RuleAction   string            `json:"rule_action,omitempty"`
	Default      bool              `json:"default,omitempty"`
	DecisionNs   int64             `json:"decision_ns,omitempty"`
	Target       string            `json:"target,omitempty"`
	TargetHost   string            `json:"target_host,omitempty"`
	TargetPort   string            `json:"target_port,omitempty"`
	Network      string            `json:"network,omitempty"`
	Source       string            `json:"source,omitempty"`
	Application  string            `json:"application,omitempty"`
	Hops         []Hop             `json:"hops,omitempty"`
	Timeline     []TimelineEvent   `json:"timeline,omitempty"`
	Visibility   *Visibility       `json:"visibility,omitempty"`
	Explanation  *RouteExplanation `json:"explanation,omitempty"`
	RouteControl *RouteControl     `json:"route_control,omitempty"`
	Geo          geo.Location      `json:"geo"`
	GeoError     string            `json:"geo_error,omitempty"`

	TotalDialNs int64   `json:"total_dial_ns,omitempty"`
	RxBps       float64 `json:"rx_bps"`
	TxBps       float64 `json:"tx_bps"`
	RxTotal     uint64  `json:"rx_total"`
	TxTotal     uint64  `json:"tx_total"`
	DurationNs  int64   `json:"duration_ns,omitempty"`
	CloseReason string  `json:"close_reason,omitempty"`
}

// Summary is the aggregate traffic state in a snapshot.
type Summary struct {
	ActiveConnections int     `json:"active_connections"`
	RxBps             float64 `json:"rx_bps"`
	TxBps             float64 `json:"tx_bps"`
	RxTotal           uint64  `json:"rx_total"`
	TxTotal           uint64  `json:"tx_total"`
	HistoryLimit      int     `json:"history_limit"`
	HistoryPath       string  `json:"history_path,omitempty"`
	HistoryPersisted  bool    `json:"history_persisted"`
	PersistError      string  `json:"persist_error,omitempty"`
}

// Snapshot is returned by GET /api/v1/traffic.
type Snapshot struct {
	UpdatedTsNs        int64               `json:"updated_ts_ns"`
	Summary            Summary             `json:"summary"`
	Connections        []Connection        `json:"connections"`
	TemporaryRules     []temprules.Rule    `json:"temporary_rules,omitempty"`
	ProfileContext     ProfileContext      `json:"profile_context,omitempty"`
	QuickFilters       []QuickFilter       `json:"quick_filters,omitempty"`
	RuleHits           []RuleHit           `json:"rule_hits,omitempty"`
	BlockDecisions     []BlockDecision     `json:"block_decisions,omitempty"`
	DestinationGroups  []DestinationGroup  `json:"destination_groups,omitempty"`
	CleanupSuggestions []CleanupSuggestion `json:"cleanup_suggestions,omitempty"`
	RuleSuggestions    []RuleSuggestion    `json:"rule_suggestions,omitempty"`
	Breakdowns         TrafficBreakdowns   `json:"breakdowns,omitempty"`
}

// TrafficBreakdowns groups traffic counters for monitor summaries.
type TrafficBreakdowns struct {
	Profiles []BreakdownRow `json:"profiles,omitempty"`
	Chains   []BreakdownRow `json:"chains,omitempty"`
	Rules    []BreakdownRow `json:"rules,omitempty"`
	Actions  []BreakdownRow `json:"actions,omitempty"`
	Networks []BreakdownRow `json:"networks,omitempty"`
}

// BreakdownRow is one count/byte aggregate.
type BreakdownRow struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Count   int    `json:"count"`
	RxTotal uint64 `json:"rx_total"`
	TxTotal uint64 `json:"tx_total"`
}

// SnapshotOptions controls optional filtering and UI analytics.
type SnapshotOptions struct {
	State          string
	Limit          int
	Action         string
	Profile        string
	Rule           string
	Country        string
	Port           string
	Query          string
	ActiveProfile  string
	Profiles       []string
	Rules          []config.RuleConfig
	EffectiveRules []config.RuleConfig
	TemporaryRules []temprules.Rule
}

// RouteExplanation explains why a connection used its selected route.
type RouteExplanation struct {
	Source        string `json:"source,omitempty"`
	RuleName      string `json:"rule_name,omitempty"`
	RuleNumber    int    `json:"rule_number,omitempty"`
	MatcherKind   string `json:"matcher_kind,omitempty"`
	MatcherValue  string `json:"matcher_value,omitempty"`
	DefaultChain  string `json:"default_chain,omitempty"`
	PolicyGroup   string `json:"policy_group,omitempty"`
	SelectedChain string `json:"selected_chain,omitempty"`
	FinalChain    string `json:"final_chain,omitempty"`
	Summary       string `json:"summary,omitempty"`
}

// RouteControl is the API-ready normalized route-mode state for a connection.
type RouteControl struct {
	Mode            string `json:"mode,omitempty"`
	Decision        string `json:"decision,omitempty"`
	Source          string `json:"source,omitempty"`
	RuleName        string `json:"rule_name,omitempty"`
	RuleNumber      int    `json:"rule_number,omitempty"`
	PolicyGroup     string `json:"policy_group,omitempty"`
	SelectedChain   string `json:"selected_chain,omitempty"`
	SelectionReason string `json:"selection_reason,omitempty"`
	Fallback        bool   `json:"fallback,omitempty"`
	Default         bool   `json:"default,omitempty"`
}

// ProfileContext names the active profile and available profile choices.
type ProfileContext struct {
	Active   string   `json:"active,omitempty"`
	Profiles []string `json:"profiles,omitempty"`
}

// QuickFilter is a count-backed monitor filter token.
type QuickFilter struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// RuleHit summarizes history-derived rule usage.
type RuleHit struct {
	Profile     string `json:"profile,omitempty"`
	RuleName    string `json:"rule_name"`
	Action      string `json:"action"`
	Count       int    `json:"count"`
	LastHitTsNs int64  `json:"last_hit_ts_ns,omitempty"`
	RxTotal     uint64 `json:"rx_total"`
	TxTotal     uint64 `json:"tx_total"`
	LastTarget  string `json:"last_target,omitempty"`
	Default     bool   `json:"default,omitempty"`
	Temporary   bool   `json:"temporary,omitempty"`
}

// DestinationGroup summarizes repeated destinations under a normalized group
// key so noisy domains can be collapsed in activity views.
type DestinationGroup struct {
	Key           string   `json:"key"`
	Profile       string   `json:"profile,omitempty"`
	DisplayHost   string   `json:"display_host,omitempty"`
	DomainSuffix  string   `json:"domain_suffix,omitempty"`
	Count         int      `json:"count"`
	Actions       []string `json:"actions,omitempty"`
	Profiles      []string `json:"profiles,omitempty"`
	LastSeenTsNs  int64    `json:"last_seen_ts_ns,omitempty"`
	SampleTargets []string `json:"sample_targets,omitempty"`
	TopRuleName   string   `json:"top_rule_name,omitempty"`
	TopChainName  string   `json:"top_chain_name,omitempty"`
	RxTotal       uint64   `json:"rx_total"`
	TxTotal       uint64   `json:"tx_total"`
}

// BlockDecision is a compact recent block/reject decision row.
type BlockDecision struct {
	ConnID      string `json:"conn_id"`
	Profile     string `json:"profile,omitempty"`
	RuleName    string `json:"rule_name,omitempty"`
	Action      string `json:"action"`
	Target      string `json:"target,omitempty"`
	TargetHost  string `json:"target_host,omitempty"`
	TargetPort  string `json:"target_port,omitempty"`
	Network     string `json:"network,omitempty"`
	TsNs        int64  `json:"ts_ns"`
	CloseReason string `json:"close_reason,omitempty"`
}

// CleanupSuggestion describes a rule cleanup action the user can confirm.
type CleanupSuggestion struct {
	Kind           string `json:"kind"`
	Profile        string `json:"profile,omitempty"`
	RuleName       string `json:"rule_name"`
	TargetRuleName string `json:"target_rule_name,omitempty"`
	Operation      string `json:"operation,omitempty"`
	Action         string `json:"action,omitempty"`
	Message        string `json:"message"`
	Count          int    `json:"count,omitempty"`
	LastHitTsNs    int64  `json:"last_hit_ts_ns,omitempty"`
}

// RuleSuggestion is a draft allow/block rule derived from observed traffic.
// It is advisory: clients must ask the user before persisting DraftRule.
type RuleSuggestion struct {
	ID            string            `json:"id"`
	Kind          string            `json:"kind"`
	Profile       string            `json:"profile,omitempty"`
	Action        string            `json:"action"`
	DraftRule     config.RuleConfig `json:"draft_rule"`
	Count         int               `json:"count"`
	LastSeenTsNs  int64             `json:"last_seen_ts_ns,omitempty"`
	SampleTargets []string          `json:"sample_targets,omitempty"`
	Confidence    string            `json:"confidence,omitempty"`
	Reason        string            `json:"reason,omitempty"`
}

type historyFile struct {
	Version   int          `json:"version"`
	SavedTsNs int64        `json:"saved_ts_ns"`
	Closed    []Connection `json:"closed"`
}

// NewStore builds a traffic store from config. A disabled config returns nil.
func NewStore(cfg config.TrafficConfig, geoLookup GeoLookupFunc) (*Store, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	limit := cfg.HistoryLimit
	if limit <= 0 {
		limit = config.DefaultTrafficConfig().HistoryLimit
	}
	maxAge := cfg.HistoryMaxAge.Std()
	if maxAge <= 0 {
		maxAge = config.DefaultTrafficConfig().HistoryMaxAge.Std()
	}
	path := strings.TrimSpace(cfg.HistoryPath)
	if path == "" {
		path = defaultHistoryPath()
	}

	s := &Store{
		enabled:       true,
		historyLimit:  limit,
		historyMaxAge: maxAge,
		historyPath:   path,
		geoLookup:     geoLookup,
		active:        make(map[string]*Connection),
	}
	if err := s.loadHistory(); err != nil {
		s.persistErr = err.Error()
	}
	return s, nil
}

// Reconfigure updates bounded-history settings after config reload. A store
// that was never created because traffic started disabled cannot be enabled
// through this method; callers should construct a store at startup.
func (s *Store) Reconfigure(cfg config.TrafficConfig) error {
	if s == nil {
		return nil
	}
	limit := cfg.HistoryLimit
	if limit <= 0 {
		limit = config.DefaultTrafficConfig().HistoryLimit
	}
	maxAge := cfg.HistoryMaxAge.Std()
	if maxAge <= 0 {
		maxAge = config.DefaultTrafficConfig().HistoryMaxAge.Std()
	}
	path := strings.TrimSpace(cfg.HistoryPath)
	if path == "" {
		path = defaultHistoryPath()
	}

	var save []Connection
	var savePath string
	s.mu.Lock()
	s.enabled = cfg.Enabled
	s.historyLimit = limit
	s.historyMaxAge = maxAge
	s.historyPath = path
	if !cfg.Enabled {
		s.active = make(map[string]*Connection)
	}
	s.pruneClosedLocked(time.Now())
	if cfg.Enabled {
		save = cloneConnections(s.closed)
		savePath = s.historyPath
	}
	s.mu.Unlock()

	if save != nil {
		err := writeHistory(savePath, save)
		s.setPersistResult(err)
		return err
	}
	return nil
}

func defaultHistoryPath() string {
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		return filepath.Join(dir, "clambhook", "traffic-history.json")
	}
	return filepath.Join(os.TempDir(), "clambhook", "traffic-history.json")
}

// Start subscribes the store to the event bus until ctx is cancelled or the
// bus closes. It is safe to call with nil receiver or bus.
func (s *Store) Start(ctx context.Context, bus *events.Bus) {
	if s == nil || bus == nil {
		return
	}
	sub := bus.Subscribe(events.Filter{Types: []string{"connection.*", "hop.*", "rule.*"}})
	go func() {
		defer sub.Unsubscribe()
		for {
			select {
			case ev, ok := <-sub.Ch():
				if !ok {
					return
				}
				s.ApplyEvent(ev)
			case <-ctx.Done():
				return
			case <-sub.Context().Done():
				return
			}
		}
	}()
}

// ApplyEvent updates traffic state from one daemon event.
func (s *Store) ApplyEvent(ev events.Event) {
	if s == nil {
		return
	}
	if ev.TsNs == 0 {
		ev.TsNs = time.Now().UnixNano()
	}

	var save []Connection
	var path string
	s.mu.Lock()
	if !s.enabled {
		s.mu.Unlock()
		return
	}
	switch ev.Type {
	case events.TypeConnectionOpened:
		s.applyOpenedLocked(ev)
	case events.TypeConnectionDialing:
		s.applyDialingLocked(ev)
	case events.TypeConnectionVisibility:
		s.applyVisibilityLocked(ev)
	case events.TypeHopDialing:
		s.applyHopDialingLocked(ev)
	case events.TypeHopConnected:
		s.applyHopConnectedLocked(ev)
	case events.TypeHopError:
		s.applyHopErrorLocked(ev)
	case events.TypeConnectionEstablished:
		s.applyEstablishedLocked(ev)
	case events.TypeConnectionBytes:
		s.applyBytesLocked(ev)
	case events.TypeConnectionClosed:
		if s.applyClosedLocked(ev) {
			s.pruneClosedLocked(time.Now())
			save = cloneConnections(s.closed)
			path = s.historyPath
		}
	case events.TypeRuleMatched, events.TypeRuleDirect, events.TypeRuleBlocked:
		s.applyRuleDecisionLocked(ev)
	}
	s.mu.Unlock()

	if save != nil {
		err := writeHistory(path, save)
		s.setPersistResult(err)
	}
}

// Snapshot returns a consistent copy of the current traffic state.
func (s *Store) Snapshot(state string, limit int) Snapshot {
	return s.SnapshotWithOptions(SnapshotOptions{State: state, Limit: limit})
}

// Connection returns one active or recently closed connection by ID.
func (s *Store) Connection(connID string) (Connection, bool) {
	if s == nil {
		return Connection{}, false
	}
	connID = strings.TrimSpace(connID)
	if connID == "" {
		return Connection{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if conn, ok := s.active[connID]; ok {
		return cloneConnection(*conn), true
	}
	for _, conn := range s.closed {
		if conn.ConnID == connID {
			return cloneConnection(conn), true
		}
	}
	return Connection{}, false
}

// SnapshotWithOptions returns a consistent copy of traffic state, with
// optional filters and analytics for connection-monitor UIs.
func (s *Store) SnapshotWithOptions(opts SnapshotOptions) Snapshot {
	if s == nil {
		return Snapshot{
			UpdatedTsNs:    time.Now().UnixNano(),
			ProfileContext: ProfileContext{Active: opts.ActiveProfile, Profiles: append([]string(nil), opts.Profiles...)},
			Connections:    []Connection{},
		}
	}
	state := strings.ToLower(strings.TrimSpace(opts.State))
	if state == "" {
		state = "all"
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	active := make([]Connection, 0, len(s.active))
	var summary Summary
	summary.HistoryLimit = s.historyLimit
	summary.HistoryPath = s.historyPath
	summary.HistoryPersisted = s.enabled && s.historyPath != ""
	summary.PersistError = s.persistErr

	for _, conn := range s.active {
		c := cloneConnection(*conn)
		active = append(active, c)
		summary.ActiveConnections++
		summary.RxBps += c.RxBps
		summary.TxBps += c.TxBps
		summary.RxTotal += c.RxTotal
		summary.TxTotal += c.TxTotal
	}
	for _, conn := range s.closed {
		summary.RxTotal += conn.RxTotal
		summary.TxTotal += conn.TxTotal
	}

	sort.Slice(active, func(i, j int) bool {
		return active[i].UpdatedTsNs > active[j].UpdatedTsNs
	})

	var all []Connection
	if state == "all" || state == "active" {
		all = append(all, active...)
	}
	if state == "all" || state == "closed" {
		all = append(all, cloneConnections(s.closed)...)
	}
	filtered := filterConnections(all, opts)
	limited := filtered
	if opts.Limit > 0 && len(limited) > opts.Limit {
		limited = limited[:opts.Limit]
	}

	return Snapshot{
		UpdatedTsNs:        time.Now().UnixNano(),
		Summary:            summary,
		Connections:        limited,
		TemporaryRules:     append([]temprules.Rule(nil), opts.TemporaryRules...),
		ProfileContext:     ProfileContext{Active: opts.ActiveProfile, Profiles: append([]string(nil), opts.Profiles...)},
		QuickFilters:       buildQuickFilters(all),
		RuleHits:           buildRuleHits(all),
		BlockDecisions:     buildBlockDecisions(all, 12),
		DestinationGroups:  buildDestinationGroups(all, 12),
		CleanupSuggestions: buildCleanupSuggestions(opts.ActiveProfile, opts.Rules, all),
		RuleSuggestions:    buildRuleSuggestions(opts.ActiveProfile, suggestionCoverageRules(opts), all, 12),
		Breakdowns:         buildBreakdowns(all),
	}
}

func suggestionCoverageRules(opts SnapshotOptions) []config.RuleConfig {
	if len(opts.EffectiveRules) > 0 {
		return opts.EffectiveRules
	}
	return opts.Rules
}

func filterConnections(conns []Connection, opts SnapshotOptions) []Connection {
	action := strings.ToLower(strings.TrimSpace(opts.Action))
	profile := strings.TrimSpace(opts.Profile)
	rule := strings.TrimSpace(opts.Rule)
	country := strings.ToUpper(strings.TrimSpace(opts.Country))
	port := strings.TrimSpace(opts.Port)
	query := strings.ToLower(strings.TrimSpace(opts.Query))
	if action == "" && profile == "" && rule == "" && country == "" && port == "" && query == "" {
		return conns
	}
	out := make([]Connection, 0, len(conns))
	for _, conn := range conns {
		if action != "" && actionFamily(conn.RuleAction) != action {
			continue
		}
		if profile != "" && conn.Profile != profile {
			continue
		}
		if rule != "" {
			name := conn.RuleName
			if name == "" && conn.Default {
				name = "default"
			}
			if name != rule {
				continue
			}
		}
		if country != "" && strings.ToUpper(conn.Geo.CountryCode) != country {
			continue
		}
		if port != "" && conn.TargetPort != port {
			continue
		}
		if query != "" && !connectionMatchesQuery(conn, query) {
			continue
		}
		out = append(out, conn)
	}
	return out
}

func buildQuickFilters(conns []Connection) []QuickFilter {
	counts := map[string]int{
		"all":    len(conns),
		"active": 0,
		"proxy":  0,
		"direct": 0,
		"block":  0,
	}
	countries := map[string]int{}
	ports := map[string]int{}
	for _, conn := range conns {
		if conn.State == StateActive || conn.State == StateDialing || conn.State == StateOpening {
			counts["active"]++
		}
		counts[actionFamily(conn.RuleAction)]++
		if conn.Geo.CountryCode != "" {
			countries[strings.ToUpper(conn.Geo.CountryCode)]++
		}
		if conn.TargetPort != "" {
			ports[conn.TargetPort]++
		}
	}
	filters := []QuickFilter{
		{Key: "all", Label: "All", Count: counts["all"]},
		{Key: "active", Label: "Active", Count: counts["active"]},
		{Key: "proxy", Label: "Proxy", Count: counts["proxy"]},
		{Key: "direct", Label: "Direct", Count: counts["direct"]},
		{Key: "block", Label: "Block", Count: counts["block"]},
	}
	for _, row := range topCounts(countries, 3) {
		filters = append(filters, QuickFilter{Key: "country:" + row.Key, Label: row.Key, Count: row.Count})
	}
	for _, row := range topCounts(ports, 3) {
		filters = append(filters, QuickFilter{Key: "port:" + row.Key, Label: ":" + row.Key, Count: row.Count})
	}
	return filters
}

func buildBreakdowns(conns []Connection) TrafficBreakdowns {
	return TrafficBreakdowns{
		Profiles: breakdownRows(conns, func(c Connection) (string, string) {
			return valueOrDefault(c.Profile, "unknown"), valueOrDefault(c.Profile, "Unknown")
		}),
		Chains: breakdownRows(conns, func(c Connection) (string, string) {
			return valueOrDefault(c.ChainName, "direct"), valueOrDefault(c.ChainName, "Direct")
		}),
		Rules: breakdownRows(conns, func(c Connection) (string, string) {
			name := c.RuleName
			if name == "" && c.Default {
				name = "default"
			}
			return valueOrDefault(name, "none"), valueOrDefault(name, "None")
		}),
		Actions: breakdownRows(conns, func(c Connection) (string, string) {
			action := actionFamily(c.RuleAction)
			return action, titleLabel(action)
		}),
		Networks: breakdownRows(conns, func(c Connection) (string, string) {
			network := strings.ToLower(strings.TrimSpace(c.Network))
			if network == "" {
				network = "unknown"
			}
			return network, strings.ToUpper(network)
		}),
	}
}

func breakdownRows(conns []Connection, keyFn func(Connection) (string, string)) []BreakdownRow {
	index := map[string]*BreakdownRow{}
	for _, conn := range conns {
		key, label := keyFn(conn)
		if key == "" {
			key = "unknown"
		}
		if label == "" {
			label = key
		}
		row := index[key]
		if row == nil {
			row = &BreakdownRow{Key: key, Label: label}
			index[key] = row
		}
		row.Count++
		row.RxTotal += conn.RxTotal
		row.TxTotal += conn.TxTotal
	}
	out := make([]BreakdownRow, 0, len(index))
	for _, row := range index {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Label < out[j].Label
		}
		return out[i].Count > out[j].Count
	})
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func valueOrDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func titleLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func buildRuleHits(conns []Connection) []RuleHit {
	index := map[string]*RuleHit{}
	for _, conn := range conns {
		if conn.RuleName == "" && conn.RuleAction == "" && !conn.Default {
			continue
		}
		name := conn.RuleName
		if name == "" && conn.Default {
			name = "default"
		}
		action := actionFamily(conn.RuleAction)
		key := conn.Profile + "\x00" + name + "\x00" + action
		hit := index[key]
		if hit == nil {
			hit = &RuleHit{Profile: conn.Profile, RuleName: name, Action: action, Default: conn.Default, Temporary: conn.Explanation != nil && conn.Explanation.Source == "temporary_rule"}
			index[key] = hit
		}
		hit.Count++
		hit.RxTotal += conn.RxTotal
		hit.TxTotal += conn.TxTotal
		if conn.UpdatedTsNs > hit.LastHitTsNs {
			hit.LastHitTsNs = conn.UpdatedTsNs
			hit.LastTarget = conn.Target
		}
	}
	out := make([]RuleHit, 0, len(index))
	for _, hit := range index {
		out = append(out, *hit)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			if out[i].LastHitTsNs == out[j].LastHitTsNs {
				return out[i].RuleName < out[j].RuleName
			}
			return out[i].LastHitTsNs > out[j].LastHitTsNs
		}
		return out[i].Count > out[j].Count
	})
	return out
}

type destinationAccumulator struct {
	row      DestinationGroup
	actions  map[string]int
	profiles map[string]int
	rules    map[string]int
	chains   map[string]int
}

func buildDestinationGroups(conns []Connection, limit int) []DestinationGroup {
	index := map[string]*destinationAccumulator{}
	for _, conn := range conns {
		host := suggestionHost(conn)
		if host == "" {
			continue
		}
		key, suffix := destinationGroupKey(host)
		if key == "" {
			continue
		}
		acc := index[key]
		if acc == nil {
			acc = &destinationAccumulator{
				row: DestinationGroup{
					Key:          key,
					DisplayHost:  host,
					DomainSuffix: suffix,
				},
				actions:  map[string]int{},
				profiles: map[string]int{},
				rules:    map[string]int{},
				chains:   map[string]int{},
			}
			index[key] = acc
		}
		acc.row.Count++
		acc.row.RxTotal += conn.RxTotal
		acc.row.TxTotal += conn.TxTotal
		if conn.UpdatedTsNs > acc.row.LastSeenTsNs {
			acc.row.LastSeenTsNs = conn.UpdatedTsNs
			acc.row.DisplayHost = host
		}
		if conn.Target != "" && !containsString(acc.row.SampleTargets, conn.Target) {
			acc.row.SampleTargets = append(acc.row.SampleTargets, conn.Target)
			if len(acc.row.SampleTargets) > 3 {
				acc.row.SampleTargets = acc.row.SampleTargets[:3]
			}
		}
		action := actionFamily(conn.RuleAction)
		acc.actions[action]++
		if conn.Profile != "" {
			acc.profiles[conn.Profile]++
		}
		if conn.RuleName != "" {
			acc.rules[conn.RuleName]++
		}
		if conn.ChainName != "" {
			acc.chains[conn.ChainName]++
		}
	}
	out := make([]DestinationGroup, 0, len(index))
	for _, acc := range index {
		acc.row.Actions = sortedKeys(acc.actions)
		acc.row.Profiles = sortedKeys(acc.profiles)
		acc.row.TopRuleName = topCounterKey(acc.rules)
		acc.row.TopChainName = topCounterKey(acc.chains)
		if len(acc.row.Profiles) == 1 {
			acc.row.Profile = acc.row.Profiles[0]
		}
		out = append(out, acc.row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			if out[i].LastSeenTsNs == out[j].LastSeenTsNs {
				return out[i].Key < out[j].Key
			}
			return out[i].LastSeenTsNs > out[j].LastSeenTsNs
		}
		return out[i].Count > out[j].Count
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func destinationGroupKey(host string) (key, suffix string) {
	host = strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	if host == "" {
		return "", ""
	}
	if looksLikeIP(host) {
		return "ip:" + host, ""
	}
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return "host:" + host, host
	}
	suffix = strings.Join(parts[len(parts)-2:], ".")
	if broadSuffix(suffix) && len(parts) >= 3 {
		suffix = strings.Join(parts[len(parts)-3:], ".")
	}
	return "domain:" + suffix, suffix
}

func sortedKeys(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func topCounterKey(counts map[string]int) string {
	bestKey := ""
	bestCount := 0
	for key, count := range counts {
		if count > bestCount || (count == bestCount && (bestKey == "" || key < bestKey)) {
			bestKey = key
			bestCount = count
		}
	}
	return bestKey
}

func buildBlockDecisions(conns []Connection, limit int) []BlockDecision {
	out := make([]BlockDecision, 0)
	for _, conn := range conns {
		action := actionFamily(conn.RuleAction)
		if action != "block" {
			continue
		}
		out = append(out, BlockDecision{
			ConnID:      conn.ConnID,
			Profile:     conn.Profile,
			RuleName:    conn.RuleName,
			Action:      conn.RuleAction,
			Target:      conn.Target,
			TargetHost:  conn.TargetHost,
			TargetPort:  conn.TargetPort,
			Network:     conn.Network,
			TsNs:        conn.UpdatedTsNs,
			CloseReason: conn.CloseReason,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TsNs > out[j].TsNs })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func buildCleanupSuggestions(profile string, rules []config.RuleConfig, conns []Connection) []CleanupSuggestion {
	if len(rules) == 0 {
		return nil
	}
	hits := map[string]RuleHit{}
	for _, hit := range buildRuleHits(conns) {
		if profile != "" && hit.Profile != "" && hit.Profile != profile {
			continue
		}
		hits[hit.RuleName] = hit
	}
	out := make([]CleanupSuggestion, 0)
	seen := map[string]string{}
	exactDomains := map[string][]ruleShadowDescriptor{}
	for i, rule := range rules {
		name := strings.TrimSpace(rule.Name)
		action := strings.TrimSpace(rule.Action)
		if name == "" {
			name = "unnamed"
		}
		isFirstBroadRule := i == 0 && ruleHasNoMatchers(rule)
		if _, ok := hits[name]; !ok && !isFirstBroadRule {
			out = append(out, CleanupSuggestion{
				Kind:           "unused_in_history",
				Profile:        profile,
				RuleName:       name,
				TargetRuleName: name,
				Operation:      "delete_rule",
				Action:         action,
				Message:        "No recent traffic-history entries matched this rule.",
			})
		}
		key := ruleMatcherKey(rule)
		if key != "" {
			if prev, ok := seen[key]; ok {
				out = append(out, CleanupSuggestion{
					Kind:           "duplicate_matcher",
					Profile:        profile,
					RuleName:       name,
					TargetRuleName: name,
					Operation:      "delete_rule",
					Action:         action,
					Message:        fmt.Sprintf("Matches the same traffic as rule %q.", prev),
				})
			} else {
				seen[key] = name
			}
		}
		if !ruleHasNonExactDestinationMatchers(rule) {
			descriptor := ruleShadowDescriptor{
				name:     name,
				action:   normalizedRuleAction(rule),
				scopeKey: ruleNonDestinationScopeKey(rule),
			}
			for _, domain := range normalizeStrings(rule.Domains) {
				exactDomains[domain] = append(exactDomains[domain], descriptor)
			}
		}
		if !ruleHasNonSuffixDestinationMatchers(rule) {
			descriptor := ruleShadowDescriptor{
				name:     name,
				action:   normalizedRuleAction(rule),
				scopeKey: ruleNonDestinationScopeKey(rule),
			}
			for _, suffix := range normalizeStrings(rule.DomainSuffixes) {
				for domain, previous := range exactDomains {
					if domain != suffix && !strings.HasSuffix(domain, "."+suffix) {
						continue
					}
					if prev, ok := matchingShadowDescriptor(previous, descriptor); ok {
						out = append(out, CleanupSuggestion{
							Kind:           "shadowed_exact_match",
							Profile:        profile,
							RuleName:       name,
							TargetRuleName: prev.name,
							Operation:      "delete_rule",
							Action:         action,
							Message:        fmt.Sprintf("May make earlier exact-domain rule %q redundant.", prev.name),
						})
						break
					}
				}
			}
		}
		if isFirstBroadRule {
			out = append(out, CleanupSuggestion{
				Kind:           "broad_match",
				Profile:        profile,
				RuleName:       name,
				TargetRuleName: name,
				Operation:      "move_rule_to_end",
				Action:         action,
				Message:        "First rule has no matchers and may shadow every later rule.",
			})
		}
	}
	return out
}

type ruleShadowDescriptor struct {
	name     string
	action   string
	scopeKey string
}

func matchingShadowDescriptor(previous []ruleShadowDescriptor, current ruleShadowDescriptor) (ruleShadowDescriptor, bool) {
	for _, prev := range previous {
		if prev.name == current.name {
			continue
		}
		if prev.action == current.action && prev.scopeKey == current.scopeKey {
			return prev, true
		}
	}
	return ruleShadowDescriptor{}, false
}

type suggestionGroup struct {
	kind     string
	profile  string
	action   string
	value    string
	isIP     bool
	count    int
	hosts    map[string]struct{}
	ports    map[string]int
	networks map[string]int
	samples  []string
	lastTsNs int64
}

func buildRuleSuggestions(profile string, rules []config.RuleConfig, conns []Connection, limit int) []RuleSuggestion {
	if len(conns) == 0 {
		return nil
	}
	coverage := compileRuleCoverage(rules)
	exactGroups := map[string]*suggestionGroup{}
	suffixGroups := map[string]*suggestionGroup{}
	for _, conn := range conns {
		if profile != "" && conn.Profile != "" && conn.Profile != profile {
			continue
		}
		host := suggestionHost(conn)
		if host == "" {
			continue
		}
		action := suggestionAction(conn)
		if action == "" {
			continue
		}
		isIP := looksLikeIP(host)
		if coverage.coversHostAction(host, action, isIP) {
			continue
		}
		exactKey := strings.Join([]string{"exact_host", conn.Profile, action, host}, "\x00")
		addSuggestionSample(exactGroups, exactKey, "exact_host", conn.Profile, action, host, host, isIP, conn)

		if !isIP {
			suffix := suffixCandidate(host)
			if suffix != "" && suffix != host && !coverage.coversSuffixAction(suffix, action) {
				suffixKey := strings.Join([]string{"domain_suffix", conn.Profile, action, suffix}, "\x00")
				addSuggestionSample(suffixGroups, suffixKey, "domain_suffix", conn.Profile, action, suffix, host, false, conn)
			}
		}
	}

	out := make([]RuleSuggestion, 0, len(exactGroups)+len(suffixGroups))
	for _, group := range exactGroups {
		out = append(out, group.ruleSuggestion())
	}
	for _, group := range suffixGroups {
		if group.count < 3 || len(group.hosts) < 2 {
			continue
		}
		out = append(out, group.ruleSuggestion())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			if out[i].LastSeenTsNs == out[j].LastSeenTsNs {
				return out[i].ID < out[j].ID
			}
			return out[i].LastSeenTsNs > out[j].LastSeenTsNs
		}
		return out[i].Count > out[j].Count
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func addSuggestionSample(groups map[string]*suggestionGroup, key, kind, profile, action, value, host string, isIP bool, conn Connection) {
	group := groups[key]
	if group == nil {
		group = &suggestionGroup{
			kind:     kind,
			profile:  profile,
			action:   action,
			value:    value,
			isIP:     isIP,
			hosts:    map[string]struct{}{},
			ports:    map[string]int{},
			networks: map[string]int{},
		}
		groups[key] = group
	}
	group.count++
	group.hosts[host] = struct{}{}
	if conn.TargetPort != "" {
		group.ports[conn.TargetPort]++
	}
	if conn.Network != "" {
		group.networks[strings.ToLower(conn.Network)]++
	}
	if conn.UpdatedTsNs > group.lastTsNs {
		group.lastTsNs = conn.UpdatedTsNs
	}
	if conn.Target != "" && !containsString(group.samples, conn.Target) {
		group.samples = append(group.samples, conn.Target)
		if len(group.samples) > 3 {
			group.samples = group.samples[:3]
		}
	}
}

func (g *suggestionGroup) ruleSuggestion() RuleSuggestion {
	rule := config.RuleConfig{
		Name:   ruleNameForSuggestion(g.action, g.value),
		Action: g.action,
	}
	switch {
	case g.kind == "domain_suffix":
		rule.DomainSuffixes = []string{g.value}
	case g.isIP:
		if strings.Contains(g.value, ":") {
			rule.CIDRs = []string{g.value + "/128"}
		} else {
			rule.CIDRs = []string{g.value + "/32"}
		}
	default:
		rule.Domains = []string{g.value}
	}
	if only := onlyCounterKey(g.networks, g.count); only != "" {
		rule.Networks = []string{only}
	}
	if only := onlyCounterKey(g.ports, g.count); only != "" {
		if port, err := strconv.Atoi(only); err == nil && port > 0 && port <= 65535 {
			rule.Ports = []int{port}
		}
	}
	confidence := "medium"
	reason := fmt.Sprintf("Observed %d matching connections.", g.count)
	if g.kind == "domain_suffix" {
		confidence = "low"
		reason = fmt.Sprintf("Observed %d connections across %d subdomains.", g.count, len(g.hosts))
	} else if g.count >= 3 {
		confidence = "high"
	}
	return RuleSuggestion{
		ID:            strings.Join([]string{g.kind, g.action, g.value}, ":"),
		Kind:          g.kind,
		Profile:       g.profile,
		Action:        g.action,
		DraftRule:     rule,
		Count:         g.count,
		LastSeenTsNs:  g.lastTsNs,
		SampleTargets: append([]string(nil), g.samples...),
		Confidence:    confidence,
		Reason:        reason,
	}
}

type ruleCoverage struct {
	domains        map[string]map[string]struct{}
	domainSuffixes map[string]map[string]struct{}
	cidrs          map[string]map[string]struct{}
	broadActions   map[string]struct{}
}

func compileRuleCoverage(rules []config.RuleConfig) ruleCoverage {
	c := ruleCoverage{
		domains:        map[string]map[string]struct{}{},
		domainSuffixes: map[string]map[string]struct{}{},
		cidrs:          map[string]map[string]struct{}{},
		broadActions:   map[string]struct{}{},
	}
	for _, rule := range rules {
		action := strings.ToLower(strings.TrimSpace(rule.Action))
		if action == "" {
			continue
		}
		if ruleHasNoMatchers(rule) {
			c.broadActions[action] = struct{}{}
			continue
		}
		for _, domain := range normalizeStrings(rule.Domains) {
			addCoverage(c.domains, domain, action)
		}
		for _, suffix := range normalizeStrings(rule.DomainSuffixes) {
			addCoverage(c.domainSuffixes, suffix, action)
		}
		for _, cidr := range normalizeStrings(rule.CIDRs) {
			addCoverage(c.cidrs, cidr, action)
		}
	}
	return c
}

func addCoverage(index map[string]map[string]struct{}, key, action string) {
	actions := index[key]
	if actions == nil {
		actions = map[string]struct{}{}
		index[key] = actions
	}
	actions[action] = struct{}{}
}

func (c ruleCoverage) coversHostAction(host, action string, isIP bool) bool {
	if actionCovered(c.broadActions, action) {
		return true
	}
	if isIP {
		if strings.Contains(host, ":") && actionCovered(c.cidrs[host+"/128"], action) {
			return true
		}
		if !strings.Contains(host, ":") && actionCovered(c.cidrs[host+"/32"], action) {
			return true
		}
		return false
	}
	if actionCovered(c.domains[host], action) {
		return true
	}
	return c.coversSuffixAction(host, action)
}

func (c ruleCoverage) coversSuffixAction(suffix, action string) bool {
	if actionCovered(c.broadActions, action) {
		return true
	}
	if actionCovered(c.domainSuffixes[suffix], action) {
		return true
	}
	for i := strings.IndexByte(suffix, '.'); i >= 0 && i < len(suffix)-1; {
		parent := suffix[i+1:]
		if actionCovered(c.domainSuffixes[parent], action) {
			return true
		}
		next := strings.IndexByte(suffix[i+1:], '.')
		if next < 0 {
			break
		}
		i += next + 1
	}
	return false
}

func actionCovered(actions map[string]struct{}, action string) bool {
	if len(actions) == 0 {
		return false
	}
	if _, ok := actions[action]; ok {
		return true
	}
	switch actionFamily(action) {
	case "proxy":
		_, ok := actions["chain"]
		return ok
	case "block":
		if _, ok := actions["block"]; ok {
			return true
		}
		_, ok := actions["reject"]
		return ok
	default:
		return false
	}
}

func suggestionHost(conn Connection) string {
	host := conn.TargetHost
	if host == "" && conn.Visibility != nil {
		host = conn.Visibility.Host
	}
	if host == "" {
		host, _ = splitTarget(conn.Target)
	}
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	host = strings.TrimSuffix(host, ".")
	return strings.ToLower(host)
}

func suggestionAction(conn Connection) string {
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

func suffixCandidate(host string) string {
	parts := strings.Split(host, ".")
	if len(parts) < 3 {
		return ""
	}
	candidate := strings.Join(parts[len(parts)-2:], ".")
	if broadSuffix(candidate) {
		return ""
	}
	return candidate
}

func broadSuffix(suffix string) bool {
	switch suffix {
	case "co.uk", "com.au", "co.jp", "com.br", "com.cn", "com.sg", "co.nz":
		return true
	default:
		return false
	}
}

func looksLikeIP(host string) bool {
	return net.ParseIP(strings.Trim(host, "[]")) != nil
}

func onlyCounterKey(counts map[string]int, total int) string {
	if len(counts) != 1 {
		return ""
	}
	for key, count := range counts {
		if count == total {
			return key
		}
	}
	return ""
}

func ruleNameForSuggestion(action, value string) string {
	family := actionFamily(action)
	token := strings.ToLower(strings.Trim(value, "[]"))
	replacer := strings.NewReplacer(".", "-", ":", "-", "_", "-", " ", "-")
	token = strings.Trim(replacer.Replace(token), "-")
	if token == "" {
		token = "connection"
	}
	return family + "-" + token
}

func containsString(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

type countRow struct {
	Key   string
	Count int
}

func topCounts(counts map[string]int, limit int) []countRow {
	rows := make([]countRow, 0, len(counts))
	for key, count := range counts {
		rows = append(rows, countRow{Key: key, Count: count})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count == rows[j].Count {
			return rows[i].Key < rows[j].Key
		}
		return rows[i].Count > rows[j].Count
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

func actionFamily(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "direct":
		return "direct"
	case "block", "reject":
		return "block"
	default:
		return "proxy"
	}
}

func connectionMatchesQuery(conn Connection, query string) bool {
	fields := []string{
		conn.Profile, conn.Target, conn.TargetHost, conn.TargetPort, conn.RuleName,
		conn.RuleAction, conn.ChainName, conn.GroupName, conn.Application, conn.Network,
		conn.Listener.Protocol, conn.Listener.Addr, conn.ClientAddr,
		conn.Geo.Country, conn.Geo.CountryCode, conn.Geo.City, conn.CloseReason,
	}
	if conn.Visibility != nil {
		fields = append(fields, conn.Visibility.Kind, conn.Visibility.Method, conn.Visibility.Host, conn.Visibility.Port, conn.Visibility.Path, conn.Visibility.QueryType)
	}
	for _, hop := range conn.Hops {
		fields = append(fields, hop.Name, hop.Protocol, hop.Address, hop.State, hop.Error)
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}

func ruleMatcherKey(rule config.RuleConfig) string {
	parts := []string{
		strings.ToLower(strings.TrimSpace(rule.Action)),
		"rule_sets=" + strings.Join(normalizeStrings(rule.RuleSets), ","),
		"domains=" + strings.Join(normalizeStrings(rule.Domains), ","),
		"suffixes=" + strings.Join(normalizeStrings(rule.DomainSuffixes), ","),
		"keywords=" + strings.Join(normalizeStrings(rule.DomainKeywords), ","),
		"cidrs=" + strings.Join(normalizeStrings(rule.CIDRs), ","),
		"source_cidrs=" + strings.Join(normalizeStrings(rule.SourceCIDRs), ","),
		"networks=" + strings.Join(normalizeStrings(rule.Networks), ","),
	}
	ports := make([]string, 0, len(rule.Ports))
	for _, port := range rule.Ports {
		ports = append(ports, strconv.Itoa(port))
	}
	sort.Strings(ports)
	parts = append(parts, "ports="+strings.Join(ports, ","))
	key := strings.Join(parts, "|")
	if ruleHasNoMatchers(rule) {
		return ""
	}
	return key
}

func normalizedRuleAction(rule config.RuleConfig) string {
	return strings.ToLower(strings.TrimSpace(rule.Action))
}

func ruleNonDestinationScopeKey(rule config.RuleConfig) string {
	parts := []string{
		"source_cidrs=" + strings.Join(normalizeStrings(rule.SourceCIDRs), ","),
		"networks=" + strings.Join(normalizeStrings(rule.Networks), ","),
	}
	ports := make([]string, 0, len(rule.Ports))
	for _, port := range rule.Ports {
		ports = append(ports, strconv.Itoa(port))
	}
	sort.Strings(ports)
	parts = append(parts, "ports="+strings.Join(ports, ","))
	return strings.Join(parts, "|")
}

func ruleHasNonExactDestinationMatchers(rule config.RuleConfig) bool {
	return len(rule.DomainSuffixes) > 0 ||
		len(rule.DomainKeywords) > 0 ||
		len(rule.CIDRs) > 0 ||
		len(rule.RuleSets) > 0
}

func ruleHasNonSuffixDestinationMatchers(rule config.RuleConfig) bool {
	return len(rule.Domains) > 0 ||
		len(rule.DomainKeywords) > 0 ||
		len(rule.CIDRs) > 0 ||
		len(rule.RuleSets) > 0
}

func ruleHasNoMatchers(rule config.RuleConfig) bool {
	return len(rule.Domains) == 0 &&
		len(rule.DomainSuffixes) == 0 &&
		len(rule.DomainKeywords) == 0 &&
		len(rule.CIDRs) == 0 &&
		len(rule.RuleSets) == 0 &&
		len(rule.SourceCIDRs) == 0 &&
		len(rule.Ports) == 0 &&
		len(rule.Networks) == 0
}

func normalizeStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Store) setPersistResult(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.persistErr = err.Error()
		return
	}
	s.persistErr = ""
	s.lastSavedNs = time.Now().UnixNano()
}

func (s *Store) applyOpenedLocked(ev events.Event) {
	data, ok := ev.Data.(events.ConnectionOpenedData)
	if !ok {
		return
	}
	s.active[data.ConnID] = &Connection{
		ConnID:      data.ConnID,
		Profile:     data.Profile,
		State:       StateOpening,
		StartTsNs:   ev.TsNs,
		UpdatedTsNs: ev.TsNs,
		Listener: Listener{
			Protocol: data.Listener.Protocol,
			Addr:     data.Listener.Addr,
		},
		ClientAddr: data.ClientAddr,
		ChainName:  data.ChainName,
	}
	c := s.active[data.ConnID]
	addTimeline(c, ev, "Opened", strings.TrimSpace(data.Listener.Protocol+" "+data.ClientAddr))
}

func (s *Store) applyDialingLocked(ev events.Event) {
	data, ok := ev.Data.(events.ConnectionDialingData)
	if !ok {
		return
	}
	c := s.ensureConnLocked(data.ConnID, ev.TsNs)
	c.State = StateDialing
	if data.Profile != "" {
		c.Profile = data.Profile
	}
	c.Target = data.Target
	c.Network = data.Network
	c.TargetHost = data.TargetHost
	c.TargetPort = data.TargetPort
	c.Application = data.Application
	c.Source = data.Source
	c.RuleName = data.RuleName
	c.RuleAction = data.RuleAction
	c.GroupName = data.GroupName
	c.Default = data.Default
	c.DecisionNs = data.DecisionNs
	applyExplanation(c, data.Explanation)
	applyRouteControl(c, data.RouteControl)
	applyVisibility(c, data.Visibility)
	if data.ChainName != "" {
		c.ChainName = data.ChainName
	}
	if data.GroupName != "" {
		c.GroupName = data.GroupName
	}
	if c.TargetHost == "" || c.TargetPort == "" {
		host, port := splitTarget(data.Target)
		if c.TargetHost == "" {
			c.TargetHost = host
		}
		if c.TargetPort == "" {
			c.TargetPort = port
		}
	}
	if c.Application == "" {
		c.Application = inferApplication(c.Network, c.TargetHost, c.TargetPort)
	}
	c.Hops = make([]Hop, 0, len(data.Hops))
	for _, hop := range data.Hops {
		c.Hops = append(c.Hops, Hop{
			Index:    hop.Index,
			Name:     hop.Name,
			Protocol: hop.Protocol,
			Address:  hop.Address,
			State:    "pending",
		})
	}
	if s.geoLookup != nil && c.Target != "" {
		loc, err := s.geoLookup(c.Target)
		if err != nil {
			c.Geo = geo.Location{}
			c.GeoError = err.Error()
		} else if loc != nil {
			c.Geo = *loc
			c.GeoError = ""
		}
	}
	c.UpdatedTsNs = ev.TsNs
	addTimeline(c, ev, "Dialing", c.Target)
}

func (s *Store) applyRuleDecisionLocked(ev events.Event) {
	data, ok := ev.Data.(events.RuleDecisionData)
	if !ok {
		return
	}
	c := s.ensureConnLocked(data.ConnID, ev.TsNs)
	c.RuleName = data.RuleName
	c.RuleAction = data.Action
	c.GroupName = data.GroupName
	c.Default = data.Default
	c.DecisionNs = data.ElapsedNs
	applyExplanation(c, data.Explanation)
	applyRouteControl(c, data.RouteControl)
	if data.Profile != "" {
		c.Profile = data.Profile
	}
	if data.ChainName != "" {
		c.ChainName = data.ChainName
	}
	if c.Target == "" {
		c.Target = data.Target
	}
	if c.TargetHost == "" {
		c.TargetHost = data.TargetHost
	}
	if c.TargetPort == "" {
		c.TargetPort = data.TargetPort
	}
	if c.Network == "" {
		c.Network = data.Network
	}
	if c.Source == "" {
		c.Source = data.Source
	}
	c.UpdatedTsNs = ev.TsNs
	addTimeline(c, ev, "Decision", decisionDetail(c.RuleName, c.RuleAction, c.ChainName))
}

func (s *Store) applyHopDialingLocked(ev events.Event) {
	data, ok := ev.Data.(events.HopDialingData)
	if !ok {
		return
	}
	c := s.ensureConnLocked(data.ConnID, ev.TsNs)
	h := c.ensureHop(data.HopIndex)
	h.Name = data.HopName
	h.Protocol = data.Protocol
	h.Address = data.Address
	h.State = "dialing"
	h.Error = ""
	c.UpdatedTsNs = ev.TsNs
	addTimeline(c, ev, "Hop Dialing", hopDetail(h))
}

func (s *Store) applyHopConnectedLocked(ev events.Event) {
	data, ok := ev.Data.(events.HopConnectedData)
	if !ok {
		return
	}
	c := s.ensureConnLocked(data.ConnID, ev.TsNs)
	h := c.ensureHop(data.HopIndex)
	h.State = "connected"
	h.ElapsedNs = data.ElapsedNs
	h.Error = ""
	c.UpdatedTsNs = ev.TsNs
	addTimeline(c, ev, "Hop Connected", hopDetail(h))
}

func (s *Store) applyHopErrorLocked(ev events.Event) {
	data, ok := ev.Data.(events.HopErrorData)
	if !ok {
		return
	}
	c := s.ensureConnLocked(data.ConnID, ev.TsNs)
	h := c.ensureHop(data.HopIndex)
	h.State = "error"
	h.Error = data.Error
	c.UpdatedTsNs = ev.TsNs
	addTimeline(c, ev, "Hop Error", hopDetail(h))
}

func (s *Store) applyEstablishedLocked(ev events.Event) {
	data, ok := ev.Data.(events.ConnectionEstablishedData)
	if !ok {
		return
	}
	c := s.ensureConnLocked(data.ConnID, ev.TsNs)
	c.State = StateActive
	c.TotalDialNs = data.TotalDialNs
	c.UpdatedTsNs = ev.TsNs
	addTimeline(c, ev, "Connected", formatNs(data.TotalDialNs))
}

func (s *Store) applyBytesLocked(ev events.Event) {
	data, ok := ev.Data.(events.ConnectionBytesData)
	if !ok {
		return
	}
	c := s.ensureConnLocked(data.ConnID, ev.TsNs)
	if c.State == StateOpening || c.State == StateDialing {
		c.State = StateActive
	}
	c.RxTotal = data.RxTotal
	c.TxTotal = data.TxTotal
	if data.IntervalNs > 0 {
		seconds := float64(data.IntervalNs) / float64(time.Second)
		c.RxBps = float64(data.RxDelta) / seconds
		c.TxBps = float64(data.TxDelta) / seconds
	}
	c.UpdatedTsNs = ev.TsNs
}

func (s *Store) applyClosedLocked(ev events.Event) bool {
	data, ok := ev.Data.(events.ConnectionClosedData)
	if !ok {
		return false
	}
	c := s.ensureConnLocked(data.ConnID, ev.TsNs)
	c.State = StateClosed
	c.EndTsNs = ev.TsNs
	c.UpdatedTsNs = ev.TsNs
	c.RxBps = 0
	c.TxBps = 0
	c.RxTotal = data.RxTotal
	c.TxTotal = data.TxTotal
	c.DurationNs = data.DurationNs
	c.CloseReason = data.Reason

	delete(s.active, data.ConnID)
	addTimeline(c, ev, "Closed", data.Reason)
	s.closed = append([]Connection{cloneConnection(*c)}, s.closed...)
	return true
}

func (s *Store) applyVisibilityLocked(ev events.Event) {
	data, ok := ev.Data.(events.ConnectionVisibilityData)
	if !ok {
		return
	}
	c := s.ensureConnLocked(data.ConnID, ev.TsNs)
	applyVisibility(c, data.Visibility)
	c.UpdatedTsNs = ev.TsNs
	addTimeline(c, ev, "Visibility", visibilityDetail(c.Visibility))
}

func (s *Store) ensureConnLocked(connID string, tsNs int64) *Connection {
	if c, ok := s.active[connID]; ok {
		return c
	}
	c := &Connection{
		ConnID:      connID,
		State:       StateOpening,
		StartTsNs:   tsNs,
		UpdatedTsNs: tsNs,
	}
	s.active[connID] = c
	return c
}

func (c *Connection) ensureHop(index int) *Hop {
	for i := range c.Hops {
		if c.Hops[i].Index == index {
			return &c.Hops[i]
		}
	}
	c.Hops = append(c.Hops, Hop{Index: index})
	sort.Slice(c.Hops, func(i, j int) bool {
		return c.Hops[i].Index < c.Hops[j].Index
	})
	for i := range c.Hops {
		if c.Hops[i].Index == index {
			return &c.Hops[i]
		}
	}
	return &c.Hops[len(c.Hops)-1]
}

func (s *Store) pruneClosedLocked(now time.Time) {
	if len(s.closed) == 0 {
		return
	}
	cutoff := now.Add(-s.historyMaxAge).UnixNano()
	pruned := s.closed[:0]
	for _, conn := range s.closed {
		if s.historyLimit > 0 && len(pruned) >= s.historyLimit {
			continue
		}
		if conn.EndTsNs > 0 && conn.EndTsNs < cutoff {
			continue
		}
		pruned = append(pruned, conn)
	}
	s.closed = pruned
}

func (s *Store) loadHistory() error {
	data, err := os.ReadFile(s.historyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read traffic history: %w", err)
	}
	var file historyFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("parse traffic history: %w", err)
	}
	if file.Version != historyVersion {
		return fmt.Errorf("traffic history version %d is not supported", file.Version)
	}
	s.closed = cloneConnections(file.Closed)
	for i := range s.closed {
		s.closed[i].State = StateClosed
	}
	s.pruneClosedLocked(time.Now())
	return nil
}

func writeHistory(path string, closed []Connection) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create traffic history dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".traffic-*.tmp")
	if err != nil {
		return fmt.Errorf("create traffic history temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod traffic history temp: %w", err)
	}
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(historyFile{
		Version:   historyVersion,
		SavedTsNs: time.Now().UnixNano(),
		Closed:    closed,
	}); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write traffic history: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close traffic history: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace traffic history: %w", err)
	}
	return nil
}

func cloneConnections(in []Connection) []Connection {
	if len(in) == 0 {
		return nil
	}
	out := make([]Connection, len(in))
	for i := range in {
		out[i] = cloneConnection(in[i])
	}
	return out
}

func cloneConnection(in Connection) Connection {
	if len(in.Hops) > 0 {
		in.Hops = append([]Hop(nil), in.Hops...)
	}
	if len(in.Timeline) > 0 {
		in.Timeline = append([]TimelineEvent(nil), in.Timeline...)
	}
	if in.Visibility != nil {
		visibility := *in.Visibility
		in.Visibility = &visibility
	}
	if in.Explanation != nil {
		explanation := *in.Explanation
		in.Explanation = &explanation
	}
	if in.RouteControl != nil {
		routeControl := *in.RouteControl
		in.RouteControl = &routeControl
	}
	return in
}

func addTimeline(c *Connection, ev events.Event, title, detail string) {
	if c == nil {
		return
	}
	c.Timeline = append(c.Timeline, TimelineEvent{
		TsNs:   ev.TsNs,
		Type:   ev.Type,
		Title:  title,
		Detail: detail,
	})
}

func applyVisibility(c *Connection, info events.VisibilityInfo) {
	if c == nil || (info.Kind == "" && info.Method == "" && info.Scheme == "" && info.Host == "" && info.Port == "" && info.Path == "" && info.QueryType == "") {
		return
	}
	visibility := &Visibility{
		Kind:      info.Kind,
		Method:    info.Method,
		Scheme:    info.Scheme,
		Host:      info.Host,
		Port:      info.Port,
		Path:      info.Path,
		QueryType: info.QueryType,
	}
	c.Visibility = visibility
	if c.Application == "" {
		switch visibility.Kind {
		case "dns":
			c.Application = "DNS"
		case "http":
			c.Application = "HTTP"
		case "http_connect":
			c.Application = "HTTPS"
		}
	}
}

func applyRouteControl(c *Connection, info events.RouteControl) {
	if c == nil || (info.Mode == "" && info.Decision == "" && info.Source == "" && info.RuleName == "" && info.RuleNumber == 0 && info.PolicyGroup == "" && info.SelectedChain == "" && info.SelectionReason == "" && !info.Fallback && !info.Default) {
		return
	}
	c.RouteControl = &RouteControl{
		Mode:            info.Mode,
		Decision:        info.Decision,
		Source:          info.Source,
		RuleName:        info.RuleName,
		RuleNumber:      info.RuleNumber,
		PolicyGroup:     info.PolicyGroup,
		SelectedChain:   info.SelectedChain,
		SelectionReason: info.SelectionReason,
		Fallback:        info.Fallback,
		Default:         info.Default,
	}
}

func applyExplanation(c *Connection, info events.RouteExplanation) {
	if c == nil || (info.Source == "" && info.RuleName == "" && info.RuleNumber == 0 && info.MatcherKind == "" && info.MatcherValue == "" && info.DefaultChain == "" && info.PolicyGroup == "" && info.SelectedChain == "" && info.FinalChain == "" && info.Summary == "") {
		return
	}
	c.Explanation = &RouteExplanation{
		Source:        info.Source,
		RuleName:      info.RuleName,
		RuleNumber:    info.RuleNumber,
		MatcherKind:   info.MatcherKind,
		MatcherValue:  info.MatcherValue,
		DefaultChain:  info.DefaultChain,
		PolicyGroup:   info.PolicyGroup,
		SelectedChain: info.SelectedChain,
		FinalChain:    info.FinalChain,
		Summary:       info.Summary,
	}
}

func decisionDetail(ruleName, action, chainName string) string {
	parts := []string{}
	if action != "" {
		parts = append(parts, action)
	}
	if ruleName != "" {
		parts = append(parts, ruleName)
	}
	if chainName != "" {
		parts = append(parts, chainName)
	}
	return strings.Join(parts, " / ")
}

func hopDetail(h *Hop) string {
	if h == nil {
		return ""
	}
	parts := []string{h.Name, h.Protocol, h.Address}
	if h.ElapsedNs > 0 {
		parts = append(parts, formatNs(h.ElapsedNs))
	}
	if h.Error != "" {
		parts = append(parts, h.Error)
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, " / ")
}

func visibilityDetail(v *Visibility) string {
	if v == nil {
		return ""
	}
	parts := []string{v.Kind, v.Method, v.Host}
	if v.Path != "" {
		parts = append(parts, v.Path)
	}
	if v.QueryType != "" {
		parts = append(parts, v.QueryType)
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, " / ")
}

func formatNs(ns int64) string {
	if ns <= 0 {
		return ""
	}
	if ns < int64(time.Second) {
		return fmt.Sprintf("%dms", ns/int64(time.Millisecond))
	}
	return (time.Duration(ns)).String()
}

func splitTarget(target string) (host, port string) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", ""
	}
	if h, p, err := net.SplitHostPort(target); err == nil {
		return strings.Trim(h, "[]"), p
	}
	if i := strings.LastIndexByte(target, ':'); i > 0 && i < len(target)-1 {
		candidate := target[i+1:]
		if _, err := strconv.Atoi(candidate); err == nil {
			return strings.Trim(target[:i], "[]"), candidate
		}
	}
	return strings.Trim(target, "[]"), ""
}

func inferApplication(network, host, port string) string {
	switch port {
	case "20", "21":
		return "FTP"
	case "22":
		return "SSH"
	case "25", "465", "587":
		return "SMTP"
	case "53":
		return "DNS"
	case "80", "8080":
		return "HTTP"
	case "110", "995":
		return "POP3"
	case "123":
		return "NTP"
	case "143", "993":
		return "IMAP"
	case "443", "8443":
		return "HTTPS"
	case "853":
		return "DNS over TLS"
	}
	if strings.HasPrefix(strings.ToLower(host), "www.") && port == "" {
		return "Web"
	}
	if network != "" {
		return strings.ToUpper(network)
	}
	return ""
}
