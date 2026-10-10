package core

import (
	"context"
	"strings"
	"testing"
)

type echoTool struct{}

func (e echoTool) Name() string        { return "echo" }
func (e echoTool) Description() string { return "Echo arguments" }
func (e echoTool) Schema() any         { return nil }
func (e echoTool) Execute(ctx context.Context, ic InvokeContext, args map[string]any) (map[string]any, error) {
	return map[string]any{"echo": args}, nil
}

func TestRegistryInvoke(t *testing.T) {
	sink := NewEventSink()
	reg := NewRegistry(sink)
	reg.Register(echoTool{})
	out, err := reg.Invoke(context.Background(), "echo", InvokeContext{TaskID: "t1"}, map[string]any{"msg": "hi"})
	if err != nil {
		t.Fatalf("invoke error: %v", err)
	}
	if out["echo"] == nil {
		t.Fatalf("expected echo in result")
	}
	evs, _ := sink.List("t1", 0)
	if len(evs) == 0 {
		t.Fatalf("expected ToolInvoked event")
	}
	if evs[0].Type != EventToolInvoked {
		t.Fatalf("expected ToolInvoked event, got %s", evs[0].Type)
	}
	if !strings.Contains(evs[0].Data, `"msg":"hi"`) {
		t.Fatalf("expected args summary in event data, got %q", evs[0].Data)
	}
}

func TestRegistryInvokeRedactsSensitiveArgs(t *testing.T) {
	sink := NewEventSink()
	reg := NewRegistry(sink)
	reg.Register(echoTool{})
	_, err := reg.Invoke(context.Background(), "echo", InvokeContext{TaskID: "t2"}, map[string]any{
		"token":           "secret-token",
		"handoff_context": "sensitive compacted project context",
		"env": map[string]any{
			"OPENAI_API_KEY": "abc",
		},
		"nested": map[string]any{
			"password": "pw",
		},
	})
	if err != nil {
		t.Fatalf("invoke error: %v", err)
	}
	evs, _ := sink.List("t2", 0)
	if len(evs) == 0 {
		t.Fatalf("expected ToolInvoked event")
	}
	data := evs[0].Data
	if strings.Contains(data, "secret-token") || strings.Contains(data, "sensitive compacted project context") || strings.Contains(data, `"OPENAI_API_KEY":"abc"`) || strings.Contains(data, `"password":"pw"`) {
		t.Fatalf("expected sensitive values to be redacted, got %q", data)
	}
	if !strings.Contains(data, `"token":"[REDACTED]"`) {
		t.Fatalf("expected token redaction, got %q", data)
	}
	if !strings.Contains(data, `"handoff_context":"[REDACTED]"`) {
		t.Fatalf("expected handoff context redaction, got %q", data)
	}
}

func TestRegistryVisibleFiltering(t *testing.T) {
	reg := NewRegistry(nil)
	reg.Register(echoTool{})
	reg.Hide("echo")

	if reg.IsVisible("echo") {
		t.Fatalf("expected echo to be hidden")
	}
	if names := reg.ListVisible(); len(names) != 0 {
		t.Fatalf("expected no visible tools, got %v", names)
	}
	if _, ok := reg.GetVisible("echo"); ok {
		t.Fatalf("expected hidden tool to be inaccessible via GetVisible")
	}
	if _, ok := reg.Get("echo"); !ok {
		t.Fatalf("expected hidden tool to remain internally registered")
	}
}
