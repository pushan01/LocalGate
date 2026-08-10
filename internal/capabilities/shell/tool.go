package shell

import (
	"context"
	"fmt"
	"time"

	"localgate/internal/config"
	"localgate/internal/core"
)

type Tool struct {
	WS               config.Workspace
	DefaultTimeoutMs int64
}

func NewTool(ws config.Workspace, defaultTimeoutMs int64) core.Tool {
	return Tool{WS: ws, DefaultTimeoutMs: defaultTimeoutMs}
}

func (t Tool) Name() string { return "run_command" }
func (t Tool) Description() string {
	return "Run a one-shot, non-interactive shell command inside the workspace. Use exec_command instead for interactive programs, dev servers, watch processes, or commands that need stdin after startup."
}
func (t Tool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"command":    map[string]any{"type": "string"},
			"cwd":        map[string]any{"type": "string", "description": "Absolute path inside workspace"},
			"timeout_ms": map[string]any{"type": "integer"},
			"env": map[string]any{
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "string"},
			},
		},
		"required": []string{"command"},
	}
}

func (t Tool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	cmd, _ := args["command"].(string)
	cwd, _ := args["cwd"].(string)
	timeoutMs := intFromAny(args["timeout_ms"])
	if timeoutMs == 0 && t.DefaultTimeoutMs > 0 {
		timeoutMs = int(t.DefaultTimeoutMs)
	}
	env := mapFromAny(args["env"])
	r := Runner{WS: t.WS}
	start := time.Now()
	exit, err := r.Run(ctx, ic.TaskID, cmd, cwd, env, time.Duration(timeoutMs)*time.Millisecond, ic.Sink)
	dur := time.Since(start)
	res := map[string]any{
		"exit_code":   exit,
		"duration_ms": dur.Milliseconds(),
	}
	if err != nil {
		return res, err
	}
	if exit != 0 {
		return res, fmt.Errorf("exit code %d", exit)
	}
	return res, nil
}

func intFromAny(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	default:
		return 0
	}
}

func mapFromAny(v any) map[string]string {
	out := map[string]string{}
	if v == nil {
		return out
	}
	if m, ok := v.(map[string]string); ok {
		return m
	}
	if m, ok := v.(map[string]any); ok {
		for k, vv := range m {
			if s, ok := vv.(string); ok {
				out[k] = s
			}
		}
	}
	return out
}
