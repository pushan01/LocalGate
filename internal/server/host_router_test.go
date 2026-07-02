package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestControlHostBoundaryAllowsOnlyControlAndAdminHosts(t *testing.T) {
	control := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Route", "control")
		w.WriteHeader(http.StatusOK)
	})
	handler := newControlHostBoundary(control, "https://control.example.test", "https://admin.example.test", "127.0.0.1:2525")

	cases := []struct {
		url  string
		want int
	}{
		{"https://control.example.test/tasks", http.StatusOK},
		{"https://demo.apps.example.test/tasks", http.StatusNotFound},
		{"https://admin.example.test/admin/history", http.StatusOK},
		{"https://admin.example.test/tasks", http.StatusNotFound},
		{"https://control.example.test/admin/history", http.StatusNotFound},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.url, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatalf("GET %s: got %d want %d", tc.url, w.Code, tc.want)
		}
	}
}

func TestControlHostBoundaryPreservesBearerAuthentication(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/secure", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	control := wrapBearerAuth(mux, AuthOptions{Enabled: true, BearerToken: "secret"})
	handler := newControlHostBoundary(control, "https://control.example.test", "", "127.0.0.1:2525")

	unauthorized := httptest.NewRequest(http.MethodGet, "https://control.example.test/secure", nil)
	unauthorizedW := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedW, unauthorized)
	if unauthorizedW.Code != http.StatusUnauthorized {
		t.Fatalf("expected bearer authentication, got %d", unauthorizedW.Code)
	}

	appHost := httptest.NewRequest(http.MethodGet, "https://demo.apps.example.test/secure", nil)
	appHost.Header.Set("Authorization", "Bearer secret")
	appHostW := httptest.NewRecorder()
	handler.ServeHTTP(appHostW, appHost)
	if appHostW.Code != http.StatusNotFound {
		t.Fatalf("application host must not reach the control handler, got %d", appHostW.Code)
	}
}

func TestControlHostBoundaryAllowsSharedControlAndAdminHost(t *testing.T) {
	control := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := newControlHostBoundary(control, "https://control.example.test", "https://control.example.test", "127.0.0.1:2525")
	for _, path := range []string{"/approval-ui", "/tasks"} {
		req := httptest.NewRequest(http.MethodGet, "https://control.example.test"+path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("shared host path %s: %d", path, w.Code)
		}
	}
}

func TestControlHostBoundaryAllowsLoopbackOnlyForMCPOnControlPort(t *testing.T) {
	control := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Route", "control")
		w.WriteHeader(http.StatusOK)
	})
	handler := newControlHostBoundary(control, "https://control.example.test", "https://admin.example.test", "127.0.0.1:2525")

	allowed := []string{
		"http://localhost:2525/mcp",
		"http://127.0.0.1:2525/mcp",
		"http://[::1]:2525/mcp",
	}
	for _, target := range allowed {
		req := httptest.NewRequest(http.MethodPost, target, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK || w.Header().Get("X-Route") != "control" {
			t.Fatalf("loopback MCP request %s: code=%d headers=%v", target, w.Code, w.Header())
		}
	}

	rejected := []string{
		"http://localhost:2526/mcp",
		"http://127.0.0.1:2526/mcp",
		"http://localhost:2525/tasks",
		"http://localhost:2525/admin/",
	}
	for _, target := range rejected {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound || w.Header().Get("X-Route") != "" {
			t.Fatalf("loopback request %s must remain isolated: code=%d headers=%v", target, w.Code, w.Header())
		}
	}
}

func FuzzNormalizeRequestHost(f *testing.F) {
	for _, value := range []string{"control.example.test", "APP.EXAMPLE.TEST:8080", "demo.apps.test.", "", "bad..host", "-bad.test", "evil:bad", "xn--bcher-kva.example"} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		host, ok := normalizeRequestHost(raw)
		if !ok {
			return
		}
		if host == "" {
			t.Fatal("accepted an empty normalized host")
		}
		again, againOK := normalizeRequestHost(host)
		if !againOK || again != host {
			t.Fatalf("normalization is not idempotent: %q -> %q", raw, host)
		}
	})
}
