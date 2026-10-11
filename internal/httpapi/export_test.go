package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"localgate/internal/app"
	"localgate/internal/config"
	"localgate/internal/core"
	"localgate/internal/fileexport"
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
		Root: config.Root{Path: root},
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	defer application.Close()
	api := &API{App: application}
	mux := http.NewServeMux()
	api.RegisterControl(mux)

	result, err := application.InvokeTool(context.Background(), "export_file", map[string]any{"path": target})
	if err != nil {
		t.Fatalf("export_file: %v", err)
	}
	tool, ok := application.Registry.Get("export_file")
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
	if got := w.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment") {
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

func TestDedicatedExportListenerOnlyExposesSignedDownloads(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "payload.txt")
	if err := os.WriteFile(target, []byte("dedicated-export"), 0o600); err != nil {
		t.Fatal(err)
	}
	application, err := app.New(config.Config{
		LogStore: t.TempDir(),
		HTTP:     config.HTTP{BaseURL: "https://macgate.example"},
		FileExports: config.FileExports{
			Enabled: true, Bind: "127.0.0.1:2529", BaseURL: "https://files.example",
			DefaultTTLSeconds: 300, MaxTTLSeconds: 600, MaxFileBytes: 1 << 20,
		},
		Root: config.Root{Path: root},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	api := &API{App: application}
	control := http.NewServeMux()
	api.RegisterControl(control)
	exports := http.NewServeMux()
	api.RegisterFileExports(exports)
	result, err := application.InvokeTool(context.Background(), "export_file", map[string]any{"path": target})
	if err != nil {
		t.Fatal(err)
	}
	tool, _ := application.Registry.Get("export_file")
	links := core.ToolResourceLinks(tool, result)
	if len(links) != 1 {
		t.Fatalf("expected signed download URL: %#v", links)
	}
	u, err := url.Parse(links[0].URI)
	if err != nil || u.Host != "files.example" {
		t.Fatalf("unexpected signed export URL: %q, %v", links[0].URI, err)
	}
	for _, tc := range []struct {
		name, method, path string
		handler            http.Handler
		want               int
	}{
		{"export GET", "GET", u.RequestURI(), exports, http.StatusOK},
		{"export HEAD", "HEAD", u.RequestURI(), exports, http.StatusOK},
		{"missing signature", "GET", u.Path, exports, http.StatusNotFound},
		{"old control export closed", "GET", u.RequestURI(), control, http.StatusNotFound},
		{"tools unavailable", "GET", "/tools", exports, http.StatusNotFound},
		{"mcp unavailable", "GET", "/mcp", exports, http.StatusNotFound},
		{"download-token unavailable", "GET", "/system/downloads", exports, http.StatusNotFound},
		{"admin unavailable", "GET", "/admin", exports, http.StatusNotFound},
		{"method denied", "POST", u.RequestURI(), exports, http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.handler.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.want {
				t.Fatalf("status=%d, want=%d, body=%s", w.Code, tc.want, w.Body.String())
			}
			if tc.name == "export GET" && w.Body.String() != "dedicated-export" {
				t.Fatalf("wrong download bytes: %q", w.Body.String())
			}
		})
	}
}

func TestExportEndpointAcceptsGrantIssuedByAnotherAppInstance(t *testing.T) {
	root := t.TempDir()
	store := t.TempDir()
	target := filepath.Join(root, "cross-process.txt")
	if err := os.WriteFile(target, []byte("cross-process-export"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		LogStore: store,
		HTTP:     config.HTTP{BaseURL: "https://gate.example"},
		FileExports: config.FileExports{
			Enabled: true, BaseURL: "https://gate.example",
			DefaultTTLSeconds: 300, MaxTTLSeconds: 600, MaxFileBytes: 1 << 20,
		},
		Root: config.Root{Path: root},
	}
	issuer, err := app.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer issuer.Close()
	receiver, err := app.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	result, err := issuer.InvokeTool(context.Background(), "export_file", map[string]any{"path": target})
	if err != nil {
		t.Fatal(err)
	}
	tool, _ := issuer.Registry.Get("export_file")
	links := core.ToolResourceLinks(tool, result)
	if len(links) != 1 {
		t.Fatalf("expected export link, got %#v", links)
	}
	u, err := url.Parse(links[0].URI)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(&API{App: receiver}).RegisterControl(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, u.RequestURI(), nil))
	if w.Code != http.StatusOK || w.Body.String() != "cross-process-export" {
		t.Fatalf("grant from separate app instance must download: status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestExportEndpointServesValidatedInternalSourceOutsideWorkspace(t *testing.T) {
	workspaceRoot := t.TempDir()
	internalRoot := t.TempDir()
	target := filepath.Join(internalRoot, "artifact.png")
	payload := []byte("internal-artifact")
	if err := os.WriteFile(target, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)

	application, err := app.New(config.Config{
		LogStore: t.TempDir(),
		HTTP:     config.HTTP{BaseURL: "https://gate.example"},
		FileExports: config.FileExports{
			Enabled:           true,
			BaseURL:           "https://gate.example",
			DefaultTTLSeconds: 300,
			MaxTTLSeconds:     600,
			MaxFileBytes:      1 << 20,
		},
		Root: config.Root{Path: workspaceRoot},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	grant, err := application.Exports.Issue(fileexport.Grant{
		Path:            target,
		FileName:        "artifact.png",
		MIMEType:        "image/png",
		Size:            info.Size(),
		SHA256:          hex.EncodeToString(sum[:]),
		ModTimeUnixNano: info.ModTime().UnixNano(),
		InternalSource:  true,
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	link, err := application.Exports.Link(grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(link.URI)
	if err != nil {
		t.Fatal(err)
	}
	api := &API{App: application}
	mux := http.NewServeMux()
	api.RegisterControl(mux)
	req := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != string(payload) {
		t.Fatalf("unexpected internal export: status=%d body=%q", w.Code, w.Body.String())
	}

	if err := os.WriteFile(target, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("expected internal source mutation to fail, got %d", w.Code)
	}
}
