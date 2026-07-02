package mcpgo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"localgate/internal/app"
	"localgate/internal/config"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestRegisterCoreToolsAddsAsyncSchema(t *testing.T) {
	root := t.TempDir()
	s := newTestServer(t, root, nil)

	tool := s.impl.ListTools()["run_command"]
	if tool == nil {
		t.Fatal("run_command tool not registered")
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.Tool.RawInputSchema, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	props, _ := schema["properties"].(map[string]any)
	asyncProp, ok := props["async"].(map[string]any)
	if !ok {
		t.Fatalf("async property missing in schema: %#v", schema)
	}
	if asyncProp["type"] != "boolean" {
		t.Fatalf("unexpected async type: %#v", asyncProp)
	}

	startup := s.impl.ListTools()["get_startup_instructions"]
	if startup == nil {
		t.Fatal("get_startup_instructions tool not registered")
	}
	if startup.Tool.Description == "" {
		t.Fatalf("get_startup_instructions should expose description")
	}
	var startupSchema map[string]any
	if err := json.Unmarshal(startup.Tool.RawInputSchema, &startupSchema); err != nil {
		t.Fatalf("unmarshal startup schema: %v", err)
	}
	startupProps, _ := startupSchema["properties"].(map[string]any)
	if _, ok := startupProps["async"]; ok {
		t.Fatalf("get_startup_instructions should not expose async: %#v", startupSchema)
	}

	if tool := s.impl.ListTools()["get_tool_schema"]; tool == nil {
		t.Fatal("get_tool_schema tool should be registered in legacy mode")
	}
	if tool := s.impl.ListTools()["get_temporary_access_token"]; tool != nil {
		t.Fatal("get_temporary_access_token should not be registered in legacy mode")
	}
}

func TestRegisterCoreToolsExposeOnlyBase64dataSchemaWhenEnabled(t *testing.T) {
	root := t.TempDir()
	s := newTestServerWithConfig(t, config.Config{
		LogStore:         t.TempDir(),
		DefaultTimeoutMs: 10_000,
		ToolArguments:    config.ToolArguments{EnableBase64: true},
		Workspaces: []config.Workspace{{
			ID:               "ws",
			Roots:            []string{root},
			CWD:              root,
			CommandAllowlist: []string{"echo", "sh"},
		}},
	})

	tool := s.impl.ListTools()["run_command"]
	if tool == nil {
		t.Fatal("run_command tool not registered")
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.Tool.RawInputSchema, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	props, _ := schema["properties"].(map[string]any)
	if len(props) != 1 {
		t.Fatalf("expected single base64data property, got %#v", schema)
	}
	if _, ok := props["base64data"]; !ok {
		t.Fatalf("missing base64data property: %#v", schema)
	}
	if _, ok := props["command"]; ok {
		t.Fatalf("should not expose original properties in base64 mode: %#v", schema)
	}
	if _, ok := props["async"]; ok {
		t.Fatalf("should not expose async property in base64 mode: %#v", schema)
	}
}

func TestDefaultHiddenToolNotRegisteredInMCPGo(t *testing.T) {
	root := t.TempDir()
	s := newTestServer(t, root, []string{"echo", "sh"})
	if tool := s.impl.ListTools()["list_directory"]; tool != nil {
		t.Fatalf("list_directory should be hidden by default in mcpgo")
	}
	if tool := s.impl.ListTools()["search_files"]; tool != nil {
		t.Fatalf("search_files should be hidden by default in mcpgo")
	}
	if tool := s.impl.ListTools()["run_argv"]; tool != nil {
		t.Fatalf("run_argv should be hidden by default in mcpgo")
	}
	if tool := s.impl.ListTools()["create_upload_token"]; tool != nil {
		t.Fatalf("create_upload_token should be hidden by default in mcpgo")
	}
	if tool := s.impl.ListTools()["create_download_token"]; tool != nil {
		t.Fatalf("create_download_token should be hidden by default in mcpgo")
	}
}

func TestHiddenToolNotRegisteredInMCPGo(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		LogStore:         t.TempDir(),
		DefaultTimeoutMs: 10_000,
		Workspaces: []config.Workspace{
			{
				ID:               "ws",
				Roots:            []string{root},
				CWD:              root,
				CommandAllowlist: []string{"echo", "sh"},
				HiddenTools:      []string{"run_command"},
			},
		},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	s, err := New(application, "localgate", "test", "ws")
	if err != nil {
		t.Fatalf("mcpgo.New: %v", err)
	}
	if tool := s.impl.ListTools()["run_command"]; tool != nil {
		t.Fatalf("hidden tool should not be registered in mcpgo")
	}
}

func TestApplyPatchRegisteredAndWorksInMCPGo(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "a.txt")
	if err := os.WriteFile(target, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	s := newTestServer(t, root, []string{"echo", "sh"})
	if tool := s.impl.ListTools()["apply_patch"]; tool == nil {
		t.Fatalf("apply_patch tool not registered")
	}

	res, err := s.makeInvokeHandler("apply_patch")(context.Background(), callReq(map[string]any{
		"patch": strings.Join([]string{
			"*** Begin Patch",
			"*** Update File: a.txt",
			"@@",
			"-hello",
			"+world",
			"*** End Patch",
		}, "\n"),
	}))
	if err != nil {
		t.Fatalf("invoke apply_patch: %v", err)
	}
	payload := payloadFromResult(t, res)
	if got := statusFromPayload(payload); got != "succeeded" {
		t.Fatalf("unexpected status: %s payload=%#v", got, payload)
	}
	result, ok := payload["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing result payload: %#v", payload)
	}
	if result["updated"] != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(b) != "world\n" {
		t.Fatalf("unexpected content: %q", string(b))
	}
}

func TestGetStartupInstructionsRegisteredAndWorksInMCPGo(t *testing.T) {
	root := t.TempDir()
	s := newTestServer(t, root, []string{"echo", "sh"})
	if tool := s.impl.ListTools()["get_startup_instructions"]; tool == nil {
		t.Fatalf("get_startup_instructions tool not registered")
	} else if tool.Tool.Description == "" {
		t.Fatalf("get_startup_instructions description should not be empty")
	}

	res, err := s.makeInvokeHandler("get_startup_instructions")(context.Background(), callReq(nil))
	if err != nil {
		t.Fatalf("invoke get_startup_instructions: %v", err)
	}
	payload := payloadFromResult(t, res)
	if got := statusFromPayload(payload); got != "succeeded" {
		t.Fatalf("unexpected status: %s payload=%#v", got, payload)
	}
	result, ok := payload["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing result payload: %#v", payload)
	}
	prompt, _ := result["prompt"].(string)
	if !strings.Contains(prompt, "LocalGate") {
		t.Fatalf("missing service instructions in prompt: %#v", result)
	}
	if !strings.Contains(prompt, "run_command") {
		t.Fatalf("missing visible tool in prompt: %#v", result)
	}
	switch tools := result["tools"].(type) {
	case []map[string]any:
		if len(tools) == 0 {
			t.Fatalf("missing visible tools in result: %#v", result)
		}
	case []any:
		if len(tools) == 0 {
			t.Fatalf("missing visible tools in result: %#v", result)
		}
	default:
		t.Fatalf("missing visible tools in result: %#v", result)
	}
	if len(result) != 2 {
		t.Fatalf("startup instructions should only return prompt and tools: %#v", result)
	}
}

func TestToolHTTPModeOnlyRegistersThreeToolsInMCPGo(t *testing.T) {
	root := t.TempDir()
	s := newTestServerWithConfig(t, config.Config{
		LogStore: filepath.Join(root, "store"),
		HTTP:     config.HTTP{BaseURL: "http://127.0.0.1:8000"},
		ToolHTTPMode: config.ToolHTTPMode{
			Enabled:               true,
			AccessTokenTTLSeconds: 600,
		},
		Workspaces: []config.Workspace{{
			ID:    "ws",
			Roots: []string{root},
			CWD:   root,
		}},
	})

	tools := s.impl.ListTools()
	if len(tools) != 3 {
		t.Fatalf("expected exactly 3 tools, got %#v", tools)
	}
	if tools["get_startup_instructions"] == nil || tools["get_temporary_access_token"] == nil || tools["get_tool_schema"] == nil {
		t.Fatalf("missing minimal tools: %#v", tools)
	}
	if tools["task_status"] != nil || tools["run_command"] != nil {
		t.Fatalf("tool http mode should not register admin or shell tools in MCP: %#v", tools)
	}
}

func TestUploadFileExposesOpenAIFileParamMetadataInMCPGo(t *testing.T) {
	root := t.TempDir()
	s := newTestServer(t, root, []string{"echo", "sh"})
	tool := s.impl.ListTools()["upload_file"]
	if tool == nil {
		t.Fatal("upload_file tool not registered")
	}
	if tool.Tool.Meta == nil {
		t.Fatalf("upload_file should expose tool metadata")
	}
	got := tool.Tool.Meta.AdditionalFields["openai/fileParams"]
	switch params := got.(type) {
	case []string:
		if len(params) != 1 || params[0] != "file" {
			t.Fatalf("unexpected upload_file metadata: %#v", tool.Tool.Meta.AdditionalFields)
		}
	case []any:
		if len(params) != 1 || params[0] != "file" {
			t.Fatalf("unexpected upload_file metadata: %#v", tool.Tool.Meta.AdditionalFields)
		}
	default:
		t.Fatalf("unexpected upload_file metadata: %#v", tool.Tool.Meta.AdditionalFields)
	}
}

func TestUploadFileKeepsOriginalSchemaInBase64ModeInMCPGo(t *testing.T) {
	root := t.TempDir()
	s := newTestServerWithConfig(t, config.Config{
		LogStore:         t.TempDir(),
		DefaultTimeoutMs: 10_000,
		ToolArguments:    config.ToolArguments{EnableBase64: true},
		Workspaces: []config.Workspace{{
			ID:               "ws",
			Roots:            []string{root},
			CWD:              root,
			CommandAllowlist: []string{"echo", "sh"},
		}},
	})
	tool := s.impl.ListTools()["upload_file"]
	if tool == nil {
		t.Fatal("upload_file tool not registered")
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.Tool.RawInputSchema, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["base64data"]; ok {
		t.Fatalf("upload_file should not expose base64data schema: %#v", schema)
	}
	if _, ok := props["file"]; !ok {
		t.Fatalf("upload_file should preserve file property: %#v", schema)
	}
	if _, ok := props["path"]; !ok {
		t.Fatalf("upload_file should preserve path property: %#v", schema)
	}
}

func TestStartupInstructionsKeepsOriginalSchemaInBase64ModeInMCPGo(t *testing.T) {
	root := t.TempDir()
	s := newTestServerWithConfig(t, config.Config{
		LogStore:         t.TempDir(),
		DefaultTimeoutMs: 10_000,
		ToolArguments:    config.ToolArguments{EnableBase64: true},
		Workspaces: []config.Workspace{{
			ID:               "ws",
			Roots:            []string{root},
			CWD:              root,
			CommandAllowlist: []string{"echo", "sh"},
		}},
	})
	tool := s.impl.ListTools()["get_startup_instructions"]
	if tool == nil {
		t.Fatal("get_startup_instructions tool not registered")
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.Tool.RawInputSchema, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	props, _ := schema["properties"].(map[string]any)
	if len(props) != 0 {
		t.Fatalf("get_startup_instructions should preserve original empty schema: %#v", schema)
	}
	if _, ok := props["base64data"]; ok {
		t.Fatalf("get_startup_instructions should not expose base64data schema: %#v", schema)
	}
}

func TestGetToolSchemaKeepsOriginalSchemaInBase64ModeInMCPGo(t *testing.T) {
	root := t.TempDir()
	s := newTestServerWithConfig(t, config.Config{
		LogStore:         t.TempDir(),
		DefaultTimeoutMs: 10_000,
		ToolHTTPMode:     config.ToolHTTPMode{Enabled: true, AccessTokenTTLSeconds: 600},
		HTTP:             config.HTTP{BaseURL: "http://127.0.0.1:8000"},
		ToolArguments:    config.ToolArguments{EnableBase64: true},
		Workspaces: []config.Workspace{{
			ID:               "ws",
			Roots:            []string{root},
			CWD:              root,
			CommandAllowlist: []string{"echo", "sh"},
		}},
	})
	tool := s.impl.ListTools()["get_tool_schema"]
	if tool == nil {
		t.Fatal("get_tool_schema tool not registered")
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.Tool.RawInputSchema, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["base64data"]; ok {
		t.Fatalf("get_tool_schema should not expose base64data schema: %#v", schema)
	}
	if _, ok := props["tool"]; !ok {
		t.Fatalf("get_tool_schema should preserve tool property: %#v", schema)
	}
}

func TestInvokeHandlerDefaultWaitReturnsResult(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	s := newTestServer(t, root, []string{"sleep", "false"})

	res, err := s.makeInvokeHandler("list_directory")(context.Background(), callReq(map[string]any{
		"path": root,
	}))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	payload := payloadFromResult(t, res)
	if got := statusFromPayload(payload); got != "succeeded" {
		t.Fatalf("unexpected status: %s payload=%#v", got, payload)
	}
	result, ok := payload["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing result payload: %#v", payload)
	}
	entries, ok := result["entries"].([]map[string]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("missing entries in result: %#v", result)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %#v", payload)
	}
}

func TestInvokeHandlerAsyncReturnsImmediatelyAndTaskResult(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	s := newTestServer(t, root, []string{"sleep", "false"})

	res, err := s.makeInvokeHandler("list_directory")(context.Background(), callReq(map[string]any{
		"path":  root,
		"async": true,
	}))
	if err != nil {
		t.Fatalf("invoke async: %v", err)
	}
	payload := payloadFromResult(t, res)
	taskID, _ := payload["task_id"].(string)
	if taskID == "" {
		t.Fatalf("missing task_id: %#v", payload)
	}
	if async, _ := payload["async"].(bool); !async {
		t.Fatalf("async marker missing: %#v", payload)
	}
	if res.IsError {
		t.Fatalf("async submission should not be error: %#v", payload)
	}

	finalRes := waitForTaskResult(t, s, taskID, 2*time.Second)
	finalPayload := payloadFromResult(t, finalRes)
	if ready, _ := finalPayload["ready"].(bool); !ready {
		t.Fatalf("task_result not ready: %#v", finalPayload)
	}
	if got := statusFromPayload(finalPayload); got != "succeeded" {
		t.Fatalf("unexpected final status: %s payload=%#v", got, finalPayload)
	}
	if _, ok := finalPayload["result"].(map[string]any); !ok {
		t.Fatalf("missing final result: %#v", finalPayload)
	}
}

func TestTaskResultIncludesCommandOutput(t *testing.T) {
	root := t.TempDir()
	s := newTestServer(t, root, []string{"echo"})

	res, err := s.makeInvokeHandler("run_command")(context.Background(), callReq(map[string]any{
		"command": "echo hi",
		"async":   true,
	}))
	if err != nil {
		t.Fatalf("invoke async command: %v", err)
	}
	payload := payloadFromResult(t, res)
	taskID, _ := payload["task_id"].(string)
	if taskID == "" {
		t.Fatalf("missing task_id: %#v", payload)
	}

	finalRes := waitForTaskResult(t, s, taskID, 2*time.Second)
	finalPayload := payloadFromResult(t, finalRes)
	stdout, _ := finalPayload["stdout"].(string)
	if stdout == "" || stdout != "hi" {
		t.Fatalf("unexpected stdout in task_result: %#v", finalPayload)
	}
}

func TestInvokeHandlerLongRunningDefaultWaitIsNotError(t *testing.T) {
	root := t.TempDir()
	s := newTestServer(t, root, []string{"sleep"})

	res, err := s.makeInvokeHandler("run_command")(context.Background(), callReq(map[string]any{
		"command":    "sleep 6",
		"timeout_ms": 8000,
	}))
	if err != nil {
		t.Fatalf("invoke long running: %v", err)
	}
	payload := payloadFromResult(t, res)
	if res.IsError {
		t.Fatalf("running task should not be marked as error: %#v", payload)
	}
	status := statusFromPayload(payload)
	if status != "queued" && status != "running" {
		t.Fatalf("unexpected in-flight status: %s payload=%#v", status, payload)
	}
	taskID, _ := payload["task_id"].(string)
	if taskID == "" {
		t.Fatalf("missing task_id for in-flight task: %#v", payload)
	}
	if err := s.App.Tasks.Cancel(taskID); err != nil {
		t.Fatalf("cancel in-flight task: %v", err)
	}
	finalRes := waitForTaskResult(t, s, taskID, 2*time.Second)
	finalPayload := payloadFromResult(t, finalRes)
	if got := statusFromPayload(finalPayload); got != "canceled" {
		t.Fatalf("expected canceled task after cleanup, got %s payload=%#v", got, finalPayload)
	}
}

func TestInvokeHandlerAcceptsBase64ArgumentsWhenEnabled(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "a.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	s := newTestServerWithConfig(t, config.Config{
		LogStore:         t.TempDir(),
		DefaultTimeoutMs: 10_000,
		ToolArguments:    config.ToolArguments{EnableBase64: true},
		Workspaces: []config.Workspace{{
			ID:    "ws",
			Roots: []string{root},
			CWD:   root,
		}},
	})

	raw, _ := json.Marshal(map[string]any{"path": target})
	encoded := base64.StdEncoding.EncodeToString(raw)
	res, err := s.makeInvokeHandler("read_file")(context.Background(), callReq(encoded))
	if err != nil {
		t.Fatalf("invoke base64: %v", err)
	}
	payload := payloadFromResult(t, res)
	if got := statusFromPayload(payload); got != "succeeded" {
		t.Fatalf("unexpected status: %s payload=%#v", got, payload)
	}
	result, ok := payload["result"].(map[string]any)
	if !ok || result["content"] != "hello" {
		t.Fatalf("unexpected result payload: %#v", payload)
	}
}

func TestInvokeHandlerAcceptsBase64EnvelopeArgumentsWhenEnabled(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "a.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	s := newTestServerWithConfig(t, config.Config{
		LogStore:         t.TempDir(),
		DefaultTimeoutMs: 10_000,
		ToolArguments:    config.ToolArguments{EnableBase64: true},
		Workspaces: []config.Workspace{{
			ID:    "ws",
			Roots: []string{root},
			CWD:   root,
		}},
	})

	raw, _ := json.Marshal(map[string]any{"path": target})
	encoded := base64.StdEncoding.EncodeToString(raw)
	res, err := s.makeInvokeHandler("read_file")(context.Background(), callReq(map[string]any{"base64data": encoded}))
	if err != nil {
		t.Fatalf("invoke base64 envelope: %v", err)
	}
	payload := payloadFromResult(t, res)
	if got := statusFromPayload(payload); got != "succeeded" {
		t.Fatalf("unexpected status: %s payload=%#v", got, payload)
	}
	result, ok := payload["result"].(map[string]any)
	if !ok || result["content"] != "hello" {
		t.Fatalf("unexpected result payload: %#v", payload)
	}
}

func TestInvokeHandlerRejectsBase64ArgumentsWhenDisabled(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "a.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	s := newTestServerWithConfig(t, config.Config{
		LogStore:         t.TempDir(),
		DefaultTimeoutMs: 10_000,
		Workspaces: []config.Workspace{{
			ID:    "ws",
			Roots: []string{root},
			CWD:   root,
		}},
	})

	raw, _ := json.Marshal(map[string]any{"path": target})
	encoded := base64.StdEncoding.EncodeToString(raw)
	res, err := s.makeInvokeHandler("read_file")(context.Background(), callReq(encoded))
	if err != nil {
		t.Fatalf("invoke base64: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error result: %#v", res)
	}
}

func TestTaskResultReturnsFailureDetails(t *testing.T) {
	root := t.TempDir()
	s := newTestServer(t, root, []string{"false"})

	res, err := s.makeInvokeHandler("run_command")(context.Background(), callReq(map[string]any{
		"command": "false",
		"async":   true,
	}))
	if err != nil {
		t.Fatalf("invoke async failure: %v", err)
	}
	payload := payloadFromResult(t, res)
	taskID, _ := payload["task_id"].(string)
	if taskID == "" {
		t.Fatalf("missing task_id: %#v", payload)
	}

	finalRes := waitForTaskResult(t, s, taskID, 2*time.Second)
	finalPayload := payloadFromResult(t, finalRes)
	if got := statusFromPayload(finalPayload); got != "failed" {
		t.Fatalf("unexpected final status: %s payload=%#v", got, finalPayload)
	}
	if !finalRes.IsError {
		t.Fatalf("failed task_result should be marked as error: %#v", finalPayload)
	}
	if finalPayload["error_msg"] == "" {
		t.Fatalf("missing error message: %#v", finalPayload)
	}
}

func TestWaitTaskFinishReturnsTerminalStatusWithoutResult(t *testing.T) {
	root := t.TempDir()
	s := newTestServer(t, root, []string{"echo"})

	res, err := s.makeInvokeHandler("run_command")(context.Background(), callReq(map[string]any{
		"command": "echo hi",
		"async":   true,
	}))
	if err != nil {
		t.Fatalf("invoke async command: %v", err)
	}
	payload := payloadFromResult(t, res)
	taskID, _ := payload["task_id"].(string)
	if taskID == "" {
		t.Fatalf("missing task_id: %#v", payload)
	}

	waitRes, err := s.handleWaitTaskFinish(context.Background(), callReq(map[string]any{
		"task_id":    taskID,
		"timeout_ms": 2000,
	}))
	if err != nil {
		t.Fatalf("wait_task_finish: %v", err)
	}
	waitPayload := payloadFromResult(t, waitRes)
	if ready, _ := waitPayload["ready"].(bool); !ready {
		t.Fatalf("expected ready=true: %#v", waitPayload)
	}
	if got := statusFromPayload(waitPayload); got != "succeeded" {
		t.Fatalf("unexpected status: %s payload=%#v", got, waitPayload)
	}
	if _, ok := waitPayload["result"]; ok {
		t.Fatalf("wait_task_finish should not return result: %#v", waitPayload)
	}
	if _, ok := waitPayload["stdout"]; ok {
		t.Fatalf("wait_task_finish should not return stdout: %#v", waitPayload)
	}
}

func TestRegisterCoreToolsIncludesRelayTools(t *testing.T) {
	root := t.TempDir()
	s := newTestServerWithConfig(t, config.Config{
		LogStore:         t.TempDir(),
		DefaultTimeoutMs: 10_000,
		MCPServers: []config.MCPServer{{
			Name:    "fake",
			Command: "go",
			Args:    []string{"run", fakeServerDir(t)},
		}},
		Workspaces: []config.Workspace{{
			ID:    "ws",
			Roots: []string{root},
			CWD:   root,
		}},
	})

	tool := s.impl.ListTools()["fake__echo_remote"]
	if tool == nil {
		t.Fatalf("expected relay tool to be registered")
	}
	if tool.Tool.Description == "" {
		t.Fatalf("relay tool description should not be empty")
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.Tool.RawInputSchema, &schema); err != nil {
		t.Fatalf("unmarshal relay tool schema: %v", err)
	}
	if schema["type"] != "object" {
		t.Fatalf("unexpected relay schema: %#v", schema)
	}
}

func TestRelayInvokeHandlerReturnsDownstreamResult(t *testing.T) {
	root := t.TempDir()
	s := newTestServerWithConfig(t, config.Config{
		LogStore:         t.TempDir(),
		DefaultTimeoutMs: 10_000,
		MCPServers: []config.MCPServer{{
			Name:    "fake",
			Command: "go",
			Args:    []string{"run", fakeServerDir(t)},
		}},
		Workspaces: []config.Workspace{{
			ID:    "ws",
			Roots: []string{root},
			CWD:   root,
		}},
	})

	res, err := s.makeRelayInvokeHandler(s.App.Relays["fake"])(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "fake__echo_remote",
			Arguments: map[string]any{"text": "hello"},
		},
	})
	if err != nil {
		t.Fatalf("invoke relay tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("relay call should succeed: %#v", res)
	}
	if got, ok := res.StructuredContent.(map[string]any); !ok || got["echo"] != "hello" {
		t.Fatalf("unexpected structured content: %#v", res.StructuredContent)
	}
}

func TestFailedRelayNotRegisteredInMCPGo(t *testing.T) {
	root := t.TempDir()
	s := newTestServerWithConfig(t, config.Config{
		LogStore:         t.TempDir(),
		DefaultTimeoutMs: 10_000,
		MCPServers: []config.MCPServer{{
			Name:    "broken",
			Command: "definitely-not-a-real-command",
		}},
		Workspaces: []config.Workspace{{
			ID:    "ws",
			Roots: []string{root},
			CWD:   root,
		}},
	})

	if tool := s.impl.ListTools()["broken__echo_remote"]; tool != nil {
		t.Fatalf("failed relay tool should not be registered")
	}
}

func newTestServer(t *testing.T, root string, allowlist []string) *Server {
	t.Helper()
	return newTestServerWithConfig(t, config.Config{
		LogStore:         t.TempDir(),
		DefaultTimeoutMs: 10_000,
		Workspaces: []config.Workspace{
			{
				ID:               "ws",
				Roots:            []string{root},
				CWD:              root,
				CommandAllowlist: allowlist,
			},
		},
	})
}

func newTestServerWithConfig(t *testing.T, cfg config.Config) *Server {
	t.Helper()
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	s, err := New(application, "localgate", "test", "ws")
	if err != nil {
		t.Fatalf("mcpgo.New: %v", err)
	}
	return s
}

func callReq(args any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: args,
		},
	}
}

func payloadFromResult(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	payload, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content missing: %#v", res)
	}
	return payload
}

func statusFromPayload(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	return fmt.Sprint(payload["status"])
}

func waitForTaskResult(t *testing.T, s *Server, taskID string, timeout time.Duration) *mcp.CallToolResult {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		res, err := s.handleTaskResult(context.Background(), callReq(map[string]any{
			"task_id": taskID,
		}))
		if err != nil {
			t.Fatalf("task_result: %v", err)
		}
		payload := payloadFromResult(t, res)
		if ready, _ := payload["ready"].(bool); ready {
			return res
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task_result timeout for %s", taskID)
	return nil
}

func fakeServerDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "testutil", "fake_mcp_server"))
}

func TestDeployToolRegisteredAndWorksInMCPGo(t *testing.T) {
	workspaceRoot := t.TempDir()
	storeRoot := t.TempDir()
	s := newTestServerWithConfig(t, config.Config{
		LogStore: storeRoot,
		HTTP:     config.HTTP{BaseURL: "http://127.0.0.1:8000"},
		Deployments: config.DeploymentsConfig{PublicBaseURL: "https://apps.test", PrivateBaseURL: "https://private.apps.test", DefaultAccess: "public",
			Static: config.StaticHosting{
				MaxFiles:      100,
				MaxTotalBytes: 1 << 20,
				MaxFileBytes:  1 << 20,
			},
		},
		Workspaces: []config.Workspace{{
			ID:    "ws",
			Roots: []string{workspaceRoot},
			CWD:   workspaceRoot,
		}},
	})

	registered := s.impl.ListTools()["deploy_tool"]
	if registered == nil {
		t.Fatal("deploy_tool should be registered in standard MCP")
	}
	var schema map[string]any
	if err := json.Unmarshal(registered.Tool.RawInputSchema, &schema); err != nil {
		t.Fatalf("unmarshal deploy schema: %v", err)
	}
	properties, _ := schema["properties"].(map[string]any)
	options, _ := properties["options"].(map[string]any)
	optionProperties, _ := options["properties"].(map[string]any)
	if optionProperties["preserve_source"] == nil {
		t.Fatalf("deploy schema should expose preserve_source: %#v", schema)
	}

	source := filepath.Join(workspaceRoot, "dist")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("standard-mcp"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	res, err := s.makeInvokeHandler("deploy_tool")(context.Background(), callReq(map[string]any{
		"type":   "static",
		"name":   "mcp-site",
		"source": source,
		"options": map[string]any{
			"preserve_source": true,
		},
	}))
	if err != nil {
		t.Fatalf("invoke deploy_tool: %v", err)
	}
	payload := payloadFromResult(t, res)
	if got := statusFromPayload(payload); got != "succeeded" {
		t.Fatalf("unexpected deployment status: %s payload=%#v", got, payload)
	}
	result, ok := payload["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing deployment result: %#v", payload)
	}
	if result["site"] != "mcp-site" || result["source_preserved"] != true {
		t.Fatalf("unexpected deployment result: %#v", result)
	}
	deployed := filepath.Join(storeRoot, "static", "mcp-site", "index.html")
	if got, err := os.ReadFile(deployed); err != nil || string(got) != "standard-mcp" {
		t.Fatalf("deployed file missing: content=%q err=%v", got, err)
	}

	records, err := s.App.Approvals.QueryAudit(map[string]string{"tool": "deploy_tool"}, 20, 0)
	if err != nil {
		t.Fatalf("query deployment audit: %v", err)
	}
	actions := map[string]bool{}
	for _, record := range records {
		actions[record.Action] = true
	}
	if !actions["execution_started"] || !actions["execution_succeeded"] {
		t.Fatalf("missing standard MCP deployment audit: %#v", records)
	}
}

func TestDeployToolReturnsPendingApprovalInMCPGo(t *testing.T) {
	workspaceRoot := t.TempDir()
	storeRoot := t.TempDir()
	s := newTestServerWithConfig(t, config.Config{
		LogStore: storeRoot,
		HTTP:     config.HTTP{BaseURL: "http://127.0.0.1:8000"},
		Deployments: config.DeploymentsConfig{
			Static: config.StaticHosting{
				MaxFiles:        100,
				MaxTotalBytes:   1 << 20,
				MaxFileBytes:    1 << 20,
				RequireApproval: true,
			},
		},
		Workspaces: []config.Workspace{{
			ID:    "ws",
			Roots: []string{workspaceRoot},
			CWD:   workspaceRoot,
		}},
	})

	source := filepath.Join(workspaceRoot, "dist")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("pending"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	res, err := s.makeInvokeHandler("deploy_tool")(context.Background(), callReq(map[string]any{
		"type":   "static",
		"name":   "pending-site",
		"source": source,
	}))
	if err != nil {
		t.Fatalf("invoke deploy_tool: %v", err)
	}
	payload := payloadFromResult(t, res)
	if got := statusFromPayload(payload); got != "pending_approval" {
		t.Fatalf("expected pending approval, got %s payload=%#v", got, payload)
	}
	approvalID, _ := payload["approval_id"].(string)
	if approvalID == "" {
		t.Fatalf("missing approval id: %#v", payload)
	}
	request, err := s.App.Approvals.Get(approvalID)
	if err != nil {
		t.Fatalf("load approval: %v", err)
	}
	if request.Tool != "deploy_tool" || request.Execution != "app_tool" {
		t.Fatalf("unexpected approval request: %#v", request)
	}
	if _, err := os.Stat(filepath.Join(storeRoot, "static", "pending-site")); !os.IsNotExist(err) {
		t.Fatalf("site must not deploy before approval: %v", err)
	}
}
