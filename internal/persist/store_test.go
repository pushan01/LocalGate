package persist

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"localgate/internal/core"
)

func TestStoreTaskAndEvents(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	task := core.Task{
		ID:        "t1",
		Status:    core.StatusSucceeded,
		CreatedAt: time.Now().UTC(),
		EndedAt:   time.Now().UTC(),
		Result: map[string]any{
			"entries": []string{"a.txt"},
		},
	}
	if err := s.SaveTask(task); err != nil {
		t.Fatalf("save task: %v", err)
	}
	tasks, err := s.LoadTasks()
	if err != nil {
		t.Fatalf("load tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "t1" {
		t.Fatalf("unexpected tasks: %+v", tasks)
	}
	if got, ok := tasks[0].Result["entries"].([]any); !ok || len(got) != 1 || got[0] != "a.txt" {
		t.Fatalf("unexpected task result: %#v", tasks[0].Result)
	}
	ev := core.Event{TaskID: "t1", Type: core.EventTaskCreated, Time: time.Now().UTC()}
	if err := s.AppendEvent(ev); err != nil {
		t.Fatalf("append event: %v", err)
	}
	evs, err := s.LoadEvents("t1", 0)
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(evs) != 1 || evs[0].Type != core.EventTaskCreated {
		t.Fatalf("unexpected events: %+v", evs)
	}
	if _, err := os.Stat(filepath.Join(dir, "tasks", "t1.json")); err != nil {
		t.Fatalf("task file missing: %v", err)
	}
}

func TestPruneTasksBefore(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	now := time.Now().UTC()
	cutoff := now.Add(-48 * time.Hour)
	tasks := []core.Task{
		{ID: "expired", Status: core.StatusSucceeded, CreatedAt: now.Add(-96 * time.Hour), EndedAt: now.Add(-72 * time.Hour)},
		{ID: "recent", Status: core.StatusFailed, CreatedAt: now.Add(-36 * time.Hour), EndedAt: now.Add(-24 * time.Hour)},
		{ID: "running", Status: core.StatusRunning, CreatedAt: now.Add(-96 * time.Hour)},
	}
	for _, task := range tasks {
		if err := store.SaveTask(task); err != nil {
			t.Fatalf("save task %s: %v", task.ID, err)
		}
		if err := store.AppendEvent(core.Event{TaskID: task.ID, Type: core.EventTaskCreated, Time: task.CreatedAt}); err != nil {
			t.Fatalf("append event %s: %v", task.ID, err)
		}
	}
	if err := store.AppendEvent(core.Event{TaskID: "orphan", Type: core.EventTaskCreated, Time: now.Add(-72 * time.Hour)}); err != nil {
		t.Fatalf("append orphan event: %v", err)
	}
	orphanPath := filepath.Join(dir, "events", "orphan.log")
	old := now.Add(-72 * time.Hour)
	if err := os.Chtimes(orphanPath, old, old); err != nil {
		t.Fatalf("age orphan event: %v", err)
	}

	removed, err := store.PruneTasksBefore(cutoff)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if len(removed) != 1 || removed[0] != "expired" {
		t.Fatalf("unexpected removed tasks: %#v", removed)
	}
	for _, path := range []string{
		filepath.Join(dir, "tasks", "expired.json"),
		filepath.Join(dir, "events", "expired.log"),
		orphanPath,
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, err=%v", path, err)
		}
	}
	for _, path := range []string{
		filepath.Join(dir, "tasks", "recent.json"),
		filepath.Join(dir, "events", "recent.log"),
		filepath.Join(dir, "tasks", "running.json"),
		filepath.Join(dir, "events", "running.log"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to remain: %v", path, err)
		}
	}
}
