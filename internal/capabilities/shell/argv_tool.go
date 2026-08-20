package shell

import (
	"context"
	"fmt"
	"time"

	"localgate/internal/config"
	"localgate/internal/core"
)

type ArgvTool struct {
	WS               config.Root
	DefaultTimeoutMs int64
}

func NewArgvTool(ws config.Root, defaultTimeoutMs int64) core.Tool {
	return ArgvTool{WS: ws, DefaultTimeoutMs: defaultTimeoutMs}
}

func (t ArgvTool) Name() string { return "run_argv" }
func (t ArgvTool) Description() string {
	return "Run a command by argv without shell expansion, with an optional absolute cwd inside the configured root"
}
func (t ArgvTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"argv":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"cwd":        map[string]any{"type": "string", "description": "Absolute path inside the configured root"},
			"timeout_ms": map[string]any{"type": "integer", "minimum": 0},
			"env": map[string]any{
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "string"},
			},
		},
		"required": []string{"argv"},
	}
}

func (t ArgvTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	argv := sliceFromAny(args["argv"])
	if len(argv) == 0 {
		return nil, fmt.Errorf("argv required")
	}
	cwd, _ := args["cwd"].(string)
	timeoutMs := intFromAny(args["timeout_ms"])
	if timeoutMs < 0 {
		return nil, fmt.Errorf("timeout_ms must be non-negative")
	}
	if timeoutMs == 0 && t.DefaultTimeoutMs > 0 {
		timeoutMs = int(t.DefaultTimeoutMs)
	}
	timeout, err := durationFromMilliseconds(timeoutMs)
	if err != nil {
		return nil, err
	}
	env := mapFromAny(args["env"])
	r := Runner{WS: t.WS}
	start := time.Now()
	exit, err := r.RunArgv(ctx, ic.TaskID, argv, cwd, env, timeout, ic.Sink)
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

func sliceFromAny(v any) []string {
	out := []string{}
	if v == nil {
		return out
	}
	if s, ok := v.([]string); ok {
		return s
	}
	if a, ok := v.([]any); ok {
		for _, vv := range a {
			if str, ok := vv.(string); ok {
				out = append(out, str)
			}
		}
	}
	return out
}
