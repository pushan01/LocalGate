package adminapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"localgate/internal/app"
	"localgate/internal/config"
	"localgate/internal/core"
)

func TestFileTerminalStartsInCurrentDirectoryAndReadsOutput(t *testing.T) {
	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	application, mux := newFileTerminalTestAPI(t, []config.Workspace{{
		ID:               "ws",
		Roots:            []string{root},
		CWD:              root,
		CommandAllowlist: []string{"printf", "cat", "sh", "/bin/sh"},
	}}, false)

	start := createFileTerminal(t, mux, createFileTerminalRequest{
		WorkspaceID: "ws",
		RootID:      "0",
		Path:        "docs",
		Command:     "printf hello",
		TTY:         boolPtr(false),
		YieldTimeMS: 50,
	})
	task := waitTerminalTask(t, application, start.TaskID)
	sessionID, _ := task.Result["session_id"].(string)
	if sessionID == "" {
		t.Fatalf("missing session id: %#v", task.Result)
	}
	session, err := application.Sessions.GetForWorkspace(sessionID, "ws")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	realDocs, err := filepath.EvalSymlinks(docs)
	if err != nil {
		t.Fatalf("resolve docs: %v", err)
	}
	if session.CWD != realDocs || session.TaskID != start.TaskID || session.Requester != "admin:file-browser" || session.Transport != "admin" {
		t.Fatalf("unexpected session metadata: %#v", session)
	}

	var read struct {
		Items      []core.SessionOutput `json:"items"`
		NextOffset int                  `json:"next_offset"`
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		req := httptest.NewRequest(http.MethodGet, "https://admin.test/api/admin/v1/file-browser/terminals/"+sessionID+"?workspace_id=ws&cursor=0", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("read expected 200, got %d body=%s", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &read); err != nil {
			t.Fatalf("decode read: %v", err)
		}
		if strings.Contains(joinTerminalTestOutput(read.Items), "hello") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal output missing: %#v", read.Items)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if read.NextOffset <= 0 {
		t.Fatalf("expected positive cursor: %#v", read)
	}
}

func TestFileTerminalStdinCloseAndWorkspaceOwnership(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	application, mux := newFileTerminalTestAPI(t, []config.Workspace{
		{ID: "ws-a", Roots: []string{rootA}, CWD: rootA, CommandAllowlist: []string{"cat", "sh", "/bin/sh"}},
		{ID: "ws-b", Roots: []string{rootB}, CWD: rootB, CommandAllowlist: []string{"cat", "sh", "/bin/sh"}},
	}, false)
	start := createFileTerminal(t, mux, createFileTerminalRequest{
		WorkspaceID: "ws-a",
		RootID:      "0",
		Command:     "cat",
		TTY:         boolPtr(false),
		YieldTimeMS: 20,
	})
	task := waitTerminalTask(t, application, start.TaskID)
	sessionID, _ := task.Result["session_id"].(string)
	if sessionID == "" {
		t.Fatalf("missing session id: %#v", task.Result)
	}

	wrongReq := httptest.NewRequest(http.MethodGet, "https://admin.test/api/admin/v1/file-browser/terminals/"+sessionID+"?workspace_id=ws-b", nil)
	wrongW := httptest.NewRecorder()
	mux.ServeHTTP(wrongW, wrongReq)
	if wrongW.Code != http.StatusForbidden {
		t.Fatalf("cross-workspace read expected 403, got %d body=%s", wrongW.Code, wrongW.Body.String())
	}

	inputBody, _ := json.Marshal(fileTerminalInputRequest{WorkspaceID: "ws-a", Chars: "hello\n", Cursor: 0, WaitMS: 100})
	inputReq := httptest.NewRequest(http.MethodPost, "https://admin.test/api/admin/v1/file-browser/terminals/"+sessionID+"/stdin", bytes.NewReader(inputBody))
	inputReq.Header.Set("Content-Type", "application/json")
	inputReq.Header.Set("Origin", "https://admin.test")
	inputW := httptest.NewRecorder()
	mux.ServeHTTP(inputW, inputReq)
	if inputW.Code != http.StatusOK {
		t.Fatalf("stdin expected 200, got %d body=%s", inputW.Code, inputW.Body.String())
	}
	var inputResult struct {
		Items []core.SessionOutput `json:"items"`
	}
	if err := json.Unmarshal(inputW.Body.Bytes(), &inputResult); err != nil {
		t.Fatalf("decode stdin: %v", err)
	}
	if !strings.Contains(joinTerminalTestOutput(inputResult.Items), "hello") {
		t.Fatalf("stdin output missing: %#v", inputResult.Items)
	}

	closeBody, _ := json.Marshal(fileTerminalCloseRequest{WorkspaceID: "ws-a"})
	closeReq := httptest.NewRequest(http.MethodPost, "https://admin.test/api/admin/v1/file-browser/terminals/"+sessionID+"/close", bytes.NewReader(closeBody))
	closeReq.Header.Set("Content-Type", "application/json")
	closeReq.Header.Set("Origin", "https://admin.test")
	closeW := httptest.NewRecorder()
	mux.ServeHTTP(closeW, closeReq)
	if closeW.Code != http.StatusAccepted {
		t.Fatalf("close expected 202, got %d body=%s", closeW.Code, closeW.Body.String())
	}
	closed, err := application.Sessions.GetForWorkspace(sessionID, "ws-a")
	if err != nil {
		t.Fatalf("get closed session: %v", err)
	}
	if closed.Status != core.SessionClosed {
		t.Fatalf("expected closed session, got %#v", closed)
	}
}

func TestFileTerminalReturnsApprovalTaskContext(t *testing.T) {
	root := t.TempDir()
	application, mux := newFileTerminalTestAPI(t, []config.Workspace{{
		ID:               "ws",
		Roots:            []string{root},
		CWD:              root,
		CommandAllowlist: []string{"printf", "sh", "/bin/sh"},
	}}, true)
	start := createFileTerminal(t, mux, createFileTerminalRequest{
		WorkspaceID: "ws",
		RootID:      "0",
		Command:     "printf hello",
		TTY:         boolPtr(false),
	})
	if start.Status != core.StatusAwaitingApproval || start.Approval == nil || start.Approval.ID == "" {
		t.Fatalf("expected approval response: %#v", start)
	}
	task, err := application.Tasks.Get(start.TaskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Status != core.StatusAwaitingApproval || task.ApprovalID != start.Approval.ID || task.WorkspaceID != "ws" {
		t.Fatalf("unexpected approval task: %#v", task)
	}
}

func TestFileTerminalRejectsNonDirectoryAndCrossSiteMutation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	_, mux := newFileTerminalTestAPI(t, []config.Workspace{{
		ID:               "ws",
		Roots:            []string{root},
		CWD:              root,
		CommandAllowlist: []string{"printf", "sh", "/bin/sh"},
	}}, false)
	body, _ := json.Marshal(createFileTerminalRequest{WorkspaceID: "ws", RootID: "0", Path: "note.txt", Command: "printf hello"})
	req := httptest.NewRequest(http.MethodPost, "https://admin.test/api/admin/v1/file-browser/terminals", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.test")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("file workdir expected 400, got %d body=%s", w.Code, w.Body.String())
	}

	crossReq := httptest.NewRequest(http.MethodPost, "https://admin.test/api/admin/v1/file-browser/terminals", bytes.NewReader(body))
	crossReq.Header.Set("Content-Type", "application/json")
	crossReq.Header.Set("Origin", "https://evil.test")
	crossReq.Header.Set("Sec-Fetch-Site", "cross-site")
	crossW := httptest.NewRecorder()
	mux.ServeHTTP(crossW, crossReq)
	if crossW.Code != http.StatusForbidden {
		t.Fatalf("cross-site create expected 403, got %d body=%s", crossW.Code, crossW.Body.String())
	}
}

func TestFileTerminalStreamPushesOutputAndFinalState(t *testing.T) {
	root := t.TempDir()
	application, mux := newFileTerminalTestAPI(t, []config.Workspace{{ID: "ws", Roots: []string{root}, CWD: root}}, false)
	session := application.Sessions.Create(core.Session{WorkspaceID: "ws"}, core.SessionController{})
	if err := application.Sessions.Start(session.ID); err != nil {
		t.Fatalf("start session: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "https://admin.test/api/admin/v1/file-browser/terminals/"+session.ID+"/stream?workspace_id=ws&cursor=0", nil).WithContext(ctx)
	w := newTerminalStreamWriter()
	done := make(chan struct{})
	go func() {
		defer close(done)
		mux.ServeHTTP(w, req)
	}()
	w.waitFlush(t)
	if got := w.bodyString(); !strings.Contains(got, "event: terminal") || !strings.Contains(got, `"status":"running"`) {
		t.Fatalf("initial stream event missing: %s", got)
	}

	if err := application.Sessions.AppendOutput(session.ID, "stdout", "streamed"); err != nil {
		t.Fatalf("append output: %v", err)
	}
	w.waitFlush(t)
	if got := w.bodyString(); !strings.Contains(got, "streamed") || !strings.Contains(got, "id: 1") {
		t.Fatalf("output stream event missing: %s", got)
	}
	if err := application.Sessions.SetOutputError(session.ID, "stderr", context.DeadlineExceeded); err != nil {
		t.Fatalf("set output error: %v", err)
	}
	w.waitFlush(t)
	if got := w.bodyString(); !strings.Contains(got, `"output_error":"stderr: context deadline exceeded"`) {
		t.Fatalf("output error stream event missing: %s", got)
	}

	if err := application.Sessions.Finish(session.ID, 0, nil); err != nil {
		t.Fatalf("finish session: %v", err)
	}
	w.waitFlush(t)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("terminal stream did not close after final state")
	}
	if got := w.bodyString(); !strings.Contains(got, `"status":"exited"`) {
		t.Fatalf("final stream event missing: %s", got)
	}
}

func TestFileTerminalStreamRejectsWorkspaceAndInvalidCursor(t *testing.T) {
	root := t.TempDir()
	application, mux := newFileTerminalTestAPI(t, []config.Workspace{{ID: "ws", Roots: []string{root}, CWD: root}, {ID: "other", Roots: []string{t.TempDir()}}}, false)
	session := application.Sessions.Create(core.Session{WorkspaceID: "ws"}, core.SessionController{})

	wrongReq := httptest.NewRequest(http.MethodGet, "https://admin.test/api/admin/v1/file-browser/terminals/"+session.ID+"/stream?workspace_id=other", nil)
	wrongW := httptest.NewRecorder()
	mux.ServeHTTP(wrongW, wrongReq)
	if wrongW.Code != http.StatusForbidden {
		t.Fatalf("cross-workspace stream expected 403, got %d body=%s", wrongW.Code, wrongW.Body.String())
	}

	invalidReq := httptest.NewRequest(http.MethodGet, "https://admin.test/api/admin/v1/file-browser/terminals/"+session.ID+"/stream?workspace_id=ws&cursor=-1", nil)
	invalidW := httptest.NewRecorder()
	mux.ServeHTTP(invalidW, invalidReq)
	if invalidW.Code != http.StatusBadRequest {
		t.Fatalf("invalid stream cursor expected 400, got %d body=%s", invalidW.Code, invalidW.Body.String())
	}
}

func TestFileTerminalStreamRequiresFlusher(t *testing.T) {
	root := t.TempDir()
	application, mux := newFileTerminalTestAPI(t, []config.Workspace{{ID: "ws", Roots: []string{root}, CWD: root}}, false)
	session := application.Sessions.Create(core.Session{WorkspaceID: "ws"}, core.SessionController{})
	req := httptest.NewRequest(http.MethodGet, "https://admin.test/api/admin/v1/file-browser/terminals/"+session.ID+"/stream?workspace_id=ws", nil)
	w := &terminalNonFlushingWriter{header: make(http.Header)}
	mux.ServeHTTP(w, req)
	if w.status != http.StatusInternalServerError || !strings.Contains(w.body.String(), "streaming is not supported") {
		t.Fatalf("unsupported flusher expected 500, got %d body=%s", w.status, w.body.String())
	}
}

type terminalStreamWriter struct {
	mu      sync.Mutex
	header  http.Header
	status  int
	body    bytes.Buffer
	flushes chan struct{}
}

func newTerminalStreamWriter() *terminalStreamWriter {
	return &terminalStreamWriter{header: make(http.Header), flushes: make(chan struct{}, 8)}
}

func (w *terminalStreamWriter) Header() http.Header { return w.header }

func (w *terminalStreamWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.status == 0 {
		w.status = status
	}
}

func (w *terminalStreamWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(data)
}

func (w *terminalStreamWriter) Flush() {
	select {
	case w.flushes <- struct{}{}:
	default:
	}
}

func (w *terminalStreamWriter) waitFlush(t *testing.T) {
	t.Helper()
	select {
	case <-w.flushes:
	case <-time.After(time.Second):
		t.Fatal("terminal stream did not flush")
	}
}

func (w *terminalStreamWriter) bodyString() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String()
}

type terminalNonFlushingWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *terminalNonFlushingWriter) Header() http.Header    { return w.header }
func (w *terminalNonFlushingWriter) WriteHeader(status int) { w.status = status }
func (w *terminalNonFlushingWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(data)
}

type fileTerminalStartResponse struct {
	TaskID   string      `json:"task_id"`
	Status   core.Status `json:"status"`
	Approval *struct {
		ID string `json:"id"`
	} `json:"approval"`
}

func newFileTerminalTestAPI(t *testing.T, workspaces []config.Workspace, approvals bool) (*app.App, *http.ServeMux) {
	t.Helper()
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Admin.Approval.Enabled = approvals
	cfg.FileBrowser.Enabled = true
	cfg.Workspaces = workspaces
	if approvals {
		cfg.Admin.Approval.Rules = []config.ApprovalRule{{
			ID:     "terminal",
			Effect: "require_approval",
			Tools:  []string{"exec_command"},
			Risk:   "high",
		}}
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	mux := http.NewServeMux()
	defaultWS := ""
	if len(workspaces) > 0 {
		defaultWS = workspaces[0].ID
	}
	(&API{App: application, DefaultWSID: defaultWS, BaseURL: "https://admin.test"}).Register(mux)
	return application, mux
}

func createFileTerminal(t *testing.T, mux *http.ServeMux, request createFileTerminalRequest) fileTerminalStartResponse {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal create terminal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://admin.test/api/admin/v1/file-browser/terminals", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.test")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("create terminal expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	var response fileTerminalStartResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode create terminal: %v", err)
	}
	if response.TaskID == "" {
		t.Fatalf("missing terminal task: %#v", response)
	}
	return response
}

func waitTerminalTask(t *testing.T, application *app.App, taskID string) *core.Task {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		task, err := application.Tasks.Get(taskID)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		switch task.Status {
		case core.StatusSucceeded:
			return task
		case core.StatusFailed, core.StatusCanceled, core.StatusTimeout:
			t.Fatalf("terminal task failed: %#v", task)
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal task did not finish: %#v", task)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func joinTerminalTestOutput(items []core.SessionOutput) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, item.Data)
	}
	return strings.Join(parts, "")
}

func boolPtr(value bool) *bool { return &value }
