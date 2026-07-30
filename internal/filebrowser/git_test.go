package filebrowser

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"localgate/internal/config"
)

func TestGitStatusAndDiff(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	initGitRepository(t, root)
	mustWrite(t, filepath.Join(root, "tracked.txt"), "base\n")
	runTestGit(t, root, "add", "tracked.txt")
	runTestGit(t, root, "commit", "-m", "base")

	mustWrite(t, filepath.Join(root, "tracked.txt"), "staged\n")
	runTestGit(t, root, "add", "tracked.txt")
	mustWrite(t, filepath.Join(root, "tracked.txt"), "worktree\n")
	mustWrite(t, filepath.Join(root, "new.txt"), "new\n")

	svc := gitTestService(root)
	status, err := svc.GitStatus(context.Background(), PathRequest{WorkspaceID: "ws", RootID: "0"})
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if !status.Repository || status.RepositoryPath != "" || status.Branch == "" {
		t.Fatalf("unexpected repository metadata: %#v", status)
	}
	entries := gitEntriesByPath(status.Entries)
	if got := entries["tracked.txt"]; got.IndexStatus != "M" || got.WorktreeStatus != "M" || got.Kind != "modified" {
		t.Fatalf("unexpected tracked status: %#v", got)
	}
	if got := entries["new.txt"]; got.Kind != "untracked" {
		t.Fatalf("unexpected untracked status: %#v", got)
	}

	staged, err := svc.GitDiff(context.Background(), PathRequest{WorkspaceID: "ws", RootID: "0", Path: "tracked.txt"}, true)
	if err != nil {
		t.Fatalf("staged diff: %v", err)
	}
	if !strings.Contains(staged.Diff, "+staged") || strings.Contains(staged.Diff, "+worktree") {
		t.Fatalf("unexpected staged diff: %s", staged.Diff)
	}
	unstaged, err := svc.GitDiff(context.Background(), PathRequest{WorkspaceID: "ws", RootID: "0", Path: "tracked.txt"}, false)
	if err != nil {
		t.Fatalf("unstaged diff: %v", err)
	}
	if !strings.Contains(unstaged.Diff, "+worktree") {
		t.Fatalf("unexpected unstaged diff: %s", unstaged.Diff)
	}
}

func TestGitStatusScopesDirectoryAndPrefixesNestedRepository(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "inside"), 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepository(t, repo)
	mustWrite(t, filepath.Join(repo, "inside", "one.txt"), "one\n")
	mustWrite(t, filepath.Join(repo, "outside.txt"), "outside\n")

	svc := gitTestService(root)
	status, err := svc.GitStatus(context.Background(), PathRequest{WorkspaceID: "ws", RootID: "0", Path: "repo/inside"})
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if status.RepositoryPath != "repo" || len(status.Entries) != 1 || status.Entries[0].Path != "repo/inside/one.txt" {
		t.Fatalf("unexpected scoped status: %#v", status)
	}
}

func TestGitDiffSupportsDeletedFiles(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	initGitRepository(t, root)
	mustWrite(t, filepath.Join(root, "deleted.txt"), "base\n")
	runTestGit(t, root, "add", "deleted.txt")
	runTestGit(t, root, "commit", "-m", "base")
	if err := os.Remove(filepath.Join(root, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	svc := gitTestService(root)
	working, err := svc.GitDiff(context.Background(), PathRequest{WorkspaceID: "ws", RootID: "0", Path: "deleted.txt"}, false)
	if err != nil {
		t.Fatalf("deleted working diff: %v", err)
	}
	if !strings.Contains(working.Diff, "-base") {
		t.Fatalf("unexpected deleted working diff: %s", working.Diff)
	}
	runTestGit(t, root, "add", "deleted.txt")
	staged, err := svc.GitDiff(context.Background(), PathRequest{WorkspaceID: "ws", RootID: "0", Path: "deleted.txt"}, true)
	if err != nil {
		t.Fatalf("deleted staged diff: %v", err)
	}
	if !strings.Contains(staged.Diff, "-base") {
		t.Fatalf("unexpected deleted staged diff: %s", staged.Diff)
	}
}

func TestGitStatusReportsNonRepository(t *testing.T) {
	requireGit(t)
	svc, _ := testService(t)
	status, err := svc.GitStatus(context.Background(), PathRequest{WorkspaceID: "ws", RootID: "0"})
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if status.Repository || len(status.Entries) != 0 {
		t.Fatalf("unexpected non-repository result: %#v", status)
	}
}

func TestGitRepositoryOutsideSelectedRootIsRejected(t *testing.T) {
	requireGit(t)
	parent := t.TempDir()
	initGitRepository(t, parent)
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := gitTestService(child)
	_, err := svc.GitStatus(context.Background(), PathRequest{WorkspaceID: "ws", RootID: "0"})
	if !errors.Is(err, ErrGitRepositoryOutsideRoot) {
		t.Fatalf("expected outside-root error, got %v", err)
	}
}

func TestGitMetadataOutsideSelectedRootIsRejected(t *testing.T) {
	requireGit(t)
	mainRepo := t.TempDir()
	initGitRepository(t, mainRepo)
	mustWrite(t, filepath.Join(mainRepo, "base.txt"), "base\n")
	runTestGit(t, mainRepo, "add", "base.txt")
	runTestGit(t, mainRepo, "commit", "-m", "base")
	worktree := filepath.Join(t.TempDir(), "worktree")
	runTestGit(t, mainRepo, "worktree", "add", "-q", "-b", "test-worktree", worktree)
	svc := gitTestService(worktree)
	_, err := svc.GitStatus(context.Background(), PathRequest{WorkspaceID: "ws", RootID: "0"})
	if !errors.Is(err, ErrGitRepositoryOutsideRoot) {
		t.Fatalf("expected metadata outside-root error, got %v", err)
	}
}

func TestGitDoesNotRunExternalDiffOrFSMonitor(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	initGitRepository(t, root)
	mustWrite(t, filepath.Join(root, "tracked.txt"), "base\n")
	runTestGit(t, root, "add", "tracked.txt")
	runTestGit(t, root, "commit", "-m", "base")
	mustWrite(t, filepath.Join(root, "tracked.txt"), "changed\n")

	marker := filepath.Join(root, "executed")
	script := filepath.Join(root, "danger.sh")
	mustWrite(t, script, "#!/bin/sh\nprintf executed > \""+marker+"\"\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, root, "config", "diff.external", script)
	runTestGit(t, root, "config", "core.fsmonitor", script)

	svc := gitTestService(root)
	if _, err := svc.GitStatus(context.Background(), PathRequest{WorkspaceID: "ws", RootID: "0"}); err != nil {
		t.Fatalf("git status: %v", err)
	}
	if _, err := svc.GitDiff(context.Background(), PathRequest{WorkspaceID: "ws", RootID: "0", Path: "tracked.txt"}, false); err != nil {
		t.Fatalf("git diff: %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repository command configuration executed unexpectedly: %v", err)
	}
}

func TestGitStatusHonorsCanceledContext(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	initGitRepository(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := gitTestService(root).GitStatus(ctx, PathRequest{WorkspaceID: "ws", RootID: "0"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}

func TestParseGitStatusRenameAndLimitedBuffer(t *testing.T) {
	data := []byte("# branch.oid abc\x00# branch.head main\x002 R. N... 100644 100644 100644 old new R100 new name.txt\x00old name.txt\x00")
	parsed, err := parseGitStatus(data)
	if err != nil {
		t.Fatalf("parse status: %v", err)
	}
	if len(parsed.Entries) != 1 || parsed.Entries[0].Path != "new name.txt" || parsed.Entries[0].OriginalPath != "old name.txt" || parsed.Entries[0].Kind != "renamed" {
		t.Fatalf("unexpected rename entry: %#v", parsed.Entries)
	}
	buffer := newLimitedBuffer(4)
	if _, err := buffer.Write([]byte("12345")); !errors.Is(err, ErrGitOutputTooLarge) || !buffer.tooLarge || buffer.String() != "1234" {
		t.Fatalf("unexpected limited buffer: value=%q too_large=%v err=%v", buffer.String(), buffer.tooLarge, err)
	}
}

func gitTestService(root string) *Service {
	cfg := config.Default().FileBrowser
	cfg.Enabled = true
	return NewService(cfg, map[string]config.Workspace{"ws": {ID: "ws", Roots: []string{root}}})
}

func initGitRepository(t *testing.T, root string) {
	t.Helper()
	runTestGit(t, root, "init", "-q")
	runTestGit(t, root, "config", "user.email", "localgate@example.test")
	runTestGit(t, root, "config", "user.name", "LocalGate Test")
}

func runTestGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

func gitEntriesByPath(entries []GitStatusEntry) map[string]GitStatusEntry {
	result := make(map[string]GitStatusEntry, len(entries))
	for _, entry := range entries {
		result[entry.Path] = entry
	}
	return result
}
