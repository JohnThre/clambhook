package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestWriteAtomicWithBackupCapsRetention(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// The first write has no existing file, so it produces no backup. Every
	// subsequent write backs up the current active config. Do enough extra
	// writes that the accumulated backups exceed the retention cap.
	if _, err := WriteAtomicWithBackup(path, validConfig()); err != nil {
		t.Fatalf("initial write: %v", err)
	}
	extraWrites := MaxConfigBackups + 3
	var backups []string
	for range extraWrites {
		res, err := WriteAtomicWithBackup(path, validConfig())
		if err != nil {
			t.Fatalf("write: %v", err)
		}
		if res.BackupPath == "" {
			t.Fatalf("expected a backup path for a write over an existing config")
		}
		backups = append(backups, res.BackupPath)
	}

	remaining := listBackups(path)
	if len(remaining) != MaxConfigBackups {
		t.Fatalf("retained backups = %d, want %d", len(remaining), MaxConfigBackups)
	}

	// The retained set must be the newest MaxConfigBackups, oldest pruned.
	wantNewest := backups[len(backups)-MaxConfigBackups:]
	got := map[string]bool{}
	for _, p := range remaining {
		got[p] = true
	}
	for _, p := range wantNewest {
		if !got[p] {
			t.Fatalf("expected newest backup %q to be retained; remaining=%v", p, remaining)
		}
	}
	for _, p := range backups[:len(backups)-MaxConfigBackups] {
		if got[p] {
			t.Fatalf("expected oldest backup %q to be pruned; remaining=%v", p, remaining)
		}
	}

	// Every retained backup must keep the private 0600 mode.
	for _, p := range remaining {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat backup %q: %v", p, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("backup %q mode = %o, want 0600", p, perm)
		}
	}

	// The active config must remain mode 0600 and still load and validate.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat active config: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("active config mode = %o, want 0600", perm)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("active config invalid after writes: %v", err)
	}
}

// TestExampleConfigDecodesAsTOML is a parse-only guard: the public template
// intentionally uses placeholder protocols that fail runtime validation, so we
// assert it is well-formed TOML without invoking config.Load's validation.
func TestExampleConfigDecodesAsTOML(t *testing.T) {
	const path = "../../configs/example.toml"
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		t.Fatalf("example config is not valid TOML: %v", err)
	}
	if cfg.Active == "" {
		t.Fatalf("example config decoded but active profile is empty")
	}
	if len(cfg.Profiles) == 0 {
		t.Fatalf("example config decoded but has no profiles")
	}
}
