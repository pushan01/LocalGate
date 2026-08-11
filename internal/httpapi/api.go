package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"localgate/internal/adminapi"
	"localgate/internal/app"
	"localgate/internal/approval"
	"localgate/internal/core"
	"localgate/internal/download"
	"localgate/internal/fileexport"
	"localgate/internal/fileio"
	"localgate/internal/invocation"
	"localgate/internal/security"
	"localgate/internal/server"
	"localgate/internal/toolaccess"
	"localgate/internal/toolargs"
	"localgate/internal/upgrade"
	"localgate/internal/upload"
	adminui "localgate/web/admin"
)

const (
	upgradeTokenHeader    = "X-LocalGate-Upgrade-Token"
	upgradeUploadMaxBytes = 512 << 20
	uploadTokenHeader     = "X-LocalGate-Upload-Token"
	uploadFileMaxBytes    = 512 << 20
	downloadTokenQueryKey = "token"
)

type API struct {
	App         *app.App
	DefaultWSID string
	mcpRuntime  *mcpRuntime
}

func (a *API) Register(mux *http.ServeMux) {
	a.RegisterAdmin(mux)
	a.RegisterControl(mux)
}

func (a *API) RegisterAdmin(mux *http.ServeMux) {
	if a.App.Admin.Enabled {
		(&adminapi.API{App: a.App, DefaultWSID: a.DefaultWSID, BaseURL: a.App.Admin.BaseURL}).Register(mux)
		mux.Handle("/admin", adminui.Handler(a.App.Admin.BaseURL))
		mux.Handle("/admin/", adminui.Handler(a.App.Admin.BaseURL))
		mux.HandleFunc("/approvals", a.handleApprovalList)
		mux.HandleFunc("/approvals/", a.handleApprovalRoutes)
		mux.HandleFunc("/approval-ui", a.handleApprovalUI)
		mux.HandleFunc("/approval-ui/", a.handleApprovalUI)
		mux.HandleFunc("/deploy-mgr", a.handleDeploymentOverview)
		mux.HandleFunc("/deploy-mgr/", a.handleDeploymentOverview)
	}
}

func (a *API) RegisterControl(mux *http.ServeMux) {
	mux.HandleFunc("/tools", a.handleTools)
	mux.HandleFunc("/tools/", a.handleToolRoutes)
	mux.HandleFunc("/tasks", a.handleTasks)
	mux.HandleFunc("/tasks/", a.handleTaskRoutes)
	mux.HandleFunc("/sessions/exec", a.handleSessionExec)
	mux.HandleFunc("/sessions/", a.handleSessionRoutes)
	mux.HandleFunc("/audits", a.handleAuditQuery)
	mux.HandleFunc("/system/downloads", a.handleDownloads)
	mux.HandleFunc("/system/exports/", a.handleExports)
	mux.HandleFunc("/system/uploads", a.handleUploads)
	mux.HandleFunc("/system/upgrades", a.handleUpgrades)
	mux.HandleFunc("/system/upgrades/", a.handleUpgradeRoutes)
	a.wireMCPRuntime(mux)
}

type toolSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type toolDetail struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Schema      any    `json:"schema,omitempty"`
	Meta        any    `json:"_meta,omitempty"`
}

type createTaskRequest struct {
	Tool            string `json:"tool"`
	Args            any    `json:"args"`
	WorkspaceID     string `json:"workspace_id"`
	Requester       string `json:"requester"`
	SessionID       string `json:"session_id,omitempty"`
	SessionMode     string `json:"session_mode,omitempty"`
	SessionIDSource string `json:"session_id_source,omitempty"`
	RequestID       string `json:"request_id,omitempty"`
}

type execSessionRequest struct {
	Cmd             string         `json:"cmd"`
	Workdir         string         `json:"workdir"`
	YieldTimeMs     int            `json:"yield_time_ms"`
	MaxOutputItems  int            `json:"max_output_items"`
	MaxOutputTokens int            `json:"max_output_tokens"`
	Shell           string         `json:"shell"`
	Login           bool           `json:"login"`
	TTY             bool           `json:"tty"`
	Requester       string         `json:"requester"`
	WorkspaceID     string         `json:"workspace_id"`
	Env             map[string]any `json:"env"`
}

type stdinRequest struct {
	Chars           string `json:"chars"`
	YieldTimeMs     int    `json:"yield_time_ms"`
	MaxOutputItems  int    `json:"max_output_items"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	Cursor          int    `json:"cursor"`
}

func (a *API) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	var req createTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.WriteError(w, http.StatusBadRequest, "bad_request", "invalid json body", nil)
		return
	}
	if req.Tool == "" {
		server.WriteError(w, http.StatusBadRequest, "bad_request", "tool is required", nil)
		return
	}
	wsid := req.WorkspaceID
	if wsid == "" {
		wsid = a.DefaultWSID
	}
	reg, ok := a.App.Registries[wsid]
	if !ok {
		server.WriteError(w, http.StatusBadRequest, "workspace_not_found", "workspace not found", map[string]any{"workspace_id": wsid})
		return
	}
	args, err := toolargs.Decode(req.Args, a.App.ToolArguments.EnableBase64)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	if !reg.IsVisible(req.Tool) {
		server.WriteError(w, http.StatusNotFound, "tool_not_found", "tool not found", nil)
		return
	}
	submission, err := a.App.SubmitToolTaskWithMeta(invocation.Meta{
		Transport:       invocation.TransportREST,
		WorkspaceID:     wsid,
		Requester:       req.Requester,
		MCPSessionID:    req.SessionID,
		SessionMode:     req.SessionMode,
		SessionIDSource: req.SessionIDSource,
		RequestID:       req.RequestID,
	}, req.Tool, args)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "submit_failed", err.Error(), nil)
		return
	}
	if submission.Approval != nil {
		server.WriteJSON(w, http.StatusAccepted, map[string]any{
			"status":       "pending_approval",
			"approval_id":  submission.Approval.ID,
			"approval_url": submission.Approval.ApprovalURL,
			"task_id":      submission.Approval.TaskID,
			"expires_at":   submission.Approval.ExpiresAt,
			"summary":      submission.Approval.Summary,
			"risk_level":   submission.Approval.RiskLevel,
		})
		return
	}
	// Streamable creation: if ?stream=1, directly open SSE stream for this task
	if r.URL.Query().Get("stream") == "1" {
		// Reuse existing SSE handler
		taskID := submission.Task.ID
		// Switch method to GET to satisfy SSE handler check
		r.Method = http.MethodGet
		a.handleEventsStream(w, r, taskID)
		return
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"task_id": submission.Task.ID, "status": submission.Task.Status})
}

func (a *API) handleTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	wsid, reg, ok := a.resolveRegistry(w, r)
	if !ok {
		return
	}
	names := reg.ListVisible()
	sort.Strings(names)
	items := make([]any, 0, len(names))
	summaryOnly := r.URL.Query().Get("summary") == "1"
	for _, name := range names {
		t, ok := reg.GetVisible(name)
		if !ok {
			continue
		}
		if summaryOnly {
			items = append(items, toolSummary{Name: t.Name(), Description: t.Description()})
			continue
		}
		items = append(items, toolDetail{Name: t.Name(), Description: t.Description(), Schema: t.Schema(), Meta: core.ToolMetadata(t)})
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"workspace_id": wsid,
		"items":        items,
	})
}

func (a *API) handleSessionExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	var req execSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.WriteError(w, http.StatusBadRequest, "bad_request", "invalid json body", nil)
		return
	}
	if req.Cmd == "" {
		server.WriteError(w, http.StatusBadRequest, "bad_request", "cmd is required", nil)
		return
	}
	wsid := req.WorkspaceID
	if wsid == "" {
		wsid = a.DefaultWSID
	}
	if _, ok := a.App.Registries[wsid]; !ok {
		server.WriteError(w, http.StatusBadRequest, "workspace_not_found", "workspace not found", map[string]any{"workspace_id": wsid})
		return
	}
	args := map[string]any{
		"cmd":           req.Cmd,
		"workdir":       req.Workdir,
		"yield_time_ms": req.YieldTimeMs,
		"shell":         req.Shell,
		"login":         req.Login,
		"tty":           req.TTY,
		"env":           req.Env,
	}
	if req.MaxOutputItems > 0 {
		args["max_output_items"] = req.MaxOutputItems
	} else if req.MaxOutputTokens > 0 {
		args["max_output_tokens"] = req.MaxOutputTokens
	}
	submission, err := a.App.SubmitToolTaskWithMeta(invocation.Meta{
		Transport: invocation.TransportREST, WorkspaceID: wsid, Requester: req.Requester,
	}, "exec_command", args)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "session_exec_failed", err.Error(), nil)
		return
	}
	a.writeSubmissionResult(r.Context(), w, submission, http.StatusCreated)
}

func (a *API) handleSessionRoutes(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/sessions/")
	if p == "" {
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
	parts := strings.Split(p, "/")
	sessionID := parts[0]
	if sessionID == "" {
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
	wsid := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	if wsid == "" {
		wsid = a.DefaultWSID
	}
	if _, ok := a.App.Registries[wsid]; !ok {
		server.WriteError(w, http.StatusBadRequest, "workspace_not_found", "workspace not found", map[string]any{"workspace_id": wsid})
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		s, err := a.App.Sessions.GetForWorkspace(sessionID, wsid)
		if err != nil {
			writeSessionRouteError(w, err)
			return
		}
		server.WriteJSON(w, http.StatusOK, s)
		return
	}
	switch parts[1] {
	case "stdin":
		if r.Method != http.MethodPost {
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		var req stdinRequest
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				server.WriteError(w, http.StatusBadRequest, "bad_request", "invalid json body", nil)
				return
			}
		}
		args := map[string]any{
			"session_id":    sessionID,
			"chars":         req.Chars,
			"yield_time_ms": req.YieldTimeMs,
			"cursor":        req.Cursor,
		}
		if req.MaxOutputItems > 0 {
			args["max_output_items"] = req.MaxOutputItems
		} else if req.MaxOutputTokens > 0 {
			args["max_output_tokens"] = req.MaxOutputTokens
		}
		res, err := a.App.InvokeTool(r.Context(), wsid, "write_stdin", args)
		if err != nil {
			if errors.Is(err, core.ErrSessionNotFound) || errors.Is(err, core.ErrSessionWorkspaceMismatch) {
				writeSessionRouteError(w, err)
			} else {
				server.WriteError(w, http.StatusBadRequest, "stdin_failed", err.Error(), nil)
			}
			return
		}
		server.WriteJSON(w, http.StatusOK, res)
		return
	case "terminal":
		if r.Method != http.MethodGet {
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		maxOutput := 0
		if v := r.URL.Query().Get("max_output_items"); v != "" {
			if i, err := strconv.Atoi(v); err == nil && i > 0 {
				maxOutput = i
			}
		} else if v := r.URL.Query().Get("max_output_tokens"); v != "" {
			if i, err := strconv.Atoi(v); err == nil && i > 0 {
				maxOutput = i
			}
		}
		cursor := 0
		if v := r.URL.Query().Get("cursor"); v != "" {
			if i, err := strconv.Atoi(v); err == nil && i >= 0 {
				cursor = i
			}
		}
		waitMS := 0
		if v := r.URL.Query().Get("wait_ms"); v != "" {
			if i, err := strconv.Atoi(v); err == nil && i >= 0 {
				waitMS = i
			}
		}
		res, err := a.App.InvokeTool(r.Context(), wsid, "read_thread_terminal", map[string]any{
			"session_id":       sessionID,
			"max_output_items": maxOutput,
			"cursor":           cursor,
			"wait_ms":          waitMS,
		})
		if err != nil {
			if errors.Is(err, core.ErrSessionNotFound) || errors.Is(err, core.ErrSessionWorkspaceMismatch) {
				writeSessionRouteError(w, err)
			} else {
				server.WriteError(w, http.StatusBadRequest, "terminal_read_failed", err.Error(), nil)
			}
			return
		}
		server.WriteJSON(w, http.StatusOK, res)
		return
	case "close":
		if r.Method != http.MethodPost {
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		s, err := a.App.Sessions.CloseForWorkspace(sessionID, wsid)
		if err != nil {
			writeSessionRouteError(w, err)
			return
		}
		server.WriteJSON(w, http.StatusAccepted, s)
		return
	default:
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
}

func writeSessionRouteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, core.ErrSessionNotFound):
		server.WriteError(w, http.StatusNotFound, "session_not_found", "session not found", nil)
	case errors.Is(err, core.ErrSessionWorkspaceMismatch):
		server.WriteError(w, http.StatusForbidden, "session_workspace_mismatch", "session does not belong to the requested workspace", nil)
	default:
		server.WriteError(w, http.StatusBadRequest, "session_operation_failed", err.Error(), nil)
	}
}

func (a *API) handleToolRoutes(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/tools/")
	if name == "" {
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
	if strings.HasSuffix(name, "/call") {
		a.handleToolCall(w, r, strings.TrimSuffix(name, "/call"))
		return
	}
	if r.Method != http.MethodGet {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if strings.Contains(name, "/") {
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
	wsid, reg, ok := a.resolveRegistry(w, r)
	if !ok {
		return
	}
	t, ok := reg.GetVisible(name)
	if !ok {
		server.WriteError(w, http.StatusNotFound, "tool_not_found", "tool not found", nil)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"workspace_id": wsid,
		"name":         t.Name(),
		"description":  t.Description(),
		"schema":       t.Schema(),
		"_meta":        core.ToolMetadata(t),
	})
}

func (a *API) handleToolCall(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !a.App.ToolHTTPMode.Enabled {
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
	if name == "" || strings.Contains(name, "/") {
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
	grant, ok := a.authorizeToolRequest(w, r)
	if !ok {
		return
	}
	reg, ok := a.App.Registries[grant.WorkspaceID]
	if !ok {
		server.WriteError(w, http.StatusBadRequest, "workspace_not_found", "workspace not found", map[string]any{"workspace_id": grant.WorkspaceID})
		return
	}
	if !reg.IsVisible(name) {
		server.WriteError(w, http.StatusNotFound, "tool_not_http_exposed", "tool not found", map[string]any{"tool": name})
		return
	}
	body := []byte(nil)
	if r.Body != nil {
		defer r.Body.Close()
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			server.WriteError(w, http.StatusBadRequest, "bad_request", "failed to read request body", nil)
			return
		}
	}
	args, err := toolargs.DecodeHTTPBody(body, a.App.ToolArguments.EnableBase64)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	submission, err := a.App.SubmitToolTaskWithMeta(invocation.Meta{
		Transport: invocation.TransportToolHTTP, WorkspaceID: grant.WorkspaceID, Requester: "tool-http",
	}, name, args)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "tool_call_failed", err.Error(), map[string]any{"tool": name})
		return
	}
	a.writeSubmissionResult(r.Context(), w, submission, http.StatusOK)
}

func (a *API) writeSubmissionResult(ctx context.Context, w http.ResponseWriter, submission app.Submission, successStatus int) {
	if submission.Approval != nil {
		if _, resolved, err := a.App.WaitForApprovalDecision(ctx, submission); err != nil {
			server.WriteError(w, http.StatusRequestTimeout, "approval_wait_interrupted", err.Error(), map[string]any{"task_id": submission.Task.ID})
			return
		} else if !resolved {
			server.WriteJSON(w, http.StatusAccepted, map[string]any{
				"status":       "pending_approval",
				"approval_id":  submission.Approval.ID,
				"approval_url": submission.Approval.ApprovalURL,
				"task_id":      submission.Approval.TaskID,
				"expires_at":   submission.Approval.ExpiresAt,
				"summary":      submission.Approval.Summary,
				"risk_level":   submission.Approval.RiskLevel,
			})
			return
		}
	}
	if submission.Task == nil {
		server.WriteError(w, http.StatusInternalServerError, "task_missing", "task was not created", nil)
		return
	}
	wait := time.Duration(a.App.Approval.MaxSyncWaitSeconds) * time.Second
	deadline := time.Now().Add(wait)
	for {
		task, err := a.App.Tasks.Get(submission.Task.ID)
		if err != nil {
			server.WriteError(w, http.StatusNotFound, "task_not_found", "task not found", nil)
			return
		}
		if task.Status == core.StatusSucceeded {
			server.WriteJSON(w, successStatus, task.Result)
			return
		}
		if task.Status == core.StatusFailed || task.Status == core.StatusCanceled || task.Status == core.StatusTimeout {
			status := http.StatusBadRequest
			code := "tool_execution_failed"
			if task.ErrorCode != "" {
				code = task.ErrorCode
			}
			if task.Status == core.StatusTimeout {
				status = http.StatusRequestTimeout
			} else if task.Status == core.StatusCanceled && strings.HasPrefix(task.ErrorCode, "approval_") {
				status = http.StatusConflict
			}
			server.WriteError(w, status, code, task.ErrorMsg, map[string]any{
				"task_id":    task.ID,
				"status":     task.Status,
				"error_code": task.ErrorCode,
			})
			return
		}
		if wait <= 0 || time.Now().After(deadline) {
			server.WriteJSON(w, http.StatusAccepted, map[string]any{"task_id": task.ID, "status": task.Status})
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (a *API) handleTaskRoutes(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/tasks/")
	if p == "" {
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
	parts := strings.Split(p, "/")
	taskID := parts[0]
	if taskID == "" {
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}

	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		t, err := a.App.Tasks.Get(taskID)
		if err != nil {
			server.WriteError(w, http.StatusNotFound, "task_not_found", "task not found", nil)
			return
		}
		server.WriteJSON(w, http.StatusOK, t)
		return
	}

	switch parts[1] {
	case "cancel":
		if r.Method != http.MethodPost {
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		if err := a.App.Tasks.Cancel(taskID); err != nil {
			server.WriteError(w, http.StatusNotFound, "task_not_found", "task not found", nil)
			return
		}
		t, _ := a.App.Tasks.Get(taskID)
		server.WriteJSON(w, http.StatusAccepted, t)
		return
	case "result":
		if r.Method != http.MethodGet {
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		t, err := a.App.Tasks.Get(taskID)
		if err != nil {
			server.WriteError(w, http.StatusNotFound, "task_not_found", "task not found", nil)
			return
		}
		payload := buildTaskResultPayload(t)
		appendTaskStreams(payload, a.App.Sink, taskID)
		server.WriteJSON(w, http.StatusOK, payload)
		return
	case "logs":
		if r.Method != http.MethodGet {
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		if _, err := a.App.Tasks.Get(taskID); err != nil {
			server.WriteError(w, http.StatusNotFound, "task_not_found", "task not found", nil)
			return
		}
		cursor := 0
		if v := r.URL.Query().Get("cursor"); v != "" {
			if i, err := strconv.Atoi(v); err == nil && i >= 0 {
				cursor = i
			}
		}
		evs, next := a.App.Sink.List(taskID, cursor)
		server.WriteJSON(w, http.StatusOK, map[string]any{"cursor": next, "events": evs})
		return
	case "events":
		if len(parts) == 3 && parts[2] == "stream" {
			a.handleEventsStream(w, r, taskID)
			return
		}
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	default:
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
}

func (a *API) handleApprovalList(w http.ResponseWriter, r *http.Request) {
	if !a.requireApprovalURL(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	items, err := a.App.Approvals.List(r.URL.Query().Get("status"))
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "approval_list_failed", err.Error(), nil)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (a *API) handleApprovalRoutes(w http.ResponseWriter, r *http.Request) {
	if !a.requireApprovalURL(w, r) {
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/approvals/")
	if p == "" {
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
	parts := strings.Split(p, "/")
	id := parts[0]
	if id == "" {
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		req, err := a.App.Approvals.Get(id)
		if err != nil {
			server.WriteError(w, http.StatusNotFound, "approval_not_found", "approval not found", nil)
			return
		}
		server.WriteJSON(w, http.StatusOK, req)
		return
	}
	principal := server.PrincipalFromRequest(r)
	actor := approval.Actor{ID: principal.ID, AuthMethod: principal.AuthMethod}
	switch parts[1] {
	case "approve":
		if r.Method != http.MethodPost {
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		if !a.validateApprovalMutationOrigin(w, r) {
			return
		}
		updated, err := a.App.ApproveApproval(id, actor)
		if err != nil {
			status := http.StatusBadRequest
			code := "approval_failed"
			switch {
			case errors.Is(err, approval.ErrApprovalNotFound):
				status = http.StatusNotFound
				code = "approval_not_found"
			case errors.Is(err, approval.ErrApprovalBindingMismatch):
				status = http.StatusConflict
				code = "approval_binding_mismatch"
			case strings.Contains(err.Error(), "approval gate not found"):
				status = http.StatusInternalServerError
				code = "approval_release_failed"
			}
			server.WriteError(w, status, code, err.Error(), nil)
			return
		}
		a.writeApprovalMutationResponse(w, r, updated, "Approval granted", "The task has been released and will continue automatically. Return to the conversation and query the task ID for the final result.")
		return
	case "reject":
		if r.Method != http.MethodPost {
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		if !a.validateApprovalMutationOrigin(w, r) {
			return
		}
		req, err := a.App.Approvals.RejectBy(id, actor)
		if err != nil {
			server.WriteError(w, http.StatusBadRequest, "approval_failed", err.Error(), nil)
			return
		}
		_ = a.App.CancelApprovalTaskWithReason(id, req.TaskID, "approval_rejected", "approval was rejected before execution")
		a.writeApprovalMutationResponse(w, r, req, "Approval rejected", "The request was rejected and its task will not execute.")
		return
	case "cancel":
		if r.Method != http.MethodPost {
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		if !a.validateApprovalMutationOrigin(w, r) {
			return
		}
		req, err := a.App.Approvals.CancelBy(id, actor)
		if err != nil {
			server.WriteError(w, http.StatusBadRequest, "approval_failed", err.Error(), nil)
			return
		}
		_ = a.App.CancelApprovalTaskWithReason(id, req.TaskID, "approval_canceled", "approval was canceled before execution")
		a.writeApprovalMutationResponse(w, r, req, "Approval canceled", "The approval request and its waiting task were canceled.")
		return
	default:
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
}

func (a *API) writeApprovalMutationResponse(w http.ResponseWriter, r *http.Request, req approval.Request, title, message string) {
	if !isApprovalBrowserRequest(r) {
		server.WriteJSON(w, http.StatusAccepted, req)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	viewURL := strings.TrimRight(strings.TrimSpace(a.App.Admin.BaseURL), "/") + "/approval-ui/" + url.PathEscape(req.ID)
	view := struct {
		Title   string
		Message string
		Status  approval.Status
		TaskID  string
		ViewURL string
	}{
		Title:   title,
		Message: message,
		Status:  req.Status,
		TaskID:  req.TaskID,
		ViewURL: viewURL,
	}
	tpl := template.Must(template.New("approval-result").Parse(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title><style>body{font-family:system-ui,sans-serif;max-width:720px;margin:56px auto;padding:0 20px;line-height:1.5}.card{border:1px solid #ddd;border-radius:12px;padding:24px}code{background:#f5f5f5;padding:2px 6px;border-radius:5px}a{display:inline-block;margin-top:16px}</style></head><body><div class="card"><h1>{{.Title}}</h1><p>{{.Message}}</p><p><strong>Status:</strong> {{.Status}}</p>{{if .TaskID}}<p><strong>Task ID:</strong> <code>{{.TaskID}}</code></p>{{end}}<a href="{{.ViewURL}}">View approval details</a></div></body></html>`))
	if err := tpl.Execute(w, view); err != nil {
		server.WriteError(w, http.StatusInternalServerError, "approval_result_page_failed", err.Error(), nil)
	}
}

func isApprovalBrowserRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") || strings.HasPrefix(contentType, "multipart/form-data") {
		return true
	}
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html")
}

func (a *API) handleApprovalUI(w http.ResponseWriter, r *http.Request) {
	if !a.requireApprovalURL(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if r.URL.Path == "/approval-ui" || r.URL.Path == "/approval-ui/" {
		a.handleApprovalManagementUI(w, r)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/approval-ui/")
	if id == "" || strings.Contains(id, "/") {
		server.WriteError(w, http.StatusNotFound, "approval_not_found", "approval not found", nil)
		return
	}
	req, err := a.App.Approvals.Get(id)
	if err != nil {
		server.WriteError(w, http.StatusNotFound, "approval_not_found", "approval not found", nil)
		return
	}
	setApprovalPageHeaders(w)
	view := struct {
		Request    any
		ApproveURL string
		RejectURL  string
		ListURL    string
	}{
		Request:    req,
		ApproveURL: approvalActionURL(a.App.Admin.BaseURL, req.ID, "approve"),
		RejectURL:  approvalActionURL(a.App.Admin.BaseURL, req.ID, "reject"),
		ListURL:    approvalManagementURL(a.App.Admin.BaseURL, "pending"),
	}
	tpl := template.Must(template.New("approval").Parse(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>LocalGate Approval</title><style>body{font-family:system-ui,sans-serif;max-width:760px;margin:48px auto;padding:0 20px}pre{white-space:pre-wrap;background:#f5f5f5;padding:16px;border-radius:8px}button{padding:10px 18px;margin-right:10px}</style></head><body><p><a href="{{.ListURL}}">← Approval management</a></p><h1>Approval required</h1><p><strong>Status:</strong> {{.Request.Status}}</p><p><strong>Summary:</strong> {{.Request.Summary}}</p><p><strong>Risk:</strong> {{.Request.RiskLevel}}</p><p><strong>Tool:</strong> {{.Request.Tool}}</p><p><strong>Workspace:</strong> {{.Request.WorkspaceID}}</p><p><strong>Task:</strong> {{.Request.TaskID}}</p><p><strong>Expires:</strong> {{.Request.ExpiresAt}}</p><pre>{{printf "%#v" .Request.Args}}</pre>{{if eq .Request.Status "pending"}}<form method="post" action="{{.ApproveURL}}" style="display:inline"><button type="submit">Approve and execute</button></form><form method="post" action="{{.RejectURL}}" style="display:inline"><button type="submit">Reject</button></form>{{end}}</body></html>`))
	if err := tpl.Execute(w, view); err != nil {
		server.WriteError(w, http.StatusInternalServerError, "approval_page_failed", err.Error(), nil)
	}
}

func (a *API) validateApprovalMutationOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	browserRequest := strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")) != "" || strings.HasPrefix(contentType, "application/x-www-form-urlencoded")
	if origin == "" {
		if browserRequest {
			server.WriteError(w, http.StatusForbidden, "approval_origin_required", "browser approval requests must include Origin", nil)
			return false
		}
		return true
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
		server.WriteError(w, http.StatusForbidden, "approval_origin_forbidden", "cross-site approval requests are not allowed", nil)
		return false
	}
	expected, err := url.Parse(strings.TrimSpace(a.App.Admin.BaseURL))
	if err != nil || expected.Scheme == "" || expected.Host == "" {
		server.WriteError(w, http.StatusInternalServerError, "approval_origin_config_invalid", "approval origin is not configured", nil)
		return false
	}
	actual, err := url.Parse(origin)
	if err != nil || actual.Scheme == "" || actual.Host == "" || actual.User != nil || (actual.Path != "" && actual.Path != "/") || actual.RawQuery != "" || actual.Fragment != "" {
		server.WriteError(w, http.StatusForbidden, "approval_origin_forbidden", "approval Origin is invalid", nil)
		return false
	}
	if !strings.EqualFold(actual.Scheme, expected.Scheme) || !strings.EqualFold(actual.Host, expected.Host) {
		server.WriteError(w, http.StatusForbidden, "approval_origin_forbidden", "approval Origin does not match admin.base_url", nil)
		return false
	}
	return true
}

func approvalActionURL(baseURL, approvalID, action string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/approvals/" + url.PathEscape(approvalID) + "/" + action
}

func (a *API) handleAuditQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	filter := map[string]string{
		"approval_id":  r.URL.Query().Get("approval_id"),
		"operation_id": r.URL.Query().Get("operation_id"),
		"task_id":      r.URL.Query().Get("task_id"),
		"requester":    r.URL.Query().Get("requester"),
		"tool":         r.URL.Query().Get("tool"),
		"action":       r.URL.Query().Get("action"),
	}
	limit := 100
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			limit = i
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i >= 0 {
			offset = i
		}
	}
	recs, err := a.App.Approvals.QueryAudit(filter, limit, offset)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "audit_query_failed", err.Error(), nil)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"items": recs, "limit": limit, "offset": offset})
}

func (a *API) handleUpgrades(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if a.App.Upgrades == nil || !a.App.Upgrades.Enabled() {
		server.WriteError(w, http.StatusServiceUnavailable, "upgrade_not_enabled", "upgrade endpoint is not enabled", nil)
		return
	}
	if !server.MatchHeaderToken(r, upgradeTokenHeader, a.App.Upgrades.Token()) {
		server.WriteError(w, http.StatusUnauthorized, "invalid_upgrade_token", "invalid upgrade token", nil)
		return
	}
	if a.App.Upgrades.RuntimeMode() != "http_daemon" {
		server.WriteError(w, http.StatusBadRequest, "upgrade_not_supported", "upgrade only supported for http daemon mode", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, upgradeUploadMaxBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		server.WriteError(w, http.StatusBadRequest, "bad_request", "invalid multipart form", nil)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "bad_request", "file is required", nil)
		return
	}
	defer file.Close()

	state, err := a.App.Upgrades.Create(file, header.Filename)
	if err != nil {
		switch {
		case errors.Is(err, upgrade.ErrActiveUpgrade):
			server.WriteError(w, http.StatusConflict, "upgrade_in_progress", "another upgrade is already active", nil)
		default:
			server.WriteError(w, http.StatusBadRequest, "upgrade_create_failed", err.Error(), nil)
		}
		return
	}

	task := a.App.Tasks.CreateTask(core.Task{Requester: "upgrade"}, func(ctx context.Context, taskID string) (int, error) {
		res, err := a.App.Upgrades.Dispatch(ctx, state.ID, taskID)
		if res != nil {
			_ = a.App.Tasks.SetResult(taskID, res)
		}
		return 0, err
	})
	server.WriteJSON(w, http.StatusAccepted, map[string]any{
		"upgrade_id": state.ID,
		"task_id":    task.ID,
		"status":     "accepted",
	})
}

func (a *API) handleUploads(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if a.App.Uploads == nil {
		server.WriteError(w, http.StatusServiceUnavailable, "upload_not_enabled", "upload endpoint is not enabled", nil)
		return
	}

	grant, err := a.App.Uploads.Get(r.Header.Get(uploadTokenHeader))
	if err != nil {
		switch {
		case errors.Is(err, upload.ErrTokenRequired), errors.Is(err, upload.ErrTokenNotFound), errors.Is(err, upload.ErrTokenExpired):
			server.WriteError(w, http.StatusUnauthorized, "invalid_upload_token", err.Error(), nil)
		default:
			server.WriteError(w, http.StatusInternalServerError, "upload_token_check_failed", err.Error(), nil)
		}
		return
	}

	ws, ok := a.App.Workspaces[grant.WorkspaceID]
	if !ok {
		server.WriteError(w, http.StatusBadRequest, "workspace_not_found", "workspace not found", map[string]any{"workspace_id": grant.WorkspaceID})
		return
	}
	dec := security.ValidatePath(grant.Path, ws, security.Options{FollowSymlink: true, AllowNonExist: true})
	if !dec.Allowed {
		server.WriteError(w, http.StatusBadRequest, "path_not_allowed", "path not allowed: "+dec.Reason, nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, uploadFileMaxBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		server.WriteError(w, http.StatusBadRequest, "bad_request", "invalid multipart form", nil)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "bad_request", "file is required", nil)
		return
	}
	defer file.Close()

	_, statErr := os.Stat(dec.Path)
	overwritten := statErr == nil
	written, err := fileio.WriteReaderAtomic(dec.Path, file, fileio.AtomicWriteOptions{
		Mode:          0o644,
		PreserveMode:  true,
		CreateParents: true,
		MaxBytes:      uploadFileMaxBytes,
	})
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "upload_write_failed", err.Error(), nil)
		return
	}

	status := "created"
	code := http.StatusCreated
	if overwritten {
		status = "overwritten"
		code = http.StatusOK
	}
	server.WriteJSON(w, code, map[string]any{
		"status":      status,
		"path":        dec.Path,
		"bytes":       written,
		"overwritten": overwritten,
	})
}

func (a *API) handleDownloads(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if a.App.Downloads == nil {
		server.WriteError(w, http.StatusServiceUnavailable, "download_not_enabled", "download endpoint is not enabled", nil)
		return
	}

	grant, err := a.App.Downloads.Get(r.URL.Query().Get(downloadTokenQueryKey))
	if err != nil {
		switch {
		case errors.Is(err, download.ErrTokenRequired), errors.Is(err, download.ErrTokenNotFound), errors.Is(err, download.ErrTokenExpired):
			server.WriteError(w, http.StatusUnauthorized, "invalid_download_token", err.Error(), nil)
		default:
			server.WriteError(w, http.StatusInternalServerError, "download_token_check_failed", err.Error(), nil)
		}
		return
	}

	ws, ok := a.App.Workspaces[grant.WorkspaceID]
	if !ok {
		server.WriteError(w, http.StatusBadRequest, "workspace_not_found", "workspace not found", map[string]any{"workspace_id": grant.WorkspaceID})
		return
	}
	dec := security.ValidatePath(grant.Path, ws, security.Options{FollowSymlink: true, AllowNonExist: true})
	if !dec.Allowed {
		server.WriteError(w, http.StatusBadRequest, "path_not_allowed", "path not allowed: "+dec.Reason, nil)
		return
	}

	info, err := os.Stat(dec.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			server.WriteError(w, http.StatusNotFound, "file_not_found", "file not found", nil)
			return
		}
		server.WriteError(w, http.StatusInternalServerError, "download_read_failed", err.Error(), nil)
		return
	}
	if info.IsDir() {
		server.WriteError(w, http.StatusBadRequest, "path_is_directory", "path must be a file", nil)
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(filepath.Base(dec.Path)))
	http.ServeFile(w, r, dec.Path)
}

func (a *API) handleExports(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if a.App.Exports == nil || !a.App.FileExports.Enabled {
		server.WriteError(w, http.StatusNotFound, "export_not_found", "export not found", nil)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/system/exports/")
	if id == "" || strings.Contains(id, "/") {
		server.WriteError(w, http.StatusNotFound, "export_not_found", "export not found", nil)
		return
	}
	grant, err := a.App.Exports.Authorize(id, r.URL.Query().Get("signature"))
	if err != nil {
		if errors.Is(err, fileexport.ErrExportExpired) {
			server.WriteError(w, http.StatusGone, "export_expired", "export expired", nil)
			return
		}
		server.WriteError(w, http.StatusNotFound, "export_not_found", "export not found", nil)
		return
	}

	ws, ok := a.App.Workspaces[grant.WorkspaceID]
	if !ok {
		server.WriteError(w, http.StatusNotFound, "export_not_found", "export not found", nil)
		return
	}
	lexical := security.ValidatePath(grant.Path, ws, security.Options{})
	if !lexical.Allowed {
		server.WriteError(w, http.StatusConflict, "export_source_changed", "export source is no longer available", nil)
		return
	}
	if info, err := os.Lstat(lexical.Path); err != nil || info.Mode()&os.ModeSymlink != 0 {
		server.WriteError(w, http.StatusConflict, "export_source_changed", "export source is no longer available", nil)
		return
	}
	dec := security.ValidatePath(lexical.Path, ws, security.Options{FollowSymlink: true})
	if !dec.Allowed {
		server.WriteError(w, http.StatusConflict, "export_source_changed", "export source is no longer available", nil)
		return
	}
	f, err := os.Open(dec.Path)
	if err != nil {
		server.WriteError(w, http.StatusConflict, "export_source_changed", "export source is no longer available", nil)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != grant.Size || info.ModTime().UnixNano() != grant.ModTimeUnixNano {
		server.WriteError(w, http.StatusConflict, "export_source_changed", "export source changed after the export was created", nil)
		return
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		server.WriteError(w, http.StatusInternalServerError, "export_read_failed", "failed to read exported file", nil)
		return
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), grant.SHA256) {
		server.WriteError(w, http.StatusConflict, "export_source_changed", "export source changed after the export was created", nil)
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		server.WriteError(w, http.StatusInternalServerError, "export_read_failed", "failed to read exported file", nil)
		return
	}

	w.Header().Set("Content-Type", grant.MIMEType)
	if disposition := mime.FormatMediaType("attachment", map[string]string{"filename": grant.FileName}); disposition != "" {
		w.Header().Set("Content-Disposition", disposition)
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.ServeContent(w, r, grant.FileName, info.ModTime(), f)
}

func (a *API) handleUpgradeRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if a.App.Upgrades == nil {
		server.WriteError(w, http.StatusServiceUnavailable, "upgrade_not_enabled", "upgrade endpoint is not enabled", nil)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/system/upgrades/")
	if id == "" || strings.Contains(id, "/") {
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
	state, err := a.App.Upgrades.Get(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			server.WriteError(w, http.StatusNotFound, "upgrade_not_found", "upgrade not found", nil)
			return
		}
		server.WriteError(w, http.StatusInternalServerError, "upgrade_read_failed", err.Error(), nil)
		return
	}
	server.WriteJSON(w, http.StatusOK, state)
}

func (a *API) resolveRegistry(w http.ResponseWriter, r *http.Request) (string, *core.Registry, bool) {
	wsid := r.URL.Query().Get("workspace_id")
	if wsid == "" {
		wsid = a.DefaultWSID
	}
	reg, ok := a.App.Registries[wsid]
	if !ok {
		server.WriteError(w, http.StatusBadRequest, "workspace_not_found", "workspace not found", map[string]any{"workspace_id": wsid})
		return "", nil, false
	}
	return wsid, reg, true
}

func (a *API) authorizeToolRequest(w http.ResponseWriter, r *http.Request) (toolaccess.Grant, bool) {
	if a.App.ToolAccess == nil {
		server.WriteError(w, http.StatusUnauthorized, "tool_access_disabled", "tool access is not enabled", nil)
		return toolaccess.Grant{}, false
	}
	grant, err := a.App.ToolAccess.Get(r.Header.Get(toolaccess.HeaderName))
	if err != nil {
		switch {
		case errors.Is(err, toolaccess.ErrAuthValueRequired):
			server.WriteError(w, http.StatusUnauthorized, "missing_tool_access", err.Error(), nil)
		case errors.Is(err, toolaccess.ErrAuthValueNotFound), errors.Is(err, toolaccess.ErrAuthValueExpired):
			server.WriteError(w, http.StatusUnauthorized, "invalid_tool_access", err.Error(), nil)
		default:
			server.WriteError(w, http.StatusUnauthorized, "invalid_tool_access", err.Error(), nil)
		}
		return toolaccess.Grant{}, false
	}
	return grant, true
}

func (a *API) handleEventsStream(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodGet {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if _, err := a.App.Tasks.Get(taskID); err != nil {
		server.WriteError(w, http.StatusNotFound, "task_not_found", "task not found", nil)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		server.WriteError(w, http.StatusInternalServerError, "stream_not_supported", "stream not supported", nil)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	cursor := 0
	if v := r.URL.Query().Get("cursor"); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i >= 0 {
			cursor = i
		}
	} else if v := r.Header.Get("Last-Event-ID"); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i >= 0 {
			cursor = i
		}
	}
	// First: replay backlog from cursor
	if evs, next := a.App.Sink.List(taskID, cursor); len(evs) > 0 {
		for i, ev := range evs {
			b, _ := json.Marshal(ev)
			id := cursor + i + 1
			_, _ = w.Write([]byte("id: " + strconv.Itoa(id) + "\n"))
			_, _ = w.Write([]byte("event: " + string(ev.Type) + "\n"))
			_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
		}
		cursor = next
		flusher.Flush()
	}
	// Then: subscribe for realtime
	ch, cancel := a.App.Sink.Subscribe(taskID, 64)
	defer cancel()
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			cursor++
			b, _ := json.Marshal(ev)
			_, _ = w.Write([]byte("id: " + strconv.Itoa(cursor) + "\n"))
			_, _ = w.Write([]byte("event: " + string(ev.Type) + "\n"))
			_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
			flusher.Flush()
		case <-heartbeat.C:
			_, _ = w.Write([]byte(": heartbeat\n\n"))
			flusher.Flush()
		}
	}
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

func buildTaskResultPayload(t *core.Task) map[string]any {
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
	return payload
}

func appendTaskStreams(payload map[string]any, sink *core.EventSink, taskID string) {
	if payload == nil || sink == nil || taskID == "" {
		return
	}
	stdout, stderr := collectTaskStreams(sink, taskID, 2000)
	if stdout != "" {
		payload["stdout"] = stdout
	}
	if stderr != "" {
		payload["stderr"] = stderr
	}
}

func collectTaskStreams(sink *core.EventSink, taskID string, maxLines int) (string, string) {
	evs, _ := sink.List(taskID, 0)
	var stdout strings.Builder
	var stderr strings.Builder
	stdoutEvents := 0
	stderrEvents := 0
	for _, ev := range evs {
		if ev.Type == core.EventTaskStdout {
			appendTaskEventOutput(&stdout, ev)
			stdoutEvents++
		} else if ev.Type == core.EventTaskStderr {
			appendTaskEventOutput(&stderr, ev)
			stderrEvents++
		}
		if maxLines > 0 && (stdoutEvents+stderrEvents) >= maxLines {
			break
		}
	}
	return strings.TrimSuffix(stdout.String(), "\n"), strings.TrimSuffix(stderr.String(), "\n")
}

func appendTaskEventOutput(out *strings.Builder, ev core.Event) {
	out.WriteString(ev.Data)
	if !ev.Continues {
		out.WriteByte('\n')
	}
}

func isTerminalStatus(status core.Status) bool {
	return status == core.StatusSucceeded || status == core.StatusFailed || status == core.StatusCanceled || status == core.StatusTimeout
}
