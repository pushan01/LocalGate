package core

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

func TestSessionManagerUsesAbsoluteCursorAfterTrim(t *testing.T) {
	m := NewSessionManagerWithLimit(3)
	s := m.Create(Session{}, SessionController{})
	for _, value := range []string{"zero", "one", "two", "three", "four"} {
		if err := m.AppendOutput(s.ID, "stdout", value); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	read, err := m.Read(s.ID, 0, 10)
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
	s := m.Create(Session{}, SessionController{})
	for _, value := range []string{"a", "b", "c"} {
		_ = m.AppendOutput(s.ID, "stdout", value)
	}
	first, err := m.Read(s.ID, 0, 2)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	second, err := m.Read(s.ID, first.NextOffset, 2)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if len(first.Items) != 2 || len(second.Items) != 1 || second.Items[0].Data != "c" {
		t.Fatalf("unexpected pages: first=%+v second=%+v", first, second)
	}
}

func TestSessionManagerReportsHasMoreForPagedRead(t *testing.T) {
	m := NewSessionManager()
	s := m.Create(Session{}, SessionController{})
	for _, value := range []string{"a", "b", "c"} {
		_ = m.AppendOutput(s.ID, "stdout", value)
	}
	_, first, err := m.ReadSnapshot(s.ID, 0, 2)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if !first.HasMore || first.Truncated || first.BufferTruncated || first.NextOffset != 2 {
		t.Fatalf("unexpected first page metadata: %+v", first)
	}
	_, second, err := m.ReadSnapshot(s.ID, first.NextOffset, 2)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if second.HasMore || len(second.Items) != 1 || second.NextOffset != 3 {
		t.Fatalf("unexpected second page: %+v", second)
	}
}

func TestSessionManagerWaitReadWakesOnOutput(t *testing.T) {
	m := NewSessionManager()
	s := m.Create(Session{}, SessionController{})
	if err := m.Start(s.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = m.AppendOutput(s.ID, "stdout", "ready")
	}()
	start := time.Now()
	_, read, err := m.WaitRead(context.Background(), s.ID, 0, 10, time.Second)
	if err != nil {
		t.Fatalf("wait read: %v", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("wait read did not wake promptly")
	}
	if len(read.Items) != 1 || read.Items[0].Data != "ready" {
		t.Fatalf("unexpected read: %+v", read)
	}
}

func TestSessionManagerEmptyWriteAllowedAfterExit(t *testing.T) {
	m := NewSessionManager()
	s := m.Create(Session{}, SessionController{})
	if err := m.Start(s.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := m.Finish(s.ID, 0, nil); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if err := m.WriteStdinContext(context.Background(), s.ID, ""); err != nil {
		t.Fatalf("empty write on exited session: %v", err)
	}
	if err := m.WriteStdinContext(context.Background(), s.ID, "x"); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("non-empty write should fail on exited session: %v", err)
	}
}

type serializedWriteProbe struct {
	mu      sync.Mutex
	active  bool
	overlap bool
}

func (w *serializedWriteProbe) Write(p []byte) (int, error) {
	w.mu.Lock()
	if w.active {
		w.overlap = true
	}
	w.active = true
	w.mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	w.mu.Lock()
	w.active = false
	w.mu.Unlock()
	return len(p), nil
}

func (w *serializedWriteProbe) Close() error { return nil }

func TestSessionManagerSerializesConcurrentStdinWrites(t *testing.T) {
	m := NewSessionManager()
	probe := &serializedWriteProbe{}
	s := m.Create(Session{}, SessionController{Stdin: probe})
	if err := m.Start(s.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
	var wg sync.WaitGroup
	for _, chars := range []string{"first", "second"} {
		wg.Add(1)
		go func(value string) {
			defer wg.Done()
			if err := m.WriteStdin(s.ID, value); err != nil {
				t.Errorf("write %q: %v", value, err)
			}
		}(chars)
	}
	wg.Wait()
	probe.mu.Lock()
	overlap := probe.overlap
	probe.mu.Unlock()
	if overlap {
		t.Fatal("stdin writes overlapped")
	}
}

type blockingSessionWriter struct {
	startedOnce sync.Once
	closedOnce  sync.Once
	started     chan struct{}
	closed      chan struct{}
}

func newBlockingSessionWriter() *blockingSessionWriter {
	return &blockingSessionWriter{started: make(chan struct{}), closed: make(chan struct{})}
}

func (w *blockingSessionWriter) Write([]byte) (int, error) {
	w.startedOnce.Do(func() { close(w.started) })
	<-w.closed
	return 0, io.ErrClosedPipe
}

func (w *blockingSessionWriter) Close() error {
	w.closedOnce.Do(func() { close(w.closed) })
	return nil
}

func TestSessionManagerPreCanceledWriteDoesNotReachStdin(t *testing.T) {
	m := NewSessionManager()
	probe := &serializedWriteProbe{}
	s := m.Create(Session{}, SessionController{Stdin: probe})
	if err := m.Start(s.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.WriteStdinContext(ctx, s.ID, "must-not-write"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	probe.mu.Lock()
	active := probe.active
	overlap := probe.overlap
	probe.mu.Unlock()
	if active || overlap {
		t.Fatal("pre-canceled stdin request reached writer")
	}
}

func TestSessionManagerCanceledBlockingWriteClosesSession(t *testing.T) {
	m := NewSessionManager()
	writer := newBlockingSessionWriter()
	s := m.Create(Session{}, SessionController{Stdin: writer, Close: writer.Close})
	if err := m.Start(s.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- m.WriteStdinContext(ctx, s.ID, "blocked")
	}()
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("write did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled write did not return")
	}
	got, err := m.Get(s.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != SessionClosed {
		t.Fatalf("session not closed after canceled partial write: %s", got.Status)
	}
}

func TestSessionManagerRejectsMissingSession(t *testing.T) {
	m := NewSessionManager()
	if _, err := m.Get("missing"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("get missing: %v", err)
	}
	if _, err := m.Read("missing", 0, 1); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("read missing: %v", err)
	}
	if err := m.WriteStdin("missing", "x"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("stdin missing: %v", err)
	}
	if _, err := m.Close("missing"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("close missing: %v", err)
	}
	if _, _, err := m.Subscribe("missing"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("subscribe missing: %v", err)
	}
}

func TestSessionManagerSubscriptionSignalsOutputAndFinish(t *testing.T) {
	m := NewSessionManager()
	s := m.Create(Session{}, SessionController{})
	updates, cancel, err := m.Subscribe(s.ID)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancel()

	if err := m.AppendOutput(s.ID, "stdout", "hello"); err != nil {
		t.Fatalf("append: %v", err)
	}
	select {
	case <-updates:
	case <-time.After(time.Second):
		t.Fatal("output update was not signaled")
	}

	if err := m.Finish(s.ID, 0, nil); err != nil {
		t.Fatalf("finish: %v", err)
	}
	select {
	case <-updates:
	case <-time.After(time.Second):
		t.Fatal("finish update was not signaled")
	}
}

func TestSessionManagerSubscriptionCancelRemovesWatcher(t *testing.T) {
	m := NewSessionManager()
	s := m.Create(Session{}, SessionController{})
	updates, cancel, err := m.Subscribe(s.ID)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	cancel()
	cancel()
	select {
	case _, ok := <-updates:
		if ok {
			t.Fatal("expected canceled subscription channel to close")
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not close subscription")
	}
	m.mu.Lock()
	watchers := len(m.sessions[s.ID].watchers)
	m.mu.Unlock()
	if watchers != 0 {
		t.Fatalf("expected watcher removal, got %d", watchers)
	}
}

func TestSessionManagerSubscriptionClosesWhenSessionSwept(t *testing.T) {
	m := NewSessionManager()
	m.SetCleanupOptions(SessionCleanupOptions{FinishedTTL: time.Minute, MaxSessions: 100})
	s := m.Create(Session{}, SessionController{})
	updates, cancel, err := m.Subscribe(s.ID)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancel()
	if err := m.Finish(s.ID, 0, nil); err != nil {
		t.Fatalf("finish: %v", err)
	}
	<-updates
	m.mu.Lock()
	m.sessions[s.ID].session.EndedAt = time.Now().UTC().Add(-2 * time.Minute)
	m.mu.Unlock()
	m.Sweep(time.Now().UTC())
	select {
	case _, ok := <-updates:
		if ok {
			t.Fatal("expected subscription channel to close")
		}
	case <-time.After(time.Second):
		t.Fatal("sweep did not close subscription")
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
	s := m.Create(Session{}, SessionController{})
	if err := m.Start(s.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := m.Close(s.ID); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := m.Finish(s.ID, -1, errors.New("signal: terminated")); err != nil {
		t.Fatalf("finish: %v", err)
	}
	got, err := m.Get(s.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != SessionClosed || got.ErrorMsg != "" {
		t.Fatalf("explicit close was overwritten: %#v", got)
	}
}

func TestSessionManagerCloseAllClosesActiveControllers(t *testing.T) {
	m := NewSessionManager()
	closed := 0
	for i := 0; i < 2; i++ {
		s := m.Create(Session{}, SessionController{Close: func() error {
			closed++
			return nil
		}})
		if err := m.Start(s.ID); err != nil {
			t.Fatalf("start: %v", err)
		}
	}
	if err := m.CloseAll(); err != nil {
		t.Fatalf("close all: %v", err)
	}
	if closed != 2 {
		t.Fatalf("closed controllers = %d, want 2", closed)
	}
	for _, st := range m.sessions {
		if st.session.Status != SessionClosed {
			t.Fatalf("session still active: %s", st.session.Status)
		}
	}
}
