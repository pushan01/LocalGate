package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"localgate/internal/core"
	"localgate/internal/deployment"
)

type SetDeploymentAccessTool struct{ manager *deployment.Manager }

func NewSetDeploymentAccessToolWithManager(manager *deployment.Manager) *SetDeploymentAccessTool {
	return &SetDeploymentAccessTool{manager: manager}
}

func (t *SetDeploymentAccessTool) Name() string { return "set_deployment_access" }

func (t *SetDeploymentAccessTool) Description() string {
	return "Change a static or dynamic deployment between private and public app domains."
}

func (t *SetDeploymentAccessTool) InputSchema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"type", "name", "access"},
		"properties": map[string]any{
			"type":   map[string]any{"type": "string", "enum": []any{"static", "dynamic"}},
			"name":   map[string]any{"type": "string", "minLength": 1},
			"access": map[string]any{"type": "string", "enum": []any{"private", "public"}},
		},
	}
}

func (t *SetDeploymentAccessTool) OutputSchema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"status", "type", "site", "access", "hostname", "url"},
		"properties": map[string]any{
			"status":   map[string]any{"type": "string", "enum": []any{"updated"}},
			"type":     map[string]any{"type": "string", "enum": []any{"static", "dynamic"}},
			"site":     map[string]any{"type": "string"},
			"access":   map[string]any{"type": "string", "enum": []any{"private", "public"}},
			"hostname": map[string]any{"type": "string"},
			"url":      map[string]any{"type": "string"},
		},
	}
}

func (t *SetDeploymentAccessTool) Execute(ctx context.Context, input json.RawMessage) (any, error) {
	if t == nil || t.manager == nil {
		return nil, errors.New("deployment service is not available")
	}
	var request struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		Access string `json:"access"`
	}
	if err := json.Unmarshal(input, &request); err != nil {
		return nil, err
	}
	view, err := t.manager.SetAccess(ctx, request.Type, request.Name, request.Access)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status": "updated", "type": view.Type, "site": view.Site,
		"access": view.Access, "hostname": view.Hostname, "url": view.URL,
	}, nil
}

type CoreSetDeploymentAccessTool struct {
	manager         *deployment.Manager
	requireApproval bool
}

func NewCoreSetDeploymentAccessToolWithManager(manager *deployment.Manager, requireApproval bool) *CoreSetDeploymentAccessTool {
	return &CoreSetDeploymentAccessTool{manager: manager, requireApproval: requireApproval}
}

func (t *CoreSetDeploymentAccessTool) Name() string { return "set_deployment_access" }

func (t *CoreSetDeploymentAccessTool) Description() string {
	return "Change a static or dynamic deployment between private and public app domains."
}

func (t *CoreSetDeploymentAccessTool) Schema() any {
	return NewSetDeploymentAccessToolWithManager(t.manager).InputSchema()
}

func (t *CoreSetDeploymentAccessTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	if t == nil || t.manager == nil {
		return nil, errors.New("deployment service is not available")
	}
	if t.requireApproval && !ic.Authorized {
		return nil, errors.New("deployment access change requires approval")
	}
	typeName := fmt.Sprint(args["type"])
	name := fmt.Sprint(args["name"])
	access := fmt.Sprint(args["access"])
	view, err := t.manager.SetAccess(ctx, typeName, name, access)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status":   "updated",
		"type":     view.Type,
		"site":     view.Site,
		"access":   view.Access,
		"hostname": view.Hostname,
		"url":      view.URL,
	}, nil
}
