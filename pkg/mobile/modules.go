package mobile

import (
	"encoding/json"
	"fmt"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/scripting"
)

// ModulesJSON returns the configured modules as a JSON string. The config file
// is loaded directly; if configPath is empty an empty list is returned.
func ModulesJSON(configPath string) (string, error) {
	modules := []config.ModuleConfig{}
	if configPath != "" {
		cfg, err := config.Load(configPath)
		if err != nil {
			return "", fmt.Errorf("load config: %w", err)
		}
		modules = cfg.Modules
	}
	data, err := json.Marshal(map[string]any{
		"modules": modules,
	})
	if err != nil {
		return "", fmt.Errorf("marshal modules: %w", err)
	}
	return string(data), nil
}

// ReplaceModulesJSON replaces the top-level module list and writes the config
// atomically. modulesJSON must encode []config.ModuleConfig.
func ReplaceModulesJSON(configPath, modulesJSON string) error {
	if configPath == "" {
		return fmt.Errorf("config path is required")
	}
	var modules []config.ModuleConfig
	if err := json.Unmarshal([]byte(modulesJSON), &modules); err != nil {
		return fmt.Errorf("parse modules: %w", err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cfg.Modules = modules
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	if _, err := config.WriteAtomicWithBackup(configPath, cfg); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// ModuleLogsJSON returns recent log entries for a module as a JSON string.
// Because logs live in the daemon runtime, this static helper returns an
// empty list; live logs are fetched through the HTTP API or a TunnelRuntime
// method.
func ModuleLogsJSON(moduleID string) (string, error) {
	data, err := json.Marshal(map[string]any{
		"module": moduleID,
		"logs":   []scripting.LogEntry{},
	})
	if err != nil {
		return "", fmt.Errorf("marshal logs: %w", err)
	}
	return string(data), nil
}
