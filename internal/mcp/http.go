package mcp

import (
	"crypto/subtle"
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
// When token is non-empty, /mcp requires "Authorization: Bearer <token>".
// The health endpoint is always unauthenticated so launchers can wait for
// readiness before starting an agent.
func (s *Server) HTTPHandler(token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(HealthPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})

	var mcpHandler http.Handler = mcpserver.NewStreamableHTTPServer(s.mcp)
	if token != "" {
		mcpHandler = requireBearer(token, mcpHandler)
	}
	mux.Handle(MCPPath, mcpHandler)

	return mux
}

func requireBearer(token string, next http.Handler) http.Handler {
	want := "Bearer " + token
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="boundary-mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
