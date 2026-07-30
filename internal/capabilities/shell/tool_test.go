package shell

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"localgate/internal/config"
	"localgate/internal/core"
)

func TestRunEcho(t *testing.T) {
	ws := config.Workspace{
		ID:               "ws",
		CommandAllowlist: []string{"echo", "sh", "/bin/sh"},
	}
	tool := NewTool(ws, 0)
	sink := core.NewEventSink()
	res, err := tool.Execute(context.Background(), core.InvokeContext{TaskID: "t1", Sink: sink}, map[string]any{
		"command": "echo hello",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res["exit_code"].(int) != 0 {
		t.Fatalf("unexpected exit: %v", res)
	}
	evs, _ := sink.List("t1", 0)
	found := false
	for _, e := range evs {
		if e.Type == core.EventTaskStdout {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("stdout event not found")
	}
}

func TestRunCommandPreservesAllowlistedProcessPath(t *testing.T) {
	pathValue := filepath.Join(t.TempDir(), "first") + string(os.PathListSeparator) +
		filepath.Join(t.TempDir(), "second")
	t.Setenv("PATH", pathValue)

	ws := config.Workspace{
		ID:               "ws",
		CommandAllowlist: []string{"printf"},
		EnvAllowlist:     []string{"PATH"},
	}
	sink := core.NewEventSink()
	tool := NewTool(ws, 0)

	_, err := tool.Execute(context.Background(), core.InvokeContext{TaskID: "t-path", Sink: sink}, map[string]any{
		"command": `printf '%s\n' "$PATH"`,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	events, _ := sink.List("t-path", 0)
	var stdout []string
	for _, event := range events {
		if event.Type == core.EventTaskStdout {
			stdout = append(stdout, event.Data)
		}
	}
	if got := strings.Join(stdout, "\n"); got != pathValue {
		t.Fatalf("PATH changed by run_command: got %q, want %q", got, pathValue)
	}
}

func TestRunCommandDoesNotInheritUnallowlistedEnvironment(t *testing.T) {
	t.Setenv("LOCALGATE_SECRET_TEST", "should-not-leak")
	ws := config.Workspace{ID: "ws", CommandAllowlist: []string{"printf"}}
	sink := core.NewEventSink()
	tool := NewTool(ws, 0)
	_, err := tool.Execute(context.Background(), core.InvokeContext{TaskID: "t-env", Sink: sink}, map[string]any{
		"command": `printf '%s' "${LOCALGATE_SECRET_TEST-unset}"`,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	events, _ := sink.List("t-env", 0)
	var stdout []string
	for _, event := range events {
		if event.Type == core.EventTaskStdout {
			stdout = append(stdout, event.Data)
		}
	}
	if got := strings.Join(stdout, "\n"); got != "unset" {
		t.Fatalf("unallowlisted environment leaked: %q", got)
	}
}

func TestRunTimeout(t *testing.T) {
	ws := config.Workspace{
		ID:               "ws",
		CommandAllowlist: []string{"sleep", "sh", "/bin/sh"},
	}
	tool := NewTool(ws, 0)
	sink := core.NewEventSink()
	start := time.Now()
	res, _ := tool.Execute(context.Background(), core.InvokeContext{TaskID: "t2", Sink: sink}, map[string]any{
		"command":    "sleep 1",
		"timeout_ms": 100,
	})
	if res["exit_code"] == nil {
		t.Fatalf("missing exit")
	}
	if time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("timeout didn't work")
	}
}

func TestCancelKillsProcessGroup(t *testing.T) {
	ws := config.Workspace{
		ID:               "ws",
		CommandAllowlist: []string{"sleep"},
	}
	tool := NewTool(ws, 0)
	sink := core.NewEventSink()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := tool.Execute(ctx, core.InvokeContext{TaskID: "t3", Sink: sink}, map[string]any{
			"command": "sleep 10",
		})
		done <- err
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected cancel error")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("did not return after cancel")
	}
}

func TestRunCommandRejectsRelativeCWD(t *testing.T) {
	root := t.TempDir()
	ws := config.Workspace{
		ID:               "ws",
		Roots:            []string{root},
		CWD:              root,
		CommandAllowlist: []string{"echo", "sh", "/bin/sh"},
	}
	tool := NewTool(ws, 0)
	_, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"command": "echo hello",
		"cwd":     ".",
	})
	if err == nil {
		t.Fatalf("expected relative cwd to be rejected")
	}
}

func TestRunArgvRejectsRelativeCWD(t *testing.T) {
	root := t.TempDir()
	ws := config.Workspace{
		ID:               "ws",
		Roots:            []string{root},
		CWD:              root,
		CommandAllowlist: []string{"echo"},
	}
	tool := NewArgvTool(ws, 0)
	_, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"argv": []any{"echo", "hello"},
		"cwd":  "subdir",
	})
	if err == nil {
		t.Fatalf("expected relative cwd to be rejected")
	}
}

func TestRunCommandAllowsAbsoluteCWD(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ws := config.Workspace{
		ID:               "ws",
		Roots:            []string{root},
		CWD:              root,
		CommandAllowlist: []string{"pwd", "sh", "/bin/sh"},
	}
	tool := NewTool(ws, 0)
	sink := core.NewEventSink()
	_, err := tool.Execute(context.Background(), core.InvokeContext{TaskID: "t4", Sink: sink}, map[string]any{
		"command": "pwd",
		"cwd":     subdir,
	})
	if err != nil {
		t.Fatalf("expected absolute cwd to work: %v", err)
	}
}
