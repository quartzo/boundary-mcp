package judge

import "testing"

func TestPolicyDecide(t *testing.T) {
	t.Parallel()

	policy := DefaultPolicy()
	tests := []struct {
		name  string
		rules map[string]float64
		want  Decision
	}{
		{name: "allow", rules: map[string]float64{"a": 0.1, "b": 0.2}, want: Allow},
		{name: "review", rules: map[string]float64{"a": 0.5}, want: Review},
		{name: "reject", rules: map[string]float64{"a": 0.9}, want: Reject},
		{name: "empty", rules: map[string]float64{}, want: Review},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			verdict := policy.Decide(&Assessment{Rules: tt.rules})
			if verdict.Decision != tt.want {
				t.Fatalf("Decision = %q, want %q", verdict.Decision, tt.want)
			}
		})
	}
}

func TestPolicyMaxRuleAndFindings(t *testing.T) {
	t.Parallel()

	verdict := DefaultPolicy().Decide(&Assessment{Rules: map[string]float64{
		"intent_mismatch":   0.12,
		"dropped_invariant": 0.81,
		"out_of_scope":      0.44,
	}})

	if verdict.MaxRule != "dropped_invariant" {
		t.Fatalf("MaxRule = %q", verdict.MaxRule)
	}
	if verdict.MaxProbability != 0.81 {
		t.Fatalf("MaxProbability = %v", verdict.MaxProbability)
	}
	if len(verdict.Findings) != 2 {
		t.Fatalf("Findings = %+v, want 2", verdict.Findings)
	}
	if verdict.Findings[0].Rule != "dropped_invariant" || verdict.Findings[1].Rule != "out_of_scope" {
		t.Fatalf("Findings order = %+v", verdict.Findings)
	}
}

func TestPolicyNormalizesThresholds(t *testing.T) {
	t.Parallel()

	policy := Policy{ReviewAt: 0.8, RejectAt: 0.2}
	verdict := policy.Decide(&Assessment{Rules: map[string]float64{"a": 0.75}})
	if verdict.Decision != Reject {
		t.Fatalf("Decision = %q, want reject with default thresholds", verdict.Decision)
	}
}

func TestDefaultPolicyCalibration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		probability float64
		want        Decision
	}{
		{0.39, Allow},
		{0.40, Review},
		{0.69, Review},
		{0.70, Reject},
	}
	for _, tt := range tests {
		verdict := DefaultPolicy().Decide(&Assessment{Rules: map[string]float64{"rule": tt.probability}})
		if verdict.Decision != tt.want {
			t.Errorf("p=%.2f: Decision = %q, want %q", tt.probability, verdict.Decision, tt.want)
		}
	}
}

func TestPolicyNilAssessment(t *testing.T) {
	t.Parallel()

	if got := DefaultPolicy().Decide(nil); got.Decision != Review {
		t.Fatalf("Decision = %q, want review", got.Decision)
	}
}
