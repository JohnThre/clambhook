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
type Manager struct {
	mu    sync.RWMutex
	rules []Rule
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
func (m *Manager) Decide(profile, defaultChain, network, target, source, procName, procPath string, knownChains, knownGroups map[string]struct{}) (rules.Decision, bool, error) {
	if m == nil {
		return rules.Decision{}, false, nil
	}
	profile = strings.TrimSpace(profile)
	current := m.Snapshot(profile)
	if len(current) == 0 {
		return rules.Decision{}, false, nil
	}
	runtimeRules := make([]rules.Rule, 0, len(current))
	for _, temp := range current {
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
	engine, err := rules.CompileWithRuleSets(runtimeRules, defaultChain, knownChains, knownGroups, nil)
	if err != nil {
		return rules.Decision{}, false, err
	}
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
	for _, rule := range m.rules {
		if rule.ExpiresTsNs > nowNs {
			dst = append(dst, rule)
		}
	}
	m.rules = dst
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
