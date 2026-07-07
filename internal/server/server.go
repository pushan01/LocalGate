package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type principalContextKey struct{}

type Principal struct {
	ID            string `json:"id"`
	AuthMethod    string `json:"auth_method"`
	Authenticated bool   `json:"authenticated"`
}

func PrincipalFromRequest(r *http.Request) Principal {
	if r != nil {
		if principal, ok := r.Context().Value(principalContextKey{}).(Principal); ok && strings.TrimSpace(principal.ID) != "" {
			return principal
		}
	}
	return Principal{ID: "local-unauthenticated", AuthMethod: "none", Authenticated: false}
}

func withPrincipal(r *http.Request, principal Principal) *http.Request {
	if r == nil {
		return nil
	}
	return r.WithContext(context.WithValue(r.Context(), principalContextKey{}, principal))
}

type Options struct {
	Bind           string
	Version        string
	Auth           AuthOptions
	CORS           CORSOptions
	AdminBaseURL   string
	ControlBaseURL string
	Register       func(mux *http.ServeMux)
}

type AdminOptions struct {
	Bind     string
	BaseURL  string
	Auth     AuthOptions
	CORS     CORSOptions
	Register func(mux *http.ServeMux)
}

type AuthOptions struct {
	Enabled           bool
	BearerToken       string
	AllowPaths        []string
	AllowPathPrefixes []string
}

type CORSOptions struct {
	Enabled        bool
	AllowedOrigins []string
}

func MatchHeaderToken(r *http.Request, headerName, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" || r == nil {
		return false
	}
	got := strings.TrimSpace(r.Header.Get(headerName))
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

type Server struct {
	opts Options
	srv  *http.Server
	ln   net.Listener
}

func New(opts Options) *Server {
	mux := http.NewServeMux()
	controlHandler := wrapManagementURLBoundary(
		wrapCORS(wrapBearerAuth(mux, opts.Auth), opts.CORS),
		opts.AdminBaseURL,
		opts.ControlBaseURL,
	)
	handler := http.Handler(controlHandler)
	if approvalURLHost(opts.ControlBaseURL) != "" || approvalURLHost(opts.AdminBaseURL) != "" {
		handler = newControlHostBoundary(controlHandler, opts.ControlBaseURL, opts.AdminBaseURL, opts.Bind)
	}
	s := &Server{
		opts: opts,
		srv: &http.Server{
			Addr:              opts.Bind,
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
	}
	mux.HandleFunc("/healthz", s.handleHealthz)
	if opts.Register != nil {
		opts.Register(mux)
	}
	return s
}

func NewAdmin(opts AdminOptions) *Server {
	mux := http.NewServeMux()
	adminHandler := wrapCORS(wrapBearerAuth(mux, opts.Auth), opts.CORS)
	handler := http.Handler(adminHandler)
	if approvalURLHost(opts.BaseURL) != "" {
		handler = newAdminHostBoundary(adminHandler, opts.BaseURL)
	}
	s := &Server{
		opts: Options{Bind: opts.Bind},
		srv: &http.Server{
			Addr:              opts.Bind,
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
	}
	if opts.Register != nil {
		opts.Register(mux)
	}
	return s
}

func wrapManagementURLBoundary(next http.Handler, adminBaseURL, controlBaseURL string) http.Handler {
	adminConfigured := approvalURLHost(adminBaseURL) != ""
	if !adminConfigured {
		return next
	}
	adminDedicated := adminConfigured && !sameBaseURLAuthority(adminBaseURL, controlBaseURL)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		adminPath := isManagementRequestPath(r.URL.Path)
		adminHostRequest := adminConfigured && RequestHostMatchesBaseURL(r.Host, adminBaseURL)
		if adminPath && !adminHostRequest {
			http.NotFound(w, r)
			return
		}
		if adminDedicated && adminHostRequest && !adminPath {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLegacyManagementRequestPath(path string) bool {
	return path == "/approvals" || strings.HasPrefix(path, "/approvals/") ||
		path == "/approval-ui" || strings.HasPrefix(path, "/approval-ui/") ||
		path == "/deploy-mgr" || strings.HasPrefix(path, "/deploy-mgr/")
}

func isAdminRequestPath(path string) bool {
	return path == "/admin" || strings.HasPrefix(path, "/admin/") ||
		path == "/api/admin/v1" || strings.HasPrefix(path, "/api/admin/v1/")
}

func isManagementRequestPath(path string) bool {
	return isAdminRequestPath(path) || isLegacyManagementRequestPath(path)
}

func approvalURLHost(baseURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
}

func sameBaseURLAuthority(first, second string) bool {
	firstURL, firstErr := url.Parse(strings.TrimSpace(first))
	secondURL, secondErr := url.Parse(strings.TrimSpace(second))
	if firstErr != nil || secondErr != nil || firstURL.Hostname() == "" || secondURL.Hostname() == "" {
		return false
	}
	if !strings.EqualFold(strings.TrimSuffix(firstURL.Hostname(), "."), strings.TrimSuffix(secondURL.Hostname(), ".")) {
		return false
	}
	return effectiveURLPort(firstURL) == effectiveURLPort(secondURL)
}

func effectiveURLPort(value *url.URL) string {
	if value == nil {
		return ""
	}
	if port := value.Port(); port != "" {
		return port
	}
	if value.Scheme == "https" {
		return "443"
	}
	if value.Scheme == "http" {
		return "80"
	}
	return ""
}

func RequestHostMatchesBaseURL(requestHost, baseURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	host := strings.TrimSpace(requestHost)
	requestName := host
	requestPort := ""
	if parsedHost, parsedPort, splitErr := net.SplitHostPort(host); splitErr == nil {
		requestName, requestPort = parsedHost, parsedPort
	} else if strings.Count(host, ":") > 0 {
		return false
	}
	requestName = strings.ToLower(strings.TrimSuffix(requestName, "."))
	if requestName != strings.ToLower(strings.TrimSuffix(parsed.Hostname(), ".")) {
		return false
	}
	expectedPort := parsed.Port()
	if expectedPort == "" {
		return requestPort == "" || (parsed.Scheme == "https" && requestPort == "443") || (parsed.Scheme == "http" && requestPort == "80")
	}
	return requestPort == expectedPort
}

func wrapCORS(next http.Handler, cors CORSOptions) http.Handler {
	allowedOrigins := make(map[string]struct{}, len(cors.AllowedOrigins))
	for _, origin := range cors.AllowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			allowedOrigins[origin] = struct{}{}
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		// Approval and admin mutations have their own stricter same-origin
		// validation. Let those handlers receive Origin requests even when the
		// general cross-origin API policy is disabled; no CORS headers are added.
		if isManagementRequestPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if !cors.Enabled {
			WriteError(w, http.StatusForbidden, "origin_forbidden", "browser-origin requests are disabled", nil)
			return
		}
		if _, ok := allowedOrigins[origin]; !ok {
			WriteError(w, http.StatusForbidden, "origin_forbidden", "origin is not allowed", nil)
			return
		}

		headers := w.Header()
		headers.Set("Access-Control-Allow-Origin", origin)
		headers.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		headers.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Last-Event-ID, Mcp-Session-Id, X-LocalGate-Tool-Access, X-LocalGate-Upload-Token, X-LocalGate-Upgrade-Token")
		headers.Set("Access-Control-Expose-Headers", "Content-Type, Mcp-Session-Id")
		headers.Add("Vary", "Origin")
		headers.Add("Vary", "Access-Control-Request-Method")
		headers.Add("Vary", "Access-Control-Request-Headers")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func wrapBearerAuth(next http.Handler, auth AuthOptions) http.Handler {
	allowPaths := make(map[string]struct{}, len(auth.AllowPaths))
	for _, path := range auth.AllowPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		allowPaths[path] = struct{}{}
	}
	allowPathPrefixes := make([]string, 0, len(auth.AllowPathPrefixes))
	for _, prefix := range auth.AllowPathPrefixes {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" {
			continue
		}
		allowPathPrefixes = append(allowPathPrefixes, prefix)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !auth.Enabled || strings.TrimSpace(auth.BearerToken) == "" {
			next.ServeHTTP(w, withPrincipal(r, Principal{
				ID:            "local-unauthenticated",
				AuthMethod:    "none",
				Authenticated: false,
			}))
			return
		}
		// Admin UI assets are public so the browser can load the login shell.
		// Every /api/admin/v1 request remains protected by Bearer auth.
		if r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/") {
			next.ServeHTTP(w, withPrincipal(r, Principal{
				ID:            "public-admin-ui",
				AuthMethod:    "public_asset",
				Authenticated: false,
			}))
			return
		}
		if _, ok := allowPaths[r.URL.Path]; ok {
			next.ServeHTTP(w, withPrincipal(r, Principal{
				ID:            "public-unauthenticated",
				AuthMethod:    "allow_path",
				Authenticated: false,
			}))
			return
		}
		for _, allowedPrefix := range allowPathPrefixes {
			if strings.HasPrefix(r.URL.Path, allowedPrefix) {
				next.ServeHTTP(w, withPrincipal(r, Principal{
					ID:            "public-unauthenticated",
					AuthMethod:    "allow_path_prefix",
					Authenticated: false,
				}))
				return
			}
		}
		const prefix = "Bearer "
		authz := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(authz, prefix) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="localgate"`)
			WriteError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token", nil)
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(authz, prefix))
		if subtle.ConstantTimeCompare([]byte(token), []byte(auth.BearerToken)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="localgate"`)
			WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer token", nil)
			return
		}
		next.ServeHTTP(w, withPrincipal(r, Principal{
			ID:            "http-bearer",
			AuthMethod:    "bearer",
			Authenticated: true,
		}))
	})
}

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.opts.Bind)
	if err != nil {
		return err
	}
	s.ln = ln
	return s.srv.Serve(ln)
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"status":  "ok",
		"version": s.opts.Version,
		"time":    time.Now().UTC().Format(time.RFC3339Nano),
	}
	_ = json.NewEncoder(w).Encode(resp)
}

type APIError struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

func WriteError(w http.ResponseWriter, status int, code, msg string, details any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(APIError{
		Code:    code,
		Message: msg,
		Details: details,
	})
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		WriteError(w, http.StatusInternalServerError, "encode_error", fmt.Sprintf("failed to encode response: %v", err), nil)
	}
}
