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
	Profile         string                        `json:"profile"`
	Listen          configSettingsListenPayload   `json:"listen"`
	DNS             config.DNSConfig              `json:"dns"`
	NetworkTriggers []config.NetworkTriggerConfig `json:"network_triggers"`
	BackupPath      string                        `json:"backup_path,omitempty"`
}

type configSettingsListenPayload struct {
	SOCKS5      string                   `json:"socks5"`
	SOCKS5Chain string                   `json:"socks5_chain"`
	HTTP        string                   `json:"http"`
	HTTPChain   string                   `json:"http_chain"`
	TUN         configSettingsTUNPayload `json:"tun"`
}

type configSettingsTUNPayload struct {
	Enabled      bool     `json:"enabled"`
	Name         string   `json:"name,omitempty"`
	Chain        string   `json:"chain,omitempty"`
	MTU          int      `json:"mtu,omitempty"`
	Addresses    []string `json:"addresses,omitempty"`
	Routes       []string `json:"routes,omitempty"`
	ExcludeCIDRs []string `json:"exclude_cidrs,omitempty"`
}

type updateConfigSettingsRequest struct {
	Profile         string                         `json:"profile"`
	Listen          *updateConfigListenSettings    `json:"listen,omitempty"`
	DNS             *config.DNSConfig              `json:"dns,omitempty"`
	NetworkTriggers *[]config.NetworkTriggerConfig `json:"network_triggers,omitempty"`
}

type updateConfigListenSettings struct {
	SOCKS5      *string                   `json:"socks5,omitempty"`
	SOCKS5Chain *string                   `json:"socks5_chain,omitempty"`
	HTTP        *string                   `json:"http,omitempty"`
	HTTPChain   *string                   `json:"http_chain,omitempty"`
	TUN         *configSettingsTUNPayload `json:"tun,omitempty"`
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
	// Serialize the whole read-modify-validate-write-reload transaction against
	// every other config mutation so concurrent edits cannot overwrite each other.
	defer s.lockConfigTxn()()
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
	if req.NetworkTriggers != nil {
		profile.NetworkTriggers = sanitizeNetworkTriggers(*req.NetworkTriggers)
	}
	if err := engine.ValidateConfig(cfg); err != nil {
		return configSettingsPayload{}, rulePersistenceError{status: http.StatusBadRequest, err: err}
	}
	result, err := config.WriteAtomicWithBackup(s.configPath, cfg)
	if err != nil {
		return configSettingsPayload{}, rulePersistenceError{status: http.StatusInternalServerError, err: err}
	}
	if err := s.engine.Reload(cfg); err != nil {
		restoreConfigBackup(s.configPath, result.BackupPath)
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
	if req.TUN != nil {
		listen.TUN = &config.TUNConfig{
			Enabled:      req.TUN.Enabled,
			Name:         strings.TrimSpace(req.TUN.Name),
			Chain:        strings.TrimSpace(req.TUN.Chain),
			MTU:          req.TUN.MTU,
			Addresses:    trimStringList(req.TUN.Addresses),
			Routes:       trimStringList(req.TUN.Routes),
			ExcludeCIDRs: trimStringList(req.TUN.ExcludeCIDRs),
		}
		if !req.TUN.Enabled && listen.TUN.Name == "" && listen.TUN.Chain == "" && listen.TUN.MTU == 0 &&
			len(listen.TUN.Addresses) == 0 && len(listen.TUN.Routes) == 0 && len(listen.TUN.ExcludeCIDRs) == 0 {
			listen.TUN = nil
		}
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
			TUN:         configSettingsTUNSnapshot(profile.Listen.TUN),
		},
		DNS:             profile.DNS,
		NetworkTriggers: append([]config.NetworkTriggerConfig(nil), profile.NetworkTriggers...),
		BackupPath:      backupPath,
	}
}

func configSettingsTUNSnapshot(tun *config.TUNConfig) configSettingsTUNPayload {
	if tun == nil {
		return configSettingsTUNPayload{}
	}
	return configSettingsTUNPayload{
		Enabled:      tun.Enabled,
		Name:         tun.Name,
		Chain:        tun.Chain,
		MTU:          tun.MTU,
		Addresses:    append([]string(nil), tun.Addresses...),
		Routes:       append([]string(nil), tun.Routes...),
		ExcludeCIDRs: append([]string(nil), tun.ExcludeCIDRs...),
	}
}

func trimStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// sanitizeNetworkTriggers trims each trigger and drops entries where both the
// SSID and interface are empty, which the engine ignores at runtime.
func sanitizeNetworkTriggers(triggers []config.NetworkTriggerConfig) []config.NetworkTriggerConfig {
	out := make([]config.NetworkTriggerConfig, 0, len(triggers))
	for _, trigger := range triggers {
		ssid := strings.TrimSpace(trigger.SSID)
		iface := strings.TrimSpace(trigger.Interface)
		if ssid == "" && iface == "" {
			continue
		}
		out = append(out, config.NetworkTriggerConfig{SSID: ssid, Interface: iface})
	}
	return out
}
