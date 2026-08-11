package shell

import (
	"context"
	"fmt"
	"strings"
	"time"

	"localgate/internal/config"
	"localgate/internal/core"
)

const (
	defaultSessionReadItems = 200
	maxSessionReadItems     = 500
	maxSessionWaitMS        = 30_000
	maxSessionInputBytes    = 64 << 10
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
	return "Start an interactive or long-running command session inside the workspace. Use run_command instead for one-shot commands that only need a final result."
}

func (t ExecCommandTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"cmd":               map[string]any{"type": "string"},
			"workdir":           map[string]any{"type": "string", "description": "Absolute path inside workspace"},
			"yield_time_ms":     map[string]any{"type": "integer", "minimum": 0, "maximum": maxSessionWaitMS},
			"max_output_items":  map[string]any{"type": "integer", "minimum": 1, "maximum": maxSessionReadItems, "description": "Maximum buffered output items returned in this response."},
			"max_output_tokens": map[string]any{"type": "integer", "minimum": 0, "description": "Deprecated compatibility alias for max_output_items; this value limits output items, not model tokens. Large legacy values are capped internally."},
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
	yield, err := sessionWaitMS(args, "yield_time_ms")
	if err != nil {
		return nil, err
	}
	maxItems, err := sessionReadItems(args)
	if err != nil {
		return nil, err
	}
	env := mapFromAny(args["env"])
	r := SessionRunner{WS: t.WS, Sessions: t.Sessions}
	session, err := r.StartSessionWithInvokeContext(ctx, ic, cmd, workdir, env, shellName, login, tty)
	if err != nil {
		return nil, err
	}
	if yield > 0 {
		timer := time.NewTimer(time.Duration(yield) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	current, read, err := r.ReadSessionForWorkspace(session.ID, t.WS.ID, 0, maxItems)
	if err != nil {
		return nil, err
	}
	return sessionReadPayload(current, read), nil
}

type WriteStdinTool struct {
	Sessions *core.SessionManager
	WSID     string
}

func NewWriteStdinTool(ws config.Workspace, sessions *core.SessionManager) core.Tool {
	return WriteStdinTool{Sessions: sessions, WSID: ws.ID}
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
			"chars":             map[string]any{"type": "string", "description": "Input to write. Empty input performs only the wait/read step. Input is limited to 64 KiB per call."},
			"yield_time_ms":     map[string]any{"type": "integer", "minimum": 0, "maximum": maxSessionWaitMS, "description": "Wait up to this many milliseconds for new output or session completion after writing."},
			"max_output_items":  map[string]any{"type": "integer", "minimum": 1, "maximum": maxSessionReadItems},
			"max_output_tokens": map[string]any{"type": "integer", "minimum": 0, "description": "Deprecated compatibility alias for max_output_items. Large legacy values are capped internally."},
			"cursor":            map[string]any{"type": "integer", "minimum": 0, "description": "Absolute output cursor returned by the previous call"},
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
	if len([]byte(chars)) > maxSessionInputBytes {
		return nil, fmt.Errorf("chars exceeds %d byte session input limit", maxSessionInputBytes)
	}
	yield, err := sessionWaitMS(args, "yield_time_ms")
	if err != nil {
		return nil, err
	}
	maxItems, err := sessionReadItems(args)
	if err != nil {
		return nil, err
	}
	cursor, err := sessionCursor(args)
	if err != nil {
		return nil, err
	}
	r := SessionRunner{WS: config.Workspace{ID: t.WSID}, Sessions: t.Sessions}
	session, read, err := r.WriteStdinForWorkspace(ctx, sessionID, t.WSID, chars, cursor, time.Duration(yield)*time.Millisecond, maxItems)
	if err != nil {
		return nil, err
	}
	return sessionReadPayload(session, read), nil
}

type ReadThreadTerminalTool struct {
	Sessions *core.SessionManager
	WSID     string
}

type CloseSessionTool struct {
	Sessions *core.SessionManager
	WSID     string
}

func NewCloseSessionTool(ws config.Workspace, sessions *core.SessionManager) core.Tool {
	return CloseSessionTool{Sessions: sessions, WSID: ws.ID}
}

func (t CloseSessionTool) Name() string { return "close_session" }

func (t CloseSessionTool) Description() string {
	return "Terminate and close an existing interactive or long-running session in the current workspace."
}

func (t CloseSessionTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
		},
		"required": []string{"session_id"},
	}
}

func (t CloseSessionTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	_ = ctx
	_ = ic
	sessionID, _ := args["session_id"].(string)
	if sessionID == "" {
		return nil, fmt.Errorf("session_id required")
	}
	session, err := t.Sessions.CloseForWorkspace(sessionID, t.WSID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"session_id":   session.ID,
		"status":       session.Status,
		"running":      false,
		"completed":    true,
		"exit_code":    session.ExitCode,
		"workspace_id": session.WorkspaceID,
	}, nil
}

func NewReadThreadTerminalTool(ws config.Workspace, sessions *core.SessionManager) core.Tool {
	return ReadThreadTerminalTool{Sessions: sessions, WSID: ws.ID}
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
			"wait_ms":           map[string]any{"type": "integer", "minimum": 0, "maximum": maxSessionWaitMS, "description": "Wait up to this many milliseconds for new output or session completion when the cursor is already caught up."},
			"max_output_items":  map[string]any{"type": "integer", "minimum": 1, "maximum": maxSessionReadItems},
			"max_output_tokens": map[string]any{"type": "integer", "minimum": 0, "description": "Deprecated compatibility alias for max_output_items. Large legacy values are capped internally."},
			"cursor":            map[string]any{"type": "integer", "minimum": 0, "description": "Absolute output cursor returned by the previous call"},
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
	maxItems, err := sessionReadItems(args)
	if err != nil {
		return nil, err
	}
	cursor, err := sessionCursor(args)
	if err != nil {
		return nil, err
	}
	wait, err := sessionWaitMS(args, "wait_ms")
	if err != nil {
		return nil, err
	}
	r := SessionRunner{WS: config.Workspace{ID: t.WSID}, Sessions: t.Sessions}
	session, read, err := r.WaitReadSessionForWorkspace(ctx, sessionID, t.WSID, cursor, maxItems, time.Duration(wait)*time.Millisecond)
	if err != nil {
		return nil, err
	}
	return sessionReadPayload(session, read), nil
}

func sessionReadPayload(session core.Session, read core.SessionReadResult) map[string]any {
	ended := session.Status == core.SessionExited || session.Status == core.SessionFailed || session.Status == core.SessionClosed
	return map[string]any{
		"session_id":       session.ID,
		"status":           session.Status,
		"running":          session.Status == core.SessionStarting || session.Status == core.SessionRunning,
		"completed":        ended && !read.HasMore,
		"output_complete":  ended && !read.HasMore,
		"output":           joinSessionOutput(read.Items),
		"items":            read.Items,
		"next_offset":      read.NextOffset,
		"base_offset":      read.BaseOffset,
		"truncated":        read.Truncated,
		"buffer_truncated": read.BufferTruncated,
		"cursor_expired":   read.CursorExpired,
		"has_more":         read.HasMore,
		"output_error":     read.OutputError,
		"exit_code":        session.ExitCode,
		"workspace_id":     session.WorkspaceID,
	}
}

func sessionReadItems(args map[string]any) (int, error) {
	value := 0
	if raw, ok := args["max_output_items"]; ok {
		value = intFromAny(raw)
	} else {
		value = intFromAny(args["max_output_tokens"])
	}
	if value < 0 {
		return 0, fmt.Errorf("max_output_items must be non-negative")
	}
	if value == 0 {
		return defaultSessionReadItems, nil
	}
	if value > maxSessionReadItems {
		return maxSessionReadItems, nil
	}
	return value, nil
}

func sessionCursor(args map[string]any) (int, error) {
	cursor := intFromAny(args["cursor"])
	if cursor < 0 {
		return 0, core.ErrSessionCursorInvalid
	}
	return cursor, nil
}

func sessionWaitMS(args map[string]any, key string) (int, error) {
	wait := intFromAny(args[key])
	if wait < 0 {
		return 0, fmt.Errorf("%s must be non-negative", key)
	}
	if wait > maxSessionWaitMS {
		return maxSessionWaitMS, nil
	}
	return wait, nil
}

func joinSessionOutput(items []core.SessionOutput) string {
	if len(items) == 0 {
		return ""
	}
	var out strings.Builder
	for i, item := range items {
		out.WriteString(item.Data)
		if !item.Continues && i < len(items)-1 {
			out.WriteByte('\n')
		}
	}
	return out.String()
}
