package core

import (
	"testing"
	"time"
)

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
