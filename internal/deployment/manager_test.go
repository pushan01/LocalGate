package deployment

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"localgate/internal/config"
	"localgate/internal/static"
)

func TestManagerStaticLifecycle(t *testing.T) {
	workspaceRoot := t.TempDir()
	source := filepath.Join(workspaceRoot, "site")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(static.NewServiceAt(filepath.Join(t.TempDir(), "managed")), nil, "https://control.test/")
	workspace := config.Workspace{ID: "ws", Roots: []string{workspaceRoot}, CWD: workspaceRoot}

	result, err := manager.Deploy(context.Background(), DeployRequest{
		Type: TypeStatic, Name: "demo", Source: source, Workspace: workspace,
		Options: DeployOptions{PreserveSource: true},
	})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if result.Type != TypeStatic || result.Site != "demo" || result.URL != "https://control.test/static/demo/" {
		t.Fatalf("unexpected deploy result: %#v", result)
	}
	items, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].Site != "demo" || items[0].Type != TypeStatic {
		t.Fatalf("unexpected list: %#v", items)
	}
	if err := manager.Delete(context.Background(), TypeStatic, "demo"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	items, err = manager.List(context.Background())
	if err != nil || len(items) != 0 {
		t.Fatalf("list after delete: %#v err=%v", items, err)
	}
}

func TestManagerRejectsWorkspaceRootRemoval(t *testing.T) {
	workspaceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(static.NewServiceAt(filepath.Join(t.TempDir(), "managed")), nil, "")
	_, err := manager.Deploy(context.Background(), DeployRequest{
		Type: TypeStatic, Name: "demo", Source: workspaceRoot,
		Workspace: config.Workspace{ID: "ws", Roots: []string{workspaceRoot}, CWD: workspaceRoot},
		Options:   DeployOptions{PreserveSource: false},
	})
	if !errors.Is(err, ErrUnsafeSourceRemoval) {
		t.Fatalf("expected unsafe removal error, got %v", err)
	}
}
