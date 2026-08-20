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
	return "Return the exact input schema for one available LocalGate Registry tool by name before invoking it through a progressive transport such as invoke_tool or Tool HTTP."
}

func (t getToolSchemaTool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"tool": map[string]any{
				"type":        "string",
				"description": "Available LocalGate Registry tool name, for example deploy_tool.",
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
	if IsBootstrapTool(name) {
		return nil, errors.New("bootstrap tools do not have Registry invocation schemas")
	}
	tool, ok := t.reg.Get(name)
	if !ok {
		return nil, errors.New("tool not found")
	}
	return map[string]any{
		"name":        tool.Name(),
		"description": tool.Description(),
		"schema":      tool.Schema(),
	}, nil
}
