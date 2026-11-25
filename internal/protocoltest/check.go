package protocoltest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Report is the result of checking one spec against its schema.
type Report struct {
	Protocol   string
	Cases      int
	Accept     int
	Reject     int
	Assertions int
	Roundtrip  bool
	Failures   []string
}

// Pass reports whether every structural expectation held.
func (r *Report) Pass() bool { return len(r.Failures) == 0 }

// Check expands spec twice, verifies that the expansion is deterministic and
// checks every structural case against the schema validator.
//
// Semantic assertions are returned as a count only: they describe behavior an
// implementation must test, and the server never runs that implementation.
func Check(ctx context.Context, opts ExpandOptions, spec *Spec) (*Report, error) {
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

	first, err := expandWithSchema(opts, spec, schema)
	if err != nil {
		return nil, err
	}
	second, err := expandWithSchema(opts, spec, schema)
	if err != nil {
		return nil, err
	}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		return nil, err
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		return nil, errors.New("protocoltest: expansion is not deterministic")
	}

	report := &Report{
		Protocol:   first.Protocol,
		Cases:      len(first.Cases),
		Assertions: len(first.Assertions),
		Roundtrip:  spec.Roundtrip,
	}
	for _, testCase := range first.Cases {
		payload, err := base64.StdEncoding.DecodeString(testCase.Frame)
		if err != nil {
			report.Failures = append(report.Failures, fmt.Sprintf("%s: decode frame: %v", testCase.Name, err))
			continue
		}
		validateErr := schema.ValidateFor(payload, testCase.Table)
		switch testCase.Expect {
		case ExpectAccept:
			report.Accept++
			if validateErr != nil {
				report.Failures = append(report.Failures,
					fmt.Sprintf("%s: expected accept, got %v", testCase.Name, validateErr))
			}
		case ExpectReject:
			report.Reject++
			if validateErr == nil {
				report.Failures = append(report.Failures,
					fmt.Sprintf("%s: expected reject, got a valid frame", testCase.Name))
			}
		default:
			report.Failures = append(report.Failures,
				fmt.Sprintf("%s: unknown expectation %q", testCase.Name, testCase.Expect))
		}
	}
	return report, nil
}

// LoadSpecDir reads and validates every *.json spec in dir, keyed by file name.
// A missing directory is not an error.
func LoadSpecDir(dir string) (map[string]*Spec, error) {
	out := make(map[string]*Spec)

	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("protocoltest: read spec dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		spec, err := LoadSpec(path)
		if err != nil {
			return nil, err
		}
		out[strings.TrimSuffix(entry.Name(), ".json")] = spec
	}
	return out, nil
}
