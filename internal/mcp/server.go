// Package mcp exposes the boundary-mcp control plane as MCP tools.
package mcp

import (
	"fmt"
	"os"
	"path/filepath"

	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Options configures the boundary MCP server.
type Options struct {
	// GovernanceDir is the directory containing purpose.md and protocol.fbs.
	GovernanceDir string
	// SDKPath is the read-only SDK mount path advertised to actors (e.g. /sdk).
	SDKPath string
	// Name is the MCP server name. Defaults to "boundary-mcp".
	Name string
	// Version is reported during initialization. Defaults to "dev".
	Version string
	// Evolver backs propose_contract_evolution. Nil makes the tool report
	// that the semantic compiler is not configured yet.
	Evolver ContractEvolver
	// Verifier backs verify_actor_behavior. Nil makes the tool report that
	// the harness is not configured yet.
	Verifier BehaviorVerifier
}

// Server owns the MCP server and its tool handlers.
type Server struct {
	opts Options
	mcp  *mcpserver.MCPServer
}

// New builds the MCP server and registers its tools.
func New(opts Options) (*Server, error) {
	if opts.GovernanceDir == "" {
		return nil, fmt.Errorf("mcp: governance directory is required")
	}
	if opts.SDKPath == "" {
		opts.SDKPath = "/sdk"
	}
	if opts.Name == "" {
		opts.Name = "boundary-mcp"
	}
	if opts.Version == "" {
		opts.Version = "dev"
	}

	for _, name := range []string{"purpose.md", "protocol.fbs"} {
		path := filepath.Join(opts.GovernanceDir, name)
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("mcp: governance file %s: %w", path, err)
		}
	}

	s := &Server{opts: opts}
	s.mcp = mcpserver.NewMCPServer(
		opts.Name,
		opts.Version,
		mcpserver.WithToolCapabilities(false),
	)
	s.registerTools()
	return s, nil
}

// MCP returns the underlying MCP server for stdio or HTTP transports.
func (s *Server) MCP() *mcpserver.MCPServer { return s.mcp }
