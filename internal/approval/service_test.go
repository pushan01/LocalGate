package approval

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"localgate/internal/config"
)

func TestApprovalApproveFlow(t *testing.T) {
	store := NewStore(t.TempDir())
	svc := NewService(store)
	req, err := svc.Create(Request{
		Tool:      "run_command",
		Requester: "test",
		Reason:    "command matched approval policy",
		Args:      map[string]any{"command": "rm file.txt"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.BindTask(req.ID, "task-1"); err != nil {
		t.Fatalf("bind task: %v", err)
	}
	got, err := svc.ApproveBound(req.ID, Actor{ID: "alice", AuthMethod: "bearer"}, Binding{
		ApprovalID: req.ID,
		TaskID:     "task-1",
		Requester:  req.Requester,
		Tool:       req.Tool,
		ArgsDigest: req.ArgsDigest,
		TaskStatus: "awaiting_approval",
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if got.TaskID != "task-1" || got.Status != StatusApproved || got.Actor != "alice" || got.AuthMethod != "bearer" {
		t.Fatalf("unexpected approval result: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(store.Base, "audit", "events.jsonl")); err != nil {
		t.Fatalf("audit log missing: %v", err)
	}
}

func TestApproveBoundRejectsBindingMismatches(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Binding)
	}{
		{name: "approval id", mutate: func(binding *Binding) { binding.ApprovalID = "other" }},
		{name: "task id", mutate: func(binding *Binding) { binding.TaskID = "other" }},
		{name: "requester", mutate: func(binding *Binding) { binding.Requester = "other" }},
		{name: "tool", mutate: func(binding *Binding) { binding.Tool = "write_file" }},
		{name: "args digest", mutate: func(binding *Binding) { binding.ArgsDigest = "other" }},
		{name: "task status", mutate: func(binding *Binding) { binding.TaskStatus = "running" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := NewService(NewStore(t.TempDir()))
			req, err := svc.Create(Request{
				Tool:      "run_command",
				Requester: "requester",
				Reason:    "test",
				Args:      map[string]any{"command": "echo ok"},
			})
			if err != nil {
				t.Fatalf("create approval: %v", err)
			}
			if _, err := svc.BindTask(req.ID, "task-1"); err != nil {
				t.Fatalf("bind task: %v", err)
			}
			binding := Binding{
				ApprovalID: req.ID,
				TaskID:     "task-1",
				Requester:  req.Requester,
				Tool:       req.Tool,
				ArgsDigest: req.ArgsDigest,
				TaskStatus: "awaiting_approval",
			}
			test.mutate(&binding)
			if _, err := svc.ApproveBound(req.ID, Actor{ID: "reviewer", AuthMethod: "bearer"}, binding); !errors.Is(err, ErrApprovalBindingMismatch) {
				t.Fatalf("expected binding mismatch, got %v", err)
			}
			stored, err := svc.Get(req.ID)
			if err != nil {
				t.Fatalf("get approval: %v", err)
			}
			if stored.Status != StatusPending {
				t.Fatalf("binding mismatch changed approval state: %#v", stored)
			}
			records, err := svc.QueryAudit(map[string]string{"approval_id": req.ID, "action": "approval_validation_failed"}, 10, 0)
			if err != nil {
				t.Fatalf("query validation audit: %v", err)
			}
			if len(records) != 1 || records[0].Actor != "reviewer" || records[0].AuthMethod != "bearer" {
				t.Fatalf("unexpected validation audit: %#v", records)
			}
		})
	}
}

func TestApprovalRejectFlow(t *testing.T) {
	store := NewStore(t.TempDir())
	svc := NewService(store)
	req, err := svc.Create(Request{
		Tool:      "run_command",
		Requester: "test",
		Reason:    "command matched approval policy",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := svc.Reject(req.ID, "bob")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if got.Status != StatusRejected || got.Actor != "bob" {
		t.Fatalf("unexpected reject result: %+v", got)
	}
}

func TestRequiresApprovalPolicyFromConfig(t *testing.T) {
	store := NewStore(t.TempDir())
	svc := NewService(store)
	ws := config.Root{ApprovalPolicy: config.ApprovalPolicy{
		Commands:   []string{"rm", "curl"},
		WritePaths: []string{"/secret"},
	},
	}
	need, reason := svc.RequiresApproval("run_command", ws, map[string]any{"command": "rm -rf /"})
	if !need || reason == "" {
		t.Fatalf("expected command policy approval")
	}
	need2, _ := svc.RequiresApproval("write_file", ws, map[string]any{"path": "/secret/a.txt"})
	if !need2 {
		t.Fatalf("expected write path approval")
	}
}

func TestApprovalRedactsSensitiveArgsAndAuditDetails(t *testing.T) {
	store := NewStore(t.TempDir())
	svc := NewService(store)
	req, err := svc.Create(Request{
		Tool:      "run_command",
		Requester: "test",
		Reason:    "test",
		Args: map[string]any{
			"command": "echo ok",
			"token":   "top-secret-token",
			"nested": map[string]any{
				"password":          "top-secret-password",
				"max_output_tokens": 100,
				"headers": map[string]any{
					"Authorization": "Bearer top-secret-auth",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	if req.Args["token"] != redactedValue {
		t.Fatalf("token was not redacted: %#v", req.Args)
	}
	nested, _ := req.Args["nested"].(map[string]any)
	if nested["password"] != redactedValue || nested["max_output_tokens"] != 100 {
		t.Fatalf("unexpected nested redaction: %#v", nested)
	}

	stored, err := os.ReadFile(filepath.Join(store.Base, "approvals", req.ID+".json"))
	if err != nil {
		t.Fatalf("read stored approval: %v", err)
	}
	for _, secret := range []string{"top-secret-token", "top-secret-password", "top-secret-auth"} {
		if strings.Contains(string(stored), secret) {
			t.Fatalf("secret %q was persisted: %s", secret, stored)
		}
	}

	if _, err := svc.RecordAudit(AuditRecord{
		Action: "execution_started",
		Tool:   "run_command",
		Details: map[string]any{
			"cookie": "top-secret-cookie",
			"safe":   "visible",
		},
	}); err != nil {
		t.Fatalf("record audit: %v", err)
	}
	records, err := svc.QueryAudit(map[string]string{"action": "execution_started"}, 10, 0)
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if len(records) != 1 || records[0].Details["cookie"] != redactedValue || records[0].Details["safe"] != "visible" {
		t.Fatalf("unexpected audit redaction: %#v", records)
	}

	rawAudit, err := os.ReadFile(filepath.Join(store.Base, "audit", "events.jsonl"))
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if strings.Contains(string(rawAudit), "top-secret-cookie") {
		t.Fatalf("audit secret was persisted: %s", rawAudit)
	}
	var parsed map[string]any
	lastLine := strings.TrimSpace(string(rawAudit[strings.LastIndex(string(rawAudit[:len(rawAudit)-1]), "\n")+1:]))
	if err := json.Unmarshal([]byte(lastLine), &parsed); err != nil {
		t.Fatalf("decode audit line: %v", err)
	}
}

func TestCancelExpireAndStartupReconcileWriteAudit(t *testing.T) {
	store := NewStore(t.TempDir())
	svc := NewServiceWithOptions(store, Options{TTL: time.Minute})

	cancelReq, err := svc.Create(Request{Tool: "write_file", Reason: "cancel"})
	if err != nil {
		t.Fatalf("create cancel approval: %v", err)
	}
	if _, err := svc.Cancel(cancelReq.ID, "alice"); err != nil {
		t.Fatalf("cancel approval: %v", err)
	}

	expireReq, err := svc.Create(Request{Tool: "write_file", Reason: "expire"})
	if err != nil {
		t.Fatalf("create expire approval: %v", err)
	}
	if _, err := svc.Expire(expireReq.ID); err != nil {
		t.Fatalf("expire approval: %v", err)
	}

	restartReq, err := svc.Create(Request{Tool: "run_command", Reason: "restart"})
	if err != nil {
		t.Fatalf("create restart approval: %v", err)
	}
	resolved, err := svc.ReconcilePendingOnStartup("system:restart")
	if err != nil {
		t.Fatalf("reconcile pending: %v", err)
	}
	if len(resolved) != 1 || resolved[0].ID != restartReq.ID || resolved[0].Status != StatusCanceled {
		t.Fatalf("unexpected reconciled approvals: %#v", resolved)
	}

	records, err := svc.QueryAudit(nil, 100, 0)
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	actions := map[string]int{}
	for _, record := range records {
		actions[record.Action]++
	}
	if actions["canceled"] != 2 || actions["expired"] != 1 {
		t.Fatalf("missing terminal audit events: %#v", actions)
	}
}

func TestApprovalObserverReceivesBoundAndResolvedRequests(t *testing.T) {
	svc := NewService(NewStore(t.TempDir()))
	observed := make([]Status, 0, 2)
	svc.SetObserver(func(req Request) {
		observed = append(observed, req.Status)
	})
	req, err := svc.Create(Request{Tool: "run_command", Requester: "tester"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.BindTask(req.ID, "task-1"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if _, err := svc.RejectBy(req.ID, Actor{ID: "reviewer", AuthMethod: "test"}); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if len(observed) != 2 || observed[0] != StatusPending || observed[1] != StatusRejected {
		t.Fatalf("unexpected observed statuses: %#v", observed)
	}
}

func TestApprovalRedactsConversationHandoffContextButKeepsDigestBinding(t *testing.T) {
	store := NewStore(t.TempDir())
	svc := NewService(store)
	args := map[string]any{
		"handoff_context": "sensitive compacted project context",
		"project":         "deadbeef",
	}
	req, err := svc.Create(Request{
		Tool:      "conversation_handoff",
		Requester: "test",
		Reason:    "test",
		Args:      args,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if req.Args["handoff_context"] != redactedValue {
		t.Fatalf("handoff context was not redacted: %#v", req.Args)
	}
	if req.Args["project"] != "deadbeef" {
		t.Fatalf("non-sensitive handoff metadata was unexpectedly redacted: %#v", req.Args)
	}
	if want := Digest("conversation_handoff", "test", args); req.ArgsDigest != want {
		t.Fatalf("args digest = %q, want %q", req.ArgsDigest, want)
	}
	stored, err := os.ReadFile(filepath.Join(store.Base, "approvals", req.ID+".json"))
	if err != nil {
		t.Fatalf("read stored approval: %v", err)
	}
	if strings.Contains(string(stored), "sensitive compacted project context") {
		t.Fatalf("stored approval leaked handoff context: %s", stored)
	}
}
