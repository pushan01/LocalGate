package server

import (
	"net"
	"net/http"
	"strings"
)

type controlHostBoundary struct {
	control        http.Handler
	controlHost    string
	adminHost      string
	adminExclusive bool
	localMCPPort   string
}

type adminHostBoundary struct {
	admin     http.Handler
	adminHost string
}

func newAdminHostBoundary(admin http.Handler, adminBaseURL string) http.Handler {
	return &adminHostBoundary{admin: admin, adminHost: approvalURLHost(adminBaseURL)}
}

func (h *adminHostBoundary) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, ok := normalizeRequestHost(r.Host)
	if !ok {
		WriteError(w, http.StatusBadRequest, "invalid_host", "invalid request host", nil)
		return
	}
	if h.adminHost != "" && host == h.adminHost {
		h.admin.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

func newControlHostBoundary(control http.Handler, controlBaseURL, adminBaseURL, controlBind string) http.Handler {
	controlHost := approvalURLHost(controlBaseURL)
	adminHost := approvalURLHost(adminBaseURL)
	return &controlHostBoundary{
		control:        control,
		controlHost:    controlHost,
		adminHost:      adminHost,
		adminExclusive: adminHost != "" && adminHost != controlHost,
		localMCPPort:   listenerPort(controlBind),
	}
}

func (h *controlHostBoundary) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/mcp" && isLoopbackAuthorityOnPort(r.Host, h.localMCPPort) {
		h.control.ServeHTTP(w, r)
		return
	}
	host, ok := normalizeRequestHost(r.Host)
	if !ok {
		WriteError(w, http.StatusBadRequest, "invalid_host", "invalid request host", nil)
		return
	}
	if isManagementRequestPath(r.URL.Path) {
		if h.adminHost != "" && host == h.adminHost {
			h.control.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
		return
	}
	if h.adminExclusive && host == h.adminHost {
		http.NotFound(w, r)
		return
	}
	if h.controlHost != "" && host == h.controlHost {
		h.control.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

func listenerPort(bind string) string {
	_, port, err := net.SplitHostPort(strings.TrimSpace(bind))
	if err != nil {
		return ""
	}
	return port
}

func isLoopbackAuthorityOnPort(raw, expectedPort string) bool {
	if expectedPort == "" {
		return false
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(raw))
	if err != nil || port != expectedPort {
		return false
	}
	host = strings.TrimSpace(strings.TrimSuffix(host, "."))
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func normalizeRequestHost(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	host := raw
	if parsedHost, _, err := net.SplitHostPort(raw); err == nil {
		host = parsedHost
	} else if strings.Contains(raw, ":") {
		return "", false
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" || len(host) > 253 {
		return "", false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, ch := range label {
			if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' {
				continue
			}
			return "", false
		}
	}
	return host, true
}
