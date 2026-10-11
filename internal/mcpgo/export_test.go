package mcpgo

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"localgate/internal/config"
	"localgate/internal/core"

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
		Root: config.Root{Path: root},
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
	if !strings.Contains(string(structured), "signature=") {
		t.Fatalf("immediate structured MCP result missing signed fileUrl: %s", structured)
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

func TestImageExportRendersInlineAndAsyncTaskResultKeepsArtifacts(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "image.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newTestServerWithConfig(t, config.Config{
		LogStore: t.TempDir(),
		HTTP:     config.HTTP{BaseURL: "https://gate.example"},
		FileExports: config.FileExports{
			Enabled: true, BaseURL: "https://gate.example",
			DefaultTTLSeconds: 300, MaxTTLSeconds: 600, MaxFileBytes: 1 << 20,
		},
		Root: config.Root{Path: root},
	})

	check := func(name string, res *mcp.CallToolResult) {
		t.Helper()
		if res.IsError {
			t.Fatalf("%s: unexpected error: %#v", name, res)
		}
		seenImage, seenResource, seenDownloadText := false, false, false
		for _, c := range res.Content {
			switch v := c.(type) {
			case mcp.ImageContent:
				decoded, err := base64.StdEncoding.DecodeString(v.Data)
				if err != nil || v.MIMEType != "image/png" || !bytes.Equal(decoded, buf.Bytes()) {
					t.Fatalf("%s: invalid inline image", name)
				}
				seenImage = true
			case mcp.ResourceLink:
				seenResource = strings.Contains(v.URI, "signature=")
			case mcp.TextContent:
				seenDownloadText = seenDownloadText || strings.Contains(v.Text, "Download ")
			}
		}
		if !seenImage || !seenResource || !seenDownloadText {
			t.Fatalf("%s: missing rich image and download content: %#v", name, res.Content)
		}
		structured, err := json.Marshal(res.StructuredContent)
		if err != nil || !strings.Contains(string(structured), "signature=") {
			t.Fatalf("%s: missing signed fileUrl in immediate MCP response: %s", name, structured)
		}
	}

	// An async tool call returns a task id before the resource can be delivered.
	queued, err := s.makeInvokeHandler("export_file")(context.Background(), callReq(map[string]any{"path": path, "async": true}))
	if err != nil || queued.IsError {
		t.Fatalf("async export: %v, %#v", err, queued)
	}
	id, _ := queued.StructuredContent.(map[string]any)["task_id"].(string)
	if id == "" {
		t.Fatalf("async export should return task_id: %#v", queued.StructuredContent)
	}
	result := waitTaskSnapshot(s.App.Tasks, id, 5*time.Second)
	if result.Status != core.StatusSucceeded {
		t.Fatalf("async export did not finish: %#v", result)
	}
	got, err := s.makeInvokeHandler("task_result")(context.Background(), callReq(map[string]any{"task_id": id}))
	if err != nil {
		t.Fatal(err)
	}
	check("async result", got)

	// The normal synchronous route must present the same result blocks.
	syncResult, err := s.makeInvokeHandler("export_file")(context.Background(), callReq(map[string]any{"path": path}))
	if err != nil {
		t.Fatal(err)
	}
	check("sync export", syncResult)
}
