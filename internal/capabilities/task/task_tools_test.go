package task

import (
	"context"
	"testing"
	"time"

	"localgate/internal/core"
)

func TestTaskResultReturnsStructuredResultAndStreams(t *testing.T) {
	sink := core.NewEventSink()
	manager := core.NewManagerWithSink(sink)
	manager.LoadHistory([]core.Task{{
		ID:     "task-1",
		Status: core.StatusSucceeded,
		Result: map[string]any{"value": "done"},
	}})
	sink.Append(core.Event{TaskID: "task-1", Type: core.EventTaskStdout, Time: time.Now().UTC(), Data: "hello"})

	tool := findTool(t, NewTools(manager, sink), "task_result")
	result, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"task_id": "task-1"})
	if err != nil {
		t.Fatalf("task_result: %v", err)
	}
	if result["ready"] != true || result["status"] != core.StatusSucceeded {
		t.Fatalf("unexpected task result status: %#v", result)
	}
	nested, _ := result["result"].(map[string]any)
	if nested["value"] != "done" || result["stdout"] != "hello" {
		t.Fatalf("unexpected task result payload: %#v", result)
	}
}

func TestTaskToolsHideMissingTask(t *testing.T) {
	manager := core.NewManager()
	tool := findTool(t, NewTools(manager, core.NewEventSink()), "task_status")

	_, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"task_id": "missing"})
	if err == nil {
		t.Fatalf("missing task lookup should fail")
	}
}

func TestWaitTaskFinishReturnsWhenTargetCompletes(t *testing.T) {
	manager := core.NewManager()
	defer manager.Close()
	target := manager.CreateTask(core.Task{}, func(ctx context.Context, taskID string) (int, error) {
		time.Sleep(20 * time.Millisecond)
		return 0, nil
	})
	tool := findTool(t, NewTools(manager, core.NewEventSink()), "wait_task_finish")

	result, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"task_id": target.ID, "timeout_ms": 1000,
	})
	if err != nil {
		t.Fatalf("wait_task_finish: %v", err)
	}
	if result["ready"] != true || result["status"] != core.StatusSucceeded {
		t.Fatalf("unexpected wait result: %#v", result)
	}
}

func findTool(t *testing.T, tools []core.Tool, name string) core.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}
