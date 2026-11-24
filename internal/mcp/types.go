package mcp

import "context"

// ContractEvolver validates and applies a proposed contract change.
// Phase 3 provides the semantic compiler implementation.
type ContractEvolver interface {
	Propose(ctx context.Context, req ProposalRequest) (*ProposalResult, error)
}

// BehaviorVerifier compiles an actor and runs the hidden boundary suite.
// Phase 4 provides the deterministic harness implementation.
type BehaviorVerifier interface {
	Verify(ctx context.Context, req VerifyRequest) (*VerifyResult, error)
}

// ProposalRequest is a request to evolve protocol.fbs and/or purpose.md.
type ProposalRequest struct {
	ActorName          string   `json:"actor_name"`
	IntentDescription  string   `json:"intent_description"`
	ProposedFBSSnippet string   `json:"proposed_fbs_snippet,omitempty"`
	AffectsInvariants  []string `json:"affects_invariants,omitempty"`
}

// ProposalResult reports the outcome of a contract evolution.
type ProposalResult struct {
	Status      string `json:"status"`
	PatchPath   string `json:"patch_path,omitempty"`
	Diagnostics string `json:"diagnostics,omitempty"`
}

// VerifyRequest is a request to verify an actor binary against the hidden suite.
type VerifyRequest struct {
	ActorName     string `json:"actor_name"`
	EntrypointCmd string `json:"entrypoint_cmd"`
}

// VerifyResult reports the outcome of a behavioral verification.
type VerifyResult struct {
	Status         string `json:"status"`
	CasesExecuted  int    `json:"cases_executed"`
	P99LatencyUS   int64  `json:"p99_latency_us"`
	Counterexample string `json:"counterexample,omitempty"`
}
