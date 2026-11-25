package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

const resourcePurpose = `# Test system

## Actors

- **actor_demo**: a demo actor.

## Boundary Invariants (Hard Contracts)

- **INV-01:** ` + "`Msg.id`" + ` must be non-empty.
`

const resourceProtocol = `table Msg {
  id:string (required);
}

root_type Msg;
`

const resourceSpec = `{
  "spec_version": 1,
  "schema": "protocol.fbs",
  "seeds": [
    {"name": "msg", "table": "Msg", "values": {"id": "demo"}}
  ],
  "rules": [
    {"table": "Msg", "field": "id", "regex": "^.+$", "valid": ["demo"], "invalid": [""]}
  ]
}
`

func newResourceServer(t *testing.T) *Server {
	t.Helper()

	dir := t.TempDir()
	testsDir := filepath.Join(dir, "protocol_tests")
	if err := os.MkdirAll(testsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "purpose.md"), []byte(resourcePurpose), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "protocol.fbs"), []byte(resourceProtocol), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testsDir, "demo.json"), []byte(resourceSpec), 0o644); err != nil {
		t.Fatal(err)
	}

	server, err := New(Options{GovernanceDir: dir, ProtocolTestsDir: testsDir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return server
}

func protocolURI(t *testing.T, server *Server, suffix string) string {
	t.Helper()
	hash, err := server.currentProtocolHash()
	if err != nil {
		t.Fatalf("currentProtocolHash: %v", err)
	}
	return "boundary://protocol/" + strings.TrimPrefix(hash, "sha256:") + "/" + suffix
}

func readResource(t *testing.T, handler func(context.Context, mcpsdk.ReadResourceRequest) ([]mcpsdk.ResourceContents, error), uri string) []mcpsdk.ResourceContents {
	t.Helper()
	contents, err := handler(context.Background(), mcpsdk.ReadResourceRequest{
		Params: mcpsdk.ReadResourceParams{URI: uri},
	})
	if err != nil {
		t.Fatalf("read %s: %v", uri, err)
	}
	if len(contents) == 0 {
		t.Fatalf("read %s: no contents", uri)
	}
	return contents
}

func TestProtocolTestsResource(t *testing.T) {
	t.Parallel()

	server := newResourceServer(t)
	contents := readResource(t, server.handleProtocolTests, protocolURI(t, server, "tests"))

	text, ok := contents[0].(mcpsdk.TextResourceContents)
	if !ok {
		t.Fatalf("content type = %T, want text", contents[0])
	}
	if !strings.Contains(text.Text, `"demo"`) || !strings.Contains(text.Text, `"spec_version": 1`) {
		t.Fatalf("payload missing spec: %s", text.Text)
	}
	if !strings.Contains(text.Text, `"protocol": "sha256:`) {
		t.Fatalf("payload missing content hash: %s", text.Text)
	}
}

func TestProtocolTestsResourceRejectsStaleHash(t *testing.T) {
	t.Parallel()

	server := newResourceServer(t)
	_, err := server.handleProtocolTests(context.Background(), mcpsdk.ReadResourceRequest{
		Params: mcpsdk.ReadResourceParams{URI: "boundary://protocol/deadbeef/tests"},
	})
	if err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("err = %v, want stale hash error", err)
	}
}

func TestProtocolFBSResource(t *testing.T) {
	t.Parallel()

	server := newResourceServer(t)
	contents := readResource(t, server.handleProtocolFBS, protocolURI(t, server, "fbs"))

	text, ok := contents[0].(mcpsdk.TextResourceContents)
	if !ok {
		t.Fatalf("content type = %T, want text", contents[0])
	}
	if !strings.Contains(text.Text, "table Msg") {
		t.Fatalf("payload missing schema: %s", text.Text)
	}
}

func TestActorContextResource(t *testing.T) {
	t.Parallel()

	server := newResourceServer(t)
	contents := readResource(t, server.handleActorContext, "boundary://actor/actor_demo/context")

	text, ok := contents[0].(mcpsdk.TextResourceContents)
	if !ok {
		t.Fatalf("content type = %T, want text", contents[0])
	}
	if !strings.Contains(text.Text, "Boundary context for actor_demo") {
		t.Fatalf("payload missing context: %s", text.Text)
	}
}

func TestInvalidProtocolTestSpecIsRejected(t *testing.T) {
	t.Parallel()

	server := newResourceServer(t)
	bad := filepath.Join(server.opts.ProtocolTestsDir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"spec_version": 1, "unknown": true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := server.loadProtocolSpecs(); err == nil {
		t.Fatal("loadProtocolSpecs accepted an invalid spec")
	}
}

func TestProtocolTestsResourceOverMCP(t *testing.T) {
	t.Parallel()

	server := newResourceServer(t)
	client := startClient(t, server)

	result, err := client.ReadResource(context.Background(), mcpsdk.ReadResourceRequest{
		Params: mcpsdk.ReadResourceParams{URI: protocolURI(t, server, "tests")},
	})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("ReadResource returned no contents")
	}
	text, ok := result.Contents[0].(mcpsdk.TextResourceContents)
	if !ok {
		t.Fatalf("content type = %T, want text", result.Contents[0])
	}
	if !strings.Contains(text.Text, `"demo"`) {
		t.Fatalf("payload missing spec: %s", text.Text)
	}
}
