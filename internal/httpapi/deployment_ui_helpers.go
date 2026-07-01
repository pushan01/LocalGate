package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"localgate/internal/core"
	"localgate/internal/server"
)

func waitForDeploymentManagementTask(r *http.Request, a *API, taskID string, timeout time.Duration) (*core.Task, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		task, err := a.App.Tasks.Get(taskID)
		if err != nil {
			return nil, err
		}
		switch task.Status {
		case core.StatusSucceeded, core.StatusFailed, core.StatusCanceled, core.StatusTimeout:
			return task, nil
		}
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-deadline.C:
			return task, errors.New("operation is still running; refresh the deployment page shortly")
		case <-ticker.C:
		}
	}
}

func (a *API) validateDeploymentManagementOrigin(w http.ResponseWriter, r *http.Request) bool {
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	browserRequest := strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")) != "" || strings.HasPrefix(contentType, "application/x-www-form-urlencoded")
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		if browserRequest {
			server.WriteError(w, http.StatusForbidden, "deployment_origin_required", "browser deployment actions must include Origin", nil)
			return false
		}
		return true
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
		server.WriteError(w, http.StatusForbidden, "deployment_origin_forbidden", "cross-site deployment actions are not allowed", nil)
		return false
	}
	expected, err := url.Parse(strings.TrimSpace(a.App.Admin.BaseURL))
	if err != nil || expected.Scheme == "" || expected.Host == "" {
		server.WriteError(w, http.StatusInternalServerError, "deployment_origin_config_invalid", "http.base_url is not configured", nil)
		return false
	}
	actual, err := url.Parse(origin)
	if err != nil || actual.Scheme == "" || actual.Host == "" || actual.User != nil || (actual.Path != "" && actual.Path != "/") || actual.RawQuery != "" || actual.Fragment != "" {
		server.WriteError(w, http.StatusForbidden, "deployment_origin_forbidden", "deployment Origin is invalid", nil)
		return false
	}
	if !strings.EqualFold(actual.Scheme, expected.Scheme) || !strings.EqualFold(actual.Host, expected.Host) {
		server.WriteError(w, http.StatusForbidden, "deployment_origin_forbidden", "deployment Origin does not match http.base_url", nil)
		return false
	}
	return true
}

func (a *API) redirectDeploymentManagement(w http.ResponseWriter, r *http.Request, level, notice string) {
	target := strings.TrimRight(strings.TrimSpace(a.App.Admin.BaseURL), "/") + "/deploy-mgr"
	values := url.Values{}
	values.Set("notice", notice)
	if level != "" {
		values.Set("level", level)
	}
	http.Redirect(w, r, target+"?"+values.Encode(), http.StatusSeeOther)
}

func deploymentActionURL(baseURL, deploymentType, name, action string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/deploy-mgr/actions/" +
		url.PathEscape(deploymentType) + "/" + url.PathEscape(name) + "/" + url.PathEscape(action)
}

func deploymentActionSuccessMessage(action, deploymentType, name string) string {
	switch action {
	case "delete":
		return deploymentType + " deployment " + name + " was deleted."
	case "stop":
		return "Dynamic deployment " + name + " was stopped."
	case "start":
		return "Dynamic deployment " + name + " was started."
	default:
		return "Deployment operation completed."
	}
}

func setDeploymentManagementHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
}
