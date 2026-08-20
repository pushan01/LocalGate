package dynamic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"localgate/internal/config"
)

func TestDynamicDeploymentAccessControlsDomainRouting(t *testing.T) {
	managedRoot := filepath.Join(t.TempDir(), "managed")
	if err := os.MkdirAll(managedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	registry := `{"deployments":{"demo":{"name":"demo","type":"dynamic","workspace_id":"ws","access":"private","active_release":"release-a","desired_state":"stopped","status":"stopped","health":"stopped","argv":["unused"],"working_dir":".","health_path":"/healthz","restart_policy":"never","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","file_count":1,"total_bytes":2}}}`
	if err := os.WriteFile(filepath.Join(managedRoot, "deployments.json"), []byte(registry), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(managedRoot, config.DynamicHosting{
		Enabled: true, DomainSuffix: "app", PublicBaseURL: "https://public.test", PrivateBaseURL: "https://private.test", DefaultAccess: "private",
	}, config.Root{}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()

	request := func(host string) int {
		req := httptest.NewRequest(http.MethodGet, "https://"+host+"/", nil)
		w := httptest.NewRecorder()
		service.ServeHTTP(w, req)
		return w.Code
	}
	if got := request("demoapp.private.test"); got != http.StatusServiceUnavailable {
		t.Fatalf("private host should resolve stopped deployment, got %d", got)
	}
	if got := request("demoapp.public.test"); got != http.StatusNotFound {
		t.Fatalf("public host should not resolve private deployment, got %d", got)
	}
	view, err := service.SetAccess(context.Background(), "demo", "public")
	if err != nil {
		t.Fatal(err)
	}
	if view.Access != "public" || view.URL != "https://demoapp.public.test/" {
		t.Fatalf("unexpected switched view: %#v", view)
	}
	if got := request("demoapp.private.test"); got != http.StatusNotFound {
		t.Fatalf("old private host should be invalidated, got %d", got)
	}
	if got := request("demoapp.public.test"); got != http.StatusServiceUnavailable {
		t.Fatalf("public host should resolve switched deployment, got %d", got)
	}
	if got := request("demo.public.test"); got != http.StatusNotFound {
		t.Fatalf("unsuffixed host should not resolve deployment, got %d", got)
	}
}
