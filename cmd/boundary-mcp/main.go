// Command boundary-mcp serves the boundary control plane as an MCP server.
//
// It runs over stdio by default. In sandboxed sessions, the launcher starts it
// outside the mount namespace and opencode connects over loopback Streamable
// HTTP, so governance files never enter the actor sandbox.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"boundary-mcp/internal/mcp"
)

var version = "dev"

func main() {
	var (
		governanceDir = flag.String("governance", "governance", "directory containing purpose.md and protocol.fbs")
		sdkPath       = flag.String("sdk", "/sdk", "read-only SDK mount path advertised to actors")
		transport     = flag.String("transport", "stdio", "transport: stdio or http")
		addr          = flag.String("addr", "127.0.0.1:8765", "HTTP listen address (transport=http)")
		token         = flag.String("token", "", "bearer token for HTTP transport; empty disables auth")
		readyFile     = flag.String("ready-file", "", "write the effective MCP URL to this file once listening")
		showVersion   = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	srv, err := mcp.New(mcp.Options{
		GovernanceDir: *governanceDir,
		SDKPath:       *sdkPath,
		Version:       version,
	})
	if err != nil {
		log.Fatalf("boundary-mcp: %v", err)
	}

	switch *transport {
	case "stdio":
		if err := mcpserver.ServeStdio(srv.MCP()); err != nil {
			log.Fatalf("boundary-mcp: stdio: %v", err)
		}
	case "http":
		if err := serveHTTP(srv, *addr, *token, *readyFile); err != nil {
			log.Fatalf("boundary-mcp: http: %v", err)
		}
	default:
		log.Fatalf("boundary-mcp: unknown transport %q (want stdio or http)", *transport)
	}
}

func serveHTTP(srv *mcp.Server, addr, token, readyFile string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	mcpURL := "http://" + ln.Addr().String() + mcp.MCPPath
	if readyFile != "" {
		if err := writeReadyFile(readyFile, mcpURL+"\n"); err != nil {
			_ = ln.Close()
			return err
		}
		defer func() { _ = os.Remove(readyFile) }()
	}
	log.Printf("boundary-mcp %s listening on %s (health %s)", version, mcpURL, mcp.HealthPath)

	httpSrv := &http.Server{
		Handler:           srv.HTTPHandler(token),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.Serve(ln) }()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-signals:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpSrv.Shutdown(ctx)
	}
}

func writeReadyFile(path, content string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
