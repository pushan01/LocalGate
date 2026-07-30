package filebrowser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"localgate/internal/config"
	"localgate/internal/security"
)

const (
	gitCommandTimeout = 10 * time.Second
	gitStatusMaxBytes = 4 << 20
	gitDiffMaxBytes   = 2 << 20
	gitStatusMaxItems = 5000
)

var (
	ErrGitUnavailable           = errors.New("git is unavailable")
	ErrGitRepositoryNotFound    = errors.New("git repository not found")
	ErrGitRepositoryOutsideRoot = errors.New("git repository is outside workspace root")
	ErrGitOutputTooLarge        = errors.New("git output too large")
	ErrGitTimeout               = errors.New("git command timed out")
)

type gitRepository struct {
	root     string
	rootPath string
	pathspec string
}

func (s *Service) GitStatus(ctx context.Context, req PathRequest) (GitStatusResult, error) {
	resolved, err := s.resolve(req, true)
	if err != nil {
		return GitStatusResult{}, err
	}
	result := GitStatusResult{
		WorkspaceID: resolved.workspaceID,
		RootID:      resolved.rootID,
		Path:        filepath.ToSlash(resolved.relPath),
		Entries:     []GitStatusEntry{},
	}
	repo, err := s.findGitRepository(ctx, resolved)
	if errors.Is(err, ErrGitRepositoryNotFound) {
		return result, nil
	}
	if err != nil {
		return GitStatusResult{}, err
	}
	args := []string{"status", "--porcelain=v2", "-z", "--branch", "--untracked-files=all", "--ignored=no"}
	if repo.pathspec != "" {
		args = append(args, "--", repo.pathspec)
	}
	out, _, err := s.runGit(ctx, repo.root, gitStatusMaxBytes, args...)
	if err != nil {
		return GitStatusResult{}, err
	}
	parsed, err := parseGitStatus(out)
	if err != nil {
		return GitStatusResult{}, err
	}
	result.Repository = true
	result.RepositoryPath = repo.rootPath
	result.OID = parsed.OID
	result.Branch = parsed.Branch
	result.Detached = parsed.Detached
	result.Upstream = parsed.Upstream
	result.Ahead = parsed.Ahead
	result.Behind = parsed.Behind
	for i := range parsed.Entries {
		parsed.Entries[i].Path = joinGitRootPath(repo.rootPath, parsed.Entries[i].Path)
		if parsed.Entries[i].OriginalPath != "" {
			parsed.Entries[i].OriginalPath = joinGitRootPath(repo.rootPath, parsed.Entries[i].OriginalPath)
		}
	}
	if len(parsed.Entries) > gitStatusMaxItems {
		result.Entries = parsed.Entries[:gitStatusMaxItems]
		result.Truncated = true
	} else {
		result.Entries = parsed.Entries
	}
	return result, nil
}

func (s *Service) GitDiff(ctx context.Context, req PathRequest, staged bool) (GitDiffResult, error) {
	resolved, err := s.resolveGitTarget(req)
	if err != nil {
		return GitDiffResult{}, err
	}
	repo, err := s.findGitRepository(ctx, resolved)
	if err != nil {
		return GitDiffResult{}, err
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "--unified=3"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--")
	if repo.pathspec != "" {
		args = append(args, repo.pathspec)
	}
	out, _, err := s.runGit(ctx, repo.root, gitDiffMaxBytes, args...)
	if err != nil {
		return GitDiffResult{}, err
	}
	return GitDiffResult{
		WorkspaceID:    resolved.workspaceID,
		RootID:         resolved.rootID,
		Path:           filepath.ToSlash(resolved.relPath),
		RepositoryPath: repo.rootPath,
		Staged:         staged,
		Diff:           string(out),
		Bytes:          len(out),
	}, nil
}

func (s *Service) resolveGitTarget(req PathRequest) (resolvedPath, error) {
	resolved, err := s.resolve(req, false)
	if err == nil || strings.TrimSpace(req.Path) == "" {
		return resolved, err
	}
	if !s.Enabled() {
		return resolvedPath{}, ErrDisabled
	}
	workspaceID := strings.TrimSpace(req.WorkspaceID)
	if workspaceID == "" {
		return resolvedPath{}, ErrWorkspaceNotFound
	}
	ws, ok := s.workspaces[workspaceID]
	if !ok {
		return resolvedPath{}, ErrWorkspaceNotFound
	}
	rootID := strings.TrimSpace(req.RootID)
	if rootID == "" {
		rootID = "0"
	}
	rootIndex, parseErr := strconv.Atoi(rootID)
	if parseErr != nil || rootIndex < 0 || rootIndex >= len(ws.Roots) {
		return resolvedPath{}, ErrRootNotFound
	}
	root := filepath.Clean(ws.Roots[rootIndex])
	rel, cleanErr := cleanRelative(req.Path)
	if cleanErr != nil || rel == "" {
		return resolvedPath{}, fmt.Errorf("%w: invalid Git target", ErrPathNotAllowed)
	}
	if s.excluded(rel) || (!s.cfg.ShowHiddenFiles && hasHiddenComponent(rel)) {
		return resolvedPath{}, ErrPathExcluded
	}
	logical := filepath.Join(root, rel)
	if _, lstatErr := os.Lstat(logical); lstatErr == nil || !errors.Is(lstatErr, os.ErrNotExist) {
		return resolvedPath{}, err
	}
	parent := filepath.Dir(logical)
	decision := security.ValidatePath(parent, config.Workspace{ID: ws.ID, Roots: []string{root}, PathDenylist: ws.PathDenylist}, security.Options{FollowSymlink: true, AllowNonExist: false})
	if !decision.Allowed {
		return resolvedPath{}, fmt.Errorf("%w: parent %s", ErrPathNotAllowed, decision.Reason)
	}
	info, statErr := os.Stat(decision.Path)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return resolvedPath{}, ErrFileNotFound
		}
		return resolvedPath{}, statErr
	}
	if !info.IsDir() {
		return resolvedPath{}, ErrNotDirectory
	}
	return resolvedPath{
		workspaceID: workspaceID,
		rootID:      rootID,
		root:        root,
		relPath:     rel,
		absPath:     filepath.Join(decision.Path, filepath.Base(logical)),
	}, nil
}

func (s *Service) findGitRepository(ctx context.Context, resolved resolvedPath) (gitRepository, error) {
	start := resolved.absPath
	if info, err := os.Stat(start); errors.Is(err, os.ErrNotExist) {
		start = filepath.Dir(start)
	} else if err != nil {
		return gitRepository{}, err
	} else if !info.IsDir() {
		start = filepath.Dir(start)
	}
	out, stderr, err := s.runGit(ctx, start, 64<<10, "rev-parse", "--show-toplevel")
	if err != nil {
		if errors.Is(err, ErrGitUnavailable) || errors.Is(err, ErrGitOutputTooLarge) || errors.Is(err, ErrGitTimeout) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return gitRepository{}, err
		}
		if strings.Contains(strings.ToLower(string(stderr)), "not a git repository") {
			return gitRepository{}, ErrGitRepositoryNotFound
		}
		return gitRepository{}, err
	}
	repoRoot := strings.TrimSpace(string(out))
	if repoRoot == "" || !filepath.IsAbs(repoRoot) {
		return gitRepository{}, ErrGitRepositoryNotFound
	}
	repoRoot, err = filepath.EvalSymlinks(filepath.Clean(repoRoot))
	if err != nil {
		return gitRepository{}, ErrGitRepositoryNotFound
	}
	root, err := filepath.EvalSymlinks(filepath.Clean(resolved.root))
	if err != nil {
		return gitRepository{}, err
	}
	repoRel, err := filepath.Rel(root, repoRoot)
	if err != nil || pathEscapes(repoRel) {
		return gitRepository{}, ErrGitRepositoryOutsideRoot
	}
	metadata, _, err := s.runGit(ctx, repoRoot, 64<<10, "rev-parse", "--absolute-git-dir", "--git-common-dir")
	if err != nil {
		return gitRepository{}, err
	}
	metadataPaths := strings.Split(strings.TrimSpace(string(metadata)), "\n")
	if len(metadataPaths) != 2 {
		return gitRepository{}, fmt.Errorf("git metadata discovery returned an unexpected result")
	}
	for _, metadataPath := range metadataPaths {
		metadataPath = strings.TrimSpace(metadataPath)
		if !filepath.IsAbs(metadataPath) {
			metadataPath = filepath.Join(repoRoot, metadataPath)
		}
		canonical, evalErr := filepath.EvalSymlinks(filepath.Clean(metadataPath))
		if evalErr != nil {
			return gitRepository{}, evalErr
		}
		metadataRel, relErr := filepath.Rel(root, canonical)
		if relErr != nil || pathEscapes(metadataRel) {
			return gitRepository{}, ErrGitRepositoryOutsideRoot
		}
	}
	target, err := filepath.EvalSymlinks(filepath.Clean(resolved.absPath))
	if errors.Is(err, os.ErrNotExist) {
		parent, parentErr := filepath.EvalSymlinks(filepath.Dir(filepath.Clean(resolved.absPath)))
		if parentErr != nil {
			return gitRepository{}, parentErr
		}
		target = filepath.Join(parent, filepath.Base(resolved.absPath))
	} else if err != nil {
		return gitRepository{}, err
	}
	pathspec, err := filepath.Rel(repoRoot, target)
	if err != nil || pathEscapes(pathspec) {
		return gitRepository{}, ErrGitRepositoryOutsideRoot
	}
	if pathspec == "." {
		pathspec = ""
	}
	return gitRepository{
		root:     repoRoot,
		rootPath: cleanSlashDot(repoRel),
		pathspec: filepath.ToSlash(pathspec),
	}, nil
}

func (s *Service) runGit(ctx context.Context, directory string, maxBytes int, args ...string) ([]byte, []byte, error) {
	gitPath, err := trustedGitExecutable()
	if err != nil {
		return nil, nil, err
	}
	runCtx, cancel := context.WithTimeout(ctx, gitCommandTimeout)
	defer cancel()
	baseArgs := []string{
		"-c", "core.fsmonitor=false",
		"-c", "core.untrackedCache=false",
		"-c", "core.pager=cat",
		"-c", "pager.diff=false",
		"--no-pager",
		"-C", directory,
	}
	cmd := exec.CommandContext(runCtx, gitPath, append(baseArgs, args...)...)
	cmd.Env = []string{
		"LC_ALL=C",
		"LANG=C",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_SYSTEM=" + os.DevNull,
		"GIT_PAGER=cat",
		"PAGER=cat",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_EXTERNAL_DIFF=",
	}
	stdout := newLimitedBuffer(maxBytes)
	stderr := newLimitedBuffer(64 << 10)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		return nil, stderr.Bytes(), ctx.Err()
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return nil, stderr.Bytes(), ErrGitTimeout
	}
	if stdout.tooLarge || stderr.tooLarge {
		return nil, stderr.Bytes(), ErrGitOutputTooLarge
	}
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return stdout.Bytes(), stderr.Bytes(), fmt.Errorf("git command failed: %s", message)
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}

func trustedGitExecutable() (string, error) {
	candidates := []string{"/usr/bin/git", "/bin/git", "/usr/local/bin/git", "/opt/homebrew/bin/git"}
	if located, err := exec.LookPath("git"); err == nil {
		candidates = append(candidates, located)
	}
	allowedDirectories := map[string]struct{}{
		"/usr/bin":          {},
		"/bin":              {},
		"/usr/local/bin":    {},
		"/opt/homebrew/bin": {},
	}
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		if !filepath.IsAbs(candidate) {
			absolute, err := filepath.Abs(candidate)
			if err != nil {
				continue
			}
			candidate = absolute
		}
		canonical, err := filepath.EvalSymlinks(filepath.Clean(candidate))
		if err != nil {
			continue
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		if _, ok := allowedDirectories[filepath.Dir(canonical)]; !ok {
			continue
		}
		info, err := os.Stat(canonical)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return canonical, nil
		}
	}
	return "", ErrGitUnavailable
}

type parsedGitStatus struct {
	OID      string
	Branch   string
	Detached bool
	Upstream string
	Ahead    int
	Behind   int
	Entries  []GitStatusEntry
}

func parseGitStatus(data []byte) (parsedGitStatus, error) {
	result := parsedGitStatus{Entries: []GitStatusEntry{}}
	records := bytes.Split(data, []byte{0})
	for i := 0; i < len(records); i++ {
		record := string(records[i])
		if record == "" {
			continue
		}
		switch {
		case strings.HasPrefix(record, "# branch.oid "):
			result.OID = strings.TrimPrefix(record, "# branch.oid ")
		case strings.HasPrefix(record, "# branch.head "):
			result.Branch = strings.TrimPrefix(record, "# branch.head ")
			result.Detached = result.Branch == "(detached)"
		case strings.HasPrefix(record, "# branch.upstream "):
			result.Upstream = strings.TrimPrefix(record, "# branch.upstream ")
		case strings.HasPrefix(record, "# branch.ab "):
			parts := strings.Fields(strings.TrimPrefix(record, "# branch.ab "))
			if len(parts) == 2 {
				result.Ahead, _ = strconv.Atoi(strings.TrimPrefix(parts[0], "+"))
				result.Behind, _ = strconv.Atoi(strings.TrimPrefix(parts[1], "-"))
			}
		case strings.HasPrefix(record, "1 "):
			parts := strings.SplitN(record, " ", 9)
			if len(parts) != 9 || len(parts[1]) != 2 {
				return parsedGitStatus{}, fmt.Errorf("invalid git status record")
			}
			result.Entries = append(result.Entries, gitStatusEntry(parts[8], "", parts[1], ""))
		case strings.HasPrefix(record, "2 "):
			parts := strings.SplitN(record, " ", 10)
			if len(parts) != 10 || len(parts[1]) != 2 || i+1 >= len(records) {
				return parsedGitStatus{}, fmt.Errorf("invalid git rename status record")
			}
			i++
			result.Entries = append(result.Entries, gitStatusEntry(parts[9], string(records[i]), parts[1], parts[8]))
		case strings.HasPrefix(record, "u "):
			parts := strings.SplitN(record, " ", 11)
			if len(parts) != 11 || len(parts[1]) != 2 {
				return parsedGitStatus{}, fmt.Errorf("invalid git conflict status record")
			}
			result.Entries = append(result.Entries, gitStatusEntry(parts[10], "", parts[1], "U"))
		case strings.HasPrefix(record, "? "):
			result.Entries = append(result.Entries, GitStatusEntry{Path: strings.TrimPrefix(record, "? "), IndexStatus: "?", WorktreeStatus: "?", Kind: "untracked"})
		}
	}
	return result, nil
}

func gitStatusEntry(path, original, xy, score string) GitStatusEntry {
	kind := statusKind(xy)
	if strings.HasPrefix(score, "R") {
		kind = "renamed"
	} else if strings.HasPrefix(score, "C") {
		kind = "copied"
	}
	return GitStatusEntry{
		Path:           filepath.ToSlash(path),
		OriginalPath:   filepath.ToSlash(original),
		IndexStatus:    string(xy[0]),
		WorktreeStatus: string(xy[1]),
		Kind:           kind,
	}
}

func statusKind(xy string) string {
	if strings.Contains(xy, "U") || xy == "AA" || xy == "DD" {
		return "conflicted"
	}
	for _, status := range xy {
		switch status {
		case 'A':
			return "added"
		case 'D':
			return "deleted"
		case 'R':
			return "renamed"
		case 'C':
			return "copied"
		case 'T':
			return "type_changed"
		case 'M':
			return "modified"
		}
	}
	return "changed"
}

func newLimitedBuffer(limit int) *limitedBuffer {
	return &limitedBuffer{limit: limit}
}

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	tooLarge bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 || b.Len()+len(p) <= b.limit {
		return b.Buffer.Write(p)
	}
	remaining := b.limit - b.Len()
	if remaining > 0 {
		_, _ = b.Buffer.Write(p[:remaining])
	}
	b.tooLarge = true
	return 0, ErrGitOutputTooLarge
}

func pathEscapes(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel)
}

func cleanSlashDot(value string) string {
	if value == "." {
		return ""
	}
	return filepath.ToSlash(value)
}

func joinGitRootPath(rootPath, path string) string {
	if rootPath == "" {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(filepath.Join(filepath.FromSlash(rootPath), filepath.FromSlash(path)))
}
