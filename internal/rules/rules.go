// Package rules implements ordered routing decisions for proxy traffic.
package rules

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const (
	ActionChain  = "chain"
	ActionGroup  = "group"
	ActionDirect = "direct"
	ActionBlock  = "block"
	ActionReject = "reject"
)

// Rule is the runtime form of one ordered routing rule. Empty matcher fields
// are wildcards; when multiple matcher families are set, all populated
// families must match.
type Rule struct {
	Name           string
	Action         string
	RuleSets       []string
	Domains        []string
	DomainSuffixes []string
	DomainKeywords []string
	CIDRs          []string
	SourceCIDRs    []string
	Ports          []int
	Networks       []string
}

// RuleSet is a reusable destination matcher referenced by Rule.RuleSets.
type RuleSet struct {
	Domains        []string
	DomainSuffixes []string
	DomainKeywords []string
	CIDRs          []string
}

// Decision is the result of evaluating a target against the rule set.
type Decision struct {
	RuleName   string `json:"rule_name,omitempty"`
	RuleNumber int    `json:"rule_number,omitempty"`
	Action     string `json:"action"`
	ChainName  string `json:"chain_name,omitempty"`
	GroupName  string `json:"group_name,omitempty"`
	Target     string `json:"target"`
	Host       string `json:"target_host,omitempty"`
	Port       string `json:"target_port,omitempty"`
	Network    string `json:"network,omitempty"`
	Source     string `json:"source,omitempty"`
	Default    bool   `json:"default,omitempty"`
	ElapsedNs  int64  `json:"elapsed_ns,omitempty"`
}

// Engine evaluates ordered rules and falls back to a default chain.
type Engine struct {
	defaultChain string
	rules        []compiledRule
}

type compiledRule struct {
	name           string
	action         string
	chainName      string
	groupName      string
	domains        map[string]struct{}
	domainSuffixes map[string]struct{}
	domainKeywords []string
	cidrs          []netip.Prefix
	sourceCIDRs    []netip.Prefix
	ports          map[int]struct{}
	networks       map[string]struct{}
	ruleSets       []compiledRuleSet
}

type compiledRuleSet struct {
	name           string
	domains        map[string]struct{}
	domainSuffixes map[string]struct{}
	domainKeywords []string
	cidrs          []netip.Prefix
}

// Compile validates and prepares rules for efficient matching.
func Compile(in []Rule, defaultChain string, knownChains, knownGroups map[string]struct{}) (*Engine, error) {
	return CompileWithRuleSets(in, defaultChain, knownChains, knownGroups, nil)
}

// CompileWithRuleSets validates and prepares rules plus named rule-set
// references for efficient matching.
func CompileWithRuleSets(in []Rule, defaultChain string, knownChains, knownGroups map[string]struct{}, knownRuleSets map[string]RuleSet) (*Engine, error) {
	defaultChain = strings.TrimSpace(defaultChain)
	if defaultChain == "" {
		return nil, fmt.Errorf("rules: default chain is required")
	}
	if _, ok := knownChains[defaultChain]; !ok {
		return nil, fmt.Errorf("rules: default chain %q not found", defaultChain)
	}
	out := &Engine{defaultChain: defaultChain, rules: make([]compiledRule, 0, len(in))}
	for i, rule := range in {
		cr, err := compileRule(rule, knownChains, knownGroups, knownRuleSets)
		if err != nil {
			return nil, fmt.Errorf("rule %d: %w", i, err)
		}
		out.rules = append(out.rules, cr)
	}
	return out, nil
}

func compileRule(rule Rule, knownChains, knownGroups map[string]struct{}, knownRuleSets map[string]RuleSet) (compiledRule, error) {
	name := strings.TrimSpace(rule.Name)
	if name == "" {
		name = "unnamed"
	}
	action, targetName, err := parseAction(rule.Action)
	if err != nil {
		return compiledRule{}, err
	}
	chainName := ""
	groupName := ""
	if action == ActionChain {
		if targetName == "" {
			return compiledRule{}, fmt.Errorf("chain action requires chain:<name>")
		}
		if _, ok := knownChains[targetName]; !ok {
			return compiledRule{}, fmt.Errorf("chain %q not found", targetName)
		}
		chainName = targetName
	}
	if action == ActionGroup {
		if targetName == "" {
			return compiledRule{}, fmt.Errorf("group action requires group:<name>")
		}
		if _, ok := knownGroups[targetName]; !ok {
			return compiledRule{}, fmt.Errorf("policy group %q not found", targetName)
		}
		groupName = targetName
	}
	cr := compiledRule{
		name:           name,
		action:         action,
		chainName:      chainName,
		groupName:      groupName,
		domains:        makeStringSet(normalizeStrings(rule.Domains)),
		domainSuffixes: makeStringSet(normalizeSuffixes(rule.DomainSuffixes)),
		domainKeywords: normalizeStrings(rule.DomainKeywords),
		ports:          makePortSet(rule.Ports),
		networks:       makeStringSet(normalizeStrings(rule.Networks)),
	}
	if len(rule.RuleSets) > 0 && (len(rule.Domains) > 0 || len(rule.DomainSuffixes) > 0 || len(rule.DomainKeywords) > 0 || len(rule.CIDRs) > 0) {
		return compiledRule{}, fmt.Errorf("rule_sets cannot be combined with destination matchers")
	}
	for _, raw := range rule.CIDRs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return compiledRule{}, fmt.Errorf("cidr %q: %w", raw, err)
		}
		cr.cidrs = append(cr.cidrs, prefix)
	}
	for _, raw := range rule.SourceCIDRs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return compiledRule{}, fmt.Errorf("source cidr %q: %w", raw, err)
		}
		cr.sourceCIDRs = append(cr.sourceCIDRs, prefix)
	}
	seenRuleSets := make(map[string]struct{}, len(rule.RuleSets))
	for _, raw := range rule.RuleSets {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if _, seen := seenRuleSets[name]; seen {
			continue
		}
		seenRuleSets[name] = struct{}{}
		set, ok := knownRuleSets[name]
		if !ok {
			return compiledRule{}, fmt.Errorf("rule set %q not found", name)
		}
		compiled, err := compileRuleSet(name, set)
		if err != nil {
			return compiledRule{}, err
		}
		cr.ruleSets = append(cr.ruleSets, compiled)
	}
	return cr, nil
}

func compileRuleSet(name string, set RuleSet) (compiledRuleSet, error) {
	out := compiledRuleSet{
		name:           name,
		domains:        makeStringSet(normalizeStrings(set.Domains)),
		domainSuffixes: makeStringSet(normalizeSuffixes(set.DomainSuffixes)),
		domainKeywords: normalizeStrings(set.DomainKeywords),
	}
	for _, raw := range set.CIDRs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return compiledRuleSet{}, fmt.Errorf("rule set %q cidr %q: %w", name, raw, err)
		}
		out.cidrs = append(out.cidrs, prefix)
	}
	return out, nil
}

func parseAction(raw string) (action, chainName string, err error) {
	raw = strings.TrimSpace(raw)
	lower := strings.ToLower(raw)
	switch {
	case lower == ActionDirect:
		return ActionDirect, "", nil
	case lower == ActionBlock:
		return ActionBlock, "", nil
	case lower == ActionReject:
		return ActionReject, "", nil
	case strings.HasPrefix(lower, ActionChain+":"):
		name := strings.TrimSpace(raw[len(ActionChain)+1:])
		if name == "" {
			return "", "", fmt.Errorf("chain action requires chain:<name>")
		}
		return ActionChain, name, nil
	case strings.HasPrefix(lower, ActionGroup+":"):
		name := strings.TrimSpace(raw[len(ActionGroup)+1:])
		if name == "" {
			return "", "", fmt.Errorf("group action requires group:<name>")
		}
		return ActionGroup, name, nil
	default:
		return "", "", fmt.Errorf("unknown action %q", raw)
	}
}

// Decide returns the first matching rule decision, or the default chain.
func (e *Engine) Decide(network, target string) Decision {
	return e.DecideWithSource(network, target, "")
}

// DecideWithSource returns the first matching rule decision, including
// optional source/client-address matchers.
func (e *Engine) DecideWithSource(network, target, source string) Decision {
	start := time.Now()
	host, port := SplitTarget(target)
	network = strings.ToLower(strings.TrimSpace(network))
	for i, rule := range e.rules {
		if !rule.match(network, host, port, source) {
			continue
		}
		return Decision{
			RuleName:   rule.name,
			RuleNumber: i + 1,
			Action:     rule.action,
			ChainName:  rule.chainName,
			GroupName:  rule.groupName,
			Target:     target,
			Host:       host,
			Port:       port,
			Network:    network,
			Source:     source,
			ElapsedNs:  time.Since(start).Nanoseconds(),
		}
	}
	return Decision{
		RuleNumber: len(e.rules) + 1,
		Action:     ActionChain,
		ChainName:  e.defaultChain,
		Target:     target,
		Host:       host,
		Port:       port,
		Network:    network,
		Source:     source,
		Default:    true,
		ElapsedNs:  time.Since(start).Nanoseconds(),
	}
}

func (r compiledRule) match(network, host, port, source string) bool {
	if len(r.networks) > 0 {
		if _, ok := r.networks[network]; !ok {
			return false
		}
	}
	if len(r.ports) > 0 {
		n, err := strconv.Atoi(port)
		if err != nil {
			return false
		}
		if _, ok := r.ports[n]; !ok {
			return false
		}
	}
	if len(r.sourceCIDRs) > 0 && !matchCIDRList(r.sourceCIDRs, source) {
		return false
	}
	if r.hasDomainMatchers() && !r.matchDomain(host) {
		return false
	}
	if len(r.cidrs) > 0 && !matchCIDRList(r.cidrs, host) {
		return false
	}
	if len(r.ruleSets) > 0 && !r.matchRuleSets(host) {
		return false
	}
	return true
}

func (r compiledRule) hasDomainMatchers() bool {
	return len(r.domains) > 0 || len(r.domainSuffixes) > 0 || len(r.domainKeywords) > 0
}

func (r compiledRule) matchDomain(host string) bool {
	host = normalizeHost(host)
	if host == "" {
		return false
	}
	if _, ok := r.domains[host]; ok {
		return true
	}
	if _, ok := r.domainSuffixes[host]; ok {
		return true
	}
	for i := strings.IndexByte(host, '.'); i >= 0 && i < len(host)-1; {
		if _, ok := r.domainSuffixes[host[i+1:]]; ok {
			return true
		}
		next := strings.IndexByte(host[i+1:], '.')
		if next < 0 {
			break
		}
		i += next + 1
	}
	for _, keyword := range r.domainKeywords {
		if strings.Contains(host, keyword) {
			return true
		}
	}
	return false
}

func (r compiledRule) matchRuleSets(host string) bool {
	for _, set := range r.ruleSets {
		if set.match(host) {
			return true
		}
	}
	return false
}

func (s compiledRuleSet) match(host string) bool {
	if len(s.domains) == 0 && len(s.domainSuffixes) == 0 && len(s.domainKeywords) == 0 && len(s.cidrs) == 0 {
		return false
	}
	if len(s.domains) > 0 || len(s.domainSuffixes) > 0 || len(s.domainKeywords) > 0 {
		r := compiledRule{domains: s.domains, domainSuffixes: s.domainSuffixes, domainKeywords: s.domainKeywords}
		if r.matchDomain(host) {
			return true
		}
	}
	return matchCIDRList(s.cidrs, host)
}

func matchCIDRList(prefixes []netip.Prefix, host string) bool {
	host, _ = SplitTarget(host)
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	}
	for _, prefix := range prefixes {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// SplitTarget splits host:port targets while tolerating bare hosts.
func SplitTarget(target string) (host, port string) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", ""
	}
	if h, p, err := net.SplitHostPort(target); err == nil {
		return normalizeHost(h), p
	}
	if i := strings.LastIndexByte(target, ':'); i > 0 && i < len(target)-1 {
		candidate := target[i+1:]
		if _, err := strconv.Atoi(candidate); err == nil {
			return normalizeHost(target[:i]), candidate
		}
	}
	return normalizeHost(target), ""
}

func normalizeHost(host string) string {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	host = strings.TrimSuffix(host, ".")
	return strings.ToLower(host)
}

func normalizeStrings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		v := strings.ToLower(strings.TrimSpace(raw))
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func normalizeSuffixes(in []string) []string {
	out := normalizeStrings(in)
	for i := range out {
		out[i] = strings.TrimPrefix(out[i], ".")
	}
	return out
}

func makeStringSet(in []string) map[string]struct{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(in))
	for _, v := range in {
		out[v] = struct{}{}
	}
	return out
}

func makePortSet(in []int) map[int]struct{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[int]struct{}, len(in))
	for _, v := range in {
		out[v] = struct{}{}
	}
	return out
}
