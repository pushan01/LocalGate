package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Workspace struct {
	ID                     string         `yaml:"id"`
	Roots                  []string       `yaml:"roots"`
	HiddenTools            []string       `yaml:"hidden_tools"`
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
	Bind             string         `yaml:"bind"`
	MaxConcurrency   int            `yaml:"max_concurrency"`
	DefaultTimeoutMs int64          `yaml:"default_timeout_ms"`
	MaxOutputBytes   int64          `yaml:"max_output_bytes"`
	LogStore         string         `yaml:"log_store"`
	Admin            AdminConfig    `yaml:"admin"`
	SessionCleanup   SessionCleanup `yaml:"session_cleanup"`
	HTTP             HTTP           `yaml:"http"`
	MCPHTTP          MCPHTTP        `yaml:"mcp_http"`
	StaticHosting    StaticHosting  `yaml:"static_hosting"`
	DynamicHosting   DynamicHosting `yaml:"dynamic_hosting"`
	HTTPAuth         HTTPAuth       `yaml:"http_auth"`
	ToolArguments    ToolArguments  `yaml:"tool_arguments"`
	ToolHTTPMode     ToolHTTPMode   `yaml:"tool_http_mode"`
	MCPServers       []MCPServer    `yaml:"mcp_servers"`
	Upgrade          Upgrade        `yaml:"-"`
	Runtime          Runtime        `yaml:"-"`
	Workspaces       []Workspace    `yaml:"workspaces"`
}

type LoadOptions struct {
	Path         string
	PathProvided bool
	Bind         string
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

type StaticHosting struct {
	MaxFiles        int   `yaml:"max_files"`
	MaxTotalBytes   int64 `yaml:"max_total_bytes"`
	MaxFileBytes    int64 `yaml:"max_file_bytes"`
	RequireApproval bool  `yaml:"require_approval"`
}

type DynamicHosting struct {
	Enabled                      bool     `yaml:"enabled"`
	ControlHosts                 []string `yaml:"control_hosts"`
	AppDomain                    string   `yaml:"app_domain"`
	PublicScheme                 string   `yaml:"public_scheme"`
	RequireApproval              bool     `yaml:"require_approval"`
	ReservedNames                []string `yaml:"reserved_names"`
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
	Mode       string `yaml:"-"`
	Bin        string `yaml:"-"`
	ConfigPath string `yaml:"-"`
	Bind       string `yaml:"-"`
	PIDFile    string `yaml:"-"`
	LogFile    string `yaml:"-"`
	RunDir     string `yaml:"-"`
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
	return Config{
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
		StaticHosting: StaticHosting{
			MaxFiles:        10_000,
			MaxTotalBytes:   50 << 20,
			MaxFileBytes:    50 << 20,
			RequireApproval: true,
		},
		DynamicHosting: DynamicHosting{
			PublicScheme:                 "https",
			RequireApproval:              true,
			ReservedNames:                []string{"macgate", "www", "api", "admin", "mcp", "tasks", "approvals", "static"},
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
		HTTPAuth: HTTPAuth{
			AllowPaths: []string{"/healthz"},
		},
	}
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
		BaseURL:  "http://127.0.0.1:3737",
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
		if err := rejectLegacyApprovalConfig(b); err != nil {
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
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_STATIC_MAX_FILES")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.StaticHosting.MaxFiles = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_STATIC_MAX_TOTAL_BYTES")); v != "" {
		if iv, err := atoi64(v); err == nil {
			cfg.StaticHosting.MaxTotalBytes = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_STATIC_MAX_FILE_BYTES")); v != "" {
		if iv, err := atoi64(v); err == nil {
			cfg.StaticHosting.MaxFileBytes = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_STATIC_REQUIRE_APPROVAL")); v != "" {
		if bv, ok := atob(v); ok {
			cfg.StaticHosting.RequireApproval = bv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_ENABLED")); v != "" {
		if bv, ok := atob(v); ok {
			cfg.DynamicHosting.Enabled = bv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_CONTROL_HOSTS")); v != "" {
		cfg.DynamicHosting.ControlHosts = splitCommaSeparated(v)
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_APP_DOMAIN")); v != "" {
		cfg.DynamicHosting.AppDomain = v
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_PUBLIC_SCHEME")); v != "" {
		cfg.DynamicHosting.PublicScheme = v
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_ALLOWED_EXECUTABLES")); v != "" {
		cfg.DynamicHosting.AllowedExecutables = splitCommaSeparated(v)
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_REQUIRE_APPROVAL")); v != "" {
		if bv, ok := atob(v); ok {
			cfg.DynamicHosting.RequireApproval = bv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_RESERVED_NAMES")); v != "" {
		cfg.DynamicHosting.ReservedNames = splitCommaSeparated(v)
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_ENV_ALLOWLIST")); v != "" {
		cfg.DynamicHosting.EnvAllowlist = splitCommaSeparated(v)
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_MAX_APPS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.DynamicHosting.MaxApps = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_MAX_FILES")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.DynamicHosting.MaxFiles = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_MAX_TOTAL_BYTES")); v != "" {
		if iv, err := atoi64(v); err == nil {
			cfg.DynamicHosting.MaxTotalBytes = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_MAX_FILE_BYTES")); v != "" {
		if iv, err := atoi64(v); err == nil {
			cfg.DynamicHosting.MaxFileBytes = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_STARTUP_TIMEOUT_SECONDS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.DynamicHosting.StartupTimeoutSeconds = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_SHUTDOWN_TIMEOUT_SECONDS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.DynamicHosting.ShutdownTimeoutSeconds = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_RESTART_POLICY")); v != "" {
		cfg.DynamicHosting.RestartPolicy = v
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_RESTART_MAX_ATTEMPTS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.DynamicHosting.RestartMaxAttempts = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_RESTART_BACKOFF_SECONDS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.DynamicHosting.RestartBackoffSeconds = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_RESTART_RESET_SECONDS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.DynamicHosting.RestartResetSeconds = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_MAX_LOG_BYTES")); v != "" {
		if iv, err := atoi64(v); err == nil {
			cfg.DynamicHosting.MaxLogBytes = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_MAX_LOG_FILES")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.DynamicHosting.MaxLogFiles = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_RELEASE_RETENTION")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.DynamicHosting.ReleaseRetention = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_MAX_REQUEST_BODY_BYTES")); v != "" {
		if iv, err := atoi64(v); err == nil {
			cfg.DynamicHosting.MaxRequestBodyBytes = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_MAX_REQUEST_HEADER_BYTES")); v != "" {
		if iv, err := atoi64(v); err == nil {
			cfg.DynamicHosting.MaxRequestHeaderBytes = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_MAX_CONCURRENT_REQUESTS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.DynamicHosting.MaxConcurrentRequests = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_UPSTREAM_HEADER_TIMEOUT_SECONDS")); v != "" {
		if iv, err := atoi(v); err == nil {
			cfg.DynamicHosting.UpstreamHeaderTimeoutSeconds = iv
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOCALGATE_DYNAMIC_MAX_RESPONSE_HEADER_BYTES")); v != "" {
		if iv, err := atoi64(v); err == nil {
			cfg.DynamicHosting.MaxResponseHeaderBytes = iv
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
		Mode:       strings.TrimSpace(os.Getenv("LOCALGATE_RUNTIME_MODE")),
		Bin:        strings.TrimSpace(os.Getenv("LOCALGATE_RUNTIME_BIN")),
		ConfigPath: strings.TrimSpace(os.Getenv("LOCALGATE_RUNTIME_CONFIG")),
		Bind:       strings.TrimSpace(os.Getenv("LOCALGATE_RUNTIME_BIND")),
		PIDFile:    strings.TrimSpace(os.Getenv("LOCALGATE_RUNTIME_PID_FILE")),
		LogFile:    strings.TrimSpace(os.Getenv("LOCALGATE_RUNTIME_LOG_FILE")),
		RunDir:     strings.TrimSpace(os.Getenv("LOCALGATE_RUNTIME_RUN_DIR")),
	}

	// Flag overrides
	if opts.Bind != "" {
		cfg.Bind = opts.Bind
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
	if !cfg.Admin.Enabled {
		cfg.Admin.Approval.Enabled = false
	}
	cfg.DynamicHosting.AppDomain = normalizeHostname(cfg.DynamicHosting.AppDomain)
	for i := range cfg.DynamicHosting.ControlHosts {
		cfg.DynamicHosting.ControlHosts[i] = normalizeHostname(cfg.DynamicHosting.ControlHosts[i])
	}
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
	if err := validateStaticHosting(cfg.StaticHosting); err != nil {
		return Config{}, err
	}
	if err := validateDynamicHosting(cfg.DynamicHosting); err != nil {
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

func rejectLegacyApprovalConfig(data []byte) error {
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil
	}
	if _, exists := root["approval"]; exists {
		return errors.New("top-level approval config was removed; move it to admin.approval and move base_url to admin.base_url")
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
		"static_hosting": map[string]any{
			"max_files":        c.StaticHosting.MaxFiles,
			"max_total_bytes":  c.StaticHosting.MaxTotalBytes,
			"max_file_bytes":   c.StaticHosting.MaxFileBytes,
			"require_approval": c.StaticHosting.RequireApproval,
		},
		"dynamic_hosting": map[string]any{
			"enabled":                         c.DynamicHosting.Enabled,
			"control_hosts":                   c.DynamicHosting.ControlHosts,
			"app_domain":                      c.DynamicHosting.AppDomain,
			"public_scheme":                   c.DynamicHosting.PublicScheme,
			"require_approval":                c.DynamicHosting.RequireApproval,
			"allowed_executables":             c.DynamicHosting.AllowedExecutables,
			"max_apps":                        c.DynamicHosting.MaxApps,
			"max_files":                       c.DynamicHosting.MaxFiles,
			"max_total_bytes":                 c.DynamicHosting.MaxTotalBytes,
			"max_file_bytes":                  c.DynamicHosting.MaxFileBytes,
			"startup_timeout_seconds":         c.DynamicHosting.StartupTimeoutSeconds,
			"shutdown_timeout_seconds":        c.DynamicHosting.ShutdownTimeoutSeconds,
			"restart_policy":                  c.DynamicHosting.RestartPolicy,
			"restart_max_attempts":            c.DynamicHosting.RestartMaxAttempts,
			"restart_backoff_seconds":         c.DynamicHosting.RestartBackoffSeconds,
			"restart_reset_seconds":           c.DynamicHosting.RestartResetSeconds,
			"max_log_bytes":                   c.DynamicHosting.MaxLogBytes,
			"max_log_files":                   c.DynamicHosting.MaxLogFiles,
			"release_retention":               c.DynamicHosting.ReleaseRetention,
			"max_request_body_bytes":          c.DynamicHosting.MaxRequestBodyBytes,
			"max_request_header_bytes":        c.DynamicHosting.MaxRequestHeaderBytes,
			"max_concurrent_requests":         c.DynamicHosting.MaxConcurrentRequests,
			"upstream_header_timeout_seconds": c.DynamicHosting.UpstreamHeaderTimeoutSeconds,
			"max_response_header_bytes":       c.DynamicHosting.MaxResponseHeaderBytes,
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
			"mode": c.Runtime.Mode,
			"bind": c.Runtime.Bind,
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

func validateStaticHosting(cfg StaticHosting) error {
	if cfg.MaxFiles <= 0 {
		return errors.New("static_hosting.max_files must be positive")
	}
	if cfg.MaxTotalBytes <= 0 {
		return errors.New("static_hosting.max_total_bytes must be positive")
	}
	if cfg.MaxFileBytes <= 0 {
		return errors.New("static_hosting.max_file_bytes must be positive")
	}
	if cfg.MaxFileBytes > cfg.MaxTotalBytes {
		return errors.New("static_hosting.max_file_bytes must not exceed max_total_bytes")
	}
	return nil
}

func validateDynamicHosting(cfg DynamicHosting) error {
	if cfg.MaxApps <= 0 {
		return errors.New("dynamic_hosting.max_apps must be positive")
	}
	if cfg.MaxFiles <= 0 || cfg.MaxTotalBytes <= 0 || cfg.MaxFileBytes <= 0 {
		return errors.New("dynamic_hosting file limits must be positive")
	}
	if cfg.MaxFileBytes > cfg.MaxTotalBytes {
		return errors.New("dynamic_hosting.max_file_bytes must not exceed max_total_bytes")
	}
	if cfg.StartupTimeoutSeconds <= 0 || cfg.ShutdownTimeoutSeconds <= 0 {
		return errors.New("dynamic_hosting startup and shutdown timeouts must be positive")
	}
	if cfg.RestartMaxAttempts < 0 || cfg.RestartBackoffSeconds < 0 || cfg.RestartResetSeconds <= 0 {
		return errors.New("dynamic_hosting restart limits are invalid")
	}
	if cfg.MaxLogBytes <= 0 || cfg.MaxLogFiles <= 0 || cfg.ReleaseRetention <= 0 {
		return errors.New("dynamic_hosting log and release limits are invalid")
	}
	if cfg.MaxRequestBodyBytes <= 0 || cfg.MaxRequestHeaderBytes <= 0 || cfg.MaxConcurrentRequests <= 0 || cfg.UpstreamHeaderTimeoutSeconds <= 0 || cfg.MaxResponseHeaderBytes <= 0 {
		return errors.New("dynamic_hosting proxy limits are invalid")
	}
	switch cfg.RestartPolicy {
	case "never", "on-failure":
	default:
		return errors.New("dynamic_hosting.restart_policy must be never or on-failure")
	}
	if cfg.PublicScheme != "http" && cfg.PublicScheme != "https" {
		return errors.New("dynamic_hosting.public_scheme must be http or https")
	}
	if !cfg.Enabled {
		return nil
	}
	if cfg.AppDomain == "" {
		return errors.New("dynamic_hosting.app_domain is required when enabled")
	}
	if len(cfg.ControlHosts) == 0 {
		return errors.New("dynamic_hosting.control_hosts must not be empty when enabled")
	}
	if len(cfg.AllowedExecutables) == 0 {
		return errors.New("dynamic_hosting.allowed_executables must not be empty when enabled")
	}
	seen := map[string]struct{}{}
	for _, host := range cfg.ControlHosts {
		if host == "" || !validHostname(host) {
			return fmt.Errorf("invalid dynamic_hosting control host: %q", host)
		}
		if host == cfg.AppDomain || strings.HasSuffix(host, "."+cfg.AppDomain) {
			// Exact control hosts are permitted under the app domain and take precedence.
		}
		if _, ok := seen[host]; ok {
			return fmt.Errorf("duplicate dynamic_hosting control host: %s", host)
		}
		seen[host] = struct{}{}
	}
	if !validHostname(cfg.AppDomain) {
		return errors.New("dynamic_hosting.app_domain is invalid")
	}
	return nil
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

func MergeHiddenTools(hidden []string) []string {
	if len(DefaultHiddenTools) == 0 && len(hidden) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(DefaultHiddenTools)+len(hidden))
	out := make([]string, 0, len(DefaultHiddenTools)+len(hidden))
	for _, name := range DefaultHiddenTools {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	for _, name := range hidden {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
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
