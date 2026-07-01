package shell

import (
	"context"
	"strings"
	"testing"
	"time"

	"localgate/internal/config"
	"localgate/internal/core"
)

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
