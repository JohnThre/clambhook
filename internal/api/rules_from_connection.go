package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/rules"
	"github.com/JohnThre/clambhook/internal/traffic"
)

type createRuleFromConnectionRequest struct {
	ConnID   string `json:"conn_id"`
	Profile  string `json:"profile"`
	Name     string `json:"name"`
	Action   string `json:"action"`
	Scope    string `json:"scope"`
	Position string `json:"position"`
}

func (s *Server) handleCreateRuleFromConnection(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.configPath) == "" {
		http.Error(w, "rule persistence requires daemon config path", http.StatusConflict)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req createRuleFromConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if pos := strings.TrimSpace(req.Position); pos != "" && pos != "append" {
		http.Error(w, "position must be append", http.StatusBadRequest)
		return
	}
	connID := strings.TrimSpace(req.ConnID)
	if connID == "" {
		http.Error(w, "conn_id is required", http.StatusBadRequest)
		return
	}
	store := s.trafficStore()
	if store == nil {
		http.Error(w, "connection not found", http.StatusNotFound)
		return
	}
	conn, ok := store.Connection(connID)
	if !ok {
		http.Error(w, "connection not found", http.StatusNotFound)
		return
	}

	cfg := s.engine.Config()
	profileName := strings.TrimSpace(req.Profile)
	if profileName == "" {
		profileName = strings.TrimSpace(conn.Profile)
	}
	profile, err := selectAPIProfile(cfg, profileName)
	if err != nil {
		writeProfileSelectionError(w, err)
		return
	}
	rule, err := ruleFromConnection(profile, conn, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if name := strings.TrimSpace(req.Name); name != "" {
		rule.Name = name
	}

	resp, err := s.persistRules(profile.Name, func(existing []config.RuleConfig) []config.RuleConfig {
		rule.Name = uniqueRuleName(existing, rule.Name)
		return append(existing, rule)
	})
	if err != nil {
		writeRulePersistenceError(w, err)
		return
	}
	writeJSON(w, resp)
}

func ruleFromConnection(profile *config.Profile, conn traffic.Connection, req createRuleFromConnectionRequest) (config.RuleConfig, error) {
	return RuleFromConnection(profile, conn, req.Name, req.Action, req.Scope)
}

// RuleFromConnection derives a rule from a captured connection using the same
// action and scope semantics as the daemon API.
func RuleFromConnection(profile *config.Profile, conn traffic.Connection, name, actionRaw, scopeRaw string) (config.RuleConfig, error) {
	if profile == nil {
		return config.RuleConfig{}, fmt.Errorf("profile is required")
	}
	action, nameFamily, err := actionFromConnection(profile, conn, actionRaw)
	if err != nil {
		return config.RuleConfig{}, err
	}
	host := connectionRuleHost(conn)
	if host == "" {
		return config.RuleConfig{}, fmt.Errorf("connection has no ruleable host")
	}
	scope := strings.ToLower(strings.TrimSpace(scopeRaw))
	if scope == "" {
		scope = "auto"
	}
	ip, isIP := parseRuleHostIP(host)
	if scope == "auto" {
		if isIP {
			scope = "cidr"
		} else {
			scope = "exact_host"
		}
	}

	rule := config.RuleConfig{
		Name:   ruleNameForConnection(nameFamily, host),
		Action: action,
	}
	switch scope {
	case "exact_host":
		rule.Domains = []string{host}
	case "domain_suffix":
		if isIP {
			return config.RuleConfig{}, fmt.Errorf("domain_suffix scope requires a domain host")
		}
		rule.DomainSuffixes = []string{domainSuffixForRule(host)}
	case "cidr":
		if !isIP {
			return config.RuleConfig{}, fmt.Errorf("cidr scope requires an IP host")
		}
		bits := 32
		if ip.Is6() {
			bits = 128
		}
		rule.CIDRs = []string{ip.String() + "/" + strconv.Itoa(bits)}
	default:
		return config.RuleConfig{}, fmt.Errorf("scope must be auto, exact_host, domain_suffix, or cidr")
	}
	if name := strings.TrimSpace(name); name != "" {
		rule.Name = name
	}
	return rule, nil
}

func actionFromConnection(profile *config.Profile, conn traffic.Connection, raw string) (action, nameFamily string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "allow") {
		action, err := allowActionFromConnection(profile, conn)
		if err != nil {
			return "", "", err
		}
		return action, "allow", nil
	}
	lower := strings.ToLower(raw)
	switch {
	case lower == "direct" || lower == "block" || lower == "reject":
		return lower, actionFamilyForRuleName(lower), nil
	case strings.HasPrefix(lower, "chain:"):
		name := strings.TrimSpace(raw[len("chain:"):])
		if name == "" {
			return "", "", fmt.Errorf("chain action requires chain:<name>")
		}
		return "chain:" + name, "proxy", nil
	case strings.HasPrefix(lower, "group:"):
		name := strings.TrimSpace(raw[len("group:"):])
		if name == "" {
			return "", "", fmt.Errorf("group action requires group:<name>")
		}
		return "group:" + name, "proxy", nil
	default:
		return "", "", fmt.Errorf("action must be allow, direct, block, reject, chain:<name>, or group:<name>")
	}
}

func allowActionFromConnection(profile *config.Profile, conn traffic.Connection) (string, error) {
	action := strings.ToLower(strings.TrimSpace(conn.RuleAction))
	switch action {
	case "direct":
		return "direct", nil
	case "chain":
		if strings.TrimSpace(conn.ChainName) != "" {
			return "chain:" + strings.TrimSpace(conn.ChainName), nil
		}
	case "group":
		if strings.TrimSpace(conn.GroupName) != "" {
			return "group:" + strings.TrimSpace(conn.GroupName), nil
		}
	}
	if strings.TrimSpace(conn.GroupName) != "" {
		return "group:" + strings.TrimSpace(conn.GroupName), nil
	}
	if strings.TrimSpace(conn.ChainName) != "" {
		return "chain:" + strings.TrimSpace(conn.ChainName), nil
	}
	defaultChain, err := defaultChainForConnection(profile, conn)
	if err != nil {
		return "", err
	}
	return "chain:" + defaultChain, nil
}

func defaultChainForConnection(profile *config.Profile, conn traffic.Connection) (string, error) {
	if profile == nil || len(profile.Chains) == 0 {
		return "", fmt.Errorf("profile has no chains for allow rule")
	}
	switch strings.ToLower(strings.TrimSpace(conn.Listener.Protocol)) {
	case "socks5":
		if strings.TrimSpace(profile.Listen.SOCKS5Chain) != "" {
			return strings.TrimSpace(profile.Listen.SOCKS5Chain), nil
		}
	case "http":
		if strings.TrimSpace(profile.Listen.HTTPChain) != "" {
			return strings.TrimSpace(profile.Listen.HTTPChain), nil
		}
	case "tun":
		if profile.Listen.TUN != nil && strings.TrimSpace(profile.Listen.TUN.Chain) != "" {
			return strings.TrimSpace(profile.Listen.TUN.Chain), nil
		}
	}
	return profile.Chains[0].Name, nil
}

func connectionRuleHost(conn traffic.Connection) string {
	host := conn.TargetHost
	if host == "" && conn.Visibility != nil {
		host = conn.Visibility.Host
	}
	if host == "" {
		host, _ = rules.SplitTarget(conn.Target)
	}
	host, _ = rules.SplitTarget(host)
	return host
}

func parseRuleHostIP(host string) (netip.Addr, bool) {
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return ip, err == nil
}

func domainSuffixForRule(host string) string {
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return host
	}
	candidate := strings.Join(parts[len(parts)-2:], ".")
	if broadRuleSuffix(candidate) && len(parts) >= 3 {
		return strings.Join(parts[len(parts)-3:], ".")
	}
	return candidate
}

func broadRuleSuffix(suffix string) bool {
	switch suffix {
	case "co.uk", "com.au", "co.jp", "com.br", "com.cn", "com.sg", "co.nz":
		return true
	default:
		return false
	}
}

func ruleNameForConnection(family, host string) string {
	family = strings.ToLower(strings.TrimSpace(family))
	if family == "" {
		family = "rule"
	}
	token := strings.ToLower(strings.Trim(host, "[]"))
	replacer := strings.NewReplacer(".", "-", ":", "-", "_", "-", " ", "-")
	token = strings.Trim(replacer.Replace(token), "-")
	if token == "" {
		token = "connection"
	}
	return family + "-" + token
}

func actionFamilyForRuleName(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "direct":
		return "direct"
	case "block", "reject":
		return "block"
	default:
		return "proxy"
	}
}

func uniqueRuleName(existing []config.RuleConfig, base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "connection-rule"
	}
	used := make(map[string]struct{}, len(existing))
	for _, rule := range existing {
		used[rule.Name] = struct{}{}
	}
	if _, ok := used[base]; !ok {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if _, ok := used[candidate]; !ok {
			return candidate
		}
	}
}
