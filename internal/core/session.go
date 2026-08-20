package core

import "time"

type SessionStatus string

const (
	SessionStarting SessionStatus = "starting"
	SessionRunning  SessionStatus = "running"
	SessionExited   SessionStatus = "exited"
	SessionFailed   SessionStatus = "failed"
	SessionClosed   SessionStatus = "closed"
)

type Session struct {
	ID              string        `json:"id"`
	TaskID          string        `json:"task_id,omitempty"`
	Requester       string        `json:"requester,omitempty"`
	Transport       string        `json:"transport,omitempty"`
	MCPSessionID    string        `json:"mcp_session_id,omitempty"`
	SessionMode     string        `json:"session_mode,omitempty"`
	SessionIDSource string        `json:"session_id_source,omitempty"`
	RequestID       string        `json:"request_id,omitempty"`
	Command         string        `json:"command,omitempty"`
	CWD             string        `json:"cwd,omitempty"`
	Shell           string        `json:"shell,omitempty"`
	Login           bool          `json:"login,omitempty"`
	TTY             bool          `json:"tty,omitempty"`
	Status          SessionStatus `json:"status"`
	ExitCode        int           `json:"exit_code,omitempty"`
	ErrorMsg        string        `json:"error_msg,omitempty"`
	CreatedAt       time.Time     `json:"created_at"`
	StartedAt       time.Time     `json:"started_at,omitempty"`
	EndedAt         time.Time     `json:"ended_at,omitempty"`
	LastActiveAt    time.Time     `json:"last_active_at,omitempty"`
	LastInputAt     time.Time     `json:"last_input_at,omitempty"`
	LastReadAt      time.Time     `json:"last_read_at,omitempty"`
	LastOutputAt    time.Time     `json:"last_output_at,omitempty"`
	LastOffset      int           `json:"last_offset,omitempty"`
	OutputError     string        `json:"output_error,omitempty"`
}

type SessionOutput struct {
	Offset    int       `json:"offset"`
	Stream    string    `json:"stream"`
	Data      string    `json:"data"`
	Continues bool      `json:"continues,omitempty"`
	Time      time.Time `json:"time"`
}

type SessionReadResult struct {
	Items           []SessionOutput `json:"items"`
	NextOffset      int             `json:"next_offset"`
	BaseOffset      int             `json:"base_offset"`
	Truncated       bool            `json:"truncated"`
	BufferTruncated bool            `json:"buffer_truncated"`
	CursorExpired   bool            `json:"cursor_expired,omitempty"`
	HasMore         bool            `json:"has_more"`
	OutputError     string          `json:"output_error,omitempty"`
}

type SessionCleanupOptions struct {
	FinishedTTL time.Duration `json:"finished_ttl,omitempty"`
	IdleTTL     time.Duration `json:"idle_ttl,omitempty"`
	MaxSessions int           `json:"max_sessions,omitempty"`
}

type SessionSweepResult struct {
	Closed  int `json:"closed"`
	Deleted int `json:"deleted"`
}
