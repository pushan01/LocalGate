package shell

import (
	"context"
	"strings"
	"testing"
	"time"

	"localgate/internal/config"
	"localgate/internal/core"
)

func TestRunArgvEcho(t *testing.T) {
	ws := config.Root{CommandAllowlist: []string{"echo"}}
	tool := NewArgvTool(ws, 0)
	sink := core.NewEventSink()
	res, err := tool.Execute(context.Background(), core.InvokeContext{TaskID: "targv1", Sink: sink}, map[string]any{
		"argv": []string{"echo", "hello"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res["exit_code"].(int) != 0 {
		t.Fatalf("unexpected exit: %v", res)
	}
}

func TestRunArgvTimeout(t *testing.T) {
	ws := config.Root{CommandAllowlist: []string{"sleep"}}
	tool := NewArgvTool(ws, 0)
	start := time.Now()
	_, err := tool.Execute(context.Background(), core.InvokeContext{TaskID: "targv2", Sink: core.NewEventSink()}, map[string]any{
		"argv":       []string{"sleep", "1"},
		"timeout_ms": 100,
	})
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("timeout didn't work")
	}
}

func TestRunArgvRejectsNegativeTimeout(t *testing.T) {
	ws := config.Root{CommandAllowlist: []string{"sleep"}}
	tool := NewArgvTool(ws, 12_000)
	if _, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"argv":       []string{"sleep", "1"},
		"timeout_ms": -1,
	}); err == nil || !strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("expected negative timeout rejection, got %v", err)
	}
}

func TestRunArgvAllowsLiteralShellMetacharactersInArguments(t *testing.T) {
	ws := config.Root{CommandAllowlist: []string{"echo"}}
	tool := NewArgvTool(ws, 0)
	sink := core.NewEventSink()
	_, err := tool.Execute(context.Background(), core.InvokeContext{TaskID: "literal-meta", Sink: sink}, map[string]any{
		"argv": []string{"echo", "a&b", "dollar$sign", "x|y", "semi;colon"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	events, _ := sink.List("literal-meta", 0)
	var stdout []string
	for _, event := range events {
		if event.Type == core.EventTaskStdout {
			stdout = append(stdout, event.Data)
		}
	}
	if got := strings.Join(stdout, ""); got != "a&b dollar$sign x|y semi;colon" {
		t.Fatalf("argv arguments were changed: %q", got)
	}
}
