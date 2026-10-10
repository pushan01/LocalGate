package fileexport

import (
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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

func TestPersistentGrantAcrossOperatingSystemProcesses(t *testing.T) {
	store := filepath.Join(t.TempDir(), "exports")
	opts := Options{BaseURL: "https://gate.example", DefaultTTL: time.Minute, MaxTTL: 2 * time.Minute, StoreDir: store}
	issuer, err := NewService(opts)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := issuer.Issue(Grant{
		Path: "/workspace/report.pdf", FileName: "report.pdf",
		MIMEType: "application/pdf", Size: 42,
		SHA256: strings.Repeat("a", 64), ModTimeUnixNano: 123,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	link, err := issuer.Link(grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(link.URI)
	if err != nil {
		t.Fatal(err)
	}
	signature := u.Query().Get("signature")
	if signature == "" {
		t.Fatal("missing signature")
	}
	info, err := os.Stat(filepath.Join(store, grant.ID+".json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("grant must be private (0600): info=%v err=%v", info, err)
	}
	checkChild := func(expect string) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestPersistentGrantChild$")
		cmd.Env = append(os.Environ(),
			"LG_EXPORT_CHILD=1", "LG_EXPORT_STORE="+store,
			"LG_EXPORT_ID="+grant.ID, "LG_EXPORT_SIGNATURE="+signature,
			"LG_EXPORT_EXPECT="+expect)
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "PASS") {
			t.Fatalf("subprocess check %q failed: %v: %s", expect, err, out)
		}
	}
	checkChild("authorized")
	issuer.Revoke(grant.ID)
	checkChild("revoked")
}

func TestPersistentGrantChild(t *testing.T) {
	if os.Getenv("LG_EXPORT_CHILD") != "1" {
		return
	}
	svc, err := NewService(Options{
		BaseURL:    "https://gate.example",
		DefaultTTL: time.Minute, MaxTTL: 2 * time.Minute,
		StoreDir: os.Getenv("LG_EXPORT_STORE"),
	})
	if err != nil {
		t.Fatal(err)
	}
	id, signature := os.Getenv("LG_EXPORT_ID"), os.Getenv("LG_EXPORT_SIGNATURE")
	grant, err := svc.Authorize(id, signature)
	if os.Getenv("LG_EXPORT_EXPECT") == "revoked" {
		if !errors.Is(err, ErrExportNotFound) {
			t.Fatalf("expected revoked export to fail across processes, got %v", err)
		}
		return
	}
	if err != nil || grant.Size != 42 || grant.SHA256 != strings.Repeat("a", 64) {
		t.Fatalf("cross-process grant mismatch: %#v, %v", grant, err)
	}
	link, err := svc.Link(id)
	if err != nil || !strings.Contains(link.URI, "signature="+signature) {
		t.Fatalf("cross-process signature must be stable: %#v, %v", link, err)
	}
}

func TestPersistentGrantExpiresAndPrivateStoreRequired(t *testing.T) {
	store := filepath.Join(t.TempDir(), "exports")
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	opts := Options{
		BaseURL: "https://gate.example", DefaultTTL: time.Minute,
		MaxTTL: 2 * time.Minute, StoreDir: store,
		Now: func() time.Time { return now },
	}
	svc, err := NewService(opts)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := svc.Issue(Grant{
		Path: "/workspace/report.pdf", FileName: "report.pdf",
		SHA256: strings.Repeat("a", 64),
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	link, err := svc.Link(grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(link.URI)
	now = now.Add(2 * time.Minute)
	second, err := NewService(opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Authorize(grant.ID, u.Query().Get("signature")); !errors.Is(err, ErrExportExpired) {
		t.Fatalf("expired grant must not work in another instance: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store, grant.ID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired grant must be cleaned up: %v", err)
	}
	if err := os.Chmod(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(opts); err == nil {
		t.Fatal("refuse world-readable export store")
	}
}
