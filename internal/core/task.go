package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

type Status string

const (
	StatusAwaitingApproval Status = "awaiting_approval"
	StatusQueued           Status = "queued"
	StatusRunning          Status = "running"
	StatusSucceeded        Status = "succeeded"
	StatusFailed           Status = "failed"
	StatusCanceled         Status = "canceled"
	StatusTimeout          Status = "timeout"
)

type Task struct {
	ID        string    `json:"id"`
	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	StartedAt time.Time `json:"started_at,omitempty"`
	EndedAt   time.Time `json:"ended_at,omitempty"`

	Transport       string `json:"transport,omitempty"`
	MCPSessionID    string `json:"session_id,omitempty"`
	SessionMode     string `json:"session_mode,omitempty"`
	SessionIDSource string `json:"session_id_source,omitempty"`
	RequestID       string `json:"request_id,omitempty"`

	Requester   string    `json:"requester,omitempty"`
	Tool        string    `json:"tool,omitempty"`
	ArgsDigest  string    `json:"args_digest,omitempty"`
	ApprovalID  string    `json:"approval_id,omitempty"`
	ApprovalURL string    `json:"approval_url,omitempty"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`

	ExitCode  int            `json:"exit_code,omitempty"`
	ErrorCode string         `json:"error_code,omitempty"`
	ErrorMsg  string         `json:"error_msg,omitempty"`
	Result    map[string]any `json:"result,omitempty"`
}

type Manager struct {
	mu           sync.Mutex
	wg           sync.WaitGroup
	tasks        map[string]*Task
	cancel       map[string]chan struct{}
	cancelFn     map[string]context.CancelFunc
	cancelReason map[string]CancellationReason
	running      map[string]struct{}
	sink         *EventSink
	sem          chan struct{}
	tw           TaskWriter
}

type CancellationReason struct {
	Code    string
	Message string
}

type TaskWriter interface {
	SaveTask(Task) error
}

func NewManager() *Manager {
	return &Manager{
		tasks:        make(map[string]*Task),
		cancel:       make(map[string]chan struct{}),
		cancelFn:     make(map[string]context.CancelFunc),
		cancelReason: make(map[string]CancellationReason),
		running:      make(map[string]struct{}),
	}
}

func NewManagerWithSink(sink *EventSink) *Manager {
	m := NewManager()
	m.sink = sink
	return m
}

func NewManagerWithOptions(sink *EventSink, maxConcurrency int) *Manager {
	m := NewManagerWithSink(sink)
	if maxConcurrency > 0 {
		m.sem = make(chan struct{}, maxConcurrency)
	}
	return m
}

func (m *Manager) SetTaskWriter(w TaskWriter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tw = w
}

// LoadHistory loads previously persisted tasks into the manager.
// It should be called at startup before new tasks are created.
func (m *Manager) LoadHistory(tasks []Task) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range tasks {
		// don't override if already present
		if _, exists := m.tasks[t.ID]; exists {
			continue
		}
		cp := cloneTask(t)
		m.tasks[t.ID] = &cp
	}
}

// RecoverInterruptedTasks marks persisted runnable tasks as canceled because
// their goroutines cannot survive a process restart. Approval-gated tasks are
// reconciled separately by the approval service.
func (m *Manager) RecoverInterruptedTasks() error {
	now := time.Now().UTC()
	type persisted struct {
		task Task
	}
	updates := make([]persisted, 0)
	m.mu.Lock()
	for _, task := range m.tasks {
		if task.Status != StatusQueued && task.Status != StatusRunning {
			continue
		}
		task.Status = StatusCanceled
		task.EndedAt = now
		task.ExitCode = -1
		task.ErrorCode = "service_restart"
		task.ErrorMsg = "task was interrupted by service restart"
		updates = append(updates, persisted{task: cloneTask(*task)})
	}
	w := m.tw
	sink := m.sink
	m.mu.Unlock()

	for _, update := range updates {
		if w != nil {
			if err := w.SaveTask(update.task); err != nil {
				return err
			}
		}
		if sink != nil {
			sink.Append(Event{TaskID: update.task.ID, Type: EventTaskCanceled, Time: now})
		}
	}
	return nil
}

// CancelRecoveredApproval marks a persisted approval-gated task as canceled
// during startup reconciliation. Recovered tasks do not have a live context or
// gate, so Manager.Cancel cannot transition them safely.
func (m *Manager) CancelRecoveredApproval(id string) error {
	m.mu.Lock()
	t, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	if t.Status != StatusAwaitingApproval {
		m.mu.Unlock()
		return nil
	}
	t.Status = StatusCanceled
	t.EndedAt = time.Now().UTC()
	t.ExitCode = -1
	t.ErrorCode = "approval_restart"
	t.ErrorMsg = "pending approval was canceled after service restart"
	snapshot := *t
	w := m.tw
	sink := m.sink
	m.mu.Unlock()

	if w != nil {
		if err := w.SaveTask(snapshot); err != nil {
			return err
		}
	}
	if sink != nil {
		sink.Append(Event{TaskID: id, Type: EventTaskCanceled, Time: snapshot.EndedAt})
	}
	return nil
}

func (m *Manager) CreateEmptyTask(runFor time.Duration) *Task {
	id := newID()
	t := &Task{
		ID:        id,
		Status:    StatusQueued,
		CreatedAt: time.Now().UTC(),
	}
	m.mu.Lock()
	m.tasks[id] = t
	ch := make(chan struct{})
	m.cancel[id] = ch
	w := m.tw
	m.mu.Unlock()
	if w != nil {
		_ = w.SaveTask(cloneTask(*t))
	}

	if m.sink != nil {
		m.sink.Append(Event{
			TaskID: id,
			Type:   EventTaskCreated,
			Time:   time.Now().UTC(),
		})
	}

	// simulate execution
	snapshot := cloneTask(*t)
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.startTask(id)
		timer := time.NewTimer(runFor)
		defer timer.Stop()
		select {
		case <-timer.C:
			m.finishTask(id, StatusSucceeded, 0, "", "")
		case <-ch:
			code, message := m.cancellationReasonFor(id, "", "")
			m.finishTask(id, StatusCanceled, 0, code, message)
		}
	}()
	return &snapshot
}

func (m *Manager) startTask(id string) {
	m.mu.Lock()
	var snapshot Task
	var changed bool
	if t, ok := m.tasks[id]; ok && t.Status == StatusQueued {
		t.Status = StatusRunning
		t.StartedAt = time.Now().UTC()
		m.running[id] = struct{}{}
		snapshot = cloneTask(*t)
		changed = true
	}
	w := m.tw
	sink := m.sink
	m.mu.Unlock()
	if !changed {
		return
	}
	if w != nil {
		_ = w.SaveTask(snapshot)
	}
	if sink != nil {
		sink.Append(Event{TaskID: id, Type: EventTaskStarted, Time: snapshot.StartedAt})
	}
}

func (m *Manager) finishTask(id string, status Status, exit int, code, msg string) {
	m.mu.Lock()
	var snapshot Task
	var changed bool
	if t, ok := m.tasks[id]; ok {
		if t.Status == StatusRunning || t.Status == StatusQueued || t.Status == StatusAwaitingApproval {
			t.Status = status
			t.EndedAt = time.Now().UTC()
			t.ExitCode = exit
			t.ErrorCode = code
			t.ErrorMsg = msg
			snapshot = cloneTask(*t)
			changed = true
		}
	}
	delete(m.running, id)
	delete(m.cancelReason, id)
	w := m.tw
	sink := m.sink
	m.mu.Unlock()
	if !changed {
		return
	}
	if w != nil {
		_ = w.SaveTask(snapshot)
	}
	if sink != nil {
		evType := EventTaskCompleted
		switch status {
		case StatusCanceled:
			evType = EventTaskCanceled
		case StatusFailed:
			evType = EventTaskFailed
		case StatusTimeout:
			evType = EventTaskTimeout
		}
		sink.Append(Event{TaskID: id, Type: evType, Time: snapshot.EndedAt})
	}
}

func (m *Manager) SetResult(id string, result map[string]any) error {
	m.mu.Lock()
	t, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	if result == nil {
		t.Result = nil
	} else {
		t.Result = cloneResultMap(result)
	}
	snapshot := cloneTask(*t)
	w := m.tw
	m.mu.Unlock()
	if w != nil {
		_ = w.SaveTask(snapshot)
	}
	return nil
}

var ErrNotFound = errors.New("task not found")

func (m *Manager) Get(id string) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return nil, ErrNotFound
	}
	// Return a detached snapshot; callers must use Get for later state.
	c := cloneTask(*t)
	return &c, nil
}

// RemoveTerminalTasks removes retained history for the supplied task IDs.
// Active and approval-gated tasks are never removed by this method.
func (m *Manager) RemoveTerminalTasks(ids []string) {
	if len(ids) == 0 {
		return
	}
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id != "" {
			wanted[id] = struct{}{}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range wanted {
		task, ok := m.tasks[id]
		if !ok || !isTerminalTaskStatus(task.Status) {
			continue
		}
		delete(m.tasks, id)
		delete(m.cancel, id)
		delete(m.cancelFn, id)
		delete(m.cancelReason, id)
		delete(m.running, id)
	}
}

func isTerminalTaskStatus(status Status) bool {
	switch status {
	case StatusSucceeded, StatusFailed, StatusCanceled, StatusTimeout:
		return true
	default:
		return false
	}
}

func (m *Manager) Cancel(id string) error {
	return m.CancelWithReason(id, "", "")
}

func (m *Manager) CancelWithReason(id, code, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.tasks[id]
	if !ok {
		return ErrNotFound
	}
	if task.Status == StatusSucceeded || task.Status == StatusFailed || task.Status == StatusCanceled || task.Status == StatusTimeout {
		return nil
	}
	if code != "" || message != "" {
		m.cancelReason[id] = CancellationReason{Code: code, Message: message}
	}
	if fn, ok := m.cancelFn[id]; ok {
		fn()
		return nil
	}
	if ch, ok := m.cancel[id]; ok {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	return nil
}

func (m *Manager) cancellationReasonFor(id, fallbackCode, fallbackMessage string) (string, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if reason, ok := m.cancelReason[id]; ok {
		return reason.Code, reason.Message
	}
	return fallbackCode, fallbackMessage
}

type RunFunc func(ctx context.Context, taskID string) (int, error)

func (m *Manager) CreateTask(t Task, run RunFunc) *Task {
	return m.createTask(t, nil, run)
}

func (m *Manager) CreateGatedTask(t Task, run RunFunc) (*Task, func()) {
	gate := make(chan struct{})
	var once sync.Once
	release := func() {
		once.Do(func() { close(gate) })
	}
	return m.createTask(t, gate, run), release
}

func (m *Manager) createTask(t Task, gate <-chan struct{}, run RunFunc) *Task {
	id := newID()
	t.ID = id
	if gate == nil {
		t.Status = StatusQueued
	} else {
		t.Status = StatusAwaitingApproval
	}
	t.CreatedAt = time.Now().UTC()

	ctx, cancel := context.WithCancel(context.Background())

	m.mu.Lock()
	m.tasks[id] = &t
	m.cancelFn[id] = cancel
	w := m.tw
	m.mu.Unlock()
	if w != nil {
		_ = w.SaveTask(cloneTask(t))
	}

	if m.sink != nil {
		m.sink.Append(Event{TaskID: id, Type: EventTaskCreated, Time: time.Now().UTC()})
	}

	snapshot := cloneTask(t)
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		if gate != nil {
			select {
			case <-gate:
				// Cancellation may have happened before a late approval release.
				// When both channels are ready, select is intentionally random, so
				// re-check the context before transitioning into the runnable queue.
				if ctx.Err() != nil {
					code, message := m.cancellationReasonFor(id, "approval_canceled", "approval was not granted")
					m.finishTask(id, StatusCanceled, -1, code, message)
					return
				}
				queued := false
				var queuedSnapshot Task
				m.mu.Lock()
				if current, ok := m.tasks[id]; ok && current.Status == StatusAwaitingApproval {
					current.Status = StatusQueued
					queuedSnapshot = cloneTask(*current)
					queued = true
				}
				writer := m.tw
				m.mu.Unlock()
				if !queued {
					return
				}
				if writer != nil {
					_ = writer.SaveTask(queuedSnapshot)
				}
			case <-ctx.Done():
				code, message := m.cancellationReasonFor(id, "approval_canceled", "approval was not granted")
				m.finishTask(id, StatusCanceled, -1, code, message)
				return
			}
		}
		if m.sem != nil {
			select {
			case m.sem <- struct{}{}:
				defer func() { <-m.sem }()
			case <-ctx.Done():
				code, message := m.cancellationReasonFor(id, "", "")
				m.finishTask(id, StatusCanceled, -1, code, message)
				return
			}
		}
		m.startTask(id)
		exit, err := run(ctx, id)
		switch {
		case errors.Is(err, context.Canceled):
			code, message := m.cancellationReasonFor(id, "", "")
			m.finishTask(id, StatusCanceled, exit, code, message)
		case errors.Is(err, context.DeadlineExceeded):
			m.finishTask(id, StatusTimeout, exit, "", "")
		case err != nil:
			code := ""
			var coded interface{ ErrorCode() string }
			if errors.As(err, &coded) {
				code = coded.ErrorCode()
			}
			m.finishTask(id, StatusFailed, exit, code, err.Error())
		case exit != 0:
			m.finishTask(id, StatusFailed, exit, "", "non-zero exit code")
		default:
			m.finishTask(id, StatusSucceeded, exit, "", "")
		}
	}()

	return &snapshot
}

// Close asks active tasks to stop and waits for their final persistence writes.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	cancelFns := make([]context.CancelFunc, 0, len(m.cancelFn))
	cancelChans := make([]chan struct{}, 0, len(m.cancel))
	for id, task := range m.tasks {
		if task.Status == StatusSucceeded || task.Status == StatusFailed || task.Status == StatusCanceled || task.Status == StatusTimeout {
			continue
		}
		m.cancelReason[id] = CancellationReason{Code: "service_shutdown", Message: "task stopped during service shutdown"}
		if fn, ok := m.cancelFn[id]; ok {
			cancelFns = append(cancelFns, fn)
			continue
		}
		if ch, ok := m.cancel[id]; ok {
			cancelChans = append(cancelChans, ch)
		}
	}
	m.mu.Unlock()
	for _, fn := range cancelFns {
		fn()
	}
	for _, ch := range cancelChans {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	m.wg.Wait()
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func cloneTask(in Task) Task {
	out := in
	out.Result = cloneResultMap(in.Result)
	return out
}

func cloneResultMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
