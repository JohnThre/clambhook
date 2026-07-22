package api

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/engine"
)

// maxConfigTransferBytes bounds both config export and import bodies. It is
// intentionally the same value in both directions so export/import is a
// symmetric round-trip contract: any config the daemon can export, a caller
// can import.
const maxConfigTransferBytes = 4 << 20

func (s *Server) handleExportConfig(w http.ResponseWriter, r *http.Request) {
	configPath := strings.TrimSpace(s.configPath)
	if configPath == "" {
		http.Error(w, "config export requires daemon config path", http.StatusConflict)
		return
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		http.Error(w, "read config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if int64(len(data)) > maxConfigTransferBytes {
		http.Error(w, "config exceeds export size limit", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="clambhook.toml"`)
	_, _ = w.Write(data)
}

func (s *Server) handleImportConfig(w http.ResponseWriter, r *http.Request) {
	configPath := strings.TrimSpace(s.configPath)
	if configPath == "" {
		http.Error(w, "config import requires daemon config path", http.StatusConflict)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxConfigTransferBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	incoming := strings.TrimSpace(string(body))
	if incoming == "" {
		http.Error(w, "empty config body", http.StatusBadRequest)
		return
	}
	var cfg config.Config
	cfg.Traffic = config.DefaultTrafficConfig()
	cfg.Developer = config.DefaultDeveloperConfig()
	if err := toml.Unmarshal([]byte(incoming), &cfg); err != nil {
		http.Error(w, "parse config: "+err.Error(), http.StatusBadRequest)
		return
	}
	cfg.Path = configPath
	// Serialize the validate-write-reload transaction so an import cannot
	// interleave with a concurrent config edit and drop either change.
	defer s.lockConfigTxn()()
	if err := engine.ValidateConfig(&cfg); err != nil {
		http.Error(w, "validate config: "+err.Error(), http.StatusBadRequest)
		return
	}
	result, err := config.WriteAtomicWithBackup(configPath, &cfg)
	if err != nil {
		http.Error(w, "write config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.engine.Reload(&cfg); err != nil {
		http.Error(w, "reload engine: "+err.Error(), http.StatusInternalServerError)
		return
	}
	names := make([]string, 0, len(cfg.Profiles))
	for _, p := range cfg.Profiles {
		names = append(names, p.Name)
	}
	writeJSON(w, map[string]any{
		"profiles":    names,
		"active":      cfg.Active,
		"backup_path": result.BackupPath,
		"message":     fmt.Sprintf("imported %d profile(s)", len(cfg.Profiles)),
	})
}
