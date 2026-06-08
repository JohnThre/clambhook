package api

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/rules"
	"github.com/JohnThre/clambhook/internal/subscription"
)

const defaultDNSProxyTimeout = 5 * time.Second

type DNSPayload struct {
	Profile          string                     `json:"profile"`
	Strategy         string                     `json:"strategy"`
	Enabled          bool                       `json:"enabled"`
	Timeout          string                     `json:"timeout,omitempty"`
	Upstreams        []config.DNSUpstreamConfig `json:"upstreams"`
	InterceptsPort53 bool                       `json:"intercepts_port_53"`
	UpstreamRoutes   []DNSUpstreamRoutePayload  `json:"upstream_routes,omitempty"`
}

type DNSUpstreamRoutePayload struct {
	Name      string `json:"name,omitempty"`
	Protocol  string `json:"protocol"`
	Target    string `json:"target"`
	Network   string `json:"network"`
	Action    string `json:"action,omitempty"`
	ChainName string `json:"chain_name,omitempty"`
	GroupName string `json:"group_name,omitempty"`
	RuleName  string `json:"rule_name,omitempty"`
	Default   bool   `json:"default,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (s *Server) handleDNS(w http.ResponseWriter, r *http.Request) {
	cfg := s.engine.Config()
	profile, err := selectAPIProfile(cfg, r.URL.Query().Get("profile"))
	if err != nil {
		writeProfileSelectionError(w, err)
		return
	}
	writeJSON(w, DNSSnapshot(cfg, profile))
}

func DNSSnapshot(cfg *config.Config, profile *config.Profile) DNSPayload {
	if profile == nil {
		return DNSPayload{
			Strategy:  "route",
			Upstreams: []config.DNSUpstreamConfig{},
		}
	}
	payload := DNSPayload{
		Profile:   profile.Name,
		Strategy:  "route",
		Upstreams: []config.DNSUpstreamConfig{},
	}
	if !profile.DNS.Enabled {
		return payload
	}
	timeout := profile.DNS.Timeout.Std()
	if timeout == 0 {
		timeout = defaultDNSProxyTimeout
	}
	payload.Strategy = "encrypted"
	payload.Enabled = true
	payload.Timeout = timeout.String()
	payload.Upstreams = append([]config.DNSUpstreamConfig(nil), profile.DNS.Upstreams...)
	payload.InterceptsPort53 = true
	payload.UpstreamRoutes = dnsUpstreamRoutes(cfg, profile)
	return payload
}

func dnsUpstreamRoutes(cfg *config.Config, profile *config.Profile) []DNSUpstreamRoutePayload {
	if cfg == nil || profile == nil || len(profile.Chains) == 0 {
		return nil
	}
	effectiveProfile := subscription.ProfileWithCachedRules(cfg.Path, profile)
	engine, err := compileProfileRules(cfg.Path, &effectiveProfile, profile.Chains[0].Name)
	if err != nil {
		return []DNSUpstreamRoutePayload{{Error: err.Error()}}
	}
	rows := make([]DNSUpstreamRoutePayload, 0, len(profile.DNS.Upstreams))
	for _, upstream := range profile.DNS.Upstreams {
		target, network, err := dnsUpstreamTarget(upstream)
		row := DNSUpstreamRoutePayload{
			Name:     upstream.Name,
			Protocol: upstream.Protocol,
			Target:   target,
			Network:  network,
		}
		if err != nil {
			row.Error = err.Error()
			rows = append(rows, row)
			continue
		}
		decision := engine.Decide(network, target)
		row.Action = decision.Action
		row.ChainName = decision.ChainName
		row.GroupName = decision.GroupName
		row.RuleName = decision.RuleName
		row.Default = decision.Default
		if decision.Action == rules.ActionGroup {
			selected, selectErr := selectPolicyGroupChain(profile, decision.GroupName, network)
			if selectErr != nil {
				row.Error = selectErr.Error()
			} else {
				row.ChainName = selected
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func dnsUpstreamTarget(up config.DNSUpstreamConfig) (target, network string, err error) {
	protocol := strings.ToLower(strings.TrimSpace(up.Protocol))
	switch protocol {
	case "doh":
		parsed, err := url.Parse(up.URL)
		if err != nil {
			return "", "tcp", err
		}
		host := parsed.Host
		if _, _, err := net.SplitHostPort(host); err == nil {
			return host, "tcp", nil
		}
		return net.JoinHostPort(parsed.Hostname(), "443"), "tcp", nil
	case "dot":
		return up.Address, "tcp", nil
	case "doq":
		return up.Address, "udp", nil
	default:
		return "", "", nil
	}
}
