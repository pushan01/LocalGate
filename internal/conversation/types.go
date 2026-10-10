package conversation

import "context"

type CreateRequest struct {
	Prompt  string
	Project string
}

type Conversation struct {
	ID        string
	URL       string
	ProjectID string
}

type Creator interface {
	CreateConversation(context.Context, CreateRequest) (Conversation, error)
}
