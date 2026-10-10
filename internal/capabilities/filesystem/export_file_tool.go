package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"localgate/internal/config"
	"localgate/internal/core"
	"localgate/internal/fileexport"
	"localgate/internal/security"
)

type exportFileTool struct {
	ws      config.Root
	config  config.FileExports
	exports *fileexport.Service
}

func NewExportFileTool(ws config.Root, cfg config.FileExports, exports *fileexport.Service) core.Tool {
	return exportFileTool{ws: ws, config: cfg, exports: exports}
}

func (t exportFileTool) Name() string { return "export_file" }

func (t exportFileTool) Description() string {
	return "Export an existing file from the configured root to the current conversation as a short-lived downloadable MCP resource. The source must be a regular file at an absolute path inside the configured root. This transfers data outside LocalGate, may require approval, and does not modify the source file."
}

func (t exportFileTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute path of an existing regular file inside the configured root. Directories and symlinks are not allowed.",
			},
			"file_name": map[string]any{
				"type":        "string",
				"description": "Optional filename shown in the current conversation and used when downloading. It must be a plain filename without directory components.",
			},
			"ttl_seconds": map[string]any{
				"type":        "integer",
				"minimum":     60,
				"maximum":     t.config.MaxTTLSeconds,
				"description": fmt.Sprintf("Optional lifetime of the downloadable MCP resource in seconds. It must be between 60 and %d.", t.config.MaxTTLSeconds),
			},
		},
		"required": []string{"path"},
	}
}

func (t exportFileTool) OutputSchema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": true,
		"properties": map[string]any{
			"status":      map[string]any{"type": "string"},
			"task_id":     map[string]any{"type": "string"},
			"approval_id": map[string]any{"type": "string"},
			"result": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"status":     map[string]any{"type": "string"},
					"export_id":  map[string]any{"type": "string"},
					"file_name":  map[string]any{"type": "string"},
					"mime_type":  map[string]any{"type": "string"},
					"size":       map[string]any{"type": "integer"},
					"sha256":     map[string]any{"type": "string"},
					"expires_at": map[string]any{"type": "string"},
				},
				"required": []string{"status", "export_id", "file_name", "mime_type", "size", "sha256", "expires_at"},
			},
		},
		"required": []string{"status"},
	}
}

func (t exportFileTool) ToolAnnotations() map[string]any {
	return map[string]any{
		"readOnlyHint":    true,
		"destructiveHint": false,
		"idempotentHint":  false,
		"openWorldHint":   true,
	}
}

func (t exportFileTool) ResourceLinks(result map[string]any) []core.ResourceLink {
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

func (t exportFileTool) NativeContent(result map[string]any) []map[string]any {
	if t.exports == nil || result == nil {
		return nil
	}
	id, _ := result["export_id"].(string)
	return t.exports.NativeContent(id)
}

func (t exportFileTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	if t.exports == nil {
		return nil, errors.New("file export service is not enabled")
	}
	p, _ := args["path"].(string)
	lexical := security.ValidatePath(p, t.ws, security.Options{})
	if !lexical.Allowed {
		return nil, errors.New("path not allowed: " + lexical.Reason)
	}
	if fi, err := os.Lstat(lexical.Path); err != nil {
		return nil, err
	} else if fi.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("path not allowed: symlink")
	}
	dec := security.ValidatePath(lexical.Path, t.ws, security.Options{FollowSymlink: true})
	if !dec.Allowed {
		return nil, errors.New("path not allowed: " + dec.Reason)
	}

	f, err := os.Open(dec.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	infoBefore, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !infoBefore.Mode().IsRegular() {
		return nil, errors.New("path must be a regular file")
	}
	if infoBefore.Size() > t.config.MaxFileBytes {
		return nil, fmt.Errorf("file exceeds export limit of %d bytes", t.config.MaxFileBytes)
	}

	fileName := filepath.Base(dec.Path)
	if requested, _ := args["file_name"].(string); strings.TrimSpace(requested) != "" {
		fileName, err = validateExportFileName(requested)
		if err != nil {
			return nil, err
		}
	}

	mimeType, err := detectExportMIME(f, fileName)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	infoAfter, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if infoAfter.Size() != infoBefore.Size() || !infoAfter.ModTime().Equal(infoBefore.ModTime()) {
		return nil, errors.New("file changed while preparing export")
	}

	ttl := time.Duration(t.config.DefaultTTLSeconds) * time.Second
	if value, ok := integerArg(args["ttl_seconds"]); ok {
		if value < 60 || value > int64(t.config.MaxTTLSeconds) {
			return nil, fmt.Errorf("ttl_seconds must be between 60 and %d", t.config.MaxTTLSeconds)
		}
		ttl = time.Duration(value) * time.Second
	}
	grant, err := t.exports.Issue(fileexport.Grant{
		Path:            lexical.Path,
		FileName:        fileName,
		MIMEType:        mimeType,
		Size:            infoBefore.Size(),
		SHA256:          hex.EncodeToString(h.Sum(nil)),
		ModTimeUnixNano: infoBefore.ModTime().UnixNano(),
	}, ttl)
	if err != nil {
		return nil, err
	}
	_ = ctx
	_ = ic
	return map[string]any{
		"status":     "ready",
		"export_id":  grant.ID,
		"file_name":  grant.FileName,
		"mime_type":  grant.MIMEType,
		"size":       grant.Size,
		"sha256":     grant.SHA256,
		"expires_at": grant.ExpiresAt.Format(time.RFC3339),
	}, nil
}

func validateExportFileName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, 0) || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return "", errors.New("file_name must be a plain file name without directory components")
	}
	return name, nil
}

func detectExportMIME(f *os.File, fileName string) (string, error) {
	if value := mime.TypeByExtension(strings.ToLower(filepath.Ext(fileName))); value != "" {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
		return value, nil
	}
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return http.DetectContentType(buf[:n]), nil
}

func integerArg(value any) (int64, bool) {
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
