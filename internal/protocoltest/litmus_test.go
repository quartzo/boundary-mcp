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

func TestLitmusCheck(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("flatc"); err != nil {
		t.Skip("flatc not installed")
	}

	governanceDir := filepath.Join("..", "..", "examples", "litmus", "governance")
	specs, err := LoadSpecDir(filepath.Join(governanceDir, "protocol_tests"))
	if err != nil {
		t.Fatalf("LoadSpecDir: %v", err)
	}
	if len(specs) == 0 {
		t.Fatal("no specs")
	}

	for name, spec := range specs {
		report, err := Check(context.Background(), ExpandOptions{GovernanceDir: governanceDir}, spec)
		if err != nil {
			t.Fatalf("%s: Check: %v", name, err)
		}
		if !report.Pass() {
			t.Fatalf("%s: failures: %v", name, report.Failures)
		}
		if report.Accept == 0 || report.Reject == 0 {
			t.Fatalf("%s: accept=%d reject=%d, want both", name, report.Accept, report.Reject)
		}
	}
}
