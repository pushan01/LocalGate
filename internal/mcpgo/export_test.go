package mcpgo

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"localgate/internal/config"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestExportFileToolDeclaresAndReturnsResourceLink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "result.png")
	if err := os.WriteFile(target, []byte("not-a-real-png-but-transferable"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	s := newTestServerWithConfig(t, config.Config{
		LogStore: t.TempDir(),
		HTTP:     config.HTTP{BaseURL: "https://gate.example"},
		FileExports: config.FileExports{
			Enabled:           true,
			BaseURL:           "https://gate.example",
			DefaultTTLSeconds: 300,
			MaxTTLSeconds:     600,
			MaxFileBytes:      1 << 20,
			RequireApproval:   false,
		},
		Workspaces: []config.Workspace{{ID: "ws", Roots: []string{root}, CWD: root}},
	})

	registered := s.impl.ListTools()["export_file"]
	if registered == nil {
		t.Fatal("export_file tool not registered")
	}
	if len(registered.Tool.RawOutputSchema) == 0 {
		t.Fatal("export_file should declare an output schema")
	}
	if registered.Tool.Annotations.ReadOnlyHint == nil || !*registered.Tool.Annotations.ReadOnlyHint {
		t.Fatalf("expected readOnlyHint=true, got %#v", registered.Tool.Annotations)
	}
	if registered.Tool.Annotations.OpenWorldHint == nil || !*registered.Tool.Annotations.OpenWorldHint {
		t.Fatalf("expected openWorldHint=true, got %#v", registered.Tool.Annotations)
	}

	res, err := s.makeInvokeHandler("export_file")(context.Background(), callReq(map[string]any{
		"path":      target,
		"file_name": "preview.png",
	}))
	if err != nil {
		t.Fatalf("invoke export_file: %v", err)
	}
	if res.IsError {
		t.Fatalf("export_file returned error: %#v", res)
	}
	structured, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if strings.Contains(string(structured), "signature=") {
		t.Fatalf("signed URL leaked into structured task result: %s", structured)
	}

	found := false
	for _, content := range res.Content {
		link, ok := content.(mcp.ResourceLink)
		if !ok {
			continue
		}
		found = true
		if link.Type != "resource_link" || link.Name != "preview.png" || link.MIMEType != "image/png" {
			t.Fatalf("unexpected resource link: %#v", link)
		}
		if !strings.HasPrefix(link.URI, "https://gate.example/system/exports/lgexp_") || !strings.Contains(link.URI, "signature=") {
			t.Fatalf("unexpected resource URI: %q", link.URI)
		}
	}
	if !found {
		t.Fatalf("resource_link missing from tool result: %#v", res.Content)
	}
}
