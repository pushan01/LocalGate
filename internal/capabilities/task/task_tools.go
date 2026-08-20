package task

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"localgate/internal/core"
)

const defaultWaitTimeout = 60 * time.Second

type toolBase struct {
	tasks *core.Manager
	sink  *core.EventSink
}

func NewTools(tasks *core.Manager, sink *core.EventSink) []core.Tool {
	base := toolBase{tasks: tasks, sink: sink}
	return []core.Tool{
		statusTool{toolBase: base},
		logsTool{toolBase: base},
		cancelTool{toolBase: base},
		resultTool{toolBase: base},
		waitTool{toolBase: base},
	}
}

type statusTool struct{ toolBase }

func (statusTool) Name() string { return "task_status" }
func (statusTool) Description() string {
	return "Get the current status and metadata of a submitted task by task_id."
}
func (statusTool) Schema() any { return taskIDSchema() }
func (t statusTool) Execute(_ context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	task, err := t.task(taskIDArg(args))
	if err != nil {
		return nil, err
	}
	return taskAsMap(task)
}

type logsTool struct{ toolBase }

func (logsTool) Name() string { return "task_logs" }
func (logsTool) Description() string {
	return "Read task events and streamed output from a cursor position."
}
func (logsTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"task_id": map[string]any{"type": "string", "description": "Task id"},
			"cursor":  map[string]any{"type": "integer", "minimum": 0, "description": "Cursor offset (default 0)"},
		},
		"required": []string{"task_id"},
	}
}
func (t logsTool) Execute(_ context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	id := taskIDArg(args)
	if _, err := t.task(id); err != nil {
		return nil, err
	}
	cursor := intArg(args["cursor"])
	events, next := t.sink.List(id, cursor)
	return map[string]any{"cursor": next, "events": events}, nil
}

type cancelTool struct{ toolBase }

func (cancelTool) Name() string { return "task_cancel" }
func (cancelTool) Description() string {
	return "Request cancellation of a running task by task_id."
}
func (cancelTool) Schema() any { return taskIDSchema() }
func (t cancelTool) Execute(_ context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	id := taskIDArg(args)
	if _, err := t.task(id); err != nil {
		return nil, err
	}
	if err := t.tasks.Cancel(id); err != nil {
		return nil, errors.New("task not found")
	}
	return map[string]any{"ok": true}, nil
}

type resultTool struct{ toolBase }

func (resultTool) Name() string { return "task_result" }
func (resultTool) Description() string {
	return "Get the final structured result and captured output of a task by task_id."
}
func (resultTool) Schema() any { return taskIDSchema() }
func (t resultTool) Execute(_ context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	task, err := t.task(taskIDArg(args))
	if err != nil {
		return nil, err
	}
	payload := taskResultPayload(task)
	payload["ready"] = isTerminal(task.Status)
	appendStreams(payload, t.sink, task.ID)
	return payload, nil
}

type waitTool struct{ toolBase }

func (waitTool) Name() string { return "wait_task_finish" }
func (waitTool) Description() string {
	return "Block until a task reaches a terminal state without returning the task result."
}
func (waitTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"task_id":    map[string]any{"type": "string", "description": "Task id"},
			"timeout_ms": map[string]any{"type": "integer", "minimum": 1, "description": "Max wait time in milliseconds (default 60000)"},
		},
		"required": []string{"task_id"},
	}
}
func (t waitTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	id := taskIDArg(args)
	if _, err := t.task(id); err != nil {
		return nil, err
	}
	timeout := defaultWaitTimeout
	if ms := intArg(args["timeout_ms"]); ms > 0 {
		timeout = time.Duration(ms) * time.Millisecond
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		task, err := t.task(id)
		if err != nil {
			return nil, err
		}
		if isTerminal(task.Status) {
			return waitPayload(task), nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return waitPayload(task), nil
		case <-ticker.C:
		}
	}
}

func (t toolBase) task(id string) (*core.Task, error) {
	if t.tasks == nil || strings.TrimSpace(id) == "" {
		return nil, errors.New("task not found")
	}
	task, err := t.tasks.Get(id)
	if err != nil {
		return nil, errors.New("task not found")
	}
	return task, nil
}

func taskIDSchema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"task_id": map[string]any{"type": "string", "description": "Task id"},
		},
		"required": []string{"task_id"},
	}
}

func taskIDArg(args map[string]any) string {
	id, _ := args["task_id"].(string)
	return strings.TrimSpace(id)
}

func intArg(value any) int {
	switch value := value.(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		if parsed, err := value.Int64(); err == nil {
			return int(parsed)
		}
	}
	return 0
}

func taskAsMap(task *core.Task) (map[string]any, error) {
	encoded, err := json.Marshal(task)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func taskResultPayload(task *core.Task) map[string]any {
	payload := map[string]any{"task_id": task.ID, "status": task.Status}
	if task.Result != nil {
		payload["result"] = task.Result
	}
	if task.ExitCode != 0 {
		payload["exit_code"] = task.ExitCode
	}
	if task.ErrorCode != "" {
		payload["error_code"] = task.ErrorCode
	}
	if task.ErrorMsg != "" {
		payload["error_msg"] = task.ErrorMsg
	}
	return payload
}

func waitPayload(task *core.Task) map[string]any {
	payload := map[string]any{
		"task_id": task.ID,
		"status":  task.Status,
		"ready":   isTerminal(task.Status),
	}
	if task.ExitCode != 0 {
		payload["exit_code"] = task.ExitCode
	}
	if task.ErrorCode != "" {
		payload["error_code"] = task.ErrorCode
	}
	if task.ErrorMsg != "" {
		payload["error_msg"] = task.ErrorMsg
	}
	return payload
}

func isTerminal(status core.Status) bool {
	return status == core.StatusSucceeded || status == core.StatusFailed || status == core.StatusCanceled || status == core.StatusTimeout
}

func appendStreams(payload map[string]any, sink *core.EventSink, taskID string) {
	if sink == nil {
		return
	}
	events, _ := sink.List(taskID, 0)
	var stdout strings.Builder
	var stderr strings.Builder
	for _, event := range events {
		switch event.Type {
		case core.EventTaskStdout:
			stdout.WriteString(event.Data)
			if !event.Continues {
				stdout.WriteByte('\n')
			}
		case core.EventTaskStderr:
			stderr.WriteString(event.Data)
			if !event.Continues {
				stderr.WriteByte('\n')
			}
		}
	}
	if text := strings.TrimSuffix(stdout.String(), "\n"); text != "" {
		payload["stdout"] = text
	}
	if text := strings.TrimSuffix(stderr.String(), "\n"); text != "" {
		payload["stderr"] = text
	}
}
