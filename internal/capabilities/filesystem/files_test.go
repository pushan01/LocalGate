package filesystem

import (
	"context"
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
	ws := config.Workspace{ID: "ws", Roots: []string{dir}}
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

func TestUploadFile(t *testing.T) {
	dir := t.TempDir()
	ws := config.Workspace{ID: "ws", Roots: []string{dir}}
	var tool core.Tool
	for _, tl := range NewTools(ws) {
		if tl.Name() == "upload_file" {
			tool = tl
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "payload")
	}))
	defer srv.Close()

	target := filepath.Join(dir, "nested", "target.bin")
	res, err := tool.Execute(context.Background(), core.InvokeContext{WorkspaceID: "ws"}, map[string]any{
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

func TestUploadFileRejectsOutsideRoots(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	ws := config.Workspace{ID: "ws", Roots: []string{root}}
	var tool core.Tool
	for _, tl := range NewTools(ws) {
		if tl.Name() == "upload_file" {
			tool = tl
		}
	}
	_, err := tool.Execute(context.Background(), core.InvokeContext{WorkspaceID: "ws"}, map[string]any{
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
	ws := config.Workspace{ID: "ws", Roots: []string{root}}
	var tool core.Tool
	for _, tl := range NewTools(ws) {
		if tl.Name() == "upload_file" {
			tool = tl
		}
	}
	_, err := tool.Execute(context.Background(), core.InvokeContext{WorkspaceID: "ws"}, map[string]any{
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
	ws := config.Workspace{ID: "ws", Roots: []string{dir}}
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

func TestSymlinkEscapeDenied(t *testing.T) {
	root := t.TempDir()
	out := t.TempDir()
	target := filepath.Join(out, "secret.txt")
	_ = os.WriteFile(target, []byte("secret"), 0o644)
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlink not supported")
	}
	ws := config.Workspace{ID: "ws", Roots: []string{root}}
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
	ws := config.Workspace{ID: "ws", Roots: []string{root}}
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
	ws := config.Workspace{ID: "ws", Roots: []string{root}}
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
	ws := config.Workspace{ID: "ws", Roots: []string{root}}
	svc := upload.NewService()
	tool := NewCreateUploadTokenTool(ws, "http://127.0.0.1:8000/", svc)

	target := filepath.Join(root, "nested", "target.bin")
	res, err := tool.Execute(context.Background(), core.InvokeContext{WorkspaceID: "ws"}, map[string]any{"path": target})
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
	ws := config.Workspace{ID: "ws", Roots: []string{root}}
	svc := upload.NewService()
	tool := NewCreateUploadTokenTool(ws, "http://127.0.0.1:8000", svc)

	_, err := tool.Execute(context.Background(), core.InvokeContext{WorkspaceID: "ws"}, map[string]any{"path": filepath.Join(outside, "target.bin")})
	if err == nil || !strings.Contains(err.Error(), "path not allowed") {
		t.Fatalf("expected path rejection, got: %v", err)
	}
}

func TestCreateUploadTokenToolRequiresBaseURL(t *testing.T) {
	root := t.TempDir()
	ws := config.Workspace{ID: "ws", Roots: []string{root}}
	svc := upload.NewService()
	tool := NewCreateUploadTokenTool(ws, "", svc)

	_, err := tool.Execute(context.Background(), core.InvokeContext{WorkspaceID: "ws"}, map[string]any{"path": filepath.Join(root, "target.bin")})
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("expected base url error, got: %v", err)
	}
}

func TestCreateDownloadTokenTool(t *testing.T) {
	root := t.TempDir()
	ws := config.Workspace{ID: "ws", Roots: []string{root}}
	svc := download.NewService()
	tool := NewCreateDownloadTokenTool(ws, "http://127.0.0.1:8000/", svc)

	target := filepath.Join(root, "nested", "target.bin")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(target, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	res, err := tool.Execute(context.Background(), core.InvokeContext{WorkspaceID: "ws"}, map[string]any{"path": target})
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
	ws := config.Workspace{ID: "ws", Roots: []string{root}}
	svc := download.NewService()
	tool := NewCreateDownloadTokenTool(ws, "http://127.0.0.1:8000", svc)

	target := filepath.Join(outside, "target.bin")
	if err := os.WriteFile(target, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	_, err := tool.Execute(context.Background(), core.InvokeContext{WorkspaceID: "ws"}, map[string]any{"path": target})
	if err == nil || !strings.Contains(err.Error(), "path not allowed") {
		t.Fatalf("expected path rejection, got: %v", err)
	}
}

func TestCreateDownloadTokenToolRequiresBaseURL(t *testing.T) {
	root := t.TempDir()
	ws := config.Workspace{ID: "ws", Roots: []string{root}}
	svc := download.NewService()
	tool := NewCreateDownloadTokenTool(ws, "", svc)

	target := filepath.Join(root, "target.bin")
	if err := os.WriteFile(target, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	_, err := tool.Execute(context.Background(), core.InvokeContext{WorkspaceID: "ws"}, map[string]any{"path": target})
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("expected base url error, got: %v", err)
	}
}

func TestCreateDownloadTokenToolRejectsMissingFile(t *testing.T) {
	root := t.TempDir()
	ws := config.Workspace{ID: "ws", Roots: []string{root}}
	svc := download.NewService()
	tool := NewCreateDownloadTokenTool(ws, "http://127.0.0.1:8000", svc)

	_, err := tool.Execute(context.Background(), core.InvokeContext{WorkspaceID: "ws"}, map[string]any{"path": filepath.Join(root, "missing.bin")})
	if err == nil {
		t.Fatalf("expected missing file error")
	}
}

func TestCreateDownloadTokenToolRejectsDirectory(t *testing.T) {
	root := t.TempDir()
	ws := config.Workspace{ID: "ws", Roots: []string{root}}
	svc := download.NewService()
	tool := NewCreateDownloadTokenTool(ws, "http://127.0.0.1:8000", svc)

	dir := filepath.Join(root, "folder")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	_, err := tool.Execute(context.Background(), core.InvokeContext{WorkspaceID: "ws"}, map[string]any{"path": dir})
	if err == nil || !strings.Contains(err.Error(), "must be a file") {
		t.Fatalf("expected directory rejection, got: %v", err)
	}
}
