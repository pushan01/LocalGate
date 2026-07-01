package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHostRouterSeparatesControlAndApplicationHosts(t *testing.T) {
	control := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Route", "control")
		w.WriteHeader(http.StatusOK)
	})
	application := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Route", "application")
		w.Header().Set("X-Path", r.URL.Path)
		w.WriteHeader(http.StatusAccepted)
	})
	router := newHostRouter(control, HostRoutingOptions{
		Enabled: true, ControlHosts: []string{"control.example.test"},
		AppDomain: "example.test", AppHandler: application,
	})

	controlReq := httptest.NewRequest(http.MethodGet, "http://control.example.test/tasks", nil)
	controlReq.Host = "CONTROL.EXAMPLE.TEST:80"
	controlW := httptest.NewRecorder()
	router.ServeHTTP(controlW, controlReq)
	if controlW.Code != http.StatusOK || controlW.Header().Get("X-Route") != "control" {
		t.Fatalf("control route: code=%d headers=%v", controlW.Code, controlW.Header())
	}

	appReq := httptest.NewRequest(http.MethodGet, "http://blog.example.test/tasks", nil)
	appReq.Host = "blog.example.test"
	appReq.Header.Set("X-Forwarded-Host", "control.example.test")
	appW := httptest.NewRecorder()
	router.ServeHTTP(appW, appReq)
	if appW.Code != http.StatusAccepted || appW.Header().Get("X-Route") != "application" || appW.Header().Get("X-Path") != "/tasks" {
		t.Fatalf("application route: code=%d headers=%v", appW.Code, appW.Header())
	}

	unknownReq := httptest.NewRequest(http.MethodGet, "http://unknown.invalid/", nil)
	unknownW := httptest.NewRecorder()
	router.ServeHTTP(unknownW, unknownReq)
	if unknownW.Code != http.StatusNotFound {
		t.Fatalf("unknown host: %d", unknownW.Code)
	}

	nestedReq := httptest.NewRequest(http.MethodGet, "http://a.b.example.test/", nil)
	nestedW := httptest.NewRecorder()
	router.ServeHTTP(nestedW, nestedReq)
	if nestedW.Code != http.StatusNotFound {
		t.Fatalf("nested app host should be rejected: %d", nestedW.Code)
	}
}

func TestHostRouterAppliesBearerOnlyToControlPlane(t *testing.T) {
	controlMux := http.NewServeMux()
	controlMux.HandleFunc("/secure", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	control := wrapBearerAuth(controlMux, AuthOptions{Enabled: true, BearerToken: "secret"})
	application := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	router := newHostRouter(control, HostRoutingOptions{
		Enabled: true, ControlHosts: []string{"control.example.test"},
		AppDomain: "example.test", AppHandler: application,
	})

	controlReq := httptest.NewRequest(http.MethodGet, "http://control.example.test/secure", nil)
	controlReq.Host = "control.example.test"
	controlW := httptest.NewRecorder()
	router.ServeHTTP(controlW, controlReq)
	if controlW.Code != http.StatusUnauthorized {
		t.Fatalf("expected control auth, got %d", controlW.Code)
	}

	appReq := httptest.NewRequest(http.MethodGet, "http://app.example.test/secure", nil)
	appReq.Host = "app.example.test"
	appW := httptest.NewRecorder()
	router.ServeHTTP(appW, appReq)
	if appW.Code != http.StatusNoContent {
		t.Fatalf("application host should bypass control bearer auth, got %d", appW.Code)
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

func TestHostRouterRestrictsLegacyApprovalRoutesToAdminHost(t *testing.T) {
	control := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Route", "control")
		w.WriteHeader(http.StatusOK)
	})
	router := newHostRouter(control, HostRoutingOptions{
		Enabled: true, ControlHosts: []string{"control.example.test"},
		AppDomain: "apps.example.test", AppHandler: http.NotFoundHandler(),
		AdminBaseURL: "https://admin.example.test",
	})

	approvalReq := httptest.NewRequest(http.MethodGet, "https://admin.example.test/approval-ui", nil)
	approvalW := httptest.NewRecorder()
	router.ServeHTTP(approvalW, approvalReq)
	if approvalW.Code != http.StatusOK || approvalW.Header().Get("X-Route") != "control" {
		t.Fatalf("admin host route: code=%d headers=%v", approvalW.Code, approvalW.Header())
	}

	wrongHostReq := httptest.NewRequest(http.MethodGet, "https://control.example.test/approvals", nil)
	wrongHostW := httptest.NewRecorder()
	router.ServeHTTP(wrongHostW, wrongHostReq)
	if wrongHostW.Code != http.StatusNotFound {
		t.Fatalf("management route on control host should be hidden: %d", wrongHostW.Code)
	}

	approvalHostControlReq := httptest.NewRequest(http.MethodGet, "https://admin.example.test/tasks", nil)
	approvalHostControlW := httptest.NewRecorder()
	router.ServeHTTP(approvalHostControlW, approvalHostControlReq)
	if approvalHostControlW.Code != http.StatusNotFound {
		t.Fatalf("non-management route on dedicated admin host should be hidden: %d", approvalHostControlW.Code)
	}
}

func TestHostRouterRestrictsAdminRoutesToAdminHost(t *testing.T) {
	control := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Route", "control")
		w.WriteHeader(http.StatusOK)
	})
	router := newHostRouter(control, HostRoutingOptions{
		Enabled: true, ControlHosts: []string{"control.example.test"},
		AppDomain: "apps.example.test", AppHandler: http.NotFoundHandler(),
		AdminBaseURL: "https://admin.example.test",
	})

	adminReq := httptest.NewRequest(http.MethodGet, "https://admin.example.test/admin/history", nil)
	adminW := httptest.NewRecorder()
	router.ServeHTTP(adminW, adminReq)
	if adminW.Code != http.StatusOK || adminW.Header().Get("X-Route") != "control" {
		t.Fatalf("admin host route: code=%d headers=%v", adminW.Code, adminW.Header())
	}

	wrongHostReq := httptest.NewRequest(http.MethodGet, "https://control.example.test/api/admin/v1/tasks", nil)
	wrongHostW := httptest.NewRecorder()
	router.ServeHTTP(wrongHostW, wrongHostReq)
	if wrongHostW.Code != http.StatusNotFound {
		t.Fatalf("admin route on control host should be hidden: %d", wrongHostW.Code)
	}

	foreignReq := httptest.NewRequest(http.MethodGet, "https://admin.example.test/mcp", nil)
	foreignW := httptest.NewRecorder()
	router.ServeHTTP(foreignW, foreignReq)
	if foreignW.Code != http.StatusNotFound {
		t.Fatalf("admin host must not expose control routes: %d", foreignW.Code)
	}
}

func TestHostRouterAllowsSharedControlAndAdminHost(t *testing.T) {
	control := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	router := newHostRouter(control, HostRoutingOptions{
		Enabled: true, ControlHosts: []string{"control.example.test"},
		AppDomain: "apps.example.test", AppHandler: http.NotFoundHandler(),
		AdminBaseURL: "https://control.example.test",
	})
	for _, path := range []string{"/approval-ui", "/tasks"} {
		req := httptest.NewRequest(http.MethodGet, "https://control.example.test"+path, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("shared host path %s: %d", path, w.Code)
		}
	}
}
