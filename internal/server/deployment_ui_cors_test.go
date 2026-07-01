package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeploymentManagementRouteUsesDedicatedOriginValidationWhenCORSDisabled(t *testing.T) {
	srv := New(Options{
		Bind:    "127.0.0.1:0",
		Version: "test",
		CORS:    CORSOptions{Enabled: false},
		Register: func(mux *http.ServeMux) {
			mux.HandleFunc("/deploy-mgr/actions/dynamic/demo/stop", func(w http.ResponseWriter, _ *http.Request) {
				WriteJSON(w, http.StatusOK, map[string]any{"handled": true})
			})
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/deploy-mgr/actions/dynamic/demo/stop", nil)
	req.Header.Set("Origin", "https://control.example")
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("deployment handler should receive Origin request: code=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("deployment route should not emit general CORS headers: %q", got)
	}
}
