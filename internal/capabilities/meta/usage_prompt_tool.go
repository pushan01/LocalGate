package meta

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	toolcap "localgate/internal/capabilities/toolaccess"
	"localgate/internal/config"
	"localgate/internal/core"
)

type StartupInstructionsTool struct {
	WS      config.Root
	Reg     *core.Registry
	Options StartupPromptOptions
}

type StartupPromptOptions struct {
	BaseURL          string
	ToolHTTPMode     bool
	EnableBase64     bool
	GlobalSkillRoots []string
	MCPServers       []config.MCPServer
}

func NewStartupInstructionsTool(ws config.Root, reg *core.Registry, opts StartupPromptOptions) core.Tool {
	return StartupInstructionsTool{WS: ws, Reg: reg, Options: opts}
}

func (t StartupInstructionsTool) Name() string {
	return "get_startup_instructions"
}

func (t StartupInstructionsTool) Description() string {
	return "Call this first to get LocalGate capability routing, core execution constraints, and the dynamic skill index."
}

func (t StartupInstructionsTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
	}
}

func (t StartupInstructionsTool) ToolAnnotations() map[string]any {
	return map[string]any{"readOnlyHint": true}
}

func (t StartupInstructionsTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	_ = ctx
	_ = args

	tools := t.exposedTools()
	hasTools := visibleToolFlags(tools)
	capabilities := t.registryCapabilityFlags()
	skillCapabilities := capabilities
	if t.Options.ToolHTTPMode {
		// Tool HTTP cannot use invoke_tool to reach hidden Registry capabilities,
		// so skill routing must stay bounded to the HTTP-callable visible list.
		skillCapabilities = hasTools
	}
	hasRG := executableAvailable("rg")
	now := time.Now()
	shell := os.Getenv("SHELL")
	date := now.Format("2006-01-02")
	timezone := displayTimezone(now)
	skillRoots, externalSkills := visibleSkillsForPrompt(t.WS.Path, t.Options.GlobalSkillRoots)
	builtinSkills := visibleBuiltinSkillsForPrompt(skillCapabilities, t.Options)
	skills := mergeSkillIndexes(builtinSkills, externalSkills)
	mcpServers := safeMCPServersForPrompt(t.Options.MCPServers)
	if t.Options.ToolHTTPMode {
		mcpServers = nil
	}
	prompt, err := renderEmbeddedPromptTemplate(map[string]any{
		"cwd":              t.WS.Path,
		"roots":            []string{t.WS.Path},
		"tool_count":       len(tools),
		"tools":            tools,
		"has_tools":        hasTools,
		"has_capabilities": capabilities,
		"skill_roots":      append([]string(nil), skillRoots...),
		"skill_count":      len(skills),
		"skills":           skills,
		"mcp_servers":      mcpServers,
		"mcp_server_count": len(mcpServers),
		"shell":            shell,
		"date":             date,
		"timezone":         timezone,
		"has_rg":           hasRG,
		"missing_rg":       !hasRG,
		"tool_http_mode":   t.Options.ToolHTTPMode,
		"legacy_mode":      !t.Options.ToolHTTPMode,
		"enable_base64":    t.Options.EnableBase64,
		"base_url":         t.Options.BaseURL,
		"tool_http_route":  "/tools/{tool}/call",
	})
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"prompt": prompt,
		"tools":  tools,
	}, nil
}

func visibleToolFlags(tools []map[string]any) map[string]any {
	flags := make(map[string]any, len(tools))
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		name = strings.TrimSpace(name)
		if name != "" {
			flags[name] = true
		}
	}
	return flags
}

func hasAnyVisibleTool(flags map[string]any, names ...string) bool {
	for _, name := range names {
		if visible, _ := flags[name].(bool); visible {
			return true
		}
	}
	return false
}

func safeMCPServersForPrompt(servers []config.MCPServer) []map[string]any {
	out := make([]map[string]any, 0, len(servers))
	for _, server := range servers {
		envKeys := make([]string, 0, len(server.Env))
		for key := range server.Env {
			envKeys = append(envKeys, key)
		}
		slices.Sort(envKeys)
		out = append(out, map[string]any{
			"name":     server.Name,
			"env_keys": envKeys,
		})
	}
	return out
}

func displayTimezone(now time.Time) string {
	if name := normalizedTimezoneName(os.Getenv("TZ")); name != "" {
		return name
	}
	if name := normalizedTimezoneName(now.Location().String()); name != "" {
		return name
	}
	for _, path := range []string{"/etc/localtime", "/var/db/timezone/localtime"} {
		if target, err := os.Readlink(path); err == nil {
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(path), target)
			}
			if name := timezoneNameFromPath(target); name != "" {
				return name
			}
		}
	}
	if data, err := os.ReadFile("/etc/timezone"); err == nil {
		if name := normalizedTimezoneName(string(data)); name != "" {
			return name
		}
	}
	return formatUTCOffset(now)
}

func executableAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func formatUTCOffset(now time.Time) string {
	_, offset := now.Zone()
	if offset == 0 {
		return "UTC"
	}
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	return fmt.Sprintf("UTC%s%02d:%02d", sign, offset/3600, offset%3600/60)
}

func normalizedTimezoneName(value string) string {
	name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), ":"))
	if name == "" || strings.EqualFold(name, "Local") {
		return ""
	}
	if strings.Contains(name, string(filepath.Separator)) {
		if fromPath := timezoneNameFromPath(name); fromPath != "" {
			return fromPath
		}
	}
	if name == "UTC" {
		return name
	}
	if _, err := time.LoadLocation(name); err == nil {
		return name
	}
	return ""
}

func timezoneNameFromPath(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	const marker = "/zoneinfo/"
	idx := strings.LastIndex(path, marker)
	if idx < 0 {
		return ""
	}
	name := strings.TrimPrefix(path[idx+len(marker):], "posix/")
	name = strings.TrimPrefix(name, "right/")
	return normalizedTimezoneName(name)
}

func (t StartupInstructionsTool) exposedTools() []map[string]any {
	if t.Reg == nil {
		return nil
	}
	if t.Options.ToolHTTPMode {
		return t.visibleHTTPTools()
	}

	names := t.Reg.ListVisible()
	slices.Sort(names)
	tools := make([]map[string]any, 0, len(names)+1)
	for _, name := range names {
		if name == t.Name() || !toolcap.IsDirectMCPTool(name) {
			continue
		}
		tool, ok := t.Reg.GetVisible(name)
		if !ok {
			continue
		}
		tools = append(tools, map[string]any{"name": name, "description": tool.Description()})
	}
	tools = append(tools, map[string]any{
		"name":        toolcap.InvokeToolName,
		"description": "Invoke one available LocalGate Registry tool by name after loading the relevant skill and exact tool schema.",
	})
	slices.SortFunc(tools, func(a, b map[string]any) int {
		return strings.Compare(fmtString(a["name"]), fmtString(b["name"]))
	})
	return tools
}

func (t StartupInstructionsTool) visibleHTTPTools() []map[string]any {
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
		if isStartupBootstrapTool(name) {
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

func (t StartupInstructionsTool) registryCapabilityFlags() map[string]any {
	flags := make(map[string]any)
	if t.Reg == nil {
		return flags
	}
	for _, name := range t.Reg.List() {
		name = strings.TrimSpace(name)
		if name != "" {
			flags[name] = true
		}
	}
	return flags
}

func fmtString(v any) string {
	s, _ := v.(string)
	return s
}

func isStartupBootstrapTool(name string) bool {
	return toolcap.IsBootstrapTool(name)
}
