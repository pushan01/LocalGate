package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"localgate/internal/approval"
	"localgate/internal/core"
	"localgate/internal/deployment"
	"localgate/internal/dynamic"
)

type DynamicDeploymentTool struct {
	action  string
	manager *deployment.Manager
}

func NewDynamicDeploymentTools(service *dynamic.Service) []*DynamicDeploymentTool {
	return NewDynamicDeploymentToolsWithManager(deployment.NewManager(nil, service, ""))
}
func NewDynamicDeploymentToolsWithManager(manager *deployment.Manager) []*DynamicDeploymentTool {
	return []*DynamicDeploymentTool{
		{action: "get_deployment", manager: manager},
		{action: "restart_deployment", manager: manager},
		{action: "get_deployment_logs", manager: manager},
		{action: "stop_deployment", manager: manager},
		{action: "start_deployment", manager: manager},
		{action: "list_deployment_releases", manager: manager},
		{action: "rollback_deployment", manager: manager},
	}
}

func (t *DynamicDeploymentTool) Name() string { return t.action }

func (t *DynamicDeploymentTool) Description() string {
	switch t.action {
	case "get_deployment":
		return "Get status and metadata for a managed dynamic deployment."
	case "restart_deployment":
		return "Restart a managed dynamic deployment from its active release."
	case "get_deployment_logs":
		return "Read the tail of stdout or stderr for a managed dynamic deployment."
	case "stop_deployment":
		return "Stop a managed dynamic deployment without deleting its release."
	case "start_deployment":
		return "Start a previously stopped managed dynamic deployment."
	case "list_deployment_releases":
		return "List retained releases for a managed dynamic deployment."
	case "rollback_deployment":
		return "Roll back a managed dynamic deployment to a retained release."
	default:
		return "Manage a dynamic deployment."
	}
}

func (t *DynamicDeploymentTool) InputSchema() any {
	properties := map[string]any{
		"type": map[string]any{"type": "string", "enum": []any{"dynamic"}},
		"name": map[string]any{"type": "string", "minLength": 1},
	}
	if t.action == "get_deployment_logs" {
		properties["stream"] = map[string]any{"type": "string", "enum": []any{"stdout", "stderr"}}
		properties["tail_bytes"] = map[string]any{"type": "integer"}
	}
	required := []string{"type", "name"}
	if t.action == "rollback_deployment" {
		properties["release_id"] = map[string]any{"type": "string", "minLength": 1}
		required = append(required, "release_id")
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             required,
		"properties":           properties,
	}
}

func (t *DynamicDeploymentTool) OutputSchema() any { return nil }

func (t *DynamicDeploymentTool) Execute(ctx context.Context, input json.RawMessage) (any, error) {
	if t == nil || t.manager == nil {
		return nil, &dynamic.CodedError{Code: "dynamic_hosting_disabled", Err: dynamic.ErrDisabled}
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	var request struct {
		Type      string `json:"type"`
		Name      string `json:"name"`
		Stream    string `json:"stream,omitempty"`
		TailBytes int64  `json:"tail_bytes,omitempty"`
		ReleaseID string `json:"release_id,omitempty"`
	}
	if err := decoder.Decode(&request); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("request must contain exactly one JSON value")
		}
		return nil, err
	}
	if request.Type != dynamic.Type {
		return nil, errors.New("type must be dynamic")
	}
	switch t.action {
	case "get_deployment":
		view, err := t.manager.Get(request.Type, request.Name)
		return encodeDynamicResult(view, err)
	case "restart_deployment":
		view, err := t.manager.Restart(ctx, request.Type, request.Name)
		return encodeDynamicResult(view, err)
	case "stop_deployment":
		view, err := t.manager.Stop(ctx, request.Type, request.Name)
		return encodeDynamicResult(view, err)
	case "start_deployment":
		view, err := t.manager.Start(ctx, request.Type, request.Name)
		return encodeDynamicResult(view, err)
	case "list_deployment_releases":
		releases, err := t.manager.Releases(request.Type, request.Name)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "dynamic", "site": request.Name, "count": len(releases), "releases": releases}, nil
	case "rollback_deployment":
		view, err := t.manager.Rollback(ctx, request.Type, request.Name, request.ReleaseID)
		return encodeDynamicResult(view, err)
	case "get_deployment_logs":
		logs, err := t.manager.Logs(request.Type, request.Name, request.Stream, request.TailBytes)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"type": "dynamic", "site": logs.Name, "stream": logs.Stream,
			"data": logs.Data, "truncated": logs.Truncated,
		}, nil
	default:
		return nil, errors.New("unsupported dynamic deployment action")
	}
}

func encodeDynamicResult(view deployment.View, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(view)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(b, &result); err != nil {
		return nil, err
	}
	return result, nil
}

type CoreDynamicDeploymentTool struct {
	inner     *DynamicDeploymentTool
	approvals *approval.Service
}

func NewCoreDynamicDeploymentTools(service *dynamic.Service, approvals *approval.Service) []*CoreDynamicDeploymentTool {
	return NewCoreDynamicDeploymentToolsWithManager(deployment.NewManager(nil, service, ""), approvals)
}
func NewCoreDynamicDeploymentToolsWithManager(manager *deployment.Manager, approvals *approval.Service) []*CoreDynamicDeploymentTool {
	inners := NewDynamicDeploymentToolsWithManager(manager)
	out := make([]*CoreDynamicDeploymentTool, 0, len(inners))
	for _, inner := range inners {
		out = append(out, &CoreDynamicDeploymentTool{inner: inner, approvals: approvals})
	}
	return out
}

func (t *CoreDynamicDeploymentTool) Name() string        { return t.inner.Name() }
func (t *CoreDynamicDeploymentTool) Description() string { return t.inner.Description() }
func (t *CoreDynamicDeploymentTool) Schema() any         { return t.inner.InputSchema() }

func (t *CoreDynamicDeploymentTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	input, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	mutating := t.Name() == "restart_deployment" || t.Name() == "stop_deployment" || t.Name() == "start_deployment" || t.Name() == "rollback_deployment"
	if mutating {
		if err := t.recordAudit(ic, args, "execution_started", ""); err != nil {
			return nil, err
		}
	}
	requester := strings.TrimSpace(ic.Requester)
	if requester == "" {
		requester = "mcp"
	}
	ctx = dynamic.WithOperation(ctx, dynamic.Operation{
		ApprovalID: ic.ApprovalID, TaskID: ic.TaskID, Requester: requester, Tool: t.Name(),
		Transport: ic.Transport, MCPSessionID: ic.MCPSessionID, SessionMode: ic.SessionMode,
		SessionIDSource: ic.SessionIDSource, RequestID: ic.RequestID,
	})
	result, executeErr := t.inner.Execute(ctx, input)
	if mutating {
		action := "execution_succeeded"
		errText := ""
		if executeErr != nil {
			action = "execution_failed"
			errText = executeErr.Error()
		}
		if auditErr := t.recordAudit(ic, args, action, errText); auditErr != nil {
			if executeErr != nil {
				return nil, errors.Join(executeErr, auditErr)
			}
			return nil, auditErr
		}
	}
	if executeErr != nil {
		return nil, executeErr
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		return nil, errors.New("dynamic deployment tool returned an invalid result")
	}
	return resultMap, nil
}

func (t *CoreDynamicDeploymentTool) recordAudit(ic core.InvokeContext, args map[string]any, action, errorText string) error {
	if t.approvals == nil {
		return errors.New("deployment audit service is not available")
	}
	requester := strings.TrimSpace(ic.Requester)
	if requester == "" {
		requester = "mcp"
	}
	details := map[string]any{"type": "dynamic"}
	if name, ok := args["name"]; ok {
		details["name"] = name
	}
	operationID := ic.TaskID
	if operationID == "" {
		operationID = "direct-" + t.Name()
	}
	_, err := t.approvals.RecordAudit(approval.AuditRecord{
		ApprovalID: ic.ApprovalID, OperationID: operationID, TaskID: ic.TaskID,
		Requester: requester, Transport: ic.Transport, MCPSessionID: ic.MCPSessionID,
		SessionMode: ic.SessionMode, SessionIDSource: ic.SessionIDSource, RequestID: ic.RequestID,
		Details: details, Error: errorText,
	})
	if err != nil {
		return fmt.Errorf("record dynamic deployment audit: %w", err)
	}
	return nil
}
