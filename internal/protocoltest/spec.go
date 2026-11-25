// Package protocoltest defines and validates the declarative protocol test
// specification that boundary-mcp publishes.
//
// A spec is normative data: it names the structural cases a frame must accept
// or reject and the semantic assertions an implementation must satisfy. It
// never references an actor binary, a build command or a process. Whoever runs
// it — the coder during development or CI before a merge — expands the same
// content into the same case set.
package protocoltest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
)

// SpecVersion is the only accepted specification version.
const SpecVersion = 1

// Spec is a protocol test specification.
type Spec struct {
	SpecVersion int        `json:"spec_version"`
	Protocol    string     `json:"protocol,omitempty"`
	Schema      string     `json:"schema,omitempty"`
	Seeds       []Seed     `json:"seeds,omitempty"`
	Rules       []Rule     `json:"rules,omitempty"`
	Mutations   []string   `json:"mutations,omitempty"`
	Roundtrip   bool       `json:"roundtrip,omitempty"`
	Generation  Generation `json:"generation,omitempty"`
}

// Seed is a valid frame the structural cases start from.
type Seed struct {
	Name   string         `json:"name,omitempty"`
	Table  string         `json:"table,omitempty"`
	Values map[string]any `json:"values,omitempty"`
}

// Rule is a semantic assertion about one field.
type Rule struct {
	Table   string   `json:"table"`
	Field   string   `json:"field"`
	Regex   string   `json:"regex,omitempty"`
	Format  string   `json:"format,omitempty"`
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
	Valid   []any    `json:"valid"`
	Invalid []any    `json:"invalid"`
}

// Generation records how the case set is derived. Cases, when set, is a
// consistency check: expansion fails if the produced count differs.
type Generation struct {
	Seed  string `json:"seed,omitempty"`
	Cases int    `json:"cases,omitempty"`
}

// Mutation categories accepted by Spec.Mutations.
const (
	MutationTruncate     = "truncate"
	MutationBadOffset    = "bad-offset"
	MutationInvalidUTF8  = "invalid-utf8"
	MutationFlip         = "flip"
	MutationScalarBounds = "scalar-bounds"
)

var mutationCategories = map[string]bool{
	MutationTruncate:     true,
	MutationBadOffset:    true,
	MutationInvalidUTF8:  true,
	MutationFlip:         true,
	MutationScalarBounds: true,
}

var uuidv7Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// LoadSpec reads and validates a spec file.
func LoadSpec(path string) (*Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("protocoltest: read spec: %w", err)
	}
	spec, err := ParseSpec(data)
	if err != nil {
		return nil, fmt.Errorf("protocoltest: %s: %w", path, err)
	}
	return spec, nil
}

// ParseSpec parses a spec strictly: unknown fields are rejected.
func ParseSpec(data []byte) (*Spec, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var spec Spec
	if err := decoder.Decode(&spec); err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}
	if decoder.More() {
		return nil, errors.New("parse spec: trailing data")
	}

	spec.applyDefaults()
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	return &spec, nil
}

func (s *Spec) applyDefaults() {
	if s.SpecVersion == 0 {
		s.SpecVersion = SpecVersion
	}
	if s.Schema == "" {
		s.Schema = "protocol.bfbs"
	}
}

// Validate checks the spec for internal consistency.
func (s *Spec) Validate() error {
	if s == nil {
		return errors.New("spec is nil")
	}
	if s.SpecVersion != SpecVersion {
		return fmt.Errorf("unsupported spec_version %d (want %d)", s.SpecVersion, SpecVersion)
	}
	if len(s.Seeds) == 0 && len(s.Rules) == 0 {
		return errors.New("spec has no seeds and no rules")
	}
	if len(s.Seeds) == 0 && len(s.Mutations) > 0 {
		return errors.New("mutations require at least one seed")
	}
	for i, category := range s.Mutations {
		if !mutationCategories[category] {
			return fmt.Errorf("mutations[%d]: unknown category %q", i, category)
		}
	}
	seenNames := make(map[string]bool, len(s.Seeds))
	for i := range s.Seeds {
		if err := s.Seeds[i].validate(); err != nil {
			return fmt.Errorf("seeds[%d]: %w", i, err)
		}
		if name := s.Seeds[i].Name; name != "" {
			if seenNames[name] {
				return fmt.Errorf("seeds[%d]: duplicate name %q", i, name)
			}
			seenNames[name] = true
		}
	}
	for i := range s.Rules {
		if err := s.Rules[i].validate(); err != nil {
			return fmt.Errorf("rules[%d]: %w", i, err)
		}
	}
	if s.Generation.Cases < 0 {
		return errors.New("generation.cases must be non-negative")
	}
	return nil
}

func (s Seed) validate() error {
	if s.Table != "" && len(s.Values) == 0 {
		return errors.New("seed with a table must carry values")
	}
	return nil
}

func (r Rule) validate() error {
	if r.Table == "" {
		return errors.New("table is required")
	}
	if r.Field == "" {
		return errors.New("field is required")
	}
	if len(r.Valid) == 0 {
		return errors.New("valid must contain at least one value")
	}
	if len(r.Invalid) == 0 {
		return errors.New("invalid must contain at least one value")
	}
	if r.Regex != "" && (r.Min != nil || r.Max != nil) {
		return errors.New("regex and min/max are mutually exclusive")
	}

	var pattern *regexp.Regexp
	if r.Regex != "" {
		compiled, err := regexp.Compile(r.Regex)
		if err != nil {
			return fmt.Errorf("regex: %w", err)
		}
		pattern = compiled
	}

	switch r.Format {
	case "":
	case "uuidv7":
		if pattern == nil {
			pattern = uuidv7Pattern
		}
	default:
		return fmt.Errorf("unsupported format %q", r.Format)
	}

	for _, value := range r.Valid {
		if err := r.checkValue(value, true, pattern); err != nil {
			return fmt.Errorf("valid value %v: %w", value, err)
		}
	}
	for _, value := range r.Invalid {
		if err := r.checkValue(value, false, pattern); err != nil {
			return fmt.Errorf("invalid value %v: %w", value, err)
		}
	}
	return nil
}

func (r Rule) checkValue(value any, valid bool, pattern *regexp.Regexp) error {
	if pattern != nil {
		str, ok := value.(string)
		if !ok {
			return errors.New("must be a string when regex or format is set")
		}
		if match := pattern.MatchString(str); match != valid {
			if valid {
				return errors.New("does not match the declared pattern")
			}
			return errors.New("matches the declared pattern but is listed as invalid")
		}
	}

	if r.Min == nil && r.Max == nil {
		return nil
	}
	number, ok := asFloat(value)
	if !ok {
		return errors.New("must be numeric when min/max is set")
	}
	inRange := (r.Min == nil || number >= *r.Min) && (r.Max == nil || number <= *r.Max)
	if inRange != valid {
		if valid {
			return errors.New("is outside the declared range")
		}
		return errors.New("is inside the declared range but listed as invalid")
	}
	return nil
}

func asFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	default:
		return 0, false
	}
}
