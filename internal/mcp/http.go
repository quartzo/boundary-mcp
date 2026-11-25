package mcp

import (
	"io"
	"net/http"

	mcpserver "github.com/mark3labs/mcp-go/server"
)

// HealthPath is the unauthenticated readiness endpoint.
const HealthPath = "/health"

// MCPPath is the Streamable HTTP endpoint path.
const MCPPath = "/mcp"

// HTTPHandler mounts the MCP Streamable HTTP endpoint and a health endpoint.
//
// When auth is enabled, /mcp requires "Authorization: Bearer <token>". A
// scoped token binds subsequent tool and resource calls to one actor. The
// health endpoint is always unauthenticated so launchers can wait for
// readiness before starting a coder.
func (s *Server) HTTPHandler(auth Auth) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(HealthPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})

	var mcpHandler http.Handler = mcpserver.NewStreamableHTTPServer(s.mcp)
	if auth.enabled() {
		mcpHandler = requireBearer(auth, mcpHandler)
	}
	mux.Handle(MCPPath, mcpHandler)

	return mux
}
