package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"localgate/internal/app"
	"localgate/internal/config"
)

func TestAdminDisabledHidesAdminAndLegacyManagementRoutes(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.Enabled = false
	cfg.Root = config.Root{Path: root}

	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	if application.Approval.Enabled {
		t.Fatal("approval must be disabled when admin is disabled")
	}

	mux := http.NewServeMux()
	(&API{App: application}).Register(mux)
	for _, path := range []string{
		"/admin",
		"/api/admin/v1/overview",
		"/approval-ui",
		"/approvals",
		"/deploy-mgr",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("GET %s: got %d want 404", path, w.Code)
		}
	}
}
