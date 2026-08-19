package scripting

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// maxScriptSourceBytes caps the size of a file-backed module script to
// prevent a huge file from being read into memory.
const maxScriptSourceBytes = 1 << 20 // 1 MiB

// persistentStore provides a simple key/value store scoped to the scripting
// engine. Values are stored as JSON in a per-module directory under the data
// directory.
type persistentStore struct {
	mu   sync.RWMutex
	root string
	data map[string]map[string]json.RawMessage
}

func newPersistentStore(root string) (*persistentStore, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create store dir: %w", err)
	}
	return &persistentStore{root: root, data: make(map[string]map[string]json.RawMessage)}, nil
}

func (s *persistentStore) Read(module, key string) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	mod, ok := s.data[module]
	if !ok {
		v, ok, err := s.readDisk(module, key)
		return v, ok, err
	}
	raw, ok := mod[key]
	if !ok {
		return s.readDisk(module, key)
	}
	return string(raw), true, nil
}

func (s *persistentStore) Write(module, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	mod, ok := s.data[module]
	if !ok {
		mod = make(map[string]json.RawMessage)
		s.data[module] = mod
	}
	mod[key] = json.RawMessage(value)
	return s.writeDisk(module, key, value)
}

func (s *persistentStore) Remove(module, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mod, ok := s.data[module]; ok {
		delete(mod, key)
	}
	return os.Remove(s.keyPath(module, key))
}

func (s *persistentStore) readDisk(module, key string) (string, bool, error) {
	path := s.keyPath(module, key)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return string(data), true, nil
}

func (s *persistentStore) writeDisk(module, key, value string) error {
	path := s.keyPath(module, key)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(value), 0o600)
}

func (s *persistentStore) keyPath(module, key string) string {
	return filepath.Join(s.root, safeName(module), safeName(key)+".json")
}

func safeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "_"
	}
	// Flatten to a filesystem-safe string.
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// readFileLimited reads at most limit bytes from path. It returns an error if
// the file is larger than limit.
func readFileLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return data, nil
}
