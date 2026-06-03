package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
	"github.com/JohnThre/clambhook/internal/protocol"
	"github.com/JohnThre/clambhook/internal/rules"
	"github.com/JohnThre/clambhook/internal/subscription"
	"github.com/JohnThre/clambhook/internal/traffic"
)

const maxJSONRequestBytes = 1 << 20

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/status", s.handleStatus)
	mux.HandleFunc("GET /api/v1/profiles", s.handleProfiles)
	mux.HandleFunc("GET /api/v1/servers", s.handleServers)
	mux.HandleFunc("GET /api/v1/policy-groups", s.handlePolicyGroups)
	mux.HandleFunc("POST /api/v1/policy-groups/test", s.handlePolicyGroupTest)
	mux.HandleFunc("GET /api/v1/rules", s.handleRules)
	mux.HandleFunc("GET /api/v1/dns", s.handleDNS)
	mux.HandleFunc("POST /api/v1/rules", s.handleCreateRule)
	mux.HandleFunc("PUT /api/v1/rules", s.handleReplaceRules)
	mux.HandleFunc("POST /api/v1/rules/test", s.handleTestRule)
	mux.HandleFunc("GET /api/v1/rule-subscriptions", s.handleRuleSubscriptions)
	mux.HandleFunc("POST /api/v1/rule-subscriptions/refresh", s.handleRefreshRuleSubscriptions)
	mux.HandleFunc("GET /api/v1/decisions", s.handleDecisions)
	mux.HandleFunc("PUT /api/v1/profiles/active", s.handleSetActiveProfile)
	mux.HandleFunc("POST /api/v1/connect", s.handleConnect)
	mux.HandleFunc("POST /api/v1/disconnect", s.handleDisconnect)
	mux.HandleFunc("GET /api/v1/events", s.handleEvents)
	mux.HandleFunc("GET /api/v1/traffic", s.handleTraffic)
	mux.HandleFunc("GET /api/v1/developer/status", s.handleDeveloperStatus)
	mux.HandleFunc("GET /api/v1/developer/ca.pem", s.handleDeveloperCA)
	mux.HandleFunc("GET /api/v1/developer/entries", s.handleDeveloperEntries)
	mux.HandleFunc("GET /api/v1/developer/entries/{id}", s.handleDeveloperEntry)
	mux.HandleFunc("GET /api/v1/developer/har", s.handleDeveloperHAR)
	mux.HandleFunc("DELETE /api/v1/developer/entries", s.handleDeveloperClear)
}

type createRuleRequest struct {
	Profile  string            `json:"profile"`
	Rule     config.RuleConfig `json:"rule"`
	Position string            `json:"position"`
}

type replaceRulesRequest struct {
	Profile string              `json:"profile"`
	Rules   []config.RuleConfig `json:"rules"`
}

type testRuleRequest struct {
	Profile string `json:"profile"`
	Network string `json:"network"`
	Target  string `json:"target"`
}

type refreshRuleSubscriptionsRequest struct {
	Profile string   `json:"profile"`
	Names   []string `json:"names"`
}

type testRuleResponse struct {
	Profile  string          `json:"profile"`
	Decision rules.Decision  `json:"decision"`
	Chain    *testRuleChain  `json:"chain,omitempty"`
	Hops     []serverPayload `json:"hops,omitempty"`
}

type testRuleChain struct {
	Name         string                `json:"name"`
	HopCount     int                   `json:"hop_count"`
	Capabilities protocol.Capabilities `json:"capabilities"`
}

func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	cfg := s.engine.Config()
	profile, err := selectAPIProfile(cfg, r.URL.Query().Get("profile"))
	if err != nil {
		writeProfileSelectionError(w, err)
		return
	}
	manualRules, generatedRules, effectiveRules := subscription.EffectiveRules(cfg.Path, profile)
	writeJSON(w, map[string]any{
		"profile":         profile.Name,
		"rules":           manualRules,
		"generated_rules": generatedRules,
		"effective_rules": effectiveRules,
		"subscriptions":   subscription.Statuses(cfg.Path, profile),
	})
}

func (s *Server) handleTestRule(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req testRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	network := strings.ToLower(strings.TrimSpace(req.Network))
	if network != "tcp" && network != "udp" {
		http.Error(w, "network must be tcp or udp", http.StatusBadRequest)
		return
	}
	target := strings.TrimSpace(req.Target)
	if err := validateRuleTestTarget(target); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := s.engine.Config()
	profile, err := selectRuleTestProfile(cfg, req.Profile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if len(profile.Chains) == 0 {
		http.Error(w, "profile has no chains", http.StatusBadRequest)
		return
	}
	effectiveProfile := subscription.ProfileWithCachedRules(cfg.Path, profile)
	ruleEngine, err := compileProfileRules(&effectiveProfile, profile.Chains[0].Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	decision := ruleEngine.Decide(network, target)
	resp := testRuleResponse{
		Profile:  profile.Name,
		Decision: decision,
	}
	if decision.Action == rules.ActionChain {
		populateRuleTestChain(profile, decision.ChainName, &resp)
	}
	if decision.Action == rules.ActionGroup {
		selected := ""
		ok := false
		var err error
		if s.engine.Status().Profile == profile.Name {
			selected, ok, err = s.engine.SelectedPolicyChain(decision.GroupName, network)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		if !ok || selected == "" {
			selected, err = selectPolicyGroupChain(profile, decision.GroupName, network)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		resp.Decision.ChainName = selected
		populateRuleTestChain(profile, selected, &resp)
	}
	writeJSON(w, resp)
}

func (s *Server) handleRuleSubscriptions(w http.ResponseWriter, r *http.Request) {
	cfg := s.engine.Config()
	payload, err := subscription.StatusPayloadForProfile(cfg, r.URL.Query().Get("profile"))
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, payload)
}

func (s *Server) handleRefreshRuleSubscriptions(w http.ResponseWriter, r *http.Request) {
	configPath := strings.TrimSpace(s.configPath)
	if configPath == "" {
		if cfg := s.engine.Config(); cfg != nil {
			configPath = strings.TrimSpace(cfg.Path)
		}
	}
	if configPath == "" {
		http.Error(w, "rule subscription refresh requires daemon config path", http.StatusConflict)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req refreshRuleSubscriptionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	currentProfile := strings.TrimSpace(s.engine.Status().Profile)
	if currentProfile != "" {
		cfg.Active = currentProfile
	}
	payload, err := subscription.RefreshProfile(r.Context(), cfg, req.Profile, req.Names, nil)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	if err := s.engine.Reload(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, payload)
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
	return selectAPIProfile(cfg, name)
}

func selectAPIProfile(cfg *config.Config, name string) (*config.Profile, error) {
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

func writeProfileSelectionError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if strings.Contains(err.Error(), "not found") {
		status = http.StatusNotFound
	} else if strings.Contains(err.Error(), "active profile") {
		status = http.StatusBadRequest
	}
	http.Error(w, err.Error(), status)
}

func compileProfileRules(profile *config.Profile, defaultChainName string) (*rules.Engine, error) {
	known := make(map[string]struct{}, len(profile.Chains))
	for _, ch := range profile.Chains {
		known[ch.Name] = struct{}{}
	}
	knownGroups := make(map[string]struct{}, len(profile.PolicyGroups))
	for _, group := range profile.PolicyGroups {
		knownGroups[group.Name] = struct{}{}
	}
	ruleSet := make([]rules.Rule, 0, len(profile.Rules))
	for _, rule := range profile.Rules {
		ruleSet = append(ruleSet, rules.Rule{
			Name:           rule.Name,
			Action:         rule.Action,
			Domains:        rule.Domains,
			DomainSuffixes: rule.DomainSuffixes,
			DomainKeywords: rule.DomainKeywords,
			CIDRs:          rule.CIDRs,
			Ports:          rule.Ports,
			Networks:       rule.Networks,
		})
	}
	return rules.Compile(ruleSet, defaultChainName, known, knownGroups)
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

func populateRuleTestChain(profile *config.Profile, chainName string, resp *testRuleResponse) {
	ch := findChain(profile, chainName)
	if ch == nil || resp == nil {
		return
	}
	caps := chainCapabilities(*ch)
	resp.Chain = &testRuleChain{
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

func (s *Server) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.configPath) == "" {
		http.Error(w, "rule persistence requires daemon config path", http.StatusConflict)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req createRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if pos := strings.TrimSpace(req.Position); pos != "" && pos != "append" {
		http.Error(w, "position must be append", http.StatusBadRequest)
		return
	}
	resp, err := s.persistRules(req.Profile, func(existing []config.RuleConfig) []config.RuleConfig {
		return append(existing, req.Rule)
	})
	if err != nil {
		writeRulePersistenceError(w, err)
		return
	}
	writeJSON(w, resp)
}

func (s *Server) handleReplaceRules(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.configPath) == "" {
		http.Error(w, "rule persistence requires daemon config path", http.StatusConflict)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req replaceRulesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := s.persistRules(req.Profile, func([]config.RuleConfig) []config.RuleConfig {
		return append([]config.RuleConfig(nil), req.Rules...)
	})
	if err != nil {
		writeRulePersistenceError(w, err)
		return
	}
	writeJSON(w, resp)
}

type rulePersistenceResponse struct {
	Profile    string              `json:"profile"`
	Rules      []config.RuleConfig `json:"rules"`
	BackupPath string              `json:"backup_path"`
}

type rulePersistenceError struct {
	status int
	err    error
}

func (e rulePersistenceError) Error() string { return e.err.Error() }

func (s *Server) persistRules(profileName string, nextRules func([]config.RuleConfig) []config.RuleConfig) (rulePersistenceResponse, error) {
	cfg, err := config.Load(s.configPath)
	if err != nil {
		return rulePersistenceResponse{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
	}

	currentProfile := strings.TrimSpace(s.engine.Status().Profile)
	if currentProfile != "" {
		cfg.Active = currentProfile
	}
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		profileName = cfg.Active
	}
	profile, ok := cfg.ProfileByName(profileName)
	if !ok {
		return rulePersistenceResponse{}, rulePersistenceError{status: http.StatusNotFound, err: fmt.Errorf("profile not found")}
	}
	profile.Rules = nextRules(profile.Rules)

	if err := engine.ValidateConfig(cfg); err != nil {
		return rulePersistenceResponse{}, rulePersistenceError{status: http.StatusBadRequest, err: err}
	}
	result, err := config.WriteAtomicWithBackup(s.configPath, cfg)
	if err != nil {
		return rulePersistenceResponse{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
	}
	if err := s.engine.Reload(cfg); err != nil {
		return rulePersistenceResponse{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
	}
	return rulePersistenceResponse{
		Profile:    profile.Name,
		Rules:      profile.Rules,
		BackupPath: result.BackupPath,
	}, nil
}

func writeRulePersistenceError(w http.ResponseWriter, err error) {
	var ruleErr rulePersistenceError
	if errors.As(err, &ruleErr) {
		http.Error(w, ruleErr.err.Error(), ruleErr.status)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func (s *Server) handleDecisions(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		limit = n
	}
	store := s.trafficStore()
	if store == nil {
		writeJSON(w, map[string]any{
			"updated_ts_ns": 0,
			"decisions":     []any{},
		})
		return
	}
	snapshot := store.Snapshot("all", limit)
	decisions := make([]traffic.Connection, 0, len(snapshot.Connections))
	for _, conn := range snapshot.Connections {
		if conn.RuleAction != "" {
			decisions = append(decisions, conn)
		}
	}
	writeJSON(w, map[string]any{
		"updated_ts_ns": snapshot.UpdatedTsNs,
		"decisions":     decisions,
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.engine.Status())
}

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	cfg := s.engine.Config()
	writeJSON(w, map[string]any{
		"profiles": cfg.ProfileNames(),
		"active":   cfg.Active,
	})
}

func (s *Server) handleSetActiveProfile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.engine.SetActiveProfile(req.Name); err != nil {
		// "not found" is the only user-correctable case.
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, s.engine.Status())
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.Start(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, s.engine.Status())
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.Stop(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, s.engine.Status())
}

func (s *Server) handleTraffic(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	state := query.Get("state")
	limit := 200
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		limit = n
	}
	var profileNames []string
	activeProfile := ""
	activeRules := []config.RuleConfig(nil)
	if s.engine != nil {
		cfg := s.engine.Config()
		activeProfile = cfg.Active
		profileNames = cfg.ProfileNames()
		if profile, err := cfg.ActiveProfile(); err == nil {
			activeProfile = profile.Name
			activeRules = profile.Rules
		}
	}
	opts := traffic.SnapshotOptions{
		State:         state,
		Limit:         limit,
		Action:        query.Get("action"),
		Profile:       query.Get("profile"),
		Rule:          query.Get("rule"),
		Country:       query.Get("country"),
		Port:          query.Get("port"),
		Query:         query.Get("query"),
		ActiveProfile: activeProfile,
		Profiles:      profileNames,
		Rules:         activeRules,
	}
	store := s.trafficStore()
	if store == nil {
		var empty *traffic.Store
		writeJSON(w, empty.SnapshotWithOptions(opts))
		return
	}
	writeJSON(w, store.SnapshotWithOptions(opts))
}

func (s *Server) handleDeveloperStatus(w http.ResponseWriter, r *http.Request) {
	dev := s.developerManager()
	if dev == nil {
		writeJSON(w, map[string]any{"enabled": false})
		return
	}
	writeJSON(w, dev.Status())
}

func (s *Server) handleDeveloperCA(w http.ResponseWriter, r *http.Request) {
	dev := s.developerManager()
	if dev == nil {
		http.Error(w, "developer mode disabled", http.StatusNotImplemented)
		return
	}
	cert, ok := dev.CACertPEM()
	if !ok {
		http.Error(w, "developer MITM CA unavailable", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="clambhook-developer-ca.pem"`)
	_, _ = w.Write(cert)
}

func (s *Server) handleDeveloperEntries(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		limit = n
	}
	dev := s.developerManager()
	if dev == nil {
		writeJSON(w, map[string]any{"entries": []any{}})
		return
	}
	writeJSON(w, map[string]any{
		"entries": dev.List(limit),
	})
}

func (s *Server) handleDeveloperEntry(w http.ResponseWriter, r *http.Request) {
	dev := s.developerManager()
	if dev == nil {
		http.Error(w, "developer mode disabled", http.StatusNotImplemented)
		return
	}
	entry, ok := dev.Get(r.PathValue("id"))
	if !ok {
		http.Error(w, "developer entry not found", http.StatusNotFound)
		return
	}
	writeJSON(w, entry)
}

func (s *Server) handleDeveloperHAR(w http.ResponseWriter, r *http.Request) {
	dev := s.developerManager()
	if dev == nil {
		writeJSON(w, map[string]any{"log": map[string]any{"version": "1.2", "entries": []any{}}})
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="clambhook.har"`)
	writeJSON(w, dev.HAR())
}

func (s *Server) handleDeveloperClear(w http.ResponseWriter, r *http.Request) {
	dev := s.developerManager()
	if dev != nil {
		dev.Clear()
	}
	writeJSON(w, map[string]any{"cleared": true})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
