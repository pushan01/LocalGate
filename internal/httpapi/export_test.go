package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"localgate/internal/app"
	"localgate/internal/config"
	"localgate/internal/core"
)

func TestExportEndpointSupportsGetHeadRangeAndDetectsChanges(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "payload.txt")
	if err := os.WriteFile(target, []byte("abcdef"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	application, err := app.New(config.Config{
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
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	defer application.Close()
	api := &API{App: application, DefaultWSID: application.DefaultWSID}
	mux := http.NewServeMux()
	api.RegisterControl(mux)

	result, err := application.InvokeTool(context.Background(), "ws", "export_file", map[string]any{"path": target})
	if err != nil {
		t.Fatalf("export_file: %v", err)
	}
	tool, ok := application.Registries["ws"].Get("export_file")
	if !ok {
		t.Fatal("export_file tool not registered")
	}
	links := core.ToolResourceLinks(tool, result)
	if len(links) != 1 {
		t.Fatalf("expected resource link, got %#v", links)
	}
	parsed, err := url.Parse(links[0].URI)
	if err != nil {
		t.Fatalf("parse URI: %v", err)
	}

	request := func(method string, rangeHeader string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, parsed.RequestURI(), nil)
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}

	w := request(http.MethodGet, "")
	if w.Code != http.StatusOK || w.Body.String() != "abcdef" {
		t.Fatalf("unexpected GET: status=%d body=%q", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("unexpected cache control: %q", got)
	}
	if got := w.Header().Get("Content-Disposition"); !strings.Contains(got, "payload.txt") {
		t.Fatalf("unexpected content disposition: %q", got)
	}

	w = request(http.MethodHead, "")
	if w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Fatalf("unexpected HEAD: status=%d body=%q", w.Code, w.Body.String())
	}

	w = request(http.MethodGet, "bytes=1-3")
	if w.Code != http.StatusPartialContent || w.Body.String() != "bcd" {
		t.Fatalf("unexpected range response: status=%d body=%q", w.Code, w.Body.String())
	}

	tampered := *parsed
	q := tampered.Query()
	q.Set("signature", q.Get("signature")+"x")
	tampered.RawQuery = q.Encode()
	req := httptest.NewRequest(http.MethodGet, tampered.RequestURI(), nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected tampered signature 404, got %d", w.Code)
	}

	if err := os.WriteFile(target, []byte("ABCDEF"), 0o644); err != nil {
		t.Fatalf("modify target: %v", err)
	}
	w = request(http.MethodGet, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("expected changed source 409, got %d body=%s", w.Code, w.Body.String())
	}
}
