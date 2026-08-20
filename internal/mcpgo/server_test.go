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
	"localgate/internal/approval"
	"localgate/internal/config"
	"localgate/internal/core"

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

func TestNormalMCPExposesStableNineToolSurface(t *testing.T) {
	root := t.TempDir()
	s := newTestServerWithConfig(t, config.Config{
		LogStore:         t.TempDir(),
		DefaultTimeoutMs: 10_000,
		HTTP:             config.HTTP{BaseURL: "http://127.0.0.1:8000"},
		FileExports: config.FileExports{
			Enabled:           true,
			BaseURL:           "http://127.0.0.1:8000",
			DefaultTTLSeconds: 300,
			MaxTTLSeconds:     600,
			MaxFileBytes:      1 << 20,
		},
		Root: config.Root{Path: root,
			CommandAllowlist: []string{"echo"},
		},
	})

	want := map[string]bool{
		"get_startup_instructions": true,
		"get_skill":                true,
		"get_tool_schema":          true,
		"invoke_tool":              true,
		"read_file":                true,
		"apply_patch":              true,
		"run_command":              true,
		"upload_file":              true,
		"export_file":              true,
	}
	got := s.impl.ListTools()
	if len(got) != len(want) {
		t.Fatalf("expected stable %d-tool MCP surface, got %d: %#v", len(want), len(got), got)
	}
	for name := range want {
		if got[name] == nil {
			t.Fatalf("missing direct MCP tool %q: %#v", name, got)
		}
	}
	for _, progressive := range []string{"write_file", "exec_command", "task_result", "deploy_tool", "send_notification"} {
		if got[progressive] != nil {
			t.Fatalf("progressive tool %q should not be directly MCP-exposed", progressive)
		}
	}
}

func TestSubagentSpawnProgressivelyInvokableInMCPGo(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "opencli-fake")
	if err := os.WriteFile(executable, []byte(`#!/bin/sh
set -eu
printf '%s\n' '[{"conversationId":"conversation123","conversationUrl":"https://chatgpt.com/g/g-p-deadbeef/c/conversation123","response":"delegated"}]'
`), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.LogStore = filepath.Join(root, ".localgate")
	cfg.Subagents.Enabled = true
	cfg.Subagents.DefaultProvider = "opencli_chatgpt"
	cfg.Subagents.Providers.OpenCLIChatGPT.Executable = executable
	cfg.Subagents.Providers.OpenCLIChatGPT.DefaultProject = "deadbeef"
	cfg.Root = config.Root{Path: root}
	s := newTestServerWithConfig(t, cfg)
	if s.impl.ListTools()["subagent_spawn"] != nil || s.impl.ListTools()["subagent_send"] != nil || s.impl.ListTools()["subagent_poll"] != nil || s.impl.ListTools()["subagent_wait"] != nil || s.impl.ListTools()["subagent_archive"] != nil {
		t.Fatalf("subagent tools should not be directly MCP-exposed: %#v", s.impl.ListTools())
	}
	if s.impl.ListTools()["invoke_tool"] == nil {
		t.Fatal("invoke_tool should be registered")
	}

	res, err := s.handleInvokeTool(context.Background(), callReq(map[string]any{
		"tool": "subagent_spawn",
		"arguments": map[string]any{
			"name":        "reviewer",
			"instruction": "review code",
		},
	}))
	if err != nil {
		t.Fatalf("invoke subagent_spawn: %v", err)
	}
	payload := payloadFromResult(t, res)
	result, _ := payload["result"].(map[string]any)
	if statusFromPayload(payload) != "succeeded" || result["accepted"] != true || result["status"] != "busy" {
		t.Fatalf("unexpected MCP subagent reply: %#v", payload)
	}
	if id, _ := result["subagent_id"].(string); !strings.HasPrefix(id, "sg_") {
		t.Fatalf("unexpected MCP subagent id: %#v", payload)
	}
}

func TestRegisterCoreToolsExposeOnlyBase64dataSchemaWhenEnabled(t *testing.T) {
	root := t.TempDir()
	s := newTestServerWithConfig(t, config.Config{
		LogStore:         t.TempDir(),
		DefaultTimeoutMs: 10_000,
		ToolArguments:    config.ToolArguments{EnableBase64: true},
		Root: config.Root{Path: root,
			CommandAllowlist: []string{"echo", "sh"},
		},
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
	if !strings.Contains(prompt, "localgate-task-session") || !strings.Contains(prompt, "loader: `get_skill`") {
		t.Fatalf("missing built-in skill routing in prompt: %#v", result)
	}
	switch tools := result["tools"].(type) {
	case []map[string]any:
		if len(tools) == 0 {
			t.Fatalf("missing visible tools in result: %#v", result)
		}
		foundRun := false
		for _, tool := range tools {
			if tool["name"] == "run_command" {
				foundRun = true
				break
			}
		}
		if !foundRun {
			t.Fatalf("run_command should be in structured tools result: %#v", result)
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

func TestGetSkillRegisteredAndWorksInMCPGo(t *testing.T) {
	root := t.TempDir()
	s := newTestServer(t, root, []string{"echo", "sh"})
	if tool := s.impl.ListTools()["get_skill"]; tool == nil {
		t.Fatal("get_skill tool not registered")
	} else if tool.Tool.Description == "" {
		t.Fatal("get_skill description should not be empty")
	}

	res, err := s.makeInvokeHandler("get_skill")(context.Background(), callReq(map[string]any{"name": "localgate-task-session"}))
	if err != nil {
		t.Fatalf("invoke get_skill: %v", err)
	}
	payload := payloadFromResult(t, res)
	if got := statusFromPayload(payload); got != "succeeded" {
		t.Fatalf("unexpected status: %s payload=%#v", got, payload)
	}
	result, ok := payload["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing result payload: %#v", payload)
	}
	if result["name"] != "localgate-task-session" || result["source"] != "builtin" {
		t.Fatalf("unexpected get_skill metadata: %#v", result)
	}
	content, _ := result["content"].(string)
	if !strings.Contains(content, "# LocalGate Task and Session Lifecycle") {
		t.Fatalf("missing embedded skill content: %#v", result)
	}
}

func TestToolHTTPModeOnlyRegistersBootstrapToolsInMCPGo(t *testing.T) {
	root := t.TempDir()
	s := newTestServerWithConfig(t, config.Config{
		LogStore: filepath.Join(root, "store"),
		HTTP:     config.HTTP{BaseURL: "http://127.0.0.1:8000"},
		ToolHTTPMode: config.ToolHTTPMode{
			Enabled:               true,
			AccessTokenTTLSeconds: 600,
		},
		Root: config.Root{Path: root},
	})

	tools := s.impl.ListTools()
	if len(tools) != 4 {
		t.Fatalf("expected exactly 4 bootstrap tools, got %#v", tools)
	}
	if tools["get_startup_instructions"] == nil || tools["get_skill"] == nil || tools["get_temporary_access_token"] == nil || tools["get_tool_schema"] == nil {
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
		Root: config.Root{Path: root,
			CommandAllowlist: []string{"echo", "sh"},
		},
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
		Root: config.Root{Path: root,
			CommandAllowlist: []string{"echo", "sh"},
		},
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
		Root: config.Root{Path: root,
			CommandAllowlist: []string{"echo", "sh"},
		},
	})
	for name, property := range map[string]string{"get_tool_schema": "tool", "get_skill": "name"} {
		tool := s.impl.ListTools()[name]
		if tool == nil {
			t.Fatalf("%s tool not registered", name)
		}
		var schema map[string]any
		if err := json.Unmarshal(tool.Tool.RawInputSchema, &schema); err != nil {
			t.Fatalf("unmarshal %s schema: %v", name, err)
		}
		props, _ := schema["properties"].(map[string]any)
		if _, ok := props["base64data"]; ok {
			t.Fatalf("%s should not expose base64data schema: %#v", name, schema)
		}
		if _, ok := props[property]; !ok {
			t.Fatalf("%s should preserve %s property: %#v", name, property, schema)
		}
		if name == "get_skill" {
			if _, ok := props["async"]; ok {
				t.Fatalf("get_skill should remain an immediate bootstrap read: %#v", schema)
			}
		}
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
		Root:             config.Root{Path: root},
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
		Root:             config.Root{Path: root},
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
		Root:             config.Root{Path: root},
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

	waitRes, err := s.handleInvokeTool(context.Background(), callReq(map[string]any{
		"tool": "wait_task_finish",
		"arguments": map[string]any{
			"task_id":    taskID,
			"timeout_ms": 2000,
		},
	}))
	if err != nil {
		t.Fatalf("wait_task_finish: %v", err)
	}
	waitEnvelope := payloadFromResult(t, waitRes)
	waitPayload, _ := waitEnvelope["result"].(map[string]any)
	if waitPayload == nil {
		t.Fatalf("missing progressive wait result: %#v", waitEnvelope)
	}
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

func TestRegisterCoreToolsDoesNotDirectlyExposeRelayTools(t *testing.T) {
	root := t.TempDir()
	s := newTestServerWithConfig(t, config.Config{
		LogStore:         t.TempDir(),
		DefaultTimeoutMs: 10_000,
		MCPServers: []config.MCPServer{{
			Name:    "fake",
			Command: "go",
			Args:    []string{"run", fakeServerDir(t)},
		}},
		Root: config.Root{Path: root},
	})

	if tool := s.impl.ListTools()["fake__echo_remote"]; tool != nil {
		t.Fatalf("relay tool should not be directly MCP-exposed")
	}
}

func TestRelayToolProgressivelyInvokesAndPreservesNativeContent(t *testing.T) {
	root := t.TempDir()
	s := newTestServerWithConfig(t, config.Config{
		LogStore:         t.TempDir(),
		DefaultTimeoutMs: 10_000,
		MCPServers: []config.MCPServer{{
			Name:    "fake",
			Command: "go",
			Args:    []string{"run", fakeServerDir(t)},
		}},
		Root: config.Root{Path: root},
	})

	if s.impl.ListTools()["fake__echo_remote"] != nil {
		t.Fatal("relay capability should not be directly MCP-exposed")
	}
	if _, ok := s.App.Registry.Get("fake__echo_remote"); !ok {
		t.Fatal("relay capability should be registered for progressive invocation")
	}
	res, err := s.handleInvokeTool(context.Background(), callReq(map[string]any{
		"tool":      "fake__echo_remote",
		"arguments": map[string]any{"text": "hello"},
	}))
	if err != nil {
		t.Fatalf("invoke relay tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("relay call should succeed: %#v", res)
	}
	payload := payloadFromResult(t, res)
	result, _ := payload["result"].(map[string]any)
	structured, _ := result["structured_content"].(map[string]any)
	if structured["echo"] != "hello" {
		t.Fatalf("unexpected relay structured result: %#v", payload)
	}
	foundNativeText := false
	for _, content := range res.Content {
		if text, ok := content.(mcp.TextContent); ok && text.Text == "echo:hello" {
			foundNativeText = true
		}
	}
	if !foundNativeText {
		t.Fatalf("relay native MCP text content should be preserved: %#v", res.Content)
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
		Root: config.Root{Path: root},
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
		Root: config.Root{Path: root,
			CommandAllowlist: allowlist,
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
	s, err := New(application, "localgate", "test")
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
		task, err := s.App.Tasks.Get(taskID)
		if err != nil {
			t.Fatalf("task_result: %v", err)
		}
		if isTerminalStatus(task.Status) {
			payload := buildTaskPayload(task)
			payload["ready"] = true
			appendTaskStreams(payload, s.App.Sink, taskID, true)
			return newStructuredToolResult(payload, isErrorStatus(task.Status))
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

func TestDeployToolProgressivelyInvokableInMCPGo(t *testing.T) {
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
		Root: config.Root{Path: workspaceRoot},
	})

	if registered := s.impl.ListTools()["deploy_tool"]; registered != nil {
		t.Fatal("deploy_tool should not be directly exposed in standard MCP")
	}
	schemaTool, ok := s.App.Registry.Get("get_tool_schema")
	if !ok {
		t.Fatal("get_tool_schema missing from registry")
	}
	schemaRes, err := schemaTool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"tool": "deploy_tool"})
	if err != nil {
		t.Fatalf("get deploy schema: %v", err)
	}
	schema, _ := schemaRes["schema"].(map[string]any)
	properties, _ := schema["properties"].(map[string]any)
	options, _ := properties["options"].(map[string]any)
	optionProperties, _ := options["properties"].(map[string]any)
	if optionProperties["preserve_source"] == nil {
		t.Fatalf("deploy schema should expose preserve_source: %#v", schemaRes)
	}

	source := filepath.Join(workspaceRoot, "dist")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("standard-mcp"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	res, err := s.handleInvokeTool(context.Background(), callReq(map[string]any{
		"tool": "deploy_tool",
		"arguments": map[string]any{
			"type":   "static",
			"name":   "mcp-site",
			"source": source,
			"options": map[string]any{
				"preserve_source": true,
			},
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
		Admin: config.AdminConfig{
			Enabled: true,
			Approval: config.ApprovalConfig{
				Enabled:            true,
				PendingWaitSeconds: 0,
			},
		},
		Deployments: config.DeploymentsConfig{
			PublicBaseURL:  "https://apps.test",
			PrivateBaseURL: "https://private.apps.test",
			DefaultAccess:  "public",
			Static: config.StaticHosting{
				MaxFiles:        100,
				MaxTotalBytes:   1 << 20,
				MaxFileBytes:    1 << 20,
				RequireApproval: true,
			},
		},
		Root: config.Root{Path: workspaceRoot},
	})

	source := filepath.Join(workspaceRoot, "dist")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("pending"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	res, err := s.handleInvokeTool(context.Background(), callReq(map[string]any{
		"tool": "deploy_tool",
		"arguments": map[string]any{
			"type":   "static",
			"name":   "pending-site",
			"source": source,
		},
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

func TestDeployToolWaitsForApprovalAndReturnsResultInMCPGo(t *testing.T) {
	workspaceRoot := t.TempDir()
	storeRoot := t.TempDir()
	s := newTestServerWithConfig(t, config.Config{
		LogStore: storeRoot,
		HTTP:     config.HTTP{BaseURL: "http://127.0.0.1:8000"},
		Admin: config.AdminConfig{
			Enabled: true,
			BaseURL: "http://admin.test",
			Approval: config.ApprovalConfig{
				Enabled:            true,
				PendingWaitSeconds: 1,
			},
		},
		Deployments: config.DeploymentsConfig{
			PublicBaseURL:  "https://apps.test",
			PrivateBaseURL: "https://private.apps.test",
			DefaultAccess:  "public",
			Static: config.StaticHosting{
				MaxFiles:        100,
				MaxTotalBytes:   1 << 20,
				MaxFileBytes:    1 << 20,
				RequireApproval: true,
			},
		},
		Root: config.Root{Path: workspaceRoot},
	})

	source := filepath.Join(workspaceRoot, "dist")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("approved-inline"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	type invokeResult struct {
		result *mcp.CallToolResult
		err    error
	}
	resultCh := make(chan invokeResult, 1)
	go func() {
		result, err := s.handleInvokeTool(context.Background(), callReq(map[string]any{
			"tool": "deploy_tool",
			"arguments": map[string]any{
				"type":   "static",
				"name":   "inline-approved-site",
				"source": source,
			},
		}))
		resultCh <- invokeResult{result: result, err: err}
	}()

	var request approval.Request
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		pending, err := s.App.Approvals.List("")
		if err != nil {
			t.Fatalf("list approvals: %v", err)
		}
		if len(pending) > 0 {
			request = pending[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if request.ID == "" {
		t.Fatal("approval was not created within the synchronous wait window")
	}
	if _, err := s.App.ApproveApproval(request.ID, approval.Actor{ID: "tester", AuthMethod: "test"}); err != nil {
		t.Fatalf("approve: %v", err)
	}

	select {
	case invoked := <-resultCh:
		if invoked.err != nil {
			t.Fatalf("invoke deploy_tool: %v", invoked.err)
		}
		payload := payloadFromResult(t, invoked.result)
		if got := statusFromPayload(payload); got != "succeeded" {
			t.Fatalf("expected direct succeeded result, got %s payload=%#v", got, payload)
		}
		if payload["task_id"] != request.TaskID {
			t.Fatalf("expected original gated task %s, got %#v", request.TaskID, payload["task_id"])
		}
		result, _ := payload["result"].(map[string]any)
		if result["site"] != "inline-approved-site" {
			t.Fatalf("unexpected deployment result: %#v", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("tool invocation did not return after approval")
	}
}
