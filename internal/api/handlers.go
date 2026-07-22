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
	"github.com/JohnThre/clambhook/internal/developer"
	"github.com/JohnThre/clambhook/internal/engine"
	"github.com/JohnThre/clambhook/internal/policy"
	"github.com/JohnThre/clambhook/internal/protocol"
	"github.com/JohnThre/clambhook/internal/rules"
	"github.com/JohnThre/clambhook/internal/ruleset"
	"github.com/JohnThre/clambhook/internal/subscription"
	"github.com/JohnThre/clambhook/internal/traffic"
)

const maxJSONRequestBytes = 1 << 20

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/status", s.handleStatus)
	mux.HandleFunc("GET /api/v1/profiles", s.handleProfiles)
	mux.HandleFunc("GET /api/v1/servers", s.handleServers)
	mux.HandleFunc("GET /api/v1/policy-groups", s.handlePolicyGroups)
	mux.HandleFunc("PUT /api/v1/policy-groups", s.handleReplacePolicyGroups)
	mux.HandleFunc("PUT /api/v1/policy-groups/selection", s.handlePolicyGroupSelection)
	mux.HandleFunc("POST /api/v1/policy-groups/test", s.handlePolicyGroupTest)
	mux.HandleFunc("GET /api/v1/rule-sets", s.handleRuleSets)
	mux.HandleFunc("PUT /api/v1/rule-sets", s.handleReplaceRuleSets)
	mux.HandleFunc("POST /api/v1/rule-sets/refresh", s.handleRefreshRuleSets)
	mux.HandleFunc("GET /api/v1/rules", s.handleRules)
	mux.HandleFunc("GET /api/v1/dns", s.handleDNS)
	mux.HandleFunc("PUT /api/v1/dns", s.handleUpdateDNS)
	mux.HandleFunc("GET /api/v1/config/export", s.handleExportConfig)
	mux.HandleFunc("POST /api/v1/config/import", s.handleImportConfig)
	mux.HandleFunc("GET /api/v1/config/settings", s.handleConfigSettings)
	mux.HandleFunc("PUT /api/v1/config/settings", s.handleUpdateConfigSettings)
	mux.HandleFunc("POST /api/v1/rules", s.handleCreateRule)
	mux.HandleFunc("POST /api/v1/rules/cleanup", s.handleCleanupRule)
	mux.HandleFunc("POST /api/v1/rules/from-connection", s.handleCreateRuleFromConnection)
	mux.HandleFunc("GET /api/v1/rules/temporary", s.handleTemporaryRules)
	mux.HandleFunc("POST /api/v1/rules/temporary/from-connection", s.handleCreateTemporaryRuleFromConnection)
	mux.HandleFunc("DELETE /api/v1/rules/temporary/{id}", s.handleDeleteTemporaryRule)
	mux.HandleFunc("GET /api/v1/prompts/pending", s.handlePendingPrompts)
	mux.HandleFunc("POST /api/v1/prompts/{id}/resolve", s.handleResolvePrompt)
	mux.HandleFunc("PUT /api/v1/rules", s.handleReplaceRules)
	mux.HandleFunc("POST /api/v1/rules/test", s.handleTestRule)
	mux.HandleFunc("POST /api/v1/routes/explain", s.handleExplainRoute)
	mux.HandleFunc("GET /api/v1/rule-subscriptions", s.handleRuleSubscriptions)
	mux.HandleFunc("PUT /api/v1/rule-subscriptions", s.handleReplaceRuleSubscriptions)
	mux.HandleFunc("POST /api/v1/rule-subscriptions/refresh", s.handleRefreshRuleSubscriptions)
	mux.HandleFunc("GET /api/v1/decisions", s.handleDecisions)
	mux.HandleFunc("PUT /api/v1/profiles/active", s.handleSetActiveProfile)
	mux.HandleFunc("POST /api/v1/connect", s.handleConnect)
	mux.HandleFunc("POST /api/v1/disconnect", s.handleDisconnect)
	mux.HandleFunc("GET /api/v1/events", s.handleEvents)
	mux.HandleFunc("GET /api/v1/traffic", s.handleTraffic)
	mux.HandleFunc("GET /api/v1/developer/status", s.handleDeveloperStatus)
	mux.HandleFunc("GET /api/v1/developer/settings", s.handleDeveloperSettings)
	mux.HandleFunc("PUT /api/v1/developer/settings", s.handleUpdateDeveloperSettings)
	mux.HandleFunc("GET /api/v1/developer/ca.pem", s.handleDeveloperCA)
	mux.HandleFunc("POST /api/v1/developer/ca/regenerate", s.handleDeveloperRegenerateCA)
	mux.HandleFunc("GET /api/v1/developer/entries", s.handleDeveloperEntries)
	mux.HandleFunc("GET /api/v1/developer/entries/{id}", s.handleDeveloperEntry)
	mux.HandleFunc("GET /api/v1/developer/har", s.handleDeveloperHAR)
	mux.HandleFunc("POST /api/v1/developer/repeat", s.handleDeveloperRepeat)
	mux.HandleFunc("GET /api/v1/developer/map-rules", s.handleDeveloperMapRules)
	mux.HandleFunc("PUT /api/v1/developer/map-rules", s.handleDeveloperReplaceMapRules)
	mux.HandleFunc("DELETE /api/v1/developer/map-rules/{id}", s.handleDeveloperDeleteMapRule)
	mux.HandleFunc("GET /api/v1/developer/breakpoint-rules", s.handleDeveloperBreakpointRules)
	mux.HandleFunc("PUT /api/v1/developer/breakpoint-rules", s.handleDeveloperReplaceBreakpointRules)
	mux.HandleFunc("DELETE /api/v1/developer/breakpoint-rules/{id}", s.handleDeveloperDeleteBreakpointRule)
	mux.HandleFunc("GET /api/v1/developer/breakpoints/pending", s.handleDeveloperPendingBreakpoints)
	mux.HandleFunc("POST /api/v1/developer/breakpoints/{id}/resolve", s.handleDeveloperResolveBreakpoint)
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
	Source  string `json:"source"`
}

type refreshRuleSubscriptionsRequest struct {
	Profile string   `json:"profile"`
	Names   []string `json:"names"`
}

type refreshRuleSetsRequest struct {
	Profile string   `json:"profile"`
	Names   []string `json:"names"`
}

type replaceRuleSetsRequest struct {
	Profile  string                 `json:"profile"`
	RuleSets []config.RuleSetConfig `json:"rule_sets"`
}

type replacePolicyGroupsRequest struct {
	Profile      string                     `json:"profile"`
	PolicyGroups []config.PolicyGroupConfig `json:"policy_groups"`
}

type replaceRuleSubscriptionsRequest struct {
	Profile       string                          `json:"profile"`
	Subscriptions []config.RuleSubscriptionConfig `json:"subscriptions"`
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
		"rule_sets":       rulesetStatusRows(cfg.Path, profile),
		"subscriptions":   subscription.Statuses(cfg.Path, profile),
	})
}

func rulesetStatusRows(configPath string, profile *config.Profile) []ruleset.Status {
	_, statuses := ruleset.Resolve(configPath, profile)
	return statuses
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
	resp, err := s.explainRouteForProfile(cfg, profile, &effectiveProfile, network, target, strings.TrimSpace(req.Source))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, resp)
}

func (s *Server) handleExplainRoute(w http.ResponseWriter, r *http.Request) {
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
	resp, err := s.explainRouteForProfile(cfg, profile, &effectiveProfile, network, target, strings.TrimSpace(req.Source))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
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

func (s *Server) handleReplaceRuleSubscriptions(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.configPath) == "" {
		http.Error(w, "rule subscription persistence requires daemon config path", http.StatusConflict)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req replaceRuleSubscriptionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := s.persistRuleSubscriptions(req.Profile, req.Subscriptions)
	if err != nil {
		writeRulePersistenceError(w, err)
		return
	}
	writeJSON(w, resp)
}

func (s *Server) handleRuleSets(w http.ResponseWriter, r *http.Request) {
	cfg := s.engine.Config()
	profile, err := selectAPIProfile(cfg, r.URL.Query().Get("profile"))
	if err != nil {
		writeProfileSelectionError(w, err)
		return
	}
	_, statuses := ruleset.Resolve(cfg.Path, profile)
	writeJSON(w, map[string]any{
		"profile":   profile.Name,
		"rule_sets": append([]config.RuleSetConfig(nil), profile.RuleSets...),
		"statuses":  statuses,
	})
}

func (s *Server) handleReplaceRuleSets(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.configPath) == "" {
		http.Error(w, "rule set persistence requires daemon config path", http.StatusConflict)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req replaceRuleSetsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := s.persistRuleSets(req.Profile, req.RuleSets)
	if err != nil {
		writeRulePersistenceError(w, err)
		return
	}
	writeJSON(w, resp)
}

func (s *Server) handleRefreshRuleSets(w http.ResponseWriter, r *http.Request) {
	configPath := strings.TrimSpace(s.configPath)
	if configPath == "" {
		if cfg := s.engine.Config(); cfg != nil {
			configPath = strings.TrimSpace(cfg.Path)
		}
	}
	if configPath == "" {
		http.Error(w, "rule set refresh requires daemon config path", http.StatusConflict)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req refreshRuleSetsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Fetch remote data while NOT holding the config transaction lock. Slow
	// fetches must not block other config edits, and the fetched cache files
	// on disk are already atomically replaced by ruleset.RefreshProfile.
	fetchCfg, err := config.Load(configPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	currentProfile := strings.TrimSpace(s.engine.Status().Profile)
	if currentProfile != "" {
		fetchCfg.Active = currentProfile
	}
	payload, err := ruleset.RefreshProfile(r.Context(), fetchCfg, req.Profile, req.Names, s.httpClient)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}

	// Finalize: reload the latest disk config under the serializing lock so a
	// concurrent edit committed during the fetch is not overwritten by the
	// stale pre-fetch config, then write and reload the engine.
	defer s.lockConfigTxn()()
	cfg, err := config.Load(configPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if currentProfile != "" {
		cfg.Active = currentProfile
	}
	profile, ok := cfg.ProfileByName(payload.Profile)
	if !ok {
		// The profile could have been renamed or deleted while we were
		// unlocked; fall back to reporting the payload we fetched.
		profile, _ = fetchCfg.ProfileByName(payload.Profile)
	}
	if profile != nil {
		// Ensure the returned config snapshot reflects any rule-set entries
		// that were present on disk before the fetch (the cache files were
		// already updated atomically by the fetch).
		_ = profile
	}
	if err := engine.ValidateConfig(cfg); err != nil {
		http.Error(w, "validate config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	result, err := config.WriteAtomicWithBackup(configPath, cfg)
	if err != nil {
		http.Error(w, "write config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.engine.Reload(cfg); err != nil {
		http.Error(w, "reload engine: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"profile":    payload.Profile,
		"rule_sets":  append([]config.RuleSetConfig(nil), profile.RuleSets...),
		"statuses":   payload.RuleSets,
		"backup_path": result.BackupPath,
	})
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

	// Fetch remote data while NOT holding the config transaction lock. Slow
	// fetches must not block other config edits; subscription cache files are
	// atomically replaced by subscription.RefreshProfile.
	fetchCfg, err := config.Load(configPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	currentProfile := strings.TrimSpace(s.engine.Status().Profile)
	if currentProfile != "" {
		fetchCfg.Active = currentProfile
	}
	payload, err := subscription.RefreshProfile(r.Context(), fetchCfg, req.Profile, req.Names, s.httpClient)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}

	// Finalize: reload the latest disk config under the serializing lock so a
	// concurrent edit committed during the fetch is not overwritten by the
	// stale pre-fetch config, then write and reload the engine.
	defer s.lockConfigTxn()()
	cfg, err := config.Load(configPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if currentProfile != "" {
		cfg.Active = currentProfile
	}
	if _, ok := cfg.ProfileByName(payload.Profile); !ok {
		// The profile could have been renamed or deleted while we were
		// unlocked. Report the fetched payload but do not write anything.
		writeJSON(w, payload)
		return
	}
	if err := engine.ValidateConfig(cfg); err != nil {
		http.Error(w, "validate config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	result, err := config.WriteAtomicWithBackup(configPath, cfg)
	if err != nil {
		http.Error(w, "write config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.engine.Reload(cfg); err != nil {
		http.Error(w, "reload engine: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"profile":     payload.Profile,
		"subscriptions": payload.Subscriptions,
		"backup_path": result.BackupPath,
	})
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

func (s *Server) explainRouteForProfile(cfg *config.Config, profile, effectiveProfile *config.Profile, network, target, source string) (testRuleResponse, error) {
	defaultChainName := profile.Chains[0].Name
	var decision rules.Decision
	if manager := s.temporaryRules(); manager != nil {
		tempDecision, ok, err := manager.Decide(profile.Name, defaultChainName, network, target, source, "", "", knownChainNames(profile), knownPolicyGroupNames(profile))
		if err != nil {
			return testRuleResponse{}, err
		}
		if ok {
			decision = tempDecision
		}
	}
	if decision.Action == "" {
		ruleEngine, err := compileProfileRules(cfg.Path, effectiveProfile, defaultChainName)
		if err != nil {
			return testRuleResponse{}, err
		}
		decision = ruleEngine.DecideWithSource(network, target, source)
	}
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
			selected, ok, err = s.engine.SelectedPolicyChain(decision.GroupName, policy.SelectionContext{
				Network: network,
				Target:  target,
				Source:  source,
			})
			if err != nil {
				return testRuleResponse{}, err
			}
		}
		if !ok || selected == "" {
			selected, err = selectPolicyGroupChain(profile, decision.GroupName, network)
			if err != nil {
				return testRuleResponse{}, err
			}
		}
		resp.Decision.ChainName = selected
		populateRuleTestChain(profile, selected, &resp)
	}
	return resp, nil
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

func compileProfileRules(configPath string, profile *config.Profile, defaultChainName string) (*rules.Engine, error) {
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
	ruleSets, _ := ruleset.Resolve(configPath, profile)
	return rules.CompileWithRuleSets(ruleSet, defaultChainName, known, knownGroups, ruleSets)
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

type ruleSetPersistenceResponse struct {
	Profile    string                 `json:"profile"`
	RuleSets   []config.RuleSetConfig `json:"rule_sets"`
	BackupPath string                 `json:"backup_path"`
}

type policyGroupPersistenceResponse struct {
	Profile      string                     `json:"profile"`
	PolicyGroups []config.PolicyGroupConfig `json:"policy_groups"`
	BackupPath   string                     `json:"backup_path"`
}

type ruleSubscriptionPersistenceResponse struct {
	Profile       string                          `json:"profile"`
	Subscriptions []config.RuleSubscriptionConfig `json:"subscriptions"`
	BackupPath    string                          `json:"backup_path"`
}

type developerMapRulesRequest struct {
	Rules []config.DeveloperMapRuleConfig `json:"rules"`
}

type developerBreakpointRulesRequest struct {
	Rules []config.DeveloperBreakpointRuleConfig `json:"rules"`
}

type developerRulesPersistenceResponse struct {
	Developer  config.DeveloperConfig `json:"developer"`
	BackupPath string                 `json:"backup_path"`
}

type rulePersistenceError struct {
	status int
	err    error
}

func (e rulePersistenceError) Error() string { return e.err.Error() }

func (s *Server) persistRules(profileName string, nextRules func([]config.RuleConfig) []config.RuleConfig) (rulePersistenceResponse, error) {
	return s.persistRulesWithError(profileName, func(_ string, existing []config.RuleConfig) ([]config.RuleConfig, error) {
		return nextRules(existing), nil
	})
}

func (s *Server) persistRulesWithError(profileName string, nextRules func(string, []config.RuleConfig) ([]config.RuleConfig, error)) (rulePersistenceResponse, error) {
	// Serialize the whole read-modify-validate-write-reload transaction.
	defer s.lockConfigTxn()()
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
	rules, err := nextRules(profile.Name, append([]config.RuleConfig(nil), profile.Rules...))
	if err != nil {
		return rulePersistenceResponse{}, err
	}
	profile.Rules = rules

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

func (s *Server) persistRuleSets(profileName string, nextRuleSets []config.RuleSetConfig) (ruleSetPersistenceResponse, error) {
	// Serialize the whole read-modify-validate-write-reload transaction.
	defer s.lockConfigTxn()()
	cfg, err := config.Load(s.configPath)
	if err != nil {
		return ruleSetPersistenceResponse{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
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
		return ruleSetPersistenceResponse{}, rulePersistenceError{status: http.StatusNotFound, err: fmt.Errorf("profile not found")}
	}
	profile.RuleSets = append([]config.RuleSetConfig(nil), nextRuleSets...)
	if err := engine.ValidateConfig(cfg); err != nil {
		return ruleSetPersistenceResponse{}, rulePersistenceError{status: http.StatusBadRequest, err: err}
	}
	result, err := config.WriteAtomicWithBackup(s.configPath, cfg)
	if err != nil {
		return ruleSetPersistenceResponse{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
	}
	if err := s.engine.Reload(cfg); err != nil {
		return ruleSetPersistenceResponse{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
	}
	return ruleSetPersistenceResponse{
		Profile:    profile.Name,
		RuleSets:   profile.RuleSets,
		BackupPath: result.BackupPath,
	}, nil
}

func (s *Server) persistPolicyGroups(profileName string, nextPolicyGroups []config.PolicyGroupConfig) (policyGroupPersistenceResponse, error) {
	// Serialize the whole read-modify-validate-write-reload transaction.
	defer s.lockConfigTxn()()
	cfg, err := config.Load(s.configPath)
	if err != nil {
		return policyGroupPersistenceResponse{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
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
		return policyGroupPersistenceResponse{}, rulePersistenceError{status: http.StatusNotFound, err: fmt.Errorf("profile not found")}
	}
	profile.PolicyGroups = append([]config.PolicyGroupConfig(nil), nextPolicyGroups...)
	if err := engine.ValidateConfig(cfg); err != nil {
		return policyGroupPersistenceResponse{}, rulePersistenceError{status: http.StatusBadRequest, err: err}
	}
	result, err := config.WriteAtomicWithBackup(s.configPath, cfg)
	if err != nil {
		return policyGroupPersistenceResponse{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
	}
	if err := s.engine.Reload(cfg); err != nil {
		return policyGroupPersistenceResponse{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
	}
	return policyGroupPersistenceResponse{
		Profile:      profile.Name,
		PolicyGroups: profile.PolicyGroups,
		BackupPath:   result.BackupPath,
	}, nil
}

func (s *Server) persistRuleSubscriptions(profileName string, nextSubscriptions []config.RuleSubscriptionConfig) (ruleSubscriptionPersistenceResponse, error) {
	// Serialize the whole read-modify-validate-write-reload transaction.
	defer s.lockConfigTxn()()
	cfg, err := config.Load(s.configPath)
	if err != nil {
		return ruleSubscriptionPersistenceResponse{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
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
		return ruleSubscriptionPersistenceResponse{}, rulePersistenceError{status: http.StatusNotFound, err: fmt.Errorf("profile not found")}
	}
	profile.RuleSubscriptions = append([]config.RuleSubscriptionConfig(nil), nextSubscriptions...)
	if err := engine.ValidateConfig(cfg); err != nil {
		return ruleSubscriptionPersistenceResponse{}, rulePersistenceError{status: http.StatusBadRequest, err: err}
	}
	result, err := config.WriteAtomicWithBackup(s.configPath, cfg)
	if err != nil {
		return ruleSubscriptionPersistenceResponse{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
	}
	if err := s.engine.Reload(cfg); err != nil {
		return ruleSubscriptionPersistenceResponse{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
	}
	return ruleSubscriptionPersistenceResponse{
		Profile:       profile.Name,
		Subscriptions: profile.RuleSubscriptions,
		BackupPath:    result.BackupPath,
	}, nil
}

func (s *Server) persistDeveloperConfig(update func(config.DeveloperConfig) config.DeveloperConfig) (developerRulesPersistenceResponse, error) {
	return s.persistDeveloperConfigWithError(func(dev config.DeveloperConfig) (config.DeveloperConfig, error) {
		return update(dev), nil
	})
}

func (s *Server) persistDeveloperConfigWithError(update func(config.DeveloperConfig) (config.DeveloperConfig, error)) (developerRulesPersistenceResponse, error) {
	// Serialize the whole read-modify-validate-write-reload transaction.
	defer s.lockConfigTxn()()
	cfg, err := config.Load(s.configPath)
	if err != nil {
		return developerRulesPersistenceResponse{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
	}
	next, err := update(cfg.Developer)
	if err != nil {
		return developerRulesPersistenceResponse{}, err
	}
	cfg.Developer = next
	if err := engine.ValidateConfig(cfg); err != nil {
		return developerRulesPersistenceResponse{}, rulePersistenceError{status: http.StatusBadRequest, err: err}
	}
	result, err := config.WriteAtomicWithBackup(s.configPath, cfg)
	if err != nil {
		return developerRulesPersistenceResponse{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
	}
	if err := s.engine.Reload(cfg); err != nil {
		return developerRulesPersistenceResponse{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
	}
	dev := s.developerManager()
	if dev == nil {
		dev, err = developer.NewManager(cfg.Developer)
		if err != nil {
			return developerRulesPersistenceResponse{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
		}
	} else if err := dev.Reconfigure(cfg.Developer); err != nil {
		return developerRulesPersistenceResponse{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
	}
	s.SetDeveloper(dev)
	s.engine.SetHTTPInspector(dev)
	return developerRulesPersistenceResponse{
		Developer:  cfg.Developer,
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
	status := s.engine.Status()
	netInfo := s.engine.NetworkInfo()
	type statusWithNetwork struct {
		engine.Status
		NetworkInfo networkInfoPayload `json:"network_info,omitempty"`
	}
	writeJSON(w, statusWithNetwork{
		Status: status,
		NetworkInfo: networkInfoPayload{
			InterfaceName: netInfo.InterfaceName,
			SSID:          netInfo.SSID,
			IsWiFi:        netInfo.IsWiFi,
		},
	})
}

type networkInfoPayload struct {
	InterfaceName string `json:"interface_name,omitempty"`
	SSID          string `json:"ssid,omitempty"`
	IsWiFi        bool   `json:"is_wifi,omitempty"`
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

	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "profile name is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(s.configPath) != "" {
		// Serialize the whole read-modify-validate-write-reload transaction.
		defer s.lockConfigTxn()()
		cfg, err := config.Load(s.configPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if _, ok := cfg.ProfileByName(name); !ok {
			http.Error(w, fmt.Sprintf("profile %q not found", name), http.StatusNotFound)
			return
		}
		cfg.Active = name
		if err := engine.ValidateConfig(cfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := config.WriteAtomicWithBackup(s.configPath, cfg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := s.engine.Reload(cfg); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		status := s.engine.Status()
		writeJSON(w, struct {
			engine.Status
			Persisted  bool   `json:"persisted"`
			BackupPath string `json:"backup_path,omitempty"`
		}{Status: status, Persisted: true, BackupPath: result.BackupPath})
		return
	}
	if err := s.engine.SetActiveProfile(name); err != nil {
		// "not found" is the only user-correctable case.
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	status := s.engine.Status()
	writeJSON(w, struct {
		engine.Status
		Persisted bool `json:"persisted"`
	}{Status: status})
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
	activeEffectiveRules := []config.RuleConfig(nil)
	if s.engine != nil {
		cfg := s.engine.Config()
		activeProfile = cfg.Active
		profileNames = cfg.ProfileNames()
		if profile, err := cfg.ActiveProfile(); err == nil {
			activeProfile = profile.Name
			activeRules = profile.Rules
			_, _, activeEffectiveRules = subscription.EffectiveRules(cfg.Path, profile)
		}
	}
	opts := traffic.SnapshotOptions{
		State:          state,
		Limit:          limit,
		Action:         query.Get("action"),
		Profile:        query.Get("profile"),
		Rule:           query.Get("rule"),
		Country:        query.Get("country"),
		Port:           query.Get("port"),
		Query:          query.Get("query"),
		App:            query.Get("app"),
		Domain:         query.Get("domain"),
		ActiveProfile:  activeProfile,
		Profiles:       profileNames,
		Rules:          activeRules,
		EffectiveRules: activeEffectiveRules,
	}
	if manager := s.temporaryRules(); manager != nil {
		opts.TemporaryRules = manager.Snapshot(activeProfile)
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

func (s *Server) handleDeveloperRegenerateCA(w http.ResponseWriter, r *http.Request) {
	dev := s.developerManager()
	if dev == nil {
		http.Error(w, "developer mode disabled", http.StatusNotImplemented)
		return
	}
	status, err := dev.RegenerateCA()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, status)
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

func (s *Server) handleDeveloperRepeat(w http.ResponseWriter, r *http.Request) {
	dev := s.developerManager()
	if dev == nil {
		http.Error(w, "developer mode disabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req developer.RepeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := dev.Repeat(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, resp)
}

func (s *Server) handleDeveloperMapRules(w http.ResponseWriter, r *http.Request) {
	dev := s.developerManager()
	if dev == nil {
		writeJSON(w, map[string]any{"rules": []any{}})
		return
	}
	writeJSON(w, map[string]any{"rules": dev.ConfigSnapshot().MapRules})
}

func (s *Server) handleDeveloperReplaceMapRules(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.configPath) == "" {
		http.Error(w, "developer rule persistence requires daemon config path", http.StatusConflict)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req developerMapRulesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := s.persistDeveloperConfig(func(dev config.DeveloperConfig) config.DeveloperConfig {
		dev.MapRules = append([]config.DeveloperMapRuleConfig(nil), req.Rules...)
		return dev
	})
	if err != nil {
		writeRulePersistenceError(w, err)
		return
	}
	writeJSON(w, resp)
}

func (s *Server) handleDeveloperDeleteMapRule(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.configPath) == "" {
		http.Error(w, "developer rule persistence requires daemon config path", http.StatusConflict)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	resp, err := s.persistDeveloperConfig(func(dev config.DeveloperConfig) config.DeveloperConfig {
		next := make([]config.DeveloperMapRuleConfig, 0, len(dev.MapRules))
		for _, rule := range dev.MapRules {
			if rule.ID != id {
				next = append(next, rule)
			}
		}
		dev.MapRules = next
		return dev
	})
	if err != nil {
		writeRulePersistenceError(w, err)
		return
	}
	writeJSON(w, resp)
}

func (s *Server) handleDeveloperBreakpointRules(w http.ResponseWriter, r *http.Request) {
	dev := s.developerManager()
	if dev == nil {
		writeJSON(w, map[string]any{"rules": []any{}})
		return
	}
	writeJSON(w, map[string]any{"rules": dev.ConfigSnapshot().BreakpointRules})
}

func (s *Server) handleDeveloperReplaceBreakpointRules(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.configPath) == "" {
		http.Error(w, "developer rule persistence requires daemon config path", http.StatusConflict)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req developerBreakpointRulesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := s.persistDeveloperConfig(func(dev config.DeveloperConfig) config.DeveloperConfig {
		dev.BreakpointRules = append([]config.DeveloperBreakpointRuleConfig(nil), req.Rules...)
		return dev
	})
	if err != nil {
		writeRulePersistenceError(w, err)
		return
	}
	writeJSON(w, resp)
}

func (s *Server) handleDeveloperDeleteBreakpointRule(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.configPath) == "" {
		http.Error(w, "developer rule persistence requires daemon config path", http.StatusConflict)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	resp, err := s.persistDeveloperConfig(func(dev config.DeveloperConfig) config.DeveloperConfig {
		next := make([]config.DeveloperBreakpointRuleConfig, 0, len(dev.BreakpointRules))
		for _, rule := range dev.BreakpointRules {
			if rule.ID != id {
				next = append(next, rule)
			}
		}
		dev.BreakpointRules = next
		return dev
	})
	if err != nil {
		writeRulePersistenceError(w, err)
		return
	}
	writeJSON(w, resp)
}

func (s *Server) handleDeveloperPendingBreakpoints(w http.ResponseWriter, r *http.Request) {
	dev := s.developerManager()
	if dev == nil {
		writeJSON(w, map[string]any{"breakpoints": []any{}})
		return
	}
	writeJSON(w, map[string]any{"breakpoints": dev.PendingBreakpoints()})
}

func (s *Server) handleDeveloperResolveBreakpoint(w http.ResponseWriter, r *http.Request) {
	dev := s.developerManager()
	if dev == nil {
		http.Error(w, "developer mode disabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req developer.BreakpointResolution
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !dev.ResolveBreakpoint(r.PathValue("id"), req) {
		http.Error(w, "breakpoint not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"resolved": true})
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
