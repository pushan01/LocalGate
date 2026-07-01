package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			writeResult(req.ID, map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo": map[string]any{
					"name":    "fake-mcp-server",
					"version": "1.0.0",
				},
			})
		case "notifications/initialized":
			if os.Getenv("FAKE_MCP_EXIT_AFTER_INIT") == "1" {
				os.Exit(0)
			}
		case "tools/list":
			writeResult(req.ID, mcp.ListToolsResult{
				Tools: []mcp.Tool{
					{
						Name:        "echo_remote",
						Description: "Echo arguments back from fake downstream server",
						RawInputSchema: mustRawJSON(map[string]any{
							"type": "object",
							"properties": map[string]any{
								"text": map[string]any{"type": "string"},
							},
							"required": []string{"text"},
						}),
					},
					{
						Name:        "fail_remote",
						Description: "Return an isError tool result",
						RawInputSchema: mustRawJSON(map[string]any{
							"type": "object",
							"properties": map[string]any{
								"message": map[string]any{"type": "string"},
							},
						}),
					},
				},
			})
			if os.Getenv("FAKE_MCP_EXIT_AFTER_LIST") == "1" {
				os.Exit(0)
			}
		case "tools/call":
			var params toolCallParams
			_ = json.Unmarshal(req.Params, &params)
			switch params.Name {
			case "echo_remote":
				text, _ := params.Arguments["text"].(string)
				writeResult(req.ID, &mcp.CallToolResult{
					Content: []mcp.Content{
						mcp.TextContent{
							Type: "text",
							Text: "echo:" + text,
						},
					},
					StructuredContent: map[string]any{
						"echo":   text,
						"server": "fake",
					},
				})
			case "fail_remote":
				msg, _ := params.Arguments["message"].(string)
				if msg == "" {
					msg = "remote failure"
				}
				writeResult(req.ID, mcp.NewToolResultError(msg))
			default:
				writeError(req.ID, -32601, fmt.Sprintf("tool not found: %s", params.Name))
			}
		default:
			writeError(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
		}
	}
}

func writeResult(id json.RawMessage, result any) {
	writeEnvelope(map[string]any{
		"jsonrpc": "2.0",
		"id":      rawID(id),
		"result":  result,
	})
}

func writeError(id json.RawMessage, code int, message string) {
	writeEnvelope(map[string]any{
		"jsonrpc": "2.0",
		"id":      rawID(id),
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func writeEnvelope(v map[string]any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func rawID(id json.RawMessage) any {
	raw := strings.TrimSpace(string(id))
	if raw == "" {
		return nil
	}
	var value any
	if err := json.Unmarshal(id, &value); err != nil {
		return nil
	}
	return value
}

func mustRawJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
