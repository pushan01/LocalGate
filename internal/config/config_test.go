package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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

func loadWithTestRoot(t *testing.T, opts LoadOptions) (Config, error) {
	t.Helper()
	if opts.Path == "" {
		root := t.TempDir()
		path := filepath.Join(t.TempDir(), "localgate.yaml")
		if err := os.WriteFile(path, []byte("root: "+root+"\n"), 0o600); err != nil {
			t.Fatalf("write root config: %v", err)
		}
		opts.Path = path
		opts.PathProvided = true
	}
	return Load(opts)
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
root: /tmp
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

func TestLoadSkillsGlobalRootsNormalizesAndDeduplicates(t *testing.T) {
	root := t.TempDir()
	globalRoot := filepath.Join(t.TempDir(), "shared-skills")
	if err := os.MkdirAll(globalRoot, 0o755); err != nil {
		t.Fatalf("mkdir global skills root: %v", err)
	}
	cfgPath := filepath.Join(t.TempDir(), "localgate.yaml")
	content := fmt.Sprintf("root: %q\nskills:\n  global_roots:\n    - %q\n    - %q\n", root, globalRoot, globalRoot+string(filepath.Separator))
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !reflect.DeepEqual(cfg.Skills.GlobalRoots, []string{globalRoot}) {
		t.Fatalf("unexpected global skill roots: %#v", cfg.Skills.GlobalRoots)
	}

	badPath := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(badPath, []byte("root: /tmp\nskills:\n  global_roots: [relative/skills]\n"), 0o600); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}
	if _, err := Load(LoadOptions{Path: badPath, PathProvided: true}); err == nil || !strings.Contains(err.Error(), "skills.global_roots[0] must be absolute") {
		t.Fatalf("expected absolute global root validation error, got %v", err)
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
root: /tmp
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
root: /tmp
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
root: /tmp
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
root: /tmp
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
	withEnv(t, "LOCALGATE_RUNTIME_ADMIN_BIND", "127.0.0.1:8002")
	withEnv(t, "LOCALGATE_RUNTIME_PUBLIC_DEPLOYMENT_BIND", "127.0.0.1:8001")
	withEnv(t, "LOCALGATE_RUNTIME_PRIVATE_DEPLOYMENT_BIND", "127.0.0.1:8003")

	cfg, err := loadWithTestRoot(t, LoadOptions{})
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
	if cfg.Runtime.AdminBind != "127.0.0.1:8002" {
		t.Fatalf("unexpected runtime admin bind: %q", cfg.Runtime.AdminBind)
	}
	if cfg.Runtime.PublicDeploymentBind != "127.0.0.1:8001" {
		t.Fatalf("unexpected runtime public deployment bind: %q", cfg.Runtime.PublicDeploymentBind)
	}
	if cfg.Runtime.PrivateDeploymentBind != "127.0.0.1:8003" {
		t.Fatalf("unexpected runtime private deployment bind: %q", cfg.Runtime.PrivateDeploymentBind)
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
root: /tmp
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

	cfg, err := loadWithTestRoot(t, LoadOptions{})
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
root: /tmp
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
	cfg, err := loadWithTestRoot(t, LoadOptions{})
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
	if err := os.WriteFile(cfgPath, []byte("http:\n  allow_cors: true\nroot: /tmp\n"), 0o644); err != nil {
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
	if err := os.WriteFile(cfgPath, []byte("http:\n  allowed_origins: [\"*\"]\nroot: /tmp\n"), 0o644); err != nil {
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
root: /tmp
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
	cfg, err := loadWithTestRoot(t, LoadOptions{})
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
root: /tmp
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
	cfg, err := loadWithTestRoot(t, LoadOptions{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.ToolArguments.EnableBase64 {
		t.Fatalf("expected enable_base64 from env")
	}
}

func TestLoadFileBrowserConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	err := os.WriteFile(cfgPath, []byte(`
file_browser:
  enabled: true
  read_only: true
  max_file_bytes: 1234
  max_upload_bytes: 5678
  show_hidden_files: true
  exclude: [".git", "tmp"]
  require_approval_for_write: false
  require_approval_for_delete: false
root: /tmp
`), 0o644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.FileBrowser.Enabled || !cfg.FileBrowser.ReadOnly || cfg.FileBrowser.MaxFileBytes != 1234 || cfg.FileBrowser.MaxUploadBytes != 5678 || !cfg.FileBrowser.ShowHiddenFiles {
		t.Fatalf("unexpected file browser config: %#v", cfg.FileBrowser)
	}
	if len(cfg.FileBrowser.Exclude) != 2 || cfg.FileBrowser.Exclude[1] != "tmp" {
		t.Fatalf("unexpected file browser exclude: %#v", cfg.FileBrowser.Exclude)
	}
	if cfg.FileBrowser.RequireApprovalForWrite || cfg.FileBrowser.RequireApprovalForDelete {
		t.Fatalf("unexpected approval flags: %#v", cfg.FileBrowser)
	}
	summary := cfg.SafeSummary()
	fileBrowserSummary, _ := summary["file_browser"].(map[string]any)
	if fileBrowserSummary["max_file_bytes"] != int64(1234) || fileBrowserSummary["read_only"] != true {
		t.Fatalf("unexpected safe summary: %#v", summary)
	}
}

func TestLoadFileBrowserFromEnv(t *testing.T) {
	withEnv(t, "LOCALGATE_FILE_BROWSER_ENABLED", "false")
	withEnv(t, "LOCALGATE_FILE_BROWSER_READ_ONLY", "true")
	withEnv(t, "LOCALGATE_FILE_BROWSER_MAX_FILE_BYTES", "222")
	withEnv(t, "LOCALGATE_FILE_BROWSER_MAX_UPLOAD_BYTES", "333")
	withEnv(t, "LOCALGATE_FILE_BROWSER_SHOW_HIDDEN_FILES", "true")
	withEnv(t, "LOCALGATE_FILE_BROWSER_EXCLUDE", ".git,tmp")
	withEnv(t, "LOCALGATE_FILE_BROWSER_REQUIRE_APPROVAL_FOR_WRITE", "false")
	withEnv(t, "LOCALGATE_FILE_BROWSER_REQUIRE_APPROVAL_FOR_DELETE", "false")
	cfg, err := loadWithTestRoot(t, LoadOptions{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.FileBrowser.Enabled || !cfg.FileBrowser.ReadOnly || cfg.FileBrowser.MaxFileBytes != 222 || cfg.FileBrowser.MaxUploadBytes != 333 || !cfg.FileBrowser.ShowHiddenFiles {
		t.Fatalf("unexpected file browser config from env: %#v", cfg.FileBrowser)
	}
	if len(cfg.FileBrowser.Exclude) != 2 || cfg.FileBrowser.Exclude[1] != "tmp" {
		t.Fatalf("unexpected exclude from env: %#v", cfg.FileBrowser.Exclude)
	}
	if cfg.FileBrowser.RequireApprovalForWrite || cfg.FileBrowser.RequireApprovalForDelete {
		t.Fatalf("unexpected approval flags from env: %#v", cfg.FileBrowser)
	}
}

func TestLoadFileBrowserRejectsInvalidLimits(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	if err := os.WriteFile(cfgPath, []byte("file_browser:\n  enabled: true\n  max_file_bytes: 0\nroot: /tmp\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err == nil || !strings.Contains(err.Error(), "file_browser.max_file_bytes") {
		t.Fatalf("expected file browser max file validation error, got %v", err)
	}
}

func TestRejectLegacyWorkspacesConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	err := os.WriteFile(cfgPath, []byte(`
workspaces:
  - id: ws
    roots: ["/tmp"]
`), 0o644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err = Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err == nil || !strings.Contains(err.Error(), "workspaces was removed") {
		t.Fatalf("expected removed workspaces error, got %v", err)
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
root: /tmp
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
root: /tmp
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
root: /tmp
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
root: /tmp
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
root: /tmp
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
root: /tmp
`), 0o644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err = Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err == nil || !strings.Contains(err.Error(), "mcp_servers[0].cwd must be absolute") {
		t.Fatalf("expected relative cwd error, got: %v", err)
	}
}

func TestDefaultHiddenTools(t *testing.T) {
	want := []string{"run_argv", "search_files", "list_directory", "create_upload_token", "create_download_token"}
	if !reflect.DeepEqual(DefaultHiddenTools, want) {
		t.Fatalf("unexpected default hidden tools: %#v", DefaultHiddenTools)
	}
}
func toString(v any) string {
	return fmt.Sprintf("%v", v)
}

func TestDefaultStaticHostingConfig(t *testing.T) {
	cfg := Default()
	if cfg.Deployments.Static.MaxTotalBytes != 50<<20 || cfg.Deployments.Static.MaxFileBytes != 50<<20 {
		t.Fatalf("unexpected default static size limits: %#v", cfg.Deployments.Static)
	}
	if cfg.Deployments.Static.MaxFiles != 10_000 || !cfg.Deployments.Static.RequireApproval {
		t.Fatalf("unexpected default static config: %#v", cfg.Deployments.Static)
	}
}

func TestDefaultDynamicHostingConfig(t *testing.T) {
	cfg := Default().Deployments.Dynamic
	if cfg.Enabled || !cfg.RequireApproval || cfg.DefaultAccess != "private" {
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
	if cfg.Admin.Approval.PendingWaitSeconds != 5 {
		t.Fatalf("unexpected pending approval wait: %d", cfg.Admin.Approval.PendingWaitSeconds)
	}
	if cfg.Admin.Approval.MaxSyncWaitSeconds != 30 {
		t.Fatalf("unexpected approval sync wait: %d", cfg.Admin.Approval.MaxSyncWaitSeconds)
	}
}

func TestLoadApprovalConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	content := "admin:\n  base_url: https://admin.example.com/root///\n  approval:\n    enabled: false\n    default_ttl_seconds: 120\n    pending_wait_seconds: 3\n    max_sync_wait_seconds: 7\nroot: /tmp\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Admin.BaseURL != "https://admin.example.com/root" || cfg.Admin.Approval.Enabled || cfg.Admin.Approval.DefaultTTLSeconds != 120 || cfg.Admin.Approval.PendingWaitSeconds != 3 || cfg.Admin.Approval.MaxSyncWaitSeconds != 7 {
		t.Fatalf("unexpected approval config: %#v", cfg.Admin.Approval)
	}
	adminSummary, _ := cfg.SafeSummary()["admin"].(map[string]any)
	approvalSummary, _ := adminSummary["approval"].(map[string]any)
	if adminSummary["base_url"] != "https://admin.example.com/root" || approvalSummary["enabled"] != false || approvalSummary["default_ttl_seconds"] != 120 || approvalSummary["pending_wait_seconds"] != 3 || approvalSummary["max_sync_wait_seconds"] != 7 {
		t.Fatalf("unexpected admin approval summary: %#v", adminSummary)
	}
}

func TestLoadApprovalConfigFromEnv(t *testing.T) {
	withEnv(t, "LOCALGATE_ADMIN_BASE_URL", "https://admin.example.com/path/")
	withEnv(t, "LOCALGATE_APPROVAL_ENABLED", "false")
	withEnv(t, "LOCALGATE_APPROVAL_DEFAULT_TTL_SECONDS", "90")
	withEnv(t, "LOCALGATE_APPROVAL_PENDING_WAIT_SECONDS", "4")
	withEnv(t, "LOCALGATE_APPROVAL_MAX_SYNC_WAIT_SECONDS", "5")
	cfg, err := loadWithTestRoot(t, LoadOptions{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Admin.BaseURL != "https://admin.example.com/path" || cfg.Admin.Approval.Enabled || cfg.Admin.Approval.DefaultTTLSeconds != 90 || cfg.Admin.Approval.PendingWaitSeconds != 4 || cfg.Admin.Approval.MaxSyncWaitSeconds != 5 {
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
			content: "admin:\n  approval:\n    default_ttl_seconds: 0\nroot: /tmp\n",
			want:    "admin.approval.default_ttl_seconds",
		},
		{
			name:    "negative pending wait",
			content: "admin:\n  approval:\n    pending_wait_seconds: -1\nroot: /tmp\n",
			want:    "admin.approval.pending_wait_seconds",
		},
		{
			name:    "negative sync wait",
			content: "admin:\n  approval:\n    max_sync_wait_seconds: -1\nroot: /tmp\n",
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
	content := "admin:\n  base_url: https://admin.example.com\n  approval:\n    rules:\n      - id: delete-static\n        effect: require_approval\n        tools: [delete_deployment]\n        risk: critical\nroot: /tmp\n"
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
	content := "admin:\n  base_url: https://admin.example.com\n  approval:\n    rules:\n      - id: bad\n        effect: maybe\n        tools: [run_command]\nroot: /tmp\n"
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
	content := "deployments:\n  static:\n    domain_suffix: SITE\n    max_files: 123\n    max_total_bytes: 4096\n    max_file_bytes: 2048\n    require_approval: false\nroot: /tmp\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Deployments.Static.DomainSuffix != "site" || cfg.Deployments.Static.MaxFiles != 123 || cfg.Deployments.Static.MaxTotalBytes != 4096 || cfg.Deployments.Static.MaxFileBytes != 2048 || cfg.Deployments.Static.RequireApproval {
		t.Fatalf("unexpected static config: %#v", cfg.Deployments.Static)
	}
}

func TestLoadStaticHostingFromEnv(t *testing.T) {
	withEnv(t, "LOCALGATE_DEPLOYMENTS_STATIC_DOMAIN_SUFFIX", "-site")
	withEnv(t, "LOCALGATE_DEPLOYMENTS_STATIC_MAX_FILES", "77")
	withEnv(t, "LOCALGATE_DEPLOYMENTS_STATIC_MAX_TOTAL_BYTES", "1000")
	withEnv(t, "LOCALGATE_DEPLOYMENTS_STATIC_MAX_FILE_BYTES", "500")
	withEnv(t, "LOCALGATE_DEPLOYMENTS_STATIC_REQUIRE_APPROVAL", "false")
	cfg, err := loadWithTestRoot(t, LoadOptions{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Deployments.Static.DomainSuffix != "-site" || cfg.Deployments.Static.MaxFiles != 77 || cfg.Deployments.Static.MaxTotalBytes != 1000 || cfg.Deployments.Static.MaxFileBytes != 500 || cfg.Deployments.Static.RequireApproval {
		t.Fatalf("unexpected static env config: %#v", cfg.Deployments.Static)
	}
}

func TestLoadStaticHostingRejectsInvalidLimits(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	content := "deployments:\n  static:\n    max_files: 10\n    max_total_bytes: 100\n    max_file_bytes: 200\nroot: /tmp\n"
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
http:
  base_url: https://control.example.test
deployments:
  public_base_url: http://apps.example.test
  private_base_url: https://private.apps.example.test
  default_access: public
  dynamic:
    enabled: true
    domain_suffix: APP
    allowed_executables: [node]
    env_allowlist: [NODE_ENV]
    max_apps: 3
root: /tmp
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err != nil {
		t.Fatalf("load dynamic config: %v", err)
	}
	dynamic := cfg.Deployments.Dynamic
	if !dynamic.Enabled || dynamic.DomainSuffix != "app" || dynamic.PublicBaseURL != "http://apps.example.test" || dynamic.PrivateBaseURL != "https://private.apps.example.test" || dynamic.DefaultAccess != "public" || dynamic.ControlBaseURL != "https://control.example.test" || dynamic.MaxApps != 3 {
		t.Fatalf("unexpected dynamic config: %#v", dynamic)
	}
	deploymentsSummary := cfg.SafeSummary()["deployments"].(map[string]any)
	summary := deploymentsSummary["dynamic"].(map[string]any)
	if summary["enabled"] != true || summary["domain_suffix"] != "app" || deploymentsSummary["public_base_url"] != "http://apps.example.test" || deploymentsSummary["private_base_url"] != "https://private.apps.example.test" {
		t.Fatalf("unexpected dynamic summary: %#v", summary)
	}
}

func TestLoadDeploymentDomainSuffixFromEnv(t *testing.T) {
	withEnv(t, "LOCALGATE_DEPLOYMENTS_DYNAMIC_DOMAIN_SUFFIX", "APP")
	cfg, err := loadWithTestRoot(t, LoadOptions{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Deployments.Dynamic.DomainSuffix != "app" {
		t.Fatalf("unexpected dynamic domain suffix: %q", cfg.Deployments.Dynamic.DomainSuffix)
	}
}

func TestLoadDeploymentDomainSuffixRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"static dot", "deployments:\n  static:\n    domain_suffix: .site\nroot: /tmp\n", "deployments.static.domain_suffix"},
		{"dynamic trailing hyphen", "deployments:\n  dynamic:\n    domain_suffix: app-\nroot: /tmp\n", "deployments.dynamic.domain_suffix"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "localgate.yaml")
			if err := os.WriteFile(path, []byte(test.content), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(LoadOptions{Path: path, PathProvided: true})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q validation error, got %v", test.want, err)
			}
		})
	}
}

func TestLoadBaseURLRoutingWithoutDynamicRuntime(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "localgate.yaml")
	content := `
deployments:
  public_base_url: https://apps.example.test
  private_base_url: https://private.apps.example.test
  dynamic:
    enabled: false
root: /tmp
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(LoadOptions{Path: cfgPath, PathProvided: true})
	if err != nil {
		t.Fatalf("load base URL routing config: %v", err)
	}
	if cfg.Deployments.Dynamic.Enabled || cfg.Deployments.PublicBaseURL != "https://apps.example.test" || cfg.Deployments.PrivateBaseURL != "https://private.apps.example.test" {
		t.Fatalf("unexpected app routing config: %#v", cfg.Deployments.Dynamic)
	}
}

func TestLoadPublicPrivateBaseURLs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "localgate.yaml")
	content := `
deployments:
  public_base_url: https://public.example.test
  private_base_url: http://private.example.test
  default_access: private
  dynamic:
    enabled: false
root: /tmp
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(LoadOptions{Path: path, PathProvided: true})
	if err != nil {
		t.Fatal(err)
	}
	d := cfg.Deployments.Dynamic
	if d.PublicBaseURL != "https://public.example.test" || d.PrivateBaseURL != "http://private.example.test" || d.DefaultAccess != "private" {
		t.Fatalf("unexpected app hosting config: %#v", d)
	}
}

func TestLoadDynamicHostingRequiresRoutingAndExecutableConfig(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"missing base URLs", "deployments:\n  dynamic:\n    enabled: true\n    allowed_executables: [node]\nroot: /tmp\n", "base_url"},
		{"missing private base URL", "deployments:\n  public_base_url: https://apps.test\n  dynamic:\n    enabled: true\n    allowed_executables: [node]\nroot: /tmp\n", "must both be configured"},
		{"missing executables", "deployments:\n  public_base_url: https://apps.test\n  private_base_url: https://private.apps.test\n  dynamic:\n    enabled: true\nroot: /tmp\n", "allowed_executables"},
		{"base URL with path", "deployments:\n  public_base_url: https://apps.test/root\n  private_base_url: https://private.apps.test\nroot: /tmp\n", "must not contain"},
		{"base URLs share hostname", "deployments:\n  public_base_url: https://apps.test\n  private_base_url: http://apps.test\nroot: /tmp\n", "different hostnames"},
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
		"LOCALGATE_HTTP_BASE_URL":                                       "https://control.env.test",
		"LOCALGATE_DEPLOYMENTS_PUBLIC_BASE_URL":                         "https://apps.env.test",
		"LOCALGATE_DEPLOYMENTS_PRIVATE_BASE_URL":                        "http://private.apps.env.test",
		"LOCALGATE_DEPLOYMENTS_DEFAULT_ACCESS":                          "public",
		"LOCALGATE_DEPLOYMENTS_DYNAMIC_REQUIRE_APPROVAL":                "false",
		"LOCALGATE_DEPLOYMENTS_RESERVED_NAMES":                          "one,two",
		"LOCALGATE_DEPLOYMENTS_DYNAMIC_ENV_ALLOWLIST":                   "APP_ENV,NODE_ENV",
		"LOCALGATE_DEPLOYMENTS_DYNAMIC_MAX_APPS":                        "9",
		"LOCALGATE_DEPLOYMENTS_DYNAMIC_MAX_FILES":                       "1000",
		"LOCALGATE_DEPLOYMENTS_DYNAMIC_MAX_TOTAL_BYTES":                 "9000",
		"LOCALGATE_DEPLOYMENTS_DYNAMIC_MAX_FILE_BYTES":                  "8000",
		"LOCALGATE_DEPLOYMENTS_DYNAMIC_STARTUP_TIMEOUT_SECONDS":         "11",
		"LOCALGATE_DEPLOYMENTS_DYNAMIC_SHUTDOWN_TIMEOUT_SECONDS":        "12",
		"LOCALGATE_DEPLOYMENTS_DYNAMIC_RESTART_POLICY":                  "never",
		"LOCALGATE_DEPLOYMENTS_DYNAMIC_RESTART_MAX_ATTEMPTS":            "4",
		"LOCALGATE_DEPLOYMENTS_DYNAMIC_RESTART_BACKOFF_SECONDS":         "5",
		"LOCALGATE_DEPLOYMENTS_DYNAMIC_RESTART_RESET_SECONDS":           "70",
		"LOCALGATE_DEPLOYMENTS_DYNAMIC_MAX_LOG_BYTES":                   "7000",
		"LOCALGATE_DEPLOYMENTS_DYNAMIC_MAX_LOG_FILES":                   "4",
		"LOCALGATE_DEPLOYMENTS_DYNAMIC_RELEASE_RETENTION":               "5",
		"LOCALGATE_DEPLOYMENTS_DYNAMIC_MAX_REQUEST_BODY_BYTES":          "6000",
		"LOCALGATE_DEPLOYMENTS_DYNAMIC_MAX_REQUEST_HEADER_BYTES":        "5500",
		"LOCALGATE_DEPLOYMENTS_DYNAMIC_MAX_CONCURRENT_REQUESTS":         "6",
		"LOCALGATE_DEPLOYMENTS_DYNAMIC_UPSTREAM_HEADER_TIMEOUT_SECONDS": "13",
		"LOCALGATE_DEPLOYMENTS_DYNAMIC_MAX_RESPONSE_HEADER_BYTES":       "5000",
	}
	for key, value := range values {
		withEnv(t, key, value)
	}
	cfg, err := loadWithTestRoot(t, LoadOptions{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	d := cfg.Deployments.Dynamic
	if d.RequireApproval || d.MaxApps != 9 || d.MaxFiles != 1000 || d.MaxTotalBytes != 9000 || d.MaxFileBytes != 8000 {
		t.Fatalf("unexpected dynamic capacity env config: %#v", d)
	}
	if d.PublicBaseURL != "https://apps.env.test" || d.PrivateBaseURL != "http://private.apps.env.test" || d.DefaultAccess != "public" || d.ControlBaseURL != "https://control.env.test" {
		t.Fatalf("unexpected dynamic routing env config: %#v", d)
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
	d := cfg.Deployments.Dynamic
	if d.Enabled {
		t.Fatal("example dynamic hosting must remain disabled by default")
	}
	if cfg.Bind != "127.0.0.1:3737" || cfg.Admin.Bind != "127.0.0.1:3739" || cfg.Deployments.PublicBind != "127.0.0.1:3738" || cfg.Deployments.PrivateBind != "127.0.0.1:3740" {
		t.Fatalf("example listeners must be separated: control=%q admin=%q public=%q private=%q", cfg.Bind, cfg.Admin.Bind, cfg.Deployments.PublicBind, cfg.Deployments.PrivateBind)
	}
	if d.ControlBaseURL != cfg.HTTP.BaseURL || d.PublicBaseURL != "https://apps.example.com" || d.PrivateBaseURL != "https://private-apps.example.com" || d.DefaultAccess != "private" {
		t.Fatalf("unexpected example routing placeholders: %#v", d)
	}
	if len(d.AllowedExecutables) != 2 || !strings.HasPrefix(d.AllowedExecutables[0], "/absolute/path/to/") {
		t.Fatalf("unexpected executable placeholders: %#v", d.AllowedExecutables)
	}
	if d.MaxRequestHeaderBytes != 1<<20 || d.MaxRequestBodyBytes != 32<<20 || d.ReleaseRetention != 3 || d.MaxLogFiles != 3 {
		t.Fatalf("unexpected example production limits: %#v", d)
	}
}

func TestDeploymentBindOverridesAndUsesSeparatePorts(t *testing.T) {
	withEnv(t, "LOCALGATE_DEPLOYMENTS_PUBLIC_BIND", "127.0.0.1:4888")
	withEnv(t, "LOCALGATE_DEPLOYMENTS_PRIVATE_BIND", "127.0.0.1:4889")
	cfg, err := loadWithTestRoot(t, LoadOptions{PublicDeploymentsBind: "127.0.0.1:4998", PrivateDeploymentsBind: "127.0.0.1:4999"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Deployments.PublicBind != "127.0.0.1:4998" || cfg.Deployments.PrivateBind != "127.0.0.1:4999" {
		t.Fatalf("unexpected deployment bind overrides: public=%q private=%q", cfg.Deployments.PublicBind, cfg.Deployments.PrivateBind)
	}
	summary := cfg.SafeSummary()
	deployments := summary["deployments"].(map[string]any)
	if deployments["public_bind"] != "127.0.0.1:4998" || deployments["private_bind"] != "127.0.0.1:4999" {
		t.Fatalf("deployment binds missing from safe summary: %#v", summary)
	}
}

func TestAdminBindOverridesAndUsesSeparatePort(t *testing.T) {
	withEnv(t, "LOCALGATE_ADMIN_BIND", "127.0.0.1:4887")
	cfg, err := loadWithTestRoot(t, LoadOptions{AdminBind: "127.0.0.1:4998"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Admin.Bind != "127.0.0.1:4998" {
		t.Fatalf("unexpected admin bind override: %q", cfg.Admin.Bind)
	}
	summary := cfg.SafeSummary()
	if summary["admin"].(map[string]any)["bind"] != "127.0.0.1:4998" {
		t.Fatalf("admin bind missing from safe summary: %#v", summary)
	}
}

func TestAdminBindMustUseDifferentPortFromControl(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "localgate.yaml")
	content := "bind: 127.0.0.1:4800\nadmin:\n  enabled: true\n  bind: 0.0.0.0:4800\n  base_url: https://admin.test\nroot: /tmp\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(LoadOptions{Path: path, PathProvided: true})
	if err == nil || !strings.Contains(err.Error(), "admin.bind must use a different TCP port") {
		t.Fatalf("expected separate admin port validation error, got %v", err)
	}
}

func TestDeploymentBindsMustUseDifferentPortsFromAdmin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "localgate.yaml")
	content := "bind: 127.0.0.1:4800\nadmin:\n  enabled: true\n  bind: 127.0.0.1:4801\n  base_url: https://admin.test\ndeployments:\n  public_bind: 0.0.0.0:4801\n  private_bind: 0.0.0.0:4802\n  public_base_url: https://apps.test\n  private_base_url: https://private.apps.test\nroot: /tmp\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(LoadOptions{Path: path, PathProvided: true})
	if err == nil || !strings.Contains(err.Error(), "deployments.public_bind must use a different TCP port from admin.bind") {
		t.Fatalf("expected admin/deployment port validation error, got %v", err)
	}
}

func TestPublicAndPrivateDeploymentBindsMustDiffer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "localgate.yaml")
	content := `
bind: 127.0.0.1:4800
deployments:
  public_bind: 0.0.0.0:4802
  private_bind: 127.0.0.1:4802
  public_base_url: https://apps.test
  private_base_url: https://private.apps.test
  dynamic:
    enabled: false
root: /tmp
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(LoadOptions{Path: path, PathProvided: true})
	if err == nil || !strings.Contains(err.Error(), "public_bind and deployments.private_bind must use different TCP ports") {
		t.Fatalf("expected separate deployment port validation error, got %v", err)
	}
}

func TestDeploymentBindsRejectInvalidAddress(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "localgate.yaml")
	if err := os.WriteFile(path, []byte(`deployments:
  public_bind: not-an-address
  private_bind: 127.0.0.1:4803
root: /tmp
`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(LoadOptions{Path: path, PathProvided: true})
	if err == nil || !strings.Contains(err.Error(), "deployments.public_bind is invalid") {
		t.Fatalf("expected invalid deployment bind error, got %v", err)
	}
}

func TestRejectLegacyDeploymentConfigShape(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"top-level deployment bind", `deployment_bind: 127.0.0.1:3738
`, "move deployment settings under deployments"},
		{"top-level static hosting", `static_hosting:
  max_files: 10
`, "move deployment settings under deployments"},
		{"top-level dynamic hosting", `dynamic_hosting:
  enabled: false
`, "move deployment settings under deployments"},
		{"single deployment bind", `deployments:
  bind: 127.0.0.1:3738
`, "use deployments.public_bind and deployments.private_bind"},
		{"shared field nested under dynamic", `deployments:
  dynamic:
    public_base_url: https://apps.test
`, "move it to deployments.public_base_url"},
		{"removed deployments control hosts", `deployments:
  control_hosts: [control.test]
`, "derived from http.base_url"},
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

func TestLoadNotificationsConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localgate.yaml")
	content := `
notifications:
  enabled: true
  system_enabled: true
  tool_enabled: true
  bark:
    base_url: https://api.day.app/
    device_key: device-secret
    encryption_key: "1234567890123456"
    encryption_iv: "1111111111111111"
root: /tmp
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(LoadOptions{Path: path, PathProvided: true})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Notifications.Enabled || !cfg.Notifications.SystemEnabled || !cfg.Notifications.ToolEnabled {
		t.Fatalf("unexpected notification config: %#v", cfg.Notifications)
	}
	if cfg.Notifications.Bark.BaseURL != "https://api.day.app" {
		t.Fatalf("unexpected normalized base URL: %q", cfg.Notifications.Bark.BaseURL)
	}
	summary := cfg.SafeSummary()
	text := toString(summary["notifications"])
	if strings.Contains(text, "device-secret") || strings.Contains(text, "1234567890123456") || strings.Contains(text, "1111111111111111") {
		t.Fatalf("safe summary exposed Bark secrets: %#v", summary["notifications"])
	}
}

func TestLoadNotificationsRejectsInvalidEncryption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localgate.yaml")
	content := `
notifications:
  enabled: true
  bark:
    device_key: device-secret
    encryption_key: short
    encryption_iv: "1111111111111111"
root: /tmp
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(LoadOptions{Path: path, PathProvided: true}); err == nil || !strings.Contains(err.Error(), "encryption_key") {
		t.Fatalf("expected encryption key validation error, got %v", err)
	}
}

func TestLoadSubagentsConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localgate.yaml")
	content := `
subagents:
  enabled: true
  default_provider: fake
  max_agents: 7
  max_concurrency: 2
  max_pending_per_agent: 3
  same_agent_poll_interval_seconds: 13
  different_agent_poll_interval_seconds: 7
  providers:
    opencli_chatgpt:
      enabled: true
      executable: /opt/local/bin/opencli
      timeout_seconds: 45
      submit_interval_seconds: 3
      default_project: deadbeef
    opencli_gemini:
      enabled: true
      executable: /opt/local/bin/opencli-gemini
      browser_executable: /opt/local/bin/opencli-browser
      default_model: Flash
      timeout_seconds: 55
      submit_interval_seconds: 7
root: /tmp
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(LoadOptions{Path: path, PathProvided: true})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Subagents.Enabled || cfg.Subagents.DefaultProvider != "fake" || cfg.Subagents.MaxAgents != 7 || cfg.Subagents.MaxConcurrency != 2 || cfg.Subagents.MaxPendingPerAgent != 3 || cfg.Subagents.SameAgentPollIntervalSeconds != 13 || cfg.Subagents.DifferentAgentPollIntervalSeconds != 7 {
		t.Fatalf("unexpected subagent config: %#v", cfg.Subagents)
	}
	if !cfg.Subagents.Providers.OpenCLIChatGPT.Enabled || cfg.Subagents.Providers.OpenCLIChatGPT.Executable != "/opt/local/bin/opencli" || cfg.Subagents.Providers.OpenCLIChatGPT.TimeoutSeconds != 45 || cfg.Subagents.Providers.OpenCLIChatGPT.SubmitIntervalSeconds != 3 || cfg.Subagents.Providers.OpenCLIChatGPT.DefaultProject != "deadbeef" {
		t.Fatalf("unexpected opencli provider config: %#v", cfg.Subagents.Providers.OpenCLIChatGPT)
	}
	if !cfg.Subagents.Providers.OpenCLIGemini.Enabled || cfg.Subagents.Providers.OpenCLIGemini.Executable != "/opt/local/bin/opencli-gemini" || cfg.Subagents.Providers.OpenCLIGemini.BrowserExecutable != "/opt/local/bin/opencli-browser" || cfg.Subagents.Providers.OpenCLIGemini.DefaultModel != "Flash" || cfg.Subagents.Providers.OpenCLIGemini.TimeoutSeconds != 55 || cfg.Subagents.Providers.OpenCLIGemini.SubmitIntervalSeconds != 7 {
		t.Fatalf("unexpected Gemini provider config: %#v", cfg.Subagents.Providers.OpenCLIGemini)
	}
	summary := cfg.SafeSummary()["subagents"].(map[string]any)
	if summary["default_provider"] != "fake" || summary["max_agents"] != 7 || summary["max_concurrency"] != 2 || summary["max_pending_per_agent"] != 3 || summary["same_agent_poll_interval_seconds"] != 13 || summary["different_agent_poll_interval_seconds"] != 7 {
		t.Fatalf("unexpected subagent summary: %#v", summary)
	}
	providers := summary["providers"].(map[string]any)
	opencliSummary := providers["opencli_chatgpt"].(map[string]any)
	if opencliSummary["enabled"] != true || opencliSummary["executable"] != "/opt/local/bin/opencli" || opencliSummary["timeout_seconds"] != 45 || opencliSummary["submit_interval_seconds"] != 3 || opencliSummary["default_project"] != "deadbeef" {
		t.Fatalf("unexpected opencli provider summary: %#v", opencliSummary)
	}
	geminiSummary := providers["opencli_gemini"].(map[string]any)
	if geminiSummary["enabled"] != true || geminiSummary["executable"] != "/opt/local/bin/opencli-gemini" || geminiSummary["browser_executable"] != "/opt/local/bin/opencli-browser" || geminiSummary["default_model"] != "Flash" || geminiSummary["timeout_seconds"] != 55 || geminiSummary["submit_interval_seconds"] != 7 {
		t.Fatalf("unexpected Gemini provider summary: %#v", geminiSummary)
	}
}

func TestValidateSubagentsRejectsNegativeProviderSubmitInterval(t *testing.T) {
	cfg := Default().Subagents
	cfg.Providers.OpenCLIChatGPT.SubmitIntervalSeconds = -1
	if err := validateSubagentsConfig(cfg); err == nil || !strings.Contains(err.Error(), "opencli_chatgpt.submit_interval_seconds") {
		t.Fatalf("expected ChatGPT submit interval validation error, got %v", err)
	}
	cfg = Default().Subagents
	cfg.Providers.OpenCLIGemini.SubmitIntervalSeconds = -1
	if err := validateSubagentsConfig(cfg); err == nil || !strings.Contains(err.Error(), "opencli_gemini.submit_interval_seconds") {
		t.Fatalf("expected Gemini submit interval validation error, got %v", err)
	}
}

func TestLoadSubagentsAllowsBundledGeminiAdapter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localgate.yaml")
	content := `
subagents:
  enabled: true
  default_provider: opencli_gemini
  providers:
    opencli_gemini:
      enabled: true
      browser_executable: opencli
root: /tmp
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(LoadOptions{Path: path, PathProvided: true})
	if err != nil {
		t.Fatalf("load bundled Gemini config: %v", err)
	}
	if cfg.Subagents.Providers.OpenCLIGemini.Executable != "" || cfg.Subagents.Providers.OpenCLIGemini.BrowserExecutable != "opencli" || cfg.Subagents.Providers.OpenCLIGemini.DefaultModel != "Pro" {
		t.Fatalf("unexpected bundled Gemini config: %#v", cfg.Subagents.Providers.OpenCLIGemini)
	}
}

func TestLoadSubagentsRequiresDefaultProviderWhenEnabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localgate.yaml")
	content := `
subagents:
  enabled: true
root: /tmp
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(LoadOptions{Path: path, PathProvided: true}); err == nil || !strings.Contains(err.Error(), "subagents.default_provider") {
		t.Fatalf("expected default provider validation error, got %v", err)
	}
}
