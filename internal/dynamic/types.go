package dynamic

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const Type = "dynamic"

const (
	StatusStaging        = "staging"
	StatusStarting       = "starting"
	StatusActive         = "active"
	StatusStopping       = "stopping"
	StatusStopped        = "stopped"
	StatusFailed         = "failed"
	StatusCrashed        = "crashed"
	StatusDeleting       = "deleting"
	StatusCleanupPending = "cleanup_pending"
)

const (
	DesiredRunning = "running"
	DesiredStopped = "stopped"
	DesiredDeleted = "deleted"
)

var (
	ErrDisabled          = errors.New("dynamic hosting is disabled")
	ErrNotFound          = errors.New("dynamic deployment not found")
	ErrAlreadyExists     = errors.New("dynamic deployment already exists")
	ErrExecutableDenied  = errors.New("dynamic deployment executable denied")
	ErrEnvironmentDenied = errors.New("dynamic deployment environment denied")
)

type RuntimeSpec struct {
	Argv []string          `json:"argv"`
	CWD  string            `json:"cwd,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
}

type DeployOptions struct {
	Overwrite        bool   `json:"overwrite,omitempty"`
	PreserveSource   bool   `json:"preserve_source"`
	Access           string `json:"access,omitempty"`
	HealthPath       string `json:"health_path,omitempty"`
	StartupTimeoutMS int    `json:"startup_timeout_ms,omitempty"`
	RestartPolicy    string `json:"restart_policy,omitempty"`
}

type DeployRequest struct {
	WorkspaceID string
	Name        string
	Source      string
	Runtime     RuntimeSpec
	Options     DeployOptions
}

type Deployment struct {
	Name              string            `json:"name"`
	Type              string            `json:"type"`
	WorkspaceID       string            `json:"workspace_id"`
	Access            string            `json:"access,omitempty"`
	ActiveRelease     string            `json:"active_release"`
	DesiredState      string            `json:"desired_state"`
	Status            string            `json:"status"`
	Health            string            `json:"health"`
	Argv              []string          `json:"argv"`
	Executable        string            `json:"executable,omitempty"`
	WorkingDir        string            `json:"working_dir"`
	Env               map[string]string `json:"env,omitempty"`
	HealthPath        string            `json:"health_path"`
	RestartPolicy     string            `json:"restart_policy"`
	RestartCount      int               `json:"restart_count"`
	CreatedAt         string            `json:"created_at"`
	UpdatedAt         string            `json:"updated_at"`
	StartedAt         string            `json:"started_at,omitempty"`
	LastExitAt        string            `json:"last_exit_at,omitempty"`
	LastError         string            `json:"last_error,omitempty"`
	LastWarning       string            `json:"last_warning,omitempty"`
	CleanupPending    bool              `json:"cleanup_pending,omitempty"`
	SourceCleanupPath string            `json:"source_cleanup_path,omitempty"`
	FileCount         int               `json:"file_count"`
	TotalBytes        int64             `json:"total_bytes"`
}

type View struct {
	Name           string   `json:"site"`
	Type           string   `json:"type"`
	WorkspaceID    string   `json:"workspace_id"`
	Access         string   `json:"access"`
	Hostname       string   `json:"hostname"`
	URL            string   `json:"url"`
	ReleaseID      string   `json:"release_id"`
	DesiredState   string   `json:"desired_state"`
	Status         string   `json:"status"`
	Health         string   `json:"health"`
	Argv           []string `json:"argv"`
	WorkingDir     string   `json:"working_dir"`
	HealthPath     string   `json:"health_path"`
	RestartPolicy  string   `json:"restart_policy"`
	RestartCount   int      `json:"restart_count"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
	StartedAt      string   `json:"started_at,omitempty"`
	LastExitAt     string   `json:"last_exit_at,omitempty"`
	LastError      string   `json:"last_error,omitempty"`
	LastWarning    string   `json:"last_warning,omitempty"`
	CleanupPending bool     `json:"cleanup_pending,omitempty"`
	FileCount      int      `json:"file_count"`
	TotalBytes     int64    `json:"total_bytes"`
	EnvKeys        []string `json:"env_keys,omitempty"`
}

type DeployResult struct {
	Deployment      View
	SourcePreserved bool
	WarningCode     string
	Warning         string
	CleanupPending  bool
}

type Release struct {
	ID         string `json:"release_id"`
	Active     bool   `json:"active"`
	CreatedAt  string `json:"created_at"`
	FileCount  int    `json:"file_count"`
	TotalBytes int64  `json:"total_bytes"`
}

type releaseMetadata struct {
	Name          string   `json:"name,omitempty"`
	Type          string   `json:"type,omitempty"`
	WorkspaceID   string   `json:"workspace_id,omitempty"`
	ReleaseID     string   `json:"release_id"`
	CreatedAt     string   `json:"created_at"`
	FileCount     int      `json:"file_count"`
	TotalBytes    int64    `json:"total_bytes"`
	Executable    string   `json:"executable,omitempty"`
	Argv          []string `json:"argv,omitempty"`
	WorkingDir    string   `json:"working_dir,omitempty"`
	HealthPath    string   `json:"health_path,omitempty"`
	RestartPolicy string   `json:"restart_policy,omitempty"`
}

type Logs struct {
	Name      string `json:"site"`
	Stream    string `json:"stream"`
	Data      string `json:"data"`
	Truncated bool   `json:"truncated"`
}

type Operation struct {
	ApprovalID      string
	TaskID          string
	Requester       string
	Tool            string
	Transport       string
	MCPSessionID    string
	SessionMode     string
	SessionIDSource string
	RequestID       string
}

type Event struct {
	Action       string
	Name         string
	WorkspaceID  string
	ReleaseID    string
	Executable   string
	Hostname     string
	HealthPath   string
	RestartCount int
	ExitCode     int
	Error        string
	Details      map[string]any
	Operation    Operation
	CreatedAt    time.Time
}

type EventSink func(Event)

type operationContextKey struct{}

func WithOperation(ctx context.Context, operation Operation) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, operationContextKey{}, operation)
}

func operationFromContext(ctx context.Context) Operation {
	if ctx == nil {
		return Operation{}
	}
	operation, _ := ctx.Value(operationContextKey{}).(Operation)
	return operation
}

type CodedError struct {
	Code string
	Err  error
}

func (e *CodedError) Error() string {
	if e == nil || e.Err == nil {
		return "dynamic deployment error"
	}
	return e.Err.Error()
}

func (e *CodedError) Unwrap() error { return e.Err }

func (e *CodedError) ErrorCode() string {
	if e == nil {
		return ""
	}
	return e.Code
}

func errorf(code, format string, args ...any) error {
	return &CodedError{Code: code, Err: fmt.Errorf(format, args...)}
}

func ErrorCode(err error) string {
	var coded *CodedError
	if errors.As(err, &coded) {
		return coded.Code
	}
	return ""
}

func publicView(d Deployment, hostname, publicURL string) View {
	envKeys := make([]string, 0, len(d.Env))
	for key := range d.Env {
		envKeys = append(envKeys, key)
	}
	sort.Strings(envKeys)
	access := normalizeAccess(d.Access, "public")
	return View{
		Name: d.Name, Type: Type, WorkspaceID: d.WorkspaceID, Access: access, Hostname: hostname,
		URL: publicURL, ReleaseID: d.ActiveRelease, DesiredState: d.DesiredState,
		Status: d.Status, Health: d.Health, Argv: append([]string(nil), d.Argv...),
		WorkingDir: d.WorkingDir, HealthPath: d.HealthPath, RestartPolicy: d.RestartPolicy,
		RestartCount: d.RestartCount, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
		StartedAt: d.StartedAt, LastExitAt: d.LastExitAt, LastError: d.LastError,
		LastWarning: d.LastWarning, CleanupPending: d.CleanupPending,
		FileCount: d.FileCount, TotalBytes: d.TotalBytes, EnvKeys: envKeys,
	}
}

func normalizeAccess(access, fallback string) string {
	access = strings.ToLower(strings.TrimSpace(access))
	if access == "public" || access == "private" {
		return access
	}
	fallback = strings.ToLower(strings.TrimSpace(fallback))
	if fallback == "private" {
		return "private"
	}
	return "public"
}

func nowString() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func normalizeHealthPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/", nil
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.Contains(path, "://") {
		return "", errorf("deployment_health_path_invalid", "health_path must be an absolute application path")
	}
	return path, nil
}
