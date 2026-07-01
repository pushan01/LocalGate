package toolaccess

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

var (
	ErrAuthValueRequired = errors.New("tool access credential required")
	ErrAuthValueNotFound = errors.New("tool access credential not found")
	ErrAuthValueExpired  = errors.New("tool access credential expired")
)

const HeaderName = "X-LocalGate-Tool-Access"

type Grant struct {
	WorkspaceID string    `json:"workspace_id"`
	AuthValue   string    `json:"auth_value"`
	ExpiresAt   time.Time `json:"expires_at"`
	IssuedAt    time.Time `json:"issued_at"`
}

type Service struct {
	mu     sync.Mutex
	grants map[string]Grant
	now    func() time.Time
}

func NewService() *Service {
	return &Service{
		grants: make(map[string]Grant),
		now:    func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) Issue(workspaceID string, ttl time.Duration) (Grant, error) {
	if s == nil {
		return Grant{}, errors.New("tool access service is nil")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return Grant{}, errors.New("workspace_id is required")
	}
	if ttl <= 0 {
		return Grant{}, errors.New("ttl must be positive")
	}
	authValue, err := newAuthValue()
	if err != nil {
		return Grant{}, err
	}
	now := s.now()
	grant := Grant{
		WorkspaceID: workspaceID,
		AuthValue:   authValue,
		IssuedAt:    now,
		ExpiresAt:   now.Add(ttl),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	s.grants[authValue] = grant
	return grant, nil
}

func (s *Service) Get(authValue string) (Grant, error) {
	if s == nil {
		return Grant{}, errors.New("tool access service is nil")
	}
	authValue = strings.TrimSpace(authValue)
	if authValue == "" {
		return Grant{}, ErrAuthValueRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLockedExcept(authValue)
	grant, ok := s.grants[authValue]
	if !ok {
		return Grant{}, ErrAuthValueNotFound
	}
	if !grant.ExpiresAt.After(s.now()) {
		delete(s.grants, authValue)
		return Grant{}, ErrAuthValueExpired
	}
	return grant, nil
}

func (s *Service) cleanupLocked() {
	now := s.now()
	for authValue, grant := range s.grants {
		if !grant.ExpiresAt.After(now) {
			delete(s.grants, authValue)
		}
	}
}

func (s *Service) cleanupLockedExcept(skip string) {
	now := s.now()
	for authValue, grant := range s.grants {
		if authValue == skip {
			continue
		}
		if !grant.ExpiresAt.After(now) {
			delete(s.grants, authValue)
		}
	}
}

func newAuthValue() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "lgta_" + hex.EncodeToString(buf), nil
}
