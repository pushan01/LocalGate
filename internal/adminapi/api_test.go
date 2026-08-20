package adminapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"localgate/internal/app"
	"localgate/internal/config"
	"localgate/internal/core"
	"localgate/internal/persist"
)

func TestTaskListFiltersSessionAndOmitsResult(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.Approval.Enabled = false
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Root = config.Root{Path: root}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	application.Tasks.LoadHistory([]core.Task{
		{ID: "one", Status: core.StatusSucceeded, Tool: "read_file", MCPSessionID: "session-a", CreatedAt: time.Now().UTC(), Result: map[string]any{"secret": "large"}},
		{ID: "two", Status: core.StatusFailed, Tool: "run_command", MCPSessionID: "session-b", CreatedAt: time.Now().UTC().Add(-time.Minute)},
	})

	mux := http.NewServeMux()
	(&API{App: application, BaseURL: "https://admin.test"}).Register(mux)
	req := httptest.NewRequest(http.MethodGet, "https://admin.test/api/admin/v1/tasks?session_id=session-a", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0]["id"] != "one" {
		t.Fatalf("unexpected items: %#v", body.Items)
	}
	if _, exists := body.Items[0]["result"]; exists {
		t.Fatalf("list must not expose full result: %#v", body.Items[0])
	}
}

func TestAdminAPIRejectsWrongHost(t *testing.T) {
	mux := http.NewServeMux()
	(&API{App: &app.App{}, BaseURL: "https://admin.test"}).Register(mux)
	req := httptest.NewRequest(http.MethodGet, "https://control.test/api/admin/v1/overview", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestTaskEventsFallBackToPersistedHistory(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.Approval.Enabled = false
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Root = config.Root{Path: root}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	application.Tasks.LoadHistory([]core.Task{{ID: "history-task", Status: core.StatusSucceeded, CreatedAt: time.Now().UTC()}})
	store := persist.NewStore(application.StoreBase)
	if err := store.AppendEvent(core.Event{TaskID: "history-task", Type: core.EventTaskCompleted, Time: time.Now().UTC()}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	mux := http.NewServeMux()
	(&API{App: application, BaseURL: "https://admin.test"}).Register(mux)
	req := httptest.NewRequest(http.MethodGet, "https://admin.test/api/admin/v1/tasks/history-task/events", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Items []core.Event `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].Type != core.EventTaskCompleted {
		t.Fatalf("unexpected persisted events: %#v", body.Items)
	}
}
