package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBearerAuthAllowsOnlyConfiguredExportPrefix(t *testing.T) {
	srv := New(Options{
		Bind:    "127.0.0.1:0",
		Version: "test",
		Auth: AuthOptions{
			Enabled:           true,
			BearerToken:       "secret-token",
			AllowPathPrefixes: []string{"/system/exports/"},
		},
		Register: func(mux *http.ServeMux) {
			mux.HandleFunc("/system/exports/", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			mux.HandleFunc("/system/other", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/system/exports/lgexp_x?signature=x", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected export prefix to bypass bearer, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/system/other", nil)
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected unrelated route to remain protected, got %d", w.Code)
	}
}
