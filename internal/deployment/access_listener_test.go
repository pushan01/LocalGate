package deployment

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"localgate/internal/config"
	"localgate/internal/static"
)

func TestAccessListenersSeparateApplicationsAndDeploymentLists(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(static.NewServiceAt(filepath.Join(t.TempDir(), "managed")), nil, "")
	manager.ConfigureAppHosting("https://apps.test", "https://private.apps.test", AccessPrivate)
	workspace := config.Root{Path: root}

	deployStaticForAccessTest(t, manager, workspace, root, "public-app", AccessPublic, "public body")
	deployStaticForAccessTest(t, manager, workspace, root, "private-app", AccessPrivate, "private body")

	publicHandler := manager.HandlerForAccess(AccessPublic)
	privateHandler := manager.HandlerForAccess(AccessPrivate)

	assertResponse := func(handler http.Handler, target string, wantCode int, wantBody, forbiddenBody string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != wantCode {
			t.Fatalf("GET %s: got %d want %d body=%s", target, w.Code, wantCode, w.Body.String())
		}
		if wantBody != "" && !strings.Contains(w.Body.String(), wantBody) {
			t.Fatalf("GET %s: body missing %q: %s", target, wantBody, w.Body.String())
		}
		if forbiddenBody != "" && strings.Contains(w.Body.String(), forbiddenBody) {
			t.Fatalf("GET %s: body unexpectedly contains %q: %s", target, forbiddenBody, w.Body.String())
		}
	}

	assertResponse(publicHandler, "https://deploys.apps.test/", http.StatusOK, "public-app", "private-app")
	assertResponse(privateHandler, "https://deploys.private.apps.test/", http.StatusOK, "private-app", "public-app")
	assertResponse(publicHandler, "https://public-app.apps.test/", http.StatusOK, "public body", "")
	assertResponse(privateHandler, "https://private-app.private.apps.test/", http.StatusOK, "private body", "")

	assertResponse(publicHandler, "https://private-app.private.apps.test/", http.StatusNotFound, "", "")
	assertResponse(privateHandler, "https://public-app.apps.test/", http.StatusNotFound, "", "")
	assertResponse(publicHandler, "https://apps.test/", http.StatusNotFound, "", "")
	assertResponse(privateHandler, "https://private.apps.test/", http.StatusNotFound, "", "")
	assertResponse(publicHandler, "https://apps.test/deploys", http.StatusNotFound, "", "")
	assertResponse(privateHandler, "https://private.apps.test/deploys", http.StatusNotFound, "", "")
	assertResponse(publicHandler, "http://deploys.localhost:3738/", http.StatusNotFound, "", "")
	assertResponse(privateHandler, "http://deploys.127.0.0.1:3740/", http.StatusNotFound, "", "")

	post := httptest.NewRequest(http.MethodPost, "https://deploys.apps.test/", nil)
	postW := httptest.NewRecorder()
	publicHandler.ServeHTTP(postW, post)
	if postW.Code != http.StatusMethodNotAllowed || postW.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("unexpected deployment list POST response: code=%d allow=%q", postW.Code, postW.Header().Get("Allow"))
	}

	nested := httptest.NewRequest(http.MethodGet, "https://deploys.apps.test/unknown", nil)
	nestedW := httptest.NewRecorder()
	publicHandler.ServeHTTP(nestedW, nested)
	if nestedW.Code != http.StatusNotFound {
		t.Fatalf("nested deployment list path must be 404, got %d", nestedW.Code)
	}
}

func TestAccessSwitchMovesDeploymentBetweenListeners(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(static.NewServiceAt(filepath.Join(t.TempDir(), "managed")), nil, "")
	manager.ConfigureAppHosting("https://apps.test", "https://private.apps.test", AccessPrivate)
	workspace := config.Root{Path: root}
	deployStaticForAccessTest(t, manager, workspace, root, "move-me", AccessPublic, "move body")

	publicHandler := manager.HandlerForAccess(AccessPublic)
	privateHandler := manager.HandlerForAccess(AccessPrivate)
	if _, err := manager.SetAccess(context.Background(), TypeStatic, "move-me", AccessPrivate); err != nil {
		t.Fatalf("set private: %v", err)
	}

	publicReq := httptest.NewRequest(http.MethodGet, "https://move-me.apps.test/", nil)
	publicW := httptest.NewRecorder()
	publicHandler.ServeHTTP(publicW, publicReq)
	if publicW.Code != http.StatusNotFound {
		t.Fatalf("public listener kept serving private deployment: %d", publicW.Code)
	}

	privateReq := httptest.NewRequest(http.MethodGet, "https://move-me.private.apps.test/", nil)
	privateW := httptest.NewRecorder()
	privateHandler.ServeHTTP(privateW, privateReq)
	if privateW.Code != http.StatusOK || privateW.Body.String() != "move body" {
		t.Fatalf("private listener did not serve moved deployment: code=%d body=%q", privateW.Code, privateW.Body.String())
	}
}

func deployStaticForAccessTest(t *testing.T, manager *Manager, workspace config.Root, root, name, access, body string) {
	t.Helper()
	source := filepath.Join(root, name)
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Deploy(context.Background(), DeployRequest{
		Type: TypeStatic, Name: name, Source: source, Root: workspace,
		Options: DeployOptions{PreserveSource: true, Access: access},
	}); err != nil {
		t.Fatalf("deploy %s: %v", name, err)
	}
}
