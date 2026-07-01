package meta

import (
	"context"
	"os"
	"slices"
	"time"

	"localgate/internal/config"
	"localgate/internal/core"
)

type StartupInstructionsTool struct {
	WS      config.Workspace
	Reg     *core.Registry
	Options StartupPromptOptions
}

type StartupPromptOptions struct {
	BaseURL      string
	ToolHTTPMode bool
	EnableBase64 bool
}

func NewStartupInstructionsTool(ws config.Workspace, reg *core.Registry, opts StartupPromptOptions) core.Tool {
	return StartupInstructionsTool{WS: ws, Reg: reg, Options: opts}
}

func (t StartupInstructionsTool) Name() string {
	return "get_startup_instructions"
}

func (t StartupInstructionsTool) Description() string {
	return "Call this first to get LocalGate startup instructions, visible tool guidance, and dynamic skill metadata"
}

func (t StartupInstructionsTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
	}
}

func (t StartupInstructionsTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	_ = ctx
	_ = args

	tools := t.visibleTools()
	now := time.Now()
	shell := os.Getenv("SHELL")
	date := now.Format("2006-01-02")
	timezone := now.Location().String()
	skillRoots, skills := visibleSkillsForPrompt(t.WS.CWD)
	prompt, err := renderEmbeddedPromptTemplate(map[string]any{
		"workspace_id":    ic.WorkspaceID,
		"cwd":             t.WS.CWD,
		"roots":           append([]string(nil), t.WS.Roots...),
		"tool_count":      len(tools),
		"tools":           tools,
		"skill_roots":     append([]string(nil), skillRoots...),
		"skill_count":     len(skills),
		"skills":          skills,
		"shell":           shell,
		"date":            date,
		"timezone":        timezone,
		"tool_http_mode":  t.Options.ToolHTTPMode,
		"legacy_mode":     !t.Options.ToolHTTPMode,
		"enable_base64":   t.Options.EnableBase64,
		"base_url":        t.Options.BaseURL,
		"tool_http_route": "/tools/{tool}/call",
	})
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"prompt": prompt,
		"tools":  tools,
	}, nil
}

func (t StartupInstructionsTool) visibleTools() []map[string]any {
	if t.Reg == nil {
		return nil
	}
	names := t.Reg.ListVisible()
	slices.Sort(names)

	tools := make([]map[string]any, 0, len(names))
	for _, name := range names {
		if name == t.Name() {
			continue
		}
		if t.Options.ToolHTTPMode && isStartupBootstrapTool(name) {
			continue
		}
		tool, ok := t.Reg.GetVisible(name)
		if !ok {
			continue
		}
		tools = append(tools, map[string]any{
			"name":        name,
			"description": tool.Description(),
		})
	}
	return tools
}

func fmtString(v any) string {
	s, _ := v.(string)
	return s
}

func isStartupBootstrapTool(name string) bool {
	switch name {
	case "get_startup_instructions", "get_temporary_access_token", "get_tool_schema":
		return true
	default:
		return false
	}
}
