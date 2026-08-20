package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"localgate/internal/app"
	"localgate/internal/config"
	"localgate/internal/core"
)

func newMCPRuntimeTestAPI(t *testing.T) (*API, *http.ServeMux, string, string) {
	t.Helper()
	workspaceRoot := t.TempDir()
	storeRoot := t.TempDir()
	cfg := config.Config{
		LogStore: storeRoot,
		HTTP:     config.HTTP{BaseURL: "http://127.0.0.1:8000"},
		Admin: config.AdminConfig{
			Enabled: true,
			Approval: config.ApprovalConfig{
				Enabled:            true,
				PendingWaitSeconds: 0,
				MaxSyncWaitSeconds: 2,
			},
		},
		Deployments: config.DeploymentsConfig{PublicBaseURL: "https://apps.test", PrivateBaseURL: "https://private.apps.test", DefaultAccess: "public"},
		Root: config.Root{Path: workspaceRoot,
			CommandAllowlist: []string{"echo"},
		},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)
	return api, mux, workspaceRoot, storeRoot
}

func TestMCPRuntimeToolDiscovery(t *testing.T) {
	_, mux, _, _ := newMCPRuntimeTestAPI(t)
	req := httptest.NewRequest(http.MethodGet, "/mcp/tools", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Tools []struct {
			Name        string         `json:"name"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Tools) != 4 || body.Tools[0].Name != "delete_deployment" || body.Tools[1].Name != "deploy_tool" || body.Tools[2].Name != "list_deployments" || body.Tools[3].Name != "set_deployment_access" {
		t.Fatalf("unexpected tools: %#v", body.Tools)
	}
	for _, tool := range body.Tools {
		if tool.InputSchema["type"] != "object" {
			t.Fatalf("missing input schema: %#v", tool)
		}
	}
}

func TestMCPRuntimeExecuteDeployTool(t *testing.T) {
	api, mux, workspaceRoot, storeRoot := newMCPRuntimeTestAPI(t)
	source := filepath.Join(workspaceRoot, "site")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	body := map[string]any{
		"tool":      "deploy_tool",
		"requester": "direct-client",
		"input": map[string]any{
			"type":   "static",
			"name":   "demo",
			"source": source,
			"options": map[string]any{
				"overwrite": false,
			},
		},
	}
	encoded, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp/tools/execute", bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var response mcpRuntimeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Success {
		t.Fatalf("unexpected failure: %#v", response)
	}
	data, ok := response.Data.(map[string]any)
	if !ok || data["source_preserved"] != true {
		t.Fatalf("source should be preserved by default: %#v", response.Data)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("default deployment should preserve source: %v", err)
	}
	deployed := filepath.Join(storeRoot, "static", "demo", "index.html")
	if got, err := os.ReadFile(deployed); err != nil || string(got) != "hello" {
		t.Fatalf("unexpected deployed file=%q err=%v", got, err)
	}
	staticReq := httptest.NewRequest(http.MethodGet, "https://demo.apps.test/", nil)
	staticW := httptest.NewRecorder()
	api.App.DeploymentManager.ServeHTTP(staticW, staticReq)
	if staticW.Code != http.StatusOK || staticW.Body.String() != "hello" {
		t.Fatalf("static site unavailable: code=%d body=%q", staticW.Code, staticW.Body.String())
	}
	legacyReq := httptest.NewRequest(http.MethodGet, "/static/demo/", nil)
	legacyW := httptest.NewRecorder()
	mux.ServeHTTP(legacyW, legacyReq)
	if legacyW.Code != http.StatusNotFound {
		t.Fatalf("legacy static path must be unavailable, got %d", legacyW.Code)
	}
	records, err := api.App.Approvals.QueryAudit(map[string]string{"tool": "deploy_tool"}, 20, 0)
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	actions := map[string]bool{}
	for _, record := range records {
		actions[record.Action] = true
	}
	if !actions["execution_started"] || !actions["execution_succeeded"] {
		t.Fatalf("missing direct execution audit records: %#v", records)
	}
}

func TestMCPRuntimeRejectsInvalidInputAndUnknownTool(t *testing.T) {
	_, mux, _, _ := newMCPRuntimeTestAPI(t)
	invalid := []byte(`{"tool":"deploy_tool","input":{"type":"static","name":"demo"}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp/tools/execute", bytes.NewReader(invalid))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	var response mcpRuntimeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode invalid response: %v", err)
	}
	if response.Success || response.Code != "invalid_tool_input" {
		t.Fatalf("unexpected invalid response: %#v", response)
	}

	unknown := []byte(`{"tool":"missing","input":{}}`)
	req = httptest.NewRequest(http.MethodPost, "/mcp/tools/execute", bytes.NewReader(unknown))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMCPRuntimeRejectsLegacyTool(t *testing.T) {
	_, mux, _, _ := newMCPRuntimeTestAPI(t)
	body := []byte(`{"tool":"get_startup_instructions","input":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp/tools/execute", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected legacy tool to be unavailable, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestMCPRuntimeRejectsSourceOutsideWorkspace(t *testing.T) {
	_, mux, _, _ := newMCPRuntimeTestAPI(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "index.html"), []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside source: %v", err)
	}
	body := map[string]any{
		"tool": "deploy_tool",
		"input": map[string]any{
			"type":   "static",
			"name":   "outside",
			"source": outside,
		},
	}
	encoded, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp/tools/execute", bytes.NewReader(encoded))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	var response mcpRuntimeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Success || response.Code != "tool_execution_failed" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestMCPRuntimeDeploymentApprovalAndAuditFlow(t *testing.T) {
	workspaceRoot := t.TempDir()
	storeRoot := t.TempDir()
	cfg := config.Config{
		LogStore: storeRoot,
		HTTP:     config.HTTP{BaseURL: "http://127.0.0.1:8000"},
		Admin: config.AdminConfig{Enabled: true, BaseURL: "http://admin.test", Approval: config.ApprovalConfig{
			Enabled:            true,
			PendingWaitSeconds: 0,
		}},
		Deployments: config.DeploymentsConfig{PublicBaseURL: "https://apps.test", PrivateBaseURL: "https://private.apps.test", DefaultAccess: "public",
			Static: config.StaticHosting{
				MaxFiles:        100,
				MaxTotalBytes:   50 << 20,
				MaxFileBytes:    50 << 20,
				RequireApproval: true,
			},
		},
		Root: config.Root{Path: workspaceRoot,
			CommandAllowlist: []string{"echo"},
		},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	source := filepath.Join(workspaceRoot, "site")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("approved"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	body := map[string]any{
		"tool":      "deploy_tool",
		"requester": "test-client",
		"input": map[string]any{
			"type":   "static",
			"name":   "approved-site",
			"source": source,
			"options": map[string]any{
				"preserve_source": false,
			},
		},
	}
	encoded, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPost, "/mcp/tools/execute", bytes.NewReader(encoded))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, request)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected pending approval, got %d body=%s", w.Code, w.Body.String())
	}
	var pending mcpRuntimeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &pending); err != nil {
		t.Fatalf("decode pending response: %v", err)
	}
	data, _ := pending.Data.(map[string]any)
	approvalID, _ := data["approval_id"].(string)
	if pending.Code != "approval_required" || approvalID == "" {
		t.Fatalf("unexpected pending response: %#v", pending)
	}
	if _, err := os.Stat(filepath.Join(storeRoot, "static", "approved-site")); !os.IsNotExist(err) {
		t.Fatalf("site should not exist before approval: %v", err)
	}

	approveReq := httptest.NewRequest(http.MethodPost, "http://admin.test/approvals/"+approvalID+"/approve", bytes.NewBufferString(`{"actor":"reviewer"}`))
	approveW := httptest.NewRecorder()
	mux.ServeHTTP(approveW, approveReq)
	if approveW.Code != http.StatusAccepted {
		t.Fatalf("approve failed: code=%d body=%s", approveW.Code, approveW.Body.String())
	}
	var approved struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(approveW.Body.Bytes(), &approved); err != nil {
		t.Fatalf("decode approved response: %v", err)
	}
	if approved.TaskID == "" {
		t.Fatalf("missing approved task id: %s", approveW.Body.String())
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		task, err := application.Tasks.Get(approved.TaskID)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if task.Status == "succeeded" {
			break
		}
		if task.Status == "failed" || task.Status == "canceled" || task.Status == "timeout" {
			t.Fatalf("approved deployment task failed: %+v", task)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for approved deployment: %+v", task)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("approved deployment should remove source when preserve_source=false: %v", err)
	}

	staticReq := httptest.NewRequest(http.MethodGet, "https://approved-site.apps.test/", nil)
	staticW := httptest.NewRecorder()
	application.DeploymentManager.ServeHTTP(staticW, staticReq)
	if staticW.Code != http.StatusOK || staticW.Body.String() != "approved" {
		t.Fatalf("approved site unavailable: code=%d body=%q", staticW.Code, staticW.Body.String())
	}

	records, err := application.Approvals.QueryAudit(map[string]string{
		"tool":      "deploy_tool",
		"requester": "test-client",
	}, 20, 0)
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	actions := map[string]bool{}
	foundRemovalAudit := false
	for _, record := range records {
		actions[record.Action] = true
		if record.Action == "execution_started" && record.Details["preserve_source"] == false {
			foundRemovalAudit = true
		}
	}
	if !foundRemovalAudit {
		t.Fatalf("missing preserve_source=false audit details: %#v", records)
	}
	for _, action := range []string{"created", "approved", "execution_started", "execution_succeeded"} {
		if !actions[action] {
			t.Fatalf("missing audit action %q: %#v", action, records)
		}
	}
}

func TestMCPRuntimeGlobalApprovalSwitchBypassesStaticApproval(t *testing.T) {
	workspaceRoot := t.TempDir()
	storeRoot := t.TempDir()
	cfg := config.Default()
	cfg.LogStore = storeRoot
	cfg.HTTP.BaseURL = "http://127.0.0.1:8000"
	cfg.Admin.Approval.Enabled = false
	cfg.Deployments.Static.RequireApproval = true
	cfg.Deployments.PublicBaseURL = "https://apps.test"
	cfg.Deployments.PrivateBaseURL = "https://private.apps.test"
	cfg.Deployments.DefaultAccess = "public"
	cfg.HTTP.BaseURL = "https://control.test"
	cfg.Root = config.Root{Path: workspaceRoot}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	api := &API{App: application}
	mux := http.NewServeMux()
	api.Register(mux)

	source := filepath.Join(workspaceRoot, "site")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("no-approval"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	body, _ := json.Marshal(map[string]any{
		"tool": "deploy_tool",
		"input": map[string]any{
			"type":   "static",
			"name":   "global-switch-off",
			"source": source,
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp/tools/execute", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("global approval switch should bypass pending approval: code=%d body=%s", w.Code, w.Body.String())
	}
	var response mcpRuntimeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Success {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestUnsafeStaticManagementEndpointIsAbsent(t *testing.T) {
	_, mux, _, _ := newMCPRuntimeTestAPI(t)
	req := httptest.NewRequest(http.MethodPost, "/system/static/sites/register", bytes.NewBufferString("name=x&path=/tmp"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unsafe management endpoint should not be registered: code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestMCPRuntimeCanRemoveSourceAfterDeployment(t *testing.T) {
	_, mux, workspaceRoot, storeRoot := newMCPRuntimeTestAPI(t)
	source := filepath.Join(workspaceRoot, "removable-dist")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("moved"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	body := map[string]any{
		"tool": "deploy_tool",
		"input": map[string]any{
			"type":   "static",
			"name":   "remove-source",
			"source": source,
			"options": map[string]any{
				"preserve_source": false,
			},
		},
	}
	encoded, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp/tools/execute", bytes.NewReader(encoded))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var response mcpRuntimeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data, ok := response.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected response data: %#v", response.Data)
	}
	preserved, exists := data["source_preserved"].(bool)
	if !exists || preserved {
		t.Fatalf("expected source_preserved=false: %#v", data)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source should be removed: %v", err)
	}
	deployed := filepath.Join(storeRoot, "static", "remove-source", "index.html")
	if got, err := os.ReadFile(deployed); err != nil || string(got) != "moved" {
		t.Fatalf("deployed file missing after source removal: content=%q err=%v", got, err)
	}
}

func TestMCPRuntimeRefusesToRemoveWorkspaceRoot(t *testing.T) {
	_, mux, workspaceRoot, _ := newMCPRuntimeTestAPI(t)
	if err := os.WriteFile(filepath.Join(workspaceRoot, "index.html"), []byte("root"), 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}
	body := map[string]any{
		"tool": "deploy_tool",
		"input": map[string]any{
			"type":   "static",
			"name":   "unsafe-root",
			"source": workspaceRoot,
			"options": map[string]any{
				"preserve_source": false,
			},
		},
	}
	encoded, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp/tools/execute", bytes.NewReader(encoded))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	var response mcpRuntimeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != "unsafe_source_removal" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if _, err := os.Stat(workspaceRoot); err != nil {
		t.Fatalf("workspace root must remain intact: %v", err)
	}
}

func TestMCPRuntimeUsesUnifiedApprovalRules(t *testing.T) {
	setup := func(t *testing.T, rules []config.ApprovalRule) (*API, *http.ServeMux, string, string) {
		t.Helper()
		workspaceRoot := t.TempDir()
		storeRoot := t.TempDir()
		cfg := config.Default()
		cfg.LogStore = storeRoot
		cfg.HTTP.BaseURL = "http://127.0.0.1:8000"
		cfg.Admin.BaseURL = "https://admin.example"
		cfg.Admin.Approval = config.ApprovalConfig{
			Enabled:            true,
			DefaultTTLSeconds:  600,
			PendingWaitSeconds: 0,
			MaxSyncWaitSeconds: 2,
			Rules:              rules,
		}
		cfg.Deployments.Static.RequireApproval = true
		cfg.Deployments.PublicBaseURL = "https://apps.test"
		cfg.Deployments.PrivateBaseURL = "https://private.apps.test"
		cfg.Deployments.DefaultAccess = "public"
		cfg.HTTP.BaseURL = "https://control.test"
		cfg.Root = config.Root{Path: workspaceRoot}
		application, err := app.New(cfg)
		if err != nil {
			t.Fatalf("app.New: %v", err)
		}
		t.Cleanup(func() { _ = application.Close() })
		api := &API{App: application}
		mux := http.NewServeMux()
		api.Register(mux)
		return api, mux, workspaceRoot, storeRoot
	}
	execute := func(t *testing.T, mux *http.ServeMux, body map[string]any) (*httptest.ResponseRecorder, mcpRuntimeResponse) {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode request: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/mcp/tools/execute", bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		var response mcpRuntimeResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response: %v body=%s", err, w.Body.String())
		}
		return w, response
	}

	t.Run("allow overrides static approval fallback", func(t *testing.T) {
		api, mux, workspaceRoot, _ := setup(t, []config.ApprovalRule{{
			ID: "allow-deploy", Effect: "allow", Tools: []string{"deploy_tool"},
		}})
		source := filepath.Join(workspaceRoot, "allow-site")
		if err := os.MkdirAll(source, 0o755); err != nil {
			t.Fatalf("mkdir source: %v", err)
		}
		if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("allowed"), 0o644); err != nil {
			t.Fatalf("write source: %v", err)
		}
		w, response := execute(t, mux, map[string]any{
			"tool":  "deploy_tool",
			"input": map[string]any{"type": "static", "name": "allowed", "source": source},
		})
		if w.Code != http.StatusOK || !response.Success {
			t.Fatalf("allow rule did not execute: code=%d response=%#v", w.Code, response)
		}
		approvals, err := api.App.Approvals.List("")
		if err != nil || len(approvals) != 0 {
			t.Fatalf("allow rule created approval: %#v err=%v", approvals, err)
		}
	})

	t.Run("deny returns stable policy error", func(t *testing.T) {
		api, mux, workspaceRoot, _ := setup(t, []config.ApprovalRule{{
			ID: "deny-deploy", Effect: "deny", Tools: []string{"deploy_tool"},
		}})
		source := filepath.Join(workspaceRoot, "deny-site")
		if err := os.MkdirAll(source, 0o755); err != nil {
			t.Fatalf("mkdir source: %v", err)
		}
		if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("denied"), 0o644); err != nil {
			t.Fatalf("write source: %v", err)
		}
		w, response := execute(t, mux, map[string]any{
			"tool":  "deploy_tool",
			"input": map[string]any{"type": "static", "name": "denied", "source": source},
		})
		if w.Code != http.StatusForbidden || response.Code != "policy_denied" {
			t.Fatalf("unexpected deny response: code=%d response=%#v", w.Code, response)
		}
		if _, exists := api.App.StaticService.GetSite("denied"); exists {
			t.Fatal("denied deployment was executed")
		}
	})

	t.Run("arbitrary runtime tool can require approval", func(t *testing.T) {
		api, mux, _, _ := setup(t, []config.ApprovalRule{{
			ID: "approve-list", Effect: "require_approval", Tools: []string{"list_deployments"}, Risk: "low",
		}})
		w, response := execute(t, mux, map[string]any{
			"tool":  "list_deployments",
			"input": map[string]any{},
		})
		if w.Code != http.StatusAccepted || response.Code != "approval_required" {
			t.Fatalf("unexpected approval response: code=%d response=%#v", w.Code, response)
		}
		data, _ := response.Data.(map[string]any)
		approvalID, _ := data["approval_id"].(string)
		if approvalID == "" {
			t.Fatalf("missing approval id: %#v", response)
		}
		stored, err := api.App.Approvals.Get(approvalID)
		if err != nil || stored.Tool != "list_deployments" || stored.Status != "pending" {
			t.Fatalf("unexpected stored approval: %#v err=%v", stored, err)
		}
	})
}

func TestRuntimeTaskErrorStatusPreservesApprovalReason(t *testing.T) {
	tests := []struct {
		code       string
		wantStatus int
	}{
		{code: "approval_rejected", wantStatus: http.StatusConflict},
		{code: "approval_expired", wantStatus: http.StatusConflict},
		{code: "approval_canceled", wantStatus: http.StatusConflict},
		{code: "approval_restart", wantStatus: http.StatusConflict},
		{code: "approval_release_failed", wantStatus: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			status, code := runtimeTaskErrorStatus(&core.Task{
				Status:    core.StatusCanceled,
				ErrorCode: test.code,
			})
			if status != test.wantStatus || code != test.code {
				t.Fatalf("unexpected mapping: status=%d code=%q", status, code)
			}
		})
	}
}

func TestRuntimeTaskErrorStatusPreservesDynamicDeploymentCode(t *testing.T) {
	tests := []struct {
		code       string
		wantStatus int
	}{
		{code: "deployment_not_found", wantStatus: http.StatusNotFound},
		{code: "dynamic_hosting_disabled", wantStatus: http.StatusServiceUnavailable},
		{code: "deployment_executable_denied", wantStatus: http.StatusBadRequest},
		{code: "deployment_snapshot_failed", wantStatus: http.StatusRequestEntityTooLarge},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			status, code := runtimeTaskErrorStatus(&core.Task{Status: core.StatusFailed, ErrorCode: test.code})
			if status != test.wantStatus || code != test.code {
				t.Fatalf("unexpected mapping: status=%d code=%q", status, code)
			}
		})
	}
}
