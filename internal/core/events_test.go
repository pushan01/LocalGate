package core

import (
	"testing"
	"time"
)

func TestEventSinkBasic(t *testing.T) {
	sink := NewEventSink()
	id := "task-1"
	sink.Append(Event{TaskID: id, Type: EventTaskCreated, Time: time.Now()})
	sink.Append(Event{TaskID: id, Type: EventTaskStarted, Time: time.Now()})
	evs, next := sink.List(id, 0)
	if len(evs) != 2 || next != 2 {
		t.Fatalf("unexpected events len=%d next=%d", len(evs), next)
	}
}

func TestManagerEmitsEvents(t *testing.T) {
	sink := NewEventSink()
	m := NewManagerWithSink(sink)
	tk := m.CreateEmptyTask(10 * time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	evs, _ := sink.List(tk.ID, 0)
	if len(evs) == 0 {
		t.Fatalf("expected some events")
	}
	time.Sleep(20 * time.Millisecond)
	evs2, _ := sink.List(tk.ID, 0)
	if len(evs2) < len(evs) {
		t.Fatalf("expected events to grow")
	}
}

func TestEventSinkLimit(t *testing.T) {
	sink := NewEventSinkWithLimit(3)
	id := "taskX"
	for i := 0; i < 10; i++ {
		sink.Append(Event{TaskID: id, Type: EventTaskStdout, Time: time.Now(), Data: "x"})
	}
	evs, next := sink.List(id, 0)
	if len(evs) != 3 || next != 3 {
		t.Fatalf("expected 3 limited events, got len=%d next=%d", len(evs), next)
	}
}
