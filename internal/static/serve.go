package static

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const PublicPathPrefix = "/static/"

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s == nil || s.registry == nil {
		http.Error(w, "static service unavailable", http.StatusServiceUnavailable)
		return
	}
	if !strings.HasPrefix(r.URL.Path, PublicPathPrefix) {
		http.NotFound(w, r)
		return
	}
	remainder := strings.TrimPrefix(r.URL.Path, PublicPathPrefix)
	parts := strings.SplitN(remainder, "/", 2)
	name := parts[0]
	if err := validateSiteName(name); err != nil {
		http.NotFound(w, r)
		return
	}
	site, ok := s.GetSite(name)
	if !ok {
		http.NotFound(w, r)
		return
	}

	siteRoot := filepath.Join(s.root, name)
	rootResolved, err := canonicalExistingPath(siteRoot)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	managedRoot, err := canonicalPathAllowMissing(s.root)
	if err != nil || !withinRoot(rootResolved, managedRoot) {
		http.NotFound(w, r)
		return
	}

	relative := ""
	if len(parts) == 2 {
		relative = filepath.FromSlash(parts[1])
	}
	target := filepath.Join(rootResolved, relative)
	if !withinRoot(target, rootResolved) {
		http.NotFound(w, r)
		return
	}
	target, err = resolveStaticTarget(target, rootResolved, site)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	for key, value := range site.Headers {
		if key != "" {
			w.Header().Set(key, value)
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, target)
}

func resolveStaticTarget(target, siteRoot string, site Site) (string, error) {
	info, err := os.Lstat(target)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("symbolic link not allowed")
		}
		if info.IsDir() {
			index := site.Index
			if index == "" {
				index = "index.html"
			}
			target = filepath.Join(target, index)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	} else if site.SPA {
		index := site.Index
		if index == "" {
			index = "index.html"
		}
		target = filepath.Join(siteRoot, index)
	} else {
		return "", err
	}

	info, err = os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		if err == nil {
			err = errors.New("target is not a regular file")
		}
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	if !withinRoot(resolved, siteRoot) {
		return "", errors.New("target escaped site root")
	}
	return resolved, nil
}
