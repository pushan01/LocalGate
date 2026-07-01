package meta

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"localgate/internal/config"
	"localgate/internal/core"
)

type dummyTool struct {
	name string
	desc string
}

func (t dummyTool) Name() string        { return t.name }
func (t dummyTool) Description() string { return t.desc }
func (t dummyTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{},
	}
}
func (t dummyTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	return map[string]any{"ok": true}, nil
}

func TestStartupInstructionsToolReturnsPromptAndVisibleTools(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	writeGlobalSkillFixture(t, home, "alpha", "---\nname: alpha\ndescription: Alpha skill\n---\n")
	writeGlobalSkillFixture(t, home, "beta", "---\nname: beta\ndescription: Beta skill\n---\n")
	writeLocalSkillFixture(t, workspace, "shadow", "---\nname: alpha\ndescription: Shadowed local skill\n---\n")
	writeLocalSkillFixture(t, workspace, "local-only", "---\nname: local-only\ndescription: Local only skill\n---\n")
	writeLocalSkillFixture(t, workspace, "hidden", "---\nname: hidden\ndescription: Hidden skill\ndisable-model-invocation: true\n---\n")
	ws := config.Workspace{
		ID:    "ws",
		Roots: []string{workspace},
		CWD:   workspace,
	}
	reg := core.NewRegistry(nil)
	reg.Register(dummyTool{name: "read_file", desc: "Read file contents"})
	reg.Register(dummyTool{name: "write_file", desc: "Write content to a file"})
	reg.Register(dummyTool{name: "run_argv", desc: "Hidden tool"})
	reg.Hide("run_argv")

	tool := NewStartupInstructionsTool(ws, reg, StartupPromptOptions{})
	reg.Register(tool)

	res, err := tool.Execute(context.Background(), core.InvokeContext{WorkspaceID: "ws"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	prompt, _ := res["prompt"].(string)
	if !strings.Contains(prompt, "LocalGate使用说明") {
		t.Fatalf("missing service title in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, workspace) {
		t.Fatalf("missing rendered cwd in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "`read_file`") {
		t.Fatalf("missing visible tool name in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "/bin/zsh") {
		t.Fatalf("missing shell in rendered prompt: %q", prompt)
	}
	if !strings.Contains(prompt, time.Now().Location().String()) {
		t.Fatalf("missing timezone in rendered prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "`alpha`") || !strings.Contains(prompt, "Alpha skill") {
		t.Fatalf("missing injected skill metadata in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "local-only") {
		t.Fatalf("missing local skill in prompt: %q", prompt)
	}
	if strings.Contains(prompt, "Shadowed local skill") || strings.Contains(prompt, "`hidden`") {
		t.Fatalf("prompt should respect de-duplication and disable-model-invocation: %q", prompt)
	}
	if strings.Contains(prompt, "{{") || strings.Contains(prompt, "{%") {
		t.Fatalf("template syntax should be rendered: %q", prompt)
	}
	if !strings.Contains(prompt, "`apply_patch` 必须传一个完整的 patch 字符串") {
		t.Fatalf("prompt should explain apply_patch patch envelope: %q", prompt)
	}
	if !strings.Contains(prompt, "*** Update File: /abs/path/to/file.txt") {
		t.Fatalf("prompt should include apply_patch example: %q", prompt)
	}
	if !strings.Contains(prompt, "`create_upload_token` 和 `create_download_token` 适用于跨多个 sandbox、跨机器传输文件") {
		t.Fatalf("prompt should explain upload/download transfer scenarios: %q", prompt)
	}
	if !strings.Contains(prompt, "读取返回里的 `upload_url`、`auth_header_name`、`auth_value`") {
		t.Fatalf("prompt should explain upload credential fields: %q", prompt)
	}
	if !strings.Contains(prompt, "调用 `create_download_token` 获取 `download_url`") {
		t.Fatalf("prompt should explain create_download_token usage: %q", prompt)
	}
	if !strings.Contains(prompt, "输出字段刻意不用 `token` 命名") {
		t.Fatalf("prompt should explain non-token field naming: %q", prompt)
	}
	if !strings.Contains(prompt, "`server__tool` 形式出现在 `tools/list`") {
		t.Fatalf("prompt should explain relay tool naming: %q", prompt)
	}
	if !strings.Contains(prompt, "不要把它们当成本地 filesystem/shell 工具处理") {
		t.Fatalf("prompt should explain relay tool boundaries: %q", prompt)
	}
	if schema := tool.Schema(); schema == nil {
		t.Fatalf("startup instructions schema should be non-nil")
	} else if m, ok := schema.(map[string]any); !ok {
		t.Fatalf("unexpected schema type: %#v", schema)
	} else if _, ok := m["properties"]; ok {
		t.Fatalf("startup instructions schema should not define parameters: %#v", m)
	}

	tools, ok := res["tools"].([]map[string]any)
	if !ok {
		t.Fatalf("unexpected tools type: %#v", res["tools"])
	}
	if len(tools) != 2 {
		t.Fatalf("unexpected tools len: %#v", tools)
	}
	if tools[0]["name"] != "read_file" || tools[1]["name"] != "write_file" {
		t.Fatalf("tools should be sorted and visible only: %#v", tools)
	}
	if tools[0]["description"] != "Read file contents" || tools[1]["description"] != "Write content to a file" {
		t.Fatalf("tool descriptions should be preserved in result payload: %#v", tools)
	}
	for _, tool := range tools {
		if tool["name"] == "run_argv" {
			t.Fatalf("hidden tool should not appear in visible tool list: %#v", tools)
		}
	}
	if len(res) != 2 {
		t.Fatalf("startup instructions should only return prompt and tools: %#v", res)
	}
}

func TestRenderEmbeddedPromptTemplate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	writeGlobalSkillFixture(t, home, "gamma", "---\nname: gamma\ndescription: Gamma skill\n---\n")
	ws := config.Workspace{
		ID:    "ws",
		Roots: []string{workspace},
		CWD:   workspace,
	}
	reg := core.NewRegistry(nil)
	reg.Register(dummyTool{name: "read_file", desc: "Read file contents"})
	reg.Register(dummyTool{name: "run_command", desc: "Run a shell command"})
	reg.Register(dummyTool{name: "deploy_tool", desc: "Deploy a static site"})

	tool := NewStartupInstructionsTool(ws, reg, StartupPromptOptions{})
	reg.Register(tool)

	res, err := tool.Execute(context.Background(), core.InvokeContext{WorkspaceID: "ws"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	prompt, _ := res["prompt"].(string)
	if !strings.Contains(prompt, "run_command") {
		t.Fatalf("missing second tool in rendered prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "`deploy_tool` 使用说明") {
		t.Fatalf("missing deploy tool guidance in rendered prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "options.preserve_source") {
		t.Fatalf("missing deploy source preservation guidance in rendered prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "status=pending_approval") {
		t.Fatalf("missing deploy approval guidance in rendered prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "不要重复提交原工具调用") || !strings.Contains(prompt, "task_result") {
		t.Fatalf("missing approval continuation guidance in rendered prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Gamma skill") {
		t.Fatalf("missing dynamic skill description in rendered prompt: %q", prompt)
	}
}

func TestStartupInstructionsToolToolHTTPMode(t *testing.T) {
	ws := config.Workspace{
		ID:    "ws",
		Roots: []string{t.TempDir()},
		CWD:   t.TempDir(),
	}
	reg := core.NewRegistry(nil)
	reg.Register(dummyTool{name: "read_file", desc: "Read file contents"})
	reg.Register(dummyTool{name: "get_temporary_access_token", desc: "Issue tool credential"})
	reg.Register(dummyTool{name: "get_tool_schema", desc: "Read tool schema"})
	tool := NewStartupInstructionsTool(ws, reg, StartupPromptOptions{
		BaseURL:      "http://127.0.0.1:8000",
		ToolHTTPMode: true,
	})
	reg.Register(tool)

	res, err := tool.Execute(context.Background(), core.InvokeContext{WorkspaceID: "ws"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	prompt, _ := res["prompt"].(string)
	if !strings.Contains(prompt, "工具 HTTP 化模式") {
		t.Fatalf("missing tool http mode summary: %q", prompt)
	}
	if !strings.Contains(prompt, "get_temporary_access_token") {
		t.Fatalf("missing tool access tool: %q", prompt)
	}
	if !strings.Contains(prompt, "get_tool_schema") {
		t.Fatalf("missing get_tool_schema guidance: %q", prompt)
	}
	if !strings.Contains(prompt, "/tools/{tool}/call") {
		t.Fatalf("missing tool HTTP call guidance: %q", prompt)
	}
	if !strings.Contains(prompt, "X-LocalGate-Tool-Access") {
		t.Fatalf("missing tool auth header guidance: %q", prompt)
	}
	if strings.Contains(prompt, "/platforms/") {
		t.Fatalf("legacy platform routing should not appear: %q", prompt)
	}
	tools, ok := res["tools"].([]map[string]any)
	if !ok {
		t.Fatalf("unexpected tools type: %#v", res["tools"])
	}
	if len(tools) != 1 || tools[0]["name"] != "read_file" {
		t.Fatalf("tool http mode should expose business tools, not bootstrap tools: %#v", tools)
	}
	if len(tools[0]) != 2 {
		t.Fatalf("tool http mode startup payload should only include name and description: %#v", tools)
	}
}

func TestStartupInstructionsToolRequiresBase64WhenEnabled(t *testing.T) {
	ws := config.Workspace{
		ID:    "ws",
		Roots: []string{t.TempDir()},
		CWD:   t.TempDir(),
	}
	reg := core.NewRegistry(nil)
	reg.Register(dummyTool{name: "read_file", desc: "Read file contents"})
	tool := NewStartupInstructionsTool(ws, reg, StartupPromptOptions{
		BaseURL:      "http://127.0.0.1:8000",
		ToolHTTPMode: true,
		EnableBase64: true,
	})
	reg.Register(tool)

	res, err := tool.Execute(context.Background(), core.InvokeContext{WorkspaceID: "ws"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	prompt, _ := res["prompt"].(string)
	if !strings.Contains(prompt, "请把整个工具参数编码为一个 base64 字符串后再提交") {
		t.Fatalf("startup prompt should require base64 in tool http mode when enabled: %q", prompt)
	}
	if strings.Contains(prompt, "还可以整体传成一个 base64 字符串") {
		t.Fatalf("startup prompt should not describe base64 as optional when enabled: %q", prompt)
	}
}

func TestLoadAllSkillsSupportsGlobalAndLocalLayouts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()

	writeGlobalSkillFixture(t, home, "global-dir", "---\nname: global-dir\ndescription: From global dir\n---\n")
	writeFile(t, filepath.Join(workspace, ".agents", "skills", "flat.md"), "---\nname: flat\ndescription: Flat local file\n---\n")
	writeFile(t, filepath.Join(workspace, ".agents", "skills", "nested", "SKILL.md"), "---\nname: nested\ndescription: Nested local skill\n---\n")
	writeFile(t, filepath.Join(workspace, ".agents", "skills", "dupe", "SKILL.md"), "---\nname: global-dir\ndescription: Local duplicate\n---\n")

	roots, skills := loadAllSkills(workspace)
	if len(roots) != 2 {
		t.Fatalf("unexpected roots: %#v", roots)
	}
	if len(skills) != 3 {
		t.Fatalf("unexpected loaded skills: %#v", skills)
	}

	_, visible := visibleSkillsForPrompt(workspace)
	if len(visible) != 3 {
		t.Fatalf("unexpected visible skills: %#v", visible)
	}
	if visible[0]["name"] != "flat" || visible[1]["name"] != "global-dir" || visible[2]["name"] != "nested" {
		t.Fatalf("unexpected visible skill ordering: %#v", visible)
	}
	if visible[1]["description"] != "From global dir" {
		t.Fatalf("global skill should win on duplicate names: %#v", visible)
	}
}

func TestLoadAllSkillsFollowsSkillSymlinks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()

	globalTarget := filepath.Join(home, "real-global-skills")
	if err := os.MkdirAll(filepath.Join(globalTarget, "global-linked"), 0o755); err != nil {
		t.Fatalf("mkdir global target: %v", err)
	}
	writeFile(t, filepath.Join(globalTarget, "global-linked", "SKILL.md"), "---\nname: global-linked\ndescription: Global linked skill\n---\n")
	if err := os.MkdirAll(filepath.Join(home, ".agents"), 0o755); err != nil {
		t.Fatalf("mkdir global parent: %v", err)
	}
	if err := os.Symlink(globalTarget, filepath.Join(home, ".agents", "skills")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	localTarget := filepath.Join(workspace, "real-local-skill")
	if err := os.MkdirAll(localTarget, 0o755); err != nil {
		t.Fatalf("mkdir local target: %v", err)
	}
	writeFile(t, filepath.Join(localTarget, "SKILL.md"), "---\nname: local-linked\ndescription: Local linked skill\n---\n")
	localSkillsRoot := filepath.Join(workspace, ".agents", "skills")
	if err := os.MkdirAll(localSkillsRoot, 0o755); err != nil {
		t.Fatalf("mkdir local skills root: %v", err)
	}
	if err := os.Symlink(localTarget, filepath.Join(localSkillsRoot, "linked")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	_, skills := loadAllSkills(workspace)
	if len(skills) != 2 {
		t.Fatalf("unexpected skills loaded through symlink: %#v", skills)
	}

	_, visible := visibleSkillsForPrompt(workspace)
	if len(visible) != 2 {
		t.Fatalf("unexpected visible skills loaded through symlink: %#v", visible)
	}
	if visible[0]["name"] != "global-linked" || visible[1]["name"] != "local-linked" {
		t.Fatalf("unexpected visible skill ordering through symlink: %#v", visible)
	}
}

func writeGlobalSkillFixture(t *testing.T, home, name, content string) {
	t.Helper()
	path := filepath.Join(home, ".agents", "skills", name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir skill fixture: %v", err)
	}
	writeFile(t, filepath.Join(path, "SKILL.md"), content)
}

func writeLocalSkillFixture(t *testing.T, workspace, name, content string) {
	t.Helper()
	path := filepath.Join(workspace, ".agents", "skills", name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir local skill fixture: %v", err)
	}
	writeFile(t, filepath.Join(path, "SKILL.md"), content)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}
