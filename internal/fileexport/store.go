package fileexport

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Each export owns a random bearer signature. Storing the grant and its
// signature together avoids process-local keys and permits links to be
// regenerated for asynchronous MCP results after a process restart.
type storedGrant struct {
	Grant     Grant  `json:"grant"`
	Signature string `json:"signature"`
}

func (s *Service) initStore(dir string) error {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return fmt.Errorf("create file export store: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("file export store must be a private, non-symlink directory (mode 0700)")
	}
	s.storeDir = absolute
	return nil
}

func validGrantID(id string) bool {
	if len(id) != len("lgexp_")+32 || !strings.HasPrefix(id, "lgexp_") {
		return false
	}
	for _, c := range id[len("lgexp_"):] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (s *Service) grantFile(id string) (string, error) {
	if !validGrantID(id) {
		return "", ErrExportNotFound
	}
	return filepath.Join(s.storeDir, id+".json"), nil
}

func (s *Service) saveGrant(grant Grant) error {
	name, err := s.grantFile(grant.ID)
	if err != nil {
		return err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	payload, err := json.Marshal(storedGrant{
		Grant: grant, Signature: base64.RawURLEncoding.EncodeToString(secret),
	})
	if err != nil {
		return err
	}

	// Write to a private temporary file and publish the complete grant with
	// rename so another process never observes a partially written JSON file.
	f, err := os.CreateTemp(s.storeDir, ".grant-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(payload); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), name)
}

func (s *Service) loadGrant(id string) (Grant, string, error) {
	name, err := s.grantFile(id)
	if err != nil {
		return Grant{}, "", err
	}
	info, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return Grant{}, "", ErrExportNotFound
	}
	if err != nil {
		return Grant{}, "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 8192 {
		return Grant{}, "", ErrExportNotFound
	}
	payload, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		return Grant{}, "", ErrExportNotFound
	}
	if err != nil {
		return Grant{}, "", err
	}
	var record storedGrant
	if err := json.Unmarshal(payload, &record); err != nil {
		return Grant{}, "", ErrExportNotFound
	}
	secret, err := base64.RawURLEncoding.DecodeString(record.Signature)
	if record.Grant.ID != id || err != nil || len(secret) != 32 {
		return Grant{}, "", ErrExportNotFound
	}
	if !record.Grant.ExpiresAt.After(s.now()) {
		s.removeGrant(id)
		return Grant{}, "", ErrExportExpired
	}
	return record.Grant, record.Signature, nil
}

func (s *Service) removeGrant(id string) {
	if name, err := s.grantFile(id); err == nil {
		_ = os.Remove(name)
	}
}

// Opportunistically expire abandoned exports too: clients may never fetch
// their links. Throttle scans so ordinary export calls remain inexpensive.
func (s *Service) pruneStoredGrants() {
	s.mu.Lock()
	now := s.now()
	if !s.lastPrune.IsZero() && now.Sub(s.lastPrune) < time.Minute {
		s.mu.Unlock()
		return
	}
	s.lastPrune = now
	s.mu.Unlock()

	entries, err := os.ReadDir(s.storeDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".json")
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || !validGrantID(id) {
			continue
		}
		_, _, _ = s.loadGrant(id)
	}
}
