package harness

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestValidatePayload(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, msgSchema)
	payload := msgPayload()

	if err := schema.Validate(payload); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := schema.ValidateFor(payload, "Msg"); err != nil {
		t.Fatalf("ValidateFor(Msg): %v", err)
	}
	if err := schema.ValidateFor(payload, "Other"); err == nil {
		t.Fatal("ValidateFor accepted an unknown table")
	}
}

func TestReadFields(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, msgSchema)
	payload := msgPayload()

	id, err := schema.ReadScalar(payload, "id")
	if err != nil {
		t.Fatalf("ReadScalar(id): %v", err)
	}
	if id != int32(7) {
		t.Fatalf("id = %v, want 7", id)
	}

	flag, err := schema.ReadScalar(payload, "flag")
	if err != nil {
		t.Fatalf("ReadScalar(flag): %v", err)
	}
	if flag != true {
		t.Fatalf("flag = %v, want true", flag)
	}

	name, err := schema.ReadString(payload, "name")
	if err != nil {
		t.Fatalf("ReadString(name): %v", err)
	}
	if name != "foo" {
		t.Fatalf("name = %q, want foo", name)
	}

	if _, err := schema.ReadScalar(payload, "name"); err == nil {
		t.Fatal("ReadScalar accepted a string field")
	}
	if _, err := schema.ReadScalar(payload, "nope"); err == nil {
		t.Fatal("ReadScalar accepted an unknown field")
	}
}

func TestMutateScalar(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, msgSchema)
	payload := msgPayload()

	mutated, err := schema.MutateScalar(payload, "id", int32(-1))
	if err != nil {
		t.Fatalf("MutateScalar: %v", err)
	}
	if err := schema.Validate(mutated); err != nil {
		t.Fatalf("mutated payload is invalid: %v", err)
	}
	id, err := schema.ReadScalar(mutated, "id")
	if err != nil || id != int32(-1) {
		t.Fatalf("id = %v, err = %v", id, err)
	}

	if _, err := schema.MutateScalar(payload, "id", int64(math.MaxInt64)); err == nil {
		t.Fatal("MutateScalar accepted an out-of-range int")
	}
	if _, err := schema.MutateScalar(payload, "name", "x"); err == nil {
		t.Fatal("MutateScalar accepted a string field")
	}
	if _, err := schema.MutateScalar(payload, "nope", 1); err == nil {
		t.Fatal("MutateScalar accepted an unknown field")
	}
}

func TestRequiredFieldMissing(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, msgSchema)
	payload := msgPayload()

	// Clear the name field's vtable entry (id 1 -> vtable slot 6).
	binary.LittleEndian.PutUint16(payload[10:], 0)

	if err := schema.Validate(payload); err == nil {
		t.Fatal("Validate accepted a missing required field")
	}
	if _, err := schema.ReadString(payload, "name"); err == nil {
		t.Fatal("ReadString accepted an absent field")
	}
}

func TestInvalidPayloads(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, msgSchema)
	payload := msgPayload()

	cases := map[string][]byte{
		"empty":     {},
		"short":     payload[:2],
		"truncated": payload[:20],
	}
	for name, data := range cases {
		if err := schema.Validate(data); err == nil {
			t.Errorf("%s: Validate returned nil error", name)
		}
	}
}

func TestBoundaryMutations(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, msgSchema)
	payload := msgPayload()

	mutations, err := schema.BoundaryMutations(payload)
	if err != nil {
		t.Fatalf("BoundaryMutations: %v", err)
	}
	if len(mutations) < 8 {
		t.Fatalf("mutations = %d, want at least 8", len(mutations))
	}

	var invalidUTF8 *Mutation
	for i := range mutations {
		if mutations[i].Name == "name=invalid-utf8" {
			invalidUTF8 = &mutations[i]
		}
	}
	if invalidUTF8 == nil {
		t.Fatal("missing name=invalid-utf8 mutation")
	}
	if err := schema.Validate(invalidUTF8.Payload); err == nil {
		t.Fatal("invalid UTF-8 mutation passed validation")
	}

	for _, mutation := range mutations {
		if mutation.Name == "id=0" {
			if err := schema.Validate(mutation.Payload); err != nil {
				t.Fatalf("id=0 mutation is invalid: %v", err)
			}
		}
	}
}

func TestEnumValidation(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, `namespace harness.test;

enum Status : byte { IDLE = 0, PENDING = 1, DONE = 2 }

table Msg {
  status:Status;
}

root_type Msg;
`)

	if err := schema.Validate(singleBytePayload(1)); err != nil {
		t.Fatalf("valid enum rejected: %v", err)
	}
	if err := schema.Validate(singleBytePayload(9)); err == nil {
		t.Fatal("invalid enum accepted")
	}
}

func TestStringVectorValidation(t *testing.T) {
	t.Parallel()

	schema := compileSchema(t, `namespace harness.test;

table Msg {
  tags:[string];
}

root_type Msg;
`)

	payload := tagsPayload()
	if err := schema.Validate(payload); err != nil {
		t.Fatalf("valid string vector rejected: %v", err)
	}

	bad := append([]byte(nil), payload...)
	bad[40] = 0xFF
	if err := schema.Validate(bad); err == nil {
		t.Fatal("invalid UTF-8 string in vector accepted")
	}
}

func TestValidateBuffer(t *testing.T) {
	t.Parallel()

	if err := ValidateBuffer(msgPayload()); err != nil {
		t.Fatalf("ValidateBuffer: %v", err)
	}
	if err := ValidateBuffer(msgPayload()[:20]); err == nil {
		t.Fatal("ValidateBuffer accepted a truncated buffer")
	}
}

// singleBytePayload builds a Msg with a single byte field (field id 0) set to
// value.
func singleBytePayload(value byte) []byte {
	b := make([]byte, 20)
	binary.LittleEndian.PutUint32(b[0:], 12)
	binary.LittleEndian.PutUint16(b[4:], 6)
	binary.LittleEndian.PutUint16(b[6:], 8)
	binary.LittleEndian.PutUint16(b[8:], 4)
	binary.LittleEndian.PutUint32(b[12:], 8)
	b[16] = value
	return b
}

// tagsPayload builds a Msg { tags:[string] } with tags = ["foo"].
func tagsPayload() []byte {
	b := make([]byte, 44)
	binary.LittleEndian.PutUint32(b[0:], 12)
	binary.LittleEndian.PutUint16(b[4:], 6)
	binary.LittleEndian.PutUint16(b[6:], 8)
	binary.LittleEndian.PutUint16(b[8:], 4)
	binary.LittleEndian.PutUint32(b[12:], 8) // table -> vtable at 4
	binary.LittleEndian.PutUint32(b[16:], 8) // tags -> vector at 24
	binary.LittleEndian.PutUint32(b[24:], 1) // vector length
	binary.LittleEndian.PutUint32(b[28:], 8) // element -> string at 36
	binary.LittleEndian.PutUint32(b[36:], 3) // string length
	copy(b[40:], "foo")
	return b
}

func TestBoundaryMutationsWithoutRoot(t *testing.T) {
	t.Parallel()

	schema := &Schema{}
	if _, err := schema.BoundaryMutations(msgPayload()); err == nil {
		t.Fatal("BoundaryMutations without a root table returned nil error")
	}
}
