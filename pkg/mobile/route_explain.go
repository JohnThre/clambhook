package mobile

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
	"github.com/JohnThre/clambhook/internal/protocol"
	"github.com/JohnThre/clambhook/internal/rules"
	"github.com/JohnThre/clambhook/internal/ruleset"
	"github.com/JohnThre/clambhook/internal/subscription"
	"github.com/JohnThre/clambhook/internal/temprules"
)

type ruleTestResponse struct {
	Profile  string          `json:"profile"`
	Decision rules.Decision  `json:"decision"`
	Chain    *ruleTestChain  `json:"chain,omitempty"`
	Hops     []serverPayload `json:"hops,omitempty"`
}

type ruleTestChain struct {
	Name         string                `json:"name"`
	HopCount     int                   `json:"hop_count"`
	Capabilities protocol.Capabilities `json:"capabilities"`
}

// TestRuleJSON evaluates the selected profile's routing rules directly from
// configPath. It is used by app and extension code when the daemon HTTP API is
// not running.
func TestRuleJSON(configPath, profileName, network, target, source string) (string, error) {
	cfg, err := loadTunnelConfig(configPath)
	if err != nil {
		return "", err
	}
	if err := engine.ValidateConfig(cfg); err != nil {
		return "", err
	}
	resp, err := explainRouteForConfig(cfg, profileName, network, target, source, nil)
	if err != nil {
		return "", err
	}
	return marshalString(resp)
}

// TestRuleJSON evaluates rules against the live tunnel config and includes
// in-memory temporary rules created by the packet tunnel runtime.
func (r *TunnelRuntime) TestRuleJSON(profileName, network, target, source string) (string, error) {
	r.mu.Lock()
	cfg := r.cfg
	tempRules := r.temp
	r.mu.Unlock()
	if cfg == nil {
		return "", fmt.Errorf("tunnel: runtime is not running")
	}
	resp, err := explainRouteForConfig(cfg, profileName, network, target, source, tempRules)
	if err != nil {
		return "", err
	}
	return marshalString(resp)
}

func explainRouteForConfig(cfg *config.Config, profileName, network, target, source string, tempRules *temprules.Manager) (ruleTestResponse, error) {
	network = strings.ToLower(strings.TrimSpace(network))
	if network != "tcp" && network != "udp" {
		return ruleTestResponse{}, fmt.Errorf("network must be tcp or udp")
	}
	if err := validateRuleTestTarget(target); err != nil {
		return ruleTestResponse{}, err
	}
	profile, err := selectRuleTestProfile(cfg, profileName)
	if err != nil {
		return ruleTestResponse{}, err
	}
	effectiveProfile := subscriptionProfile(cfg.Path, profile)
	defaultChainName := profile.Chains[0].Name
	var decision rules.Decision
	if tempRules != nil {
		tempDecision, ok, err := tempRules.Decide(profile.Name, defaultChainName, network, target, source, "", "", knownChainNames(profile), knownPolicyGroupNames(profile))
		if err != nil {
			return ruleTestResponse{}, err
		}
		if ok {
			decision = tempDecision
		}
	}
	if decision.Action == "" {
		ruleEngine, err := compileProfileRules(cfg.Path, effectiveProfile, defaultChainName)
		if err != nil {
			return ruleTestResponse{}, err
		}
		decision = ruleEngine.DecideWithSource(network, target, source)
	}
	resp := ruleTestResponse{
		Profile:  profile.Name,
		Decision: decision,
	}
	if decision.Action == rules.ActionChain {
		populateRuleTestChain(profile, decision.ChainName, &resp)
	}
	if decision.Action == rules.ActionGroup {
		selected, err := selectPolicyGroupChain(profile, decision.GroupName, network)
		if err != nil {
			return ruleTestResponse{}, err
		}
		resp.Decision.ChainName = selected
		resp.Decision.Explanation.SelectedChain = selected
		resp.Decision.Explanation.FinalChain = selected
		populateRuleTestChain(profile, selected, &resp)
	}
	return resp, nil
}

func validateRuleTestTarget(target string) error {
	host, port := rules.SplitTarget(target)
	if host == "" || port == "" {
		return fmt.Errorf("target must be host:port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("target port must be between 1 and 65535")
	}
	return nil
}

func selectRuleTestProfile(cfg *config.Config, name string) (*config.Profile, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return cfg.ActiveProfile()
	}
	profile, ok := cfg.ProfileByName(name)
	if !ok {
		return nil, fmt.Errorf("profile %q not found", name)
	}
	return profile, nil
}

func subscriptionProfile(configPath string, profile *config.Profile) *config.Profile {
	cp := subscriptionProfileWithoutGeneratedRules(profile)
	_, _, effective := subscription.EffectiveRules(configPath, profile)
	cp.Rules = effective
	return &cp
}

func subscriptionProfileWithoutGeneratedRules(profile *config.Profile) config.Profile {
	if profile == nil {
		return config.Profile{}
	}
	cp := *profile
	cp.Rules = append([]config.RuleConfig(nil), profile.Rules...)
	cp.RuleSets = append([]config.RuleSetConfig(nil), profile.RuleSets...)
	cp.RuleSubscriptions = append([]config.RuleSubscriptionConfig(nil), profile.RuleSubscriptions...)
	cp.PolicyGroups = append([]config.PolicyGroupConfig(nil), profile.PolicyGroups...)
	cp.Chains = append([]config.ChainConfig(nil), profile.Chains...)
	return cp
}

func compileProfileRules(configPath string, profile *config.Profile, defaultChainName string) (*rules.Engine, error) {
	known := knownChainNames(profile)
	knownGroups := knownPolicyGroupNames(profile)
	ruleSet := make([]rules.Rule, 0, len(profile.Rules))
	for _, rule := range profile.Rules {
		ruleSet = append(ruleSet, rules.Rule{
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
		})
	}
	resolvedRuleSets, _ := ruleset.Resolve(configPath, profile)
	return rules.CompileWithRuleSets(ruleSet, defaultChainName, known, knownGroups, resolvedRuleSets)
}

func knownChainNames(profile *config.Profile) map[string]struct{} {
	known := make(map[string]struct{}, len(profile.Chains))
	for _, ch := range profile.Chains {
		known[ch.Name] = struct{}{}
	}
	return known
}

func knownPolicyGroupNames(profile *config.Profile) map[string]struct{} {
	known := make(map[string]struct{}, len(profile.PolicyGroups))
	for _, group := range profile.PolicyGroups {
		known[group.Name] = struct{}{}
	}
	return known
}

func findChain(profile *config.Profile, name string) *config.ChainConfig {
	for i := range profile.Chains {
		if profile.Chains[i].Name == name {
			return &profile.Chains[i]
		}
	}
	return nil
}

func findPolicyGroup(profile *config.Profile, name string) *config.PolicyGroupConfig {
	for i := range profile.PolicyGroups {
		if profile.PolicyGroups[i].Name == name {
			return &profile.PolicyGroups[i]
		}
	}
	return nil
}

func selectPolicyGroupChain(profile *config.Profile, groupName, network string) (string, error) {
	group := findPolicyGroup(profile, groupName)
	if group == nil {
		return "", fmt.Errorf("policy group %q not found", groupName)
	}
	if strings.EqualFold(strings.TrimSpace(group.Type), "select") {
		selected := strings.TrimSpace(group.Selected)
		if selected == "" && len(group.Chains) > 0 {
			selected = group.Chains[0]
		}
		ch := findChain(profile, selected)
		if ch == nil {
			return "", fmt.Errorf("policy group %q selected missing chain %q", groupName, selected)
		}
		if network == "udp" && !chainCapabilities(*ch).UDP {
			return "", fmt.Errorf("policy group %q selected chain %q is not UDP-capable", groupName, selected)
		}
		return selected, nil
	}
	for _, chainName := range group.Chains {
		ch := findChain(profile, chainName)
		if ch == nil {
			continue
		}
		if network == "udp" && !chainCapabilities(*ch).UDP {
			continue
		}
		return chainName, nil
	}
	if network == "udp" {
		return "", fmt.Errorf("policy group %q has no UDP-capable member chains", groupName)
	}
	return "", fmt.Errorf("policy group %q has no member chains", groupName)
}

func populateRuleTestChain(profile *config.Profile, chainName string, resp *ruleTestResponse) {
	ch := findChain(profile, chainName)
	if ch == nil || resp == nil {
		return
	}
	caps := chainCapabilities(*ch)
	resp.Chain = &ruleTestChain{
		Name:         ch.Name,
		HopCount:     len(ch.Servers),
		Capabilities: caps,
	}
	resp.Hops = make([]serverPayload, 0, len(ch.Servers))
	for _, server := range ch.Servers {
		resp.Hops = append(resp.Hops, serverPayload{
			Name:         server.Name,
			Address:      server.Address,
			Protocol:     server.Protocol,
			Capabilities: protocol.CapabilitiesForProtocol(server.Protocol),
		})
	}
}
