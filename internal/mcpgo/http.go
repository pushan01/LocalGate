package mcpgo

import (
	"net/http"

	"github.com/mark3labs/mcp-go/server"
)

type HTTPOptions struct {
	Stateless bool
}

// MountHTTP registers the MCP streamable-HTTP handler at the given path (e.g., "/mcp").
// - POST <path>: JSON-RPC requests (initialize, tools/list, tools/call, ...)
// - GET  <path>: SSE stream for server-initiated notifications
func (s *Server) MountHTTP(mux *http.ServeMux, path string, opts HTTPOptions) {
	if mux == nil || s == nil || s.impl == nil {
		return
	}
	if path == "" {
		path = "/mcp"
	}
	s.transport = "mcp_http"
	s.stateless = opts.Stateless
	hs := server.NewStreamableHTTPServer(s.impl, server.WithStateLess(opts.Stateless))
	mux.Handle(path, hs)
}
