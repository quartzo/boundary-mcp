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
	"sort"
	"strings"
	"syscall"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"boundary-mcp/internal/compiler"
	"boundary-mcp/internal/fbs"
	"boundary-mcp/internal/judge"
	"boundary-mcp/internal/llm"
	"boundary-mcp/internal/mcp"
)

var version = "dev"

func main() {
	var (
		governanceDir = flag.String("governance", "governance", "directory containing purpose.md and protocol.fbs")
		sdkPath       = flag.String("sdk", "/sdk", "read-only SDK mount path advertised to actors")
		sdkOut        = flag.String("sdk-out", "sdk", "host directory for regenerated SDKs (go/ and rust/); empty disables")
		protocolTests = flag.String("protocol-tests", "", "protocol test spec directory (default: <governance>/protocol_tests)")
		transport     = flag.String("transport", "stdio", "transport: stdio or http")
		addr          = flag.String("addr", "127.0.0.1:8765", "HTTP listen address (transport=http)")
		token         = flag.String("token", "", "unscoped bearer token for HTTP transport; empty disables auth")
		readyFile     = flag.String("ready-file", "", "write the effective MCP URL to this file once listening")
		llmBaseURL    = flag.String("llm-base-url", envOr("BOUNDARY_LLM_BASE_URL", ""), "OpenAI-compatible API base URL")
		llmAPIKey     = flag.String("llm-api-key", envOr("BOUNDARY_LLM_API_KEY", ""), "API key for the LLM provider")
		llmModel      = flag.String("llm-model", envOr("BOUNDARY_LLM_MODEL", ""), "model used by the semantic compiler")
		judgeBaseURL  = flag.String("judge-base-url", envOr("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"), "System One API base URL")
		judgeAPIKey   = flag.String("judge-api-key", envOr("OPENROUTER_API_KEY", ""), "OpenRouter API key for JEV")
		judgeModel    = flag.String("judge-model", envOr("JUDGE_MODEL", "typesafe/jev-1.14"), "JEV model ID")
		judgeReviewAt = flag.Float64("judge-review-at", 0.40, "judge review threshold")
		judgeRejectAt = flag.Float64("judge-reject-at", 0.70, "judge reject threshold")
		syncFlag      = flag.Bool("sync", false, "sync purpose.md and protocol.fbs once, print the result and exit")
		showVersion   = flag.Bool("version", false, "print version and exit")
	)
	actorTokens := actorTokenFlags{}
	flag.Var(actorTokens, "actor-token", "actor=token; scope a bearer token to one actor (repeatable)")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	comp, err := newCompiler(
		*governanceDir, *sdkOut,
		*llmBaseURL, *llmAPIKey, *llmModel,
		*judgeBaseURL, *judgeAPIKey, *judgeModel,
		*judgeReviewAt, *judgeRejectAt,
	)
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
		GovernanceDir:    *governanceDir,
		SDKPath:          *sdkPath,
		Version:          version,
		Evolver:          evolver,
		ProtocolTestsDir: *protocolTests,
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
		auth := mcp.Auth{Token: *token, ActorTokens: actorTokens}
		if err := serveHTTP(srv, *addr, auth, *readyFile); err != nil {
			log.Fatalf("boundary-mcp: http: %v", err)
		}
	default:
		log.Fatalf("boundary-mcp: unknown transport %q (want stdio or http)", *transport)
	}
}

func newCompiler(governanceDir, sdkOut, baseURL, apiKey, model, judgeBaseURL, judgeAPIKey, judgeModel string, judgeReviewAt, judgeRejectAt float64) (*compiler.Compiler, error) {
	modelConfigured := strings.TrimSpace(model) != ""
	if !modelConfigured {
		// No semantic compiler means no proposal or sync path to judge.
		return nil, nil
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.openai.com/v1"
	}

	client, err := llm.NewOpenAI(llm.Options{BaseURL: baseURL, APIKey: apiKey, Model: model})
	if err != nil {
		return nil, err
	}

	key := judge.ResolveAPIKey(judgeAPIKey)
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("semantic compiler requires the JEV judge: set -judge-api-key, OPENROUTER_API_KEY, or opencode auth")
	}
	jev, err := judge.NewOpenRouter(judge.OpenRouterOptions{
		BaseURL: judgeBaseURL,
		APIKey:  key,
		Model:   judgeModel,
	})
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
		Judge:         judge.NewCached(jev, jev.Model()),
		JudgeMode:     compiler.JudgeEnforce,
		JudgePolicy: judge.Policy{
			ReviewAt: judgeReviewAt,
			RejectAt: judgeRejectAt,
		},
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

func serveHTTP(srv *mcp.Server, addr string, auth mcp.Auth, readyFile string) error {
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
		Handler:           srv.HTTPHandler(auth),
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

// actorTokenFlags collects repeated -actor-token actor=token values.
type actorTokenFlags map[string]string

func (t actorTokenFlags) String() string {
	parts := make([]string, 0, len(t))
	for actor := range t {
		parts = append(parts, actor+"=***")
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func (t actorTokenFlags) Set(value string) error {
	actor, token, ok := strings.Cut(value, "=")
	actor, token = strings.TrimSpace(actor), strings.TrimSpace(token)
	if !ok || actor == "" || token == "" {
		return errors.New("want actor=token")
	}
	if _, exists := t[actor]; exists {
		return fmt.Errorf("duplicate actor-token for %q", actor)
	}
	t[actor] = token
	return nil
}
