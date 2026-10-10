package conversationhandoff

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"localgate/internal/conversation"
)

const ProviderOpenCLIChatGPT = "opencli_chatgpt"

type Request struct {
	HandoffContext string
	Project        string
}

type Result struct {
	Provider        string
	ConversationID  string
	ConversationURL string
	ProjectID       string
}

type Service struct {
	provider string
	creator  conversation.Creator
}

func NewService(provider string, creator conversation.Creator) *Service {
	return &Service{
		provider: strings.TrimSpace(provider),
		creator:  creator,
	}
}

func (s *Service) Handoff(ctx context.Context, req Request) (Result, error) {
	if s == nil || s.creator == nil {
		return Result{}, newError("configuration", "conversation handoff is not configured", nil)
	}
	handoffContext := strings.TrimSpace(req.HandoffContext)
	if handoffContext == "" {
		return Result{}, newError("configuration", "handoff context is required", nil)
	}
	created, err := s.creator.CreateConversation(ctx, conversation.CreateRequest{
		Prompt:  buildBootstrapPrompt(handoffContext),
		Project: strings.TrimSpace(req.Project),
	})
	if err != nil {
		return Result{}, mapCreateError(err)
	}
	if strings.TrimSpace(created.ID) == "" || strings.TrimSpace(created.URL) == "" {
		return Result{}, newError("outcome_unknown", "the new conversation may have been created, but LocalGate did not receive both its id and URL; do not automatically retry the handoff", nil)
	}
	return Result{
		Provider:        s.provider,
		ConversationID:  created.ID,
		ConversationURL: created.URL,
		ProjectID:       created.ProjectID,
	}, nil
}

func buildBootstrapPrompt(handoffContext string) string {
	return strings.Join([]string{
		"You are continuing work from a previous conversation.",
		"",
		"The previous conversation was compacted and handed off to this new conversation. The checkpoint below is a curated continuation state, not a transcript or execution log.",
		"",
		"Treat the user's current goal and latest intent in the checkpoint as primary. Preserve established constraints and decisions unless the user explicitly changes them or new evidence requires revision. Do not restart work already marked completed, and do not re-litigate resolved implementation history unless it is needed for the next action.",
		"",
		"Do not immediately continue executing the task. Briefly acknowledge that the context has been received, then wait for the user's next message.",
		"",
		"<conversation_handoff>",
		handoffContext,
		"</conversation_handoff>",
	}, "\n")
}

type Error struct {
	code    string
	message string
	cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.message != "" {
		return e.message
	}
	return e.code
}

func (e *Error) Unwrap() error { return e.cause }

func (e *Error) ErrorCode() string {
	if e == nil {
		return ""
	}
	return "conversation_handoff_" + e.code
}

func newError(code, message string, cause error) error {
	return &Error{code: code, message: message, cause: cause}
}

func mapCreateError(err error) error {
	if err == nil {
		return nil
	}
	var uncertain interface {
		SubmissionMayHaveOccurred() bool
	}
	if errors.As(err, &uncertain) && uncertain.SubmissionMayHaveOccurred() {
		return newError("outcome_unknown", "the new conversation may have been created, but LocalGate could not confirm its id or URL; do not automatically retry the handoff", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return newError("timeout", fmt.Sprintf("conversation handoff failed: %v", err), err)
	}
	if errors.Is(err, context.Canceled) {
		return newError("canceled", fmt.Sprintf("conversation handoff failed: %v", err), err)
	}
	code := "execution_failed"
	var coded interface {
		ErrorCode() string
	}
	if errors.As(err, &coded) {
		if candidate := strings.TrimSpace(coded.ErrorCode()); candidate != "" {
			code = candidate
		}
	}
	return newError(code, fmt.Sprintf("conversation handoff failed: %v", err), err)
}
