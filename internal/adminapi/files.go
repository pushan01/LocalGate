package adminapi

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"

	"localgate/internal/filebrowser"
	"localgate/internal/server"
)

type saveFileContentRequest struct {
	WorkspaceID string `json:"workspace_id"`
	RootID      string `json:"root_id"`
	Path        string `json:"path"`
	Content     string `json:"content"`
	ETag        string `json:"etag"`
	Create      bool   `json:"create"`
	Overwrite   bool   `json:"overwrite"`
}

type mkdirRequest struct {
	WorkspaceID string `json:"workspace_id"`
	RootID      string `json:"root_id"`
	Path        string `json:"path"`
}

type renameRequest struct {
	WorkspaceID string `json:"workspace_id"`
	RootID      string `json:"root_id"`
	FromPath    string `json:"from_path"`
	ToPath      string `json:"to_path"`
}

type deleteRequest struct {
	WorkspaceID  string `json:"workspace_id"`
	RootID       string `json:"root_id"`
	Path         string `json:"path"`
	Recursive    bool   `json:"recursive"`
	Confirmation string `json:"confirmation"`
}

func (a *API) handleFileBrowserWorkspaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	svc, ok := a.fileBrowserService(w)
	if !ok {
		return
	}
	items, err := svc.Workspaces()
	if err != nil {
		a.writeFileBrowserError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *API) handleFileList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	svc, ok := a.fileBrowserService(w)
	if !ok {
		return
	}
	result, err := svc.List(r.Context(), a.fileBrowserPathRequest(r))
	if err != nil {
		a.writeFileBrowserError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, result)
}

func (a *API) handleFileContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	svc, ok := a.fileBrowserService(w)
	if !ok {
		return
	}
	if r.Method == http.MethodPut {
		if !a.validateMutationOrigin(w, r) {
			return
		}
		var req saveFileContentRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.WorkspaceID == "" {
			req.WorkspaceID = a.DefaultWSID
		}
		result, err := svc.SaveContent(r.Context(), filebrowser.SaveContentRequest{
			WorkspaceID: req.WorkspaceID,
			RootID:      req.RootID,
			Path:        req.Path,
			Content:     req.Content,
			ETag:        req.ETag,
			Create:      req.Create,
			Overwrite:   req.Overwrite,
		})
		if err != nil {
			if errors.Is(err, filebrowser.ErrApprovalRequired) {
				submission, submitErr := a.App.SubmitFileBrowserSave("admin", filebrowser.SaveContentRequest{
					WorkspaceID: req.WorkspaceID,
					RootID:      req.RootID,
					Path:        req.Path,
					Content:     req.Content,
					ETag:        req.ETag,
					Create:      req.Create,
					Overwrite:   req.Overwrite,
				})
				if submitErr != nil {
					a.writeFileBrowserError(w, submitErr)
					return
				}
				writeSubmission(w, submission)
				return
			}
			a.writeFileBrowserError(w, err)
			return
		}
		status := http.StatusOK
		if result.Created {
			status = http.StatusCreated
		}
		server.WriteJSON(w, status, result)
		return
	}
	content, err := svc.ReadContent(r.Context(), a.fileBrowserPathRequest(r))
	if err != nil {
		a.writeFileBrowserError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, content)
}

func (a *API) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
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
	limit := a.App.FileBrowser.MaxUploadBytes
	if limit <= 0 {
		limit = 100 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit+(1<<20))
	if err := r.ParseMultipartForm(limit + (1 << 20)); err != nil {
		server.WriteError(w, http.StatusBadRequest, "bad_request", "invalid multipart upload", nil)
		return
	}
	part, header, err := r.FormFile("file")
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "bad_request", "missing file field", nil)
		return
	}
	defer part.Close()
	content, err := io.ReadAll(io.LimitReader(part, limit+1))
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "bad_request", "read upload failed", nil)
		return
	}
	if int64(len(content)) > limit {
		a.writeFileBrowserError(w, filebrowser.ErrFileTooLarge)
		return
	}
	targetPath := r.FormValue("path")
	if targetPath == "" && header != nil {
		targetPath = filepath.Base(header.Filename)
	}
	create := true
	if value := r.FormValue("create"); value != "" {
		create, _ = strconv.ParseBool(value)
	}
	overwrite := false
	if value := r.FormValue("overwrite"); value != "" {
		overwrite, _ = strconv.ParseBool(value)
	}
	wsid := r.FormValue("workspace_id")
	if wsid == "" {
		wsid = a.DefaultWSID
	}
	req := filebrowser.UploadRequest{WorkspaceID: wsid, RootID: r.FormValue("root_id"), Path: targetPath, Content: content, ETag: r.FormValue("etag"), Create: create, Overwrite: overwrite}
	result, err := svc.Upload(r.Context(), req)
	if err != nil {
		if errors.Is(err, filebrowser.ErrApprovalRequired) {
			submission, submitErr := a.App.SubmitFileBrowserUpload("admin", req)
			if submitErr != nil {
				a.writeFileBrowserError(w, submitErr)
				return
			}
			writeSubmission(w, submission)
			return
		}
		a.writeFileBrowserError(w, err)
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	server.WriteJSON(w, status, result)
}

func (a *API) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		server.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	svc, ok := a.fileBrowserService(w)
	if !ok {
		return
	}
	file, err := svc.DownloadFile(r.Context(), a.fileBrowserPathRequest(r))
	if err != nil {
		a.writeFileBrowserError(w, err)
		return
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, file.AbsPath)
}

func (a *API) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
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
	var req deleteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.WorkspaceID == "" {
		req.WorkspaceID = a.DefaultWSID
	}
	if req.Recursive && req.Confirmation != "delete "+req.Path {
		server.WriteError(w, http.StatusBadRequest, "confirmation_required", "recursive delete requires confirmation: delete "+req.Path, nil)
		return
	}
	result, err := svc.Delete(r.Context(), filebrowser.DeleteRequest{WorkspaceID: req.WorkspaceID, RootID: req.RootID, Path: req.Path, Recursive: req.Recursive})
	if err != nil {
		if errors.Is(err, filebrowser.ErrApprovalRequired) {
			submission, submitErr := a.App.SubmitFileBrowserDelete("admin", filebrowser.DeleteRequest{WorkspaceID: req.WorkspaceID, RootID: req.RootID, Path: req.Path, Recursive: req.Recursive})
			if submitErr != nil {
				a.writeFileBrowserError(w, submitErr)
				return
			}
			writeSubmission(w, submission)
			return
		}
		a.writeFileBrowserError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, result)
}

func (a *API) handleFileRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
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
	var req renameRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.WorkspaceID == "" {
		req.WorkspaceID = a.DefaultWSID
	}
	result, err := svc.Rename(r.Context(), filebrowser.RenameRequest{WorkspaceID: req.WorkspaceID, RootID: req.RootID, FromPath: req.FromPath, ToPath: req.ToPath})
	if err != nil {
		if errors.Is(err, filebrowser.ErrApprovalRequired) {
			submission, submitErr := a.App.SubmitFileBrowserRename("admin", filebrowser.RenameRequest{WorkspaceID: req.WorkspaceID, RootID: req.RootID, FromPath: req.FromPath, ToPath: req.ToPath})
			if submitErr != nil {
				a.writeFileBrowserError(w, submitErr)
				return
			}
			writeSubmission(w, submission)
			return
		}
		a.writeFileBrowserError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, result)
}

func (a *API) handleFileMkdir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
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
	var req mkdirRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.WorkspaceID == "" {
		req.WorkspaceID = a.DefaultWSID
	}
	result, err := svc.Mkdir(r.Context(), filebrowser.MkdirRequest{WorkspaceID: req.WorkspaceID, RootID: req.RootID, Path: req.Path})
	if err != nil {
		if errors.Is(err, filebrowser.ErrApprovalRequired) {
			submission, submitErr := a.App.SubmitFileBrowserMkdir("admin", filebrowser.MkdirRequest{WorkspaceID: req.WorkspaceID, RootID: req.RootID, Path: req.Path})
			if submitErr != nil {
				a.writeFileBrowserError(w, submitErr)
				return
			}
			writeSubmission(w, submission)
			return
		}
		a.writeFileBrowserError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusCreated, result)
}

func (a *API) fileBrowserPathRequest(r *http.Request) filebrowser.PathRequest {
	wsid := r.URL.Query().Get("workspace_id")
	if wsid == "" {
		wsid = a.DefaultWSID
	}
	return filebrowser.PathRequest{
		WorkspaceID: wsid,
		RootID:      r.URL.Query().Get("root_id"),
		Path:        r.URL.Query().Get("path"),
	}
}

func (a *API) fileBrowserService(w http.ResponseWriter) (*filebrowser.Service, bool) {
	if a == nil || a.App == nil || a.App.FileBrowserService == nil || !a.App.FileBrowserService.Enabled() {
		server.WriteError(w, http.StatusNotFound, "file_browser_disabled", "file browser is not enabled", nil)
		return nil, false
	}
	return a.App.FileBrowserService, true
}

func (a *API) writeFileBrowserError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, filebrowser.ErrDisabled):
		server.WriteError(w, http.StatusNotFound, "file_browser_disabled", "file browser is not enabled", nil)
	case errors.Is(err, filebrowser.ErrWorkspaceNotFound):
		server.WriteError(w, http.StatusBadRequest, "workspace_not_found", "workspace not found", nil)
	case errors.Is(err, filebrowser.ErrRootNotFound):
		server.WriteError(w, http.StatusBadRequest, "workspace_root_not_found", "workspace root not found", nil)
	case errors.Is(err, filebrowser.ErrFileNotFound):
		server.WriteError(w, http.StatusNotFound, "file_not_found", "file not found", nil)
	case errors.Is(err, filebrowser.ErrPathNotAllowed):
		server.WriteError(w, http.StatusBadRequest, "path_not_allowed", err.Error(), nil)
	case errors.Is(err, filebrowser.ErrPathExcluded):
		server.WriteError(w, http.StatusForbidden, "path_excluded", "path is excluded by file browser policy", nil)
	case errors.Is(err, filebrowser.ErrPathIsDirectory):
		server.WriteError(w, http.StatusBadRequest, "path_is_directory", "path is a directory", nil)
	case errors.Is(err, filebrowser.ErrNotDirectory):
		server.WriteError(w, http.StatusBadRequest, "path_not_directory", "path is not a directory", nil)
	case errors.Is(err, filebrowser.ErrFileTooLarge):
		server.WriteError(w, http.StatusRequestEntityTooLarge, "file_too_large", err.Error(), nil)
	case errors.Is(err, filebrowser.ErrBinaryFile):
		server.WriteError(w, http.StatusUnsupportedMediaType, "binary_file", "binary files cannot be opened as text", nil)
	case errors.Is(err, filebrowser.ErrReadOnly):
		server.WriteError(w, http.StatusForbidden, "file_browser_read_only", "file browser is read-only", nil)
	case errors.Is(err, filebrowser.ErrApprovalRequired):
		server.WriteError(w, http.StatusPreconditionRequired, "approval_required", "file browser write requires approval; approval-backed file writes are not enabled yet", nil)
	case errors.Is(err, filebrowser.ErrFileChanged):
		server.WriteError(w, http.StatusConflict, "file_changed", "file changed; reload before saving or set overwrite=true", nil)
	case errors.Is(err, filebrowser.ErrFileExists):
		server.WriteError(w, http.StatusConflict, "file_exists", "file already exists", nil)
	default:
		server.WriteError(w, http.StatusInternalServerError, "file_browser_failed", err.Error(), nil)
	}
}
