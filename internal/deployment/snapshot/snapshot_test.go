package snapshot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyRejectsSymlinkAndLimits(t *testing.T) {
	source := t.TempDir()
	destination := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(source, "file.txt"), filepath.Join(source, "link.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := Copy(context.Background(), source, destination, Limits{MaxFiles: 10, MaxTotalBytes: 100, MaxFileBytes: 100}, Options{})
	if !errors.Is(err, ErrSymlink) {
		t.Fatalf("expected symlink error, got %v", err)
	}

	if err := os.Remove(filepath.Join(source, "link.txt")); err != nil {
		t.Fatal(err)
	}
	_, err = Copy(context.Background(), source, t.TempDir(), Limits{MaxFiles: 10, MaxTotalBytes: 4, MaxFileBytes: 4}, Options{})
	if !errors.Is(err, ErrSizeLimit) {
		t.Fatalf("expected size error, got %v", err)
	}
}

func TestCopyHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Copy(ctx, t.TempDir(), t.TempDir(), Limits{MaxFiles: 1, MaxTotalBytes: 1, MaxFileBytes: 1}, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
