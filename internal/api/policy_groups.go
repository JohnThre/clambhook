package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

type testPolicyGroupRequest struct {
	Group string `json:"group"`
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
