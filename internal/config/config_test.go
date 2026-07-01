package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func withEnv(t *testing.T, key, value string) {
	t.Helper()
	old, ok := os.LookupEnv(key)
	if value == "" {
		_ = os.Unsetenv(key)
	} else {
		_ = os.Setenv(key, value)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, old)
			return
		}
		_ = os.Unsetenv(key)
	})
}

func TestLoadSessionCleanupConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	err := os.WriteFile(cfgPath, []byte(`
session_cleanup:
  finished_ttl_seconds: 11
  idle_ttl_seconds: 22
  max_sessions: 33
  sweep_interval_ms: 44
workspaces:
  - id: ws
`), 0o644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.SessionCleanup.FinishedTTLSeconds != 11 {
		t.Fatalf("unexpected finished ttl: %d", cfg.SessionCleanup.FinishedTTLSeconds)
	}
	if cfg.SessionCleanup.IdleTTLSeconds != 22 {
		t.Fatalf("unexpected idle ttl: %d", cfg.SessionCleanup.IdleTTLSeconds)
	}
	if cfg.SessionCleanup.MaxSessions != 33 {
		t.Fatalf("unexpected max sessions: %d", cfg.SessionCleanup.MaxSessions)
	}
	if cfg.SessionCleanup.SweepIntervalMs != 44 {
		t.Fatalf("unexpected sweep interval: %d", cfg.SessionCleanup.SweepIntervalMs)
	}
}

func TestLoadHTTPAuthConfig(t *testing.T) {
	withEnv(t, "LOCALGATE_HTTP_BEARER_TOKEN", "")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	err := os.WriteFile(cfgPath, []byte(`
http_auth:
  enabled: true
  bearer_token: "secret-token"
  allow_paths:
    - /healthz
    - /public
workspaces:
  - id: ws
`), 0o644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.HTTPAuth.Enabled {
		t.Fatalf("expected http auth enabled")
	}
	if cfg.HTTPAuth.BearerToken != "secret-token" {
		t.Fatalf("unexpected bearer token: %q", cfg.HTTPAuth.BearerToken)
	}
	if len(cfg.HTTPAuth.AllowPaths) != 2 {
		t.Fatalf("unexpected allow paths: %#v", cfg.HTTPAuth.AllowPaths)
	}

	summary := cfg.SafeSummary()
	summaryText := summary["http_auth"]
	if strings.Contains(strings.ToLower(toString(summaryText)), "secret-token") {
		t.Fatalf("safe summary should not expose bearer token: %#v", summary)
	}
}

func TestLoadAdminApprovalConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localgate.yaml")
	content := `
admin:
  enabled: true
  base_url: https://admin.example.test
  approval:
    enabled: true
    default_ttl_seconds: 120
workspaces:
  - id: ws
    roots: ["."]
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(LoadOptions{Path: path, PathProvided: true})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Admin.Enabled || cfg.Admin.BaseURL != "https://admin.example.test" || !cfg.Admin.Approval.Enabled || cfg.Admin.Approval.DefaultTTLSeconds != 120 {
		t.Fatalf("unexpected admin config: %#v", cfg.Admin)
	}
}

func TestLoadAdminConfigRejectsInvalidBaseURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localgate.yaml")
	content := `
admin:
  enabled: true
  base_url: not-a-url
workspaces:
  - id: ws
    roots: ["."]
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(LoadOptions{Path: path, PathProvided: true}); err == nil {
		t.Fatal("expected invalid admin base URL")
	}
}

func TestLoadHTTPAuthRequiresBearerToken(t *testing.T) {
	withEnv(t, "LOCALGATE_HTTP_BEARER_TOKEN", "")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	err := os.WriteFile(cfgPath, []byte(`
http_auth:
  enabled: true
workspaces:
  - id: ws
`), 0o644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err = Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err == nil {
		t.Fatalf("expected load error")
	}
	if !strings.Contains(err.Error(), "http_auth.bearer_token") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadUpgradeTokenFromEnvOnly(t *testing.T) {
	withEnv(t, "LOCALGATE_UPGRADE_TOKEN", "upgrade-secret")
	withEnv(t, "LOCALGATE_RUNTIME_MODE", "http_daemon")
	withEnv(t, "LOCALGATE_RUNTIME_BIND", "127.0.0.1:8000")

	cfg, err := Load(LoadOptions{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Upgrade.Token != "upgrade-secret" {
		t.Fatalf("unexpected upgrade token: %q", cfg.Upgrade.Token)
	}
	if cfg.Runtime.Mode != "http_daemon" {
		t.Fatalf("unexpected runtime mode: %q", cfg.Runtime.Mode)
	}
	if cfg.Runtime.Bind != "127.0.0.1:8000" {
		t.Fatalf("unexpected runtime bind: %q", cfg.Runtime.Bind)
	}

	summary := cfg.SafeSummary()
	if strings.Contains(strings.ToLower(toString(summary)), "upgrade-secret") {
		t.Fatalf("safe summary should not expose upgrade token: %#v", summary)
	}
	upgradeSummary, _ := summary["upgrade"].(map[string]any)
	if enabled, _ := upgradeSummary["enabled"].(bool); !enabled {
		t.Fatalf("expected upgrade enabled in summary: %#v", summary)
	}
}

func TestLoadHTTPBaseURLConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	err := os.WriteFile(cfgPath, []byte(`
http:
  base_url: "http://127.0.0.1:8000///"
workspaces:
  - id: ws
`), 0o644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.HTTP.BaseURL != "http://127.0.0.1:8000" {
		t.Fatalf("unexpected base url: %q", cfg.HTTP.BaseURL)
	}
	summary := cfg.SafeSummary()
	httpSummary, _ := summary["http"].(map[string]any)
	if httpSummary["base_url"] != "http://127.0.0.1:8000" {
		t.Fatalf("unexpected safe summary http config: %#v", summary)
	}
}

func TestLoadHTTPBaseURLFromEnv(t *testing.T) {
	withEnv(t, "LOCALGATE_HTTP_BASE_URL", "https://example.com/localgate/")

	cfg, err := Load(LoadOptions{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.HTTP.BaseURL != "https://example.com/localgate" {
		t.Fatalf("unexpected base url from env: %q", cfg.HTTP.BaseURL)
	}
}

func TestLoadHTTPAllowCORSConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	err := os.WriteFile(cfgPath, []byte(`
http:
  allow_cors: true
  allowed_origins:
    - http://localhost:3000/
    - https://example.com
workspaces:
  - id: ws
`), 0o644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.HTTP.AllowCORS {
		t.Fatalf("expected allow_cors enabled")
	}
	if got := cfg.HTTP.AllowedOrigins; len(got) != 2 || got[0] != "http://localhost:3000" || got[1] != "https://example.com" {
		t.Fatalf("unexpected allowed origins: %#v", got)
	}
}

func TestLoadHTTPAllowCORSFromEnv(t *testing.T) {
	withEnv(t, "LOCALGATE_HTTP_ALLOW_CORS", "true")
	withEnv(t, "LOCALGATE_HTTP_ALLOWED_ORIGINS", "http://localhost:3000, https://example.com")
	cfg, err := Load(LoadOptions{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.HTTP.AllowCORS || len(cfg.HTTP.AllowedOrigins) != 2 {
		t.Fatalf("unexpected cors config: %#v", cfg.HTTP)
	}
}

func TestLoadHTTPAllowCORSRequiresOrigins(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	if err := os.WriteFile(cfgPath, []byte("http:\n  allow_cors: true\nworkspaces:\n  - id: ws\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err == nil || !strings.Contains(err.Error(), "allowed_origins") {
		t.Fatalf("expected allowed_origins validation error, got %v", err)
	}
}

func TestLoadHTTPRejectsWildcardOrigin(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	if err := os.WriteFile(cfgPath, []byte("http:\n  allowed_origins: [\"*\"]\nworkspaces:\n  - id: ws\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err == nil || !strings.Contains(err.Error(), "wildcard") {
		t.Fatalf("expected wildcard validation error, got %v", err)
	}
}

func TestLoadMCPHTTPConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	err := os.WriteFile(cfgPath, []byte(`
mcp_http:
  stateless: true
workspaces:
  - id: ws
`), 0o644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.MCPHTTP.Stateless {
		t.Fatalf("expected mcp_http.stateless enabled")
	}
	summary := cfg.SafeSummary()
	mcpHTTPSummary, _ := summary["mcp_http"].(map[string]any)
	if stateless, _ := mcpHTTPSummary["stateless"].(bool); !stateless {
		t.Fatalf("unexpected safe summary mcp_http config: %#v", summary)
	}
}

func TestLoadMCPHTTPFromEnv(t *testing.T) {
	withEnv(t, "LOCALGATE_MCP_HTTP_STATELESS", "true")
	cfg, err := Load(LoadOptions{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.MCPHTTP.Stateless {
		t.Fatalf("expected mcp_http.stateless from env")
	}
}

func TestLoadToolArgumentsConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	err := os.WriteFile(cfgPath, []byte(`
tool_arguments:
  enable_base64: true
workspaces:
  - id: ws
`), 0o644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.ToolArguments.EnableBase64 {
		t.Fatalf("expected enable_base64 enabled")
	}
	summary := cfg.SafeSummary()
	toolArgsSummary, _ := summary["tool_arguments"].(map[string]any)
	if enabled, _ := toolArgsSummary["enable_base64"].(bool); !enabled {
		t.Fatalf("unexpected safe summary tool arguments config: %#v", summary)
	}
}

func TestLoadToolArgumentsFromEnv(t *testing.T) {
	withEnv(t, "LOCALGATE_TOOL_ARGUMENTS_ENABLE_BASE64", "true")
	cfg, err := Load(LoadOptions{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.ToolArguments.EnableBase64 {
		t.Fatalf("expected enable_base64 from env")
	}
}

func TestLoadWorkspaceHiddenTools(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	err := os.WriteFile(cfgPath, []byte(`
workspaces:
  - id: ws
    hidden_tools:
      - run_command
      - exec_command
`), 0o644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.Workspaces) != 1 {
		t.Fatalf("unexpected workspaces: %#v", cfg.Workspaces)
	}
	if got := cfg.Workspaces[0].HiddenTools; len(got) != 2 || got[0] != "run_command" || got[1] != "exec_command" {
		t.Fatalf("unexpected hidden tools: %#v", got)
	}
}

func TestLoadMCPServersConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	err := os.WriteFile(cfgPath, []byte(`
mcp_servers:
  - name: github
    command: npx
    args:
      - -y
      - "@modelcontextprotocol/server-github"
    env:
      GITHUB_TOKEN: secret-token
    cwd: /tmp
workspaces:
  - id: ws
`), 0o644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.MCPServers) != 1 {
		t.Fatalf("unexpected mcp servers: %#v", cfg.MCPServers)
	}
	srv := cfg.MCPServers[0]
	if srv.Name != "github" || srv.Command != "npx" || srv.CWD != "/tmp" {
		t.Fatalf("unexpected mcp server: %#v", srv)
	}
	if len(srv.Args) != 2 || srv.Args[1] != "@modelcontextprotocol/server-github" {
		t.Fatalf("unexpected mcp args: %#v", srv.Args)
	}
	summary := cfg.SafeSummary()
	if strings.Contains(strings.ToLower(toString(summary)), "secret-token") {
		t.Fatalf("safe summary should not expose mcp env values: %#v", summary)
	}
}

func TestLoadMCPServersRejectsDuplicateNames(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	err := os.WriteFile(cfgPath, []byte(`
mcp_servers:
  - name: github
    command: npx
  - name: github
    command: uvx
workspaces:
  - id: ws
`), 0o644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err = Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err == nil || !strings.Contains(err.Error(), "duplicate mcp server name") {
		t.Fatalf("expected duplicate name error, got: %v", err)
	}
}

func TestLoadToolHTTPModeConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	err := os.WriteFile(cfgPath, []byte(`
http:
  base_url: "http://127.0.0.1:8000/"
tool_http_mode:
  enabled: true
  access_token_ttl_seconds: 321
workspaces:
  - id: ws
`), 0o644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.ToolHTTPMode.Enabled {
		t.Fatalf("expected tool http mode enabled")
	}
	if cfg.ToolHTTPMode.AccessTokenTTLSeconds != 321 {
		t.Fatalf("unexpected access ttl: %d", cfg.ToolHTTPMode.AccessTokenTTLSeconds)
	}
	summary := cfg.SafeSummary()
	mode, _ := summary["tool_http_mode"].(map[string]any)
	if enabled, _ := mode["enabled"].(bool); !enabled {
		t.Fatalf("unexpected safe summary mode: %#v", summary)
	}
}

func TestLoadToolHTTPModeRequiresBaseURL(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	err := os.WriteFile(cfgPath, []byte(`
tool_http_mode:
  enabled: true
workspaces:
  - id: ws
`), 0o644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err = Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err == nil || !strings.Contains(err.Error(), "http.base_url") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadToolHTTPModeRequiresPositiveTTL(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	err := os.WriteFile(cfgPath, []byte(`
http:
  base_url: "http://127.0.0.1:8000"
tool_http_mode:
  enabled: true
  access_token_ttl_seconds: 0
workspaces:
  - id: ws
`), 0o644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err = Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err == nil || !strings.Contains(err.Error(), "tool_http_mode.access_token_ttl_seconds") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadMCPServersRejectsRelativeCWD(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	err := os.WriteFile(cfgPath, []byte(`
mcp_servers:
  - name: github
    command: npx
    cwd: ./relative
workspaces:
  - id: ws
`), 0o644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err = Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err == nil || !strings.Contains(err.Error(), "mcp_servers[0].cwd must be absolute") {
		t.Fatalf("expected relative cwd error, got: %v", err)
	}
}

func TestMergeHiddenToolsIncludesDefaults(t *testing.T) {
	got := MergeHiddenTools([]string{"run_command", "search_files"})
	want := []string{"run_argv", "search_files", "list_directory", "create_upload_token", "create_download_token", "run_command"}
	if len(got) != len(want) {
		t.Fatalf("unexpected merged hidden tools: %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected merged hidden tools: %#v", got)
		}
	}
	for _, name := range got {
		if name == "get_tool_schema" {
			t.Fatalf("get_tool_schema must not be hidden by default: %#v", got)
		}
	}
}

func toString(v any) string {
	return fmt.Sprintf("%v", v)
}

func TestDefaultStaticHostingConfig(t *testing.T) {
	cfg := Default()
	if cfg.StaticHosting.MaxTotalBytes != 50<<20 || cfg.StaticHosting.MaxFileBytes != 50<<20 {
		t.Fatalf("unexpected default static size limits: %#v", cfg.StaticHosting)
	}
	if cfg.StaticHosting.MaxFiles != 10_000 || !cfg.StaticHosting.RequireApproval {
		t.Fatalf("unexpected default static config: %#v", cfg.StaticHosting)
	}
}

func TestDefaultDynamicHostingConfig(t *testing.T) {
	cfg := Default().DynamicHosting
	if cfg.Enabled || !cfg.RequireApproval || cfg.PublicScheme != "https" {
		t.Fatalf("unexpected dynamic defaults: %#v", cfg)
	}
	if cfg.MaxApps != 20 || cfg.MaxFiles != 20_000 || cfg.RestartPolicy != "on-failure" {
		t.Fatalf("unexpected dynamic limits: %#v", cfg)
	}
	if cfg.RestartResetSeconds != 60 || cfg.MaxLogFiles != 3 || cfg.ReleaseRetention != 3 || cfg.MaxRequestBodyBytes != 32<<20 || cfg.MaxConcurrentRequests != 100 || cfg.UpstreamHeaderTimeoutSeconds != 30 || cfg.MaxResponseHeaderBytes != 1<<20 {
		t.Fatalf("unexpected dynamic production defaults: %#v", cfg)
	}
}

func TestDefaultApprovalConfig(t *testing.T) {
	cfg := Default()
	if !cfg.Admin.Approval.Enabled {
		t.Fatal("approval should be enabled by default")
	}
	if cfg.Admin.Approval.DefaultTTLSeconds != 600 {
		t.Fatalf("unexpected approval ttl: %d", cfg.Admin.Approval.DefaultTTLSeconds)
	}
	if cfg.Admin.Approval.MaxSyncWaitSeconds != 30 {
		t.Fatalf("unexpected approval sync wait: %d", cfg.Admin.Approval.MaxSyncWaitSeconds)
	}
}

func TestLoadApprovalConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	content := "admin:\n  base_url: https://admin.example.com/root///\n  approval:\n    enabled: false\n    default_ttl_seconds: 120\n    max_sync_wait_seconds: 7\nworkspaces:\n  - id: ws\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Admin.BaseURL != "https://admin.example.com/root" || cfg.Admin.Approval.Enabled || cfg.Admin.Approval.DefaultTTLSeconds != 120 || cfg.Admin.Approval.MaxSyncWaitSeconds != 7 {
		t.Fatalf("unexpected approval config: %#v", cfg.Admin.Approval)
	}
	adminSummary, _ := cfg.SafeSummary()["admin"].(map[string]any)
	approvalSummary, _ := adminSummary["approval"].(map[string]any)
	if adminSummary["base_url"] != "https://admin.example.com/root" || approvalSummary["enabled"] != false || approvalSummary["default_ttl_seconds"] != 120 || approvalSummary["max_sync_wait_seconds"] != 7 {
		t.Fatalf("unexpected admin approval summary: %#v", adminSummary)
	}
}

func TestLoadApprovalConfigFromEnv(t *testing.T) {
	withEnv(t, "LOCALGATE_ADMIN_BASE_URL", "https://admin.example.com/path/")
	withEnv(t, "LOCALGATE_APPROVAL_ENABLED", "false")
	withEnv(t, "LOCALGATE_APPROVAL_DEFAULT_TTL_SECONDS", "90")
	withEnv(t, "LOCALGATE_APPROVAL_MAX_SYNC_WAIT_SECONDS", "5")
	cfg, err := Load(LoadOptions{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Admin.BaseURL != "https://admin.example.com/path" || cfg.Admin.Approval.Enabled || cfg.Admin.Approval.DefaultTTLSeconds != 90 || cfg.Admin.Approval.MaxSyncWaitSeconds != 5 {
		t.Fatalf("unexpected approval env config: %#v", cfg.Admin.Approval)
	}
}

func TestLoadApprovalConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "non-positive ttl",
			content: "admin:\n  approval:\n    default_ttl_seconds: 0\nworkspaces:\n  - id: ws\n",
			want:    "admin.approval.default_ttl_seconds",
		},
		{
			name:    "negative sync wait",
			content: "admin:\n  approval:\n    max_sync_wait_seconds: -1\nworkspaces:\n  - id: ws\n",
			want:    "admin.approval.max_sync_wait_seconds",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "localgate.yaml")
			if err := os.WriteFile(cfgPath, []byte(test.content), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			_, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q validation error, got %v", test.want, err)
			}
		})
	}
}

func TestLoadApprovalRules(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	content := "admin:\n  base_url: https://admin.example.com\n  approval:\n    rules:\n      - id: delete-static\n        effect: require_approval\n        tools: [delete_deployment]\n        risk: critical\nworkspaces:\n  - id: ws\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.Admin.Approval.Rules) != 1 {
		t.Fatalf("unexpected rules: %#v", cfg.Admin.Approval.Rules)
	}
	rule := cfg.Admin.Approval.Rules[0]
	if rule.ID != "delete-static" || rule.Effect != "require_approval" || rule.Risk != "critical" || len(rule.Tools) != 1 || rule.Tools[0] != "delete_deployment" {
		t.Fatalf("unexpected rule: %#v", rule)
	}
}

func TestLoadApprovalRulesRejectInvalidEffect(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	content := "admin:\n  base_url: https://admin.example.com\n  approval:\n    rules:\n      - id: bad\n        effect: maybe\n        tools: [run_command]\nworkspaces:\n  - id: ws\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err == nil || !strings.Contains(err.Error(), "effect") {
		t.Fatalf("expected invalid effect error, got %v", err)
	}
}

func TestLoadStaticHostingConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	content := "static_hosting:\n  max_files: 123\n  max_total_bytes: 4096\n  max_file_bytes: 2048\n  require_approval: false\nworkspaces:\n  - id: ws\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.StaticHosting.MaxFiles != 123 || cfg.StaticHosting.MaxTotalBytes != 4096 || cfg.StaticHosting.MaxFileBytes != 2048 || cfg.StaticHosting.RequireApproval {
		t.Fatalf("unexpected static config: %#v", cfg.StaticHosting)
	}
}

func TestLoadStaticHostingFromEnv(t *testing.T) {
	withEnv(t, "LOCALGATE_STATIC_MAX_FILES", "77")
	withEnv(t, "LOCALGATE_STATIC_MAX_TOTAL_BYTES", "1000")
	withEnv(t, "LOCALGATE_STATIC_MAX_FILE_BYTES", "500")
	withEnv(t, "LOCALGATE_STATIC_REQUIRE_APPROVAL", "false")
	cfg, err := Load(LoadOptions{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.StaticHosting.MaxFiles != 77 || cfg.StaticHosting.MaxTotalBytes != 1000 || cfg.StaticHosting.MaxFileBytes != 500 || cfg.StaticHosting.RequireApproval {
		t.Fatalf("unexpected static env config: %#v", cfg.StaticHosting)
	}
}

func TestLoadStaticHostingRejectsInvalidLimits(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	content := "static_hosting:\n  max_files: 10\n  max_total_bytes: 100\n  max_file_bytes: 200\nworkspaces:\n  - id: ws\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err == nil || !strings.Contains(err.Error(), "max_file_bytes") {
		t.Fatalf("expected static limit validation error, got %v", err)
	}
}

func TestLoadDynamicHostingConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	content := `
dynamic_hosting:
  enabled: true
  control_hosts: [Control.Example.Test.]
  app_domain: Apps.Example.Test.
  public_scheme: http
  allowed_executables: [node]
  env_allowlist: [NODE_ENV]
  max_apps: 3
workspaces:
  - id: ws
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err != nil {
		t.Fatalf("load dynamic config: %v", err)
	}
	dynamic := cfg.DynamicHosting
	if !dynamic.Enabled || dynamic.AppDomain != "apps.example.test" || dynamic.ControlHosts[0] != "control.example.test" || dynamic.PublicScheme != "http" || dynamic.MaxApps != 3 {
		t.Fatalf("unexpected dynamic config: %#v", dynamic)
	}
	summary := cfg.SafeSummary()["dynamic_hosting"].(map[string]any)
	if summary["enabled"] != true || summary["app_domain"] != "apps.example.test" {
		t.Fatalf("unexpected dynamic summary: %#v", summary)
	}
}

func TestLoadDynamicHostingRequiresRoutingAndExecutableConfig(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"missing app domain", "dynamic_hosting:\n  enabled: true\n  control_hosts: [control.test]\n  allowed_executables: [node]\nworkspaces:\n  - id: ws\n", "app_domain"},
		{"missing control hosts", "dynamic_hosting:\n  enabled: true\n  app_domain: apps.test\n  allowed_executables: [node]\nworkspaces:\n  - id: ws\n", "control_hosts"},
		{"missing executables", "dynamic_hosting:\n  enabled: true\n  app_domain: apps.test\n  control_hosts: [control.test]\nworkspaces:\n  - id: ws\n", "allowed_executables"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "localgate.yaml")
			if err := os.WriteFile(path, []byte(test.content), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(LoadOptions{Path: path, PathProvided: true})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q error, got %v", test.want, err)
			}
		})
	}
}

func TestLoadDynamicHostingProductionLimitsFromEnv(t *testing.T) {
	values := map[string]string{
		"LOCALGATE_DYNAMIC_REQUIRE_APPROVAL":                "false",
		"LOCALGATE_DYNAMIC_RESERVED_NAMES":                  "one,two",
		"LOCALGATE_DYNAMIC_ENV_ALLOWLIST":                   "APP_ENV,NODE_ENV",
		"LOCALGATE_DYNAMIC_MAX_APPS":                        "9",
		"LOCALGATE_DYNAMIC_MAX_FILES":                       "1000",
		"LOCALGATE_DYNAMIC_MAX_TOTAL_BYTES":                 "9000",
		"LOCALGATE_DYNAMIC_MAX_FILE_BYTES":                  "8000",
		"LOCALGATE_DYNAMIC_STARTUP_TIMEOUT_SECONDS":         "11",
		"LOCALGATE_DYNAMIC_SHUTDOWN_TIMEOUT_SECONDS":        "12",
		"LOCALGATE_DYNAMIC_RESTART_POLICY":                  "never",
		"LOCALGATE_DYNAMIC_RESTART_MAX_ATTEMPTS":            "4",
		"LOCALGATE_DYNAMIC_RESTART_BACKOFF_SECONDS":         "5",
		"LOCALGATE_DYNAMIC_RESTART_RESET_SECONDS":           "70",
		"LOCALGATE_DYNAMIC_MAX_LOG_BYTES":                   "7000",
		"LOCALGATE_DYNAMIC_MAX_LOG_FILES":                   "4",
		"LOCALGATE_DYNAMIC_RELEASE_RETENTION":               "5",
		"LOCALGATE_DYNAMIC_MAX_REQUEST_BODY_BYTES":          "6000",
		"LOCALGATE_DYNAMIC_MAX_REQUEST_HEADER_BYTES":        "5500",
		"LOCALGATE_DYNAMIC_MAX_CONCURRENT_REQUESTS":         "6",
		"LOCALGATE_DYNAMIC_UPSTREAM_HEADER_TIMEOUT_SECONDS": "13",
		"LOCALGATE_DYNAMIC_MAX_RESPONSE_HEADER_BYTES":       "5000",
	}
	for key, value := range values {
		withEnv(t, key, value)
	}
	cfg, err := Load(LoadOptions{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	d := cfg.DynamicHosting
	if d.RequireApproval || d.MaxApps != 9 || d.MaxFiles != 1000 || d.MaxTotalBytes != 9000 || d.MaxFileBytes != 8000 {
		t.Fatalf("unexpected dynamic capacity env config: %#v", d)
	}
	if d.StartupTimeoutSeconds != 11 || d.ShutdownTimeoutSeconds != 12 || d.RestartPolicy != "never" || d.RestartMaxAttempts != 4 || d.RestartBackoffSeconds != 5 || d.RestartResetSeconds != 70 {
		t.Fatalf("unexpected dynamic lifecycle env config: %#v", d)
	}
	if d.MaxLogBytes != 7000 || d.MaxLogFiles != 4 || d.ReleaseRetention != 5 || d.MaxRequestBodyBytes != 6000 || d.MaxRequestHeaderBytes != 5500 || d.MaxConcurrentRequests != 6 || d.UpstreamHeaderTimeoutSeconds != 13 || d.MaxResponseHeaderBytes != 5000 {
		t.Fatalf("unexpected dynamic production env config: %#v", d)
	}
	if strings.Join(d.ReservedNames, ",") != "one,two" || strings.Join(d.EnvAllowlist, ",") != "APP_ENV,NODE_ENV" {
		t.Fatalf("unexpected dynamic list env config: %#v", d)
	}
}

func TestExampleConfigLoadsWithDynamicPlaceholders(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve config test path")
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "localgate.example.yaml"))
	cfg, err := Load(LoadOptions{Path: path, PathProvided: true})
	if err != nil {
		t.Fatalf("load example config: %v", err)
	}
	d := cfg.DynamicHosting
	if d.Enabled {
		t.Fatal("example dynamic hosting must remain disabled by default")
	}
	if len(d.ControlHosts) != 1 || d.ControlHosts[0] != "control.example.com" || d.AppDomain != "apps.example.com" {
		t.Fatalf("unexpected example routing placeholders: %#v", d)
	}
	if len(d.AllowedExecutables) != 2 || !strings.HasPrefix(d.AllowedExecutables[0], "/absolute/path/to/") {
		t.Fatalf("unexpected executable placeholders: %#v", d.AllowedExecutables)
	}
	if d.MaxRequestHeaderBytes != 1<<20 || d.MaxRequestBodyBytes != 32<<20 || d.ReleaseRetention != 3 || d.MaxLogFiles != 3 {
		t.Fatalf("unexpected example production limits: %#v", d)
	}
}
