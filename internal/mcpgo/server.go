package mcpgo

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"localgate/internal/app"
	toolcap "localgate/internal/capabilities/toolaccess"
	"localgate/internal/core"
	"localgate/internal/invocation"
	"localgate/internal/toolargs"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Server adapts LocalGate's app/registry/tasks to a standard MCP server (mcp-go).
type Server struct {
	App       *app.App
	transport string
	stateless bool
	impl      *server.MCPServer
}

// New constructs an MCP server and registers tools from the LocalGate registry.
func New(a *app.App, name, version string) (*Server, error) {
	s := server.NewMCPServer(
		name,
		version,
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	)

	wrapped := &Server{App: a, transport: invocation.TransportMCPStdio, impl: s}
	wrapped.registerCoreTools()
	return wrapped, nil
}

// ServeStdio starts the stdio transport.
func (s *Server) ServeStdio() error {
	return server.ServeStdio(s.impl)
}

// registerCoreTools bridges LocalGate's tools/manager into mcp-go tools.
func (s *Server) registerCoreTools() {
	reg := s.App.Registry
	if reg == nil {
		return
	}
	toolMode := s.App.ToolHTTPMode.Enabled
	// Keep the MCP surface intentionally small. The Registry remains the complete
	// LocalGate capability catalog; non-core business tools are discovered through
	// startup/skills and invoked through invoke_tool.
	for _, name := range reg.ListVisible() {
		if toolMode {
			if !isBootstrapTool(name) {
				continue
			}
		} else if !toolcap.IsDirectMCPTool(name) {
			continue
		}
		if t, ok := reg.GetVisible(name); ok {
			mcpTool := newToolWithRawSchema(
				t.Name(),
				t.Description(),
				mcpExposedSchema(t.Schema(), s.App.ToolArguments.EnableBase64 && !isMCPArgumentEncodingExempt(name), name != "get_startup_instructions" && name != "get_skill"),
				core.ToolMetadata(t),
			)
			applyCoreToolExtensions(&mcpTool, t)
			s.impl.AddTool(mcpTool, s.makeInvokeHandler(name))
		}
	}
	if toolMode {
		return
	}

	s.impl.AddTool(newToolWithRawSchema(
		toolcap.InvokeToolName,
		"Invoke one available LocalGate Registry tool by name. Use get_skill for workflow guidance and get_tool_schema for the target tool's exact input schema before non-trivial calls.",
		mcpExposedSchema(invokeToolSchema(), s.App.ToolArguments.EnableBase64, false),
		nil,
	), s.handleInvokeTool)
}

func isMCPArgumentEncodingExempt(name string) bool {
	switch name {
	case "upload_file", "get_startup_instructions", "get_skill", "get_tool_schema":
		return true
	default:
		return false
	}
}

func invokeToolSchema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"tool": map[string]any{
				"type":        "string",
				"description": "Registered LocalGate business tool name returned by startup guidance or a built-in skill.",
			},
			"arguments": map[string]any{
				"type":                 "object",
				"additionalProperties": true,
				"description":          "Arguments for the target tool. Use get_tool_schema for the exact schema.",
			},
			"async": map[string]any{
				"type":        "boolean",
				"description": "Submit the target tool task and return immediately.",
			},
		},
		"required": []string{"tool"},
	}
}

func isBootstrapTool(name string) bool {
	return toolcap.IsBootstrapTool(name)
}

func (s *Server) makeInvokeHandler(toolName string) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		decodedArgs, err := s.decodeToolArguments(req.GetRawArguments())
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		args, async := splitAsyncArg(decodedArgs)
		return s.submitRegistryTool(ctx, toolName, args, async)
	}
}

func (s *Server) handleInvokeTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	decoded, err := s.decodeToolArguments(req.GetRawArguments())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	toolName, _ := decoded["tool"].(string)
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return mcp.NewToolResultError("tool required"), nil
	}
	if isBootstrapTool(toolName) {
		return mcp.NewToolResultError("bootstrap tools cannot be invoked through invoke_tool"), nil
	}
	reg := s.App.Registry
	if reg == nil {
		return mcp.NewToolResultError("tool registry is not available"), nil
	}
	if _, ok := reg.Get(toolName); !ok {
		return mcp.NewToolResultError("tool not found"), nil
	}
	args := map[string]any{}
	if rawArgs, ok := decoded["arguments"]; ok && rawArgs != nil {
		var valid bool
		args, valid = rawArgs.(map[string]any)
		if !valid {
			return mcp.NewToolResultError("arguments must be an object"), nil
		}
	}
	async, _ := decoded["async"].(bool)
	return s.submitRegistryTool(ctx, toolName, args, async)
}

func (s *Server) submitRegistryTool(ctx context.Context, toolName string, args map[string]any, async bool) (*mcp.CallToolResult, error) {
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

func (s *Server) invocationMeta(ctx context.Context) invocation.Meta {
	meta := invocation.Meta{
		Transport: s.transport,
		Requester: "mcp",
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

// collectStreams mirrors internal/mcp/stdio.go behavior to provide a quick snapshot.
func collectStreams(sink *core.EventSink, taskID string, maxLines int) (string, string) {
	evs, _ := sink.List(taskID, 0)
	var stdout strings.Builder
	var stderr strings.Builder
	stdoutEvents := 0
	stderrEvents := 0
	for _, ev := range evs {
		if ev.Type == core.EventTaskStdout {
			appendEventOutput(&stdout, ev)
			stdoutEvents++
		} else if ev.Type == core.EventTaskStderr {
			appendEventOutput(&stderr, ev)
			stderrEvents++
		}
		if maxLines > 0 && (stdoutEvents+stderrEvents) >= maxLines {
			break
		}
	}
	return strings.TrimSuffix(stdout.String(), "\n"), strings.TrimSuffix(stderr.String(), "\n")
}

func appendEventOutput(out *strings.Builder, ev core.Event) {
	out.WriteString(ev.Data)
	if !ev.Continues {
		out.WriteByte('\n')
	}
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
	if s == nil || s.App == nil || task == nil || task.Result == nil {
		return result
	}
	reg := s.App.Registry
	if reg == nil {
		return result
	}
	tool, ok := reg.Get(task.Tool)
	if !ok {
		return result
	}
	if !isError {
		for _, link := range core.ToolResourceLinks(tool, task.Result) {
			result.Content = append(result.Content, mcp.ResourceLink{
				Type:        "resource_link",
				URI:         link.URI,
				Name:        link.Name,
				Description: link.Description,
				MIMEType:    link.MIMEType,
			})
		}
	}
	for _, block := range core.ToolNativeContent(tool, task.Result) {
		content, err := mcp.ParseContent(block)
		if err != nil {
			continue
		}
		result.Content = append(result.Content, content)
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
