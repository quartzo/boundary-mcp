package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

const testPurpose = `# System: Test Pipeline

## Actors
1. **actor_ingest** (Emitter): Sends requests.
2. **actor_enrich** (Receiver): Handles requests.

## Protocol & State Flow
- **Transition 1:** actor_ingest sends EnrichRequest.
- **Transition 2:** actor_enrich replies.

## Boundary Invariants (Hard Contracts)
- **INV-01:** correlation_id must be non-empty.
`

const testSchema = `namespace boundary.test;

table EnrichRequest {
  correlation_id:string (id: 0);
}

root_type EnrichRequest;
`

func TestListTools(t *testing.T) {
	t.Parallel()

	c := startClient(t, newTestServer(t, Options{}))
	res, err := c.ListTools(context.Background(), mcpsdk.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	got := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, want := range []string{"get_boundary_context", "propose_contract_evolution", "get_protocol_tests"} {
		if !got[want] {
			t.Errorf("tool %q is not registered (got %v)", want, got)
		}
	}
}

func TestGetBoundaryContext(t *testing.T) {
	t.Parallel()

	c := startClient(t, newTestServer(t, Options{SDKPath: "/sdk"}))
	res := callTool(t, c, "get_boundary_context", map[string]any{"actor_name": "actor_ingest"})
	if res.IsError {
		t.Fatalf("tool error: %s", toolText(res))
	}

	text := toolText(res)
	for _, want := range []string{"**actor_ingest**", "INV-01", "table EnrichRequest", "/sdk"} {
		if !strings.Contains(text, want) {
			t.Errorf("result does not contain %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "**actor_enrich** (Receiver)") {
		t.Errorf("result leaked another actor:\n%s", text)
	}

	structured, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent = %T, want map[string]any", res.StructuredContent)
	}
	if structured["actor_name"] != "actor_ingest" {
		t.Errorf("actor_name = %v, want actor_ingest", structured["actor_name"])
	}
}

func TestGetBoundaryContextUnknownActor(t *testing.T) {
	t.Parallel()

	c := startClient(t, newTestServer(t, Options{}))
	res := callTool(t, c, "get_boundary_context", map[string]any{"actor_name": "actor_ghost"})
	if !res.IsError {
		t.Fatal("expected a tool error for an unknown actor")
	}
	if !strings.Contains(toolText(res), "not listed") {
		t.Fatalf("error text = %q", toolText(res))
	}
}

func TestGetBoundaryContextRequiresActor(t *testing.T) {
	t.Parallel()

	c := startClient(t, newTestServer(t, Options{}))
	res := callTool(t, c, "get_boundary_context", map[string]any{})
	if !res.IsError {
		t.Fatal("expected a tool error for a missing actor_name")
	}
}

func TestUnavailableDependencies(t *testing.T) {
	t.Parallel()

	c := startClient(t, newTestServer(t, Options{}))

	propose := callTool(t, c, "propose_contract_evolution", map[string]any{
		"actor_name":         "actor_ingest",
		"intent_description": "need a new field",
	})
	if !propose.IsError || !strings.Contains(toolText(propose), "semantic compiler not configured") {
		t.Fatalf("propose result = %q (isError=%v), want a configuration error", toolText(propose), propose.IsError)
	}
}

func TestProposeContractEvolutionDelegates(t *testing.T) {
	t.Parallel()

	evolver := &fakeEvolver{result: &ProposalResult{Status: "committed", PatchPath: "governance/history/1.patch"}}
	c := startClient(t, newTestServer(t, Options{Evolver: evolver}))

	res := callTool(t, c, "propose_contract_evolution", map[string]any{
		"actor_name":           "actor_ingest",
		"intent_description":   "add requested_at",
		"proposed_fbs_snippet": "requested_at:ulong (id: 2);",
		"affects_invariants":   []any{"INV-02"},
	})
	if res.IsError {
		t.Fatalf("tool error: %s", toolText(res))
	}

	if evolver.got.ActorName != "actor_ingest" {
		t.Errorf("ActorName = %q", evolver.got.ActorName)
	}
	if evolver.got.IntentDescription != "add requested_at" {
		t.Errorf("IntentDescription = %q", evolver.got.IntentDescription)
	}
	if evolver.got.ProposedFBSSnippet != "requested_at:ulong (id: 2);" {
		t.Errorf("ProposedFBSSnippet = %q", evolver.got.ProposedFBSSnippet)
	}
	if len(evolver.got.AffectsInvariants) != 1 || evolver.got.AffectsInvariants[0] != "INV-02" {
		t.Errorf("AffectsInvariants = %v", evolver.got.AffectsInvariants)
	}
}

func TestProposeRequiresIntent(t *testing.T) {
	t.Parallel()

	c := startClient(t, newTestServer(t, Options{Evolver: &fakeEvolver{}}))
	res := callTool(t, c, "propose_contract_evolution", map[string]any{"actor_name": "actor_ingest"})
	if !res.IsError {
		t.Fatal("expected a tool error for a missing intent_description")
	}
}

func TestProposeRejectsUnlistedActor(t *testing.T) {
	t.Parallel()

	evolver := &fakeEvolver{result: &ProposalResult{Status: "committed"}}
	c := startClient(t, newTestServer(t, Options{Evolver: evolver}))

	res := callTool(t, c, "propose_contract_evolution", map[string]any{
		"actor_name":         "actor_ghost",
		"intent_description": "add a field",
	})
	if !res.IsError || !strings.Contains(toolText(res), "not listed") {
		t.Fatalf("result = %q (isError=%v), want a not-listed error", toolText(res), res.IsError)
	}
	if evolver.got.ActorName != "" {
		t.Fatalf("evolver was called for %q", evolver.got.ActorName)
	}
}

func TestNewRequiresGovernanceFiles(t *testing.T) {
	t.Parallel()

	_, err := New(Options{GovernanceDir: t.TempDir()})
	if err == nil {
		t.Fatal("New with missing governance files returned nil error")
	}
}

type fakeEvolver struct {
	got    ProposalRequest
	result *ProposalResult
	err    error
}

func (f *fakeEvolver) Propose(_ context.Context, req ProposalRequest) (*ProposalResult, error) {
	f.got = req
	return f.result, f.err
}

func newTestServer(t *testing.T, opts Options) *Server {
	t.Helper()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "purpose.md"), testPurpose)
	writeFile(t, filepath.Join(dir, "protocol.fbs"), testSchema)
	opts.GovernanceDir = dir

	s, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func startClient(t *testing.T, s *Server) *client.Client {
	t.Helper()

	c, err := client.NewInProcessClient(s.MCP())
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("client.Start: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	initReq := mcpsdk.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcpsdk.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcpsdk.Implementation{Name: "test", Version: "0"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		t.Fatalf("client.Initialize: %v", err)
	}
	return c
}

func callTool(t *testing.T, c *client.Client, name string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()

	req := mcpsdk.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	res, err := c.CallTool(context.Background(), req)
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return res
}

func toolText(res *mcpsdk.CallToolResult) string {
	var b strings.Builder
	for _, content := range res.Content {
		if text, ok := content.(mcpsdk.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}
