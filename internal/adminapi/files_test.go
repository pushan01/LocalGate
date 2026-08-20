package adminapi

import (
	"bytes"
	"encoding/json"
	"errors"
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

func TestFileBrowserListAndContent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Admin.Approval.Enabled = false
	cfg.Root = config.Root{Path: root}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	mux := http.NewServeMux()
	(&API{App: application, BaseURL: "https://admin.test"}).Register(mux)

	listReq := httptest.NewRequest(http.MethodGet, "https://admin.test/api/admin/v1/file-browser/files/list", nil)
	listW := httptest.NewRecorder()
	mux.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", listW.Code, listW.Body.String())
	}
	var listBody struct {
		Entries []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(listW.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listBody.Entries) != 2 || listBody.Entries[0].Name != "docs" || listBody.Entries[0].Type != "directory" || listBody.Entries[1].Name != "README.md" {
		t.Fatalf("unexpected entries: %#v", listBody.Entries)
	}

	contentReq := httptest.NewRequest(http.MethodGet, "https://admin.test/api/admin/v1/file-browser/files/content?path=README.md", nil)
	contentW := httptest.NewRecorder()
	mux.ServeHTTP(contentW, contentReq)
	if contentW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", contentW.Code, contentW.Body.String())
	}
	var contentBody struct {
		Content  string `json:"content"`
		Language string `json:"language"`
		ETag     string `json:"etag"`
	}
	if err := json.Unmarshal(contentW.Body.Bytes(), &contentBody); err != nil {
		t.Fatalf("decode content: %v", err)
	}
	if contentBody.Content != "# Hello\n" || contentBody.Language != "markdown" || contentBody.ETag == "" {
		t.Fatalf("unexpected content: %#v", contentBody)
	}
}

func TestFileBrowserSaveContent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Admin.Approval.Enabled = false
	cfg.FileBrowser.RequireApprovalForWrite = false
	cfg.Root = config.Root{Path: root}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	mux := http.NewServeMux()
	(&API{App: application, BaseURL: "https://admin.test"}).Register(mux)

	contentReq := httptest.NewRequest(http.MethodGet, "https://admin.test/api/admin/v1/file-browser/files/content?path=note.txt", nil)
	contentW := httptest.NewRecorder()
	mux.ServeHTTP(contentW, contentReq)
	if contentW.Code != http.StatusOK {
		t.Fatalf("read expected 200, got %d body=%s", contentW.Code, contentW.Body.String())
	}
	var contentBody struct {
		ETag string `json:"etag"`
	}
	if err := json.Unmarshal(contentW.Body.Bytes(), &contentBody); err != nil {
		t.Fatalf("decode content: %v", err)
	}
	body, err := json.Marshal(saveFileContentRequest{Path: "note.txt", Content: "new", ETag: contentBody.ETag})
	if err != nil {
		t.Fatalf("marshal save request: %v", err)
	}
	putReq := httptest.NewRequest(http.MethodPut, "https://admin.test/api/admin/v1/file-browser/files/content", bytes.NewReader(body))
	putReq.Header.Set("Content-Type", "application/json")
	putReq.Header.Set("Origin", "https://admin.test")
	putW := httptest.NewRecorder()
	mux.ServeHTTP(putW, putReq)
	if putW.Code != http.StatusOK {
		t.Fatalf("save expected 200, got %d body=%s", putW.Code, putW.Body.String())
	}
	if got, err := os.ReadFile(filepath.Join(root, "note.txt")); err != nil || string(got) != "new" {
		t.Fatalf("unexpected saved content: %q err=%v", got, err)
	}

	staleReq := httptest.NewRequest(http.MethodPut, "https://admin.test/api/admin/v1/file-browser/files/content", bytes.NewReader(body))
	staleReq.Header.Set("Content-Type", "application/json")
	staleReq.Header.Set("Origin", "https://admin.test")
	staleW := httptest.NewRecorder()
	mux.ServeHTTP(staleW, staleReq)
	if staleW.Code != http.StatusConflict {
		t.Fatalf("stale save expected 409, got %d body=%s", staleW.Code, staleW.Body.String())
	}
}

func TestFileBrowserMkdir(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Admin.Approval.Enabled = false
	cfg.FileBrowser.RequireApprovalForWrite = false
	cfg.Root = config.Root{Path: root}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	mux := http.NewServeMux()
	(&API{App: application, BaseURL: "https://admin.test"}).Register(mux)
	body, err := json.Marshal(mkdirRequest{Path: "docs"})
	if err != nil {
		t.Fatalf("marshal mkdir request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://admin.test/api/admin/v1/file-browser/files/mkdir", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.test")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	if info, err := os.Stat(filepath.Join(root, "docs")); err != nil || !info.IsDir() {
		t.Fatalf("directory not created: info=%#v err=%v", info, err)
	}
}

func TestFileBrowserRename(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "old.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Admin.Approval.Enabled = false
	cfg.FileBrowser.RequireApprovalForWrite = false
	cfg.Root = config.Root{Path: root}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	mux := http.NewServeMux()
	(&API{App: application, BaseURL: "https://admin.test"}).Register(mux)
	body, err := json.Marshal(renameRequest{FromPath: "old.txt", ToPath: "new.txt"})
	if err != nil {
		t.Fatalf("marshal rename request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://admin.test/api/admin/v1/file-browser/files/rename", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.test")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got, err := os.ReadFile(filepath.Join(root, "new.txt")); err != nil || string(got) != "hello" {
		t.Fatalf("unexpected renamed content: %q err=%v", got, err)
	}
}

func TestFileBrowserDelete(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "old.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Admin.Approval.Enabled = false
	cfg.FileBrowser.RequireApprovalForDelete = false
	cfg.Root = config.Root{Path: root}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	mux := http.NewServeMux()
	(&API{App: application, BaseURL: "https://admin.test"}).Register(mux)
	body, err := json.Marshal(deleteRequest{Path: "old.txt"})
	if err != nil {
		t.Fatalf("marshal delete request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://admin.test/api/admin/v1/file-browser/files/delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.test")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "old.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file should be deleted, got %v", err)
	}

	body, err = json.Marshal(deleteRequest{Path: "docs", Recursive: true})
	if err != nil {
		t.Fatalf("marshal delete request: %v", err)
	}
	req = httptest.NewRequest(http.MethodPost, "https://admin.test/api/admin/v1/file-browser/files/delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.test")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected confirmation 400, got %d body=%s", w.Code, w.Body.String())
	}
	body, err = json.Marshal(deleteRequest{Path: "docs", Recursive: true, Confirmation: "delete docs"})
	if err != nil {
		t.Fatalf("marshal delete request: %v", err)
	}
	req = httptest.NewRequest(http.MethodPost, "https://admin.test/api/admin/v1/file-browser/files/delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.test")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected recursive delete 200, got %d body=%s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "docs")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("directory should be deleted, got %v", err)
	}
}

func TestFileBrowserDownload(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.bin"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Admin.Approval.Enabled = false
	cfg.Root = config.Root{Path: root}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	mux := http.NewServeMux()
	(&API{App: application, BaseURL: "https://admin.test"}).Register(mux)
	req := httptest.NewRequest(http.MethodGet, "https://admin.test/api/admin/v1/file-browser/files/download?path=file.bin", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "hello" {
		t.Fatalf("unexpected body: %q", got)
	}
	if got := w.Header().Get("Content-Disposition"); got != "attachment; filename=file.bin" {
		t.Fatalf("unexpected content-disposition: %q", got)
	}

	escapeReq := httptest.NewRequest(http.MethodGet, "https://admin.test/api/admin/v1/file-browser/files/download?path=../escape", nil)
	escapeW := httptest.NewRecorder()
	mux.ServeHTTP(escapeW, escapeReq)
	if escapeW.Code != http.StatusBadRequest {
		t.Fatalf("expected escape 400, got %d body=%s", escapeW.Code, escapeW.Body.String())
	}
}

func TestFileBrowserImagePreview(t *testing.T) {
	root := t.TempDir()
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if err := os.WriteFile(filepath.Join(root, "image.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Admin.Approval.Enabled = false
	cfg.Root = config.Root{Path: root}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	mux := http.NewServeMux()
	(&API{App: application, BaseURL: "https://admin.test"}).Register(mux)

	req := httptest.NewRequest(http.MethodGet, "https://admin.test/api/admin/v1/file-browser/files/preview?path=image.png", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("preview expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("unexpected content type %q", got)
	}
	if !strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline") {
		t.Fatalf("unexpected content disposition %q", w.Header().Get("Content-Disposition"))
	}
	if !bytes.Equal(w.Body.Bytes(), png) {
		t.Fatalf("unexpected preview body %v", w.Body.Bytes())
	}

	unsupportedReq := httptest.NewRequest(http.MethodGet, "https://admin.test/api/admin/v1/file-browser/files/preview?path=note.txt", nil)
	unsupportedW := httptest.NewRecorder()
	mux.ServeHTTP(unsupportedW, unsupportedReq)
	if unsupportedW.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("unsupported preview expected 415, got %d body=%s", unsupportedW.Code, unsupportedW.Body.String())
	}
}

func TestFileBrowserUpload(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Admin.Approval.Enabled = false
	cfg.FileBrowser.RequireApprovalForWrite = false
	cfg.Root = config.Root{Path: root}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	mux := http.NewServeMux()
	(&API{App: application, BaseURL: "https://admin.test"}).Register(mux)

	body, contentType := multipartUploadBody(t, map[string]string{"path": "upload.bin", "create": "true"}, "file", "upload.bin", []byte{0, 1, 2})
	req := httptest.NewRequest(http.MethodPost, "https://admin.test/api/admin/v1/file-browser/files/upload", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Origin", "https://admin.test")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	if got, err := os.ReadFile(filepath.Join(root, "upload.bin")); err != nil || !bytes.Equal(got, []byte{0, 1, 2}) {
		t.Fatalf("unexpected uploaded content: %v err=%v", got, err)
	}

	body, contentType = multipartUploadBody(t, map[string]string{"path": "upload.bin", "create": "true"}, "file", "upload.bin", []byte("again"))
	req = httptest.NewRequest(http.MethodPost, "https://admin.test/api/admin/v1/file-browser/files/upload", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Origin", "https://admin.test")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected conflict, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestFileBrowserSaveRequiresApprovalWhenConfigured(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Admin.Approval.Enabled = false
	cfg.FileBrowser.RequireApprovalForWrite = true
	cfg.Root = config.Root{Path: root}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	mux := http.NewServeMux()
	(&API{App: application, BaseURL: "https://admin.test"}).Register(mux)
	body, err := json.Marshal(saveFileContentRequest{Path: "note.txt", Content: "new", Create: true})
	if err != nil {
		t.Fatalf("marshal save request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "https://admin.test/api/admin/v1/file-browser/files/content", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.test")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusPreconditionRequired {
		t.Fatalf("expected approval required 428, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestFileBrowserSaveApprovalFlow(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Admin.Approval.Enabled = true
	cfg.FileBrowser.RequireApprovalForWrite = true
	cfg.Root = config.Root{Path: root}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	mux := http.NewServeMux()
	(&API{App: application, BaseURL: "https://admin.test"}).Register(mux)
	body, err := json.Marshal(saveFileContentRequest{Path: "note.txt", Content: "approved", Create: true})
	if err != nil {
		t.Fatalf("marshal save request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "https://admin.test/api/admin/v1/file-browser/files/content", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.test")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	var submitBody struct {
		TaskID   string `json:"task_id"`
		Approval struct {
			ID      string `json:"id"`
			Summary string `json:"summary"`
		} `json:"approval"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &submitBody); err != nil {
		t.Fatalf("decode submission: %v", err)
	}
	if submitBody.TaskID == "" || submitBody.Approval.ID == "" || submitBody.Approval.Summary != "Save file note.txt" {
		t.Fatalf("unexpected submission: %#v", submitBody)
	}
	stagePath := fileBrowserStagePath(t, application, submitBody.Approval.ID)
	if got, err := os.ReadFile(stagePath); err != nil || string(got) != "approved" {
		t.Fatalf("unexpected staged save content: %q err=%v", got, err)
	}
	if _, err := application.ApproveApproval(submitBody.Approval.ID, approval.Actor{ID: "tester", AuthMethod: "test"}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	waitForTaskStatus(t, application, submitBody.TaskID, core.StatusSucceeded)
	if _, err := os.Stat(stagePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged save payload should be removed, got %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "note.txt")); err != nil || string(got) != "approved" {
		t.Fatalf("unexpected approved content: %q err=%v", got, err)
	}

	records, err := application.Approvals.QueryAudit(map[string]string{"task_id": submitBody.TaskID, "action": "file_browser_write_succeeded"}, 10, 0)
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if len(records) != 1 || records[0].Tool != "file_browser.write" {
		t.Fatalf("expected file browser write audit, got %#v", records)
	}

	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("server changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	overwriteBody, err := json.Marshal(saveFileContentRequest{Path: "note.txt", Content: "forced overwrite", Overwrite: true})
	if err != nil {
		t.Fatalf("marshal overwrite request: %v", err)
	}
	overwriteReq := httptest.NewRequest(http.MethodPut, "https://admin.test/api/admin/v1/file-browser/files/content", bytes.NewReader(overwriteBody))
	overwriteReq.Header.Set("Content-Type", "application/json")
	overwriteReq.Header.Set("Origin", "https://admin.test")
	overwriteW := httptest.NewRecorder()
	mux.ServeHTTP(overwriteW, overwriteReq)
	if overwriteW.Code != http.StatusAccepted {
		t.Fatalf("overwrite expected 202, got %d body=%s", overwriteW.Code, overwriteW.Body.String())
	}
	var overwriteSubmit struct {
		TaskID   string `json:"task_id"`
		Approval struct {
			ID string `json:"id"`
		} `json:"approval"`
	}
	if err := json.Unmarshal(overwriteW.Body.Bytes(), &overwriteSubmit); err != nil {
		t.Fatalf("decode overwrite submission: %v", err)
	}
	if overwriteSubmit.TaskID == "" || overwriteSubmit.Approval.ID == "" {
		t.Fatalf("unexpected overwrite submission: %#v", overwriteSubmit)
	}
	if _, err := application.ApproveApproval(overwriteSubmit.Approval.ID, approval.Actor{ID: "tester", AuthMethod: "test"}); err != nil {
		t.Fatalf("approve overwrite: %v", err)
	}
	waitForTaskStatus(t, application, overwriteSubmit.TaskID, core.StatusSucceeded)
	if got, err := os.ReadFile(filepath.Join(root, "note.txt")); err != nil || string(got) != "forced overwrite" {
		t.Fatalf("unexpected overwrite content: %q err=%v", got, err)
	}
	overwriteRecords, err := application.Approvals.QueryAudit(map[string]string{"task_id": overwriteSubmit.TaskID, "action": "file_browser_write_succeeded"}, 10, 0)
	if err != nil {
		t.Fatalf("query overwrite audit: %v", err)
	}
	if len(overwriteRecords) != 1 || overwriteRecords[0].Tool != "file_browser.write" {
		t.Fatalf("expected overwrite audit, got %#v", overwriteRecords)
	}
}

func TestFileBrowserMkdirApprovalFlow(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Admin.Approval.Enabled = true
	cfg.FileBrowser.RequireApprovalForWrite = true
	cfg.Root = config.Root{Path: root}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	mux := http.NewServeMux()
	(&API{App: application, BaseURL: "https://admin.test"}).Register(mux)
	body, err := json.Marshal(mkdirRequest{Path: "docs"})
	if err != nil {
		t.Fatalf("marshal mkdir request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://admin.test/api/admin/v1/file-browser/files/mkdir", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.test")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	var submitBody struct {
		TaskID   string `json:"task_id"`
		Approval struct {
			ID      string `json:"id"`
			Summary string `json:"summary"`
		} `json:"approval"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &submitBody); err != nil {
		t.Fatalf("decode submission: %v", err)
	}
	if submitBody.TaskID == "" || submitBody.Approval.ID == "" || submitBody.Approval.Summary != "Create directory docs" {
		t.Fatalf("unexpected submission: %#v", submitBody)
	}
	if _, err := application.ApproveApproval(submitBody.Approval.ID, approval.Actor{ID: "tester", AuthMethod: "test"}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	waitForTaskStatus(t, application, submitBody.TaskID, core.StatusSucceeded)
	if info, err := os.Stat(filepath.Join(root, "docs")); err != nil || !info.IsDir() {
		t.Fatalf("directory not created: info=%#v err=%v", info, err)
	}
}

func TestFileBrowserRenameApprovalFlow(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "old.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Admin.Approval.Enabled = true
	cfg.FileBrowser.RequireApprovalForWrite = true
	cfg.Root = config.Root{Path: root}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	mux := http.NewServeMux()
	(&API{App: application, BaseURL: "https://admin.test"}).Register(mux)
	body, err := json.Marshal(renameRequest{FromPath: "old.txt", ToPath: "new.txt"})
	if err != nil {
		t.Fatalf("marshal rename request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://admin.test/api/admin/v1/file-browser/files/rename", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.test")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	var submitBody struct {
		TaskID   string `json:"task_id"`
		Approval struct {
			ID      string `json:"id"`
			Summary string `json:"summary"`
		} `json:"approval"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &submitBody); err != nil {
		t.Fatalf("decode submission: %v", err)
	}
	if submitBody.TaskID == "" || submitBody.Approval.ID == "" || submitBody.Approval.Summary != "Rename old.txt to new.txt" {
		t.Fatalf("unexpected submission: %#v", submitBody)
	}
	if _, err := application.ApproveApproval(submitBody.Approval.ID, approval.Actor{ID: "tester", AuthMethod: "test"}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	waitForTaskStatus(t, application, submitBody.TaskID, core.StatusSucceeded)
	if got, err := os.ReadFile(filepath.Join(root, "new.txt")); err != nil || string(got) != "hello" {
		t.Fatalf("unexpected renamed content: %q err=%v", got, err)
	}
}

func TestFileBrowserDeleteApprovalFlow(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "old.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Admin.Approval.Enabled = true
	cfg.FileBrowser.RequireApprovalForDelete = true
	cfg.Root = config.Root{Path: root}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	mux := http.NewServeMux()
	(&API{App: application, BaseURL: "https://admin.test"}).Register(mux)
	body, err := json.Marshal(deleteRequest{Path: "old.txt"})
	if err != nil {
		t.Fatalf("marshal delete request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://admin.test/api/admin/v1/file-browser/files/delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.test")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	var submitBody struct {
		TaskID   string `json:"task_id"`
		Approval struct {
			ID      string `json:"id"`
			Summary string `json:"summary"`
		} `json:"approval"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &submitBody); err != nil {
		t.Fatalf("decode submission: %v", err)
	}
	if submitBody.TaskID == "" || submitBody.Approval.ID == "" || submitBody.Approval.Summary != "Delete old.txt" {
		t.Fatalf("unexpected submission: %#v", submitBody)
	}
	if _, err := application.ApproveApproval(submitBody.Approval.ID, approval.Actor{ID: "tester", AuthMethod: "test"}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	waitForTaskStatus(t, application, submitBody.TaskID, core.StatusSucceeded)
	if _, err := os.Stat(filepath.Join(root, "old.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file should be deleted, got %v", err)
	}
}

func TestFileBrowserUploadApprovalFlow(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Admin.Approval.Enabled = true
	cfg.FileBrowser.RequireApprovalForWrite = true
	cfg.Root = config.Root{Path: root}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	mux := http.NewServeMux()
	(&API{App: application, BaseURL: "https://admin.test"}).Register(mux)
	body, contentType := multipartUploadBody(t, map[string]string{"path": "upload.bin", "create": "true"}, "file", "upload.bin", []byte("approved"))
	req := httptest.NewRequest(http.MethodPost, "https://admin.test/api/admin/v1/file-browser/files/upload", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Origin", "https://admin.test")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	var submitBody struct {
		TaskID   string `json:"task_id"`
		Approval struct {
			ID      string `json:"id"`
			Summary string `json:"summary"`
		} `json:"approval"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &submitBody); err != nil {
		t.Fatalf("decode submission: %v", err)
	}
	if submitBody.TaskID == "" || submitBody.Approval.ID == "" || submitBody.Approval.Summary != "Upload file upload.bin" {
		t.Fatalf("unexpected submission: %#v", submitBody)
	}
	stagePath := fileBrowserStagePath(t, application, submitBody.Approval.ID)
	if got, err := os.ReadFile(stagePath); err != nil || string(got) != "approved" {
		t.Fatalf("unexpected staged upload content: %q err=%v", got, err)
	}
	if _, err := application.ApproveApproval(submitBody.Approval.ID, approval.Actor{ID: "tester", AuthMethod: "test"}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	waitForTaskStatus(t, application, submitBody.TaskID, core.StatusSucceeded)
	if _, err := os.Stat(stagePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged upload payload should be removed, got %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "upload.bin")); err != nil || string(got) != "approved" {
		t.Fatalf("unexpected uploaded content: %q err=%v", got, err)
	}
}

func fileBrowserStagePath(t *testing.T, application *app.App, approvalID string) string {
	t.Helper()
	req, err := application.Approvals.Get(approvalID)
	if err != nil {
		t.Fatalf("load approval: %v", err)
	}
	stageID, ok := req.Args["content_stage_id"].(string)
	if !ok || stageID == "" {
		t.Fatalf("approval missing content_stage_id: %#v", req.Args)
	}
	return filepath.Join(application.StoreBase, "filebrowser", "staging", stageID+".bin")
}

func multipartUploadBody(t *testing.T, fields map[string]string, fieldName, filename string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write field %s: %v", key, err)
		}
	}
	part, err := writer.CreateFormFile(fieldName, filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return body, writer.FormDataContentType()
}

func waitForTaskStatus(t *testing.T, application *app.App, taskID string, want core.Status) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		task, err := application.Tasks.Get(taskID)
		if err == nil && task.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, err := application.Tasks.Get(taskID)
	if err != nil {
		t.Fatalf("load task %s: %v", taskID, err)
	}
	t.Fatalf("task %s status = %s, want %s: %s", taskID, task.Status, want, task.ErrorMsg)
}

func TestFileBrowserRejectsWrongHostAndEscapes(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Admin.Approval.Enabled = false
	cfg.Root = config.Root{Path: root}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	mux := http.NewServeMux()
	(&API{App: application, BaseURL: "https://admin.test"}).Register(mux)

	wrongHostReq := httptest.NewRequest(http.MethodGet, "https://control.test/api/admin/v1/file-browser/files/list", nil)
	wrongHostW := httptest.NewRecorder()
	mux.ServeHTTP(wrongHostW, wrongHostReq)
	if wrongHostW.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for wrong host, got %d", wrongHostW.Code)
	}

	escapeReq := httptest.NewRequest(http.MethodGet, "https://admin.test/api/admin/v1/file-browser/files/content?path=../secret.txt", nil)
	escapeW := httptest.NewRecorder()
	mux.ServeHTTP(escapeW, escapeReq)
	if escapeW.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for escape, got %d body=%s", escapeW.Code, escapeW.Body.String())
	}
}

func TestFileBrowserDisabled(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Admin.Approval.Enabled = false
	cfg.FileBrowser.Enabled = false
	cfg.Root = config.Root{Path: root}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	mux := http.NewServeMux()
	(&API{App: application, BaseURL: "https://admin.test"}).Register(mux)
	req := httptest.NewRequest(http.MethodGet, "https://admin.test/api/admin/v1/file-browser/files/list", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}
