package core

import (
	"context"
	"errors"
	"io"
	"sort"
	"sync"
	"time"
)

var ErrSessionNotFound = errors.New("session not found")
var ErrSessionClosed = errors.New("session closed")
var ErrSessionWorkspaceMismatch = errors.New("session workspace mismatch")
var ErrSessionCursorInvalid = errors.New("session cursor must be non-negative")

type SessionController struct {
	Stdin io.WriteCloser
	Close func() error
}

type sessionState struct {
	session    Session
	outputs    []SessionOutput
	maxOut     int
	baseOffset int
	nextOffset int
	ctrl       SessionController
	watchers   map[chan struct{}]struct{}
	stdinMu    sync.Mutex
}

type SessionManager struct {
	mu            sync.Mutex
	sessions      map[string]*sessionState
	maxPerSession int
	cleanup       SessionCleanupOptions
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions:      make(map[string]*sessionState),
		maxPerSession: 1000,
		cleanup: SessionCleanupOptions{
			FinishedTTL: 30 * time.Minute,
			IdleTTL:     30 * time.Minute,
			MaxSessions: 100,
		},
	}
}

func NewSessionManagerWithLimit(limit int) *SessionManager {
	m := NewSessionManager()
	if limit > 0 {
		m.maxPerSession = limit
	}
	return m
}

func (m *SessionManager) SetCleanupOptions(opt SessionCleanupOptions) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanup = opt
}

func (m *SessionManager) Create(s Session, ctrl SessionController) Session {
	_ = m.Sweep(time.Now().UTC())
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.ID == "" {
		s.ID = newID()
	}
	if s.Status == "" {
		s.Status = SessionStarting
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	if s.LastActiveAt.IsZero() {
		s.LastActiveAt = s.CreatedAt
	}
	cp := s
	m.sessions[s.ID] = &sessionState{session: cp, maxOut: m.maxPerSession, ctrl: ctrl, watchers: map[chan struct{}]struct{}{}}
	return cp
}

func (m *SessionManager) SetController(id string, ctrl SessionController) error {
	_ = m.Sweep(time.Now().UTC())
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	st.ctrl = ctrl
	return nil
}

func (m *SessionManager) Start(id string) error {
	_ = m.Sweep(time.Now().UTC())
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	if st.session.Status == SessionStarting {
		st.session.Status = SessionRunning
		now := time.Now().UTC()
		st.session.StartedAt = now
		st.session.LastActiveAt = now
		notifySessionWatchersLocked(st)
	}
	return nil
}

func (m *SessionManager) Subscribe(id string) (<-chan struct{}, func(), error) {
	_ = m.Sweep(time.Now().UTC())
	m.mu.Lock()
	st, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return nil, nil, ErrSessionNotFound
	}
	watcher := make(chan struct{}, 1)
	if st.watchers == nil {
		st.watchers = map[chan struct{}]struct{}{}
	}
	st.watchers[watcher] = struct{}{}
	m.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			m.mu.Lock()
			if current, exists := m.sessions[id]; exists {
				if _, subscribed := current.watchers[watcher]; subscribed {
					delete(current.watchers, watcher)
					close(watcher)
				}
			}
			m.mu.Unlock()
		})
	}
	return watcher, cancel, nil
}

func (m *SessionManager) Get(id string) (Session, error) {
	_ = m.Sweep(time.Now().UTC())
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sessions[id]
	if !ok {
		return Session{}, ErrSessionNotFound
	}
	return st.session, nil
}

func (m *SessionManager) Read(id string, from int, maxItems int) (SessionReadResult, error) {
	_, read, err := m.ReadSnapshot(id, from, maxItems)
	return read, err
}

func (m *SessionManager) ReadSnapshot(id string, from int, maxItems int) (Session, SessionReadResult, error) {
	_ = m.Sweep(time.Now().UTC())
	if from < 0 {
		return Session{}, SessionReadResult{}, ErrSessionCursorInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sessions[id]
	if !ok {
		return Session{}, SessionReadResult{}, ErrSessionNotFound
	}
	return st.session, readSessionState(st, from, maxItems), nil
}

func readSessionState(st *sessionState, from int, maxItems int) SessionReadResult {
	base := st.baseOffset
	nextAvailable := st.nextOffset
	cursorExpired := from < base
	if from < base {
		from = base
	}
	if from > nextAvailable {
		from = nextAvailable
	}
	start := from - base
	arr := st.outputs
	if start < 0 {
		start = 0
	}
	if start > len(arr) {
		start = len(arr)
	}
	if maxItems <= 0 || start+maxItems > len(arr) {
		maxItems = len(arr) - start
	}
	out := make([]SessionOutput, maxItems)
	copy(out, arr[start:start+maxItems])
	next := from + maxItems
	now := time.Now().UTC()
	st.session.LastOffset = next
	st.session.LastReadAt = now
	st.session.LastActiveAt = now
	return SessionReadResult{
		Items:           out,
		NextOffset:      next,
		BaseOffset:      base,
		Truncated:       st.baseOffset > 0,
		BufferTruncated: st.baseOffset > 0,
		CursorExpired:   cursorExpired,
		HasMore:         next < nextAvailable,
		OutputError:     st.session.OutputError,
	}
}

func (m *SessionManager) ReadLatest(id string, maxItems int) (SessionReadResult, error) {
	_ = m.Sweep(time.Now().UTC())
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sessions[id]
	if !ok {
		return SessionReadResult{}, ErrSessionNotFound
	}
	return readSessionState(st, st.session.LastOffset, maxItems), nil
}

func (m *SessionManager) WaitRead(ctx context.Context, id string, from, maxItems int, wait time.Duration) (Session, SessionReadResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if wait <= 0 {
		return m.ReadSnapshot(id, from, maxItems)
	}
	updates, cancel, err := m.Subscribe(id)
	if err != nil {
		return Session{}, SessionReadResult{}, err
	}
	defer cancel()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		session, read, readErr := m.ReadSnapshot(id, from, maxItems)
		if readErr != nil {
			return Session{}, SessionReadResult{}, readErr
		}
		if len(read.Items) > 0 || read.CursorExpired || read.OutputError != "" || isEnded(session.Status) {
			return session, read, nil
		}
		select {
		case <-ctx.Done():
			return Session{}, SessionReadResult{}, ctx.Err()
		case <-timer.C:
			return m.ReadSnapshot(id, from, maxItems)
		case _, open := <-updates:
			if !open {
				return m.ReadSnapshot(id, from, maxItems)
			}
		}
	}
}

func (m *SessionManager) AppendOutput(id, stream, data string) error {
	return m.AppendOutputChunk(id, stream, data, false)
}

func (m *SessionManager) AppendOutputChunk(id, stream, data string, continues bool) error {
	_ = m.Sweep(time.Now().UTC())
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	now := time.Now().UTC()
	st.outputs = append(st.outputs, SessionOutput{
		Offset:    st.nextOffset,
		Stream:    stream,
		Data:      data,
		Continues: continues,
		Time:      now,
	})
	st.nextOffset++
	st.session.LastOutputAt = now
	st.session.LastActiveAt = now
	if st.maxOut > 0 && len(st.outputs) > st.maxOut {
		dropped := len(st.outputs) - st.maxOut
		st.outputs = st.outputs[dropped:]
		st.baseOffset += dropped
		if st.session.LastOffset < st.baseOffset {
			st.session.LastOffset = st.baseOffset
		}
	}
	notifySessionWatchersLocked(st)
	return nil
}

func (m *SessionManager) SetOutputError(id, stream string, err error) error {
	if err == nil {
		return nil
	}
	_ = m.Sweep(time.Now().UTC())
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	message := err.Error()
	if stream != "" {
		message = stream + ": " + message
	}
	st.session.OutputError = message
	st.session.LastActiveAt = time.Now().UTC()
	notifySessionWatchersLocked(st)
	return nil
}

func (m *SessionManager) WriteStdin(id string, chars string) error {
	return m.writeStdin(id, chars)
}

func (m *SessionManager) WriteStdinContext(ctx context.Context, id, chars string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if chars == "" {
		return m.writeStdin(id, chars)
	}
	done := make(chan error, 1)
	go func() {
		done <- m.writeStdin(id, chars)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// Prefer a write that completed concurrently with cancellation; in that
		// case all input was delivered and the session does not need recovery.
		select {
		case err := <-done:
			return err
		default:
		}
		// Once an in-flight stdin write is canceled, partial delivery is
		// possible. Closing the session is safer than leaving a blocked or
		// protocol-corrupted interactive process behind, and it unblocks
		// pipe/PTY writes.
		_, _ = m.Close(id)
		select {
		case <-done:
		case <-time.After(time.Second):
		}
		return ctx.Err()
	}
}

func (m *SessionManager) writeStdin(id, chars string) error {
	_ = m.Sweep(time.Now().UTC())
	m.mu.Lock()
	st, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return ErrSessionNotFound
	}
	if chars == "" {
		m.mu.Unlock()
		return nil
	}
	if isEnded(st.session.Status) {
		m.mu.Unlock()
		return ErrSessionClosed
	}
	m.mu.Unlock()

	st.stdinMu.Lock()
	defer st.stdinMu.Unlock()

	m.mu.Lock()
	current, ok := m.sessions[id]
	if !ok || current != st {
		m.mu.Unlock()
		return ErrSessionNotFound
	}
	if isEnded(st.session.Status) {
		m.mu.Unlock()
		return ErrSessionClosed
	}
	stdin := st.ctrl.Stdin
	m.mu.Unlock()
	if stdin == nil {
		return ErrSessionClosed
	}
	_, err := io.WriteString(stdin, chars)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	m.mu.Lock()
	if st2, ok := m.sessions[id]; ok {
		st2.session.LastInputAt = now
		st2.session.LastActiveAt = now
	}
	m.mu.Unlock()
	return nil
}

func (m *SessionManager) Finish(id string, exitCode int, err error) error {
	_ = m.Sweep(time.Now().UTC())
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	st.session.ExitCode = exitCode
	now := time.Now().UTC()
	if st.session.EndedAt.IsZero() {
		st.session.EndedAt = now
	}
	st.session.LastActiveAt = now
	if st.session.Status == SessionClosed {
		return nil
	}
	if err != nil {
		st.session.Status = SessionFailed
		st.session.ErrorMsg = err.Error()
	} else {
		st.session.Status = SessionExited
	}
	notifySessionWatchersLocked(st)
	return nil
}

func (m *SessionManager) Close(id string) (Session, error) {
	_ = m.Sweep(time.Now().UTC())
	m.mu.Lock()
	st, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return Session{}, ErrSessionNotFound
	}
	closeFn := st.ctrl.Close
	st.session.Status = SessionClosed
	now := time.Now().UTC()
	st.session.EndedAt = now
	st.session.LastActiveAt = now
	out := st.session
	notifySessionWatchersLocked(st)
	m.mu.Unlock()
	if closeFn != nil {
		return out, closeFn()
	}
	return out, nil
}

func (m *SessionManager) CloseAll() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	closeFns := make([]func() error, 0, len(m.sessions))
	now := time.Now().UTC()
	for _, st := range m.sessions {
		if isEnded(st.session.Status) {
			continue
		}
		st.session.Status = SessionClosed
		st.session.EndedAt = now
		st.session.LastActiveAt = now
		notifySessionWatchersLocked(st)
		if st.ctrl.Close != nil {
			closeFns = append(closeFns, st.ctrl.Close)
		}
	}
	m.mu.Unlock()
	var errs []error
	for _, closeFn := range closeFns {
		if err := closeFn(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *SessionManager) Sweep(now time.Time) SessionSweepResult {
	m.mu.Lock()
	type closeAction struct {
		fn func() error
	}
	closeActions := []closeAction{}
	deletedIDs := map[string]struct{}{}
	res := SessionSweepResult{}
	for id, st := range m.sessions {
		if shouldDeleteFinished(st.session, m.cleanup, now) {
			closeSessionWatchersLocked(st)
			delete(m.sessions, id)
			deletedIDs[id] = struct{}{}
			res.Deleted++
			continue
		}
		if shouldCloseIdle(st.session, m.cleanup, now) {
			if st.ctrl.Close != nil {
				closeActions = append(closeActions, closeAction{fn: st.ctrl.Close})
				res.Closed++
			}
			closeSessionWatchersLocked(st)
			delete(m.sessions, id)
			deletedIDs[id] = struct{}{}
			res.Deleted++
		}
	}
	if m.cleanup.MaxSessions > 0 && len(m.sessions) > m.cleanup.MaxSessions {
		type candidate struct {
			id  string
			key time.Time
		}
		candidates := []candidate{}
		for id, st := range m.sessions {
			if isEnded(st.session.Status) {
				key := st.session.EndedAt
				if key.IsZero() {
					key = st.session.CreatedAt
				}
				candidates = append(candidates, candidate{id: id, key: key})
			}
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].key.Before(candidates[j].key) })
		for _, c := range candidates {
			if len(m.sessions) <= m.cleanup.MaxSessions {
				break
			}
			closeSessionWatchersLocked(m.sessions[c.id])
			delete(m.sessions, c.id)
			if _, ok := deletedIDs[c.id]; !ok {
				res.Deleted++
				deletedIDs[c.id] = struct{}{}
			}
		}
	}
	m.mu.Unlock()
	for _, action := range closeActions {
		if action.fn != nil {
			_ = action.fn()
		}
	}
	return res
}

func notifySessionWatchersLocked(st *sessionState) {
	if st == nil {
		return
	}
	for watcher := range st.watchers {
		select {
		case watcher <- struct{}{}:
		default:
		}
	}
}

func closeSessionWatchersLocked(st *sessionState) {
	if st == nil {
		return
	}
	for watcher := range st.watchers {
		close(watcher)
		delete(st.watchers, watcher)
	}
}

func shouldDeleteFinished(s Session, opt SessionCleanupOptions, now time.Time) bool {
	if opt.FinishedTTL <= 0 || !isEnded(s.Status) || s.EndedAt.IsZero() {
		return false
	}
	return !now.Before(s.EndedAt.Add(opt.FinishedTTL))
}

func shouldCloseIdle(s Session, opt SessionCleanupOptions, now time.Time) bool {
	if opt.IdleTTL <= 0 || isEnded(s.Status) {
		return false
	}
	last := s.LastActiveAt
	if last.IsZero() {
		last = s.CreatedAt
	}
	if last.IsZero() {
		return false
	}
	return !now.Before(last.Add(opt.IdleTTL))
}

func isEnded(status SessionStatus) bool {
	return status == SessionExited || status == SessionFailed || status == SessionClosed
}
