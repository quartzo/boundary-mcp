package harness

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestLoadSchemaFallsBackToFBS(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("flatc"); err != nil {
		t.Skip("flatc not installed")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "protocol.fbs"), []byte(msgSchema), 0o644); err != nil {
		t.Fatal(err)
	}

	verifier, err := NewVerifier(VerifierOptions{GovernanceDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	schema, cleanup, err := verifier.loadSchema(context.Background(), &Suite{Schema: "protocol.bfbs"})
	if err != nil {
		t.Fatalf("loadSchema: %v", err)
	}
	defer cleanup()
	if schema.Root == nil || ShortName(schema.Root.Name) != "Msg" {
		t.Fatalf("root = %+v", schema.Root)
	}
}

func TestVerifierServerMode(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("verifier uses a POSIX shell and Unix sockets")
	}

	bfbsPath, schema := compileSchemaFile(t, msgSchema)
	response, err := schema.BuildFrame(FrameSpec{
		Table:  "Msg",
		Values: map[string]any{"id": 9, "name": "ok"},
	})
	if err != nil {
		t.Fatalf("BuildFrame(response): %v", err)
	}

	root := t.TempDir()
	governanceDir := filepath.Join(root, "governance")
	actorsDir := filepath.Join(root, "actors")
	suitesDir := filepath.Join(root, "suites")
	for _, dir := range []string{governanceDir, filepath.Join(actorsDir, "actor_demo"), suitesDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	suite := Suite{
		Mode:          "server",
		Schema:        bfbsPath,
		ResponseTable: "Msg",
		Run:           []string{testExecutable(t), "-test.run=TestHelperActor"},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1",
			"HELPER_MODE":            "server",
			"HELPER_RESPONSE_HEX":    hex.EncodeToString(response),
		},
		TimeoutMS: 3000,
		Seeds: []FrameSpec{{
			Table:  "Msg",
			Values: map[string]any{"id": 1, "name": "seed"},
		}},
	}
	writeSuiteJSON(t, filepath.Join(suitesDir, "actor_demo.json"), suite)

	verifier, err := NewVerifier(VerifierOptions{
		GovernanceDir: governanceDir,
		ActorsDir:     actorsDir,
		SuitesDir:     suitesDir,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	result, err := verifier.Verify(context.Background(), VerifyRequest{
		ActorName:     "actor_demo",
		EntrypointCmd: "true",
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Status != "PASS" {
		t.Fatalf("result = %+v", result)
	}
	if result.CasesExecuted == 0 {
		t.Fatal("no cases were executed")
	}
}

func TestVerifierClientMode(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("verifier uses a POSIX shell and Unix sockets")
	}

	bfbsPath, schema := compileSchemaFile(t, msgSchema)
	request, err := schema.BuildFrame(FrameSpec{
		Table:  "Msg",
		Values: map[string]any{"id": 1, "name": "seed"},
	})
	if err != nil {
		t.Fatalf("BuildFrame(request): %v", err)
	}

	root := t.TempDir()
	governanceDir := filepath.Join(root, "governance")
	actorsDir := filepath.Join(root, "actors")
	suitesDir := filepath.Join(root, "suites")
	for _, dir := range []string{governanceDir, filepath.Join(actorsDir, "actor_demo"), suitesDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	suite := Suite{
		Mode:   "client",
		Schema: bfbsPath,
		Run:    []string{testExecutable(t), "-test.run=TestHelperActor"},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1",
			"HELPER_MODE":            "client",
			"HELPER_REQUEST_HEX":     hex.EncodeToString(request),
		},
		TimeoutMS: 3000,
		Scenarios: []ScenarioSpec{{
			Name: "happy-path",
			Expectations: []ExpectationSpec{{
				Name:  "request",
				Table: "Msg",
				Checks: []CheckSpec{
					{Field: "name", Regex: "^seed$"},
					{Field: "id", Min: floatPtr(0), Max: floatPtr(10)},
				},
				Response: []FrameSpec{{
					Table:  "Msg",
					Values: map[string]any{"id": 2, "name": "ok"},
				}},
			}},
		}},
	}
	writeSuiteJSON(t, filepath.Join(suitesDir, "actor_demo.json"), suite)

	verifier, err := NewVerifier(VerifierOptions{
		GovernanceDir: governanceDir,
		ActorsDir:     actorsDir,
		SuitesDir:     suitesDir,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	result, err := verifier.Verify(context.Background(), VerifyRequest{
		ActorName:     "actor_demo",
		EntrypointCmd: "true",
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Status != "PASS" {
		t.Fatalf("result = %+v", result)
	}
	if result.CasesExecuted != 1 {
		t.Fatalf("CasesExecuted = %d, want 1", result.CasesExecuted)
	}
}

// TestHelperActor is re-executed as the fake actor binary by the verifier
// tests. In a normal test run it is a no-op.
func TestHelperActor(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	socket := os.Getenv("BOUNDARY_SOCKET")
	switch os.Getenv("HELPER_MODE") {
	case "server":
		helperServer(socket)
	case "client":
		helperClient(socket)
	}
	os.Exit(0)
}

func helperServer(socket string) {
	listener, err := net.Listen("unix", socket)
	if err != nil {
		os.Exit(2)
	}
	defer func() { _ = listener.Close() }()

	conn, err := listener.Accept()
	if err != nil {
		os.Exit(2)
	}
	defer func() { _ = conn.Close() }()

	framed := NewFramedConn(conn)
	response, _ := hex.DecodeString(os.Getenv("HELPER_RESPONSE_HEX"))
	for {
		if _, err := framed.Recv(context.Background()); err != nil {
			return
		}
		if err := framed.Send(response); err != nil {
			return
		}
	}
}

func helperClient(socket string) {
	conn, err := net.Dial("unix", socket)
	if err != nil {
		os.Exit(2)
	}
	defer func() { _ = conn.Close() }()

	framed := NewFramedConn(conn)
	request, _ := hex.DecodeString(os.Getenv("HELPER_REQUEST_HEX"))
	if err := framed.Send(request); err != nil {
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := framed.Recv(ctx); err != nil {
		os.Exit(2)
	}
}

func testExecutable(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return path
}

func writeSuiteJSON(t *testing.T, path string, suite Suite) {
	t.Helper()
	data, err := json.MarshalIndent(suite, "", "  ")
	if err != nil {
		t.Fatalf("marshal suite: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
}

func floatPtr(v float64) *float64 { return &v }
