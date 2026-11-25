package protocol

import "testing"

func TestParseSchema(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, richSchema)

	if schema.Root == nil {
		t.Fatal("Root is nil")
	}
	if ShortName(schema.Root.Name) != "Payload" {
		t.Fatalf("root = %q, want Payload", schema.Root.Name)
	}
	if schema.FileIdent != "BFBS" {
		t.Errorf("FileIdent = %q, want BFBS", schema.FileIdent)
	}

	id := schema.Root.Field("id")
	if id == nil || id.Type.Base != BaseInt {
		t.Fatalf("id field = %+v", id)
	}
	name := schema.Root.Field("name")
	if name == nil || name.Type.Base != BaseString || !name.Required {
		t.Fatalf("name field = %+v", name)
	}
	status := schema.Root.Field("status")
	if status == nil || status.Type.Base != BaseByte || status.Type.Index != 0 {
		t.Fatalf("status field = %+v", status)
	}
	tags := schema.Root.Field("tags")
	if tags == nil || tags.Type.Base != BaseVector || tags.Type.Element != BaseString {
		t.Fatalf("tags field = %+v", tags)
	}
	child := schema.Root.Field("child")
	if child == nil || child.Type.Base != BaseObj {
		t.Fatalf("child field = %+v", child)
	}
	if got := schema.ObjectAt(child.Type.Index); got == nil || ShortName(got.Name) != "Child" {
		t.Fatalf("child object = %+v", got)
	}
	if schema.Object("Child") == nil {
		t.Fatal("short-name lookup for Child failed")
	}

	if len(schema.Enums) != 1 {
		t.Fatalf("enums = %d, want 1", len(schema.Enums))
	}
	enum := schema.Enums[0]
	if ShortName(enum.Name) != "Status" {
		t.Fatalf("enum name = %q", enum.Name)
	}
	if !enum.Has(1) || enum.Has(9) {
		t.Fatalf("enum values = %+v", enum.Values)
	}
}

func TestParseSchemaRejectsMalformed(t *testing.T) {
	t.Parallel()

	for _, data := range [][]byte{nil, {1, 2, 3}, make([]byte, 8)} {
		if _, err := ParseSchema(data); err == nil {
			t.Errorf("ParseSchema(%v) returned nil error", data)
		}
	}
}

func TestLoadSchemaMissingFile(t *testing.T) {
	t.Parallel()

	if _, err := LoadSchema("does-not-exist.bfbs"); err == nil {
		t.Fatal("LoadSchema returned nil error for a missing file")
	}
}
