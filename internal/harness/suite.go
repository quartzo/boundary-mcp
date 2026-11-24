package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// Suite is a hidden test suite for one actor.
type Suite struct {
	// Mode is "server" (verify a receiver) or "client" (verify an emitter).
	Mode string `json:"mode"`
	// Schema is a .bfbs (or .fbs) path relative to the governance dir.
	Schema string `json:"schema,omitempty"`
	// ResponseTable validates server-mode responses. Empty uses the root table.
	ResponseTable string `json:"response_table,omitempty"`
	// Run launches the actor. "{socket}" is replaced with the Unix socket path.
	Run []string `json:"run"`
	// Build overrides the agent-provided entrypoint_cmd. Suites are trusted
	// governance artifacts, so pinning build/run commands here is preferred
	// over executing a command supplied by the agent.
	Build string `json:"build,omitempty"`
	// Env adds environment variables to the actor process.
	Env map[string]string `json:"env,omitempty"`
	// TimeoutMS bounds startup and each exchange.
	TimeoutMS int `json:"timeout_ms,omitempty"`
	// Seeds are the valid frames sent by a HarnessClient (server mode).
	Seeds []FrameSpec `json:"seeds,omitempty"`
	// Scenarios script the Reference Mock Peer (client mode).
	Scenarios []ScenarioSpec `json:"scenarios,omitempty"`
}

// ScenarioSpec is one scripted interaction with a client actor.
type ScenarioSpec struct {
	Name         string            `json:"name"`
	Before       []FrameSpec       `json:"before,omitempty"`
	Expectations []ExpectationSpec `json:"expectations"`
	TimeoutMS    int               `json:"timeout_ms,omitempty"`
}

// ExpectationSpec is one request/response step.
type ExpectationSpec struct {
	Name     string      `json:"name,omitempty"`
	Table    string      `json:"table,omitempty"`
	Checks   []CheckSpec `json:"checks,omitempty"`
	DelayMS  int         `json:"delay_ms,omitempty"`
	Response []FrameSpec `json:"response,omitempty"`
}

// CheckSpec is a declarative assertion on a request field.
type CheckSpec struct {
	Field  string          `json:"field"`
	Regex  string          `json:"regex,omitempty"`
	Min    *float64        `json:"min,omitempty"`
	Max    *float64        `json:"max,omitempty"`
	Equals json.RawMessage `json:"equals,omitempty"`
}

// LoadSuite reads and validates a suite JSON file.
func LoadSuite(path string) (*Suite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("harness: read suite: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var suite Suite
	if err := decoder.Decode(&suite); err != nil {
		return nil, fmt.Errorf("harness: parse suite %s: %w", path, err)
	}
	if decoder.More() {
		return nil, fmt.Errorf("harness: parse suite %s: trailing data", path)
	}

	suite.applyDefaults()
	if err := suite.validate(); err != nil {
		return nil, fmt.Errorf("harness: suite %s: %w", path, err)
	}
	return &suite, nil
}

func (s *Suite) applyDefaults() {
	if s.Schema == "" {
		s.Schema = "protocol.bfbs"
	}
	if s.TimeoutMS <= 0 {
		s.TimeoutMS = 2000
	}
	if s.Mode == "server" && len(s.Seeds) == 0 {
		s.Seeds = []FrameSpec{{}}
	}
}

func (s *Suite) validate() error {
	switch s.Mode {
	case "server", "client":
	default:
		return fmt.Errorf("mode must be \"server\" or \"client\" (got %q)", s.Mode)
	}
	if len(s.Run) == 0 || s.Run[0] == "" {
		return errors.New("run must contain an executable")
	}
	if s.Mode == "client" && len(s.Scenarios) == 0 {
		return errors.New("client mode requires at least one scenario")
	}
	for i, scenario := range s.Scenarios {
		if len(scenario.Expectations) == 0 {
			return fmt.Errorf("scenario[%d] has no expectations", i)
		}
	}
	return nil
}

// Timeout returns the suite timeout.
func (s *Suite) Timeout() time.Duration {
	return time.Duration(s.TimeoutMS) * time.Millisecond
}
