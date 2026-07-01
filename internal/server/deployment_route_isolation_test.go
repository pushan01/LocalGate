package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeploymentPagesAreSeparatedByHostAndPath(t *testing.T) {
	control := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	router := newHostRouter(control, HostRoutingOptions{
		Enabled: true, ControlHosts: []string{"control.example.test"},
		AppDomain: "apps.example.test", AppHandler: http.NotFoundHandler(),
		AdminBaseURL: "https://admin.example.test/review",
	})
	cases := []struct {
		url  string
		want int
	}{
		{"https://control.example.test/deploys", http.StatusOK},
		{"https://control.example.test/deploy-mgr", http.StatusNotFound},
		{"https://control.example.test/deploy-mgr/actions/dynamic/demo/stop", http.StatusNotFound},
		{"https://admin.example.test/deploy-mgr", http.StatusOK},
		{"https://admin.example.test/deploy-mgr/actions/dynamic/demo/stop", http.StatusOK},
		{"https://admin.example.test/deploys", http.StatusNotFound},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.url, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatalf("GET %s: got %d want %d", tc.url, w.Code, tc.want)
		}
	}
}
