package upgrade

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"localgate/internal/config"
)

type Status string

const (
	StatusUploaded        Status = "uploaded"
	StatusValidated       Status = "validated"
	StatusHandoffStarted  Status = "handoff_started"
	StatusStoppingOld     Status = "stopping_old"
	StatusReplacingBinary Status = "replacing_binary"
	StatusStartingNew     Status = "starting_new"
	StatusHealthChecking  Status = "health_checking"
	StatusSucceeded       Status = "succeeded"
	StatusRollbackStarted Status = "rollback_started"
	StatusRolledBack      Status = "rolled_back"
	StatusFailed          Status = "failed"
)

type State struct {
	ID               string         `json:"id"`
	Status           Status         `json:"status"`
	Message          string         `json:"message,omitempty"`
	TaskID           string         `json:"task_id,omitempty"`
	OriginalFilename string         `json:"original_filename,omitempty"`
	TargetBinary     string         `json:"target_binary"`
	CandidateBinary  string         `json:"candidate_binary"`
	BackupBinary     string         `json:"backup_binary"`
	RunnerLogFile    string         `json:"runner_log_file"`
	VersionBefore    string         `json:"version_before,omitempty"`
	VersionAfter     string         `json:"version_after,omitempty"`
	OldPID           int            `json:"old_pid,omitempty"`
	NewPID           int            `json:"new_pid,omitempty"`
	Runtime          config.Runtime `json:"runtime"`
	StartArgs        []string       `json:"start_args,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

func (s State) Terminal() bool {
	switch s.Status {
	case StatusSucceeded, StatusRolledBack, StatusFailed:
		return true
	default:
		return false
	}
}

type Store struct {
	Base string
	mu   sync.Mutex
	now  func() time.Time
}

func NewStore(base string) *Store {
	return &Store{
		Base: base,
		now:  func() time.Time { return time.Now().UTC() },
	}
}

func (s *Store) Ensure() error {
	return os.MkdirAll(s.Base, 0o755)
}

func (s *Store) UpgradeDir(id string) string {
	return filepath.Join(s.Base, id)
}

func (s *Store) StatePath(id string) string {
	return filepath.Join(s.UpgradeDir(id), "state.json")
}

func (s *Store) CandidatePath(id string) string {
	return filepath.Join(s.UpgradeDir(id), "candidate", "localgate")
}

func (s *Store) BackupPath(id string) string {
	return filepath.Join(s.UpgradeDir(id), "backup", "localgate.old")
}

func (s *Store) RunnerLogPath(id string) string {
	return filepath.Join(s.UpgradeDir(id), "runner.log")
}

func (s *Store) ActiveLockPath() string {
	return filepath.Join(s.Base, "active.lock")
}

func (s *Store) InitState(st State) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st.ID == "" {
		st.ID = newID()
	}
	now := s.now()
	st.CreatedAt = now
	st.UpdatedAt = now
	if st.Status == "" {
		st.Status = StatusUploaded
	}
	if err := s.ensureUpgradeDirs(st.ID); err != nil {
		return State{}, err
	}
	if err := s.saveUnlocked(st); err != nil {
		return State{}, err
	}
	return st, nil
}

func (s *Store) Save(st State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st.UpdatedAt = s.now()
	if err := s.ensureUpgradeDirs(st.ID); err != nil {
		return err
	}
	return s.saveUnlocked(st)
}

func (s *Store) Update(id string, fn func(*State) error) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadUnlocked(id)
	if err != nil {
		return State{}, err
	}
	if err := fn(&st); err != nil {
		return State{}, err
	}
	st.UpdatedAt = s.now()
	if err := s.saveUnlocked(st); err != nil {
		return State{}, err
	}
	return st, nil
}

func (s *Store) Get(id string) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadUnlocked(id)
}

func (s *Store) GetByPath(path string) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadPathUnlocked(path)
}

func (s *Store) AcquireActive(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Ensure(); err != nil {
		return err
	}
	lockPath := s.ActiveLockPath()
	if data, err := os.ReadFile(lockPath); err == nil {
		currentID := strings.TrimSpace(string(data))
		if currentID != "" {
			st, loadErr := s.loadUnlocked(currentID)
			if loadErr == nil && !st.Terminal() {
				return ErrActiveUpgrade
			}
		}
		_ = os.Remove(lockPath)
	}
	return os.WriteFile(lockPath, []byte(id), 0o644)
}

func (s *Store) ReleaseActive(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	lockPath := s.ActiveLockPath()
	data, err := os.ReadFile(lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(string(data)) != id {
		return nil
	}
	return os.Remove(lockPath)
}

func (s *Store) ensureUpgradeDirs(id string) error {
	for _, path := range []string{
		s.UpgradeDir(id),
		filepath.Dir(s.CandidatePath(id)),
		filepath.Dir(s.BackupPath(id)),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) saveUnlocked(st State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.StatePath(st.ID), b, 0o644)
}

func (s *Store) loadUnlocked(id string) (State, error) {
	return s.loadPathUnlocked(s.StatePath(id))
}

func (s *Store) loadPathUnlocked(path string) (State, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return State{}, err
	}
	if st.ID == "" {
		return State{}, errors.New("upgrade state id empty")
	}
	return st, nil
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
