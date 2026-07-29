package core

import (
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
