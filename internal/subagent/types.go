package subagent

import "time"

type Status string

const (
	StatusReady    Status = "ready"
	StatusBusy     Status = "busy"
	StatusDegraded Status = "degraded"
	StatusClosed   Status = "closed"
)

type ProviderState struct {
	ConversationID  string            `json:"conversation_id,omitempty"`
	ConversationURL string            `json:"conversation_url,omitempty"`
	ProjectID       string            `json:"project_id,omitempty"`
	Hints           map[string]string `json:"hints,omitempty"`
}

type Artifact struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	MIMEType string `json:"mime_type,omitempty"`
	FileName string `json:"file_name,omitempty"`
	Path     string `json:"path,omitempty"`
	Size     int64  `json:"size,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	Alt      string `json:"alt,omitempty"`
}

type Subagent struct {
	ID             string        `json:"id"`
	Name           string        `json:"name,omitempty"`
	SubagentType   string        `json:"subagent_type,omitempty"`
	Provider       string        `json:"provider"`
	Status         Status        `json:"status"`
	Instruction    string        `json:"instruction,omitempty"`
	ProviderState  ProviderState `json:"provider_state"`
	Artifacts      []Artifact    `json:"artifacts,omitempty"`
	LastError      string        `json:"last_error,omitempty"`
	LastErrorCode  string        `json:"last_error_code,omitempty"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
	LastObservedAt time.Time     `json:"last_observed_at,omitempty"`
}

type Reply struct {
	Text      string     `json:"text"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
}

type Observation struct {
	Generating bool  `json:"generating"`
	Reply      Reply `json:"reply"`
}

type CreateRequest struct {
	Name         string
	SubagentType string
	Provider     string
	Instruction  string
	Options      map[string]string
}

type CreateResult struct {
	Subagent Subagent
}

type ObserveResult struct {
	Subagent   Subagent
	Reply      Reply
	Refreshed  bool
	RetryAfter time.Duration
}
