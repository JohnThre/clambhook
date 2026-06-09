package policy

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JohnThre/clambhook/internal/chain"
	"github.com/JohnThre/clambhook/internal/config"
)

const (
	TypeURLTest     = "url-test"
	TypeSelect      = "select"
	TypeFallback    = "fallback"
	TypeLoadBalance = "load-balance"
	TypeSmart       = "smart"
	DefaultTestURL  = "https://www.gstatic.com/generate_204"
)

const (
	defaultInterval = 30 * time.Second
	defaultTimeout  = 5 * time.Second
	smartMinDelta   = 50 * time.Millisecond
)

// SelectionContext carries route-specific inputs used by policy-group types
// that need deterministic per-flow choices.
type SelectionContext struct {
	Network string
	Target  string
	Source  string
}

// ProbeFunc measures one chain against one URL.
type ProbeFunc func(context.Context, *chain.Chain, string) ProbeResult

// Option customizes a Manager. Tests use this to inject deterministic probes.
type Option func(*Manager)

// WithProbeFunc replaces the production HTTP probe implementation.
func WithProbeFunc(fn ProbeFunc) Option {
	return func(m *Manager) {
		if fn != nil {
			m.probe = fn
		}
	}
}

// ProbeResult is the latest latency-test result for one chain.
type ProbeResult struct {
	ChainName    string `json:"chain_name"`
	Healthy      bool   `json:"healthy"`
	LatencyNs    int64  `json:"latency_ns,omitempty"`
	StatusCode   int    `json:"status_code,omitempty"`
	Error        string `json:"error,omitempty"`
	LastTestTsNs int64  `json:"last_test_ts_ns,omitempty"`
	UDPCapable   bool   `json:"udp_capable"`
	UDPError     string `json:"udp_error,omitempty"`
}

// GroupSnapshot exposes the runtime state for one smart policy group.
type GroupSnapshot struct {
	Name            string        `json:"name"`
	Type            string        `json:"type"`
	Chains          []string      `json:"chains"`
	Selected        string        `json:"selected,omitempty"`
	Hidden          bool          `json:"hidden,omitempty"`
	TestURL         string        `json:"test_url"`
	Interval        string        `json:"interval"`
	Timeout         string        `json:"timeout"`
	SelectedChain   string        `json:"selected_chain,omitempty"`
	SelectionMode   string        `json:"selection_mode,omitempty"`
	SelectionReason string        `json:"selection_reason,omitempty"`
	UpdatedTsNs     int64         `json:"updated_ts_ns,omitempty"`
	Results         []ProbeResult `json:"results"`
}

// Snapshot is the API-ready policy group state for a profile.
type Snapshot struct {
	Profile string          `json:"profile"`
	Groups  []GroupSnapshot `json:"groups"`
}

// Manager owns smart policy group probe state and chain selection.
type Manager struct {
	mu      sync.RWMutex
	groups  map[string]*groupState
	order   []string
	probe   ProbeFunc
	cancel  context.CancelFunc
	started bool
	wg      sync.WaitGroup
}

type groupState struct {
	name            string
	groupType       string
	chainNames      []string
	chains          map[string]*chain.Chain
	udpCapable      map[string]bool
	udpErrors       map[string]string
	testURL         string
	interval        time.Duration
	timeout         time.Duration
	hidden          bool
	results         map[string]ProbeResult
	selectedChain   string
	selectionReason string
	updatedTsNs     int64
}

// New builds a policy manager from profile policy groups and runtime chains.
func New(groups []config.PolicyGroupConfig, chains map[string]*chain.Chain, opts ...Option) (*Manager, error) {
	m := &Manager{
		groups: make(map[string]*groupState, len(groups)),
		order:  make([]string, 0, len(groups)),
		probe:  defaultProbe,
	}
	for _, opt := range opts {
		opt(m)
	}
	for _, cfg := range groups {
		gs, err := newGroupState(cfg, chains)
		if err != nil {
			return nil, err
		}
		m.groups[gs.name] = gs
		m.order = append(m.order, gs.name)
	}
	return m, nil
}

func newGroupState(cfg config.PolicyGroupConfig, chains map[string]*chain.Chain) (*groupState, error) {
	groupType := strings.ToLower(strings.TrimSpace(cfg.Type))
	if groupType == "" {
		groupType = TypeURLTest
	}
	if !isSupportedGroupType(groupType) {
		return nil, fmt.Errorf("policy group %q: unsupported type %q", cfg.Name, cfg.Type)
	}
	testURL := strings.TrimSpace(cfg.TestURL)
	if testURL == "" {
		testURL = DefaultTestURL
	}
	if groupType == TypeURLTest {
		if parsed, err := url.Parse(testURL); err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, fmt.Errorf("policy group %q: invalid test_url %q", cfg.Name, testURL)
		}
	}
	selected := strings.TrimSpace(cfg.Selected)
	if selected == "" {
		selected = firstString(cfg.Chains)
	}
	if selected != "" && !containsString(cfg.Chains, selected) {
		return nil, fmt.Errorf("policy group %q: selected chain %q is not a member", cfg.Name, selected)
	}
	if groupType == TypeSelect && selected == "" {
		return nil, fmt.Errorf("policy group %q: select group requires at least one chain", cfg.Name)
	}
	interval := cfg.Interval.Std()
	if interval <= 0 {
		interval = defaultInterval
	}
	timeout := cfg.Timeout.Std()
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	gs := &groupState{
		name:            cfg.Name,
		groupType:       groupType,
		chainNames:      append([]string(nil), cfg.Chains...),
		chains:          make(map[string]*chain.Chain, len(cfg.Chains)),
		udpCapable:      make(map[string]bool, len(cfg.Chains)),
		udpErrors:       make(map[string]string, len(cfg.Chains)),
		testURL:         testURL,
		interval:        interval,
		timeout:         timeout,
		hidden:          cfg.Hidden,
		results:         make(map[string]ProbeResult, len(cfg.Chains)),
		selectedChain:   selected,
		selectionReason: initialSelectionReason(groupType),
	}
	for _, name := range cfg.Chains {
		ch := chains[name]
		if ch == nil {
			return nil, fmt.Errorf("policy group %q: chain %q not found", cfg.Name, name)
		}
		gs.chains[name] = ch
		if err := ch.CheckPacketSupport(); err != nil {
			gs.udpErrors[name] = err.Error()
		} else {
			gs.udpCapable[name] = true
		}
	}
	return gs, nil
}

// Start begins background latency probes. It is idempotent.
func (m *Manager) Start(parent context.Context) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	m.started = true
	names := append([]string(nil), m.order...)
	m.mu.Unlock()

	for _, name := range names {
		if !groupTypeUsesHealth(m.groupType(name)) {
			continue
		}
		m.wg.Add(1)
		go m.probeLoop(ctx, name)
	}
}

func (m *Manager) groupType(groupName string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if gs := m.groups[groupName]; gs != nil {
		return gs.groupType
	}
	return ""
}

func (m *Manager) probeLoop(ctx context.Context, groupName string) {
	defer m.wg.Done()
	_, _ = m.Refresh(ctx, groupName)

	m.mu.RLock()
	gs := m.groups[groupName]
	interval := defaultInterval
	if gs != nil {
		interval = gs.interval
	}
	m.mu.RUnlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = m.Refresh(ctx, groupName)
		}
	}
}

// Close stops background probes. It is idempotent.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	cancel := m.cancel
	m.cancel = nil
	m.started = false
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.wg.Wait()
	return nil
}

// Refresh probes one group or all groups when groupName is empty.
func (m *Manager) Refresh(ctx context.Context, groupName string) (Snapshot, error) {
	if m == nil {
		return Snapshot{}, errors.New("policy manager is nil")
	}
	groupName = strings.TrimSpace(groupName)
	if groupName != "" {
		if err := m.refreshGroup(ctx, groupName); err != nil {
			return m.Snapshot(""), err
		}
		return m.Snapshot(""), nil
	}

	var errs []error
	for _, name := range m.groupNames() {
		if err := m.refreshGroup(ctx, name); err != nil {
			errs = append(errs, err)
		}
	}
	return m.Snapshot(""), errors.Join(errs...)
}

func (m *Manager) groupNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.order...)
}

func (m *Manager) refreshGroup(ctx context.Context, groupName string) error {
	m.mu.RLock()
	gs := m.groups[groupName]
	if gs == nil {
		m.mu.RUnlock()
		return fmt.Errorf("policy group %q not found", groupName)
	}
	if !groupTypeUsesHealth(gs.groupType) {
		m.mu.RUnlock()
		m.mu.Lock()
		if current := m.groups[groupName]; current != nil {
			current.updatedTsNs = time.Now().UnixNano()
		}
		m.mu.Unlock()
		return nil
	}
	chainNames := append([]string(nil), gs.chainNames...)
	chains := make(map[string]*chain.Chain, len(gs.chains))
	for name, ch := range gs.chains {
		chains[name] = ch
	}
	udpCapable := make(map[string]bool, len(gs.udpCapable))
	for name, capable := range gs.udpCapable {
		udpCapable[name] = capable
	}
	udpErrors := make(map[string]string, len(gs.udpErrors))
	for name, udpErr := range gs.udpErrors {
		udpErrors[name] = udpErr
	}
	testURL := gs.testURL
	timeout := gs.timeout
	m.mu.RUnlock()

	results := make(map[string]ProbeResult, len(chainNames))
	var wg sync.WaitGroup
	var resultMu sync.Mutex
	for _, name := range chainNames {
		name := name
		ch := chains[name]
		wg.Add(1)
		go func() {
			defer wg.Done()
			probeCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			result := m.probe(probeCtx, ch, testURL)
			result.ChainName = name
			if result.LastTestTsNs == 0 {
				result.LastTestTsNs = time.Now().UnixNano()
			}
			result.UDPCapable = udpCapable[name]
			result.UDPError = udpErrors[name]
			resultMu.Lock()
			results[name] = result
			resultMu.Unlock()
		}()
	}
	wg.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()
	gs = m.groups[groupName]
	if gs == nil {
		return fmt.Errorf("policy group %q not found", groupName)
	}
	for name, result := range results {
		gs.results[name] = result
	}
	selected, reason := selectChainLocked(gs, SelectionContext{}, nil)
	gs.selectedChain = selected
	gs.selectionReason = reason
	gs.updatedTsNs = time.Now().UnixNano()
	return nil
}

// Select returns the concrete runtime chain for a policy group and route
// context.
func (m *Manager) Select(groupName string, sel SelectionContext) (*chain.Chain, string, error) {
	if m == nil {
		return nil, "", errors.New("policy manager is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	gs := m.groups[groupName]
	if gs == nil {
		return nil, "", fmt.Errorf("policy group %q not found", groupName)
	}
	eligible := map[string]bool(nil)
	if strings.EqualFold(strings.TrimSpace(sel.Network), "udp") {
		eligible = make(map[string]bool, len(gs.chainNames))
		for _, name := range gs.chainNames {
			if gs.udpCapable[name] {
				eligible[name] = true
			}
		}
		if len(eligible) == 0 {
			return nil, "", fmt.Errorf("policy group %q has no UDP-capable member chains", groupName)
		}
	}
	selected, reason := selectChainLocked(gs, sel, eligible)
	if eligible != nil && selected != "" && !eligible[selected] {
		if udpReason := gs.udpErrors[selected]; udpReason != "" {
			return nil, "", fmt.Errorf("policy group %q selected chain %q is not UDP-capable: %s", groupName, selected, udpReason)
		}
		return nil, "", fmt.Errorf("policy group %q selected chain %q is not UDP-capable", groupName, selected)
	}
	ch := gs.chains[selected]
	if ch == nil {
		return nil, "", fmt.Errorf("policy group %q selected missing chain %q", groupName, selected)
	}
	gs.selectedChain = selected
	gs.selectionReason = reason
	gs.updatedTsNs = time.Now().UnixNano()
	return ch, selected, nil
}

// SetSelection updates a select group's current member chain in memory.
func (m *Manager) SetSelection(groupName, chainName string) error {
	if m == nil {
		return errors.New("policy manager is nil")
	}
	groupName = strings.TrimSpace(groupName)
	chainName = strings.TrimSpace(chainName)
	m.mu.Lock()
	defer m.mu.Unlock()
	gs := m.groups[groupName]
	if gs == nil {
		return fmt.Errorf("policy group %q not found", groupName)
	}
	if gs.groupType != TypeSelect {
		return fmt.Errorf("policy group %q is %s, not select", groupName, gs.groupType)
	}
	if _, ok := gs.chains[chainName]; !ok {
		return fmt.Errorf("policy group %q has no member chain %q", groupName, chainName)
	}
	gs.selectedChain = chainName
	gs.selectionReason = "manual"
	gs.updatedTsNs = time.Now().UnixNano()
	return nil
}

// Snapshot returns a copy of current policy group state.
func (m *Manager) Snapshot(profile string) Snapshot {
	snap := Snapshot{Profile: profile}
	if m == nil {
		return snap
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	snap.Groups = make([]GroupSnapshot, 0, len(m.order))
	for _, name := range m.order {
		gs := m.groups[name]
		if gs == nil {
			continue
		}
		snap.Groups = append(snap.Groups, snapshotGroupLocked(gs))
	}
	return snap
}

// ConfigSnapshot builds a config-only snapshot when no runtime manager exists.
func ConfigSnapshot(profile string, groups []config.PolicyGroupConfig) Snapshot {
	snap := Snapshot{Profile: profile, Groups: make([]GroupSnapshot, 0, len(groups))}
	for _, group := range groups {
		testURL := strings.TrimSpace(group.TestURL)
		if testURL == "" {
			testURL = DefaultTestURL
		}
		interval := group.Interval.Std()
		if interval <= 0 {
			interval = defaultInterval
		}
		timeout := group.Timeout.Std()
		if timeout <= 0 {
			timeout = defaultTimeout
		}
		snap.Groups = append(snap.Groups, GroupSnapshot{
			Name:            group.Name,
			Type:            groupTypeForConfig(group),
			Chains:          append([]string(nil), group.Chains...),
			Selected:        selectedForConfig(group),
			Hidden:          group.Hidden,
			TestURL:         testURL,
			Interval:        interval.String(),
			Timeout:         timeout.String(),
			SelectedChain:   selectedForConfig(group),
			SelectionMode:   selectionModeForType(groupTypeForConfig(group)),
			SelectionReason: initialSelectionReason(groupTypeForConfig(group)),
			Results:         []ProbeResult{},
		})
	}
	return snap
}

func snapshotGroupLocked(gs *groupState) GroupSnapshot {
	results := make([]ProbeResult, 0, len(gs.results))
	for _, name := range gs.chainNames {
		if result, ok := gs.results[name]; ok {
			results = append(results, result)
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		return chainIndex(gs.chainNames, results[i].ChainName) < chainIndex(gs.chainNames, results[j].ChainName)
	})
	return GroupSnapshot{
		Name:            gs.name,
		Type:            gs.groupType,
		Chains:          append([]string(nil), gs.chainNames...),
		Selected:        gs.selectedChain,
		Hidden:          gs.hidden,
		TestURL:         gs.testURL,
		Interval:        gs.interval.String(),
		Timeout:         gs.timeout.String(),
		SelectedChain:   gs.selectedChain,
		SelectionMode:   selectionModeForType(gs.groupType),
		SelectionReason: gs.selectionReason,
		UpdatedTsNs:     gs.updatedTsNs,
		Results:         results,
	}
}

func selectChainLocked(gs *groupState, sel SelectionContext, eligible map[string]bool) (string, string) {
	switch gs.groupType {
	case TypeSelect:
		selected := gs.selectedChain
		if selected == "" {
			selected = firstString(gs.chainNames)
		}
		return selected, "manual"
	case TypeFallback:
		return selectFirstHealthyChain(gs, eligible)
	case TypeLoadBalance:
		return selectLoadBalancedChain(gs, sel, eligible)
	case TypeSmart:
		return selectSmartChain(gs, eligible)
	default:
		return selectLowestLatencyChain(gs, eligible)
	}
}

func selectLowestLatencyChain(gs *groupState, eligible map[string]bool) (string, string) {
	best := ""
	var bestLatency int64
	for _, name := range gs.chainNames {
		if eligible != nil && !eligible[name] {
			continue
		}
		result, ok := gs.results[name]
		if !ok || !result.Healthy {
			continue
		}
		if best == "" || result.LatencyNs < bestLatency {
			best = name
			bestLatency = result.LatencyNs
		}
	}
	if best != "" {
		return best, "lowest_latency"
	}
	if fallback := firstEligibleChain(gs, eligible); fallback != "" {
		return fallback, "no_healthy_fallback"
	}
	return "", "no_member"
}

func selectFirstHealthyChain(gs *groupState, eligible map[string]bool) (string, string) {
	for _, name := range gs.chainNames {
		if eligible != nil && !eligible[name] {
			continue
		}
		if result, ok := gs.results[name]; ok && result.Healthy {
			return name, "first_healthy"
		}
	}
	if fallback := firstEligibleChain(gs, eligible); fallback != "" {
		return fallback, "no_healthy_fallback"
	}
	return "", "no_member"
}

func selectLoadBalancedChain(gs *groupState, sel SelectionContext, eligible map[string]bool) (string, string) {
	healthy := make([]string, 0, len(gs.chainNames))
	for _, name := range gs.chainNames {
		if eligible != nil && !eligible[name] {
			continue
		}
		if result, ok := gs.results[name]; ok && result.Healthy {
			healthy = append(healthy, name)
		}
	}
	if len(healthy) == 0 {
		if fallback := firstEligibleChain(gs, eligible); fallback != "" {
			return fallback, "no_healthy_fallback"
		}
		return "", "no_member"
	}
	key := strings.Join([]string{
		strings.TrimSpace(sel.Source),
		strings.ToLower(strings.TrimSpace(sel.Network)),
		strings.TrimSpace(sel.Target),
	}, "|")
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return healthy[int(h.Sum32())%len(healthy)], "stable_hash"
}

func selectSmartChain(gs *groupState, eligible map[string]bool) (string, string) {
	best, bestReason := selectLowestLatencyChain(gs, eligible)
	if best == "" {
		return best, bestReason
	}
	current := gs.selectedChain
	if current == "" {
		return best, bestReason
	}
	if eligible != nil && !eligible[current] {
		return best, bestReason
	}
	currentResult, currentOK := gs.results[current]
	if !currentOK || !currentResult.Healthy {
		return best, bestReason
	}
	bestResult, bestOK := gs.results[best]
	if !bestOK || !bestResult.Healthy || best == current {
		return current, "sticky_healthy"
	}
	delta := time.Duration(currentResult.LatencyNs - bestResult.LatencyNs)
	percentDelta := time.Duration(currentResult.LatencyNs / 5)
	if percentDelta < smartMinDelta {
		percentDelta = smartMinDelta
	}
	if delta >= percentDelta {
		return best, "better_latency"
	}
	return current, "sticky_healthy"
}

func firstEligibleChain(gs *groupState, eligible map[string]bool) string {
	for _, name := range gs.chainNames {
		if eligible == nil || eligible[name] {
			return name
		}
	}
	return ""
}

func defaultProbe(ctx context.Context, ch *chain.Chain, rawURL string) ProbeResult {
	start := time.Now()
	result := ProbeResult{LastTestTsNs: start.UnixNano()}
	transport := &http.Transport{
		DisableKeepAlives: true,
		Proxy:             nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return ch.Dial(ctx, network, address)
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	resp, err := client.Do(req)
	result.LatencyNs = time.Since(start).Nanoseconds()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	result.StatusCode = resp.StatusCode
	if resp.StatusCode >= 500 {
		result.Error = fmt.Sprintf("probe returned HTTP %d", resp.StatusCode)
		return result
	}
	result.Healthy = true
	return result
}

func firstString(in []string) string {
	if len(in) == 0 {
		return ""
	}
	return in[0]
}

func containsString(in []string, target string) bool {
	for _, v := range in {
		if v == target {
			return true
		}
	}
	return false
}

func groupTypeForConfig(group config.PolicyGroupConfig) string {
	groupType := strings.ToLower(strings.TrimSpace(group.Type))
	if groupType == "" {
		return TypeURLTest
	}
	return groupType
}

func isSupportedGroupType(groupType string) bool {
	switch groupType {
	case TypeURLTest, TypeSelect, TypeFallback, TypeLoadBalance, TypeSmart:
		return true
	default:
		return false
	}
}

func groupTypeUsesHealth(groupType string) bool {
	return groupType != TypeSelect
}

func selectedForConfig(group config.PolicyGroupConfig) string {
	selected := strings.TrimSpace(group.Selected)
	if selected != "" {
		return selected
	}
	return firstString(group.Chains)
}

func selectionModeForType(groupType string) string {
	switch groupType {
	case TypeSelect:
		return "manual"
	case TypeFallback:
		return "fallback"
	case TypeLoadBalance:
		return "load-balance"
	case TypeSmart:
		return "smart"
	default:
		return "latency"
	}
}

func initialSelectionReason(groupType string) string {
	switch groupType {
	case TypeSelect:
		return "manual"
	case TypeFallback:
		return "first_healthy"
	case TypeLoadBalance:
		return "stable_hash"
	case TypeSmart:
		return "sticky_healthy"
	default:
		return "lowest_latency"
	}
}

func chainIndex(chains []string, target string) int {
	for i, name := range chains {
		if name == target {
			return i
		}
	}
	return len(chains)
}
