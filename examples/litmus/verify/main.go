// Command verify runs the litmus hidden suites against both example actors.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"boundary-mcp/internal/harness"
	"boundary-mcp/internal/mcp"
)

func main() {
	root := flag.String("root", ".", "litmus example root")
	flag.Parse()

	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		fail("resolve root: %v", err)
	}

	governanceDir := filepath.Join(absoluteRoot, "governance")
	verifier, err := harness.NewVerifier(harness.VerifierOptions{
		GovernanceDir: governanceDir,
		ActorsDir:     filepath.Join(absoluteRoot, "actors"),
		SuitesDir:     filepath.Join(governanceDir, "hidden_tests"),
	})
	if err != nil {
		fail("new verifier: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	failed := false
	for _, actor := range []string{"actor_enrich", "actor_ingest"} {
		result, err := verifier.Verify(ctx, mcp.VerifyRequest{ActorName: actor})
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: ERROR: %v\n", actor, err)
			failed = true
			continue
		}

		fmt.Printf("%s: %s (cases=%d, p99=%dus)\n", actor, result.Status, result.CasesExecuted, result.P99LatencyUS)
		if result.Counterexample != "" {
			fmt.Printf("  counterexample: %s\n", result.Counterexample)
		}
		if result.Status != "PASS" {
			failed = true
		}
	}

	if failed {
		os.Exit(1)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "litmus: "+format+"\n", args...)
	os.Exit(1)
}
