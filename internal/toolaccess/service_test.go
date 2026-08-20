package toolaccess

import (
	"errors"
	"testing"
	"time"
)

func TestIssueAndGet(t *testing.T) {
	svc := NewService()
	grant, err := svc.Issue(time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if grant.AuthValue == "" {
		t.Fatalf("expected auth value")
	}
	got, err := svc.Get(grant.AuthValue)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AuthValue != grant.AuthValue {
		t.Fatalf("unexpected grant: %#v", got)
	}
}

func TestGetExpired(t *testing.T) {
	svc := NewService()
	grant, err := svc.Issue(time.Millisecond)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	_, err = svc.Get(grant.AuthValue)
	if !errors.Is(err, ErrAuthValueExpired) {
		t.Fatalf("expected expired error, got %v", err)
	}
}

func TestGetRequiresAuthValue(t *testing.T) {
	svc := NewService()
	_, err := svc.Get("")
	if !errors.Is(err, ErrAuthValueRequired) {
		t.Fatalf("expected required error, got %v", err)
	}
}
