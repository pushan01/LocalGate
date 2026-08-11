package toolaccess

import (
	"context"
	"errors"
	"strings"

	"localgate/internal/core"
)

type getToolSchemaTool struct {
	reg *core.Registry
}

func NewGetToolSchemaTool(reg *core.Registry) core.Tool {
	return getToolSchemaTool{reg: reg}
}

func (t getToolSchemaTool) Name() string { return "get_tool_schema" }

func (t getToolSchemaTool) Description() string {
	return "Return the input schema for one HTTP-callable LocalGate tool by name."
}

func (t getToolSchemaTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"tool": map[string]any{
				"type":        "string",
				"description": "Visible LocalGate tool name, for example read_file.",
			},
		},
		"required": []string{"tool"},
	}
}

func (t getToolSchemaTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	_ = ctx
	_ = ic
	if t.reg == nil {
		return nil, errors.New("tool registry is not available")
	}
	name, _ := args["tool"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("tool is required")
	}
	if isBootstrapTool(name) {
		return nil, errors.New("bootstrap tools do not have HTTP-callable schemas")
	}
	tool, ok := t.reg.GetVisible(name)
	if !ok {
		return nil, errors.New("tool not found")
	}
	return map[string]any{
		"name":        tool.Name(),
		"description": tool.Description(),
		"schema":      tool.Schema(),
	}, nil
}

func isBootstrapTool(name string) bool {
	switch name {
	case "get_startup_instructions", "get_skill", "get_temporary_access_token", "get_tool_schema":
		return true
	default:
		return false
	}
}
