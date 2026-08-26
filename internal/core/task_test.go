package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCreateAndFinishTask(t *testing.T) {
	m := NewManager()
	tk := m.CreateEmptyTask(50 * time.Millisecond)
	if tk.Status != StatusQueued {
		t.Fatalf("expected queued, got %s", tk.Status)
	}
	time.Sleep(20 * time.Millisecond)
	got, err := m.Get(tk.ID)
	if err != nil {
		t.Fatalf("get error: %v", err)
	}
	if got.Status != StatusRunning && got.Status != StatusQueued {
		t.Fatalf("expected running or queued, got %s", got.Status)
	}
	time.Sleep(80 * time.Millisecond)
	got, err = m.Get(tk.ID)
	if err != nil {
		t.Fatalf("get error: %v", err)
	}
	if got.Status != StatusSucceeded {
		t.Fatalf("expected succeeded, got %s", got.Status)
	}
}

func TestIsolatedTasksDoNotStarveDefaultTasks(t *testing.T) {
	m := NewManagerWithOptions(nil, 1)
	release := make(chan struct{})
	isolatedStarted := make(chan struct{})
	m.CreateTask(Task{Tool: "subagent_wait", ConcurrencyClass: TaskConcurrencyIsolated}, func(ctx context.Context, _ string) (int, error) {
		close(isolatedStarted)
		select {
		case <-release:
			return 0, nil
		case <-ctx.Done():
			return -1, ctx.Err()
		}
	})
	select {
	case <-isolatedStarted:
	case <-time.After(time.Second):
		t.Fatal("isolated task did not start")
	}

	defaultStarted := make(chan struct{})
	m.CreateTask(Task{Tool: "read_file"}, func(context.Context, string) (int, error) {
		close(defaultStarted)
		return 0, nil
	})
	select {
	case <-defaultStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("default task was starved by isolated task")
	}
	close(release)
}

func TestCancelTask(t *testing.T) {
	m := NewManager()
	tk := m.CreateEmptyTask(200 * time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	if err := m.Cancel(tk.ID); err != nil {
		t.Fatalf("cancel error: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	got, err := m.Get(tk.ID)
	if err != nil {
		t.Fatalf("get error: %v", err)
	}
	if got.Status != StatusCanceled && got.Status != StatusRunning {
		t.Fatalf("expected canceled or running (race), got %s", got.Status)
	}
	time.Sleep(50 * time.Millisecond)
	got, _ = m.Get(tk.ID)
	if got.Status != StatusCanceled {
		t.Fatalf("expected canceled settled, got %s", got.Status)
	}
}

func TestRemoveTerminalTasksKeepsActiveTasks(t *testing.T) {
	m := NewManager()
	m.LoadHistory([]Task{
		{ID: "finished", Status: StatusSucceeded},
		{ID: "running", Status: StatusRunning},
	})
	m.RemoveTerminalTasks([]string{"finished", "running"})
	if _, err := m.Get("finished"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected finished task to be removed, got %v", err)
	}
	if _, err := m.Get("running"); err != nil {
		t.Fatalf("expected running task to remain: %v", err)
	}
}
