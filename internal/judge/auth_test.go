package judge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveAPIKeyPrecedence(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"openrouter":{"key":"from-auth","type":"api"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENCODE_AUTH_FILE", authPath)
	t.Setenv("OPENROUTER_API_KEY", "")

	if got := ResolveAPIKey("explicit"); got != "explicit" {
		t.Fatalf("explicit = %q", got)
	}

	t.Setenv("OPENROUTER_API_KEY", "from-env")
	if got := ResolveAPIKey(""); got != "from-env" {
		t.Fatalf("env = %q", got)
	}

	t.Setenv("OPENROUTER_API_KEY", "")
	if got := ResolveAPIKey(""); got != "from-auth" {
		t.Fatalf("opencode auth = %q", got)
	}
}

func TestOpenCodeAPIKeyMissing(t *testing.T) {
	t.Setenv("OPENCODE_AUTH_FILE", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("OPENROUTER_API_KEY", "")

	if got := ResolveAPIKey(""); got != "" {
		t.Fatalf("key = %q, want empty", got)
	}
}

func TestOpenCodeAPIKeyMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENCODE_AUTH_FILE", path)
	t.Setenv("OPENROUTER_API_KEY", "")

	if got := ResolveAPIKey(""); got != "" {
		t.Fatalf("key = %q, want empty", got)
	}
}
