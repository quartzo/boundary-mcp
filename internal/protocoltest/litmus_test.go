package protocoltest

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLitmusSpecExpands(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("flatc"); err != nil {
		t.Skip("flatc not installed")
	}

	governanceDir := filepath.Join("..", "..", "examples", "litmus", "governance")
	spec, err := LoadSpec(filepath.Join(governanceDir, "protocol_tests", "protocol.json"))
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	artifact, err := Expand(context.Background(), ExpandOptions{GovernanceDir: governanceDir}, spec)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	if len(artifact.Cases) == 0 {
		t.Fatal("no structural cases generated")
	}
	if len(artifact.Assertions) != 3 {
		t.Fatalf("assertions = %d, want 3", len(artifact.Assertions))
	}

	var reject int
	for _, c := range artifact.Cases {
		if c.Expect == ExpectReject {
			reject++
		}
	}
	if reject == 0 {
		t.Fatal("no rejection cases generated")
	}
}
