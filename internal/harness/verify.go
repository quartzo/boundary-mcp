package harness

// VerifyRequest asks the verifier to build an actor and run its suite.
//
// The MCP server adapts its own request type to this one in cmd/boundary-mcp,
// so the harness does not depend on the mcp package.
type VerifyRequest struct {
	ActorName     string `json:"actor_name"`
	EntrypointCmd string `json:"entrypoint_cmd"`
}

// VerifyResult is the outcome of a behavioral verification run.
type VerifyResult struct {
	Status         string `json:"status"`
	CasesExecuted  int    `json:"cases_executed"`
	P99LatencyUS   int64  `json:"p99_latency_us"`
	Counterexample string `json:"counterexample,omitempty"`
}
