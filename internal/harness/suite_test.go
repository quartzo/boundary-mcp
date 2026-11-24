package harness

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadSuite(t *testing.T) {
	t.Parallel()

	path := writeSuiteFile(t, `{
  "mode": "server",
  "run": ["./actor", "--socket", "{socket}"],
  "timeout_ms": 1500,
  "seeds": [{"table": "Msg", "values": {"id": 1}}]
}`)

	suite, err := LoadSuite(path)
	if err != nil {
		t.Fatalf("LoadSuite: %v", err)
	}
	if suite.Mode != "server" {
		t.Errorf("Mode = %q", suite.Mode)
	}
	if suite.Schema != "protocol.bfbs" {
		t.Errorf("Schema = %q, want default", suite.Schema)
	}
	if suite.Timeout() != 1500*time.Millisecond {
		t.Errorf("Timeout = %v", suite.Timeout())
	}
	if len(suite.Seeds) != 1 || suite.Seeds[0].Table != "Msg" {
		t.Errorf("Seeds = %+v", suite.Seeds)
	}
}

func TestLoadSuiteDefaultsSeed(t *testing.T) {
	t.Parallel()

	path := writeSuiteFile(t, `{"mode":"server","run":["./actor"]}`)
	suite, err := LoadSuite(path)
	if err != nil {
		t.Fatalf("LoadSuite: %v", err)
	}
	if len(suite.Seeds) != 1 {
		t.Fatalf("Seeds = %+v, want one default seed", suite.Seeds)
	}
}

func TestLoadSuiteRejectsInvalid(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"unknown field": `{"mode":"server","run":["./a"],"bogus":true}`,
		"missing run":   `{"mode":"server"}`,
		"bad mode":      `{"mode":"peer","run":["./a"]}`,
		"no scenarios":  `{"mode":"client","run":["./a"]}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeSuiteFile(t, content)
			if _, err := LoadSuite(path); err == nil {
				t.Fatal("LoadSuite returned nil error")
			}
		})
	}
}

func TestLoadSuiteMissingFile(t *testing.T) {
	t.Parallel()

	if _, err := LoadSuite(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("LoadSuite returned nil error for a missing file")
	}
}

func writeSuiteFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "suite.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	return path
}
