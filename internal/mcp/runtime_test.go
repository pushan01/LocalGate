package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type runtimeTestTool struct{}

func (runtimeTestTool) Name() string        { return "echo_v2" }
func (runtimeTestTool) Description() string { return "echo a message" }
func (runtimeTestTool) InputSchema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"message"},
		"properties": map[string]any{
			"message": map[string]any{"type": "string", "minLength": 1},
			"loud":    map[string]any{"type": "boolean"},
		},
	}
}
func (runtimeTestTool) OutputSchema() any {
	return map[string]any{"type": "object"}
}
func (runtimeTestTool) Execute(_ context.Context, input json.RawMessage) (any, error) {
	var request map[string]any
	if err := json.Unmarshal(input, &request); err != nil {
		return nil, err
	}
	return map[string]any{"message": request["message"]}, nil
}

func TestDispatcherValidatesAndExecutesTool(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(runtimeTestTool{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	dispatcher := NewDispatcher(registry)
	result, err := dispatcher.Execute(context.Background(), MCPRequest{
		Tool:  "echo_v2",
		Input: json.RawMessage(`{"message":"hello","loud":true}`),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	payload, _ := result.(map[string]any)
	if payload["message"] != "hello" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestDispatcherRejectsInvalidInput(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(runtimeTestTool{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	dispatcher := NewDispatcher(registry)

	_, err := dispatcher.Execute(context.Background(), MCPRequest{
		Tool:  "echo_v2",
		Input: json.RawMessage(`{"loud":true}`),
	})
	var validationErr *InputValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected validation error, got %v", err)
	}

	_, err = dispatcher.Execute(context.Background(), MCPRequest{
		Tool:  "echo_v2",
		Input: json.RawMessage(`{"message":"hello","extra":1}`),
	})
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected additional property validation error, got %v", err)
	}
}

func TestDispatcherReportsUnknownTool(t *testing.T) {
	dispatcher := NewDispatcher(NewRegistry())
	_, err := dispatcher.Execute(context.Background(), MCPRequest{
		Tool:  "missing",
		Input: json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("expected tool not found, got %v", err)
	}
}

func TestRegistryRejectsDuplicateTool(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(runtimeTestTool{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := registry.Register(runtimeTestTool{}); err == nil {
		t.Fatalf("expected duplicate registration error")
	}
}

type invalidOutputTool struct{}

func (invalidOutputTool) Name() string        { return "invalid_output" }
func (invalidOutputTool) Description() string { return "returns an invalid output" }
func (invalidOutputTool) InputSchema() any {
	return map[string]any{"type": "object"}
}
func (invalidOutputTool) OutputSchema() any {
	return map[string]any{
		"type":     "object",
		"required": []string{"status"},
		"properties": map[string]any{
			"status": map[string]any{"type": "string"},
		},
	}
}
func (invalidOutputTool) Execute(context.Context, json.RawMessage) (any, error) {
	return map[string]any{"status": 123}, nil
}

func TestDispatcherRejectsInvalidToolOutput(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(invalidOutputTool{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, err := NewDispatcher(registry).Execute(context.Background(), MCPRequest{
		Tool:  "invalid_output",
		Input: json.RawMessage(`{}`),
	})
	var outputErr *OutputValidationError
	if !errors.As(err, &outputErr) {
		t.Fatalf("expected output validation error, got %v", err)
	}
}
