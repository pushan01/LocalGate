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
