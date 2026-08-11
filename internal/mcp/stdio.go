package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"

	"localgate/internal/app"
	"localgate/internal/core"
	"localgate/internal/invocation"
	"localgate/internal/toolargs"
)

type Server struct {
	App     *app.App
	Name    string
	Version string
	WSID    string
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (s *Server) Serve(in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	bw := bufio.NewWriter(out)
	defer bw.Flush()

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		if len(req.ID) == 0 {
			s.handleNotification(req)
			continue
		}
		resp := s.handleRequest(req)
		b, _ := json.Marshal(resp)
		_, _ = bw.Write(b)
		_, _ = bw.Write([]byte("\n"))
		_ = bw.Flush()
	}
	return sc.Err()
}

func (s *Server) handleNotification(req request) {
	_ = req
}

func (s *Server) handleRequest(req request) response {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "ping":
		return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	default:
		return response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32601, Message: "method not found"}}
	}
}

func (s *Server) handleInitialize(req request) response {
	proto := "2025-11-25"
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(req.Params, &params)
	if params.ProtocolVersion == "" {
		params.ProtocolVersion = proto
	}
	if params.ProtocolVersion != proto {
		params.ProtocolVersion = proto
	}
	result := map[string]any{
		"protocolVersion": params.ProtocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{
				"listChanged": false,
			},
		},
		"serverInfo": map[string]any{
			"name":    s.Name,
			"version": s.Version,
		},
	}
	return response{JSONRPC: "2.0", ID: req.ID, Result: result}
}

func (s *Server) handleToolsList(req request) response {
	wsid := s.WSID
	if wsid == "" {
		wsid = s.App.DefaultWSID
	}
	reg, ok := s.App.Registries[wsid]
	if !ok {
		return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": []any{}}}
	}
	tools := []map[string]any{}
	for _, name := range reg.ListVisible() {
		if s.App.ToolHTTPMode.Enabled && !isBootstrapTool(name) {
			continue
		}
		if t, ok := reg.GetVisible(name); ok {
			item := map[string]any{
				"name":        name,
				"description": t.Description(),
				"inputSchema": toolargs.MCPInputSchema(t.Schema(), s.App.ToolArguments.EnableBase64 && name != "upload_file" && name != "get_startup_instructions" && name != "get_skill" && name != "get_tool_schema"),
			}
			if meta := core.ToolMetadata(t); len(meta) > 0 {
				item["_meta"] = meta
			}
			if outputSchema := core.ToolOutputSchema(t); outputSchema != nil {
				item["outputSchema"] = outputSchema
			}
			if annotations := core.ToolAnnotations(t); len(annotations) > 0 {
				item["annotations"] = annotations
			}
			tools = append(tools, item)
		}
	}
	if !s.App.ToolHTTPMode.Enabled {
		for _, name := range []string{"task_status", "task_logs", "task_cancel", "task_result", "wait_task_finish"} {
			tools = append(tools, map[string]any{
				"name":        name,
				"description": mcpAdminDescription(name),
				"inputSchema": toolargs.MCPInputSchema(mcpAdminSchema(name), s.App.ToolArguments.EnableBase64),
			})
		}
	}
	return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": tools}}
}

func (s *Server) handleToolsCall(req request) response {
	var params struct {
		Name      string `json:"name"`
		Arguments any    `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32602, Message: "invalid params"}}
	}
	args, err := toolargs.Decode(params.Arguments, s.App.ToolArguments.EnableBase64)
	if err != nil {
		return response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32602, Message: err.Error()}}
	}
	wsid := s.WSID
	if wsid == "" {
		wsid = s.App.DefaultWSID
	}
	if s.App.ToolHTTPMode.Enabled && !isBootstrapTool(params.Name) {
		return response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32601, Message: "tool not found"}}
	}
	if !s.App.ToolHTTPMode.Enabled {
		switch params.Name {
		case "task_status":
			return s.callTaskStatus(req.ID, args)
		case "task_logs":
			return s.callTaskLogs(req.ID, args)
		case "task_cancel":
			return s.callTaskCancel(req.ID, args)
		case "task_result":
			return s.callTaskResult(req.ID, args)
		case "wait_task_finish":
			return s.callWaitTaskFinish(req.ID, args)
		case "exec_command", "write_stdin", "read_thread_terminal":
			if !s.App.IsToolVisible(wsid, params.Name) {
				return response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32601, Message: "tool not found"}}
			}
			return s.callSessionTool(req.ID, params.Name, args)
		}
	}

	if !s.App.IsToolVisible(wsid, params.Name) {
		return response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32601, Message: "tool not found"}}
	}
	submission, err := s.App.SubmitToolTaskWithMeta(invocation.Meta{
		Transport: invocation.TransportMCPStdio, Requester: "mcp", WorkspaceID: wsid,
	}, params.Name, args)
	if err != nil {
		return response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32602, Message: err.Error()}}
	}
	payload := map[string]any{
		"status": "pending_approval",
	}
	isErr := false
	var completedTask *core.Task
	if submission.Approval != nil {
		payload["approval_id"] = submission.Approval.ID
		payload["approval_url"] = submission.Approval.ApprovalURL
		payload["task_id"] = submission.Approval.TaskID
		payload["expires_at"] = submission.Approval.ExpiresAt
		payload["summary"] = submission.Approval.Summary
		payload["risk_level"] = submission.Approval.RiskLevel
	} else {
		status := submission.Task.Status
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			if t, e := s.App.Tasks.Get(submission.Task.ID); e == nil {
				status = t.Status
				if status == core.StatusSucceeded || status == core.StatusFailed || status == core.StatusCanceled || status == core.StatusTimeout {
					payload["task"] = t
					completedTask = t
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		payload["task_id"] = submission.Task.ID
		payload["status"] = status
		stdout, stderr := collectStreams(s.App.Sink, submission.Task.ID, 2000)
		payload["stdout"] = stdout
		payload["stderr"] = stderr
		if status != core.StatusSucceeded {
			isErr = true
		}
	}
	b, _ := json.Marshal(payload)
	content := []map[string]any{{"type": "text", "text": string(b)}}
	if !isErr && completedTask != nil {
		content = append(content, s.resourceLinkContent(wsid, params.Name, completedTask.Result)...)
	}
	return response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"content":           content,
			"structuredContent": payload,
			"isError":           isErr,
		},
	}
}

func (s *Server) resourceLinkContent(wsid, toolName string, result map[string]any) []map[string]any {
	if s == nil || s.App == nil || result == nil {
		return nil
	}
	reg, ok := s.App.Registries[wsid]
	if !ok {
		return nil
	}
	tool, ok := reg.Get(toolName)
	if !ok {
		return nil
	}
	links := core.ToolResourceLinks(tool, result)
	out := make([]map[string]any, 0, len(links))
	for _, link := range links {
		item := map[string]any{
			"type":        "resource_link",
			"uri":         link.URI,
			"name":        link.Name,
			"description": link.Description,
			"mimeType":    link.MIMEType,
		}
		if link.Size >= 0 {
			item["size"] = link.Size
		}
		out = append(out, item)
	}
	return out
}

func isBootstrapTool(name string) bool {
	switch name {
	case "get_startup_instructions", "get_skill", "get_temporary_access_token", "get_tool_schema":
		return true
	default:
		return false
	}
}

func (s *Server) callSessionTool(id json.RawMessage, name string, args map[string]any) response {
	wsid := s.WSID
	if wsid == "" {
		wsid = s.App.DefaultWSID
	}
	if name == "exec_command" {
		submission, err := s.App.SubmitToolTaskWithMeta(invocation.Meta{
			Transport: invocation.TransportMCPStdio, Requester: "mcp", WorkspaceID: wsid,
		}, name, args)
		if err != nil {
			return response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32602, Message: err.Error()}}
		}
		if submission.Approval != nil {
			payload := map[string]any{
				"status":       "pending_approval",
				"approval_id":  submission.Approval.ID,
				"approval_url": submission.Approval.ApprovalURL,
				"task_id":      submission.Approval.TaskID,
				"expires_at":   submission.Approval.ExpiresAt,
				"summary":      submission.Approval.Summary,
				"risk_level":   submission.Approval.RiskLevel,
			}
			b, _ := json.Marshal(payload)
			return response{JSONRPC: "2.0", ID: id, Result: map[string]any{
				"content":           []map[string]any{{"type": "text", "text": string(b)}},
				"structuredContent": payload,
				"isError":           false,
			}}
		}
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			task, getErr := s.App.Tasks.Get(submission.Task.ID)
			if getErr == nil && isTerminalStatus(task.Status) {
				if task.Status != core.StatusSucceeded {
					return response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32602, Message: task.ErrorMsg}}
				}
				res := task.Result
				b, _ := json.Marshal(res)
				return response{JSONRPC: "2.0", ID: id, Result: map[string]any{
					"content":           []map[string]any{{"type": "text", "text": string(b)}},
					"structuredContent": res,
					"isError":           false,
				}}
			}
			time.Sleep(50 * time.Millisecond)
		}
		return response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32603, Message: "session creation timed out"}}
	}
	res, err := s.App.InvokeTool(context.Background(), wsid, name, args)
	if err != nil {
		return response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32602, Message: err.Error()}}
	}
	b, _ := json.Marshal(res)
	return response{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]any{
			"content":           []map[string]any{{"type": "text", "text": string(b)}},
			"structuredContent": res,
			"isError":           false,
		},
	}
}

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

func mcpAdminDescription(name string) string {
	switch name {
	case "task_status":
		return "Get the current status and metadata of a submitted task by task_id"
	case "task_logs":
		return "Read task events and streamed output from a cursor position"
	case "task_cancel":
		return "Request cancellation of a running task by task_id"
	case "task_result":
		return "Get the final structured result and captured output of a task by task_id"
	case "wait_task_finish":
		return "Block until a task reaches a terminal state without returning the task result"
	default:
		return ""
	}
}

func mcpAdminSchema(name string) any {
	switch name {
	case "task_status", "task_result":
		return map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string"},
			},
			"required": []string{"task_id"},
		}
	case "task_logs":
		return map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string"},
				"cursor":  map[string]any{"type": "integer"},
			},
			"required": []string{"task_id"},
		}
	case "task_cancel":
		return map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string"},
			},
			"required": []string{"task_id"},
		}
	case "wait_task_finish":
		return map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"task_id":    map[string]any{"type": "string"},
				"timeout_ms": map[string]any{"type": "integer"},
			},
			"required": []string{"task_id"},
		}
	default:
		return map[string]any{"type": "object"}
	}
}

func (s *Server) callTaskStatus(id json.RawMessage, args map[string]any) response {
	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		return response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32602, Message: "task_id required"}}
	}
	t, err := s.App.Tasks.Get(taskID)
	if err != nil {
		return response{JSONRPC: "2.0", ID: id, Result: map[string]any{"content": []map[string]any{{"type": "text", "text": "task not found"}}, "isError": true}}
	}
	b, _ := json.Marshal(t)
	return response{JSONRPC: "2.0", ID: id, Result: map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}, "isError": false}}
}

func (s *Server) callTaskResult(id json.RawMessage, args map[string]any) response {
	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		return response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32602, Message: "task_id required"}}
	}
	t, err := s.App.Tasks.Get(taskID)
	if err != nil {
		return response{JSONRPC: "2.0", ID: id, Result: map[string]any{"content": []map[string]any{{"type": "text", "text": "task not found"}}, "isError": true}}
	}
	payload := map[string]any{
		"task_id": t.ID,
		"status":  t.Status,
		"ready":   isTerminalStatus(t.Status),
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
	b, _ := json.Marshal(payload)
	content := []map[string]any{{"type": "text", "text": string(b)}}
	if t.Status == core.StatusSucceeded && t.Result != nil {
		wsid := t.WorkspaceID
		if wsid == "" {
			wsid = s.App.DefaultWSID
		}
		content = append(content, s.resourceLinkContent(wsid, t.Tool, t.Result)...)
	}
	return response{JSONRPC: "2.0", ID: id, Result: map[string]any{
		"content":           content,
		"structuredContent": payload,
		"isError":           isErrorStatus(t.Status),
	}}
}

func (s *Server) callTaskLogs(id json.RawMessage, args map[string]any) response {
	taskID, _ := args["task_id"].(string)
	cursor := intFromAny(args["cursor"])
	if taskID == "" {
		return response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32602, Message: "task_id required"}}
	}
	evs, next := s.App.Sink.List(taskID, cursor)
	payload := map[string]any{"cursor": next, "events": evs}
	b, _ := json.Marshal(payload)
	return response{JSONRPC: "2.0", ID: id, Result: map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}, "isError": false}}
}

func (s *Server) callTaskCancel(id json.RawMessage, args map[string]any) response {
	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		return response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32602, Message: "task_id required"}}
	}
	_ = s.App.Tasks.Cancel(taskID)
	t, _ := s.App.Tasks.Get(taskID)
	b, _ := json.Marshal(t)
	return response{JSONRPC: "2.0", ID: id, Result: map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}, "isError": false}}
}

func (s *Server) callWaitTaskFinish(id json.RawMessage, args map[string]any) response {
	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		return response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32602, Message: "task_id required"}}
	}
	timeoutMs := intFromAny(args["timeout_ms"])
	if timeoutMs <= 0 {
		timeoutMs = 60000
	}
	t := waitTaskSnapshot(s.App.Tasks, taskID, time.Duration(timeoutMs)*time.Millisecond)
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
	b, _ := json.Marshal(payload)
	return response{JSONRPC: "2.0", ID: id, Result: map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}, "structuredContent": payload, "isError": false}}
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

func isTerminalStatus(status core.Status) bool {
	return status == core.StatusSucceeded || status == core.StatusFailed || status == core.StatusCanceled || status == core.StatusTimeout
}

func isErrorStatus(status core.Status) bool {
	return status == core.StatusFailed || status == core.StatusCanceled || status == core.StatusTimeout
}

func intFromAny(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		i, _ := strconv.Atoi(x)
		return i
	default:
		return 0
	}
}
