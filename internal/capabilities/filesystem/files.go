package filesystem

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"localgate/internal/config"
	"localgate/internal/core"
	"localgate/internal/security"
)

type fsToolBase struct {
	ws config.Workspace
}

func NewTools(ws config.Workspace) []core.Tool {
	b := fsToolBase{ws: ws}
	return []core.Tool{
		readFileTool{fsToolBase: b},
		writeFileTool{fsToolBase: b},
		uploadFileTool{fsToolBase: b},
		listDirTool{fsToolBase: b},
		searchFilesTool{fsToolBase: b},
		newApplyPatchTool(ws),
	}
}

const uploadFileMaxDownloadBytes = 512 << 20

type readFileTool struct{ fsToolBase }

func (t readFileTool) Name() string { return "read_file" }
func (t readFileTool) Description() string {
	return "Read a file by absolute path from the workspace, or from the shared ~/.agents/skills directory"
}
func (t readFileTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "Absolute path"},
		},
		"required": []string{"path"},
	}
}
func (t readFileTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	p, _ := args["path"].(string)
	dec := validateReadPath(p, t.ws)
	if !dec.Allowed {
		return nil, errors.New("path not allowed: " + dec.Reason)
	}
	b, err := os.ReadFile(dec.Path)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": dec.Path, "content": string(b)}, nil
}

type writeFileTool struct{ fsToolBase }

func (t writeFileTool) Name() string { return "write_file" }
func (t writeFileTool) Description() string {
	return "Write text content to a file by absolute path inside the workspace; creates parent directories when needed"
}
func (t writeFileTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "Absolute path"},
			"content": map[string]any{"type": "string"},
		},
		"required": []string{"path", "content"},
	}
}
func (t writeFileTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	p, _ := args["path"].(string)
	content, _ := args["content"].(string)
	dec := security.ValidatePath(p, t.ws, security.Options{FollowSymlink: true, AllowNonExist: true})
	if !dec.Allowed {
		return nil, errors.New("path not allowed: " + dec.Reason)
	}
	if fi, err := os.Lstat(dec.Path); err == nil && (fi.Mode()&os.ModeSymlink) != 0 {
		return nil, errors.New("path not allowed: symlink")
	}
	if err := os.MkdirAll(filepath.Dir(dec.Path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(dec.Path, []byte(content), 0o644); err != nil {
		return nil, err
	}
	return map[string]any{"path": dec.Path, "bytes": len(content)}, nil
}

type uploadFileTool struct{ fsToolBase }

func (t uploadFileTool) Name() string { return "upload_file" }
func (t uploadFileTool) Description() string {
	return "Download a file from a provided download_url and save it to an absolute path inside the workspace. Use this when a host provides a temporary file URL, for example ChatGPT file input objects with file_id and download_url."
}
func (t uploadFileTool) ToolMetadata() map[string]any {
	return map[string]any{
		"openai/fileParams": []string{"file"},
	}
}
func (t uploadFileTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute target path inside the workspace.",
			},
			"file": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"description":          "File reference object. download_url is required. file_id, mime_type, and file_name are optional metadata.",
				"properties": map[string]any{
					"download_url": map[string]any{"type": "string"},
					"file_id":      map[string]any{"type": "string"},
					"mime_type":    map[string]any{"type": "string"},
					"file_name":    map[string]any{"type": "string"},
				},
				"required": []string{"download_url"},
			},
		},
		"required": []string{"path", "file"},
	}
}
func (t uploadFileTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	_ = ic
	p, _ := args["path"].(string)
	dec := security.ValidatePath(p, t.ws, security.Options{FollowSymlink: true, AllowNonExist: true})
	if !dec.Allowed {
		return nil, errors.New("path not allowed: " + dec.Reason)
	}
	if fi, err := os.Lstat(dec.Path); err == nil && (fi.Mode()&os.ModeSymlink) != 0 {
		return nil, errors.New("path not allowed: symlink")
	}

	file, _ := args["file"].(map[string]any)
	if file == nil {
		return nil, errors.New("file required")
	}
	downloadURL, _ := file["download_url"].(string)
	downloadURL = strings.TrimSpace(downloadURL)
	if downloadURL == "" {
		return nil, errors.New("file.download_url required")
	}
	if !strings.HasPrefix(downloadURL, "http://") && !strings.HasPrefix(downloadURL, "https://") {
		return nil, errors.New("file.download_url must be http or https")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download file: unexpected status %d", resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dec.Path), 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dec.Path), ".upload-file-*")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	written, err := io.Copy(tmp, io.LimitReader(resp.Body, uploadFileMaxDownloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("save downloaded file: %w", err)
	}
	if written > uploadFileMaxDownloadBytes {
		return nil, fmt.Errorf("download file exceeds %d bytes", uploadFileMaxDownloadBytes)
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmpPath, dec.Path); err != nil {
		return nil, err
	}
	fileID, _ := file["file_id"].(string)
	fileName, _ := file["file_name"].(string)
	mimeType, _ := file["mime_type"].(string)
	return map[string]any{
		"path":      dec.Path,
		"bytes":     written,
		"file_id":   fileID,
		"file_name": fileName,
		"mime_type": mimeType,
	}, nil
}

type listDirTool struct{ fsToolBase }

func (t listDirTool) Name() string { return "list_directory" }
func (t listDirTool) Description() string {
	return "List files and directories under an absolute path inside the workspace, optionally recursively"
}
func (t listDirTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"path":      map[string]any{"type": "string", "description": "Absolute path"},
			"recursive": map[string]any{"type": "boolean"},
		},
		"required": []string{"path"},
	}
}
func (t listDirTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	p, _ := args["path"].(string)
	recursive, _ := args["recursive"].(bool)
	dec := security.ValidatePath(p, t.ws, security.Options{FollowSymlink: true})
	if !dec.Allowed {
		return nil, errors.New("path not allowed: " + dec.Reason)
	}
	var out []map[string]any
	if !recursive {
		entries, err := os.ReadDir(dec.Path)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			info, _ := e.Info()
			out = append(out, map[string]any{
				"name":  e.Name(),
				"isDir": e.IsDir(),
				"size":  sizeOf(info),
			})
		}
	} else {
		filepath.WalkDir(dec.Path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(dec.Path, p)
			if rel == "." {
				return nil
			}
			info, _ := d.Info()
			out = append(out, map[string]any{
				"path":  rel,
				"isDir": d.IsDir(),
				"size":  sizeOf(info),
			})
			return nil
		})
	}
	return map[string]any{"entries": out}, nil
}

type searchFilesTool struct{ fsToolBase }

func (t searchFilesTool) Name() string { return "search_files" }
func (t searchFilesTool) Description() string {
	return "Find files under an absolute workspace root using a glob pattern and return relative matches"
}
func (t searchFilesTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"root":        map[string]any{"type": "string", "description": "Absolute path"},
			"glob":        map[string]any{"type": "string"},
			"max_results": map[string]any{"type": "integer"},
		},
		"required": []string{"root"},
	}
}
func (t searchFilesTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	root, _ := args["root"].(string)
	pattern, _ := args["glob"].(string)
	if pattern == "" {
		pattern = "*"
	}
	maxResults := 200
	if v := intFromAny(args["max_results"]); v > 0 {
		maxResults = v
	}
	dec := security.ValidatePath(root, t.ws, security.Options{FollowSymlink: true})
	if !dec.Allowed {
		return nil, errors.New("path not allowed: " + dec.Reason)
	}
	var results []string
	filepath.WalkDir(dec.Path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		match, _ := path.Match(pattern, name)
		if match {
			results = append(results, p)
			if len(results) >= maxResults {
				return errors.New("max_results reached")
			}
		}
		return nil
	})
	for i := range results {
		results[i] = strings.TrimPrefix(results[i], dec.Path+string(os.PathSeparator))
	}
	return map[string]any{"matches": results}, nil
}

func intFromAny(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	default:
		return 0
	}
}

func sizeOf(info fs.FileInfo) int64 {
	if info == nil {
		return 0
	}
	return info.Size()
}

func validateReadPath(p string, ws config.Workspace) security.Decision {
	dec := security.ValidatePath(p, ws, security.Options{FollowSymlink: true})
	if dec.Allowed {
		return dec
	}
	sharedRoots := sharedSkillRoots()
	if len(sharedRoots) == 0 {
		return dec
	}
	wsWithSharedRead := ws
	wsWithSharedRead.Roots = append(append([]string(nil), ws.Roots...), sharedRoots...)
	return security.ValidatePath(p, wsWithSharedRead, security.Options{FollowSymlink: true})
}

func sharedSkillRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".agents", "skills"),
	}
}
