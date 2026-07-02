package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestControlAdminAndDeploymentHandlersArePhysicallySeparated(t *testing.T) {
	control := New(Options{
		Bind:           "127.0.0.1:1",
		ControlBaseURL: "https://control.example.test",
		Register: func(mux *http.ServeMux) {
			mux.HandleFunc("/tasks", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Plane", "control")
				w.WriteHeader(http.StatusOK)
			})
		},
	})
	admin := NewAdmin(AdminOptions{
		Bind:    "127.0.0.1:3",
		BaseURL: "https://admin.example.test",
		Register: func(mux *http.ServeMux) {
			mux.HandleFunc("/admin", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Plane", "admin")
				w.WriteHeader(http.StatusOK)
			})
		},
	})
	deployment := NewDeployment(DeploymentOptions{
		Bind: "127.0.0.1:2",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Plane", "deployment")
			w.WriteHeader(http.StatusAccepted)
		}),
	})

	appOnControl := httptest.NewRequest(http.MethodGet, "https://demo.apps.example.test/tasks", nil)
	appOnControlW := httptest.NewRecorder()
	control.srv.Handler.ServeHTTP(appOnControlW, appOnControl)
	if appOnControlW.Code != http.StatusNotFound || appOnControlW.Header().Get("X-Plane") != "" {
		t.Fatalf("deployment host reached control plane: code=%d headers=%v", appOnControlW.Code, appOnControlW.Header())
	}

	adminOnControl := httptest.NewRequest(http.MethodGet, "https://control.example.test/admin", nil)
	adminOnControlW := httptest.NewRecorder()
	control.srv.Handler.ServeHTTP(adminOnControlW, adminOnControl)
	if adminOnControlW.Code != http.StatusNotFound || adminOnControlW.Header().Get("X-Plane") != "" {
		t.Fatalf("admin route reached control listener: code=%d headers=%v", adminOnControlW.Code, adminOnControlW.Header())
	}

	adminRequest := httptest.NewRequest(http.MethodGet, "https://admin.example.test/admin", nil)
	adminRequestW := httptest.NewRecorder()
	admin.srv.Handler.ServeHTTP(adminRequestW, adminRequest)
	if adminRequestW.Code != http.StatusOK || adminRequestW.Header().Get("X-Plane") != "admin" {
		t.Fatalf("admin listener did not serve admin route: code=%d headers=%v", adminRequestW.Code, adminRequestW.Header())
	}

	controlOnAdmin := httptest.NewRequest(http.MethodGet, "https://admin.example.test/tasks", nil)
	controlOnAdminW := httptest.NewRecorder()
	admin.srv.Handler.ServeHTTP(controlOnAdminW, controlOnAdmin)
	if controlOnAdminW.Code != http.StatusNotFound || controlOnAdminW.Header().Get("X-Plane") != "" {
		t.Fatalf("control route reached admin listener: code=%d headers=%v", controlOnAdminW.Code, controlOnAdminW.Header())
	}

	localhostOnAdmin := httptest.NewRequest(http.MethodGet, "http://localhost:3/admin", nil)
	localhostOnAdminW := httptest.NewRecorder()
	admin.srv.Handler.ServeHTTP(localhostOnAdminW, localhostOnAdmin)
	if localhostOnAdminW.Code != http.StatusNotFound || localhostOnAdminW.Header().Get("X-Plane") != "" {
		t.Fatalf("localhost must not bypass admin host isolation: code=%d headers=%v", localhostOnAdminW.Code, localhostOnAdminW.Header())
	}

	controlOnDeployment := httptest.NewRequest(http.MethodGet, "https://control.example.test/tasks", nil)
	controlOnDeploymentW := httptest.NewRecorder()
	deployment.srv.Handler.ServeHTTP(controlOnDeploymentW, controlOnDeployment)
	if controlOnDeploymentW.Code != http.StatusAccepted || controlOnDeploymentW.Header().Get("X-Plane") != "deployment" {
		t.Fatalf("deployment listener unexpectedly mounted control routes: code=%d headers=%v", controlOnDeploymentW.Code, controlOnDeploymentW.Header())
	}
}

func TestDeploymentServerDoesNotMountControlHealthEndpoint(t *testing.T) {
	deployment := NewDeployment(DeploymentOptions{Bind: "127.0.0.1:2", Handler: http.NotFoundHandler()})
	req := httptest.NewRequest(http.MethodGet, "http://demo.apps.example.test/healthz", nil)
	w := httptest.NewRecorder()
	deployment.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("deployment listener exposed a control health endpoint: %d", w.Code)
	}
}
