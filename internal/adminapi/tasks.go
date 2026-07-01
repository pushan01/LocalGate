package adminapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"localgate/internal/core"
	"localgate/internal/persist"
	"localgate/internal/server"
)

type taskListItem struct {
	ID              string      `json:"id"`
	Status          core.Status `json:"status"`
	Tool            string      `json:"tool,omitempty"`
	Transport       string      `json:"transport,omitempty"`
	SessionID       string      `json:"session_id,omitempty"`
	SessionMode     string      `json:"session_mode,omitempty"`
	SessionIDSource string      `json:"session_id_source,omitempty"`
	RequestID       string      `json:"request_id,omitempty"`
	WorkspaceID     string      `json:"workspace_id,omitempty"`
	Requester       string      `json:"requester,omitempty"`
	ApprovalID      string      `json:"approval_id,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
	StartedAt       time.Time   `json:"started_at,omitempty"`
	EndedAt         time.Time   `json:"ended_at,omitempty"`
	DurationMS      int64       `json:"duration_ms,omitempty"`
	ErrorCode       string      `json:"error_code,omitempty"`
	ErrorMessage    string      `json:"error_message,omitempty"`
}

func (a *API) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	createdAfter, err := parseOptionalTime(r.URL.Query().Get("created_after"))
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid_created_after", err.Error(), nil)
		return
	}
	createdBefore, err := parseOptionalTime(r.URL.Query().Get("created_before"))
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid_created_before", err.Error(), nil)
		return
	}
	page, err := a.App.Tasks.List(core.TaskFilter{
		SessionID: r.URL.Query().Get("session_id"), Transport: r.URL.Query().Get("transport"),
		Tool: r.URL.Query().Get("tool"), Status: core.Status(r.URL.Query().Get("status")),
		WorkspaceID: r.URL.Query().Get("workspace_id"), Requester: r.URL.Query().Get("requester"),
		ApprovalID: r.URL.Query().Get("approval_id"), CreatedAfter: createdAfter, CreatedBefore: createdBefore,
		Limit: parseLimit(r, 50), Cursor: r.URL.Query().Get("cursor"),
	})
	if errors.Is(err, core.ErrInvalidTaskCursor) {
		server.WriteError(w, http.StatusBadRequest, "invalid_cursor", err.Error(), nil)
		return
	}
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "task_list_failed", err.Error(), nil)
		return
	}
	items := make([]taskListItem, 0, len(page.Items))
	for _, task := range page.Items {
		items = append(items, summarizeTask(task))
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": page.NextCursor})
}

func (a *API) handleTaskRoutes(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path, Prefix+"/tasks/")
	if len(parts) == 0 {
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
	taskID := parts[0]
	task, err := a.App.Tasks.Get(taskID)
	if err != nil {
		server.WriteError(w, http.StatusNotFound, "task_not_found", "task not found", nil)
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		var approvalValue any
		if task.ApprovalID != "" {
			if req, getErr := a.App.Approvals.Get(task.ApprovalID); getErr == nil {
				approvalValue = req
			}
		}
		server.WriteJSON(w, http.StatusOK, map[string]any{"task": task, "approval": approvalValue})
		return
	}
	switch parts[1] {
	case "events":
		if r.Method != http.MethodGet {
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		cursor := 0
		if value := strings.TrimSpace(r.URL.Query().Get("cursor")); value != "" {
			parsed, parseErr := strconv.Atoi(value)
			if parseErr != nil || parsed < 0 {
				server.WriteError(w, http.StatusBadRequest, "invalid_cursor", "cursor must be a non-negative integer", nil)
				return
			}
			cursor = parsed
		}
		events, next, loadErr := a.taskEvents(taskID, cursor)
		if loadErr != nil {
			server.WriteError(w, http.StatusInternalServerError, "task_events_failed", loadErr.Error(), nil)
			return
		}
		server.WriteJSON(w, http.StatusOK, map[string]any{"items": events, "next_cursor": next})
	case "audits":
		if r.Method != http.MethodGet {
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		records, queryErr := a.App.Approvals.QueryAudit(map[string]string{"task_id": taskID}, parseLimit(r, 100), 0)
		if queryErr != nil {
			server.WriteError(w, http.StatusInternalServerError, "audit_query_failed", queryErr.Error(), nil)
			return
		}
		server.WriteJSON(w, http.StatusOK, map[string]any{"items": records})
	case "cancel":
		if r.Method != http.MethodPost {
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		if !a.validateMutationOrigin(w, r) {
			return
		}
		if err := a.App.Tasks.CancelWithReason(taskID, "admin_canceled", "task was canceled from the admin console"); err != nil {
			server.WriteError(w, http.StatusBadRequest, "task_cancel_failed", err.Error(), nil)
			return
		}
		updated, _ := a.App.Tasks.Get(taskID)
		server.WriteJSON(w, http.StatusAccepted, map[string]any{"task": updated})
	default:
		server.WriteError(w, http.StatusNotFound, "not_found", "not found", nil)
	}
}

func (a *API) taskEvents(taskID string, cursor int) ([]core.Event, int, error) {
	all, next := a.App.Sink.List(taskID, 0)
	if next > 0 {
		if cursor > len(all) {
			cursor = len(all)
		}
		return append([]core.Event(nil), all[cursor:]...), len(all), nil
	}
	all, err := persist.NewStore(a.App.StoreBase).LoadEvents(taskID, 0)
	if err != nil {
		return nil, 0, err
	}
	if cursor > len(all) {
		cursor = len(all)
	}
	return append([]core.Event(nil), all[cursor:]...), len(all), nil
}

func summarizeTask(task core.Task) taskListItem {
	end := task.EndedAt
	if end.IsZero() && !task.StartedAt.IsZero() {
		end = time.Now().UTC()
	}
	duration := int64(0)
	if !task.StartedAt.IsZero() && !end.IsZero() {
		duration = end.Sub(task.StartedAt).Milliseconds()
	}
	return taskListItem{
		ID: task.ID, Status: task.Status, Tool: task.Tool, Transport: task.Transport,
		SessionID: task.MCPSessionID, SessionMode: task.SessionMode, SessionIDSource: task.SessionIDSource,
		RequestID: task.RequestID, WorkspaceID: task.WorkspaceID, Requester: task.Requester,
		ApprovalID: task.ApprovalID, CreatedAt: task.CreatedAt, StartedAt: task.StartedAt,
		EndedAt: task.EndedAt, DurationMS: duration, ErrorCode: task.ErrorCode, ErrorMessage: task.ErrorMsg,
	}
}
