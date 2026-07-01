package core

import (
	"testing"
	"time"
)

func TestManagerListFiltersSortsAndPaginates(t *testing.T) {
	manager := NewManager()
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	manager.LoadHistory([]Task{
		{ID: "a", Status: StatusSucceeded, Tool: "read_file", Transport: "mcp_http", MCPSessionID: "session-a", CreatedAt: base},
		{ID: "b", Status: StatusFailed, Tool: "run_command", Transport: "mcp_http", MCPSessionID: "session-b", CreatedAt: base.Add(time.Minute)},
		{ID: "c", Status: StatusSucceeded, Tool: "write_file", Transport: "rest", MCPSessionID: "session-a", CreatedAt: base.Add(2 * time.Minute)},
	})

	filtered, err := manager.List(TaskFilter{SessionID: "session-a", Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(filtered.Items) != 2 || filtered.Items[0].ID != "c" || filtered.Items[1].ID != "a" {
		t.Fatalf("unexpected filtered order: %#v", filtered.Items)
	}

	first, err := manager.List(TaskFilter{Limit: 1})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first.Items) != 1 || first.Items[0].ID != "c" || first.NextCursor == "" {
		t.Fatalf("unexpected first page: %#v", first)
	}
	second, err := manager.List(TaskFilter{Limit: 1, Cursor: first.NextCursor})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].ID != "b" {
		t.Fatalf("unexpected second page: %#v", second)
	}
}

func TestManagerListRejectsInvalidCursor(t *testing.T) {
	manager := NewManager()
	if _, err := manager.List(TaskFilter{Cursor: "not-a-cursor"}); err != ErrInvalidTaskCursor {
		t.Fatalf("expected invalid cursor, got %v", err)
	}
}

func TestRecoverInterruptedTasks(t *testing.T) {
	manager := NewManager()
	manager.LoadHistory([]Task{
		{ID: "running", Status: StatusRunning, CreatedAt: time.Now().UTC()},
		{ID: "waiting", Status: StatusAwaitingApproval, CreatedAt: time.Now().UTC()},
	})
	if err := manager.RecoverInterruptedTasks(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	running, _ := manager.Get("running")
	if running.Status != StatusCanceled || running.ErrorCode != "service_restart" {
		t.Fatalf("unexpected recovered task: %#v", running)
	}
	waiting, _ := manager.Get("waiting")
	if waiting.Status != StatusAwaitingApproval {
		t.Fatalf("approval task should be reconciled separately: %#v", waiting)
	}
}
