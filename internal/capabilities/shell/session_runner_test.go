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
	s := sessions.Create(core.Session{WorkspaceID: "ws"}, core.SessionController{})
	r := SessionRunner{WS: config.Workspace{ID: "ws"}, Sessions: sessions}
	want := strings.Repeat("x", 2*1024*1024)
	r.streamSessionOutput(s.ID, "stdout", strings.NewReader(want))
	read, err := sessions.ReadForWorkspace(s.ID, "ws", 0, 0)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := joinSessionOutput(read.Items); got != want {
		t.Fatalf("long output changed: got=%d want=%d", len(got), len(want))
	}
}

func TestSessionRunnerReportsOutputReadErrors(t *testing.T) {
	sessions := core.NewSessionManager()
	s := sessions.Create(core.Session{WorkspaceID: "ws"}, core.SessionController{})
	r := SessionRunner{WS: config.Workspace{ID: "ws"}, Sessions: sessions}
	r.streamSessionOutput(s.ID, "stdout", &failingReader{})
	read, err := sessions.ReadForWorkspace(s.ID, "ws", 0, 0)
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
	r := SessionRunner{WS: config.Workspace{ID: "ws", Roots: []string{root}, CWD: root, CommandAllowlist: []string{"echo"}}, Sessions: core.NewSessionManager()}
	if _, err := r.StartSession(context.Background(), "tester", "echo hello", root, nil, fakeShell, false, false); err == nil || !strings.Contains(err.Error(), "trusted system shell") {
		t.Fatalf("expected untrusted shell rejection, got %v", err)
	}
}

func TestSessionRunnerDoesNotInheritUnallowlistedEnvironment(t *testing.T) {
	t.Setenv("LOCALGATE_SECRET_TEST", "should-not-leak")
	r := SessionRunner{WS: config.Workspace{ID: "ws", CommandAllowlist: []string{"printf"}}, Sessions: core.NewSessionManager()}
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

func TestSessionRunnerStartAndRead(t *testing.T) {
	ws := config.Workspace{
		ID:               "ws",
		CommandAllowlist: []string{"echo", "cat", "sh"},
	}
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

func TestSessionRunnerWriteStdin(t *testing.T) {
	ws := config.Workspace{
		ID:               "ws",
		CommandAllowlist: []string{"echo", "cat", "sh"},
	}
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

func TestSessionRunnerTTY(t *testing.T) {
	ws := config.Workspace{
		ID:               "ws",
		CommandAllowlist: []string{"sh"},
	}
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
	ws := config.Workspace{
		ID:               "ws",
		Roots:            []string{t.TempDir()},
		CommandAllowlist: []string{"echo", "sh"},
	}
	sessions := core.NewSessionManager()
	r := SessionRunner{WS: ws, Sessions: sessions}
	_, err := r.StartSession(context.Background(), "tester", "echo hello", ".", nil, "", false, false)
	if err == nil {
		t.Fatalf("expected relative workdir to be rejected")
	}
}
