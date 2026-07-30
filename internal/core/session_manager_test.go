package core

import (
	"errors"
	"testing"
	"time"
)

func TestSessionManagerUsesAbsoluteCursorAfterTrim(t *testing.T) {
	m := NewSessionManagerWithLimit(3)
	s := m.Create(Session{WorkspaceID: "ws"}, SessionController{})
	for _, value := range []string{"zero", "one", "two", "three", "four"} {
		if err := m.AppendOutput(s.ID, "stdout", value); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	read, err := m.ReadForWorkspace(s.ID, "ws", 0, 10)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if read.BaseOffset != 2 || read.NextOffset != 5 || !read.Truncated || !read.CursorExpired {
		t.Fatalf("unexpected cursor metadata: %+v", read)
	}
	if len(read.Items) != 3 || read.Items[0].Offset != 2 || read.Items[2].Offset != 4 {
		t.Fatalf("unexpected retained output: %+v", read.Items)
	}
}

func TestSessionManagerClientCursorDoesNotRepeatPages(t *testing.T) {
	m := NewSessionManager()
	s := m.Create(Session{WorkspaceID: "ws"}, SessionController{})
	for _, value := range []string{"a", "b", "c"} {
		_ = m.AppendOutput(s.ID, "stdout", value)
	}
	first, err := m.ReadForWorkspace(s.ID, "ws", 0, 2)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	second, err := m.ReadForWorkspace(s.ID, "ws", first.NextOffset, 2)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if len(first.Items) != 2 || len(second.Items) != 1 || second.Items[0].Data != "c" {
		t.Fatalf("unexpected pages: first=%+v second=%+v", first, second)
	}
}

func TestSessionManagerRejectsCrossWorkspaceAccess(t *testing.T) {
	m := NewSessionManager()
	s := m.Create(Session{WorkspaceID: "ws-a"}, SessionController{})
	if _, err := m.GetForWorkspace(s.ID, "ws-b"); !errors.Is(err, ErrSessionWorkspaceMismatch) {
		t.Fatalf("get mismatch: %v", err)
	}
	if _, err := m.ReadForWorkspace(s.ID, "ws-b", 0, 1); !errors.Is(err, ErrSessionWorkspaceMismatch) {
		t.Fatalf("read mismatch: %v", err)
	}
	if err := m.WriteStdinForWorkspace(s.ID, "ws-b", "x"); !errors.Is(err, ErrSessionWorkspaceMismatch) {
		t.Fatalf("stdin mismatch: %v", err)
	}
	if _, err := m.CloseForWorkspace(s.ID, "ws-b"); !errors.Is(err, ErrSessionWorkspaceMismatch) {
		t.Fatalf("close mismatch: %v", err)
	}
}

func TestSessionManagerSweepDeletesFinished(t *testing.T) {
	m := NewSessionManager()
	m.SetCleanupOptions(SessionCleanupOptions{
		FinishedTTL: time.Minute,
		IdleTTL:     0,
		MaxSessions: 100,
	})
	s := m.Create(Session{}, SessionController{})
	if err := m.Finish(s.ID, 0, nil); err != nil {
		t.Fatalf("finish: %v", err)
	}
	m.mu.Lock()
	st := m.sessions[s.ID]
	st.session.EndedAt = time.Now().UTC().Add(-2 * time.Minute)
	m.mu.Unlock()

	res := m.Sweep(time.Now().UTC())
	if res.Deleted != 1 {
		t.Fatalf("expected 1 deleted, got %+v", res)
	}
	if _, err := m.Get(s.ID); err == nil {
		t.Fatalf("expected session deleted")
	}
}

func TestSessionManagerSweepClosesIdleRunning(t *testing.T) {
	m := NewSessionManager()
	m.SetCleanupOptions(SessionCleanupOptions{
		FinishedTTL: 0,
		IdleTTL:     time.Minute,
		MaxSessions: 100,
	})
	closed := false
	s := m.Create(Session{}, SessionController{
		Close: func() error {
			closed = true
			return nil
		},
	})
	if err := m.Start(s.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
	m.mu.Lock()
	st := m.sessions[s.ID]
	st.session.LastActiveAt = time.Now().UTC().Add(-2 * time.Minute)
	m.mu.Unlock()

	res := m.Sweep(time.Now().UTC())
	if res.Closed != 1 || res.Deleted != 1 {
		t.Fatalf("unexpected sweep result: %+v", res)
	}
	if !closed {
		t.Fatalf("expected close callback called")
	}
	if _, err := m.Get(s.ID); err == nil {
		t.Fatalf("expected idle session removed")
	}
}

func TestSessionManagerSweepKeepsNewestEndedWhenOverLimit(t *testing.T) {
	m := NewSessionManager()
	m.SetCleanupOptions(SessionCleanupOptions{
		FinishedTTL: 0,
		IdleTTL:     0,
		MaxSessions: 10,
	})
	s1 := m.Create(Session{}, SessionController{})
	_ = m.Finish(s1.ID, 0, nil)
	s2 := m.Create(Session{}, SessionController{})
	_ = m.Finish(s2.ID, 0, nil)
	m.SetCleanupOptions(SessionCleanupOptions{
		FinishedTTL: 0,
		IdleTTL:     0,
		MaxSessions: 1,
	})
	m.mu.Lock()
	m.sessions[s1.ID].session.EndedAt = time.Now().UTC().Add(-2 * time.Minute)
	m.sessions[s2.ID].session.EndedAt = time.Now().UTC().Add(-time.Minute)
	m.mu.Unlock()

	res := m.Sweep(time.Now().UTC())
	if res.Deleted != 1 {
		t.Fatalf("expected 1 deleted, got %+v", res)
	}
	if _, err := m.Get(s1.ID); err == nil {
		t.Fatalf("expected oldest session removed")
	}
	if _, err := m.Get(s2.ID); err != nil {
		t.Fatalf("expected newest ended session kept: %v", err)
	}
}

func TestSessionManagerFinishDoesNotOverwriteExplicitClose(t *testing.T) {
	m := NewSessionManager()
	s := m.Create(Session{WorkspaceID: "ws"}, SessionController{})
	if err := m.Start(s.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := m.CloseForWorkspace(s.ID, "ws"); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := m.Finish(s.ID, -1, errors.New("signal: terminated")); err != nil {
		t.Fatalf("finish: %v", err)
	}
	got, err := m.GetForWorkspace(s.ID, "ws")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != SessionClosed || got.ErrorMsg != "" {
		t.Fatalf("explicit close was overwritten: %#v", got)
	}
}
