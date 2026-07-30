package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
)

// conditionerPayload is the wire shape returned by the conditioner endpoints.
// It mirrors config.ConditionerConfig but carries the owning profile name and
// an optional backup path (set only on a successful persisting PUT).
type conditionerPayload struct {
	Profile      string  `json:"profile"`
	Enabled      bool    `json:"enabled"`
	DownloadKbps int     `json:"download_kbps"`
	UploadKbps   int     `json:"upload_kbps"`
	Latency      string  `json:"latency,omitempty"`
	Jitter       string  `json:"jitter,omitempty"`
	LossPercent  float64 `json:"loss_percent"`
	BackupPath   string  `json:"backup_path,omitempty"`
}

// updateConditionerRequest is a partial update: every optional field is a
// pointer so an omitted field leaves the persisted value untouched, matching
// the config-settings/DNS PUT convention.
type updateConditionerRequest struct {
	Profile      string   `json:"profile"`
	Enabled      *bool    `json:"enabled,omitempty"`
	DownloadKbps *int     `json:"download_kbps,omitempty"`
	UploadKbps   *int     `json:"upload_kbps,omitempty"`
	Latency      *string  `json:"latency,omitempty"`
	Jitter       *string  `json:"jitter,omitempty"`
	LossPercent  *float64 `json:"loss_percent,omitempty"`
}

func (s *Server) handleConditioner(w http.ResponseWriter, r *http.Request) {
	cfg := s.engine.Config()
	profile, err := selectAPIProfile(cfg, r.URL.Query().Get("profile"))
	if err != nil {
		writeProfileSelectionError(w, err)
		return
	}
	writeJSON(w, conditionerSnapshot(profile, ""))
}

func (s *Server) handleUpdateConditioner(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.configPath) == "" {
		http.Error(w, "conditioner persistence requires daemon config path", http.StatusConflict)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req updateConditionerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	payload, err := s.persistConditioner(req)
	if err != nil {
		writeRulePersistenceError(w, err)
		return
	}
	writeJSON(w, payload)
}

func (s *Server) persistConditioner(req updateConditionerRequest) (conditionerPayload, error) {
	// Serialize the whole read-modify-validate-write-reload transaction so
	// concurrent edits cannot overwrite each other, mirroring the config
	// settings and DNS endpoints.
	defer s.lockConfigTxn()()
	cfg, err := config.Load(s.configPath)
	if err != nil {
		return conditionerPayload{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
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
		return conditionerPayload{}, rulePersistenceError{status: http.StatusNotFound, err: fmt.Errorf("profile not found")}
	}
	if err := applyConditionerUpdate(&profile.Conditioner, req); err != nil {
		return conditionerPayload{}, rulePersistenceError{status: http.StatusBadRequest, err: err}
	}
	if err := engine.ValidateConfig(cfg); err != nil {
		return conditionerPayload{}, rulePersistenceError{status: http.StatusBadRequest, err: err}
	}
	result, err := config.WriteAtomicWithBackup(s.configPath, cfg)
	if err != nil {
		return conditionerPayload{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
	}
	// Reload rebuilds listeners against the new config and re-syncs the
	// engine-owned shaper, so the change takes effect without a daemon
	// restart. Apply the live shaper first as a fast path for the active
	// profile so subsequent dials are shaped even before the rebuild lands.
	if profileName == cfg.Active {
		s.engine.ApplyConditioner(profile.Conditioner)
	}
	if err := s.engine.Reload(cfg); err != nil {
		restoreConfigBackup(s.configPath, result.BackupPath)
		return conditionerPayload{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
	}
	return conditionerSnapshot(profile, result.BackupPath), nil
}

// applyConditionerUpdate applies a partial request onto an existing block,
// parsing and validating duration and bandwidth/loss fields.
func applyConditionerUpdate(c *config.ConditionerConfig, req updateConditionerRequest) error {
	if req.Enabled != nil {
		c.Enabled = *req.Enabled
	}
	if req.DownloadKbps != nil {
		if *req.DownloadKbps < 0 {
			return fmt.Errorf("download_kbps must not be negative")
		}
		c.DownloadKbps = *req.DownloadKbps
	}
	if req.UploadKbps != nil {
		if *req.UploadKbps < 0 {
			return fmt.Errorf("upload_kbps must not be negative")
		}
		c.UploadKbps = *req.UploadKbps
	}
	if req.Latency != nil {
		d, err := parseConditionerDuration("latency", *req.Latency)
		if err != nil {
			return err
		}
		c.Latency = d
	}
	if req.Jitter != nil {
		d, err := parseConditionerDuration("jitter", *req.Jitter)
		if err != nil {
			return err
		}
		c.Jitter = d
	}
	if req.LossPercent != nil {
		if *req.LossPercent < 0 || *req.LossPercent > 100 {
			return fmt.Errorf("loss_percent must be between 0 and 100")
		}
		c.LossPercent = *req.LossPercent
	}
	return nil
}

// parseConditionerDuration parses a Go duration string (e.g. "40ms"). An empty
// string clears the value.
func parseConditionerDuration(field, value string) (config.Duration, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", field, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("%s must not be negative", field)
	}
	return config.Duration(d), nil
}

func conditionerSnapshot(profile *config.Profile, backupPath string) conditionerPayload {
	if profile == nil {
		return conditionerPayload{}
	}
	c := profile.Conditioner
	payload := conditionerPayload{
		Profile:      profile.Name,
		Enabled:      c.Enabled,
		DownloadKbps: c.DownloadKbps,
		UploadKbps:   c.UploadKbps,
		LossPercent:  c.LossPercent,
		BackupPath:   backupPath,
	}
	if c.Latency > 0 {
		payload.Latency = c.Latency.Std().String()
	}
	if c.Jitter > 0 {
		payload.Jitter = c.Jitter.Std().String()
	}
	return payload
}
