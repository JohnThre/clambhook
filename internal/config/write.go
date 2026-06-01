package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// WriteResult describes files created by WriteAtomicWithBackup.
type WriteResult struct {
	BackupPath string
}

// WriteAtomicWithBackup writes cfg as normalized TOML, replacing path
// atomically and keeping a timestamped backup when an existing file is present.
func WriteAtomicWithBackup(path string, cfg *Config) (WriteResult, error) {
	if path == "" {
		return WriteResult{}, fmt.Errorf("config path is required")
	}
	if cfg == nil {
		return WriteResult{}, fmt.Errorf("config is nil")
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return WriteResult{}, fmt.Errorf("encode config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return WriteResult{}, fmt.Errorf("create config dir: %w", err)
	}

	var result WriteResult
	if existing, err := os.ReadFile(path); err == nil && len(existing) > 0 {
		result.BackupPath = fmt.Sprintf("%s.%d.bak", path, time.Now().UnixNano())
		if err := os.WriteFile(result.BackupPath, existing, 0o600); err != nil {
			return WriteResult{}, fmt.Errorf("write backup: %w", err)
		}
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".clambhook-*.toml")
	if err != nil {
		return WriteResult{}, fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return WriteResult{}, fmt.Errorf("chmod temp config: %w", err)
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		return WriteResult{}, fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return WriteResult{}, fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return WriteResult{}, fmt.Errorf("replace config: %w", err)
	}
	return result, nil
}
