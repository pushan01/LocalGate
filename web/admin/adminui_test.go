package adminui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesSPAAndEnforcesHost(t *testing.T) {
	handler := Handler("https://admin.test")
	for _, target := range []string{"https://admin.test/admin", "https://admin.test/admin/history/task-id"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "LocalGate Admin") {
			t.Fatalf("unexpected response for %s: code=%d body=%s", target, w.Code, w.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "https://control.test/admin", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected wrong host 404, got %d", w.Code)
	}
}
