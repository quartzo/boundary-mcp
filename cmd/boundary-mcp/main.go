// Command boundary-mcp serves the boundary control plane as an MCP server.
//
// It runs over stdio by default. In sandboxed sessions, the launcher starts it
// outside the mount namespace and opencode connects over loopback Streamable
// HTTP, so governance files never enter the actor sandbox.
//
// When an LLM is configured, boundary-mcp also exposes the semantic compiler
// (propose_contract_evolution) and a one-shot -sync mode.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"boundary-mcp/internal/compiler"
	"boundary-mcp/internal/fbs"
	"boundary-mcp/internal/llm"
	"boundary-mcp/internal/mcp"
)

var version = "dev"

func main() {
	var (
		governanceDir = flag.String("governance", "governance", "directory containing purpose.md and protocol.fbs")
		sdkPath       = flag.String("sdk", "/sdk", "read-only SDK mount path advertised to actors")
		sdkOut        = flag.String("sdk-out", "sdk", "host directory for regenerated SDKs (go/ and rust/); empty disables")
		transport     = flag.String("transport", "stdio", "transport: stdio or http")
		addr          = flag.String("addr", "127.0.0.1:8765", "HTTP listen address (transport=http)")
		token         = flag.String("token", "", "bearer token for HTTP transport; empty disables auth")
		readyFile     = flag.String("ready-file", "", "write the effective MCP URL to this file once listening")
		llmBaseURL    = flag.String("llm-base-url", envOr("BOUNDARY_LLM_BASE_URL", ""), "OpenAI-compatible API base URL")
		llmAPIKey     = flag.String("llm-api-key", envOr("BOUNDARY_LLM_API_KEY", ""), "API key for the LLM provider")
		llmModel      = flag.String("llm-model", envOr("BOUNDARY_LLM_MODEL", ""), "model used by the semantic compiler")
		syncFlag      = flag.Bool("sync", false, "sync purpose.md and protocol.fbs once, print the result and exit")
		showVersion   = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	comp, err := newCompiler(*governanceDir, *sdkOut, *llmBaseURL, *llmAPIKey, *llmModel)
	if err != nil {
		log.Fatalf("boundary-mcp: %v", err)
	}

	if *syncFlag {
		if comp == nil {
			log.Fatal("boundary-mcp: -sync requires an LLM (set -llm-model or BOUNDARY_LLM_MODEL)")
		}
		runSync(comp)
		return
	}

	var evolver mcp.ContractEvolver
	if comp != nil {
		evolver = comp
	}
	srv, err := mcp.New(mcp.Options{
		GovernanceDir: *governanceDir,
		SDKPath:       *sdkPath,
		Version:       version,
		Evolver:       evolver,
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

func newCompiler(governanceDir, sdkOut, baseURL, apiKey, model string) (*compiler.Compiler, error) {
	if strings.TrimSpace(model) == "" {
		return nil, nil
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.openai.com/v1"
	}

	client, err := llm.NewOpenAI(llm.Options{BaseURL: baseURL, APIKey: apiKey, Model: model})
	if err != nil {
		return nil, err
	}

	var targets []compiler.SDKTarget
	if strings.TrimSpace(sdkOut) != "" {
		targets = []compiler.SDKTarget{
			{Language: fbs.Go, Dir: filepath.Join(sdkOut, "go")},
			{Language: fbs.Rust, Dir: filepath.Join(sdkOut, "rust")},
		}
	}

	return compiler.New(compiler.Options{
		GovernanceDir: governanceDir,
		LLM:           client,
		SDKTargets:    targets,
	})
}

func runSync(comp *compiler.Compiler) {
	res, err := comp.Sync(context.Background())
	if err != nil {
		log.Fatalf("boundary-mcp: sync: %v", err)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(res); err != nil {
		log.Fatalf("boundary-mcp: sync: %v", err)
	}
	if res.Status == "rejected" {
		os.Exit(1)
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

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
