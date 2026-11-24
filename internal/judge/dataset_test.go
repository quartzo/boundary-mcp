package judge

import "testing"

func TestSyntheticCases(t *testing.T) {
	t.Parallel()

	cases := SyntheticCases()
	if len(cases) != 100 {
		t.Fatalf("cases = %d, want 100", len(cases))
	}

	seen := make(map[string]bool, len(cases))
	counts := make(map[Decision]int)
	for _, c := range cases {
		if c.Name == "" || c.Actor == "" || c.Intent == "" {
			t.Fatalf("incomplete case: %+v", c)
		}
		if c.PurposeBefore == c.PurposeAfter && c.ProtocolBefore == c.ProtocolAfter {
			t.Fatalf("case %s has no changes", c.Name)
		}
		if seen[c.Name] {
			t.Fatalf("duplicate case name %s", c.Name)
		}
		seen[c.Name] = true
		counts[c.Want]++
	}

	for _, want := range []Decision{Allow, Review, Reject} {
		if counts[want] == 0 {
			t.Fatalf("no cases labeled %q", want)
		}
	}
}
