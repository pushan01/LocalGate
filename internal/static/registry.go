package static

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const defaultRegistryPath = ".localgate/static/sites.json"

type Site struct {
	Path      string            `json:"path"`
	Access    string            `json:"access,omitempty"`
	SPA       bool              `json:"spa"`
	Index     string            `json:"index"`
	Headers   map[string]string `json:"headers"`
	Rewrite   []string          `json:"rewrite"`
	CreatedAt string            `json:"created_at"`
	UpdatedAt string            `json:"updated_at"`
}

type Registry struct {
	Sites map[string]Site `json:"sites"`
}

type RegistryStore struct {
	path string
	mu   sync.RWMutex
}

func NewRegistryStore(path string) *RegistryStore {
	return &RegistryStore{path: filepath.Clean(path)}
}

func (s *RegistryStore) Load() (*Registry, error) {
	if s == nil || s.path == "" {
		return nil, errors.New("static registry path is empty")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Registry{Sites: map[string]Site{}}, nil
		}
		return nil, err
	}

	var registry Registry
	if err := json.Unmarshal(b, &registry); err != nil {
		return nil, err
	}
	if registry.Sites == nil {
		registry.Sites = map[string]Site{}
	}
	return &registry, nil
}

func (s *RegistryStore) Save(registry *Registry) error {
	if s == nil || s.path == "" {
		return errors.New("static registry path is empty")
	}
	if registry == nil {
		return errors.New("static registry is nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if registry.Sites == nil {
		registry.Sites = map[string]Site{}
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".sites-*.json")
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
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.path)
}

var defaultRegistryStore = NewRegistryStore(defaultRegistryPath)

func Load() (*Registry, error) {
	return defaultRegistryStore.Load()
}

func Save(registry *Registry) error {
	return defaultRegistryStore.Save(registry)
}

func Now() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
