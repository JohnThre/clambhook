// Package temprules maintains in-memory, expiring routing rules.
package temprules

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/rules"
	"github.com/google/uuid"
)

// Rule is one in-memory temporary rule. Temporary rules are never persisted
// to TOML and disappear on daemon restart.
type Rule struct {
	ID               string            `json:"id"`
	Profile          string            `json:"profile"`
	Rule             config.RuleConfig `json:"rule"`
	CreatedTsNs      int64             `json:"created_ts_ns"`
	ExpiresTsNs      int64             `json:"expires_ts_ns"`
	SourceConnID     string            `json:"source_conn_id,omitempty"`
	SourceTarget     string            `json:"source_target,omitempty"`
	SourceTargetHost string            `json:"source_target_host,omitempty"`
}

// CreateRequest describes a temporary rule to install.
type CreateRequest struct {
	Profile          string
	Rule             config.RuleConfig
	TTL              time.Duration
	SourceConnID     string
	SourceTarget     string
	SourceTargetHost string
}

// Manager stores temporary rules ordered by creation time. Newer temporary
// rules are evaluated before older ones so a short-lived correction can
// override an earlier temporary choice.
//
// Compiled rule engines are cached per profile and rebuilt only when the rule
// set changes (mutation or TTL expiry) or when the caller's chain/group
// context changes. generation is bumped whenever the stored rules change;
// nextExpiryNs records the soonest upcoming expiry so Decide can serve the
// unchanged hot path under a shared read lock and escalate to an exclusive
// lock only when a rule is due to expire.
type Manager struct {
	mu           sync.RWMutex
	rules        []Rule
	generation   uint64
	nextExpiryNs int64
	cache        map[string]*compiledProfile
	compileCount uint64
}

// compiledProfile is a cached, immutable compilation of a profile's temporary
// rules. engine is nil when empty is true (no temporary rules for the
// profile). Once stored it is never mutated; a rebuild replaces the pointer.
type compiledProfile struct {
	generation   uint64
	defaultChain string
	chainsHash   uint64
	groupsHash   uint64
	engine       *rules.Engine
	empty        bool
}

// New creates an empty temporary-rule manager.
func New() *Manager { return &Manager{} }

// Create installs a temporary rule and returns the stored rule with ID and
// timestamps populated.
func (m *Manager) Create(req CreateRequest) (Rule, error) {
	if m == nil {
		return Rule{}, fmt.Errorf("temporary rules are not configured")
	}
	profile := strings.TrimSpace(req.Profile)
	if profile == "" {
		return Rule{}, fmt.Errorf("profile is required")
	}
	if strings.TrimSpace(req.Rule.Name) == "" {
		return Rule{}, fmt.Errorf("rule name is required")
	}
	if strings.TrimSpace(req.Rule.Action) == "" {
		return Rule{}, fmt.Errorf("rule action is required")
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	now := time.Now()
	rule := Rule{
		ID:               uuid.NewString(),
		Profile:          profile,
		Rule:             req.Rule,
		CreatedTsNs:      now.UnixNano(),
		ExpiresTsNs:      now.Add(ttl).UnixNano(),
		SourceConnID:     strings.TrimSpace(req.SourceConnID),
		SourceTarget:     strings.TrimSpace(req.SourceTarget),
		SourceTargetHost: strings.TrimSpace(req.SourceTargetHost),
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneExpiredLocked(now)
	m.rules = append([]Rule{rule}, m.rules...)
	m.generation++
	m.recomputeNextExpiryLocked()
	return rule, nil
}

// Delete removes one temporary rule by ID.
func (m *Manager) Delete(id string) bool {
	if m == nil {
		return false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneExpiredLocked(time.Now())
	for i, rule := range m.rules {
		if rule.ID != id {
			continue
		}
		m.rules = append(m.rules[:i], m.rules[i+1:]...)
		m.generation++
		m.recomputeNextExpiryLocked()
		return true
	}
	return false
}

// Snapshot returns current non-expired temporary rules. Empty profile returns
// all profiles.
func (m *Manager) Snapshot(profile string) []Rule {
	if m == nil {
		return nil
	}
	profile = strings.TrimSpace(profile)
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneExpiredLocked(now)
	out := make([]Rule, 0, len(m.rules))
	for _, rule := range m.rules {
		if profile != "" && rule.Profile != profile {
			continue
		}
		out = append(out, cloneRule(rule))
	}
	return out
}

// Decide evaluates non-expired temporary rules for a profile. ok is false
// when no temporary rule matched.
//
// The compiled engine is reused across calls: as long as the rule set,
// generation, and the caller's default chain / known chains / known groups are
// unchanged and no rule is due to expire, Decide serves from cache under a
// shared read lock and never recompiles.
func (m *Manager) Decide(profile, defaultChain, network, target, source, procName, procPath string, knownChains, knownGroups map[string]struct{}) (rules.Decision, bool, error) {
	if m == nil {
		return rules.Decision{}, false, nil
	}
	profile = strings.TrimSpace(profile)
	nowNs := time.Now().UnixNano()
	chainsHash := hashSet(knownChains)
	groupsHash := hashSet(knownGroups)

	m.mu.RLock()
	if m.nextExpiryNs == 0 || nowNs < m.nextExpiryNs {
		if entry := m.cache[profile]; entry != nil &&
			entry.generation == m.generation &&
			entry.defaultChain == defaultChain &&
			entry.chainsHash == chainsHash &&
			entry.groupsHash == groupsHash {
			engine := entry.engine
			empty := entry.empty
			m.mu.RUnlock()
			if empty {
				return rules.Decision{}, false, nil
			}
			return decideWith(engine, network, target, source, procName, procPath)
		}
	}
	m.mu.RUnlock()

	return m.decideSlow(profile, defaultChain, network, target, source, procName, procPath, knownChains, knownGroups, chainsHash, groupsHash)
}

// decideSlow prunes expired rules, (re)compiles the profile engine when the
// cache is stale, and evaluates the request. It holds the exclusive lock only
// while touching shared state; the immutable engine is evaluated after unlock.
func (m *Manager) decideSlow(profile, defaultChain, network, target, source, procName, procPath string, knownChains, knownGroups map[string]struct{}, chainsHash, groupsHash uint64) (rules.Decision, bool, error) {
	m.mu.Lock()
	m.pruneExpiredLocked(time.Now())
	entry := m.cache[profile]
	if entry == nil ||
		entry.generation != m.generation ||
		entry.defaultChain != defaultChain ||
		entry.chainsHash != chainsHash ||
		entry.groupsHash != groupsHash {
		engine, empty, err := m.compileProfileLocked(profile, defaultChain, knownChains, knownGroups)
		if err != nil {
			m.mu.Unlock()
			return rules.Decision{}, false, err
		}
		entry = &compiledProfile{
			generation:   m.generation,
			defaultChain: defaultChain,
			chainsHash:   chainsHash,
			groupsHash:   groupsHash,
			engine:       engine,
			empty:        empty,
		}
		if m.cache == nil {
			m.cache = make(map[string]*compiledProfile)
		}
		m.cache[profile] = entry
	}
	engine := entry.engine
	empty := entry.empty
	m.mu.Unlock()

	if empty {
		return rules.Decision{}, false, nil
	}
	return decideWith(engine, network, target, source, procName, procPath)
}

// compileProfileLocked builds a rules engine from the profile's current
// temporary rules. empty is true when the profile has no temporary rules.
func (m *Manager) compileProfileLocked(profile, defaultChain string, knownChains, knownGroups map[string]struct{}) (*rules.Engine, bool, error) {
	runtimeRules := make([]rules.Rule, 0, len(m.rules))
	for _, temp := range m.rules {
		if profile != "" && temp.Profile != profile {
			continue
		}
		rule := temp.Rule
		runtimeRules = append(runtimeRules, rules.Rule{
			Name:           rule.Name,
			Action:         rule.Action,
			RuleSets:       rule.RuleSets,
			Domains:        rule.Domains,
			DomainSuffixes: rule.DomainSuffixes,
			DomainKeywords: rule.DomainKeywords,
			CIDRs:          rule.CIDRs,
			SourceCIDRs:    rule.SourceCIDRs,
			Ports:          rule.Ports,
			Networks:       rule.Networks,
			Processes:      rule.Processes,
		})
	}
	if len(runtimeRules) == 0 {
		return nil, true, nil
	}
	engine, err := rules.CompileWithRuleSets(runtimeRules, defaultChain, knownChains, knownGroups, nil)
	if err != nil {
		return nil, false, err
	}
	m.compileCount++
	return engine, false, nil
}

// decideWith evaluates an already-compiled engine. The engine is immutable
// after compilation so this is safe to call without holding any lock.
func decideWith(engine *rules.Engine, network, target, source, procName, procPath string) (rules.Decision, bool, error) {
	decision := engine.DecideContext(rules.MatchContext{
		Network:     network,
		Target:      target,
		Source:      source,
		ProcessName: procName,
		ProcessPath: procPath,
	})
	if decision.Default {
		return rules.Decision{}, false, nil
	}
	decision.Explanation.Source = "temporary_rule"
	return decision, true, nil
}

func (m *Manager) pruneExpiredLocked(now time.Time) {
	nowNs := now.UnixNano()
	dst := m.rules[:0]
	removed := false
	for _, rule := range m.rules {
		if rule.ExpiresTsNs > nowNs {
			dst = append(dst, rule)
		} else {
			removed = true
		}
	}
	for i := len(dst); i < len(m.rules); i++ {
		m.rules[i] = Rule{}
	}
	m.rules = dst
	if removed {
		m.generation++
		m.recomputeNextExpiryLocked()
	}
}

// recomputeNextExpiryLocked refreshes the soonest upcoming rule expiry. It is
// zero when no temporary rules remain.
func (m *Manager) recomputeNextExpiryLocked() {
	var next int64
	for _, rule := range m.rules {
		if next == 0 || rule.ExpiresTsNs < next {
			next = rule.ExpiresTsNs
		}
	}
	m.nextExpiryNs = next
}

// hashSet returns an order-independent fingerprint of a name set so Decide can
// detect chain/group context changes without allocating or sorting.
func hashSet(set map[string]struct{}) uint64 {
	const prime = 1099511628211
	var xorAcc, sumAcc uint64
	for name := range set {
		h := fnv64(name)
		xorAcc ^= h
		sumAcc += h
	}
	return (xorAcc * prime) ^ sumAcc ^ (uint64(len(set)) * prime)
}

func fnv64(s string) uint64 {
	const offset = 14695981039346656037
	const prime = 1099511628211
	h := uint64(offset)
	for i := range len(s) {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

func cloneRule(rule Rule) Rule {
	rule.Rule.RuleSets = append([]string(nil), rule.Rule.RuleSets...)
	rule.Rule.Domains = append([]string(nil), rule.Rule.Domains...)
	rule.Rule.DomainSuffixes = append([]string(nil), rule.Rule.DomainSuffixes...)
	rule.Rule.DomainKeywords = append([]string(nil), rule.Rule.DomainKeywords...)
	rule.Rule.CIDRs = append([]string(nil), rule.Rule.CIDRs...)
	rule.Rule.SourceCIDRs = append([]string(nil), rule.Rule.SourceCIDRs...)
	rule.Rule.Ports = append([]int(nil), rule.Rule.Ports...)
	rule.Rule.Networks = append([]string(nil), rule.Rule.Networks...)
	rule.Rule.Processes = append([]string(nil), rule.Rule.Processes...)
	return rule
}
