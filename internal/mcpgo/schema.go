package mcpgo

import (
	"encoding/json"

	"localgate/internal/toolargs"

	"github.com/mark3labs/mcp-go/mcp"
)

// newToolWithRawSchema builds a tool that publishes its input schema as raw JSON schema.
// We keep mcp.NewTool defaults (annotations, etc.) but clear InputSchema.Type to avoid
// "both InputSchema and RawInputSchema set" marshal errors.
func newToolWithRawSchema(name, description string, schema any, meta map[string]any) mcp.Tool {
	t := mcp.NewTool(name, mcp.WithDescription(description))
	if raw := marshalSchema(schema); raw != nil {
		t.RawInputSchema = raw
		// Must be empty when RawInputSchema is set (mcp-go checks InputSchema.Type != "").
		t.InputSchema = mcp.ToolInputSchema{}
	}
	if len(meta) > 0 {
		t.Meta = mcp.NewMetaFromMap(meta)
	}
	return t
}

// newAsyncToolWithRawSchema exposes a business tool schema plus the MCP wrapper-level async flag.
func newAsyncToolWithRawSchema(name, description string, schema any, meta map[string]any) mcp.Tool {
	return newToolWithRawSchema(name, description, withAsyncSchema(schema), meta)
}

func mcpExposedSchema(schema any, base64Enabled bool, async bool) any {
	if base64Enabled {
		return toolargs.MCPInputSchema(schema, true)
	}
	if async {
		return withAsyncSchema(schema)
	}
	return schema
}

func marshalSchema(schema any) json.RawMessage {
	if schema == nil {
		return nil
	}
	b, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	return json.RawMessage(b)
}

func withAsyncSchema(schema any) any {
	if schema == nil {
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"async": map[string]any{
					"type":        "boolean",
					"description": "Submit task only and return immediately",
				},
			},
		}
	}
	base, ok := schema.(map[string]any)
	if !ok {
		return schema
	}
	out := cloneMap(base)
	props, _ := out["properties"].(map[string]any)
	props = cloneMap(props)
	props["async"] = map[string]any{
		"type":        "boolean",
		"description": "Submit task only and return immediately",
	}
	out["properties"] = props
	if _, ok := out["type"]; !ok {
		out["type"] = "object"
	}
	return out
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
