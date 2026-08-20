package filebrowser

import "time"

type DirectoryContext struct {
	Path    string `json:"path"`
	AbsPath string `json:"-"`
}

type Entry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Type     string    `json:"type"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"mod_time"`
	ETag     string    `json:"etag,omitempty"`
	Hidden   bool      `json:"hidden"`
	Readable bool      `json:"readable"`
	Writable bool      `json:"writable"`
}

type ListResult struct {
	Path    string  `json:"path"`
	Entries []Entry `json:"entries"`
}

type FileContent struct {
	Path        string    `json:"path"`
	Content     string    `json:"content"`
	Encoding    string    `json:"encoding"`
	Language    string    `json:"language"`
	ETag        string    `json:"etag"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"mod_time"`
}

type SaveContentRequest struct {
	Path        string
	Content     string
	ETag        string
	Create      bool
	Overwrite   bool
}

type UploadRequest struct {
	Path        string
	Content     []byte
	ETag        string
	Create      bool
	Overwrite   bool
}

type SaveContentResult struct {
	Path        string    `json:"path"`
	Status      string    `json:"status"`
	Bytes       int       `json:"bytes"`
	ETag        string    `json:"etag"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"mod_time"`
	Created     bool      `json:"created"`
	Overwritten bool      `json:"overwritten"`
}

type UploadResult = SaveContentResult

type MkdirRequest struct {
	Path        string
}

type MkdirResult struct {
	Path        string    `json:"path"`
	Status      string    `json:"status"`
	ModTime     time.Time `json:"mod_time"`
}

type RenameRequest struct {
	FromPath    string
	ToPath      string
}

type RenameResult struct {
	FromPath    string    `json:"from_path"`
	ToPath      string    `json:"to_path"`
	Type        string    `json:"type"`
	Status      string    `json:"status"`
	ModTime     time.Time `json:"mod_time"`
}

type DeleteRequest struct {
	Path        string
	Recursive   bool
}

type DeleteResult struct {
	Path        string    `json:"path"`
	Type        string    `json:"type"`
	Status      string    `json:"status"`
	Recursive   bool      `json:"recursive"`
	DeletedAt   time.Time `json:"deleted_at"`
}

type DownloadFile struct {
	Path        string
	AbsPath     string
	Name        string
	Size        int64
	ModTime     time.Time
}

type PreviewFile struct {
	DownloadFile
	ContentType string
}

type PathRequest struct {
	Path        string
}

type GitStatusEntry struct {
	Path           string `json:"path"`
	OriginalPath   string `json:"original_path,omitempty"`
	IndexStatus    string `json:"index_status"`
	WorktreeStatus string `json:"worktree_status"`
	Kind           string `json:"kind"`
}

type GitStatusResult struct {
	Path           string           `json:"path"`
	Repository     bool             `json:"repository"`
	RepositoryPath string           `json:"repository_path,omitempty"`
	OID            string           `json:"oid,omitempty"`
	Branch         string           `json:"branch,omitempty"`
	Detached       bool             `json:"detached"`
	Upstream       string           `json:"upstream,omitempty"`
	Ahead          int              `json:"ahead"`
	Behind         int              `json:"behind"`
	Entries        []GitStatusEntry `json:"entries"`
	Truncated      bool             `json:"truncated"`
}

type GitDiffResult struct {
	Path           string `json:"path"`
	RepositoryPath string `json:"repository_path"`
	Staged         bool   `json:"staged"`
	Diff           string `json:"diff"`
	Bytes          int    `json:"bytes"`
}
