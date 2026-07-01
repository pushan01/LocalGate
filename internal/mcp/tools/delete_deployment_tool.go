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
	"localgate/internal/static"
)

type DeleteDeploymentRequest struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type DeleteDeploymentTool struct{ Manager *deployment.Manager }

func NewDeleteDeploymentTool(service *static.Service) *DeleteDeploymentTool {
	return NewDeleteDeploymentToolWithDynamic(service, nil)
}
func NewDeleteDeploymentToolWithDynamic(staticService *static.Service, dynamicService *dynamic.Service) *DeleteDeploymentTool {
	return NewDeleteDeploymentToolWithManager(deployment.NewManager(staticService, dynamicService, ""))
}
func NewDeleteDeploymentToolWithManager(manager *deployment.Manager) *DeleteDeploymentTool {
	return &DeleteDeploymentTool{Manager: manager}
}

func (t *DeleteDeploymentTool) Name() string {
	return "delete_deployment"
}

func (t *DeleteDeploymentTool) Description() string {
	return "Delete a managed static or dynamic deployment and remove its published files."
}

func (t *DeleteDeploymentTool) InputSchema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"type", "name"},
		"properties": map[string]any{
			"type": map[string]any{
				"type": "string",
				"enum": []any{"static", "dynamic"},
			},
			"name": map[string]any{
				"type":      "string",
				"minLength": 1,
			},
		},
	}
}

func (t *DeleteDeploymentTool) OutputSchema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"status", "type", "site"},
		"properties": map[string]any{
			"status": map[string]any{"type": "string", "enum": []any{"deleted"}},
			"type":   map[string]any{"type": "string", "enum": []any{"static", "dynamic"}},
			"site":   map[string]any{"type": "string"},
		},
	}
}

func (t *DeleteDeploymentTool) Execute(ctx context.Context, input json.RawMessage) (any, error) {
	if t == nil || t.Manager == nil {
		return nil, errors.New("deployment service is not available")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	request, err := DecodeDeleteDeploymentRequest(input)
	if err != nil {
		return nil, err
	}
	if err := t.Manager.Delete(ctx, request.Type, request.Name); err != nil {
		return nil, err
	}
	return map[string]any{"status": "deleted", "type": request.Type, "site": request.Name}, nil
}

func DecodeDeleteDeploymentRequest(input json.RawMessage) (DeleteDeploymentRequest, error) {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	var request DeleteDeploymentRequest
	if err := decoder.Decode(&request); err != nil {
		return DeleteDeploymentRequest{}, fmt.Errorf("decode delete deployment request: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return DeleteDeploymentRequest{}, errors.New("delete deployment request must contain exactly one JSON value")
		}
		return DeleteDeploymentRequest{}, fmt.Errorf("decode trailing delete deployment request data: %w", err)
	}
	return request, nil
}

type CoreDeleteDeploymentTool struct {
	inner           *DeleteDeploymentTool
	approvals       *approval.Service
	requireApproval bool
}

func NewCoreDeleteDeploymentTool(service *static.Service, approvals *approval.Service, requireApproval bool) *CoreDeleteDeploymentTool {
	return NewCoreDeleteDeploymentToolWithDynamic(service, nil, approvals, requireApproval)
}

func NewCoreDeleteDeploymentToolWithDynamic(staticService *static.Service, dynamicService *dynamic.Service, approvals *approval.Service, requireApproval bool) *CoreDeleteDeploymentTool {
	return NewCoreDeleteDeploymentToolWithManager(deployment.NewManager(staticService, dynamicService, ""), approvals, requireApproval)
}
func NewCoreDeleteDeploymentToolWithManager(manager *deployment.Manager, approvals *approval.Service, requireApproval bool) *CoreDeleteDeploymentTool {
	return &CoreDeleteDeploymentTool{inner: NewDeleteDeploymentToolWithManager(manager), approvals: approvals, requireApproval: requireApproval}
}

func (t *CoreDeleteDeploymentTool) Name() string {
	return t.inner.Name()
}

func (t *CoreDeleteDeploymentTool) Description() string {
	return t.inner.Description()
}

func (t *CoreDeleteDeploymentTool) Schema() any {
	return t.inner.InputSchema()
}

func (t *CoreDeleteDeploymentTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	if t.requireApproval && !ic.Authorized {
		return nil, errors.New("deployment deletion requires approval")
	}
	input, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("encode delete deployment arguments: %w", err)
	}
	requester := strings.TrimSpace(ic.Requester)
	if requester == "" {
		requester = "mcp"
	}
	details := map[string]any{}
	for _, key := range []string{"type", "name"} {
		if value, ok := args[key]; ok {
			details[key] = value
		}
	}
	operationID := ic.TaskID
	if operationID == "" {
		operationID = "direct-delete-deployment"
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
		return nil, errors.New("delete deployment tool returned an invalid result")
	}
	return resultMap, nil
}

func (t *CoreDeleteDeploymentTool) recordAudit(record approval.AuditRecord) error {
	if t == nil || t.approvals == nil {
		return errors.New("deployment audit service is not available")
	}
	if _, err := t.approvals.RecordAudit(record); err != nil {
		return fmt.Errorf("record deployment deletion audit: %w", err)
	}
	return nil
}
