//go:build live

package compiler

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"boundary-mcp/internal/fbs"
	"boundary-mcp/internal/judge"
	"boundary-mcp/internal/mcp"
)

// TestLiveJudgeProposal exercises the real compiler -> judge path through
// OpenRouter. Run with: go test -tags=live ./internal/compiler -run TestLiveJudgeProposal
// It uses the opencode auth fallback when OPENROUTER_API_KEY is unset.
func TestLiveJudgeProposal(t *testing.T) {
	flatcPath, err := exec.LookPath("flatc")
	if err != nil {
		t.Skip("flatc not installed")
	}
	key := judge.ResolveAPIKey("")
	if key == "" {
		t.Skip("no OpenRouter key")
	}

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "purpose.md"), basePurpose)
	writeTestFile(t, filepath.Join(dir, "protocol.fbs"), baseProtocol)
	writeTestFile(t, filepath.Join(dir, "state_machine.json"), baseState)

	fake := &fakeLLM{responses: map[string]string{
		"contract_evolution": proposalJSON(t, basePurpose, addField(baseProtocol, "requested_at:ulong (id: 2);"), baseState),
	}}
	jev, err := judge.NewOpenRouter(judge.OpenRouterOptions{APIKey: key})
	if err != nil {
		t.Fatal(err)
	}

	c, err := New(Options{
		GovernanceDir: dir,
		Flatc:         fbs.NewCompiler(flatcPath),
		LLM:           fake,
		Judge:         judge.NewCached(jev, jev.Model()),
		JudgeMode:     JudgeEnforce,
		JudgePolicy:   judge.Policy{ReviewAt: 0.60, RejectAt: 0.70},
		Clock:         newClock(),
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := c.Propose(context.Background(), mcp.ProposalRequest{
		ActorName:         "actor_ingest",
		IntentDescription: "add requested_at",
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if res.Status != "committed" {
		t.Fatalf("status = %q, diagnostics = %q", res.Status, res.Diagnostics)
	}

	entries := historyEntries(t, dir)
	if len(entries) != 1 {
		t.Fatalf("history entries = %v", entries)
	}
	meta := readFile(t, filepath.Join(dir, "history", entries[0], "meta.json"))
	if !strings.Contains(meta, `"judge"`) {
		t.Fatalf("history meta has no judge record: %s", meta)
	}
}
