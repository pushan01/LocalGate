package adminapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"localgate/internal/app"
	"localgate/internal/approval"
	"localgate/internal/core"
	"localgate/internal/server"
)

func parseLimit(r *http.Request, fallback int) int {
	limit := fallback
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 200 {
		limit = 200
	}
	return limit
}

func parseOptionalTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, errors.New("time must use RFC3339 format")
	}
	return parsed.UTC(), nil
}

func (a *API) validateMutationOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	browserRequest := strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")) != ""
	if origin == "" {
		if browserRequest {
			server.WriteError(w, http.StatusForbidden, "admin_origin_required", "browser admin requests must include Origin", nil)
			return false
		}
		return true
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
		server.WriteError(w, http.StatusForbidden, "admin_origin_forbidden", "cross-site admin requests are not allowed", nil)
		return false
	}
	expected, err := url.Parse(strings.TrimSpace(a.BaseURL))
	if err != nil || expected.Scheme == "" || expected.Host == "" {
		server.WriteError(w, http.StatusInternalServerError, "admin_origin_config_invalid", "admin base URL is invalid", nil)
		return false
	}
	actual, err := url.Parse(origin)
	if err != nil || actual.Scheme == "" || actual.Host == "" || actual.User != nil ||
		(actual.Path != "" && actual.Path != "/") || actual.RawQuery != "" || actual.Fragment != "" {
		server.WriteError(w, http.StatusForbidden, "admin_origin_forbidden", "admin Origin is invalid", nil)
		return false
	}
	if !strings.EqualFold(actual.Scheme, expected.Scheme) || !strings.EqualFold(actual.Host, expected.Host) {
		server.WriteError(w, http.StatusForbidden, "admin_origin_forbidden", "admin Origin does not match admin base URL", nil)
		return false
	}
	return true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		server.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body", nil)
		return false
	}
	return true
}

func writeSubmission(w http.ResponseWriter, submission app.Submission) {
	if submission.Approval != nil {
		server.WriteJSON(w, http.StatusAccepted, map[string]any{
			"task_id": submission.Approval.TaskID,
			"status":  core.StatusAwaitingApproval,
			"approval": map[string]any{
				"id":         submission.Approval.ID,
				"url":        "/admin/approvals/" + submission.Approval.ID,
				"expires_at": submission.Approval.ExpiresAt,
				"summary":    submission.Approval.Summary,
				"risk_level": submission.Approval.RiskLevel,
			},
		})
		return
	}
	if submission.Task == nil {
		server.WriteError(w, http.StatusInternalServerError, "task_create_failed", "operation did not create a task", nil)
		return
	}
	server.WriteJSON(w, http.StatusAccepted, map[string]any{
		"task_id":  submission.Task.ID,
		"status":   submission.Task.Status,
		"approval": nil,
	})
}

func writeApprovalError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := "approval_failed"
	switch {
	case errors.Is(err, approval.ErrApprovalNotFound):
		status = http.StatusNotFound
		code = "approval_not_found"
	case errors.Is(err, approval.ErrApprovalResolved), errors.Is(err, approval.ErrApprovalExpired):
		status = http.StatusConflict
		code = "approval_resolved"
	case errors.Is(err, approval.ErrApprovalBindingMismatch):
		status = http.StatusConflict
		code = "approval_binding_mismatch"
	}
	server.WriteError(w, status, code, err.Error(), nil)
}
