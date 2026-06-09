package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/JohnThre/clambhook/internal/temprules"
)

const (
	defaultTemporaryRuleTTL = 15 * time.Minute
	maxTemporaryRuleTTL     = 24 * time.Hour
)

type createTemporaryRuleFromConnectionRequest struct {
	ConnID     string `json:"conn_id"`
	Profile    string `json:"profile"`
	Name       string `json:"name"`
	Action     string `json:"action"`
	Scope      string `json:"scope"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

func (s *Server) handleTemporaryRules(w http.ResponseWriter, r *http.Request) {
	manager := s.temporaryRules()
	if manager == nil {
		writeJSON(w, map[string]any{"temporary_rules": []temprules.Rule{}})
		return
	}
	writeJSON(w, map[string]any{
		"temporary_rules": manager.Snapshot(r.URL.Query().Get("profile")),
	})
}

func (s *Server) handleCreateTemporaryRuleFromConnection(w http.ResponseWriter, r *http.Request) {
	manager := s.temporaryRules()
	if manager == nil {
		http.Error(w, "temporary rules are not configured", http.StatusConflict)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req createTemporaryRuleFromConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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
	ruleReq := createRuleFromConnectionRequest{
		ConnID:  req.ConnID,
		Profile: req.Profile,
		Name:    req.Name,
		Action:  req.Action,
		Scope:   req.Scope,
	}
	rule, err := ruleFromConnection(profile, conn, ruleReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if name := strings.TrimSpace(req.Name); name != "" {
		rule.Name = name
	}
	ttl := defaultTemporaryRuleTTL
	if req.TTLSeconds > 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}
	if ttl > maxTemporaryRuleTTL {
		http.Error(w, "ttl_seconds must be at most 86400", http.StatusBadRequest)
		return
	}
	created, err := manager.Create(temprules.CreateRequest{
		Profile:          profile.Name,
		Rule:             rule,
		TTL:              ttl,
		SourceConnID:     conn.ConnID,
		SourceTarget:     conn.Target,
		SourceTargetHost: connectionRuleHost(conn),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{
		"temporary_rule":  created,
		"temporary_rules": manager.Snapshot(profile.Name),
	})
}

func (s *Server) handleDeleteTemporaryRule(w http.ResponseWriter, r *http.Request) {
	manager := s.temporaryRules()
	if manager == nil {
		http.Error(w, "temporary rules are not configured", http.StatusConflict)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" || !manager.Delete(id) {
		http.Error(w, "temporary rule not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{
		"deleted":         true,
		"temporary_rules": manager.Snapshot(r.URL.Query().Get("profile")),
	})
}

func (s *Server) temporaryRules() *temprules.Manager {
	if s == nil || s.engine == nil {
		return nil
	}
	return s.engine.TemporaryRules()
}
