package judge

import (
	"context"
	"errors"
	"testing"
)

type countingJudge struct {
	calls      int
	assessment *Assessment
	err        error
}

func (c *countingJudge) Evaluate(context.Context, Input) (*Assessment, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	assessment := *c.assessment
	return &assessment, nil
}

func TestCachedEvaluate(t *testing.T) {
	t.Parallel()

	inner := &countingJudge{assessment: &Assessment{
		Rules: map[string]float64{"a": 0.1},
		Model: "test-model",
	}}
	cached := NewCached(inner, "test-model")

	input := Input{Actor: "actor_a", Intent: "add a field", PurposeBefore: "INV-01", PurposeAfter: "INV-01"}
	first, err := cached.Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Cached {
		t.Fatal("first call was marked cached")
	}
	if inner.calls != 1 {
		t.Fatalf("calls = %d", inner.calls)
	}

	second, err := cached.Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Cached {
		t.Fatal("second call was not marked cached")
	}
	if inner.calls != 1 {
		t.Fatalf("calls = %d, want 1", inner.calls)
	}
	if second.Rules["a"] != 0.1 {
		t.Fatalf("rules = %+v", second.Rules)
	}

	input.Intent = "add a different field"
	if _, err := cached.Evaluate(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if inner.calls != 2 {
		t.Fatalf("calls = %d, want 2", inner.calls)
	}
}

func TestCachedPropagatesError(t *testing.T) {
	t.Parallel()

	inner := &countingJudge{err: errors.New("provider down")}
	cached := NewCached(inner, "test")
	if _, err := cached.Evaluate(context.Background(), Input{Actor: "a"}); err == nil {
		t.Fatal("Evaluate returned nil error")
	}
}
