package core

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGatedTaskDoesNotExecuteBeforeRelease(t *testing.T) {
	m := NewManager()
	started := make(chan struct{})
	task, release := m.CreateGatedTask(Task{}, func(context.Context, string) (int, error) {
		close(started)
		return 0, nil
	})

	if task.Status != StatusAwaitingApproval {
		t.Fatalf("expected awaiting approval, got %s", task.Status)
	}
	select {
	case <-started:
		t.Fatal("gated task executed before release")
	case <-time.After(50 * time.Millisecond):
	}

	release()
	final := waitForTaskTerminal(t, m, task.ID, time.Second)
	if final.Status != StatusSucceeded {
		t.Fatalf("expected succeeded after release, got %#v", final)
	}
	select {
	case <-started:
	default:
		t.Fatal("released task did not execute")
	}
}

func TestGatedTaskConcurrentReleaseExecutesOnce(t *testing.T) {
	m := NewManager()
	var executions atomic.Int32
	task, release := m.CreateGatedTask(Task{}, func(context.Context, string) (int, error) {
		executions.Add(1)
		return 0, nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release()
		}()
	}
	wg.Wait()

	final := waitForTaskTerminal(t, m, task.ID, time.Second)
	if final.Status != StatusSucceeded {
		t.Fatalf("unexpected final status: %#v", final)
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("expected exactly one execution, got %d", got)
	}
}

func TestGatedTaskCancelBeforeReleaseNeverExecutes(t *testing.T) {
	m := NewManager()
	var executions atomic.Int32
	task, release := m.CreateGatedTask(Task{}, func(context.Context, string) (int, error) {
		executions.Add(1)
		return 0, nil
	})

	if err := m.Cancel(task.ID); err != nil {
		t.Fatalf("cancel gated task: %v", err)
	}
	// A late or duplicate approval release must not revive a canceled task.
	release()
	release()

	final := waitForTaskTerminal(t, m, task.ID, time.Second)
	if final.Status != StatusCanceled {
		t.Fatalf("expected canceled, got %#v", final)
	}
	if final.ErrorCode != "approval_canceled" {
		t.Fatalf("unexpected cancellation reason: %#v", final)
	}
	if got := executions.Load(); got != 0 {
		t.Fatalf("canceled gated task executed %d times", got)
	}
}

func TestGatedTaskConcurrentReleaseAndCancelIsTerminalAndAtMostOnce(t *testing.T) {
	for i := 0; i < 50; i++ {
		m := NewManager()
		var executions atomic.Int32
		task, release := m.CreateGatedTask(Task{}, func(ctx context.Context, _ string) (int, error) {
			executions.Add(1)
			select {
			case <-ctx.Done():
				return -1, ctx.Err()
			case <-time.After(time.Millisecond):
				return 0, nil
			}
		})

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			release()
		}()
		go func() {
			defer wg.Done()
			<-start
			_ = m.Cancel(task.ID)
		}()
		close(start)
		wg.Wait()

		final := waitForTaskTerminal(t, m, task.ID, time.Second)
		if final.Status != StatusSucceeded && final.Status != StatusCanceled {
			t.Fatalf("iteration %d: unexpected final status %#v", i, final)
		}
		if got := executions.Load(); got > 1 {
			t.Fatalf("iteration %d: task executed %d times", i, got)
		}
	}
}

func waitForTaskTerminal(t *testing.T, manager *Manager, taskID string, timeout time.Duration) *Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := manager.Get(taskID)
		if err != nil {
			t.Fatalf("get task %s: %v", taskID, err)
		}
		switch task.Status {
		case StatusSucceeded, StatusFailed, StatusCanceled, StatusTimeout:
			return task
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("task %s did not reach terminal state", taskID)
	return nil
}
