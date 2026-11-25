package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

func TestAuthResolve(t *testing.T) {
	t.Parallel()

	auth := Auth{
		Token:       "global",
		ActorTokens: map[string]string{"scoped": "actor_ingest"},
	}

	cases := []struct {
		header string
		actor  string
		ok     bool
	}{
		{header: "Bearer scoped", actor: "actor_ingest", ok: true},
		{header: "Bearer global", actor: "", ok: true},
		{header: "Bearer nope", ok: false},
		{header: "", ok: false},
	}
	for _, tc := range cases {
		actor, ok := auth.resolve(tc.header)
		if ok != tc.ok || actor != tc.actor {
			t.Errorf("resolve(%q) = (%q, %v), want (%q, %v)", tc.header, actor, ok, tc.actor, tc.ok)
		}
	}
}

func TestBearerMiddlewareBindsActor(t *testing.T) {
	t.Parallel()

	var gotActor string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotActor, _ = ActorFromContext(r.Context())
	})

	handler := requireBearer(Auth{ActorTokens: map[string]string{"tok": "actor_ingest"}}, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, MCPPath, strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer tok")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotActor != "actor_ingest" {
		t.Fatalf("actor = %q, want actor_ingest", gotActor)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, MCPPath, strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer wrong")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAuthorizeActor(t *testing.T) {
	t.Parallel()

	server := newTestServer(t, Options{})

	if err := server.authorizeActor(context.Background(), "actor_ingest"); err != nil {
		t.Fatalf("authorizeActor(actor_ingest): %v", err)
	}
	if err := server.authorizeActor(context.Background(), "actor_ghost"); err == nil ||
		!strings.Contains(err.Error(), "not listed") {
		t.Fatalf("err = %v, want not-listed error", err)
	}

	scoped := WithActor(context.Background(), "actor_ingest")
	if err := server.authorizeActor(scoped, "actor_ingest"); err != nil {
		t.Fatalf("scoped same actor: %v", err)
	}
	if err := server.authorizeActor(scoped, "actor_enrich"); err == nil ||
		!strings.Contains(err.Error(), "scoped to actor") {
		t.Fatalf("err = %v, want scope error", err)
	}
}

func TestScopedTokenOverHTTP(t *testing.T) {
	t.Parallel()

	server := newTestServer(t, Options{})
	httpServer := httptest.NewServer(server.HTTPHandler(Auth{
		ActorTokens: map[string]string{"tok": "actor_ingest"},
	}))
	t.Cleanup(httpServer.Close)

	c, err := client.NewStreamableHttpClient(
		httpServer.URL+MCPPath,
		transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer tok"}),
	)
	if err != nil {
		t.Fatalf("NewStreamableHttpClient: %v", err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	initReq := mcpsdk.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcpsdk.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcpsdk.Implementation{Name: "test", Version: "0"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	res := callTool(t, c, "get_boundary_context", map[string]any{"actor_name": "actor_ingest"})
	if res.IsError {
		t.Fatalf("scoped actor rejected: %s", toolText(res))
	}

	res = callTool(t, c, "get_boundary_context", map[string]any{"actor_name": "actor_enrich"})
	if !res.IsError || !strings.Contains(toolText(res), "scoped to actor") {
		t.Fatalf("cross-actor result = %q (isError=%v), want a scope error", toolText(res), res.IsError)
	}

	res = callTool(t, c, "propose_contract_evolution", map[string]any{
		"actor_name":         "actor_enrich",
		"intent_description": "add a field",
	})
	if !res.IsError || !strings.Contains(toolText(res), "scoped to actor") {
		t.Fatalf("cross-actor proposal = %q (isError=%v), want a scope error", toolText(res), res.IsError)
	}

	if _, err := c.ReadResource(ctx, mcpsdk.ReadResourceRequest{
		Params: mcpsdk.ReadResourceParams{URI: "boundary://actor/actor_enrich/context"},
	}); err == nil {
		t.Fatal("cross-actor resource was allowed")
	}
}
