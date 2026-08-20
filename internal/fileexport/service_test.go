package fileexport

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestServiceIssueLinkAuthorizeExpireAndRevoke(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	svc, err := NewService(Options{
		BaseURL:    "https://gate.example/",
		DefaultTTL: 5 * time.Minute,
		MaxTTL:     10 * time.Minute,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	grant, err := svc.Issue(Grant{
		Path:            "/workspace/report.pdf",
		FileName:        "report.pdf",
		MIMEType:        "application/pdf",
		Size:            123,
		SHA256:          strings.Repeat("a", 64),
		ModTimeUnixNano: 42,
	}, 0)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if grant.ID == "" || !strings.HasPrefix(grant.ID, "lgexp_") {
		t.Fatalf("unexpected grant id: %q", grant.ID)
	}
	if got := grant.ExpiresAt; !got.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("unexpected expiry: %s", got)
	}

	link, err := svc.Link(grant.ID)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if !strings.HasPrefix(link.URI, "https://gate.example/system/exports/"+grant.ID+"?signature=") {
		t.Fatalf("unexpected URI: %q", link.URI)
	}
	signature := strings.SplitN(link.URI, "signature=", 2)[1]
	authorized, err := svc.Authorize(grant.ID, signature)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if authorized.Path != grant.Path || authorized.FileName != grant.FileName {
		t.Fatalf("unexpected authorized grant: %#v", authorized)
	}
	if _, err := svc.Authorize(grant.ID, signature+"x"); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected invalid signature, got %v", err)
	}

	now = now.Add(6 * time.Minute)
	if _, err := svc.Authorize(grant.ID, signature); !errors.Is(err, ErrExportExpired) {
		t.Fatalf("expected expired export, got %v", err)
	}

	now = now.Add(time.Minute)
	grant2, err := svc.Issue(Grant{
		Path:     "/workspace/other.txt",
		FileName: "other.txt",
		MIMEType: "text/plain",
		Size:     1,
		SHA256:   strings.Repeat("b", 64),
	}, time.Minute)
	if err != nil {
		t.Fatalf("Issue second grant: %v", err)
	}
	link2, err := svc.Link(grant2.ID)
	if err != nil {
		t.Fatalf("Link second grant: %v", err)
	}
	signature2 := strings.SplitN(link2.URI, "signature=", 2)[1]
	svc.Revoke(grant2.ID)
	if _, err := svc.Authorize(grant2.ID, signature2); !errors.Is(err, ErrExportNotFound) {
		t.Fatalf("expected revoked export to be absent, got %v", err)
	}
}

func TestServiceRejectsInvalidTTLAndMetadata(t *testing.T) {
	svc, err := NewService(Options{
		BaseURL:    "https://gate.example",
		DefaultTTL: time.Minute,
		MaxTTL:     2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	base := Grant{
		Path:     "/workspace/file.txt",
		FileName: "file.txt",
		MIMEType: "text/plain",
		Size:     1,
		SHA256:   strings.Repeat("c", 64),
	}
	if _, err := svc.Issue(base, 3*time.Minute); err == nil {
		t.Fatal("expected oversized TTL to fail")
	}
	base.SHA256 = "not-a-digest"
	if _, err := svc.Issue(base, time.Minute); err == nil {
		t.Fatal("expected invalid sha256 to fail")
	}
}
