package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"localgate/internal/app"
	"localgate/internal/config"
	"localgate/internal/deployment"
	"localgate/internal/dynamic"
)

func TestDeploymentUIStaticDeleteAction(t *testing.T) {
	application, mux, workspaceRoot := newDeploymentOverviewTestServer(t)
	deployOverviewTestSite(t, application, workspaceRoot, "delete-me", "hello")

	form := url.Values{"confirm": {"delete"}}
	req := httptest.NewRequest(http.MethodPost, "https://admin.example.test/deploy-mgr/actions/static/delete-me/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://admin.example.test")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d body=%s", w.Code, w.Body.String())
	}
	if location := w.Header().Get("Location"); !strings.Contains(location, "/review/deploy-mgr?") || !strings.Contains(location, "was+deleted") {
		t.Fatalf("unexpected redirect: %q", location)
	}
	items, err := application.DeploymentManager.List(context.Background())
	if err != nil {
		t.Fatalf("list deployments: %v", err)
	}
	for _, item := range items {
		if item.Type == deployment.TypeStatic && item.Site == "delete-me" {
			t.Fatalf("deployment still exists after delete: %#v", item)
		}
	}
}

func TestDeploymentUIRejectsUnconfirmedDeleteAndCrossSiteAction(t *testing.T) {
	application, mux, workspaceRoot := newDeploymentOverviewTestServer(t)
	deployOverviewTestSite(t, application, workspaceRoot, "safe", "hello")

	unconfirmed := httptest.NewRequest(http.MethodPost, "https://admin.example.test/deploy-mgr/actions/static/safe/delete", strings.NewReader(""))
	unconfirmed.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	unconfirmed.Header.Set("Origin", "https://admin.example.test")
	unconfirmed.Header.Set("Sec-Fetch-Site", "same-origin")
	unconfirmedW := httptest.NewRecorder()
	mux.ServeHTTP(unconfirmedW, unconfirmed)
	if unconfirmedW.Code != http.StatusBadRequest {
		t.Fatalf("expected confirmation error, got %d body=%s", unconfirmedW.Code, unconfirmedW.Body.String())
	}

	crossSite := httptest.NewRequest(http.MethodPost, "https://admin.example.test/deploy-mgr/actions/static/safe/delete", strings.NewReader("confirm=delete"))
	crossSite.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	crossSite.Header.Set("Origin", "https://evil.example")
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	crossSiteW := httptest.NewRecorder()
	mux.ServeHTTP(crossSiteW, crossSite)
	if crossSiteW.Code != http.StatusForbidden {
		t.Fatalf("expected cross-site rejection, got %d body=%s", crossSiteW.Code, crossSiteW.Body.String())
	}
	items, err := application.DeploymentManager.List(context.Background())
	if err != nil || len(items) != 1 || items[0].Site != "safe" {
		t.Fatalf("rejected actions changed deployment: %#v err=%v", items, err)
	}
}

func TestDeploymentUIDeleteUsesApprovalPipeline(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.HTTP.BaseURL = "https://control.example.test/localgate"
	cfg.Admin.Approval.Enabled = true
	cfg.Admin.BaseURL = "https://admin.example.test/review"
	cfg.Deployments.Static.RequireApproval = true
	cfg.Workspaces = []config.Workspace{{ID: "ws", Roots: []string{workspaceRoot}, CWD: workspaceRoot}}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	deployOverviewTestSite(t, application, workspaceRoot, "approval-delete", "hello")
	api := &API{App: application, DefaultWSID: "ws"}
	mux := http.NewServeMux()
	api.Register(mux)

	form := url.Values{"confirm": {"delete"}}
	req := httptest.NewRequest(http.MethodPost, "https://admin.example.test/deploy-mgr/actions/static/approval-delete/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://admin.example.test")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther || !strings.HasPrefix(w.Header().Get("Location"), "https://admin.example.test/review/admin/approvals/") {
		t.Fatalf("expected approval redirect, got code=%d location=%q body=%s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	items, err := application.DeploymentManager.List(context.Background())
	if err != nil || len(items) != 1 || items[0].Site != "approval-delete" {
		t.Fatalf("deployment changed before approval: %#v err=%v", items, err)
	}
}

func TestDeploymentUIDynamicStopAndStartActions(t *testing.T) {
	application, mux, source, executable := newDeploymentUIDynamicTestServer(t)
	_, err := application.DeploymentManager.Deploy(context.Background(), deployment.DeployRequest{
		Type:      deployment.TypeDynamic,
		Name:      "dynamic-ui-test",
		Source:    source,
		Workspace: application.Workspaces["ws"],
		Runtime: &dynamic.RuntimeSpec{
			Argv: []string{executable, "-test.run=^TestDeploymentUIDynamicHelperProcess$"},
			Env:  map[string]string{"LOCALGATE_DEPLOYMENT_UI_HELPER": "1"},
		},
		Options: deployment.DeployOptions{PreserveSource: true, HealthPath: "/healthz", RestartPolicy: "never"},
	})
	if err != nil {
		t.Fatalf("deploy dynamic fixture: %v", err)
	}

	page := httptest.NewRequest(http.MethodGet, "https://admin.example.test/deploy-mgr", nil)
	pageW := httptest.NewRecorder()
	mux.ServeHTTP(pageW, page)
	if pageW.Code != http.StatusOK || !strings.Contains(pageW.Body.String(), "Stop service") {
		t.Fatalf("active dynamic deployment has no stop action: code=%d body=%s", pageW.Code, pageW.Body.String())
	}

	postDeploymentAction(t, mux, "/deploy-mgr/actions/dynamic/dynamic-ui-test/stop")
	stopped, err := application.DeploymentManager.Get(deployment.TypeDynamic, "dynamic-ui-test")
	if err != nil || stopped.Status != dynamic.StatusStopped || stopped.Health != "stopped" {
		t.Fatalf("dynamic deployment was not stopped: %#v err=%v", stopped, err)
	}

	pageW = httptest.NewRecorder()
	mux.ServeHTTP(pageW, httptest.NewRequest(http.MethodGet, "https://admin.example.test/deploy-mgr", nil))
	if !strings.Contains(pageW.Body.String(), "Start service") {
		t.Fatalf("stopped dynamic deployment has no start action: %s", pageW.Body.String())
	}

	postDeploymentAction(t, mux, "/deploy-mgr/actions/dynamic/dynamic-ui-test/start")
	started, err := application.DeploymentManager.Get(deployment.TypeDynamic, "dynamic-ui-test")
	if err != nil || started.Status != dynamic.StatusActive || started.Health != "healthy" {
		t.Fatalf("dynamic deployment was not restarted: %#v err=%v", started, err)
	}
}

func postDeploymentAction(t *testing.T, mux *http.ServeMux, path string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Host = "admin.example.test"
	req.Header.Set("Origin", "https://admin.example.test")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST %s: expected redirect, got %d body=%s", path, w.Code, w.Body.String())
	}
}

func newDeploymentUIDynamicTestServer(t *testing.T) (*app.App, *http.ServeMux, string, string) {
	t.Helper()
	workspaceRoot := t.TempDir()
	source := filepath.Join(workspaceRoot, "app")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "README.txt"), []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.HTTP.BaseURL = "https://control.example.test/localgate"
	cfg.Admin.BaseURL = "https://admin.example.test/review"
	cfg.Admin.Approval.Enabled = false
	cfg.Deployments.Static.RequireApproval = false
	cfg.Deployments.Dynamic.Enabled = true
	cfg.Deployments.Dynamic.RequireApproval = false
	cfg.HTTP.BaseURL = "https://control.example.test"
	cfg.Deployments.PublicBaseURL = "https://apps.example.test"
	cfg.Deployments.PrivateBaseURL = "https://private.apps.example.test"
	cfg.Deployments.DefaultAccess = "public"
	cfg.Deployments.Dynamic.AllowedExecutables = []string{executable}
	cfg.Deployments.Dynamic.EnvAllowlist = []string{"LOCALGATE_DEPLOYMENT_UI_HELPER"}
	cfg.Deployments.Dynamic.RestartPolicy = "never"
	cfg.Workspaces = []config.Workspace{{ID: "ws", Roots: []string{workspaceRoot}, CWD: workspaceRoot}}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	api := &API{App: application, DefaultWSID: "ws"}
	mux := http.NewServeMux()
	api.Register(mux)
	return application, mux, source, executable
}

func TestDeploymentUIDynamicHelperProcess(t *testing.T) {
	if os.Getenv("LOCALGATE_DEPLOYMENT_UI_HELPER") != "1" {
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	if err := http.ListenAndServe("127.0.0.1:"+os.Getenv("PORT"), mux); err != nil {
		os.Exit(2)
	}
}
