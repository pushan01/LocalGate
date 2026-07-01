package toolaccess

import (
	"context"
	"errors"
	"path"
	"strings"
	"time"

	"localgate/internal/core"
	service "localgate/internal/toolaccess"
)

type getTemporaryAccessTokenTool struct {
	baseURL string
	ttl     time.Duration
	svc     *service.Service
}

func NewGetTemporaryAccessTokenTool(baseURL string, ttl time.Duration, svc *service.Service) core.Tool {
	return getTemporaryAccessTokenTool{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		ttl:     ttl,
		svc:     svc,
	}
}

func (t getTemporaryAccessTokenTool) Name() string { return "get_temporary_access_token" }

func (t getTemporaryAccessTokenTool) Description() string {
	return "Create a short-lived HTTP access credential for the current workspace. Use it together with Authorization: Bearer to call LocalGate tool HTTP endpoints."
}

func (t getTemporaryAccessTokenTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
	}
}

func (t getTemporaryAccessTokenTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	_ = ctx
	_ = args
	if t.svc == nil {
		return nil, errors.New("tool access service is not enabled")
	}
	if t.baseURL == "" {
		return nil, errors.New("tool http base_url is not configured")
	}
	if t.ttl <= 0 {
		return nil, errors.New("tool access ttl must be positive")
	}
	workspaceID := strings.TrimSpace(ic.WorkspaceID)
	if workspaceID == "" {
		return nil, errors.New("workspace_id is required")
	}
	grant, err := t.svc.Issue(workspaceID, t.ttl)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"workspace_id":      workspaceID,
		"auth_value":        grant.AuthValue,
		"auth_header_name":  service.HeaderName,
		"base_url":          t.baseURL,
		"call_path_pattern": path.Clean("/tools/{tool}/call"),
		"expires_at":        grant.ExpiresAt.Format(time.RFC3339),
	}, nil
}
