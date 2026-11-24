package judge

import (
	"strings"
	"testing"
)

func TestExtractInvariants(t *testing.T) {
	t.Parallel()

	got := extractInvariants("INV-02 appears first. INV-01 next. INV-02 again.")
	want := []string{"INV-02", "INV-01"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestBuildState(t *testing.T) {
	t.Parallel()

	in := Input{
		Actor:              "actor_ingest",
		Intent:             "add requested_at",
		AffectedInvariants: []string{"INV-02"},
		PurposeBefore:      "## Boundary Invariants\n- **INV-01:** correlation_id.\n",
		PurposeAfter:       "## Boundary Invariants\n- **INV-01:** correlation_id.\n- **INV-02:** requested_at.\n",
		ProtocolBefore:     "table EnrichRequest { correlation_id:string; }\n",
		ProtocolAfter:      "table EnrichRequest { correlation_id:string; requested_at:ulong; }\n",
		StateMachineBefore: `{"states":["IDLE"]}`,
		StateMachineAfter:  `{"states":["IDLE","PENDING"]}`,
		FlatcStatus:        "pass",
	}

	got := buildState(in)
	if got.Actor != "actor_ingest" || got.Intent != "add requested_at" || got.Flatc != "pass" {
		t.Fatalf("state = %+v", got)
	}
	if len(got.InvariantsBefore) != 1 || got.InvariantsBefore[0] != "INV-01" {
		t.Fatalf("InvariantsBefore = %v", got.InvariantsBefore)
	}
	if len(got.InvariantsAfter) != 2 {
		t.Fatalf("InvariantsAfter = %v", got.InvariantsAfter)
	}
	if !strings.Contains(got.ProtocolDiff, "+table EnrichRequest { correlation_id:string; requested_at:ulong; }") &&
		!strings.Contains(got.ProtocolDiff, "requested_at") {
		t.Fatalf("ProtocolDiff = %q", got.ProtocolDiff)
	}
	if got.PurposeAfter != in.PurposeAfter || got.ProtocolAfter != in.ProtocolAfter {
		t.Fatal("after artifacts were not copied into the state")
	}
}

func TestBuildStateDefaultFlatc(t *testing.T) {
	t.Parallel()

	got := buildState(Input{Actor: "a"})
	if got.Flatc != "pass" {
		t.Fatalf("Flatc = %q, want pass", got.Flatc)
	}
	if got.PurposeDiff != "" || got.ProtocolDiff != "" {
		t.Fatal("unchanged artifacts should produce empty diffs")
	}
}
