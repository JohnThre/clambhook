package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/prompt"
	"github.com/JohnThre/clambhook/internal/temprules"
)

// resolvePromptRequest answers a pending interactive connection prompt.
type resolvePromptRequest struct {
	// Action is "allow" or "block".
	Action string `json:"action"`
	// Scope is "once" (this connection only), "session" (a temporary rule),
	// "until_quit" (a temporary rule that expires when the owning process
	// exits), or "forever" (a persisted rule). Defaults to "once".
	Scope string `json:"scope"`
	// MatchHost pins a remembered rule to the connection's destination host.
	// When false the rule matches the process for every destination.
	MatchHost bool `json:"match_host"`
	// MatchPort pins a remembered rule to the connection's destination port.
	MatchProtocol bool `json:"match_protocol"`
	// MatchProtocol pins a remembered rule to the connection's network/protocol
	// (e.g. tcp, udp).
	MatchPort bool `json:"match_port"`
	// TTLSeconds overrides the session/until_quit rule lifetime; 0 uses the
	// default (or, for until_quit, the long safety-net TTL).
	TTLSeconds int64 `json:"ttl_seconds"`
}

// promoteSilentDecisionRequest turns a logged Silent Mode decision into a
// remembered rule. The action is fixed by the logged decision; only the scope
// and match granularity are chosen here.
type promoteSilentDecisionRequest struct {
	// Scope is "session", "until_quit", or "forever". Defaults to "session".
	Scope         string `json:"scope"`
	MatchHost     bool   `json:"match_host"`
	MatchPort     bool   `json:"match_port"`
	MatchProtocol bool   `json:"match_protocol"`
	TTLSeconds    int64  `json:"ttl_seconds"`
}

func (s *Server) promptManager() *prompt.Manager {
	if s == nil || s.engine == nil {
		return nil
	}
	return s.engine.PromptManager()
}

func (s *Server) handlePendingPrompts(w http.ResponseWriter, r *http.Request) {
	m := s.promptManager()
	if m == nil {
		writeJSON(w, map[string]any{"prompts": []any{}})
		return
	}
	writeJSON(w, map[string]any{"prompts": m.Pending()})
}

// handleSilentDecisions returns the bounded Silent Mode review log, newest
// first. Each entry is a connection that was auto-decided while Silent Mode was
// active and can be promoted to a remembered rule.
func (s *Server) handleSilentDecisions(w http.ResponseWriter, r *http.Request) {
	m := s.promptManager()
	if m == nil {
		writeJSON(w, map[string]any{"decisions": []any{}})
		return
	}
	writeJSON(w, map[string]any{"decisions": m.SilentDecisions()})
}

// handlePromoteSilentDecision turns one logged Silent Mode decision into a
// remembered rule (session / until_quit / forever), reusing the same
// rule-builder and persistence path as an interactive prompt resolution.
func (s *Server) handlePromoteSilentDecision(w http.ResponseWriter, r *http.Request) {
	m := s.promptManager()
	if m == nil {
		http.Error(w, "interactive prompts are disabled", http.StatusNotImplemented)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		http.Error(w, "decision id is required", http.StatusBadRequest)
		return
	}
	decision, ok := m.SilentDecision(id)
	if !ok {
		http.Error(w, "silent decision not found", http.StatusNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req promoteSilentDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	scope := strings.ToLower(strings.TrimSpace(req.Scope))
	if scope == "" {
		scope = "session"
	}
	if scope != "session" && scope != "until_quit" && scope != "forever" {
		http.Error(w, "scope must be session, until_quit, or forever", http.StatusBadRequest)
		return
	}

	allow := decision.Action == "allow"
	pending := prompt.Pending{
		Profile:     decision.Profile,
		Network:     decision.Network,
		Target:      decision.Target,
		TargetHost:  decision.TargetHost,
		TargetPort:  decision.TargetPort,
		PID:         decision.PID,
		ProcessName: decision.ProcessName,
		ProcessPath: decision.ProcessPath,
	}
	rule, err := s.promptRuleFromPending(pending, allow, req.MatchHost, req.MatchPort, req.MatchProtocol)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	remembered, rerr := s.createRememberedRule(pending, rule, allow, scope, req.TTLSeconds)
	if rerr != nil {
		writeRulePersistenceError(w, rerr)
		return
	}
	resp := map[string]any{
		"promoted": true,
		"id":       id,
		"action":   decision.Action,
		"scope":    scope,
	}
	for k, v := range remembered {
		resp[k] = v
	}
	writeJSON(w, resp)
}

func (s *Server) handleResolvePrompt(w http.ResponseWriter, r *http.Request) {
	m := s.promptManager()
	if m == nil {
		http.Error(w, "interactive prompts are disabled", http.StatusNotImplemented)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		http.Error(w, "prompt id is required", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req resolvePromptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var allow bool
	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "allow":
		allow = true
	case "block":
		allow = false
	default:
		http.Error(w, "action must be allow or block", http.StatusBadRequest)
		return
	}

	pending, ok := m.Resolve(id, prompt.Resolution{Allow: allow})
	if !ok {
		http.Error(w, "prompt not found", http.StatusNotFound)
		return
	}

	scope := strings.ToLower(strings.TrimSpace(req.Scope))
	if scope == "" {
		scope = "once"
	}
	resp := map[string]any{
		"resolved": true,
		"id":       id,
		"action":   strings.ToLower(strings.TrimSpace(req.Action)),
		"scope":    scope,
	}
	if scope == "once" {
		writeJSON(w, resp)
		return
	}

	rule, err := s.promptRuleFromPending(pending, allow, req.MatchHost, req.MatchPort, req.MatchProtocol)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	remembered, rerr := s.createRememberedRule(pending, rule, allow, scope, req.TTLSeconds)
	if rerr != nil {
		writeRulePersistenceError(w, rerr)
		return
	}
	for k, v := range remembered {
		resp[k] = v
	}
	writeJSON(w, resp)
}

// createRememberedRule installs a session / until_quit / forever rule built from
// a resolved prompt or a promoted silent decision. It returns the response
// fields describing the created rule. A rulePersistenceError carries the HTTP
// status so callers can route it through writeRulePersistenceError.
func (s *Server) createRememberedRule(pending prompt.Pending, rule config.RuleConfig, allow bool, scope string, ttlSeconds int64) (map[string]any, error) {
	switch scope {
	case "session", "until_quit":
		mgr := s.temporaryRules()
		if mgr == nil {
			return nil, rulePersistenceError{status: http.StatusNotImplemented, err: fmt.Errorf("temporary rules are unavailable")}
		}
		if scope == "until_quit" && pending.PID <= 0 {
			return nil, rulePersistenceError{status: http.StatusBadRequest, err: fmt.Errorf("until_quit requires an attributed process")}
		}
		ttl := time.Duration(ttlSeconds) * time.Second
		req := temprules.CreateRequest{
			Profile:          pending.Profile,
			Rule:             rule,
			TTL:              ttl,
			SourceConnID:     pending.ConnID,
			SourceTarget:     pending.Target,
			SourceTargetHost: pending.TargetHost,
		}
		if scope == "until_quit" {
			req.UntilQuitPID = pending.PID
		}
		created, cerr := mgr.Create(req)
		if cerr != nil {
			return nil, rulePersistenceError{status: http.StatusBadRequest, err: cerr}
		}
		return map[string]any{"temporary_rule": created}, nil
	case "forever":
		if strings.TrimSpace(s.configPath) == "" {
			return nil, rulePersistenceError{status: http.StatusConflict, err: fmt.Errorf("persistent rules require a daemon config path")}
		}
		// Block rules go first so a deny wins; allow rules go last so specific
		// routing rules still take precedence and the allow rule only catches
		// traffic that would otherwise fall through to the default chain.
		result, perr := s.persistRules(pending.Profile, func(existing []config.RuleConfig) []config.RuleConfig {
			rule.Name = uniqueRuleName(existing, rule.Name)
			if allow {
				return append(existing, rule)
			}
			return append([]config.RuleConfig{rule}, existing...)
		})
		if perr != nil {
			return nil, perr
		}
		return map[string]any{"rule": rule, "backup_path": result.BackupPath}, nil
	default:
		return nil, rulePersistenceError{status: http.StatusBadRequest, err: fmt.Errorf("scope must be once, session, until_quit, or forever")}
	}
}

// promptRuleFromPending builds a remembered rule from a resolved prompt. Allow
// rules route the process's default-bound traffic through the profile's default
// chain (which both permits the connection and suppresses future prompts); block
// rules deny it. matchHost/matchPort/matchProtocol pin the rule to the
// connection's destination host, port, and network respectively; when false the
// rule matches the process for every destination/port/protocol.
// PromptRuleFromPending builds a remembered rule from a resolved prompt or a
// promoted silent decision. It is the exported form of the API server's
// promptRuleFromPending so the mobile bridge reuses the exact rule shape.
func PromptRuleFromPending(pending prompt.Pending, allow, matchHost, matchPort, matchProtocol bool, cfg *config.Config) (config.RuleConfig, error) {
	proc := strings.TrimSpace(pending.ProcessPath)
	if proc == "" {
		proc = strings.TrimSpace(pending.ProcessName)
	}
	if proc == "" {
		return config.RuleConfig{}, fmt.Errorf("prompt has no attributed process to remember")
	}

	action := "block"
	verb := "block"
	if allow {
		verb = "allow"
		action = "direct"
		if cfg != nil {
			if profile, ok := cfg.ProfileByName(pending.Profile); ok && len(profile.Chains) > 0 {
				action = "chain:" + profile.Chains[0].Name
			}
		}
	}

	label := pending.ProcessName
	if label == "" {
		label = proc
	}
	name := fmt.Sprintf("prompt %s %s", verb, label)

	rule := config.RuleConfig{
		Name:      name,
		Action:    action,
		Processes: []string{proc},
	}
	if matchHost && strings.TrimSpace(pending.TargetHost) != "" {
		rule.Name = fmt.Sprintf("%s %s", name, pending.TargetHost)
		rule.Domains = []string{pending.TargetHost}
	}
	if matchPort && strings.TrimSpace(pending.TargetPort) != "" {
		if port, err := strconv.Atoi(pending.TargetPort); err == nil && port > 0 {
			rule.Ports = []int{port}
		}
	}
	if matchProtocol && strings.TrimSpace(pending.Network) != "" {
		rule.Networks = []string{pending.Network}
	}
	return rule, nil
}

func (s *Server) promptRuleFromPending(pending prompt.Pending, allow, matchHost, matchPort, matchProtocol bool) (config.RuleConfig, error) {
	return PromptRuleFromPending(pending, allow, matchHost, matchPort, matchProtocol, s.engine.Config())
}
