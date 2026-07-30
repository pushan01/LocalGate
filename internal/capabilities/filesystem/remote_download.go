package filesystem

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const remoteDownloadTimeout = 30 * time.Second

type ipLookupFunc func(context.Context, string) ([]net.IPAddr, error)

func newRemoteDownloadClient() *http.Client {
	return newRemoteDownloadClientWithLookup(net.DefaultResolver.LookupIPAddr)
}

func newRemoteDownloadClientWithLookup(lookup ipLookupFunc) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("parse remote address: %w", err)
			}
			ips, err := resolvePublicIPs(ctx, host, lookup)
			if err != nil {
				return nil, err
			}
			var lastErr error
			for _, ip := range ips {
				conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if dialErr == nil {
					return conn, nil
				}
				lastErr = dialErr
			}
			if lastErr == nil {
				lastErr = errors.New("remote host resolved without usable addresses")
			}
			return nil, lastErr
		},
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   remoteDownloadTimeout,
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		if err := validateRemoteURL(req.Context(), req.URL, lookup); err != nil {
			return fmt.Errorf("redirect target rejected: %w", err)
		}
		return nil
	}
	return client
}

func parseRemoteURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid file.download_url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("file.download_url must be http or https")
	}
	if u.Hostname() == "" {
		return nil, errors.New("file.download_url host required")
	}
	if u.User != nil {
		return nil, errors.New("file.download_url must not include userinfo")
	}
	return u, nil
}

func validateRemoteURL(ctx context.Context, u *url.URL, lookup ipLookupFunc) error {
	if u == nil {
		return errors.New("remote URL required")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("file.download_url must be http or https")
	}
	if u.User != nil {
		return errors.New("file.download_url must not include userinfo")
	}
	_, err := resolvePublicIPs(ctx, u.Hostname(), lookup)
	return err
}

func resolvePublicIPs(ctx context.Context, host string, lookup ipLookupFunc) ([]net.IP, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, errors.New("remote host required")
	}
	if literal := net.ParseIP(host); literal != nil {
		if err := validatePublicIP(literal); err != nil {
			return nil, err
		}
		return []net.IP{literal}, nil
	}
	if lookup == nil {
		return nil, errors.New("remote DNS resolver unavailable")
	}
	addrs, err := lookup(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve remote host %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("remote host %s resolved without addresses", host)
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		if err := validatePublicIP(addr.IP); err != nil {
			return nil, fmt.Errorf("remote host %s: %w", host, err)
		}
		ips = append(ips, addr.IP)
	}
	return ips, nil
}

func validatePublicIP(ip net.IP) error {
	if ip == nil {
		return errors.New("remote address is invalid")
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || !ip.IsGlobalUnicast() {
		return fmt.Errorf("remote address %s is not public", ip.String())
	}
	return nil
}
