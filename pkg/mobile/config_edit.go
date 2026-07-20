package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	api "github.com/JohnThre/clambhook/internal/api"
	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
	"github.com/JohnThre/clambhook/internal/geo"
	"github.com/JohnThre/clambhook/internal/ruleset"
	"github.com/JohnThre/clambhook/internal/subscription"
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
	if err := engine.ValidateConfig(cfg); err != nil {
		return err
	}
	stack, chains, err := engine.BuildPacketStackForConfig(cfg, nil, discardPacketWriter{})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := stack.Start(ctx); err != nil {
		_ = stack.Stop()
		_ = closePacketChains(chains)
		return err
	}
	if err := stack.Stop(); err != nil {
		_ = closePacketChains(chains)
		return err
	}
	return closePacketChains(chains)
}

// TunnelConfigDashboardJSON returns profile, server, and rule data directly
// from configPath. It lets the Android app render onboarding/config screens
// before the tunnel runtime is connected.
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
		Servers:           serversForConfig(cfg, geoReader),
		Rules:             rulesForConfig(cfg),
		PolicyGroups:      policyGroupsForConfig(cfg),
		RuleSets:          ruleSetsForConfig(cfg),
		RuleSubscriptions: ruleSubscriptionsForConfig(cfg),
		Traffic:           empty.Snapshot("all", 200),
		DNS:               dnsForConfig(cfg),
		NetworkSettings:   networkSettingsForConfig(cfg),
	}
	return marshalString(payload)
}

// ReplaceTunnelRulesJSON replaces the active profile's ordered rules and
// writes the config atomically. rulesJSON must encode []config.RuleConfig.
func ReplaceTunnelRulesJSON(configPath, profileName, rulesJSON string) error {
	cfg, err := loadEditableTunnelConfig(configPath)
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

// AppendTunnelRuleJSON appends one rule to the selected profile and writes the
// tunnel config atomically. ruleJSON must encode config.RuleConfig.
func AppendTunnelRuleJSON(configPath, profileName, ruleJSON string) (string, error) {
	cfg, err := loadEditableTunnelConfig(configPath)
	if err != nil {
		return "", err
	}
	var rule config.RuleConfig
	if err := json.Unmarshal([]byte(ruleJSON), &rule); err != nil {
		return "", fmt.Errorf("parse rule: %w", err)
	}
	profile := selectProfileForEdit(cfg, profileName)
	if profile == nil {
		return "", fmt.Errorf("profile %q not found", profileName)
	}
	profile.Rules = append(profile.Rules, rule)
	if err := engine.ValidateConfig(cfg); err != nil {
		return "", err
	}
	if err := writeTunnelConfig(configPath, cfg); err != nil {
		return "", err
	}
	return marshalString(rulesPayload{Profile: profile.Name, Rules: profile.Rules})
}

// appendConnectionRuleToTunnelConfig derives a rule from a tracked connection
// and appends it to the selected profile in configPath, writing atomically.
func appendConnectionRuleToTunnelConfig(configPath, profileName string, conn traffic.Connection, name, action, scope string) (string, error) {
	cfg, err := loadEditableTunnelConfig(configPath)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(profileName) == "" {
		profileName = conn.Profile
	}
	profile := selectProfileForEdit(cfg, profileName)
	if profile == nil {
		return "", fmt.Errorf("profile %q not found", profileName)
	}
	rule, err := api.RuleFromConnection(profile, conn, name, action, scope)
	if err != nil {
		return "", err
	}
	rule.Name = uniqueMobileRuleName(profile.Rules, rule.Name)
	profile.Rules = append(profile.Rules, rule)
	if err := engine.ValidateConfig(cfg); err != nil {
		return "", err
	}
	if err := writeTunnelConfig(configPath, cfg); err != nil {
		return "", err
	}
	return marshalString(rulesPayload{Profile: profile.Name, Rules: profile.Rules})
}

// applyTunnelCleanupToConfig applies a cleanup suggestion to the persisted
// config. The request is rejected as stale unless suggestions contains a live
// match, mirroring the daemon's staleness guard.
func applyTunnelCleanupToConfig(configPath, profileName string, suggestions []traffic.CleanupSuggestion, kind, ruleName, targetRuleName, operation string) (string, error) {
	kind = strings.TrimSpace(kind)
	ruleName = strings.TrimSpace(ruleName)
	targetRuleName = strings.TrimSpace(targetRuleName)
	operation = strings.TrimSpace(operation)
	if kind == "" {
		return "", errors.New("kind is required")
	}
	if ruleName == "" {
		return "", errors.New("rule_name is required")
	}
	if targetRuleName == "" {
		return "", errors.New("target_rule_name is required")
	}
	if operation != "delete_rule" && operation != "move_rule_to_end" {
		return "", errors.New("operation must be delete_rule or move_rule_to_end")
	}
	matched := false
	for _, suggestion := range suggestions {
		if suggestion.Kind == kind && suggestion.RuleName == ruleName &&
			suggestion.TargetRuleName == targetRuleName && suggestion.Operation == operation {
			matched = true
			break
		}
	}
	if !matched {
		return "", errors.New("cleanup suggestion is stale")
	}
	cfg, err := loadEditableTunnelConfig(configPath)
	if err != nil {
		return "", err
	}
	profile := selectProfileForEdit(cfg, profileName)
	if profile == nil {
		return "", fmt.Errorf("profile %q not found", profileName)
	}
	idx := indexMobileRuleByName(profile.Rules, targetRuleName)
	if idx < 0 {
		return "", errors.New("cleanup target rule not found")
	}
	switch operation {
	case "delete_rule":
		profile.Rules = append(profile.Rules[:idx:idx], profile.Rules[idx+1:]...)
	case "move_rule_to_end":
		if idx != len(profile.Rules)-1 {
			rule := profile.Rules[idx]
			profile.Rules = append(profile.Rules[:idx:idx], profile.Rules[idx+1:]...)
			profile.Rules = append(profile.Rules, rule)
		}
	}
	if err := engine.ValidateConfig(cfg); err != nil {
		return "", err
	}
	if err := writeTunnelConfig(configPath, cfg); err != nil {
		return "", err
	}
	return marshalString(rulesPayload{Profile: profile.Name, Rules: profile.Rules})
}

func uniqueMobileRuleName(existing []config.RuleConfig, base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "connection-rule"
	}
	used := make(map[string]struct{}, len(existing))
	for _, rule := range existing {
		used[rule.Name] = struct{}{}
	}
	if _, exists := used[base]; !exists {
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s-%d", base, suffix)
		if _, exists := used[candidate]; !exists {
			return candidate
		}
	}
}

func indexMobileRuleByName(rules []config.RuleConfig, name string) int {
	for i := range rules {
		if rules[i].Name == name {
			return i
		}
	}
	return -1
}

// SelectPolicyGroupJSON updates a select policy group's selected chain and
// writes the tunnel config atomically.
func SelectPolicyGroupJSON(configPath, profileName, groupName, chainName string) (string, error) {
	cfg, err := loadEditableTunnelConfig(configPath)
	if err != nil {
		return "", err
	}
	profile := selectProfileForEdit(cfg, profileName)
	if profile == nil {
		return "", fmt.Errorf("profile %q not found", profileName)
	}
	groupName = strings.TrimSpace(groupName)
	chainName = strings.TrimSpace(chainName)
	if groupName == "" || chainName == "" {
		return "", fmt.Errorf("group and chain are required")
	}
	var group *config.PolicyGroupConfig
	for i := range profile.PolicyGroups {
		if profile.PolicyGroups[i].Name == groupName {
			group = &profile.PolicyGroups[i]
			break
		}
	}
	if group == nil {
		return "", fmt.Errorf("policy group %q not found", groupName)
	}
	if !strings.EqualFold(strings.TrimSpace(group.Type), "select") {
		return "", fmt.Errorf("policy group %q is %s, not select", groupName, group.Type)
	}
	member := false
	for _, chain := range group.Chains {
		if chain == chainName {
			member = true
			break
		}
	}
	if !member {
		return "", fmt.Errorf("policy group %q has no member chain %q", groupName, chainName)
	}
	group.Selected = chainName
	if err := engine.ValidateConfig(cfg); err != nil {
		return "", err
	}
	if err := writeTunnelConfig(configPath, cfg); err != nil {
		return "", err
	}
	return marshalString(policyGroupsForConfig(cfg))
}

// ReplaceTunnelPolicyGroupsJSON replaces the active profile's policy groups
// and writes the config atomically. policyGroupsJSON must encode
// []config.PolicyGroupConfig.
func ReplaceTunnelPolicyGroupsJSON(configPath, profileName, policyGroupsJSON string) error {
	cfg, err := loadEditableTunnelConfig(configPath)
	if err != nil {
		return err
	}
	var groups []config.PolicyGroupConfig
	if err := json.Unmarshal([]byte(policyGroupsJSON), &groups); err != nil {
		return fmt.Errorf("parse policy groups: %w", err)
	}
	profile := selectProfileForEdit(cfg, profileName)
	if profile == nil {
		return fmt.Errorf("profile %q not found", profileName)
	}
	profile.PolicyGroups = groups
	if err := engine.ValidateConfig(cfg); err != nil {
		return err
	}
	return writeTunnelConfig(configPath, cfg)
}

// RuleSetsJSON returns rule-set cache status for a profile.
func RuleSetsJSON(configPath, profileName string) (string, error) {
	cfg, err := loadTunnelConfig(configPath)
	if err != nil {
		return "", err
	}
	if err := engine.ValidateConfig(cfg); err != nil {
		return "", err
	}
	profile := selectProfileForEdit(cfg, profileName)
	if profile == nil {
		return "", fmt.Errorf("profile %q not found", profileName)
	}
	statusesPayload, err := ruleset.StatusPayloadForProfile(cfg, profileName)
	if err != nil {
		return "", err
	}
	return marshalString(ruleSetsPayload{
		Profile:  profile.Name,
		RuleSets: append([]config.RuleSetConfig(nil), profile.RuleSets...),
		Statuses: statusesPayload.RuleSets,
	})
}

// ReplaceTunnelRuleSetsJSON replaces the active profile's named rule sets and
// writes the config atomically. ruleSetsJSON must encode []config.RuleSetConfig.
func ReplaceTunnelRuleSetsJSON(configPath, profileName, ruleSetsJSON string) error {
	cfg, err := loadEditableTunnelConfig(configPath)
	if err != nil {
		return err
	}
	var ruleSets []config.RuleSetConfig
	if err := json.Unmarshal([]byte(ruleSetsJSON), &ruleSets); err != nil {
		return fmt.Errorf("parse rule sets: %w", err)
	}
	profile := selectProfileForEdit(cfg, profileName)
	if profile == nil {
		return fmt.Errorf("profile %q not found", profileName)
	}
	profile.RuleSets = ruleSets
	if err := engine.ValidateConfig(cfg); err != nil {
		return err
	}
	return writeTunnelConfig(configPath, cfg)
}

// RefreshRuleSetsJSON refreshes selected enabled remote rule sets. namesJSON
// must encode []string; an empty string or [] refreshes all enabled sets.
func RefreshRuleSetsJSON(configPath, profileName, namesJSON string) (string, error) {
	cfg, err := loadTunnelConfig(configPath)
	if err != nil {
		return "", err
	}
	if err := engine.ValidateConfig(cfg); err != nil {
		return "", err
	}
	var names []string
	if strings.TrimSpace(namesJSON) != "" {
		if err := json.Unmarshal([]byte(namesJSON), &names); err != nil {
			return "", fmt.Errorf("parse rule set names: %w", err)
		}
	}
	payload, err := ruleset.RefreshProfile(context.Background(), cfg, profileName, names, nil)
	if err != nil {
		return "", err
	}
	profile := selectProfileForEdit(cfg, profileName)
	if profile == nil {
		return "", fmt.Errorf("profile %q not found", profileName)
	}
	return marshalString(ruleSetsPayload{
		Profile:  profile.Name,
		RuleSets: append([]config.RuleSetConfig(nil), profile.RuleSets...),
		Statuses: payload.RuleSets,
	})
}

// RuleSubscriptionsJSON returns rule subscription cache status for a profile.
func RuleSubscriptionsJSON(configPath, profileName string) (string, error) {
	cfg, err := loadTunnelConfig(configPath)
	if err != nil {
		return "", err
	}
	if err := engine.ValidateConfig(cfg); err != nil {
		return "", err
	}
	payload, err := subscription.StatusPayloadForProfile(cfg, profileName)
	if err != nil {
		return "", err
	}
	return marshalString(payload)
}

// ReplaceTunnelRuleSubscriptionsJSON replaces the active profile's rule
// subscriptions and writes the config atomically. subscriptionsJSON must encode
// []config.RuleSubscriptionConfig.
func ReplaceTunnelRuleSubscriptionsJSON(configPath, profileName, subscriptionsJSON string) error {
	cfg, err := loadEditableTunnelConfig(configPath)
	if err != nil {
		return err
	}
	var subscriptions []config.RuleSubscriptionConfig
	if err := json.Unmarshal([]byte(subscriptionsJSON), &subscriptions); err != nil {
		return fmt.Errorf("parse rule subscriptions: %w", err)
	}
	profile := selectProfileForEdit(cfg, profileName)
	if profile == nil {
		return fmt.Errorf("profile %q not found", profileName)
	}
	profile.RuleSubscriptions = subscriptions
	if err := engine.ValidateConfig(cfg); err != nil {
		return err
	}
	return writeTunnelConfig(configPath, cfg)
}

// RefreshRuleSubscriptionsJSON refreshes selected enabled rule subscriptions.
// namesJSON must encode []string; an empty string or [] refreshes all enabled
// subscriptions for the selected profile.
func RefreshRuleSubscriptionsJSON(configPath, profileName, namesJSON string) (string, error) {
	cfg, err := loadTunnelConfig(configPath)
	if err != nil {
		return "", err
	}
	if err := engine.ValidateConfig(cfg); err != nil {
		return "", err
	}
	var names []string
	if strings.TrimSpace(namesJSON) != "" {
		if err := json.Unmarshal([]byte(namesJSON), &names); err != nil {
			return "", fmt.Errorf("parse subscription names: %w", err)
		}
	}
	payload, err := subscription.RefreshProfile(context.Background(), cfg, profileName, names, nil)
	if err != nil {
		return "", err
	}
	return marshalString(payload)
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

type tunnelImportReviewPayload struct {
	ActiveProfile string                      `json:"active_profile"`
	Profiles      []tunnelImportReviewProfile `json:"profiles"`
}

type tunnelImportReviewProfile struct {
	Name        string   `json:"name"`
	ChainCount  int      `json:"chain_count"`
	ServerCount int      `json:"server_count"`
	RuleCount   int      `json:"rule_count"`
	Protocols   []string `json:"protocols"`
}

type reviewedTunnelImportRequest struct {
	ImportText      string                        `json:"import_text"`
	Profiles        []reviewedTunnelImportProfile `json:"profiles"`
	ActivateProfile string                        `json:"activate_profile"`
}

type reviewedTunnelImportProfile struct {
	SourceName string `json:"source_name"`
	TargetName string `json:"target_name"`
}

// TunnelImportReviewJSON parses decoded import TOML and returns profile
// summaries for the native review UI. It never reads or writes the active
// tunnel config.
func TunnelImportReviewJSON(importText string) (string, error) {
	cfg, err := parseTunnelImportConfig(importText)
	if err != nil {
		return "", err
	}
	return marshalString(tunnelImportReviewPayload{
		ActiveProfile: cfg.Active,
		Profiles:      reviewProfiles(cfg.Profiles),
	})
}

// ValidateReviewedTunnelImportJSON validates the reviewed import merge against
// configPath without writing any file.
func ValidateReviewedTunnelImportJSON(configPath, requestJSON string) error {
	_, err := buildReviewedTunnelImportConfig(configPath, requestJSON)
	return err
}

// ApplyReviewedTunnelImportJSON validates, merges, and writes reviewed import
// profiles into configPath. It only changes the active profile when the request
// names activate_profile.
func ApplyReviewedTunnelImportJSON(configPath, requestJSON string) error {
	cfg, err := buildReviewedTunnelImportConfig(configPath, requestJSON)
	if err != nil {
		return err
	}
	return writeTunnelConfig(configPath, cfg)
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

	cfg, err := loadEditableTunnelConfig(configPath)
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
	if err := engine.ValidateConfig(cfg); err != nil {
		return err
	}
	return writeTunnelConfig(configPath, cfg)
}

// SetActiveTunnelProfileConfig sets the active profile in configPath and writes
// the updated tunnel config atomically.
func SetActiveTunnelProfileConfig(configPath, profileName string) error {
	cfg, err := loadEditableTunnelConfig(configPath)
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
	if err := engine.ValidateConfig(cfg); err != nil {
		return err
	}
	return writeTunnelConfig(configPath, cfg)
}

func parseTunnelImportConfig(importText string) (*config.Config, error) {
	importText = strings.TrimSpace(importText)
	if importText == "" {
		return nil, fmt.Errorf("import text is required")
	}
	cfg := config.Config{Traffic: config.DefaultTrafficConfig()}
	if err := toml.Unmarshal([]byte(importText), &cfg); err != nil {
		return nil, fmt.Errorf("parse import config: %w", err)
	}
	if err := engine.ValidateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("validate import config: %w", err)
	}
	return &cfg, nil
}

func reviewProfiles(profiles []config.Profile) []tunnelImportReviewProfile {
	rows := make([]tunnelImportReviewProfile, 0, len(profiles))
	for _, profile := range profiles {
		protocols := map[string]struct{}{}
		serverCount := 0
		for _, ch := range profile.Chains {
			for _, server := range ch.Servers {
				serverCount++
				proto := strings.TrimSpace(strings.ToLower(server.Protocol))
				if proto != "" {
					protocols[proto] = struct{}{}
				}
			}
		}
		protocolNames := make([]string, 0, len(protocols))
		for protocolName := range protocols {
			protocolNames = append(protocolNames, protocolName)
		}
		sort.Strings(protocolNames)
		rows = append(rows, tunnelImportReviewProfile{
			Name:        profile.Name,
			ChainCount:  len(profile.Chains),
			ServerCount: serverCount,
			RuleCount:   len(profile.Rules),
			Protocols:   protocolNames,
		})
	}
	return rows
}

func buildReviewedTunnelImportConfig(configPath, requestJSON string) (*config.Config, error) {
	var req reviewedTunnelImportRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return nil, fmt.Errorf("parse reviewed import request: %w", err)
	}
	importCfg, err := parseTunnelImportConfig(req.ImportText)
	if err != nil {
		return nil, err
	}
	if len(req.Profiles) == 0 {
		return nil, fmt.Errorf("select at least one profile")
	}

	cfg, err := loadEditableTunnelConfig(configPath)
	if err != nil {
		if !isMissingConfigError(err) {
			return nil, err
		}
		cfg = &config.Config{Traffic: config.DefaultTrafficConfig()}
	}
	placeholder := isPlaceholderConfig(cfg)
	if cfg.Traffic == (config.TrafficConfig{}) {
		cfg.Traffic = config.DefaultTrafficConfig()
	}

	existingNames := make(map[string]struct{}, len(cfg.Profiles))
	if !placeholder {
		for _, profile := range cfg.Profiles {
			existingNames[profile.Name] = struct{}{}
		}
	}
	selectedNames := make(map[string]struct{}, len(req.Profiles))
	nextProfiles := make([]config.Profile, 0, len(req.Profiles))
	for i, row := range req.Profiles {
		sourceName := strings.TrimSpace(row.SourceName)
		targetName := strings.TrimSpace(row.TargetName)
		if sourceName == "" {
			return nil, fmt.Errorf("profile %d: source_name is required", i)
		}
		if targetName == "" {
			return nil, fmt.Errorf("profile %q: target_name is required", sourceName)
		}
		if targetName != row.TargetName {
			return nil, fmt.Errorf("profile %q: target_name must not have surrounding whitespace", sourceName)
		}
		if _, ok := selectedNames[targetName]; ok {
			return nil, fmt.Errorf("profile %q: duplicate target name", targetName)
		}
		if _, ok := existingNames[targetName]; ok {
			return nil, fmt.Errorf("profile %q already exists", targetName)
		}
		source, ok := importCfg.ProfileByName(sourceName)
		if !ok {
			return nil, fmt.Errorf("import profile %q not found", sourceName)
		}
		selectedNames[targetName] = struct{}{}
		next := *source
		next.Name = targetName
		nextProfiles = append(nextProfiles, next)
	}

	if placeholder {
		cfg.Profiles = nextProfiles
	} else {
		cfg.Profiles = append(cfg.Profiles, nextProfiles...)
	}
	activateProfile := strings.TrimSpace(req.ActivateProfile)
	if activateProfile != req.ActivateProfile {
		return nil, fmt.Errorf("activate_profile must not have surrounding whitespace")
	}
	if activateProfile != "" {
		if _, ok := selectedNames[activateProfile]; !ok {
			return nil, fmt.Errorf("activate_profile %q was not selected", activateProfile)
		}
		cfg.Active = activateProfile
	} else if placeholder || cfg.Active == "" {
		cfg.Active = nextProfiles[0].Name
	}
	if err := engine.ValidateConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
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

// loadEditableTunnelConfig loads configPath for a persisted edit. Unlike
// loadTunnelConfig it deliberately does NOT apply the mobile runtime overlay:
// it must not force-enable TUN or inject default routes into every profile, and
// it must not reset the user's [developer] section to mobile defaults. A
// persisted rule/policy/config edit changes only its target and leaves
// unrelated profiles and sections byte-semantically unchanged; the TUN and
// developer-safety overlays are applied in memory at runtime start instead.
func loadEditableTunnelConfig(configPath string) (*config.Config, error) {
	return loadConfig(configPath, defaultAPIAddr)
}

func writeTunnelConfig(configPath string, cfg *config.Config) error {
	configPath = strings.TrimSpace(configPath)
	_, err := config.WriteAtomicWithBackup(configPath, cfg)
	return err
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

type discardPacketWriter struct{}

func (discardPacketWriter) WritePacket([]byte) error { return nil }
