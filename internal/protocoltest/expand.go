package protocoltest

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"boundary-mcp/internal/fbs"
	"boundary-mcp/internal/protocol"
)

// ExpandOptions configures deterministic expansion of a spec.
type ExpandOptions struct {
	// GovernanceDir resolves a relative Spec.Schema path.
	GovernanceDir string
	// Flatc compiles a .fbs schema into .bfbs when needed.
	Flatc *fbs.Compiler
	// ProtocolHash is the content hash of the bundle the spec belongs to.
	ProtocolHash string
}

// Artifact is a spec expanded into concrete cases and assertions.
type Artifact struct {
	Protocol   string      `json:"protocol,omitempty"`
	Spec       *Spec       `json:"spec"`
	Cases      []Case      `json:"cases"`
	Assertions []Assertion `json:"assertions"`
}

// Case is one concrete frame with its expected structural outcome.
type Case struct {
	Name     string `json:"name"`
	Table    string `json:"table,omitempty"`
	Frame    string `json:"frame"`
	Expect   string `json:"expect"`
	Mutation string `json:"mutation,omitempty"`
}

// Expected structural outcomes.
const (
	ExpectAccept = "accept"
	ExpectReject = "reject"
)

// Assertion is a semantic rule an implementation must satisfy.
type Assertion struct {
	ID     string   `json:"id"`
	Table  string   `json:"table"`
	Field  string   `json:"field"`
	Regex  string   `json:"regex,omitempty"`
	Format string   `json:"format,omitempty"`
	Min    *float64 `json:"min,omitempty"`
	Max    *float64 `json:"max,omitempty"`
	Accept []any    `json:"accept"`
	Reject []any    `json:"reject"`
}

// Expand turns a spec into the normative case set.
//
// Expansion is deterministic: the same spec, schema and flatc output produce
// byte-identical artifacts. Cases are sorted by name and carry base64 frames.
func Expand(ctx context.Context, opts ExpandOptions, spec *Spec) (*Artifact, error) {
	if spec == nil {
		return nil, errors.New("protocoltest: spec is nil")
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}

	schema, cleanup, err := loadSchema(ctx, opts, spec.Schema)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	requested := requestedMutations(spec.Mutations)

	var cases []Case
	for i, seed := range spec.Seeds {
		name := seed.Name
		if name == "" {
			name = fmt.Sprintf("seed-%d", i+1)
		}

		frame, err := schema.BuildFrame(protocol.FrameSpec{Table: seed.Table, Values: seed.Values})
		if err != nil {
			return nil, fmt.Errorf("protocoltest: seed %q: %w", name, err)
		}
		table := seed.Table
		if table == "" && schema.Root != nil {
			table = schema.Root.Name
		}
		cases = append(cases, Case{
			Name:   name,
			Table:  table,
			Frame:  base64.StdEncoding.EncodeToString(frame),
			Expect: ExpectAccept,
		})

		if len(requested) == 0 {
			continue
		}
		mutations, err := schema.BoundaryMutations(frame)
		if err != nil {
			return nil, fmt.Errorf("protocoltest: seed %q: %w", name, err)
		}
		for _, mutation := range mutations {
			category := mutationCategory(mutation.Name)
			if !requested[category] {
				continue
			}
			expect := ExpectReject
			if category == MutationScalarBounds {
				// Boundary values are legal frames; they exercise decoding,
				// not rejection.
				expect = ExpectAccept
			}
			cases = append(cases, Case{
				Name:     name + "/" + mutation.Name,
				Table:    table,
				Frame:    base64.StdEncoding.EncodeToString(mutation.Payload),
				Expect:   expect,
				Mutation: mutation.Name,
			})
		}
	}
	sort.SliceStable(cases, func(i, j int) bool { return cases[i].Name < cases[j].Name })

	assertions := make([]Assertion, 0, len(spec.Rules))
	for i, rule := range spec.Rules {
		assertions = append(assertions, Assertion{
			ID:     fmt.Sprintf("%s.%s/%d", rule.Table, rule.Field, i+1),
			Table:  rule.Table,
			Field:  rule.Field,
			Regex:  rule.Regex,
			Format: rule.Format,
			Min:    rule.Min,
			Max:    rule.Max,
			Accept: rule.Valid,
			Reject: rule.Invalid,
		})
	}

	if spec.Generation.Cases > 0 && spec.Generation.Cases != len(cases) {
		return nil, fmt.Errorf(
			"protocoltest: generation.cases is %d but expansion produced %d cases",
			spec.Generation.Cases, len(cases),
		)
	}

	return &Artifact{
		Protocol:   opts.ProtocolHash,
		Spec:       spec,
		Cases:      cases,
		Assertions: assertions,
	}, nil
}

func requestedMutations(categories []string) map[string]bool {
	out := make(map[string]bool, len(categories))
	for _, category := range categories {
		out[category] = true
	}
	if len(out) == 0 {
		out[MutationTruncate] = true
		out[MutationBadOffset] = true
		out[MutationFlip] = true
	}
	return out
}

func mutationCategory(name string) string {
	switch {
	case strings.HasPrefix(name, "truncate-"):
		return MutationTruncate
	case name == "bad-root-offset" || name == "bad-vtable":
		return MutationBadOffset
	case name == "flip-last-byte":
		return MutationFlip
	case strings.HasSuffix(name, "=invalid-utf8"):
		return MutationInvalidUTF8
	case strings.Contains(name, "="):
		return MutationScalarBounds
	default:
		return ""
	}
}

func loadSchema(ctx context.Context, opts ExpandOptions, ref string) (*protocol.Schema, func(), error) {
	if strings.TrimSpace(ref) == "" {
		return nil, nil, errors.New("protocoltest: schema path is required")
	}
	path := ref
	if !filepath.IsAbs(path) {
		path = filepath.Join(opts.GovernanceDir, path)
	}

	if strings.HasSuffix(strings.ToLower(path), ".bfbs") {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			if fbsPath := strings.TrimSuffix(path, ".bfbs") + ".fbs"; fileExists(fbsPath) {
				path = fbsPath
			}
		}
	}

	if !strings.HasSuffix(strings.ToLower(path), ".fbs") {
		schema, err := protocol.LoadSchema(path)
		return schema, func() {}, err
	}

	flatc := opts.Flatc
	if flatc == nil {
		flatc = fbs.NewCompiler("")
	}
	dir, err := os.MkdirTemp("", "boundary-protocoltest-*")
	if err != nil {
		return nil, nil, fmt.Errorf("protocoltest: schema temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	bfbs, err := flatc.CompileBinarySchema(ctx, path, dir)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	schema, err := protocol.LoadSchema(bfbs)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return schema, cleanup, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
