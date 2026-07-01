package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"localgate/internal/approval"
	"localgate/internal/core"
	"localgate/internal/static"
)

func TestDeleteDeploymentToolDeletesStaticSite(t *testing.T) {
	managedRoot := filepath.Join(t.TempDir(), "managed")
	service := static.NewServiceAt(managedRoot)
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if _, err := service.DeploySite(context.Background(), "demo", source, false); err != nil {
		t.Fatalf("deploy site: %v", err)
	}

	tool := NewDeleteDeploymentTool(service)
	input, _ := json.Marshal(map[string]any{"type": "static", "name": "demo"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("delete deployment: %v", err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok || resultMap["status"] != "deleted" || resultMap["site"] != "demo" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if _, exists := service.GetSite("demo"); exists {
		t.Fatal("site should be removed from registry")
	}
	if _, err := os.Stat(filepath.Join(managedRoot, "demo")); !os.IsNotExist(err) {
		t.Fatalf("site directory should be removed: %v", err)
	}
}

func TestCoreDeleteDeploymentToolRequiresApproval(t *testing.T) {
	managedRoot := filepath.Join(t.TempDir(), "managed")
	service := static.NewServiceAt(managedRoot)
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if _, err := service.DeploySite(context.Background(), "demo", source, false); err != nil {
		t.Fatalf("deploy site: %v", err)
	}
	approvalService := approval.NewService(approval.NewStore(filepath.Join(t.TempDir(), "approval")))
	tool := NewCoreDeleteDeploymentTool(service, approvalService, true)
	args := map[string]any{"type": "static", "name": "demo"}

	if _, err := tool.Execute(context.Background(), core.InvokeContext{WorkspaceID: "ws"}, args); err == nil {
		t.Fatal("expected approval requirement")
	}
	if _, exists := service.GetSite("demo"); !exists {
		t.Fatal("site must remain before approval")
	}

	result, err := tool.Execute(context.Background(), core.InvokeContext{
		TaskID:      "task-delete",
		WorkspaceID: "ws",
		Requester:   "test",
		ApprovalID:  "approval-delete",
		Authorized:  true,
	}, args)
	if err != nil {
		t.Fatalf("approved delete: %v", err)
	}
	if result["status"] != "deleted" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if _, exists := service.GetSite("demo"); exists {
		t.Fatal("site should be removed after approved deletion")
	}
}

func TestDecodeDeleteDeploymentRequestRejectsUnknownFields(t *testing.T) {
	_, err := DecodeDeleteDeploymentRequest([]byte(`{"type":"static","name":"demo","extra":true}`))
	if err == nil {
		t.Fatal("expected unknown field error")
	}
}
