package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
	"github.com/JohnThre/clambhook/internal/protocol"
	"github.com/JohnThre/clambhook/internal/rules"
	"github.com/JohnThre/clambhook/internal/traffic"
)

const maxJSONRequestBytes = 1 << 20

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/status", s.handleStatus)
	mux.HandleFunc("GET /api/v1/profiles", s.handleProfiles)
	mux.HandleFunc("GET /api/v1/servers", s.handleServers)
	mux.HandleFunc("GET /api/v1/rules", s.handleRules)
	mux.HandleFunc("POST /api/v1/rules", s.handleCreateRule)
	mux.HandleFunc("POST /api/v1/rules/test", s.handleTestRule)
	mux.HandleFunc("GET /api/v1/decisions", s.handleDecisions)
	mux.HandleFunc("PUT /api/v1/profiles/active", s.handleSetActiveProfile)
	mux.HandleFunc("POST /api/v1/connect", s.handleConnect)
	mux.HandleFunc("POST /api/v1/disconnect", s.handleDisconnect)
	mux.HandleFunc("GET /api/v1/events", s.handleEvents)
	mux.HandleFunc("GET /api/v1/traffic", s.handleTraffic)
}

type createRuleRequest struct {
	Profile  string            `json:"profile"`
	Rule     config.RuleConfig `json:"rule"`
	Position string            `json:"position"`
}

type testRuleRequest struct {
	Profile string `json:"profile"`
	Network string `json:"network"`
	Target  string `json:"target"`
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
	profile, err := cfg.ActiveProfile()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"profile": profile.Name,
		"rules":   profile.Rules,
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
	ruleEngine, err := compileProfileRules(profile, profile.Chains[0].Name)
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
		if ch := findChain(profile, decision.ChainName); ch != nil {
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
	}
	writeJSON(w, resp)
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

func compileProfileRules(profile *config.Profile, defaultChainName string) (*rules.Engine, error) {
	known := make(map[string]struct{}, len(profile.Chains))
	for _, ch := range profile.Chains {
		known[ch.Name] = struct{}{}
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
	return rules.Compile(ruleSet, defaultChainName, known)
}

func findChain(profile *config.Profile, name string) *config.ChainConfig {
	for i := range profile.Chains {
		if profile.Chains[i].Name == name {
			return &profile.Chains[i]
		}
	}
	return nil
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

	cfg, err := config.Load(s.configPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	currentProfile := strings.TrimSpace(s.engine.Status().Profile)
	if currentProfile != "" {
		cfg.Active = currentProfile
	}
	profileName := strings.TrimSpace(req.Profile)
	if profileName == "" {
		profileName = cfg.Active
	}
	profile, ok := cfg.ProfileByName(profileName)
	if !ok {
		http.Error(w, "profile not found", http.StatusNotFound)
		return
	}
	profile.Rules = append(profile.Rules, req.Rule)

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
	writeJSON(w, map[string]any{
		"profile":     profile.Name,
		"rules":       profile.Rules,
		"backup_path": result.BackupPath,
	})
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
	state := r.URL.Query().Get("state")
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
		var empty *traffic.Store
		writeJSON(w, empty.Snapshot(state, limit))
		return
	}
	writeJSON(w, store.Snapshot(state, limit))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
