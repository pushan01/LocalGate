package persist

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"localgate/internal/core"
)

type Store struct {
	Base string
}

func NewStore(base string) *Store {
	return &Store{Base: base}
}

func (s *Store) ensureDirs() error {
	for _, d := range []string{
		filepath.Join(s.Base, "tasks"),
		filepath.Join(s.Base, "events"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SaveTask(t core.Task) error {
	if t.ID == "" {
		return errors.New("task id empty")
	}
	if err := s.ensureDirs(); err != nil {
		return err
	}
	fp := filepath.Join(s.Base, "tasks", t.ID+".json")
	f, err := os.Create(fp)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(t)
}

func (s *Store) LoadTasks() ([]core.Task, error) {
	dir := filepath.Join(s.Base, "tasks")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []core.Task
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fp := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(fp)
		if err != nil {
			return nil, err
		}
		var t core.Task
		if err := json.Unmarshal(b, &t); err != nil {
			return nil, fmt.Errorf("decode %s: %w", e.Name(), err)
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *Store) AppendEvent(ev core.Event) error {
	if ev.TaskID == "" {
		return errors.New("task id empty")
	}
	if err := s.ensureDirs(); err != nil {
		return err
	}
	fp := filepath.Join(s.Base, "events", ev.TaskID+".log")
	f, err := os.OpenFile(fp, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	return enc.Encode(ev)
}

func (s *Store) LoadEvents(taskID string, limit int) ([]core.Event, error) {
	fp := filepath.Join(s.Base, "events", taskID+".log")
	f, err := os.Open(fp)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var out []core.Event
	for sc.Scan() {
		var ev core.Event
		if err := json.Unmarshal([]byte(sc.Text()), &ev); err != nil {
			return nil, err
		}
		out = append(out, ev)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, sc.Err()
}

// PruneTasksBefore removes terminal tasks older than cutoff together with
// their persisted event logs. Runnable and approval-gated tasks are retained
// regardless of age. Orphaned event logs are removed when their modification
// time is older than the same cutoff.
func (s *Store) PruneTasksBefore(cutoff time.Time) ([]string, error) {
	if cutoff.IsZero() {
		return nil, nil
	}
	tasksDir := filepath.Join(s.Base, "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	removed := make([]string, 0)
	var cleanupErrors []error
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if id == "" {
			continue
		}
		taskPath := filepath.Join(tasksDir, entry.Name())
		data, readErr := os.ReadFile(taskPath)
		if readErr != nil {
			cleanupErrors = append(cleanupErrors, readErr)
			continue
		}
		var task core.Task
		if decodeErr := json.Unmarshal(data, &task); decodeErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("decode %s: %w", entry.Name(), decodeErr))
			continue
		}
		if task.ID != id || !terminalTaskStatus(task.Status) {
			continue
		}
		retentionTime := task.EndedAt
		if retentionTime.IsZero() {
			retentionTime = task.CreatedAt
		}
		if retentionTime.IsZero() {
			info, infoErr := entry.Info()
			if infoErr != nil {
				cleanupErrors = append(cleanupErrors, infoErr)
				continue
			}
			retentionTime = info.ModTime()
		}
		if !retentionTime.Before(cutoff) {
			continue
		}
		if removeErr := os.Remove(taskPath); removeErr != nil && !os.IsNotExist(removeErr) {
			cleanupErrors = append(cleanupErrors, removeErr)
			continue
		}
		removed = append(removed, id)
		eventPath := filepath.Join(s.Base, "events", id+".log")
		if removeErr := os.Remove(eventPath); removeErr != nil && !os.IsNotExist(removeErr) {
			cleanupErrors = append(cleanupErrors, removeErr)
		}
	}

	if err := s.pruneOrphanEventsBefore(cutoff); err != nil {
		cleanupErrors = append(cleanupErrors, err)
	}
	return removed, errors.Join(cleanupErrors...)
}

func (s *Store) pruneOrphanEventsBefore(cutoff time.Time) error {
	eventsDir := filepath.Join(s.Base, "events")
	entries, err := os.ReadDir(eventsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var cleanupErrors []error
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".log" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".log")
		if id == "" {
			continue
		}
		if _, statErr := os.Stat(filepath.Join(s.Base, "tasks", id+".json")); statErr == nil {
			continue
		} else if !os.IsNotExist(statErr) {
			cleanupErrors = append(cleanupErrors, statErr)
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			cleanupErrors = append(cleanupErrors, infoErr)
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		if removeErr := os.Remove(filepath.Join(eventsDir, entry.Name())); removeErr != nil && !os.IsNotExist(removeErr) {
			cleanupErrors = append(cleanupErrors, removeErr)
		}
	}
	return errors.Join(cleanupErrors...)
}

func terminalTaskStatus(status core.Status) bool {
	switch status {
	case core.StatusSucceeded, core.StatusFailed, core.StatusCanceled, core.StatusTimeout:
		return true
	default:
		return false
	}
}
