package dynamic

import (
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s == nil || !s.config.Enabled {
		http.NotFound(w, r)
		return
	}
	host, ok := normalizeHost(r.Host)
	if !ok {
		http.Error(w, "invalid host", http.StatusBadRequest)
		return
	}
	name, access, ok := s.resolveHost(host)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.mu.RLock()
	deployment, found := s.deployments[name]
	proc := s.processes[name]
	s.mu.RUnlock()
	if found && normalizeAccess(deployment.Access, "public") != access {
		found = false
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	if deployment.Status != StatusActive || proc == nil {
		http.Error(w, "deployment unavailable", http.StatusServiceUnavailable)
		return
	}
	if s.proxySlots != nil {
		select {
		case s.proxySlots <- struct{}{}:
			defer func() { <-s.proxySlots }()
		default:
			http.Error(w, "deployment request limit exceeded", http.StatusServiceUnavailable)
			return
		}
	}
	if s.config.MaxRequestBodyBytes > 0 && r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, s.config.MaxRequestBodyBytes)
	}
	target := &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(proc.port))}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = s.proxyTransport
	originalDirector := proxy.Director
	proxy.Director = func(request *http.Request) {
		originalHost := request.Host
		for _, header := range []string{
			"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Port", "X-Real-IP",
		} {
			request.Header.Del(header)
		}
		originalDirector(request)
		request.Host = originalHost
		request.Header.Set("X-Forwarded-Host", originalHost)
		request.Header.Set("X-Forwarded-Proto", s.baseSchemeForAccess(access))
		if remoteHost, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			request.Header.Set("X-Real-IP", remoteHost)
		}
		request.Header.Set("X-LocalGate-Deployment", deployment.Name)
	}
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "deployment backend unavailable", http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}

func (s *Service) resolveHost(host string) (string, string, bool) {
	for _, candidate := range []struct{ access, domain string }{
		{"public", appBaseHostname(s.config.PublicBaseURL)},
		{"private", appBaseHostname(s.config.PrivateBaseURL)},
	} {
		if candidate.domain == "" || !strings.HasSuffix(host, "."+candidate.domain) {
			continue
		}
		name := strings.TrimSuffix(host, "."+candidate.domain)
		if name != "" && !strings.Contains(name, ".") {
			return name, candidate.access, true
		}
	}
	return "", "", false
}

func normalizeHost(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	host := raw
	if parsed, _, err := net.SplitHostPort(raw); err == nil {
		host = parsed
	} else if strings.Contains(raw, ":") {
		return "", false
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return "", false
	}
	return host, true
}
