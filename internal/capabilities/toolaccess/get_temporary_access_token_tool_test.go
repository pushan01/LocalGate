package toolaccess

import (
	"context"
	"testing"
	"time"

	"localgate/internal/core"
	service "localgate/internal/toolaccess"
)

func TestGetTemporaryAccessTokenTool(t *testing.T) {
	tool := NewGetTemporaryAccessTokenTool("http://127.0.0.1:8000", time.Minute, service.NewService())
	res, err := tool.Execute(context.Background(), core.InvokeContext{WorkspaceID: "ws"}, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got, _ := res["workspace_id"].(string); got != "ws" {
		t.Fatalf("unexpected workspace id: %#v", res)
	}
	if got, _ := res["auth_header_name"].(string); got != service.HeaderName {
		t.Fatalf("unexpected auth header: %#v", res)
	}
	if got, _ := res["base_url"].(string); got != "http://127.0.0.1:8000" {
		t.Fatalf("unexpected base url: %#v", res)
	}
	if got, _ := res["call_path_pattern"].(string); got != "/tools/{tool}/call" {
		t.Fatalf("unexpected path pattern: %#v", res)
	}
	if got, _ := res["auth_value"].(string); got == "" {
		t.Fatalf("expected auth value: %#v", res)
	}
}

func TestGetTemporaryAccessTokenToolRequiresWorkspace(t *testing.T) {
	tool := NewGetTemporaryAccessTokenTool("http://127.0.0.1:8000", time.Minute, service.NewService())
	_, err := tool.Execute(context.Background(), core.InvokeContext{}, nil)
	if err == nil || err.Error() != "workspace_id is required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetTemporaryAccessTokenToolRequiresBaseURL(t *testing.T) {
	tool := NewGetTemporaryAccessTokenTool("", time.Minute, service.NewService())
	_, err := tool.Execute(context.Background(), core.InvokeContext{WorkspaceID: "ws"}, nil)
	if err == nil || err.Error() != "tool http base_url is not configured" {
		t.Fatalf("unexpected error: %v", err)
	}
}
