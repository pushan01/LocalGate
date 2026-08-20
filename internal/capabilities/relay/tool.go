package relay

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"localgate/internal/core"
	"localgate/internal/mcprelay"

	"github.com/mark3labs/mcp-go/mcp"
)

type Tool struct {
	name        string
	description string
	schema      any
	remoteName  string
	client      *mcprelay.Client
}

func NewTools(serverName string, client *mcprelay.Client) []core.Tool {
	if client == nil {
		return nil
	}
	serverName = strings.TrimSpace(serverName)
	if serverName == "" {
		serverName = strings.TrimSpace(client.Name())
	}
	tools := client.Tools()
	out := make([]core.Tool, 0, len(tools))
	for _, remote := range tools {
		remoteName := strings.TrimSpace(remote.Name)
		if remoteName == "" {
			continue
		}
		out = append(out, Tool{
			name:        serverName + "__" + remoteName,
			description: remote.Description,
			schema:      remote.InputSchema,
			remoteName:  remoteName,
			client:      client,
		})
	}
	return out
}

func (t Tool) Name() string        { return t.name }
func (t Tool) Description() string { return t.description }
func (t Tool) Schema() any         { return t.schema }

func (t Tool) Execute(ctx context.Context, _ core.InvokeContext, args map[string]any) (map[string]any, error) {
	if t.client == nil {
		return nil, errors.New("downstream MCP relay is not available")
	}
	result, err := t.client.CallTool(ctx, t.remoteName, args)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{}
	if result.StructuredContent != nil {
		payload["structured_content"] = result.StructuredContent
	}
	if blocks := encodeContent(result.Content); len(blocks) > 0 {
		payload["content"] = blocks
	}
	if result.IsError {
		payload["is_error"] = true
		message := relayErrorMessage(result.Content)
		if message == "" {
			message = "downstream MCP tool returned an error"
		}
		return payload, errors.New(message)
	}
	return payload, nil
}

func (t Tool) NativeContent(result map[string]any) []map[string]any {
	raw, ok := result["content"]
	if !ok {
		return nil
	}
	switch blocks := raw.(type) {
	case []map[string]any:
		return blocks
	case []any:
		out := make([]map[string]any, 0, len(blocks))
		for _, item := range blocks {
			if block, ok := item.(map[string]any); ok {
				out = append(out, block)
			}
		}
		return out
	default:
		return nil
	}
}

func encodeContent(content []mcp.Content) []map[string]any {
	if len(content) == 0 {
		return nil
	}
	encoded, err := json.Marshal(content)
	if err != nil {
		return nil
	}
	var blocks []map[string]any
	if err := json.Unmarshal(encoded, &blocks); err != nil {
		return nil
	}
	return blocks
}

func relayErrorMessage(content []mcp.Content) string {
	for _, item := range content {
		if text := strings.TrimSpace(mcp.GetTextFromContent(item)); text != "" {
			return text
		}
	}
	return ""
}
