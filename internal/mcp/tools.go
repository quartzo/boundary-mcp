package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"

	"boundary-mcp/internal/governance"
)

type getBoundaryContextInput struct {
	ActorName string `json:"actor_name"`
}

type boundaryContextOutput struct {
	ActorName string `json:"actor_name"`
	Purpose   string `json:"purpose"`
	Protocol  string `json:"protocol"`
	SDKPath   string `json:"sdk_path"`
}

type proposeInput struct {
	ActorName          string   `json:"actor_name"`
	IntentDescription  string   `json:"intent_description"`
	ProposedFBSSnippet string   `json:"proposed_fbs_snippet,omitempty"`
	AffectsInvariants  []string `json:"affects_invariants,omitempty"`
}

type verifyInput struct {
	ActorName     string `json:"actor_name"`
	EntrypointCmd string `json:"entrypoint_cmd"`
}

func (s *Server) registerTools() {
	s.mcp.AddTool(
		mcpsdk.NewTool("get_boundary_context",
			mcpsdk.WithDescription("Return the boundary view for an actor: filtered purpose.md, the full protocol.fbs, and the read-only SDK path."),
			mcpsdk.WithString("actor_name",
				mcpsdk.Required(),
				mcpsdk.Description("Actor requesting its boundary context (e.g. actor_ingest)."),
			),
			mcpsdk.WithOutputSchema[boundaryContextOutput](),
			mcpsdk.WithReadOnlyHintAnnotation(true),
		),
		s.handleGetBoundaryContext,
	)

	s.mcp.AddTool(
		mcpsdk.NewTool("propose_contract_evolution",
			mcpsdk.WithDescription("Propose a change to protocol.fbs and/or purpose.md. The change is validated by flatc before being committed."),
			mcpsdk.WithString("actor_name",
				mcpsdk.Required(),
				mcpsdk.Description("Actor proposing the change."),
			),
			mcpsdk.WithString("intent_description",
				mcpsdk.Required(),
				mcpsdk.Description("What the actor needs and why the current contract is insufficient."),
			),
			mcpsdk.WithString("proposed_fbs_snippet",
				mcpsdk.Description("Optional FlatBuffers snippet to use as a starting point."),
			),
			mcpsdk.WithArray("affects_invariants",
				mcpsdk.Description("Invariant IDs this proposal may affect (e.g. INV-02)."),
				mcpsdk.WithStringItems(),
			),
			mcpsdk.WithOutputSchema[ProposalResult](),
		),
		s.handleProposeContractEvolution,
	)

	s.mcp.AddTool(
		mcpsdk.NewTool("verify_actor_behavior",
			mcpsdk.WithDescription("Compile an actor and run the hidden deterministic boundary tests against it."),
			mcpsdk.WithString("actor_name",
				mcpsdk.Required(),
				mcpsdk.Description("Actor to verify."),
			),
			mcpsdk.WithString("entrypoint_cmd",
				mcpsdk.Required(),
				mcpsdk.Description("Command used to build the actor inside its workspace (e.g. go build ./...)."),
			),
			mcpsdk.WithOutputSchema[VerifyResult](),
		),
		s.handleVerifyActorBehavior,
	)
}

func (s *Server) handleGetBoundaryContext(_ context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	var in getBoundaryContextInput
	if err := req.BindArguments(&in); err != nil {
		return mcpsdk.NewToolResultErrorf("invalid arguments: %v", err), nil
	}
	actor := strings.TrimSpace(in.ActorName)
	if actor == "" {
		return mcpsdk.NewToolResultError("actor_name is required"), nil
	}

	out, err := s.boundaryContext(actor)
	if err != nil {
		return mcpsdk.NewToolResultError(err.Error()), nil
	}
	return mcpsdk.NewToolResultStructured(out, formatBoundaryContext(out)), nil
}

func (s *Server) boundaryContext(actor string) (boundaryContextOutput, error) {
	purpose, err := governance.LoadPurpose(filepath.Join(s.opts.GovernanceDir, "purpose.md"))
	if err != nil {
		return boundaryContextOutput{}, err
	}
	view, err := purpose.ViewFor(actor)
	if err != nil {
		return boundaryContextOutput{}, err
	}

	protocolPath := filepath.Join(s.opts.GovernanceDir, "protocol.fbs")
	protocol, err := os.ReadFile(protocolPath)
	if err != nil {
		return boundaryContextOutput{}, fmt.Errorf("mcp: read protocol: %w", err)
	}

	return boundaryContextOutput{
		ActorName: actor,
		Purpose:   view,
		Protocol:  string(protocol),
		SDKPath:   s.opts.SDKPath,
	}, nil
}

func (s *Server) handleProposeContractEvolution(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	var in proposeInput
	if err := req.BindArguments(&in); err != nil {
		return mcpsdk.NewToolResultErrorf("invalid arguments: %v", err), nil
	}
	if strings.TrimSpace(in.ActorName) == "" {
		return mcpsdk.NewToolResultError("actor_name is required"), nil
	}
	if strings.TrimSpace(in.IntentDescription) == "" {
		return mcpsdk.NewToolResultError("intent_description is required"), nil
	}
	if s.opts.Evolver == nil {
		return mcpsdk.NewToolResultError("propose_contract_evolution is not available: semantic compiler not configured (Phase 3)"), nil
	}

	res, err := s.opts.Evolver.Propose(ctx, ProposalRequest{
		ActorName:          in.ActorName,
		IntentDescription:  in.IntentDescription,
		ProposedFBSSnippet: in.ProposedFBSSnippet,
		AffectsInvariants:  in.AffectsInvariants,
	})
	if err != nil {
		return mcpsdk.NewToolResultErrorf("proposal failed: %v", err), nil
	}
	if res == nil {
		return mcpsdk.NewToolResultError("proposal returned no result"), nil
	}
	return mcpsdk.NewToolResultStructured(res, formatProposalResult(res)), nil
}

func (s *Server) handleVerifyActorBehavior(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	var in verifyInput
	if err := req.BindArguments(&in); err != nil {
		return mcpsdk.NewToolResultErrorf("invalid arguments: %v", err), nil
	}
	if strings.TrimSpace(in.ActorName) == "" {
		return mcpsdk.NewToolResultError("actor_name is required"), nil
	}
	if strings.TrimSpace(in.EntrypointCmd) == "" {
		return mcpsdk.NewToolResultError("entrypoint_cmd is required"), nil
	}
	if s.opts.Verifier == nil {
		return mcpsdk.NewToolResultError("verify_actor_behavior is not available: deterministic harness not configured (Phase 4)"), nil
	}

	res, err := s.opts.Verifier.Verify(ctx, VerifyRequest{
		ActorName:     in.ActorName,
		EntrypointCmd: in.EntrypointCmd,
	})
	if err != nil {
		return mcpsdk.NewToolResultErrorf("verification failed: %v", err), nil
	}
	if res == nil {
		return mcpsdk.NewToolResultError("verification returned no result"), nil
	}
	return mcpsdk.NewToolResultStructured(res, formatVerifyResult(res)), nil
}

func formatBoundaryContext(out boundaryContextOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Boundary context for %s\n\n", out.ActorName)
	fmt.Fprintf(&b, "Read-only SDK mount: `%s`\n\n", out.SDKPath)
	b.WriteString(out.Purpose)
	b.WriteString("\n---\n\n## protocol.fbs\n\n```fbs\n")
	b.WriteString(out.Protocol)
	if !strings.HasSuffix(out.Protocol, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("```\n")
	return b.String()
}

func formatProposalResult(res *ProposalResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "status: %s\n", res.Status)
	if res.PatchPath != "" {
		fmt.Fprintf(&b, "patch: %s\n", res.PatchPath)
	}
	if res.Diagnostics != "" {
		fmt.Fprintf(&b, "\ndiagnostics:\n%s\n", res.Diagnostics)
	}
	return b.String()
}

func formatVerifyResult(res *VerifyResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "status: %s\n", res.Status)
	fmt.Fprintf(&b, "cases_executed: %d\n", res.CasesExecuted)
	fmt.Fprintf(&b, "p99_latency_us: %d\n", res.P99LatencyUS)
	if res.Counterexample != "" {
		fmt.Fprintf(&b, "\ncounterexample:\n%s\n", res.Counterexample)
	}
	return b.String()
}
