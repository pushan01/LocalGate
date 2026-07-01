package adminapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"localgate/internal/deployment"
	"localgate/internal/invocation"
	"localgate/internal/server"
)

func (a *API) handleDeployments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	items, err := a.App.DeploymentManager.List(r.Context())
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "deployment_list_failed", err.Error(), nil)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (a *API) handleDeploymentRoutes(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path, Prefix+"/deployments/")
	if len(parts) < 2 {
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
	deploymentType, err := url.PathUnescape(parts[0])
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid_deployment_type", "invalid deployment type", nil)
		return
	}
	name, err := url.PathUnescape(parts[1])
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid_deployment_name", "invalid deployment name", nil)
		return
	}
	if deploymentType != deployment.TypeStatic && deploymentType != deployment.TypeDynamic {
		server.WriteError(w, http.StatusBadRequest, "invalid_deployment_type", "unsupported deployment type", nil)
		return
	}
	if len(parts) == 2 && r.Method == http.MethodGet {
		view, getErr := a.findDeployment(r, deploymentType, name)
		if getErr != nil {
			server.WriteError(w, http.StatusNotFound, "deployment_not_found", getErr.Error(), nil)
			return
		}
		server.WriteJSON(w, http.StatusOK, map[string]any{"deployment": view})
		return
	}
	if len(parts) == 3 && parts[2] == "logs" {
		if r.Method != http.MethodGet {
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		tailBytes := int64(64 * 1024)
		if value := strings.TrimSpace(r.URL.Query().Get("tail_bytes")); value != "" {
			if parsed, parseErr := strconv.ParseInt(value, 10, 64); parseErr == nil && parsed > 0 {
				tailBytes = parsed
			}
		}
		logs, logsErr := a.App.DeploymentManager.Logs(deploymentType, name, r.URL.Query().Get("stream"), tailBytes)
		if logsErr != nil {
			server.WriteError(w, http.StatusBadRequest, "deployment_logs_failed", logsErr.Error(), nil)
			return
		}
		server.WriteJSON(w, http.StatusOK, logs)
		return
	}
	if len(parts) == 3 && parts[2] == "releases" {
		if r.Method != http.MethodGet {
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		releases, releasesErr := a.App.DeploymentManager.Releases(deploymentType, name)
		if releasesErr != nil {
			server.WriteError(w, http.StatusBadRequest, "deployment_releases_failed", releasesErr.Error(), nil)
			return
		}
		server.WriteJSON(w, http.StatusOK, map[string]any{"items": releases})
		return
	}

	action := "delete"
	if len(parts) == 3 {
		action = parts[2]
	}
	if action == "delete" {
		if r.Method != http.MethodDelete {
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
	} else if r.Method != http.MethodPost {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !a.validateMutationOrigin(w, r) {
		return
	}
	args := map[string]any{"type": deploymentType, "name": name}
	tool := ""
	switch action {
	case "start":
		tool = "start_deployment"
	case "stop":
		tool = "stop_deployment"
	case "restart":
		tool = "restart_deployment"
	case "rollback":
		var body struct {
			ReleaseID string `json:"release_id"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if strings.TrimSpace(body.ReleaseID) == "" {
			server.WriteError(w, http.StatusBadRequest, "release_id_required", "release_id is required", nil)
			return
		}
		args["release_id"] = body.ReleaseID
		tool = "rollback_deployment"
	case "delete":
		var body struct {
			Confirmation string `json:"confirmation"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if body.Confirmation != "delete" {
			server.WriteError(w, http.StatusBadRequest, "deployment_delete_confirmation_required", "confirmation must equal delete", nil)
			return
		}
		tool = "delete_deployment"
	default:
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
	principal := server.PrincipalFromRequest(r)
	submission, submitErr := a.App.SubmitToolTaskWithMeta(invocation.Meta{
		Transport: invocation.TransportAdmin, WorkspaceID: a.DefaultWSID, Requester: principal.ID,
	}, tool, args)
	if submitErr != nil {
		server.WriteError(w, http.StatusBadRequest, "deployment_submission_failed", submitErr.Error(), nil)
		return
	}
	writeSubmission(w, submission)
}

func (a *API) findDeployment(r *http.Request, deploymentType, name string) (deployment.View, error) {
	items, err := a.App.DeploymentManager.List(r.Context())
	if err != nil {
		return deployment.View{}, err
	}
	for _, item := range items {
		if item.Type == deploymentType && item.Site == name {
			return item, nil
		}
	}
	return deployment.View{}, errors.New("deployment not found")
}
