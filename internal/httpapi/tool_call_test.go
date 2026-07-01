package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"localgate/internal/app"
	"localgate/internal/config"
	"localgate/internal/toolaccess"
)

func TestToolCallRequiresToolAccess(t *testing.T) {
	_, handler, _ := newToolHTTPModeTestServer(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/tools/read_file/call", bytes.NewBufferString(`{}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "missing_tool_access") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestToolCallSucceedsWithAccess(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "note.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	application, handler, _ := newToolHTTPModeTestServer(t, &toolHTTPModeTestOptions{
		root: root,
	})
	res, err := application.InvokeTool(context.Background(), "ws", "get_temporary_access_token", nil)
	if err != nil {
		t.Fatalf("get_temporary_access_token: %v", err)
	}
	authValue, _ := res["auth_value"].(string)

	req := httptest.NewRequest(http.MethodPost, "/tools/read_file/call", bytes.NewBufferString(`{"path":"`+target+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(toolaccess.HeaderName, authValue)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["content"] != "hello" {
		t.Fatalf("unexpected tool result: %#v", body)
	}
}

func TestToolCallRejectsHiddenTool(t *testing.T) {
	application, handler, _ := newToolHTTPModeTestServer(t, &toolHTTPModeTestOptions{
		hiddenTools: []string{"read_file"},
	})
	res, err := application.InvokeTool(context.Background(), "ws", "get_temporary_access_token", nil)
	if err != nil {
		t.Fatalf("get_temporary_access_token: %v", err)
	}
	authValue, _ := res["auth_value"].(string)

	req := httptest.NewRequest(http.MethodPost, "/tools/read_file/call", bytes.NewBufferString(`{"path":"/tmp/x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(toolaccess.HeaderName, authValue)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "tool_not_http_exposed") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestToolCallRejectsExpiredCredential(t *testing.T) {
	application, handler, _ := newToolHTTPModeTestServer(t, nil)
	grant, err := application.ToolAccess.Issue("ws", time.Millisecond)
	if err != nil {
		t.Fatalf("issue tool access: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	req := httptest.NewRequest(http.MethodPost, "/tools/read_file/call", bytes.NewBufferString(`{}`))
	req.Header.Set(toolaccess.HeaderName, grant.AuthValue)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestToolCallAcceptsBase64BodyWhenEnabled(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "note.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	application, handler, _ := newToolHTTPModeTestServer(t, &toolHTTPModeTestOptions{
		root:         root,
		enableBase64: true,
	})
	res, err := application.InvokeTool(context.Background(), "ws", "get_temporary_access_token", nil)
	if err != nil {
		t.Fatalf("get_temporary_access_token: %v", err)
	}
	authValue, _ := res["auth_value"].(string)

	raw, _ := json.Marshal(map[string]any{"path": target})
	encoded := base64.StdEncoding.EncodeToString(raw)
	req := httptest.NewRequest(http.MethodPost, "/tools/read_file/call", bytes.NewBufferString(encoded))
	req.Header.Set(toolaccess.HeaderName, authValue)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["content"] != "hello" {
		t.Fatalf("unexpected tool result: %#v", body)
	}
}

func TestTaskSubmitAcceptsBase64ArgsWhenEnabled(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "note.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	_, handler, _ := newToolHTTPModeTestServer(t, &toolHTTPModeTestOptions{
		root:         root,
		enableBase64: true,
	})

	raw, _ := json.Marshal(map[string]any{"path": target})
	encoded := base64.StdEncoding.EncodeToString(raw)
	req := httptest.NewRequest(http.MethodPost, "/tasks", bytes.NewBufferString(`{"tool":"read_file","workspace_id":"ws","args":"`+encoded+`"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestToolCallRejectsBase64BodyWhenDisabled(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "note.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	application, handler, _ := newToolHTTPModeTestServer(t, &toolHTTPModeTestOptions{
		root: root,
	})
	res, err := application.InvokeTool(context.Background(), "ws", "get_temporary_access_token", nil)
	if err != nil {
		t.Fatalf("get_temporary_access_token: %v", err)
	}
	authValue, _ := res["auth_value"].(string)

	raw, _ := json.Marshal(map[string]any{"path": target})
	encoded := base64.StdEncoding.EncodeToString(raw)
	req := httptest.NewRequest(http.MethodPost, "/tools/read_file/call", bytes.NewBufferString(encoded))
	req.Header.Set(toolaccess.HeaderName, authValue)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

type toolHTTPModeTestOptions struct {
	root         string
	hiddenTools  []string
	enableBase64 bool
}

func newToolHTTPModeTestServer(t *testing.T, opts *toolHTTPModeTestOptions) (*app.App, http.Handler, string) {
	t.Helper()
	root := t.TempDir()
	hiddenTools := []string(nil)
	if opts != nil {
		if opts.root != "" {
			root = opts.root
		}
		hiddenTools = append(hiddenTools, opts.hiddenTools...)
	}
	cfg := config.Config{
		HTTP: config.HTTP{BaseURL: "http://127.0.0.1:8000"},
		ToolArguments: config.ToolArguments{
			EnableBase64: opts != nil && opts.enableBase64,
		},
		ToolHTTPMode: config.ToolHTTPMode{
			Enabled:               true,
			AccessTokenTTLSeconds: 600,
		},
		Workspaces: []config.Workspace{
			{
				ID:               "ws",
				Roots:            []string{root},
				CWD:              root,
				HiddenTools:      hiddenTools,
				CommandAllowlist: []string{"echo", "sh"},
			},
		},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	api := &API{App: application, DefaultWSID: application.DefaultWSID}
	mux := http.NewServeMux()
	api.Register(mux)
	return application, mux, root
}
