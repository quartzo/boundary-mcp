package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Case is one labeled proposal for the judge benchmark.
type Case struct {
	Name               string   `json:"name"`
	Actor              string   `json:"actor"`
	Intent             string   `json:"intent"`
	AffectedInvariants []string `json:"affected_invariants,omitempty"`
	PurposeBefore      string   `json:"purpose_before"`
	PurposeAfter       string   `json:"purpose_after"`
	ProtocolBefore     string   `json:"protocol_before"`
	ProtocolAfter      string   `json:"protocol_after"`
	StateMachineBefore string   `json:"state_machine_before,omitempty"`
	StateMachineAfter  string   `json:"state_machine_after,omitempty"`
	FlatcStatus        string   `json:"flatc_status,omitempty"`
	Want               Decision `json:"want"`
}

// Input converts the case to a judge input.
func (c Case) Input() Input {
	return Input{
		Actor:              c.Actor,
		Intent:             c.Intent,
		AffectedInvariants: c.AffectedInvariants,
		PurposeBefore:      c.PurposeBefore,
		PurposeAfter:       c.PurposeAfter,
		ProtocolBefore:     c.ProtocolBefore,
		ProtocolAfter:      c.ProtocolAfter,
		StateMachineBefore: c.StateMachineBefore,
		StateMachineAfter:  c.StateMachineAfter,
		FlatcStatus:        c.FlatcStatus,
	}
}

// LoadCases reads labeled JSON cases from dir.
func LoadCases(dir string) ([]Case, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("judge: read cases: %w", err)
	}

	var cases []Case
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("judge: read %s: %w", path, err)
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		var c Case
		if err := decoder.Decode(&c); err != nil {
			return nil, fmt.Errorf("judge: parse %s: %w", path, err)
		}
		if err := c.validate(); err != nil {
			return nil, fmt.Errorf("judge: %s: %w", path, err)
		}
		cases = append(cases, c)
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].Name < cases[j].Name })
	return cases, nil
}

func (c Case) validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("name is required")
	}
	switch c.Want {
	case Allow, Review, Reject:
	default:
		return fmt.Errorf("want must be allow, review or reject (got %q)", c.Want)
	}
	return nil
}

// CaseResult is one executed case.
type CaseResult struct {
	Case       Case
	Assessment *Assessment
	Verdict    Verdict
	Err        error
}

// Report summarizes a benchmark run.
type Report struct {
	Results       []CaseResult
	Total         int
	Correct       int
	Errors        int
	FalseAllow    int
	FalseEscalate int
	CostUSD       float64
	Latencies     []time.Duration
}

// Accuracy returns the fraction of cases whose decision matches the label.
func (r *Report) Accuracy() float64 {
	if r.Total == 0 {
		return 0
	}
	return float64(r.Correct) / float64(r.Total)
}

// P95Latency returns the 95th percentile judge latency.
func (r *Report) P95Latency() time.Duration {
	if len(r.Latencies) == 0 {
		return 0
	}
	values := append([]time.Duration(nil), r.Latencies...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	index := int(float64(len(values)-1) * 0.95)
	return values[index]
}

// Confusion returns counts keyed by want and then by observed decision.
func (r *Report) Confusion() map[Decision]map[Decision]int {
	out := make(map[Decision]map[Decision]int)
	for _, result := range r.Results {
		if result.Err != nil {
			continue
		}
		if out[result.Case.Want] == nil {
			out[result.Case.Want] = make(map[Decision]int)
		}
		out[result.Case.Want][result.Verdict.Decision]++
	}
	return out
}

// EvaluateCases runs every case through the judge and policy.
func EvaluateCases(ctx context.Context, j Judge, cases []Case, policy Policy) *Report {
	report := &Report{Total: len(cases)}
	for _, c := range cases {
		assessment, err := j.Evaluate(ctx, c.Input())
		result := CaseResult{Case: c, Assessment: assessment, Err: err}
		if err == nil {
			result.Verdict = policy.Decide(assessment)
			if result.Verdict.Decision == c.Want {
				report.Correct++
			}
			if c.Want != Allow && result.Verdict.Decision == Allow {
				report.FalseAllow++
			}
			if c.Want == Allow && result.Verdict.Decision != Allow {
				report.FalseEscalate++
			}
			report.CostUSD += assessment.CostUSD
			report.Latencies = append(report.Latencies, assessment.Latency)
		} else {
			report.Errors++
		}
		report.Results = append(report.Results, result)
	}
	return report
}
