package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFileExportsConfigAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "localgate.yaml")
	if err := os.WriteFile(path, []byte(`
http:
  base_url: "https://gate.example/"
file_exports:
  enabled: true
  default_ttl_seconds: 120
  max_ttl_seconds: 300
  max_file_bytes: 4096
  require_approval: true
root: "`+dir+`"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	withEnv(t, "LOCALGATE_FILE_EXPORTS_MAX_FILE_BYTES", "8192")
	cfg, err := Load(LoadOptions{Path: path, PathProvided: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.FileExports.Enabled || cfg.FileExports.BaseURL != "https://gate.example" {
		t.Fatalf("unexpected file export config: %#v", cfg.FileExports)
	}
	if cfg.FileExports.DefaultTTLSeconds != 120 || cfg.FileExports.MaxTTLSeconds != 300 || cfg.FileExports.MaxFileBytes != 8192 || !cfg.FileExports.RequireApproval {
		t.Fatalf("unexpected normalized file export config: %#v", cfg.FileExports)
	}
	summary := cfg.SafeSummary()
	fileExports, _ := summary["file_exports"].(map[string]any)
	if fileExports["enabled"] != true || fileExports["base_url"] != "https://gate.example" {
		t.Fatalf("file exports missing from safe summary: %#v", summary)
	}
}

func TestFileExportsDefaultDoesNotRequireApproval(t *testing.T) {
	cfg := Default()
	if cfg.FileExports.RequireApproval {
		t.Fatalf("file exports should not require approval by default: %#v", cfg.FileExports)
	}
}

func TestLoadRejectsInvalidFileExportsConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "localgate.yaml")
	if err := os.WriteFile(path, []byte(`
http:
  base_url: "https://gate.example"
file_exports:
  enabled: true
  base_url: "https://files.example"
  default_ttl_seconds: 30
  max_ttl_seconds: 20
  max_file_bytes: 1
root: "`+dir+`"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := Load(LoadOptions{Path: path, PathProvided: true})
	if err == nil || !strings.Contains(err.Error(), "same scheme and authority") {
		t.Fatalf("expected authority validation error, got %v", err)
	}
}

func TestLoadDedicatedFileExportListener(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "localgate.yaml")
	if err := os.WriteFile(path, []byte(`
bind: "127.0.0.1:2525"
admin:
  enabled: true
  bind: "127.0.0.1:2527"
deployments:
  public_bind: "127.0.0.1:2526"
  private_bind: "127.0.0.1:2528"
http:
  base_url: "https://macgate.example"
file_exports:
  enabled: true
  bind: "127.0.0.1:2529"
  base_url: "https://files.example"
root: "`+dir+`"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(LoadOptions{Path: path, PathProvided: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FileExports.Bind != "127.0.0.1:2529" || cfg.FileExports.BaseURL != "https://files.example" {
		t.Fatalf("unexpected dedicated export config: %#v", cfg.FileExports)
	}
	summary, ok := cfg.SafeSummary()["file_exports"].(map[string]any)
	if !ok || summary["bind"] != cfg.FileExports.Bind {
		t.Fatalf("missing export listener bind in summary: %#v", summary)
	}
	wildcard := cfg
	wildcard.FileExports.Bind = "0.0.0.0:2529"
	if err := validateExportListenerBind(wildcard.FileExports, wildcard.Bind, wildcard.Admin.Bind, wildcard.Deployments.PublicBind, wildcard.Deployments.PrivateBind, wildcard.Admin.Enabled); err != nil {
		t.Fatalf("wildcard file export listener should be allowed: %v", err)
	}
	for _, tc := range []struct{ bind, want string }{
		{"192.168.1.10:2529", "loopback"},
		{"127.0.0.1:2525", "different TCP port"},
		{"127.0.0.1:2526", "different TCP port"},
		{"127.0.0.1:2527", "different TCP port"},
		{"127.0.0.1:2528", "different TCP port"},
		{"127.0.0.1:65536", "port"},
	} {
		t.Run(tc.bind, func(t *testing.T) {
			bad := cfg
			bad.FileExports.Bind = tc.bind
			err := validateExportListenerBind(bad.FileExports, bad.Bind, bad.Admin.Bind, bad.Deployments.PublicBind, bad.Deployments.PrivateBind, bad.Admin.Enabled)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("bind %q: expected %q error, got %v", tc.bind, tc.want, err)
			}
		})
	}
}
