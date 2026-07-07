package deployment

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"localgate/internal/config"
	"localgate/internal/dynamic"
	"localgate/internal/security"
	"localgate/internal/static"
)

const (
	TypeStatic    = "static"
	TypeDynamic   = "dynamic"
	AccessPublic  = "public"
	AccessPrivate = "private"
	listHostLabel = "deploys"
)

var (
	ErrUnsafeSourceRemoval    = errors.New("unsafe source removal")
	ErrAppHostingDisabled     = errors.New("app hosting is not configured")
	ErrDeploymentNameConflict = errors.New("deployment name is already used by another deployment type")
	ErrDeploymentNameBlocked  = errors.New("deployment name is unavailable for app routing")
)

type Manager struct {
	static         *static.Service
	dynamic        *dynamic.Service
	publicBaseURL  *url.URL
	privateBaseURL *url.URL
	publicDomain   string
	privateDomain  string
	defaultAccess  string
	staticSuffix   string
	dynamicSuffix  string
	reserved       map[string]struct{}
	controls       map[string]struct{}
}

type DeployOptions struct {
	Overwrite        bool
	PreserveSource   bool
	Access           string
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
	Access          string
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
	Access         string
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

func NewManager(staticService *static.Service, dynamicService *dynamic.Service, appBaseURL string) *Manager {
	manager := &Manager{static: staticService, dynamic: dynamicService}
	if dynamicService != nil {
		manager.dynamicSuffix = normalizeDomainSuffix(dynamicService.Config().DomainSuffix)
	}
	if parsed := parseAppBaseURL(appBaseURL); parsed != nil {
		manager.publicBaseURL = parsed
		manager.privateBaseURL = cloneURL(parsed)
		manager.publicDomain = strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
		manager.privateDomain = manager.publicDomain
		manager.defaultAccess = AccessPublic
	}
	return manager
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

func (m *Manager) AppHostingEnabled() bool {
	return m != nil && m.publicBaseURL != nil && m.privateBaseURL != nil && m.publicDomain != "" && m.privateDomain != ""
}

func (m *Manager) ConfigureAppHosting(publicBaseURL, privateBaseURL, defaultAccess string) {
	m.ConfigureAppHostingWithSuffixes(publicBaseURL, privateBaseURL, defaultAccess, "", "")
}

func (m *Manager) ConfigureAppHostingWithSuffixes(publicBaseURL, privateBaseURL, defaultAccess, staticSuffix, dynamicSuffix string) {
	if m == nil {
		return
	}
	m.publicBaseURL = parseAppBaseURL(publicBaseURL)
	m.privateBaseURL = parseAppBaseURL(privateBaseURL)
	m.publicDomain = ""
	m.privateDomain = ""
	if m.publicBaseURL != nil {
		m.publicDomain = strings.ToLower(strings.TrimSuffix(m.publicBaseURL.Hostname(), "."))
	}
	if m.privateBaseURL != nil {
		m.privateDomain = strings.ToLower(strings.TrimSuffix(m.privateBaseURL.Hostname(), "."))
	}
	m.defaultAccess = normalizeAccess(defaultAccess, AccessPrivate)
	m.staticSuffix = normalizeDomainSuffix(staticSuffix)
	m.dynamicSuffix = normalizeDomainSuffix(dynamicSuffix)
}

func (m *Manager) ConfigureAppRouting(controlBaseURL string, reservedNames []string) {
	if m == nil {
		return
	}
	m.controls = make(map[string]struct{}, 1)
	if parsed := parseAppBaseURL(controlBaseURL); parsed != nil {
		host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
		if host != "" {
			m.controls[host] = struct{}{}
		}
	}
	m.reserved = make(map[string]struct{}, len(reservedNames))
	for _, name := range reservedNames {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" {
			m.reserved[name] = struct{}{}
		}
	}
}

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
	if !m.AppHostingEnabled() {
		return DeployResult{}, ErrAppHostingDisabled
	}
	if err := m.validateAppName(TypeStatic, request.Name); err != nil {
		return DeployResult{}, err
	}
	if m.dynamic != nil {
		if _, err := m.dynamic.Get(request.Name); err == nil {
			return DeployResult{}, fmt.Errorf("%w: %s is already a dynamic deployment", ErrDeploymentNameConflict, request.Name)
		} else if !errors.Is(err, dynamic.ErrNotFound) {
			return DeployResult{}, err
		}
	}
	if err := m.validateHostnameAvailable(TypeStatic, request.Name); err != nil {
		return DeployResult{}, err
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
		Access: normalizeAccess(request.Options.Access, m.defaultAccess),
	})
	if err != nil {
		return DeployResult{}, err
	}
	access := normalizeAccess(result.Site.Access, m.defaultAccess)
	hostname, publicURL := m.appLocation(TypeStatic, request.Name, access)
	return DeployResult{
		Status: "deployed", Type: TypeStatic, Site: request.Name, URL: publicURL, Hostname: hostname, Access: access,
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
	if err := m.validateAppName(TypeDynamic, request.Name); err != nil {
		return DeployResult{}, err
	}
	if m.static != nil {
		if _, exists := m.static.GetSite(request.Name); exists {
			return DeployResult{}, fmt.Errorf("%w: %s is already a static deployment", ErrDeploymentNameConflict, request.Name)
		}
	}
	if err := m.validateHostnameAvailable(TypeDynamic, request.Name); err != nil {
		return DeployResult{}, err
	}
	result, err := m.dynamic.Deploy(ctx, dynamic.DeployRequest{
		WorkspaceID: request.Workspace.ID,
		Name:        request.Name, Source: request.Source, Runtime: *request.Runtime,
		Options: dynamic.DeployOptions{
			Overwrite: request.Options.Overwrite, PreserveSource: request.Options.PreserveSource,
			Access:     normalizeAccess(request.Options.Access, m.defaultAccess),
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
		URL: result.Deployment.URL, Hostname: result.Deployment.Hostname, Access: result.Deployment.Access,
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
		access := normalizeAccess(site.Access, AccessPublic)
		hostname, publicURL := m.appLocation(TypeStatic, name, access)
		out = append(out, View{Type: TypeStatic, Site: name, URL: publicURL, Hostname: hostname, Access: access, Status: "active", Index: site.Index, SPA: site.SPA, CreatedAt: site.CreatedAt, UpdatedAt: site.UpdatedAt})
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

func (m *Manager) appLocation(deploymentType, name, access string) (string, string) {
	if !m.AppHostingEnabled() {
		return "", ""
	}
	base := m.publicBaseURL
	if normalizeAccess(access, m.defaultAccess) == AccessPrivate {
		base = m.privateBaseURL
	}
	if base == nil {
		return "", ""
	}
	hostname := m.hostLabel(deploymentType, name) + "." + strings.ToLower(strings.TrimSuffix(base.Hostname(), "."))
	location := cloneURL(base)
	location.Host = hostname
	if port := base.Port(); port != "" {
		location.Host = net.JoinHostPort(hostname, port)
	}
	location.Path = "/"
	location.RawPath = ""
	return hostname, location.String()
}

func (m *Manager) validateAppName(deploymentType, name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == listHostLabel {
		return fmt.Errorf("%w: %s is reserved for the deployment list", ErrDeploymentNameBlocked, name)
	}
	if _, blocked := m.reserved[name]; blocked {
		return fmt.Errorf("%w: %s is reserved", ErrDeploymentNameBlocked, name)
	}
	label := m.hostLabel(deploymentType, name)
	if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return fmt.Errorf("%w: %s with its domain suffix is not a valid DNS label", ErrDeploymentNameBlocked, name)
	}
	if m.AppHostingEnabled() {
		for _, domain := range []string{m.publicDomain, m.privateDomain} {
			hostname := label + "." + domain
			if _, blocked := m.controls[hostname]; blocked {
				return fmt.Errorf("%w: %s conflicts with a control host", ErrDeploymentNameBlocked, name)
			}
		}
	}
	return nil
}

func (m *Manager) validateHostnameAvailable(deploymentType, name string) error {
	label := m.hostLabel(deploymentType, name)
	switch deploymentType {
	case TypeStatic:
		if m.dynamic != nil {
			for _, item := range m.dynamic.List() {
				if label == m.hostLabel(TypeDynamic, item.Name) {
					return fmt.Errorf("%w: hostname label %s is already used by dynamic deployment %s", ErrDeploymentNameConflict, label, item.Name)
				}
			}
		}
	case TypeDynamic:
		if m.static != nil {
			registry, err := m.static.ListSites()
			if err != nil {
				return err
			}
			for staticName := range registry.Sites {
				if label == m.hostLabel(TypeStatic, staticName) {
					return fmt.Errorf("%w: hostname label %s is already used by static deployment %s", ErrDeploymentNameConflict, label, staticName)
				}
			}
		}
	}
	return nil
}

func (m *Manager) hostLabel(deploymentType, name string) string {
	suffix := m.staticSuffix
	if deploymentType == TypeDynamic {
		suffix = m.dynamicSuffix
	}
	return strings.ToLower(strings.TrimSpace(name)) + suffix
}

func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if m == nil || !m.AppHostingEnabled() {
		http.NotFound(w, r)
		return
	}
	host, ok := normalizeHost(r.Host)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if host == listHostLabel+"."+m.publicDomain {
		m.serveAccess(AccessPublic, w, r)
		return
	}
	if host == listHostLabel+"."+m.privateDomain {
		m.serveAccess(AccessPrivate, w, r)
		return
	}
	candidates := []struct {
		access string
		domain string
	}{
		{access: AccessPublic, domain: m.publicDomain},
		{access: AccessPrivate, domain: m.privateDomain},
	}
	if len(candidates[1].domain) > len(candidates[0].domain) {
		candidates[0], candidates[1] = candidates[1], candidates[0]
	}
	for _, candidate := range candidates {
		if strings.HasSuffix(host, "."+candidate.domain) {
			m.serveDeploymentHost(host, candidate.domain, candidate.access, w, r)
			return
		}
	}
	http.NotFound(w, r)
}

func (m *Manager) HandlerForAccess(access string) http.Handler {
	var ok bool
	access, ok = strictAccess(access)
	if !ok {
		return http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.serveAccess(access, w, r)
	})
}

func (m *Manager) serveAccess(access string, w http.ResponseWriter, r *http.Request) {
	if m == nil || !m.AppHostingEnabled() || (access != AccessPublic && access != AccessPrivate) {
		http.NotFound(w, r)
		return
	}
	host, ok := normalizeHost(r.Host)
	if !ok {
		http.NotFound(w, r)
		return
	}
	domain := m.publicDomain
	if access == AccessPrivate {
		domain = m.privateDomain
	}
	if host == listHostLabel+"."+domain {
		m.serveDeploymentList(access, w, r)
		return
	}
	m.serveDeploymentHost(host, domain, access, w, r)
}

func (m *Manager) serveDeploymentHost(host, domain, access string, w http.ResponseWriter, r *http.Request) {
	staticName, staticCandidate := resolveHostForAccess(host, domain, m.staticSuffix)
	dynamicName, dynamicCandidate := resolveHostForAccess(host, domain, m.dynamicSuffix)
	staticSite, staticExists := m.staticSite(staticName)
	staticExists = staticCandidate && staticExists
	if staticExists && normalizeAccess(staticSite.Access, AccessPublic) != access {
		staticExists = false
	}
	dynamicExists := false
	if dynamicCandidate && m.dynamic != nil {
		if item, err := m.dynamic.Get(dynamicName); err == nil {
			dynamicExists = item.Access == access
		} else if !errors.Is(err, dynamic.ErrNotFound) {
			http.Error(w, "deployment lookup failed", http.StatusInternalServerError)
			return
		}
	}
	if staticExists == dynamicExists {
		http.NotFound(w, r)
		return
	}
	if staticExists {
		m.static.ServeSite(staticName, w, r)
		return
	}
	m.dynamic.ServeHTTP(w, r)
}

func resolveHostForAccess(host, domain, suffix string) (string, bool) {
	if domain == "" || !strings.HasSuffix(host, "."+domain) {
		return "", false
	}
	label := strings.TrimSuffix(host, "."+domain)
	if label == "" || strings.Contains(label, ".") || !strings.HasSuffix(label, suffix) {
		return "", false
	}
	name := strings.TrimSuffix(label, suffix)
	if name == "" {
		return "", false
	}
	return name, true
}

func parseAppBaseURL(raw string) *url.URL {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil
	}
	parsed.Path = ""
	parsed.RawPath = ""
	return parsed
}

func cloneURL(value *url.URL) *url.URL {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func normalizeAccess(access, fallback string) string {
	access = strings.ToLower(strings.TrimSpace(access))
	if access == AccessPublic || access == AccessPrivate {
		return access
	}
	if strings.ToLower(strings.TrimSpace(fallback)) == AccessPrivate {
		return AccessPrivate
	}
	return AccessPublic
}

func normalizeDomainSuffix(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func strictAccess(access string) (string, bool) {
	access = strings.ToLower(strings.TrimSpace(access))
	if access != AccessPublic && access != AccessPrivate {
		return "", false
	}
	return access, true
}

func (m *Manager) staticSite(name string) (static.Site, bool) {
	if m == nil || m.static == nil {
		return static.Site{}, false
	}
	return m.static.GetSite(name)
}

func normalizeHost(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	host := raw
	if parsed, _, err := net.SplitHostPort(raw); err == nil {
		host = parsed
	} else if strings.Contains(raw, ":") {
		return "", false
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return "", false
	}
	return host, true
}

func dynamicView(item dynamic.View) View {
	return View{
		Type: TypeDynamic, Site: item.Name, URL: item.URL, Status: item.Status,
		Hostname: item.Hostname, Access: item.Access, Health: item.Health, ReleaseID: item.ReleaseID,
		RestartCount: item.RestartCount, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		DesiredState: item.DesiredState, WorkingDir: item.WorkingDir, HealthPath: item.HealthPath,
		RestartPolicy: item.RestartPolicy, LastError: item.LastError, LastWarning: item.LastWarning,
		CleanupPending: item.CleanupPending, FileCount: item.FileCount, TotalBytes: item.TotalBytes,
		EnvKeys: append([]string(nil), item.EnvKeys...),
	}
}

func (m *Manager) SetAccess(ctx context.Context, deploymentType, name, access string) (View, error) {
	access = strings.ToLower(strings.TrimSpace(access))
	if access != AccessPublic && access != AccessPrivate {
		return View{}, errors.New("access must be public or private")
	}
	switch deploymentType {
	case TypeStatic:
		site, err := m.static.SetAccess(name, access)
		if err != nil {
			return View{}, err
		}
		hostname, publicURL := m.appLocation(TypeStatic, name, access)
		return View{Type: TypeStatic, Site: name, URL: publicURL, Hostname: hostname, Access: access, Status: "active", Index: site.Index, SPA: site.SPA, CreatedAt: site.CreatedAt, UpdatedAt: site.UpdatedAt}, nil
	case TypeDynamic:
		item, err := m.dynamic.SetAccess(ctx, name, access)
		if err != nil {
			return View{}, err
		}
		return dynamicView(item), nil
	default:
		return View{}, errors.New("unsupported deployment type")
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
