package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"localgate/internal/app"
	"localgate/internal/config"
	"localgate/internal/core"
)

func main() {
	tmp, err := os.MkdirTemp("", "localgate-demo-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)

	proj := filepath.Join(tmp, "buggy")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		panic(err)
	}

	write(filepath.Join(proj, "go.mod"), []byte("module buggy\n\ngo 1.22\n"))
	write(filepath.Join(proj, "calc.go"), []byte(`package buggy

func Add(a, b int) int {
	return a - b
}
`))
	write(filepath.Join(proj, "calc_test.go"), []byte(`package buggy

import "testing"

func TestAdd(t *testing.T) {
	if Add(2, 1) != 3 {
		t.Fatalf("expected 3")
	}
}
`))

	cfg := config.Config{
		Root: config.Root{
			Path:             tmp,
			CommandAllowlist: []string{"go", "diff", "sh", "cat"},
		},
	}
	application, err := app.New(cfg)
	if err != nil {
		panic(err)
	}
	reg := application.Registry

	fmt.Printf("root=%s\n", tmp)

	before, _ := runTool(application, reg, "run_command", map[string]any{"command": "go test ./...", "cwd": proj, "timeout_ms": 10_000})
	fmt.Printf("initial_test=%s task_id=%s\n", before.Status, before.ID)

	srcPath := filepath.Join(proj, "calc.go")
	readRes, _ := runTool(application, reg, "read_file", map[string]any{"path": srcPath})
	orig, _ := readRes.Result["content"].(string)
	fixed := replaceOnce(orig, "return a - b", "return a + b")
	_, _ = runTool(application, reg, "write_file", map[string]any{"path": srcPath, "content": fixed})

	after, _ := runTool(application, reg, "run_command", map[string]any{"command": "go test ./...", "cwd": proj, "timeout_ms": 10_000})
	fmt.Printf("final_test=%s task_id=%s\n", after.Status, after.ID)

	oldFile := filepath.Join(tmp, "before.go")
	newFile := filepath.Join(tmp, "after.go")
	_, _ = runTool(application, reg, "write_file", map[string]any{"path": oldFile, "content": orig})
	_, _ = runTool(application, reg, "write_file", map[string]any{"path": newFile, "content": fixed})

	diffTask, _ := runTool(application, reg, "run_command", map[string]any{"command": "diff -u " + oldFile + " " + newFile + " || true", "cwd": tmp, "timeout_ms": 10_000})
	fmt.Printf("diff task_id=%s:\n", diffTask.ID)
	fmt.Print(collectStdout(application.Sink, diffTask.ID))
}

type taskRun struct {
	ID     string
	Status core.Status
	Result map[string]any
}

func runTool(a *app.App, reg *core.Registry, tool string, args map[string]any) (taskRun, error) {
	resCh := make(chan map[string]any, 1)
	errCh := make(chan error, 1)

	task := a.Tasks.CreateTask(core.Task{Requester: "demo"}, func(ctx context.Context, taskID string) (int, error) {
		res, err := reg.Invoke(ctx, tool, core.InvokeContext{TaskID: taskID, Sink: a.Sink}, args)
		resCh <- res
		errCh <- err
		return intFromAny(res["exit_code"]), err
	})

	res := <-resCh
	err := <-errCh

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		t, e := a.Tasks.Get(task.ID)
		if e == nil && (t.Status == core.StatusSucceeded || t.Status == core.StatusFailed || t.Status == core.StatusCanceled || t.Status == core.StatusTimeout) {
			return taskRun{ID: task.ID, Status: t.Status, Result: res}, err
		}
		time.Sleep(10 * time.Millisecond)
	}
	t, _ := a.Tasks.Get(task.ID)
	return taskRun{ID: task.ID, Status: t.Status, Result: res}, err
}

func collectStdout(sink *core.EventSink, taskID string) string {
	evs, _ := sink.List(taskID, 0)
	out := ""
	for _, ev := range evs {
		if ev.Type == core.EventTaskStdout {
			out += ev.Data + "\n"
		}
	}
	return out
}

func replaceOnce(s, old, new string) string {
	i := indexOf(s, old)
	if i < 0 {
		return s
	}
	return s[:i] + new + s[i+len(old):]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func write(p string, b []byte) {
	if err := os.WriteFile(p, b, 0o644); err != nil {
		panic(err)
	}
}

func intFromAny(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	default:
		return 0
	}
}
