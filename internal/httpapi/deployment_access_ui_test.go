package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"localgate/internal/deployment"
)

func TestDeploymentUIChangesStaticAccess(t *testing.T) {
	application, mux, workspaceRoot := newDeploymentOverviewTestServer(t)
	application.DeploymentManager.ConfigureAppHosting("https://public.example.test", "https://private.example.test", deployment.AccessPrivate)
	deployOverviewTestSite(t, application, workspaceRoot, "access-site", "hello")

	items, err := application.DeploymentManager.List(context.Background())
	if err != nil || len(items) != 1 || items[0].Access != deployment.AccessPublic {
		t.Fatalf("legacy seeded deployment should be public: %#v err=%v", items, err)
	}
	postDeploymentAction(t, mux, "/deploy-mgr/actions/static/access-site/set-private")
	items, err = application.DeploymentManager.List(context.Background())
	if err != nil || len(items) != 1 || items[0].Access != deployment.AccessPrivate || items[0].URL != "https://access-site.private.example.test/" {
		t.Fatalf("deployment access was not changed: %#v err=%v", items, err)
	}

	page := httptest.NewRequest(http.MethodGet, "https://admin.example.test/deploy-mgr", nil)
	pageW := httptest.NewRecorder()
	mux.ServeHTTP(pageW, page)
	if pageW.Code != http.StatusOK || !strings.Contains(pageW.Body.String(), "Make public") || !strings.Contains(pageW.Body.String(), "private") {
		t.Fatalf("management page did not show access conversion: code=%d body=%s", pageW.Code, pageW.Body.String())
	}
}
