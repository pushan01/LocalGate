package static

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	deploysnapshot "localgate/internal/deployment/snapshot"
)

const (
	DefaultMaxFiles      = 10_000
	DefaultMaxTotalBytes = int64(50 << 20)
	DefaultMaxFileBytes  = int64(50 << 20)
)

var (
	ErrLimitExceeded       = errors.New("static deployment limit exceeded")
	ErrSourceOverlapsStore = errors.New("static source overlaps managed storage")
)

type Limits struct {
	MaxFiles      int
	MaxTotalBytes int64
	MaxFileBytes  int64
}

type DeployOptions struct {
	Overwrite    bool
	RemoveSource bool
	Access       string
}

type Deployment struct {
	Site            Site
	FileCount       int
	TotalBytes      int64
	SourcePreserved bool
}

type Service struct {
	root     string
	registry *RegistryStore
	limits   Limits
	mu       sync.Mutex
}

func NewService() *Service {
	return NewServiceAt(".localgate/static")
}

func NewServiceAt(root string) *Service {
	return NewServiceAtWithLimits(root, Limits{})
}

func NewServiceAtWithLimits(root string, limits Limits) *Service {
	if !filepath.IsAbs(root) {
		if absolute, err := filepath.Abs(root); err == nil {
			root = absolute
		}
	}
	root = filepath.Clean(root)
	return &Service{
		root:     root,
		registry: NewRegistryStore(filepath.Join(root, "sites.json")),
		limits:   normalizeLimits(limits),
	}
}

func normalizeLimits(limits Limits) Limits {
	if limits.MaxFiles <= 0 {
		limits.MaxFiles = DefaultMaxFiles
	}
	if limits.MaxTotalBytes <= 0 {
		limits.MaxTotalBytes = DefaultMaxTotalBytes
	}
	if limits.MaxFileBytes <= 0 {
		limits.MaxFileBytes = DefaultMaxFileBytes
	}
	return limits
}

func (s *Service) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *Service) Limits() Limits {
	if s == nil {
		return normalizeLimits(Limits{})
	}
	return s.limits
}

func (s *Service) DeploySite(ctx context.Context, name, source string, overwrite bool) (Deployment, error) {
	return s.DeploySiteWithOptions(ctx, name, source, DeployOptions{Overwrite: overwrite})
}

func (s *Service) DeploySiteWithOptions(ctx context.Context, name, source string, options DeployOptions) (Deployment, error) {
	if s == nil || s.registry == nil || s.root == "" {
		return Deployment{}, errors.New("static service is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := contextErr(ctx); err != nil {
		return Deployment{}, err
	}
	if err := validateSiteName(name); err != nil {
		return Deployment{}, err
	}
	if source == "" || !filepath.IsAbs(source) {
		return Deployment{}, errors.New("static source must be an absolute path")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	source, err := canonicalExistingPath(source)
	if err != nil {
		return Deployment{}, fmt.Errorf("resolve static source: %w", err)
	}
	root, err := canonicalPathAllowMissing(s.root)
	if err != nil {
		return Deployment{}, fmt.Errorf("resolve managed static root: %w", err)
	}
	if pathsOverlap(source, root) {
		return Deployment{}, fmt.Errorf("%w: source=%s root=%s", ErrSourceOverlapsStore, source, root)
	}
	if err := contextErr(ctx); err != nil {
		return Deployment{}, err
	}

	info, err := os.Stat(source)
	if err != nil {
		return Deployment{}, fmt.Errorf("stat static source: %w", err)
	}
	if !info.IsDir() {
		return Deployment{}, errors.New("static source must be a directory")
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return Deployment{}, err
	}
	if err := contextErr(ctx); err != nil {
		return Deployment{}, err
	}

	registry, err := s.registry.Load()
	if err != nil {
		return Deployment{}, err
	}
	destination := filepath.Join(s.root, name)
	if !withinRoot(destination, s.root) {
		return Deployment{}, errors.New("invalid static destination")
	}
	previous, registered := registry.Sites[name]
	if registered && !options.Overwrite {
		return Deployment{}, errors.New("site already exists")
	}
	if _, err := os.Stat(destination); err == nil && !options.Overwrite {
		return Deployment{}, errors.New("site already exists")
	} else if err != nil && !os.IsNotExist(err) {
		return Deployment{}, err
	}

	stage, err := os.MkdirTemp(s.root, ".deploy-"+name+"-")
	if err != nil {
		return Deployment{}, err
	}
	defer os.RemoveAll(stage)
	stats, err := copyDirContents(ctx, source, stage, s.limits)
	if err != nil {
		return Deployment{}, err
	}
	indexFile, err := detectSiteIndex(stage)
	if err != nil {
		return Deployment{}, err
	}
	if err := contextErr(ctx); err != nil {
		return Deployment{}, err
	}

	sourceStash := ""
	restoreSource := func() error {
		if sourceStash == "" {
			return nil
		}
		if _, err := os.Lstat(source); err == nil {
			return errors.New("restore static source: original path already exists")
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("restore static source: inspect original path: %w", err)
		}
		if err := os.Rename(sourceStash, source); err != nil {
			return fmt.Errorf("restore static source: %w", err)
		}
		sourceStash = ""
		return nil
	}
	if options.RemoveSource {
		currentInfo, err := os.Stat(source)
		if err != nil {
			return Deployment{}, fmt.Errorf("revalidate static source before removal: %w", err)
		}
		if !os.SameFile(info, currentInfo) {
			return Deployment{}, errors.New("static source changed during deployment")
		}
		stash, err := unusedSourceStashPath(source)
		if err != nil {
			return Deployment{}, err
		}
		if err := os.Rename(source, stash); err != nil {
			return Deployment{}, fmt.Errorf("stage static source removal: %w", err)
		}
		sourceStash = stash
	}

	backup := ""
	if _, err := os.Stat(destination); err == nil {
		backup = filepath.Join(s.root, fmt.Sprintf(".backup-%s-%d", name, time.Now().UnixNano()))
		if err := os.Rename(destination, backup); err != nil {
			restoreErr := restoreSource()
			return Deployment{}, errors.Join(fmt.Errorf("backup existing site: %w", err), restoreErr)
		}
	}
	rollback := func() error {
		var rollbackErrors []error
		if err := os.RemoveAll(destination); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("remove activated site during rollback: %w", err))
		}
		if backup != "" {
			if err := os.Rename(backup, destination); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("restore previous site: %w", err))
			}
		}
		if err := restoreSource(); err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
		return errors.Join(rollbackErrors...)
	}
	if err := contextErr(ctx); err != nil {
		return Deployment{}, errors.Join(err, rollback())
	}
	if err := os.Rename(stage, destination); err != nil {
		return Deployment{}, errors.Join(fmt.Errorf("activate static site: %w", err), rollback())
	}
	if err := contextErr(ctx); err != nil {
		return Deployment{}, errors.Join(err, rollback())
	}

	now := Now()
	createdAt := now
	if registered && previous.CreatedAt != "" {
		createdAt = previous.CreatedAt
	}
	site := Site{
		Path:      destination,
		Access:    options.Access,
		SPA:       true,
		Index:     indexFile,
		Headers:   map[string]string{},
		Rewrite:   nil,
		CreatedAt: createdAt,
		UpdatedAt: now,
	}
	registry.Sites[name] = site
	if err := contextErr(ctx); err != nil {
		return Deployment{}, errors.Join(err, rollback())
	}
	if err := s.registry.Save(registry); err != nil {
		return Deployment{}, errors.Join(err, rollback())
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}

	deployment := Deployment{
		Site:            site,
		FileCount:       stats.FileCount,
		TotalBytes:      stats.TotalBytes,
		SourcePreserved: !options.RemoveSource,
	}
	if sourceStash != "" {
		if err := os.RemoveAll(sourceStash); err != nil {
			return deployment, fmt.Errorf("remove static source after deployment: %w", err)
		}
		sourceStash = ""
	}
	return deployment, nil
}

func detectSiteIndex(root string) (string, error) {
	indexPath := filepath.Join(root, "index.html")
	if info, err := os.Lstat(indexPath); err == nil {
		if info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return "index.html", nil
		}
		return "", errors.New("static index.html must be a regular file")
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect static index.html: %w", err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("read static site root: %w", err)
	}
	candidates := make([]string, 0, 1)
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext == ".html" || ext == ".htm" {
			candidates = append(candidates, entry.Name())
		}
	}

	switch len(candidates) {
	case 1:
		return candidates[0], nil
	case 0:
		return "", errors.New("static site root must contain index.html or exactly one HTML entry file")
	default:
		return "", errors.New("static site root contains multiple HTML files but no index.html")
	}
}

func unusedSourceStashPath(source string) (string, error) {
	parent := filepath.Dir(source)
	base := filepath.Base(source)
	for attempt := 0; attempt < 100; attempt++ {
		candidate := filepath.Join(parent, fmt.Sprintf(".localgate-source-%s-%d-%d", base, time.Now().UnixNano(), attempt))
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", fmt.Errorf("inspect static source removal staging path: %w", err)
		}
	}
	return "", errors.New("unable to allocate static source removal staging path")
}

func (s *Service) DeleteSite(name string) error {
	if s == nil || s.registry == nil {
		return errors.New("static service is not initialized")
	}
	if err := validateSiteName(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	registry, err := s.registry.Load()
	if err != nil {
		return err
	}
	if _, ok := registry.Sites[name]; !ok {
		return errors.New("site not found")
	}
	destination := filepath.Join(s.root, name)
	if !withinRoot(destination, s.root) {
		return errors.New("invalid static destination")
	}
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	delete(registry.Sites, name)
	return s.registry.Save(registry)
}

func (s *Service) GetSite(name string) (Site, bool) {
	if s == nil || s.registry == nil || validateSiteName(name) != nil {
		return Site{}, false
	}
	registry, err := s.registry.Load()
	if err != nil {
		return Site{}, false
	}
	site, ok := registry.Sites[name]
	return site, ok
}

func (s *Service) SetAccess(name, access string) (Site, error) {
	if s == nil || s.registry == nil {
		return Site{}, errors.New("static service is not initialized")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	registry, err := s.registry.Load()
	if err != nil {
		return Site{}, err
	}
	site, ok := registry.Sites[name]
	if !ok {
		return Site{}, errors.New("site not found")
	}
	site.Access = access
	site.UpdatedAt = Now()
	registry.Sites[name] = site
	if err := s.registry.Save(registry); err != nil {
		return Site{}, err
	}
	return site, nil
}

func (s *Service) ListSites() (*Registry, error) {
	if s == nil || s.registry == nil {
		return nil, errors.New("static service is not initialized")
	}
	return s.registry.Load()
}

func validateSiteName(name string) error {
	if len(name) < 1 || len(name) > 64 {
		return errors.New("site name must contain 1 to 64 characters")
	}
	for i, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			if i == 0 && (r == '-' || r == '_') {
				return errors.New("site name must start with a letter or number")
			}
			continue
		}
		return errors.New("site name may contain only letters, numbers, hyphens, and underscores")
	}
	return nil
}

type copyStats struct {
	FileCount  int
	TotalBytes int64
}

func copyDirContents(ctx context.Context, source, destination string, limits Limits) (copyStats, error) {
	limits = normalizeLimits(limits)
	stats, err := deploysnapshot.Copy(ctx, source, destination, deploysnapshot.Limits{
		MaxFiles: limits.MaxFiles, MaxTotalBytes: limits.MaxTotalBytes, MaxFileBytes: limits.MaxFileBytes,
	}, deploysnapshot.Options{Mode: func(mode os.FileMode, _ bool) os.FileMode { return mode.Perm() }})
	if err != nil {
		if errors.Is(err, deploysnapshot.ErrFileLimit) || errors.Is(err, deploysnapshot.ErrSizeLimit) {
			return copyStats{}, fmt.Errorf("%w: %v", ErrLimitExceeded, err)
		}
		return copyStats{}, err
	}
	return copyStats{FileCount: stats.FileCount, TotalBytes: stats.TotalBytes}, nil
}

func contextErr(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func canonicalExistingPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(filepath.Clean(absolute))
}

func canonicalPathAllowMissing(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	missing := []string{}
	current := absolute
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func pathsOverlap(first, second string) bool { return deploysnapshot.PathsOverlap(first, second) }
func withinRoot(path, root string) bool      { return deploysnapshot.WithinRoot(path, root) }
