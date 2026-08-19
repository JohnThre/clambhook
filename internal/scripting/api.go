package scripting

import (
	"fmt"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/dop251/goja"
)

// bindAPI installs the ClambHook global API into a runtime. Each module gets
// its own persistent-store namespace and logging buffer.
func (m *Manager) bindAPI(rt *goja.Runtime, moduleName string, _allowNet config.ModuleNetworkConfig) error {
	moduleSafe := normalizeModuleName(moduleName)
	api := map[string]any{
		"log": m.makeLogFunc(moduleSafe),
		"persistentStore": map[string]any{
			"read": func(key string) string {
				if m.store == nil {
					return ""
				}
				v, _, _ := m.store.Read(moduleSafe, key)
				return v
			},
			"write": func(key, value string) {
				if m.store == nil {
					return
				}
				_ = m.store.Write(moduleSafe, key, value)
			},
			"remove": func(key string) {
				if m.store == nil {
					return
				}
				_ = m.store.Remove(moduleSafe, key)
			},
		},
	}
	return rt.Set("clambhook", api)
}

func (m *Manager) makeLogFunc(module string) func(any) {
	return func(v any) {
		msg := fmt.Sprint(v)
		m.log(module, msg)
	}
}

// log records a module log entry. It is safe for concurrent use.
func (m *Manager) log(module, message string) {
	entry := LogEntry{
		Time:    time.Now().Unix(),
		Module:  module,
		Message: message,
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.moduleLogs == nil {
		m.moduleLogs = make(map[string][]LogEntry)
	}
	logs := append(m.moduleLogs[module], entry)
	if len(logs) > m.maxLogs {
		logs = logs[len(logs)-m.maxLogs:]
	}
	m.moduleLogs[module] = logs
}

// logsForModule returns the buffered log entries for a module.
func (m *Manager) logsForModule(name string) []LogEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	module := normalizeModuleName(name)
	out := make([]LogEntry, len(m.moduleLogs[module]))
	copy(out, m.moduleLogs[module])
	return out
}
