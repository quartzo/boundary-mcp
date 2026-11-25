package protocol

import (
	"testing"

	"pgregory.net/rapid"
)

func TestMutateScalarProperty(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, msgSchema)
	payload := msgPayload()

	rapid.Check(t, func(t *rapid.T) {
		value := rapid.Int32().Draw(t, "id")

		mutated, err := schema.MutateScalar(payload, "id", value)
		if err != nil {
			t.Fatalf("MutateScalar: %v", err)
		}
		if err := schema.Validate(mutated); err != nil {
			t.Fatalf("mutated payload is invalid: %v", err)
		}

		got, err := schema.ReadScalar(mutated, "id")
		if err != nil {
			t.Fatalf("ReadScalar: %v", err)
		}
		if got != value {
			t.Fatalf("id = %v, want %v", got, value)
		}
	})
}

func TestBoundaryMutationsNeverPanic(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, msgSchema)
	payload := msgPayload()

	rapid.Check(t, func(t *rapid.T) {
		cut := rapid.IntRange(0, len(payload)).Draw(t, "cut")
		data := payload[:cut]
		_ = schema.Validate(data)
	})
}
