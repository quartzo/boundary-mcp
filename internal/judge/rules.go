package judge

import (
	"fmt"
	"sort"
	"strings"
)

// Rule is one semantic check expressed as a Noul question.
type Rule struct {
	ID           string
	Instructions string
	True         string
	False        string
}

// Rules is the proposal rule set. True means the rule is broken.
var Rules = []Rule{
	{
		ID:           "removed_or_renamed_field",
		Instructions: "Does the change remove an existing field, change its field ID, or rename it instead of adding a new field or deprecating the old one?",
		True:         "A field present before is absent after, its ID changed, or its name changed without deprecation.",
		False:        "Every existing field remains with the same ID and type; changes are additive or mark fields deprecated.",
	},
	{
		ID:           "dropped_invariant",
		Instructions: "Does an invariant from the previous purpose.md disappear, or lose its hard-contract requirement?",
		True:         "An INV-XX present before is missing after, or is rewritten as optional guidance rather than a hard contract.",
		False:        "Every previous invariant remains present with the same hard-contract meaning.",
	},
	{
		ID:           "out_of_scope",
		Instructions: "Does the change modify actors, messages or invariants that are unrelated to the requesting actor or the declared intent?",
		True:         "The diff changes an actor, message or invariant that neither involves `actor` nor follows from `intent`.",
		False:        "The diff stays within the requesting actor and the declared intent.",
	},
	{
		ID:           "intent_mismatch",
		Instructions: "Do the updated purpose.md and protocol.fbs fail to implement the declared intent?",
		True:         "The diff does not add, change or remove the contract elements needed by `intent`.",
		False:        "The diff implements the declared intent.",
	},
	{
		ID:           "purpose_protocol_drift",
		Instructions: "Do the updated purpose.md and protocol.fbs describe different behavior?",
		True:         "A message, field, state or invariant described as active in purpose_after is absent or contradictory in protocol_after, or vice versa.",
		False:        "purpose_after and protocol_after describe the same behavior. A field that remains in protocol_after as deprecated, and is described as deprecated in purpose_after, is not drift.",
	},
	{
		ID:           "unsafe_optionality",
		Instructions: "Does the change make a previously required or validated field optional, or remove validation, without justification in the intent?",
		True:         "A required field becomes optional, a constraint disappears, or validation is removed without an explicit intent.",
		False:        "Requiredness and validation are preserved, or the declared intent justifies the change.",
	},
	{
		ID:           "invented_scope",
		Instructions: "Does the change introduce a new actor, message or invariant that is not implied by the intent or the existing purpose?",
		True:         "A new actor, message or invariant appears that is unrelated to `intent` and the existing scope.",
		False:        "New elements are required by the intent or already implied by the purpose.",
	},
}

type question struct {
	Type         string   `json:"type"`
	Instructions string   `json:"instructions"`
	Criteria     criteria `json:"criteria"`
}

type criteria struct {
	True  string `json:"true"`
	False string `json:"false"`
}

func questions() map[string]question {
	out := make(map[string]question, len(Rules))
	for _, rule := range Rules {
		out[rule.ID] = question{
			Type:         "noul",
			Instructions: rule.Instructions,
			Criteria:     criteria{True: rule.True, False: rule.False},
		}
	}
	return out
}

// Summary renders the findings that triggered the decision.
func (v Verdict) Summary() string {
	if len(v.Findings) == 0 {
		return string(v.Decision)
	}
	parts := make([]string, 0, len(v.Findings))
	for _, finding := range v.Findings {
		parts = append(parts, fmt.Sprintf("%s=%.2f", finding.Rule, finding.Probability))
	}
	return strings.Join(parts, ", ")
}

func sortedFindings(rules map[string]float64, threshold float64) []Finding {
	findings := make([]Finding, 0, len(rules))
	for rule, probability := range rules {
		if probability >= threshold {
			findings = append(findings, Finding{Rule: rule, Probability: probability})
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Probability == findings[j].Probability {
			return findings[i].Rule < findings[j].Rule
		}
		return findings[i].Probability > findings[j].Probability
	})
	return findings
}
