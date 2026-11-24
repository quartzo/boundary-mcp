package versioning

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"boundary-mcp/internal/fbs"
	"boundary-mcp/internal/harness"
)

func TestClassify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		changes      []Change
		wireBreaking bool
		want         Class
	}{
		{name: "identical", want: ClassIdentical},
		{name: "additive", changes: []Change{{Additive: true}}, want: ClassAdditive},
		{name: "semantic", changes: []Change{{Kind: "field_required_removed"}}, want: ClassSemanticChange},
		{name: "breaking", changes: []Change{{Breaking: true}}, want: ClassBreaking},
		{name: "wire breaking", wireBreaking: true, want: ClassBreaking},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Classify(test.changes, test.wireBreaking); got != test.want {
				t.Fatalf("Classify() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDiffSchemasAdditive(t *testing.T) {
	t.Parallel()

	base := requestSchema(field("id", 0, harness.BaseString, true), field("count", 1, harness.BaseUInt, false))
	candidate := requestSchema(
		field("id", 0, harness.BaseString, true),
		field("count", 1, harness.BaseUInt, false),
		field("note", 2, harness.BaseString, false),
	)
	changes := DiffSchemas(base, candidate)
	if got := Classify(changes, false); got != ClassAdditive {
		t.Fatalf("class = %q, want additive; changes = %+v", got, changes)
	}
	change := findChange(changes, "field_added", "note")
	if change == nil || !change.Additive || change.Breaking {
		t.Fatalf("field_added = %+v", change)
	}
}

func TestDiffSchemasRequiredFieldIsBreaking(t *testing.T) {
	t.Parallel()

	base := requestSchema(field("id", 0, harness.BaseString, true))
	candidate := requestSchema(
		field("id", 0, harness.BaseString, true),
		field("note", 1, harness.BaseString, true),
	)
	changes := DiffSchemas(base, candidate)
	if got := Classify(changes, false); got != ClassBreaking {
		t.Fatalf("class = %q, want breaking; changes = %+v", got, changes)
	}
	change := findChange(changes, "field_added", "note")
	if change == nil || !change.Breaking || change.Additive {
		t.Fatalf("field_added = %+v", change)
	}
}

func TestDiffSchemasRenameIsSourceBreaking(t *testing.T) {
	t.Parallel()

	base := requestSchema(field("count", 1, harness.BaseUInt, false))
	candidate := requestSchema(field("total", 1, harness.BaseUInt, false))
	changes := DiffSchemas(base, candidate)
	if got := Classify(changes, false); got != ClassBreaking {
		t.Fatalf("class = %q, want breaking; changes = %+v", got, changes)
	}
	if change := findChange(changes, "field_renamed", "count"); change == nil {
		t.Fatalf("field_renamed not found: %+v", changes)
	}
}

func TestDiffSchemasRequiredRemovedIsSemantic(t *testing.T) {
	t.Parallel()

	base := requestSchema(field("id", 0, harness.BaseString, true))
	candidate := requestSchema(field("id", 0, harness.BaseString, false))
	changes := DiffSchemas(base, candidate)
	if got := Classify(changes, false); got != ClassSemanticChange {
		t.Fatalf("class = %q, want semantic_change; changes = %+v", got, changes)
	}
	if change := findChange(changes, "field_required_removed", "id"); change == nil {
		t.Fatalf("field_required_removed not found: %+v", changes)
	}
}

func TestDiffSchemasEnumAdditive(t *testing.T) {
	t.Parallel()

	base := &harness.Schema{
		Enums: []*harness.Enum{{
			Name:       "test.Color",
			Values:     []harness.EnumValue{{Name: "RED", Value: 0}},
			Underlying: harness.Type{Base: harness.BaseByte, Index: -1},
		}},
	}
	candidate := &harness.Schema{
		Enums: []*harness.Enum{{
			Name:       "test.Color",
			Values:     []harness.EnumValue{{Name: "RED", Value: 0}, {Name: "BLUE", Value: 1}},
			Underlying: harness.Type{Base: harness.BaseByte, Index: -1},
		}},
	}
	changes := DiffSchemas(base, candidate)
	if got := Classify(changes, false); got != ClassAdditive {
		t.Fatalf("class = %q, want additive; changes = %+v", got, changes)
	}
	if change := findChange(changes, "enum_value_added", "BLUE"); change == nil {
		t.Fatalf("enum_value_added not found: %+v", changes)
	}
}

func TestAddBundleChanges(t *testing.T) {
	t.Parallel()

	compat := &Compatibility{Wire: WireCompatible, Class: ClassIdentical}
	AddBundleChanges(compat,
		Bundle{Purpose: "purpose", Protocol: "protocol", StateMachine: "{}"},
		Bundle{Purpose: "purpose v2", Protocol: "protocol", StateMachine: "{}"},
	)
	if compat.Class != ClassSemanticChange || !compat.ReviewRequired {
		t.Fatalf("compat = %+v", compat)
	}
	if findChange(compat.Changes, "purpose_changed", "") == nil {
		t.Fatalf("purpose_changed not found: %+v", compat.Changes)
	}

	identical := &Compatibility{Wire: WireCompatible, Class: ClassIdentical}
	AddBundleChanges(identical, Bundle{}, Bundle{})
	if identical.Class != ClassIdentical || identical.ReviewRequired {
		t.Fatalf("identical = %+v", identical)
	}
}

func TestAnalyzeWithFlatc(t *testing.T) {
	t.Parallel()

	flatc := fbs.NewCompiler("")
	if _, err := flatc.Version(context.Background()); err != nil {
		t.Skipf("flatc not available: %v", err)
	}

	const baseSchema = `namespace test;

table Request {
  id:string (required, id: 0);
  count:uint (id: 1);
}

table Envelope {
  request:Request (id: 0);
}

root_type Envelope;
`

	t.Run("additive", func(t *testing.T) {
		baseDir := t.TempDir()
		candidateDir := t.TempDir()
		writeSchema(t, baseDir, baseSchema)
		writeSchema(t, candidateDir, strings.Replace(baseSchema, "  count:uint (id: 1);", "  count:uint (id: 1);\n  note:string (id: 2);", 1))

		compat, err := Analyze(context.Background(), flatc, baseDir, candidateDir)
		if err != nil {
			t.Fatal(err)
		}
		if compat.Wire != WireCompatible || compat.Class != ClassAdditive || compat.ReviewRequired {
			t.Fatalf("compat = %+v", compat)
		}
		if findChange(compat.Changes, "field_added", "note") == nil {
			t.Fatalf("field_added not found: %+v", compat.Changes)
		}
	})

	t.Run("required addition", func(t *testing.T) {
		baseDir := t.TempDir()
		candidateDir := t.TempDir()
		writeSchema(t, baseDir, baseSchema)
		writeSchema(t, candidateDir, strings.Replace(baseSchema, "  count:uint (id: 1);", "  count:uint (id: 1);\n  note:string (required, id: 2);", 1))

		compat, err := Analyze(context.Background(), flatc, baseDir, candidateDir)
		if err != nil {
			t.Fatal(err)
		}
		if compat.Class != ClassBreaking || !compat.ReviewRequired {
			t.Fatalf("compat = %+v", compat)
		}
	})

	t.Run("rename", func(t *testing.T) {
		baseDir := t.TempDir()
		candidateDir := t.TempDir()
		writeSchema(t, baseDir, baseSchema)
		writeSchema(t, candidateDir, strings.Replace(baseSchema, "count:uint", "total:uint", 1))

		compat, err := Analyze(context.Background(), flatc, baseDir, candidateDir)
		if err != nil {
			t.Fatal(err)
		}
		if compat.Class != ClassBreaking || !compat.ReviewRequired {
			t.Fatalf("compat = %+v", compat)
		}
		if findChange(compat.Changes, "field_renamed", "count") == nil {
			t.Fatalf("field_renamed not found: %+v", compat.Changes)
		}
	})

	t.Run("removal", func(t *testing.T) {
		baseDir := t.TempDir()
		candidateDir := t.TempDir()
		writeSchema(t, baseDir, baseSchema)
		writeSchema(t, candidateDir, strings.Replace(baseSchema, "  count:uint (id: 1);\n", "", 1))

		compat, err := Analyze(context.Background(), flatc, baseDir, candidateDir)
		if err != nil {
			t.Fatal(err)
		}
		if compat.Wire != WireBreaking || compat.Class != ClassBreaking || !compat.ReviewRequired {
			t.Fatalf("compat = %+v", compat)
		}
		if findChange(compat.Changes, "field_removed", "count") == nil {
			t.Fatalf("field_removed not found: %+v", compat.Changes)
		}
		if strings.TrimSpace(compat.Evidence.Diagnostics) == "" {
			t.Fatal("expected flatc diagnostics")
		}
	})
}

func requestSchema(fields ...*harness.Field) *harness.Schema {
	request := &harness.Object{Name: "test.Request", Fields: fields}
	return &harness.Schema{Objects: []*harness.Object{request}, Root: request}
}

func field(name string, id uint16, base harness.BaseType, required bool) *harness.Field {
	return &harness.Field{
		Name:     name,
		ID:       id,
		Type:     harness.Type{Base: base, Index: -1},
		Required: required,
	}
}

func findChange(changes []Change, kind, name string) *Change {
	for i := range changes {
		change := &changes[i]
		if change.Kind == kind && (change.Field == name || change.Value == name || change.Object == name || change.Enum == name) {
			return change
		}
	}
	return nil
}

func writeSchema(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "protocol.fbs"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
