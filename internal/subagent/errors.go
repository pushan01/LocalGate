package subagent

import (
	"errors"
	"fmt"
)

var ErrOutcomeUnknown = errors.New("subagent message outcome unknown")

var (
	ErrBusy                 error = busyError{}
	ErrNotReady             error = notReadyError{}
	ErrArchiveBusy          error = archiveBusyError{}
	ErrArchiveUnsupported         = errors.New("subagent provider does not support archive")
	ErrConversationConflict       = errors.New("subagent conversation is already owned by another local subagent")
)

type OutcomeUnknownError struct {
	Cause error
}

func (e *OutcomeUnknownError) Error() string {
	if e == nil || e.Cause == nil {
		return ErrOutcomeUnknown.Error()
	}
	return fmt.Sprintf("%s: %v", ErrOutcomeUnknown, e.Cause)
}

func (e *OutcomeUnknownError) Unwrap() error { return ErrOutcomeUnknown }

func (e *OutcomeUnknownError) ErrorCode() string { return "subagent_outcome_unknown" }

func NewOutcomeUnknownError(cause error) error {
	return &OutcomeUnknownError{Cause: cause}
}

type queueFullError struct{}

func (queueFullError) Error() string     { return "subagent operation queue is full" }
func (queueFullError) ErrorCode() string { return "subagent_queue_full" }

type busyError struct{}

func (busyError) Error() string {
	return "subagent is busy; poll or wait before sending another message"
}
func (busyError) ErrorCode() string { return "subagent_busy" }

type notReadyError struct{}

func (notReadyError) Error() string {
	return "subagent is not ready; poll or wait before sending another message"
}
func (notReadyError) ErrorCode() string { return "subagent_not_ready" }

type archiveBusyError struct{}

func (archiveBusyError) Error() string {
	return "subagent is still generating; retry archive after the current turn finishes"
}
func (archiveBusyError) ErrorCode() string { return "subagent_archive_busy" }

type ConversationConflictError struct {
	ConversationID string
	OwnerID        string
}

func (e *ConversationConflictError) Error() string {
	if e == nil {
		return ErrConversationConflict.Error()
	}
	return fmt.Sprintf("%s: conversation_id=%s owner=%s", ErrConversationConflict, e.ConversationID, e.OwnerID)
}

func (e *ConversationConflictError) Unwrap() error { return ErrConversationConflict }

func (e *ConversationConflictError) ErrorCode() string { return "subagent_conversation_conflict" }
