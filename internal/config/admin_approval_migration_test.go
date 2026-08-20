package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectLegacyApprovalConfigLocations(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "top-level approval",
			content: "approval:\n  enabled: true\nroot: /tmp\n",
			want:    "admin.approval",
		},
		{
			name:    "nested approval base URL",
			content: "admin:\n  approval:\n    base_url: https://admin.example.test\nroot: /tmp\n",
			want:    "admin.base_url",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "localgate.yaml")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(LoadOptions{Path: path, PathProvided: true})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected migration error containing %q, got %v", test.want, err)
			}
		})
	}
}

func TestRejectLegacyApprovalBaseURLEnvironmentVariable(t *testing.T) {
	t.Setenv("LOCALGATE_APPROVAL_BASE_URL", "https://legacy.example.test")
	_, err := Load(LoadOptions{})
	if err == nil || !strings.Contains(err.Error(), "LOCALGATE_ADMIN_BASE_URL") {
		t.Fatalf("expected environment migration error, got %v", err)
	}
}

func TestAdminDisabledDisablesApprovalSubfeature(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localgate.yaml")
	content := "admin:\n  enabled: false\n  approval:\n    enabled: true\nroot: /tmp\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(LoadOptions{Path: path, PathProvided: true})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Admin.Enabled || cfg.Admin.Approval.Enabled {
		t.Fatalf("approval must be disabled with admin: %#v", cfg.Admin)
	}
}
