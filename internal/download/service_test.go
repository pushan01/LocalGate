package download

import (
	"errors"
	"testing"
	"time"
)

func TestIssueAndGet(t *testing.T) {
	base := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	svc := NewService()
	svc.now = func() time.Time { return base }

	grant, err := svc.Issue("ws", "/tmp/target.bin", 10*time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if grant.Token == "" {
		t.Fatalf("expected token")
	}
	if grant.WorkspaceID != "ws" || grant.Path != "/tmp/target.bin" {
		t.Fatalf("unexpected grant: %#v", grant)
	}

	got, err := svc.Get(grant.Token)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Token != grant.Token {
		t.Fatalf("unexpected token: %#v", got)
	}
}

func TestGetRejectsExpiredToken(t *testing.T) {
	base := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	svc := NewService()
	svc.now = func() time.Time { return base }

	grant, err := svc.Issue("ws", "/tmp/target.bin", time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	svc.now = func() time.Time { return base.Add(2 * time.Minute) }

	_, err = svc.Get(grant.Token)
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expected expired token error, got: %v", err)
	}
}

func TestGetAllowsRepeatedUseBeforeExpiry(t *testing.T) {
	base := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	svc := NewService()
	svc.now = func() time.Time { return base }

	grant, err := svc.Issue("ws", "/tmp/target.bin", 10*time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	for i := 0; i < 2; i++ {
		got, err := svc.Get(grant.Token)
		if err != nil {
			t.Fatalf("get #%d: %v", i+1, err)
		}
		if got.Path != "/tmp/target.bin" {
			t.Fatalf("unexpected grant on reuse: %#v", got)
		}
	}
}
