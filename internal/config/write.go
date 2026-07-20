package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// MaxConfigBackups is the number of timestamped plaintext config backups
// retained beside the active config. After a successful write the oldest
// backups beyond this count are pruned so a busy edit session (rules, DNS,
// developer, and policy-group saves each write) cannot accumulate an unbounded
// trail of world-readable-adjacent copies. A small window is enough to recover
// from a bad edit while keeping the on-disk footprint bounded.
const MaxConfigBackups = 5

const backupSuffix = ".bak"

// WriteResult describes files created by WriteAtomicWithBackup.
type WriteResult struct {
	BackupPath string
}

// WriteAtomicWithBackup writes cfg as normalized TOML, replacing path
// atomically and keeping a timestamped backup when an existing file is present.
// Backups are capped at MaxConfigBackups: after a successful write the oldest
// backups are pruned, leaving only the newest MaxConfigBackups.
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
		backupPath, err := uniqueBackupPath(path)
		if err != nil {
			return WriteResult{}, err
		}
		if err := os.WriteFile(backupPath, existing, 0o600); err != nil {
			return WriteResult{}, fmt.Errorf("write backup: %w", err)
		}
		result.BackupPath = backupPath
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

	// Prune only after the active config is safely in place, so a failed
	// write never destroys recovery points.
	pruneBackups(path)
	return result, nil
}

// uniqueBackupPath returns a timestamped backup path that does not yet exist.
// Nanosecond timestamps can collide when several writes land in the same tick;
// bumping the counter keeps names unique and monotonically ordered so pruning
// removes the genuinely oldest copies.
func uniqueBackupPath(path string) (string, error) {
	ts := time.Now().UnixNano()
	for i := 0; i < 1024; i++ {
		candidate := fmt.Sprintf("%s.%d%s", path, ts, backupSuffix)
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("stat backup: %w", err)
		}
		ts++
	}
	return "", fmt.Errorf("unable to allocate backup path for %q", path)
}

// pruneBackups removes the oldest timestamped backups for path, keeping only
// the newest MaxConfigBackups. Errors are ignored: pruning is best-effort
// housekeeping and must never fail an otherwise successful write.
func pruneBackups(path string) {
	backups := listBackups(path)
	if len(backups) <= MaxConfigBackups {
		return
	}
	for _, old := range backups[:len(backups)-MaxConfigBackups] {
		_ = os.Remove(old)
	}
}

// listBackups returns backup paths for path sorted oldest-first by their
// embedded nanosecond timestamp.
func listBackups(path string) []string {
	dir := filepath.Dir(path)
	prefix := filepath.Base(path) + "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type backup struct {
		path string
		ts   int64
	}
	var found []backup
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, backupSuffix) {
			continue
		}
		mid := name[len(prefix) : len(name)-len(backupSuffix)]
		ts, err := strconv.ParseInt(mid, 10, 64)
		if err != nil {
			continue
		}
		found = append(found, backup{path: filepath.Join(dir, name), ts: ts})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].ts < found[j].ts })
	paths := make([]string, len(found))
	for i, b := range found {
		paths[i] = b.path
	}
	return paths
}
