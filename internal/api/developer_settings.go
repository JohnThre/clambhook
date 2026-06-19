package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/JohnThre/clambhook/internal/config"
)

type developerSettingsPayload struct {
	Enabled               bool     `json:"enabled"`
	MITMEnabled           bool     `json:"mitm_enabled"`
	CaptureLimit          int      `json:"capture_limit"`
	BodyLimitBytes        int64    `json:"body_limit_bytes"`
	HeaderValueLimitBytes int      `json:"header_value_limit_bytes"`
	RedactHeaders         []string `json:"redact_headers"`
	RedactQueryParams     []string `json:"redact_query_params"`
	BackupPath            string   `json:"backup_path,omitempty"`
}

type updateDeveloperSettingsRequest struct {
	Enabled               *bool    `json:"enabled,omitempty"`
	MITMEnabled           *bool    `json:"mitm_enabled,omitempty"`
	CaptureLimit          *int     `json:"capture_limit,omitempty"`
	BodyLimitBytes        *int64   `json:"body_limit_bytes,omitempty"`
	HeaderValueLimitBytes *int     `json:"header_value_limit_bytes,omitempty"`
	RedactHeaders         []string `json:"redact_headers,omitempty"`
	RedactQueryParams     []string `json:"redact_query_params,omitempty"`
	HTTPSCaptureAck       bool     `json:"https_capture_ack,omitempty"`
}

func (s *Server) handleDeveloperSettings(w http.ResponseWriter, r *http.Request) {
	dev := s.developerManager()
	if dev != nil {
		writeJSON(w, developerSettingsSnapshot(dev.ConfigSnapshot(), ""))
		return
	}
	writeJSON(w, developerSettingsSnapshot(s.engine.Config().Developer, ""))
}

func (s *Server) handleUpdateDeveloperSettings(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.configPath) == "" {
		http.Error(w, "developer settings persistence requires daemon config path", http.StatusConflict)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req updateDeveloperSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := s.persistDeveloperConfigWithError(func(dev config.DeveloperConfig) (config.DeveloperConfig, error) {
		return applyDeveloperSettingsUpdate(dev, req)
	})
	if err != nil {
		writeRulePersistenceError(w, err)
		return
	}
	writeJSON(w, developerSettingsSnapshot(resp.Developer, resp.BackupPath))
}

func applyDeveloperSettingsUpdate(current config.DeveloperConfig, req updateDeveloperSettingsRequest) (config.DeveloperConfig, error) {
	next := current
	wasHTTPSCaptureEnabled := current.Enabled && current.MITMEnabled
	if req.Enabled != nil {
		next.Enabled = *req.Enabled
		if !next.Enabled {
			next.MITMEnabled = false
		} else if req.MITMEnabled == nil {
			next.MITMEnabled = false
		}
	}
	if req.MITMEnabled != nil {
		if *req.MITMEnabled && !next.Enabled {
			return config.DeveloperConfig{}, rulePersistenceError{
				status: http.StatusBadRequest,
				err:    fmt.Errorf("developer capture must be enabled before HTTPS capture"),
			}
		}
		if *req.MITMEnabled && !wasHTTPSCaptureEnabled && !req.HTTPSCaptureAck {
			return config.DeveloperConfig{}, rulePersistenceError{
				status: http.StatusBadRequest,
				err:    fmt.Errorf("https_capture_ack is required when enabling HTTPS capture"),
			}
		}
		next.MITMEnabled = *req.MITMEnabled
	}
	if req.CaptureLimit != nil {
		next.CaptureLimit = *req.CaptureLimit
	}
	if req.BodyLimitBytes != nil {
		next.BodyLimitBytes = *req.BodyLimitBytes
	}
	if req.HeaderValueLimitBytes != nil {
		next.HeaderValueLimitBytes = *req.HeaderValueLimitBytes
	}
	def := config.DefaultDeveloperConfig()
	var err error
	if req.RedactHeaders != nil {
		next.RedactHeaders, err = normalizeDeveloperNameList("developer.redact_headers", req.RedactHeaders, def.RedactHeaders)
		if err != nil {
			return config.DeveloperConfig{}, rulePersistenceError{status: http.StatusBadRequest, err: err}
		}
	}
	if req.RedactQueryParams != nil {
		next.RedactQueryParams, err = normalizeDeveloperNameList("developer.redact_query_params", req.RedactQueryParams, def.RedactQueryParams)
		if err != nil {
			return config.DeveloperConfig{}, rulePersistenceError{status: http.StatusBadRequest, err: err}
		}
	}
	return next, nil
}

func developerSettingsSnapshot(dev config.DeveloperConfig, backupPath string) developerSettingsPayload {
	def := config.DefaultDeveloperConfig()
	if dev.CaptureLimit == 0 {
		dev.CaptureLimit = def.CaptureLimit
	}
	if dev.BodyLimitBytes == 0 {
		dev.BodyLimitBytes = def.BodyLimitBytes
	}
	if dev.HeaderValueLimitBytes == 0 {
		dev.HeaderValueLimitBytes = def.HeaderValueLimitBytes
	}
	if len(dev.RedactHeaders) == 0 {
		dev.RedactHeaders = append([]string(nil), def.RedactHeaders...)
	}
	if len(dev.RedactQueryParams) == 0 {
		dev.RedactQueryParams = append([]string(nil), def.RedactQueryParams...)
	}
	return developerSettingsPayload{
		Enabled:               dev.Enabled,
		MITMEnabled:           dev.MITMEnabled,
		CaptureLimit:          dev.CaptureLimit,
		BodyLimitBytes:        dev.BodyLimitBytes,
		HeaderValueLimitBytes: dev.HeaderValueLimitBytes,
		RedactHeaders:         append([]string(nil), dev.RedactHeaders...),
		RedactQueryParams:     append([]string(nil), dev.RedactQueryParams...),
		BackupPath:            backupPath,
	}
}

func normalizeDeveloperNameList(label string, values, fallback []string) ([]string, error) {
	out := make([]string, 0, len(values))
	for i, raw := range values {
		name := strings.TrimSpace(strings.ToLower(raw))
		if name == "" {
			return nil, fmt.Errorf("%s[%d] must not be empty", label, i)
		}
		out = append(out, name)
	}
	if len(out) == 0 {
		return append([]string(nil), fallback...), nil
	}
	return out, nil
}
