package adminapi

import (
	"net/http"
	"strings"

	"localgate/internal/app"
	"localgate/internal/server"
)

const Prefix = "/api/admin/v1"

type API struct {
	App         *app.App
	DefaultWSID string
	BaseURL     string
}

func (a *API) Register(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc(Prefix+"/overview", a.wrap(a.handleOverview))
	mux.HandleFunc(Prefix+"/tasks", a.wrap(a.handleTasks))
	mux.HandleFunc(Prefix+"/tasks/", a.wrap(a.handleTaskRoutes))
	mux.HandleFunc(Prefix+"/approvals", a.wrap(a.handleApprovals))
	mux.HandleFunc(Prefix+"/approvals/", a.wrap(a.handleApprovalRoutes))
	mux.HandleFunc(Prefix+"/deployments", a.wrap(a.handleDeployments))
	mux.HandleFunc(Prefix+"/deployments/", a.wrap(a.handleDeploymentRoutes))
}

func (a *API) wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a == nil || a.App == nil {
			server.WriteError(w, http.StatusServiceUnavailable, "admin_unavailable", "admin service is not available", nil)
			return
		}
		if !server.RequestHostMatchesBaseURL(r.Host, a.BaseURL) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next(w, r)
	}
}

func pathParts(path, prefix string) []string {
	trimmed := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}
