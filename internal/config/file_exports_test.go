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
workspaces:
  - id: ws
    roots: ["`+dir+`"]
    cwd: "`+dir+`"
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
workspaces:
  - id: ws
    roots: ["`+dir+`"]
    cwd: "`+dir+`"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := Load(LoadOptions{Path: path, PathProvided: true})
	if err == nil || !strings.Contains(err.Error(), "same scheme and authority") {
		t.Fatalf("expected authority validation error, got %v", err)
	}
}
