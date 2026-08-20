package filesystem

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"localgate/internal/config"
	"localgate/internal/core"
	"localgate/internal/download"
	"localgate/internal/upload"
)

func TestReadWriteList(t *testing.T) {
	dir := t.TempDir()
	ws := config.Root{Path: dir}
	tools := NewTools(ws)
	var read, write, list core.Tool
	for _, tl := range tools {
		switch tl.Name() {
		case "read_file":
			read = tl
		case "write_file":
			write = tl
		case "list_directory":
			list = tl
		}
	}
	f := filepath.Join(dir, "a.txt")
	_, err := write.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": f, "content": "hello"})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	res, err := read.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": f})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if res["content"] != "hello" {
		t.Fatalf("unexpected content: %v", res["content"])
	}
	// list
	res2, err := list.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": dir})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if res2["entries"] == nil {
		t.Fatalf("no entries")
	}
}

func TestReadFileDoesNotRejectUTF8RuneSplitAtSampleBoundary(t *testing.T) {
	dir := t.TempDir()
	tool := readFileTool{fsToolBase: fsToolBase{ws: config.Root{Path: dir}}}
	contentBytes := append(bytes.Repeat([]byte("a"), 8191), []byte("中")...)
	contentBytes = append(contentBytes, bytes.Repeat([]byte("文"), 100)...)
	content := string(contentBytes)
	path := filepath.Join(dir, "utf8-boundary.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	res, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": path})
	if err != nil {
		t.Fatalf("read utf8 boundary file: %v", err)
	}
	if res["content"] != content {
		t.Fatalf("unexpected content")
	}
}

func TestFilesystemToolDescriptionsExplainSelectionBoundaries(t *testing.T) {
	ws := config.Root{Path: t.TempDir()}
	descriptions := map[string]string{}
	for _, tool := range NewTools(ws) {
		descriptions[tool.Name()] = tool.Description()
	}
	for name, want := range map[string]string{
		"read_file":  "regular text file",
		"write_file": "prefer apply_patch for partial edits",
	} {
		if !strings.Contains(descriptions[name], want) {
			t.Fatalf("%s description missing %q: %q", name, want, descriptions[name])
		}
	}
}

func TestWriteFilePreservesExistingMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(target, []byte("old"), 0o751); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool := writeFileTool{fsToolBase: fsToolBase{ws: config.Root{Path: dir}}}
	if _, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": target, "content": "new"}); err != nil {
		t.Fatalf("write_file: %v", err)
	}
	info, _ := os.Stat(target)
	if info.Mode().Perm() != 0o751 {
		t.Fatalf("mode changed: %o", info.Mode().Perm())
	}
}

func TestWriteFileRejectsFinalSymlinkInsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("original"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlink not supported")
	}
	tool := writeFileTool{fsToolBase: fsToolBase{ws: config.Root{Path: dir}}}
	if _, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": link, "content": "mutated"}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected final symlink rejection, got %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "original" {
		t.Fatalf("symlink target was modified: %q", got)
	}
}

func TestWriteFileRejectsBinaryContent(t *testing.T) {
	dir := t.TempDir()
	tool := writeFileTool{fsToolBase: fsToolBase{ws: config.Root{Path: dir}}}
	if _, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"path":    filepath.Join(dir, "binary.txt"),
		"content": "a\x00b",
	}); err == nil || !strings.Contains(err.Error(), "binary content") {
		t.Fatalf("expected binary content rejection, got %v", err)
	}
}

func TestUploadFileRejectsFinalSymlinkInsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.bin")
	if err := os.WriteFile(target, []byte("original"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(dir, "link.bin")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlink not supported")
	}
	tool := uploadFileTool{fsToolBase: fsToolBase{ws: config.Root{Path: dir}}}
	if _, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"path": link,
		"file": map[string]any{"download_url": "https://example.com/file"},
	}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected final symlink rejection, got %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "original" {
		t.Fatalf("symlink target was modified: %q", got)
	}
}

func TestUploadFile(t *testing.T) {
	dir := t.TempDir()
	ws := config.Root{Path: dir}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "payload")
	}))
	defer srv.Close()
	tool := uploadFileTool{fsToolBase: fsToolBase{ws: ws}, client: srv.Client()}

	target := filepath.Join(dir, "nested", "target.bin")
	res, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"path": target,
		"file": map[string]any{
			"download_url": srv.URL + "/file",
			"file_id":      "file_123",
			"file_name":    "input.bin",
			"mime_type":    "application/octet-stream",
		},
	})
	if err != nil {
		t.Fatalf("upload_file: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "payload" {
		t.Fatalf("unexpected content: %q", got)
	}
	if res["file_id"] != "file_123" || res["file_name"] != "input.bin" || res["mime_type"] != "application/octet-stream" {
		t.Fatalf("unexpected metadata: %#v", res)
	}
}

func TestUploadFileRejectsLoopbackDownload(t *testing.T) {
	dir := t.TempDir()
	ws := config.Root{Path: dir}
	tool := uploadFileTool{fsToolBase: fsToolBase{ws: ws}}
	_, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"path": filepath.Join(dir, "target.bin"),
		"file": map[string]any{"download_url": "http://127.0.0.1/file"},
	})
	if err == nil || !strings.Contains(err.Error(), "not public") {
		t.Fatalf("expected loopback rejection, got %v", err)
	}
}

func TestUploadFileDescriptionAndSchema(t *testing.T) {
	ws := config.Root{Path: t.TempDir()}
	var tool core.Tool
	for _, candidate := range NewTools(ws) {
		if candidate.Name() == "upload_file" {
			tool = candidate
			break
		}
	}
	if tool == nil {
		t.Fatal("upload_file tool not found")
	}

	description := tool.Description()
	for _, want := range []string{
		"Copy a host-provided file",
		"configured root",
		"parent directories are created",
		"source is not modified",
	} {
		if !strings.Contains(description, want) {
			t.Fatalf("upload_file description missing %q: %q", want, description)
		}
	}
	if strings.HasPrefix(description, "Download a file") {
		t.Fatalf("upload_file description should be framed as an upload: %q", description)
	}

	schema, ok := tool.Schema().(map[string]any)
	if !ok {
		t.Fatalf("unexpected schema type: %#v", tool.Schema())
	}
	properties, _ := schema["properties"].(map[string]any)
	pathSchema, _ := properties["path"].(map[string]any)
	fileSchema, _ := properties["file"].(map[string]any)
	pathDescription, _ := pathSchema["description"].(string)
	fileDescription, _ := fileSchema["description"].(string)
	if !strings.Contains(pathDescription, "including the filename") || !strings.Contains(pathDescription, "Parent directories") {
		t.Fatalf("unexpected path description: %q", pathDescription)
	}
	if !strings.Contains(fileDescription, "file.download_url") || !strings.Contains(fileDescription, "local mounted path") {
		t.Fatalf("unexpected file description: %q", fileDescription)
	}
}

func TestUploadFileRejectsOutsideRoots(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	ws := config.Root{Path: root}
	var tool core.Tool
	for _, tl := range NewTools(ws) {
		if tl.Name() == "upload_file" {
			tool = tl
		}
	}
	_, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"path": filepath.Join(outside, "target.bin"),
		"file": map[string]any{
			"download_url": "https://example.com/file.bin",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "path not allowed") {
		t.Fatalf("expected path rejection, got: %v", err)
	}
}

func TestUploadFileRequiresHTTPURL(t *testing.T) {
	root := t.TempDir()
	ws := config.Root{Path: root}
	var tool core.Tool
	for _, tl := range NewTools(ws) {
		if tl.Name() == "upload_file" {
			tool = tl
		}
	}
	_, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"path": filepath.Join(root, "target.bin"),
		"file": map[string]any{
			"download_url": "file:///tmp/input.bin",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "must be http or https") {
		t.Fatalf("expected http/https rejection, got: %v", err)
	}
}

func TestSearchFiles(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "app.go"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "app_test.go"), []byte("x"), 0o644)
	ws := config.Root{Path: dir}
	var search core.Tool
	for _, tl := range NewTools(ws) {
		if tl.Name() == "search_files" {
			search = tl
		}
	}
	res, err := search.Execute(context.Background(), core.InvokeContext{}, map[string]any{"root": dir, "glob": "*.go"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if res["matches"] == nil {
		t.Fatalf("no matches")
	}
}

func TestReadFileRejectsDirectoryBinaryAndOversize(t *testing.T) {
	dir := t.TempDir()
	tool := readFileTool{fsToolBase: fsToolBase{ws: config.Root{Path: dir}}}
	if _, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": dir}); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("expected directory rejection, got %v", err)
	}
	binaryPath := filepath.Join(dir, "binary.bin")
	if err := os.WriteFile(binaryPath, []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	if _, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": binaryPath}); err == nil || !strings.Contains(err.Error(), "binary file") {
		t.Fatalf("expected binary rejection, got %v", err)
	}
	largePath := filepath.Join(dir, "large.txt")
	if err := os.WriteFile(largePath, bytes.Repeat([]byte("x"), readFileMaxBytes+1), 0o644); err != nil {
		t.Fatalf("write large: %v", err)
	}
	if _, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": largePath}); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size rejection, got %v", err)
	}
}

func TestReadFileHonorsCanceledContext(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "file.txt")
	_ = os.WriteFile(target, []byte("content"), 0o644)
	tool := readFileTool{fsToolBase: fsToolBase{ws: config.Root{Path: dir}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tool.Execute(ctx, core.InvokeContext{}, map[string]any{"path": target}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestListDirectoryReturnsExplicitTruncation(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		_ = os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644)
	}
	tool := listDirTool{fsToolBase: fsToolBase{ws: config.Root{Path: dir}}}
	res, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": dir, "max_results": 2})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	entries, _ := res["entries"].([]map[string]any)
	if len(entries) != 2 || res["truncated"] != true {
		t.Fatalf("unexpected bounded list: %#v", res)
	}
}

func TestRecursiveListHonorsCanceledContext(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a"), []byte("a"), 0o644)
	tool := listDirTool{fsToolBase: fsToolBase{ws: config.Root{Path: dir}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tool.Execute(ctx, core.InvokeContext{}, map[string]any{"path": dir, "recursive": true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestRecursiveTraversalPropagatesPermissionErrors(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatalf("mkdir blocked: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatalf("write blocked file: %v", err)
	}
	if err := os.Chmod(blocked, 0); err != nil {
		t.Skipf("chmod unavailable: %v", err)
	}
	defer os.Chmod(blocked, 0o755)
	ws := config.Root{Path: dir}
	list := listDirTool{fsToolBase: fsToolBase{ws: ws}}
	search := searchFilesTool{fsToolBase: fsToolBase{ws: ws}}
	_, listErr := list.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": dir, "recursive": true})
	_, searchErr := search.Execute(context.Background(), core.InvokeContext{}, map[string]any{"root": dir, "glob": "*.txt"})
	if listErr == nil && searchErr == nil {
		t.Skip("filesystem does not enforce directory permission bits for this process")
	}
	if listErr == nil || searchErr == nil {
		t.Fatalf("permission error was swallowed: list=%v search=%v", listErr, searchErr)
	}
}

func TestSearchFilesRejectsInvalidGlobAndReportsTruncation(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.go", "b.go", "c.go"} {
		_ = os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644)
	}
	tool := searchFilesTool{fsToolBase: fsToolBase{ws: config.Root{Path: dir}}}
	if _, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"root": dir, "glob": "["}); err == nil || !strings.Contains(err.Error(), "invalid glob") {
		t.Fatalf("expected invalid glob rejection, got %v", err)
	}
	res, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"root": dir, "glob": "*.go", "max_results": 2})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	matches, _ := res["matches"].([]string)
	if len(matches) != 2 || res["truncated"] != true {
		t.Fatalf("unexpected bounded search: %#v", res)
	}
}

func TestSymlinkEscapeDenied(t *testing.T) {
	root := t.TempDir()
	out := t.TempDir()
	target := filepath.Join(out, "secret.txt")
	_ = os.WriteFile(target, []byte("secret"), 0o644)
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlink not supported")
	}
	ws := config.Root{Path: root}
	var read core.Tool
	for _, tl := range NewTools(ws) {
		if tl.Name() == "read_file" {
			read = tl
		}
	}
	_, err := read.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": link})
	if err == nil {
		t.Fatalf("expected symlink escape denied")
	}
}

func TestReadFileAllowsSharedSkillsRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	skillDir := filepath.Join(home, ".agents", "skills", "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillFile := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillFile, []byte("name: demo"), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
	ws := config.Root{Path: root}
	var read, write core.Tool
	for _, tl := range NewTools(ws) {
		switch tl.Name() {
		case "read_file":
			read = tl
		case "write_file":
			write = tl
		}
	}
	res, err := read.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": skillFile})
	if err != nil {
		t.Fatalf("read shared skills file: %v", err)
	}
	if res["content"] != "name: demo" {
		t.Fatalf("unexpected content: %v", res["content"])
	}
	if _, err := write.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": skillFile, "content": "mutated"}); err == nil {
		t.Fatalf("write_file should still reject shared skills root")
	}
}

func TestReadFileRejectsRelativePath(t *testing.T) {
	root := t.TempDir()
	ws := config.Root{Path: root}
	var read core.Tool
	for _, tl := range NewTools(ws) {
		if tl.Name() == "read_file" {
			read = tl
		}
	}
	if _, err := read.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": "a.txt"}); err == nil {
		t.Fatalf("expected relative path to be rejected")
	}
}

func TestCreateUploadTokenTool(t *testing.T) {
	root := t.TempDir()
	ws := config.Root{Path: root}
	svc := upload.NewService()
	tool := NewCreateUploadTokenTool(ws, "http://127.0.0.1:8000/", svc)

	target := filepath.Join(root, "nested", "target.bin")
	res, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": target})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("eval symlinks root: %v", err)
	}
	wantPath := filepath.Join(realRoot, "nested", "target.bin")
	if res["path"] != wantPath {
		t.Fatalf("unexpected path: %#v", res)
	}
	if res["upload_url"] != "http://127.0.0.1:8000/system/uploads" {
		t.Fatalf("unexpected url: %#v", res)
	}
	if res["auth_header_name"] != uploadTokenHeaderName {
		t.Fatalf("unexpected header name: %#v", res)
	}
	if authValue, _ := res["auth_value"].(string); authValue == "" {
		t.Fatalf("missing auth value: %#v", res)
	}
}

func TestCreateUploadTokenToolRejectsOutsideRoots(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	ws := config.Root{Path: root}
	svc := upload.NewService()
	tool := NewCreateUploadTokenTool(ws, "http://127.0.0.1:8000", svc)

	_, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": filepath.Join(outside, "target.bin")})
	if err == nil || !strings.Contains(err.Error(), "path not allowed") {
		t.Fatalf("expected path rejection, got: %v", err)
	}
}

func TestCreateUploadTokenToolRequiresBaseURL(t *testing.T) {
	root := t.TempDir()
	ws := config.Root{Path: root}
	svc := upload.NewService()
	tool := NewCreateUploadTokenTool(ws, "", svc)

	_, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": filepath.Join(root, "target.bin")})
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("expected base url error, got: %v", err)
	}
}

func TestCreateDownloadTokenTool(t *testing.T) {
	root := t.TempDir()
	ws := config.Root{Path: root}
	svc := download.NewService()
	tool := NewCreateDownloadTokenTool(ws, "http://127.0.0.1:8000/", svc)

	target := filepath.Join(root, "nested", "target.bin")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(target, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	res, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": target})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("eval symlinks root: %v", err)
	}
	wantPath := filepath.Join(realRoot, "nested", "target.bin")
	if res["path"] != wantPath {
		t.Fatalf("unexpected path: %#v", res)
	}
	if got, _ := res["download_url"].(string); !strings.HasPrefix(got, "http://127.0.0.1:8000/system/downloads?token=lgdl_") {
		t.Fatalf("unexpected url: %#v", res)
	}
	if authValue, _ := res["auth_value"].(string); authValue == "" || !strings.HasPrefix(authValue, "lgdl_") {
		t.Fatalf("missing auth value: %#v", res)
	}
	if res["auth_query_key"] != "token" {
		t.Fatalf("unexpected auth query key: %#v", res)
	}
}

func TestCreateDownloadTokenToolRejectsOutsideRoots(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	ws := config.Root{Path: root}
	svc := download.NewService()
	tool := NewCreateDownloadTokenTool(ws, "http://127.0.0.1:8000", svc)

	target := filepath.Join(outside, "target.bin")
	if err := os.WriteFile(target, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	_, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": target})
	if err == nil || !strings.Contains(err.Error(), "path not allowed") {
		t.Fatalf("expected path rejection, got: %v", err)
	}
}

func TestCreateDownloadTokenToolRequiresBaseURL(t *testing.T) {
	root := t.TempDir()
	ws := config.Root{Path: root}
	svc := download.NewService()
	tool := NewCreateDownloadTokenTool(ws, "", svc)

	target := filepath.Join(root, "target.bin")
	if err := os.WriteFile(target, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	_, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": target})
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("expected base url error, got: %v", err)
	}
}

func TestCreateDownloadTokenToolRejectsMissingFile(t *testing.T) {
	root := t.TempDir()
	ws := config.Root{Path: root}
	svc := download.NewService()
	tool := NewCreateDownloadTokenTool(ws, "http://127.0.0.1:8000", svc)

	_, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": filepath.Join(root, "missing.bin")})
	if err == nil {
		t.Fatalf("expected missing file error")
	}
}

func TestCreateDownloadTokenToolRejectsDirectory(t *testing.T) {
	root := t.TempDir()
	ws := config.Root{Path: root}
	svc := download.NewService()
	tool := NewCreateDownloadTokenTool(ws, "http://127.0.0.1:8000", svc)

	dir := filepath.Join(root, "folder")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	_, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": dir})
	if err == nil || !strings.Contains(err.Error(), "must be a file") {
		t.Fatalf("expected directory rejection, got: %v", err)
	}
}
