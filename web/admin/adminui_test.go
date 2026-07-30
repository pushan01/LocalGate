package adminui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	for _, expected := range []string{"/admin/files", "Files", "/file-browser/workspaces", "/file-browser/files/list", "/file-browser/files/content", "/file-browser/files/mkdir", "/file-browser/files/rename", "/file-browser/files/delete", "/file-browser/files/download", "/file-browser/files/preview", "/file-browser/files/upload", "/file-browser/git/status", "/file-browser/git/diff", "/file-browser/terminals", "/stream?", "text/event-stream", "consumeSSE", "实时终端流不可用", "data-file-open", "data-file-editor", "data-file-save", "data-file-rename", "data-file-delete", "data-file-download", "data-file-new-file", "data-file-new-folder", "data-file-upload", "data-file-upload-input", "data-file-entry-rename", "data-file-entry-delete", "data-file-markdown-preview", "data-file-image-preview", "data-file-conflict-reload", "data-file-conflict-overwrite", "data-file-conflict-diff", "data-file-tab", "data-file-tab-close", "data-file-git-toggle", "data-file-git-diff", "data-file-git-open", "data-file-terminal-start", "data-file-terminal-stdin", "data-file-terminal-close", "FileTabStore", "FileGitState", "FileTerminalState", "terminal_task", "terminal_session", "return_to", "preserveFileTabs", "reloadActiveFile", "renderMarkdown", "requestBlob", "confirmDiscardFileChanges", "beforeunload", "file_changed", "file-etag"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("admin file browser UI missing %q", expected)
		}
	}
	styles, err := fs.ReadFile(assets, "dist/styles.css")
	if err != nil {
		t.Fatalf("read styles.css: %v", err)
	}
	for _, expected := range []string{".markdown-preview", ".file-preview-image", ".diff-grid", ".diff-line-changed", ".file-tabs", ".file-tab.active", ".file-tab-dirty", ".file-git-card", ".git-status-row", ".git-diff", ".file-terminal-card", ".file-terminal-output", ".file-terminal-input", ".file-etag"} {
		if !strings.Contains(string(styles), expected) {
			t.Fatalf("admin file browser styles missing %q", expected)
		}
	}
}

func TestAdminSidebarCanCollapse(t *testing.T) {
	appData, err := fs.ReadFile(assets, "dist/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	stylesData, err := fs.ReadFile(assets, "dist/styles.css")
	if err != nil {
		t.Fatalf("read styles.css: %v", err)
	}
	appText := string(appData)
	for _, expected := range []string{"localgate_admin_sidebar_collapsed", "toggle-sidebar", "setSidebarCollapsed", "sidebar-collapsed", "aria-expanded"} {
		if !strings.Contains(appText, expected) {
			t.Fatalf("admin sidebar UI missing %q", expected)
		}
	}
	stylesText := string(stylesData)
	for _, expected := range []string{".shell.sidebar-collapsed", ".sidebar-toggle", ".nav-icon", "grid-template-columns: 72px"} {
		if !strings.Contains(stylesText, expected) {
			t.Fatalf("admin sidebar styles missing %q", expected)
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

func TestAdminFrontendBuildInputsExist(t *testing.T) {
	for _, name := range []string{
		"package.json",
		"package-lock.json",
		filepath.Join("scripts", "build.mjs"),
		filepath.Join("src", "app.js"),
		filepath.Join("src", "file-tabs.js"),
		filepath.Join("src", "file-tabs.test.js"),
		filepath.Join("src", "file-terminal.js"),
		filepath.Join("src", "file-terminal.test.js"),
		filepath.Join("src", "file-terminal-stream.js"),
		filepath.Join("src", "file-terminal-stream.test.js"),
		filepath.Join("src", "file-git.js"),
		filepath.Join("src", "file-git.test.js"),
		filepath.Join("src", "styles.css"),
		filepath.Join("src", "index.html"),
	} {
		if _, err := os.Stat(name); err != nil {
			t.Fatalf("admin frontend build input %q unavailable: %v", name, err)
		}
	}
}
