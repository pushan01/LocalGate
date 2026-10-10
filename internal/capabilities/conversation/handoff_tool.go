package conversationcap

import (
	"context"
	"errors"
	"strings"

	"localgate/internal/conversationhandoff"
	"localgate/internal/core"
)

const (
	maxHandoffContextBytes = 256 << 10
	maxProjectBytes        = 2048
)

type HandoffTool struct {
	service *conversationhandoff.Service
}

func NewHandoffTool(service *conversationhandoff.Service) HandoffTool {
	return HandoffTool{service: service}
}

func (HandoffTool) Name() string { return "conversation_handoff" }

func (HandoffTool) Description() string {
	return "Create a new ordinary ChatGPT conversation initialized with model-prepared handoff context. This does not create a LocalGate subagent and LocalGate does not manage later messages in the new conversation."
}

func (HandoffTool) Schema() any {
	return objectSchema(map[string]any{
		"handoff_context": map[string]any{
			"type":        "string",
			"description": "Concise continuation checkpoint prepared by the current model. Prefer current user intent, active state, confirmed decisions, constraints, unresolved issues, and the next action over a chronological transcript or tool log.",
		},
		"project": map[string]any{
			"type":        "string",
			"description": "Optional ChatGPT Project ID or /g/g-p-... URL. Omit to create a normal top-level ChatGPT conversation outside any configured Subagent project.",
		},
	}, []string{"handoff_context"})
}

func (HandoffTool) OutputSchema() any {
	return objectSchema(map[string]any{
		"provider":         map[string]any{"type": "string"},
		"conversation_id":  map[string]any{"type": "string"},
		"conversation_url": map[string]any{"type": "string"},
		"project_id":       map[string]any{"type": "string"},
		"accepted":         map[string]any{"type": "boolean"},
	}, []string{"provider", "conversation_id", "conversation_url", "accepted"})
}

func (HandoffTool) ToolAnnotations() map[string]any {
	return map[string]any{
		"readOnlyHint":    false,
		"destructiveHint": false,
		"idempotentHint":  false,
		"openWorldHint":   true,
	}
}

func (t HandoffTool) Execute(ctx context.Context, _ core.InvokeContext, args map[string]any) (map[string]any, error) {
	if t.service == nil {
		return nil, errors.New("conversation handoff service is not available")
	}
	handoffContext, _ := args["handoff_context"].(string)
	if strings.TrimSpace(handoffContext) == "" {
		return nil, errors.New("handoff_context is required")
	}
	if len([]byte(handoffContext)) > maxHandoffContextBytes {
		return nil, errors.New("handoff_context exceeds 262144 bytes")
	}
	project, _ := args["project"].(string)
	project = strings.TrimSpace(project)
	if len([]byte(project)) > maxProjectBytes {
		return nil, errors.New("project exceeds 2048 bytes")
	}
	result, err := t.service.Handoff(ctx, conversationhandoff.Request{
		HandoffContext: handoffContext,
		Project:        project,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"provider":         result.Provider,
		"conversation_id":  result.ConversationID,
		"conversation_url": result.ConversationURL,
		"project_id":       result.ProjectID,
		"accepted":         true,
	}, nil
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}
