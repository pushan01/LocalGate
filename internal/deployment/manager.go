package deployment

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"localgate/internal/config"
	"localgate/internal/dynamic"
	"localgate/internal/security"
	"localgate/internal/static"
)

const (
	TypeStatic  = "static"
	TypeDynamic = "dynamic"
)

var ErrUnsafeSourceRemoval = errors.New("unsafe source removal")

type Manager struct {
	static  *static.Service
	dynamic *dynamic.Service
	baseURL string
}

type DeployOptions struct {
	Overwrite        bool
	PreserveSource   bool
	HealthPath       string
	StartupTimeoutMS int
	RestartPolicy    string
}

type DeployRequest struct {
	Type      string
	Name      string
	Source    string
	Workspace config.Workspace
	Runtime   *dynamic.RuntimeSpec
	Options   DeployOptions
}

type DeployResult struct {
	Status          string
	Type            string
	Site            string
	URL             string
	Hostname        string
	ReleaseID       string
	RuntimeStatus   string
	Health          string
	FileCount       int
	TotalBytes      int64
	SourcePreserved bool
	CleanupPending  bool
	WarningCode     string
	Warning         string
}

type View struct {
	Type           string
	Site           string
	URL            string
	Status         string
	Index          string
	SPA            bool
	Hostname       string
	Health         string
	ReleaseID      string
	RestartCount   int
	CreatedAt      string
	UpdatedAt      string
	DesiredState   string
	WorkingDir     string
	HealthPath     string
	RestartPolicy  string
	LastError      string
	LastWarning    string
	CleanupPending bool
	FileCount      int
	TotalBytes     int64
	EnvKeys        []string
}

func NewManager(staticService *static.Service, dynamicService *dynamic.Service, baseURL string) *Manager {
	return &Manager{static: staticService, dynamic: dynamicService, baseURL: strings.TrimRight(baseURL, "/")}
}

func (m *Manager) StaticService() *static.Service {
	if m == nil {
		return nil
	}
	return m.static
}
func (m *Manager) DynamicService() *dynamic.Service {
	if m == nil {
		return nil
	}
	return m.dynamic
}
func (m *Manager) DynamicEnabled() bool { return m != nil && m.dynamic != nil && m.dynamic.Enabled() }

func (m *Manager) Deploy(ctx context.Context, request DeployRequest) (DeployResult, error) {
	if m == nil {
		return DeployResult{}, errors.New("deployment manager is not available")
	}
	switch request.Type {
	case TypeStatic:
		return m.deployStatic(ctx, request)
	case TypeDynamic:
		return m.deployDynamic(ctx, request)
	default:
		return DeployResult{}, errors.New("unsupported deployment type")
	}
}

func (m *Manager) deployStatic(ctx context.Context, request DeployRequest) (DeployResult, error) {
	if m.static == nil {
		return DeployResult{}, errors.New("static service is not available")
	}
	decision := security.ValidatePath(request.Source, request.Workspace, security.Options{FollowSymlink: true})
	if !decision.Allowed {
		return DeployResult{}, fmt.Errorf("static source is not allowed: %s", decision.Reason)
	}
	if !request.Options.PreserveSource && filepath.Clean(decision.Path) == filepath.Clean(decision.Root) {
		return DeployResult{}, fmt.Errorf("%w: source must be a strict child of the workspace root", ErrUnsafeSourceRemoval)
	}
	result, err := m.static.DeploySiteWithOptions(ctx, request.Name, decision.Path, static.DeployOptions{
		Overwrite: request.Options.Overwrite, RemoveSource: !request.Options.PreserveSource,
	})
	if err != nil {
		return DeployResult{}, err
	}
	url := static.PublicPathPrefix + request.Name + "/"
	if m.baseURL != "" {
		url = m.baseURL + url
	}
	return DeployResult{
		Status: "deployed", Type: TypeStatic, Site: request.Name, URL: url,
		FileCount: result.FileCount, TotalBytes: result.TotalBytes, SourcePreserved: result.SourcePreserved,
	}, nil
}

func (m *Manager) deployDynamic(ctx context.Context, request DeployRequest) (DeployResult, error) {
	if m.dynamic == nil {
		return DeployResult{}, &dynamic.CodedError{Code: "dynamic_hosting_disabled", Err: dynamic.ErrDisabled}
	}
	if request.Runtime == nil {
		return DeployResult{}, errors.New("runtime is required for dynamic deployment")
	}
	result, err := m.dynamic.Deploy(ctx, dynamic.DeployRequest{
		WorkspaceID: request.Workspace.ID,
		Name:        request.Name, Source: request.Source, Runtime: *request.Runtime,
		Options: dynamic.DeployOptions{
			Overwrite: request.Options.Overwrite, PreserveSource: request.Options.PreserveSource,
			HealthPath: request.Options.HealthPath, StartupTimeoutMS: request.Options.StartupTimeoutMS,
			RestartPolicy: request.Options.RestartPolicy,
		},
	})
	if err != nil {
		return DeployResult{}, err
	}
	status := "deployed"
	if result.WarningCode != "" {
		status = "deployed_with_warning"
	}
	return DeployResult{
		Status: status, Type: TypeDynamic, Site: result.Deployment.Name,
		URL: result.Deployment.URL, Hostname: result.Deployment.Hostname,
		ReleaseID: result.Deployment.ReleaseID, RuntimeStatus: result.Deployment.Status,
		Health: result.Deployment.Health, FileCount: result.Deployment.FileCount,
		TotalBytes: result.Deployment.TotalBytes, SourcePreserved: result.SourcePreserved,
		CleanupPending: result.CleanupPending, WarningCode: result.WarningCode, Warning: result.Warning,
	}, nil
}

func (m *Manager) Delete(ctx context.Context, deploymentType, name string) error {
	if m == nil {
		return errors.New("deployment manager is not available")
	}
	switch deploymentType {
	case TypeStatic:
		if m.static == nil {
			return errors.New("static service is not available")
		}
		return m.static.DeleteSite(name)
	case TypeDynamic:
		if m.dynamic == nil {
			return &dynamic.CodedError{Code: "dynamic_hosting_disabled", Err: dynamic.ErrDisabled}
		}
		return m.dynamic.Delete(ctx, name)
	default:
		return errors.New("unsupported deployment type")
	}
}

func (m *Manager) List(ctx context.Context) ([]View, error) {
	if m == nil || m.static == nil {
		return nil, errors.New("deployment manager is not available")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	registry, err := m.static.ListSites()
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(registry.Sites))
	for name, site := range registry.Sites {
		url := static.PublicPathPrefix + name + "/"
		if m.baseURL != "" {
			url = m.baseURL + url
		}
		out = append(out, View{Type: TypeStatic, Site: name, URL: url, Status: "active", Index: site.Index, SPA: site.SPA, CreatedAt: site.CreatedAt, UpdatedAt: site.UpdatedAt})
	}
	if m.dynamic != nil {
		for _, item := range m.dynamic.List() {
			out = append(out, dynamicView(item))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Site == out[j].Site {
			return out[i].Type < out[j].Type
		}
		return out[i].Site < out[j].Site
	})
	return out, nil
}

func dynamicView(item dynamic.View) View {
	return View{
		Type: TypeDynamic, Site: item.Name, URL: item.URL, Status: item.Status,
		Hostname: item.Hostname, Health: item.Health, ReleaseID: item.ReleaseID,
		RestartCount: item.RestartCount, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		DesiredState: item.DesiredState, WorkingDir: item.WorkingDir, HealthPath: item.HealthPath,
		RestartPolicy: item.RestartPolicy, LastError: item.LastError, LastWarning: item.LastWarning,
		CleanupPending: item.CleanupPending, FileCount: item.FileCount, TotalBytes: item.TotalBytes,
		EnvKeys: append([]string(nil), item.EnvKeys...),
	}
}

func (m *Manager) Get(deploymentType, name string) (View, error) {
	if deploymentType != TypeDynamic || m == nil || m.dynamic == nil {
		return View{}, &dynamic.CodedError{Code: "dynamic_hosting_disabled", Err: dynamic.ErrDisabled}
	}
	item, err := m.dynamic.Get(name)
	if err != nil {
		return View{}, err
	}
	return dynamicView(item), nil
}

func (m *Manager) Restart(ctx context.Context, deploymentType, name string) (View, error) {
	if deploymentType != TypeDynamic || m == nil || m.dynamic == nil {
		return View{}, &dynamic.CodedError{Code: "dynamic_hosting_disabled", Err: dynamic.ErrDisabled}
	}
	item, err := m.dynamic.Restart(ctx, name)
	if err != nil {
		return View{}, err
	}
	return dynamicView(item), nil
}
func (m *Manager) Stop(ctx context.Context, deploymentType, name string) (View, error) {
	if deploymentType != TypeDynamic || m == nil || m.dynamic == nil {
		return View{}, &dynamic.CodedError{Code: "dynamic_hosting_disabled", Err: dynamic.ErrDisabled}
	}
	item, err := m.dynamic.Stop(ctx, name)
	if err != nil {
		return View{}, err
	}
	return dynamicView(item), nil
}
func (m *Manager) Start(ctx context.Context, deploymentType, name string) (View, error) {
	if deploymentType != TypeDynamic || m == nil || m.dynamic == nil {
		return View{}, &dynamic.CodedError{Code: "dynamic_hosting_disabled", Err: dynamic.ErrDisabled}
	}
	item, err := m.dynamic.Start(ctx, name)
	if err != nil {
		return View{}, err
	}
	return dynamicView(item), nil
}
func (m *Manager) Logs(deploymentType, name, stream string, tailBytes int64) (dynamic.Logs, error) {
	if deploymentType != TypeDynamic || m == nil || m.dynamic == nil {
		return dynamic.Logs{}, &dynamic.CodedError{Code: "dynamic_hosting_disabled", Err: dynamic.ErrDisabled}
	}
	return m.dynamic.Logs(name, stream, tailBytes)
}

func (m *Manager) Releases(deploymentType, name string) ([]dynamic.Release, error) {
	if deploymentType != TypeDynamic || m == nil || m.dynamic == nil {
		return nil, &dynamic.CodedError{Code: "dynamic_hosting_disabled", Err: dynamic.ErrDisabled}
	}
	return m.dynamic.Releases(name)
}

func (m *Manager) Rollback(ctx context.Context, deploymentType, name, releaseID string) (View, error) {
	if deploymentType != TypeDynamic || m == nil || m.dynamic == nil {
		return View{}, &dynamic.CodedError{Code: "dynamic_hosting_disabled", Err: dynamic.ErrDisabled}
	}
	item, err := m.dynamic.Rollback(ctx, name, releaseID)
	if err != nil {
		return View{}, err
	}
	return dynamicView(item), nil
}
