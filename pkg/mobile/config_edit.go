package mobile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
	"github.com/JohnThre/clambhook/internal/geo"
	"github.com/JohnThre/clambhook/internal/traffic"
)

// ValidateTunnelConfig parses configPath as an on-device tunnel config and
// applies the same runtime validation used before starting a packet tunnel.
func ValidateTunnelConfig(configPath string) error {
	cfg, err := loadTunnelConfig(configPath)
	if err != nil {
		return err
	}
	return engine.ValidateConfig(cfg)
}

// ValidateUsableTunnelConfig rejects the generated placeholder profile before
// applying packet tunnel runtime validation.
func ValidateUsableTunnelConfig(configPath string) error {
	cfg, err := loadTunnelConfig(configPath)
	if err != nil {
		return err
	}
	if isPlaceholderConfig(cfg) {
		return errors.New("tunnel config still contains the placeholder profile")
	}
	return engine.ValidateConfig(cfg)
}

// TunnelConfigDashboardJSON returns profile, server, and rule data directly
// from configPath. It lets the iOS app render onboarding/config screens before
// the NetworkExtension runtime is connected.
func TunnelConfigDashboardJSON(configPath string) (string, error) {
	cfg, err := loadTunnelConfig(configPath)
	if err != nil {
		return "", err
	}
	if err := engine.ValidateConfig(cfg); err != nil {
		return "", err
	}
	geoReader, err := geo.Open(cfg.Geo.Database)
	if err != nil {
		log.Printf("geo: %v; continuing without geo lookups", err)
	}
	if geoReader != nil {
		defer geoReader.Close()
	}
	var empty *traffic.Store
	payload := dashboardPayload{
		Status: statusPayload{Running: false, Profile: activeProfileName(cfg)},
		Profiles: profilesPayload{
			Profiles: profileNames(cfg),
			Active:   activeProfileName(cfg),
		},
		Servers: serversForConfig(cfg, geoReader),
		Rules:   rulesForConfig(cfg),
		Traffic: empty.Snapshot("all", 200),
	}
	return marshalString(payload)
}

// ReplaceTunnelRulesJSON replaces the active profile's ordered rules and
// writes the config atomically. rulesJSON must encode []config.RuleConfig.
func ReplaceTunnelRulesJSON(configPath, profileName, rulesJSON string) error {
	cfg, err := loadTunnelConfig(configPath)
	if err != nil {
		return err
	}
	var rules []config.RuleConfig
	if err := json.Unmarshal([]byte(rulesJSON), &rules); err != nil {
		return fmt.Errorf("parse rules: %w", err)
	}
	profile := selectProfileForEdit(cfg, profileName)
	if profile == nil {
		return fmt.Errorf("profile %q not found", profileName)
	}
	profile.Rules = rules
	if err := engine.ValidateConfig(cfg); err != nil {
		return err
	}
	return writeTunnelConfig(configPath, cfg)
}

type createTunnelProfileRequest struct {
	ProfileName   string         `json:"profile_name"`
	ChainName     string         `json:"chain_name"`
	ServerName    string         `json:"server_name"`
	ServerAddress string         `json:"server_address"`
	Protocol      string         `json:"protocol"`
	Settings      map[string]any `json:"settings"`
	SettingsTOML  string         `json:"settings_toml"`
	Replace       bool           `json:"replace"`
}

// CreateTunnelProfileConfigJSON creates or updates one TUN-enabled profile and
// sets it active. requestJSON encodes createTunnelProfileRequest.
func CreateTunnelProfileConfigJSON(configPath, requestJSON string) error {
	var req createTunnelProfileRequest
	decoder := json.NewDecoder(strings.NewReader(requestJSON))
	decoder.UseNumber()
	if err := decoder.Decode(&req); err != nil {
		return fmt.Errorf("parse profile request: %w", err)
	}
	req = req.normalized()
	if req.Settings == nil {
		settings, err := parseSettingsTOML(req.SettingsTOML)
		if err != nil {
			return err
		}
		req.Settings = settings
	}
	req.Settings = normalizeJSONSettingsMap(req.Settings)

	cfg, err := loadTunnelConfig(configPath)
	if err != nil {
		if !isMissingConfigError(err) {
			return err
		}
		cfg = &config.Config{Traffic: config.DefaultTrafficConfig()}
	}
	profile := config.Profile{
		Name: req.ProfileName,
		Listen: config.ListenConfig{
			TUN: &config.TUNConfig{
				Enabled: true,
				MTU:     defaultTunnelMTU,
				Routes:  append([]string(nil), defaultTunnelRoutes...),
			},
		},
		Chains: []config.ChainConfig{{
			Name: req.ChainName,
			Servers: []config.ServerConfig{{
				Name:     req.ServerName,
				Address:  req.ServerAddress,
				Protocol: req.Protocol,
				Settings: req.Settings,
			}},
		}},
	}
	if req.Replace || isPlaceholderConfig(cfg) {
		cfg.Profiles = []config.Profile{profile}
	} else if existing, ok := cfg.ProfileByName(profile.Name); ok {
		*existing = profile
	} else {
		cfg.Profiles = append(cfg.Profiles, profile)
	}
	cfg.Active = profile.Name
	if cfg.Traffic == (config.TrafficConfig{}) {
		cfg.Traffic = config.DefaultTrafficConfig()
	}
	ensureTunnelConfig(cfg)
	if err := engine.ValidateConfig(cfg); err != nil {
		return err
	}
	return writeTunnelConfig(configPath, cfg)
}

// SetActiveTunnelProfileConfig sets the active profile in configPath and writes
// the updated tunnel config atomically.
func SetActiveTunnelProfileConfig(configPath, profileName string) error {
	cfg, err := loadTunnelConfig(configPath)
	if err != nil {
		return err
	}
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return fmt.Errorf("profile name is required")
	}
	if _, ok := cfg.ProfileByName(profileName); !ok {
		return fmt.Errorf("profile %q not found", profileName)
	}
	cfg.Active = profileName
	ensureTunnelConfig(cfg)
	if err := engine.ValidateConfig(cfg); err != nil {
		return err
	}
	return writeTunnelConfig(configPath, cfg)
}

func (r createTunnelProfileRequest) normalized() createTunnelProfileRequest {
	r.ProfileName = strings.TrimSpace(r.ProfileName)
	r.ChainName = strings.TrimSpace(r.ChainName)
	r.ServerName = strings.TrimSpace(r.ServerName)
	r.ServerAddress = strings.TrimSpace(r.ServerAddress)
	r.Protocol = strings.TrimSpace(strings.ToLower(r.Protocol))
	if r.ProfileName == "" {
		r.ProfileName = "default"
	}
	if r.ChainName == "" {
		r.ChainName = "proxy"
	}
	if r.ServerName == "" {
		r.ServerName = "server"
	}
	return r
}

func parseSettingsTOML(raw string) (map[string]any, error) {
	settings := map[string]any{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return settings, nil
	}
	if _, err := toml.Decode(raw, &settings); err != nil {
		return nil, fmt.Errorf("parse server settings: %w", err)
	}
	return settings, nil
}

func normalizeJSONSettingsMap(settings map[string]any) map[string]any {
	if settings == nil {
		return nil
	}
	normalized, _ := normalizeJSONSettings(settings).(map[string]any)
	return normalized
}

func normalizeJSONSettings(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, nested := range v {
			out[key] = normalizeJSONSettings(nested)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, nested := range v {
			out[i] = normalizeJSONSettings(nested)
		}
		return out
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
		if f, err := v.Float64(); err == nil {
			return f
		}
		return string(v)
	default:
		return v
	}
}

func selectProfileForEdit(cfg *config.Config, profileName string) *config.Profile {
	if cfg == nil {
		return nil
	}
	profileName = strings.TrimSpace(profileName)
	if profileName != "" {
		if profile, ok := cfg.ProfileByName(profileName); ok {
			return profile
		}
		return nil
	}
	profile, err := cfg.ActiveProfile()
	if err != nil {
		return nil
	}
	return profile
}

func writeTunnelConfig(configPath string, cfg *config.Config) error {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return fmt.Errorf("config path is required")
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if existing, err := os.ReadFile(configPath); err == nil && len(existing) > 0 {
		backup := fmt.Sprintf("%s.%d.bak", configPath, time.Now().Unix())
		if err := os.WriteFile(backup, existing, 0o600); err != nil {
			return fmt.Errorf("write backup: %w", err)
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(configPath), ".clambhook-*.toml")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp config: %w", err)
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmpName, configPath); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

func isMissingConfigError(err error) bool {
	return errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "no such file") || strings.Contains(err.Error(), "not exist")
}

func isPlaceholderConfig(cfg *config.Config) bool {
	if cfg == nil || len(cfg.Profiles) != 1 {
		return false
	}
	profile := cfg.Profiles[0]
	if profile.Name != "default" || len(profile.Chains) != 1 || len(profile.Chains[0].Servers) != 1 {
		return false
	}
	server := profile.Chains[0].Servers[0]
	return server.Name == "replace-me" || strings.Contains(server.Address, "proxy.example.com")
}
