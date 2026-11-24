package versioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"boundary-mcp/internal/fbs"
	"boundary-mcp/internal/harness"
)

// Class is a structural compatibility classification. Role-specific classes
// (producer_only, consumer_only, requires_peer_upgrade) are reserved for the
// semantic classifier that consumes purpose.md and state_machine.json.
type Class string

const (
	ClassIdentical           Class = "identical"
	ClassAdditive            Class = "additive"
	ClassProducerOnly        Class = "producer_only"
	ClassConsumerOnly        Class = "consumer_only"
	ClassRequiresPeerUpgrade Class = "requires_peer_upgrade"
	ClassSemanticChange      Class = "semantic_change"
	ClassBreaking            Class = "breaking"
	ClassUnknown             Class = "unknown"
)

// Wire compatibility values.
const (
	WireCompatible = "compatible"
	WireBreaking   = "breaking"
)

// Change is one structural difference between two schemas.
type Change struct {
	Kind     string `json:"kind"`
	Object   string `json:"object,omitempty"`
	Field    string `json:"field,omitempty"`
	Enum     string `json:"enum,omitempty"`
	Value    string `json:"value,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Breaking bool   `json:"breaking,omitempty"`
	Additive bool   `json:"additive,omitempty"`
}

// Edge is a role-specific compatibility impact. The prototype leaves this
// empty; the semantic classifier fills it in a later phase.
type Edge struct {
	Message  string `json:"message"`
	Producer string `json:"producer,omitempty"`
	Consumer string `json:"consumer,omitempty"`
	Impact   string `json:"impact"`
	Reason   string `json:"reason,omitempty"`
}

// Evidence records the deterministic facts behind a compatibility manifest.
type Evidence struct {
	FlatcConform   string `json:"flatc_conform"`
	FlatcVersion   string `json:"flatc_version,omitempty"`
	StructuralDiff string `json:"structural_diff"`
	Diagnostics    string `json:"diagnostics,omitempty"`
}

// Compatibility is the compatibility relation between a base version and a
// candidate version.
type Compatibility struct {
	Base           string    `json:"base"`
	Candidate      string    `json:"candidate"`
	Wire           string    `json:"wire"`
	Class          Class     `json:"class"`
	ReviewRequired bool      `json:"review_required"`
	Semantic       string    `json:"semantic"`
	Changes        []Change  `json:"changes,omitempty"`
	Edges          []Edge    `json:"edges,omitempty"`
	Evidence       Evidence  `json:"evidence"`
	CreatedAt      time.Time `json:"created_at"`
}

// Flatc is the subset of the flatc wrapper used by Analyze.
type Flatc interface {
	Conform(ctx context.Context, oldSchema, newSchema string) error
	CompileBinarySchema(ctx context.Context, schema, outDir string, includeDirs ...string) (string, error)
	Version(ctx context.Context) (string, error)
}

// Analyze compares the protocol.fbs in baseDir with the one in candidateDir.
//
// flatc --conform is authoritative for wire compatibility. When both schemas
// compile, a reflection diff classifies the structural changes. The semantic
// and role-specific classification is intentionally left to the advisory
// judge; ReviewRequired is set for anything that is not purely additive.
func Analyze(ctx context.Context, flatc Flatc, baseDir, candidateDir string) (*Compatibility, error) {
	if flatc == nil {
		return nil, errors.New("versioning: flatc is required")
	}
	if strings.TrimSpace(baseDir) == "" || strings.TrimSpace(candidateDir) == "" {
		return nil, errors.New("versioning: base and candidate directories are required")
	}

	baseProtocol := filepath.Join(baseDir, "protocol.fbs")
	candidateProtocol := filepath.Join(candidateDir, "protocol.fbs")
	compat := &Compatibility{
		Wire:      WireCompatible,
		Class:     ClassIdentical,
		Semantic:  "not_evaluated",
		Evidence:  Evidence{FlatcConform: "pass", StructuralDiff: "pass"},
		CreatedAt: time.Now().UTC(),
	}

	conformErr := flatc.Conform(ctx, baseProtocol, candidateProtocol)
	if conformErr != nil {
		if errors.Is(conformErr, fbs.ErrNotInstalled) {
			return nil, conformErr
		}
		compat.Wire = WireBreaking
		compat.Evidence.FlatcConform = "fail"
		var typed *fbs.ConformError
		if errors.As(conformErr, &typed) {
			compat.Evidence.Diagnostics = typed.Diagnostics
		} else {
			compat.Evidence.Diagnostics = conformErr.Error()
		}
	} else if version, err := flatc.Version(ctx); err == nil {
		compat.Evidence.FlatcVersion = version
	}

	changes, diffErr := diffSchemas(ctx, flatc, baseProtocol, candidateProtocol)
	if diffErr != nil {
		if compat.Wire == WireBreaking {
			compat.Evidence.StructuralDiff = "unavailable"
			compat.Evidence.Diagnostics = joinDiagnostics(compat.Evidence.Diagnostics, diffErr.Error())
			compat.Class = ClassBreaking
			compat.ReviewRequired = true
			return compat, nil
		}
		return nil, diffErr
	}

	compat.Changes = changes
	compat.Class = Classify(changes, compat.Wire == WireBreaking)
	compat.ReviewRequired = compat.Class == ClassSemanticChange ||
		compat.Class == ClassBreaking ||
		compat.Class == ClassUnknown
	return compat, nil
}

func diffSchemas(ctx context.Context, flatc Flatc, baseProtocol, candidateProtocol string) ([]Change, error) {
	tmp, err := os.MkdirTemp("", "boundary-mcp-compat-*")
	if err != nil {
		return nil, fmt.Errorf("versioning: create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	baseOut := filepath.Join(tmp, "base")
	candidateOut := filepath.Join(tmp, "candidate")
	for _, dir := range []string{baseOut, candidateOut} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("versioning: create %s: %w", dir, err)
		}
	}

	baseBFBS, err := flatc.CompileBinarySchema(ctx, baseProtocol, baseOut)
	if err != nil {
		return nil, fmt.Errorf("versioning: compile base schema: %w", err)
	}
	candidateBFBS, err := flatc.CompileBinarySchema(ctx, candidateProtocol, candidateOut)
	if err != nil {
		return nil, fmt.Errorf("versioning: compile candidate schema: %w", err)
	}
	baseSchema, err := harness.LoadSchema(baseBFBS)
	if err != nil {
		return nil, fmt.Errorf("versioning: load base schema: %w", err)
	}
	candidateSchema, err := harness.LoadSchema(candidateBFBS)
	if err != nil {
		return nil, fmt.Errorf("versioning: load candidate schema: %w", err)
	}
	return DiffSchemas(baseSchema, candidateSchema), nil
}

// DiffSchemas compares two parsed schemas. The comparison is structural: wire
// identity is the field ID, while a rename is treated as a source-breaking
// change even when flatc accepts it.
func DiffSchemas(base, candidate *harness.Schema) []Change {
	if base == nil || candidate == nil {
		return nil
	}
	var changes []Change

	baseRoot, candidateRoot := rootName(base), rootName(candidate)
	if baseRoot != candidateRoot {
		changes = append(changes, Change{
			Kind:     "root_changed",
			Detail:   baseRoot + " -> " + candidateRoot,
			Breaking: true,
		})
	}
	if base.DeclaredFileIdent != candidate.DeclaredFileIdent {
		changes = append(changes, Change{
			Kind:     "file_identifier_changed",
			Detail:   fmt.Sprintf("%q -> %q", base.DeclaredFileIdent, candidate.DeclaredFileIdent),
			Breaking: true,
		})
	}

	baseObjects := objectMap(base)
	candidateObjects := objectMap(candidate)
	for _, name := range sortedKeys(baseObjects) {
		baseObject := baseObjects[name]
		candidateObject, ok := candidateObjects[name]
		if !ok {
			changes = append(changes, Change{Kind: "object_removed", Object: name, Breaking: true})
			continue
		}
		diffObject(base, candidate, baseObject, candidateObject, &changes)
	}
	for _, name := range sortedKeys(candidateObjects) {
		if _, ok := baseObjects[name]; !ok {
			changes = append(changes, Change{Kind: "object_added", Object: name, Additive: true})
		}
	}

	baseEnums := enumMap(base)
	candidateEnums := enumMap(candidate)
	for _, name := range sortedKeys(baseEnums) {
		baseEnum := baseEnums[name]
		candidateEnum, ok := candidateEnums[name]
		if !ok {
			changes = append(changes, Change{Kind: "enum_removed", Enum: name, Breaking: true})
			continue
		}
		diffEnum(base, candidate, baseEnum, candidateEnum, &changes)
	}
	for _, name := range sortedKeys(candidateEnums) {
		if _, ok := baseEnums[name]; !ok {
			changes = append(changes, Change{Kind: "enum_added", Enum: name, Additive: true})
		}
	}

	sortChanges(changes)
	return changes
}

func diffObject(baseSchema, candidateSchema *harness.Schema, base, candidate *harness.Object, out *[]Change) {
	if base.IsStruct != candidate.IsStruct {
		*out = append(*out, Change{
			Kind:     "object_kind_changed",
			Object:   base.Name,
			Detail:   fmt.Sprintf("struct=%t -> struct=%t", base.IsStruct, candidate.IsStruct),
			Breaking: true,
		})
	}

	baseByID := make(map[uint16]*harness.Field, len(base.Fields))
	baseByName := make(map[string]*harness.Field, len(base.Fields))
	for _, field := range base.Fields {
		baseByID[field.ID] = field
		baseByName[field.Name] = field
	}
	candidateByID := make(map[uint16]*harness.Field, len(candidate.Fields))
	candidateByName := make(map[string]*harness.Field, len(candidate.Fields))
	for _, field := range candidate.Fields {
		candidateByID[field.ID] = field
		candidateByName[field.Name] = field
	}

	seenCandidate := make(map[string]bool, len(candidate.Fields))
	for _, baseField := range base.Fields {
		if candidateField, ok := candidateByID[baseField.ID]; ok {
			seenCandidate[candidateField.Name] = true
			diffField(baseSchema, candidateSchema, base.Name, baseField, candidateField, out)
			continue
		}
		if candidateField, ok := candidateByName[baseField.Name]; ok {
			seenCandidate[candidateField.Name] = true
			*out = append(*out, Change{
				Kind:     "field_id_changed",
				Object:   base.Name,
				Field:    baseField.Name,
				Detail:   fmt.Sprintf("id %d -> %d", baseField.ID, candidateField.ID),
				Breaking: true,
			})
			diffField(baseSchema, candidateSchema, base.Name, baseField, candidateField, out)
			continue
		}
		*out = append(*out, Change{
			Kind:     "field_removed",
			Object:   base.Name,
			Field:    baseField.Name,
			Detail:   fmt.Sprintf("id %d", baseField.ID),
			Breaking: true,
		})
	}

	for _, candidateField := range candidate.Fields {
		if seenCandidate[candidateField.Name] {
			continue
		}
		if _, ok := baseByID[candidateField.ID]; ok {
			continue
		}
		if _, ok := baseByName[candidateField.Name]; ok {
			continue
		}
		change := Change{
			Kind:     "field_added",
			Object:   base.Name,
			Field:    candidateField.Name,
			Detail:   fmt.Sprintf("id %d", candidateField.ID),
			Additive: !candidateField.Required,
			Breaking: candidateField.Required,
		}
		*out = append(*out, change)
	}
}

func diffField(baseSchema, candidateSchema *harness.Schema, object string, base, candidate *harness.Field, out *[]Change) {
	add := func(change Change) {
		change.Object = object
		change.Field = base.Name
		*out = append(*out, change)
	}

	if base.Name != candidate.Name {
		add(Change{
			Kind:     "field_renamed",
			Detail:   fmt.Sprintf("%q -> %q (wire-compatible; source-breaking)", base.Name, candidate.Name),
			Breaking: true,
		})
	}
	baseType := typeName(baseSchema, base.Type)
	candidateType := typeName(candidateSchema, candidate.Type)
	if baseType != candidateType {
		add(Change{
			Kind:     "field_type_changed",
			Detail:   baseType + " -> " + candidateType,
			Breaking: true,
		})
	}
	if !base.Required && candidate.Required {
		add(Change{Kind: "field_required_added", Breaking: true})
	}
	if base.Required && !candidate.Required {
		add(Change{Kind: "field_required_removed", Detail: "optionality weakened"})
	}
	if !base.Deprecated && candidate.Deprecated {
		add(Change{Kind: "field_deprecated"})
	}
	if base.Deprecated && !candidate.Deprecated {
		add(Change{Kind: "field_undeprecated"})
	}
	if base.DefaultInteger != candidate.DefaultInteger || base.DefaultReal != candidate.DefaultReal {
		add(Change{
			Kind:   "field_default_changed",
			Detail: fmt.Sprintf("%v/%v -> %v/%v", base.DefaultInteger, base.DefaultReal, candidate.DefaultInteger, candidate.DefaultReal),
		})
	}
	if base.Key != candidate.Key {
		add(Change{Kind: "field_key_changed"})
	}
	if base.Optional != candidate.Optional {
		add(Change{Kind: "field_optional_changed"})
	}
}

func diffEnum(baseSchema, candidateSchema *harness.Schema, base, candidate *harness.Enum, out *[]Change) {
	if base.IsUnion != candidate.IsUnion {
		*out = append(*out, Change{
			Kind:     "enum_union_changed",
			Enum:     base.Name,
			Detail:   fmt.Sprintf("union=%t -> union=%t", base.IsUnion, candidate.IsUnion),
			Breaking: true,
		})
	}
	if baseType, candidateType := typeName(baseSchema, base.Underlying), typeName(candidateSchema, candidate.Underlying); baseType != candidateType {
		*out = append(*out, Change{
			Kind:     "enum_underlying_changed",
			Enum:     base.Name,
			Detail:   baseType + " -> " + candidateType,
			Breaking: true,
		})
	}

	baseByValue := make(map[int64]harness.EnumValue, len(base.Values))
	baseByName := make(map[string]harness.EnumValue, len(base.Values))
	for _, value := range base.Values {
		baseByValue[value.Value] = value
		baseByName[value.Name] = value
	}
	candidateByValue := make(map[int64]harness.EnumValue, len(candidate.Values))
	candidateByName := make(map[string]harness.EnumValue, len(candidate.Values))
	for _, value := range candidate.Values {
		candidateByValue[value.Value] = value
		candidateByName[value.Name] = value
	}

	seenCandidate := make(map[int64]bool, len(candidate.Values))
	for _, baseValue := range base.Values {
		if candidateValue, ok := candidateByValue[baseValue.Value]; ok {
			seenCandidate[candidateValue.Value] = true
			if baseValue.Name != candidateValue.Name {
				*out = append(*out, Change{
					Kind:     "enum_value_renamed",
					Enum:     base.Name,
					Value:    baseValue.Name,
					Detail:   fmt.Sprintf("%q -> %q (value %d)", baseValue.Name, candidateValue.Name, baseValue.Value),
					Breaking: true,
				})
			}
			continue
		}
		if candidateValue, ok := candidateByName[baseValue.Name]; ok {
			seenCandidate[candidateValue.Value] = true
			*out = append(*out, Change{
				Kind:     "enum_value_renumbered",
				Enum:     base.Name,
				Value:    baseValue.Name,
				Detail:   fmt.Sprintf("%d -> %d", baseValue.Value, candidateValue.Value),
				Breaking: true,
			})
			continue
		}
		*out = append(*out, Change{
			Kind:     "enum_value_removed",
			Enum:     base.Name,
			Value:    baseValue.Name,
			Detail:   fmt.Sprintf("value %d", baseValue.Value),
			Breaking: true,
		})
	}

	for _, candidateValue := range candidate.Values {
		if seenCandidate[candidateValue.Value] {
			continue
		}
		if _, ok := baseByValue[candidateValue.Value]; ok {
			continue
		}
		if _, ok := baseByName[candidateValue.Name]; ok {
			continue
		}
		*out = append(*out, Change{
			Kind:     "enum_value_added",
			Enum:     base.Name,
			Value:    candidateValue.Name,
			Detail:   fmt.Sprintf("value %d", candidateValue.Value),
			Additive: true,
		})
	}
}

// Classify derives a structural class from the change list and flatc's verdict.
func Classify(changes []Change, wireBreaking bool) Class {
	if wireBreaking {
		return ClassBreaking
	}
	if len(changes) == 0 {
		return ClassIdentical
	}
	breaking := false
	additive := true
	for _, change := range changes {
		if change.Breaking {
			breaking = true
		}
		if !change.Additive {
			additive = false
		}
	}
	switch {
	case breaking:
		return ClassBreaking
	case additive:
		return ClassAdditive
	default:
		return ClassSemanticChange
	}
}

// AddBundleChanges records semantic changes that are not visible in
// protocol.fbs. A purpose or state-machine change still requires review even
// when the wire schema is identical.
func AddBundleChanges(compat *Compatibility, base, candidate Bundle) {
	if compat == nil {
		return
	}
	if base.Purpose != candidate.Purpose {
		compat.Changes = append(compat.Changes, Change{
			Kind:   "purpose_changed",
			Detail: "purpose.md changed; semantic review required",
		})
	}
	if base.StateMachine != candidate.StateMachine {
		compat.Changes = append(compat.Changes, Change{
			Kind:   "state_machine_changed",
			Detail: "state_machine.json changed; semantic review required",
		})
	}
	sortChanges(compat.Changes)
	compat.Class = Classify(compat.Changes, compat.Wire == WireBreaking)
	compat.ReviewRequired = compat.Class == ClassSemanticChange ||
		compat.Class == ClassBreaking ||
		compat.Class == ClassUnknown
}

// WriteCompat stores a compatibility manifest under governance/compat.
//
// Re-analyzing the same pair preserves the original CreatedAt when the
// substantive manifest is unchanged, so repeated runs do not create noise.
func (s *Store) WriteCompat(compat Compatibility) (string, error) {
	if strings.TrimSpace(compat.Base) == "" || strings.TrimSpace(compat.Candidate) == "" {
		return "", errors.New("versioning: base and candidate hashes are required")
	}
	path := filepath.Join(s.CompatDir(), compatFilename(compat.Base, compat.Candidate))
	if data, err := os.ReadFile(path); err == nil {
		var existing Compatibility
		if json.Unmarshal(data, &existing) == nil && sameCompat(existing, compat) {
			return path, nil
		}
	}
	if err := writeJSON(path, compat); err != nil {
		return "", err
	}
	return path, nil
}

func sameCompat(first, second Compatibility) bool {
	first.CreatedAt = time.Time{}
	second.CreatedAt = time.Time{}
	// The flatc version is evidence, not a substantive compatibility result;
	// preserving it keeps repeated runs idempotent across toolchain upgrades.
	first.Evidence.FlatcVersion = ""
	second.Evidence.FlatcVersion = ""
	return reflect.DeepEqual(first, second)
}

func compatFilename(base, candidate string) string {
	return strings.TrimPrefix(base, "sha256:") + "__" + strings.TrimPrefix(candidate, "sha256:") + ".json"
}

func rootName(schema *harness.Schema) string {
	if schema == nil || schema.Root == nil {
		return ""
	}
	return schema.Root.Name
}

func objectMap(schema *harness.Schema) map[string]*harness.Object {
	objects := make(map[string]*harness.Object, len(schema.Objects))
	for _, object := range schema.Objects {
		objects[object.Name] = object
	}
	return objects
}

func enumMap(schema *harness.Schema) map[string]*harness.Enum {
	enums := make(map[string]*harness.Enum, len(schema.Enums))
	for _, enum := range schema.Enums {
		enums[enum.Name] = enum
	}
	return enums
}

func typeName(schema *harness.Schema, typ harness.Type) string {
	switch typ.Base {
	case harness.BaseVector:
		return "vector<" + baseTypeName(schema, typ.Element, typ.Index) + ">"
	case harness.BaseVector64:
		return "vector64<" + baseTypeName(schema, typ.Element, typ.Index) + ">"
	case harness.BaseArray:
		return fmt.Sprintf("array<%s,%d>", baseTypeName(schema, typ.Element, typ.Index), typ.FixedLength)
	default:
		return baseTypeName(schema, typ.Base, typ.Index)
	}
}

func baseTypeName(schema *harness.Schema, base harness.BaseType, index int) string {
	if schema != nil && index >= 0 {
		switch base {
		case harness.BaseObj, harness.BaseUnion, harness.BaseUType:
			if object := schema.ObjectAt(index); object != nil {
				return base.String() + ":" + object.Name
			}
		}
		if enum := schema.EnumAt(index); enum != nil {
			return "enum:" + enum.Name
		}
	}
	return base.String()
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortChanges(changes []Change) {
	sort.SliceStable(changes, func(i, j int) bool {
		return changeKey(changes[i]) < changeKey(changes[j])
	})
}

func changeKey(change Change) string {
	return strings.Join([]string{
		change.Kind,
		change.Object,
		change.Field,
		change.Enum,
		change.Value,
		change.Detail,
	}, "\x00")
}

func joinDiagnostics(first, second string) string {
	switch {
	case strings.TrimSpace(first) == "":
		return second
	case strings.TrimSpace(second) == "":
		return first
	default:
		return first + "\n" + second
	}
}
