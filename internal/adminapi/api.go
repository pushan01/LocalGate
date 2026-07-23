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
	mux.HandleFunc(Prefix+"/file-browser/workspaces", a.wrap(a.handleFileBrowserWorkspaces))
	mux.HandleFunc(Prefix+"/file-browser/files/list", a.wrap(a.handleFileList))
	mux.HandleFunc(Prefix+"/file-browser/files/content", a.wrap(a.handleFileContent))
	mux.HandleFunc(Prefix+"/file-browser/files/mkdir", a.wrap(a.handleFileMkdir))
	mux.HandleFunc(Prefix+"/file-browser/files/rename", a.wrap(a.handleFileRename))
	mux.HandleFunc(Prefix+"/file-browser/files/delete", a.wrap(a.handleFileDelete))
	mux.HandleFunc(Prefix+"/file-browser/files/download", a.wrap(a.handleFileDownload))
	mux.HandleFunc(Prefix+"/file-browser/files/upload", a.wrap(a.handleFileUpload))
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
