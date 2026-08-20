package subagentcap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"localgate/internal/config"
	"localgate/internal/core"
	"localgate/internal/fileexport"
	runtime "localgate/internal/subagent"
)

type ArtifactGetTool struct {
	manager *runtime.Manager
	config  config.FileExports
	exports *fileexport.Service
}

func NewArtifactGetTool(manager *runtime.Manager, cfg config.FileExports, exports *fileexport.Service) core.Tool {
	return ArtifactGetTool{manager: manager, config: cfg, exports: exports}
}

func (ArtifactGetTool) Name() string { return "subagent_artifact_get" }

func (ArtifactGetTool) Description() string {
	return "Return one persisted subagent artifact as a short-lived LocalGate download/resource link. The artifact must belong to the requested sg_* and is revalidated by size and SHA-256 before export."
}

func (t ArtifactGetTool) Schema() any {
	return objectSchema(map[string]any{
		"subagent_id": map[string]any{
			"type":        "string",
			"description": "Stable sg_* id that owns the artifact.",
		},
		"artifact_id": map[string]any{
			"type":        "string",
			"description": "Artifact id returned by subagent_poll, subagent_wait, subagent_get, or subagent_list.",
		},
		"ttl_seconds": map[string]any{
			"type":        "integer",
			"minimum":     60,
			"maximum":     t.config.MaxTTLSeconds,
			"description": fmt.Sprintf("Optional download lifetime in seconds, between 60 and %d.", t.config.MaxTTLSeconds),
		},
	}, []string{"subagent_id", "artifact_id"})
}

func (t ArtifactGetTool) OutputSchema() any {
	return objectSchema(map[string]any{
		"status":      map[string]any{"type": "string"},
		"subagent_id": map[string]any{"type": "string"},
		"artifact_id": map[string]any{"type": "string"},
		"type":        map[string]any{"type": "string"},
		"file_name":   map[string]any{"type": "string"},
		"mime_type":   map[string]any{"type": "string"},
		"size":        map[string]any{"type": "integer"},
		"sha256":      map[string]any{"type": "string"},
		"width":       map[string]any{"type": "integer"},
		"height":      map[string]any{"type": "integer"},
		"alt":         map[string]any{"type": "string"},
		"export_id":   map[string]any{"type": "string"},
		"expires_at":  map[string]any{"type": "string"},
		"url":         map[string]any{"type": "string"},
	}, []string{"status", "subagent_id", "artifact_id", "type", "file_name", "mime_type", "size", "sha256", "export_id", "expires_at", "url"})
}

func (ArtifactGetTool) ToolAnnotations() map[string]any {
	return map[string]any{
		"readOnlyHint":    true,
		"destructiveHint": false,
		"idempotentHint":  false,
		"openWorldHint":   true,
	}
}

func (t ArtifactGetTool) ResourceLinks(result map[string]any) []core.ResourceLink {
	if t.exports == nil || result == nil {
		return nil
	}
	id, _ := result["export_id"].(string)
	link, err := t.exports.Link(id)
	if err != nil {
		return nil
	}
	return []core.ResourceLink{{
		URI:         link.URI,
		Name:        link.Name,
		Description: link.Description,
		MIMEType:    link.MIMEType,
		Size:        link.Size,
	}}
}

func (t ArtifactGetTool) Execute(_ context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	if t.manager == nil {
		return nil, errors.New("subagent runtime is not enabled")
	}
	if t.exports == nil || !t.config.Enabled {
		return nil, errors.New("file export service is not enabled")
	}
	subagentID := strings.TrimSpace(stringArg(args, "subagent_id"))
	if err := requireText("subagent_id", subagentID, 128); err != nil {
		return nil, err
	}
	artifactID := strings.TrimSpace(stringArg(args, "artifact_id"))
	if err := requireText("artifact_id", artifactID, 128); err != nil {
		return nil, err
	}
	agent, err := t.manager.Get(ic.WorkspaceID, subagentID)
	if err != nil {
		return nil, err
	}
	artifact, ok := findArtifact(agent.Artifacts, artifactID)
	if !ok {
		return nil, errors.New("subagent artifact not found")
	}
	info, digest, err := validateArtifactFile(artifact, t.config.MaxFileBytes)
	if err != nil {
		return nil, err
	}
	ttl := time.Duration(t.config.DefaultTTLSeconds) * time.Second
	if value, ok := artifactTTLArg(args["ttl_seconds"]); ok {
		if value < 60 || value > int64(t.config.MaxTTLSeconds) {
			return nil, fmt.Errorf("ttl_seconds must be between 60 and %d", t.config.MaxTTLSeconds)
		}
		ttl = time.Duration(value) * time.Second
	}
	grant, err := t.exports.Issue(fileexport.Grant{
		WorkspaceID:     ic.WorkspaceID,
		Path:            artifact.Path,
		FileName:        artifact.FileName,
		MIMEType:        artifact.MIMEType,
		Size:            info.Size(),
		SHA256:          digest,
		ModTimeUnixNano: info.ModTime().UnixNano(),
		InternalSource:  true,
	}, ttl)
	if err != nil {
		return nil, err
	}
	link, err := t.exports.Link(grant.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status":      "ready",
		"subagent_id": subagentID,
		"artifact_id": artifact.ID,
		"type":        artifact.Type,
		"file_name":   artifact.FileName,
		"mime_type":   artifact.MIMEType,
		"size":        artifact.Size,
		"sha256":      artifact.SHA256,
		"width":       artifact.Width,
		"height":      artifact.Height,
		"alt":         artifact.Alt,
		"export_id":   grant.ID,
		"expires_at":  grant.ExpiresAt.Format(time.RFC3339),
		"url":         link.URI,
	}, nil
}

func findArtifact(artifacts []runtime.Artifact, id string) (runtime.Artifact, bool) {
	for _, artifact := range artifacts {
		if artifact.ID == id {
			return artifact, true
		}
	}
	return runtime.Artifact{}, false
}

func validateArtifactFile(artifact runtime.Artifact, maxBytes int64) (os.FileInfo, string, error) {
	if strings.TrimSpace(artifact.Path) == "" || !filepath.IsAbs(artifact.Path) {
		return nil, "", errors.New("subagent artifact path is invalid")
	}
	if strings.TrimSpace(artifact.FileName) == "" || filepath.Base(artifact.FileName) != artifact.FileName {
		return nil, "", errors.New("subagent artifact file name is invalid")
	}
	if artifact.Size <= 0 || (maxBytes > 0 && artifact.Size > maxBytes) {
		return nil, "", errors.New("subagent artifact exceeds file export limits")
	}
	if info, err := os.Lstat(artifact.Path); err != nil {
		return nil, "", fmt.Errorf("stat subagent artifact: %w", err)
	} else if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, "", errors.New("subagent artifact must be a regular file")
	}
	f, err := os.Open(artifact.Path)
	if err != nil {
		return nil, "", fmt.Errorf("open subagent artifact: %w", err)
	}
	defer f.Close()
	infoBefore, err := f.Stat()
	if err != nil {
		return nil, "", err
	}
	if !infoBefore.Mode().IsRegular() || infoBefore.Size() != artifact.Size {
		return nil, "", errors.New("subagent artifact file metadata mismatch")
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, "", fmt.Errorf("read subagent artifact: %w", err)
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(digest, strings.TrimSpace(artifact.SHA256)) {
		return nil, "", errors.New("subagent artifact sha256 mismatch")
	}
	infoAfter, err := f.Stat()
	if err != nil {
		return nil, "", err
	}
	if infoAfter.Size() != infoBefore.Size() || !infoAfter.ModTime().Equal(infoBefore.ModTime()) {
		return nil, "", errors.New("subagent artifact changed while preparing export")
	}
	return infoAfter, digest, nil
}

func artifactTTLArg(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		if v == float64(int64(v)) {
			return int64(v), true
		}
	}
	return 0, false
}
