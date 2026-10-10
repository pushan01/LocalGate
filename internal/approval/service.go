package approval

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"localgate/internal/config"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusRejected Status = "rejected"
	StatusExpired  Status = "expired"
	StatusCanceled Status = "canceled"

	ExecutionAppTool    = "app_tool"
	ExecutionMCPRuntime = "mcp_runtime"

	redactedValue = "[REDACTED]"
)

type Request struct {
	ID              string         `json:"id"`
	TaskID          string         `json:"task_id,omitempty"`
	Tool            string         `json:"tool"`
	Requester       string         `json:"requester,omitempty"`
	Transport       string         `json:"transport,omitempty"`
	MCPSessionID    string         `json:"session_id,omitempty"`
	SessionMode     string         `json:"session_mode,omitempty"`
	SessionIDSource string         `json:"session_id_source,omitempty"`
	RequestID       string         `json:"request_id,omitempty"`
	Execution       string         `json:"execution,omitempty"`
	Reason          string         `json:"reason"`
	Summary         string         `json:"summary,omitempty"`
	RiskLevel       string         `json:"risk_level,omitempty"`
	Status          Status         `json:"status"`
	Args            map[string]any `json:"args,omitempty"`
	ArgsDigest      string         `json:"args_digest,omitempty"`
	ApprovalURL     string         `json:"approval_url,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	ExpiresAt       time.Time      `json:"expires_at,omitempty"`
	DecidedAt       time.Time      `json:"decided_at,omitempty"`
	Actor           string         `json:"actor,omitempty"`
	AuthMethod      string         `json:"auth_method,omitempty"`
}

type AuditRecord struct {
	ID              string         `json:"id"`
	ApprovalID      string         `json:"approval_id,omitempty"`
	OperationID     string         `json:"operation_id,omitempty"`
	TaskID          string         `json:"task_id,omitempty"`
	Action          string         `json:"action"`
	Actor           string         `json:"actor,omitempty"`
	AuthMethod      string         `json:"auth_method,omitempty"`
	Tool            string         `json:"tool"`
	Requester       string         `json:"requester,omitempty"`
	Transport       string         `json:"transport,omitempty"`
	MCPSessionID    string         `json:"session_id,omitempty"`
	SessionMode     string         `json:"session_mode,omitempty"`
	SessionIDSource string         `json:"session_id_source,omitempty"`
	RequestID       string         `json:"request_id,omitempty"`
	Reason          string         `json:"reason,omitempty"`
	Error           string         `json:"error,omitempty"`
	Details         map[string]any `json:"details,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
}

type Actor struct {
	ID         string
	AuthMethod string
}

type Binding struct {
	ApprovalID string
	TaskID     string
	Requester  string
	Tool       string
	ArgsDigest string
	TaskStatus string
}

type Store struct {
	Base string
	mu   sync.Mutex
}

func NewStore(base string) *Store {
	return &Store{Base: base}
}

func (s *Store) ensure() error {
	for _, p := range []string{
		filepath.Join(s.Base, "approvals"),
		filepath.Join(s.Base, "audit"),
	} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SaveRequest(req Request) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveRequestLocked(req)
}

func (s *Store) saveRequestLocked(req Request) error {
	if err := s.ensure(); err != nil {
		return err
	}
	fp := filepath.Join(s.Base, "approvals", req.ID+".json")
	b, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(fp), ".approval-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil && !errors.Is(err, syscall.ENOTTY) {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, fp)
}

func (s *Store) UpdateRequest(id string, update func(*Request) error) (Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fp := filepath.Join(s.Base, "approvals", id+".json")
	b, err := os.ReadFile(fp)
	if err != nil {
		return Request{}, err
	}
	var req Request
	if err := json.Unmarshal(b, &req); err != nil {
		return Request{}, err
	}
	if err := update(&req); err != nil {
		return req, err
	}
	if err := s.saveRequestLocked(req); err != nil {
		return Request{}, err
	}
	return req, nil
}

func (s *Store) ListRequests() ([]Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.Base, "approvals")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Request{}, nil
		}
		return nil, err
	}
	out := make([]Request, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var req Request
		if err := json.Unmarshal(b, &req); err != nil {
			return nil, err
		}
		out = append(out, req)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *Store) GetRequest(id string) (Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fp := filepath.Join(s.Base, "approvals", id+".json")
	b, err := os.ReadFile(fp)
	if err != nil {
		return Request{}, err
	}
	var req Request
	if err := json.Unmarshal(b, &req); err != nil {
		return Request{}, err
	}
	return req, nil
}

func (s *Store) AppendAudit(rec AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensure(); err != nil {
		return err
	}
	fp := filepath.Join(s.Base, "audit", "events.jsonl")
	f, err := os.OpenFile(fp, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	return enc.Encode(rec)
}

type Service struct {
	store        *Store
	baseURL      string
	approvalPath string
	ttl          time.Duration
	observerMu   sync.RWMutex
	observer     func(Request)
}

type Options struct {
	BaseURL      string
	ApprovalPath string
	TTL          time.Duration
}

func NewService(store *Store) *Service {
	return NewServiceWithOptions(store, Options{TTL: 10 * time.Minute})
}

func NewServiceWithOptions(store *Store, options Options) *Service {
	if options.TTL <= 0 {
		options.TTL = 10 * time.Minute
	}
	approvalPath := strings.TrimSpace(options.ApprovalPath)
	if approvalPath == "" {
		approvalPath = "/approval-ui/"
	}
	if !strings.HasPrefix(approvalPath, "/") {
		approvalPath = "/" + approvalPath
	}
	if !strings.HasSuffix(approvalPath, "/") {
		approvalPath += "/"
	}
	return &Service{
		store:        store,
		baseURL:      strings.TrimRight(strings.TrimSpace(options.BaseURL), "/"),
		approvalPath: approvalPath,
		ttl:          options.TTL,
	}
}

func (s *Service) Store() *Store {
	return s.store
}

func (s *Service) SetObserver(observer func(Request)) {
	s.observerMu.Lock()
	defer s.observerMu.Unlock()
	s.observer = observer
}

func (s *Service) notify(req Request) {
	s.observerMu.RLock()
	observer := s.observer
	s.observerMu.RUnlock()
	if observer != nil {
		observer(redactRequest(req))
	}
}

func (s *Service) RecordAudit(rec AuditRecord) (AuditRecord, error) {
	if s == nil || s.store == nil {
		return AuditRecord{}, errors.New("approval audit service is not initialized")
	}
	if rec.ID == "" {
		rec.ID = newID()
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	rec.Details = RedactMap(rec.Details)
	if err := s.store.AppendAudit(rec); err != nil {
		return AuditRecord{}, err
	}
	return rec, nil
}

func (s *Service) RequiresApproval(tool string, ws config.Root, args map[string]any) (bool, string) {
	if tool == "run_command" || tool == "run_argv" || tool == "exec_command" {
		cmd := strings.ToLower(strings.TrimSpace(firstString(args)))
		for _, p := range ws.ApprovalPolicy.Commands {
			if p == "" {
				continue
			}
			if strings.Contains(cmd, strings.ToLower(p)) {
				return true, "approval.policy.command"
			}
		}
	}
	if tool == "write_file" {
		p, _ := args["path"].(string)
		abs := filepath.Clean(p)
		for _, w := range ws.ApprovalPolicy.WritePaths {
			if within(abs, filepath.Clean(w)) {
				return true, "approval.policy.write_path"
			}
		}
	}
	return false, ""
}

func (s *Service) Create(req Request) (Request, error) {
	req.ID = newID()
	req.Status = StatusPending
	req.CreatedAt = time.Now().UTC()
	req.ExpiresAt = req.CreatedAt.Add(s.ttl)
	rawArgs := req.Args
	if rawArgs == nil {
		rawArgs = map[string]any{}
	}
	req.ArgsDigest = Digest(req.Tool, req.Requester, rawArgs)
	req.Args = RedactMap(rawArgs)
	if s.baseURL != "" {
		req.ApprovalURL = s.baseURL + s.approvalPath + req.ID
	}
	if err := s.store.SaveRequest(req); err != nil {
		return Request{}, err
	}
	if err := s.store.AppendAudit(AuditRecord{
		ID:         newID(),
		ApprovalID: req.ID,
		Action:     "created",
		Tool:       req.Tool,
		Requester:  req.Requester,
		Transport:  req.Transport, MCPSessionID: req.MCPSessionID,
		SessionMode: req.SessionMode, SessionIDSource: req.SessionIDSource, RequestID: req.RequestID,
		Reason:    req.Reason,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		return Request{}, err
	}
	return req, nil
}

func (s *Service) Get(id string) (Request, error) {
	req, err := s.store.GetRequest(id)
	if err != nil {
		return Request{}, err
	}
	return redactRequest(req), nil
}

func (s *Service) BindTask(id, taskID string) (Request, error) {
	if strings.TrimSpace(taskID) == "" {
		return Request{}, ErrTaskIDRequired
	}
	req, err := s.store.UpdateRequest(id, func(req *Request) error {
		if req.Status != StatusPending {
			return ErrApprovalResolved
		}
		if req.TaskID != "" && req.TaskID != taskID {
			return errors.New("approval task mismatch")
		}
		req.TaskID = taskID
		return nil
	})
	if err == nil {
		s.notify(req)
	}
	return req, err
}

var ErrApprovalNotFound = errors.New("approval not found")
var ErrApprovalResolved = errors.New("approval already resolved")
var ErrTaskIDRequired = errors.New("task id required")
var ErrApprovalExpired = errors.New("approval expired")
var ErrApprovalBindingMismatch = errors.New("approval binding mismatch")

func (s *Service) ApproveBound(id string, actor Actor, binding Binding) (Request, error) {
	expired := false
	req, err := s.store.UpdateRequest(id, func(req *Request) error {
		if req.Status != StatusPending {
			return ErrApprovalResolved
		}
		if !req.ExpiresAt.IsZero() && !time.Now().UTC().Before(req.ExpiresAt) {
			req.Status = StatusExpired
			req.Actor = "system"
			req.AuthMethod = "system"
			req.DecidedAt = time.Now().UTC()
			expired = true
			return nil
		}
		if binding.TaskID == "" {
			return ErrTaskIDRequired
		}
		if err := validateBinding(*req, binding); err != nil {
			return err
		}
		req.Status = StatusApproved
		req.TaskID = binding.TaskID
		req.Actor = normalizeActor(actor).ID
		req.AuthMethod = normalizeActor(actor).AuthMethod
		req.DecidedAt = time.Now().UTC()
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return Request{}, ErrApprovalNotFound
		}
		if errors.Is(err, ErrApprovalBindingMismatch) {
			_ = s.store.AppendAudit(AuditRecord{
				ID:         newID(),
				ApprovalID: req.ID,
				TaskID:     req.TaskID,
				Action:     "approval_validation_failed",
				Actor:      normalizeActor(actor).ID,
				AuthMethod: normalizeActor(actor).AuthMethod,
				Tool:       req.Tool,
				Requester:  req.Requester,
				Transport:  req.Transport, MCPSessionID: req.MCPSessionID,
				SessionMode: req.SessionMode, SessionIDSource: req.SessionIDSource, RequestID: req.RequestID,
				Reason: req.Reason,
				Error:  err.Error(),
				Details: map[string]any{
					"binding": map[string]any{
						"approval_id": binding.ApprovalID,
						"task_id":     binding.TaskID,
						"requester":   binding.Requester,
						"tool":        binding.Tool,
						"args_digest": binding.ArgsDigest,
						"task_status": binding.TaskStatus,
					},
				},
				CreatedAt: time.Now().UTC(),
			})
		}
		return Request{}, err
	}
	if expired {
		if err := s.appendDecisionAudit(req, "expired", Actor{ID: "system", AuthMethod: "system"}); err != nil {
			return Request{}, err
		}
		s.notify(req)
		return redactRequest(req), ErrApprovalExpired
	}
	if err := s.appendDecisionAudit(req, "approved", normalizeActor(actor)); err != nil {
		return Request{}, err
	}
	s.notify(req)
	return redactRequest(req), nil
}

func (s *Service) Reject(id, actor string) (Request, error) {
	return s.RejectBy(id, Actor{ID: actor})
}

func (s *Service) RejectBy(id string, actor Actor) (Request, error) {
	actor = normalizeActor(actor)
	req, err := s.store.UpdateRequest(id, func(req *Request) error {
		if req.Status != StatusPending {
			return ErrApprovalResolved
		}
		req.Status = StatusRejected
		req.Actor = actor.ID
		req.AuthMethod = actor.AuthMethod
		req.DecidedAt = time.Now().UTC()
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return Request{}, ErrApprovalNotFound
		}
		return Request{}, err
	}
	if err := s.appendDecisionAudit(req, "rejected", actor); err != nil {
		return Request{}, err
	}
	s.notify(req)
	return redactRequest(req), nil
}

func (s *Service) Cancel(id, actor string) (Request, error) {
	return s.CancelBy(id, Actor{ID: actor})
}

func (s *Service) CancelBy(id string, actor Actor) (Request, error) {
	actor = normalizeActor(actor)
	req, err := s.store.UpdateRequest(id, func(req *Request) error {
		if req.Status != StatusPending {
			return ErrApprovalResolved
		}
		req.Status = StatusCanceled
		req.Actor = actor.ID
		req.AuthMethod = actor.AuthMethod
		req.DecidedAt = time.Now().UTC()
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return Request{}, ErrApprovalNotFound
		}
		return Request{}, err
	}
	if err := s.appendDecisionAudit(req, "canceled", actor); err != nil {
		return Request{}, err
	}
	s.notify(req)
	return redactRequest(req), nil
}

func (s *Service) Expire(id string) (Request, error) {
	return s.expireWithActor(id, "system")
}

func (s *Service) expireWithActor(id, actor string) (Request, error) {
	resolvedActor := normalizeActor(Actor{ID: actor, AuthMethod: "system"})
	req, err := s.store.UpdateRequest(id, func(req *Request) error {
		if req.Status != StatusPending {
			return ErrApprovalResolved
		}
		req.Status = StatusExpired
		req.Actor = resolvedActor.ID
		req.AuthMethod = resolvedActor.AuthMethod
		req.DecidedAt = time.Now().UTC()
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return Request{}, ErrApprovalNotFound
		}
		return Request{}, err
	}
	if err := s.appendDecisionAudit(req, "expired", resolvedActor); err != nil {
		return Request{}, err
	}
	s.notify(req)
	return redactRequest(req), nil
}

func (s *Service) List(status string) ([]Request, error) {
	requests, err := s.store.ListRequests()
	if err != nil {
		return nil, err
	}
	status = strings.TrimSpace(status)
	if status == "" {
		return redactRequests(requests), nil
	}
	out := make([]Request, 0, len(requests))
	for _, req := range requests {
		if string(req.Status) == status {
			out = append(out, redactRequest(req))
		}
	}
	return out, nil
}

// ReconcilePendingOnStartup resolves approvals whose in-memory task gates were
// lost during a process restart. Expired requests remain expired; all other
// pending requests are canceled so they can never be released accidentally.
func (s *Service) ReconcilePendingOnStartup(actor string) ([]Request, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = "system:restart"
	}
	pending, err := s.store.ListRequests()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	resolved := make([]Request, 0)
	for _, req := range pending {
		if req.Status != StatusPending {
			continue
		}
		var updated Request
		if !req.ExpiresAt.IsZero() && !now.Before(req.ExpiresAt) {
			updated, err = s.expireWithActor(req.ID, actor)
		} else {
			updated, err = s.CancelBy(req.ID, Actor{ID: actor, AuthMethod: "system"})
		}
		if err != nil {
			return resolved, err
		}
		resolved = append(resolved, updated)
	}
	return resolved, nil
}

func (s *Service) appendDecisionAudit(req Request, action string, actor Actor) error {
	actor = normalizeActor(actor)
	return s.store.AppendAudit(AuditRecord{
		ID:         newID(),
		ApprovalID: req.ID,
		TaskID:     req.TaskID,
		Action:     action,
		Actor:      actor.ID,
		AuthMethod: actor.AuthMethod,
		Tool:       req.Tool,
		Requester:  req.Requester,
		Transport:  req.Transport, MCPSessionID: req.MCPSessionID,
		SessionMode: req.SessionMode, SessionIDSource: req.SessionIDSource, RequestID: req.RequestID,
		Reason:    req.Reason,
		CreatedAt: time.Now().UTC(),
	})
}

func normalizeActor(actor Actor) Actor {
	actor.ID = strings.TrimSpace(actor.ID)
	actor.AuthMethod = strings.TrimSpace(actor.AuthMethod)
	if actor.ID == "" {
		actor.ID = "local-unauthenticated"
	}
	if actor.AuthMethod == "" {
		actor.AuthMethod = "none"
	}
	return actor
}

func validateBinding(req Request, binding Binding) error {
	if binding.ApprovalID != req.ID {
		return fmt.Errorf("%w: approval_id", ErrApprovalBindingMismatch)
	}
	if req.TaskID == "" || binding.TaskID != req.TaskID {
		return fmt.Errorf("%w: task_id", ErrApprovalBindingMismatch)
	}
	if binding.Requester != req.Requester {
		return fmt.Errorf("%w: requester", ErrApprovalBindingMismatch)
	}
	if binding.Tool != req.Tool {
		return fmt.Errorf("%w: tool", ErrApprovalBindingMismatch)
	}
	if binding.ArgsDigest != req.ArgsDigest {
		return fmt.Errorf("%w: args_digest", ErrApprovalBindingMismatch)
	}
	if binding.TaskStatus != "awaiting_approval" {
		return fmt.Errorf("%w: task_status", ErrApprovalBindingMismatch)
	}
	return nil
}

// RedactMap returns a deep, JSON-compatible copy with sensitive values hidden.
// It is intentionally exported so HTTP presentation code can also sanitize
// legacy records that may have been written before storage redaction existed.
func RedactMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		if isSensitiveKey(key) {
			out[key] = redactedValue
			continue
		}
		out[key] = RedactValue(value)
	}
	return out
}

func RedactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return RedactMap(typed)
	case map[string]string:
		out := make(map[string]string, len(typed))
		for key, item := range typed {
			if isSensitiveKey(key) {
				out[key] = redactedValue
			} else {
				out[key] = item
			}
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = RedactValue(item)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(typed))
		for i, item := range typed {
			out[i] = RedactMap(item)
		}
		return out
	default:
		return value
	}
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(normalized)
	switch normalized {
	case "authorization", "proxyauthorization", "cookie", "setcookie",
		"password", "passwd", "passphrase", "secret", "clientsecret",
		"privatekey", "apikey", "token", "accesstoken", "authtoken",
		"bearertoken", "refreshtoken", "sessiontoken", "credential", "credentials",
		"handoffcontext":
		return true
	}
	return strings.HasSuffix(normalized, "password") ||
		strings.HasSuffix(normalized, "secret") ||
		strings.HasSuffix(normalized, "privatekey") ||
		strings.HasSuffix(normalized, "authorization") ||
		strings.HasSuffix(normalized, "cookie") ||
		(strings.HasSuffix(normalized, "token") && !strings.HasSuffix(normalized, "tokens"))
}

func redactRequest(req Request) Request {
	req.Args = RedactMap(req.Args)
	return req
}

func redactRequests(requests []Request) []Request {
	out := make([]Request, len(requests))
	for i, req := range requests {
		out[i] = redactRequest(req)
	}
	return out
}

func Digest(tool, requester string, args map[string]any) string {
	payload, _ := json.Marshal(map[string]any{
		"tool":      tool,
		"requester": requester,
		"args":      args,
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func ValidateBaseURL(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("approval base url is required")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("invalid approval base url: %q", value)
	}
	return nil
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func firstString(args map[string]any) string {
	if v, ok := args["command"].(string); ok {
		return v
	}
	if a, ok := args["argv"].([]string); ok && len(a) > 0 {
		return a[0]
	}
	if a, ok := args["argv"].([]any); ok && len(a) > 0 {
		if s, ok := a[0].(string); ok {
			return s
		}
	}
	return ""
}

func within(p, root string) bool {
	p = filepath.Clean(p)
	root = filepath.Clean(root)
	if !strings.HasSuffix(root, string(os.PathSeparator)) {
		root = root + string(os.PathSeparator)
	}
	if !strings.HasSuffix(p, string(os.PathSeparator)) {
		p = p + string(os.PathSeparator)
	}
	return strings.HasPrefix(p, root)
}

func (s *Service) QueryAudit(filter map[string]string, limit, offset int) ([]AuditRecord, error) {
	fp := filepath.Join(s.store.Base, "audit", "events.jsonl")
	f, err := os.Open(fp)
	if err != nil {
		if os.IsNotExist(err) {
			return []AuditRecord{}, nil
		}
		return nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	out := []AuditRecord{}
	count := 0
	for {
		var rec AuditRecord
		if err := dec.Decode(&rec); err != nil {
			break
		}
		if matchAudit(rec, filter) {
			count++
			if count <= offset {
				continue
			}
			rec.Details = RedactMap(rec.Details)
			out = append(out, rec)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func matchAudit(rec AuditRecord, filter map[string]string) bool {
	if v := filter["approval_id"]; v != "" && rec.ApprovalID != v {
		return false
	}
	if v := filter["task_id"]; v != "" && rec.TaskID != v {
		return false
	}
	if v := filter["requester"]; v != "" && rec.Requester != v {
		return false
	}
	if v := filter["tool"]; v != "" && rec.Tool != v {
		return false
	}
	if v := filter["action"]; v != "" && rec.Action != v {
		return false
	}
	if v := filter["operation_id"]; v != "" && rec.OperationID != v {
		return false
	}
	if v := filter["session_id"]; v != "" && rec.MCPSessionID != v {
		return false
	}
	if v := filter["transport"]; v != "" && rec.Transport != v {
		return false
	}
	return true
}
