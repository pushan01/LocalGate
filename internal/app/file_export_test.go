package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"localgate/internal/config"
)

func TestExportFileRequiresApprovalByDefault(t *testing.T) {
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
