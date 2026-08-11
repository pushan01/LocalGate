package core

import (
	"sync"
	"time"
)

type EventType string

const (
	EventTaskCreated   EventType = "TaskCreated"
	EventTaskStarted   EventType = "TaskStarted"
	EventTaskStdout    EventType = "TaskStdout"
	EventTaskStderr    EventType = "TaskStderr"
	EventTaskCompleted EventType = "TaskCompleted"
	EventTaskFailed    EventType = "TaskFailed"
	EventTaskCanceled  EventType = "TaskCanceled"
	EventTaskTimeout   EventType = "TaskTimeout"
	EventToolInvoked   EventType = "ToolInvoked"
)

type Event struct {
	TaskID string    `json:"task_id"`
	Type   EventType `json:"type"`
	Time   time.Time `json:"time"`
	// Optional fields for logs
	Stream    string `json:"stream,omitempty"` // stdout/stderr
	Data      string `json:"data,omitempty"`
	Continues bool   `json:"continues,omitempty"` // output chunk continues the same logical line
}

type EventWriter interface {
	AppendEvent(Event) error
}

type EventSink struct {
	mu         sync.Mutex
	events     map[string][]Event
	maxPerTask int
	writer     EventWriter
	subs       map[string]map[chan Event]struct{}
	observer   func(Event)
}

func NewEventSink() *EventSink {
	return &EventSink{events: make(map[string][]Event), maxPerTask: 1000, subs: make(map[string]map[chan Event]struct{})}
}

func NewEventSinkWithLimit(limit int) *EventSink {
	if limit <= 0 {
		limit = 1000
	}
	return &EventSink{events: make(map[string][]Event), maxPerTask: limit, subs: make(map[string]map[chan Event]struct{})}
}

func (s *EventSink) SetWriter(w EventWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writer = w
}

func (s *EventSink) SetObserver(observer func(Event)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observer = observer
}

func (s *EventSink) Append(ev Event) {
	s.mu.Lock()
	buf := append(s.events[ev.TaskID], ev)
	if s.maxPerTask > 0 && len(buf) > s.maxPerTask {
		buf = buf[len(buf)-s.maxPerTask:]
	}
	s.events[ev.TaskID] = buf
	if s.writer != nil {
		_ = s.writer.AppendEvent(ev)
	}
	if m := s.subs[ev.TaskID]; m != nil {
		for ch := range m {
			select {
			case ch <- ev:
			default:
			}
		}
	}
	observer := s.observer
	s.mu.Unlock()
	if observer != nil {
		observer(ev)
	}
}

// List returns events for a task from a given offset and the next offset
func (s *EventSink) List(taskID string, from int) ([]Event, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	arr := s.events[taskID]
	if from < 0 || from > len(arr) {
		from = 0
	}
	out := make([]Event, len(arr[from:]))
	copy(out, arr[from:])
	return out, len(arr)
}

// RemoveTasks drops retained in-memory events for completed task history.
func (s *EventSink) RemoveTasks(taskIDs []string) {
	if len(taskIDs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, taskID := range taskIDs {
		delete(s.events, taskID)
	}
}

func (s *EventSink) Subscribe(taskID string, buffer int) (chan Event, func()) {
	if buffer <= 0 {
		buffer = 64
	}
	ch := make(chan Event, buffer)
	s.mu.Lock()
	if s.subs[taskID] == nil {
		s.subs[taskID] = make(map[chan Event]struct{})
	}
	s.subs[taskID][ch] = struct{}{}
	s.mu.Unlock()
	cancel := func() {
		s.mu.Lock()
		if m := s.subs[taskID]; m != nil {
			delete(m, ch)
			if len(m) == 0 {
				delete(s.subs, taskID)
			}
		}
		s.mu.Unlock()
		close(ch)
	}
	return ch, cancel
}
