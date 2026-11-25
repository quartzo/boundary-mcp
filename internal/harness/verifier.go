package harness

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"boundary-mcp/internal/fbs"
)

// VerifierOptions configures the harness-backed behavior verifier.
type VerifierOptions struct {
	// GovernanceDir contains protocol.fbs/.bfbs and hidden_tests/.
	GovernanceDir string
	// ActorsDir contains one workspace per actor.
	ActorsDir string
	// SuitesDir defaults to <GovernanceDir>/hidden_tests.
	SuitesDir string
	// Flatc compiles .fbs suites to .bfbs. Defaults to flatc from PATH.
	Flatc *fbs.Compiler
}

// Verifier builds an actor and runs its hidden JSON suite.
//
// cmd/boundary-mcp adapts it to the MCP BehaviorVerifier interface.
type Verifier struct {
	opts VerifierOptions
}

// NewVerifier builds a verifier.
func NewVerifier(opts VerifierOptions) (*Verifier, error) {
	if strings.TrimSpace(opts.GovernanceDir) == "" {
		return nil, errors.New("harness: governance directory is required")
	}
	if strings.TrimSpace(opts.ActorsDir) == "" {
		opts.ActorsDir = "actors"
	}
	if strings.TrimSpace(opts.SuitesDir) == "" {
		opts.SuitesDir = filepath.Join(opts.GovernanceDir, "hidden_tests")
	}
	if opts.Flatc == nil {
		opts.Flatc = fbs.NewCompiler("")
	}
	return &Verifier{opts: opts}, nil
}

// Verify builds the actor, loads governance/hidden_tests/<actor>.json and runs
// the requested mode.
func (v *Verifier) Verify(ctx context.Context, req VerifyRequest) (*VerifyResult, error) {
	actor := strings.TrimSpace(req.ActorName)
	if actor == "" {
		return nil, errors.New("harness: actor_name is required")
	}

	workspace := filepath.Join(v.opts.ActorsDir, actor)
	info, err := os.Stat(workspace)
	if err != nil {
		return nil, fmt.Errorf("harness: actor workspace: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("harness: actor workspace %s is not a directory", workspace)
	}

	suite, err := LoadSuite(filepath.Join(v.opts.SuitesDir, actor+".json"))
	if err != nil {
		return nil, err
	}

	buildCommand := suite.Build
	if strings.TrimSpace(buildCommand) == "" {
		buildCommand = req.EntrypointCmd
	}
	if strings.TrimSpace(buildCommand) == "" {
		return nil, errors.New("harness: no build command (set suite.build or entrypoint_cmd)")
	}
	if err := v.build(ctx, workspace, buildCommand); err != nil {
		return nil, err
	}

	schema, cleanup, err := v.loadSchema(ctx, suite)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	switch suite.Mode {
	case "server":
		return v.verifyServer(ctx, suite, schema, workspace)
	case "client":
		return v.verifyClient(ctx, suite, schema, workspace)
	default:
		return nil, fmt.Errorf("harness: unsupported mode %q", suite.Mode)
	}
}

func (v *Verifier) build(ctx context.Context, workspace, command string) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = workspace
	cmd.Env = os.Environ()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("harness: build failed: %w\n%s", err, trimOutput(output))
	}
	return nil
}

func (v *Verifier) loadSchema(ctx context.Context, suite *Suite) (*Schema, func(), error) {
	path := suite.Schema
	if !filepath.IsAbs(path) {
		path = filepath.Join(v.opts.GovernanceDir, path)
	}

	if strings.HasSuffix(strings.ToLower(path), ".bfbs") {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			fbsPath := strings.TrimSuffix(path, ".bfbs") + ".fbs"
			if _, err := os.Stat(fbsPath); err == nil {
				path = fbsPath
			}
		}
	}

	if strings.HasSuffix(strings.ToLower(path), ".fbs") {
		dir, err := os.MkdirTemp("", "boundary-schema-*")
		if err != nil {
			return nil, nil, fmt.Errorf("harness: schema temp dir: %w", err)
		}
		cleanup := func() { _ = os.RemoveAll(dir) }

		bfbs, err := v.opts.Flatc.CompileBinarySchema(ctx, path, dir)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		schema, err := LoadSchema(bfbs)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		return schema, cleanup, nil
	}

	schema, err := LoadSchema(path)
	return schema, func() {}, err
}

func (v *Verifier) verifyServer(ctx context.Context, suite *Suite, schema *Schema, workspace string) (*VerifyResult, error) {
	seeds, err := buildFrames(schema, suite.Seeds)
	if err != nil {
		return nil, err
	}

	dir, err := os.MkdirTemp("", "boundary-harness-*")
	if err != nil {
		return nil, fmt.Errorf("harness: temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	socket := filepath.Join(dir, "actor.sock")
	stop, err := v.startActor(ctx, suite, workspace, socket)
	if err != nil {
		return nil, err
	}
	defer stop()

	conn, err := v.dialWithRetry(ctx, socket, suite.Timeout())
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	client := &HarnessClient{Options: ClientOptions{
		Schema:        schema,
		ResponseTable: suite.ResponseTable,
		Timeout:       suite.Timeout(),
	}}
	report, err := client.Verify(ctx, conn, seeds)
	if err != nil {
		return nil, err
	}
	return clientResult(report), nil
}

func (v *Verifier) verifyClient(ctx context.Context, suite *Suite, schema *Schema, workspace string) (*VerifyResult, error) {
	total := &PeerReport{}
	for _, spec := range suite.Scenarios {
		report, err := v.runClientScenario(ctx, suite, schema, workspace, spec)
		if err != nil {
			return nil, err
		}
		total.Scenarios += report.Scenarios
		total.Cases += report.Cases
		total.Failures += report.Failures
		if total.Counterexample == nil {
			total.Counterexample = report.Counterexample
		}
	}
	return peerResult(total), nil
}

func (v *Verifier) runClientScenario(ctx context.Context, suite *Suite, schema *Schema, workspace string, spec ScenarioSpec) (*PeerReport, error) {
	scenario, err := buildScenario(schema, spec, suite.Timeout())
	if err != nil {
		return nil, err
	}

	dir, err := os.MkdirTemp("", "boundary-harness-*")
	if err != nil {
		return nil, fmt.Errorf("harness: temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	socket := filepath.Join(dir, "actor.sock")
	listener, err := ListenUnix(socket)
	if err != nil {
		return nil, fmt.Errorf("harness: listen: %w", err)
	}
	defer func() { _ = listener.Close() }()

	stop, err := v.startActor(ctx, suite, workspace, socket)
	if err != nil {
		return nil, err
	}
	defer stop()

	acceptCtx, cancel := context.WithTimeout(ctx, suite.Timeout())
	defer cancel()
	conn, err := Accept(acceptCtx, listener)
	if err != nil {
		return nil, fmt.Errorf("harness: actor did not connect: %w", err)
	}

	peer := &ReferenceMockPeer{Scenarios: []PeerScenario{scenario}, Timeout: suite.Timeout()}
	return peer.Verify(ctx, func(context.Context) (Conn, error) { return conn, nil })
}

func buildScenario(schema *Schema, spec ScenarioSpec, defaultTimeout time.Duration) (PeerScenario, error) {
	before, err := buildFrames(schema, spec.Before)
	if err != nil {
		return PeerScenario{}, err
	}

	timeout := defaultTimeout
	if spec.TimeoutMS > 0 {
		timeout = time.Duration(spec.TimeoutMS) * time.Millisecond
	}

	scenario := PeerScenario{Name: spec.Name, Before: before, Timeout: timeout}
	for _, expectation := range spec.Expectations {
		check, err := compileChecks(schema, expectation.Table, expectation.Checks)
		if err != nil {
			return PeerScenario{}, err
		}
		responses, err := buildFrames(schema, expectation.Response)
		if err != nil {
			return PeerScenario{}, err
		}
		scenario.Expectations = append(scenario.Expectations, PeerExpectation{
			Name:     expectation.Name,
			Check:    check,
			Delay:    time.Duration(expectation.DelayMS) * time.Millisecond,
			Response: responses,
		})
	}
	return scenario, nil
}

func buildFrames(schema *Schema, specs []FrameSpec) ([][]byte, error) {
	frames := make([][]byte, len(specs))
	for i, spec := range specs {
		frame, err := schema.BuildFrame(spec)
		if err != nil {
			return nil, fmt.Errorf("harness: build frame %d: %w", i, err)
		}
		frames[i] = frame
	}
	return frames, nil
}

func (v *Verifier) startActor(ctx context.Context, suite *Suite, workspace, socket string) (func(), error) {
	args := make([]string, len(suite.Run)-1)
	for i, arg := range suite.Run[1:] {
		args[i] = strings.ReplaceAll(arg, "{socket}", socket)
	}

	runCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(runCtx, suite.Run[0], args...)
	cmd.Dir = workspace

	env := append([]string(nil), os.Environ()...)
	env = append(env, "BOUNDARY_SOCKET="+socket)
	for key, value := range suite.Env {
		env = append(env, key+"="+value)
	}
	cmd.Env = env

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("harness: start actor: %w", err)
	}

	return func() {
		cancel()
		_ = cmd.Wait()
	}, nil
}

func (v *Verifier) dialWithRetry(ctx context.Context, socket string, timeout time.Duration) (Conn, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error

	for {
		conn, err := DialUnix(ctx, socket)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("harness: actor did not listen on %s: %w", socket, lastErr)
		}

		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		}
	}
}

func clientResult(report *ClientReport) *VerifyResult {
	result := &VerifyResult{
		Status:        "PASS",
		CasesExecuted: report.Cases,
		P99LatencyUS:  report.P99LatencyUS,
	}
	if report.Failures > 0 || report.Counterexample != nil {
		result.Status = "FAIL"
		if c := report.Counterexample; c != nil {
			result.Counterexample = fmt.Sprintf(
				"case=%s payload=%s observed=%s violation=%s",
				c.Case, hex.EncodeToString(c.Payload), c.Observed, c.Violation,
			)
		}
	}
	return result
}

func peerResult(report *PeerReport) *VerifyResult {
	result := &VerifyResult{
		Status:        "PASS",
		CasesExecuted: report.Cases,
	}
	if report.Failures > 0 || report.Counterexample != nil {
		result.Status = "FAIL"
		if c := report.Counterexample; c != nil {
			result.Counterexample = fmt.Sprintf(
				"scenario=%s step=%s observed=%s violation=%s",
				c.Scenario, c.Step, hex.EncodeToString(c.Observed), c.Violation,
			)
		}
	}
	return result
}

func trimOutput(output []byte) string {
	text := strings.TrimSpace(string(output))
	const max = 4000
	if len(text) > max {
		return text[:max] + "..."
	}
	return text
}
