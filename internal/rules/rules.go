// Package rules implements ordered routing decisions for proxy traffic.
package rules

import (
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
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
	Processes      []string
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
	RuleName    string      `json:"rule_name,omitempty"`
	RuleNumber  int         `json:"rule_number,omitempty"`
	Action      string      `json:"action"`
	ChainName   string      `json:"chain_name,omitempty"`
	GroupName   string      `json:"group_name,omitempty"`
	Target      string      `json:"target"`
	Host        string      `json:"target_host,omitempty"`
	Port        string      `json:"target_port,omitempty"`
	Network     string      `json:"network,omitempty"`
	Source      string      `json:"source,omitempty"`
	Default     bool        `json:"default,omitempty"`
	ElapsedNs   int64       `json:"elapsed_ns,omitempty"`
	Explanation Explanation `json:"explanation,omitempty"`
}

// Explanation is a compact account of why a route decision was selected.
type Explanation struct {
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
	processes      []string
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
		processes:      normalizeStrings(rule.Processes),
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
	return e.DecideContext(MatchContext{Network: network, Target: target})
}

// DecideWithSource returns the first matching rule decision, including the
// optional source/client-address matcher.
func (e *Engine) DecideWithSource(network, target, source string) Decision {
	return e.DecideContext(MatchContext{Network: network, Target: target, Source: source})
}

// MatchContext carries every per-connection input a rule can match against.
// Empty fields simply don't participate in matching.
type MatchContext struct {
	Network     string
	Target      string
	Source      string
	ProcessName string
	ProcessPath string
}

// DecideContext returns the first matching rule decision for the full match
// context, or the default chain when nothing matches.
func (e *Engine) DecideContext(mc MatchContext) Decision {
	start := time.Now()
	target := mc.Target
	host, port := SplitTarget(target)
	network := strings.ToLower(strings.TrimSpace(mc.Network))
	for i, rule := range e.rules {
		match, ok := rule.match(network, host, port, mc.Source, mc.ProcessName, mc.ProcessPath)
		if !ok {
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
			Source:     mc.Source,
			ElapsedNs:  time.Since(start).Nanoseconds(),
			Explanation: Explanation{
				Source:       "profile_rule",
				RuleName:     rule.name,
				RuleNumber:   i + 1,
				MatcherKind:  match.Kind,
				MatcherValue: match.Value,
				PolicyGroup:  rule.groupName,
				FinalChain:   rule.chainName,
				Summary:      explainMatchedRule(rule.name, match),
			},
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
		Source:     mc.Source,
		Default:    true,
		ElapsedNs:  time.Since(start).Nanoseconds(),
		Explanation: Explanation{
			Source:       "default",
			RuleNumber:   len(e.rules) + 1,
			DefaultChain: e.defaultChain,
			FinalChain:   e.defaultChain,
			Summary:      "No rule matched; used the default chain.",
		},
	}
}

type matchInfo struct {
	Kind  string
	Value string
}

func (r compiledRule) match(network, host, port, source, procName, procPath string) (matchInfo, bool) {
	var first matchInfo
	if len(r.networks) > 0 {
		if _, ok := r.networks[network]; !ok {
			return matchInfo{}, false
		}
		first = firstMatch(first, "network", network)
	}
	if len(r.ports) > 0 {
		n, err := strconv.Atoi(port)
		if err != nil {
			return matchInfo{}, false
		}
		if _, ok := r.ports[n]; !ok {
			return matchInfo{}, false
		}
		first = firstMatch(first, "port", port)
	}
	if len(r.sourceCIDRs) > 0 {
		sourceMatch, ok := matchCIDRListInfo(r.sourceCIDRs, source)
		if !ok {
			return matchInfo{}, false
		}
		first = firstMatch(first, "source_cidr", sourceMatch)
	}
	if len(r.processes) > 0 {
		procMatch, ok := r.matchProcess(procName, procPath)
		if !ok {
			return matchInfo{}, false
		}
		first = firstMatch(first, "process", procMatch)
	}
	if r.hasDomainMatchers() {
		domainMatch, ok := r.matchDomain(host)
		if !ok {
			return matchInfo{}, false
		}
		first = firstMatch(first, domainMatch.Kind, domainMatch.Value)
	}
	if len(r.cidrs) > 0 {
		cidrMatch, ok := matchCIDRListInfo(r.cidrs, host)
		if !ok {
			return matchInfo{}, false
		}
		first = firstMatch(first, "cidr", cidrMatch)
	}
	if len(r.ruleSets) > 0 {
		setMatch, ok := r.matchRuleSets(host)
		if !ok {
			return matchInfo{}, false
		}
		first = firstMatch(first, setMatch.Kind, setMatch.Value)
	}
	if first.Kind == "" {
		first = matchInfo{Kind: "all_traffic", Value: "*"}
	}
	return first, true
}

// matchProcess reports whether the owning process matches any configured
// process pattern. A pattern matches the process name, its full executable
// path, or the path's base name, case-insensitively.
func (r compiledRule) matchProcess(procName, procPath string) (string, bool) {
	name := strings.ToLower(strings.TrimSpace(procName))
	fullPath := strings.ToLower(strings.TrimSpace(procPath))
	base := ""
	if fullPath != "" {
		base = strings.ToLower(filepath.Base(procPath))
	}
	for _, p := range r.processes {
		if p == "" {
			continue
		}
		if p == name || (fullPath != "" && p == fullPath) || (base != "" && p == base) {
			return p, true
		}
	}
	return "", false
}

func (r compiledRule) hasDomainMatchers() bool {
	return len(r.domains) > 0 || len(r.domainSuffixes) > 0 || len(r.domainKeywords) > 0
}

func (r compiledRule) matchDomain(host string) (matchInfo, bool) {
	host = normalizeHost(host)
	if host == "" {
		return matchInfo{}, false
	}
	if _, ok := r.domains[host]; ok {
		return matchInfo{Kind: "domain", Value: host}, true
	}
	if _, ok := r.domainSuffixes[host]; ok {
		return matchInfo{Kind: "domain_suffix", Value: host}, true
	}
	for i := strings.IndexByte(host, '.'); i >= 0 && i < len(host)-1; {
		suffix := host[i+1:]
		if _, ok := r.domainSuffixes[suffix]; ok {
			return matchInfo{Kind: "domain_suffix", Value: suffix}, true
		}
		next := strings.IndexByte(host[i+1:], '.')
		if next < 0 {
			break
		}
		i += next + 1
	}
	for _, keyword := range r.domainKeywords {
		if strings.Contains(host, keyword) {
			return matchInfo{Kind: "domain_keyword", Value: keyword}, true
		}
	}
	return matchInfo{}, false
}

func (r compiledRule) matchRuleSets(host string) (matchInfo, bool) {
	for _, set := range r.ruleSets {
		if match, ok := set.match(host); ok {
			if match.Value == "" {
				match.Value = set.name
			} else {
				match.Value = set.name + ":" + match.Value
			}
			match.Kind = "rule_set_" + match.Kind
			return match, true
		}
	}
	return matchInfo{}, false
}

func (s compiledRuleSet) match(host string) (matchInfo, bool) {
	if len(s.domains) == 0 && len(s.domainSuffixes) == 0 && len(s.domainKeywords) == 0 && len(s.cidrs) == 0 {
		return matchInfo{}, false
	}
	if len(s.domains) > 0 || len(s.domainSuffixes) > 0 || len(s.domainKeywords) > 0 {
		r := compiledRule{domains: s.domains, domainSuffixes: s.domainSuffixes, domainKeywords: s.domainKeywords}
		if match, ok := r.matchDomain(host); ok {
			return match, true
		}
	}
	cidr, ok := matchCIDRListInfo(s.cidrs, host)
	return matchInfo{Kind: "cidr", Value: cidr}, ok
}

func matchCIDRListInfo(prefixes []netip.Prefix, host string) (string, bool) {
	host, _ = SplitTarget(host)
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return "", false
	}
	for _, prefix := range prefixes {
		if prefix.Contains(ip) {
			return prefix.String(), true
		}
	}
	return "", false
}

func firstMatch(current matchInfo, kind, value string) matchInfo {
	if current.Kind != "" {
		return current
	}
	return matchInfo{Kind: kind, Value: value}
}

func explainMatchedRule(name string, match matchInfo) string {
	if name == "" {
		name = "unnamed"
	}
	if match.Kind == "" {
		return fmt.Sprintf("Rule %q matched.", name)
	}
	if match.Value == "" {
		return fmt.Sprintf("Rule %q matched %s.", name, match.Kind)
	}
	return fmt.Sprintf("Rule %q matched %s %q.", name, match.Kind, match.Value)
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
