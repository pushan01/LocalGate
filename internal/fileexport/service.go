package fileexport

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

var (
	ErrExportIDRequired  = errors.New("export id required")
	ErrExportNotFound    = errors.New("export not found")
	ErrSignatureRequired = errors.New("export signature required")
	ErrInvalidSignature  = errors.New("invalid export signature")
	ErrExportExpired     = errors.New("export expired")
)

type Options struct {
	BaseURL    string
	DefaultTTL time.Duration
	MaxTTL     time.Duration
	Now        func() time.Time
}

type Grant struct {
	ID              string    `json:"id"`
	Path            string    `json:"path"`
	FileName        string    `json:"file_name"`
	MIMEType        string    `json:"mime_type"`
	Size            int64     `json:"size"`
	SHA256          string    `json:"sha256"`
	ModTimeUnixNano int64     `json:"mod_time_unix_nano"`
	InternalSource  bool      `json:"internal_source,omitempty"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type Link struct {
	URI         string
	Name        string
	Description string
	MIMEType    string
	Size        int64
}

type Service struct {
	mu         sync.Mutex
	baseURL    string
	defaultTTL time.Duration
	maxTTL     time.Duration
	key        [32]byte
	grants     map[string]Grant
	now        func() time.Time
}

// MaxInlineImageBytes bounds MCP image content. Larger images still have a
// resource_link and can be downloaded without sending base64 to the model.
const MaxInlineImageBytes int64 = 1 << 20

// ImagePreview returns an MCP-ready image only after revalidating the exact
// bytes behind a live export grant. It deliberately does not expose local paths
// and never materializes arbitrary files or SVG/HTML in an image content block.
func (s *Service) ImagePreview(id string) (mimeType, base64Data string, ok bool) {
	grant, err := s.get(id)
	if err != nil || grant.Size <= 0 || grant.Size > MaxInlineImageBytes {
		return "", "", false
	}
	mimeType = strings.ToLower(strings.TrimSpace(strings.SplitN(grant.MIMEType, ";", 2)[0]))
	switch mimeType {
	case "image/png", "image/jpeg", "image/webp":
	default:
		return "", "", false
	}
	info, err := os.Lstat(grant.Path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != grant.Size || info.ModTime().UnixNano() != grant.ModTimeUnixNano {
		return "", "", false
	}
	f, err := os.Open(grant.Path)
	if err != nil {
		return "", "", false
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() != grant.Size || before.ModTime().UnixNano() != grant.ModTimeUnixNano {
		return "", "", false
	}
	payload, err := io.ReadAll(io.LimitReader(f, MaxInlineImageBytes+1))
	if err != nil || int64(len(payload)) != grant.Size {
		return "", "", false
	}
	after, err := f.Stat()
	if err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return "", "", false
	}
	if http.DetectContentType(payload) != mimeType {
		return "", "", false
	}
	digest := sha256.Sum256(payload)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), grant.SHA256) {
		return "", "", false
	}
	return mimeType, base64.StdEncoding.EncodeToString(payload), true
}

func NewService(opts Options) (*Service, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("file export base_url must be a valid http or https URL")
	}
	if opts.DefaultTTL <= 0 {
		return nil, errors.New("file export default TTL must be positive")
	}
	if opts.MaxTTL < opts.DefaultTTL {
		return nil, errors.New("file export max TTL must be at least the default TTL")
	}
	s := &Service{
		baseURL:    baseURL,
		defaultTTL: opts.DefaultTTL,
		maxTTL:     opts.MaxTTL,
		grants:     make(map[string]Grant),
		now:        opts.Now,
	}
	if s.now == nil {
		s.now = func() time.Time { return time.Now().UTC() }
	}
	if _, err := rand.Read(s.key[:]); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Service) Issue(grant Grant, ttl time.Duration) (Grant, error) {
	if s == nil {
		return Grant{}, errors.New("file export service is nil")
	}
	grant.Path = strings.TrimSpace(grant.Path)
	grant.FileName = strings.TrimSpace(grant.FileName)
	grant.MIMEType = strings.TrimSpace(grant.MIMEType)
	grant.SHA256 = strings.ToLower(strings.TrimSpace(grant.SHA256))
	if grant.Path == "" || grant.FileName == "" {
		return Grant{}, errors.New("path and file name are required")
	}
	if grant.Size < 0 {
		return Grant{}, errors.New("file size must not be negative")
	}
	if len(grant.SHA256) != sha256.Size*2 {
		return Grant{}, errors.New("sha256 is required")
	}
	if _, err := hex.DecodeString(grant.SHA256); err != nil {
		return Grant{}, errors.New("sha256 is invalid")
	}
	if ttl == 0 {
		ttl = s.defaultTTL
	}
	if ttl <= 0 || ttl > s.maxTTL {
		return Grant{}, errors.New("file export TTL is outside the allowed range")
	}
	id, err := newID()
	if err != nil {
		return Grant{}, err
	}
	grant.ID = id
	grant.ExpiresAt = s.now().Add(ttl)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	s.grants[id] = grant
	return grant, nil
}

func (s *Service) Link(id string) (Link, error) {
	grant, err := s.get(id)
	if err != nil {
		return Link{}, err
	}
	signature := s.signature(grant.ID)
	q := url.Values{}
	q.Set("signature", signature)
	return Link{
		URI:         s.baseURL + path.Clean("/system/exports/"+url.PathEscape(grant.ID)) + "?" + q.Encode(),
		Name:        grant.FileName,
		Description: "Exported from LocalGate",
		MIMEType:    grant.MIMEType,
		Size:        grant.Size,
	}, nil
}

func (s *Service) Authorize(id, signature string) (Grant, error) {
	if strings.TrimSpace(signature) == "" {
		return Grant{}, ErrSignatureRequired
	}
	grant, err := s.get(id)
	if err != nil {
		return Grant{}, err
	}
	want, err := base64.RawURLEncoding.DecodeString(s.signature(grant.ID))
	if err != nil {
		return Grant{}, ErrInvalidSignature
	}
	got, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil || !hmac.Equal(got, want) {
		return Grant{}, ErrInvalidSignature
	}
	return grant, nil
}

func (s *Service) Revoke(id string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.grants, strings.TrimSpace(id))
	s.mu.Unlock()
}

func (s *Service) get(id string) (Grant, error) {
	if s == nil {
		return Grant{}, errors.New("file export service is nil")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Grant{}, ErrExportIDRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLockedExcept(id)
	grant, ok := s.grants[id]
	if !ok {
		return Grant{}, ErrExportNotFound
	}
	if !grant.ExpiresAt.After(s.now()) {
		delete(s.grants, id)
		return Grant{}, ErrExportExpired
	}
	return grant, nil
}

func (s *Service) signature(id string) string {
	mac := hmac.New(sha256.New, s.key[:])
	_, _ = mac.Write([]byte(id))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Service) cleanupLocked() {
	now := s.now()
	for id, grant := range s.grants {
		if !grant.ExpiresAt.After(now) {
			delete(s.grants, id)
		}
	}
}

func (s *Service) cleanupLockedExcept(skip string) {
	now := s.now()
	for id, grant := range s.grants {
		if id == skip {
			continue
		}
		if !grant.ExpiresAt.After(now) {
			delete(s.grants, id)
		}
	}
}

func newID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "lgexp_" + hex.EncodeToString(buf), nil
}
