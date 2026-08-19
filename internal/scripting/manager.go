// Package scripting implements the Surge-style scripting engine for ClambHook.
//
// Each enabled module is compiled once into a goja.Program and executed in a
// fresh goja.Runtime per hook invocation. This isolates state and prevents a
// misbehaving script from corrupting the daemon. The runtime exposes a small,
// allowlisted ClambHook API (clambhook.*) plus request/response context
// objects ($request, $response, $done).
package scripting

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/JohnThre/clambhook/internal/config"
)

// Manager owns compiled modules and executes hooks.
type Manager struct {
	mu         sync.RWMutex
	dataDir    string
	modules    []compiledModule
	store      *persistentStore
	moduleLogs map[string][]LogEntry
	enabled    bool
	maxLogs    int
}

// compiledModule holds the runtime-independent compiled form of one module.
type compiledModule struct {
	cfg     config.ModuleConfig
	program *compiledScript
	errs    []string
}

// ModuleStatus describes a module for API/UI consumption.
type ModuleStatus struct {
	Name        string   `json:"name"`
	Enabled     bool     `json:"enabled"`
	ScriptPath  string   `json:"script_path,omitempty"`
	HasRequest  bool     `json:"has_request_hook"`
	HasResponse bool     `json:"has_response_hook"`
	HasCron     bool     `json:"has_cron_hooks"`
	Errors      []string `json:"errors,omitempty"`
}

// LogEntry is one emitted script log line.
type LogEntry struct {
	Time    int64  `json:"time"`
	Module  string `json:"module"`
	Message string `json:"message"`
}

// NewManager creates a scripting manager from the given module configuration.
// dataDir is the directory used for persistent stores and large script caches;
// an empty dataDir disables persistent storage.
func NewManager(modules []config.ModuleConfig, dataDir string) (*Manager, error) {
	m := &Manager{
		dataDir: dataDir,
		maxLogs: 200,
	}
	if dataDir != "" {
		var err error
		m.store, err = newPersistentStore(filepath.Join(dataDir, "module-store"))
		if err != nil {
			return nil, fmt.Errorf("scripting store: %w", err)
		}
	}
	if err := m.Reconfigure(modules); err != nil {
		return nil, err
	}
	return m, nil
}

// Reconfigure rebuilds the compiled module set from a new configuration.
func (m *Manager) Reconfigure(modules []config.ModuleConfig) error {
	compiled := make([]compiledModule, 0, len(modules))
	enabled := false
	for _, mod := range modules {
		if mod.Enabled {
			enabled = true
		}
		cm := compiledModule{cfg: mod}
		if mod.Enabled {
			src, err := moduleSource(mod)
			if err != nil {
				cm.errs = append(cm.errs, err.Error())
			} else {
				cm.program = compileScript(src)
				if cm.program.err != nil {
					cm.errs = append(cm.errs, cm.program.err.Error())
					cm.program = nil
				}
			}
		}
		compiled = append(compiled, cm)
	}
	m.mu.Lock()
	m.modules = compiled
	m.enabled = enabled
	m.mu.Unlock()
	return nil
}

// Enabled reports whether any module is enabled.
func (m *Manager) Enabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled
}

// ModulesSnapshot returns the current module statuses.
func (m *Manager) ModulesSnapshot() []ModuleStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ModuleStatus, 0, len(m.modules))
	for _, cm := range m.modules {
		st := ModuleStatus{
			Name:       cm.cfg.Name,
			Enabled:    cm.cfg.Enabled,
			ScriptPath: cm.cfg.ScriptPath,
			Errors:     append([]string(nil), cm.errs...),
		}
		if cm.program != nil {
			st.HasRequest = cm.program.hasOnRequest
			st.HasResponse = cm.program.hasOnResponse
			st.HasCron = cm.program.hasOnCron
		}
		out = append(out, st)
	}
	return out
}

// ModuleLogs returns recent log lines emitted by a module.
func (m *Manager) ModuleLogs(name string) []LogEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, cm := range m.modules {
		if cm.cfg.Name == name {
			return m.logsForModule(name)
		}
	}
	return nil
}

// Close releases resources held by the manager.
func (m *Manager) Close() error {
	return nil
}

// moduleSource returns the effective script source for a module.
func moduleSource(mod config.ModuleConfig) (string, error) {
	if mod.Script != "" {
		return mod.Script, nil
	}
	if mod.ScriptPath == "" {
		return "", fmt.Errorf("module %q has no script or script_path", mod.Name)
	}
	return readScriptFile(mod.ScriptPath)
}

func readScriptFile(path string) (string, error) {
	data, err := readFileLimited(path, maxScriptSourceBytes)
	if err != nil {
		return "", fmt.Errorf("read script_path %q: %w", path, err)
	}
	return string(data), nil
}

// normalizeModuleName returns a filesystem-safe identifier derived from the
// module name. It is used for log/store scoping only.
func normalizeModuleName(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), " ", "-")
}
