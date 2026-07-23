package filebrowser

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"localgate/internal/config"
)

func testService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default().FileBrowser
	cfg.MaxFileBytes = 64
	cfg.RequireApprovalForWrite = false
	cfg.RequireApprovalForDelete = false
	svc := NewService(cfg, map[string]config.Workspace{
		"ws": {ID: "ws", Roots: []string{root}},
	})
	return svc, root
}

func testServiceWithConfig(t *testing.T, cfg config.FileBrowserConfig) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	cfg.MaxFileBytes = 64
	svc := NewService(cfg, map[string]config.Workspace{
		"ws": {ID: "ws", Roots: []string{root}},
	})
	return svc, root
}

func TestWorkspacesReturnsRootIDsWithoutAbsolutePaths(t *testing.T) {
	svc, _ := testService(t)
	items, err := svc.Workspaces()
	if err != nil {
		t.Fatalf("workspaces: %v", err)
	}
	if len(items) != 1 || items[0].ID != "ws" || len(items[0].Roots) != 1 || items[0].Roots[0].ID != "0" {
		t.Fatalf("unexpected workspaces: %#v", items)
	}
}

func TestListFiltersHiddenAndExcludedEntries(t *testing.T) {
	svc, root := testService(t)
	mustWrite(t, filepath.Join(root, "visible.txt"), "hello")
	mustWrite(t, filepath.Join(root, ".hidden"), "secret")
	if err := os.Mkdir(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "node_modules", "pkg.js"), "x")

	result, err := svc.List(context.Background(), PathRequest{WorkspaceID: "ws", RootID: "0"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(result.Entries) != 1 || result.Entries[0].Name != "visible.txt" {
		t.Fatalf("unexpected entries: %#v", result.Entries)
	}
}

func TestReadContentReturnsTextMetadata(t *testing.T) {
	svc, root := testService(t)
	mustWrite(t, filepath.Join(root, "main.go"), "package main\n")

	content, err := svc.ReadContent(context.Background(), PathRequest{WorkspaceID: "ws", RootID: "0", Path: "main.go"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if content.Content != "package main\n" || content.Encoding != "utf-8" || content.Language != "go" || content.ETag == "" {
		t.Fatalf("unexpected content: %#v", content)
	}
}

func TestReadContentRejectsEscapesBinaryAndLargeFiles(t *testing.T) {
	svc, root := testService(t)
	mustWrite(t, filepath.Join(root, "large.txt"), "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz")
	mustWriteBytes(t, filepath.Join(root, "bin.dat"), []byte{'a', 0, 'b'})

	if _, err := svc.ReadContent(context.Background(), PathRequest{WorkspaceID: "ws", RootID: "0", Path: "../outside.txt"}); !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("expected path not allowed, got %v", err)
	}
	if _, err := svc.ReadContent(context.Background(), PathRequest{WorkspaceID: "ws", RootID: "0", Path: "large.txt"}); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("expected too large, got %v", err)
	}
	if _, err := svc.ReadContent(context.Background(), PathRequest{WorkspaceID: "ws", RootID: "0", Path: "bin.dat"}); !errors.Is(err, ErrBinaryFile) {
		t.Fatalf("expected binary file, got %v", err)
	}
}

func TestSymlinkEscapeIsRejected(t *testing.T) {
	svc, root := testService(t)
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "secret.txt"), "secret")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	_, err := svc.ReadContent(context.Background(), PathRequest{WorkspaceID: "ws", RootID: "0", Path: "link.txt"})
	if !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("expected path not allowed, got %v", err)
	}
}

func TestSaveContentCreateOverwriteAndETagConflict(t *testing.T) {
	svc, root := testService(t)
	created, err := svc.SaveContent(context.Background(), SaveContentRequest{WorkspaceID: "ws", RootID: "0", Path: "note.txt", Content: "one", Create: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !created.Created || created.Status != "created" || created.ETag == "" {
		t.Fatalf("unexpected create result: %#v", created)
	}
	if got, err := os.ReadFile(filepath.Join(root, "note.txt")); err != nil || string(got) != "one" {
		t.Fatalf("unexpected file content: %q err=%v", got, err)
	}
	if _, err := svc.SaveContent(context.Background(), SaveContentRequest{WorkspaceID: "ws", RootID: "0", Path: "note.txt", Content: "two", ETag: "sha256:wrong"}); !errors.Is(err, ErrFileChanged) {
		t.Fatalf("expected file changed, got %v", err)
	}
	overwritten, err := svc.SaveContent(context.Background(), SaveContentRequest{WorkspaceID: "ws", RootID: "0", Path: "note.txt", Content: "two", ETag: created.ETag})
	if err != nil {
		t.Fatalf("overwrite with etag: %v", err)
	}
	if !overwritten.Overwritten || overwritten.Status != "overwritten" || overwritten.Bytes != 3 {
		t.Fatalf("unexpected overwrite result: %#v", overwritten)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "note.txt")); string(got) != "two" {
		t.Fatalf("unexpected overwritten content: %q", got)
	}
}

func TestSaveContentRejectsUnsafeWrites(t *testing.T) {
	svc, root := testService(t)
	mustWrite(t, filepath.Join(root, "existing.txt"), "old")
	if _, err := svc.SaveContent(context.Background(), SaveContentRequest{WorkspaceID: "ws", RootID: "0", Path: "../escape.txt", Content: "x", Create: true}); !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("expected path not allowed, got %v", err)
	}
	if _, err := svc.SaveContent(context.Background(), SaveContentRequest{WorkspaceID: "ws", RootID: "0", Path: "existing.txt", Content: "x", Create: true}); !errors.Is(err, ErrFileExists) {
		t.Fatalf("expected file exists, got %v", err)
	}
	if _, err := svc.SaveContent(context.Background(), SaveContentRequest{WorkspaceID: "ws", RootID: "0", Path: "new.txt", Content: "x"}); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("expected file not found without create, got %v", err)
	}
	if _, err := svc.SaveContent(context.Background(), SaveContentRequest{WorkspaceID: "ws", RootID: "0", Path: "bin.txt", Content: string([]byte{'a', 0, 'b'}), Create: true}); !errors.Is(err, ErrBinaryFile) {
		t.Fatalf("expected binary file, got %v", err)
	}
}

func TestSaveContentHonorsReadOnlyAndApprovalRequired(t *testing.T) {
	cfg := config.Default().FileBrowser
	cfg.ReadOnly = true
	cfg.RequireApprovalForWrite = false
	svc, _ := testServiceWithConfig(t, cfg)
	if _, err := svc.SaveContent(context.Background(), SaveContentRequest{WorkspaceID: "ws", RootID: "0", Path: "note.txt", Content: "x", Create: true}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("expected read only, got %v", err)
	}
	cfg = config.Default().FileBrowser
	cfg.ReadOnly = false
	cfg.RequireApprovalForWrite = true
	svc, _ = testServiceWithConfig(t, cfg)
	if _, err := svc.SaveContent(context.Background(), SaveContentRequest{WorkspaceID: "ws", RootID: "0", Path: "note.txt", Content: "x", Create: true}); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected approval required, got %v", err)
	}
	if _, err := svc.SaveContentApproved(context.Background(), SaveContentRequest{WorkspaceID: "ws", RootID: "0", Path: "note.txt", Content: "x", Create: true}); err != nil {
		t.Fatalf("approved save should bypass approval-required policy: %v", err)
	}
}

func TestMkdirCreatesDirectoryAndRejectsUnsafePaths(t *testing.T) {
	svc, root := testService(t)
	res, err := svc.Mkdir(context.Background(), MkdirRequest{WorkspaceID: "ws", RootID: "0", Path: "docs"})
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if res.Status != "created" || res.Path != "docs" {
		t.Fatalf("unexpected mkdir result: %#v", res)
	}
	if info, err := os.Stat(filepath.Join(root, "docs")); err != nil || !info.IsDir() {
		t.Fatalf("directory not created: info=%#v err=%v", info, err)
	}
	if _, err := svc.Mkdir(context.Background(), MkdirRequest{WorkspaceID: "ws", RootID: "0", Path: "docs"}); !errors.Is(err, ErrFileExists) {
		t.Fatalf("expected file exists, got %v", err)
	}
	if _, err := svc.Mkdir(context.Background(), MkdirRequest{WorkspaceID: "ws", RootID: "0", Path: "../escape"}); !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("expected path not allowed, got %v", err)
	}
	if _, err := svc.Mkdir(context.Background(), MkdirRequest{WorkspaceID: "ws", RootID: "0", Path: "missing/child"}); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("expected missing parent, got %v", err)
	}
}

func TestMkdirHonorsApprovalRequired(t *testing.T) {
	cfg := config.Default().FileBrowser
	cfg.RequireApprovalForWrite = true
	svc, root := testServiceWithConfig(t, cfg)
	if _, err := svc.Mkdir(context.Background(), MkdirRequest{WorkspaceID: "ws", RootID: "0", Path: "docs"}); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected approval required, got %v", err)
	}
	if _, err := svc.MkdirApproved(context.Background(), MkdirRequest{WorkspaceID: "ws", RootID: "0", Path: "docs"}); err != nil {
		t.Fatalf("approved mkdir: %v", err)
	}
	if info, err := os.Stat(filepath.Join(root, "docs")); err != nil || !info.IsDir() {
		t.Fatalf("directory not created: info=%#v err=%v", info, err)
	}
}

func TestRenameMovesFileAndDirectory(t *testing.T) {
	svc, root := testService(t)
	mustWrite(t, filepath.Join(root, "old.txt"), "hello")
	res, err := svc.Rename(context.Background(), RenameRequest{WorkspaceID: "ws", RootID: "0", FromPath: "old.txt", ToPath: "new.txt"})
	if err != nil {
		t.Fatalf("rename file: %v", err)
	}
	if res.Status != "renamed" || res.FromPath != "old.txt" || res.ToPath != "new.txt" || res.Type != "file" {
		t.Fatalf("unexpected rename result: %#v", res)
	}
	if got, err := os.ReadFile(filepath.Join(root, "new.txt")); err != nil || string(got) != "hello" {
		t.Fatalf("unexpected renamed content: %q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, "old.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old path should not exist, got %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err = svc.Rename(context.Background(), RenameRequest{WorkspaceID: "ws", RootID: "0", FromPath: "docs", ToPath: "notes"})
	if err != nil {
		t.Fatalf("rename dir: %v", err)
	}
	if res.Type != "directory" {
		t.Fatalf("expected directory result, got %#v", res)
	}
	if info, err := os.Stat(filepath.Join(root, "notes")); err != nil || !info.IsDir() {
		t.Fatalf("renamed directory missing: info=%#v err=%v", info, err)
	}
}

func TestRenameRejectsUnsafeOperations(t *testing.T) {
	svc, root := testService(t)
	mustWrite(t, filepath.Join(root, "a.txt"), "a")
	mustWrite(t, filepath.Join(root, "b.txt"), "b")
	if _, err := svc.Rename(context.Background(), RenameRequest{WorkspaceID: "ws", RootID: "0", FromPath: "a.txt", ToPath: "b.txt"}); !errors.Is(err, ErrFileExists) {
		t.Fatalf("expected file exists, got %v", err)
	}
	if _, err := svc.Rename(context.Background(), RenameRequest{WorkspaceID: "ws", RootID: "0", FromPath: "missing.txt", ToPath: "x.txt"}); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("expected file not found, got %v", err)
	}
	if _, err := svc.Rename(context.Background(), RenameRequest{WorkspaceID: "ws", RootID: "0", FromPath: "a.txt", ToPath: "../x.txt"}); !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("expected path not allowed, got %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "parent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "parent", "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Rename(context.Background(), RenameRequest{WorkspaceID: "ws", RootID: "0", FromPath: "parent", ToPath: "parent/child/moved"}); !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("expected moving directory into itself to be rejected, got %v", err)
	}
}

func TestRenameHonorsApprovalRequired(t *testing.T) {
	cfg := config.Default().FileBrowser
	cfg.RequireApprovalForWrite = true
	svc, root := testServiceWithConfig(t, cfg)
	mustWrite(t, filepath.Join(root, "old.txt"), "hello")
	if _, err := svc.Rename(context.Background(), RenameRequest{WorkspaceID: "ws", RootID: "0", FromPath: "old.txt", ToPath: "new.txt"}); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected approval required, got %v", err)
	}
	if _, err := svc.RenameApproved(context.Background(), RenameRequest{WorkspaceID: "ws", RootID: "0", FromPath: "old.txt", ToPath: "new.txt"}); err != nil {
		t.Fatalf("approved rename: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "new.txt")); err != nil || string(got) != "hello" {
		t.Fatalf("unexpected renamed content: %q err=%v", got, err)
	}
}

func TestDeleteRemovesFileAndDirectory(t *testing.T) {
	svc, root := testService(t)
	mustWrite(t, filepath.Join(root, "old.txt"), "hello")
	res, err := svc.Delete(context.Background(), DeleteRequest{WorkspaceID: "ws", RootID: "0", Path: "old.txt"})
	if err != nil {
		t.Fatalf("delete file: %v", err)
	}
	if res.Status != "deleted" || res.Type != "file" || res.Path != "old.txt" {
		t.Fatalf("unexpected delete result: %#v", res)
	}
	if _, err := os.Stat(filepath.Join(root, "old.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file should be deleted, got %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "docs", "a.txt"), "a")
	if _, err := svc.Delete(context.Background(), DeleteRequest{WorkspaceID: "ws", RootID: "0", Path: "docs"}); !errors.Is(err, ErrPathIsDirectory) {
		t.Fatalf("expected directory delete without recursive rejected, got %v", err)
	}
	res, err = svc.Delete(context.Background(), DeleteRequest{WorkspaceID: "ws", RootID: "0", Path: "docs", Recursive: true})
	if err != nil {
		t.Fatalf("recursive delete: %v", err)
	}
	if res.Type != "directory" || !res.Recursive {
		t.Fatalf("unexpected recursive result: %#v", res)
	}
	if _, err := os.Stat(filepath.Join(root, "docs")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("directory should be deleted, got %v", err)
	}
}

func TestDeleteRejectsUnsafeOperations(t *testing.T) {
	svc, root := testService(t)
	if _, err := svc.Delete(context.Background(), DeleteRequest{WorkspaceID: "ws", RootID: "0", Path: "missing.txt"}); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("expected file not found, got %v", err)
	}
	if _, err := svc.Delete(context.Background(), DeleteRequest{WorkspaceID: "ws", RootID: "0", Path: "../escape.txt"}); !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("expected path not allowed, got %v", err)
	}
	outside := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "x"), filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := svc.Delete(context.Background(), DeleteRequest{WorkspaceID: "ws", RootID: "0", Path: "link"}); !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("expected symlink delete rejected, got %v", err)
	}
}

func TestDeleteHonorsApprovalRequired(t *testing.T) {
	cfg := config.Default().FileBrowser
	cfg.RequireApprovalForWrite = false
	cfg.RequireApprovalForDelete = true
	svc, root := testServiceWithConfig(t, cfg)
	mustWrite(t, filepath.Join(root, "old.txt"), "hello")
	if _, err := svc.Delete(context.Background(), DeleteRequest{WorkspaceID: "ws", RootID: "0", Path: "old.txt"}); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected approval required, got %v", err)
	}
	if _, err := svc.DeleteApproved(context.Background(), DeleteRequest{WorkspaceID: "ws", RootID: "0", Path: "old.txt"}); err != nil {
		t.Fatalf("approved delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "old.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file should be deleted, got %v", err)
	}
}

func TestDownloadFileReturnsSafeRegularFile(t *testing.T) {
	svc, root := testService(t)
	mustWrite(t, filepath.Join(root, "file.bin"), "hello")
	res, err := svc.DownloadFile(context.Background(), PathRequest{WorkspaceID: "ws", RootID: "0", Path: "file.bin"})
	if err != nil {
		t.Fatalf("download file: %v", err)
	}
	if res.Path != "file.bin" || res.Name != "file.bin" || res.AbsPath != filepath.Join(root, "file.bin") || res.Size != 5 {
		t.Fatalf("unexpected download result: %#v", res)
	}
}

func TestDownloadFileRejectsDirectoryEscapeAndSymlink(t *testing.T) {
	svc, root := testService(t)
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DownloadFile(context.Background(), PathRequest{WorkspaceID: "ws", RootID: "0", Path: "docs"}); !errors.Is(err, ErrPathIsDirectory) {
		t.Fatalf("expected directory rejection, got %v", err)
	}
	if _, err := svc.DownloadFile(context.Background(), PathRequest{WorkspaceID: "ws", RootID: "0", Path: "../escape"}); !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("expected path not allowed, got %v", err)
	}
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "outside.txt")
	mustWrite(t, outsideFile, "secret")
	if err := os.Symlink(outsideFile, filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := svc.DownloadFile(context.Background(), PathRequest{WorkspaceID: "ws", RootID: "0", Path: "link.txt"}); !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestUploadCreatesBinaryFileAndRejectsExistingOrLarge(t *testing.T) {
	svc, root := testService(t)
	res, err := svc.Upload(context.Background(), UploadRequest{WorkspaceID: "ws", RootID: "0", Path: "image.bin", Content: []byte{0, 1, 2}, Create: true})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if res.Status != "created" || res.Path != "image.bin" || res.Bytes != 3 {
		t.Fatalf("unexpected upload result: %#v", res)
	}
	if got, err := os.ReadFile(filepath.Join(root, "image.bin")); err != nil || !bytes.Equal(got, []byte{0, 1, 2}) {
		t.Fatalf("unexpected uploaded content: %v err=%v", got, err)
	}
	if _, err := svc.Upload(context.Background(), UploadRequest{WorkspaceID: "ws", RootID: "0", Path: "image.bin", Content: []byte("new"), Create: true}); !errors.Is(err, ErrFileExists) {
		t.Fatalf("expected file exists, got %v", err)
	}
	cfg := config.Default().FileBrowser
	cfg.RequireApprovalForWrite = false
	cfg.RequireApprovalForDelete = false
	cfg.MaxUploadBytes = 2
	smallSvc, _ := testServiceWithConfig(t, cfg)
	if _, err := smallSvc.Upload(context.Background(), UploadRequest{WorkspaceID: "ws", RootID: "0", Path: "too-big.bin", Content: []byte{0, 1, 2}, Create: true}); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("expected too large, got %v", err)
	}
}

func TestUploadHonorsApprovalRequired(t *testing.T) {
	cfg := config.Default().FileBrowser
	cfg.RequireApprovalForWrite = true
	cfg.RequireApprovalForDelete = false
	svc, root := testServiceWithConfig(t, cfg)
	if _, err := svc.Upload(context.Background(), UploadRequest{WorkspaceID: "ws", RootID: "0", Path: "upload.bin", Content: []byte{0}, Create: true}); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected approval required, got %v", err)
	}
	if _, err := svc.UploadApproved(context.Background(), UploadRequest{WorkspaceID: "ws", RootID: "0", Path: "upload.bin", Content: []byte{0}, Create: true}); err != nil {
		t.Fatalf("approved upload: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "upload.bin")); err != nil || !bytes.Equal(got, []byte{0}) {
		t.Fatalf("unexpected uploaded content: %v err=%v", got, err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	mustWriteBytes(t, path, []byte(content))
}

func mustWriteBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}
