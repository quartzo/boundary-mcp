package judge

import (
	"regexp"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

var invariantPattern = regexp.MustCompile(`\bINV-[0-9]+\b`)

type state struct {
	Actor              string   `json:"actor"`
	Intent             string   `json:"intent"`
	Flatc              string   `json:"flatc"`
	AffectedInvariants []string `json:"affected_invariants,omitempty"`
	InvariantsBefore   []string `json:"invariants_before"`
	InvariantsAfter    []string `json:"invariants_after"`
	PurposeDiff        string   `json:"purpose_diff"`
	ProtocolDiff       string   `json:"protocol_diff"`
	StateMachineDiff   string   `json:"state_machine_diff"`
	PurposeAfter       string   `json:"purpose_after"`
	ProtocolAfter      string   `json:"protocol_after"`
}

func buildState(in Input) state {
	flatc := strings.TrimSpace(in.FlatcStatus)
	if flatc == "" {
		flatc = "pass"
	}
	return state{
		Actor:              in.Actor,
		Intent:             in.Intent,
		Flatc:              flatc,
		AffectedInvariants: in.AffectedInvariants,
		InvariantsBefore:   extractInvariants(in.PurposeBefore),
		InvariantsAfter:    extractInvariants(in.PurposeAfter),
		PurposeDiff:        unifiedDiff("purpose.md", in.PurposeBefore, in.PurposeAfter),
		ProtocolDiff:       unifiedDiff("protocol.fbs", in.ProtocolBefore, in.ProtocolAfter),
		StateMachineDiff:   unifiedDiff("state_machine.json", in.StateMachineBefore, in.StateMachineAfter),
		PurposeAfter:       in.PurposeAfter,
		ProtocolAfter:      in.ProtocolAfter,
	}
}

func extractInvariants(purpose string) []string {
	matches := invariantPattern.FindAllString(purpose, -1)
	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if seen[match] {
			continue
		}
		seen[match] = true
		out = append(out, match)
	}
	return out
}

func unifiedDiff(name, before, after string) string {
	if before == after {
		return ""
	}
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(before),
		B:        difflib.SplitLines(after),
		FromFile: "a/" + name,
		ToFile:   "b/" + name,
		Context:  3,
	})
	if err != nil {
		return ""
	}
	return diff
}
