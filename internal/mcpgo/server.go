package mcpgo

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"localgate/internal/app"
	"localgate/internal/core"
	"localgate/internal/invocation"
	"localgate/internal/toolargs"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Server adapts LocalGate's app/registry/tasks to a standard MCP server (mcp-go).
type Server struct {
	App       *app.App
	WSID      string
	transport string
	stateless bool
	impl      *server.MCPServer
}

// New constructs an MCP server and registers tools from the default workspace registry.
func New(a *app.App, name, version, wsid string) (*Server, error) {
	if wsid == "" {
		wsid = a.DefaultWSID
	}
	s := server.NewMCPServer(
		name,
		version,
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	)

	wrapped := &Server{App: a, WSID: wsid, transport: invocation.TransportMCPStdio, impl: s}
	wrapped.registerCoreTools()
	return wrapped, nil
}

// ServeStdio starts the stdio transport.
func (s *Server) ServeStdio() error {
	return server.ServeStdio(s.impl)
}

// registerCoreTools bridges LocalGate's tools/manager into mcp-go tools.
func (s *Server) registerCoreTools() {
	reg, ok := s.App.Registries[s.WSID]
	if !ok {
		return
	}
	toolMode := s.App.ToolHTTPMode.Enabled
	// Bridge all registered tools
	for _, name := range reg.ListVisible() {
		if toolMode && !isBootstrapTool(name) {
			continue
		}
		if t, ok := reg.GetVisible(name); ok {
			mcpTool := newToolWithRawSchema(
				t.Name(),
				t.Description(),
				mcpExposedSchema(t.Schema(), s.App.ToolArguments.EnableBase64 && t.Name() != "upload_file" && t.Name() != "get_startup_instructions" && t.Name() != "get_skill" && t.Name() != "get_tool_schema", t.Name() != "get_startup_instructions" && t.Name() != "get_skill"),
				core.ToolMetadata(t),
			)
			applyCoreToolExtensions(&mcpTool, t)
			s.impl.AddTool(mcpTool, s.makeInvokeHandler(name))
		}
	}
	if toolMode {
		return
	}
	s.registerRelayTools()

	// Admin tools
	s.impl.AddTool(newToolWithRawSchema("task_status", "Get the current status and metadata of a submitted task by task_id", mcpExposedSchema(map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"task_id": map[string]any{"type": "string", "description": "Task id"},
		},
		"required": []string{"task_id"},
	}, s.App.ToolArguments.EnableBase64, false), nil), s.handleTaskStatus)

	s.impl.AddTool(newToolWithRawSchema("task_logs", "Read task events and streamed output from a cursor position", mcpExposedSchema(map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"task_id": map[string]any{"type": "string", "description": "Task id"},
			"cursor":  map[string]any{"type": "integer", "description": "Cursor offset (default 0)"},
		},
		"required": []string{"task_id"},
	}, s.App.ToolArguments.EnableBase64, false), nil), s.handleTaskLogs)

	s.impl.AddTool(newToolWithRawSchema("task_cancel", "Request cancellation of a running task by task_id", mcpExposedSchema(map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"task_id": map[string]any{"type": "string", "description": "Task id"},
		},
		"required": []string{"task_id"},
	}, s.App.ToolArguments.EnableBase64, false), nil), s.handleTaskCancel)

	s.impl.AddTool(newToolWithRawSchema("task_result", "Get the final structured result and captured output of a task by task_id", mcpExposedSchema(map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"task_id": map[string]any{"type": "string", "description": "Task id"},
		},
		"required": []string{"task_id"},
	}, s.App.ToolArguments.EnableBase64, false), nil), s.handleTaskResult)

	s.impl.AddTool(newToolWithRawSchema("wait_task_finish", "Block until a task reaches a terminal state without returning the task result", mcpExposedSchema(map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"task_id":    map[string]any{"type": "string", "description": "Task id"},
			"timeout_ms": map[string]any{"type": "integer", "description": "Max wait time in milliseconds (default 60000)"},
		},
		"required": []string{"task_id"},
	}, s.App.ToolArguments.EnableBase64, false), nil), s.handleWaitTaskFinish)
}

func isBootstrapTool(name string) bool {
	switch name {
	case "get_startup_instructions", "get_skill", "get_temporary_access_token", "get_tool_schema":
		return true
	default:
		return false
	}
}

func (s *Server) registerRelayTools() {
	for serverName, relay := range s.App.Relays {
		if relay == nil {
			continue
		}
		for _, tool := range relay.Tools() {
			name := serverName + "__" + tool.Name
			s.impl.AddTool(
				newToolWithRawSchema(name, tool.Description, mcpExposedSchema(tool.InputSchema, s.App.ToolArguments.EnableBase64, false), nil),
				s.makeRelayInvokeHandler(relay),
			)
		}
	}
}

func (s *Server) makeInvokeHandler(toolName string) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		decodedArgs, err := s.decodeToolArguments(req.GetRawArguments())
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		args, async := splitAsyncArg(decodedArgs)
		meta := s.invocationMeta(ctx)
		submission, submitErr := s.App.SubmitToolTaskWithMeta(meta, toolName, args)
		if submitErr != nil {
			return mcp.NewToolResultError(submitErr.Error()), nil
		}
		if submission.Approval != nil && !async {
			if _, resolved, waitErr := s.App.WaitForApprovalDecision(ctx, submission); waitErr != nil {
				return mcp.NewToolResultError(waitErr.Error()), nil
			} else if resolved {
				// Continue below and return the result of the same gated task.
			} else {
				return newStructuredToolResult(map[string]any{
					"status":       "pending_approval",
					"approval_id":  submission.Approval.ID,
					"approval_url": submission.Approval.ApprovalURL,
					"task_id":      submission.Approval.TaskID,
					"expires_at":   submission.Approval.ExpiresAt,
					"summary":      submission.Approval.Summary,
					"risk_level":   submission.Approval.RiskLevel,
					"tool":         toolName,
					"workspace_id": s.WSID,
				}, false), nil
			}
		}
		if submission.Approval != nil && async {
			return newStructuredToolResult(map[string]any{
				"status":       "pending_approval",
				"approval_id":  submission.Approval.ID,
				"approval_url": submission.Approval.ApprovalURL,
				"task_id":      submission.Approval.TaskID,
				"expires_at":   submission.Approval.ExpiresAt,
				"summary":      submission.Approval.Summary,
				"risk_level":   submission.Approval.RiskLevel,
				"tool":         toolName,
				"workspace_id": s.WSID,
			}, false), nil
		}
		task := submission.Task
		if task == nil {
			return mcp.NewToolResultError("tool task was not created"), nil
		}

		if async {
			payload := map[string]any{
				"task_id": task.ID,
				"status":  task.Status,
				"async":   true,
			}
			return newStructuredToolResult(payload, false), nil
		}

		t := waitTaskSnapshot(s.App.Tasks, task.ID, 5*time.Second)
		payload := buildTaskPayload(t)
		appendTaskStreams(payload, s.App.Sink, task.ID, isTerminalStatus(t.Status))
		return s.newTaskToolResult(payload, t, isErrorStatus(t.Status)), nil
	}
}

func (s *Server) invocationMeta(ctx context.Context) invocation.Meta {
	meta := invocation.Meta{
		Transport:   s.transport,
		Requester:   "mcp",
		WorkspaceID: s.WSID,
	}
	if session := server.ClientSessionFromContext(ctx); session != nil {
		meta.MCPSessionID = strings.TrimSpace(session.SessionID())
	}
	if s.transport == invocation.TransportMCPHTTP {
		if s.stateless {
			meta.SessionMode = invocation.SessionModeStateless
			if meta.MCPSessionID != "" {
				meta.SessionIDSource = invocation.SessionSourceClientSupplied
			}
		} else {
			meta.SessionMode = invocation.SessionModeStateful
			if meta.MCPSessionID != "" {
				meta.SessionIDSource = invocation.SessionSourceServerManaged
			}
		}
	} else if meta.MCPSessionID != "" {
		meta.SessionMode = invocation.SessionModeStateful
		meta.SessionIDSource = invocation.SessionSourceServerManaged
	}
	return meta.Normalize()
}

func (s *Server) makeRelayInvokeHandler(relay interface {
	CallTool(context.Context, string, map[string]any) (*mcp.CallToolResult, error)
}) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := s.decodeToolArguments(req.GetRawArguments())
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		name := req.Params.Name
		parts := strings.SplitN(name, "__", 2)
		if len(parts) != 2 || parts[1] == "" {
			return mcp.NewToolResultError("invalid relay tool name"), nil
		}
		result, err := relay.CallTool(ctx, parts[1], args)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return result, nil
	}
}

func (s *Server) handleTaskStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, err := s.decodeToolArguments(req.GetRawArguments())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	id, _ := args["task_id"].(string)
	if id == "" {
		return mcp.NewToolResultError("task_id required"), nil
	}
	t, err := s.App.Tasks.Get(id)
	if err != nil {
		return mcp.NewToolResultError("task not found"), nil
	}
	b, _ := json.Marshal(t)
	return mcp.NewToolResultText(string(b)), nil
}

func (s *Server) handleTaskLogs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, err := s.decodeToolArguments(req.GetRawArguments())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	id, _ := args["task_id"].(string)
	cursor := toInt(args["cursor"])
	if id == "" {
		return mcp.NewToolResultError("task_id required"), nil
	}
	evs, next := s.App.Sink.List(id, cursor)
	payload := map[string]any{
		"cursor": next,
		"events": evs,
	}
	b, _ := json.Marshal(payload)
	return mcp.NewToolResultText(string(b)), nil
}

func (s *Server) handleTaskCancel(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, err := s.decodeToolArguments(req.GetRawArguments())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	id, _ := args["task_id"].(string)
	if id == "" {
		return mcp.NewToolResultError("task_id required"), nil
	}
	if err := s.App.Tasks.Cancel(id); err != nil {
		return mcp.NewToolResultError("task not found"), nil
	}
	return mcp.NewToolResultText(`{"ok":true}`), nil
}

func (s *Server) handleTaskResult(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, err := s.decodeToolArguments(req.GetRawArguments())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	id, _ := args["task_id"].(string)
	if id == "" {
		return mcp.NewToolResultError("task_id required"), nil
	}
	t, err := s.App.Tasks.Get(id)
	if err != nil {
		return mcp.NewToolResultError("task not found"), nil
	}
	payload := buildTaskPayload(t)
	payload["ready"] = isTerminalStatus(t.Status)
	appendTaskStreams(payload, s.App.Sink, id, true)
	return s.newTaskToolResult(payload, t, isErrorStatus(t.Status)), nil
}

func (s *Server) handleWaitTaskFinish(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, err := s.decodeToolArguments(req.GetRawArguments())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	id, _ := args["task_id"].(string)
	if id == "" {
		return mcp.NewToolResultError("task_id required"), nil
	}
	timeoutMs := toFloat64(args["timeout_ms"], 60000)
	if timeoutMs <= 0 {
		timeoutMs = 60000
	}
	t := waitTaskSnapshot(s.App.Tasks, id, time.Duration(timeoutMs)*time.Millisecond)
	payload := map[string]any{
		"task_id": t.ID,
		"status":  t.Status,
		"ready":   isTerminalStatus(t.Status),
	}
	if t.ExitCode != 0 {
		payload["exit_code"] = t.ExitCode
	}
	if t.ErrorCode != "" {
		payload["error_code"] = t.ErrorCode
	}
	if t.ErrorMsg != "" {
		payload["error_msg"] = t.ErrorMsg
	}
	return newStructuredToolResult(payload, false), nil
}

// collectStreams mirrors internal/mcp/stdio.go behavior to provide a quick snapshot.
func collectStreams(sink *core.EventSink, taskID string, maxLines int) (string, string) {
	evs, _ := sink.List(taskID, 0)
	stdout := []string{}
	stderr := []string{}
	for _, ev := range evs {
		if ev.Type == core.EventTaskStdout {
			stdout = append(stdout, ev.Data)
		} else if ev.Type == core.EventTaskStderr {
			stderr = append(stderr, ev.Data)
		}
		if maxLines > 0 && (len(stdout)+len(stderr)) >= maxLines {
			break
		}
	}
	return strings.Join(stdout, "\n"), strings.Join(stderr, "\n")
}

func appendTaskStreams(payload map[string]any, sink *core.EventSink, taskID string, include bool) {
	if !include || payload == nil || sink == nil || taskID == "" {
		return
	}
	stdout, stderr := collectStreams(sink, taskID, 2000)
	if stdout != "" {
		payload["stdout"] = stdout
	}
	if stderr != "" {
		payload["stderr"] = stderr
	}
}

func (s *Server) decodeToolArguments(raw any) (map[string]any, error) {
	return toolargs.Decode(raw, s.App.ToolArguments.EnableBase64)
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		i, err := strconv.Atoi(n)
		if err == nil {
			return i
		}
	case json.Number:
		i, err := n.Int64()
		if err == nil {
			return int(i)
		}
	}
	return 0
}

func toFloat64(v any, fallback float64) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		f, err := strconv.ParseFloat(n, 64)
		if err == nil {
			return f
		}
	case json.Number:
		f, err := n.Float64()
		if err == nil {
			return f
		}
	}
	return fallback
}

func splitAsyncArg(args map[string]any) (map[string]any, bool) {
	if len(args) == 0 {
		return map[string]any{}, false
	}
	out := make(map[string]any, len(args))
	async := false
	for k, v := range args {
		if k == "async" {
			b, ok := v.(bool)
			if ok {
				async = b
			}
			continue
		}
		out[k] = v
	}
	return out, async
}

func waitTaskSnapshot(tasks *core.Manager, taskID string, timeout time.Duration) *core.Task {
	deadline := time.Now().Add(timeout)
	last := &core.Task{ID: taskID, Status: core.StatusQueued}
	for {
		if t, err := tasks.Get(taskID); err == nil {
			last = t
			if isTerminalStatus(t.Status) || time.Now().After(deadline) {
				return t
			}
		} else if time.Now().After(deadline) {
			return last
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func buildTaskPayload(t *core.Task) map[string]any {
	payload := map[string]any{
		"task_id": t.ID,
		"status":  t.Status,
	}
	if t.Result != nil {
		payload["result"] = t.Result
	}
	if t.ExitCode != 0 {
		payload["exit_code"] = t.ExitCode
	}
	if t.ErrorCode != "" {
		payload["error_code"] = t.ErrorCode
	}
	if t.ErrorMsg != "" {
		payload["error_msg"] = t.ErrorMsg
	}
	return payload
}

func newStructuredToolResult(payload map[string]any, isError bool) *mcp.CallToolResult {
	b, _ := json.Marshal(payload)
	r := mcp.NewToolResultText(string(b))
	r.StructuredContent = payload
	r.IsError = isError
	return r
}

func (s *Server) newTaskToolResult(payload map[string]any, task *core.Task, isError bool) *mcp.CallToolResult {
	result := newStructuredToolResult(payload, isError)
	if s == nil || s.App == nil || task == nil || task.Result == nil || isError {
		return result
	}
	wsid := strings.TrimSpace(task.WorkspaceID)
	if wsid == "" {
		wsid = s.App.DefaultWSID
	}
	reg, ok := s.App.Registries[wsid]
	if !ok {
		return result
	}
	tool, ok := reg.Get(task.Tool)
	if !ok {
		return result
	}
	for _, link := range core.ToolResourceLinks(tool, task.Result) {
		result.Content = append(result.Content, mcp.ResourceLink{
			Type:        "resource_link",
			URI:         link.URI,
			Name:        link.Name,
			Description: link.Description,
			MIMEType:    link.MIMEType,
		})
	}
	return result
}

func exitCodeFromResult(res map[string]any) int {
	if res == nil {
		return 0
	}
	if v, ok := res["exit_code"].(int); ok {
		return v
	}
	if v, ok := res["exit_code"].(int64); ok {
		return int(v)
	}
	if v, ok := res["exit_code"].(float64); ok {
		return int(v)
	}
	return 0
}

func isTerminalStatus(status core.Status) bool {
	return status == core.StatusSucceeded || status == core.StatusFailed || status == core.StatusCanceled || status == core.StatusTimeout
}

func isErrorStatus(status core.Status) bool {
	return status == core.StatusFailed || status == core.StatusCanceled || status == core.StatusTimeout
}
