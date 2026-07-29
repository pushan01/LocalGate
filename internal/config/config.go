package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Workspace struct {
	ID                     string         `yaml:"id"`
	Roots                  []string       `yaml:"roots"`
	PathDenylist           []string       `yaml:"path_denylist"`
	CommandAllowlist       []string       `yaml:"command_allowlist"`
	CommandDenylist        []string       `yaml:"command_denylist"`
	AllowShellPipe         bool           `yaml:"allow_shell_pipe"`
	AllowShellRedirect     bool           `yaml:"allow_shell_redirect"`
	AllowShellControl      bool           `yaml:"allow_shell_control"`
	AllowShellBackground   bool           `yaml:"allow_shell_background"`
	AllowShellSubstitution bool           `yaml:"allow_shell_substitution"`
	CWD                    string         `yaml:"cwd"`
	EnvAllowlist           []string       `yaml:"env_allowlist"`
	ApprovalPolicy         ApprovalPolicy `yaml:"approval_policy"`
}

type Config struct {
	Bind             string            `yaml:"bind"`
	MaxConcurrency   int               `yaml:"max_concurrency"`
	DefaultTimeoutMs int64             `yaml:"default_timeout_ms"`
	MaxOutputBytes   int64             `yaml:"max_output_bytes"`
	LogStore         string            `yaml:"log_store"`
	Admin            AdminConfig       `yaml:"admin"`
	SessionCleanup   SessionCleanup    `yaml:"session_cleanup"`
	HTTP             HTTP              `yaml:"http"`
	MCPHTTP          MCPHTTP           `yaml:"mcp_http"`
	FileExports      FileExports       `yaml:"file_exports"`
	FileBrowser      FileBrowserConfig `yaml:"file_browser"`
	Deployments      DeploymentsConfig `yaml:"deployments"`
	HTTPAuth         HTTPAuth          `yaml:"http_auth"`
	ToolArguments    ToolArguments     `yaml:"tool_arguments"`
	ToolHTTPMode     ToolHTTPMode      `yaml:"tool_http_mode"`
	MCPServers       []MCPServer       `yaml:"mcp_servers"`
	Upgrade          Upgrade           `yaml:"-"`
	Runtime          Runtime           `yaml:"-"`
	Workspaces       []Workspace       `yaml:"workspaces"`
}

type LoadOptions struct {
	Path                   string
	PathProvided           bool
	Bind                   string
	AdminBind              string
	PublicDeploymentsBind  string
	PrivateDeploymentsBind string
}

type ApprovalPolicy struct {
	Commands   []string `yaml:"commands"`
	WritePaths []string `yaml:"write_paths"`
}

type ApprovalConfig struct {
	Enabled            bool           `yaml:"enabled"`
	DefaultTTLSeconds  int            `yaml:"default_ttl_seconds"`
	MaxSyncWaitSeconds int            `yaml:"max_sync_wait_seconds"`
	Rules              []ApprovalRule `yaml:"rules"`
}

type AdminConfig struct {
	Enabled  bool           `yaml:"enabled"`
	Bind     string         `yaml:"bind"`
	BaseURL  string         `yaml:"base_url"`
	Approval ApprovalConfig `yaml:"approval"`
}

type ApprovalRule struct {
	ID     string   `yaml:"id"`
	Effect string   `yaml:"effect"`
	Tools  []string `yaml:"tools"`
	Risk   string   `yaml:"risk"`
}

type SessionCleanup struct {
	FinishedTTLSeconds int `yaml:"finished_ttl_seconds"`
	IdleTTLSeconds     int `yaml:"idle_ttl_seconds"`
	MaxSessions        int `yaml:"max_sessions"`
	SweepIntervalMs    int `yaml:"sweep_interval_ms"`
}

type HTTP struct {
	BaseURL        string   `yaml:"base_url"`
	AllowCORS      bool     `yaml:"allow_cors"`
	AllowedOrigins []string `yaml:"allowed_origins"`
}

type MCPHTTP struct {
	Stateless bool `yaml:"stateless"`
}

type FileExports struct {
	Enabled           bool   `yaml:"enabled"`
	BaseURL           string `yaml:"base_url"`
	DefaultTTLSeconds int    `yaml:"default_ttl_seconds"`
	MaxTTLSeconds     int    `yaml:"max_ttl_seconds"`
	MaxFileBytes      int64  `yaml:"max_file_bytes"`
	RequireApproval   bool   `yaml:"require_approval"`
}

type FileBrowserConfig struct {
	Enabled                  bool     `yaml:"enabled"`
	ReadOnly                 bool     `yaml:"read_only"`
	MaxFileBytes             int64    `yaml:"max_file_bytes"`
	MaxUploadBytes           int64    `yaml:"max_upload_bytes"`
	ShowHiddenFiles          bool     `yaml:"show_hidden_files"`
	Exclude                  []string `yaml:"exclude"`
	RequireApprovalForWrite  bool     `yaml:"require_approval_for_write"`
	RequireApprovalForDelete bool     `yaml:"require_approval_for_delete"`
}

type DeploymentsConfig struct {
	PublicBind     string         `yaml:"public_bind"`
	PrivateBind    string         `yaml:"private_bind"`
	PublicBaseURL  string         `yaml:"public_base_url"`
	PrivateBaseURL string         `yaml:"private_base_url"`
	DefaultAccess  string         `yaml:"default_access"`
	ReservedNames  []string       `yaml:"reserved_names"`
	Static         StaticHosting  `yaml:"static"`
	Dynamic        DynamicHosting `yaml:"dynamic"`
}

type StaticHosting struct {
	DomainSuffix    string `yaml:"domain_suffix"`
	MaxFiles        int    `yaml:"max_files"`
	MaxTotalBytes   int64  `yaml:"max_total_bytes"`
	MaxFileBytes    int64  `yaml:"max_file_bytes"`
	RequireApproval bool   `yaml:"require_approval"`
}

type DynamicHosting struct {
	Enabled                      bool     `yaml:"enabled"`
	DomainSuffix                 string   `yaml:"domain_suffix"`
	ControlBaseURL               string   `yaml:"-"`
	PublicBaseURL                string   `yaml:"-"`
	PrivateBaseURL               string   `yaml:"-"`
	DefaultAccess                string   `yaml:"-"`
	RequireApproval              bool     `yaml:"require_approval"`
	ReservedNames                []string `yaml:"-"`
	AllowedExecutables           []string `yaml:"allowed_executables"`
	EnvAllowlist                 []string `yaml:"env_allowlist"`
	MaxApps                      int      `yaml:"max_apps"`
	MaxFiles                     int      `yaml:"max_files"`
	MaxTotalBytes                int64    `yaml:"max_total_bytes"`
	MaxFileBytes                 int64    `yaml:"max_file_bytes"`
	StartupTimeoutSeconds        int      `yaml:"startup_timeout_seconds"`
	ShutdownTimeoutSeconds       int      `yaml:"shutdown_timeout_seconds"`
	RestartPolicy                string   `yaml:"restart_policy"`
	RestartMaxAttempts           int      `yaml:"restart_max_attempts"`
	RestartBackoffSeconds        int      `yaml:"restart_backoff_seconds"`
	RestartResetSeconds          int      `yaml:"restart_reset_seconds"`
	MaxLogBytes                  int64    `yaml:"max_log_bytes"`
	MaxLogFiles                  int      `yaml:"max_log_files"`
	ReleaseRetention             int      `yaml:"release_retention"`
	MaxRequestBodyBytes          int64    `yaml:"max_request_body_bytes"`
	MaxRequestHeaderBytes        int64    `yaml:"max_request_header_bytes"`
	MaxConcurrentRequests        int      `yaml:"max_concurrent_requests"`
	UpstreamHeaderTimeoutSeconds int      `yaml:"upstream_header_timeout_seconds"`
	MaxResponseHeaderBytes       int64    `yaml:"max_response_header_bytes"`
}

type HTTPAuth struct {
	Enabled     bool     `yaml:"enabled"`
	BearerToken string   `yaml:"bearer_token"`
	AllowPaths  []string `yaml:"allow_paths"`
}

type ToolArguments struct {
	EnableBase64 bool `yaml:"enable_base64"`
}

type ToolHTTPMode struct {
	Enabled               bool `yaml:"enabled"`
	AccessTokenTTLSeconds int  `yaml:"access_token_ttl_seconds"`
}

type Upgrade struct {
	Token string `yaml:"-"`
}

type Runtime struct {
	Mode                  string `yaml:"-"`
	Bin                   string `yaml:"-"`
	ConfigPath            string `yaml:"-"`
	Bind                  string `yaml:"-"`
	AdminBind             string `yaml:"-"`
	PublicDeploymentBind  string `yaml:"-"`
	PrivateDeploymentBind string `yaml:"-"`
	PIDFile               string `yaml:"-"`
	LogFile               string `yaml:"-"`
	RunDir                string `yaml:"-"`
}

type MCPServer struct {
	Name    string            `yaml:"name"`
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Env     map[string]string `yaml:"env"`
	CWD     string            `yaml:"cwd"`
}

var DefaultHiddenTools = []string{
	"run_argv",
	"search_files",
	"list_directory",
	"create_upload_token",
	"create_download_token",
}

func Default() Config {
	cfg := Config{
		Bind:             "127.0.0.1:3737",
		MaxConcurrency:   4,
		DefaultTimeoutMs: 10 * 60 * 1000,
		MaxOutputBytes:   10 * 1024 * 1024,
		Admin:            DefaultAdminConfig(),
		SessionCleanup: SessionCleanup{
			FinishedTTLSeconds: 30 * 60,
			IdleTTLSeconds:     30 * 60,
			MaxSessions:        100,
			SweepIntervalMs:    60 * 1000,
		},
		ToolHTTPMode: ToolHTTPMode{
			AccessTokenTTLSeconds: 10 * 60,
		},
		FileExports: FileExports{
			DefaultTTLSeconds: 5 * 60,
			MaxTTLSeconds:     10 * 60,
			MaxFileBytes:      100 << 20,
			RequireApproval:   true,
		},
		FileBrowser: FileBrowserConfig{
			Enabled:                  true,
			MaxFileBytes:             1 << 20,
			MaxUploadBytes:           100 << 20,
			Exclude:                  []string{".git", "node_modules", ".localgate"},
			RequireApprovalForWrite:  true,
			RequireApprovalForDelete: true,
		},
		Deployments: DeploymentsConfig{
			PublicBind:    "127.0.0.1:3738",
			PrivateBind:   "127.0.0.1:3740",
			DefaultAccess: "private",
			ReservedNames: []string{"macgate", "www", "api", "admin", "mcp", "tasks", "approvals", "static", "deploys"},
			Static: StaticHosting{
				MaxFiles:        10_000,
				MaxTotalBytes:   50 << 20,
				MaxFileBytes:    50 << 20,
				RequireApproval: true,
			},
			Dynamic: DynamicHosting{
				RequireApproval:              true,
				MaxApps:                      20,
				MaxFiles:                     20_000,
				MaxTotalBytes:                512 << 20,
				MaxFileBytes:                 100 << 20,
				StartupTimeoutSeconds:        20,
				ShutdownTimeoutSeconds:       5,
				RestartPolicy:                "on-failure",
				RestartMaxAttempts:           3,
				RestartBackoffSeconds:        2,
				RestartResetSeconds:          60,
				MaxLogBytes:                  10 << 20,
				MaxLogFiles:                  3,
				ReleaseRetention:             3,
				MaxRequestBodyBytes:          32 << 20,
				MaxRequestHeaderBytes:        1 << 20,
				MaxConcurrentRequests:        100,
				UpstreamHeaderTimeoutSeconds: 30,
				MaxResponseHeaderBytes:       1 << 20,
			},
		},
		HTTPAuth: HTTPAuth{
			AllowPaths: []string{"/healthz"},
		},
	}
	applyDeploymentShared(&cfg.Deployments, cfg.HTTP.BaseURL)
	return cfg
}

func DefaultApprovalConfig() ApprovalConfig {
	return ApprovalConfig{
		Enabled:            true,
		DefaultTTLSeconds:  10 * 60,
		MaxSyncWaitSeconds: 30,
	}
}

func DefaultAdminConfig() AdminConfig {
	return AdminConfig{
		Enabled:  true,
		Bind:     "127.0.0.1:3739",
		BaseURL:  "http://127.0.0.1:3739",
		Approval: DefaultApprovalConfig(),
	}
}

// NormalizeApprovalConfig applies defaults to the approval subfeature for
// programmatic AdminConfig literals. YAML configurations inherit defaults before
// unmarshalling.
func NormalizeApprovalConfig(cfg ApprovalConfig) ApprovalConfig {
	if !cfg.Enabled && cfg.DefaultTTLSeconds == 0 && cfg.MaxSyncWaitSeconds == 0 && len(cfg.Rules) == 0 {
		return DefaultApprovalConfig()
	}
	if cfg.DefaultTTLSeconds == 0 {
		cfg.DefaultTTLSeconds = DefaultApprovalConfig().DefaultTTLSeconds
	}
	return cfg
}

// NormalizeAdminConfig applies defaults for programmatic Config literals. YAML
// configurations already inherit these values before unmarshalling.
func NormalizeAdminConfig(cfg AdminConfig, httpConfig HTTP) AdminConfig {
	if !cfg.Enabled && strings.TrimSpace(cfg.BaseURL) == "" &&
		!cfg.Approval.Enabled && cfg.Approval.DefaultTTLSeconds == 0 &&
		cfg.Approval.MaxSyncWaitSeconds == 0 && len(cfg.Approval.Rules) == 0 {
		cfg = DefaultAdminConfig()
	}
	cfg.Approval = NormalizeApprovalConfig(cfg.Approval)
	if !cfg.Enabled {
		cfg.Approval.Enabled = false
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = strings.TrimSpace(httpConfig.BaseURL)
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = DefaultAdminConfig().BaseURL
	}
	if strings.TrimSpace(cfg.Bind) == "" {
		cfg.Bind = DefaultAdminConfig().Bind
	}
	return cfg
}

func Load(opts LoadOptions) (Config, error) {
	cfg := Default()

	path := opts.Path
	if path == "" {
		path = "./localgate.yaml"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if opts.PathProvided {
			return Config{}, fmt.Errorf("read config file: %w", err)
		}
		// Not provided explicitly; if file doesn't exist, continue with defaults
		if !errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("read default config file: %w", err)
		}
	} else {
		if err := rejectLegacyConfig(b); err != nil {
			return Config{}, err
		}
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config yaml: %w", err)
		}
	}

	// Env overrides (minimal set)
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_BIND")); v != "" {
		cfg.Bind = v
	}
	if strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_BIND")) != "" {
		return Config{}, errors.New("LOCALGATE_DEPLOYMENTS_BIND was removed; use LOCALGATE_DEPLOYMENTS_PUBLIC_BIND and LOCALGATE_DEPLOYMENTS_PRIVATE_BIND")
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_PUBLIC_BIND")); v != "" {
		cfg.Deployments.PublicBind = v
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_PRIVATE_BIND")); v != "" {
		cfg.Deployments.PrivateBind = v
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_MAX_CONCURRENCY")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.MaxConcurrency = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEFAULT_TIMEOUT_MS")); v != "" {
		if iv, err := atoi64(v); err == nil {
			cfg.DefaultTimeoutMs = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_MAX_OUTPUT_BYTES")); v != "" {
		if iv, err := atoi64(v); err == nil {
			cfg.MaxOutputBytes = iv
		}
	}
	if strings.TrimSpace(os.Getenv("LOCALGATE_APPROVAL_BASE_URL")) != "" {
		return Config{}, errors.New("LOCALGATE_APPROVAL_BASE_URL was removed; use LOCALGATE_ADMIN_BASE_URL")
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_APPROVAL_ENABLED")); v != "" {
		if bv, ok := atob(v); ok {
			cfg.Admin.Approval.Enabled = bv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_APPROVAL_DEFAULT_TTL_SECONDS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.Admin.Approval.DefaultTTLSeconds = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_APPROVAL_MAX_SYNC_WAIT_SECONDS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.Admin.Approval.MaxSyncWaitSeconds = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_ADMIN_ENABLED")); v != "" {
		if bv, ok := atob(v); ok {
			cfg.Admin.Enabled = bv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_ADMIN_BIND")); v != "" {
		cfg.Admin.Bind = v
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_ADMIN_BASE_URL")); v != "" {
		cfg.Admin.BaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_SESSION_FINISHED_TTL_SECONDS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.SessionCleanup.FinishedTTLSeconds = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_SESSION_IDLE_TTL_SECONDS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.SessionCleanup.IdleTTLSeconds = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_SESSION_MAX_SESSIONS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.SessionCleanup.MaxSessions = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_SESSION_SWEEP_INTERVAL_MS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.SessionCleanup.SweepIntervalMs = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_HTTP_AUTH_ENABLED")); v != "" {
		if bv, ok := atob(v); ok {
			cfg.HTTPAuth.Enabled = bv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_HTTP_BEARER_TOKEN")); v != "" {
		cfg.HTTPAuth.BearerToken = v
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_HTTP_BASE_URL")); v != "" {
		cfg.HTTP.BaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_HTTP_ALLOW_CORS")); v != "" {
		if bv, ok := atob(v); ok {
			cfg.HTTP.AllowCORS = bv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_HTTP_ALLOWED_ORIGINS")); v != "" {
		cfg.HTTP.AllowedOrigins = splitCommaSeparated(v)
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_MCP_HTTP_STATELESS")); v != "" {
		if bv, ok := atob(v); ok {
			cfg.MCPHTTP.Stateless = bv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_FILE_EXPORTS_ENABLED")); v != "" {
		if bv, ok := atob(v); ok {
			cfg.FileExports.Enabled = bv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_FILE_EXPORTS_BASE_URL")); v != "" {
		cfg.FileExports.BaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_FILE_EXPORTS_DEFAULT_TTL_SECONDS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.FileExports.DefaultTTLSeconds = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_FILE_EXPORTS_MAX_TTL_SECONDS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.FileExports.MaxTTLSeconds = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_FILE_EXPORTS_MAX_FILE_BYTES")); v != "" {
		if iv, err := atoi64(v); err == nil {
			cfg.FileExports.MaxFileBytes = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_FILE_EXPORTS_REQUIRE_APPROVAL")); v != "" {
		if bv, ok := atob(v); ok {
			cfg.FileExports.RequireApproval = bv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_FILE_BROWSER_ENABLED")); v != "" {
		if bv, ok := atob(v); ok {
			cfg.FileBrowser.Enabled = bv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_FILE_BROWSER_READ_ONLY")); v != "" {
		if bv, ok := atob(v); ok {
			cfg.FileBrowser.ReadOnly = bv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_FILE_BROWSER_MAX_FILE_BYTES")); v != "" {
		if iv, err := atoi64(v); err == nil {
			cfg.FileBrowser.MaxFileBytes = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_FILE_BROWSER_MAX_UPLOAD_BYTES")); v != "" {
		if iv, err := atoi64(v); err == nil {
			cfg.FileBrowser.MaxUploadBytes = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_FILE_BROWSER_SHOW_HIDDEN_FILES")); v != "" {
		if bv, ok := atob(v); ok {
			cfg.FileBrowser.ShowHiddenFiles = bv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_FILE_BROWSER_EXCLUDE")); v != "" {
		cfg.FileBrowser.Exclude = splitCommaSeparated(v)
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_FILE_BROWSER_REQUIRE_APPROVAL_FOR_WRITE")); v != "" {
		if bv, ok := atob(v); ok {
			cfg.FileBrowser.RequireApprovalForWrite = bv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_FILE_BROWSER_REQUIRE_APPROVAL_FOR_DELETE")); v != "" {
		if bv, ok := atob(v); ok {
			cfg.FileBrowser.RequireApprovalForDelete = bv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_STATIC_MAX_FILES")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.Deployments.Static.MaxFiles = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_STATIC_DOMAIN_SUFFIX")); v != "" {
		cfg.Deployments.Static.DomainSuffix = v
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_STATIC_MAX_TOTAL_BYTES")); v != "" {
		if iv, err := atoi64(v); err == nil {
			cfg.Deployments.Static.MaxTotalBytes = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_STATIC_MAX_FILE_BYTES")); v != "" {
		if iv, err := atoi64(v); err == nil {
			cfg.Deployments.Static.MaxFileBytes = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_STATIC_REQUIRE_APPROVAL")); v != "" {
		if bv, ok := atob(v); ok {
			cfg.Deployments.Static.RequireApproval = bv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_ENABLED")); v != "" {
		if bv, ok := atob(v); ok {
			cfg.Deployments.Dynamic.Enabled = bv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_DOMAIN_SUFFIX")); v != "" {
		cfg.Deployments.Dynamic.DomainSuffix = v
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_PUBLIC_BASE_URL")); v != "" {
		cfg.Deployments.PublicBaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_PRIVATE_BASE_URL")); v != "" {
		cfg.Deployments.PrivateBaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DEFAULT_ACCESS")); v != "" {
		cfg.Deployments.DefaultAccess = v
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_ALLOWED_EXECUTABLES")); v != "" {
		cfg.Deployments.Dynamic.AllowedExecutables = splitCommaSeparated(v)
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_REQUIRE_APPROVAL")); v != "" {
		if bv, ok := atob(v); ok {
			cfg.Deployments.Dynamic.RequireApproval = bv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_RESERVED_NAMES")); v != "" {
		cfg.Deployments.ReservedNames = splitCommaSeparated(v)
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_ENV_ALLOWLIST")); v != "" {
		cfg.Deployments.Dynamic.EnvAllowlist = splitCommaSeparated(v)
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_MAX_APPS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.Deployments.Dynamic.MaxApps = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_MAX_FILES")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.Deployments.Dynamic.MaxFiles = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_MAX_TOTAL_BYTES")); v != "" {
		if iv, err := atoi64(v); err == nil {
			cfg.Deployments.Dynamic.MaxTotalBytes = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_MAX_FILE_BYTES")); v != "" {
		if iv, err := atoi64(v); err == nil {
			cfg.Deployments.Dynamic.MaxFileBytes = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_STARTUP_TIMEOUT_SECONDS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.Deployments.Dynamic.StartupTimeoutSeconds = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_SHUTDOWN_TIMEOUT_SECONDS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.Deployments.Dynamic.ShutdownTimeoutSeconds = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_RESTART_POLICY")); v != "" {
		cfg.Deployments.Dynamic.RestartPolicy = v
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_RESTART_MAX_ATTEMPTS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.Deployments.Dynamic.RestartMaxAttempts = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_RESTART_BACKOFF_SECONDS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.Deployments.Dynamic.RestartBackoffSeconds = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_RESTART_RESET_SECONDS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.Deployments.Dynamic.RestartResetSeconds = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_MAX_LOG_BYTES")); v != "" {
		if iv, err := atoi64(v); err == nil {
			cfg.Deployments.Dynamic.MaxLogBytes = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_MAX_LOG_FILES")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.Deployments.Dynamic.MaxLogFiles = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_RELEASE_RETENTION")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.Deployments.Dynamic.ReleaseRetention = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_MAX_REQUEST_BODY_BYTES")); v != "" {
		if iv, err := atoi64(v); err == nil {
			cfg.Deployments.Dynamic.MaxRequestBodyBytes = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_MAX_REQUEST_HEADER_BYTES")); v != "" {
		if iv, err := atoi64(v); err == nil {
			cfg.Deployments.Dynamic.MaxRequestHeaderBytes = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_MAX_CONCURRENT_REQUESTS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.Deployments.Dynamic.MaxConcurrentRequests = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_UPSTREAM_HEADER_TIMEOUT_SECONDS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.Deployments.Dynamic.UpstreamHeaderTimeoutSeconds = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DEPLOYMENTS_DYNAMIC_MAX_RESPONSE_HEADER_BYTES")); v != "" {
		if iv, err := atoi64(v); err == nil {
			cfg.Deployments.Dynamic.MaxResponseHeaderBytes = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_TOOL_ARGUMENTS_ENABLE_BASE64")); v != "" {
		if bv, ok := atob(v); ok {
			cfg.ToolArguments.EnableBase64 = bv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_UPGRADE_TOKEN")); v != "" {
		cfg.Upgrade.Token = v
	}
	cfg.Runtime = Runtime{
		Mode:                  strings.TrimSpace(os.Getenv("LOCALGATE_RUNTIME_MODE")),
		Bin:                   strings.TrimSpace(os.Getenv("LOCALGATE_RUNTIME_BIN")),
		ConfigPath:            strings.TrimSpace(os.Getenv("LOCALGATE_RUNTIME_CONFIG")),
		Bind:                  strings.TrimSpace(os.Getenv("LOCALGATE_RUNTIME_BIND")),
		AdminBind:             strings.TrimSpace(os.Getenv("LOCALGATE_RUNTIME_ADMIN_BIND")),
		PublicDeploymentBind:  strings.TrimSpace(os.Getenv("LOCALGATE_RUNTIME_PUBLIC_DEPLOYMENT_BIND")),
		PrivateDeploymentBind: strings.TrimSpace(os.Getenv("LOCALGATE_RUNTIME_PRIVATE_DEPLOYMENT_BIND")),
		PIDFile:               strings.TrimSpace(os.Getenv("LOCALGATE_RUNTIME_PID_FILE")),
		LogFile:               strings.TrimSpace(os.Getenv("LOCALGATE_RUNTIME_LOG_FILE")),
		RunDir:                strings.TrimSpace(os.Getenv("LOCALGATE_RUNTIME_RUN_DIR")),
	}

	// Flag overrides
	if opts.Bind != "" {
		cfg.Bind = opts.Bind
	}
	if opts.AdminBind != "" {
		cfg.Admin.Bind = opts.AdminBind
	}
	if opts.PublicDeploymentsBind != "" {
		cfg.Deployments.PublicBind = opts.PublicDeploymentsBind
	}
	if opts.PrivateDeploymentsBind != "" {
		cfg.Deployments.PrivateBind = opts.PrivateDeploymentsBind
	}

	// Normalize paths
	for i := range cfg.Workspaces {
		ws := &cfg.Workspaces[i]
		for j := range ws.Roots {
			ws.Roots[j] = absPath(ws.Roots[j])
		}
		for j := range ws.PathDenylist {
			ws.PathDenylist[j] = absPath(ws.PathDenylist[j])
		}
		if ws.CWD != "" {
			ws.CWD = absPath(ws.CWD)
		}
	}
	for i := range cfg.MCPServers {
		srv := &cfg.MCPServers[i]
		if srv.CWD != "" {
			if !filepath.IsAbs(srv.CWD) {
				return Config{}, fmt.Errorf("mcp_servers[%d].cwd must be absolute", i)
			}
			srv.CWD = filepath.Clean(srv.CWD)
		}
	}
	if len(cfg.HTTPAuth.AllowPaths) == 0 {
		cfg.HTTPAuth.AllowPaths = []string{"/healthz"}
	}
	cfg.HTTP.BaseURL = normalizeBaseURL(cfg.HTTP.BaseURL)
	cfg.Admin.BaseURL = normalizeBaseURL(cfg.Admin.BaseURL)
	cfg.FileExports = NormalizeFileExportsConfig(cfg.FileExports, cfg.HTTP.BaseURL)
	if !cfg.Admin.Enabled {
		cfg.Admin.Approval.Enabled = false
	}
	cfg.Deployments.PublicBaseURL = normalizeBaseURL(cfg.Deployments.PublicBaseURL)
	cfg.Deployments.PrivateBaseURL = normalizeBaseURL(cfg.Deployments.PrivateBaseURL)
	cfg.Deployments.Static.DomainSuffix = normalizeDeploymentDomainSuffix(cfg.Deployments.Static.DomainSuffix)
	cfg.Deployments.Dynamic.DomainSuffix = normalizeDeploymentDomainSuffix(cfg.Deployments.Dynamic.DomainSuffix)
	cfg.Deployments.DefaultAccess = strings.ToLower(strings.TrimSpace(cfg.Deployments.DefaultAccess))
	if cfg.Deployments.DefaultAccess == "" {
		cfg.Deployments.DefaultAccess = "private"
	}
	applyDeploymentShared(&cfg.Deployments, cfg.HTTP.BaseURL)
	allowedOrigins, err := normalizeAllowedOrigins(cfg.HTTP.AllowedOrigins)
	if err != nil {
		return Config{}, err
	}
	cfg.HTTP.AllowedOrigins = allowedOrigins
	if cfg.HTTP.AllowCORS && len(cfg.HTTP.AllowedOrigins) == 0 {
		return Config{}, errors.New("http.allowed_origins must contain at least one origin when http.allow_cors is true")
	}
	if cfg.HTTPAuth.Enabled && strings.TrimSpace(cfg.HTTPAuth.BearerToken) == "" {
		return Config{}, errors.New("http_auth.bearer_token is required when http_auth.enabled is true")
	}
	if err := validateAdminConfig(cfg.Admin); err != nil {
		return Config{}, err
	}
	if err := validateFileExportsConfig(cfg.FileExports, cfg.HTTP.BaseURL); err != nil {
		return Config{}, err
	}
	if err := validateFileBrowserConfig(cfg.FileBrowser); err != nil {
		return Config{}, err
	}
	if err := validateListenerBinds(cfg.Bind, cfg.Admin.Bind, cfg.Deployments.PublicBind, cfg.Deployments.PrivateBind, cfg.Admin.Enabled); err != nil {
		return Config{}, err
	}
	if err := validateStaticHosting(cfg.Deployments.Static); err != nil {
		return Config{}, err
	}
	if err := validateDynamicHosting(cfg.Deployments.Dynamic); err != nil {
		return Config{}, err
	}
	if err := validateToolHTTPMode(cfg); err != nil {
		return Config{}, err
	}
	if err := validateMCPServers(cfg.MCPServers); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func rejectLegacyConfig(data []byte) error {
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil
	}
	if _, exists := root["approval"]; exists {
		return errors.New("top-level approval config was removed; move it to admin.approval and move base_url to admin.base_url")
	}
	for _, legacy := range []string{"deployment_bind", "static_hosting", "dynamic_hosting"} {
		if _, exists := root[legacy]; exists {
			return fmt.Errorf("top-level %s was removed; move deployment settings under deployments", legacy)
		}
	}
	deployments, _ := root["deployments"].(map[string]any)
	if _, exists := deployments["bind"]; exists {
		return errors.New("deployments.bind was removed; use deployments.public_bind and deployments.private_bind")
	}
	if _, exists := deployments["control_hosts"]; exists {
		return errors.New("deployments.control_hosts was removed; control host is derived from http.base_url")
	}
	dynamic, _ := deployments["dynamic"].(map[string]any)
	for _, shared := range []string{"bind", "public_bind", "private_bind", "public_base_url", "private_base_url", "default_access", "reserved_names"} {
		if _, exists := dynamic[shared]; exists {
			return fmt.Errorf("deployments.dynamic.%s is invalid; move it to deployments.%s", shared, shared)
		}
	}
	if _, exists := dynamic["control_hosts"]; exists {
		return errors.New("deployments.dynamic.control_hosts was removed; control host is derived from http.base_url")
	}
	workspaces, _ := root["workspaces"].([]any)
	for i, raw := range workspaces {
		workspace, _ := raw.(map[string]any)
		if _, exists := workspace["hidden_tools"]; exists {
			return fmt.Errorf("workspaces[%d].hidden_tools was removed; LocalGate now uses a fixed built-in hidden tool list", i)
		}
	}
	admin, _ := root["admin"].(map[string]any)
	approval, _ := admin["approval"].(map[string]any)
	if _, exists := approval["base_url"]; exists {
		return errors.New("admin.approval.base_url was removed; use admin.base_url")
	}
	return nil
}

func (c Config) SafeSummary() map[string]any {
	type ws struct {
		ID    string   `yaml:"id"`
		Roots []string `yaml:"roots"`
		CWD   string   `yaml:"cwd"`
	}
	wss := make([]ws, 0, len(c.Workspaces))
	for _, w := range c.Workspaces {
		wss = append(wss, ws{ID: w.ID, Roots: w.Roots, CWD: w.CWD})
	}
	mcpServers := make([]map[string]any, 0, len(c.MCPServers))
	for _, s := range c.MCPServers {
		mcpServers = append(mcpServers, map[string]any{
			"name":    s.Name,
			"command": s.Command,
			"args":    append([]string(nil), s.Args...),
			"cwd":     s.CWD,
		})
	}
	return map[string]any{
		"bind":               c.Bind,
		"max_concurrency":    c.MaxConcurrency,
		"default_timeout_ms": c.DefaultTimeoutMs,
		"max_output_bytes":   c.MaxOutputBytes,
		"admin": map[string]any{
			"enabled":  c.Admin.Enabled,
			"bind":     c.Admin.Bind,
			"base_url": c.Admin.BaseURL,
			"approval": map[string]any{
				"enabled":               c.Admin.Approval.Enabled,
				"default_ttl_seconds":   c.Admin.Approval.DefaultTTLSeconds,
				"max_sync_wait_seconds": c.Admin.Approval.MaxSyncWaitSeconds,
				"rules":                 c.Admin.Approval.Rules,
			},
		},
		"session_cleanup": map[string]any{
			"finished_ttl_seconds": c.SessionCleanup.FinishedTTLSeconds,
			"idle_ttl_seconds":     c.SessionCleanup.IdleTTLSeconds,
			"max_sessions":         c.SessionCleanup.MaxSessions,
			"sweep_interval_ms":    c.SessionCleanup.SweepIntervalMs,
		},
		"http_auth": map[string]any{
			"enabled":     c.HTTPAuth.Enabled,
			"allow_paths": c.HTTPAuth.AllowPaths,
		},
		"http": map[string]any{
			"base_url":        c.HTTP.BaseURL,
			"allow_cors":      c.HTTP.AllowCORS,
			"allowed_origins": c.HTTP.AllowedOrigins,
		},
		"mcp_http": map[string]any{
			"stateless": c.MCPHTTP.Stateless,
		},
		"file_exports": map[string]any{
			"enabled":             c.FileExports.Enabled,
			"base_url":            c.FileExports.BaseURL,
			"default_ttl_seconds": c.FileExports.DefaultTTLSeconds,
			"max_ttl_seconds":     c.FileExports.MaxTTLSeconds,
			"max_file_bytes":      c.FileExports.MaxFileBytes,
			"require_approval":    c.FileExports.RequireApproval,
		},
		"file_browser": map[string]any{
			"enabled":                     c.FileBrowser.Enabled,
			"read_only":                   c.FileBrowser.ReadOnly,
			"max_file_bytes":              c.FileBrowser.MaxFileBytes,
			"max_upload_bytes":            c.FileBrowser.MaxUploadBytes,
			"show_hidden_files":           c.FileBrowser.ShowHiddenFiles,
			"exclude":                     c.FileBrowser.Exclude,
			"require_approval_for_write":  c.FileBrowser.RequireApprovalForWrite,
			"require_approval_for_delete": c.FileBrowser.RequireApprovalForDelete,
		},
		"deployments": map[string]any{
			"public_bind":      c.Deployments.PublicBind,
			"private_bind":     c.Deployments.PrivateBind,
			"public_base_url":  c.Deployments.PublicBaseURL,
			"private_base_url": c.Deployments.PrivateBaseURL,
			"default_access":   c.Deployments.DefaultAccess,
			"reserved_names":   c.Deployments.ReservedNames,
			"static": map[string]any{
				"domain_suffix":    c.Deployments.Static.DomainSuffix,
				"max_files":        c.Deployments.Static.MaxFiles,
				"max_total_bytes":  c.Deployments.Static.MaxTotalBytes,
				"max_file_bytes":   c.Deployments.Static.MaxFileBytes,
				"require_approval": c.Deployments.Static.RequireApproval,
			},
			"dynamic": map[string]any{
				"enabled":                         c.Deployments.Dynamic.Enabled,
				"domain_suffix":                   c.Deployments.Dynamic.DomainSuffix,
				"require_approval":                c.Deployments.Dynamic.RequireApproval,
				"allowed_executables":             c.Deployments.Dynamic.AllowedExecutables,
				"env_allowlist":                   c.Deployments.Dynamic.EnvAllowlist,
				"max_apps":                        c.Deployments.Dynamic.MaxApps,
				"max_files":                       c.Deployments.Dynamic.MaxFiles,
				"max_total_bytes":                 c.Deployments.Dynamic.MaxTotalBytes,
				"max_file_bytes":                  c.Deployments.Dynamic.MaxFileBytes,
				"startup_timeout_seconds":         c.Deployments.Dynamic.StartupTimeoutSeconds,
				"shutdown_timeout_seconds":        c.Deployments.Dynamic.ShutdownTimeoutSeconds,
				"restart_policy":                  c.Deployments.Dynamic.RestartPolicy,
				"restart_max_attempts":            c.Deployments.Dynamic.RestartMaxAttempts,
				"restart_backoff_seconds":         c.Deployments.Dynamic.RestartBackoffSeconds,
				"restart_reset_seconds":           c.Deployments.Dynamic.RestartResetSeconds,
				"max_log_bytes":                   c.Deployments.Dynamic.MaxLogBytes,
				"max_log_files":                   c.Deployments.Dynamic.MaxLogFiles,
				"release_retention":               c.Deployments.Dynamic.ReleaseRetention,
				"max_request_body_bytes":          c.Deployments.Dynamic.MaxRequestBodyBytes,
				"max_request_header_bytes":        c.Deployments.Dynamic.MaxRequestHeaderBytes,
				"max_concurrent_requests":         c.Deployments.Dynamic.MaxConcurrentRequests,
				"upstream_header_timeout_seconds": c.Deployments.Dynamic.UpstreamHeaderTimeoutSeconds,
				"max_response_header_bytes":       c.Deployments.Dynamic.MaxResponseHeaderBytes,
			},
		},
		"tool_arguments": map[string]any{
			"enable_base64": c.ToolArguments.EnableBase64,
		},
		"tool_http_mode": map[string]any{
			"enabled":                  c.ToolHTTPMode.Enabled,
			"access_token_ttl_seconds": c.ToolHTTPMode.AccessTokenTTLSeconds,
		},
		"upgrade": map[string]any{
			"enabled": c.Upgrade.Token != "",
		},
		"runtime": map[string]any{
			"mode":                    c.Runtime.Mode,
			"bind":                    c.Runtime.Bind,
			"admin_bind":              c.Runtime.AdminBind,
			"public_deployment_bind":  c.Runtime.PublicDeploymentBind,
			"private_deployment_bind": c.Runtime.PrivateDeploymentBind,
		},
		"mcp_servers":      mcpServers,
		"mcp_server_count": len(c.MCPServers),
		"workspace_count":  len(c.Workspaces),
		"workspaces":       wss,
	}
}

func validateApprovalConfig(cfg ApprovalConfig) error {
	if cfg.DefaultTTLSeconds <= 0 {
		return errors.New("admin.approval.default_ttl_seconds must be positive")
	}
	if cfg.MaxSyncWaitSeconds < 0 {
		return errors.New("admin.approval.max_sync_wait_seconds must not be negative")
	}
	seen := map[string]struct{}{}
	for i, rule := range cfg.Rules {
		if strings.TrimSpace(rule.ID) == "" {
			return fmt.Errorf("admin.approval.rules[%d].id is required", i)
		}
		if _, exists := seen[rule.ID]; exists {
			return fmt.Errorf("duplicate approval rule id: %s", rule.ID)
		}
		seen[rule.ID] = struct{}{}
		switch rule.Effect {
		case "allow", "deny", "require_approval":
		default:
			return fmt.Errorf("admin.approval.rules[%d].effect must be allow, deny, or require_approval", i)
		}
		if len(rule.Tools) == 0 {
			return fmt.Errorf("admin.approval.rules[%d].tools must not be empty", i)
		}
	}
	return nil
}

func validateAdminConfig(cfg AdminConfig) error {
	if !cfg.Enabled {
		return nil
	}
	parsed, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("admin.base_url must be a valid http or https URL when admin is enabled")
	}
	return validateApprovalConfig(cfg.Approval)
}

func NormalizeFileExportsConfig(cfg FileExports, controlBaseURL string) FileExports {
	defaults := Default().FileExports
	if cfg.DefaultTTLSeconds == 0 {
		cfg.DefaultTTLSeconds = defaults.DefaultTTLSeconds
	}
	if cfg.MaxTTLSeconds == 0 {
		cfg.MaxTTLSeconds = defaults.MaxTTLSeconds
	}
	if cfg.MaxFileBytes == 0 {
		cfg.MaxFileBytes = defaults.MaxFileBytes
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = strings.TrimSpace(controlBaseURL)
	}
	cfg.BaseURL = normalizeBaseURL(cfg.BaseURL)
	return cfg
}

func validateFileExportsConfig(cfg FileExports, controlBaseURL string) error {
	if !cfg.Enabled {
		return nil
	}
	parsed, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("file_exports.base_url must be a valid http or https URL when file exports are enabled")
	}
	control, err := url.Parse(strings.TrimSpace(controlBaseURL))
	if err != nil || control.Host == "" {
		return errors.New("http.base_url must be valid when file exports are enabled")
	}
	if !strings.EqualFold(parsed.Scheme, control.Scheme) || !strings.EqualFold(parsed.Host, control.Host) {
		return errors.New("file_exports.base_url must use the same scheme and authority as http.base_url in this version")
	}
	if cfg.DefaultTTLSeconds < 60 {
		return errors.New("file_exports.default_ttl_seconds must be at least 60")
	}
	if cfg.MaxTTLSeconds < cfg.DefaultTTLSeconds {
		return errors.New("file_exports.max_ttl_seconds must be at least default_ttl_seconds")
	}
	if cfg.MaxFileBytes <= 0 {
		return errors.New("file_exports.max_file_bytes must be positive")
	}
	return nil
}

func validateFileBrowserConfig(cfg FileBrowserConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if cfg.MaxFileBytes <= 0 {
		return errors.New("file_browser.max_file_bytes must be positive when file browser is enabled")
	}
	if cfg.MaxUploadBytes <= 0 {
		return errors.New("file_browser.max_upload_bytes must be positive when file browser is enabled")
	}
	return nil
}

func NormalizeDeploymentsConfig(cfg DeploymentsConfig, controlBaseURL string) DeploymentsConfig {
	applyDeploymentShared(&cfg, controlBaseURL)
	return cfg
}

func applyDeploymentShared(cfg *DeploymentsConfig, controlBaseURL string) {
	if cfg == nil {
		return
	}
	cfg.Dynamic.ControlBaseURL = strings.TrimSpace(controlBaseURL)
	cfg.Dynamic.PublicBaseURL = cfg.PublicBaseURL
	cfg.Dynamic.PrivateBaseURL = cfg.PrivateBaseURL
	cfg.Dynamic.DefaultAccess = cfg.DefaultAccess
	cfg.Dynamic.ReservedNames = append([]string(nil), cfg.ReservedNames...)
}

func validateListenerBinds(controlBind, adminBind, publicDeploymentBind, privateDeploymentBind string, adminEnabled bool) error {
	controlPort, err := bindPort(controlBind)
	if err != nil {
		return fmt.Errorf("bind is invalid: %w", err)
	}
	publicPort, err := bindPort(publicDeploymentBind)
	if err != nil {
		return fmt.Errorf("deployments.public_bind is invalid: %w", err)
	}
	privatePort, err := bindPort(privateDeploymentBind)
	if err != nil {
		return fmt.Errorf("deployments.private_bind is invalid: %w", err)
	}
	if publicPort == privatePort {
		return errors.New("deployments.public_bind and deployments.private_bind must use different TCP ports")
	}
	for field, port := range map[string]string{
		"deployments.public_bind":  publicPort,
		"deployments.private_bind": privatePort,
	} {
		if controlPort == port {
			return fmt.Errorf("%s must use a different TCP port from bind", field)
		}
	}
	adminPort := ""
	if adminEnabled {
		adminPort, err = bindPort(adminBind)
		if err != nil {
			return fmt.Errorf("admin.bind is invalid: %w", err)
		}
		if controlPort == adminPort {
			return errors.New("admin.bind must use a different TCP port from bind")
		}
		for field, port := range map[string]string{
			"deployments.public_bind":  publicPort,
			"deployments.private_bind": privatePort,
		} {
			if adminPort == port {
				return fmt.Errorf("%s must use a different TCP port from admin.bind", field)
			}
		}
	}
	return nil
}

func bindPort(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("address is empty")
	}
	_, port, err := net.SplitHostPort(raw)
	if err != nil {
		return "", err
	}
	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value > 65535 {
		return "", errors.New("port must be between 1 and 65535")
	}
	return port, nil
}

func normalizeDeploymentDomainSuffix(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validateDeploymentDomainSuffix(field, value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 62 {
		return fmt.Errorf("%s must contain at most 62 characters", field)
	}
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' {
			continue
		}
		return fmt.Errorf("%s may contain only lowercase letters, numbers, and hyphens", field)
	}
	if value[len(value)-1] == '-' {
		return fmt.Errorf("%s must not end with a hyphen", field)
	}
	return nil
}

func validateStaticHosting(cfg StaticHosting) error {
	if err := validateDeploymentDomainSuffix("deployments.static.domain_suffix", cfg.DomainSuffix); err != nil {
		return err
	}
	if cfg.MaxFiles <= 0 {
		return errors.New("deployments.static.max_files must be positive")
	}
	if cfg.MaxTotalBytes <= 0 {
		return errors.New("deployments.static.max_total_bytes must be positive")
	}
	if cfg.MaxFileBytes <= 0 {
		return errors.New("deployments.static.max_file_bytes must be positive")
	}
	if cfg.MaxFileBytes > cfg.MaxTotalBytes {
		return errors.New("deployments.static.max_file_bytes must not exceed max_total_bytes")
	}
	return nil
}

func validateDynamicHosting(cfg DynamicHosting) error {
	if err := validateDeploymentDomainSuffix("deployments.dynamic.domain_suffix", cfg.DomainSuffix); err != nil {
		return err
	}
	if cfg.MaxApps <= 0 {
		return errors.New("deployments.dynamic.max_apps must be positive")
	}
	if cfg.MaxFiles <= 0 || cfg.MaxTotalBytes <= 0 || cfg.MaxFileBytes <= 0 {
		return errors.New("deployments.dynamic file limits must be positive")
	}
	if cfg.MaxFileBytes > cfg.MaxTotalBytes {
		return errors.New("deployments.dynamic.max_file_bytes must not exceed max_total_bytes")
	}
	if cfg.StartupTimeoutSeconds <= 0 || cfg.ShutdownTimeoutSeconds <= 0 {
		return errors.New("deployments.dynamic startup and shutdown timeouts must be positive")
	}
	if cfg.RestartMaxAttempts < 0 || cfg.RestartBackoffSeconds < 0 || cfg.RestartResetSeconds <= 0 {
		return errors.New("deployments.dynamic restart limits are invalid")
	}
	if cfg.MaxLogBytes <= 0 || cfg.MaxLogFiles <= 0 || cfg.ReleaseRetention <= 0 {
		return errors.New("deployments.dynamic log and release limits are invalid")
	}
	if cfg.MaxRequestBodyBytes <= 0 || cfg.MaxRequestHeaderBytes <= 0 || cfg.MaxConcurrentRequests <= 0 || cfg.UpstreamHeaderTimeoutSeconds <= 0 || cfg.MaxResponseHeaderBytes <= 0 {
		return errors.New("deployments.dynamic proxy limits are invalid")
	}
	switch cfg.RestartPolicy {
	case "never", "on-failure":
	default:
		return errors.New("deployments.dynamic.restart_policy must be never or on-failure")
	}
	if cfg.PublicBaseURL == "" && cfg.PrivateBaseURL == "" && !cfg.Enabled {
		return nil
	}
	if cfg.PublicBaseURL == "" || cfg.PrivateBaseURL == "" {
		return errors.New("deployments.public_base_url and deployments.private_base_url must both be configured")
	}
	publicURL, err := validateAppBaseURL("public_base_url", cfg.PublicBaseURL)
	if err != nil {
		return err
	}
	privateURL, err := validateAppBaseURL("private_base_url", cfg.PrivateBaseURL)
	if err != nil {
		return err
	}
	if strings.EqualFold(publicURL.Hostname(), privateURL.Hostname()) {
		return errors.New("deployments.public_base_url and private_base_url must use different hostnames")
	}
	if cfg.DefaultAccess != "public" && cfg.DefaultAccess != "private" {
		return errors.New("deployments.default_access must be public or private")
	}
	if cfg.Enabled && len(cfg.AllowedExecutables) == 0 {
		return errors.New("deployments.dynamic.allowed_executables must not be empty when enabled")
	}
	return nil
}

func validateAppBaseURL(field, raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("deployments.%s must be a valid http or https URL", field)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, fmt.Errorf("deployments.%s must not contain credentials, path, query, or fragment", field)
	}
	if !validHostname(normalizeHostname(parsed.Hostname())) {
		return nil, fmt.Errorf("deployments.%s hostname is invalid", field)
	}
	return parsed, nil
}

func normalizeHostname(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimSuffix(host, ".")
	return host
}

func validHostname(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func validateToolHTTPMode(cfg Config) error {
	if !cfg.ToolHTTPMode.Enabled {
		return nil
	}
	if cfg.ToolHTTPMode.AccessTokenTTLSeconds <= 0 {
		return errors.New("tool_http_mode.access_token_ttl_seconds must be positive when enabled")
	}
	if strings.TrimSpace(cfg.HTTP.BaseURL) == "" {
		return errors.New("http.base_url is required when tool_http_mode.enabled is true")
	}
	return nil
}

func validateMCPServers(servers []MCPServer) error {
	seen := make(map[string]struct{}, len(servers))
	for i, srv := range servers {
		name := strings.TrimSpace(srv.Name)
		if name == "" {
			return fmt.Errorf("mcp_servers[%d].name is required", i)
		}
		for _, ch := range name {
			if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-' {
				continue
			}
			return fmt.Errorf("mcp_servers[%d].name contains invalid character %q", i, ch)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("duplicate mcp server name: %s", name)
		}
		seen[name] = struct{}{}
		if strings.TrimSpace(srv.Command) == "" {
			return fmt.Errorf("mcp_servers[%d].command is required", i)
		}
	}
	return nil
}

func absPath(p string) string {
	if p == "" {
		return p
	}
	if strings.HasPrefix(p, "~"+string(os.PathSeparator)) {
		home, _ := os.UserHomeDir()
		if home != "" {
			p = filepath.Join(home, p[2:])
		}
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return filepath.Clean(abs)
}

func normalizeAllowedOrigins(origins []string) ([]string, error) {
	seen := make(map[string]struct{}, len(origins))
	out := make([]string, 0, len(origins))
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		if origin == "*" {
			return nil, errors.New("http.allowed_origins must not contain wildcard origin *")
		}
		origin = strings.TrimRight(origin, "/")
		if !strings.HasPrefix(origin, "http://") && !strings.HasPrefix(origin, "https://") {
			return nil, fmt.Errorf("invalid http.allowed_origins entry %q: expected an http(s) origin", origin)
		}
		if _, ok := seen[origin]; ok {
			continue
		}
		seen[origin] = struct{}{}
		out = append(out, origin)
	}
	return out, nil
}

func splitCommaSeparated(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func atoi(s string) (int, error) {
	var n int
	_, err := fmt.Sscan(s, &n)
	return n, err
}

func atoi64(s string) (int64, error) {
	var n int64
	_, err := fmt.Sscan(s, &n)
	return n, err
}

func atob(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

func normalizeBaseURL(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, "/")
	return s
}
