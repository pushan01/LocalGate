package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"localgate/internal/app"
	"localgate/internal/config"
)

func newDeploymentOverviewTestServer(t *testing.T) (*app.App, *http.ServeMux, string) {
	t.Helper()
	workspaceRoot := t.TempDir()
	cfg := config.Config{
		LogStore:   t.TempDir(),
		HTTP:       config.HTTP{BaseURL: "https://control.example.test/localgate"},
		Admin:      config.AdminConfig{Enabled: true, BaseURL: "https://admin.example.test/review", Approval: config.DefaultApprovalConfig()},
		Workspaces: []config.Workspace{{ID: "ws", Roots: []string{workspaceRoot}, CWD: workspaceRoot, CommandAllowlist: []string{"echo"}}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	api := &API{App: application, DefaultWSID: application.DefaultWSID}
	mux := http.NewServeMux()
	api.Register(mux)
	return application, mux, workspaceRoot
}

func deployOverviewTestSite(t *testing.T, application *app.App, workspaceRoot, name, body string) {
	t.Helper()
	source := filepath.Join(workspaceRoot, "source-"+name)
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := application.StaticService.DeploySite(context.Background(), name, source, false); err != nil {
		t.Fatalf("deploy %s: %v", name, err)
	}
}

func TestDeploymentListIsReadOnly(t *testing.T) {
	application, mux, workspaceRoot := newDeploymentOverviewTestServer(t)
	deployOverviewTestSite(t, application, workspaceRoot, "zeta", "zeta")
	deployOverviewTestSite(t, application, workspaceRoot, "alpha", "alpha")
	req := httptest.NewRequest(http.MethodGet, "https://control.example.test/deploys/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, expected := range []string{"2 total", "alpha", "zeta", "Open deployment", "active", "static"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("missing %q in list: %s", expected, body)
		}
	}
	for _, forbidden := range []string{"Stop service", "Start service", "Delete deployment", "/deploy-mgr/actions/"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("read-only list contains management control %q: %s", forbidden, body)
		}
	}
	if strings.Index(body, "alpha") > strings.Index(body, "zeta") {
		t.Fatalf("deployments not sorted: %s", body)
	}
}

func TestDeploymentListHostAndPaths(t *testing.T) {
	_, mux, _ := newDeploymentOverviewTestServer(t)
	for _, target := range []string{"https://control.example.test/deploys", "https://control.example.test/deploys/"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "No deployments yet") {
			t.Fatalf("GET %s: code=%d body=%s", target, w.Code, w.Body.String())
		}
	}
	wrongHost := httptest.NewRecorder()
	mux.ServeHTTP(wrongHost, httptest.NewRequest(http.MethodGet, "https://admin.example.test/deploys", nil))
	if wrongHost.Code != http.StatusNotFound {
		t.Fatalf("approval host should not expose deploys: %d", wrongHost.Code)
	}
	post := httptest.NewRecorder()
	mux.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "https://control.example.test/deploys", nil))
	if post.Code != http.StatusMethodNotAllowed || post.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("unexpected POST response: %d", post.Code)
	}
	nested := httptest.NewRecorder()
	mux.ServeHTTP(nested, httptest.NewRequest(http.MethodGet, "https://control.example.test/deploys/unknown", nil))
	if nested.Code != http.StatusNotFound {
		t.Fatalf("nested path should be 404: %d", nested.Code)
	}
	legacy := httptest.NewRecorder()
	mux.ServeHTTP(legacy, httptest.NewRequest(http.MethodGet, "https://control.example.test/deployment-ui", nil))
	if legacy.Code != http.StatusNotFound {
		t.Fatalf("legacy path should be 404: %d", legacy.Code)
	}
}
