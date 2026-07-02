package dynamic

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (s *Service) SetEventSink(sink EventSink) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.eventSink = sink
	s.mu.Unlock()
}

func (s *Service) emit(ctx context.Context, action string, deployment Deployment, err error, details map[string]any) {
	if s == nil {
		return
	}
	s.mu.RLock()
	sink := s.eventSink
	s.mu.RUnlock()
	if sink == nil {
		return
	}
	event := Event{
		Action: action, Name: deployment.Name, WorkspaceID: deployment.WorkspaceID,
		ReleaseID: deployment.ActiveRelease, Executable: deployment.Executable,
		Hostname: s.hostnameForAccess(deployment.Name, deployment.Access), HealthPath: deployment.HealthPath,
		RestartCount: deployment.RestartCount, Details: details,
		Operation: operationFromContext(ctx), CreatedAt: time.Now().UTC(),
	}
	if err != nil {
		event.Error = err.Error()
	}
	go sink(event)
}

func (s *Service) cleanupStorage() {
	if s == nil {
		return
	}
	rootEntries, _ := os.ReadDir(s.root)
	for _, entry := range rootEntries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".trash-") {
			_ = os.RemoveAll(filepath.Join(s.root, entry.Name()))
		}
	}
	appsRoot := filepath.Join(s.root, "apps")
	entries, err := os.ReadDir(appsRoot)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		s.mu.RLock()
		deployment, exists := s.deployments[name]
		s.mu.RUnlock()
		if !exists {
			_ = os.RemoveAll(filepath.Join(appsRoot, name))
			continue
		}
		releasesRoot := filepath.Join(appsRoot, name, "releases")
		releases, _ := os.ReadDir(releasesRoot)
		for _, release := range releases {
			if strings.HasPrefix(release.Name(), ".stage-") {
				_ = os.RemoveAll(filepath.Join(releasesRoot, release.Name()))
			}
		}
		s.cleanupReleases(name, deployment.ActiveRelease)
	}
}

func (s *Service) cleanupReleases(name, active string) {
	root := filepath.Join(s.root, "apps", name, "releases")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	type releaseInfo struct {
		name string
		mod  time.Time
	}
	items := make([]releaseInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".stage-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		items = append(items, releaseInfo{name: entry.Name(), mod: info.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod.After(items[j].mod) })
	keep := map[string]struct{}{active: {}}
	for _, item := range items {
		if len(keep) >= s.config.ReleaseRetention {
			break
		}
		keep[item.name] = struct{}{}
	}
	for _, item := range items {
		if _, ok := keep[item.name]; !ok {
			_ = os.RemoveAll(filepath.Join(root, item.name))
		}
	}
}

func (s *Service) trackRetiredProcess(proc *managedProcess) {
	if s == nil || proc == nil || proc.cmd == nil || proc.cmd.Process == nil {
		return
	}
	pid := proc.cmd.Process.Pid
	s.mu.Lock()
	s.orphans[pid] = proc
	s.mu.Unlock()
	go func() {
		for attempt := 0; attempt < 3; attempt++ {
			if err := terminateProcess(proc, time.Duration(s.config.ShutdownTimeoutSeconds)*time.Second); err == nil {
				s.mu.Lock()
				delete(s.orphans, pid)
				s.mu.Unlock()
				return
			}
			select {
			case <-time.After(time.Duration(attempt+1) * time.Second):
			case <-s.closed:
				return
			}
		}
	}()
}

func (s *Service) retryRegistryPersistence(name, updatedAt string) {
	go func() {
		for attempt := 0; attempt < 3; attempt++ {
			select {
			case <-time.After(time.Duration(attempt+1) * time.Second):
			case <-s.closed:
				return
			}
			s.opMu.Lock()
			s.mu.RLock()
			deployment, exists := s.deployments[name]
			next := cloneDeployments(s.deployments)
			s.mu.RUnlock()
			if !exists || deployment.UpdatedAt != updatedAt {
				s.opMu.Unlock()
				return
			}
			if err := s.store.save(&registry{Deployments: next}); err == nil {
				s.opMu.Unlock()
				return
			}
			s.opMu.Unlock()
		}
	}()
}

func writeReleaseMetadata(releaseRoot string, deployment Deployment) error {
	metadata := releaseMetadata{
		Name: deployment.Name, Type: Type, WorkspaceID: deployment.WorkspaceID,
		ReleaseID: deployment.ActiveRelease, CreatedAt: nowString(),
		FileCount: deployment.FileCount, TotalBytes: deployment.TotalBytes,
		Executable: deployment.Executable, Argv: append([]string(nil), deployment.Argv...),
		WorkingDir: deployment.WorkingDir, HealthPath: deployment.HealthPath,
		RestartPolicy: deployment.RestartPolicy,
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(releaseRoot, "metadata.json")
	tmp, err := os.CreateTemp(releaseRoot, ".metadata-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func readReleaseMetadata(releaseRoot string) (releaseMetadata, error) {
	data, err := os.ReadFile(filepath.Join(releaseRoot, "metadata.json"))
	if err != nil {
		return releaseMetadata{}, err
	}
	var metadata releaseMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return releaseMetadata{}, err
	}
	return metadata, nil
}

func (s *Service) validatePendingSourceCleanup(deployment Deployment) (string, error) {
	workspace, ok := s.workspaces[deployment.WorkspaceID]
	if !ok {
		return "", errors.New("workspace is unavailable")
	}
	path := filepath.Clean(strings.TrimSpace(deployment.SourceCleanupPath))
	if path == "" || !filepath.IsAbs(path) || !strings.HasPrefix(filepath.Base(path), ".localgate-source-") {
		return "", errors.New("cleanup path is invalid")
	}
	if pathsOverlap(path, s.root) {
		return "", errors.New("cleanup path overlaps managed storage")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	allowed := false
	for _, root := range workspace.Roots {
		absolute, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		resolved, err := filepath.EvalSymlinks(filepath.Clean(absolute))
		if err != nil {
			continue
		}
		if withinRoot(parent, resolved) && filepath.Clean(path) != filepath.Clean(resolved) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", errors.New("cleanup path is outside the workspace")
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("cleanup path is a symbolic link")
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return path, nil
}

func (s *Service) startProcessWithPortRetry(ctx context.Context, executable string, deployment Deployment, releaseRoot string, startupTimeout time.Duration) (*managedProcess, int, error) {
	hostname := s.hostnameForAccess(deployment.Name, deployment.Access)
	publicURL := s.publicURLForAccess(deployment.Name, deployment.Access)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		port, err := allocatePort()
		if err != nil {
			return nil, 0, errorf("deployment_port_allocation_failed", "%v", err)
		}
		proc, err := startManagedProcess(ctx, executable, deployment, hostname, publicURL, releaseRoot, port, startupTimeout, s.config.MaxLogBytes, s.config.MaxLogFiles)
		if err == nil {
			return proc, port, nil
		}
		lastErr = err
		if ErrorCode(err) != "deployment_port_allocation_failed" {
			return nil, 0, err
		}
	}
	if lastErr == nil {
		lastErr = errorf("deployment_port_allocation_failed", "unable to allocate an application port")
	}
	return nil, 0, lastErr
}

func (s *Service) hostname(name string) string {
	if s == nil || strings.TrimSpace(name) == "" {
		return ""
	}
	s.mu.RLock()
	deployment, ok := s.deployments[name]
	s.mu.RUnlock()
	access := s.config.DefaultAccess
	if ok {
		access = deployment.Access
	}
	return s.hostnameForAccess(name, access)
}

func (s *Service) hostnameForAccess(name, access string) string {
	hostname, _ := s.appLocationForAccess(name, access)
	return hostname
}

func (s *Service) publicURL(name string) string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	deployment, ok := s.deployments[name]
	s.mu.RUnlock()
	access := s.config.DefaultAccess
	if ok {
		access = deployment.Access
	}
	return s.publicURLForAccess(name, access)
}

func (s *Service) publicURLForAccess(name, access string) string {
	_, publicURL := s.appLocationForAccess(name, access)
	return publicURL
}

func (s *Service) publicView(deployment Deployment) View {
	if s == nil {
		return publicView(deployment, "", "")
	}
	hostname, publicURL := s.appLocationForAccess(deployment.Name, deployment.Access)
	return publicView(deployment, hostname, publicURL)
}

func (s *Service) appLocationForAccess(name, access string) (string, string) {
	if s == nil || strings.TrimSpace(name) == "" {
		return "", ""
	}
	base := s.config.PublicBaseURL
	if normalizeAccess(access, "public") == "private" {
		base = s.config.PrivateBaseURL
	}
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(base), "/"))
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", ""
	}
	hostname := name + "." + strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	location := *parsed
	location.Host = hostname
	if port := parsed.Port(); port != "" {
		location.Host = net.JoinHostPort(hostname, port)
	}
	location.Path = "/"
	location.RawPath = ""
	return hostname, location.String()
}

func (s *Service) baseSchemeForAccess(access string) string {
	if s == nil {
		return ""
	}
	base := s.config.PublicBaseURL
	if normalizeAccess(access, "public") == "private" {
		base = s.config.PrivateBaseURL
	}
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return ""
	}
	return parsed.Scheme
}

func appBaseHostname(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
}
