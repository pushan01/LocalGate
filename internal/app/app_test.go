package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"localgate/internal/approval"
	"localgate/internal/config"
	"localgate/internal/core"
	"localgate/internal/deployment"
	"localgate/internal/dynamic"
	"localgate/internal/mcprelay"
	"localgate/internal/persist"
)

func TestAppDynamicHelperProcess(t *testing.T) {
	if os.Getenv("LOCALGATE_APP_DYNAMIC_HELPER") != "1" {
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	if err := http.ListenAndServe(os.Getenv("HOST")+":"+os.Getenv("PORT"), mux); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func TestDynamicLifecycleEventsAreAudited(t *testing.T) {
	workspaceRoot := t.TempDir()
	source := filepath.Join(workspaceRoot, "app")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "README.txt"), []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.Approval.Enabled = false
	cfg.Deployments.Dynamic.Enabled = true
	cfg.HTTP.BaseURL = "https://control.test"
	cfg.Deployments.PublicBaseURL = "https://apps.test"
	cfg.Deployments.PrivateBaseURL = "https://private.apps.test"
	cfg.Deployments.DefaultAccess = "public"
	cfg.Deployments.Dynamic.AllowedExecutables = []string{executable}
	cfg.Deployments.Dynamic.EnvAllowlist = []string{"LOCALGATE_APP_DYNAMIC_HELPER"}
	cfg.Deployments.Dynamic.RestartPolicy = "never"
	cfg.Workspaces = []config.Workspace{{ID: "ws", Roots: []string{workspaceRoot}, CWD: workspaceRoot}}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("init app: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	ctx := dynamic.WithOperation(context.Background(), dynamic.Operation{TaskID: "task-audit", Requester: "tester", Tool: "deploy_tool"})
	_, err = a.DeploymentManager.Deploy(ctx, deployment.DeployRequest{
		Type: deployment.TypeDynamic, Name: "audit-demo", Source: source,
		Workspace: cfg.Workspaces[0],
		Runtime:   &dynamic.RuntimeSpec{Argv: []string{executable, "-test.run=^TestAppDynamicHelperProcess$"}, Env: map[string]string{"LOCALGATE_APP_DYNAMIC_HELPER": "1"}},
		Options:   deployment.DeployOptions{PreserveSource: true, HealthPath: "/healthz", RestartPolicy: "never"},
	})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	wanted := map[string]bool{
		"deployment_staging_started": false,
		"deployment_process_started": false,
		"deployment_health_passed":   false,
		"deployment_activated":       false,
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		records, queryErr := a.Approvals.QueryAudit(map[string]string{"task_id": "task-audit"}, 100, 0)
		if queryErr != nil {
			t.Fatalf("query audit: %v", queryErr)
		}
		for _, record := range records {
			if _, ok := wanted[record.Action]; ok {
				wanted[record.Action] = true
			}
		}
		complete := true
		for _, found := range wanted {
			complete = complete && found
		}
		if complete {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("missing lifecycle audit events: %#v records=%#v", wanted, records)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestGlobalApprovalSwitchBypassesDeployApproval(t *testing.T) {
	workspaceRoot := t.TempDir()
	source := filepath.Join(workspaceRoot, "site")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.Approval.Enabled = false
	cfg.Deployments.Static.RequireApproval = true
	cfg.Deployments.PublicBaseURL = "https://apps.test"
	cfg.Deployments.PrivateBaseURL = "https://private.apps.test"
	cfg.Deployments.DefaultAccess = "public"
	cfg.HTTP.BaseURL = "https://control.test"
	cfg.Workspaces = []config.Workspace{{
		ID:    "ws",
		Roots: []string{workspaceRoot},
		CWD:   workspaceRoot,
	}}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("init app: %v", err)
	}
	defer a.Close()

	submission, err := a.SubmitToolTask("ws", "test", "deploy_tool", map[string]any{
		"type":   "static",
		"name":   "global-switch-off",
		"source": source,
	})
	if err != nil {
		t.Fatalf("submit deployment: %v", err)
	}
	if submission.Approval != nil {
		t.Fatalf("global approval switch should bypass approval: %#v", submission.Approval)
	}
	if submission.Task == nil {
		t.Fatal("expected deployment task")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		task, getErr := a.Tasks.Get(submission.Task.ID)
		if getErr != nil {
			t.Fatalf("get task: %v", getErr)
		}
		if task.Status == core.StatusSucceeded {
			return
		}
		if task.Status == core.StatusFailed || task.Status == core.StatusCanceled || task.Status == core.StatusTimeout {
			t.Fatalf("deployment task failed with approval disabled: %+v", task)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("deployment task did not finish")
}

func TestDeleteDeploymentRequiresApproval(t *testing.T) {
	workspaceRoot := t.TempDir()
	source := filepath.Join(workspaceRoot, "site")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("delete-me"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.Approval.Enabled = true
	cfg.Deployments.Static.RequireApproval = true
	cfg.Deployments.PublicBaseURL = "https://apps.test"
	cfg.Deployments.PrivateBaseURL = "https://private.apps.test"
	cfg.Deployments.DefaultAccess = "public"
	cfg.HTTP.BaseURL = "https://control.test"
	cfg.Workspaces = []config.Workspace{{
		ID:    "ws",
		Roots: []string{workspaceRoot},
		CWD:   workspaceRoot,
	}}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("init app: %v", err)
	}
	defer a.Close()
	if _, err := a.StaticService.DeploySite(context.Background(), "delete-me", source, false); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}

	submission, err := a.SubmitToolTask("ws", "test", "delete_deployment", map[string]any{
		"type": "static",
		"name": "delete-me",
	})
	if err != nil {
		t.Fatalf("submit deletion: %v", err)
	}
	if submission.Task == nil || submission.Approval == nil {
		t.Fatalf("expected pending approval, got %#v", submission)
	}
	if submission.Task.Status != core.StatusAwaitingApproval {
		t.Fatalf("unexpected gated task status: %#v", submission.Task)
	}
	if submission.Approval.Reason != "approval.policy.static_delete" {
		t.Fatalf("unexpected approval reason: %#v", submission.Approval)
	}
	if _, exists := a.StaticService.GetSite("delete-me"); !exists {
		t.Fatal("deployment must remain before approval")
	}
}

func TestDynamicDeploymentRequiresCriticalApproval(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Deployments.Static.RequireApproval = false
	cfg.Deployments.Dynamic.Enabled = true
	cfg.HTTP.BaseURL = "https://control.example.test"
	cfg.Deployments.PublicBaseURL = "https://apps.example.test"
	cfg.Deployments.PrivateBaseURL = "https://private.apps.example.test"
	cfg.Deployments.DefaultAccess = "public"
	cfg.Deployments.Dynamic.AllowedExecutables = []string{"node"}
	cfg.Workspaces = []config.Workspace{{ID: "ws", Roots: []string{workspaceRoot}, CWD: workspaceRoot}}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("init app: %v", err)
	}
	defer a.Close()

	submission, err := a.SubmitToolTask("ws", "test", "deploy_tool", map[string]any{
		"type": "dynamic", "name": "demo", "source": workspaceRoot,
		"runtime": map[string]any{"argv": []any{"node", "server.js"}},
	})
	if err != nil {
		t.Fatalf("submit dynamic deployment: %v", err)
	}
	if submission.Approval == nil || submission.Task == nil {
		t.Fatalf("expected dynamic deployment approval: %#v", submission)
	}
	if submission.Approval.RiskLevel != "critical" || submission.Approval.Reason != "approval.policy.dynamic_deploy_tool" {
		t.Fatalf("unexpected dynamic approval: %#v", submission.Approval)
	}
	if submission.Task.Status != core.StatusAwaitingApproval {
		t.Fatalf("dynamic process must not start before approval: %#v", submission.Task)
	}
	_ = a.CancelApprovalTask(submission.Approval.ID, submission.Task.ID)
}

func TestApprovalTerminalReasonsPropagateToTask(t *testing.T) {
	tests := []struct {
		name       string
		resolve    func(*App, approval.Request) error
		wantStatus approval.Status
		wantCode   string
	}{
		{
			name: "rejected",
			resolve: func(a *App, req approval.Request) error {
				resolved, err := a.Approvals.RejectBy(req.ID, approval.Actor{ID: "reviewer", AuthMethod: "bearer"})
				if err != nil {
					return err
				}
				return a.CancelApprovalTaskWithReason(req.ID, resolved.TaskID, "approval_rejected", "approval was rejected before execution")
			},
			wantStatus: approval.StatusRejected,
			wantCode:   "approval_rejected",
		},
		{
			name: "canceled",
			resolve: func(a *App, req approval.Request) error {
				resolved, err := a.Approvals.CancelBy(req.ID, approval.Actor{ID: "reviewer", AuthMethod: "bearer"})
				if err != nil {
					return err
				}
				return a.CancelApprovalTaskWithReason(req.ID, resolved.TaskID, "approval_canceled", "approval was canceled before execution")
			},
			wantStatus: approval.StatusCanceled,
			wantCode:   "approval_canceled",
		},
		{
			name: "expired",
			resolve: func(a *App, req approval.Request) error {
				resolved, err := a.Approvals.Expire(req.ID)
				if err != nil {
					return err
				}
				return a.CancelApprovalTaskWithReason(req.ID, resolved.TaskID, "approval_expired", "approval expired before execution")
			},
			wantStatus: approval.StatusExpired,
			wantCode:   "approval_expired",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			a := newApprovalReasonTestApp(t)
			submission := submitApprovalReasonTask(t, a)
			if err := test.resolve(a, *submission.Approval); err != nil {
				t.Fatalf("resolve approval: %v", err)
			}
			final := waitForAppTaskTerminal(t, a, submission.Task.ID)
			if final.Status != core.StatusCanceled || final.ErrorCode != test.wantCode {
				t.Fatalf("unexpected terminal task: %#v", final)
			}
			stored, err := a.Approvals.Get(submission.Approval.ID)
			if err != nil {
				t.Fatalf("get approval: %v", err)
			}
			if stored.Status != test.wantStatus {
				t.Fatalf("unexpected approval status: %#v", stored)
			}
		})
	}
}

func TestApprovalReleaseFailureCancelsTaskWithStableReason(t *testing.T) {
	a := newApprovalReasonTestApp(t)
	submission := submitApprovalReasonTask(t, a)

	a.approvalMu.Lock()
	delete(a.approvalGates, submission.Approval.ID)
	a.approvalMu.Unlock()

	_, err := a.ApproveApproval(submission.Approval.ID, approval.Actor{ID: "http-bearer", AuthMethod: "bearer"})
	if err == nil || err.Error() != "approval gate not found" {
		t.Fatalf("expected release failure, got %v", err)
	}
	final := waitForAppTaskTerminal(t, a, submission.Task.ID)
	if final.Status != core.StatusCanceled || final.ErrorCode != "approval_release_failed" {
		t.Fatalf("unexpected task after release failure: %#v", final)
	}
	stored, err := a.Approvals.Get(submission.Approval.ID)
	if err != nil {
		t.Fatalf("get approval: %v", err)
	}
	if stored.Status != approval.StatusApproved || stored.Actor != "http-bearer" || stored.AuthMethod != "bearer" {
		t.Fatalf("unexpected approval after release failure: %#v", stored)
	}
	records, err := a.Approvals.QueryAudit(map[string]string{
		"approval_id": submission.Approval.ID,
		"action":      "approval_release_failed",
	}, 10, 0)
	if err != nil {
		t.Fatalf("query release failure audit: %v", err)
	}
	if len(records) != 1 || records[0].Actor != "http-bearer" || records[0].AuthMethod != "bearer" {
		t.Fatalf("unexpected release failure audit: %#v", records)
	}
}

func TestApproveApprovalRejectsTamperedBinding(t *testing.T) {
	a := newApprovalReasonTestApp(t)
	submission := submitApprovalReasonTask(t, a)
	if _, err := a.Approvals.Store().UpdateRequest(submission.Approval.ID, func(req *approval.Request) error {
		req.Tool = "read_file"
		return nil
	}); err != nil {
		t.Fatalf("tamper approval: %v", err)
	}

	if _, err := a.ApproveApproval(submission.Approval.ID, approval.Actor{ID: "http-bearer", AuthMethod: "bearer"}); !errors.Is(err, approval.ErrApprovalBindingMismatch) {
		t.Fatalf("expected binding mismatch, got %v", err)
	}
	task, err := a.Tasks.Get(submission.Task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Status != core.StatusAwaitingApproval {
		t.Fatalf("binding mismatch released task: %#v", task)
	}
}

func newApprovalReasonTestApp(t *testing.T) *App {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.LogStore = t.TempDir()
	cfg.Admin.Approval.Rules = []config.ApprovalRule{{
		ID: "approve-write", Effect: "require_approval", Tools: []string{"write_file"}, Risk: "high",
	}}
	cfg.Workspaces = []config.Workspace{{ID: "ws", Roots: []string{root}, CWD: root}}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("init app: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func submitApprovalReasonTask(t *testing.T, a *App) Submission {
	t.Helper()
	root := a.Workspaces["ws"].Roots[0]
	submission, err := a.SubmitToolTask("ws", "requester", "write_file", map[string]any{
		"path":    filepath.Join(root, "approval-reason.txt"),
		"content": "must not run",
	})
	if err != nil {
		t.Fatalf("submit approval task: %v", err)
	}
	if submission.Task == nil || submission.Approval == nil {
		t.Fatalf("expected gated task: %#v", submission)
	}
	return submission
}

func waitForAppTaskTerminal(t *testing.T, a *App, taskID string) *core.Task {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		task, err := a.Tasks.Get(taskID)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if task.Status == core.StatusSucceeded || task.Status == core.StatusFailed || task.Status == core.StatusCanceled || task.Status == core.StatusTimeout {
			return task
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("task %s did not reach terminal state", taskID)
	return nil
}

func TestLoadHistoryTasksOnStartup(t *testing.T) {
	base := t.TempDir()
	store := persist.NewStore(base)
	// persist a finished task
	task := core.Task{ID: "hist-1", Status: core.StatusSucceeded}
	if err := store.SaveTask(task); err != nil {
		t.Fatalf("save task: %v", err)
	}
	// ensure file exists
	if _, err := filepath.Glob(filepath.Join(base, "tasks", "*.json")); err != nil {
		t.Fatalf("glob: %v", err)
	}
	cfg := config.Config{
		LogStore: base,
		Workspaces: []config.Workspace{
			{ID: "ws"},
		},
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("init app: %v", err)
	}
	defer a.Close()
	got, err := a.Tasks.Get("hist-1")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != core.StatusSucceeded {
		t.Fatalf("unexpected status: %s", got.Status)
	}
}

func TestStartupCancelsRecoveredPendingApprovalTask(t *testing.T) {
	base := t.TempDir()
	approvalService := approval.NewServiceWithOptions(
		approval.NewStore(filepath.Join(base, "approval")),
		approval.Options{BaseURL: "https://approve.example", TTL: time.Minute},
	)
	req, err := approvalService.Create(approval.Request{
		Tool:        "run_command",
		WorkspaceID: "ws",
		Requester:   "test",
		Reason:      "approval.policy.command",
		Args:        map[string]any{"command": "rm file"},
	})
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	const taskID = "recovered-awaiting-approval"
	if _, err := approvalService.BindTask(req.ID, taskID); err != nil {
		t.Fatalf("bind task: %v", err)
	}
	store := persist.NewStore(base)
	if err := store.SaveTask(core.Task{
		ID:          taskID,
		Status:      core.StatusAwaitingApproval,
		CreatedAt:   time.Now().UTC(),
		WorkspaceID: "ws",
		ApprovalID:  req.ID,
	}); err != nil {
		t.Fatalf("save recovered task: %v", err)
	}

	cfg := config.Default()
	cfg.LogStore = base
	cfg.Workspaces = []config.Workspace{{ID: "ws", Roots: []string{t.TempDir()}}}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("init app: %v", err)
	}
	defer a.Close()

	gotApproval, err := a.Approvals.Get(req.ID)
	if err != nil {
		t.Fatalf("get reconciled approval: %v", err)
	}
	if gotApproval.Status != approval.StatusCanceled || gotApproval.Actor != "system:restart" {
		t.Fatalf("unexpected approval after restart: %#v", gotApproval)
	}
	gotTask, err := a.Tasks.Get(taskID)
	if err != nil {
		t.Fatalf("get recovered task: %v", err)
	}
	if gotTask.Status != core.StatusCanceled || gotTask.ErrorCode != "approval_restart" {
		t.Fatalf("unexpected task after restart: %#v", gotTask)
	}
	records, err := a.Approvals.QueryAudit(map[string]string{"approval_id": req.ID, "action": "canceled"}, 10, 0)
	if err != nil {
		t.Fatalf("query restart audit: %v", err)
	}
	if len(records) != 1 || records[0].Actor != "system:restart" {
		t.Fatalf("missing restart cancellation audit: %#v", records)
	}
}

func TestSessionJanitorSweepsIdleSessions(t *testing.T) {
	cfg := config.Config{
		SessionCleanup: config.SessionCleanup{
			FinishedTTLSeconds: 0,
			IdleTTLSeconds:     1,
			MaxSessions:        100,
			SweepIntervalMs:    100,
		},
		Workspaces: []config.Workspace{
			{ID: "ws"},
		},
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("init app: %v", err)
	}
	defer a.Close()

	closed := make(chan struct{}, 1)
	s := a.Sessions.Create(core.Session{}, core.SessionController{
		Close: func() error {
			select {
			case closed <- struct{}{}:
			default:
			}
			return nil
		},
	})
	if err := a.Sessions.Start(s.ID); err != nil {
		t.Fatalf("start session: %v", err)
	}
	select {
	case <-closed:
	case <-time.After(1500 * time.Millisecond):
		t.Fatalf("expected janitor to close idle session")
	}
	if _, err := a.Sessions.Get(s.ID); err == nil {
		t.Fatalf("expected idle session removed")
	}
}

func TestNewStartsRelayClients(t *testing.T) {
	cfg := config.Config{
		Workspaces: []config.Workspace{{ID: "ws"}},
		MCPServers: []config.MCPServer{{
			Name:    "fake",
			Command: "go",
			Args:    []string{"run", fakeServerDir(t)},
		}},
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("init app: %v", err)
	}
	defer a.Close()

	if _, ok := a.Relays["fake"]; !ok {
		t.Fatalf("expected relay client to be loaded")
	}
	if len(a.RelayErrors) != 0 {
		t.Fatalf("unexpected relay errors: %#v", a.RelayErrors)
	}
}

func TestNewSkipsFailedRelayClients(t *testing.T) {
	cfg := config.Config{
		Workspaces: []config.Workspace{{ID: "ws"}},
		MCPServers: []config.MCPServer{
			{
				Name:    "fake",
				Command: "go",
				Args:    []string{"run", fakeServerDir(t)},
			},
			{
				Name:    "broken",
				Command: "definitely-not-a-real-command",
			},
		},
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("init app: %v", err)
	}
	defer a.Close()

	if _, ok := a.Relays["fake"]; !ok {
		t.Fatalf("expected healthy relay client to be loaded")
	}
	if _, ok := a.Relays["broken"]; ok {
		t.Fatalf("broken relay should not be loaded")
	}
	if _, ok := a.RelayErrors["broken"]; !ok {
		t.Fatalf("expected broken relay error to be recorded: %#v", a.RelayErrors)
	}
}

func TestCloseStopsRelayClients(t *testing.T) {
	cfg := config.Config{
		Workspaces: []config.Workspace{{ID: "ws"}},
		MCPServers: []config.MCPServer{{
			Name:    "fake",
			Command: "go",
			Args:    []string{"run", fakeServerDir(t)},
		}},
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("init app: %v", err)
	}
	relay := a.Relays["fake"]
	if relay == nil {
		t.Fatalf("expected relay client")
	}
	a.Close()

	_, err = relay.CallTool(context.Background(), "echo_remote", map[string]any{"text": "hello"})
	if err == nil {
		t.Fatal("expected closed relay to reject calls")
	}
	var rpcErr *mcprelay.RPCError
	if errors.As(err, &rpcErr) {
		t.Fatalf("expected process exit error, got rpc error: %v", err)
	}
}

func TestNewToolHTTPModeDisablesRelayButKeepsRegistryTools(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		HTTP: config.HTTP{BaseURL: "http://127.0.0.1:8000"},
		ToolHTTPMode: config.ToolHTTPMode{
			Enabled:               true,
			AccessTokenTTLSeconds: 600,
		},
		Workspaces: []config.Workspace{{
			ID:    "ws",
			Roots: []string{root},
			CWD:   root,
		}},
		MCPServers: []config.MCPServer{{
			Name:    "fake",
			Command: "go",
			Args:    []string{"run", fakeServerDir(t)},
		}},
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("init app: %v", err)
	}
	defer a.Close()

	if len(a.Relays) != 0 {
		t.Fatalf("tool http mode should not start relays: %#v", a.Relays)
	}
	reg := a.Registries["ws"]
	if reg == nil {
		t.Fatalf("missing registry")
	}
	got := reg.ListVisible()
	if len(got) <= 2 {
		t.Fatalf("tool http mode should keep business tools in the registry: %#v", got)
	}
	if !reg.IsVisible("run_command") || !reg.IsVisible("get_temporary_access_token") {
		t.Fatalf("missing expected tool http mode tools: %#v", got)
	}
}

func fakeServerDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "testutil", "fake_mcp_server"))
}
