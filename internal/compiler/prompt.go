package compiler

import (
	"encoding/json"
	"fmt"
	"strings"

	"boundary-mcp/internal/mcp"
)

const systemPrompt = `You are the semantic compiler of boundary-mcp. You co-evolve three artifacts:

1. governance/purpose.md — the human-readable source of truth.
2. governance/protocol.fbs — the canonical FlatBuffers schema.
3. governance/state_machine.json — the induced state machine.

Hard rules:
- protocol.fbs must remain backward compatible. Never remove fields, never change field IDs, never change field types. Add new fields with new IDs; mark removed fields as deprecated instead.
- purpose.md must keep its structure: a top-level "# System: ..." title and the sections "## Actors", "## Protocol & State Flow" and "## Boundary Invariants (Hard Contracts)".
- Actors are snake_case identifiers and must be referenced verbatim.
- Every hard requirement must appear as an invariant with an ID such as INV-01.
- state_machine.json must be a JSON object with "states", "transitions" and "invariants".
- Return the complete contents of each artifact, not a patch.
- Do not change unrelated actors, messages or invariants.
- Be conservative: the smallest change that satisfies the request is the best one.`

const stateMachineShape = `{
  "states": ["IDLE", "PENDING", "COMPLETED"],
  "transitions": [
    {"from": "IDLE", "to": "PENDING", "message": "EnrichRequest", "actor": "actor_ingest"}
  ],
  "invariants": ["INV-01", "INV-02"]
}`

var proposalSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["summary", "purpose_md", "protocol_fbs", "state_machine_json"],
  "properties": {
    "summary": {"type": "string", "description": "One sentence describing the change."},
    "purpose_md": {"type": "string", "description": "Complete updated contents of purpose.md."},
    "protocol_fbs": {"type": "string", "description": "Complete updated contents of protocol.fbs."},
    "state_machine_json": {"type": "string", "description": "Complete state machine as a JSON object encoded in a string."}
  }
}`)

var purposeToProtocolSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["summary", "protocol_fbs", "state_machine_json"],
  "properties": {
    "summary": {"type": "string"},
    "protocol_fbs": {"type": "string", "description": "Complete updated contents of protocol.fbs."},
    "state_machine_json": {"type": "string", "description": "Complete state machine as a JSON object encoded in a string."}
  }
}`)

var protocolToPurposeSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["summary", "purpose_md", "state_machine_json"],
  "properties": {
    "summary": {"type": "string"},
    "purpose_md": {"type": "string", "description": "Complete updated contents of purpose.md."},
    "state_machine_json": {"type": "string", "description": "Complete state machine as a JSON object encoded in a string."}
  }
}`)

type proposalResponse struct {
	Summary          string `json:"summary"`
	PurposeMD        string `json:"purpose_md"`
	ProtocolFBS      string `json:"protocol_fbs"`
	StateMachineJSON string `json:"state_machine_json"`
}

type syncProtocolResponse struct {
	Summary          string `json:"summary"`
	ProtocolFBS      string `json:"protocol_fbs"`
	StateMachineJSON string `json:"state_machine_json"`
}

type syncPurposeResponse struct {
	Summary          string `json:"summary"`
	PurposeMD        string `json:"purpose_md"`
	StateMachineJSON string `json:"state_machine_json"`
}

func proposeUserPrompt(current Artifacts, req mcp.ProposalRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Actor: %s\n", req.ActorName)
	fmt.Fprintf(&b, "Intent: %s\n", req.IntentDescription)
	if len(req.AffectsInvariants) > 0 {
		fmt.Fprintf(&b, "Affected invariants: %s\n", strings.Join(req.AffectsInvariants, ", "))
	}
	if strings.TrimSpace(req.ProposedFBSSnippet) != "" {
		b.WriteString("\nSuggested FlatBuffers snippet:\n\n")
		fenced(&b, "fbs", req.ProposedFBSSnippet)
	}
	fmt.Fprintf(&b, "\nstate_machine.json shape:\n\n```json\n%s\n```\n", stateMachineShape)
	b.WriteString("\nCurrent purpose.md:\n\n")
	fenced(&b, "markdown", current.Purpose)
	b.WriteString("\nCurrent protocol.fbs:\n\n")
	fenced(&b, "fbs", current.Protocol)
	b.WriteString("\nCurrent state_machine.json:\n\n")
	fenced(&b, "json", current.StateMachine)
	return b.String()
}

func syncPurposeToProtocolPrompt(current Artifacts) string {
	var b strings.Builder
	b.WriteString("The architect edited purpose.md manually. Update protocol.fbs and state_machine.json to match the new purpose. Do not change purpose.md.\n")
	fmt.Fprintf(&b, "\nstate_machine.json shape:\n\n```json\n%s\n```\n", stateMachineShape)
	b.WriteString("\nCurrent purpose.md:\n\n")
	fenced(&b, "markdown", current.Purpose)
	b.WriteString("\nCurrent protocol.fbs:\n\n")
	fenced(&b, "fbs", current.Protocol)
	b.WriteString("\nCurrent state_machine.json:\n\n")
	fenced(&b, "json", current.StateMachine)
	return b.String()
}

func syncProtocolToPurposePrompt(previousPurpose string, current Artifacts) string {
	var b strings.Builder
	b.WriteString("The architect edited protocol.fbs manually. Update purpose.md and state_machine.json to match the new schema. Do not change protocol.fbs.\n")
	fmt.Fprintf(&b, "\nstate_machine.json shape:\n\n```json\n%s\n```\n", stateMachineShape)
	b.WriteString("\nPrevious purpose.md (before the schema edit):\n\n")
	fenced(&b, "markdown", previousPurpose)
	b.WriteString("\nCurrent protocol.fbs:\n\n")
	fenced(&b, "fbs", current.Protocol)
	b.WriteString("\nCurrent state_machine.json:\n\n")
	fenced(&b, "json", current.StateMachine)
	return b.String()
}

func fenced(b *strings.Builder, lang, content string) {
	b.WriteString("```")
	b.WriteString(lang)
	b.WriteByte('\n')
	b.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("```\n")
}
