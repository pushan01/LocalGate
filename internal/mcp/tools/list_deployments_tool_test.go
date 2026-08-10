package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"localgate/internal/static"
)

func TestListDeploymentsDescriptionIncludesAccessMode(t *testing.T) {
	tool := &ListDeploymentsTool{}
	if got := tool.Description(); !strings.Contains(got, "access mode") || strings.Contains(got, "public URLs") {
		t.Fatalf("unexpected list_deployments description: %q", got)
	}
}

func TestListDeploymentsToolReturnsSortedDeployments(t *testing.T) {
	service := static.NewServiceAt(filepath.Join(t.TempDir(), "managed"))
	for _, name := range []string{"zeta", "alpha"} {
		source := t.TempDir()
		if err := os.WriteFile(filepath.Join(source, "index.html"), []byte(name), 0o644); err != nil {
			t.Fatalf("write source: %v", err)
		}
		if _, err := service.DeploySite(context.Background(), name, source, false); err != nil {
			t.Fatalf("deploy %s: %v", name, err)
		}
	}

	tool := NewListDeploymentsTool(service, "https://apps.example.test/")
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("list deployments: %v", err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok || resultMap["status"] != "ok" || resultMap["count"] != 2 {
		t.Fatalf("unexpected result: %#v", result)
	}
	items, ok := resultMap["deployments"].([]map[string]any)
	if !ok || len(items) != 2 {
		t.Fatalf("unexpected deployments: %#v", resultMap["deployments"])
	}
	if items[0]["site"] != "alpha" || items[1]["site"] != "zeta" {
		t.Fatalf("deployments should be sorted: %#v", items)
	}
	if items[0]["url"] != "https://alpha.apps.example.test/" || items[0]["hostname"] != "alpha.apps.example.test" {
		t.Fatalf("unexpected public url: %#v", items[0])
	}
}

func TestListDeploymentsToolRejectsArguments(t *testing.T) {
	tool := NewListDeploymentsTool(static.NewServiceAt(filepath.Join(t.TempDir(), "managed")), "")
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"unexpected":true}`)); err == nil {
		t.Fatal("expected arguments to be rejected")
	}
}
