package persist

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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

