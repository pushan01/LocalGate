package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeploymentManagementRequiresApprovalHost(t *testing.T) {
	_, mux, _ := newDeploymentOverviewTestServer(t)

	wrongPage := httptest.NewRecorder()
	mux.ServeHTTP(wrongPage, httptest.NewRequest(http.MethodGet, "https://control.example.test/deploy-mgr", nil))
	if wrongPage.Code != http.StatusNotFound {
		t.Fatalf("management page on control host should be hidden: %d", wrongPage.Code)
	}

	wrongAction := httptest.NewRequest(http.MethodPost, "https://control.example.test/deploy-mgr/actions/dynamic/demo/stop", nil)
	wrongAction.Header.Set("Origin", "https://control.example.test")
	wrongAction.Header.Set("Sec-Fetch-Site", "same-origin")
	wrongActionW := httptest.NewRecorder()
	mux.ServeHTTP(wrongActionW, wrongAction)
	if wrongActionW.Code != http.StatusNotFound {
		t.Fatalf("management action on control host should be hidden: %d body=%s", wrongActionW.Code, wrongActionW.Body.String())
	}

	manage := httptest.NewRecorder()
	mux.ServeHTTP(manage, httptest.NewRequest(http.MethodGet, "https://admin.example.test/deploy-mgr", nil))
	if manage.Code != http.StatusOK || !strings.Contains(manage.Body.String(), "Deployment management") {
		t.Fatalf("approval host management page failed: code=%d body=%s", manage.Code, manage.Body.String())
	}
}
