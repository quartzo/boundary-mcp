// Package judge provides an advisory semantic judge for contract proposals.
//
// The judge returns calibrated probabilities for explicit semantic rules. It is
// never the contract oracle: flatc and the harness remain authoritative.
package judge

import (
	"context"
	"time"
)

// Decision is the policy outcome for one assessment.
type Decision string

const (
	// Allow means no semantic rule crossed the review threshold.
	Allow Decision = "allow"
	// Review means at least one rule is uncertain.
	Review Decision = "review"
	// Reject means at least one rule crossed the rejection threshold.
	Reject Decision = "reject"
)

// Judge evaluates a proposal and returns per-rule probabilities.
type Judge interface {
	Evaluate(ctx context.Context, in Input) (*Assessment, error)
}

// Input is the proposal state sent to the judge.
type Input struct {
	Actor              string
	Intent             string
	AffectedInvariants []string
	PurposeBefore      string
	PurposeAfter       string
	ProtocolBefore     string
	ProtocolAfter      string
	StateMachineBefore string
	StateMachineAfter  string
	// FlatcStatus is "pass" or the gatekeeper diagnostics. The compiler only
	// calls the judge after flatc passes, but the field documents the state.
	FlatcStatus string
}

// Assessment is the raw judge output.
type Assessment struct {
	Rules        map[string]float64
	Model        string
	RequestID    string
	InputTokens  int
	OutputTokens int
	CostUSD      float64
	Latency      time.Duration
	Cached       bool
}

// Finding is one rule that crossed the review threshold.
type Finding struct {
	Rule        string  `json:"rule"`
	Probability float64 `json:"probability"`
}

// Verdict is the policy decision derived from an assessment.
type Verdict struct {
	Decision       Decision  `json:"decision"`
	MaxRule        string    `json:"max_rule,omitempty"`
	MaxProbability float64   `json:"max_probability"`
	Findings       []Finding `json:"findings,omitempty"`
}
