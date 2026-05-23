package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/JohnThre/clambhook/internal/traffic"
)

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/status", s.handleStatus)
	mux.HandleFunc("GET /api/v1/profiles", s.handleProfiles)
	mux.HandleFunc("GET /api/v1/servers", s.handleServers)
	mux.HandleFunc("PUT /api/v1/profiles/active", s.handleSetActiveProfile)
	mux.HandleFunc("POST /api/v1/connect", s.handleConnect)
	mux.HandleFunc("POST /api/v1/disconnect", s.handleDisconnect)
	mux.HandleFunc("GET /api/v1/events", s.handleEvents)
	mux.HandleFunc("GET /api/v1/traffic", s.handleTraffic)
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
	if s.traffic == nil {
		var empty *traffic.Store
		writeJSON(w, empty.Snapshot(state, limit))
		return
	}
	writeJSON(w, s.traffic.Snapshot(state, limit))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
