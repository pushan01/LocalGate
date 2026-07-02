package static

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceDeployOverwriteDeleteAndServe(t *testing.T) {
	root := filepath.Join(t.TempDir(), "managed")
	source := filepath.Join(t.TempDir(), "site")
	if err := os.MkdirAll(filepath.Join(source, "assets"), 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	indexPath := filepath.Join(source, "index.html")
	if err := os.WriteFile(indexPath, []byte("v1"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "assets", "app.js"), []byte("console.log('ok')"), 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	service := NewServiceAt(root)
	deployment, err := service.DeploySite(context.Background(), "demo", source, false)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if deployment.FileCount != 2 || deployment.TotalBytes == 0 {
		t.Fatalf("unexpected deployment stats: %+v", deployment)
	}
	deployed := filepath.Join(root, "demo", "index.html")
	if got, err := os.ReadFile(deployed); err != nil || string(got) != "v1" {
		t.Fatalf("unexpected deployed content=%q err=%v", got, err)
	}
	if _, err := service.DeploySite(context.Background(), "demo", source, false); err == nil {
		t.Fatalf("expected existing site error")
	}

	if err := os.WriteFile(indexPath, []byte("v2"), 0o644); err != nil {
		t.Fatalf("rewrite source: %v", err)
	}
	if _, err := service.DeploySite(context.Background(), "demo", source, true); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if got, err := os.ReadFile(deployed); err != nil || string(got) != "v2" {
		t.Fatalf("unexpected overwritten content=%q err=%v", got, err)
	}

	for _, test := range []struct {
		path string
		want string
	}{
		{path: "/", want: "v2"},
		{path: "/assets/app.js", want: "console.log('ok')"},
		{path: "/client/route", want: "v2"},
	} {
		req := httptest.NewRequest(http.MethodGet, test.path, nil)
		w := httptest.NewRecorder()
		service.ServeSite("demo", w, req)
		if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != test.want {
			t.Fatalf("serve %s: code=%d body=%q", test.path, w.Code, w.Body.String())
		}
	}

	registry, err := service.ListSites()
	if err != nil {
		t.Fatalf("list sites: %v", err)
	}
	if _, ok := registry.Sites["demo"]; !ok {
		t.Fatalf("site missing from registry: %#v", registry)
	}
	if err := service.DeleteSite("demo"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "demo")); !os.IsNotExist(err) {
		t.Fatalf("site directory still exists: %v", err)
	}
}

func TestServiceUsesSingleRootHTMLAsIndex(t *testing.T) {
	root := filepath.Join(t.TempDir(), "managed")
	source := t.TempDir()
	entry := "苏州城市学院历年真题互动练习.html"
	if err := os.WriteFile(filepath.Join(source, entry), []byte("quiz"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	service := NewServiceAt(root)
	deployment, err := service.DeploySite(context.Background(), "quiz", source, false)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if deployment.Site.Index != entry {
		t.Fatalf("unexpected detected index: %q", deployment.Site.Index)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	service.ServeSite("quiz", w, req)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "quiz" {
		t.Fatalf("serve detected index: code=%d body=%q", w.Code, w.Body.String())
	}
}

func TestServiceRejectsAmbiguousRootHTMLWithoutIndex(t *testing.T) {
	root := filepath.Join(t.TempDir(), "managed")
	source := t.TempDir()
	for _, name := range []string{"first.html", "second.html"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(name), 0o644); err != nil {
			t.Fatalf("write source: %v", err)
		}
	}

	service := NewServiceAt(root)
	if _, err := service.DeploySite(context.Background(), "ambiguous", source, false); err == nil {
		t.Fatal("expected ambiguous HTML entry deployment to fail")
	}
	if _, err := os.Stat(filepath.Join(root, "ambiguous")); !os.IsNotExist(err) {
		t.Fatalf("ambiguous deployment should not activate a site: %v", err)
	}
}

func TestServiceRejectsUnsafeSiteName(t *testing.T) {
	service := NewServiceAt(filepath.Join(t.TempDir(), "managed"))
	source := t.TempDir()
	for _, name := range []string{"../escape", "/absolute", "bad/name", "-leading"} {
		if _, err := service.DeploySite(context.Background(), name, source, false); err == nil {
			t.Fatalf("expected unsafe name %q to fail", name)
		}
	}
}

func TestServiceRejectsSourceSymlink(t *testing.T) {
	root := filepath.Join(t.TempDir(), "managed")
	source := filepath.Join(t.TempDir(), "site")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(source, "linked.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	service := NewServiceAt(root)
	if _, err := service.DeploySite(context.Background(), "demo", source, false); err == nil {
		t.Fatalf("expected symlink deployment to fail")
	}
}

func TestServiceRejectsOverlappingSourceAndManagedRoot(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	insideService := NewServiceAt(filepath.Join(source, ".localgate", "static"))
	if _, err := insideService.DeploySite(context.Background(), "demo", source, false); !errors.Is(err, ErrSourceOverlapsStore) {
		t.Fatalf("expected source parent overlap error, got %v", err)
	}

	managedRoot := filepath.Join(parent, "managed")
	insideSource := filepath.Join(managedRoot, "input")
	if err := os.MkdirAll(insideSource, 0o755); err != nil {
		t.Fatalf("mkdir managed source: %v", err)
	}
	outsideService := NewServiceAt(managedRoot)
	if _, err := outsideService.DeploySite(context.Background(), "demo", insideSource, false); !errors.Is(err, ErrSourceOverlapsStore) {
		t.Fatalf("expected source child overlap error, got %v", err)
	}
}

func TestServiceEnforcesDeploymentLimits(t *testing.T) {
	tests := []struct {
		name   string
		limits Limits
		files  map[string]string
	}{
		{
			name:   "file count",
			limits: Limits{MaxFiles: 1, MaxTotalBytes: 100, MaxFileBytes: 100},
			files:  map[string]string{"a.txt": "a", "b.txt": "b"},
		},
		{
			name:   "single file",
			limits: Limits{MaxFiles: 10, MaxTotalBytes: 100, MaxFileBytes: 4},
			files:  map[string]string{"a.txt": "12345"},
		},
		{
			name:   "total bytes",
			limits: Limits{MaxFiles: 10, MaxTotalBytes: 8, MaxFileBytes: 8},
			files:  map[string]string{"a.txt": "12345", "b.txt": "67890"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := t.TempDir()
			for name, content := range test.files {
				if err := os.WriteFile(filepath.Join(source, name), []byte(content), 0o644); err != nil {
					t.Fatalf("write file: %v", err)
				}
			}
			service := NewServiceAtWithLimits(filepath.Join(t.TempDir(), "managed"), test.limits)
			if _, err := service.DeploySite(context.Background(), "demo", source, false); !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("expected limit error, got %v", err)
			}
		})
	}
}

func TestServiceHonorsCanceledContext(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	root := filepath.Join(t.TempDir(), "managed")
	service := NewServiceAt(root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.DeploySite(ctx, "demo", source, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled context, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "demo")); !os.IsNotExist(err) {
		t.Fatalf("canceled deployment should not activate a site: %v", err)
	}
}

func TestStaticServerRejectsTraversalAndUnknownSite(t *testing.T) {
	service := NewServiceAt(filepath.Join(t.TempDir(), "managed"))
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeploySite(context.Background(), "demo", source, false); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		path string
	}{{name: "missing", path: "/"}, {name: "demo", path: "/../../secret"}} {
		req := httptest.NewRequest(http.MethodGet, test.path, nil)
		w := httptest.NewRecorder()
		service.ServeSite(test.name, w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for %s%s, got %d", test.name, test.path, w.Code)
		}
	}
}

func TestServiceRemovesSourceAfterSuccessfulDeployment(t *testing.T) {
	workspace := t.TempDir()
	source := filepath.Join(workspace, "dist")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("remove-me"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	root := filepath.Join(t.TempDir(), "managed")
	service := NewServiceAt(root)
	deployment, err := service.DeploySiteWithOptions(context.Background(), "demo", source, DeployOptions{RemoveSource: true})
	if err != nil {
		t.Fatalf("deploy with source removal: %v", err)
	}
	if deployment.SourcePreserved {
		t.Fatalf("expected source_preserved=false: %+v", deployment)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source directory should be deleted after deployment: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "demo", "index.html")); err != nil || string(got) != "remove-me" {
		t.Fatalf("deployed content missing after source deletion: content=%q err=%v", got, err)
	}
	matches, err := filepath.Glob(filepath.Join(workspace, ".localgate-source-*"))
	if err != nil {
		t.Fatalf("glob source staging directories: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("source removal staging directories were not cleaned: %#v", matches)
	}
}

func TestServicePreservesSourceByDefault(t *testing.T) {
	source := filepath.Join(t.TempDir(), "dist")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("keep-me"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	service := NewServiceAt(filepath.Join(t.TempDir(), "managed"))
	deployment, err := service.DeploySite(context.Background(), "demo", source, false)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if !deployment.SourcePreserved {
		t.Fatalf("expected source_preserved=true: %+v", deployment)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source directory should be preserved: %v", err)
	}
}
