package shell

import (
	"context"
	"fmt"
	"strings"
	"time"

	"localgate/internal/config"
	"localgate/internal/core"
)

type ExecCommandTool struct {
	WS       config.Workspace
	Sessions *core.SessionManager
}

func NewExecCommandTool(ws config.Workspace, sessions *core.SessionManager) core.Tool {
	return ExecCommandTool{WS: ws, Sessions: sessions}
}

func (t ExecCommandTool) Name() string { return "exec_command" }

func (t ExecCommandTool) Description() string {
	return "Start an interactive or long-running command session with an optional absolute workdir inside the workspace"
}

func (t ExecCommandTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"cmd":               map[string]any{"type": "string"},
			"workdir":           map[string]any{"type": "string", "description": "Absolute path inside workspace"},
			"yield_time_ms":     map[string]any{"type": "integer"},
			"max_output_tokens": map[string]any{"type": "integer"},
			"shell":             map[string]any{"type": "string"},
			"login":             map[string]any{"type": "boolean"},
			"tty":               map[string]any{"type": "boolean"},
			"justification":     map[string]any{"type": "string"},
			"prefix_rule":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"sandbox_permissions": map[string]any{
				"type": "string",
			},
			"env": map[string]any{
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "string"},
			},
		},
		"required": []string{"cmd"},
	}
}

func (t ExecCommandTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	_ = ic
	cmd, _ := args["cmd"].(string)
	workdir, _ := args["workdir"].(string)
	shellName, _ := args["shell"].(string)
	login, _ := args["login"].(bool)
	tty, _ := args["tty"].(bool)
	yield := intFromAny(args["yield_time_ms"])
	maxItems := intFromAny(args["max_output_tokens"])
	env := mapFromAny(args["env"])
	r := SessionRunner{WS: t.WS, Sessions: t.Sessions}
	session, err := r.StartSession(ctx, "", cmd, workdir, env, shellName, login, tty)
	if err != nil {
		return nil, err
	}
	if yield > 0 {
		time.Sleep(time.Duration(yield) * time.Millisecond)
	}
	current, read, err := r.ReadSession(session.ID, maxItems)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"session_id":   current.ID,
		"status":       current.Status,
		"running":      current.Status == core.SessionStarting || current.Status == core.SessionRunning,
		"completed":    current.Status == core.SessionExited || current.Status == core.SessionFailed || current.Status == core.SessionClosed,
		"output":       joinSessionOutput(read.Items),
		"items":        read.Items,
		"next_offset":  read.NextOffset,
		"exit_code":    current.ExitCode,
		"workspace_id": current.WorkspaceID,
	}, nil
}

type WriteStdinTool struct {
	Sessions *core.SessionManager
}

func NewWriteStdinTool(sessions *core.SessionManager) core.Tool {
	return WriteStdinTool{Sessions: sessions}
}

func (t WriteStdinTool) Name() string { return "write_stdin" }

func (t WriteStdinTool) Description() string {
	return "Write stdin to an existing session and optionally wait for more output"
}

func (t WriteStdinTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id":        map[string]any{"type": "string"},
			"chars":             map[string]any{"type": "string"},
			"yield_time_ms":     map[string]any{"type": "integer"},
			"max_output_tokens": map[string]any{"type": "integer"},
		},
		"required": []string{"session_id"},
	}
}

func (t WriteStdinTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	_ = ctx
	_ = ic
	sessionID, _ := args["session_id"].(string)
	if sessionID == "" {
		return nil, fmt.Errorf("session_id required")
	}
	chars, _ := args["chars"].(string)
	yield := intFromAny(args["yield_time_ms"])
	maxItems := intFromAny(args["max_output_tokens"])
	r := SessionRunner{Sessions: t.Sessions}
	session, read, err := r.WriteStdin(sessionID, chars, time.Duration(yield)*time.Millisecond, maxItems)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"session_id":  session.ID,
		"status":      session.Status,
		"running":     session.Status == core.SessionStarting || session.Status == core.SessionRunning,
		"output":      joinSessionOutput(read.Items),
		"items":       read.Items,
		"next_offset": read.NextOffset,
		"exit_code":   session.ExitCode,
	}, nil
}

type ReadThreadTerminalTool struct {
	Sessions *core.SessionManager
}

func NewReadThreadTerminalTool(sessions *core.SessionManager) core.Tool {
	return ReadThreadTerminalTool{Sessions: sessions}
}

func (t ReadThreadTerminalTool) Name() string { return "read_thread_terminal" }

func (t ReadThreadTerminalTool) Description() string {
	return "Read buffered output from an existing session without writing stdin"
}

func (t ReadThreadTerminalTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id":        map[string]any{"type": "string"},
			"max_output_tokens": map[string]any{"type": "integer"},
		},
		"required": []string{"session_id"},
	}
}

func (t ReadThreadTerminalTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	_ = ctx
	_ = ic
	sessionID, _ := args["session_id"].(string)
	if sessionID == "" {
		return nil, fmt.Errorf("session_id required")
	}
	maxItems := intFromAny(args["max_output_tokens"])
	r := SessionRunner{Sessions: t.Sessions}
	session, read, err := r.ReadSession(sessionID, maxItems)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"session_id":  session.ID,
		"status":      session.Status,
		"running":     session.Status == core.SessionStarting || session.Status == core.SessionRunning,
		"output":      joinSessionOutput(read.Items),
		"items":       read.Items,
		"next_offset": read.NextOffset,
		"exit_code":   session.ExitCode,
	}, nil
}

func joinSessionOutput(items []core.SessionOutput) string {
	if len(items) == 0 {
		return ""
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, item.Data)
	}
	return strings.Join(lines, "\n")
}
