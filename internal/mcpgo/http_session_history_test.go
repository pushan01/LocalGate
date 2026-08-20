package mcpgo

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"localgate/internal/app"
	"localgate/internal/config"
	"localgate/internal/core"
	"localgate/internal/invocation"
)

func TestStatelessHTTPRecordsClientSuppliedSessionID(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.Approval.Enabled = false
	cfg.Root = config.Root{Path: root}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	s, err := New(application, "localgate", "test")
	if err != nil {
		t.Fatalf("mcpgo.New: %v", err)
	}
	mux := http.NewServeMux()
	s.MountHTTP(mux, "/mcp", HTTPOptions{Stateless: true})

	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_startup_instructions","arguments":{}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", "client-session-123")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	page, err := application.Tasks.List(core.TaskFilter{SessionID: "client-session-123", Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("expected one session task, page=%#v err=%v", page, err)
	}
	task := page.Items[0]
	if task.SessionMode != invocation.SessionModeStateless || task.SessionIDSource != invocation.SessionSourceClientSupplied {
		t.Fatalf("unexpected session metadata: %#v", task)
	}
}
