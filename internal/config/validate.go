package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// Validate checks structural configuration errors that can be detected
// without protocol-specific parsers or opening network resources.
func (c *Config) Validate() error {
	if c == nil {
		return errors.New("config is nil")
	}
	var errs []error
	if len(c.Profiles) == 0 {
		errs = append(errs, errors.New("at least one profile is required"))
	}

	profileNames := make(map[string]struct{}, len(c.Profiles))
	for i := range c.Profiles {
		p := &c.Profiles[i]
		name := strings.TrimSpace(p.Name)
		if name == "" {
			errs = append(errs, fmt.Errorf("profile %d: name is required", i))
		} else if name != p.Name {
			errs = append(errs, fmt.Errorf("profile %q must not have surrounding whitespace", p.Name))
		} else if _, exists := profileNames[name]; exists {
			errs = append(errs, fmt.Errorf("profile %q: duplicate profile name", name))
		} else {
			profileNames[name] = struct{}{}
		}
		errs = append(errs, validateProfile(p)...)
	}

	if active := strings.TrimSpace(c.Active); active != "" {
		if active != c.Active {
			errs = append(errs, fmt.Errorf("active profile %q must not have surrounding whitespace", c.Active))
		}
		if _, ok := profileNames[active]; !ok {
			errs = append(errs, fmt.Errorf("active profile %q not found", c.Active))
		}
	}
	errs = append(errs, validateDeveloperConfig(&c.Developer)...)
	return errors.Join(errs...)
}

func validateProfile(p *Profile) []error {
	var errs []error
	profileName := profileLabel(p.Name)

	chainNames := make(map[string]struct{}, len(p.Chains))
	for i := range p.Chains {
		ch := &p.Chains[i]
		name := strings.TrimSpace(ch.Name)
		if name == "" {
			errs = append(errs, fmt.Errorf("%s chain %d: name is required", profileName, i))
		} else if name != ch.Name {
			errs = append(errs, fmt.Errorf("%s chain %q must not have surrounding whitespace", profileName, ch.Name))
		} else if _, exists := chainNames[name]; exists {
			errs = append(errs, fmt.Errorf("%s chain %q: duplicate chain name", profileName, name))
		} else {
			chainNames[name] = struct{}{}
		}
		if len(ch.Servers) == 0 {
			errs = append(errs, fmt.Errorf("%s chain %q: at least one server is required", profileName, ch.Name))
		}
		for j := range ch.Servers {
			if strings.TrimSpace(ch.Servers[j].Protocol) == "" {
				errs = append(errs, fmt.Errorf("%s chain %q server %d: protocol is required", profileName, ch.Name, j))
			}
		}
	}
	groupNames := make(map[string]struct{}, len(p.PolicyGroups))
	for i := range p.PolicyGroups {
		errs = append(errs, validatePolicyGroup(profileName, i, &p.PolicyGroups[i], chainNames, groupNames)...)
	}
	ruleSetNames := make(map[string]struct{}, len(p.RuleSets))
	for i := range p.RuleSets {
		errs = append(errs, validateRuleSet(profileName, i, &p.RuleSets[i], ruleSetNames)...)
	}
	for i := range p.Rules {
		errs = append(errs, validateRule(profileName, i, &p.Rules[i], chainNames, groupNames, ruleSetNames)...)
	}
	subscriptionNames := make(map[string]struct{}, len(p.RuleSubscriptions))
	for i := range p.RuleSubscriptions {
		errs = append(errs, validateRuleSubscription(profileName, i, &p.RuleSubscriptions[i], subscriptionNames)...)
	}
	errs = append(errs, validateDNSConfig(profileName, &p.DNS)...)

	listen := p.Listen
	if listen.SOCKS5 != "" {
		errs = append(errs, validateListenAddr(profileName, "listen.socks5", listen.SOCKS5))
		errs = append(errs, validateChainRef(profileName, "listen.socks5_chain", listen.SOCKS5Chain, chainNames, len(p.Chains)))
	}
	if listen.SOCKS5MaxConns < 0 {
		errs = append(errs, fmt.Errorf("%s listen.socks5_max_connections must be >= 0", profileName))
	}
	if listen.SOCKS5HandshakeTimeout < 0 {
		errs = append(errs, fmt.Errorf("%s listen.socks5_handshake_timeout must be >= 0", profileName))
	}

	if listen.HTTP != "" {
		errs = append(errs, validateListenAddr(profileName, "listen.http", listen.HTTP))
		errs = append(errs, validateChainRef(profileName, "listen.http_chain", listen.HTTPChain, chainNames, len(p.Chains)))
	}
	if listen.HTTPMaxConns < 0 {
		errs = append(errs, fmt.Errorf("%s listen.http_max_connections must be >= 0", profileName))
	}
	if listen.HTTPHandshakeTimeout < 0 {
		errs = append(errs, fmt.Errorf("%s listen.http_handshake_timeout must be >= 0", profileName))
	}

	if tun := listen.TUN; tun != nil && tun.Enabled {
		if tun.MTU < 0 {
			errs = append(errs, fmt.Errorf("%s listen.tun.mtu must be >= 0", profileName))
		}
		errs = append(errs, validateChainRef(profileName, "listen.tun.chain", tun.Chain, chainNames, len(p.Chains)))
		for i, raw := range tun.Addresses {
			if _, err := netip.ParsePrefix(raw); err != nil {
				errs = append(errs, fmt.Errorf("%s listen.tun.addresses[%d] %q: %w", profileName, i, raw, err))
			}
		}
		for i, raw := range tun.Routes {
			if _, err := netip.ParsePrefix(raw); err != nil {
				errs = append(errs, fmt.Errorf("%s listen.tun.routes[%d] %q: %w", profileName, i, raw, err))
			}
		}
		for i, raw := range tun.ExcludeCIDRs {
			if _, err := netip.ParsePrefix(raw); err != nil {
				errs = append(errs, fmt.Errorf("%s listen.tun.exclude_cidrs[%d] %q: %w", profileName, i, raw, err))
			}
		}
	}

	if p.API.Listen != "" {
		errs = append(errs, validateListenAddr(profileName, "api.listen", p.API.Listen))
	}
	return errs
}

func validatePolicyGroup(profileName string, idx int, group *PolicyGroupConfig, chainNames, groupNames map[string]struct{}) []error {
	var errs []error
	label := fmt.Sprintf("%s policy_group %d", profileName, idx)
	name := strings.TrimSpace(group.Name)
	if name == "" {
		errs = append(errs, fmt.Errorf("%s: name is required", label))
	} else if name != group.Name {
		errs = append(errs, fmt.Errorf("%s name %q must not have surrounding whitespace", label, group.Name))
	} else if _, exists := groupNames[name]; exists {
		errs = append(errs, fmt.Errorf("%s name %q: duplicate policy group name", label, name))
	} else {
		groupNames[name] = struct{}{}
	}

	groupType := strings.ToLower(strings.TrimSpace(group.Type))
	if groupType == "" {
		errs = append(errs, fmt.Errorf("%s: type is required", label))
	} else if groupType != group.Type {
		errs = append(errs, fmt.Errorf("%s type %q must be lowercase without surrounding whitespace", label, group.Type))
	} else if !validPolicyGroupType(groupType) {
		errs = append(errs, fmt.Errorf("%s type %q must be select, url-test, fallback, load-balance, or smart", label, group.Type))
	}

	if len(group.Chains) == 0 {
		errs = append(errs, fmt.Errorf("%s chains: at least one chain is required", label))
	}
	seenChains := make(map[string]struct{}, len(group.Chains))
	for j, raw := range group.Chains {
		name := strings.TrimSpace(raw)
		if name == "" {
			errs = append(errs, fmt.Errorf("%s chains[%d] must not be empty", label, j))
			continue
		}
		if name != raw {
			errs = append(errs, fmt.Errorf("%s chains[%d] %q must not have surrounding whitespace", label, j, raw))
			continue
		}
		if _, ok := chainNames[name]; !ok {
			errs = append(errs, fmt.Errorf("%s chains[%d] references unknown chain %q", label, j, name))
			continue
		}
		if _, exists := seenChains[name]; exists {
			errs = append(errs, fmt.Errorf("%s chains[%d] duplicates chain %q", label, j, name))
			continue
		}
		seenChains[name] = struct{}{}
	}
	selected := strings.TrimSpace(group.Selected)
	if selected != "" {
		if selected != group.Selected {
			errs = append(errs, fmt.Errorf("%s selected %q must not have surrounding whitespace", label, group.Selected))
		} else if _, ok := seenChains[selected]; !ok {
			errs = append(errs, fmt.Errorf("%s selected %q must be one of chains", label, selected))
		}
	}
	if groupType == "select" && selected == "" && len(group.Chains) == 0 {
		errs = append(errs, fmt.Errorf("%s selected requires at least one chain", label))
	}

	if group.TestURL != "" {
		rawURL := strings.TrimSpace(group.TestURL)
		if rawURL != group.TestURL {
			errs = append(errs, fmt.Errorf("%s test_url %q must not have surrounding whitespace", label, group.TestURL))
		} else {
			parsed, err := url.Parse(rawURL)
			if err != nil || parsed.Host == "" {
				errs = append(errs, fmt.Errorf("%s test_url %q must be a valid http or https URL", label, rawURL))
			} else if parsed.Scheme != "http" && parsed.Scheme != "https" {
				errs = append(errs, fmt.Errorf("%s test_url %q must use http or https", label, rawURL))
			}
		}
	}
	if group.Interval < 0 {
		errs = append(errs, fmt.Errorf("%s interval must be >= 0", label))
	}
	if group.Timeout < 0 {
		errs = append(errs, fmt.Errorf("%s timeout must be >= 0", label))
	}
	return errs
}

func validPolicyGroupType(groupType string) bool {
	switch groupType {
	case "select", "url-test", "fallback", "load-balance", "smart":
		return true
	default:
		return false
	}
}

func validateRuleSet(profileName string, idx int, set *RuleSetConfig, names map[string]struct{}) []error {
	var errs []error
	label := fmt.Sprintf("%s rule_set %d", profileName, idx)
	name := strings.TrimSpace(set.Name)
	if name == "" {
		errs = append(errs, fmt.Errorf("%s: name is required", label))
	} else if name != set.Name {
		errs = append(errs, fmt.Errorf("%s name %q must not have surrounding whitespace", label, set.Name))
	} else if _, exists := names[name]; exists {
		errs = append(errs, fmt.Errorf("%s name %q: duplicate rule set name", label, name))
	} else {
		names[name] = struct{}{}
	}

	for _, field := range []struct {
		name string
		vals []string
	}{
		{name: "domains", vals: set.Domains},
		{name: "domain_suffixes", vals: set.DomainSuffixes},
		{name: "domain_keywords", vals: set.DomainKeywords},
	} {
		for j, raw := range field.vals {
			if strings.TrimSpace(raw) == "" {
				errs = append(errs, fmt.Errorf("%s %s[%d] must not be empty", label, field.name, j))
			} else if strings.TrimSpace(raw) != raw {
				errs = append(errs, fmt.Errorf("%s %s[%d] %q must not have surrounding whitespace", label, field.name, j, raw))
			}
		}
	}
	for j, raw := range set.CIDRs {
		if strings.TrimSpace(raw) != raw {
			errs = append(errs, fmt.Errorf("%s cidrs[%d] %q must not have surrounding whitespace", label, j, raw))
			continue
		}
		if _, err := netip.ParsePrefix(raw); err != nil {
			errs = append(errs, fmt.Errorf("%s cidrs[%d] %q: %w", label, j, raw, err))
		}
	}

	rawURL := strings.TrimSpace(set.URL)
	if rawURL != "" {
		if rawURL != set.URL {
			errs = append(errs, fmt.Errorf("%s url %q must not have surrounding whitespace", label, set.URL))
		} else {
			parsed, err := url.Parse(rawURL)
			if err != nil || parsed.Host == "" {
				errs = append(errs, fmt.Errorf("%s url %q must be a valid http or https URL", label, rawURL))
			} else if parsed.Scheme != "http" && parsed.Scheme != "https" {
				errs = append(errs, fmt.Errorf("%s url %q must use http or https", label, rawURL))
			}
		}
	}

	format := strings.ToLower(strings.TrimSpace(set.Format))
	if format != set.Format && set.Format != "" {
		errs = append(errs, fmt.Errorf("%s format %q must be lowercase without surrounding whitespace", label, set.Format))
	} else {
		switch format {
		case "", "auto", "plain", "hosts", "adblock":
		default:
			errs = append(errs, fmt.Errorf("%s format %q must be auto, plain, hosts, or adblock", label, set.Format))
		}
	}

	hasInline := len(set.Domains)+len(set.DomainSuffixes)+len(set.DomainKeywords)+len(set.CIDRs) > 0
	if !hasInline && rawURL == "" {
		errs = append(errs, fmt.Errorf("%s: at least one inline matcher or url is required", label))
	}
	return errs
}

func validateDNSConfig(profileName string, dns *DNSConfig) []error {
	if dns == nil {
		return nil
	}
	var errs []error
	if dns.Timeout < 0 {
		errs = append(errs, fmt.Errorf("%s dns.timeout must be >= 0", profileName))
	}
	if !dns.Enabled {
		return errs
	}
	if len(dns.Upstreams) == 0 {
		errs = append(errs, fmt.Errorf("%s dns: at least one upstream is required when enabled", profileName))
	}
	for i := range dns.Upstreams {
		errs = append(errs, validateDNSUpstream(profileName, i, &dns.Upstreams[i])...)
	}
	return errs
}

func validateDeveloperConfig(dev *DeveloperConfig) []error {
	if dev == nil {
		return nil
	}
	var errs []error
	if dev.CaptureLimit < 0 {
		errs = append(errs, errors.New("developer.capture_limit must be >= 0"))
	}
	if dev.BodyLimitBytes < 0 {
		errs = append(errs, errors.New("developer.body_limit_bytes must be >= 0"))
	}
	if dev.HeaderValueLimitBytes < 0 {
		errs = append(errs, errors.New("developer.header_value_limit_bytes must be >= 0"))
	}
	if strings.TrimSpace(dev.CACertPath) != dev.CACertPath {
		errs = append(errs, fmt.Errorf("developer.ca_cert_path %q must not have surrounding whitespace", dev.CACertPath))
	}
	if strings.TrimSpace(dev.CAKeyPath) != dev.CAKeyPath {
		errs = append(errs, fmt.Errorf("developer.ca_key_path %q must not have surrounding whitespace", dev.CAKeyPath))
	}
	for i, header := range dev.RedactHeaders {
		name := strings.TrimSpace(strings.ToLower(header))
		if name == "" {
			errs = append(errs, fmt.Errorf("developer.redact_headers[%d] must not be empty", i))
		} else if name != header {
			errs = append(errs, fmt.Errorf("developer.redact_headers[%d] %q must be lowercase without surrounding whitespace", i, header))
		}
	}
	return errs
}

func validateDNSUpstream(profileName string, idx int, up *DNSUpstreamConfig) []error {
	var errs []error
	label := fmt.Sprintf("%s dns.upstream %d", profileName, idx)
	if strings.TrimSpace(up.Name) != up.Name {
		errs = append(errs, fmt.Errorf("%s name %q must not have surrounding whitespace", label, up.Name))
	}
	protocol := strings.ToLower(strings.TrimSpace(up.Protocol))
	if protocol == "" {
		errs = append(errs, fmt.Errorf("%s: protocol is required", label))
	} else if protocol != up.Protocol {
		errs = append(errs, fmt.Errorf("%s protocol %q must be lowercase without surrounding whitespace", label, up.Protocol))
	}
	switch protocol {
	case "doh":
		rawURL := strings.TrimSpace(up.URL)
		if rawURL == "" {
			errs = append(errs, fmt.Errorf("%s url is required for doh", label))
		} else if rawURL != up.URL {
			errs = append(errs, fmt.Errorf("%s url %q must not have surrounding whitespace", label, up.URL))
		} else {
			parsed, err := url.Parse(rawURL)
			if err != nil || parsed.Host == "" {
				errs = append(errs, fmt.Errorf("%s url %q must be a valid https URL", label, rawURL))
			} else if parsed.Scheme != "https" {
				errs = append(errs, fmt.Errorf("%s url %q must use https", label, rawURL))
			}
		}
		if strings.TrimSpace(up.Address) != "" {
			errs = append(errs, fmt.Errorf("%s address is only valid for dot or doq", label))
		}
	case "dot", "doq":
		errs = append(errs, validateDNSUpstreamAddress(label, protocol, up.Address))
		if strings.TrimSpace(up.URL) != "" {
			errs = append(errs, fmt.Errorf("%s url is only valid for doh", label))
		}
	case "":
	default:
		errs = append(errs, fmt.Errorf("%s protocol %q must be doh, dot, or doq", label, up.Protocol))
	}
	if strings.TrimSpace(up.ServerName) != up.ServerName {
		errs = append(errs, fmt.Errorf("%s server_name %q must not have surrounding whitespace", label, up.ServerName))
	}
	for j, raw := range up.BootstrapIPs {
		if strings.TrimSpace(raw) != raw {
			errs = append(errs, fmt.Errorf("%s bootstrap_ips[%d] %q must not have surrounding whitespace", label, j, raw))
			continue
		}
		if _, err := netip.ParseAddr(raw); err != nil {
			errs = append(errs, fmt.Errorf("%s bootstrap_ips[%d] %q: %w", label, j, raw, err))
		}
	}
	return errs
}

func validateDNSUpstreamAddress(label, protocol, addr string) error {
	if strings.TrimSpace(addr) == "" {
		return fmt.Errorf("%s address is required for %s", label, protocol)
	}
	if strings.TrimSpace(addr) != addr {
		return fmt.Errorf("%s address %q must not have surrounding whitespace", label, addr)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%s address %q must be host:port: %w", label, addr, err)
	}
	if strings.ContainsAny(host, " \t\r\n") {
		return fmt.Errorf("%s address %q has invalid host whitespace", label, addr)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("%s address %q has non-numeric port %q", label, addr, port)
	}
	if n <= 0 || n > 65535 {
		return fmt.Errorf("%s address %q has port out of range", label, addr)
	}
	return nil
}

func validateRuleSubscription(profileName string, idx int, sub *RuleSubscriptionConfig, names map[string]struct{}) []error {
	var errs []error
	label := fmt.Sprintf("%s rule_subscription %d", profileName, idx)
	name := strings.TrimSpace(sub.Name)
	if name == "" {
		errs = append(errs, fmt.Errorf("%s: name is required", label))
	} else if name != sub.Name {
		errs = append(errs, fmt.Errorf("%s name %q must not have surrounding whitespace", label, sub.Name))
	} else if _, exists := names[name]; exists {
		errs = append(errs, fmt.Errorf("%s name %q: duplicate rule subscription name", label, name))
	} else {
		names[name] = struct{}{}
	}

	rawURL := strings.TrimSpace(sub.URL)
	if rawURL == "" {
		errs = append(errs, fmt.Errorf("%s: url is required", label))
	} else if rawURL != sub.URL {
		errs = append(errs, fmt.Errorf("%s url %q must not have surrounding whitespace", label, sub.URL))
	} else {
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Host == "" {
			errs = append(errs, fmt.Errorf("%s url %q must be a valid http or https URL", label, rawURL))
		} else if parsed.Scheme != "http" && parsed.Scheme != "https" {
			errs = append(errs, fmt.Errorf("%s url %q must use http or https", label, rawURL))
		}
	}

	format := strings.ToLower(strings.TrimSpace(sub.Format))
	if format != sub.Format && sub.Format != "" {
		errs = append(errs, fmt.Errorf("%s format %q must be lowercase without surrounding whitespace", label, sub.Format))
	} else {
		switch format {
		case "", "auto", "plain", "hosts", "adblock":
		default:
			errs = append(errs, fmt.Errorf("%s format %q must be auto, plain, hosts, or adblock", label, sub.Format))
		}
	}

	action := strings.ToLower(strings.TrimSpace(sub.Action))
	if action != sub.Action && sub.Action != "" {
		errs = append(errs, fmt.Errorf("%s action %q must be lowercase without surrounding whitespace", label, sub.Action))
	} else {
		switch action {
		case "", "block", "reject":
		default:
			errs = append(errs, fmt.Errorf("%s action %q must be block or reject", label, sub.Action))
		}
	}

	for j, raw := range sub.Networks {
		if strings.TrimSpace(raw) == "" {
			errs = append(errs, fmt.Errorf("%s networks[%d] must not be empty", label, j))
			continue
		}
		if strings.TrimSpace(raw) != raw {
			errs = append(errs, fmt.Errorf("%s networks[%d] %q must not have surrounding whitespace", label, j, raw))
			continue
		}
		switch strings.ToLower(raw) {
		case "tcp", "udp":
		default:
			errs = append(errs, fmt.Errorf("%s networks[%d] %q must be tcp or udp", label, j, raw))
		}
	}
	return errs
}

func validateRule(profileName string, idx int, rule *RuleConfig, chainNames, groupNames, ruleSetNames map[string]struct{}) []error {
	var errs []error
	label := fmt.Sprintf("%s rule %d", profileName, idx)
	if strings.TrimSpace(rule.Name) == "" {
		errs = append(errs, fmt.Errorf("%s: name is required", label))
	} else if strings.TrimSpace(rule.Name) != rule.Name {
		errs = append(errs, fmt.Errorf("%s name %q must not have surrounding whitespace", label, rule.Name))
	}
	action := strings.TrimSpace(rule.Action)
	if action == "" {
		errs = append(errs, fmt.Errorf("%s: action is required", label))
	} else if action != rule.Action {
		errs = append(errs, fmt.Errorf("%s action %q must not have surrounding whitespace", label, rule.Action))
	} else {
		lower := strings.ToLower(action)
		switch {
		case lower == "direct" || lower == "block" || lower == "reject":
		case strings.HasPrefix(lower, "chain:"):
			name := strings.TrimSpace(action[len("chain:"):])
			if name == "" {
				errs = append(errs, fmt.Errorf("%s action requires chain:<name>", label))
			} else if _, ok := chainNames[name]; !ok {
				errs = append(errs, fmt.Errorf("%s action references unknown chain %q", label, name))
			}
		case strings.HasPrefix(lower, "group:"):
			name := strings.TrimSpace(action[len("group:"):])
			if name == "" {
				errs = append(errs, fmt.Errorf("%s action requires group:<name>", label))
			} else if _, ok := groupNames[name]; !ok {
				errs = append(errs, fmt.Errorf("%s action references unknown policy group %q", label, name))
			}
		default:
			errs = append(errs, fmt.Errorf("%s action %q must be direct, block, reject, chain:<name>, or group:<name>", label, action))
		}
	}
	for _, field := range []struct {
		name string
		vals []string
	}{
		{name: "domains", vals: rule.Domains},
		{name: "domain_suffixes", vals: rule.DomainSuffixes},
		{name: "domain_keywords", vals: rule.DomainKeywords},
		{name: "networks", vals: rule.Networks},
		{name: "rule_sets", vals: rule.RuleSets},
	} {
		for j, raw := range field.vals {
			if strings.TrimSpace(raw) == "" {
				errs = append(errs, fmt.Errorf("%s %s[%d] must not be empty", label, field.name, j))
			} else if strings.TrimSpace(raw) != raw {
				errs = append(errs, fmt.Errorf("%s %s[%d] %q must not have surrounding whitespace", label, field.name, j, raw))
			}
		}
	}
	if len(rule.RuleSets) > 0 && (len(rule.Domains) > 0 || len(rule.DomainSuffixes) > 0 || len(rule.DomainKeywords) > 0 || len(rule.CIDRs) > 0) {
		errs = append(errs, fmt.Errorf("%s rule_sets cannot be combined with domains, domain_suffixes, domain_keywords, or cidrs", label))
	}
	for j, raw := range rule.RuleSets {
		name := strings.TrimSpace(raw)
		if name == "" || name != raw {
			continue
		}
		if _, ok := ruleSetNames[name]; !ok {
			errs = append(errs, fmt.Errorf("%s rule_sets[%d] references unknown rule set %q", label, j, name))
		}
	}
	for j, raw := range rule.Networks {
		switch strings.ToLower(raw) {
		case "tcp", "udp":
		default:
			errs = append(errs, fmt.Errorf("%s networks[%d] %q must be tcp or udp", label, j, raw))
		}
	}
	for j, raw := range rule.CIDRs {
		if strings.TrimSpace(raw) != raw {
			errs = append(errs, fmt.Errorf("%s cidrs[%d] %q must not have surrounding whitespace", label, j, raw))
			continue
		}
		if _, err := netip.ParsePrefix(raw); err != nil {
			errs = append(errs, fmt.Errorf("%s cidrs[%d] %q: %w", label, j, raw, err))
		}
	}
	for j, raw := range rule.SourceCIDRs {
		if strings.TrimSpace(raw) != raw {
			errs = append(errs, fmt.Errorf("%s source_cidrs[%d] %q must not have surrounding whitespace", label, j, raw))
			continue
		}
		if _, err := netip.ParsePrefix(raw); err != nil {
			errs = append(errs, fmt.Errorf("%s source_cidrs[%d] %q: %w", label, j, raw, err))
		}
	}
	for j, port := range rule.Ports {
		if port < 0 || port > 65535 {
			errs = append(errs, fmt.Errorf("%s ports[%d] has port out of range", label, j))
		}
	}
	return errs
}

func profileLabel(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "profile"
	}
	return fmt.Sprintf("profile %q", trimmed)
}

func validateChainRef(profileName, field, ref string, chainNames map[string]struct{}, chainCount int) error {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		if chainCount == 0 {
			return fmt.Errorf("%s %s requires at least one chain", profileName, field)
		}
		return nil
	}
	if trimmed != ref {
		return fmt.Errorf("%s %s %q must not have surrounding whitespace", profileName, field, ref)
	}
	if _, ok := chainNames[trimmed]; !ok {
		return fmt.Errorf("%s %s references unknown chain %q", profileName, field, ref)
	}
	return nil
}

func validateListenAddr(profileName, field, addr string) error {
	if strings.TrimSpace(addr) != addr {
		return fmt.Errorf("%s %s %q must not have surrounding whitespace", profileName, field, addr)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%s %s %q must be host:port: %w", profileName, field, addr, err)
	}
	if strings.ContainsAny(host, " \t\r\n") {
		return fmt.Errorf("%s %s %q has invalid host whitespace", profileName, field, addr)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("%s %s %q has non-numeric port %q", profileName, field, addr, port)
	}
	if n < 0 || n > 65535 {
		return fmt.Errorf("%s %s %q has port out of range", profileName, field, addr)
	}
	return nil
}
