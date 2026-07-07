package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"localgate/internal/config"
	"localgate/internal/core"
	"localgate/internal/fileexport"
)

func TestExportFileToolCreatesResourceLink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "report.txt")
	content := []byte("LocalGate export payload")
	if err := os.WriteFile(target, content, 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	cfg := config.FileExports{
		Enabled:           true,
		BaseURL:           "https://gate.example",
		DefaultTTLSeconds: 300,
		MaxTTLSeconds:     600,
		MaxFileBytes:      1 << 20,
	}
	svc, err := fileexport.NewService(fileexport.Options{
		BaseURL:    cfg.BaseURL,
		DefaultTTL: 5 * time.Minute,
		MaxTTL:     10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	tool := NewExportFileTool(config.Workspace{ID: "ws", Roots: []string{root}, CWD: root}, cfg, svc)
	result, err := tool.Execute(context.Background(), core.InvokeContext{WorkspaceID: "ws"}, map[string]any{
		"path":        target,
		"file_name":   "renamed.txt",
		"ttl_seconds": 120,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["status"] != "ready" || result["file_name"] != "renamed.txt" {
		t.Fatalf("unexpected result: %#v", result)
	}
	wantDigest := sha256.Sum256(content)
	if result["sha256"] != hex.EncodeToString(wantDigest[:]) {
		t.Fatalf("unexpected sha256: %#v", result["sha256"])
	}
	links := core.ToolResourceLinks(tool, result)
	if len(links) != 1 {
		t.Fatalf("expected one resource link, got %#v", links)
	}
	if !strings.HasPrefix(links[0].URI, "https://gate.example/system/exports/lgexp_") || !strings.Contains(links[0].URI, "signature=") {
		t.Fatalf("unexpected resource URI: %q", links[0].URI)
	}
	if links[0].Name != "renamed.txt" || links[0].MIMEType != "text/plain; charset=utf-8" || links[0].Size != int64(len(content)) {
		t.Fatalf("unexpected resource metadata: %#v", links[0])
	}
}

func TestExportFileToolRejectsUnsafeOrOversizedSources(t *testing.T) {
	root := t.TempDir()
	cfg := config.FileExports{
		Enabled:           true,
		BaseURL:           "https://gate.example",
		DefaultTTLSeconds: 300,
		MaxTTLSeconds:     600,
		MaxFileBytes:      3,
	}
	svc, err := fileexport.NewService(fileexport.Options{
		BaseURL:    cfg.BaseURL,
		DefaultTTL: 5 * time.Minute,
		MaxTTL:     10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	tool := NewExportFileTool(config.Workspace{ID: "ws", Roots: []string{root}, CWD: root}, cfg, svc)

	large := filepath.Join(root, "large.bin")
	if err := os.WriteFile(large, []byte("four"), 0o644); err != nil {
		t.Fatalf("write large file: %v", err)
	}
	if _, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": large}); err == nil {
		t.Fatal("expected oversized file to fail")
	}
	if _, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": root}); err == nil {
		t.Fatal("expected directory to fail")
	}

	target := filepath.Join(root, "ok.txt")
	if err := os.WriteFile(target, []byte("ok"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err == nil {
		if _, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"path": link}); err == nil {
			t.Fatal("expected symlink to fail")
		}
	}
	if _, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"path":      target,
		"file_name": "../escape.txt",
	}); err == nil {
		t.Fatal("expected unsafe display file name to fail")
	}
}
