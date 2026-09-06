package subagent

import "context"

type ProviderCreateRequest struct {
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
