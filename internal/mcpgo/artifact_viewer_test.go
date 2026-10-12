package mcpgo

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"localgate/internal/config"
)

func TestArtifactViewerAdvertisedAndReadableOverMCPHTTP(t *testing.T) {
	s := newTestServerWithConfig(t, config.Config{
		Root: config.Root{Path: t.TempDir()}, LogStore: t.TempDir(),
		HTTP: config.HTTP{BaseURL: "https://gate.example"},
		FileExports: config.FileExports{
			Enabled: true, BaseURL: "https://gate.example",
			DefaultTTLSeconds: 300, MaxTTLSeconds: 600, MaxFileBytes: 1 << 20,
		},
	})
	mux := http.NewServeMux()
	s.MountHTTP(mux, "/mcp", HTTPOptions{Stateless: true})

	rpc := func(method string, params map[string]any) map[string]any {
		t.Helper()
		body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s HTTP %d: %s", method, w.Code, w.Body.String())
		}
		var response map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response["error"] != nil {
			t.Fatalf("%s: %#v", method, response["error"])
		}
		result, ok := response["result"].(map[string]any)
		if !ok {
			t.Fatalf("%s missing result: %#v", method, response)
		}
		return result
	}

	listed := rpc("tools/list", nil)
	tools, ok := listed["tools"].([]any)
	if !ok {
		t.Fatalf("tools/list response invalid: %#v", listed)
	}
	var attached bool
	for _, item := range tools {
		tool := item.(map[string]any)
		meta, _ := tool["_meta"].(map[string]any)
		ui, _ := meta["ui"].(map[string]any)
		if tool["name"] == "export_file" {
			if ui["resourceUri"] != artifactViewerURI || meta["openai/outputTemplate"] != artifactViewerURI {
				t.Fatalf("missing UI metadata: %#v", meta)
			}
			attached = true
		} else if ui["resourceUri"] == artifactViewerURI {
			t.Fatalf("viewer should not be attached to unrelated tool %s", tool["name"])
		}
	}
	if !attached {
		t.Fatal("export_file not present in tools/list")
	}

	read := rpc("resources/read", map[string]any{"uri": artifactViewerURI})
	contents, ok := read["contents"].([]any)
	if !ok || len(contents) != 1 {
		t.Fatalf("UI resource response invalid: %#v", read)
	}
	content := contents[0].(map[string]any)
	html, _ := content["text"].(string)
	if content["mimeType"] != artifactViewerMIME || content["uri"] != artifactViewerURI {
		t.Fatalf("invalid UI resource MIME/URI: %#v", content)
	}
	for _, required := range []string{"ui/initialize", "ui/notifications/initialized", "ui/notifications/tool-result", "resource_link", "image/png", "下载原文件"} {
		if !strings.Contains(html, required) {
			t.Errorf("viewer missing %q", required)
		}
	}
	for _, forbidden := range []string{"id=\"checks\"", "id=\"followup\"", "uploadFile(", "getFileDownloadUrl(", "sendFollowUpMessage(", "setWidgetState("} {
		if strings.Contains(html, forbidden) {
			t.Errorf("viewer still contains removed test feature %q", forbidden)
		}
	}
	meta, _ := content["_meta"].(map[string]any)
	ui, _ := meta["ui"].(map[string]any)
	csp, ok := ui["csp"].(map[string]any)
	if !ok {
		t.Fatalf("widget does not declare CSP: %#v", meta)
	}
	if connect, ok := csp["connectDomains"].([]any); !ok || len(connect) != 0 {
		t.Fatalf("preview-only widget must not have fetch permissions: %#v", csp)
	}
}
