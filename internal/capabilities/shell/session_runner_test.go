package shell

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"localgate/internal/config"
	"localgate/internal/core"
)

type failingReader struct {
	read bool
}

func (r *failingReader) Read(p []byte) (int, error) {
	if !r.read {
		r.read = true
		copy(p, "partial")
		return len("partial"), nil
	}
	return 0, errors.New("read failed")
}

func TestSessionRunnerPreservesLongOutputWithoutScannerLimit(t *testing.T) {
	sessions := core.NewSessionManager()
	s := sessions.Create(core.Session{}, core.SessionController{})
	r := SessionRunner{WS: config.Root{}, Sessions: sessions}
	want := strings.Repeat("x", 2*1024*1024)
	r.streamSessionOutput(s.ID, "stdout", strings.NewReader(want))
	read, err := sessions.Read(s.ID, 0, 0)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := joinSessionOutput(read.Items); got != want {
		t.Fatalf("long output changed: got=%d want=%d", len(got), len(want))
	}
}

func TestSessionRunnerReportsOutputReadErrors(t *testing.T) {
	sessions := core.NewSessionManager()
	s := sessions.Create(core.Session{}, core.SessionController{})
	r := SessionRunner{WS: config.Root{}, Sessions: sessions}
	r.streamSessionOutput(s.ID, "stdout", &failingReader{})
	read, err := sessions.Read(s.ID, 0, 0)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(read.OutputError, "read failed") {
		t.Fatalf("missing output error: %+v", read)
	}
	if got := joinSessionOutput(read.Items); got != "partial" {
		t.Fatalf("partial output lost: %q", got)
	}
}

var _ io.Reader = (*failingReader)(nil)

func TestSessionRunnerRejectsWorkspaceShell(t *testing.T) {
	root := t.TempDir()
	fakeShell := filepath.Join(root, "sh")
	if err := os.WriteFile(fakeShell, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake shell: %v", err)
	}
	r := SessionRunner{WS: config.Root{Path: root, CommandAllowlist: []string{"echo"}}, Sessions: core.NewSessionManager()}
	if _, err := r.StartSession(context.Background(), "tester", "echo hello", root, nil, fakeShell, false, false); err == nil || !strings.Contains(err.Error(), "trusted system shell") {
		t.Fatalf("expected untrusted shell rejection, got %v", err)
	}
}

func TestSessionRunnerDoesNotInheritUnallowlistedEnvironment(t *testing.T) {
	t.Setenv("LOCALGATE_SECRET_TEST", "should-not-leak")
	r := SessionRunner{WS: config.Root{CommandAllowlist: []string{"printf"}}, Sessions: core.NewSessionManager()}
	s, err := r.StartSession(context.Background(), "tester", `printf '%s\n' "${LOCALGATE_SECRET_TEST-unset}"`, "", nil, "/bin/sh", false, false)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	_, read, err := r.ReadSession(s.ID, 10)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := joinSessionOutput(read.Items); got != "unset" {
		t.Fatalf("unallowlisted environment leaked: %q", got)
	}
}

func TestSessionRunnerInheritsProcessPathWithoutAllowlist(t *testing.T) {
	dir := t.TempDir()
	commandName := "localgate-session-path-probe"
	commandPath := filepath.Join(dir, commandName)
	if err := os.WriteFile(commandPath, []byte("#!/bin/sh\nprintf 'session-path-ok\\n'\n"), 0o755); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	t.Setenv("PATH", dir)

	r := SessionRunner{
		WS:       config.Root{CommandAllowlist: []string{commandName}},
		Sessions: core.NewSessionManager(),
	}
	s, err := r.StartSession(context.Background(), "tester", commandName, "", nil, "/bin/sh", false, false)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	_, read, err := r.ReadSession(s.ID, 10)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := joinSessionOutput(read.Items); got != "session-path-ok" {
		t.Fatalf("process PATH was not inherited by session: %q", got)
	}
}

func TestSessionRunnerStartAndRead(t *testing.T) {
	ws := config.Root{CommandAllowlist: []string{"echo", "cat", "sh"}}
	sessions := core.NewSessionManager()
	r := SessionRunner{WS: ws, Sessions: sessions}
	s, err := r.StartSession(context.Background(), "tester", "echo hello", "", nil, "", false, false)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	s, read, err := r.ReadSession(s.ID, 10)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	if s.ID == "" {
		t.Fatalf("missing session id")
	}
	if got := joinSessionOutput(read.Items); got != "hello" {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestSessionOutputWriterPreservesLongLineAndNewlines(t *testing.T) {
	sessions := core.NewSessionManagerWithLimit(1000)
	s := sessions.Create(core.Session{}, core.SessionController{})
	w := newSessionOutputWriter(sessions, s.ID, "stdout")
	want := strings.Repeat("x", sessionOutputChunkBytes*2+17) + "\nsecond\nthird"
	for start := 0; start < len(want); start += 7000 {
		end := start + 7000
		if end > len(want) {
			end = len(want)
		}
		if _, err := w.Write([]byte(want[start:end])); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	_, read, err := sessions.ReadSnapshot(s.ID, 0, 100)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := joinSessionOutput(read.Items); got != want {
		t.Fatalf("output changed: got=%d bytes want=%d bytes", len(got), len(want))
	}
}

func TestSessionRunnerExitStatusMeansOutputDrained(t *testing.T) {
	ws := config.Root{CommandAllowlist: []string{"printf"}}
	sessions := core.NewSessionManager()
	r := SessionRunner{WS: ws, Sessions: sessions}
	s, err := r.StartSession(context.Background(), "tester", "printf 'tail-output'", "", nil, "", false, false)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		current, read, readErr := sessions.ReadSnapshot(s.ID, 0, 10)
		if readErr != nil {
			t.Fatalf("read: %v", readErr)
		}
		if current.Status == core.SessionExited {
			if got := joinSessionOutput(read.Items); got != "tail-output" {
				t.Fatalf("session exited before final output was drained: %q", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session did not exit: %+v", current)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestReadThreadTerminalToolPagesTerminalOutputUntilComplete(t *testing.T) {
	sessions := core.NewSessionManager()
	s := sessions.Create(core.Session{}, core.SessionController{})
	for _, value := range []string{"one", "two", "three"} {
		if err := sessions.AppendOutput(s.ID, "stdout", value); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if err := sessions.Finish(s.ID, 0, nil); err != nil {
		t.Fatalf("finish: %v", err)
	}
	tool := NewReadThreadTerminalTool(config.Root{}, sessions)
	first, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"session_id":       s.ID,
		"cursor":           0,
		"max_output_items": 2,
	})
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if first["has_more"] != true || first["completed"] != false || first["next_offset"] != 2 {
		t.Fatalf("unexpected first page: %#v", first)
	}
	second, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"session_id":       s.ID,
		"cursor":           2,
		"max_output_items": 2,
	})
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if second["has_more"] != false || second["completed"] != true || second["next_offset"] != 3 {
		t.Fatalf("unexpected second page: %#v", second)
	}
}

func TestWriteStdinToolEmptyInputReadsExitedSession(t *testing.T) {
	sessions := core.NewSessionManager()
	s := sessions.Create(core.Session{}, core.SessionController{})
	if err := sessions.AppendOutput(s.ID, "stdout", "tail"); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := sessions.Finish(s.ID, 0, nil); err != nil {
		t.Fatalf("finish: %v", err)
	}
	tool := NewWriteStdinTool(config.Root{}, sessions)
	res, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"session_id":       s.ID,
		"chars":            "",
		"cursor":           0,
		"max_output_items": 10,
	})
	if err != nil {
		t.Fatalf("empty write/read: %v", err)
	}
	if res["output"] != "tail" || res["completed"] != true {
		t.Fatalf("unexpected result: %#v", res)
	}
}

func TestSessionToolsRejectInvalidCursorAndOversizedInput(t *testing.T) {
	sessions := core.NewSessionManager()
	s := sessions.Create(core.Session{}, core.SessionController{})
	readTool := NewReadThreadTerminalTool(config.Root{}, sessions)
	if _, err := readTool.Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"session_id": s.ID,
		"cursor":     -1,
	}); !errors.Is(err, core.ErrSessionCursorInvalid) {
		t.Fatalf("expected invalid cursor, got %v", err)
	}
	writeTool := NewWriteStdinTool(config.Root{}, sessions)
	if _, err := writeTool.Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"session_id": s.ID,
		"chars":      strings.Repeat("x", maxSessionInputBytes+1),
	}); err == nil || !strings.Contains(err.Error(), "session input limit") {
		t.Fatalf("expected input limit error, got %v", err)
	}
}

func TestSessionRunnerWriteStdin(t *testing.T) {
	ws := config.Root{CommandAllowlist: []string{"echo", "cat", "sh"}}
	sessions := core.NewSessionManager()
	r := SessionRunner{WS: ws, Sessions: sessions}
	s, err := r.StartSession(context.Background(), "tester", "cat", "", nil, "", false, false)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	_, read, err := r.WriteStdin(s.ID, "hello\n", 100*time.Millisecond, 10)
	if err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if got := joinSessionOutput(read.Items); got != "hello" {
		t.Fatalf("unexpected output: %q", got)
	}
	closed, err := r.CloseSession(s.ID)
	if err != nil {
		t.Fatalf("close session: %v", err)
	}
	if closed.Status != core.SessionClosed {
		t.Fatalf("unexpected status: %s", closed.Status)
	}
}

func TestCloseSessionToolTerminatesRunningSession(t *testing.T) {
	ws := config.Root{CommandAllowlist: []string{"sh"}}
	sessions := core.NewSessionManager()
	r := SessionRunner{WS: ws, Sessions: sessions}
	s, err := r.StartSession(context.Background(), "tester", "sh", "", nil, "", false, true)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	tool := NewCloseSessionTool(ws, sessions)
	res, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"session_id": s.ID})
	if err != nil {
		t.Fatalf("close session: %v", err)
	}
	if res["status"] != core.SessionClosed || res["running"] != false {
		t.Fatalf("unexpected close result: %#v", res)
	}
	current, err := sessions.Get(s.ID)
	if err != nil {
		t.Fatalf("get closed session: %v", err)
	}
	if current.Status != core.SessionClosed {
		t.Fatalf("session still active: %s", current.Status)
	}
}

func TestSessionRunnerTTY(t *testing.T) {
	ws := config.Root{CommandAllowlist: []string{"sh"}}
	sessions := core.NewSessionManager()
	r := SessionRunner{WS: ws, Sessions: sessions}
	s, err := r.StartSession(context.Background(), "tester", "sh", "", nil, "", false, true)
	if err != nil {
		t.Fatalf("start tty session: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	_, _, err = r.WriteStdin(s.ID, "echo hello\n", 150*time.Millisecond, 50)
	if err != nil {
		t.Fatalf("write tty stdin: %v", err)
	}
	_, read, err := r.ReadSession(s.ID, 50)
	if err != nil {
		t.Fatalf("read tty session: %v", err)
	}
	if _, _, err := r.WriteStdin(s.ID, "exit\n", 150*time.Millisecond, 50); err != nil {
		t.Fatalf("write tty exit: %v", err)
	}
	got := joinSessionOutput(read.Items)
	if !strings.Contains(got, "hello") {
		t.Fatalf("expected tty output to contain hello, got %q", got)
	}
}

func TestSessionRunnerRejectsRelativeWorkdir(t *testing.T) {
	ws := config.Root{Path: t.TempDir(),
		CommandAllowlist: []string{"echo", "sh"},
	}
	sessions := core.NewSessionManager()
	r := SessionRunner{WS: ws, Sessions: sessions}
	_, err := r.StartSession(context.Background(), "tester", "echo hello", ".", nil, "", false, false)
	if err == nil {
		t.Fatalf("expected relative workdir to be rejected")
	}
}
