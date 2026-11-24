package harness

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const msgSchema = `namespace harness.test;

table Msg {
  id:int;
  name:string (required);
  flag:bool;
}

root_type Msg;
`

const richSchema = `namespace harness.test;

enum Status : byte { IDLE = 0, PENDING = 1, DONE = 2 }

table Child {
  value:long;
}

table Payload {
  id:int;
  name:string (required);
  active:bool;
  score:float;
  status:Status;
  tags:[string];
  child:Child;
  values:[int];
}

root_type Payload;
`

func compileSchema(t *testing.T, source string) *Schema {
	t.Helper()
	_, schema := compileSchemaFile(t, source)
	return schema
}

func compileSchemaFile(t *testing.T, source string) (string, *Schema) {
	t.Helper()

	flatc, err := exec.LookPath("flatc")
	if err != nil {
		t.Skip("flatc not installed; skipping harness integration test")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "schema.fbs")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	out, err := exec.Command(flatc, "--binary", "--schema", "-o", dir, src).CombinedOutput()
	if err != nil {
		t.Fatalf("flatc: %v\n%s", err, out)
	}

	path := filepath.Join(dir, "schema.bfbs")
	schema, err := LoadSchema(path)
	if err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}
	return path, schema
}

// msgPayload builds a valid FlatBuffer for msgSchema:
// table Msg { id:int; name:string; flag:bool; }.
func msgPayload() []byte {
	b := make([]byte, 40)

	// Root uoffset -> table at 16.
	binary.LittleEndian.PutUint32(b[0:], 16)

	// Vtable at 4: size 10, table size 16, field offsets 4/8/12.
	binary.LittleEndian.PutUint16(b[4:], 10)
	binary.LittleEndian.PutUint16(b[6:], 16)
	binary.LittleEndian.PutUint16(b[8:], 4)
	binary.LittleEndian.PutUint16(b[10:], 8)
	binary.LittleEndian.PutUint16(b[12:], 12)

	// Table at 16: soffset to vtable at 4.
	binary.LittleEndian.PutUint32(b[16:], 12)
	binary.LittleEndian.PutUint32(b[20:], 7) // id
	binary.LittleEndian.PutUint32(b[24:], 8) // name -> 32
	b[28] = 1                                // flag

	// String at 32: "foo".
	binary.LittleEndian.PutUint32(b[32:], 3)
	copy(b[36:], "foo")

	return b
}
