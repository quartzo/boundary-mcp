package harness

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"
)

// Validate checks that payload is a structurally valid FlatBuffer for the
// schema root table.
func (s *Schema) Validate(payload []byte) error {
	return s.ValidateFor(payload, "")
}

// ValidateFor checks payload against the named table (fully-qualified or short
// name). An empty table name uses the schema root table.
func (s *Schema) ValidateFor(payload []byte, tableName string) (err error) {
	defer recoverMalformed(&err)

	obj := s.Root
	if tableName != "" {
		obj = s.Object(tableName)
		if obj == nil {
			return fmt.Errorf("harness: unknown table %q", tableName)
		}
	}
	if obj == nil {
		return errors.New("harness: schema has no root table")
	}

	d := &decoder{b: payload}
	return s.validateTable(d, d.root(), obj, 0)
}

func (s *Schema) validateTable(d *decoder, tablePos int, obj *Object, depth int) error {
	if depth > 32 {
		return errors.New("harness: nested table depth exceeded")
	}
	if obj.IsStruct {
		d.check(tablePos, int(obj.ByteSize))
		return nil
	}

	d.tableInfo(tablePos)
	for _, field := range obj.Fields {
		if field.Deprecated {
			continue
		}
		pos, ok := d.fieldPos(tablePos, field.ID)
		if !ok {
			if field.Required {
				return fmt.Errorf("required field %s.%s is missing", ShortName(obj.Name), field.Name)
			}
			continue
		}
		if err := s.validateValue(d, pos, field, depth); err != nil {
			return fmt.Errorf("field %s.%s: %w", ShortName(obj.Name), field.Name, err)
		}
	}
	return nil
}

func (s *Schema) validateValue(d *decoder, pos int, field *Field, depth int) error {
	base := field.Type.Base
	switch {
	case base == BaseBool:
		d.check(pos, 1)
		if d.u8(pos) > 1 {
			return errors.New("invalid bool value")
		}
		return nil
	case base.IsScalar():
		d.check(pos, base.size())
		return s.validateEnum(d, pos, field)
	case base == BaseString:
		if !utf8.ValidString(d.stringAt(pos)) {
			return errors.New("invalid UTF-8 string")
		}
		return nil
	case base == BaseVector:
		return s.validateVector(d, pos, field, depth)
	case base == BaseObj:
		target := s.ObjectAt(field.Type.Index)
		if target == nil {
			return fmt.Errorf("unknown object index %d", field.Type.Index)
		}
		if target.IsStruct {
			d.check(pos, int(target.ByteSize))
			return nil
		}
		return s.validateTable(d, d.deref(pos), target, depth+1)
	case base == BaseArray:
		size := int(field.Type.ElementSize)
		if size <= 0 {
			return errors.New("array without element size")
		}
		d.check(pos, int(field.Type.FixedLength)*size)
		return nil
	case base == BaseUnion:
		_ = d.deref(pos)
		return nil
	case base == BaseUType:
		return nil
	case base == BaseVector64:
		return errors.New("vector64 is not supported")
	default:
		return fmt.Errorf("unsupported base type %s", base)
	}
}

func (s *Schema) validateEnum(d *decoder, pos int, field *Field) error {
	if field.Type.Index < 0 {
		return nil
	}
	enum := s.EnumAt(field.Type.Index)
	if enum == nil || enum.IsUnion {
		return nil
	}
	value := d.intValue(field.Type.Base, pos)
	if !enum.Has(value) {
		return fmt.Errorf("enum %s has no value %d", ShortName(enum.Name), value)
	}
	return nil
}

func (s *Schema) validateVector(d *decoder, pos int, field *Field, depth int) error {
	start, n := d.vectorAt(pos)
	element := field.Type.Element

	switch element {
	case BaseString:
		d.check(start, n*4)
		for i := 0; i < n; i++ {
			if !utf8.ValidString(d.stringAt(start + i*4)) {
				return errors.New("invalid UTF-8 string in vector")
			}
		}
	case BaseObj:
		target := s.ObjectAt(field.Type.Index)
		if target == nil {
			return fmt.Errorf("unknown object index %d", field.Type.Index)
		}
		if target.IsStruct {
			d.check(start, n*int(target.ByteSize))
			return nil
		}
		d.check(start, n*4)
		for i := 0; i < n; i++ {
			if err := s.validateTable(d, d.deref(start+i*4), target, depth+1); err != nil {
				return err
			}
		}
	case BaseUnion, BaseUType:
		return nil
	case BaseArray, BaseVector, BaseVector64, BaseNone:
		return fmt.Errorf("unsupported vector element %s", element)
	default:
		size := element.size()
		if size == 0 {
			return fmt.Errorf("unsupported vector element %s", element)
		}
		d.check(start, n*size)
		if field.Type.Index >= 0 {
			enum := s.EnumAt(field.Type.Index)
			if enum != nil && !enum.IsUnion {
				for i := 0; i < n; i++ {
					if !enum.Has(d.intValue(element, start+i*size)) {
						return fmt.Errorf("enum %s has an invalid vector value", ShortName(enum.Name))
					}
				}
			}
		}
	}
	return nil
}

// ValidateBuffer checks that payload is a structurally valid FlatBuffer with a
// well-formed root table, without assuming a specific table type. It is used
// for responses to malformed inputs, which may legitimately be typed errors
// from another table in the schema.
func ValidateBuffer(payload []byte) (err error) {
	defer recoverMalformed(&err)

	d := &decoder{b: payload}
	d.tableInfo(d.root())
	return nil
}

// ReadScalar reads a scalar field from the root table.
func (s *Schema) ReadScalar(payload []byte, fieldName string) (value any, err error) {
	defer recoverMalformed(&err)

	field, pos, err := s.rootField(payload, fieldName)
	if err != nil {
		return nil, err
	}
	if !field.Type.Base.IsScalar() {
		return nil, fmt.Errorf("harness: field %q is not scalar", fieldName)
	}
	d := &decoder{b: payload}
	return d.scalarValue(field.Type.Base, pos), nil
}

// ReadString reads a string field from the root table.
func (s *Schema) ReadString(payload []byte, fieldName string) (value string, err error) {
	defer recoverMalformed(&err)

	field, pos, err := s.rootField(payload, fieldName)
	if err != nil {
		return "", err
	}
	if field.Type.Base != BaseString {
		return "", fmt.Errorf("harness: field %q is not a string", fieldName)
	}
	return (&decoder{b: payload}).stringAt(pos), nil
}

func (s *Schema) rootField(payload []byte, fieldName string) (*Field, int, error) {
	obj := s.Root
	if obj == nil {
		return nil, 0, errors.New("harness: schema has no root table")
	}
	field := obj.Field(fieldName)
	if field == nil {
		return nil, 0, fmt.Errorf("harness: table %s has no field %q", ShortName(obj.Name), fieldName)
	}
	d := &decoder{b: payload}
	pos, ok := d.fieldPos(d.root(), field.ID)
	if !ok {
		return nil, 0, fmt.Errorf("harness: field %q is absent", fieldName)
	}
	return field, pos, nil
}

// MutateScalar returns a copy of payload with a scalar root-table field set to
// value.
func (s *Schema) MutateScalar(payload []byte, fieldName string, value any) (out []byte, err error) {
	defer recoverMalformed(&err)

	field, pos, err := s.rootField(payload, fieldName)
	if err != nil {
		return nil, err
	}
	if !field.Type.Base.IsScalar() {
		return nil, fmt.Errorf("harness: field %q is not scalar", fieldName)
	}

	out = append([]byte(nil), payload...)
	if err := writeScalar(out, pos, field.Type.Base, value); err != nil {
		return nil, err
	}
	return out, nil
}

// Mutation is a named payload variant.
type Mutation struct {
	Name    string
	Payload []byte
}

// BoundaryMutations returns structural mutations of payload: scalar fields at
// boundary values, invalid UTF-8 strings, truncations and corrupted offsets.
func (s *Schema) BoundaryMutations(payload []byte) (mutations []Mutation, err error) {
	defer recoverMalformed(&err)

	obj := s.Root
	if obj == nil {
		return nil, errors.New("harness: schema has no root table")
	}
	d := &decoder{b: payload}
	root := d.root()

	for _, field := range obj.Fields {
		if field.Deprecated {
			continue
		}
		pos, ok := d.fieldPos(root, field.ID)
		if !ok {
			continue
		}
		switch {
		case field.Type.Base.IsScalar():
			for _, value := range boundaryValues(field.Type.Base) {
				mutated := append([]byte(nil), payload...)
				if err := writeScalar(mutated, pos, field.Type.Base, value); err != nil {
					return nil, err
				}
				mutations = append(mutations, Mutation{
					Name:    fmt.Sprintf("%s=%v", field.Name, value),
					Payload: mutated,
				})
			}
		case field.Type.Base == BaseString:
			str := d.stringAt(pos)
			if len(str) > 0 {
				mutated := append([]byte(nil), payload...)
				target := d.deref(pos)
				for i := 0; i < len(str); i++ {
					mutated[target+4+i] = 0xFF
				}
				mutations = append(mutations, Mutation{Name: field.Name + "=invalid-utf8", Payload: mutated})
			}
		}
	}

	if len(payload) > 2 {
		mutations = append(mutations,
			Mutation{Name: "truncate-half", Payload: append([]byte(nil), payload[:len(payload)/2]...)},
			Mutation{Name: "truncate-last", Payload: append([]byte(nil), payload[:len(payload)-1]...)},
		)
	}
	if len(payload) >= 4 {
		badRoot := append([]byte(nil), payload...)
		binary.LittleEndian.PutUint32(badRoot, uint32(len(badRoot)+16))
		mutations = append(mutations, Mutation{Name: "bad-root-offset", Payload: badRoot})

		badVTable := append([]byte(nil), payload...)
		binary.LittleEndian.PutUint32(badVTable[root:], uint32(root+4))
		mutations = append(mutations, Mutation{Name: "bad-vtable", Payload: badVTable})
	}
	if len(payload) > 0 {
		flipped := append([]byte(nil), payload...)
		flipped[len(flipped)-1] ^= 0xFF
		mutations = append(mutations, Mutation{Name: "flip-last-byte", Payload: flipped})
	}

	return mutations, nil
}

func boundaryValues(base BaseType) []any {
	switch base {
	case BaseBool:
		return []any{false, true}
	case BaseByte:
		return []any{int8(math.MinInt8), int8(0), int8(math.MaxInt8)}
	case BaseUByte:
		return []any{uint8(0), uint8(math.MaxUint8)}
	case BaseShort:
		return []any{int16(math.MinInt16), int16(0), int16(math.MaxInt16)}
	case BaseUShort:
		return []any{uint16(0), uint16(math.MaxUint16)}
	case BaseInt:
		return []any{int32(math.MinInt32), int32(0), int32(math.MaxInt32)}
	case BaseUInt:
		return []any{uint32(0), uint32(math.MaxUint32)}
	case BaseLong:
		return []any{int64(math.MinInt64), int64(0), int64(math.MaxInt64)}
	case BaseULong:
		return []any{uint64(0), uint64(math.MaxUint64)}
	case BaseFloat:
		return []any{float32(-math.MaxFloat32), float32(0), float32(math.MaxFloat32)}
	case BaseDouble:
		return []any{-math.MaxFloat64, float64(0), math.MaxFloat64}
	default:
		return nil
	}
}

func (d *decoder) scalarValue(base BaseType, pos int) any {
	switch base {
	case BaseBool:
		return d.boolean(pos)
	case BaseByte:
		return d.i8(pos)
	case BaseUByte:
		return d.u8(pos)
	case BaseShort:
		return d.i16(pos)
	case BaseUShort:
		return d.u16(pos)
	case BaseInt:
		return d.i32(pos)
	case BaseUInt:
		return d.u32(pos)
	case BaseLong:
		return d.i64(pos)
	case BaseULong:
		return d.u64(pos)
	case BaseFloat:
		return d.f32(pos)
	case BaseDouble:
		return d.f64(pos)
	default:
		return nil
	}
}

func (d *decoder) intValue(base BaseType, pos int) int64 {
	switch base {
	case BaseBool:
		if d.boolean(pos) {
			return 1
		}
		return 0
	case BaseByte:
		return int64(d.i8(pos))
	case BaseUByte:
		return int64(d.u8(pos))
	case BaseShort:
		return int64(d.i16(pos))
	case BaseUShort:
		return int64(d.u16(pos))
	case BaseInt:
		return int64(d.i32(pos))
	case BaseUInt:
		return int64(d.u32(pos))
	case BaseLong:
		return d.i64(pos)
	case BaseULong:
		return int64(d.u64(pos))
	case BaseFloat:
		return int64(d.f32(pos))
	case BaseDouble:
		return int64(d.f64(pos))
	default:
		return 0
	}
}

func writeScalar(b []byte, pos int, base BaseType, value any) error {
	if pos < 0 || pos+base.size() > len(b) || base.size() == 0 {
		return errors.New("harness: scalar field is out of bounds")
	}

	switch base {
	case BaseBool:
		v, ok := value.(bool)
		if !ok {
			return fmt.Errorf("harness: %s value must be bool", base)
		}
		if v {
			b[pos] = 1
		} else {
			b[pos] = 0
		}
	case BaseByte:
		v, err := asInt64(value)
		if err != nil || v < math.MinInt8 || v > math.MaxInt8 {
			return fmt.Errorf("harness: %s value out of range", base)
		}
		b[pos] = byte(int8(v))
	case BaseUByte:
		v, err := asUint64(value)
		if err != nil || v > math.MaxUint8 {
			return fmt.Errorf("harness: %s value out of range", base)
		}
		b[pos] = byte(v)
	case BaseShort:
		v, err := asInt64(value)
		if err != nil || v < math.MinInt16 || v > math.MaxInt16 {
			return fmt.Errorf("harness: %s value out of range", base)
		}
		binary.LittleEndian.PutUint16(b[pos:], uint16(int16(v)))
	case BaseUShort:
		v, err := asUint64(value)
		if err != nil || v > math.MaxUint16 {
			return fmt.Errorf("harness: %s value out of range", base)
		}
		binary.LittleEndian.PutUint16(b[pos:], uint16(v))
	case BaseInt:
		v, err := asInt64(value)
		if err != nil || v < math.MinInt32 || v > math.MaxInt32 {
			return fmt.Errorf("harness: %s value out of range", base)
		}
		binary.LittleEndian.PutUint32(b[pos:], uint32(int32(v)))
	case BaseUInt:
		v, err := asUint64(value)
		if err != nil || v > math.MaxUint32 {
			return fmt.Errorf("harness: %s value out of range", base)
		}
		binary.LittleEndian.PutUint32(b[pos:], uint32(v))
	case BaseLong:
		v, err := asInt64(value)
		if err != nil {
			return fmt.Errorf("harness: %s value out of range", base)
		}
		binary.LittleEndian.PutUint64(b[pos:], uint64(v))
	case BaseULong:
		v, err := asUint64(value)
		if err != nil {
			return fmt.Errorf("harness: %s value out of range", base)
		}
		binary.LittleEndian.PutUint64(b[pos:], v)
	case BaseFloat:
		v, err := asFloat64(value)
		if err != nil || v < -math.MaxFloat32 || v > math.MaxFloat32 {
			return fmt.Errorf("harness: %s value out of range", base)
		}
		binary.LittleEndian.PutUint32(b[pos:], math.Float32bits(float32(v)))
	case BaseDouble:
		v, err := asFloat64(value)
		if err != nil {
			return fmt.Errorf("harness: %s value out of range", base)
		}
		binary.LittleEndian.PutUint64(b[pos:], math.Float64bits(v))
	default:
		return fmt.Errorf("harness: unsupported scalar type %s", base)
	}
	return nil
}

func asInt64(value any) (int64, error) {
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int8:
		return int64(v), nil
	case int16:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case uint:
		if uint64(v) > math.MaxInt64 {
			return 0, errors.New("overflow")
		}
		return int64(v), nil
	case uint8:
		return int64(v), nil
	case uint16:
		return int64(v), nil
	case uint32:
		return int64(v), nil
	case uint64:
		if v > math.MaxInt64 {
			return 0, errors.New("overflow")
		}
		return int64(v), nil
	default:
		return 0, fmt.Errorf("unsupported integer value %T", value)
	}
}

func asUint64(value any) (uint64, error) {
	switch v := value.(type) {
	case int:
		if v < 0 {
			return 0, errors.New("negative")
		}
		return uint64(v), nil
	case int8:
		if v < 0 {
			return 0, errors.New("negative")
		}
		return uint64(v), nil
	case int16:
		if v < 0 {
			return 0, errors.New("negative")
		}
		return uint64(v), nil
	case int32:
		if v < 0 {
			return 0, errors.New("negative")
		}
		return uint64(v), nil
	case int64:
		if v < 0 {
			return 0, errors.New("negative")
		}
		return uint64(v), nil
	case uint:
		return uint64(v), nil
	case uint8:
		return uint64(v), nil
	case uint16:
		return uint64(v), nil
	case uint32:
		return uint64(v), nil
	case uint64:
		return v, nil
	default:
		return 0, fmt.Errorf("unsupported integer value %T", value)
	}
}

func asFloat64(value any) (float64, error) {
	switch v := value.(type) {
	case float32:
		return float64(v), nil
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case uint32:
		return float64(v), nil
	case uint64:
		return float64(v), nil
	default:
		return 0, fmt.Errorf("unsupported float value %T", value)
	}
}
