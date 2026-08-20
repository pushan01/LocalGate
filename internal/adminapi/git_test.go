package adminapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"localgate/internal/app"
	"localgate/internal/config"
	"localgate/internal/filebrowser"
)

func TestFileBrowserGitStatusAndDiff(t *testing.T) {
	requireAdminGit(t)
	root := t.TempDir()
	runAdminGit(t, root, "init", "-q")
	runAdminGit(t, root, "config", "user.email", "localgate@example.test")
	runAdminGit(t, root, "config", "user.name", "LocalGate Test")
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runAdminGit(t, root, "add", "note.txt")
	runAdminGit(t, root, "commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mux, application := gitAdminTestAPI(t, root)
	t.Cleanup(func() { _ = application.Close() })

	statusReq := httptest.NewRequest(http.MethodGet, "https://admin.test/api/admin/v1/file-browser/git/status", nil)
	statusW := httptest.NewRecorder()
	mux.ServeHTTP(statusW, statusReq)
	if statusW.Code != http.StatusOK {
		t.Fatalf("status expected 200, got %d body=%s", statusW.Code, statusW.Body.String())
	}
	var status filebrowser.GitStatusResult
	if err := json.Unmarshal(statusW.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !status.Repository || len(status.Entries) != 1 || status.Entries[0].Path != "note.txt" {
		t.Fatalf("unexpected status: %#v", status)
	}

	diffReq := httptest.NewRequest(http.MethodGet, "https://admin.test/api/admin/v1/file-browser/git/diff?path=note.txt", nil)
	diffW := httptest.NewRecorder()
	mux.ServeHTTP(diffW, diffReq)
	if diffW.Code != http.StatusOK {
		t.Fatalf("diff expected 200, got %d body=%s", diffW.Code, diffW.Body.String())
	}
	var diff filebrowser.GitDiffResult
	if err := json.Unmarshal(diffW.Body.Bytes(), &diff); err != nil {
		t.Fatalf("decode diff: %v", err)
	}
	if diff.Path != "note.txt" || diff.Staged || diff.Diff == "" {
		t.Fatalf("unexpected diff: %#v", diff)
	}
}

func TestFileBrowserGitNonRepositoryAndInvalidStaged(t *testing.T) {
	requireAdminGit(t)
	root := t.TempDir()
	mux, application := gitAdminTestAPI(t, root)
	t.Cleanup(func() { _ = application.Close() })

	statusReq := httptest.NewRequest(http.MethodGet, "https://admin.test/api/admin/v1/file-browser/git/status", nil)
	statusW := httptest.NewRecorder()
	mux.ServeHTTP(statusW, statusReq)
	if statusW.Code != http.StatusOK {
		t.Fatalf("status expected 200, got %d body=%s", statusW.Code, statusW.Body.String())
	}
	var status filebrowser.GitStatusResult
	if err := json.Unmarshal(statusW.Body.Bytes(), &status); err != nil || status.Repository {
		t.Fatalf("unexpected non-repository status: %#v err=%v", status, err)
	}

	diffReq := httptest.NewRequest(http.MethodGet, "https://admin.test/api/admin/v1/file-browser/git/diff?staged=maybe", nil)
	diffW := httptest.NewRecorder()
	mux.ServeHTTP(diffW, diffReq)
	if diffW.Code != http.StatusBadRequest {
		t.Fatalf("invalid staged expected 400, got %d body=%s", diffW.Code, diffW.Body.String())
	}
}

func gitAdminTestAPI(t *testing.T, root string) (*http.ServeMux, *app.App) {
	t.Helper()
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Admin.Approval.Enabled = false
	cfg.FileBrowser.Enabled = true
	cfg.Root = config.Root{Path: root}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	mux := http.NewServeMux()
	(&API{App: application, BaseURL: "https://admin.test"}).Register(mux)
	return mux, application
}

func runAdminGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func requireAdminGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}
