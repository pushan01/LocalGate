package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var ErrToolNotFound = errors.New("mcp runtime tool not found")

type InputValidationError struct {
	Tool string
	Err  error
}

func (e *InputValidationError) Error() string {
	if e == nil {
		return "invalid tool input"
	}
	if e.Tool == "" {
		return "invalid tool input: " + e.Err.Error()
	}
	return fmt.Sprintf("invalid input for %s: %v", e.Tool, e.Err)
}

func (e *InputValidationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type OutputValidationError struct {
	Tool string
	Err  error
}

func (e *OutputValidationError) Error() string {
	if e == nil {
		return "invalid tool output"
	}
	return fmt.Sprintf("invalid output from %s: %v", e.Tool, e.Err)
}

func (e *OutputValidationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type Dispatcher struct {
	Registry *Registry
}

func NewDispatcher(r *Registry) *Dispatcher {
	return &Dispatcher{Registry: r}
}

type MCPRequest struct {
	Tool            string          `json:"tool"`
	Input           json.RawMessage `json:"input"`
	Requester       string          `json:"requester,omitempty"`
	SessionID       string          `json:"session_id,omitempty"`
	SessionMode     string          `json:"session_mode,omitempty"`
	SessionIDSource string          `json:"session_id_source,omitempty"`
	RequestID       string          `json:"request_id,omitempty"`
}

func (d *Dispatcher) Validate(req MCPRequest) (Tool, error) {
	if d == nil || d.Registry == nil {
		return nil, errors.New("mcp runtime is not initialized")
	}
	name := strings.TrimSpace(req.Tool)
	if name == "" {
		return nil, &InputValidationError{Err: errors.New("tool is required")}
	}
	tool, ok := d.Registry.Get(name)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrToolNotFound, name)
	}
	if err := ValidateJSON(req.Input, tool.InputSchema()); err != nil {
		return nil, &InputValidationError{Tool: name, Err: err}
	}
	return tool, nil
}

func (d *Dispatcher) Execute(ctx context.Context, req MCPRequest) (any, error) {
	tool, err := d.Validate(req)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Tool)
	result, err := tool.Execute(ctx, req.Input)
	if err != nil {
		return nil, err
	}
	if tool.OutputSchema() != nil {
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, &OutputValidationError{Tool: name, Err: fmt.Errorf("encode result: %w", err)}
		}
		if err := ValidateJSON(raw, tool.OutputSchema()); err != nil {
			return nil, &OutputValidationError{Tool: name, Err: err}
		}
	}
	return result, nil
}
