package app

import (
	"path/filepath"
	"testing"

	"localgate/internal/config"
	"localgate/internal/core"
	"localgate/internal/invocation"
)

func TestSubmitToolTaskWithMetaPersistsSessionAcrossApproval(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.Approval.Enabled = true
	cfg.Admin.BaseURL = "https://admin.test"
	cfg.Admin.Approval.Rules = []config.ApprovalRule{{ID: "approve-write", Effect: "require_approval", Tools: []string{"write_file"}, Risk: "high"}}
	cfg.Workspaces = []config.Workspace{{ID: "ws", Roots: []string{root}, CWD: root}}
	application, err := New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	submission, err := application.SubmitToolTaskWithMeta(invocation.Meta{
		Transport:       invocation.TransportMCPHTTP,
		WorkspaceID:     "ws",
		Requester:       "mcp",
		MCPSessionID:    "client-session",
		SessionMode:     invocation.SessionModeStateless,
		SessionIDSource: invocation.SessionSourceClientSupplied,
		RequestID:       "request-1",
	}, "write_file", map[string]any{"path": filepath.Join(root, "file.txt"), "content": "hello"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if submission.Approval == nil || submission.Task == nil {
		t.Fatalf("expected gated submission: %#v", submission)
	}
	if submission.Task.Status != core.StatusAwaitingApproval || submission.Task.MCPSessionID != "client-session" {
		t.Fatalf("unexpected task metadata: %#v", submission.Task)
	}
	if submission.Approval.MCPSessionID != "client-session" || submission.Approval.SessionIDSource != invocation.SessionSourceClientSupplied {
		t.Fatalf("unexpected approval metadata: %#v", submission.Approval)
	}
	audits, err := application.Approvals.QueryAudit(map[string]string{"session_id": "client-session"}, 10, 0)
	if err != nil || len(audits) == 0 {
		t.Fatalf("expected session audit, records=%#v err=%v", audits, err)
	}
}
