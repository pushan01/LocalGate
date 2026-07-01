package adminapi

import (
	"net/http"
	"strings"

	"localgate/internal/approval"
	"localgate/internal/server"
)

func (a *API) handleApprovals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	requests, err := a.App.Approvals.List("")
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "approval_list_failed", err.Error(), nil)
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	tool := strings.TrimSpace(r.URL.Query().Get("tool"))
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	requester := strings.TrimSpace(r.URL.Query().Get("requester"))
	limit := parseLimit(r, 50)
	items := make([]approval.Request, 0, limit)
	for _, request := range requests {
		if status != "" && string(request.Status) != status {
			continue
		}
		if sessionID != "" && request.MCPSessionID != sessionID {
			continue
		}
		if tool != "" && request.Tool != tool {
			continue
		}
		if workspaceID != "" && request.WorkspaceID != workspaceID {
			continue
		}
		if requester != "" && request.Requester != requester {
			continue
		}
		items = append(items, request)
		if len(items) >= limit {
			break
		}
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (a *API) handleApprovalRoutes(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path, Prefix+"/approvals/")
	if len(parts) == 0 {
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		request, err := a.App.Approvals.Get(id)
		if err != nil {
			server.WriteError(w, http.StatusNotFound, "approval_not_found", "approval not found", nil)
			return
		}
		var task any
		if request.TaskID != "" {
			if snapshot, getErr := a.App.Tasks.Get(request.TaskID); getErr == nil {
				task = snapshot
			}
		}
		audits, _ := a.App.Approvals.QueryAudit(map[string]string{"approval_id": id}, 200, 0)
		server.WriteJSON(w, http.StatusOK, map[string]any{"approval": request, "task": task, "audits": audits})
		return
	}
	if r.Method != http.MethodPost {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !a.validateMutationOrigin(w, r) {
		return
	}
	principal := server.PrincipalFromRequest(r)
	actor := approval.Actor{ID: principal.ID, AuthMethod: principal.AuthMethod}
	var request approval.Request
	var err error
	switch parts[1] {
	case "approve":
		request, err = a.App.ApproveApproval(id, actor)
	case "reject":
		request, err = a.App.Approvals.RejectBy(id, actor)
		if err == nil {
			_ = a.App.CancelApprovalTaskWithReason(id, request.TaskID, "approval_rejected", "approval was rejected before execution")
		}
	case "cancel":
		request, err = a.App.Approvals.CancelBy(id, actor)
		if err == nil {
			_ = a.App.CancelApprovalTaskWithReason(id, request.TaskID, "approval_canceled", "approval was canceled before execution")
		}
	default:
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	var task any
	if request.TaskID != "" {
		if snapshot, getErr := a.App.Tasks.Get(request.TaskID); getErr == nil {
			task = snapshot
		}
	}
	server.WriteJSON(w, http.StatusAccepted, map[string]any{"approval": request, "task": task})
}
