package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMatchHeaderToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/system/upgrades", nil)
	req.Header.Set("X-LocalGate-Upgrade-Token", "upgrade-secret")
	if !MatchHeaderToken(req, "X-LocalGate-Upgrade-Token", "upgrade-secret") {
		t.Fatalf("expected header token to match")
	}
	if MatchHeaderToken(req, "X-LocalGate-Upgrade-Token", "wrong") {
		t.Fatalf("expected header token mismatch")
	}
}

func TestBearerAuthAllowsHealthz(t *testing.T) {
	srv := New(Options{
		Bind:    "127.0.0.1:0",
		Version: "test",
		Auth: AuthOptions{
			Enabled:     true,
			BearerToken: "secret-token",
			AllowPaths:  []string{"/healthz"},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestBearerAuthAllowsAdminAssetsButProtectsAdminAPI(t *testing.T) {
	srv := New(Options{
		Bind: "127.0.0.1:0", Version: "test",
		Auth: AuthOptions{Enabled: true, BearerToken: "secret-token", AllowPaths: []string{"/healthz"}},
		Register: func(mux *http.ServeMux) {
			mux.HandleFunc("/admin", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
			mux.HandleFunc("/api/admin/v1/overview", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		},
	})

	asset := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if asset.Code != http.StatusOK {
		t.Fatalf("expected public admin asset, got %d", asset.Code)
	}
	api := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(api, httptest.NewRequest(http.MethodGet, "/api/admin/v1/overview", nil))
	if api.Code != http.StatusUnauthorized {
		t.Fatalf("expected protected admin API, got %d", api.Code)
	}
}

func TestBearerAuthRejectsMissingToken(t *testing.T) {
	srv := New(Options{
		Bind:    "127.0.0.1:0",
		Version: "test",
		Auth: AuthOptions{
			Enabled:     true,
			BearerToken: "secret-token",
			AllowPaths:  []string{"/healthz"},
		},
		Register: func(mux *http.ServeMux) {
			mux.HandleFunc("/secure", func(w http.ResponseWriter, r *http.Request) {
				WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
			})
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("WWW-Authenticate"); got == "" {
		t.Fatalf("expected WWW-Authenticate header")
	}
	var body APIError
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != "unauthorized" {
		t.Fatalf("unexpected error body: %+v", body)
	}
}

func TestBearerAuthRejectsInvalidToken(t *testing.T) {
	srv := New(Options{
		Bind:    "127.0.0.1:0",
		Version: "test",
		Auth: AuthOptions{
			Enabled:     true,
			BearerToken: "secret-token",
			AllowPaths:  []string{"/healthz"},
		},
		Register: func(mux *http.ServeMux) {
			mux.HandleFunc("/secure", func(w http.ResponseWriter, r *http.Request) {
				WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
			})
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestBearerAuthAllowsValidToken(t *testing.T) {
	srv := New(Options{
		Bind:    "127.0.0.1:0",
		Version: "test",
		Auth: AuthOptions{
			Enabled:     true,
			BearerToken: "secret-token",
			AllowPaths:  []string{"/healthz"},
		},
		Register: func(mux *http.ServeMux) {
			mux.HandleFunc("/secure", func(w http.ResponseWriter, r *http.Request) {
				WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
			})
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestBearerAuthInjectsTrustedPrincipalAndIgnoresActorHeader(t *testing.T) {
	var got Principal
	srv := New(Options{
		Bind:    "127.0.0.1:0",
		Version: "test",
		Auth: AuthOptions{
			Enabled:     true,
			BearerToken: "secret-token",
		},
		Register: func(mux *http.ServeMux) {
			mux.HandleFunc("/secure", func(w http.ResponseWriter, r *http.Request) {
				got = PrincipalFromRequest(r)
				WriteJSON(w, http.StatusOK, got)
			})
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/secure", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("X-LocalGate-Actor", "spoofed-admin")
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got.ID != "http-bearer" || got.AuthMethod != "bearer" || !got.Authenticated {
		t.Fatalf("unexpected principal: %#v", got)
	}
}

func TestDisabledAuthInjectsUnauthenticatedPrincipal(t *testing.T) {
	var got Principal
	srv := New(Options{
		Bind:    "127.0.0.1:0",
		Version: "test",
		Register: func(mux *http.ServeMux) {
			mux.HandleFunc("/open", func(w http.ResponseWriter, r *http.Request) {
				got = PrincipalFromRequest(r)
				WriteJSON(w, http.StatusOK, got)
			})
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/open", nil)
	req.Header.Set("X-LocalGate-Actor", "spoofed-admin")
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got.ID != "local-unauthenticated" || got.AuthMethod != "none" || got.Authenticated {
		t.Fatalf("unexpected principal: %#v", got)
	}
}

func TestBearerAuthStillProtectsDownloadRoute(t *testing.T) {
	srv := New(Options{
		Bind:    "127.0.0.1:0",
		Version: "test",
		Auth: AuthOptions{
			Enabled:     true,
			BearerToken: "secret-token",
			AllowPaths:  []string{"/healthz"},
		},
		Register: func(mux *http.ServeMux) {
			mux.HandleFunc("/system/downloads", func(w http.ResponseWriter, r *http.Request) {
				WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
			})
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/system/downloads?token=lgdl_xxx", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without bearer, got %d body=%s", w.Code, w.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodGet, "/system/downloads?token=lgdl_xxx", nil)
	req2.Header.Set("Authorization", "Bearer secret-token")
	w2 := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 with bearer, got %d body=%s", w2.Code, w2.Body.String())
	}
}

func TestBearerAuthStillProtectsToolHTTPRoute(t *testing.T) {
	srv := New(Options{
		Bind:    "127.0.0.1:0",
		Version: "test",
		Auth: AuthOptions{
			Enabled:     true,
			BearerToken: "secret-token",
			AllowPaths:  []string{"/healthz"},
		},
		Register: func(mux *http.ServeMux) {
			mux.HandleFunc("/tools/read_file/call", func(w http.ResponseWriter, r *http.Request) {
				WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
			})
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/tools/read_file/call", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without bearer, got %d body=%s", w.Code, w.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/tools/read_file/call", nil)
	req2.Header.Set("Authorization", "Bearer secret-token")
	w2 := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 with bearer, got %d body=%s", w2.Code, w2.Body.String())
	}
}

func newCORSTestServer(enabled bool, origins []string) *Server {
	return New(Options{
		Bind:    "127.0.0.1:0",
		Version: "test",
		Auth: AuthOptions{
			Enabled:     true,
			BearerToken: "secret-token",
			AllowPaths:  []string{"/healthz"},
		},
		CORS: CORSOptions{
			Enabled:        enabled,
			AllowedOrigins: origins,
		},
		Register: func(mux *http.ServeMux) {
			mux.HandleFunc("/secure", func(w http.ResponseWriter, r *http.Request) {
				WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
			})
		},
	})
}

func TestCORSPreflightAllowedWithoutBearer(t *testing.T) {
	srv := newCORSTestServer(true, []string{"http://localhost:3000"})
	req := httptest.NewRequest(http.MethodOptions, "/secure", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("unexpected allow-origin: %q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Authorization") {
		t.Fatalf("expected explicit Authorization allow-header, got %q", got)
	}
}

func TestCORSHeadersOnAuthenticatedRequest(t *testing.T) {
	srv := newCORSTestServer(true, []string{"http://localhost:3000"})
	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("unexpected allow-origin: %q", got)
	}
}

func TestCORSRejectsUnlistedOrigin(t *testing.T) {
	srv := newCORSTestServer(true, []string{"http://localhost:3000"})
	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("disallowed origin received CORS header %q", got)
	}
}

func TestOriginRejectedWhenCORSDisabled(t *testing.T) {
	srv := newCORSTestServer(false, nil)
	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestApprovalRouteUsesDedicatedOriginValidationWhenCORSDisabled(t *testing.T) {
	srv := New(Options{
		Bind:    "127.0.0.1:0",
		Version: "test",
		CORS: CORSOptions{
			Enabled: false,
		},
		Register: func(mux *http.ServeMux) {
			mux.HandleFunc("/approvals/a1/reject", func(w http.ResponseWriter, r *http.Request) {
				WriteJSON(w, http.StatusOK, map[string]any{"handled": true})
			})
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/approvals/a1/reject", nil)
	req.Header.Set("Origin", "https://approve.example")
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("approval handler should receive Origin request: code=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("management route should not emit general CORS headers: %q", got)
	}
}

func TestAdminRouteUsesDedicatedOriginValidationWhenCORSDisabled(t *testing.T) {
	srv := New(Options{
		Bind: "127.0.0.1:0", Version: "test", CORS: CORSOptions{Enabled: false},
		Register: func(mux *http.ServeMux) {
			mux.HandleFunc("/api/admin/v1/tasks/a1/cancel", func(w http.ResponseWriter, _ *http.Request) {
				WriteJSON(w, http.StatusOK, map[string]any{"handled": true})
			})
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/v1/tasks/a1/cancel", nil)
	req.Header.Set("Origin", "https://admin.example")
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("admin handler should receive Origin request: code=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("admin route should not emit general CORS headers: %q", got)
	}
}

func TestAdminURLBoundaryRejectsOtherHostsWithoutHostRouting(t *testing.T) {
	srv := New(Options{
		Bind: "127.0.0.1:0", Version: "test", AdminBaseURL: "https://admin.example.test", ControlBaseURL: "https://control.example.test",
		Register: func(mux *http.ServeMux) {
			mux.HandleFunc("/approval-ui", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
			mux.HandleFunc("/approvals", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		},
	})

	correct := httptest.NewRequest(http.MethodGet, "https://admin.example.test/approval-ui", nil)
	correctW := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(correctW, correct)
	if correctW.Code != http.StatusOK {
		t.Fatalf("admin URL should be allowed: %d", correctW.Code)
	}

	for _, path := range []string{"/approval-ui", "/approvals"} {
		wrong := httptest.NewRequest(http.MethodGet, "https://control.example.test"+path, nil)
		wrongW := httptest.NewRecorder()
		srv.srv.Handler.ServeHTTP(wrongW, wrong)
		if wrongW.Code != http.StatusNotFound {
			t.Fatalf("wrong host path %s should be hidden: %d", path, wrongW.Code)
		}
	}

	nonApproval := httptest.NewRequest(http.MethodGet, "https://admin.example.test/tasks", nil)
	nonApprovalW := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(nonApprovalW, nonApproval)
	if nonApprovalW.Code != http.StatusNotFound {
		t.Fatalf("dedicated admin host should hide control routes: %d", nonApprovalW.Code)
	}
}

func TestAdminURLBoundaryHonorsConfiguredPort(t *testing.T) {
	srv := New(Options{
		Bind: "127.0.0.1:0", Version: "test", AdminBaseURL: "https://admin.example.test:8443", ControlBaseURL: "https://control.example.test",
		Register: func(mux *http.ServeMux) {
			mux.HandleFunc("/approval-ui", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		},
	})
	wrong := httptest.NewRequest(http.MethodGet, "https://admin.example.test/approval-ui", nil)
	wrongW := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(wrongW, wrong)
	if wrongW.Code != http.StatusNotFound {
		t.Fatalf("admin URL without configured port should be rejected: %d", wrongW.Code)
	}
	correct := httptest.NewRequest(http.MethodGet, "https://admin.example.test:8443/approval-ui", nil)
	correctW := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(correctW, correct)
	if correctW.Code != http.StatusOK {
		t.Fatalf("admin URL with configured port should be allowed: %d", correctW.Code)
	}
}
