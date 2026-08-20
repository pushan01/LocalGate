package security

import (
	"os"
	"path/filepath"
	"testing"

	"localgate/internal/config"
)

func TestValidatePathWithinRoots(t *testing.T) {
	dir := t.TempDir()
	ws := config.Root{Path: dir}
	target := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	dec := ValidatePath(target, ws, Options{})
	if !dec.Allowed {
		t.Fatalf("expected allowed, got %v", dec)
	}
}

func TestValidatePathDenylist(t *testing.T) {
	dir := t.TempDir()
	deny := filepath.Join(dir, "secret")
	_ = os.MkdirAll(deny, 0o755)
	ws := config.Root{Path: dir,
		PathDenylist: []string{deny},
	}
	target := filepath.Join(deny, "k.txt")
	_ = os.WriteFile(target, []byte("x"), 0o644)
	dec := ValidatePath(target, ws, Options{})
	if dec.Allowed {
		t.Fatalf("expected denied, got %v", dec)
	}
}

func TestValidatePathAllowNonExistWithMissingParents(t *testing.T) {
	dir := t.TempDir()
	ws := config.Root{Path: dir}
	target := filepath.Join(dir, "nested", "more", "a.txt")
	dec := ValidatePath(target, ws, Options{FollowSymlink: true, AllowNonExist: true})
	if !dec.Allowed {
		t.Fatalf("expected allowed for non-existent nested path, got %v", dec)
	}
	realRoot, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval symlinks root: %v", err)
	}
	want := filepath.Join(realRoot, "nested", "more", "a.txt")
	if dec.Path != want {
		t.Fatalf("unexpected resolved path: %v", dec)
	}
}
