package filesystem

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestValidatePublicIP(t *testing.T) {
	rejected := []string{
		"0.0.0.0",
		"127.0.0.1",
		"10.0.0.1",
		"172.16.0.1",
		"192.168.0.1",
		"169.254.1.1",
		"224.0.0.1",
		"::",
		"::1",
		"fc00::1",
		"fe80::1",
		"ff02::1",
	}
	for _, raw := range rejected {
		if err := validatePublicIP(net.ParseIP(raw)); err == nil {
			t.Fatalf("expected %s to be rejected", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if err := validatePublicIP(net.ParseIP(raw)); err != nil {
			t.Fatalf("expected %s to be allowed: %v", raw, err)
		}
	}
}

func TestResolvePublicIPsRejectsMixedDNSAnswers(t *testing.T) {
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("127.0.0.1")}}, nil
	}
	if _, err := resolvePublicIPs(context.Background(), "mixed.example", lookup); err == nil || !strings.Contains(err.Error(), "not public") {
		t.Fatalf("expected mixed DNS answer rejection, got %v", err)
	}
}

func TestRemoteDownloadRedirectRevalidatesTarget(t *testing.T) {
	lookup := func(_ context.Context, host string) ([]net.IPAddr, error) {
		switch host {
		case "public.example":
			return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
		case "internal.example":
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		default:
			return nil, &net.DNSError{Name: host, Err: "not found"}
		}
	}
	client := newRemoteDownloadClientWithLookup(lookup)
	redirectURL, _ := url.Parse("http://internal.example/file")
	previousURL, _ := url.Parse("http://public.example/start")
	err := client.CheckRedirect(&http.Request{URL: redirectURL}, []*http.Request{{URL: previousURL}})
	if err == nil || !strings.Contains(err.Error(), "redirect target rejected") {
		t.Fatalf("expected redirect rejection, got %v", err)
	}
	if client.Timeout != remoteDownloadTimeout {
		t.Fatalf("unexpected client timeout: %s", client.Timeout)
	}
}
