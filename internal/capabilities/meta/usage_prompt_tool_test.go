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
	t.Setenv("TZ", "Asia/Singapore")
	t.Setenv("PATH", "/usr/bin:/bin")
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	writeGlobalSkillFixture(t, home, "alpha", "---\nname: alpha\ndescription: Alpha skill\n---\n")
	writeGlobalSkillFixture(t, home, "beta", "---\nname: beta\ndescription: Beta skill\n---\n")
	writeLocalSkillFixture(t, workspace, "shadow", "---\nname: alpha\ndescription: Shadowed local skill\n---\n")
	writeLocalSkillFixture(t, workspace, "local-only", "---\nname: local-only\ndescription: Local only skill\n---\n")
	writeLocalSkillFixture(t, workspace, "hidden", "---\nname: hidden\ndescription: Hidden skill\ndisable-model-invocation: true\n---\n")
	ws := config.Root{Path: workspace}
	reg := core.NewRegistry(nil)
	reg.Register(dummyTool{name: "read_file", desc: "Read file contents"})
	reg.Register(dummyTool{name: "write_file", desc: "Write content to a file"})
	reg.Register(dummyTool{name: "run_argv", desc: "Hidden tool"})
	reg.Hide("run_argv")

	tool := NewStartupInstructionsTool(ws, reg, StartupPromptOptions{})
	reg.Register(tool)

	res, err := tool.Execute(context.Background(), core.InvokeContext{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	prompt, _ := res["prompt"].(string)
	if !strings.Contains(prompt, "LocalGate 使用说明") {
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
	if !strings.Contains(prompt, "宿主执行环境时区：`Asia/Singapore`") {
		t.Fatalf("missing host timezone label in rendered prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "当前执行环境未检测到 `rg`") || !strings.Contains(prompt, "不要反复尝试 `rg`") {
		t.Fatalf("missing rg fallback guidance in rendered prompt: %q", prompt)
	}
	if strings.Contains(prompt, "GPT-5") || !strings.Contains(prompt, "受控根目录") {
		t.Fatalf("prompt should be model-neutral: %q", prompt)
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
	if strings.Contains(prompt, "`run_argv`") {
		t.Fatalf("hidden tool guidance should be omitted from prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "`localgate-task-session`") || !strings.Contains(prompt, "loader: `get_skill`") {
		t.Fatalf("prompt should expose matching built-in skill metadata: %q", prompt)
	}
	if strings.Contains(prompt, "# LocalGate Task and Session Lifecycle") {
		t.Fatalf("startup should not inline built-in skill bodies: %q", prompt)
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
	if tools[0]["name"] != "invoke_tool" || tools[1]["name"] != "read_file" {
		t.Fatalf("tools should contain only the stable direct MCP summary: %#v", tools)
	}
	if tools[0]["description"] == "" || tools[1]["description"] != "Read file contents" {
		t.Fatalf("tool descriptions should be preserved in result payload: %#v", tools)
	}
	for _, tool := range tools {
		if tool["name"] == "run_argv" || tool["name"] == "write_file" {
			t.Fatalf("progressive tool should not appear in direct MCP list: %#v", tools)
		}
	}
	if !strings.Contains(prompt, "`localgate-filesystem`") {
		t.Fatalf("progressive filesystem capability should route through its built-in skill: %q", prompt)
	}
	if len(res) != 2 {
		t.Fatalf("startup instructions should only return prompt and tools: %#v", res)
	}
}

func TestStartupInstructionsToolOnlyGuidesVisibleTools(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	ws := config.Root{Path: workspace}
	reg := core.NewRegistry(nil)
	for _, tool := range []dummyTool{
		{name: "apply_patch", desc: "Apply a patch"},
		{name: "upload_file", desc: "Upload a file"},
		{name: "export_file", desc: "Export a file"},
		{name: "run_argv", desc: "Run argv"},
		{name: "create_upload_token", desc: "Create upload token"},
		{name: "create_download_token", desc: "Create download token"},
		{name: "deploy_tool", desc: "Deploy an application"},
		{name: "restart_deployment", desc: "Restart a deployment"},
		{name: "rollback_deployment", desc: "Rollback a deployment"},
	} {
		reg.Register(tool)
	}
	for _, name := range []string{"run_argv", "create_upload_token", "create_download_token", "rollback_deployment"} {
		reg.Hide(name)
	}
	tool := NewStartupInstructionsTool(ws, reg, StartupPromptOptions{})
	reg.Register(tool)

	res, err := tool.Execute(context.Background(), core.InvokeContext{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	prompt, _ := res["prompt"].(string)
	for _, hidden := range []string{"`run_argv`", "`create_upload_token`", "`create_download_token`", "`rollback_deployment`"} {
		if strings.Contains(prompt, hidden) {
			t.Fatalf("hidden tool %s should not be mentioned in prompt: %q", hidden, prompt)
		}
	}
	for _, visible := range []string{
		"局部修改已有文本文件优先使用 `apply_patch`",
		"`localgate-deployment`",
		"`localgate-file-transfer`",
		"loader: `get_skill`",
	} {
		if !strings.Contains(prompt, visible) {
			t.Fatalf("visible tool guidance %q is missing: %q", visible, prompt)
		}
	}
	for _, movedToSkill := range []string{"options.preserve_source", "*** Update File: /abs/path/to/file.txt", "使用 `restart_deployment`"} {
		if strings.Contains(prompt, movedToSkill) {
			t.Fatalf("workflow detail %q should be loaded from a skill, not startup: %q", movedToSkill, prompt)
		}
	}
}

func TestStartupInstructionsToolRedactsMCPEnvironmentValues(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	ws := config.Root{Path: workspace}
	reg := core.NewRegistry(nil)
	reg.Register(dummyTool{name: "read_file", desc: "Read file contents"})
	tool := NewStartupInstructionsTool(ws, reg, StartupPromptOptions{
		MCPServers: []config.MCPServer{{
			Name: "example",
			Env: map[string]string{
				"API_TOKEN": "super-secret-token",
				"APP_ENV":   "production",
			},
		}},
	})
	reg.Register(tool)

	res, err := tool.Execute(context.Background(), core.InvokeContext{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	prompt, _ := res["prompt"].(string)
	for _, expected := range []string{"`example`", "`API_TOKEN`", "`APP_ENV`", "值不会写入 startup"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("missing sanitized MCP metadata %q in prompt: %q", expected, prompt)
		}
	}
	for _, secret := range []string{"super-secret-token", "production"} {
		if strings.Contains(prompt, secret) {
			t.Fatalf("MCP environment value leaked into prompt: %q", prompt)
		}
	}
}

func TestFormatUTCOffset(t *testing.T) {
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.FixedZone("Local", 8*60*60))
	if got := formatUTCOffset(now); got != "UTC+08:00" {
		t.Fatalf("unexpected timezone fallback: %q", got)
	}
}

func TestRenderEmbeddedPromptTemplate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	writeGlobalSkillFixture(t, home, "gamma", "---\nname: gamma\ndescription: Gamma skill\n---\n")
	ws := config.Root{Path: workspace}
	reg := core.NewRegistry(nil)
	reg.Register(dummyTool{name: "read_file", desc: "Read file contents"})
	reg.Register(dummyTool{name: "run_command", desc: "Run a shell command"})
	reg.Register(dummyTool{name: "deploy_tool", desc: "Deploy a static site or dynamic application"})
	reg.Register(dummyTool{name: "restart_deployment", desc: "Restart a dynamic deployment"})
	reg.Register(dummyTool{name: "rollback_deployment", desc: "Rollback a dynamic deployment"})

	tool := NewStartupInstructionsTool(ws, reg, StartupPromptOptions{})
	reg.Register(tool)

	res, err := tool.Execute(context.Background(), core.InvokeContext{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	prompt, _ := res["prompt"].(string)
	if !strings.Contains(prompt, "`localgate-deployment`") || !strings.Contains(prompt, "builtin://localgate-deployment/SKILL.md") {
		t.Fatalf("missing deployment skill routing in rendered prompt: %q", prompt)
	}
	for _, movedToSkill := range []string{"options.preserve_source", "runtime.argv", "options.health_path", "# LocalGate Deployment"} {
		if strings.Contains(prompt, movedToSkill) {
			t.Fatalf("deployment detail %q should not be in startup: %q", movedToSkill, prompt)
		}
	}
	if !strings.Contains(prompt, "status=pending_approval") || !strings.Contains(prompt, "不要重复提交原操作") || !strings.Contains(prompt, "invoke_tool") || !strings.Contains(prompt, "get_tool_schema") {
		t.Fatalf("missing approval continuation guidance in rendered prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Gamma skill") {
		t.Fatalf("missing dynamic skill description in rendered prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "完整 Registry 能力通过 Builtin Skill 渐进披露") || !strings.Contains(prompt, "Task lifecycle") {
		t.Fatalf("missing registry/host tool boundary guidance: %q", prompt)
	}
}

func TestStartupInstructionsToolToolHTTPMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws := config.Root{Path: t.TempDir()}
	reg := core.NewRegistry(nil)
	reg.Register(dummyTool{name: "read_file", desc: "Read file contents"})
	reg.Register(dummyTool{name: "write_file", desc: "Write file contents"})
	reg.Hide("write_file")
	reg.Register(dummyTool{name: "get_temporary_access_token", desc: "Issue tool credential"})
	reg.Register(dummyTool{name: "get_tool_schema", desc: "Read tool schema"})
	tool := NewStartupInstructionsTool(ws, reg, StartupPromptOptions{
		BaseURL:      "http://127.0.0.1:8000",
		ToolHTTPMode: true,
	})
	reg.Register(tool)

	res, err := tool.Execute(context.Background(), core.InvokeContext{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	prompt, _ := res["prompt"].(string)
	if !strings.Contains(prompt, "当前模式：Tool HTTP") {
		t.Fatalf("missing tool http mode summary: %q", prompt)
	}
	if !strings.Contains(prompt, "get_skill") || !strings.Contains(prompt, "`localgate-tool-http`") {
		t.Fatalf("missing built-in skill bootstrap guidance: %q", prompt)
	}
	if !strings.Contains(prompt, "get_temporary_access_token") {
		t.Fatalf("missing tool access tool: %q", prompt)
	}
	if !strings.Contains(prompt, "get_tool_schema") {
		t.Fatalf("missing get_tool_schema guidance: %q", prompt)
	}
	if strings.Contains(prompt, "X-LocalGate-Tool-Access") {
		t.Fatalf("tool HTTP auth detail should move to the built-in skill: %q", prompt)
	}
	if strings.Contains(prompt, "/platforms/") {
		t.Fatalf("legacy platform routing should not appear: %q", prompt)
	}
	if strings.Contains(prompt, "`upload_file`") {
		t.Fatalf("hidden HTTP tool guidance should be omitted: %q", prompt)
	}
	if strings.Contains(prompt, "`localgate-filesystem`") {
		t.Fatalf("tool HTTP mode should not route to hidden-only progressive capabilities: %q", prompt)
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

func TestStartupInstructionsSurfacesSubagentSkillOnlyWithSubagentTools(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws := config.Root{Path: t.TempDir()}
	reg := core.NewRegistry(nil)
	reg.Register(dummyTool{name: "read_file", desc: "Read files"})
	tool := NewStartupInstructionsTool(ws, reg, StartupPromptOptions{})
	reg.Register(tool)

	res, err := tool.Execute(context.Background(), core.InvokeContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	prompt, _ := res["prompt"].(string)
	if strings.Contains(prompt, "`localgate-subagents`") {
		t.Fatalf("subagent skill should not surface without subagent tools: %q", prompt)
	}

	reg.Register(dummyTool{name: "subagent_spawn", desc: "Create subagent"})
	reg.Register(dummyTool{name: "subagent_send", desc: "Send to subagent"})
	res, err = tool.Execute(context.Background(), core.InvokeContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	prompt, _ = res["prompt"].(string)
	if !strings.Contains(prompt, "`localgate-subagents`") || !strings.Contains(prompt, "builtin://localgate-subagents/SKILL.md") {
		t.Fatalf("missing subagent skill routing: %q", prompt)
	}
}

func TestStartupInstructionsToolRequiresBase64WhenEnabled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws := config.Root{Path: t.TempDir()}
	reg := core.NewRegistry(nil)
	reg.Register(dummyTool{name: "read_file", desc: "Read file contents"})
	tool := NewStartupInstructionsTool(ws, reg, StartupPromptOptions{
		BaseURL:      "http://127.0.0.1:8000",
		ToolHTTPMode: true,
		EnableBase64: true,
	})
	reg.Register(tool)

	res, err := tool.Execute(context.Background(), core.InvokeContext{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	prompt, _ := res["prompt"].(string)
	if !strings.Contains(prompt, "tool_arguments.enable_base64=true") || !strings.Contains(prompt, "具体参数编码规则以 `localgate-tool-http`") {
		t.Fatalf("startup prompt should route enabled base64 handling to the tool HTTP skill: %q", prompt)
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

func TestSummarizeSkillDescriptionForStartup(t *testing.T) {
	short := "  Use this skill   for focused work.  "
	if got := summarizeSkillDescription(short); got != "Use this skill for focused work." {
		t.Fatalf("unexpected normalized summary: %q", got)
	}

	long := strings.Repeat("界", skillPromptDescriptionMaxRunes+20)
	got := summarizeSkillDescription(long)
	if len([]rune(got)) != skillPromptDescriptionMaxRunes {
		t.Fatalf("summary should be capped at %d runes, got %d", skillPromptDescriptionMaxRunes, len([]rune(got)))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("truncated summary should end with ellipsis: %q", got)
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

func TestBuiltinSkillsEmbeddedAndLoadable(t *testing.T) {
	loaded := loadBuiltinSkills()
	if len(loaded) != 8 {
		t.Fatalf("expected 8 built-in skills, got %#v", loaded)
	}
	wantNames := []string{
		"localgate-deployment",
		"localgate-file-transfer",
		"localgate-filesystem",
		"localgate-mcp-relay",
		"localgate-notification",
		"localgate-subagents",
		"localgate-task-session",
		"localgate-tool-http",
	}
	for i, want := range wantNames {
		if loaded[i].Name != want {
			t.Fatalf("unexpected built-in skill ordering: %#v", loaded)
		}
		if loaded[i].Source != "builtin" || loaded[i].Content == "" {
			t.Fatalf("built-in skill should carry source and content: %#v", loaded[i])
		}
		if strings.HasPrefix(loaded[i].Content, "---") {
			t.Fatalf("built-in skill content should exclude frontmatter: %q", loaded[i].Content)
		}
	}
}

func TestGetSkillToolReturnsEmbeddedBody(t *testing.T) {
	tool := NewGetSkillTool(config.DynamicHosting{
		Enabled:               true,
		AllowedExecutables:    []string{"/definitely/missing/localgate-test-runtime"},
		EnvAllowlist:          []string{"NODE_ENV", "APP_ENV"},
		StartupTimeoutSeconds: 37,
		RestartPolicy:         "on-failure",
		RequireApproval:       true,
	})
	res, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"name": "localgate-deployment"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res["name"] != "localgate-deployment" || res["source"] != "builtin" {
		t.Fatalf("unexpected get_skill metadata: %#v", res)
	}
	content, _ := res["content"].(string)
	for _, want := range []string{
		"# LocalGate Deployment",
		"Current instance: dynamic runtime",
		"/definitely/missing/localgate-test-runtime",
		"configured but not currently resolvable",
		"`APP_ENV`, `NODE_ENV`",
		"Default startup timeout: 37 seconds",
		"`HOST=127.0.0.1`",
		"dynamically allocated `PORT`",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("missing dynamic deployment skill context %q: %q", want, content)
		}
	}
	contextMap, ok := res["context"].(map[string]any)
	if !ok {
		t.Fatalf("missing structured skill context: %#v", res)
	}
	dynamicRuntime, ok := contextMap["dynamic_runtime"].(map[string]any)
	if !ok || dynamicRuntime["enabled"] != true || dynamicRuntime["startup_timeout_seconds"] != 37 {
		t.Fatalf("unexpected structured deployment context: %#v", contextMap)
	}
	if !strings.Contains(content, "options") {
		t.Fatalf("missing embedded deployment instructions: %q", content)
	}
	if _, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"name": "missing"}); err == nil {
		t.Fatal("expected unknown built-in skill to fail")
	}
}

func TestGetSkillToolReportsAvailableDeploymentExecutable(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	tool := NewGetSkillTool(config.DynamicHosting{
		Enabled:            true,
		AllowedExecutables: []string{executable},
	})
	res, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"name": "localgate-deployment"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content, _ := res["content"].(string)
	if !strings.Contains(content, executable) || !strings.Contains(content, "(available") {
		t.Fatalf("deployment skill should report configured executable availability: %q", content)
	}
}

func TestGetSkillToolReportsDynamicHostingDisabled(t *testing.T) {
	tool := NewGetSkillTool(config.DynamicHosting{})
	res, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"name": "localgate-deployment"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content, _ := res["content"].(string)
	if !strings.Contains(content, "Dynamic hosting is disabled") {
		t.Fatalf("deployment skill should report disabled dynamic hosting: %q", content)
	}
}

func TestStartupBuiltinSkillWinsOverExternalNameCollision(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	writeLocalSkillFixture(t, workspace, "collision", "---\nname: localgate-deployment\ndescription: External collision should not win\n---\n")

	ws := config.Root{Path: workspace}
	reg := core.NewRegistry(nil)
	reg.Register(dummyTool{name: "deploy_tool", desc: "Deploy"})
	tool := NewStartupInstructionsTool(ws, reg, StartupPromptOptions{})
	reg.Register(tool)

	res, err := tool.Execute(context.Background(), core.InvokeContext{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	prompt, _ := res["prompt"].(string)
	if !strings.Contains(prompt, "source: `builtin`") || !strings.Contains(prompt, "loader: `get_skill`") {
		t.Fatalf("built-in skill should win name collisions: %q", prompt)
	}
	if strings.Contains(prompt, "External collision should not win") {
		t.Fatalf("external skill should not shadow built-in skill: %q", prompt)
	}
}

func TestStartupSuppressesExternalCollisionWhenBuiltinIsNotRelevant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	writeLocalSkillFixture(t, workspace, "collision", "---\nname: localgate-deployment\ndescription: External deployment collision\n---\n")

	ws := config.Root{Path: workspace}
	reg := core.NewRegistry(nil)
	reg.Register(dummyTool{name: "read_file", desc: "Read"})
	tool := NewStartupInstructionsTool(ws, reg, StartupPromptOptions{})
	reg.Register(tool)

	res, err := tool.Execute(context.Background(), core.InvokeContext{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	prompt, _ := res["prompt"].(string)
	if strings.Contains(prompt, "External deployment collision") || strings.Contains(prompt, "builtin://localgate-deployment") {
		t.Fatalf("irrelevant built-in name must remain reserved without surfacing either skill: %q", prompt)
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
