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

func TestShellToolDescriptionsExplainSelectionBoundary(t *testing.T) {
	ws := config.Workspace{ID: "ws"}
	run := NewTool(ws, 0)
	exec := NewExecCommandTool(ws, core.NewSessionManager())
	if !strings.Contains(run.Description(), "one-shot, non-interactive") || !strings.Contains(run.Description(), "Use exec_command instead") {
		t.Fatalf("run_command description does not explain selection boundary: %q", run.Description())
	}
	if !strings.Contains(exec.Description(), "interactive or long-running") || !strings.Contains(exec.Description(), "Use run_command instead") {
		t.Fatalf("exec_command description does not explain selection boundary: %q", exec.Description())
	}
}

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

func TestRunCommandInheritsProcessPathWithoutAllowlist(t *testing.T) {
	dir := t.TempDir()
	commandName := "localgate-path-probe"
	commandPath := filepath.Join(dir, commandName)
	if err := os.WriteFile(commandPath, []byte("#!/bin/sh\nprintf 'path-ok\\n'\n"), 0o755); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	t.Setenv("PATH", dir)

	ws := config.Workspace{
		ID:               "ws",
		CommandAllowlist: []string{commandName},
	}
	sink := core.NewEventSink()
	tool := NewTool(ws, 0)

	res, err := tool.Execute(context.Background(), core.InvokeContext{TaskID: "t-path-default", Sink: sink}, map[string]any{
		"command": commandName,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := res["exit_code"].(int); got != 0 {
		t.Fatalf("unexpected exit: %d", got)
	}

	events, _ := sink.List("t-path-default", 0)
	var stdout []string
	for _, event := range events {
		if event.Type == core.EventTaskStdout {
			stdout = append(stdout, event.Data)
		}
	}
	if got := strings.Join(stdout, "\n"); got != "path-ok" {
		t.Fatalf("process PATH was not inherited: %q", got)
	}
}

func TestMergeEnvAllowedDoesNotPermitUnallowlistedPathOverride(t *testing.T) {
	t.Setenv("PATH", "/trusted/bin")

	env := mergeEnvAllowed(map[string]string{"PATH": "/request/bin"}, nil)
	if got := envValue(env, "PATH"); got != "/trusted/bin" {
		t.Fatalf("unallowlisted PATH override applied: got %q", got)
	}
}

func TestMergeEnvAllowedPermitsAllowlistedPathOverride(t *testing.T) {
	t.Setenv("PATH", "/trusted/bin")

	env := mergeEnvAllowed(map[string]string{"PATH": "/request/bin"}, []string{"PATH"})
	if got := envValue(env, "PATH"); got != "/request/bin" {
		t.Fatalf("allowlisted PATH override not applied: got %q", got)
	}
}

func envValue(env []string, name string) string {
	prefix := name + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
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

func TestRunCommandRejectsNegativeTimeout(t *testing.T) {
	ws := config.Workspace{ID: "ws", CommandAllowlist: []string{"sleep"}}
	tool := NewTool(ws, 12_000)
	if _, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"command":    "sleep 1",
		"timeout_ms": -1,
	}); err == nil || !strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("expected negative timeout rejection, got %v", err)
	}
}

func TestStreamPreservesLongOutputWithoutScannerLimit(t *testing.T) {
	want := strings.Repeat("x", 2*1024*1024)
	sink := core.NewEventSink()
	if err := stream(context.Background(), sink, "long-output", "stdout", strings.NewReader(want)); err != nil {
		t.Fatalf("stream: %v", err)
	}
	events, _ := sink.List("long-output", 0)
	var got strings.Builder
	for _, event := range events {
		if event.Type == core.EventTaskStdout {
			got.WriteString(event.Data)
		}
	}
	if got.String() != want {
		t.Fatalf("long output changed: got=%d want=%d", got.Len(), len(want))
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
