package fileio

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type errorReader struct {
	done bool
}

func (r *errorReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		copy(p, "new")
		return 3, nil
	}
	return 0, errors.New("source failed")
}

type createTargetReader struct {
	path string
	done bool
}

func (r *createTargetReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	if err := os.WriteFile(r.path, []byte("racer"), 0o600); err != nil {
		return 0, err
	}
	copy(p, "ours")
	return 4, io.EOF
}

func TestAtomicWriteFailureKeepsOldContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte("old"), 0o751); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if _, err := WriteReaderAtomic(path, &errorReader{}, AtomicWriteOptions{PreserveMode: true}); err == nil {
		t.Fatal("expected source failure")
	}
	content, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(content) != "old" || info.Mode().Perm() != 0o751 {
		t.Fatalf("old file changed: content=%q mode=%o", content, info.Mode().Perm())
	}
}

func TestAtomicWritePreservesExistingMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(path, []byte("old"), 0o751); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if _, err := WriteFileAtomic(path, []byte("new"), AtomicWriteOptions{PreserveMode: true}); err != nil {
		t.Fatalf("atomic write: %v", err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o751 {
		t.Fatalf("mode changed: %o", info.Mode().Perm())
	}
}

func TestAtomicCreateRejectsConcurrentTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.txt")
	_, err := WriteReaderAtomic(path, &createTargetReader{path: path}, AtomicWriteOptions{CreateParents: true})
	if !errors.Is(err, ErrTargetChanged) {
		t.Fatalf("expected target changed, got %v", err)
	}
	content, _ := os.ReadFile(path)
	if string(content) != "racer" {
		t.Fatalf("concurrent file overwritten: %q", content)
	}
}
