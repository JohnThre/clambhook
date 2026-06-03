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
	Domains        []string
	DomainSuffixes []string
	DomainKeywords []string
	CIDRs          []string
	Ports          []int
	Networks       []string
}

// Decision is the result of evaluating a target against the rule set.
type Decision struct {
	RuleName  string `json:"rule_name,omitempty"`
	Action    string `json:"action"`
	ChainName string `json:"chain_name,omitempty"`
	GroupName string `json:"group_name,omitempty"`
	Target    string `json:"target"`
	Host      string `json:"target_host,omitempty"`
	Port      string `json:"target_port,omitempty"`
	Network   string `json:"network,omitempty"`
	Default   bool   `json:"default,omitempty"`
	ElapsedNs int64  `json:"elapsed_ns,omitempty"`
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
	ports          map[int]struct{}
	networks       map[string]struct{}
}

// Compile validates and prepares rules for efficient matching.
func Compile(in []Rule, defaultChain string, knownChains, knownGroups map[string]struct{}) (*Engine, error) {
	defaultChain = strings.TrimSpace(defaultChain)
	if defaultChain == "" {
		return nil, fmt.Errorf("rules: default chain is required")
	}
	if _, ok := knownChains[defaultChain]; !ok {
		return nil, fmt.Errorf("rules: default chain %q not found", defaultChain)
	}
	out := &Engine{defaultChain: defaultChain, rules: make([]compiledRule, 0, len(in))}
	for i, rule := range in {
		cr, err := compileRule(rule, knownChains, knownGroups)
		if err != nil {
			return nil, fmt.Errorf("rule %d: %w", i, err)
		}
		out.rules = append(out.rules, cr)
	}
	return out, nil
}

func compileRule(rule Rule, knownChains, knownGroups map[string]struct{}) (compiledRule, error) {
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
	for _, raw := range rule.CIDRs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return compiledRule{}, fmt.Errorf("cidr %q: %w", raw, err)
		}
		cr.cidrs = append(cr.cidrs, prefix)
	}
	return cr, nil
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
	start := time.Now()
	host, port := SplitTarget(target)
	network = strings.ToLower(strings.TrimSpace(network))
	for _, rule := range e.rules {
		if !rule.match(network, host, port) {
			continue
		}
		return Decision{
			RuleName:  rule.name,
			Action:    rule.action,
			ChainName: rule.chainName,
			GroupName: rule.groupName,
			Target:    target,
			Host:      host,
			Port:      port,
			Network:   network,
			ElapsedNs: time.Since(start).Nanoseconds(),
		}
	}
	return Decision{
		Action:    ActionChain,
		ChainName: e.defaultChain,
		Target:    target,
		Host:      host,
		Port:      port,
		Network:   network,
		Default:   true,
		ElapsedNs: time.Since(start).Nanoseconds(),
	}
}

func (r compiledRule) match(network, host, port string) bool {
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
	if r.hasDomainMatchers() && !r.matchDomain(host) {
		return false
	}
	if len(r.cidrs) > 0 && !r.matchCIDR(host) {
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

func (r compiledRule) matchCIDR(host string) bool {
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	}
	for _, prefix := range r.cidrs {
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
