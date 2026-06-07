package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
)

type testPolicyGroupRequest struct {
	Group string `json:"group"`
}

type selectPolicyGroupRequest struct {
	Profile string `json:"profile"`
	Group   string `json:"group"`
	Chain   string `json:"chain"`
}

func (s *Server) handlePolicyGroups(w http.ResponseWriter, r *http.Request) {
	snap, err := s.engine.PolicySnapshotForProfile(r.URL.Query().Get("profile"))
	if err != nil {
		writeProfileSelectionError(w, err)
		return
	}
	writeJSON(w, snap)
}

func (s *Server) handlePolicyGroupTest(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req testPolicyGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	snap, err := s.engine.RefreshPolicyGroups(r.Context(), strings.TrimSpace(req.Group))
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "running engine") {
			status = http.StatusConflict
		} else if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, snap)
}

func (s *Server) handlePolicyGroupSelection(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.configPath) == "" {
		http.Error(w, "policy group selection requires daemon config path", http.StatusConflict)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req selectPolicyGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	groupName := strings.TrimSpace(req.Group)
	chainName := strings.TrimSpace(req.Chain)
	if groupName == "" || chainName == "" {
		http.Error(w, "group and chain are required", http.StatusBadRequest)
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
		http.Error(w, fmt.Sprintf("profile %q not found", profileName), http.StatusNotFound)
		return
	}
	var found *config.PolicyGroupConfig
	for i := range profile.PolicyGroups {
		if profile.PolicyGroups[i].Name == groupName {
			found = &profile.PolicyGroups[i]
			break
		}
	}
	if found == nil {
		http.Error(w, fmt.Sprintf("policy group %q not found", groupName), http.StatusNotFound)
		return
	}
	if found.Type != "select" {
		http.Error(w, fmt.Sprintf("policy group %q is %s, not select", groupName, found.Type), http.StatusBadRequest)
		return
	}
	member := false
	for _, chain := range found.Chains {
		if chain == chainName {
			member = true
			break
		}
	}
	if !member {
		http.Error(w, fmt.Sprintf("policy group %q has no member chain %q", groupName, chainName), http.StatusBadRequest)
		return
	}
	found.Selected = chainName
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
	snap, err := s.engine.PolicySnapshotForProfile(profile.Name)
	if err != nil {
		writeProfileSelectionError(w, err)
		return
	}
	writeJSON(w, map[string]any{
		"profile":     snap.Profile,
		"groups":      snap.Groups,
		"group":       groupName,
		"chain":       chainName,
		"backup_path": result.BackupPath,
	})
}
