package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"localgate/internal/app"
	"localgate/internal/config"
)

func newDeploymentOverviewTestServer(t *testing.T) (*app.App, *http.ServeMux, string) {
	t.Helper()
	workspaceRoot := t.TempDir()
	cfg := config.Config{
		LogStore:    t.TempDir(),
		HTTP:        config.HTTP{BaseURL: "https://control.example.test/localgate"},
		Admin:       config.AdminConfig{Enabled: true, BaseURL: "https://admin.example.test/review", Approval: config.DefaultApprovalConfig()},
		Deployments: config.DeploymentsConfig{PublicBaseURL: "https://apps.example.test", PrivateBaseURL: "https://private.apps.example.test", DefaultAccess: "public"},
		Root:        config.Root{Path: workspaceRoot, CommandAllowlist: []string{"echo"}},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	api := &API{App: application}
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

func TestControlAPIHasNoDeploymentListRoute(t *testing.T) {
	_, mux, _ := newDeploymentOverviewTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "https://control.example.test/deploys", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("control listener must not expose /deploys, got %d", w.Code)
	}
}
