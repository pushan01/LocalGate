package adminapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"localgate/internal/app"
	"localgate/internal/core"
	"localgate/internal/filebrowser"
	"localgate/internal/invocation"
	"localgate/internal/server"
)

const (
	defaultTerminalReadItems = 200
	maxTerminalReadItems     = 500
	maxTerminalCommandBytes  = 8 << 10
	maxTerminalInputBytes    = 64 << 10
	maxTerminalWaitMS        = 1000
)

type createFileTerminalRequest struct {
	WorkspaceID    string `json:"workspace_id"`
	RootID         string `json:"root_id"`
	Path           string `json:"path"`
	Command        string `json:"command"`
	Shell          string `json:"shell"`
	Login          bool   `json:"login"`
	TTY            *bool  `json:"tty"`
	YieldTimeMS    int    `json:"yield_time_ms"`
	MaxOutputItems int    `json:"max_output_items"`
}

type fileTerminalInputRequest struct {
	WorkspaceID    string `json:"workspace_id"`
	Chars          string `json:"chars"`
	Cursor         int    `json:"cursor"`
	WaitMS         int    `json:"wait_ms"`
	MaxOutputItems int    `json:"max_output_items"`
}

type fileTerminalCloseRequest struct {
	WorkspaceID string `json:"workspace_id"`
}

func (a *API) handleFileTerminalCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !a.validateMutationOrigin(w, r) {
		return
	}
	svc, ok := a.fileBrowserService(w)
	if !ok {
		return
	}
	var req createFileTerminalRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.WorkspaceID) == "" {
		req.WorkspaceID = a.DefaultWSID
	}
	command := strings.TrimSpace(req.Command)
	if command == "" {
		server.WriteError(w, http.StatusBadRequest, "command_required", "command is required", nil)
		return
	}
	if len(command) > maxTerminalCommandBytes {
		server.WriteError(w, http.StatusBadRequest, "command_too_large", "command is too large", nil)
		return
	}
	directory, err := svc.ResolveDirectory(filebrowser.PathRequest{
		WorkspaceID: req.WorkspaceID,
		RootID:      req.RootID,
		Path:        req.Path,
	})
	if err != nil {
		a.writeFileBrowserError(w, err)
		return
	}
	tty := true
	if req.TTY != nil {
		tty = *req.TTY
	}
	yield := clampInt(req.YieldTimeMS, 0, maxTerminalWaitMS)
	maxItems := terminalReadItems(req.MaxOutputItems)
	submission, err := a.App.SubmitToolTaskWithMeta(invocation.Meta{
		Transport:   invocation.TransportAdmin,
		WorkspaceID: directory.WorkspaceID,
		Requester:   "admin:file-browser",
	}, "exec_command", map[string]any{
		"cmd":               command,
		"workdir":           directory.AbsPath,
		"yield_time_ms":     yield,
		"max_output_tokens": maxItems,
		"shell":             strings.TrimSpace(req.Shell),
		"login":             req.Login,
		"tty":               tty,
	})
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "terminal_create_failed", err.Error(), nil)
		return
	}
	writeFileTerminalSubmission(w, submission, directory)
}

func (a *API) handleFileTerminalRoutes(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path, Prefix+"/file-browser/terminals/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		server.WriteError(w, http.StatusNotFound, "terminal_not_found", "terminal not found", nil)
		return
	}
	sessionID := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
		if workspaceID == "" {
			workspaceID = a.DefaultWSID
		}
		cursor, ok := parseTerminalCursor(w, r.URL.Query().Get("cursor"))
		if !ok {
			return
		}
		maxItems := terminalReadItems(parsePositiveInt(r.URL.Query().Get("max_output_items")))
		a.writeFileTerminalRead(w, sessionID, workspaceID, cursor, maxItems)
		return
	}
	switch parts[1] {
	case "stream":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
		if workspaceID == "" {
			workspaceID = a.DefaultWSID
		}
		cursor, ok := parseTerminalCursor(w, r.URL.Query().Get("cursor"))
		if !ok {
			return
		}
		maxItems := terminalReadItems(parsePositiveInt(r.URL.Query().Get("max_output_items")))
		a.writeFileTerminalStream(w, r, sessionID, workspaceID, cursor, maxItems)
	case "stdin":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		if !a.validateMutationOrigin(w, r) {
			return
		}
		var req fileTerminalInputRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.WorkspaceID) == "" {
			req.WorkspaceID = a.DefaultWSID
		}
		if req.Cursor < 0 {
			server.WriteError(w, http.StatusBadRequest, "invalid_cursor", "cursor must be a non-negative integer", nil)
			return
		}
		if len([]byte(req.Chars)) > maxTerminalInputBytes {
			server.WriteError(w, http.StatusBadRequest, "terminal_input_too_large", "terminal input exceeds 64 KiB", nil)
			return
		}
		if err := a.App.Sessions.WriteStdinForWorkspaceContext(r.Context(), sessionID, req.WorkspaceID, req.Chars); err != nil {
			writeFileTerminalError(w, err)
			return
		}
		wait := time.Duration(clampInt(req.WaitMS, 0, maxTerminalWaitMS)) * time.Millisecond
		session, read, err := a.App.Sessions.WaitReadForWorkspace(r.Context(), sessionID, req.WorkspaceID, req.Cursor, terminalReadItems(req.MaxOutputItems), wait)
		if err != nil {
			writeFileTerminalError(w, err)
			return
		}
		server.WriteJSON(w, http.StatusOK, fileTerminalReadPayload(session, read))
	case "close":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		if !a.validateMutationOrigin(w, r) {
			return
		}
		var req fileTerminalCloseRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.WorkspaceID) == "" {
			req.WorkspaceID = a.DefaultWSID
		}
		session, err := a.App.Sessions.CloseForWorkspace(sessionID, req.WorkspaceID)
		if err != nil {
			writeFileTerminalError(w, err)
			return
		}
		server.WriteJSON(w, http.StatusAccepted, map[string]any{"session": session})
	default:
		server.WriteError(w, http.StatusNotFound, "terminal_not_found", "terminal not found", nil)
	}
}

func (a *API) writeFileTerminalRead(w http.ResponseWriter, sessionID, workspaceID string, cursor, maxItems int) {
	payload, err := a.readFileTerminal(sessionID, workspaceID, cursor, maxItems)
	if err != nil {
		writeFileTerminalError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, payload)
}

type fileTerminalReadResponse struct {
	Session         core.Session         `json:"session"`
	Items           []core.SessionOutput `json:"items"`
	NextOffset      int                  `json:"next_offset"`
	BaseOffset      int                  `json:"base_offset"`
	CursorExpired   bool                 `json:"cursor_expired,omitempty"`
	Truncated       bool                 `json:"truncated"`
	BufferTruncated bool                 `json:"buffer_truncated"`
	HasMore         bool                 `json:"has_more"`
	Completed       bool                 `json:"completed"`
	OutputError     string               `json:"output_error,omitempty"`
}

func (a *API) readFileTerminal(sessionID, workspaceID string, cursor, maxItems int) (fileTerminalReadResponse, error) {
	session, read, err := a.App.Sessions.ReadSnapshotForWorkspace(sessionID, workspaceID, cursor, maxItems)
	if err != nil {
		return fileTerminalReadResponse{}, err
	}
	return fileTerminalReadPayload(session, read), nil
}

func fileTerminalReadPayload(session core.Session, read core.SessionReadResult) fileTerminalReadResponse {
	ended := terminalSessionEnded(session.Status)
	return fileTerminalReadResponse{
		Session:         session,
		Items:           read.Items,
		NextOffset:      read.NextOffset,
		BaseOffset:      read.BaseOffset,
		CursorExpired:   read.CursorExpired,
		Truncated:       read.Truncated,
		BufferTruncated: read.BufferTruncated,
		HasMore:         read.HasMore,
		Completed:       ended && !read.HasMore,
		OutputError:     read.OutputError,
	}
}

func writeFileTerminalSubmission(w http.ResponseWriter, submission app.Submission, directory filebrowser.DirectoryContext) {
	payload := map[string]any{
		"workspace_id": directory.WorkspaceID,
		"root_id":      directory.RootID,
		"path":         directory.Path,
	}
	if submission.Approval != nil {
		payload["task_id"] = submission.Approval.TaskID
		payload["status"] = core.StatusAwaitingApproval
		payload["approval"] = map[string]any{
			"id":         submission.Approval.ID,
			"url":        "/admin/approvals/" + submission.Approval.ID,
			"expires_at": submission.Approval.ExpiresAt,
			"summary":    submission.Approval.Summary,
			"risk_level": submission.Approval.RiskLevel,
		}
		server.WriteJSON(w, http.StatusAccepted, payload)
		return
	}
	if submission.Task == nil {
		server.WriteError(w, http.StatusInternalServerError, "terminal_task_missing", "terminal operation did not create a task", nil)
		return
	}
	payload["task_id"] = submission.Task.ID
	payload["status"] = submission.Task.Status
	payload["approval"] = nil
	server.WriteJSON(w, http.StatusAccepted, payload)
}

func writeFileTerminalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, core.ErrSessionNotFound):
		server.WriteError(w, http.StatusNotFound, "terminal_not_found", "terminal not found", nil)
	case errors.Is(err, core.ErrSessionWorkspaceMismatch):
		server.WriteError(w, http.StatusForbidden, "terminal_workspace_mismatch", "terminal belongs to a different workspace", nil)
	case errors.Is(err, core.ErrSessionClosed):
		server.WriteError(w, http.StatusConflict, "terminal_closed", "terminal is closed", nil)
	default:
		server.WriteError(w, http.StatusBadRequest, "terminal_failed", err.Error(), nil)
	}
}

func parseTerminalCursor(w http.ResponseWriter, value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, true
	}
	cursor, err := strconv.Atoi(value)
	if err != nil || cursor < 0 {
		server.WriteError(w, http.StatusBadRequest, "invalid_cursor", "cursor must be a non-negative integer", nil)
		return 0, false
	}
	return cursor, true
}

func parsePositiveInt(value string) int {
	parsed, _ := strconv.Atoi(strings.TrimSpace(value))
	if parsed < 0 {
		return 0
	}
	return parsed
}

func terminalReadItems(value int) int {
	if value <= 0 {
		return defaultTerminalReadItems
	}
	if value > maxTerminalReadItems {
		return maxTerminalReadItems
	}
	return value
}

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
