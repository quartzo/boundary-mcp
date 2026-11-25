package mcp

import "context"

// ContractEvolver validates and applies a proposed contract change.
// Phase 3 provides the semantic compiler implementation.
type ContractEvolver interface {
	Propose(ctx context.Context, req ProposalRequest) (*ProposalResult, error)
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
