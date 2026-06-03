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
	for i := range p.Rules {
		errs = append(errs, validateRule(profileName, i, &p.Rules[i], chainNames)...)
	}
	subscriptionNames := make(map[string]struct{}, len(p.RuleSubscriptions))
	for i := range p.RuleSubscriptions {
		errs = append(errs, validateRuleSubscription(profileName, i, &p.RuleSubscriptions[i], subscriptionNames)...)
	}

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

func validateRule(profileName string, idx int, rule *RuleConfig, chainNames map[string]struct{}) []error {
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
		default:
			errs = append(errs, fmt.Errorf("%s action %q must be direct, block, reject, or chain:<name>", label, action))
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
	} {
		for j, raw := range field.vals {
			if strings.TrimSpace(raw) == "" {
				errs = append(errs, fmt.Errorf("%s %s[%d] must not be empty", label, field.name, j))
			} else if strings.TrimSpace(raw) != raw {
				errs = append(errs, fmt.Errorf("%s %s[%d] %q must not have surrounding whitespace", label, field.name, j, raw))
			}
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
