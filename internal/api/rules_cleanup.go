package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/traffic"
)

type cleanupRuleRequest struct {
	Profile        string `json:"profile"`
	Kind           string `json:"kind"`
	RuleName       string `json:"rule_name"`
	TargetRuleName string `json:"target_rule_name"`
	Operation      string `json:"operation"`
}

func (s *Server) handleCleanupRule(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.configPath) == "" {
		http.Error(w, "rule cleanup requires daemon config path", http.StatusConflict)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req cleanupRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req = normalizeCleanupRuleRequest(req)
	if err := validateCleanupRuleRequest(req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp, err := s.persistRulesWithError(req.Profile, func(profileName string, existing []config.RuleConfig) ([]config.RuleConfig, error) {
		suggestion, ok := s.matchCurrentCleanupSuggestion(profileName, existing, req)
		if !ok {
			return nil, rulePersistenceError{status: http.StatusConflict, err: fmt.Errorf("cleanup suggestion is stale")}
		}
		next, err := applyCleanupSuggestion(existing, suggestion)
		if err != nil {
			return nil, err
		}
		return next, nil
	})
	if err != nil {
		writeRulePersistenceError(w, err)
		return
	}
	writeJSON(w, resp)
}

func normalizeCleanupRuleRequest(req cleanupRuleRequest) cleanupRuleRequest {
	req.Profile = strings.TrimSpace(req.Profile)
	req.Kind = strings.TrimSpace(req.Kind)
	req.RuleName = strings.TrimSpace(req.RuleName)
	req.TargetRuleName = strings.TrimSpace(req.TargetRuleName)
	req.Operation = strings.TrimSpace(req.Operation)
	return req
}

func validateCleanupRuleRequest(req cleanupRuleRequest) error {
	if req.Kind == "" {
		return fmt.Errorf("kind is required")
	}
	if req.RuleName == "" {
		return fmt.Errorf("rule_name is required")
	}
	if req.TargetRuleName == "" {
		return fmt.Errorf("target_rule_name is required")
	}
	switch req.Operation {
	case "delete_rule", "move_rule_to_end":
		return nil
	default:
		return fmt.Errorf("operation must be delete_rule or move_rule_to_end")
	}
}

func (s *Server) matchCurrentCleanupSuggestion(profileName string, rules []config.RuleConfig, req cleanupRuleRequest) (traffic.CleanupSuggestion, bool) {
	store := s.trafficStore()
	if store == nil {
		return traffic.CleanupSuggestion{}, false
	}
	snapshot := store.SnapshotWithOptions(traffic.SnapshotOptions{
		State:         "all",
		ActiveProfile: profileName,
		Rules:         rules,
	})
	for _, suggestion := range snapshot.CleanupSuggestions {
		if suggestion.Kind != req.Kind {
			continue
		}
		if suggestion.RuleName != req.RuleName {
			continue
		}
		if suggestion.TargetRuleName != req.TargetRuleName {
			continue
		}
		if suggestion.Operation != req.Operation {
			continue
		}
		return suggestion, true
	}
	return traffic.CleanupSuggestion{}, false
}

func applyCleanupSuggestion(rules []config.RuleConfig, suggestion traffic.CleanupSuggestion) ([]config.RuleConfig, error) {
	idx := indexRuleByName(rules, suggestion.TargetRuleName)
	if idx < 0 {
		return nil, rulePersistenceError{status: http.StatusConflict, err: fmt.Errorf("cleanup target rule not found")}
	}
	switch suggestion.Operation {
	case "delete_rule":
		next := make([]config.RuleConfig, 0, len(rules)-1)
		next = append(next, rules[:idx]...)
		next = append(next, rules[idx+1:]...)
		return next, nil
	case "move_rule_to_end":
		if idx == len(rules)-1 {
			return append([]config.RuleConfig(nil), rules...), nil
		}
		rule := rules[idx]
		next := make([]config.RuleConfig, 0, len(rules))
		next = append(next, rules[:idx]...)
		next = append(next, rules[idx+1:]...)
		next = append(next, rule)
		return next, nil
	default:
		return nil, rulePersistenceError{status: http.StatusBadRequest, err: fmt.Errorf("unsupported cleanup operation")}
	}
}

func indexRuleByName(rules []config.RuleConfig, name string) int {
	for i, rule := range rules {
		if rule.Name == name {
			return i
		}
	}
	return -1
}
