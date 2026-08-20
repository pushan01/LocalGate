package download

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

var (
	ErrTokenRequired = errors.New("download token required")
	ErrTokenNotFound = errors.New("download token not found")
	ErrTokenExpired  = errors.New("download token expired")
)

type Grant struct {
	Token     string    `json:"token"`
	Path      string    `json:"path"`
	ExpiresAt time.Time `json:"expires_at"`
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

func (s *Service) Issue(path string, ttl time.Duration) (Grant, error) {
	if s == nil {
		return Grant{}, errors.New("download service is nil")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return Grant{}, errors.New("path required")
	}
	if ttl <= 0 {
		return Grant{}, errors.New("ttl must be positive")
	}
	token, err := newToken()
	if err != nil {
		return Grant{}, err
	}
	grant := Grant{
		Token:     token,
		Path:      path,
		ExpiresAt: s.now().Add(ttl),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	s.grants[token] = grant
	return grant, nil
}

func (s *Service) Get(token string) (Grant, error) {
	if s == nil {
		return Grant{}, errors.New("download service is nil")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return Grant{}, ErrTokenRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLockedExcept(token)
	grant, ok := s.grants[token]
	if !ok {
		return Grant{}, ErrTokenNotFound
	}
	if !grant.ExpiresAt.After(s.now()) {
		delete(s.grants, token)
		return Grant{}, ErrTokenExpired
	}
	return grant, nil
}

func (s *Service) cleanupLocked() {
	now := s.now()
	for token, grant := range s.grants {
		if !grant.ExpiresAt.After(now) {
			delete(s.grants, token)
		}
	}
}

func (s *Service) cleanupLockedExcept(skipToken string) {
	now := s.now()
	for token, grant := range s.grants {
		if token == skipToken {
			continue
		}
		if !grant.ExpiresAt.After(now) {
			delete(s.grants, token)
		}
	}
}

func newToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "lgdl_" + hex.EncodeToString(buf), nil
}
