package server

import (
	"net"
	"net/http"
	"strings"
)

type hostRouter struct {
	control        http.Handler
	application    http.Handler
	controlHosts   map[string]struct{}
	appDomain      string
	adminHost      string
	adminExclusive bool
}

func newHostRouter(control http.Handler, options HostRoutingOptions) http.Handler {
	controlHosts := make(map[string]struct{}, len(options.ControlHosts))
	for _, host := range options.ControlHosts {
		if normalized, ok := normalizeRequestHost(host); ok {
			controlHosts[normalized] = struct{}{}
		}
	}
	adminHost := approvalURLHost(options.AdminBaseURL)
	_, adminIsControl := controlHosts[adminHost]
	return &hostRouter{
		control:        control,
		application:    options.AppHandler,
		controlHosts:   controlHosts,
		appDomain:      strings.ToLower(strings.TrimSuffix(strings.TrimSpace(options.AppDomain), ".")),
		adminHost:      adminHost,
		adminExclusive: adminHost != "" && !adminIsControl,
	}
}

func (h *hostRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	if _, ok := h.controlHosts[host]; ok {
		h.control.ServeHTTP(w, r)
		return
	}
	if h.application != nil && isSingleLabelSubdomain(host, h.appDomain) {
		h.application.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
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
		// IPv6 literals are not valid deployment or control hostnames.
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

func isSingleLabelSubdomain(host, domain string) bool {
	if host == "" || domain == "" || !strings.HasSuffix(host, "."+domain) {
		return false
	}
	label := strings.TrimSuffix(host, "."+domain)
	return label != "" && !strings.Contains(label, ".")
}
