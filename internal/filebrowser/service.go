package filebrowser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"localgate/internal/config"
	"localgate/internal/security"
)

var (
	ErrDisabled           = errors.New("file browser is disabled")
	ErrWorkspaceNotFound  = errors.New("workspace not found")
	ErrRootNotFound       = errors.New("workspace root not found")
	ErrPathNotAllowed     = errors.New("path not allowed")
	ErrPathExcluded       = errors.New("path excluded")
	ErrPathIsDirectory    = errors.New("path is directory")
	ErrNotDirectory       = errors.New("path is not a directory")
	ErrFileTooLarge       = errors.New("file too large")
	ErrBinaryFile         = errors.New("binary file")
	ErrFileNotFound       = errors.New("file not found")
	ErrReadOnly           = errors.New("file browser is read-only")
	ErrFileChanged        = errors.New("file changed")
	ErrFileExists         = errors.New("file already exists")
	ErrApprovalRequired   = errors.New("approval required")
	ErrUnsupportedPreview = errors.New("unsupported preview")
)

type Service struct {
	cfg        config.FileBrowserConfig
	workspaces map[string]config.Workspace
}

func NewService(cfg config.FileBrowserConfig, workspaces map[string]config.Workspace) *Service {
	copied := make(map[string]config.Workspace, len(workspaces))
	for id, ws := range workspaces {
		copied[id] = ws
	}
	return &Service{cfg: cfg, workspaces: copied}
}

func (s *Service) Enabled() bool {
	return s != nil && s.cfg.Enabled
}

func (s *Service) Workspaces() ([]WorkspaceItem, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	ids := make([]string, 0, len(s.workspaces))
	for id := range s.workspaces {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	items := make([]WorkspaceItem, 0, len(ids))
	for _, id := range ids {
		ws := s.workspaces[id]
		roots := make([]Root, 0, len(ws.Roots))
		for i, root := range ws.Roots {
			roots = append(roots, Root{ID: strconv.Itoa(i), Name: rootDisplayName(root, i)})
		}
		items = append(items, WorkspaceItem{ID: id, Roots: roots})
	}
	return items, nil
}

func (s *Service) List(ctx context.Context, req PathRequest) (ListResult, error) {
	resolved, err := s.resolve(req, true)
	if err != nil {
		return ListResult{}, err
	}
	entries, err := os.ReadDir(resolved.absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ListResult{}, ErrFileNotFound
		}
		if errors.Is(err, os.ErrPermission) {
			return ListResult{}, fmt.Errorf("%w: permission denied", ErrPathNotAllowed)
		}
		return ListResult{}, err
	}
	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return ListResult{}, ctx.Err()
		default:
		}
		name := entry.Name()
		rel := joinRel(resolved.relPath, name)
		hidden := isHiddenName(name)
		if hidden && !s.cfg.ShowHiddenFiles {
			continue
		}
		if s.excluded(rel) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		out = append(out, entryFromInfo(name, rel, info, hidden))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Type == "directory" && out[j].Type != "directory" {
			return true
		}
		if out[i].Type != "directory" && out[j].Type == "directory" {
			return false
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return ListResult{WorkspaceID: resolved.workspaceID, RootID: resolved.rootID, Path: filepath.ToSlash(resolved.relPath), Entries: out}, nil
}

func (s *Service) ReadContent(ctx context.Context, req PathRequest) (FileContent, error) {
	resolved, err := s.resolve(req, false)
	if err != nil {
		return FileContent{}, err
	}
	info, err := os.Stat(resolved.absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return FileContent{}, ErrFileNotFound
		}
		return FileContent{}, err
	}
	if info.IsDir() {
		return FileContent{}, ErrPathIsDirectory
	}
	if !info.Mode().IsRegular() {
		return FileContent{}, fmt.Errorf("%w: not a regular file", ErrPathNotAllowed)
	}
	if info.Size() > s.cfg.MaxFileBytes {
		return FileContent{}, fmt.Errorf("%w: %d > %d", ErrFileTooLarge, info.Size(), s.cfg.MaxFileBytes)
	}
	f, err := os.Open(resolved.absPath)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return FileContent{}, fmt.Errorf("%w: permission denied", ErrPathNotAllowed)
		}
		return FileContent{}, err
	}
	defer f.Close()
	limit := s.cfg.MaxFileBytes + 1
	b, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return FileContent{}, err
	}
	select {
	case <-ctx.Done():
		return FileContent{}, ctx.Err()
	default:
	}
	if int64(len(b)) > s.cfg.MaxFileBytes {
		return FileContent{}, ErrFileTooLarge
	}
	if looksBinary(b) {
		return FileContent{}, ErrBinaryFile
	}
	return FileContent{
		WorkspaceID: resolved.workspaceID,
		RootID:      resolved.rootID,
		Path:        filepath.ToSlash(resolved.relPath),
		Content:     string(b),
		Encoding:    "utf-8",
		Language:    languageForPath(resolved.relPath),
		ETag:        etag(b),
		Size:        info.Size(),
		ModTime:     info.ModTime().UTC(),
	}, nil
}

func (s *Service) DownloadFile(ctx context.Context, req PathRequest) (DownloadFile, error) {
	resolved, err := s.resolve(req, false)
	if err != nil {
		return DownloadFile{}, err
	}
	logical := filepath.Join(resolved.root, resolved.relPath)
	info, err := os.Lstat(logical)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DownloadFile{}, ErrFileNotFound
		}
		return DownloadFile{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return DownloadFile{}, fmt.Errorf("%w: symlink", ErrPathNotAllowed)
	}
	if info.IsDir() {
		return DownloadFile{}, ErrPathIsDirectory
	}
	if !info.Mode().IsRegular() {
		return DownloadFile{}, fmt.Errorf("%w: not a regular file", ErrPathNotAllowed)
	}
	select {
	case <-ctx.Done():
		return DownloadFile{}, ctx.Err()
	default:
	}
	return DownloadFile{WorkspaceID: resolved.workspaceID, RootID: resolved.rootID, Path: filepath.ToSlash(resolved.relPath), AbsPath: logical, Name: filepath.Base(logical), Size: info.Size(), ModTime: info.ModTime().UTC()}, nil
}

func (s *Service) PreviewImage(ctx context.Context, req PathRequest) (PreviewFile, error) {
	file, err := s.DownloadFile(ctx, req)
	if err != nil {
		return PreviewFile{}, err
	}
	if file.Size > s.cfg.MaxFileBytes {
		return PreviewFile{}, fmt.Errorf("%w: %d > %d", ErrFileTooLarge, file.Size, s.cfg.MaxFileBytes)
	}
	f, err := os.Open(file.AbsPath)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return PreviewFile{}, fmt.Errorf("%w: permission denied", ErrPathNotAllowed)
		}
		return PreviewFile{}, err
	}
	defer f.Close()
	header := make([]byte, 512)
	n, err := f.Read(header)
	if err != nil && !errors.Is(err, io.EOF) {
		return PreviewFile{}, err
	}
	contentType := http.DetectContentType(header[:n])
	switch contentType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return PreviewFile{DownloadFile: file, ContentType: contentType}, nil
	default:
		return PreviewFile{}, fmt.Errorf("%w: %s", ErrUnsupportedPreview, contentType)
	}
}

func (s *Service) SaveContent(ctx context.Context, req SaveContentRequest) (SaveContentResult, error) {
	return s.saveContent(ctx, req, false)
}

func (s *Service) SaveContentApproved(ctx context.Context, req SaveContentRequest) (SaveContentResult, error) {
	return s.saveContent(ctx, req, true)
}

func (s *Service) Upload(ctx context.Context, req UploadRequest) (UploadResult, error) {
	return s.upload(ctx, req, false)
}

func (s *Service) UploadApproved(ctx context.Context, req UploadRequest) (UploadResult, error) {
	return s.upload(ctx, req, true)
}

func (s *Service) Mkdir(ctx context.Context, req MkdirRequest) (MkdirResult, error) {
	return s.mkdir(ctx, req, false)
}

func (s *Service) MkdirApproved(ctx context.Context, req MkdirRequest) (MkdirResult, error) {
	return s.mkdir(ctx, req, true)
}

func (s *Service) mkdir(ctx context.Context, req MkdirRequest, approved bool) (MkdirResult, error) {
	if s == nil || !s.Enabled() {
		return MkdirResult{}, ErrDisabled
	}
	if s.cfg.ReadOnly {
		return MkdirResult{}, ErrReadOnly
	}
	if s.cfg.RequireApprovalForWrite && !approved {
		return MkdirResult{}, ErrApprovalRequired
	}
	resolved, err := s.resolveForWrite(PathRequest{WorkspaceID: req.WorkspaceID, RootID: req.RootID, Path: req.Path})
	if err != nil {
		return MkdirResult{}, err
	}
	select {
	case <-ctx.Done():
		return MkdirResult{}, ctx.Err()
	default:
	}
	if info, err := os.Lstat(resolved.absPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return MkdirResult{}, fmt.Errorf("%w: symlink", ErrPathNotAllowed)
		}
		return MkdirResult{}, ErrFileExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return MkdirResult{}, err
	}
	if err := os.Mkdir(resolved.absPath, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return MkdirResult{}, ErrFileExists
		}
		return MkdirResult{}, err
	}
	info, err := os.Stat(resolved.absPath)
	if err != nil {
		return MkdirResult{}, err
	}
	return MkdirResult{WorkspaceID: resolved.workspaceID, RootID: resolved.rootID, Path: filepath.ToSlash(resolved.relPath), Status: "created", ModTime: info.ModTime().UTC()}, nil
}

func (s *Service) Delete(ctx context.Context, req DeleteRequest) (DeleteResult, error) {
	return s.delete(ctx, req, false)
}

func (s *Service) DeleteApproved(ctx context.Context, req DeleteRequest) (DeleteResult, error) {
	return s.delete(ctx, req, true)
}

func (s *Service) delete(ctx context.Context, req DeleteRequest, approved bool) (DeleteResult, error) {
	if s == nil || !s.Enabled() {
		return DeleteResult{}, ErrDisabled
	}
	if s.cfg.ReadOnly {
		return DeleteResult{}, ErrReadOnly
	}
	if s.cfg.RequireApprovalForDelete && !approved {
		return DeleteResult{}, ErrApprovalRequired
	}
	resolved, err := s.resolveForWrite(PathRequest{WorkspaceID: req.WorkspaceID, RootID: req.RootID, Path: req.Path})
	if err != nil {
		return DeleteResult{}, err
	}
	logical := filepath.Join(resolved.root, resolved.relPath)
	info, err := os.Lstat(logical)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DeleteResult{}, ErrFileNotFound
		}
		return DeleteResult{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return DeleteResult{}, fmt.Errorf("%w: symlink", ErrPathNotAllowed)
	}
	typeName := "file"
	if info.IsDir() {
		typeName = "directory"
		if !req.Recursive {
			return DeleteResult{}, ErrPathIsDirectory
		}
	} else if !info.Mode().IsRegular() {
		typeName = "other"
	}
	select {
	case <-ctx.Done():
		return DeleteResult{}, ctx.Err()
	default:
	}
	if info.IsDir() {
		if err := os.RemoveAll(logical); err != nil {
			return DeleteResult{}, err
		}
	} else {
		if err := os.Remove(logical); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return DeleteResult{}, ErrFileNotFound
			}
			return DeleteResult{}, err
		}
	}
	return DeleteResult{WorkspaceID: resolved.workspaceID, RootID: resolved.rootID, Path: filepath.ToSlash(resolved.relPath), Type: typeName, Status: "deleted", Recursive: req.Recursive, DeletedAt: time.Now().UTC()}, nil
}

func (s *Service) Rename(ctx context.Context, req RenameRequest) (RenameResult, error) {
	return s.rename(ctx, req, false)
}

func (s *Service) RenameApproved(ctx context.Context, req RenameRequest) (RenameResult, error) {
	return s.rename(ctx, req, true)
}

func (s *Service) rename(ctx context.Context, req RenameRequest, approved bool) (RenameResult, error) {
	if s == nil || !s.Enabled() {
		return RenameResult{}, ErrDisabled
	}
	if s.cfg.ReadOnly {
		return RenameResult{}, ErrReadOnly
	}
	if s.cfg.RequireApprovalForWrite && !approved {
		return RenameResult{}, ErrApprovalRequired
	}
	from, err := s.resolveForWrite(PathRequest{WorkspaceID: req.WorkspaceID, RootID: req.RootID, Path: req.FromPath})
	if err != nil {
		return RenameResult{}, err
	}
	to, err := s.resolveForWrite(PathRequest{WorkspaceID: req.WorkspaceID, RootID: req.RootID, Path: req.ToPath})
	if err != nil {
		return RenameResult{}, err
	}
	if from.workspaceID != to.workspaceID || from.rootID != to.rootID {
		return RenameResult{}, ErrRootNotFound
	}
	if from.relPath == to.relPath {
		return RenameResult{}, fmt.Errorf("%w: source and destination are identical", ErrFileExists)
	}
	fromLogical := filepath.Join(from.root, from.relPath)
	toLogical := filepath.Join(to.root, to.relPath)
	info, err := os.Lstat(fromLogical)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RenameResult{}, ErrFileNotFound
		}
		return RenameResult{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return RenameResult{}, fmt.Errorf("%w: source symlink", ErrPathNotAllowed)
	}
	if _, err := os.Lstat(toLogical); err == nil {
		return RenameResult{}, ErrFileExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return RenameResult{}, err
	}
	if info.IsDir() {
		fromClean := filepath.Clean(fromLogical)
		toClean := filepath.Clean(toLogical)
		if strings.HasPrefix(toClean+string(os.PathSeparator), fromClean+string(os.PathSeparator)) {
			return RenameResult{}, fmt.Errorf("%w: cannot move directory into itself", ErrPathNotAllowed)
		}
	}
	select {
	case <-ctx.Done():
		return RenameResult{}, ctx.Err()
	default:
	}
	if err := os.Rename(fromLogical, toLogical); err != nil {
		if errors.Is(err, os.ErrExist) {
			return RenameResult{}, ErrFileExists
		}
		if errors.Is(err, os.ErrNotExist) {
			return RenameResult{}, ErrFileNotFound
		}
		return RenameResult{}, err
	}
	newInfo, err := os.Stat(toLogical)
	if err != nil {
		return RenameResult{}, err
	}
	typeName := "file"
	if newInfo.IsDir() {
		typeName = "directory"
	} else if !newInfo.Mode().IsRegular() {
		typeName = "other"
	}
	return RenameResult{WorkspaceID: from.workspaceID, RootID: from.rootID, FromPath: filepath.ToSlash(from.relPath), ToPath: filepath.ToSlash(to.relPath), Type: typeName, Status: "renamed", ModTime: newInfo.ModTime().UTC()}, nil
}

func (s *Service) saveContent(ctx context.Context, req SaveContentRequest, approved bool) (SaveContentResult, error) {
	content := []byte(req.Content)
	if looksBinary(content) {
		return SaveContentResult{}, ErrBinaryFile
	}
	if int64(len(content)) > s.cfg.MaxFileBytes {
		return SaveContentResult{}, fmt.Errorf("%w: %d > %d", ErrFileTooLarge, len(content), s.cfg.MaxFileBytes)
	}
	return s.writeBytes(ctx, writeBytesRequest{WorkspaceID: req.WorkspaceID, RootID: req.RootID, Path: req.Path, Content: content, ETag: req.ETag, Create: req.Create, Overwrite: req.Overwrite, LimitBytes: s.cfg.MaxFileBytes, Approved: approved})
}

func (s *Service) upload(ctx context.Context, req UploadRequest, approved bool) (UploadResult, error) {
	return s.writeBytes(ctx, writeBytesRequest{WorkspaceID: req.WorkspaceID, RootID: req.RootID, Path: req.Path, Content: req.Content, ETag: req.ETag, Create: req.Create, Overwrite: req.Overwrite, LimitBytes: s.cfg.MaxUploadBytes, Approved: approved})
}

type writeBytesRequest struct {
	WorkspaceID string
	RootID      string
	Path        string
	Content     []byte
	ETag        string
	Create      bool
	Overwrite   bool
	LimitBytes  int64
	Approved    bool
}

func (s *Service) writeBytes(ctx context.Context, req writeBytesRequest) (SaveContentResult, error) {
	if s == nil || !s.Enabled() {
		return SaveContentResult{}, ErrDisabled
	}
	if s.cfg.ReadOnly {
		return SaveContentResult{}, ErrReadOnly
	}
	if s.cfg.RequireApprovalForWrite && !req.Approved {
		return SaveContentResult{}, ErrApprovalRequired
	}
	content := req.Content
	if int64(len(content)) > req.LimitBytes {
		return SaveContentResult{}, fmt.Errorf("%w: %d > %d", ErrFileTooLarge, len(content), req.LimitBytes)
	}
	resolved, err := s.resolveForWrite(PathRequest{WorkspaceID: req.WorkspaceID, RootID: req.RootID, Path: req.Path})
	if err != nil {
		return SaveContentResult{}, err
	}
	select {
	case <-ctx.Done():
		return SaveContentResult{}, ctx.Err()
	default:
	}

	created := false
	overwrote := false
	if info, err := os.Lstat(resolved.absPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return SaveContentResult{}, fmt.Errorf("%w: symlink", ErrPathNotAllowed)
		}
		if info.IsDir() {
			return SaveContentResult{}, ErrPathIsDirectory
		}
		if !info.Mode().IsRegular() {
			return SaveContentResult{}, fmt.Errorf("%w: not a regular file", ErrPathNotAllowed)
		}
		if req.Create {
			return SaveContentResult{}, ErrFileExists
		}
		if info.Size() > req.LimitBytes {
			return SaveContentResult{}, fmt.Errorf("%w: %d > %d", ErrFileTooLarge, info.Size(), req.LimitBytes)
		}
		current, err := os.ReadFile(resolved.absPath)
		if err != nil {
			return SaveContentResult{}, err
		}
		if !req.Overwrite {
			if strings.TrimSpace(req.ETag) == "" {
				return SaveContentResult{}, ErrFileChanged
			}
			if !strings.EqualFold(strings.TrimSpace(req.ETag), etag(current)) {
				return SaveContentResult{}, ErrFileChanged
			}
		}
		overwrote = true
	} else if errors.Is(err, os.ErrNotExist) {
		if !req.Create {
			return SaveContentResult{}, ErrFileNotFound
		}
		created = true
	} else {
		return SaveContentResult{}, err
	}

	if err := os.MkdirAll(filepath.Dir(resolved.absPath), 0o755); err != nil {
		return SaveContentResult{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(resolved.absPath), ".filebrowser-*")
	if err != nil {
		return SaveContentResult{}, err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return SaveContentResult{}, err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return SaveContentResult{}, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return SaveContentResult{}, err
	}
	if err := tmp.Close(); err != nil {
		return SaveContentResult{}, err
	}
	if err := os.Rename(tmpPath, resolved.absPath); err != nil {
		return SaveContentResult{}, err
	}
	info, err := os.Stat(resolved.absPath)
	if err != nil {
		return SaveContentResult{}, err
	}
	status := "saved"
	if created {
		status = "created"
	} else if overwrote {
		status = "overwritten"
	}
	return SaveContentResult{WorkspaceID: resolved.workspaceID, RootID: resolved.rootID, Path: filepath.ToSlash(resolved.relPath), Status: status, Bytes: len(content), ETag: etag(content), Size: info.Size(), ModTime: info.ModTime().UTC(), Created: created, Overwritten: overwrote}, nil
}

type resolvedPath struct {
	workspaceID string
	rootID      string
	root        string
	relPath     string
	absPath     string
}

func (s *Service) resolve(req PathRequest, requireDirectory bool) (resolvedPath, error) {
	if !s.Enabled() {
		return resolvedPath{}, ErrDisabled
	}
	workspaceID := strings.TrimSpace(req.WorkspaceID)
	if workspaceID == "" {
		return resolvedPath{}, ErrWorkspaceNotFound
	}
	ws, ok := s.workspaces[workspaceID]
	if !ok {
		return resolvedPath{}, ErrWorkspaceNotFound
	}
	rootID := strings.TrimSpace(req.RootID)
	if rootID == "" {
		rootID = "0"
	}
	rootIndex, err := strconv.Atoi(rootID)
	if err != nil || rootIndex < 0 || rootIndex >= len(ws.Roots) {
		return resolvedPath{}, ErrRootNotFound
	}
	root := filepath.Clean(ws.Roots[rootIndex])
	rel, err := cleanRelative(req.Path)
	if err != nil {
		return resolvedPath{}, fmt.Errorf("%w: %v", ErrPathNotAllowed, err)
	}
	if s.excluded(rel) || (!s.cfg.ShowHiddenFiles && hasHiddenComponent(rel)) {
		return resolvedPath{}, ErrPathExcluded
	}
	absPath := root
	if rel != "" {
		absPath = filepath.Join(root, rel)
	}
	dec := security.ValidatePath(absPath, config.Workspace{ID: ws.ID, Roots: []string{root}, PathDenylist: ws.PathDenylist}, security.Options{FollowSymlink: true, AllowNonExist: false})
	if !dec.Allowed {
		return resolvedPath{}, fmt.Errorf("%w: %s", ErrPathNotAllowed, dec.Reason)
	}
	info, err := os.Stat(dec.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return resolvedPath{}, ErrFileNotFound
		}
		return resolvedPath{}, err
	}
	if requireDirectory && !info.IsDir() {
		return resolvedPath{}, ErrNotDirectory
	}
	return resolvedPath{workspaceID: workspaceID, rootID: rootID, root: root, relPath: rel, absPath: dec.Path}, nil
}

func (s *Service) resolveForWrite(req PathRequest) (resolvedPath, error) {
	if !s.Enabled() {
		return resolvedPath{}, ErrDisabled
	}
	workspaceID := strings.TrimSpace(req.WorkspaceID)
	if workspaceID == "" {
		return resolvedPath{}, ErrWorkspaceNotFound
	}
	ws, ok := s.workspaces[workspaceID]
	if !ok {
		return resolvedPath{}, ErrWorkspaceNotFound
	}
	rootID := strings.TrimSpace(req.RootID)
	if rootID == "" {
		rootID = "0"
	}
	rootIndex, err := strconv.Atoi(rootID)
	if err != nil || rootIndex < 0 || rootIndex >= len(ws.Roots) {
		return resolvedPath{}, ErrRootNotFound
	}
	root := filepath.Clean(ws.Roots[rootIndex])
	rel, err := cleanRelative(req.Path)
	if err != nil {
		return resolvedPath{}, fmt.Errorf("%w: %v", ErrPathNotAllowed, err)
	}
	if rel == "" {
		return resolvedPath{}, fmt.Errorf("%w: path is required", ErrPathNotAllowed)
	}
	if s.excluded(rel) || (!s.cfg.ShowHiddenFiles && hasHiddenComponent(rel)) {
		return resolvedPath{}, ErrPathExcluded
	}
	absPath := filepath.Join(root, rel)
	dec := security.ValidatePath(absPath, config.Workspace{ID: ws.ID, Roots: []string{root}, PathDenylist: ws.PathDenylist}, security.Options{FollowSymlink: true, AllowNonExist: true})
	if !dec.Allowed {
		return resolvedPath{}, fmt.Errorf("%w: %s", ErrPathNotAllowed, dec.Reason)
	}
	parent := filepath.Dir(dec.Path)
	if _, err := os.Stat(parent); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return resolvedPath{}, ErrFileNotFound
		}
		return resolvedPath{}, err
	}
	parentDec := security.ValidatePath(parent, config.Workspace{ID: ws.ID, Roots: []string{root}, PathDenylist: ws.PathDenylist}, security.Options{FollowSymlink: true, AllowNonExist: false})
	if !parentDec.Allowed {
		return resolvedPath{}, fmt.Errorf("%w: parent %s", ErrPathNotAllowed, parentDec.Reason)
	}
	if info, err := os.Stat(parentDec.Path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return resolvedPath{}, ErrFileNotFound
		}
		return resolvedPath{}, err
	} else if !info.IsDir() {
		return resolvedPath{}, ErrNotDirectory
	}
	return resolvedPath{workspaceID: workspaceID, rootID: rootID, root: root, relPath: rel, absPath: dec.Path}, nil
}

func cleanRelative(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "." {
		return "", nil
	}
	value = filepath.FromSlash(value)
	if filepath.IsAbs(value) {
		return "", errors.New("absolute paths are not allowed")
	}
	clean := filepath.Clean(value)
	if clean == "." {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", errors.New("path escapes root")
	}
	return clean, nil
}

func joinRel(base, name string) string {
	if base == "" {
		return filepath.ToSlash(name)
	}
	return filepath.ToSlash(filepath.Join(base, name))
}

func (s *Service) excluded(rel string) bool {
	if rel == "" {
		return false
	}
	components := strings.Split(filepath.ToSlash(rel), "/")
	for _, candidate := range s.cfg.Exclude {
		candidate = strings.Trim(strings.TrimSpace(filepath.ToSlash(candidate)), "/")
		if candidate == "" {
			continue
		}
		for _, component := range components {
			if component == candidate {
				return true
			}
		}
	}
	return false
}

func hasHiddenComponent(rel string) bool {
	if rel == "" {
		return false
	}
	for _, component := range strings.Split(filepath.ToSlash(rel), "/") {
		if isHiddenName(component) {
			return true
		}
	}
	return false
}

func isHiddenName(name string) bool {
	return strings.HasPrefix(name, ".") && name != "." && name != ".."
}

func entryFromInfo(name, rel string, info fs.FileInfo, hidden bool) Entry {
	typeName := "other"
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		typeName = "symlink"
	case info.IsDir():
		typeName = "directory"
	case info.Mode().IsRegular():
		typeName = "file"
	}
	return Entry{
		Name:     name,
		Path:     filepath.ToSlash(rel),
		Type:     typeName,
		Size:     info.Size(),
		ModTime:  info.ModTime().UTC(),
		Hidden:   hidden,
		Readable: true,
		Writable: true,
	}
}

func looksBinary(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	if strings.Contains(string(b), "\x00") {
		return true
	}
	return !utf8.Valid(b)
}

func etag(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

func languageForPath(p string) string {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".go":
		return "go"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".json":
		return "json"
	case ".md", ".markdown":
		return "markdown"
	case ".yaml", ".yml":
		return "yaml"
	case ".html", ".htm":
		return "html"
	case ".css":
		return "css"
	case ".py":
		return "python"
	case ".sh", ".bash":
		return "shell"
	default:
		return "plaintext"
	}
}

func rootDisplayName(root string, index int) string {
	name := filepath.Base(filepath.Clean(root))
	if strings.TrimSpace(name) == "" || name == string(os.PathSeparator) {
		return "root " + strconv.Itoa(index)
	}
	return name
}
