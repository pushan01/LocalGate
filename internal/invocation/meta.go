package invocation

import "strings"

const (
	TransportMCPHTTP    = "mcp_http"
	TransportMCPStdio   = "mcp_stdio"
	TransportMCPRuntime = "mcp_runtime"
	TransportREST       = "rest"
	TransportToolHTTP   = "tool_http"
	TransportAdmin      = "admin"
	TransportSystem     = "system"
)

const (
	SessionModeStateful  = "stateful"
	SessionModeStateless = "stateless"
	SessionModeNone      = "none"
)

const (
	SessionSourceServerManaged  = "server_managed"
	SessionSourceClientSupplied = "client_supplied"
	SessionSourceNone           = "none"
)

// Meta describes the origin and optional MCP session correlation for one tool
// invocation. Session IDs are correlation data only and are never treated as
// authentication credentials.
type Meta struct {
	Transport       string `json:"transport,omitempty"`
	MCPSessionID    string `json:"session_id,omitempty"`
	SessionMode     string `json:"session_mode,omitempty"`
	SessionIDSource string `json:"session_id_source,omitempty"`
	RequestID       string `json:"request_id,omitempty"`
	Requester       string `json:"requester,omitempty"`
	WorkspaceID     string `json:"workspace_id,omitempty"`
}

func (m Meta) Normalize() Meta {
	m.Transport = strings.TrimSpace(m.Transport)
	m.MCPSessionID = strings.TrimSpace(m.MCPSessionID)
	m.SessionMode = strings.TrimSpace(m.SessionMode)
	m.SessionIDSource = strings.TrimSpace(m.SessionIDSource)
	m.RequestID = strings.TrimSpace(m.RequestID)
	m.Requester = strings.TrimSpace(m.Requester)
	m.WorkspaceID = strings.TrimSpace(m.WorkspaceID)

	if m.SessionMode == "" {
		m.SessionMode = SessionModeNone
	}
	if m.MCPSessionID == "" {
		m.SessionIDSource = SessionSourceNone
	} else if m.SessionIDSource == "" {
		m.SessionIDSource = SessionSourceClientSupplied
	}
	return m
}
