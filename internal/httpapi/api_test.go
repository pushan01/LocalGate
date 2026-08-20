package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"localgate/internal/app"
	"localgate/internal/approval"
	"localgate/internal/config"
	"localgate/internal/core"
)

func TestListTools(t *testing.T) {
	cfg := config.Config{
		Root: config.Root{CommandAllowlist: []string{"echo", "sh"}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/tools", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if _, ok := body["workspace_id"]; ok {
		t.Fatalf("tool list must not expose workspace metadata: %v", body)
	}
	items, _ := body["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("expected tools, got %s", w.Body.String())
	}
	found := false
	foundApplyPatch := false
	for _, item := range items {
		m, _ := item.(map[string]any)
		if m["name"] == "run_command" {
			found = true
			if m["description"] == "" {
				t.Fatalf("missing description: %v", m)
			}
			if _, ok := m["schema"].(map[string]any); !ok {
				t.Fatalf("missing schema: %v", m)
			}
		}
		if m["name"] == "apply_patch" {
			foundApplyPatch = true
		}
		if m["name"] == "list_directory" || m["name"] == "search_files" || m["name"] == "run_argv" || m["name"] == "create_upload_token" || m["name"] == "create_download_token" {
			t.Fatalf("default hidden tool should not be listed: %v", m)
		}
	}
	if !found {
		t.Fatalf("run_command not found in items: %v", items)
	}
	if !foundApplyPatch {
		t.Fatalf("apply_patch not found in items: %v", items)
	}
}

func TestSessionRoutesRejectMissingSession(t *testing.T) {
	cfg := config.Config{Root: config.Root{Path: t.TempDir(), CommandAllowlist: []string{"echo"}}}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	defer application.Close()
	api := &API{App: application}
	mux := http.NewServeMux()
	api.RegisterControl(mux)

	tests := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/sessions/missing"},
		{method: http.MethodPost, path: "/sessions/missing/stdin", body: `{}`},
		{method: http.MethodGet, path: "/sessions/missing/terminal"},
		{method: http.MethodPost, path: "/sessions/missing/close"},
	}
	for _, tc := range tests {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s %s: expected 404, got %d body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestListToolsInToolHTTPModeStillShowsVisibleRegistryTools(t *testing.T) {
	cfg := config.Config{
		HTTP: config.HTTP{BaseURL: "http://127.0.0.1:8000"},
		ToolHTTPMode: config.ToolHTTPMode{
			Enabled:               true,
			AccessTokenTTLSeconds: 600,
		},
		Root: config.Root{Path: t.TempDir(), CommandAllowlist: []string{"echo", "sh"}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/tools", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	items, _ := body["items"].([]any)
	foundRunCommand := false
	foundAccess := false
	for _, item := range items {
		m, _ := item.(map[string]any)
		switch m["name"] {
		case "run_command":
			foundRunCommand = true
		case "get_temporary_access_token":
			foundAccess = true
		}
	}
	if !foundRunCommand || !foundAccess {
		t.Fatalf("tool http mode should keep visible registry tools discoverable over HTTP: %s", w.Body.String())
	}
}

func TestListToolsSummary(t *testing.T) {
	cfg := config.Config{
		Root: config.Root{CommandAllowlist: []string{"echo", "sh"}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/tools?summary=1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte(`"schema"`)) {
		t.Fatalf("summary response should not contain schema: %s", w.Body.String())
	}
}

func TestGetToolDetail(t *testing.T) {
	cfg := config.Config{
		Root: config.Root{CommandAllowlist: []string{"echo", "sh"}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/tools/run_command", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["name"] != "run_command" {
		t.Fatalf("unexpected body: %v", body)
	}
	if body["description"] == "" {
		t.Fatalf("missing description: %v", body)
	}
	if _, ok := body["schema"].(map[string]any); !ok {
		t.Fatalf("missing schema: %v", body)
	}
}

func TestUploadFileDetailIncludesOpenAIFileParamMetadata(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		Root: config.Root{Path: root, CommandAllowlist: []string{"echo", "sh"}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/tools/upload_file", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	meta, _ := body["_meta"].(map[string]any)
	params, _ := meta["openai/fileParams"].([]any)
	if len(params) != 1 || params[0] != "file" {
		t.Fatalf("unexpected metadata: %v", body)
	}
}

func TestDefaultHiddenToolNotListedOrAccessible(t *testing.T) {
	cfg := config.Config{
		Root: config.Root{CommandAllowlist: []string{"echo", "sh"}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/tools", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte(`"name":"run_argv"`)) {
		t.Fatalf("hidden tool should not be listed: %s", w.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodGet, "/tools/run_argv", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for hidden tool, got %d body=%s", w2.Code, w2.Body.String())
	}
}

func TestDefaultHiddenToolNotAccessible(t *testing.T) {
	cfg := config.Config{
		Root: config.Root{CommandAllowlist: []string{"echo", "sh"}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/tools/create_download_token", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for default hidden tool, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetToolNotFound(t *testing.T) {
	cfg := config.Config{
		Root: config.Root{},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/tools/missing", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"code":"tool_not_found"`)) {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestToolsMethodNotAllowed(t *testing.T) {
	cfg := config.Config{
		Root: config.Root{},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/tools", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateTaskRunCommandAndFetchLogs(t *testing.T) {
	cfg := config.Config{
		Root: config.Root{CommandAllowlist: []string{"echo", "sh"}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	body, _ := json.Marshal(map[string]any{
		"tool": "run_command",
		"args": map[string]any{"command": "echo hello"},
	})
	req := httptest.NewRequest(http.MethodPost, "/tasks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	taskID, _ := created["task_id"].(string)
	if taskID == "" {
		t.Fatalf("missing task_id")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req2 := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID, nil)
		w2 := httptest.NewRecorder()
		mux.ServeHTTP(w2, req2)
		if w2.Code != http.StatusOK {
			t.Fatalf("get task code=%d body=%s", w2.Code, w2.Body.String())
		}
		var task map[string]any
		_ = json.Unmarshal(w2.Body.Bytes(), &task)
		if task["status"] == "succeeded" || task["status"] == "failed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	req3 := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID+"/logs?cursor=0", nil)
	w3 := httptest.NewRecorder()
	mux.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Fatalf("logs code=%d body=%s", w3.Code, w3.Body.String())
	}
	if !bytes.Contains(w3.Body.Bytes(), []byte("hello")) {
		t.Fatalf("expected logs to contain hello, got %s", w3.Body.String())
	}
}

func TestCreateTaskRejectsDefaultHiddenTool(t *testing.T) {
	cfg := config.Config{
		Root: config.Root{CommandAllowlist: []string{"echo", "sh"}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	body, _ := json.Marshal(map[string]any{
		"tool": "run_argv",
		"args": map[string]any{"command": "echo hello"},
	})
	req := httptest.NewRequest(http.MethodPost, "/tasks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateTaskApplyPatch(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "a.txt")
	if err := os.WriteFile(target, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	cfg := config.Config{
		Root: config.Root{Path: root, CommandAllowlist: []string{"echo", "sh"}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: a.txt",
		"@@",
		"-hello",
		"+world",
		"*** End Patch",
	}, "\n")
	body, _ := json.Marshal(map[string]any{
		"tool": "apply_patch",
		"args": map[string]any{"patch": patch},
	})
	req := httptest.NewRequest(http.MethodPost, "/tasks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}

	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	taskID, _ := created["task_id"].(string)
	if taskID == "" {
		t.Fatalf("missing task_id: %v", created)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req2 := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID+"/result", nil)
		w2 := httptest.NewRecorder()
		mux.ServeHTTP(w2, req2)
		if w2.Code != http.StatusOK {
			t.Fatalf("result code=%d body=%s", w2.Code, w2.Body.String())
		}
		var result map[string]any
		_ = json.Unmarshal(w2.Body.Bytes(), &result)
		if ready, _ := result["ready"].(bool); ready {
			if result["status"] != "succeeded" {
				t.Fatalf("unexpected result: %v", result)
			}
			if got := string(mustReadHTTPFile(t, target)); got != "world\n" {
				t.Fatalf("unexpected file content: %q", got)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task result not ready in time")
}

func TestGetTaskResult(t *testing.T) {
	cfg := config.Config{
		Root: config.Root{CommandAllowlist: []string{"echo", "sh"}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	body, _ := json.Marshal(map[string]any{
		"tool": "run_command",
		"args": map[string]any{"command": "echo hello"},
	})
	req := httptest.NewRequest(http.MethodPost, "/tasks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	taskID, _ := created["task_id"].(string)
	if taskID == "" {
		t.Fatalf("missing task_id")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req2 := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID+"/result", nil)
		w2 := httptest.NewRecorder()
		mux.ServeHTTP(w2, req2)
		if w2.Code != http.StatusOK {
			t.Fatalf("result code=%d body=%s", w2.Code, w2.Body.String())
		}
		var result map[string]any
		_ = json.Unmarshal(w2.Body.Bytes(), &result)
		if ready, _ := result["ready"].(bool); ready {
			if result["status"] != "succeeded" {
				t.Fatalf("unexpected result body: %v", result)
			}
			if result["stdout"] != "hello" {
				t.Fatalf("expected stdout=hello, got %v", result["stdout"])
			}
			if _, ok := result["result"].(map[string]any); !ok {
				t.Fatalf("missing structured result: %v", result)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task result not ready in time")
}

func TestSubagentSpawnThroughHTTPTask(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "opencli-fake")
	if err := os.WriteFile(executable, []byte(`#!/bin/sh
set -eu
printf '%s\n' '[{"conversationId":"conversation123","conversationUrl":"https://chatgpt.com/g/g-p-deadbeef/c/conversation123","response":"delegated"}]'
`), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.LogStore = filepath.Join(root, ".localgate")
	cfg.Subagents.Enabled = true
	cfg.Subagents.DefaultProvider = "opencli_chatgpt"
	cfg.Subagents.Providers.OpenCLIChatGPT.Executable = executable
	cfg.Subagents.Providers.OpenCLIChatGPT.DefaultProject = "deadbeef"
	cfg.Root = config.Root{Path: root}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	defer application.Close()
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	body, _ := json.Marshal(map[string]any{
		"tool": "subagent_spawn",
		"args": map[string]any{"name": "reviewer", "instruction": "review code"},
	})
	req := httptest.NewRequest(http.MethodPost, "/tasks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	taskID, _ := created["task_id"].(string)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resultReq := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID+"/result", nil)
		resultW := httptest.NewRecorder()
		mux.ServeHTTP(resultW, resultReq)
		var result map[string]any
		_ = json.Unmarshal(resultW.Body.Bytes(), &result)
		if ready, _ := result["ready"].(bool); ready {
			payload, _ := result["result"].(map[string]any)
			subagentID, _ := payload["subagent_id"].(string)
			if result["status"] != "succeeded" || payload["accepted"] != true || payload["status"] != "busy" || !strings.HasPrefix(subagentID, "sg_") {
				t.Fatalf("unexpected subagent HTTP result: %#v", result)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("subagent HTTP task did not complete")
}

func TestGetTaskResultReadyFalse(t *testing.T) {
	cfg := config.Config{
		Root: config.Root{CommandAllowlist: []string{"sleep", "sh"}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	body, _ := json.Marshal(map[string]any{
		"tool": "run_command",
		"args": map[string]any{"command": "sleep 1"},
	})
	req := httptest.NewRequest(http.MethodPost, "/tasks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	taskID, _ := created["task_id"].(string)
	if taskID == "" {
		t.Fatalf("missing task_id")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID+"/result", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w2.Code, w2.Body.String())
	}
	var result map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &result)
	if ready, _ := result["ready"].(bool); ready {
		t.Fatalf("expected ready=false while task is running, got %v", result)
	}
}

func TestGetTaskResultNotFound(t *testing.T) {
	cfg := config.Config{
		Root: config.Root{},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/tasks/missing/result", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCancelTask(t *testing.T) {
	cfg := config.Config{
		Root: config.Root{CommandAllowlist: []string{"sleep", "sh"}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	body, _ := json.Marshal(map[string]any{
		"tool": "run_command",
		"args": map[string]any{"command": "sleep 10"},
	})
	req := httptest.NewRequest(http.MethodPost, "/tasks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	taskID, _ := created["task_id"].(string)
	if taskID == "" {
		t.Fatalf("missing task_id")
	}

	req2 := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/cancel", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w2.Code, w2.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req3 := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID, nil)
		w3 := httptest.NewRecorder()
		mux.ServeHTTP(w3, req3)
		var task map[string]any
		_ = json.Unmarshal(w3.Body.Bytes(), &task)
		if task["status"] == "canceled" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task not canceled in time")
}

func TestSSEStream(t *testing.T) {
	cfg := config.Config{
		Root: config.Root{CommandAllowlist: []string{"echo", "sh"}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body, _ := json.Marshal(map[string]any{
		"tool": "run_command",
		"args": map[string]any{"command": "echo hello"},
	})
	resp, err := http.Post(srv.URL+"/tasks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	var created map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&created)
	taskID, _ := created["task_id"].(string)
	if taskID == "" {
		t.Fatalf("missing task_id")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/tasks/"+taskID+"/events/stream?cursor=0", nil)
	streamResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stream req: %v", err)
	}
	defer streamResp.Body.Close()
	if streamResp.StatusCode != http.StatusOK {
		t.Fatalf("stream status=%d", streamResp.StatusCode)
	}
	sc := bufio.NewScanner(streamResp.Body)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, "hello") {
			return
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		t.Fatalf("scan err: %v", err)
	}
	t.Fatalf("did not receive hello in stream")
}

func mustReadHTTPFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	return b
}

func TestSSECursorResume(t *testing.T) {
	tmp := t.TempDir()
	script := filepath.Join(tmp, "s.sh")
	if err := os.WriteFile(script, []byte("echo a\nsleep 0.2\necho b\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	cfg := config.Config{
		Root: config.Root{CommandAllowlist: []string{"sh"}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body, _ := json.Marshal(map[string]any{
		"tool": "run_command",
		"args": map[string]any{"command": "sh " + script},
	})
	resp, err := http.Post(srv.URL+"/tasks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	var created map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&created)
	taskID, _ := created["task_id"].(string)
	if taskID == "" {
		t.Fatalf("missing task_id")
	}

	idA := ""
	{
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/tasks/"+taskID+"/events/stream?cursor=0", nil)
		streamResp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("stream req: %v", err)
		}
		sc := bufio.NewScanner(streamResp.Body)
		lastID := ""
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "id: ") {
				lastID = strings.TrimSpace(strings.TrimPrefix(line, "id: "))
			}
			if strings.HasPrefix(line, "data: ") && strings.Contains(line, `"data":"a"`) {
				idA = lastID
				break
			}
		}
		_ = streamResp.Body.Close()
		if idA == "" {
			t.Fatalf("did not receive a")
		}
	}

	{
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/tasks/"+taskID+"/events/stream?cursor="+idA, nil)
		streamResp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("stream req2: %v", err)
		}
		defer streamResp.Body.Close()
		sc := bufio.NewScanner(streamResp.Body)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "data: ") && strings.Contains(line, `"data":"b"`) {
				return
			}
		}
	}
	t.Fatalf("did not resume to b")
}

func TestStreamOnCreate(t *testing.T) {
	cfg := config.Config{
		Root: config.Root{CommandAllowlist: []string{"echo", "sh"}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body, _ := json.Marshal(map[string]any{
		"tool": "run_command",
		"args": map[string]any{"command": "echo hello"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/tasks?stream=1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, "hello") {
			return
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		t.Fatalf("scan err: %v", err)
	}
	t.Fatalf("did not receive hello in stream on create")
}
func TestApprovalFlow(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cfg := config.Config{
		LogStore: dir,
		Admin:    config.AdminConfig{Enabled: true, BaseURL: "http://admin.test", Approval: config.ApprovalConfig{Enabled: true}},
		Root:     config.Root{Path: dir, CommandAllowlist: []string{"rm"}, ApprovalPolicy: config.ApprovalPolicy{Commands: []string{"rm"}}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	body, _ := json.Marshal(map[string]any{
		"tool": "run_command",
		"args": map[string]any{"command": "rm " + target},
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/tasks", bytes.NewReader(body))
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	var pending map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &pending)
	approvalID, _ := pending["approval_id"].(string)
	if approvalID == "" || pending["status"] != "pending_approval" {
		t.Fatalf("unexpected pending body: %v", pending)
	}

	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "http://admin.test/approvals/"+approvalID, nil)
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("get approval code=%d body=%s", w2.Code, w2.Body.String())
	}

	approveBody, _ := json.Marshal(map[string]any{"actor": "alice"})
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "http://admin.test/approvals/"+approvalID+"/approve", bytes.NewReader(approveBody))
	mux.ServeHTTP(w3, req3)
	if w3.Code != http.StatusAccepted {
		t.Fatalf("approve code=%d body=%s", w3.Code, w3.Body.String())
	}
	var approved map[string]any
	_ = json.Unmarshal(w3.Body.Bytes(), &approved)
	taskID, _ := approved["task_id"].(string)
	if taskID == "" {
		t.Fatalf("missing task_id after approve: %v", approved)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		w4 := httptest.NewRecorder()
		req4 := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID, nil)
		mux.ServeHTTP(w4, req4)
		var task map[string]any
		_ = json.Unmarshal(w4.Body.Bytes(), &task)
		if task["status"] == "succeeded" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("expected file removed, stat err=%v", err)
	}
}

func TestApprovalEndpointsRedactSensitiveArgsAndEnforceBrowserOrigin(t *testing.T) {
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.BaseURL = "https://admin.example/localgate"
	cfg.Admin.Approval = config.ApprovalConfig{
		Enabled:            true,
		DefaultTTLSeconds:  600,
		MaxSyncWaitSeconds: 30,
	}
	cfg.Root = config.Root{Path: t.TempDir()}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	defer application.Close()
	req, err := application.Approvals.Create(approval.Request{
		Tool:      "run_command",
		Requester: "test",
		Reason:    "test",
		Args: map[string]any{
			"command": "echo ok",
			"token":   "api-secret-token",
			"nested": map[string]any{
				"password": "api-secret-password",
			},
		},
	})
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	for _, path := range []string{"/approvals", "/approvals/" + req.ID, "/approval-ui/" + req.ID} {
		w := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Host = "admin.example"
		mux.ServeHTTP(w, request)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: code=%d body=%s", path, w.Code, w.Body.String())
		}
		body := w.Body.String()
		if strings.Contains(body, "api-secret-token") || strings.Contains(body, "api-secret-password") {
			t.Fatalf("GET %s leaked sensitive args: %s", path, body)
		}
		if !strings.Contains(body, "[REDACTED]") {
			t.Fatalf("GET %s did not show redaction marker: %s", path, body)
		}
	}

	pageW := httptest.NewRecorder()
	pageReq := httptest.NewRequest(http.MethodGet, "/approval-ui/"+req.ID, nil)
	pageReq.Host = "admin.example"
	mux.ServeHTTP(pageW, pageReq)
	if !strings.Contains(pageW.Body.String(), "https://admin.example/localgate/approvals/"+req.ID+"/approve") {
		t.Fatalf("approval page lost reverse-proxy prefix: %s", pageW.Body.String())
	}

	listW := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/approval-ui?status=pending", nil)
	listReq.Host = "admin.example"
	mux.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK || !strings.Contains(listW.Body.String(), "Approval management") || !strings.Contains(listW.Body.String(), req.ID) {
		t.Fatalf("approval management page failed: code=%d body=%s", listW.Code, listW.Body.String())
	}
	if strings.Contains(listW.Body.String(), "api-secret-token") || strings.Contains(listW.Body.String(), "api-secret-password") {
		t.Fatalf("approval management page leaked sensitive args: %s", listW.Body.String())
	}

	wrongHostW := httptest.NewRecorder()
	wrongHostReq := httptest.NewRequest(http.MethodGet, "/approval-ui", nil)
	wrongHostReq.Host = "control.example"
	mux.ServeHTTP(wrongHostW, wrongHostReq)
	if wrongHostW.Code != http.StatusNotFound {
		t.Fatalf("approval UI should be hidden on non-approval host: code=%d body=%s", wrongHostW.Code, wrongHostW.Body.String())
	}

	crossSiteW := httptest.NewRecorder()
	crossSiteReq := httptest.NewRequest(http.MethodPost, "/approvals/"+req.ID+"/reject", strings.NewReader(""))
	crossSiteReq.Host = "admin.example"
	crossSiteReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	crossSiteReq.Header.Set("Origin", "https://evil.example")
	crossSiteReq.Header.Set("Sec-Fetch-Site", "cross-site")
	mux.ServeHTTP(crossSiteW, crossSiteReq)
	if crossSiteW.Code != http.StatusForbidden {
		t.Fatalf("cross-site approval should be rejected: code=%d body=%s", crossSiteW.Code, crossSiteW.Body.String())
	}

	missingOriginW := httptest.NewRecorder()
	missingOriginReq := httptest.NewRequest(http.MethodPost, "/approvals/"+req.ID+"/reject", strings.NewReader(""))
	missingOriginReq.Host = "admin.example"
	missingOriginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	missingOriginReq.Header.Set("Sec-Fetch-Site", "same-origin")
	mux.ServeHTTP(missingOriginW, missingOriginReq)
	if missingOriginW.Code != http.StatusForbidden {
		t.Fatalf("browser approval without Origin should be rejected: code=%d body=%s", missingOriginW.Code, missingOriginW.Body.String())
	}

	pending, err := application.Approvals.Get(req.ID)
	if err != nil || pending.Status != approval.StatusPending {
		t.Fatalf("rejected browser requests changed approval: %#v err=%v", pending, err)
	}

	sameOriginW := httptest.NewRecorder()
	sameOriginReq := httptest.NewRequest(http.MethodPost, "/approvals/"+req.ID+"/reject", strings.NewReader(""))
	sameOriginReq.Host = "admin.example"
	sameOriginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	sameOriginReq.Header.Set("Origin", "https://admin.example")
	sameOriginReq.Header.Set("Sec-Fetch-Site", "same-origin")
	sameOriginReq.Header.Set("X-LocalGate-Actor", "spoofed-admin")
	mux.ServeHTTP(sameOriginW, sameOriginReq)
	if sameOriginW.Code != http.StatusOK {
		t.Fatalf("same-origin approval should succeed: code=%d body=%s", sameOriginW.Code, sameOriginW.Body.String())
	}
	if contentType := sameOriginW.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("browser approval should return HTML, got %q", contentType)
	}
	if !strings.Contains(sameOriginW.Body.String(), "Approval rejected") || !strings.Contains(sameOriginW.Body.String(), req.TaskID) {
		t.Fatalf("unexpected browser result page: %s", sameOriginW.Body.String())
	}
	resolved, err := application.Approvals.Get(req.ID)
	if err != nil {
		t.Fatalf("get resolved approval: %v", err)
	}
	if resolved.Actor != "local-unauthenticated" || resolved.AuthMethod != "none" {
		t.Fatalf("client-controlled actor header was trusted: %#v", resolved)
	}
}

func TestWriteSubmissionResultReturnsApprovalCancellationCode(t *testing.T) {
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.Approval.MaxSyncWaitSeconds = 1
	cfg.Root = config.Root{Path: t.TempDir()}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	defer application.Close()
	task, _ := application.Tasks.CreateGatedTask(core.Task{}, func(context.Context, string) (int, error) {
		return 0, nil
	})
	if err := application.Tasks.CancelWithReason(task.ID, "approval_rejected", "approval was rejected before execution"); err != nil {
		t.Fatalf("cancel task: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, getErr := application.Tasks.Get(task.ID)
		if getErr != nil {
			t.Fatalf("get task: %v", getErr)
		}
		if current.Status == core.StatusCanceled {
			break
		}
		time.Sleep(time.Millisecond)
	}

	api := &API{App: application}
	w := httptest.NewRecorder()
	api.writeSubmissionResult(context.Background(), w, app.Submission{Task: task}, http.StatusOK)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "approval_rejected" || body.Details["error_code"] != "approval_rejected" {
		t.Fatalf("unexpected cancellation response: %#v", body)
	}
}

func TestSessionExecShortCommand(t *testing.T) {
	cfg := config.Config{
		Root: config.Root{CommandAllowlist: []string{"echo", "cat", "sh"}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	defer application.Close()
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	body, _ := json.Marshal(map[string]any{
		"cmd":               "echo hello",
		"yield_time_ms":     100,
		"max_output_tokens": 20,
	})
	req := httptest.NewRequest(http.MethodPost, "/sessions/exec", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "hello") {
		t.Fatalf("expected hello in response: %s", w.Body.String())
	}
}

func TestSessionStdinTerminalAndClose(t *testing.T) {
	cfg := config.Config{
		Root: config.Root{CommandAllowlist: []string{"echo", "cat", "sh"}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	defer application.Close()
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	startBody, _ := json.Marshal(map[string]any{
		"cmd":               "cat",
		"yield_time_ms":     50,
		"max_output_tokens": 20,
	})
	startReq := httptest.NewRequest(http.MethodPost, "/sessions/exec", bytes.NewReader(startBody))
	startW := httptest.NewRecorder()
	mux.ServeHTTP(startW, startReq)
	if startW.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", startW.Code, startW.Body.String())
	}
	var started map[string]any
	_ = json.Unmarshal(startW.Body.Bytes(), &started)
	sessionID, _ := started["session_id"].(string)
	if sessionID == "" {
		t.Fatalf("missing session_id: %v", started)
	}

	stdinBody, _ := json.Marshal(map[string]any{
		"chars":             "hello\n",
		"yield_time_ms":     100,
		"max_output_tokens": 20,
	})
	stdinReq := httptest.NewRequest(http.MethodPost, "/sessions/"+sessionID+"/stdin", bytes.NewReader(stdinBody))
	stdinW := httptest.NewRecorder()
	mux.ServeHTTP(stdinW, stdinReq)
	if stdinW.Code != http.StatusOK {
		t.Fatalf("stdin code=%d body=%s", stdinW.Code, stdinW.Body.String())
	}
	if !strings.Contains(stdinW.Body.String(), "hello") {
		t.Fatalf("expected stdin output hello: %s", stdinW.Body.String())
	}

	termReq := httptest.NewRequest(http.MethodGet, "/sessions/"+sessionID+"/terminal?max_output_tokens=20", nil)
	termW := httptest.NewRecorder()
	mux.ServeHTTP(termW, termReq)
	if termW.Code != http.StatusOK {
		t.Fatalf("terminal code=%d body=%s", termW.Code, termW.Body.String())
	}
	if !strings.Contains(termW.Body.String(), "hello") {
		t.Fatalf("expected terminal output hello: %s", termW.Body.String())
	}

	closeReq := httptest.NewRequest(http.MethodPost, "/sessions/"+sessionID+"/close", nil)
	closeW := httptest.NewRecorder()
	mux.ServeHTTP(closeW, closeReq)
	if closeW.Code != http.StatusAccepted {
		t.Fatalf("close code=%d body=%s", closeW.Code, closeW.Body.String())
	}
}

func TestUpgradeRequiresUpgradeToken(t *testing.T) {
	cfg := config.Config{
		Upgrade: config.Upgrade{Token: "upgrade-secret"},
		Runtime: config.Runtime{Mode: "http_daemon", Bin: "/tmp/localgate", Bind: "127.0.0.1:18000"},
		Root:    config.Root{},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	defer application.Close()

	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "localgate")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	_, _ = part.Write([]byte("candidate"))
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/system/upgrades", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestUploadRequiresToken(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		HTTP: config.HTTP{BaseURL: "http://127.0.0.1:8000"},
		Root: config.Root{Path: root},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	defer application.Close()

	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "payload.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	_, _ = part.Write([]byte("payload"))
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/system/uploads", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestUploadCreateAndOverwrite(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		HTTP: config.HTTP{BaseURL: "http://127.0.0.1:8000/"},
		Root: config.Root{Path: root},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	defer application.Close()

	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	target := filepath.Join(root, "inbox", "payload.txt")
	credentialRes, err := application.InvokeTool(context.Background(), "create_upload_token", map[string]any{"path": target})
	if err != nil {
		t.Fatalf("create_upload_token: %v", err)
	}
	authValue, _ := credentialRes["auth_value"].(string)
	if authValue == "" {
		t.Fatalf("missing auth value: %#v", credentialRes)
	}
	if credentialRes["upload_url"] != "http://127.0.0.1:8000/system/uploads" {
		t.Fatalf("unexpected upload url: %#v", credentialRes)
	}

	uploadOnce := func(payload string) *httptest.ResponseRecorder {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		part, err := writer.CreateFormFile("file", "payload.txt")
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		_, _ = part.Write([]byte(payload))
		_ = writer.Close()

		req := httptest.NewRequest(http.MethodPost, "/system/uploads", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set(uploadTokenHeader, authValue)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}

	w1 := uploadOnce("first")
	if w1.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w1.Code, w1.Body.String())
	}
	if !bytes.Contains(w1.Body.Bytes(), []byte(`"status":"created"`)) {
		t.Fatalf("unexpected create response: %s", w1.Body.String())
	}
	if !bytes.Contains(w1.Body.Bytes(), []byte(`"overwritten":false`)) {
		t.Fatalf("unexpected create response: %s", w1.Body.String())
	}

	w2 := uploadOnce("second")
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w2.Code, w2.Body.String())
	}
	if !bytes.Contains(w2.Body.Bytes(), []byte(`"status":"overwritten"`)) {
		t.Fatalf("unexpected overwrite response: %s", w2.Body.String())
	}
	if !bytes.Contains(w2.Body.Bytes(), []byte(`"overwritten":true`)) {
		t.Fatalf("unexpected overwrite response: %s", w2.Body.String())
	}

	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(b) != "second" {
		t.Fatalf("unexpected uploaded content: %q", string(b))
	}
}

func TestUploadRejectsMissingFile(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		HTTP: config.HTTP{BaseURL: "http://127.0.0.1:8000"},
		Root: config.Root{Path: root},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	defer application.Close()

	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	target := filepath.Join(root, "inbox", "payload.txt")
	credentialRes, err := application.InvokeTool(context.Background(), "create_upload_token", map[string]any{"path": target})
	if err != nil {
		t.Fatalf("create_upload_token: %v", err)
	}
	authValue, _ := credentialRes["auth_value"].(string)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/system/uploads", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set(uploadTokenHeader, authValue)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestUploadRejectsExpiredToken(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		HTTP: config.HTTP{BaseURL: "http://127.0.0.1:8000"},
		Root: config.Root{Path: root},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	defer application.Close()

	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	grant, err := application.Uploads.Issue(filepath.Join(root, "expired.txt"), time.Millisecond)
	if err != nil {
		t.Fatalf("issue upload token: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "payload.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	_, _ = part.Write([]byte("payload"))
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/system/uploads", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set(uploadTokenHeader, grant.Token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDownloadRequiresToken(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		HTTP: config.HTTP{BaseURL: "http://127.0.0.1:8000"},
		Root: config.Root{Path: root},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	defer application.Close()

	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/system/downloads", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDownloadFileSuccessAndRepeatable(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		HTTP: config.HTTP{BaseURL: "http://127.0.0.1:8000/"},
		Root: config.Root{Path: root},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	defer application.Close()

	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	target := filepath.Join(root, "outbox", "payload.txt")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(target, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	credentialRes, err := application.InvokeTool(context.Background(), "create_download_token", map[string]any{"path": target})
	if err != nil {
		t.Fatalf("create_download_token: %v", err)
	}
	authValue, _ := credentialRes["auth_value"].(string)
	if authValue == "" {
		t.Fatalf("missing auth value: %#v", credentialRes)
	}
	if got, _ := credentialRes["download_url"].(string); !strings.HasPrefix(got, "http://127.0.0.1:8000/system/downloads?token=lgdl_") {
		t.Fatalf("unexpected download url: %#v", credentialRes)
	}

	downloadOnce := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/system/downloads?token="+authValue, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}

	w1 := downloadOnce()
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w1.Code, w1.Body.String())
	}
	if w1.Body.String() != "payload" {
		t.Fatalf("unexpected payload: %q", w1.Body.String())
	}
	if got := w1.Header().Get("Content-Disposition"); !strings.Contains(got, `attachment; filename="payload.txt"`) {
		t.Fatalf("unexpected content-disposition: %q", got)
	}

	w2 := downloadOnce()
	if w2.Code != http.StatusOK {
		t.Fatalf("expected second 200, got %d body=%s", w2.Code, w2.Body.String())
	}
	if w2.Body.String() != "payload" {
		t.Fatalf("unexpected payload on repeat: %q", w2.Body.String())
	}
}

func TestDownloadRejectsExpiredToken(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		HTTP: config.HTTP{BaseURL: "http://127.0.0.1:8000"},
		Root: config.Root{Path: root},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	defer application.Close()

	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	target := filepath.Join(root, "expired.txt")
	if err := os.WriteFile(target, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	grant, err := application.Downloads.Issue(target, time.Millisecond)
	if err != nil {
		t.Fatalf("issue download token: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/system/downloads?token="+grant.Token, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDownloadRejectsMissingFileAfterGrant(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		HTTP: config.HTTP{BaseURL: "http://127.0.0.1:8000"},
		Root: config.Root{Path: root},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	defer application.Close()

	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	target := filepath.Join(root, "gone.txt")
	if err := os.WriteFile(target, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	grant, err := application.Downloads.Issue(target, time.Minute)
	if err != nil {
		t.Fatalf("issue download token: %v", err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove target: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/system/downloads?token="+grant.Token, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestUpgradeCreateAndGetStatus(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "bin", "localgate")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}
	cfg := config.Config{
		LogStore: filepath.Join(tmp, "store"),
		Upgrade:  config.Upgrade{Token: "upgrade-secret"},
		Runtime: config.Runtime{
			Mode: "http_daemon",
			Bin:  target,
			Bind: "127.0.0.1:18000",
		},
		Root: config.Root{},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	defer application.Close()

	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "localgate")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	_, _ = part.Write([]byte("candidate"))
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/system/upgrades", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set(upgradeTokenHeader, "upgrade-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}

	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	upgradeID, _ := created["upgrade_id"].(string)
	taskID, _ := created["task_id"].(string)
	if upgradeID == "" {
		t.Fatalf("missing upgrade_id: %v", created)
	}
	if taskID == "" {
		t.Fatalf("missing task_id: %v", created)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/system/upgrades/"+upgradeID, nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w2.Code, w2.Body.String())
	}
	if !bytes.Contains(w2.Body.Bytes(), []byte(`"id":"`+upgradeID+`"`)) {
		t.Fatalf("unexpected body: %s", w2.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		task, err := application.Tasks.Get(taskID)
		if err == nil && (task.Status == "succeeded" || task.Status == "failed" || task.Status == "canceled" || task.Status == "timeout") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("upgrade task did not reach terminal state")
}

func TestUpgradeConflictWhenActiveUpgradeExists(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "bin", "localgate")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}
	cfg := config.Config{
		LogStore: filepath.Join(tmp, "store"),
		Upgrade:  config.Upgrade{Token: "upgrade-secret"},
		Runtime: config.Runtime{
			Mode: "http_daemon",
			Bin:  target,
			Bind: "127.0.0.1:18000",
		},
		Root: config.Root{},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	defer application.Close()
	if _, err := application.Upgrades.Create(strings.NewReader("active"), "localgate"); err != nil {
		t.Fatalf("seed active upgrade: %v", err)
	}

	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "localgate")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	_, _ = part.Write([]byte("candidate"))
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/system/upgrades", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set(upgradeTokenHeader, "upgrade-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", w.Code, w.Body.String())
	}
}
