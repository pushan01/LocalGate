package subagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
)

type Store struct {
	Base string
	mu   sync.Mutex
}

func NewStore(base string) *Store {
	return &Store{Base: base}
}

func (s *Store) Save(agent Subagent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(agent.ID) {
		return fmt.Errorf("invalid subagent id %q", agent.ID)
	}
	if err := os.MkdirAll(s.Base, 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(agent, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Base, ".subagent-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
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
	return os.Rename(tmpPath, filepath.Join(s.Base, agent.ID+".json"))
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(id) {
		return fmt.Errorf("invalid subagent id %q", id)
	}
	err := os.Remove(filepath.Join(s.Base, id+".json"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Store) Load() ([]Subagent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.Base)
	if err != nil {
		if os.IsNotExist(err) {
			return []Subagent{}, nil
		}
		return nil, err
	}
	out := make([]Subagent, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !validID(id) {
			return nil, fmt.Errorf("invalid subagent filename %q", entry.Name())
		}
		payload, err := os.ReadFile(filepath.Join(s.Base, entry.Name()))
		if err != nil {
			return nil, err
		}
		var agent Subagent
		if err := json.Unmarshal(payload, &agent); err != nil {
			return nil, fmt.Errorf("decode %s: %w", entry.Name(), err)
		}
		if agent.ID != id {
			return nil, fmt.Errorf("subagent id mismatch in %s", entry.Name())
		}
		if _, exists := seen[agent.ID]; exists {
			return nil, fmt.Errorf("duplicate subagent id %q", agent.ID)
		}
		seen[agent.ID] = struct{}{}
		out = append(out, cloneSubagent(agent))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func validID(id string) bool {
	if !strings.HasPrefix(id, "sg_") || len(id) <= len("sg_") {
		return false
	}
	for _, r := range id[len("sg_"):] {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}
