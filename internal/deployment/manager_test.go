package deployment

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"localgate/internal/config"
	"localgate/internal/dynamic"
	"localgate/internal/static"
)

func TestManagerStaticLifecycle(t *testing.T) {
	workspaceRoot := t.TempDir()
	source := filepath.Join(workspaceRoot, "site")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(static.NewServiceAt(filepath.Join(t.TempDir(), "managed")), nil, "https://apps.test/")
	workspace := config.Workspace{ID: "ws", Roots: []string{workspaceRoot}, CWD: workspaceRoot}

	result, err := manager.Deploy(context.Background(), DeployRequest{
		Type: TypeStatic, Name: "demo", Source: source, Workspace: workspace,
		Options: DeployOptions{PreserveSource: true},
	})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if result.Type != TypeStatic || result.Site != "demo" || result.URL != "https://demo.apps.test/" || result.Hostname != "demo.apps.test" {
		t.Fatalf("unexpected deploy result: %#v", result)
	}
	items, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].Site != "demo" || items[0].Type != TypeStatic {
		t.Fatalf("unexpected list: %#v", items)
	}
	if err := manager.Delete(context.Background(), TypeStatic, "demo"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	items, err = manager.List(context.Background())
	if err != nil || len(items) != 0 {
		t.Fatalf("list after delete: %#v err=%v", items, err)
	}
}

func TestManagerRejectsWorkspaceRootRemoval(t *testing.T) {
	workspaceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(static.NewServiceAt(filepath.Join(t.TempDir(), "managed")), nil, "https://apps.test")
	_, err := manager.Deploy(context.Background(), DeployRequest{
		Type: TypeStatic, Name: "demo", Source: workspaceRoot,
		Workspace: config.Workspace{ID: "ws", Roots: []string{workspaceRoot}, CWD: workspaceRoot},
		Options:   DeployOptions{PreserveSource: false},
	})
	if !errors.Is(err, ErrUnsafeSourceRemoval) {
		t.Fatalf("expected unsafe removal error, got %v", err)
	}
}

func TestManagerServesStaticDeploymentByAppHostname(t *testing.T) {
	workspaceRoot := t.TempDir()
	source := filepath.Join(workspaceRoot, "site")
	if err := os.MkdirAll(filepath.Join(source, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("home"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "assets", "app.js"), []byte("asset"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(static.NewServiceAt(filepath.Join(t.TempDir(), "managed")), nil, "https://apps.test")
	workspace := config.Workspace{ID: "ws", Roots: []string{workspaceRoot}, CWD: workspaceRoot}
	if _, err := manager.Deploy(context.Background(), DeployRequest{
		Type: TypeStatic, Name: "demo", Source: source, Workspace: workspace,
		Options: DeployOptions{PreserveSource: true},
	}); err != nil {
		t.Fatalf("deploy: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "https://demo.apps.test/assets/app.js", nil)
	response := httptest.NewRecorder()
	manager.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "asset" {
		t.Fatalf("serve static app: code=%d body=%q", response.Code, response.Body.String())
	}

	for _, target := range []string{"https://missing.apps.test/", "https://nested.demo.apps.test/", "http://localhost:2526/", "http://127.0.0.1:2526/"} {
		request = httptest.NewRequest(http.MethodGet, target, nil)
		response = httptest.NewRecorder()
		manager.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for %s, got %d", target, response.Code)
		}
	}
}

func TestManagerRejectsDynamicDeploymentUsingStaticName(t *testing.T) {
	workspaceRoot := t.TempDir()
	source := filepath.Join(workspaceRoot, "site")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("static"), 0o644); err != nil {
		t.Fatal(err)
	}
	staticService := static.NewServiceAt(filepath.Join(t.TempDir(), "static"))
	if _, err := staticService.DeploySite(context.Background(), "demo", source, false); err != nil {
		t.Fatalf("seed static deployment: %v", err)
	}
	dynamicService, err := dynamic.NewService(filepath.Join(t.TempDir(), "dynamic"), config.DynamicHosting{}, nil, false)
	if err != nil {
		t.Fatalf("new dynamic service: %v", err)
	}
	defer dynamicService.Close()
	manager := NewManager(staticService, dynamicService, "https://apps.test")
	_, err = manager.Deploy(context.Background(), DeployRequest{
		Type: TypeDynamic, Name: "demo", Source: source,
		Workspace: config.Workspace{ID: "ws", Roots: []string{workspaceRoot}, CWD: workspaceRoot},
		Runtime:   &dynamic.RuntimeSpec{Argv: []string{"unused"}},
	})
	if !errors.Is(err, ErrDeploymentNameConflict) {
		t.Fatalf("expected cross-type name conflict, got %v", err)
	}
}

func TestManagerRejectsReservedAndControlHostNamesForStaticDeployments(t *testing.T) {
	workspaceRoot := t.TempDir()
	source := filepath.Join(workspaceRoot, "site")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("static"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(static.NewServiceAt(filepath.Join(t.TempDir(), "static")), nil, "https://apps.test")
	manager.ConfigureAppRouting("https://control.apps.test", []string{"admin"})
	workspace := config.Workspace{ID: "ws", Roots: []string{workspaceRoot}, CWD: workspaceRoot}
	for _, name := range []string{"admin", "control"} {
		_, err := manager.Deploy(context.Background(), DeployRequest{
			Type: TypeStatic, Name: name, Source: source, Workspace: workspace,
			Options: DeployOptions{PreserveSource: true},
		})
		if !errors.Is(err, ErrDeploymentNameBlocked) {
			t.Fatalf("expected %s to be blocked, got %v", name, err)
		}
	}
}

func TestManagerRoutesStaticDeploymentByAccessAndCanSwitch(t *testing.T) {
	workspaceRoot := t.TempDir()
	source := filepath.Join(workspaceRoot, "site")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("access"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(static.NewServiceAt(filepath.Join(t.TempDir(), "static")), nil, "")
	manager.ConfigureAppHosting("https://public.test", "http://private.test", AccessPrivate)
	workspace := config.Workspace{ID: "ws", Roots: []string{workspaceRoot}, CWD: workspaceRoot}
	result, err := manager.Deploy(context.Background(), DeployRequest{
		Type: TypeStatic, Name: "demo", Source: source, Workspace: workspace,
		Options: DeployOptions{PreserveSource: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Access != AccessPrivate || result.URL != "http://demo.private.test/" {
		t.Fatalf("unexpected private deployment result: %#v", result)
	}

	assertCode := func(target string, want int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		w := httptest.NewRecorder()
		manager.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s: got %d want %d", target, w.Code, want)
		}
	}
	assertCode("http://demo.private.test/", http.StatusOK)
	assertCode("https://demo.public.test/", http.StatusNotFound)

	view, err := manager.SetAccess(context.Background(), TypeStatic, "demo", AccessPublic)
	if err != nil {
		t.Fatal(err)
	}
	if view.Access != AccessPublic || view.URL != "https://demo.public.test/" {
		t.Fatalf("unexpected public view: %#v", view)
	}
	assertCode("http://demo.private.test/", http.StatusNotFound)
	assertCode("https://demo.public.test/", http.StatusOK)
}

func TestManagerTreatsLegacyStaticAccessAsPublic(t *testing.T) {
	service := static.NewServiceAt(filepath.Join(t.TempDir(), "static"))
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeploySite(context.Background(), "legacy", source, false); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(service, nil, "")
	manager.ConfigureAppHosting("https://public.test", "https://private.test", AccessPrivate)
	items, err := manager.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Access != AccessPublic || items[0].URL != "https://legacy.public.test/" {
		t.Fatalf("legacy deployment should remain public: %#v", items)
	}
}
