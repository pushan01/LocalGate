package notification

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"localgate/internal/core"
	notify "localgate/internal/notification"
)

type Tool struct {
	service *notify.Service
}

func NewTool(service *notify.Service) core.Tool {
	return Tool{service: service}
}

func (Tool) Name() string { return "send_notification" }

func (Tool) Description() string {
	return "Send one Bark notification to the configured phone. Use this only when the user explicitly asks to push or notify their phone. The interruption level is fixed to active, and Bark device and encryption keys are never exposed to the caller."
}

func (Tool) Schema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"title": map[string]any{
				"type":        "string",
				"description": "Notification title, up to 128 UTF-8 bytes.",
			},
			"subtitle": map[string]any{
				"type":        "string",
				"description": "Optional notification subtitle, up to 128 UTF-8 bytes.",
			},
			"body": map[string]any{
				"type":        "string",
				"description": "Plain notification body. Do not provide this together with markdown.",
			},
			"markdown": map[string]any{
				"type":        "string",
				"description": "Basic Markdown notification content. Do not provide this together with body.",
			},
			"group": map[string]any{
				"type":        "string",
				"description": "Optional Bark notification group, up to 64 UTF-8 bytes.",
			},
			"url": map[string]any{
				"type":        "string",
				"description": "Optional absolute URL or URL scheme opened when the notification is tapped.",
			},
		},
		"required": []string{"title"},
	}
}

func (Tool) OutputSchema() any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"status":      map[string]any{"type": "string"},
			"provider":    map[string]any{"type": "string"},
			"level":       map[string]any{"type": "string"},
			"http_status": map[string]any{"type": "integer"},
			"code":        map[string]any{"type": "integer"},
			"message":     map[string]any{"type": "string"},
		},
		"required": []string{"status", "provider", "level", "http_status"},
	}
}

func (Tool) ToolAnnotations() map[string]any {
	return map[string]any{
		"readOnlyHint":    false,
		"destructiveHint": false,
		"idempotentHint":  false,
		"openWorldHint":   true,
	}
}

func (t Tool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	if t.service == nil || !t.service.ToolEnabled() {
		return nil, errors.New("notification tool is not enabled")
	}
	message := notify.Message{
		Title:    stringArg(args, "title"),
		Subtitle: stringArg(args, "subtitle"),
		Body:     stringArg(args, "body"),
		Markdown: stringArg(args, "markdown"),
		Group:    stringArg(args, "group"),
		URL:      stringArg(args, "url"),
	}
	if err := validateMessage(message); err != nil {
		return nil, err
	}
	result, err := t.service.Send(ctx, message)
	if err != nil {
		return nil, err
	}
	_ = ic
	return map[string]any{
		"status":      result.Status,
		"provider":    result.Provider,
		"level":       "active",
		"http_status": result.HTTPStatus,
		"code":        result.Code,
		"message":     result.Message,
	}, nil
}

func validateMessage(message notify.Message) error {
	if len([]byte(strings.TrimSpace(message.Title))) == 0 {
		return errors.New("title is required")
	}
	limits := []struct {
		name  string
		value string
		max   int
	}{
		{name: "title", value: message.Title, max: 128},
		{name: "subtitle", value: message.Subtitle, max: 128},
		{name: "body", value: message.Body, max: 4096},
		{name: "markdown", value: message.Markdown, max: 8192},
		{name: "group", value: message.Group, max: 64},
		{name: "url", value: message.URL, max: 2048},
	}
	for _, limit := range limits {
		if len([]byte(limit.value)) > limit.max {
			return fmt.Errorf("%s exceeds %d UTF-8 bytes", limit.name, limit.max)
		}
	}
	body := strings.TrimSpace(message.Body)
	markdown := strings.TrimSpace(message.Markdown)
	if body == "" && markdown == "" {
		return errors.New("body or markdown is required")
	}
	if body != "" && markdown != "" {
		return errors.New("body and markdown are mutually exclusive")
	}
	return nil
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}
