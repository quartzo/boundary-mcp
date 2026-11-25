package protocoltest

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"boundary-mcp/internal/fbs"
)

func TestParseSpecRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	_, err := ParseSpec([]byte(`{
		"spec_version": 1,
		"seeds": [{"table": "Msg", "values": {"id": "x"}}],
		"mode": "server"
	}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("err = %v, want unknown field", err)
	}
}

func TestValidateRejectsInconsistentRules(t *testing.T) {
	t.Parallel()

	cases := map[string]Spec{
		"regex mismatch on valid": {
			Rules: []Rule{{Table: "Msg", Field: "id", Regex: "^[0-9]+$", Valid: []any{"abc"}, Invalid: []any{"1"}}},
		},
		"regex match on invalid": {
			Rules: []Rule{{Table: "Msg", Field: "id", Regex: "^[0-9]+$", Valid: []any{"1"}, Invalid: []any{"2"}}},
		},
		"range on valid": {
			Rules: []Rule{{Table: "Msg", Field: "score", Min: f(0), Max: f(1), Valid: []any{-1}, Invalid: []any{2}}},
		},
		"range on invalid": {
			Rules: []Rule{{Table: "Msg", Field: "score", Min: f(0), Max: f(1), Valid: []any{1}, Invalid: []any{0.5}}},
		},
		"unknown mutation": {
			Seeds:     []Seed{{Table: "Msg", Values: map[string]any{"id": "x"}}},
			Mutations: []string{"shred"},
		},
		"mutations without seeds": {
			Rules:     []Rule{{Table: "Msg", Field: "id", Regex: "^x$", Valid: []any{"x"}, Invalid: []any{"y"}}},
			Mutations: []string{"truncate"},
		},
		"duplicate seed names": {
			Seeds: []Seed{
				{Name: "a", Table: "Msg", Values: map[string]any{"id": "x"}},
				{Name: "a", Table: "Msg", Values: map[string]any{"id": "y"}},
			},
		},
	}

	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			spec.SpecVersion = SpecVersion
			if err := spec.Validate(); err == nil {
				t.Fatalf("Validate() = nil, want error")
			}
		})
	}
}

func TestValidateAcceptsConsistentRule(t *testing.T) {
	t.Parallel()

	spec := Spec{
		SpecVersion: SpecVersion,
		Seeds:       []Seed{{Name: "msg", Table: "Msg", Values: map[string]any{"id": "x"}}},
		Rules: []Rule{{
			Table:   "Msg",
			Field:   "document_id",
			Regex:   "^[0-9]{14}$",
			Valid:   []any{"12345678901234"},
			Invalid: []any{"", "1234", "abcdefghijklmn"},
		}},
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestExpandIsDeterministic(t *testing.T) {
	t.Parallel()

	dir := writeSchema(t, `namespace demo;

table Msg {
  id:string (required);
  score:float;
}

root_type Msg;
`)

	spec := &Spec{
		SpecVersion: SpecVersion,
		Schema:      "protocol.fbs",
		Seeds: []Seed{{
			Name:   "msg",
			Table:  "Msg",
			Values: map[string]any{"id": "demo"},
		}},
		Rules: []Rule{{
			Table:   "Msg",
			Field:   "score",
			Min:     f(0),
			Max:     f(1),
			Valid:   []any{0, 0.5, 1},
			Invalid: []any{-1, 2},
		}},
		Mutations: []string{MutationTruncate, MutationBadOffset},
	}

	opts := ExpandOptions{GovernanceDir: dir, Flatc: fbs.NewCompiler(""), ProtocolHash: "sha256:abc"}
	first, err := Expand(context.Background(), opts, spec)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	second, err := Expand(context.Background(), opts, spec)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("expansion is not deterministic:\n%s\n%s", firstJSON, secondJSON)
	}

	if len(first.Cases) < 3 {
		t.Fatalf("cases = %d, want seed plus structural mutations", len(first.Cases))
	}
	var accept, reject int
	for _, c := range first.Cases {
		switch c.Expect {
		case ExpectAccept:
			accept++
		case ExpectReject:
			reject++
		default:
			t.Fatalf("case %s has unexpected expectation %q", c.Name, c.Expect)
		}
	}
	if accept == 0 || reject == 0 {
		t.Fatalf("accept=%d reject=%d, want both", accept, reject)
	}
	if len(first.Assertions) != 1 {
		t.Fatalf("assertions = %d, want 1", len(first.Assertions))
	}
}

func TestExpandChecksGenerationCount(t *testing.T) {
	t.Parallel()

	dir := writeSchema(t, `namespace demo;
table Msg { id:string (required); }
root_type Msg;
`)

	spec := &Spec{
		SpecVersion: SpecVersion,
		Schema:      "protocol.fbs",
		Seeds:       []Seed{{Name: "msg", Table: "Msg", Values: map[string]any{"id": "demo"}}},
		Generation:  Generation{Cases: 1},
	}

	_, err := Expand(context.Background(), ExpandOptions{GovernanceDir: dir}, spec)
	if err == nil || !strings.Contains(err.Error(), "generation.cases") {
		t.Fatalf("err = %v, want generation count mismatch", err)
	}
}

func writeSchema(t *testing.T, schema string) string {
	t.Helper()
	if _, err := exec.LookPath("flatc"); err != nil {
		t.Skip("flatc not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "protocol.fbs"), []byte(schema), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func f(v float64) *float64 { return &v }
