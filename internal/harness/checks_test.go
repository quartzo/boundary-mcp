package harness

import (
	"encoding/json"
	"testing"
)

func TestCompileChecks(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, buildSchema)
	frame, err := schema.BuildFrame(FrameSpec{
		Table: "Msg",
		Values: map[string]any{
			"id":     42,
			"name":   "abc",
			"status": "PENDING",
		},
	})
	if err != nil {
		t.Fatalf("BuildFrame: %v", err)
	}

	min := 0.0
	max := 100.0
	check, err := compileChecks(schema, "Msg", []CheckSpec{
		{Field: "name", Regex: "^[a-z]+$"},
		{Field: "id", Min: &min, Max: &max},
		{Field: "status", Equals: json.RawMessage("1")},
	})
	if err != nil {
		t.Fatalf("compileChecks: %v", err)
	}
	if err := check(frame); err != nil {
		t.Fatalf("check rejected a valid frame: %v", err)
	}

	badFrame, err := schema.BuildFrame(FrameSpec{
		Table: "Msg",
		Values: map[string]any{
			"id":   200,
			"name": "ABC",
		},
	})
	if err != nil {
		t.Fatalf("BuildFrame: %v", err)
	}
	if err := check(badFrame); err == nil {
		t.Fatal("check accepted an invalid frame")
	}
}

func TestCompileChecksErrors(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, buildSchema)
	if _, err := compileChecks(schema, "Nope", nil); err == nil {
		t.Fatal("compileChecks accepted an unknown table")
	}
	if _, err := compileChecks(schema, "Msg", []CheckSpec{{Field: "name", Regex: "("}}); err == nil {
		t.Fatal("compileChecks accepted an invalid regex")
	}
	if _, err := compileChecks(schema, "Msg", []CheckSpec{{}}); err == nil {
		t.Fatal("compileChecks accepted a check without a field")
	}
}

func TestCompileChecksEmpty(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, buildSchema)
	check, err := compileChecks(schema, "", nil)
	if err != nil {
		t.Fatalf("compileChecks: %v", err)
	}
	if check != nil {
		t.Fatal("compileChecks returned a non-nil check for empty input")
	}
}
