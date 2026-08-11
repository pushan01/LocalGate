package toolaccess

import (
	"context"
	"testing"

	"localgate/internal/core"
)

type dummyTool struct {
	name string
	desc string
}

func (t dummyTool) Name() string        { return t.name }
func (t dummyTool) Description() string { return t.desc }
func (t dummyTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
	}
}
func (t dummyTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	return map[string]any{"ok": true}, nil
}

func TestGetToolSchemaTool(t *testing.T) {
	reg := core.NewRegistry(nil)
	reg.Register(dummyTool{name: "read_file", desc: "Read file contents"})
	tool := NewGetToolSchemaTool(reg)

	res, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"tool": "read_file"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got, _ := res["name"].(string); got != "read_file" {
		t.Fatalf("unexpected name: %#v", res)
	}
	if got, _ := res["description"].(string); got != "Read file contents" {
		t.Fatalf("unexpected description: %#v", res)
	}
	schema, ok := res["schema"].(map[string]any)
	if !ok {
		t.Fatalf("missing schema: %#v", res)
	}
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["path"]; !ok {
		t.Fatalf("expected original tool schema, got %#v", schema)
	}
	if _, ok := props["base64data"]; ok {
		t.Fatalf("get_tool_schema must return original schema, got %#v", schema)
	}
}

func TestGetToolSchemaToolRejectsBootstrapTool(t *testing.T) {
	for _, name := range []string{"get_startup_instructions", "get_skill", "get_temporary_access_token", "get_tool_schema"} {
		reg := core.NewRegistry(nil)
		reg.Register(dummyTool{name: name, desc: "bootstrap"})
		tool := NewGetToolSchemaTool(reg)

		_, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"tool": name})
		if err == nil || err.Error() != "bootstrap tools do not have HTTP-callable schemas" {
			t.Fatalf("bootstrap tool %q returned unexpected error: %v", name, err)
		}
	}
}

func TestGetToolSchemaToolRequiresVisibleTool(t *testing.T) {
	reg := core.NewRegistry(nil)
	reg.Register(dummyTool{name: "read_file", desc: "Read file contents"})
	reg.Hide("read_file")
	tool := NewGetToolSchemaTool(reg)

	_, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"tool": "read_file"})
	if err == nil || err.Error() != "tool not found" {
		t.Fatalf("unexpected error: %v", err)
	}
}
