package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"localgate/internal/app"
	"localgate/internal/core"
	"localgate/internal/invocation"
	mcpruntime "localgate/internal/mcp"
	mcptools "localgate/internal/mcp/tools"
	"localgate/internal/server"
	"localgate/internal/static"
)

const (
	mcpRuntimeMaxRequestBytes = 1 << 20
	mcpRequesterHeader        = "X-LocalGate-Requester"
)

type mcpRuntime struct {
	api           *API
	workspaceID   string
	registry      *mcpruntime.Registry
	dispatcher    *mcpruntime.Dispatcher
	staticService *static.Service
}

type mcpRuntimeResponse struct {
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
	Code    string `json:"code,omitempty"`
}

type mcpRuntimeToolDescriptor struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	InputSchema  any    `json:"inputSchema"`
	OutputSchema any    `json:"outputSchema,omitempty"`
}

func newMCPRuntime(api *API) (*mcpRuntime, error) {
	if api == nil || api.App == nil {
		return nil, errors.New("application is not initialized")
	}
	workspace, ok := api.App.Workspaces[api.DefaultWSID]
	if !ok {
		return nil, errors.New("default workspace is not configured")
	}
	staticService := api.App.StaticService
	if staticService == nil || api.App.DeploymentManager == nil {
		return nil, errors.New("deployment service is not configured")
	}
	registry := mcpruntime.NewRegistry()
	if err := registry.Register(mcptools.NewDeployToolWithManager(api.App.DeploymentManager, workspace)); err != nil {
		return nil, err
	}
	if err := registry.Register(mcptools.NewDeleteDeploymentToolWithManager(api.App.DeploymentManager)); err != nil {
		return nil, err
	}
	if err := registry.Register(mcptools.NewListDeploymentsToolWithManager(api.App.DeploymentManager)); err != nil {
		return nil, err
	}
	if api.App.DynamicHosting.Enabled {
		for _, tool := range mcptools.NewDynamicDeploymentToolsWithManager(api.App.DeploymentManager) {
			if err := registry.Register(tool); err != nil {
				return nil, err
			}
		}
	}
	return &mcpRuntime{
		api:           api,
		workspaceID:   api.DefaultWSID,
		registry:      registry,
		dispatcher:    mcpruntime.NewDispatcher(registry),
		staticService: staticService,
	}, nil
}

func (a *API) wireMCPRuntime(mux *http.ServeMux) {
	runtime, err := newMCPRuntime(a)
	if err != nil {
		a.mcpRuntime = nil
		unavailable := func(w http.ResponseWriter, r *http.Request) {
			writeMCPRuntimeError(w, http.StatusServiceUnavailable, "runtime_unavailable", err.Error())
		}
		mux.HandleFunc("/mcp/tools", unavailable)
		mux.HandleFunc("/mcp/tools/execute", unavailable)
		mux.HandleFunc(static.PublicPathPrefix, unavailable)
		return
	}
	a.mcpRuntime = runtime
	mux.HandleFunc("/mcp/tools", runtime.handleTools)
	mux.HandleFunc("/mcp/tools/execute", runtime.handleExecute)
	mux.Handle(static.PublicPathPrefix, runtime.staticService)
}

func (rt *mcpRuntime) handleTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMCPRuntimeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	tools := rt.registry.List()
	items := make([]mcpRuntimeToolDescriptor, 0, len(tools))
	for _, tool := range tools {
		items = append(items, mcpRuntimeToolDescriptor{
			Name:         tool.Name(),
			Description:  tool.Description(),
			InputSchema:  tool.InputSchema(),
			OutputSchema: tool.OutputSchema(),
		})
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"tools": items})
}

func (rt *mcpRuntime) handleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMCPRuntimeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, mcpRuntimeMaxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request mcpruntime.MCPRequest
	if err := decoder.Decode(&request); err != nil {
		writeMCPRuntimeError(w, http.StatusBadRequest, "bad_request", "invalid request: "+err.Error())
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			writeMCPRuntimeError(w, http.StatusBadRequest, "bad_request", "request must contain exactly one JSON value")
			return
		}
		writeMCPRuntimeError(w, http.StatusBadRequest, "bad_request", "invalid trailing request data: "+err.Error())
		return
	}
	request.Requester = resolveRuntimeRequester(request.Requester, r)
	toolDescriptor, err := rt.dispatcher.Validate(request)
	if err != nil {
		status, code := runtimeErrorStatus(err)
		writeMCPRuntimeError(w, status, code, err.Error())
		return
	}
	args, err := rawObject(request.Input)
	if err != nil {
		writeMCPRuntimeError(w, http.StatusBadRequest, "invalid_tool_input", err.Error())
		return
	}
	submission, err := rt.api.App.SubmitToolTaskWithMeta(invocation.Meta{
		Transport:       invocation.TransportMCPRuntime,
		WorkspaceID:     rt.workspaceID,
		Requester:       request.Requester,
		MCPSessionID:    request.SessionID,
		SessionMode:     request.SessionMode,
		SessionIDSource: request.SessionIDSource,
		RequestID:       request.RequestID,
	}, request.Tool, args)
	if err != nil {
		if errors.Is(err, app.ErrApprovalPolicyDenied) {
			writeMCPRuntimeError(w, http.StatusForbidden, "policy_denied", err.Error())
			return
		}
		writeMCPRuntimeError(w, http.StatusBadRequest, "tool_submission_failed", err.Error())
		return
	}
	if submission.Approval != nil {
		server.WriteJSON(w, http.StatusAccepted, mcpRuntimeResponse{
			Success: false,
			Code:    "approval_required",
			Data: map[string]any{
				"status":       "pending_approval",
				"approval_id":  submission.Approval.ID,
				"approval_url": submission.Approval.ApprovalURL,
				"task_id":      submission.Approval.TaskID,
				"expires_at":   submission.Approval.ExpiresAt,
				"summary":      submission.Approval.Summary,
				"risk_level":   submission.Approval.RiskLevel,
			},
		})
		return
	}
	if submission.Task == nil {
		writeMCPRuntimeError(w, http.StatusInternalServerError, "task_create_failed", "tool submission did not create a task")
		return
	}

	wait := time.Duration(rt.api.App.Approval.MaxSyncWaitSeconds) * time.Second
	task, ready := waitForRuntimeTask(r.Context(), rt.api.App.Tasks, submission.Task.ID, wait)
	if !ready {
		server.WriteJSON(w, http.StatusAccepted, mcpRuntimeResponse{
			Success: false,
			Code:    "task_pending",
			Data: map[string]any{
				"task_id": task.ID,
				"status":  task.Status,
			},
		})
		return
	}
	if task.Status != core.StatusSucceeded {
		status, code := runtimeTaskErrorStatus(task)
		writeMCPRuntimeError(w, status, code, task.ErrorMsg)
		return
	}
	if toolDescriptor.OutputSchema() != nil {
		raw, err := json.Marshal(task.Result)
		if err != nil {
			writeMCPRuntimeError(w, http.StatusInternalServerError, "invalid_tool_output", err.Error())
			return
		}
		if err := mcpruntime.ValidateJSON(raw, toolDescriptor.OutputSchema()); err != nil {
			writeMCPRuntimeError(w, http.StatusInternalServerError, "invalid_tool_output", err.Error())
			return
		}
	}
	server.WriteJSON(w, http.StatusOK, mcpRuntimeResponse{Success: true, Data: task.Result})
}

func runtimeTaskErrorStatus(task *core.Task) (int, string) {
	if task == nil {
		return http.StatusInternalServerError, "task_not_found"
	}
	if task.ErrorCode == "approval_release_failed" {
		return http.StatusInternalServerError, task.ErrorCode
	}
	if strings.HasPrefix(task.ErrorCode, "approval_") {
		return http.StatusConflict, task.ErrorCode
	}
	if task.ErrorCode != "" {
		switch task.ErrorCode {
		case "deployment_not_found", "deployment_release_not_found":
			return http.StatusNotFound, task.ErrorCode
		case "dynamic_hosting_disabled", "dynamic_runtime_not_owned", "deployment_proxy_unavailable":
			return http.StatusServiceUnavailable, task.ErrorCode
		case "deployment_limit_exceeded", "deployment_snapshot_failed":
			return http.StatusRequestEntityTooLarge, task.ErrorCode
		case "deployment_cleanup_failed":
			return http.StatusInternalServerError, task.ErrorCode
		default:
			return http.StatusBadRequest, task.ErrorCode
		}
	}
	if task.Status == core.StatusCanceled || task.Status == core.StatusTimeout {
		return http.StatusRequestTimeout, "execution_canceled"
	}
	message := task.ErrorMsg
	switch {
	case strings.Contains(message, mcptools.ErrUnsafeSourceRemoval.Error()):
		return http.StatusBadRequest, "unsafe_source_removal"
	case strings.Contains(message, static.ErrLimitExceeded.Error()):
		return http.StatusRequestEntityTooLarge, "deploy_limit_exceeded"
	case strings.Contains(message, static.ErrSourceOverlapsStore.Error()):
		return http.StatusBadRequest, "source_overlaps_storage"
	default:
		return http.StatusBadRequest, "tool_execution_failed"
	}
}

func waitForRuntimeTask(ctx context.Context, tasks *core.Manager, taskID string, wait time.Duration) (*core.Task, bool) {
	if wait < 0 {
		wait = 0
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		task, err := tasks.Get(taskID)
		if err != nil {
			return &core.Task{ID: taskID, Status: core.StatusFailed, ErrorMsg: err.Error()}, true
		}
		if isTerminalStatus(task.Status) {
			return task, true
		}
		if wait == 0 {
			return task, false
		}
		select {
		case <-ctx.Done():
			return task, false
		case <-deadline.C:
			return task, false
		case <-ticker.C:
		}
	}
}

func rawObject(raw json.RawMessage) (map[string]any, error) {
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("decode tool input: %w", err)
	}
	if value == nil {
		return nil, errors.New("tool input must be an object")
	}
	return value, nil
}

func resolveRuntimeRequester(requester string, r *http.Request) string {
	requester = strings.TrimSpace(requester)
	if requester == "" && r != nil {
		requester = strings.TrimSpace(r.Header.Get(mcpRequesterHeader))
	}
	if requester == "" {
		requester = "mcp-http"
	}
	return requester
}

func runtimeErrorStatus(err error) (int, string) {
	if errors.Is(err, mcpruntime.ErrToolNotFound) {
		return http.StatusNotFound, "tool_not_found"
	}
	var inputErr *mcpruntime.InputValidationError
	if errors.As(err, &inputErr) {
		return http.StatusBadRequest, "invalid_tool_input"
	}
	var outputErr *mcpruntime.OutputValidationError
	if errors.As(err, &outputErr) {
		return http.StatusInternalServerError, "invalid_tool_output"
	}
	if errors.Is(err, static.ErrLimitExceeded) {
		return http.StatusRequestEntityTooLarge, "deploy_limit_exceeded"
	}
	if errors.Is(err, static.ErrSourceOverlapsStore) {
		return http.StatusBadRequest, "source_overlaps_storage"
	}
	if errors.Is(err, mcptools.ErrUnsafeSourceRemoval) {
		return http.StatusBadRequest, "unsafe_source_removal"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return http.StatusRequestTimeout, "execution_canceled"
	}
	return http.StatusBadRequest, "tool_execution_failed"
}

func writeMCPRuntimeError(w http.ResponseWriter, status int, code, message string) {
	server.WriteJSON(w, status, mcpRuntimeResponse{
		Success: false,
		Error:   message,
		Code:    code,
	})
}
