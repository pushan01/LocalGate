package core

import (
	"errors"
	"io"
	"sort"
	"sync"
	"time"
)

var ErrSessionNotFound = errors.New("session not found")
var ErrSessionClosed = errors.New("session closed")

type SessionController struct {
	Stdin io.WriteCloser
	Close func() error
}

type sessionState struct {
	session Session
	outputs []SessionOutput
	maxOut  int
	ctrl    SessionController
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
	m.sessions[s.ID] = &sessionState{session: cp, maxOut: m.maxPerSession, ctrl: ctrl}
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
	}
	return nil
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
	_ = m.Sweep(time.Now().UTC())
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sessions[id]
	if !ok {
		return SessionReadResult{}, ErrSessionNotFound
	}
	arr := st.outputs
	if from < 0 || from > len(arr) {
		from = 0
	}
	if maxItems <= 0 || from+maxItems > len(arr) {
		maxItems = len(arr) - from
	}
	out := make([]SessionOutput, maxItems)
	copy(out, arr[from:from+maxItems])
	next := from + maxItems
	now := time.Now().UTC()
	st.session.LastOffset = next
	st.session.LastReadAt = now
	st.session.LastActiveAt = now
	return SessionReadResult{
		Items:      out,
		NextOffset: next,
		Truncated:  st.maxOut > 0 && len(arr) >= st.maxOut,
	}, nil
}

func (m *SessionManager) ReadLatest(id string, maxItems int) (SessionReadResult, error) {
	_ = m.Sweep(time.Now().UTC())
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sessions[id]
	if !ok {
		return SessionReadResult{}, ErrSessionNotFound
	}
	start := st.session.LastOffset
	if start < 0 || start > len(st.outputs) {
		start = 0
	}
	if maxItems > 0 && start+maxItems < len(st.outputs) {
		end := start + maxItems
		out := make([]SessionOutput, end-start)
		copy(out, st.outputs[start:end])
		now := time.Now().UTC()
		st.session.LastOffset = end
		st.session.LastReadAt = now
		st.session.LastActiveAt = now
		return SessionReadResult{Items: out, NextOffset: end, Truncated: st.maxOut > 0 && len(st.outputs) >= st.maxOut}, nil
	}
	out := make([]SessionOutput, len(st.outputs[start:]))
	copy(out, st.outputs[start:])
	now := time.Now().UTC()
	st.session.LastOffset = len(st.outputs)
	st.session.LastReadAt = now
	st.session.LastActiveAt = now
	return SessionReadResult{Items: out, NextOffset: len(st.outputs), Truncated: st.maxOut > 0 && len(st.outputs) >= st.maxOut}, nil
}

func (m *SessionManager) AppendOutput(id, stream, data string) error {
	_ = m.Sweep(time.Now().UTC())
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	now := time.Now().UTC()
	st.outputs = append(st.outputs, SessionOutput{
		Offset: len(st.outputs),
		Stream: stream,
		Data:   data,
		Time:   now,
	})
	st.session.LastOutputAt = now
	st.session.LastActiveAt = now
	if st.maxOut > 0 && len(st.outputs) > st.maxOut {
		st.outputs = st.outputs[len(st.outputs)-st.maxOut:]
		for i := range st.outputs {
			st.outputs[i].Offset = i
		}
		if st.session.LastOffset > len(st.outputs) {
			st.session.LastOffset = len(st.outputs)
		}
	}
	return nil
}

func (m *SessionManager) WriteStdin(id string, chars string) error {
	_ = m.Sweep(time.Now().UTC())
	m.mu.Lock()
	st, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return ErrSessionNotFound
	}
	if st.session.Status == SessionClosed || st.session.Status == SessionExited || st.session.Status == SessionFailed {
		m.mu.Unlock()
		return ErrSessionClosed
	}
	stdin := st.ctrl.Stdin
	m.mu.Unlock()
	if stdin == nil {
		return ErrSessionClosed
	}
	if chars == "" {
		return nil
	}
	now := time.Now().UTC()
	m.mu.Lock()
	if st2, ok := m.sessions[id]; ok {
		st2.session.LastInputAt = now
		st2.session.LastActiveAt = now
	}
	m.mu.Unlock()
	_, err := io.WriteString(stdin, chars)
	return err
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
	st.session.EndedAt = now
	st.session.LastActiveAt = now
	if err != nil {
		st.session.Status = SessionFailed
		st.session.ErrorMsg = err.Error()
	} else {
		st.session.Status = SessionExited
	}
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
	m.mu.Unlock()
	if closeFn != nil {
		return out, closeFn()
	}
	return out, nil
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
