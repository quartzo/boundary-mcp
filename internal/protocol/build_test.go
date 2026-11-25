package protocol

import (
	"strings"
	"testing"
)

const buildSchema = `namespace harness.test;

enum Status : byte { IDLE = 0, PENDING = 1, DONE = 2 }

table Child {
  value:long;
}

table Msg {
  id:int;
  name:string (required);
  active:bool;
  score:float;
  status:Status;
  child:Child;
  tags:[string];
  values:[int];
}

root_type Msg;
`

func TestBuildFrameWithValues(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, buildSchema)
	frame, err := schema.BuildFrame(FrameSpec{
		Table: "Msg",
		Values: map[string]any{
			"id":     42,
			"name":   "abc",
			"active": true,
			"score":  0.5,
			"status": "PENDING",
			"child":  map[string]any{"value": 7},
			"tags":   []any{"a", "b"},
			"values": []any{1, 2, 3},
		},
	})
	if err != nil {
		t.Fatalf("BuildFrame: %v", err)
	}
	if err := schema.ValidateFor(frame, "Msg"); err != nil {
		t.Fatalf("built frame is invalid: %v", err)
	}

	id, err := schema.ReadScalar(frame, "id")
	if err != nil || id != int32(42) {
		t.Fatalf("id = %v, err = %v", id, err)
	}
	name, err := schema.ReadString(frame, "name")
	if err != nil || name != "abc" {
		t.Fatalf("name = %q, err = %v", name, err)
	}
	status, err := schema.ReadScalar(frame, "status")
	if err != nil || status != int8(1) {
		t.Fatalf("status = %v, err = %v", status, err)
	}
}

func TestBuildFrameDefaultsRequiredFields(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, buildSchema)
	frame, err := schema.BuildFrame(FrameSpec{Table: "Msg"})
	if err != nil {
		t.Fatalf("BuildFrame: %v", err)
	}
	if err := schema.ValidateFor(frame, "Msg"); err != nil {
		t.Fatalf("built frame is invalid: %v", err)
	}

	name, err := schema.ReadString(frame, "name")
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	if name != "" {
		t.Fatalf("name = %q, want empty default", name)
	}
}

func TestBuildFrameFileIdentifier(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, `namespace harness.test;

table Msg {
  id:int;
}

root_type Msg;
file_identifier "TEST";
`)
	frame, err := schema.BuildFrame(FrameSpec{Table: "Msg"})
	if err != nil {
		t.Fatalf("BuildFrame: %v", err)
	}
	if len(frame) < 8 || string(frame[4:8]) != "TEST" {
		t.Fatalf("frame is missing the file identifier: %x", frame[:min(8, len(frame))])
	}
}

func TestBuildFrameRequiredNestedTable(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, `namespace harness.test;

table Child {
  value:long;
}

table Wrapper {
  child:Child (required);
}

root_type Wrapper;
`)
	frame, err := schema.BuildFrame(FrameSpec{Table: "Wrapper"})
	if err != nil {
		t.Fatalf("BuildFrame: %v", err)
	}
	if err := schema.ValidateFor(frame, "Wrapper"); err != nil {
		t.Fatalf("built frame is invalid: %v", err)
	}
}

func TestReadFieldPath(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, buildSchema)
	frame, err := schema.BuildFrame(FrameSpec{
		Table: "Msg",
		Values: map[string]any{
			"name":  "abc",
			"child": map[string]any{"value": 5},
		},
	})
	if err != nil {
		t.Fatalf("BuildFrame: %v", err)
	}

	value, err := schema.ReadFieldPath(frame, "child.value")
	if err != nil {
		t.Fatalf("ReadFieldPath: %v", err)
	}
	if value != int64(5) {
		t.Fatalf("value = %v, want 5", value)
	}

	for _, path := range []string{"child.nope", "name.value", "nope.value"} {
		if _, err := schema.ReadFieldPath(frame, path); err == nil {
			t.Errorf("ReadFieldPath(%q) returned nil error", path)
		}
	}
}

func TestNestedBoundaryMutations(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, buildSchema)
	frame, err := schema.BuildFrame(FrameSpec{
		Table: "Msg",
		Values: map[string]any{
			"name":  "abc",
			"child": map[string]any{"value": 5},
		},
	})
	if err != nil {
		t.Fatalf("BuildFrame: %v", err)
	}

	mutations, err := schema.BoundaryMutations(frame)
	if err != nil {
		t.Fatalf("BoundaryMutations: %v", err)
	}

	var found *Mutation
	for i := range mutations {
		if strings.HasPrefix(mutations[i].Name, "child.value=") {
			found = &mutations[i]
			break
		}
	}
	if found == nil {
		t.Fatal("no nested child.value mutation was generated")
	}
	if err := schema.Validate(found.Payload); err != nil {
		t.Fatalf("nested mutation is invalid: %v", err)
	}
}

func TestBuildFrameRejectsBadValues(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, buildSchema)

	cases := []FrameSpec{
		{Table: "Nope"},
		{Table: "Msg", Values: map[string]any{"id": "not-a-number"}},
		{Table: "Msg", Values: map[string]any{"id": 1e20}},
		{Table: "Msg", Values: map[string]any{"active": "yes"}},
		{Table: "Msg", Values: map[string]any{"tags": "not-an-array"}},
		{Table: "Msg", Values: map[string]any{"status": "NOPE"}},
	}
	for i, spec := range cases {
		if _, err := schema.BuildFrame(spec); err == nil {
			t.Errorf("case %d: BuildFrame returned nil error", i)
		}
	}
}
