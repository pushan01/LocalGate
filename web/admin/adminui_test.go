package adminui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesSPAAndEnforcesHost(t *testing.T) {
	handler := Handler("https://admin.test")
	for _, target := range []string{"https://admin.test/admin", "https://admin.test/admin/history/task-id", "https://admin.test/admin/files"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "LocalGate Admin") {
			t.Fatalf("unexpected response for %s: code=%d body=%s", target, w.Code, w.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "https://control.test/admin", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected wrong host 404, got %d", w.Code)
	}
}

func TestFileBrowserUIIsBundled(t *testing.T) {
	data, err := fs.ReadFile(assets, "dist/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	text := string(data)
	for _, expected := range []string{"/admin/files", "Files", "/file-browser/workspaces", "/file-browser/files/list", "/file-browser/files/content", "/file-browser/files/mkdir", "/file-browser/files/rename", "/file-browser/files/delete", "/file-browser/files/download", "/file-browser/files/upload", "data-file-open", "data-file-editor", "data-file-save", "data-file-rename", "data-file-delete", "data-file-download", "data-file-new-file", "data-file-new-folder", "data-file-upload", "data-file-upload-input", "data-file-entry-rename", "data-file-entry-delete", "confirmDiscardFileChanges", "beforeunload", "file_changed"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("admin file browser UI missing %q", expected)
		}
	}
}

func TestDeploymentUIIncludesAccessControls(t *testing.T) {
	data, err := fs.ReadFile(assets, "dist/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	text := string(data)
	for _, expected := range []string{"Make public", "Make private", "set-public", "set-private", "<th>Access</th>"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("admin deployment UI missing %q", expected)
		}
	}
}
