package judge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ResolveAPIKey returns the OpenRouter key for local runs.
//
// Precedence: explicit value, OPENROUTER_API_KEY, then opencode's local auth
// store ($XDG_DATA_HOME/opencode/auth.json, override with OPENCODE_AUTH_FILE).
// Production deployments should provide OPENROUTER_API_KEY explicitly.
func ResolveAPIKey(explicit string) string {
	if key := strings.TrimSpace(explicit); key != "" {
		return key
	}
	if key := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")); key != "" {
		return key
	}
	return openCodeAPIKey()
}

func openCodeAPIKey() string {
	path := strings.TrimSpace(os.Getenv("OPENCODE_AUTH_FILE"))
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
		if dataHome == "" {
			dataHome = filepath.Join(home, ".local", "share")
		}
		path = filepath.Join(dataHome, "opencode", "auth.json")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var auth map[string]struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(raw, &auth); err != nil {
		return ""
	}
	return strings.TrimSpace(auth["openrouter"].Key)
}
