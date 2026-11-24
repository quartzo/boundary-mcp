// Package compiler implements the semantic compiler: it evolves purpose.md,
// protocol.fbs and state_machine.json with an LLM, while flatc remains the
// deterministic gatekeeper for syntax and backward compatibility.
package compiler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"boundary-mcp/internal/fbs"
	"boundary-mcp/internal/governance"
	"boundary-mcp/internal/judge"
	"boundary-mcp/internal/llm"
	"boundary-mcp/internal/mcp"
)

// LLM is the structured-output client used by the compiler.
type LLM interface {
	CompleteJSON(ctx context.Context, req llm.Request) (*llm.Response, error)
}

// JudgeMode controls the advisory semantic judge.
type JudgeMode string

const (
	// JudgeOff disables the judge.
	JudgeOff JudgeMode = "off"
	// JudgeEnforce applies the judge verdict before committing.
	JudgeEnforce JudgeMode = "enforce"
)

// SDKTarget is a language/directory pair regenerated after a commit.
type SDKTarget struct {
	Language fbs.Language
	Dir      string
}

// Options configures the semantic compiler.
type Options struct {
	// GovernanceDir contains purpose.md, protocol.fbs and state_machine.json.
	GovernanceDir string
	// Flatc wraps the FlatBuffers compiler. Defaults to flatc from PATH.
	Flatc *fbs.Compiler
	// LLM is required.
	LLM LLM
	// Judge is the optional advisory semantic judge.
	Judge judge.Judge
	// JudgeMode defaults to JudgeOff.
	JudgeMode JudgeMode
	// JudgePolicy overrides the default thresholds. Zero values use the
	// calibrated defaults.
	JudgePolicy judge.Policy
	// Clock is injectable for deterministic tests. Defaults to time.Now.
	Clock func() time.Time
	// SDKTargets are regenerated after a successful commit.
	SDKTargets []SDKTarget
	// OnCommit runs after a successful commit, e.g. to regenerate hidden tests
	// or mark dependent actors stale.
	OnCommit func(ctx context.Context, event CommitEvent) error
}

// CommitEvent describes a committed contract change.
type CommitEvent struct {
	Actor      string
	Intent     string
	Direction  string
	HistoryDir string
	Artifacts  Artifacts
}

// Compiler implements [mcp.ContractEvolver] and the manual-edit sync flow.
type Compiler struct {
	opts Options
	mu   sync.Mutex
}

// New builds a semantic compiler.
func New(opts Options) (*Compiler, error) {
	if strings.TrimSpace(opts.GovernanceDir) == "" {
		return nil, errors.New("compiler: governance directory is required")
	}
	if opts.LLM == nil {
		return nil, errors.New("compiler: LLM is required")
	}
	if opts.JudgeMode == "" {
		opts.JudgeMode = JudgeOff
	}
	switch opts.JudgeMode {
	case JudgeOff, JudgeEnforce:
	default:
		return nil, fmt.Errorf("compiler: unknown judge mode %q", opts.JudgeMode)
	}
	if opts.JudgeMode == JudgeEnforce && opts.Judge == nil {
		return nil, errors.New("compiler: judge is required in enforce mode")
	}
	if opts.Flatc == nil {
		opts.Flatc = fbs.NewCompiler("")
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	for _, target := range opts.SDKTargets {
		if strings.TrimSpace(target.Dir) == "" {
			return nil, fmt.Errorf("compiler: SDK target %q has no directory", target.Language)
		}
	}

	info, err := os.Stat(opts.GovernanceDir)
	if err != nil {
		return nil, fmt.Errorf("compiler: governance directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("compiler: governance path %s is not a directory", opts.GovernanceDir)
	}
	return &Compiler{opts: opts}, nil
}

const (
	directionPropose      = "propose"
	directionPurposeToFBS = "purpose->fbs"
	directionFBSToPurpose = "fbs->purpose"
)

// Propose implements [mcp.ContractEvolver].
func (c *Compiler) Propose(ctx context.Context, req mcp.ProposalRequest) (*mcp.ProposalResult, error) {
	if strings.TrimSpace(req.ActorName) == "" {
		return nil, errors.New("compiler: actor_name is required")
	}
	if strings.TrimSpace(req.IntentDescription) == "" {
		return nil, errors.New("compiler: intent_description is required")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	current, err := c.loadArtifacts()
	if err != nil {
		return nil, err
	}
	if !governance.ParsePurpose([]byte(current.Purpose)).HasActor(req.ActorName) {
		return rejectedResult("actor: " + req.ActorName + " is not listed in the Actors section"), nil
	}
	resp, err := c.opts.LLM.CompleteJSON(ctx, llm.Request{
		SystemPrompt: systemPrompt,
		UserPrompt:   proposeUserPrompt(current, req),
		SchemaName:   "contract_evolution",
		Schema:       proposalSchema,
	})
	if err != nil {
		return nil, fmt.Errorf("compiler: proposal: %w", err)
	}

	var proposed proposalResponse
	if err := json.Unmarshal(resp.JSON, &proposed); err != nil {
		return nil, fmt.Errorf("compiler: decode proposal: %w", err)
	}

	return c.apply(ctx, applyInput{
		actor:        req.ActorName,
		intent:       req.IntentDescription,
		direction:    directionPropose,
		summary:      proposed.Summary,
		model:        resp.Model,
		affects:      req.AffectsInvariants,
		requireActor: req.ActorName,
		before:       current,
		after: Artifacts{
			Purpose:      proposed.PurposeMD,
			Protocol:     proposed.ProtocolFBS,
			StateMachine: proposed.StateMachineJSON,
		},
	})
}

// Sync reconciles purpose.md and protocol.fbs after a manual edit.
//
// With a history snapshot, it detects which artifact changed. Without one, it
// falls back to mtimes. The changed artifact is the source of truth; the LLM
// updates its counterpart and state_machine.json.
func (c *Compiler) Sync(ctx context.Context) (*mcp.ProposalResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	current, err := c.loadArtifacts()
	if err != nil {
		return nil, err
	}
	latest, err := c.loadLatestSnapshot()
	if err != nil {
		return nil, err
	}

	direction, err := c.detectDirection(current, latest)
	if err != nil {
		return nil, err
	}
	if direction == "" {
		return &mcp.ProposalResult{Status: "unchanged"}, nil
	}

	switch direction {
	case directionPurposeToFBS:
		resp, err := c.opts.LLM.CompleteJSON(ctx, llm.Request{
			SystemPrompt: systemPrompt,
			UserPrompt:   syncPurposeToProtocolPrompt(current),
			SchemaName:   "purpose_to_protocol",
			Schema:       purposeToProtocolSchema,
		})
		if err != nil {
			return nil, fmt.Errorf("compiler: sync: %w", err)
		}
		var proposed syncProtocolResponse
		if err := json.Unmarshal(resp.JSON, &proposed); err != nil {
			return nil, fmt.Errorf("compiler: decode sync: %w", err)
		}
		after := current
		after.Protocol = proposed.ProtocolFBS
		after.StateMachine = proposed.StateMachineJSON
		return c.apply(ctx, applyInput{
			actor:     "manual",
			intent:    "sync purpose.md -> protocol.fbs",
			direction: direction,
			summary:   proposed.Summary,
			model:     resp.Model,
			before:    current,
			after:     after,
		})

	case directionFBSToPurpose:
		base := current.Protocol
		if latest != nil {
			base = latest.Protocol
		}
		if err := c.validateProtocol(ctx, base, current.Protocol); err != nil {
			if errors.Is(err, fbs.ErrNotInstalled) {
				return nil, err
			}
			return rejectedResult(err.Error()), nil
		}

		previousPurpose := current.Purpose
		if latest != nil {
			previousPurpose = latest.Purpose
		}
		resp, err := c.opts.LLM.CompleteJSON(ctx, llm.Request{
			SystemPrompt: systemPrompt,
			UserPrompt:   syncProtocolToPurposePrompt(previousPurpose, current),
			SchemaName:   "protocol_to_purpose",
			Schema:       protocolToPurposeSchema,
		})
		if err != nil {
			return nil, fmt.Errorf("compiler: sync: %w", err)
		}
		var proposed syncPurposeResponse
		if err := json.Unmarshal(resp.JSON, &proposed); err != nil {
			return nil, fmt.Errorf("compiler: decode sync: %w", err)
		}
		after := current
		after.Purpose = proposed.PurposeMD
		after.StateMachine = proposed.StateMachineJSON
		return c.apply(ctx, applyInput{
			actor:     "manual",
			intent:    "sync protocol.fbs -> purpose.md",
			direction: direction,
			summary:   proposed.Summary,
			model:     resp.Model,
			before:    current,
			after:     after,
		})
	}

	return nil, fmt.Errorf("compiler: unknown sync direction %q", direction)
}

func (c *Compiler) detectDirection(current Artifacts, latest *Artifacts) (string, error) {
	if latest == nil {
		purposeInfo, err := os.Stat(c.path("purpose.md"))
		if err != nil {
			return "", err
		}
		protocolInfo, err := os.Stat(c.path("protocol.fbs"))
		if err != nil {
			return "", err
		}
		switch {
		case purposeInfo.ModTime().After(protocolInfo.ModTime()):
			return directionPurposeToFBS, nil
		case protocolInfo.ModTime().After(purposeInfo.ModTime()):
			return directionFBSToPurpose, nil
		default:
			return "", errors.New("compiler: cannot determine which artifact changed (no history snapshot)")
		}
	}

	purposeChanged := current.Purpose != latest.Purpose
	protocolChanged := current.Protocol != latest.Protocol
	switch {
	case purposeChanged && protocolChanged:
		return "", errors.New("compiler: purpose.md and protocol.fbs both changed since the last snapshot")
	case purposeChanged:
		return directionPurposeToFBS, nil
	case protocolChanged:
		return directionFBSToPurpose, nil
	default:
		return "", nil
	}
}

type applyInput struct {
	actor        string
	intent       string
	direction    string
	summary      string
	model        string
	affects      []string
	requireActor string
	before       Artifacts
	after        Artifacts
}

func (c *Compiler) apply(ctx context.Context, in applyInput) (*mcp.ProposalResult, error) {
	if strings.TrimSpace(in.after.Purpose) == "" {
		return rejectedResult("purpose_md is empty"), nil
	}
	if strings.TrimSpace(in.after.Protocol) == "" {
		return rejectedResult("protocol_fbs is empty"), nil
	}
	if err := validateStateMachine(in.after.StateMachine); err != nil {
		return rejectedResult("state_machine_json: " + err.Error()), nil
	}

	if in.requireActor != "" {
		if !governance.ParsePurpose([]byte(in.after.Purpose)).HasActor(in.requireActor) {
			return rejectedResult("purpose: " + in.requireActor + " is not listed in the Actors section"), nil
		}
	}

	if err := c.validateProtocol(ctx, in.before.Protocol, in.after.Protocol); err != nil {
		if errors.Is(err, fbs.ErrNotInstalled) {
			return nil, err
		}
		return rejectedResult(err.Error()), nil
	}
	if err := c.dryRunSDKs(ctx, in.after.Protocol); err != nil {
		if errors.Is(err, fbs.ErrNotInstalled) {
			return nil, err
		}
		return rejectedResult(err.Error()), nil
	}

	changed := changedFiles(in.before, in.after)
	if len(changed) == 0 {
		return &mcp.ProposalResult{Status: "unchanged"}, nil
	}

	var judgeRecord *judgeMeta
	if c.opts.JudgeMode == JudgeEnforce && c.opts.Judge != nil && in.direction == directionPropose {
		assessment, err := c.opts.Judge.Evaluate(ctx, judge.Input{
			Actor:              in.actor,
			Intent:             in.intent,
			AffectedInvariants: in.affects,
			PurposeBefore:      in.before.Purpose,
			PurposeAfter:       in.after.Purpose,
			ProtocolBefore:     in.before.Protocol,
			ProtocolAfter:      in.after.Protocol,
			StateMachineBefore: in.before.StateMachine,
			StateMachineAfter:  in.after.StateMachine,
			FlatcStatus:        "pass",
		})
		if err != nil {
			return nil, fmt.Errorf("compiler: judge: %w", err)
		}

		verdict := c.opts.JudgePolicy.Decide(assessment)
		judgeRecord = newJudgeMeta(assessment, verdict)
		switch verdict.Decision {
		case judge.Reject:
			return rejectedResult("semantic judge: " + verdict.Summary()), nil
		case judge.Review:
			return escalatedResult("semantic judge: " + verdict.Summary()), nil
		}
	}

	diff, err := diffArtifacts(in.before, in.after)
	if err != nil {
		return nil, fmt.Errorf("compiler: diff: %w", err)
	}

	now := c.opts.Clock()
	historyDir, patchPath, err := c.writeHistory(in, changed, diff, now, judgeRecord)
	if err != nil {
		return nil, err
	}

	if err := c.commitArtifacts(in.before, in.after); err != nil {
		return nil, err
	}

	var warnings []string
	if err := c.generateSDKs(ctx); err != nil {
		warnings = append(warnings, "sdk: "+err.Error())
	}
	if c.opts.OnCommit != nil {
		err := c.opts.OnCommit(ctx, CommitEvent{
			Actor:      in.actor,
			Intent:     in.intent,
			Direction:  in.direction,
			HistoryDir: historyDir,
			Artifacts:  in.after,
		})
		if err != nil {
			warnings = append(warnings, "on_commit: "+err.Error())
		}
	}

	return &mcp.ProposalResult{
		Status:      "committed",
		PatchPath:   patchPath,
		Diagnostics: strings.Join(warnings, "\n"),
	}, nil
}

func rejectedResult(diagnostics string) *mcp.ProposalResult {
	return &mcp.ProposalResult{Status: "rejected", Diagnostics: diagnostics}
}

func escalatedResult(diagnostics string) *mcp.ProposalResult {
	return &mcp.ProposalResult{Status: "escalated", Diagnostics: diagnostics}
}

type judgeMeta struct {
	Decision       string          `json:"decision"`
	MaxRule        string          `json:"max_rule,omitempty"`
	MaxProbability float64         `json:"max_probability"`
	Findings       []judge.Finding `json:"findings,omitempty"`
	Model          string          `json:"model,omitempty"`
	InputTokens    int             `json:"input_tokens,omitempty"`
	OutputTokens   int             `json:"output_tokens,omitempty"`
	CostUSD        float64         `json:"cost_usd,omitempty"`
	LatencyMS      int64           `json:"latency_ms,omitempty"`
	Cached         bool            `json:"cached,omitempty"`
}

func newJudgeMeta(assessment *judge.Assessment, verdict judge.Verdict) *judgeMeta {
	if assessment == nil {
		return nil
	}
	return &judgeMeta{
		Decision:       string(verdict.Decision),
		MaxRule:        verdict.MaxRule,
		MaxProbability: verdict.MaxProbability,
		Findings:       verdict.Findings,
		Model:          assessment.Model,
		InputTokens:    assessment.InputTokens,
		OutputTokens:   assessment.OutputTokens,
		CostUSD:        assessment.CostUSD,
		LatencyMS:      assessment.Latency.Milliseconds(),
		Cached:         assessment.Cached,
	}
}
