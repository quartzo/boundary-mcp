package judge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type scriptedJudge struct {
	decisions map[string]map[string]float64
	err       error
}

func (s *scriptedJudge) Evaluate(_ context.Context, in Input) (*Assessment, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &Assessment{
		Rules:   s.decisions[in.Intent],
		Model:   "fake",
		CostUSD: 0.001,
		Latency: 10 * time.Millisecond,
	}, nil
}

func TestEvaluateCases(t *testing.T) {
	t.Parallel()

	cases := []Case{
		{Name: "allow", Intent: "allow", Want: Allow},
		{Name: "review", Intent: "review", Want: Review},
		{Name: "reject", Intent: "reject", Want: Reject},
		{Name: "wrong", Intent: "wrong", Want: Allow},
	}
	scripted := &scriptedJudge{decisions: map[string]map[string]float64{
		"allow":  {"rule": 0.05},
		"review": {"rule": 0.50},
		"reject": {"rule": 0.90},
		"wrong":  {"rule": 0.90},
	}}

	report := EvaluateCases(context.Background(), scripted, cases, DefaultPolicy())
	if report.Total != 4 || report.Correct != 3 {
		t.Fatalf("report = %+v", report)
	}
	if report.FalseAllow != 0 || report.FalseEscalate != 1 {
		t.Fatalf("report = %+v", report)
	}
	if report.Accuracy() != 0.75 {
		t.Fatalf("accuracy = %v", report.Accuracy())
	}
	if report.P95Latency() != 10*time.Millisecond {
		t.Fatalf("p95 = %v", report.P95Latency())
	}
	if report.CostUSD != 0.004 {
		t.Fatalf("cost = %v", report.CostUSD)
	}
}

func TestEvaluateCasesErrors(t *testing.T) {
	t.Parallel()

	report := EvaluateCases(context.Background(), &scriptedJudge{err: errors.New("down")}, []Case{{Name: "a", Want: Allow}}, DefaultPolicy())
	if report.Errors != 1 || report.Correct != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestLoadCases(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeCase(t, dir, "b.json", `{"name":"b","intent":"i","want":"allow"}`)
	writeCase(t, dir, "a.json", `{"name":"a","intent":"i","want":"reject"}`)
	writeCase(t, dir, "ignore.txt", `not json`)

	cases, err := LoadCases(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 2 || cases[0].Name != "a" || cases[1].Name != "b" {
		t.Fatalf("cases = %+v", cases)
	}
}

func TestLoadCasesRejectsInvalid(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeCase(t, dir, "bad.json", `{"name":"bad","want":"maybe"}`)
	if _, err := LoadCases(dir); err == nil {
		t.Fatal("LoadCases accepted an invalid label")
	}
}

func writeCase(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write case: %v", err)
	}
}
