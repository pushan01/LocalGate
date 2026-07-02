package dynamic

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"localgate/internal/config"
	"localgate/internal/security"
)

type Service struct {
	root       string
	config     config.DynamicHosting
	workspaces map[string]config.Workspace
	store      *registryStore
	owner      bool

	opMu             sync.Mutex
	mu               sync.RWMutex
	deployments      map[string]Deployment
	processes        map[string]*managedProcess
	orphans          map[int]*managedProcess
	closed           chan struct{}
	closeOnce        sync.Once
	lastRuntimeError error
	proxySlots       chan struct{}
	proxyTransport   *http.Transport
	eventSink        EventSink
}

func NewService(root string, cfg config.DynamicHosting, workspaces map[string]config.Workspace, ownsRuntime bool) (*Service, error) {
	cfg = normalizeConfig(cfg)
	if !filepath.IsAbs(root) {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		root = absolute
	}
	root = filepath.Clean(root)
	store := newRegistryStore(filepath.Join(root, "deployments.json"))
	loaded, err := store.load()
	if err != nil {
		if cfg.Enabled {
			return nil, fmt.Errorf("load dynamic registry: %w", err)
		}
		loaded = &registry{Deployments: map[string]Deployment{}}
	}
	service := &Service{
		root:        root,
		config:      cfg,
		workspaces:  cloneWorkspaces(workspaces),
		store:       store,
		owner:       ownsRuntime,
		deployments: loaded.Deployments,
		processes:   map[string]*managedProcess{},
		orphans:     map[int]*managedProcess{},
		proxySlots:  make(chan struct{}, cfg.MaxConcurrentRequests),
		proxyTransport: &http.Transport{
			Proxy:                  http.ProxyFromEnvironment,
			DialContext:            (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:      false,
			ResponseHeaderTimeout:  time.Duration(cfg.UpstreamHeaderTimeoutSeconds) * time.Second,
			MaxResponseHeaderBytes: cfg.MaxResponseHeaderBytes,
		},
		closed: make(chan struct{}),
	}
	if service.deployments == nil {
		service.deployments = map[string]Deployment{}
	}
	if cfg.Enabled && ownsRuntime {
		service.cleanupStorage()
		go service.recoverAll()
	}
	return service, nil
}

func normalizeConfig(cfg config.DynamicHosting) config.DynamicHosting {
	defaults := config.Default().Deployments.Dynamic
	if len(cfg.ReservedNames) == 0 {
		cfg.ReservedNames = append([]string(nil), defaults.ReservedNames...)
	}
	if cfg.MaxApps <= 0 {
		cfg.MaxApps = defaults.MaxApps
	}
	if cfg.MaxFiles <= 0 {
		cfg.MaxFiles = defaults.MaxFiles
	}
	if cfg.MaxTotalBytes <= 0 {
		cfg.MaxTotalBytes = defaults.MaxTotalBytes
	}
	if cfg.MaxFileBytes <= 0 {
		cfg.MaxFileBytes = defaults.MaxFileBytes
	}
	if cfg.StartupTimeoutSeconds <= 0 {
		cfg.StartupTimeoutSeconds = defaults.StartupTimeoutSeconds
	}
	if cfg.ShutdownTimeoutSeconds <= 0 {
		cfg.ShutdownTimeoutSeconds = defaults.ShutdownTimeoutSeconds
	}
	if cfg.RestartPolicy == "" {
		cfg.RestartPolicy = defaults.RestartPolicy
	}
	if cfg.RestartMaxAttempts < 0 {
		cfg.RestartMaxAttempts = defaults.RestartMaxAttempts
	}
	if cfg.RestartBackoffSeconds < 0 {
		cfg.RestartBackoffSeconds = defaults.RestartBackoffSeconds
	}
	if cfg.RestartResetSeconds <= 0 {
		cfg.RestartResetSeconds = defaults.RestartResetSeconds
	}
	if cfg.MaxLogBytes <= 0 {
		cfg.MaxLogBytes = defaults.MaxLogBytes
	}
	if cfg.MaxLogFiles <= 0 {
		cfg.MaxLogFiles = defaults.MaxLogFiles
	}
	if cfg.ReleaseRetention <= 0 {
		cfg.ReleaseRetention = defaults.ReleaseRetention
	}
	if cfg.MaxRequestBodyBytes <= 0 {
		cfg.MaxRequestBodyBytes = defaults.MaxRequestBodyBytes
	}
	if cfg.MaxRequestHeaderBytes <= 0 {
		cfg.MaxRequestHeaderBytes = defaults.MaxRequestHeaderBytes
	}
	if cfg.MaxConcurrentRequests <= 0 {
		cfg.MaxConcurrentRequests = defaults.MaxConcurrentRequests
	}
	if cfg.UpstreamHeaderTimeoutSeconds <= 0 {
		cfg.UpstreamHeaderTimeoutSeconds = defaults.UpstreamHeaderTimeoutSeconds
	}
	if cfg.MaxResponseHeaderBytes <= 0 {
		cfg.MaxResponseHeaderBytes = defaults.MaxResponseHeaderBytes
	}
	cfg.PublicBaseURL = strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/")
	cfg.PrivateBaseURL = strings.TrimRight(strings.TrimSpace(cfg.PrivateBaseURL), "/")
	if cfg.DefaultAccess == "" {
		cfg.DefaultAccess = "private"
	}
	return cfg
}

func cloneWorkspaces(input map[string]config.Workspace) map[string]config.Workspace {
	out := make(map[string]config.Workspace, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func (s *Service) Enabled() bool { return s != nil && s.config.Enabled }

func (s *Service) Config() config.DynamicHosting {
	if s == nil {
		return config.DynamicHosting{}
	}
	return s.config
}

func (s *Service) Deploy(ctx context.Context, request DeployRequest) (DeployResult, error) {
	if s == nil || !s.config.Enabled {
		return DeployResult{}, &CodedError{Code: "dynamic_hosting_disabled", Err: ErrDisabled}
	}
	if !s.owner {
		return DeployResult{}, errorf("dynamic_runtime_not_owned", "this LocalGate process does not own the dynamic runtime")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()

	workspace, ok := s.workspaces[request.WorkspaceID]
	if !ok {
		return DeployResult{}, errorf("deployment_source_invalid", "workspace not found")
	}
	if err := s.validateName(request.Name); err != nil {
		return DeployResult{}, err
	}
	decision := security.ValidatePath(request.Source, workspace, security.Options{FollowSymlink: true})
	if !decision.Allowed {
		return DeployResult{}, errorf("deployment_source_invalid", "source is not allowed: %s", decision.Reason)
	}
	source := decision.Path
	if pathsOverlap(source, s.root) {
		return DeployResult{}, errorf("deployment_source_invalid", "source overlaps managed dynamic storage")
	}
	info, err := os.Stat(source)
	if err != nil || !info.IsDir() {
		return DeployResult{}, errorf("deployment_source_invalid", "source must be an existing directory")
	}
	if !request.Options.PreserveSource && filepath.Clean(source) == filepath.Clean(decision.Root) {
		return DeployResult{}, errorf("deployment_source_invalid", "source must be a strict child of the workspace root when preserve_source is false")
	}

	s.mu.RLock()
	previous, exists := s.deployments[request.Name]
	count := len(s.deployments)
	s.mu.RUnlock()
	if exists && !request.Options.Overwrite {
		return DeployResult{}, &CodedError{Code: "deployment_already_exists", Err: ErrAlreadyExists}
	}
	if !exists && count >= s.config.MaxApps {
		return DeployResult{}, errorf("deployment_limit_exceeded", "dynamic deployment count exceeds %d", s.config.MaxApps)
	}

	executable, err := s.resolveExecutable(request.Runtime.Argv)
	if err != nil {
		return DeployResult{}, err
	}
	environment, err := s.validateEnvironment(request.Runtime.Env)
	if err != nil {
		return DeployResult{}, err
	}
	healthPath, err := normalizeHealthPath(request.Options.HealthPath)
	if err != nil {
		return DeployResult{}, err
	}
	workingDir, err := normalizeWorkingDir(request.Runtime.CWD)
	if err != nil {
		return DeployResult{}, err
	}
	restartPolicy := strings.TrimSpace(request.Options.RestartPolicy)
	if restartPolicy == "" {
		restartPolicy = s.config.RestartPolicy
	}
	if restartPolicy != "never" && restartPolicy != "on-failure" {
		return DeployResult{}, errorf("deployment_restart_policy_invalid", "restart_policy must be never or on-failure")
	}

	releaseID, err := newReleaseID()
	if err != nil {
		return DeployResult{}, errorf("deployment_snapshot_failed", "create release id: %v", err)
	}
	provisional := Deployment{Name: request.Name, Type: Type, WorkspaceID: request.WorkspaceID, ActiveRelease: releaseID, Executable: executable, HealthPath: healthPath}
	s.emit(ctx, "deployment_staging_started", provisional, nil, nil)
	releasesRoot := filepath.Join(s.root, "apps", request.Name, "releases")
	if err := os.MkdirAll(releasesRoot, 0o700); err != nil {
		return DeployResult{}, errorf("deployment_snapshot_failed", "%v", err)
	}
	stage, err := os.MkdirTemp(releasesRoot, ".stage-")
	if err != nil {
		return DeployResult{}, errorf("deployment_snapshot_failed", "%v", err)
	}
	defer os.RemoveAll(stage)
	stageSource := filepath.Join(stage, "source")
	if err := os.MkdirAll(stageSource, 0o700); err != nil {
		return DeployResult{}, err
	}
	stats, err := copySnapshot(ctx, source, stageSource, s.config.MaxFiles, s.config.MaxTotalBytes, s.config.MaxFileBytes)
	if err != nil {
		return DeployResult{}, err
	}
	finalRelease := filepath.Join(releasesRoot, releaseID)
	if err := os.Rename(stage, finalRelease); err != nil {
		return DeployResult{}, errorf("deployment_snapshot_failed", "activate release snapshot: %v", err)
	}
	rollbackRelease := true
	defer func() {
		if rollbackRelease {
			_ = os.RemoveAll(finalRelease)
		}
	}()

	sourceStash := ""
	restoreSource := func() {
		if sourceStash != "" {
			_ = os.Rename(sourceStash, source)
			sourceStash = ""
		}
	}
	if !request.Options.PreserveSource {
		current, statErr := os.Stat(source)
		if statErr != nil || !os.SameFile(info, current) {
			return DeployResult{}, errorf("deployment_source_invalid", "source changed during deployment")
		}
		sourceStash = filepath.Join(filepath.Dir(source), fmt.Sprintf(".localgate-source-%s-%d", filepath.Base(source), time.Now().UnixNano()))
		if err := os.Rename(source, sourceStash); err != nil {
			return DeployResult{}, errorf("deployment_source_invalid", "stage source removal: %v", err)
		}
		defer restoreSource()
	}

	now := nowString()
	createdAt := now
	if exists && previous.CreatedAt != "" {
		createdAt = previous.CreatedAt
	}
	deployment := Deployment{
		Name: request.Name, Type: Type, WorkspaceID: request.WorkspaceID,
		Access:        normalizeAccess(request.Options.Access, s.config.DefaultAccess),
		ActiveRelease: releaseID, DesiredState: DesiredRunning, Status: StatusStarting, Health: "checking",
		Argv: append([]string(nil), request.Runtime.Argv...), Executable: executable, WorkingDir: workingDir, Env: environment,
		HealthPath: healthPath, RestartPolicy: restartPolicy, CreatedAt: createdAt, UpdatedAt: now,
		FileCount: stats.FileCount, TotalBytes: stats.TotalBytes,
	}
	if err := writeReleaseMetadata(finalRelease, deployment); err != nil {
		return DeployResult{}, errorf("deployment_snapshot_failed", "write release metadata: %v", err)
	}
	startupTimeout := time.Duration(s.config.StartupTimeoutSeconds) * time.Second
	if request.Options.StartupTimeoutMS > 0 {
		startupTimeout = time.Duration(request.Options.StartupTimeoutMS) * time.Millisecond
	}
	proc, port, err := s.startProcessWithPortRetry(ctx, executable, deployment, filepath.Join(finalRelease, "source"), startupTimeout)
	if err != nil {
		return DeployResult{}, err
	}
	s.emit(ctx, "deployment_process_started", deployment, nil, map[string]any{"port": port})
	s.emit(ctx, "deployment_health_passed", deployment, nil, nil)
	deployment.Status = StatusActive
	deployment.Health = "healthy"
	deployment.StartedAt = nowString()
	deployment.UpdatedAt = deployment.StartedAt

	if err := s.replaceDeployment(deployment, proc); err != nil {
		_ = terminateProcess(proc, time.Duration(s.config.ShutdownTimeoutSeconds)*time.Second)
		s.emit(ctx, "deployment_activation_failed", deployment, err, nil)
		return DeployResult{}, errorf("deployment_activation_failed", "%v", err)
	}
	s.emit(ctx, "deployment_activated", deployment, nil, nil)
	rollbackRelease = false
	restoreSource = func() {}
	result := DeployResult{Deployment: s.publicView(deployment), SourcePreserved: request.Options.PreserveSource}
	if sourceStash != "" {
		if err := os.RemoveAll(sourceStash); err != nil {
			deployment.CleanupPending = true
			deployment.SourceCleanupPath = sourceStash
			deployment.LastWarning = err.Error()
			deployment.UpdatedAt = nowString()
			if persistErr := s.updateDeployment(deployment); persistErr != nil {
				return DeployResult{}, errors.Join(errorf("deployment_source_cleanup_failed", "%v", err), persistErr)
			}
			result.Deployment = s.publicView(deployment)
			result.WarningCode = "deployment_source_cleanup_failed"
			result.Warning = err.Error()
			result.CleanupPending = true
		} else {
			sourceStash = ""
		}
	}
	go s.monitor(request.Name, proc)
	return result, nil
}

func (s *Service) replaceDeployment(deployment Deployment, proc *managedProcess) error {
	s.mu.Lock()
	oldProc := s.processes[deployment.Name]
	next := cloneDeployments(s.deployments)
	next[deployment.Name] = deployment
	if err := s.store.save(&registry{Deployments: next}); err != nil {
		s.mu.Unlock()
		return err
	}
	s.deployments = next
	s.processes[deployment.Name] = proc
	s.mu.Unlock()
	if oldProc != nil && oldProc != proc {
		if err := terminateProcess(oldProc, time.Duration(s.config.ShutdownTimeoutSeconds)*time.Second); err != nil {
			deployment.CleanupPending = true
			deployment.LastWarning = "retired process cleanup failed: " + err.Error()
			deployment.UpdatedAt = nowString()
			_ = s.updateDeployment(deployment)
			s.trackRetiredProcess(oldProc)
		}
	}
	s.cleanupReleases(deployment.Name, deployment.ActiveRelease)
	return nil
}

func (s *Service) Delete(ctx context.Context, name string) error {
	if s == nil || !s.config.Enabled {
		return &CodedError{Code: "dynamic_hosting_disabled", Err: ErrDisabled}
	}
	if !s.owner {
		return errorf("dynamic_runtime_not_owned", "this LocalGate process does not own the dynamic runtime")
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()

	s.mu.RLock()
	deployment, ok := s.deployments[name]
	proc := s.processes[name]
	s.mu.RUnlock()
	if !ok {
		return &CodedError{Code: "deployment_not_found", Err: ErrNotFound}
	}

	deployment.DesiredState = DesiredDeleted
	deployment.Status = StatusDeleting
	deployment.Health = "stopping"
	deployment.UpdatedAt = nowString()
	if err := s.updateDeployment(deployment); err != nil {
		return err
	}
	s.emit(ctx, "deployment_deleting", deployment, nil, nil)
	if proc != nil {
		if err := terminateProcess(proc, time.Duration(s.config.ShutdownTimeoutSeconds)*time.Second); err != nil {
			deployment.Status = StatusFailed
			deployment.Health = "unhealthy"
			deployment.CleanupPending = true
			deployment.LastWarning = err.Error()
			deployment.LastError = err.Error()
			deployment.UpdatedAt = nowString()
			_ = s.updateDeployment(deployment)
			return errorf("deployment_stop_failed", "%v", err)
		}
	}

	appRoot := filepath.Join(s.root, "apps", name)
	trashRoot := filepath.Join(s.root, fmt.Sprintf(".trash-%s-%d", name, time.Now().UnixNano()))
	moved := false
	if _, err := os.Stat(appRoot); err == nil {
		if err := os.Rename(appRoot, trashRoot); err != nil {
			deployment.Status = StatusCleanupPending
			deployment.Health = "stopped"
			deployment.CleanupPending = true
			deployment.LastWarning = err.Error()
			deployment.LastError = err.Error()
			deployment.UpdatedAt = nowString()
			_ = s.updateDeployment(deployment)
			return errorf("deployment_cleanup_failed", "stage deployment cleanup: %v", err)
		}
		moved = true
	} else if !os.IsNotExist(err) {
		return errorf("deployment_cleanup_failed", "inspect deployment files: %v", err)
	}

	s.mu.Lock()
	next := cloneDeployments(s.deployments)
	delete(next, name)
	if err := s.store.save(&registry{Deployments: next}); err != nil {
		s.mu.Unlock()
		if moved {
			_ = os.Rename(trashRoot, appRoot)
		}
		return err
	}
	s.deployments = next
	if s.processes[name] == proc {
		delete(s.processes, name)
	}
	s.mu.Unlock()

	if moved {
		// A failed physical cleanup is safe to retry at startup because the
		// registry no longer references the atomically staged trash directory.
		_ = os.RemoveAll(trashRoot)
	}
	s.emit(ctx, "deployment_deleted", deployment, nil, nil)
	return nil
}

func (s *Service) Stop(ctx context.Context, name string) (View, error) {
	if s == nil || !s.config.Enabled {
		return View{}, &CodedError{Code: "dynamic_hosting_disabled", Err: ErrDisabled}
	}
	if !s.owner {
		return View{}, errorf("dynamic_runtime_not_owned", "this LocalGate process does not own the dynamic runtime")
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.RLock()
	deployment, ok := s.deployments[name]
	proc := s.processes[name]
	s.mu.RUnlock()
	if !ok {
		return View{}, &CodedError{Code: "deployment_not_found", Err: ErrNotFound}
	}

	deployment.DesiredState = DesiredStopped
	deployment.Status = StatusStopping
	deployment.Health = "stopping"
	deployment.UpdatedAt = nowString()
	if err := s.updateDeployment(deployment); err != nil {
		return View{}, err
	}
	if proc != nil {
		if err := terminateProcess(proc, time.Duration(s.config.ShutdownTimeoutSeconds)*time.Second); err != nil {
			deployment.Status = StatusFailed
			deployment.Health = "unhealthy"
			deployment.LastError = err.Error()
			deployment.UpdatedAt = nowString()
			_ = s.updateDeployment(deployment)
			return View{}, errorf("deployment_stop_failed", "%v", err)
		}
	}
	s.mu.Lock()
	if s.processes[name] == proc {
		delete(s.processes, name)
	}
	s.mu.Unlock()
	deployment.Status = StatusStopped
	deployment.Health = "stopped"
	deployment.LastError = ""
	deployment.UpdatedAt = nowString()
	if err := s.updateDeployment(deployment); err != nil {
		return View{}, err
	}
	s.emit(ctx, "deployment_stopped", deployment, nil, nil)
	return s.publicView(deployment), nil
}

func (s *Service) Start(ctx context.Context, name string) (View, error) {
	if s == nil || !s.config.Enabled {
		return View{}, &CodedError{Code: "dynamic_hosting_disabled", Err: ErrDisabled}
	}
	if !s.owner {
		return View{}, errorf("dynamic_runtime_not_owned", "this LocalGate process does not own the dynamic runtime")
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	return s.startExistingLocked(ctx, name, true)
}

func (s *Service) Restart(ctx context.Context, name string) (View, error) {
	if s == nil || !s.config.Enabled {
		return View{}, &CodedError{Code: "dynamic_hosting_disabled", Err: ErrDisabled}
	}
	if !s.owner {
		return View{}, errorf("dynamic_runtime_not_owned", "this LocalGate process does not own the dynamic runtime")
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.RLock()
	deployment, ok := s.deployments[name]
	proc := s.processes[name]
	s.mu.RUnlock()
	if !ok {
		return View{}, &CodedError{Code: "deployment_not_found", Err: ErrNotFound}
	}
	deployment.DesiredState = DesiredRunning
	deployment.Status = StatusStopping
	deployment.Health = "restarting"
	deployment.UpdatedAt = nowString()
	if err := s.updateDeployment(deployment); err != nil {
		return View{}, err
	}
	if proc != nil {
		if err := terminateProcess(proc, time.Duration(s.config.ShutdownTimeoutSeconds)*time.Second); err != nil {
			deployment.Status = StatusFailed
			deployment.Health = "unhealthy"
			deployment.LastError = err.Error()
			deployment.UpdatedAt = nowString()
			_ = s.updateDeployment(deployment)
			return View{}, errorf("deployment_stop_failed", "%v", err)
		}
	}
	s.mu.Lock()
	if s.processes[name] == proc {
		delete(s.processes, name)
	}
	s.mu.Unlock()
	return s.startExistingLocked(ctx, name, true)
}

func (s *Service) startExistingLocked(ctx context.Context, name string, resetRestart bool) (View, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !s.owner {
		return View{}, errorf("dynamic_runtime_not_owned", "this LocalGate process does not own the dynamic runtime")
	}
	s.mu.RLock()
	deployment, ok := s.deployments[name]
	_, alreadyRunning := s.processes[name]
	s.mu.RUnlock()
	if !ok {
		return View{}, &CodedError{Code: "deployment_not_found", Err: ErrNotFound}
	}
	if alreadyRunning {
		return s.publicView(deployment), nil
	}
	executable, err := s.executableForDeployment(deployment)
	if err != nil {
		return View{}, err
	}
	if deployment.Executable == "" {
		deployment.Executable = executable
	}
	releaseRoot := filepath.Join(s.root, "apps", name, "releases", deployment.ActiveRelease, "source")
	if !withinRoot(releaseRoot, s.root) {
		return View{}, errorf("deployment_start_failed", "release path is invalid")
	}
	if _, err := os.Stat(releaseRoot); err != nil {
		return View{}, errorf("deployment_start_failed", "active release is missing")
	}
	deployment.DesiredState = DesiredRunning
	deployment.Status = StatusStarting
	deployment.Health = "checking"
	deployment.UpdatedAt = nowString()
	if resetRestart {
		deployment.RestartCount = 0
	}
	if err := s.updateDeployment(deployment); err != nil {
		return View{}, err
	}
	proc, port, err := s.startProcessWithPortRetry(ctx, executable, deployment, releaseRoot, time.Duration(s.config.StartupTimeoutSeconds)*time.Second)
	if err != nil {
		deployment.Status = StatusFailed
		deployment.Health = "unhealthy"
		deployment.LastError = err.Error()
		deployment.UpdatedAt = nowString()
		if persistErr := s.updateDeployment(deployment); persistErr != nil {
			return View{}, errors.Join(err, persistErr)
		}
		return View{}, err
	}
	s.emit(ctx, "deployment_process_started", deployment, nil, map[string]any{"port": port})
	s.emit(ctx, "deployment_health_passed", deployment, nil, nil)
	deployment.Status = StatusActive
	deployment.Health = "healthy"
	deployment.StartedAt = nowString()
	deployment.UpdatedAt = deployment.StartedAt
	deployment.LastError = ""
	if err := s.replaceDeployment(deployment, proc); err != nil {
		_ = terminateProcess(proc, time.Duration(s.config.ShutdownTimeoutSeconds)*time.Second)
		return View{}, err
	}
	s.emit(ctx, "deployment_activated", deployment, nil, nil)
	go s.monitor(name, proc)
	return s.publicView(deployment), nil
}

func (s *Service) Get(name string) (View, error) {
	s.mu.RLock()
	deployment, ok := s.deployments[name]
	s.mu.RUnlock()
	if !ok {
		return View{}, &CodedError{Code: "deployment_not_found", Err: ErrNotFound}
	}
	view := s.publicView(deployment)
	if !s.config.Enabled {
		view.Status = "disabled"
		view.Health = "unavailable"
	}
	return view, nil
}

func (s *Service) SetAccess(ctx context.Context, name, access string) (View, error) {
	if s == nil {
		return View{}, &CodedError{Code: "deployment_not_found", Err: ErrNotFound}
	}
	access = strings.ToLower(strings.TrimSpace(access))
	if access != "public" && access != "private" {
		return View{}, errorf("deployment_access_invalid", "access must be public or private")
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.RLock()
	deployment, ok := s.deployments[name]
	proc := s.processes[name]
	s.mu.RUnlock()
	if !ok {
		return View{}, &CodedError{Code: "deployment_not_found", Err: ErrNotFound}
	}
	if normalizeAccess(deployment.Access, "public") == access {
		return s.publicView(deployment), nil
	}
	if proc != nil {
		if err := terminateProcess(proc, time.Duration(s.config.ShutdownTimeoutSeconds)*time.Second); err != nil {
			return View{}, errorf("deployment_stop_failed", "%v", err)
		}
		s.mu.Lock()
		if s.processes[name] == proc {
			delete(s.processes, name)
		}
		s.mu.Unlock()
	}
	deployment.Access = access
	deployment.UpdatedAt = nowString()
	if err := s.updateDeployment(deployment); err != nil {
		return View{}, err
	}
	if proc != nil && deployment.DesiredState == DesiredRunning {
		return s.startExistingLocked(ctx, name, true)
	}
	return s.publicView(deployment), nil
}

func (s *Service) List() []View {
	s.mu.RLock()
	values := make([]Deployment, 0, len(s.deployments))
	for _, deployment := range s.deployments {
		values = append(values, deployment)
	}
	s.mu.RUnlock()
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	out := make([]View, 0, len(values))
	for _, deployment := range values {
		view := s.publicView(deployment)
		if !s.config.Enabled {
			view.Status = "disabled"
			view.Health = "unavailable"
		}
		out = append(out, view)
	}
	return out
}

func (s *Service) Logs(name, stream string, tailBytes int64) (Logs, error) {
	if stream == "" {
		stream = "stderr"
	}
	if stream != "stdout" && stream != "stderr" {
		return Logs{}, errorf("deployment_log_stream_invalid", "stream must be stdout or stderr")
	}
	s.mu.RLock()
	deployment, ok := s.deployments[name]
	s.mu.RUnlock()
	if !ok {
		return Logs{}, &CodedError{Code: "deployment_not_found", Err: ErrNotFound}
	}
	path := filepath.Join(s.root, "apps", name, "releases", deployment.ActiveRelease, stream+".log")
	data, truncated, err := readLogTail(path, tailBytes)
	if err != nil {
		return Logs{}, err
	}
	return Logs{Name: name, Stream: stream, Data: data, Truncated: truncated}, nil
}

func (s *Service) updateDeployment(deployment Deployment) error {
	s.mu.Lock()
	next := cloneDeployments(s.deployments)
	next[deployment.Name] = deployment
	if err := s.store.save(&registry{Deployments: next}); err != nil {
		s.mu.Unlock()
		return err
	}
	s.deployments = next
	s.mu.Unlock()
	return nil
}

func (s *Service) monitor(name string, proc *managedProcess) {
	<-proc.done
	if proc.wasIntentional() {
		return
	}
	exit := proc.exitResult()
	s.opMu.Lock()
	s.mu.Lock()
	current := s.processes[name]
	deployment, ok := s.deployments[name]
	if !ok || current != proc {
		s.mu.Unlock()
		s.opMu.Unlock()
		return
	}
	delete(s.processes, name)
	failed := exit.Code != 0 || exit.Err != nil
	deployment.LastExitAt = exit.At.Format(time.RFC3339Nano)
	deployment.UpdatedAt = nowString()
	if failed {
		deployment.Status = StatusCrashed
		deployment.Health = "unhealthy"
		deployment.LastError = fmt.Sprintf("process exited with code %d: %v", exit.Code, exit.Err)
	} else {
		deployment.DesiredState = DesiredStopped
		deployment.Status = StatusStopped
		deployment.Health = "stopped"
		deployment.LastError = ""
	}
	if failed && time.Since(proc.startedAt) >= time.Duration(s.config.RestartResetSeconds)*time.Second {
		deployment.RestartCount = 0
	}
	shouldRestart := failed && deployment.DesiredState == DesiredRunning && deployment.RestartPolicy == "on-failure" && deployment.RestartCount < s.config.RestartMaxAttempts
	if shouldRestart {
		deployment.RestartCount++
	} else if failed && deployment.DesiredState == DesiredRunning && deployment.RestartPolicy == "on-failure" && deployment.RestartCount >= s.config.RestartMaxAttempts {
		deployment.LastError = fmt.Sprintf("restart attempts exhausted after %d attempts; last exit: code=%d err=%v", deployment.RestartCount, exit.Code, exit.Err)
	}
	next := cloneDeployments(s.deployments)
	next[name] = deployment
	if saveErr := s.store.save(&registry{Deployments: next}); saveErr != nil {
		deployment.LastError = strings.TrimSpace(deployment.LastError + "; persist runtime state: " + saveErr.Error())
		s.deployments[name] = deployment
		s.lastRuntimeError = saveErr
		s.mu.Unlock()
		s.opMu.Unlock()
		s.emit(context.Background(), "deployment_registry_persist_failed", deployment, saveErr, nil)
		s.retryRegistryPersistence(name, deployment.UpdatedAt)
		return
	}
	s.deployments = next
	s.mu.Unlock()
	s.opMu.Unlock()
	s.emit(context.Background(), "deployment_process_exited", deployment, exit.Err, map[string]any{"exit_code": exit.Code})
	if shouldRestart {
		s.emit(context.Background(), "deployment_restart_scheduled", deployment, nil, nil)
		delay := restartBackoff(time.Duration(s.config.RestartBackoffSeconds)*time.Second, deployment.RestartCount)
		go func(release string) {
			select {
			case <-time.After(delay):
			case <-s.closed:
				return
			}
			s.opMu.Lock()
			defer s.opMu.Unlock()
			s.mu.RLock()
			latest, exists := s.deployments[name]
			_, running := s.processes[name]
			s.mu.RUnlock()
			if !exists || running || latest.DesiredState != DesiredRunning || latest.ActiveRelease != release {
				return
			}
			_, _ = s.startExistingLocked(context.Background(), name, false)
		}(deployment.ActiveRelease)
	} else if failed && deployment.DesiredState == DesiredRunning && deployment.RestartPolicy == "on-failure" {
		s.emit(context.Background(), "deployment_restart_exhausted", deployment, nil, nil)
	}
}

func restartBackoff(base time.Duration, attempt int) time.Duration {
	if base <= 0 || attempt <= 0 {
		return 0
	}
	shift := attempt - 1
	if shift > 8 {
		shift = 8
	}
	delay := base * time.Duration(1<<shift)
	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}

func (s *Service) recoverAll() {
	for _, view := range s.List() {
		select {
		case <-s.closed:
			return
		default:
		}
		s.opMu.Lock()
		s.mu.RLock()
		deployment, exists := s.deployments[view.Name]
		s.mu.RUnlock()
		if !exists {
			s.opMu.Unlock()
			continue
		}
		if deployment.DesiredState == DesiredDeleted || deployment.Status == StatusDeleting {
			_ = os.RemoveAll(filepath.Join(s.root, "apps", deployment.Name))
			s.mu.Lock()
			next := cloneDeployments(s.deployments)
			delete(next, deployment.Name)
			if err := s.store.save(&registry{Deployments: next}); err == nil {
				s.deployments = next
			}
			s.mu.Unlock()
			s.opMu.Unlock()
			continue
		}
		if deployment.SourceCleanupPath != "" {
			if cleanupPath, err := s.validatePendingSourceCleanup(deployment); err == nil {
				if err := os.RemoveAll(cleanupPath); err == nil {
					deployment.SourceCleanupPath = ""
					deployment.CleanupPending = false
					deployment.LastWarning = ""
					deployment.UpdatedAt = nowString()
					_ = s.updateDeployment(deployment)
				}
			} else {
				deployment.CleanupPending = true
				deployment.LastWarning = "pending source cleanup rejected: " + err.Error()
				deployment.UpdatedAt = nowString()
				_ = s.updateDeployment(deployment)
			}
		}
		if deployment.DesiredState != DesiredRunning {
			if deployment.Status == StatusStopping || deployment.Status == StatusFailed || deployment.Status == StatusCleanupPending {
				deployment.Status = StatusStopped
				deployment.Health = "stopped"
				deployment.UpdatedAt = nowString()
				_ = s.updateDeployment(deployment)
			}
			s.opMu.Unlock()
			continue
		}
		_, err := s.startExistingLocked(context.Background(), deployment.Name, false)
		s.opMu.Unlock()
		if err == nil {
			s.emit(context.Background(), "deployment_recovered", deployment, nil, nil)
		} else {
			s.emit(context.Background(), "deployment_recovery_failed", deployment, err, nil)
		}
	}
}

func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() { close(s.closed) })
	s.opMu.Lock()
	s.mu.Lock()
	processes := make([]*managedProcess, 0, len(s.processes))
	for name, proc := range s.processes {
		processes = append(processes, proc)
		delete(s.processes, name)
	}
	s.mu.Unlock()
	var closeErrors []error
	for _, proc := range processes {
		if err := terminateProcess(proc, time.Duration(s.config.ShutdownTimeoutSeconds)*time.Second); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}
	for pid, proc := range s.orphans {
		if err := terminateProcess(proc, time.Duration(s.config.ShutdownTimeoutSeconds)*time.Second); err != nil {
			closeErrors = append(closeErrors, fmt.Errorf("terminate retired process %d: %w", pid, err))
		}
	}
	s.opMu.Unlock()
	return errors.Join(closeErrors...)
}

func (s *Service) validateName(name string) error {
	if len(name) < 1 || len(name) > 63 || name[0] == '-' || name[len(name)-1] == '-' {
		return errorf("deployment_name_invalid", "deployment name must be a valid DNS label")
	}
	for _, ch := range name {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' {
			continue
		}
		return errorf("deployment_name_invalid", "deployment name must contain lowercase letters, numbers, and hyphens")
	}
	for _, reserved := range s.config.ReservedNames {
		if name == strings.ToLower(strings.TrimSpace(reserved)) {
			return errorf("deployment_name_invalid", "deployment name is reserved")
		}
	}
	controlHost := appBaseHostname(s.config.ControlBaseURL)
	if controlHost != "" {
		for _, baseURL := range []string{s.config.PublicBaseURL, s.config.PrivateBaseURL} {
			domain := appBaseHostname(baseURL)
			if domain != "" && name+"."+domain == controlHost {
				return errorf("deployment_host_conflict", "deployment hostname conflicts with http.base_url")
			}
		}
	}
	return nil
}

func (s *Service) resolveExecutable(argv []string) (string, error) {
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return "", errorf("deployment_executable_denied", "runtime argv must not be empty")
	}
	requested := strings.TrimSpace(argv[0])
	allowed := false
	for _, candidate := range s.config.AllowedExecutables {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if filepath.IsAbs(candidate) {
			if filepath.IsAbs(requested) && filepath.Clean(candidate) == filepath.Clean(requested) {
				allowed = true
				break
			}
			continue
		}
		if !strings.ContainsRune(requested, os.PathSeparator) && requested == candidate {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", &CodedError{Code: "deployment_executable_denied", Err: ErrExecutableDenied}
	}
	resolved, err := exec.LookPath(requested)
	if err != nil {
		return "", errorf("deployment_executable_denied", "executable not found: %s", requested)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", errorf("deployment_executable_denied", "resolve executable path: %v", err)
	}
	if evaluated, evalErr := filepath.EvalSymlinks(resolved); evalErr == nil {
		resolved = evaluated
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", errorf("deployment_executable_denied", "executable is not a regular executable file: %s", resolved)
	}
	return filepath.Clean(resolved), nil
}

func (s *Service) executableForDeployment(deployment Deployment) (string, error) {
	if strings.TrimSpace(deployment.Executable) == "" {
		return s.resolveExecutable(deployment.Argv)
	}
	resolved := filepath.Clean(deployment.Executable)
	if !filepath.IsAbs(resolved) {
		return "", errorf("deployment_executable_denied", "persisted executable path is not absolute")
	}
	if evaluated, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = filepath.Clean(evaluated)
	}
	allowed := false
	requestedName := ""
	if len(deployment.Argv) > 0 {
		requestedName = strings.TrimSpace(deployment.Argv[0])
	}
	for _, candidate := range s.config.AllowedExecutables {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if filepath.IsAbs(candidate) {
			candidatePath := filepath.Clean(candidate)
			if evaluated, err := filepath.EvalSymlinks(candidatePath); err == nil {
				candidatePath = filepath.Clean(evaluated)
			}
			if candidatePath == resolved {
				allowed = true
				break
			}
			continue
		}
		if requestedName == candidate && !strings.ContainsRune(requestedName, os.PathSeparator) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", errorf("deployment_executable_denied", "persisted executable is no longer allowed")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", errorf("deployment_executable_denied", "persisted executable is unavailable: %s", resolved)
	}
	return resolved, nil
}

func (s *Service) validateEnvironment(input map[string]string) (map[string]string, error) {
	allowed := make(map[string]struct{}, len(s.config.EnvAllowlist))
	for _, key := range s.config.EnvAllowlist {
		allowed[strings.TrimSpace(key)] = struct{}{}
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		if _, ok := allowed[key]; !ok {
			return nil, &CodedError{Code: "deployment_env_denied", Err: fmt.Errorf("%w: %s", ErrEnvironmentDenied, key)}
		}
		lower := strings.ToLower(key)
		for _, sensitive := range []string{"password", "passwd", "secret", "token", "authorization", "cookie", "private_key"} {
			if strings.Contains(lower, sensitive) {
				return nil, errorf("deployment_env_denied", "persistent secret environment variables are not supported: %s", key)
			}
		}
		out[key] = value
	}
	return out, nil
}

func normalizeWorkingDir(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "." {
		return ".", nil
	}
	if filepath.IsAbs(value) {
		return "", errorf("deployment_start_failed", "runtime cwd must be relative to the release")
	}
	clean := filepath.Clean(value)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", errorf("deployment_start_failed", "runtime cwd escaped the release")
	}
	return filepath.ToSlash(clean), nil
}

func allocatePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func newReleaseID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b), nil
}

func cloneDeployments(input map[string]Deployment) map[string]Deployment {
	out := make(map[string]Deployment, len(input))
	for key, value := range input {
		value.Argv = append([]string(nil), value.Argv...)
		if value.Env != nil {
			env := make(map[string]string, len(value.Env))
			for envKey, envValue := range value.Env {
				env[envKey] = envValue
			}
			value.Env = env
		}
		out[key] = value
	}
	return out
}
