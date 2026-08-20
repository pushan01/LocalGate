package filesystem

import (
	"context"
	"errors"
	"path"
	"strings"
	"time"

	"localgate/internal/config"
	"localgate/internal/core"
	"localgate/internal/security"
	"localgate/internal/upload"
)

const (
	uploadTokenHeaderName = "X-LocalGate-Upload-Token"
	uploadTokenTTL        = 10 * time.Minute
)

type createUploadTokenTool struct {
	ws      config.Root
	baseURL string
	uploads *upload.Service
}

func NewCreateUploadTokenTool(ws config.Root, baseURL string, uploads *upload.Service) core.Tool {
	return createUploadTokenTool{
		ws:      ws,
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		uploads: uploads,
	}
}

func (t createUploadTokenTool) Name() string { return "create_upload_token" }

func (t createUploadTokenTool) Description() string {
	return "Create a 10-minute HTTP upload credential for one absolute workspace path. Use this when files need to move across sandboxes or machines and the caller cannot write the LocalGate workspace path directly. External clients can upload or overwrite the file via the server upload endpoint."
}

func (t createUploadTokenTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute target path inside the workspace. Use this for cross-sandbox or cross-machine file transfer when the caller needs to push a file into the LocalGate workspace.",
			},
		},
		"required": []string{"path"},
	}
}

func (t createUploadTokenTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	_ = ctx
	_ = ic

	if t.uploads == nil {
		return nil, errors.New("upload service is not enabled")
	}
	if t.baseURL == "" {
		return nil, errors.New("upload base_url is not configured")
	}
	p, _ := args["path"].(string)
	dec := security.ValidatePath(p, t.ws, security.Options{FollowSymlink: true, AllowNonExist: true})
	if !dec.Allowed {
		return nil, errors.New("path not allowed: " + dec.Reason)
	}

	grant, err := t.uploads.Issue(dec.Path, uploadTokenTTL)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"auth_value":       grant.Token,
		"expires_at":       grant.ExpiresAt.Format(time.RFC3339),
		"path":             grant.Path,
		"upload_url":       t.baseURL + path.Clean("/system/uploads"),
		"auth_header_name": uploadTokenHeaderName,
	}, nil
}
