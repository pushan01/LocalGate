package security

import (
	"os"
	"path/filepath"
	"strings"

	"localgate/internal/config"
)

type Decision struct {
	Allowed bool
	Reason  string
	Path    string
	Root    string
}

type Options struct {
	FollowSymlink bool
	AllowNonExist bool
}

func ValidatePath(p string, ws config.Workspace, opt Options) Decision {
	if p == "" {
		return Decision{Allowed: false, Reason: "empty path"}
	}
	if !filepath.IsAbs(p) {
		return Decision{Allowed: false, Reason: "relative_path_not_allowed", Path: filepath.Clean(p)}
	}
	clean := filepath.Clean(p)
	real := abs(clean)
	if opt.FollowSymlink {
		if r, err := filepath.EvalSymlinks(real); err == nil {
			real = r
		} else if opt.AllowNonExist {
			resolved, ok := resolveNonExistentPath(real)
			if ok {
				real = resolved
			} else {
				return Decision{Allowed: false, Reason: "symlink_eval_failed", Path: real}
			}
		} else {
			return Decision{Allowed: false, Reason: "symlink_eval_failed", Path: real}
		}
	}
	realAbs := abs(real)
	// denylist first
	for _, d := range ws.PathDenylist {
		dd := abs(d)
		if opt.FollowSymlink {
			if r, err := filepath.EvalSymlinks(dd); err == nil {
				dd = r
			}
		}
		if within(realAbs, dd) {
			return Decision{Allowed: false, Reason: "denylist", Path: realAbs}
		}
	}
	// must be under one of roots
	for _, r := range ws.Roots {
		rAbs := abs(r)
		if opt.FollowSymlink {
			if rr, err := filepath.EvalSymlinks(rAbs); err == nil {
				rAbs = rr
			}
		}
		if within(realAbs, rAbs) {
			return Decision{Allowed: true, Path: realAbs, Root: rAbs}
		}
	}
	return Decision{Allowed: false, Reason: "outside_roots", Path: realAbs}
}

func abs(p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	a, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return filepath.Clean(a)
}

func resolveNonExistentPath(p string) (string, bool) {
	p = filepath.Clean(p)
	missing := make([]string, 0, 4)
	cur := p
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		missing = append(missing, filepath.Base(cur))
		cur = parent
	}
}

func within(p, root string) bool {
	p = filepath.Clean(p)
	root = filepath.Clean(root)
	// ensure trailing separator on root when prefix comparing
	if !strings.HasSuffix(root, string(os.PathSeparator)) {
		root = root + string(os.PathSeparator)
	}
	if !strings.HasSuffix(p, string(os.PathSeparator)) {
		p = p + string(os.PathSeparator)
	}
	return strings.HasPrefix(p, root)
}
