package conversationcap

import (
	"context"
	"strings"
	"testing"

	"localgate/internal/conversation"
	"localgate/internal/conversationhandoff"
	"localgate/internal/core"
)

type captureCreator struct {
	req conversation.CreateRequest
}

func (c *captureCreator) CreateConversation(_ context.Context, req conversation.CreateRequest) (conversation.Conversation, error) {
	c.req = req
	return conversation.Conversation{
		ID:  "conversation123",
		URL: "https://chatgpt.com/c/conversation123",
	}, nil
}

func TestHandoffToolCreatesOrdinaryConversation(t *testing.T) {
	creator := &captureCreator{}
	tool := NewHandoffTool(conversationhandoff.NewService(conversationhandoff.ProviderOpenCLIChatGPT, creator))
	result, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"handoff_context": "# Current State\nImplementation is in progress.",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["provider"] != conversationhandoff.ProviderOpenCLIChatGPT || result["conversation_id"] != "conversation123" || result["accepted"] != true {
		t.Fatalf("unexpected result: %#v", result)
	}
	if _, exists := result["subagent_id"]; exists {
		t.Fatalf("handoff must not create a subagent result: %#v", result)
	}
	if !strings.Contains(creator.req.Prompt, "# Current State") {
		t.Fatalf("checkpoint was not included in provider prompt: %q", creator.req.Prompt)
	}
}

func TestHandoffToolRejectsEmptyAndOversizedContext(t *testing.T) {
	tool := NewHandoffTool(conversationhandoff.NewService(conversationhandoff.ProviderOpenCLIChatGPT, &captureCreator{}))
	if _, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{"handoff_context": " "}); err == nil {
		t.Fatal("expected empty handoff_context error")
	}
	if _, err := tool.Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"handoff_context": strings.Repeat("x", maxHandoffContextBytes+1),
	}); err == nil {
		t.Fatal("expected oversized handoff_context error")
	}
}

func TestHandoffToolAnnotationsAreRemoteNonIdempotentMutation(t *testing.T) {
	annotations := (HandoffTool{}).ToolAnnotations()
	if annotations["readOnlyHint"] != false || annotations["destructiveHint"] != false || annotations["idempotentHint"] != false || annotations["openWorldHint"] != true {
		t.Fatalf("unexpected annotations: %#v", annotations)
	}
}
