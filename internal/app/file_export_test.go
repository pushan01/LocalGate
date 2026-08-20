package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"localgate/internal/config"
	"localgate/internal/core"
)

type artifactApprovalTestTool struct{}

func (artifactApprovalTestTool) Name() string        { return "subagent_artifact_get" }
func (artifactApprovalTestTool) Description() string { return "test subagent artifact export" }
func (artifactApprovalTestTool) Schema() any         { return map[string]any{"type": "object"} }
func (artifactApprovalTestTool) Execute(context.Context, core.InvokeContext, map[string]any) (map[string]any, error) {
	return map[string]any{"status": "ready"}, nil
}

func TestFileAndSubagentArtifactExportsDoNotRequireApprovalByDefault(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "report.txt")
	if err := os.WriteFile(target, []byte("report"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	application, err := New(config.Config{
		LogStore: t.TempDir(),
		HTTP:     config.HTTP{BaseURL: "https://gate.example"},
		Admin: config.AdminConfig{
			Enabled: true,
			BaseURL: "https://gate.example",
			Approval: config.ApprovalConfig{
				Enabled:            true,
				DefaultTTLSeconds:  600,
				MaxSyncWaitSeconds: 30,
			},
		},
		FileExports: config.FileExports{
			Enabled:           true,
			BaseURL:           "https://gate.example",
			DefaultTTLSeconds: 300,
			MaxTTLSeconds:     600,
			MaxFileBytes:      1 << 20,
		},
		Workspaces: []config.Workspace{{ID: "ws", Roots: []string{root}, CWD: root}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	application.Registries["ws"].Register(artifactApprovalTestTool{})

	fileSubmission, err := application.SubmitToolTask("ws", "test", "export_file", map[string]any{
		"path": target,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fileSubmission.Approval != nil {
		t.Fatalf("export_file unexpectedly required approval: %#v", fileSubmission.Approval)
	}

	artifactSubmission, err := application.SubmitToolTask("ws", "test", "subagent_artifact_get", map[string]any{
		"subagent_id": "sg_example",
		"artifact_id": "art_example",
	})
	if err != nil {
		t.Fatal(err)
	}
	if artifactSubmission.Approval != nil {
		t.Fatalf("subagent_artifact_get unexpectedly required approval: %#v", artifactSubmission.Approval)
	}
}

func TestExportFileRequiresApprovalWhenConfigured(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "report.txt")
	if err := os.WriteFile(target, []byte("report"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	application, err := New(config.Config{
		LogStore: t.TempDir(),
		HTTP:     config.HTTP{BaseURL: "https://gate.example"},
		Admin: config.AdminConfig{
			Enabled: true,
			BaseURL: "https://gate.example",
			Approval: config.ApprovalConfig{
				Enabled:            true,
				DefaultTTLSeconds:  600,
				MaxSyncWaitSeconds: 30,
			},
		},
		FileExports: config.FileExports{
			Enabled:           true,
			BaseURL:           "https://gate.example",
			DefaultTTLSeconds: 300,
			MaxTTLSeconds:     600,
			MaxFileBytes:      1 << 20,
			RequireApproval:   true,
		},
		Workspaces: []config.Workspace{{ID: "ws", Roots: []string{root}, CWD: root}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer application.Close()

	submission, err := application.SubmitToolTask("ws", "test", "export_file", map[string]any{
		"path":      target,
		"file_name": "shared-report.txt",
	})
	if err != nil {
		t.Fatalf("SubmitToolTask: %v", err)
	}
	if submission.Approval == nil {
		t.Fatal("expected file export to require approval")
	}
	if submission.Approval.RiskLevel != "high" || !strings.Contains(submission.Approval.Summary, target) || !strings.Contains(submission.Approval.Summary, "shared-report.txt") {
		t.Fatalf("unexpected approval: %#v", submission.Approval)
	}
}

func TestSubagentArtifactGetRequiresApprovalWhenConfigured(t *testing.T) {
	root := t.TempDir()
	application, err := New(config.Config{
		LogStore: t.TempDir(),
		HTTP:     config.HTTP{BaseURL: "https://gate.example"},
		Admin: config.AdminConfig{
			Enabled: true,
			BaseURL: "https://gate.example",
			Approval: config.ApprovalConfig{
				Enabled:            true,
				DefaultTTLSeconds:  600,
				MaxSyncWaitSeconds: 30,
			},
		},
		FileExports: config.FileExports{
			Enabled:           true,
			BaseURL:           "https://gate.example",
			DefaultTTLSeconds: 300,
			MaxTTLSeconds:     600,
			MaxFileBytes:      1 << 20,
			RequireApproval:   true,
		},
		Workspaces: []config.Workspace{{ID: "ws", Roots: []string{root}, CWD: root}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	application.Registries["ws"].Register(artifactApprovalTestTool{})

	submission, err := application.SubmitToolTask("ws", "test", "subagent_artifact_get", map[string]any{
		"subagent_id": "sg_example",
		"artifact_id": "art_example",
	})
	if err != nil {
		t.Fatal(err)
	}
	if submission.Approval == nil || submission.Approval.RiskLevel != "high" {
		t.Fatalf("expected artifact export approval, got %#v", submission.Approval)
	}
	if !strings.Contains(submission.Approval.Summary, "art_example") || !strings.Contains(submission.Approval.Summary, "sg_example") {
		t.Fatalf("unexpected artifact approval summary: %#v", submission.Approval)
	}
}
