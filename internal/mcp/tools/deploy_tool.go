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
	"localgate/internal/config"
	"localgate/internal/core"
	"localgate/internal/deployment"
	"localgate/internal/dynamic"
	"localgate/internal/static"
)

var ErrUnsafeSourceRemoval = deployment.ErrUnsafeSourceRemoval

type DeployRequest struct {
	Type    string               `json:"type"`
	Name    string               `json:"name"`
	Source  string               `json:"source"`
	Runtime *dynamic.RuntimeSpec `json:"runtime,omitempty"`
	Options map[string]any       `json:"options"`
}

type DeployTool struct {
	Manager   *deployment.Manager
	Workspace config.Workspace
}

func NewDeployTool(service *static.Service, workspace config.Workspace, appBaseURL string) *DeployTool {
	return NewDeployToolWithDynamic(service, nil, workspace, appBaseURL)
}

func NewDeployToolWithDynamic(staticService *static.Service, dynamicService *dynamic.Service, workspace config.Workspace, appBaseURL string) *DeployTool {
	return NewDeployToolWithManager(deployment.NewManager(staticService, dynamicService, appBaseURL), workspace)
}

func NewDeployToolWithManager(manager *deployment.Manager, workspace config.Workspace) *DeployTool {
	return &DeployTool{Manager: manager, Workspace: workspace}
}

func (t *DeployTool) Name() string {
	return "deploy_tool"
}

func (t *DeployTool) Description() string {
	return "Deploy a managed static site or long-running dynamic web application from an allowed workspace directory."
}

func (t *DeployTool) InputSchema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"type", "name", "source"},
		"properties": map[string]any{
			"type": map[string]any{
				"type": "string",
				"enum": []any{"static", "dynamic"},
			},
			"name": map[string]any{
				"type":      "string",
				"minLength": 1,
			},
			"source": map[string]any{
				"type":      "string",
				"minLength": 1,
			},
			"runtime": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"argv": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"cwd":  map[string]any{"type": "string"},
					"env":  map[string]any{"type": "object"},
				},
			},
			"options": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"overwrite":          map[string]any{"type": "boolean"},
					"preserve_source":    map[string]any{"type": "boolean"},
					"access":             map[string]any{"type": "string", "enum": []any{"private", "public"}},
					"health_path":        map[string]any{"type": "string"},
					"startup_timeout_ms": map[string]any{"type": "integer"},
					"restart_policy":     map[string]any{"type": "string", "enum": []any{"never", "on-failure"}},
				},
			},
		},
	}
}

func (t *DeployTool) OutputSchema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"status", "type", "site", "url", "source_preserved"},
		"properties": map[string]any{
			"status":           map[string]any{"type": "string", "enum": []any{"deployed", "deployed_with_warning"}},
			"type":             map[string]any{"type": "string", "enum": []any{"static", "dynamic"}},
			"site":             map[string]any{"type": "string"},
			"url":              map[string]any{"type": "string"},
			"file_count":       map[string]any{"type": "integer"},
			"total_bytes":      map[string]any{"type": "integer"},
			"source_preserved": map[string]any{"type": "boolean"},
			"hostname":         map[string]any{"type": "string"},
			"access":           map[string]any{"type": "string", "enum": []any{"private", "public"}},
			"release_id":       map[string]any{"type": "string"},
			"runtime_status":   map[string]any{"type": "string"},
			"health":           map[string]any{"type": "string"},
			"cleanup_pending":  map[string]any{"type": "boolean"},
			"warning_code":     map[string]any{"type": "string"},
			"warning":          map[string]any{"type": "string"},
		},
	}
}

func (t *DeployTool) Execute(ctx context.Context, input json.RawMessage) (any, error) {
	if t == nil || t.Manager == nil {
		return nil, errors.New("deployment service is not available")
	}
	request, err := DecodeDeployRequest(input)
	if err != nil {
		return nil, err
	}
	options := deployment.DeployOptions{PreserveSource: true}
	if request.Options != nil {
		options.Overwrite, _ = request.Options["overwrite"].(bool)
		options.Access, _ = request.Options["access"].(string)
		if value, ok := request.Options["preserve_source"].(bool); ok {
			options.PreserveSource = value
		}
		options.HealthPath, _ = request.Options["health_path"].(string)
		options.RestartPolicy, _ = request.Options["restart_policy"].(string)
		switch value := request.Options["startup_timeout_ms"].(type) {
		case int:
			options.StartupTimeoutMS = value
		case float64:
			options.StartupTimeoutMS = int(value)
		}
	}
	result, err := t.Manager.Deploy(ctx, deployment.DeployRequest{
		Type: request.Type, Name: request.Name, Source: request.Source,
		Workspace: t.Workspace, Runtime: request.Runtime, Options: options,
	})
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"status": result.Status, "type": result.Type, "site": result.Site, "url": result.URL,
		"access":     result.Access,
		"file_count": result.FileCount, "total_bytes": result.TotalBytes,
		"source_preserved": result.SourcePreserved,
	}
	if result.Hostname != "" {
		out["hostname"] = result.Hostname
	}
	if result.ReleaseID != "" {
		out["release_id"] = result.ReleaseID
	}
	if result.RuntimeStatus != "" {
		out["runtime_status"] = result.RuntimeStatus
	}
	if result.Health != "" {
		out["health"] = result.Health
	}
	if result.CleanupPending {
		out["cleanup_pending"] = true
	}
	if result.WarningCode != "" {
		out["warning_code"] = result.WarningCode
	}
	if result.Warning != "" {
		out["warning"] = result.Warning
	}
	return out, nil
}

func DecodeDeployRequest(input json.RawMessage) (DeployRequest, error) {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	var request DeployRequest
	if err := decoder.Decode(&request); err != nil {
		return DeployRequest{}, fmt.Errorf("decode deploy request: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return DeployRequest{}, errors.New("deploy request must contain exactly one JSON value")
		}
		return DeployRequest{}, fmt.Errorf("decode trailing deploy request data: %w", err)
	}
	return request, nil
}

// CoreDeployTool exposes the same deployment implementation through LocalGate's
// primary tool registry, which is the source of truth for standard MCP /mcp.
type CoreDeployTool struct {
	inner           *DeployTool
	approvals       *approval.Service
	requireApproval bool
}

func NewCoreDeployTool(service *static.Service, workspace config.Workspace, appBaseURL string, approvals *approval.Service, requireApproval bool) *CoreDeployTool {
	return NewCoreDeployToolWithDynamic(service, nil, workspace, appBaseURL, approvals, requireApproval)
}

func NewCoreDeployToolWithDynamic(staticService *static.Service, dynamicService *dynamic.Service, workspace config.Workspace, appBaseURL string, approvals *approval.Service, requireApproval bool) *CoreDeployTool {
	return NewCoreDeployToolWithManager(deployment.NewManager(staticService, dynamicService, appBaseURL), workspace, approvals, requireApproval)
}

func NewCoreDeployToolWithManager(manager *deployment.Manager, workspace config.Workspace, approvals *approval.Service, requireApproval bool) *CoreDeployTool {
	return &CoreDeployTool{inner: NewDeployToolWithManager(manager, workspace), approvals: approvals, requireApproval: requireApproval}
}

func (t *CoreDeployTool) Name() string {
	return t.inner.Name()
}

func (t *CoreDeployTool) Description() string {
	return t.inner.Description()
}

func (t *CoreDeployTool) Schema() any {
	return t.inner.InputSchema()
}

func (t *CoreDeployTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	if t.requireApproval && !ic.Authorized {
		return nil, errors.New("deployment requires approval")
	}
	input, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("encode deploy arguments: %w", err)
	}

	requester := strings.TrimSpace(ic.Requester)
	if requester == "" {
		requester = "mcp"
	}
	details := coreDeployAuditDetails(args)
	operationID := ic.TaskID
	if operationID == "" {
		operationID = "direct-deploy"
	}
	if err := t.recordAudit(approval.AuditRecord{
		ApprovalID:      ic.ApprovalID,
		OperationID:     operationID,
		TaskID:          ic.TaskID,
		Action:          "execution_started",
		Tool:            t.Name(),
		WorkspaceID:     ic.WorkspaceID,
		Requester:       requester,
		Transport:       ic.Transport,
		MCPSessionID:    ic.MCPSessionID,
		SessionMode:     ic.SessionMode,
		SessionIDSource: ic.SessionIDSource,
		RequestID:       ic.RequestID,
		Details:         details,
	}); err != nil {
		return nil, err
	}

	if fmt.Sprint(args["type"]) == deployment.TypeDynamic {
		ctx = dynamic.WithOperation(ctx, dynamic.Operation{
			ApprovalID: ic.ApprovalID, TaskID: ic.TaskID, Requester: requester, Tool: t.Name(),
			Transport: ic.Transport, MCPSessionID: ic.MCPSessionID, SessionMode: ic.SessionMode,
			SessionIDSource: ic.SessionIDSource, RequestID: ic.RequestID,
		})
	}
	result, executeErr := t.inner.Execute(ctx, input)
	action := "execution_succeeded"
	if executeErr != nil {
		action = "execution_failed"
		if errors.Is(executeErr, context.Canceled) || errors.Is(executeErr, context.DeadlineExceeded) {
			action = "execution_canceled"
		}
	}
	auditRecord := approval.AuditRecord{
		ApprovalID:      ic.ApprovalID,
		OperationID:     operationID,
		TaskID:          ic.TaskID,
		Action:          action,
		Tool:            t.Name(),
		WorkspaceID:     ic.WorkspaceID,
		Requester:       requester,
		Transport:       ic.Transport,
		MCPSessionID:    ic.MCPSessionID,
		SessionMode:     ic.SessionMode,
		SessionIDSource: ic.SessionIDSource,
		RequestID:       ic.RequestID,
		Details:         details,
	}
	if executeErr != nil {
		auditRecord.Error = executeErr.Error()
	}
	if auditErr := t.recordAudit(auditRecord); auditErr != nil {
		if executeErr != nil {
			return nil, errors.Join(executeErr, auditErr)
		}
		return nil, auditErr
	}
	if executeErr != nil {
		return nil, executeErr
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		return nil, errors.New("deploy tool returned an invalid result")
	}
	return resultMap, nil
}

func (t *CoreDeployTool) recordAudit(record approval.AuditRecord) error {
	if t == nil || t.approvals == nil {
		return errors.New("deployment audit service is not available")
	}
	if _, err := t.approvals.RecordAudit(record); err != nil {
		return fmt.Errorf("record deployment audit: %w", err)
	}
	return nil
}

func coreDeployAuditDetails(args map[string]any) map[string]any {
	details := map[string]any{
		"preserve_source": true,
	}
	for _, key := range []string{"type", "name", "source"} {
		if value, ok := args[key]; ok {
			details[key] = value
		}
	}
	if options, ok := args["options"].(map[string]any); ok {
		if overwrite, ok := options["overwrite"].(bool); ok {
			details["overwrite"] = overwrite
		}
		if preserveSource, ok := options["preserve_source"].(bool); ok {
			details["preserve_source"] = preserveSource
		}
		if access, ok := options["access"].(string); ok {
			details["access"] = access
		}
	}
	return details
}
