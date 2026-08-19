package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
	"github.com/JohnThre/clambhook/internal/scripting"
)

// modulesPayload is the wire shape returned by GET /api/v1/modules.
type modulesPayload struct {
	Modules []scripting.ModuleStatus `json:"modules"`
}

// replaceModulesRequest is the body for PUT /api/v1/modules.
type replaceModulesRequest struct {
	Modules []config.ModuleConfig `json:"modules"`
}

// moduleLogsPayload is the wire shape returned by GET /api/v1/modules/{id}/logs.
type moduleLogsPayload struct {
	Module string               `json:"module"`
	Logs   []scripting.LogEntry `json:"logs"`
}

func (s *Server) handleModules(w http.ResponseWriter, r *http.Request) {
	if s.scripting == nil {
		writeJSON(w, modulesPayload{Modules: nil})
		return
	}
	writeJSON(w, modulesPayload{Modules: s.scripting.ModulesSnapshot()})
}

func (s *Server) handleReplaceModules(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.configPath) == "" {
		http.Error(w, "modules persistence requires daemon config path", http.StatusConflict)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req replaceModulesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := s.persistModules(req.Modules)
	if err != nil {
		writeRulePersistenceError(w, err)
		return
	}
	writeJSON(w, resp)
}

func (s *Server) handleModuleLogs(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		http.Error(w, "module id is required", http.StatusBadRequest)
		return
	}
	var logs []scripting.LogEntry
	if s.scripting != nil {
		logs = s.scripting.ModuleLogs(id)
	}
	if logs == nil {
		logs = []scripting.LogEntry{}
	}
	writeJSON(w, moduleLogsPayload{Module: id, Logs: logs})
}

func (s *Server) persistModules(modules []config.ModuleConfig) (modulesPayload, error) {
	defer s.lockConfigTxn()()
	cfg, err := config.Load(s.configPath)
	if err != nil {
		return modulesPayload{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
	}
	cfg.Modules = modules
	if err := engine.ValidateConfig(cfg); err != nil {
		return modulesPayload{}, rulePersistenceError{status: http.StatusBadRequest, err: err}
	}
	result, err := config.WriteAtomicWithBackup(s.configPath, cfg)
	if err != nil {
		return modulesPayload{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
	}
	if err := s.engine.Reload(cfg); err != nil {
		restoreConfigBackup(s.configPath, result.BackupPath)
		return modulesPayload{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
	}
	if s.scripting != nil {
		if err := s.scripting.Reconfigure(cfg.Modules); err != nil {
			return modulesPayload{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
		}
	}
	if s.engine != nil {
		// The engine was reloaded with the new config. The scripting hook is
		// already wired via SetScriptingHook, so no further action is needed.
	}
	return modulesPayload{Modules: s.scripting.ModulesSnapshot()}, nil
}
