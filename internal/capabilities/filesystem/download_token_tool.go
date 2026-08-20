package filesystem

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"localgate/internal/config"
	"localgate/internal/core"
	"localgate/internal/download"
	"localgate/internal/security"
)

const downloadTokenTTL = 10 * time.Minute

type createDownloadTokenTool struct {
	ws        config.Root
	baseURL   string
	downloads *download.Service
}

func NewCreateDownloadTokenTool(ws config.Root, baseURL string, downloads *download.Service) core.Tool {
	return createDownloadTokenTool{
		ws:        ws,
		baseURL:   strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		downloads: downloads,
	}
}

func (t createDownloadTokenTool) Name() string { return "create_download_token" }

func (t createDownloadTokenTool) Description() string {
	return "Create a 10-minute HTTP download credential for one existing absolute workspace file. Use this when files need to move across sandboxes or machines and the caller cannot read the path directly. If global HTTP bearer auth is enabled, the download request still needs Authorization: Bearer."
}

func (t createDownloadTokenTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute file path inside the workspace. Use this for cross-sandbox or cross-machine file transfer when the caller needs a temporary download URL.",
			},
		},
		"required": []string{"path"},
	}
}

func (t createDownloadTokenTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	_ = ctx
	_ = ic

	if t.downloads == nil {
		return nil, errors.New("download service is not enabled")
	}
	if t.baseURL == "" {
		return nil, errors.New("download base_url is not configured")
	}
	p, _ := args["path"].(string)
	dec := security.ValidatePath(p, t.ws, security.Options{FollowSymlink: true})
	if !dec.Allowed {
		return nil, errors.New("path not allowed: " + dec.Reason)
	}
	info, err := os.Stat(dec.Path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("path must be a file")
	}

	grant, err := t.downloads.Issue(dec.Path, downloadTokenTTL)
	if err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Set("token", grant.Token)

	return map[string]any{
		"auth_value":     grant.Token,
		"expires_at":     grant.ExpiresAt.Format(time.RFC3339),
		"path":           grant.Path,
		"download_url":   t.baseURL + path.Clean("/system/downloads") + "?" + q.Encode(),
		"auth_query_key": "token",
	}, nil
}
