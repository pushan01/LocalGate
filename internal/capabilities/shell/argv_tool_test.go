package shell

import (
	"context"
	"testing"
	"time"

	"localgate/internal/config"
	"localgate/internal/core"
)

func TestRunArgvEcho(t *testing.T) {
	ws := config.Workspace{
		ID:               "ws",
		CommandAllowlist: []string{"echo"},
	}
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
	ws := config.Workspace{
		ID:               "ws",
		CommandAllowlist: []string{"sleep"},
	}
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
