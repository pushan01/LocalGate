package conversationhandoff

import (
	"context"
	"errors"
	"strings"
	"testing"

	"localgate/internal/conversation"
)

type fakeCreator struct {
	req    conversation.CreateRequest
	result conversation.Conversation
	err    error
}

func (f *fakeCreator) CreateConversation(_ context.Context, req conversation.CreateRequest) (conversation.Conversation, error) {
	f.req = req
	return f.result, f.err
}

type fakeCreateError struct {
	code      string
	uncertain bool
}

func (e fakeCreateError) Error() string { return e.code }

func (e fakeCreateError) ErrorCode() string { return e.code }

func (e fakeCreateError) SubmissionMayHaveOccurred() bool { return e.uncertain }

func TestHandoffWrapsCheckpointAndReturnsConversation(t *testing.T) {
	creator := &fakeCreator{result: conversation.Conversation{
		ID:        "conv-12345678",
		URL:       "https://chatgpt.com/c/conv-12345678",
		ProjectID: "deadbeef",
	}}
	svc := NewService(ProviderOpenCLIChatGPT, creator)
	result, err := svc.Handoff(context.Background(), Request{
		HandoffContext: "# User Goal\nContinue the LocalGate implementation.",
		Project:        "deadbeef",
	})
	if err != nil {
		t.Fatalf("Handoff: %v", err)
	}
	if result.Provider != ProviderOpenCLIChatGPT || result.ConversationID != "conv-12345678" || result.ConversationURL == "" || result.ProjectID != "deadbeef" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if creator.req.Project != "deadbeef" {
		t.Fatalf("project not forwarded: %#v", creator.req)
	}
	for _, want := range []string{
		"You are continuing work from a previous conversation.",
		"<conversation_handoff>",
		"# User Goal\nContinue the LocalGate implementation.",
		"</conversation_handoff>",
		"wait for the user's next message",
	} {
		if !strings.Contains(creator.req.Prompt, want) {
			t.Fatalf("bootstrap prompt missing %q: %q", want, creator.req.Prompt)
		}
	}
}

func TestHandoffMapsKnownCreatorError(t *testing.T) {
	svc := NewService(ProviderOpenCLIChatGPT, &fakeCreator{
		err: fakeCreateError{code: "auth_required"},
	})
	_, err := svc.Handoff(context.Background(), Request{HandoffContext: "checkpoint"})
	if err == nil {
		t.Fatal("expected error")
	}
	var coded interface{ ErrorCode() string }
	if !errors.As(err, &coded) || coded.ErrorCode() != "conversation_handoff_auth_required" {
		t.Fatalf("unexpected error code: %v", err)
	}
}

func TestHandoffMapsUncertainSubmissionWithoutRetrySignal(t *testing.T) {
	svc := NewService(ProviderOpenCLIChatGPT, &fakeCreator{
		err: fakeCreateError{code: "invalid_output", uncertain: true},
	})
	_, err := svc.Handoff(context.Background(), Request{HandoffContext: "checkpoint"})
	if err == nil {
		t.Fatal("expected error")
	}
	var coded interface{ ErrorCode() string }
	if !errors.As(err, &coded) || coded.ErrorCode() != "conversation_handoff_outcome_unknown" {
		t.Fatalf("unexpected error code: %v", err)
	}
	if !strings.Contains(err.Error(), "do not automatically retry") {
		t.Fatalf("missing retry warning: %v", err)
	}
}

func TestHandoffTreatsMissingURLAsOutcomeUnknown(t *testing.T) {
	svc := NewService(ProviderOpenCLIChatGPT, &fakeCreator{
		result: conversation.Conversation{ID: "conversation123"},
	})
	_, err := svc.Handoff(context.Background(), Request{HandoffContext: "checkpoint"})
	if err == nil {
		t.Fatal("expected error")
	}
	var coded interface{ ErrorCode() string }
	if !errors.As(err, &coded) || coded.ErrorCode() != "conversation_handoff_outcome_unknown" {
		t.Fatalf("unexpected error code: %v", err)
	}
}
