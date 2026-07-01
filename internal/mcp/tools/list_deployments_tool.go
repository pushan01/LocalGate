package tools

import (
	"context"
	"encoding/json"
	"errors"

	"localgate/internal/core"
	"localgate/internal/deployment"
	"localgate/internal/dynamic"
	"localgate/internal/static"
)

type ListDeploymentsTool struct{ Manager *deployment.Manager }

func NewListDeploymentsTool(service *static.Service, baseURL string) *ListDeploymentsTool {
	return NewListDeploymentsToolWithDynamic(service, nil, baseURL)
}
func NewListDeploymentsToolWithDynamic(staticService *static.Service, dynamicService *dynamic.Service, baseURL string) *ListDeploymentsTool {
	return NewListDeploymentsToolWithManager(deployment.NewManager(staticService, dynamicService, baseURL))
}
func NewListDeploymentsToolWithManager(manager *deployment.Manager) *ListDeploymentsTool {
	return &ListDeploymentsTool{Manager: manager}
}

func (t *ListDeploymentsTool) Name() string {
	return "list_deployments"
}

func (t *ListDeploymentsTool) Description() string {
	return "List managed static and dynamic deployments and their public URLs."
}

func (t *ListDeploymentsTool) InputSchema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{},
	}
}

func (t *ListDeploymentsTool) OutputSchema() any {
	itemSchema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"type", "site", "url", "status", "created_at", "updated_at"},
		"properties": map[string]any{
			"type":          map[string]any{"type": "string", "enum": []any{"static", "dynamic"}},
			"site":          map[string]any{"type": "string"},
			"url":           map[string]any{"type": "string"},
			"status":        map[string]any{"type": "string"},
			"index":         map[string]any{"type": "string"},
			"spa":           map[string]any{"type": "boolean"},
			"hostname":      map[string]any{"type": "string"},
			"health":        map[string]any{"type": "string"},
			"release_id":    map[string]any{"type": "string"},
			"restart_count": map[string]any{"type": "integer"},
			"created_at":    map[string]any{"type": "string"},
			"updated_at":    map[string]any{"type": "string"},
		},
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"status", "count", "deployments"},
		"properties": map[string]any{
			"status":      map[string]any{"type": "string", "enum": []any{"ok"}},
			"count":       map[string]any{"type": "integer"},
			"deployments": map[string]any{"type": "array", "items": itemSchema},
		},
	}
}

func (t *ListDeploymentsTool) Execute(ctx context.Context, input json.RawMessage) (any, error) {
	if t == nil || t.Manager == nil {
		return nil, errors.New("deployment service is not available")
	}
	if len(input) > 0 && string(input) != "null" && string(input) != "{}" {
		var args map[string]any
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		if len(args) != 0 {
			return nil, errors.New("list deployments does not accept arguments")
		}
	}
	items, err := t.Manager.List(ctx)
	if err != nil {
		return nil, err
	}
	deployments := make([]map[string]any, 0, len(items))
	for _, item := range items {
		entry := map[string]any{
			"type": item.Type, "site": item.Site, "url": item.URL, "status": item.Status,
			"created_at": item.CreatedAt, "updated_at": item.UpdatedAt,
		}
		if item.Type == deployment.TypeStatic {
			entry["index"] = item.Index
			entry["spa"] = item.SPA
		} else {
			entry["hostname"] = item.Hostname
			entry["health"] = item.Health
			entry["release_id"] = item.ReleaseID
			entry["restart_count"] = item.RestartCount
			if item.CleanupPending {
				entry["cleanup_pending"] = true
			}
			if item.LastWarning != "" {
				entry["last_warning"] = item.LastWarning
			}
		}
		deployments = append(deployments, entry)
	}
	return map[string]any{"status": "ok", "count": len(deployments), "deployments": deployments}, nil
}

type CoreListDeploymentsTool struct {
	inner *ListDeploymentsTool
}

func NewCoreListDeploymentsTool(service *static.Service, baseURL string) *CoreListDeploymentsTool {
	return NewCoreListDeploymentsToolWithDynamic(service, nil, baseURL)
}

func NewCoreListDeploymentsToolWithDynamic(staticService *static.Service, dynamicService *dynamic.Service, baseURL string) *CoreListDeploymentsTool {
	return NewCoreListDeploymentsToolWithManager(deployment.NewManager(staticService, dynamicService, baseURL))
}
func NewCoreListDeploymentsToolWithManager(manager *deployment.Manager) *CoreListDeploymentsTool {
	return &CoreListDeploymentsTool{inner: NewListDeploymentsToolWithManager(manager)}
}

func (t *CoreListDeploymentsTool) Name() string {
	return t.inner.Name()
}

func (t *CoreListDeploymentsTool) Description() string {
	return t.inner.Description()
}

func (t *CoreListDeploymentsTool) Schema() any {
	return t.inner.InputSchema()
}

func (t *CoreListDeploymentsTool) Execute(ctx context.Context, _ core.InvokeContext, args map[string]any) (map[string]any, error) {
	input, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	result, err := t.inner.Execute(ctx, input)
	if err != nil {
		return nil, err
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		return nil, errors.New("list deployments tool returned an invalid result")
	}
	return resultMap, nil
}
