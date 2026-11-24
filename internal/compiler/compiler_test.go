package compiler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"boundary-mcp/internal/fbs"
	"boundary-mcp/internal/judge"
	"boundary-mcp/internal/llm"
	"boundary-mcp/internal/mcp"
)

const (
	basePurpose = `# System: Test Pipeline

## Actors
1. **actor_ingest** (Emitter): Sends enrichment requests.
2. **actor_enrich** (Receiver): Answers enrichment requests.

## Protocol & State Flow
- **Initial State:** ` + "`IDLE`" + `
- **Transition 1:** ` + "`actor_ingest`" + ` sends ` + "`EnrichRequest`" + `.

## Boundary Invariants (Hard Contracts)
- **INV-01:** correlation_id must be non-empty.
`

	baseProtocol = `namespace boundary.test;

table EnrichRequest {
  correlation_id:string (id: 0);
  document_id:string (id: 1);
}

root_type EnrichRequest;
`

	baseState = `{"states":["IDLE","PENDING"],"transitions":[{"from":"IDLE","to":"PENDING","message":"EnrichRequest","actor":"actor_ingest"}],"invariants":["INV-01"]}`
)

func TestProposeCommitsCompatibleChange(t *testing.T) {
	t.Parallel()

	nextProtocol := strings.Replace(
		baseProtocol,
		"  document_id:string (id: 1);\n",
		"  document_id:string (id: 1);\n  requested_at:ulong (id: 2);\n",
		1,
	)
	fake := &fakeLLM{responses: map[string]string{
		"contract_evolution": proposalJSON(t, basePurpose, nextProtocol, baseState),
	}}
	c, dir := newTestCompiler(t, fake)

	res, err := c.Propose(context.Background(), mcp.ProposalRequest{
		ActorName:         "actor_ingest",
		IntentDescription: "add requested_at to EnrichRequest",
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if res.Status != "committed" {
		t.Fatalf("status = %q, diagnostics = %q", res.Status, res.Diagnostics)
	}
	if res.PatchPath == "" {
		t.Fatal("PatchPath is empty")
	}

	protocol := readFile(t, filepath.Join(dir, "protocol.fbs"))
	if !strings.Contains(protocol, "requested_at:ulong (id: 2);") {
		t.Fatalf("protocol.fbs was not updated:\n%s", protocol)
	}

	entries := historyEntries(t, dir)
	if len(entries) != 1 {
		t.Fatalf("history entries = %v, want 1", entries)
	}
	patch := readFile(t, filepath.Join(dir, "history", entries[0], "diff.patch"))
	if !strings.Contains(patch, "+  requested_at:ulong (id: 2);") {
		t.Fatalf("patch does not contain the added field:\n%s", patch)
	}
	meta := readFile(t, filepath.Join(dir, "history", entries[0], "meta.json"))
	if !strings.Contains(meta, `"actor": "actor_ingest"`) || !strings.Contains(meta, `"direction": "propose"`) {
		t.Fatalf("meta = %s", meta)
	}
}

func TestProposeRejectsBreakingChange(t *testing.T) {
	t.Parallel()

	breaking := strings.Replace(baseProtocol, "  document_id:string (id: 1);\n", "", 1)
	fake := &fakeLLM{responses: map[string]string{
		"contract_evolution": proposalJSON(t, basePurpose, breaking, baseState),
	}}
	c, dir := newTestCompiler(t, fake)

	res, err := c.Propose(context.Background(), mcp.ProposalRequest{
		ActorName:         "actor_ingest",
		IntentDescription: "remove document_id",
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if res.Status != "rejected" {
		t.Fatalf("status = %q, want rejected", res.Status)
	}
	if !strings.Contains(res.Diagnostics, "document_id") {
		t.Fatalf("diagnostics = %q, want the deleted field", res.Diagnostics)
	}
	if len(historyEntries(t, dir)) != 0 {
		t.Fatal("rejected proposal wrote history")
	}
	if got := readFile(t, filepath.Join(dir, "protocol.fbs")); got != baseProtocol {
		t.Fatal("rejected proposal modified protocol.fbs")
	}
}

func TestProposeRejectsInvalidSyntax(t *testing.T) {
	t.Parallel()

	fake := &fakeLLM{responses: map[string]string{
		"contract_evolution": proposalJSON(t, basePurpose, "table Broken {\n", baseState),
	}}
	c, _ := newTestCompiler(t, fake)

	res, err := c.Propose(context.Background(), mcp.ProposalRequest{
		ActorName:         "actor_ingest",
		IntentDescription: "break the schema",
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if res.Status != "rejected" || !strings.Contains(res.Diagnostics, "syntax") {
		t.Fatalf("status = %q, diagnostics = %q", res.Status, res.Diagnostics)
	}
}

func TestProposeRejectsRemovedActor(t *testing.T) {
	t.Parallel()

	nextPurpose := strings.Replace(basePurpose, "1. **actor_ingest** (Emitter): Sends enrichment requests.\n", "", 1)
	fake := &fakeLLM{responses: map[string]string{
		"contract_evolution": proposalJSON(t, nextPurpose, baseProtocol, baseState),
	}}
	c, _ := newTestCompiler(t, fake)

	res, err := c.Propose(context.Background(), mcp.ProposalRequest{
		ActorName:         "actor_ingest",
		IntentDescription: "remove myself",
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if res.Status != "rejected" || !strings.Contains(res.Diagnostics, "actor") {
		t.Fatalf("status = %q, diagnostics = %q", res.Status, res.Diagnostics)
	}
}

func TestProposeUnchanged(t *testing.T) {
	t.Parallel()

	fake := &fakeLLM{responses: map[string]string{
		"contract_evolution": proposalJSON(t, basePurpose, baseProtocol, baseState),
	}}
	c, dir := newTestCompiler(t, fake)

	res, err := c.Propose(context.Background(), mcp.ProposalRequest{
		ActorName:         "actor_ingest",
		IntentDescription: "no-op",
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if res.Status != "unchanged" {
		t.Fatalf("status = %q, want unchanged", res.Status)
	}
	if len(historyEntries(t, dir)) != 0 {
		t.Fatal("unchanged proposal wrote history")
	}
}

func TestSyncPurposeToProtocol(t *testing.T) {
	t.Parallel()

	fake := &fakeLLM{responses: map[string]string{
		"contract_evolution": proposalJSON(t, basePurpose, addField(baseProtocol, "requested_at:ulong (id: 2);"), baseState),
	}}
	c, dir := newTestCompiler(t, fake)

	if res, err := c.Propose(context.Background(), mcp.ProposalRequest{ActorName: "actor_ingest", IntentDescription: "first change"}); err != nil || res.Status != "committed" {
		t.Fatalf("initial Propose: res=%+v err=%v", res, err)
	}

	editedPurpose := basePurpose + "- **INV-02:** requested_at must be set by the emitter.\n"
	writeTestFile(t, filepath.Join(dir, "purpose.md"), editedPurpose)

	nextProtocol := addField(baseProtocol, "requested_at:ulong (id: 2);\n  retry_count:uint (id: 3);")
	fake.responses["purpose_to_protocol"] = syncProtocolJSON(t, nextProtocol, baseState)

	res, err := c.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.Status != "committed" {
		t.Fatalf("status = %q, diagnostics = %q", res.Status, res.Diagnostics)
	}
	if got := readFile(t, filepath.Join(dir, "protocol.fbs")); !strings.Contains(got, "retry_count:uint (id: 3);") {
		t.Fatalf("protocol.fbs was not updated:\n%s", got)
	}
	if got := readFile(t, filepath.Join(dir, "purpose.md")); got != editedPurpose {
		t.Fatal("sync modified the manually edited purpose.md")
	}
	if entries := historyEntries(t, dir); len(entries) != 2 {
		t.Fatalf("history entries = %v, want 2", entries)
	}
}

func TestSyncProtocolToPurpose(t *testing.T) {
	t.Parallel()

	fake := &fakeLLM{responses: map[string]string{
		"contract_evolution": proposalJSON(t, basePurpose, addField(baseProtocol, "requested_at:ulong (id: 2);"), baseState),
	}}
	c, dir := newTestCompiler(t, fake)

	if res, err := c.Propose(context.Background(), mcp.ProposalRequest{ActorName: "actor_ingest", IntentDescription: "first change"}); err != nil || res.Status != "committed" {
		t.Fatalf("initial Propose: res=%+v err=%v", res, err)
	}

	nextProtocol := addField(readFile(t, filepath.Join(dir, "protocol.fbs")), "source:string (id: 3);")
	writeTestFile(t, filepath.Join(dir, "protocol.fbs"), nextProtocol)

	nextPurpose := basePurpose + "- **INV-03:** records carry a source label.\n"
	fake.responses["protocol_to_purpose"] = syncPurposeJSON(t, nextPurpose, baseState)

	res, err := c.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.Status != "committed" {
		t.Fatalf("status = %q, diagnostics = %q", res.Status, res.Diagnostics)
	}
	if got := readFile(t, filepath.Join(dir, "purpose.md")); !strings.Contains(got, "INV-03") {
		t.Fatalf("purpose.md was not updated:\n%s", got)
	}
	if got := readFile(t, filepath.Join(dir, "protocol.fbs")); got != nextProtocol {
		t.Fatal("sync modified the manually edited protocol.fbs")
	}
}

func TestSyncAmbiguous(t *testing.T) {
	t.Parallel()

	fake := &fakeLLM{responses: map[string]string{
		"contract_evolution": proposalJSON(t, basePurpose, addField(baseProtocol, "requested_at:ulong (id: 2);"), baseState),
	}}
	c, dir := newTestCompiler(t, fake)

	if res, err := c.Propose(context.Background(), mcp.ProposalRequest{ActorName: "actor_ingest", IntentDescription: "first change"}); err != nil || res.Status != "committed" {
		t.Fatalf("initial Propose: res=%+v err=%v", res, err)
	}

	writeTestFile(t, filepath.Join(dir, "purpose.md"), basePurpose+"- **INV-02:** extra.\n")
	writeTestFile(t, filepath.Join(dir, "protocol.fbs"), addField(baseProtocol, "manual:string (id: 4);"))

	if _, err := c.Sync(context.Background()); err == nil {
		t.Fatal("Sync accepted two changed artifacts")
	}
}

func TestProposeRegeneratesSDKs(t *testing.T) {
	t.Parallel()

	nextProtocol := addField(baseProtocol, "requested_at:ulong (id: 2);")
	fake := &fakeLLM{responses: map[string]string{
		"contract_evolution": proposalJSON(t, basePurpose, nextProtocol, baseState),
	}}
	c, _ := newTestCompiler(t, fake)

	sdkRoot := t.TempDir()
	c.opts.SDKTargets = []SDKTarget{{Language: fbs.Go, Dir: filepath.Join(sdkRoot, "go")}}

	res, err := c.Propose(context.Background(), mcp.ProposalRequest{
		ActorName:         "actor_ingest",
		IntentDescription: "regenerate SDK",
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if res.Status != "committed" {
		t.Fatalf("status = %q, diagnostics = %q", res.Status, res.Diagnostics)
	}
	if res.Diagnostics != "" {
		t.Fatalf("unexpected SDK warning: %s", res.Diagnostics)
	}

	var generated []string
	err = filepath.WalkDir(filepath.Join(sdkRoot, "go"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".go") {
			generated = append(generated, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk SDK: %v", err)
	}
	if len(generated) == 0 {
		t.Fatal("no Go bindings were generated")
	}
}

func TestProposeJudgeAllows(t *testing.T) {
	t.Parallel()

	fake := &fakeLLM{responses: map[string]string{
		"contract_evolution": proposalJSON(t, basePurpose, addField(baseProtocol, "requested_at:ulong (id: 2);"), baseState),
	}}
	c, dir := newTestCompiler(t, fake)

	semanticJudge := &fakeJudge{assessment: &judge.Assessment{
		Rules: map[string]float64{"dropped_invariant": 0.05},
		Model: "test-model",
	}}
	c.opts.Judge = semanticJudge
	c.opts.JudgeMode = JudgeEnforce

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
	if semanticJudge.calls != 1 {
		t.Fatalf("judge calls = %d, want 1", semanticJudge.calls)
	}
	if semanticJudge.last.Actor != "actor_ingest" || semanticJudge.last.Intent != "add requested_at" {
		t.Fatalf("judge input = %+v", semanticJudge.last)
	}

	entries := historyEntries(t, dir)
	if len(entries) != 1 {
		t.Fatalf("history entries = %v", entries)
	}
	meta := readFile(t, filepath.Join(dir, "history", entries[0], "meta.json"))
	if !strings.Contains(meta, `"judge"`) || !strings.Contains(meta, `"decision": "allow"`) {
		t.Fatalf("meta does not contain the judge verdict: %s", meta)
	}
}

func TestProposeJudgeRejects(t *testing.T) {
	t.Parallel()

	fake := &fakeLLM{responses: map[string]string{
		"contract_evolution": proposalJSON(t, basePurpose, addField(baseProtocol, "requested_at:ulong (id: 2);"), baseState),
	}}
	c, dir := newTestCompiler(t, fake)

	semanticJudge := &fakeJudge{assessment: &judge.Assessment{
		Rules: map[string]float64{"dropped_invariant": 0.85},
		Model: "test-model",
	}}
	c.opts.Judge = semanticJudge
	c.opts.JudgeMode = JudgeEnforce

	res, err := c.Propose(context.Background(), mcp.ProposalRequest{
		ActorName:         "actor_ingest",
		IntentDescription: "add requested_at",
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if res.Status != "rejected" {
		t.Fatalf("status = %q", res.Status)
	}
	if !strings.Contains(res.Diagnostics, "dropped_invariant") {
		t.Fatalf("diagnostics = %q", res.Diagnostics)
	}
	if len(historyEntries(t, dir)) != 0 {
		t.Fatal("rejected proposal wrote history")
	}
	if got := readFile(t, filepath.Join(dir, "protocol.fbs")); got != baseProtocol {
		t.Fatal("rejected proposal modified protocol.fbs")
	}
}

func TestProposeJudgeReviews(t *testing.T) {
	t.Parallel()

	fake := &fakeLLM{responses: map[string]string{
		"contract_evolution": proposalJSON(t, basePurpose, addField(baseProtocol, "requested_at:ulong (id: 2);"), baseState),
	}}
	c, _ := newTestCompiler(t, fake)

	c.opts.Judge = &fakeJudge{assessment: &judge.Assessment{
		Rules: map[string]float64{"intent_mismatch": 0.50},
	}}
	c.opts.JudgeMode = JudgeEnforce

	res, err := c.Propose(context.Background(), mcp.ProposalRequest{
		ActorName:         "actor_ingest",
		IntentDescription: "add requested_at",
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if res.Status != "escalated" {
		t.Fatalf("status = %q, want escalated", res.Status)
	}
	if !strings.Contains(res.Diagnostics, "intent_mismatch") {
		t.Fatalf("diagnostics = %q", res.Diagnostics)
	}
}

func TestProposeJudgeError(t *testing.T) {
	t.Parallel()

	fake := &fakeLLM{responses: map[string]string{
		"contract_evolution": proposalJSON(t, basePurpose, addField(baseProtocol, "requested_at:ulong (id: 2);"), baseState),
	}}
	c, _ := newTestCompiler(t, fake)

	c.opts.Judge = &fakeJudge{err: errors.New("provider down")}
	c.opts.JudgeMode = JudgeEnforce

	if _, err := c.Propose(context.Background(), mcp.ProposalRequest{
		ActorName:         "actor_ingest",
		IntentDescription: "add requested_at",
	}); err == nil {
		t.Fatal("Propose returned nil error when the judge failed")
	}
}

func TestNewEnforceRequiresJudge(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "purpose.md"), basePurpose)
	writeTestFile(t, filepath.Join(dir, "protocol.fbs"), baseProtocol)
	writeTestFile(t, filepath.Join(dir, "state_machine.json"), baseState)

	_, err := New(Options{
		GovernanceDir: dir,
		LLM:           &fakeLLM{},
		JudgeMode:     JudgeEnforce,
	})
	if err == nil {
		t.Fatal("New with enforce mode and no judge returned nil error")
	}
}

type fakeJudge struct {
	calls      int
	last       judge.Input
	assessment *judge.Assessment
	err        error
}

func (f *fakeJudge) Evaluate(_ context.Context, in judge.Input) (*judge.Assessment, error) {
	f.calls++
	f.last = in
	return f.assessment, f.err
}

func TestProposeLLMError(t *testing.T) {
	t.Parallel()

	fake := &fakeLLM{err: errors.New("provider down")}
	c, _ := newTestCompiler(t, fake)

	if _, err := c.Propose(context.Background(), mcp.ProposalRequest{ActorName: "actor_ingest", IntentDescription: "x"}); err == nil {
		t.Fatal("Propose returned nil error when the LLM failed")
	}
}

func addField(schema, field string) string {
	return strings.Replace(schema, "}\n\nroot_type", "\n  "+field+"\n}\n\nroot_type", 1)
}

type fakeLLM struct {
	mu        sync.Mutex
	responses map[string]string
	requests  []llm.Request
	err       error
}

func (f *fakeLLM) CompleteJSON(_ context.Context, req llm.Request) (*llm.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}
	content, ok := f.responses[req.SchemaName]
	if !ok {
		return nil, fmt.Errorf("no fake response for schema %q", req.SchemaName)
	}
	return &llm.Response{JSON: []byte(content), Model: "fake-model"}, nil
}

func newTestCompiler(t *testing.T, fake *fakeLLM) (*Compiler, string) {
	t.Helper()

	flatc, err := exec.LookPath("flatc")
	if err != nil {
		t.Skip("flatc not installed; skipping compiler integration test")
	}

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "purpose.md"), basePurpose)
	writeTestFile(t, filepath.Join(dir, "protocol.fbs"), baseProtocol)
	writeTestFile(t, filepath.Join(dir, "state_machine.json"), baseState)

	c, err := New(Options{
		GovernanceDir: dir,
		Flatc:         fbs.NewCompiler(flatc),
		LLM:           fake,
		Clock:         newClock(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, dir
}

func newClock() func() time.Time {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	tick := 0
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		tick++
		return start.Add(time.Duration(tick) * time.Second)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func historyEntries(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(dir, "history"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read history: %v", err)
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	slices.Sort(names)
	return names
}

func proposalJSON(t *testing.T, purpose, protocol, state string) string {
	t.Helper()
	data, err := json.Marshal(proposalResponse{
		Summary:          "test proposal",
		PurposeMD:        purpose,
		ProtocolFBS:      protocol,
		StateMachineJSON: state,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func syncProtocolJSON(t *testing.T, protocol, state string) string {
	t.Helper()
	data, err := json.Marshal(syncProtocolResponse{
		Summary:          "sync",
		ProtocolFBS:      protocol,
		StateMachineJSON: state,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func syncPurposeJSON(t *testing.T, purpose, state string) string {
	t.Helper()
	data, err := json.Marshal(syncPurposeResponse{
		Summary:          "sync",
		PurposeMD:        purpose,
		StateMachineJSON: state,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
