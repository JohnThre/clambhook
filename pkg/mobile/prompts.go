package mobile

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	api "github.com/JohnThre/clambhook/internal/api"
	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/prompt"
	"github.com/JohnThre/clambhook/internal/temprules"
	"github.com/JohnThre/clambhook/internal/traffic"
)

// trafficFilterRequest is the JSON form of a server-side monitor filter, sent
// by the Android client's quickFilter chips and search box. Empty fields match
// all. It mirrors the GET /api/v1/traffic query params.
type trafficFilterRequest struct {
	State   string `json:"state"`
	Action  string `json:"action"`
	Profile string `json:"profile"`
	Rule    string `json:"rule"`
	Country string `json:"country"`
	Port    string `json:"port"`
	Process string `json:"process"`
	Network string `json:"network"`
	App     string `json:"app"`
	Domain  string `json:"domain"`
	Query   string `json:"query"`
	Limit   int    `json:"limit"`
	Offset  int    `json:"offset"`
}

// PendingPromptsJSON returns the interactive connection prompts currently
// awaiting a decision (Little Snitch-style), as JSON.
func (r *TunnelRuntime) PendingPromptsJSON() (string, error) {
	r.mu.Lock()
	proxy := r.proxy
	r.mu.Unlock()
	if proxy == nil {
		return marshalString(map[string]any{"prompts": []any{}})
	}
	return marshalString(map[string]any{"prompts": proxy.PromptManager().Pending()})
}

// SilentDecisionsJSON returns the bounded Silent Mode review log as JSON.
func (r *TunnelRuntime) SilentDecisionsJSON() (string, error) {
	r.mu.Lock()
	proxy := r.proxy
	r.mu.Unlock()
	if proxy == nil {
		return marshalString(map[string]any{"decisions": []any{}})
	}
	return marshalString(map[string]any{"decisions": proxy.PromptManager().SilentDecisions()})
}

// ResolvePromptJSON answers a pending connection prompt: it resolves the prompt
// and, for session/until_quit/forever scopes, remembers the decision as a rule.
func (r *TunnelRuntime) ResolvePromptJSON(id, action, scope string, matchHost, matchPort, matchProtocol bool, ttlSeconds int64) (string, error) {
	r.mu.Lock()
	proxy := r.proxy
	cfg := r.cfg
	tempRules := r.temp
	r.mu.Unlock()
	if proxy == nil {
		return "", errors.New("tunnel: runtime is not running")
	}
	allow := strings.EqualFold(strings.TrimSpace(action), "allow")
	pending, ok := proxy.PromptManager().Resolve(strings.TrimSpace(id), prompt.Resolution{Allow: allow})
	if !ok {
		return "", errors.New("prompt not found")
	}
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		scope = "once"
	}
	if scope == "once" {
		return marshalString(map[string]any{"resolved": true, "id": id, "action": strings.ToLower(strings.TrimSpace(action)), "scope": scope})
	}
	rule, err := api.PromptRuleFromPending(pending, allow, matchHost, matchPort, matchProtocol, cfg)
	if err != nil {
		return "", err
	}
	switch scope {
	case "session", "until_quit":
		if tempRules == nil {
			return "", errors.New("temporary rules are not configured")
		}
		if scope == "until_quit" && pending.PID <= 0 {
			return "", errors.New("until_quit requires an attributed process")
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
		created, cerr := tempRules.Create(req)
		if cerr != nil {
			return "", cerr
		}
		return marshalString(map[string]any{"resolved": true, "id": id, "action": strings.ToLower(strings.TrimSpace(action)), "scope": scope, "temporary_rule": created})
	case "forever":
		if cfg == nil || strings.TrimSpace(cfg.Path) == "" {
			return "", errors.New("persistent rules require a daemon config path")
		}
		return appendPromptRuleToConfig(cfg.Path, pending.Profile, rule, allow)
	default:
		return "", fmt.Errorf("scope must be once, session, until_quit, or forever")
	}
}

// PromoteSilentDecisionJSON turns a logged Silent Mode decision into a
// remembered rule (session / until_quit / forever).
func (r *TunnelRuntime) PromoteSilentDecisionJSON(id, scope string, matchHost, matchPort, matchProtocol bool) (string, error) {
	r.mu.Lock()
	proxy := r.proxy
	cfg := r.cfg
	tempRules := r.temp
	r.mu.Unlock()
	if proxy == nil {
		return "", errors.New("tunnel: runtime is not running")
	}
	decision, ok := proxy.PromptManager().SilentDecision(strings.TrimSpace(id))
	if !ok {
		return "", errors.New("silent decision not found")
	}
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		scope = "session"
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
	rule, err := api.PromptRuleFromPending(pending, allow, matchHost, matchPort, matchProtocol, cfg)
	if err != nil {
		return "", err
	}
	switch scope {
	case "session", "until_quit":
		if tempRules == nil {
			return "", errors.New("temporary rules are not configured")
		}
		if scope == "until_quit" && pending.PID <= 0 {
			return "", errors.New("until_quit requires an attributed process")
		}
		req := temprules.CreateRequest{Profile: pending.Profile, Rule: rule}
		if scope == "until_quit" {
			req.UntilQuitPID = pending.PID
		}
		created, cerr := tempRules.Create(req)
		if cerr != nil {
			return "", cerr
		}
		return marshalString(map[string]any{"promoted": true, "id": id, "scope": scope, "temporary_rule": created})
	case "forever":
		if cfg == nil || strings.TrimSpace(cfg.Path) == "" {
			return "", errors.New("persistent rules require a daemon config path")
		}
		return appendPromptRuleToConfig(cfg.Path, pending.Profile, rule, allow)
	default:
		return "", fmt.Errorf("scope must be session, until_quit, or forever")
	}
}

// TrafficFilterJSON fetches the traffic snapshot with a server-side monitor
// filter (the JSON form of trafficFilterRequest), so the live monitor's
// quickFilter chips and search reach the full history rather than the
// in-memory window.
func (r *TunnelRuntime) TrafficFilterJSON(filterJSON string) (string, error) {
	r.mu.Lock()
	trf := r.trf
	cfg := r.cfg
	tempRules := r.temp
	r.mu.Unlock()
	profile := activeProfileName(cfg)
	temporaryRules := []temprules.Rule(nil)
	if tempRules != nil {
		temporaryRules = tempRules.Snapshot(profile)
	}
	opts := traffic.SnapshotOptions{
		Limit:          200,
		ActiveProfile:  profile,
		Profiles:       profileNames(cfg),
		TemporaryRules: temporaryRules,
	}
	if strings.TrimSpace(filterJSON) != "" {
		var f trafficFilterRequest
		if err := json.Unmarshal([]byte(filterJSON), &f); err != nil {
			return "", err
		}
		opts.State = f.State
		opts.Action = f.Action
		opts.Profile = f.Profile
		opts.Rule = f.Rule
		opts.Country = f.Country
		opts.Port = f.Port
		opts.Process = f.Process
		opts.Network = f.Network
		opts.App = f.App
		opts.Domain = f.Domain
		opts.Query = f.Query
		if f.Limit > 0 {
			opts.Limit = f.Limit
		}
		opts.Offset = f.Offset
	}
	if trf == nil || trf.Store() == nil {
		var empty *traffic.Store
		return marshalString(empty.SnapshotWithOptions(opts))
	}
	return marshalString(trf.Store().SnapshotWithOptions(opts))
}

// appendPromptRuleToConfig persists a remembered prompt rule to the active
// profile's config. Block rules go first so a deny wins; allow rules go last so
// specific routing rules still take precedence. The caller reloads the runtime
// after this atomic config edit.
func appendPromptRuleToConfig(configPath, profileName string, rule config.RuleConfig, allow bool) (string, error) {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return "", errors.New("config path is required")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return "", err
	}
	profile := selectProfileForEdit(cfg, strings.TrimSpace(profileName))
	if profile == nil {
		return "", fmt.Errorf("profile %q not found", profileName)
	}
	rule.Name = uniquePromptRuleName(profile.Rules, rule.Name)
	if allow {
		profile.Rules = append(profile.Rules, rule)
	} else {
		profile.Rules = append([]config.RuleConfig{rule}, profile.Rules...)
	}
	if _, err := config.WriteAtomicWithBackup(configPath, cfg); err != nil {
		return "", err
	}
	return marshalString(map[string]any{"rule": rule})
}

// uniquePromptRuleName appends " (N)" to a remembered rule's name until it does
// not collide with an existing rule in the same profile.
func uniquePromptRuleName(existing []config.RuleConfig, name string) string {
	names := make(map[string]bool, len(existing))
	for _, r := range existing {
		names[r.Name] = true
	}
	if !names[name] {
		return name
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s (%d)", name, i)
		if !names[candidate] {
			return candidate
		}
	}
}
