package mcpgo

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"localgate/internal/app"
	"localgate/internal/config"
)

func TestMountHTTPRejectsRequestsWithoutSessionIDByDefault(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		LogStore:         t.TempDir(),
		DefaultTimeoutMs: 10_000,
		Workspaces: []config.Workspace{{
			ID:    "ws",
			Roots: []string{root},
			CWD:   root,
		}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	s, err := New(application, "localgate", "test", "ws")
	if err != nil {
		t.Fatalf("mcpgo.New: %v", err)
	}

	mux := http.NewServeMux()
	s.MountHTTP(mux, "/mcp", HTTPOptions{})

	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("Invalid session ID")) {
		t.Fatalf("expected Invalid session ID, got %s", w.Body.String())
	}
}

func TestMountHTTPAllowsRequestsWithoutSessionIDWhenStateless(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		LogStore:         t.TempDir(),
		DefaultTimeoutMs: 10_000,
		Workspaces: []config.Workspace{{
			ID:    "ws",
			Roots: []string{root},
			CWD:   root,
		}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	s, err := New(application, "localgate", "test", "ws")
	if err != nil {
		t.Fatalf("mcpgo.New: %v", err)
	}

	mux := http.NewServeMux()
	s.MountHTTP(mux, "/mcp", HTTPOptions{Stateless: true})

	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["result"] == nil {
		t.Fatalf("expected JSON-RPC result, got %#v", body)
	}
}
