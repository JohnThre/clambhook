package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
)

type configSettingsPayload struct {
	Profile    string                      `json:"profile"`
	Listen     configSettingsListenPayload `json:"listen"`
	DNS        config.DNSConfig            `json:"dns"`
	BackupPath string                      `json:"backup_path,omitempty"`
}

type configSettingsListenPayload struct {
	SOCKS5      string `json:"socks5"`
	SOCKS5Chain string `json:"socks5_chain"`
	HTTP        string `json:"http"`
	HTTPChain   string `json:"http_chain"`
}

type updateConfigSettingsRequest struct {
	Profile string                      `json:"profile"`
	Listen  *updateConfigListenSettings `json:"listen,omitempty"`
	DNS     *config.DNSConfig           `json:"dns,omitempty"`
}

type updateConfigListenSettings struct {
	SOCKS5      *string `json:"socks5,omitempty"`
	SOCKS5Chain *string `json:"socks5_chain,omitempty"`
	HTTP        *string `json:"http,omitempty"`
	HTTPChain   *string `json:"http_chain,omitempty"`
}

func (s *Server) handleConfigSettings(w http.ResponseWriter, r *http.Request) {
	cfg := s.engine.Config()
	profile, err := selectAPIProfile(cfg, r.URL.Query().Get("profile"))
	if err != nil {
		writeProfileSelectionError(w, err)
		return
	}
	writeJSON(w, configSettingsSnapshot(profile, ""))
}

func (s *Server) handleUpdateConfigSettings(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.configPath) == "" {
		http.Error(w, "config settings persistence requires daemon config path", http.StatusConflict)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	var req updateConfigSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	payload, err := s.persistConfigSettings(req)
	if err != nil {
		writeRulePersistenceError(w, err)
		return
	}
	writeJSON(w, payload)
}

func (s *Server) persistConfigSettings(req updateConfigSettingsRequest) (configSettingsPayload, error) {
	cfg, err := config.Load(s.configPath)
	if err != nil {
		return configSettingsPayload{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
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
		return configSettingsPayload{}, rulePersistenceError{status: http.StatusNotFound, err: fmt.Errorf("profile not found")}
	}
	if req.Listen != nil {
		applyConfigListenSettings(&profile.Listen, req.Listen)
	}
	if req.DNS != nil {
		profile.DNS = *req.DNS
	}
	if err := engine.ValidateConfig(cfg); err != nil {
		return configSettingsPayload{}, rulePersistenceError{status: http.StatusBadRequest, err: err}
	}
	result, err := config.WriteAtomicWithBackup(s.configPath, cfg)
	if err != nil {
		return configSettingsPayload{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
	}
	if err := s.engine.Reload(cfg); err != nil {
		return configSettingsPayload{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
	}
	return configSettingsSnapshot(profile, result.BackupPath), nil
}

func applyConfigListenSettings(listen *config.ListenConfig, req *updateConfigListenSettings) {
	if req.SOCKS5 != nil {
		listen.SOCKS5 = strings.TrimSpace(*req.SOCKS5)
	}
	if req.SOCKS5Chain != nil {
		listen.SOCKS5Chain = strings.TrimSpace(*req.SOCKS5Chain)
	}
	if req.HTTP != nil {
		listen.HTTP = strings.TrimSpace(*req.HTTP)
	}
	if req.HTTPChain != nil {
		listen.HTTPChain = strings.TrimSpace(*req.HTTPChain)
	}
}

func configSettingsSnapshot(profile *config.Profile, backupPath string) configSettingsPayload {
	if profile == nil {
		return configSettingsPayload{}
	}
	return configSettingsPayload{
		Profile: profile.Name,
		Listen: configSettingsListenPayload{
			SOCKS5:      profile.Listen.SOCKS5,
			SOCKS5Chain: profile.Listen.SOCKS5Chain,
			HTTP:        profile.Listen.HTTP,
			HTTPChain:   profile.Listen.HTTPChain,
		},
		DNS:        profile.DNS,
		BackupPath: backupPath,
	}
}
