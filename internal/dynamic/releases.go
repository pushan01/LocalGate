package dynamic

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (s *Service) Releases(name string) ([]Release, error) {
	if s == nil {
		return nil, &CodedError{Code: "deployment_not_found", Err: ErrNotFound}
	}
	s.mu.RLock()
	deployment, ok := s.deployments[name]
	s.mu.RUnlock()
	if !ok {
		return nil, &CodedError{Code: "deployment_not_found", Err: ErrNotFound}
	}
	root := filepath.Join(s.root, "apps", name, "releases")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	out := make([]Release, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".stage-") {
			continue
		}
		releaseRoot := filepath.Join(root, entry.Name())
		metadata, metadataErr := readReleaseMetadata(releaseRoot)
		if metadataErr != nil {
			info, infoErr := entry.Info()
			if infoErr != nil {
				continue
			}
			metadata = releaseMetadata{ReleaseID: entry.Name(), CreatedAt: info.ModTime().UTC().Format(time.RFC3339Nano)}
		}
		out = append(out, Release{
			ID: entry.Name(), Active: entry.Name() == deployment.ActiveRelease,
			CreatedAt: metadata.CreatedAt, FileCount: metadata.FileCount, TotalBytes: metadata.TotalBytes,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

func (s *Service) Rollback(ctx context.Context, name, releaseID string) (View, error) {
	if s == nil || !s.config.Enabled {
		return View{}, &CodedError{Code: "dynamic_hosting_disabled", Err: ErrDisabled}
	}
	if !s.owner {
		return View{}, errorf("dynamic_runtime_not_owned", "this LocalGate process does not own the dynamic runtime")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	releaseID = strings.TrimSpace(releaseID)
	if releaseID == "" || filepath.Base(releaseID) != releaseID || strings.HasPrefix(releaseID, ".") {
		return View{}, errorf("deployment_release_invalid", "release_id is invalid")
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.RLock()
	deployment, ok := s.deployments[name]
	_, alreadyRunning := s.processes[name]
	s.mu.RUnlock()
	if !ok {
		return View{}, &CodedError{Code: "deployment_not_found", Err: ErrNotFound}
	}
	if deployment.ActiveRelease == releaseID && alreadyRunning {
		return s.publicView(deployment), nil
	}
	releaseRoot := filepath.Join(s.root, "apps", name, "releases", releaseID, "source")
	if !withinRoot(releaseRoot, s.root) {
		return View{}, errorf("deployment_release_invalid", "release path is invalid")
	}
	if info, err := os.Stat(releaseRoot); err != nil || !info.IsDir() {
		return View{}, errorf("deployment_release_not_found", "release is not available")
	}
	metadata, _ := readReleaseMetadata(filepath.Dir(releaseRoot))
	candidate := deployment
	candidate.ActiveRelease = releaseID
	candidate.DesiredState = DesiredRunning
	candidate.Status = StatusStarting
	candidate.Health = "checking"
	candidate.LastError = ""
	candidate.LastWarning = ""
	candidate.CleanupPending = false
	candidate.UpdatedAt = nowString()
	if metadata.FileCount > 0 {
		candidate.FileCount = metadata.FileCount
	}
	if metadata.TotalBytes > 0 {
		candidate.TotalBytes = metadata.TotalBytes
	}
	executable, err := s.executableForDeployment(candidate)
	if err != nil {
		return View{}, err
	}
	proc, port, err := s.startProcessWithPortRetry(ctx, executable, candidate, releaseRoot, time.Duration(s.config.StartupTimeoutSeconds)*time.Second)
	if err != nil {
		s.emit(ctx, "deployment_rollback_failed", candidate, err, nil)
		return View{}, err
	}
	s.emit(ctx, "deployment_process_started", candidate, nil, map[string]any{"port": port, "rollback": true})
	s.emit(ctx, "deployment_health_passed", candidate, nil, map[string]any{"rollback": true})
	candidate.Status = StatusActive
	candidate.Health = "healthy"
	candidate.StartedAt = nowString()
	candidate.UpdatedAt = candidate.StartedAt
	if err := s.replaceDeployment(candidate, proc); err != nil {
		_ = terminateProcess(proc, time.Duration(s.config.ShutdownTimeoutSeconds)*time.Second)
		s.emit(ctx, "deployment_rollback_failed", candidate, err, nil)
		return View{}, errorf("deployment_activation_failed", "%v", err)
	}
	s.emit(ctx, "deployment_rolled_back", candidate, nil, map[string]any{"from_release": deployment.ActiveRelease})
	go s.monitor(name, proc)
	return s.publicView(candidate), nil
}
