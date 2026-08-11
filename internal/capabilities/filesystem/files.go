package filesystem

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"localgate/internal/config"
	"localgate/internal/core"
	"localgate/internal/fileio"
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

const (
	readFileMaxBytes          = 8 << 20
	listDirectoryDefaultMax   = 1000
	listDirectoryHardMax      = 10000
	searchFilesDefaultMax     = 200
	searchFilesHardMax        = 5000
	searchFilesMaxPatternSize = 1024
)

var errResultLimitReached = errors.New("result limit reached")

type readFileTool struct{ fsToolBase }

func (t readFileTool) Name() string { return "read_file" }
func (t readFileTool) Description() string {
	return "Read a regular text file by absolute path from the workspace or shared ~/.agents/skills directory. Binary files and files over the read limit are rejected."
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
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	f, err := os.Open(dec.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("path must be a regular file")
	}
	if info.Size() > readFileMaxBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", readFileMaxBytes)
	}
	b, err := io.ReadAll(io.LimitReader(f, readFileMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > readFileMaxBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", readFileMaxBytes)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if looksBinaryFile(b) {
		return nil, errors.New("binary file is not supported by read_file")
	}
	return map[string]any{"path": dec.Path, "content": string(b), "bytes": len(b)}, nil
}

type writeFileTool struct{ fsToolBase }

func (t writeFileTool) Name() string { return "write_file" }
func (t writeFileTool) Description() string {
	return "Create or completely replace a text file by absolute path inside the workspace. Creates parent directories when needed; prefer apply_patch for partial edits to an existing file."
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
	if strings.IndexByte(content, 0) >= 0 {
		return nil, errors.New("binary content is not supported by write_file; use upload_file for binary files")
	}
	lexical := security.ValidatePath(p, t.ws, security.Options{AllowNonExist: true})
	if !lexical.Allowed {
		return nil, errors.New("path not allowed: " + lexical.Reason)
	}
	if fi, err := os.Lstat(lexical.Path); err == nil && (fi.Mode()&os.ModeSymlink) != 0 {
		return nil, errors.New("path not allowed: symlink")
	}
	dec := security.ValidatePath(lexical.Path, t.ws, security.Options{FollowSymlink: true, AllowNonExist: true})
	if !dec.Allowed {
		return nil, errors.New("path not allowed: " + dec.Reason)
	}
	if _, err := fileio.WriteFileAtomic(dec.Path, []byte(content), fileio.AtomicWriteOptions{
		Mode:          0o644,
		PreserveMode:  true,
		CreateParents: true,
	}); err != nil {
		return nil, err
	}
	return map[string]any{"path": dec.Path, "bytes": len(content)}, nil
}

type uploadFileTool struct {
	fsToolBase
	client *http.Client
}

func (t uploadFileTool) Name() string { return "upload_file" }
func (t uploadFileTool) Description() string {
	return "Copy a host-provided file into an absolute workspace path. The destination must include the filename; parent directories are created as needed and the source is not modified."
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
				"description": "Absolute destination path inside the workspace, including the filename. Parent directories are created automatically.",
			},
			"file": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"description":          "Downloadable file reference supplied by the host. LocalGate requires file.download_url; compatible runtimes may accept a local mounted path and convert it to this object before invocation. The source file is not modified.",
				"properties": map[string]any{
					"download_url": map[string]any{"type": "string", "description": "Temporary HTTP(S) URL used by LocalGate to read the source file."},
					"file_id":      map[string]any{"type": "string", "description": "Optional host-provided file identifier."},
					"mime_type":    map[string]any{"type": "string", "description": "Optional MIME type of the source file."},
					"file_name":    map[string]any{"type": "string", "description": "Optional original source filename."},
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
	lexical := security.ValidatePath(p, t.ws, security.Options{AllowNonExist: true})
	if !lexical.Allowed {
		return nil, errors.New("path not allowed: " + lexical.Reason)
	}
	if fi, err := os.Lstat(lexical.Path); err == nil && (fi.Mode()&os.ModeSymlink) != 0 {
		return nil, errors.New("path not allowed: symlink")
	}
	dec := security.ValidatePath(lexical.Path, t.ws, security.Options{FollowSymlink: true, AllowNonExist: true})
	if !dec.Allowed {
		return nil, errors.New("path not allowed: " + dec.Reason)
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
	remoteURL, err := parseRemoteURL(downloadURL)
	if err != nil {
		return nil, err
	}

	client := t.client
	if client == nil {
		if err := validateRemoteURL(ctx, remoteURL, net.DefaultResolver.LookupIPAddr); err != nil {
			return nil, fmt.Errorf("file.download_url rejected: %w", err)
		}
		client = newRemoteDownloadClient()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remoteURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download file: unexpected status %d", resp.StatusCode)
	}
	written, err := fileio.WriteReaderAtomic(dec.Path, resp.Body, fileio.AtomicWriteOptions{
		Mode:          0o644,
		PreserveMode:  true,
		CreateParents: true,
		MaxBytes:      uploadFileMaxDownloadBytes,
	})
	if err != nil {
		if errors.Is(err, fileio.ErrTooLarge) {
			return nil, fmt.Errorf("download file exceeds %d bytes", uploadFileMaxDownloadBytes)
		}
		return nil, fmt.Errorf("save downloaded file: %w", err)
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
			"path":        map[string]any{"type": "string", "description": "Absolute path"},
			"recursive":   map[string]any{"type": "boolean"},
			"max_results": map[string]any{"type": "integer", "description": "Maximum entries to return, up to 10000"},
		},
		"required": []string{"path"},
	}
}
func (t listDirTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	p, _ := args["path"].(string)
	recursive, _ := args["recursive"].(bool)
	maxResults := listDirectoryDefaultMax
	if value := intFromAny(args["max_results"]); value > 0 {
		maxResults = value
	}
	if maxResults > listDirectoryHardMax {
		return nil, fmt.Errorf("max_results exceeds %d", listDirectoryHardMax)
	}
	dec := security.ValidatePath(p, t.ws, security.Options{FollowSymlink: true})
	if !dec.Allowed {
		return nil, errors.New("path not allowed: " + dec.Reason)
	}
	out := make([]map[string]any, 0, minInt(maxResults, 128))
	truncated := false
	if !recursive {
		dir, err := os.Open(dec.Path)
		if err != nil {
			return nil, err
		}
		defer dir.Close()
		entries, err := dir.ReadDir(maxResults + 1)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if len(entries) > maxResults {
			entries = entries[:maxResults]
			truncated = true
		}
		for _, e := range entries {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
			info, err := e.Info()
			if err != nil {
				return nil, err
			}
			out = append(out, map[string]any{
				"name":  e.Name(),
				"isDir": e.IsDir(),
				"size":  sizeOf(info),
			})
		}
	} else {
		walkErr := filepath.WalkDir(dec.Path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			rel, _ := filepath.Rel(dec.Path, p)
			if rel == "." {
				return nil
			}
			if len(out) >= maxResults {
				truncated = true
				return errResultLimitReached
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			out = append(out, map[string]any{
				"path":  rel,
				"isDir": d.IsDir(),
				"size":  sizeOf(info),
			})
			return nil
		})
		if walkErr != nil && !errors.Is(walkErr, errResultLimitReached) {
			return nil, walkErr
		}
	}
	return map[string]any{"entries": out, "truncated": truncated, "max_results": maxResults}, nil
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
	if len(pattern) > searchFilesMaxPatternSize {
		return nil, fmt.Errorf("glob exceeds %d bytes", searchFilesMaxPatternSize)
	}
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, fmt.Errorf("invalid glob: %w", err)
	}
	maxResults := searchFilesDefaultMax
	if v := intFromAny(args["max_results"]); v > 0 {
		maxResults = v
	}
	if maxResults > searchFilesHardMax {
		return nil, fmt.Errorf("max_results exceeds %d", searchFilesHardMax)
	}
	dec := security.ValidatePath(root, t.ws, security.Options{FollowSymlink: true})
	if !dec.Allowed {
		return nil, errors.New("path not allowed: " + dec.Reason)
	}
	results := make([]string, 0, minInt(maxResults, 128))
	truncated := false
	walkErr := filepath.WalkDir(dec.Path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		name := d.Name()
		match, err := path.Match(pattern, name)
		if err != nil {
			return err
		}
		if match {
			if len(results) >= maxResults {
				truncated = true
				return errResultLimitReached
			}
			rel, err := filepath.Rel(dec.Path, p)
			if err != nil {
				return err
			}
			results = append(results, filepath.ToSlash(rel))
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errResultLimitReached) {
		return nil, walkErr
	}
	return map[string]any{"matches": results, "truncated": truncated, "max_results": maxResults}, nil
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

func looksBinaryFile(content []byte) bool {
	if len(content) == 0 {
		return false
	}
	sample := content
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	return bytes.IndexByte(sample, 0) >= 0 || !utf8.Valid(sample)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
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
