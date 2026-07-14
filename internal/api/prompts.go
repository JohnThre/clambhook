package api

import (
	"encoding/json"
	"fmt"
	"net/http"
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
	// Scope is "once" (this connection only), "session" (a temporary rule), or
	// "forever" (a persisted rule). Defaults to "once".
	Scope string `json:"scope"`
	// MatchHost pins a remembered rule to the connection's destination host.
	// When false the rule matches the process for every destination.
	MatchHost bool `json:"match_host"`
	// TTLSeconds overrides the session-rule lifetime; 0 uses the default.
	TTLSeconds int64 `json:"ttl_seconds"`
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

	rule, err := s.promptRuleFromPending(pending, allow, req.MatchHost)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch scope {
	case "session":
		mgr := s.temporaryRules()
		if mgr == nil {
			http.Error(w, "temporary rules are unavailable", http.StatusNotImplemented)
			return
		}
		ttl := time.Duration(req.TTLSeconds) * time.Second
		created, cerr := mgr.Create(temprules.CreateRequest{
			Profile:          pending.Profile,
			Rule:             rule,
			TTL:              ttl,
			SourceConnID:     pending.ConnID,
			SourceTarget:     pending.Target,
			SourceTargetHost: pending.TargetHost,
		})
		if cerr != nil {
			http.Error(w, cerr.Error(), http.StatusBadRequest)
			return
		}
		resp["temporary_rule"] = created
	case "forever":
		if strings.TrimSpace(s.configPath) == "" {
			http.Error(w, "persistent rules require a daemon config path", http.StatusConflict)
			return
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
			writeRulePersistenceError(w, perr)
			return
		}
		resp["rule"] = rule
		resp["backup_path"] = result.BackupPath
	default:
		http.Error(w, "scope must be once, session, or forever", http.StatusBadRequest)
		return
	}
	writeJSON(w, resp)
}

// promptRuleFromPending builds a remembered rule from a resolved prompt. Allow
// rules route the process's default-bound traffic through the profile's default
// chain (which both permits the connection and suppresses future prompts);
// block rules deny it.
func (s *Server) promptRuleFromPending(pending prompt.Pending, allow, matchHost bool) (config.RuleConfig, error) {
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
		if cfg := s.engine.Config(); cfg != nil {
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
	return rule, nil
}
