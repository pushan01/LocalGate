package dynamic

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

type registry struct {
	Deployments map[string]Deployment `json:"deployments"`
}

type registryStore struct {
	path     string
	mu       sync.Mutex
	saveHook func(*registry) error
}

func newRegistryStore(path string) *registryStore {
	return &registryStore{path: filepath.Clean(path)}
}

func (s *registryStore) load() (*registry, error) {
	if s == nil || s.path == "" {
		return nil, errors.New("dynamic registry path is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &registry{Deployments: map[string]Deployment{}}, nil
		}
		return nil, err
	}
	var value registry
	if err := json.Unmarshal(b, &value); err != nil {
		return nil, err
	}
	if value.Deployments == nil {
		value.Deployments = map[string]Deployment{}
	}
	return &value, nil
}

func (s *registryStore) save(value *registry) error {
	if s == nil || s.path == "" || value == nil {
		return errors.New("dynamic registry is not initialized")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveHook != nil {
		if err := s.saveHook(value); err != nil {
			return err
		}
	}
	if value.Deployments == nil {
		value.Deployments = map[string]Deployment{}
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".deployments-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil && !errors.Is(err, syscall.ENOTTY) {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil && !errors.Is(err, syscall.ENOTTY) {
		return err
	}
	return nil
}
