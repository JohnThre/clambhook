package license

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrChallengeConsumed = errors.New("challenge already consumed")
	ErrChallengeExpired  = errors.New("challenge expired")
)

type Store interface {
	SaveChallenge(ChallengeRecord) error
	ConsumeChallenge(id, purpose, installHash, keyHash string, now time.Time) (ChallengeRecord, error)
	GetDeviceByKeyHash(keyHash string) (DeviceRecord, error)
	SaveDevice(DeviceRecord) error
	CountRecentAttestations(installHash string, since time.Time) (int, error)
}

type MemoryStore struct {
	mu         sync.Mutex
	challenges map[string]ChallengeRecord
	devices    map[string]DeviceRecord
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		challenges: make(map[string]ChallengeRecord),
		devices:    make(map[string]DeviceRecord),
	}
}

func (s *MemoryStore) SaveChallenge(ch ChallengeRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.challenges[ch.ID] = ch
	return nil
}

func (s *MemoryStore) ConsumeChallenge(id, purpose, installHash, keyHash string, now time.Time) (ChallengeRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.challenges[id]
	if !ok {
		return ChallengeRecord{}, ErrNotFound
	}
	if ch.Purpose != purpose || ch.InstallHash != installHash || ch.KeyHash != keyHash {
		return ChallengeRecord{}, ErrNotFound
	}
	if ch.ConsumedAt != nil {
		return ChallengeRecord{}, ErrChallengeConsumed
	}
	if now.After(ch.ExpiresAt) {
		return ChallengeRecord{}, ErrChallengeExpired
	}
	ch.ConsumedAt = &now
	s.challenges[id] = ch
	return ch, nil
}

func (s *MemoryStore) GetDeviceByKeyHash(keyHash string) (DeviceRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dev, ok := s.devices[keyHash]
	if !ok {
		return DeviceRecord{}, ErrNotFound
	}
	return dev, nil
}

func (s *MemoryStore) SaveDevice(dev DeviceRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.devices[dev.KeyHash] = dev
	return nil
}

func (s *MemoryStore) CountRecentAttestations(installHash string, since time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, dev := range s.devices {
		if dev.InstallHash == installHash && !dev.CreatedAt.Before(since) {
			count++
		}
	}
	return count, nil
}

type FileStore struct {
	*MemoryStore
	path string
}

type fileState struct {
	Challenges map[string]ChallengeRecord `json:"challenges"`
	Devices    map[string]DeviceRecord    `json:"devices"`
}

func NewFileStore(path string) (*FileStore, error) {
	store := &FileStore{MemoryStore: NewMemoryStore(), path: path}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *FileStore) SaveChallenge(ch ChallengeRecord) error {
	if err := s.MemoryStore.SaveChallenge(ch); err != nil {
		return err
	}
	return s.flush()
}

func (s *FileStore) ConsumeChallenge(id, purpose, installHash, keyHash string, now time.Time) (ChallengeRecord, error) {
	ch, err := s.MemoryStore.ConsumeChallenge(id, purpose, installHash, keyHash, now)
	if err != nil {
		return ChallengeRecord{}, err
	}
	return ch, s.flush()
}

func (s *FileStore) SaveDevice(dev DeviceRecord) error {
	if err := s.MemoryStore.SaveDevice(dev); err != nil {
		return err
	}
	return s.flush()
}

func (s *FileStore) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read license store: %w", err)
	}
	var state fileState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("parse license store: %w", err)
	}
	if state.Challenges != nil {
		s.challenges = state.Challenges
	}
	if state.Devices != nil {
		s.devices = state.Devices
	}
	return nil
}

func (s *FileStore) flush() error {
	s.mu.Lock()
	state := fileState{
		Challenges: make(map[string]ChallengeRecord, len(s.challenges)),
		Devices:    make(map[string]DeviceRecord, len(s.devices)),
	}
	for k, v := range s.challenges {
		state.Challenges[k] = v
	}
	for k, v := range s.devices {
		state.Devices[k] = v
	}
	s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
