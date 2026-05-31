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
	ConnID      string          `json:"conn_id"`
	State       string          `json:"state"`
	StartTsNs   int64           `json:"start_ts_ns"`
	UpdatedTsNs int64           `json:"updated_ts_ns"`
	EndTsNs     int64           `json:"end_ts_ns,omitempty"`
	Listener    Listener        `json:"listener"`
	ClientAddr  string          `json:"client_addr,omitempty"`
	ChainName   string          `json:"chain_name,omitempty"`
	RuleName    string          `json:"rule_name,omitempty"`
	RuleAction  string          `json:"rule_action,omitempty"`
	DecisionNs  int64           `json:"decision_ns,omitempty"`
	Target      string          `json:"target,omitempty"`
	TargetHost  string          `json:"target_host,omitempty"`
	TargetPort  string          `json:"target_port,omitempty"`
	Network     string          `json:"network,omitempty"`
	Application string          `json:"application,omitempty"`
	Hops        []Hop           `json:"hops,omitempty"`
	Timeline    []TimelineEvent `json:"timeline,omitempty"`
	Visibility  *Visibility     `json:"visibility,omitempty"`
	Geo         geo.Location    `json:"geo"`
	GeoError    string          `json:"geo_error,omitempty"`

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
	UpdatedTsNs int64        `json:"updated_ts_ns"`
	Summary     Summary      `json:"summary"`
	Connections []Connection `json:"connections"`
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
	if s == nil {
		return Snapshot{UpdatedTsNs: time.Now().UnixNano()}
	}
	state = strings.ToLower(strings.TrimSpace(state))
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

	var conns []Connection
	if state == "all" || state == "active" {
		conns = append(conns, active...)
	}
	if state == "all" || state == "closed" {
		conns = append(conns, cloneConnections(s.closed)...)
	}
	if limit > 0 && len(conns) > limit {
		conns = conns[:limit]
	}

	return Snapshot{
		UpdatedTsNs: time.Now().UnixNano(),
		Summary:     summary,
		Connections: conns,
	}
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
	c.Target = data.Target
	c.Network = data.Network
	c.TargetHost = data.TargetHost
	c.TargetPort = data.TargetPort
	c.Application = data.Application
	c.RuleName = data.RuleName
	c.RuleAction = data.RuleAction
	c.DecisionNs = data.DecisionNs
	applyVisibility(c, data.Visibility)
	if data.ChainName != "" {
		c.ChainName = data.ChainName
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
	c.DecisionNs = data.ElapsedNs
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
