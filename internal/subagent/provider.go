package subagent

import "context"

type ProviderCreateRequest struct {
	WorkspaceID string
	Name        string
	Instruction string
	Options     map[string]string
}

type Provider interface {
	Create(context.Context, ProviderCreateRequest) (ProviderState, error)
	Send(context.Context, ProviderState, string) (ProviderState, error)
	Poll(context.Context, ProviderState) (ProviderState, Observation, error)
	Wait(context.Context, ProviderState) (ProviderState, Observation, error)
	Recover(context.Context, ProviderState) (ProviderState, error)
	Close(context.Context, ProviderState) error
}

// Archiver is an optional provider capability. Archive must only return
// success after any configured remote archival step has completed and been
// verified. Providers may return the state unchanged when remote movement is
// intentionally disabled by configuration.
type Archiver interface {
	Archive(context.Context, ProviderState) (ProviderState, error)
}
