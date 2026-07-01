package mcprelay

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"localgate/internal/config"
)

func TestClientStartListToolsAndCall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	client, err := Start(ctx, config.MCPServer{
		Name:    "fake",
		Command: "go",
		Args:    []string{"run", fakeServerDir(t)},
	})
	if err != nil {
		t.Fatalf("start client: %v", err)
	}
	defer client.Close()

	tools := client.Tools()
	if len(tools) != 2 {
		t.Fatalf("unexpected tools: %#v", tools)
	}
	if tools[0].Name != "echo_remote" {
		t.Fatalf("unexpected first tool: %#v", tools[0])
	}
	schema, ok := tools[0].InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("expected object schema, got %#v", tools[0].InputSchema)
	}
	if schema["type"] != "object" {
		t.Fatalf("unexpected schema type: %#v", schema)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok || props["text"] == nil {
		t.Fatalf("unexpected schema properties: %#v", schema)
	}

	result, err := client.CallTool(ctx, "echo_remote", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("unexpected content: %#v", result.Content)
	}
	if got, ok := result.StructuredContent.(map[string]any); !ok || got["echo"] != "hello" {
		t.Fatalf("unexpected structured content: %#v", result.StructuredContent)
	}
}

func TestClientCallToolReturnsRPCError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	client, err := Start(ctx, config.MCPServer{
		Name:    "fake",
		Command: "go",
		Args:    []string{"run", fakeServerDir(t)},
	})
	if err != nil {
		t.Fatalf("start client: %v", err)
	}
	defer client.Close()

	_, err = client.CallTool(ctx, "missing_tool", nil)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected RPCError, got: %v", err)
	}
	if rpcErr.Code != -32601 {
		t.Fatalf("unexpected rpc error: %#v", rpcErr)
	}
}

func TestClientCallToolAfterProcessExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	client, err := Start(ctx, config.MCPServer{
		Name:    "fake",
		Command: "go",
		Args:    []string{"run", fakeServerDir(t)},
		Env: map[string]string{
			"FAKE_MCP_EXIT_AFTER_LIST": "1",
		},
	})
	if err != nil {
		t.Fatalf("start client: %v", err)
	}
	defer client.Close()

	time.Sleep(200 * time.Millisecond)
	_, err = client.CallTool(ctx, "echo_remote", map[string]any{"text": "hello"})
	if err == nil || !strings.Contains(err.Error(), "exit") {
		t.Fatalf("expected exited error, got: %v", err)
	}
}

func TestNormalizeToolSchemaCoercesNullToObject(t *testing.T) {
	schema, err := normalizeToolSchema(mcp.Tool{
		Name:           "no_params",
		RawInputSchema: json.RawMessage("null"),
	})
	if err != nil {
		t.Fatalf("normalize schema: %v", err)
	}
	obj, ok := schema.(map[string]any)
	if !ok {
		t.Fatalf("expected object schema, got %#v", schema)
	}
	if obj["type"] != "object" {
		t.Fatalf("unexpected schema: %#v", obj)
	}
	if _, ok := obj["properties"].(map[string]any); !ok {
		t.Fatalf("unexpected properties: %#v", obj)
	}
}

func fakeServerDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "testutil", "fake_mcp_server"))
}
