package protocol

import (
	"errors"
	"fmt"
	"math"

	flatbuffers "github.com/google/flatbuffers/go"
)

// FrameSpec describes a FlatBuffer message to build from the schema.
type FrameSpec struct {
	Table  string         `json:"table,omitempty"`
	Values map[string]any `json:"values,omitempty"`
}

// BuildFrame builds a valid payload for spec.Table (or the schema root table
// when Table is empty). Missing required fields receive safe defaults.
func (s *Schema) BuildFrame(spec FrameSpec) ([]byte, error) {
	obj := s.Root
	if spec.Table != "" {
		obj = s.Object(spec.Table)
	}
	if obj == nil {
		return nil, fmt.Errorf("protocol: unknown table %q", spec.Table)
	}
	if obj.IsStruct {
		return nil, fmt.Errorf("protocol: cannot build root struct %s", ShortName(obj.Name))
	}

	builder := flatbuffers.NewBuilder(1024)
	root, err := s.buildObject(builder, obj, spec.Values)
	if err != nil {
		return nil, err
	}
	if s.DeclaredFileIdent != "" {
		fid := []byte(s.DeclaredFileIdent)
		if len(fid) != 4 {
			return nil, fmt.Errorf("protocol: file_identifier must be 4 bytes (got %d)", len(fid))
		}
		builder.FinishWithFileIdentifier(root, fid)
	} else {
		builder.Finish(root)
	}

	out := make([]byte, len(builder.FinishedBytes()))
	copy(out, builder.FinishedBytes())
	return out, nil
}

type preparedField struct {
	field  *Field
	value  any
	offset flatbuffers.UOffsetT
}

func (s *Schema) buildObject(builder *flatbuffers.Builder, obj *Object, values map[string]any) (flatbuffers.UOffsetT, error) {
	if obj.IsStruct {
		return 0, fmt.Errorf("protocol: struct fields are not supported by the builder (%s)", ShortName(obj.Name))
	}

	prepared := make([]preparedField, 0, len(obj.Fields))
	for _, field := range obj.Fields {
		if field.Deprecated {
			continue
		}

		value, present := values[field.Name]
		if !present {
			if !field.Required {
				continue
			}
			var err error
			value, err = s.defaultValue(field)
			if err != nil {
				return 0, err
			}
		}

		switch base := field.Type.Base; {
		case base.IsScalar():
			converted, err := s.convertScalar(field, value)
			if err != nil {
				return 0, fmt.Errorf("field %s: %w", field.Name, err)
			}
			prepared = append(prepared, preparedField{field: field, value: converted})
		case base == BaseString:
			str, ok := value.(string)
			if !ok {
				return 0, fmt.Errorf("protocol: field %s must be a string", field.Name)
			}
			prepared = append(prepared, preparedField{
				field:  field,
				offset: builder.CreateString(str),
			})
		case base == BaseObj:
			target := s.ObjectAt(field.Type.Index)
			if target == nil {
				return 0, fmt.Errorf("protocol: field %s has an unknown object index", field.Name)
			}
			childValues, err := asValuesMap(value, field.Name)
			if err != nil {
				return 0, err
			}
			offset, err := s.buildObject(builder, target, childValues)
			if err != nil {
				return 0, err
			}
			prepared = append(prepared, preparedField{field: field, offset: offset})
		case base == BaseVector:
			offset, err := s.buildVector(builder, field, value)
			if err != nil {
				return 0, err
			}
			prepared = append(prepared, preparedField{field: field, offset: offset})
		default:
			return 0, fmt.Errorf("protocol: field %s uses unsupported type %s", field.Name, base)
		}
	}

	builder.StartObject(len(obj.Fields))
	for _, item := range prepared {
		slot := int(item.field.ID)
		if item.field.Type.Base.IsScalar() {
			prependScalarSlot(builder, slot, item.field, item.value)
		} else {
			builder.PrependUOffsetTSlot(slot, item.offset, 0)
		}
	}
	return builder.EndObject(), nil
}

func (s *Schema) buildVector(builder *flatbuffers.Builder, field *Field, value any) (flatbuffers.UOffsetT, error) {
	list, ok := value.([]any)
	if !ok {
		return 0, fmt.Errorf("protocol: field %s must be an array", field.Name)
	}
	n := len(list)
	element := field.Type.Element

	switch {
	case element == BaseString:
		offsets := make([]flatbuffers.UOffsetT, n)
		for i, item := range list {
			str, ok := item.(string)
			if !ok {
				return 0, fmt.Errorf("protocol: field %s[%d] must be a string", field.Name, i)
			}
			offsets[i] = builder.CreateString(str)
		}
		builder.StartVector(4, n, 4)
		for i := n - 1; i >= 0; i-- {
			builder.PrependUOffsetT(offsets[i])
		}
		return builder.EndVector(n), nil

	case element == BaseObj:
		target := s.ObjectAt(field.Type.Index)
		if target == nil {
			return 0, fmt.Errorf("protocol: field %s has an unknown object index", field.Name)
		}
		if target.IsStruct {
			return 0, fmt.Errorf("protocol: vectors of structs are not supported (%s)", field.Name)
		}
		offsets := make([]flatbuffers.UOffsetT, n)
		for i, item := range list {
			childValues, err := asValuesMap(item, fmt.Sprintf("%s[%d]", field.Name, i))
			if err != nil {
				return 0, err
			}
			offset, err := s.buildObject(builder, target, childValues)
			if err != nil {
				return 0, err
			}
			offsets[i] = offset
		}
		builder.StartVector(4, n, 4)
		for i := n - 1; i >= 0; i-- {
			builder.PrependUOffsetT(offsets[i])
		}
		return builder.EndVector(n), nil

	case element.IsScalar():
		elemType := field.Type
		elemType.Base = element
		elemType.Element = BaseNone
		converted := make([]any, n)
		for i, item := range list {
			value, err := s.convertScalarType(elemType, item)
			if err != nil {
				return 0, fmt.Errorf("protocol: field %s[%d]: %w", field.Name, i, err)
			}
			converted[i] = value
		}
		size := element.size()
		builder.StartVector(size, n, size)
		for i := n - 1; i >= 0; i-- {
			prependScalar(builder, element, converted[i])
		}
		return builder.EndVector(n), nil

	default:
		return 0, fmt.Errorf("protocol: vectors of %s are not supported (%s)", element, field.Name)
	}
}

func (s *Schema) convertScalar(field *Field, value any) (any, error) {
	return s.convertScalarType(field.Type, value)
}

func (s *Schema) convertScalarType(t Type, value any) (any, error) {
	if str, ok := value.(string); ok && t.Index >= 0 {
		enum := s.EnumAt(t.Index)
		if enum == nil {
			return nil, fmt.Errorf("unknown enum index %d", t.Index)
		}
		for _, candidate := range enum.Values {
			if candidate.Name == str {
				value = candidate.Value
				break
			}
		}
	}

	switch t.Base {
	case BaseBool:
		v, ok := value.(bool)
		if !ok {
			return nil, errors.New("must be a boolean")
		}
		return v, nil
	case BaseByte:
		v, err := intValue(value)
		if err != nil || v < math.MinInt8 || v > math.MaxInt8 {
			return nil, errors.New("out of range for byte")
		}
		return int8(v), nil
	case BaseUByte:
		v, err := uintValue(value)
		if err != nil || v > math.MaxUint8 {
			return nil, errors.New("out of range for ubyte")
		}
		return uint8(v), nil
	case BaseShort:
		v, err := intValue(value)
		if err != nil || v < math.MinInt16 || v > math.MaxInt16 {
			return nil, errors.New("out of range for short")
		}
		return int16(v), nil
	case BaseUShort:
		v, err := uintValue(value)
		if err != nil || v > math.MaxUint16 {
			return nil, errors.New("out of range for ushort")
		}
		return uint16(v), nil
	case BaseInt:
		v, err := intValue(value)
		if err != nil || v < math.MinInt32 || v > math.MaxInt32 {
			return nil, errors.New("out of range for int")
		}
		return int32(v), nil
	case BaseUInt:
		v, err := uintValue(value)
		if err != nil || v > math.MaxUint32 {
			return nil, errors.New("out of range for uint")
		}
		return uint32(v), nil
	case BaseLong:
		v, err := intValue(value)
		if err != nil {
			return nil, errors.New("out of range for long")
		}
		return v, nil
	case BaseULong:
		v, err := uintValue(value)
		if err != nil {
			return nil, errors.New("out of range for ulong")
		}
		return v, nil
	case BaseFloat:
		v, err := floatValue(value)
		if err != nil {
			return nil, err
		}
		return float32(v), nil
	case BaseDouble:
		v, err := floatValue(value)
		if err != nil {
			return nil, err
		}
		return v, nil
	default:
		return nil, fmt.Errorf("unsupported scalar type %s", t.Base)
	}
}

func (s *Schema) defaultValue(field *Field) (any, error) {
	if field.Type.Base == BaseObj {
		return map[string]any{}, nil
	}
	if field.Type.Base == BaseVector {
		return []any{}, nil
	}
	if field.Type.Base == BaseString {
		return "", nil
	}
	if field.Type.Base.IsScalar() {
		if field.Type.Index >= 0 {
			if enum := s.EnumAt(field.Type.Index); enum != nil && len(enum.Values) > 0 {
				return enum.Values[0].Value, nil
			}
		}
		switch field.Type.Base {
		case BaseBool:
			return false, nil
		case BaseFloat, BaseDouble:
			return float64(1), nil
		default:
			return int64(1), nil
		}
	}
	return nil, fmt.Errorf("protocol: required field %s uses unsupported type %s", field.Name, field.Type.Base)
}

func prependScalarSlot(builder *flatbuffers.Builder, slot int, field *Field, value any) {
	base := field.Type.Base
	def := fieldDefault(field)
	if field.Required {
		def = forceDefault(base, value)
	}

	switch base {
	case BaseBool:
		builder.PrependBoolSlot(slot, value.(bool), def.(bool))
	case BaseByte:
		builder.PrependInt8Slot(slot, value.(int8), def.(int8))
	case BaseUByte:
		builder.PrependUint8Slot(slot, value.(uint8), def.(uint8))
	case BaseShort:
		builder.PrependInt16Slot(slot, value.(int16), def.(int16))
	case BaseUShort:
		builder.PrependUint16Slot(slot, value.(uint16), def.(uint16))
	case BaseInt:
		builder.PrependInt32Slot(slot, value.(int32), def.(int32))
	case BaseUInt:
		builder.PrependUint32Slot(slot, value.(uint32), def.(uint32))
	case BaseLong:
		builder.PrependInt64Slot(slot, value.(int64), def.(int64))
	case BaseULong:
		builder.PrependUint64Slot(slot, value.(uint64), def.(uint64))
	case BaseFloat:
		builder.PrependFloat32Slot(slot, value.(float32), def.(float32))
	case BaseDouble:
		builder.PrependFloat64Slot(slot, value.(float64), def.(float64))
	}
}

func prependScalar(builder *flatbuffers.Builder, base BaseType, value any) {
	switch base {
	case BaseBool:
		builder.PrependBool(value.(bool))
	case BaseByte:
		builder.PrependInt8(value.(int8))
	case BaseUByte:
		builder.PrependUint8(value.(uint8))
	case BaseShort:
		builder.PrependInt16(value.(int16))
	case BaseUShort:
		builder.PrependUint16(value.(uint16))
	case BaseInt:
		builder.PrependInt32(value.(int32))
	case BaseUInt:
		builder.PrependUint32(value.(uint32))
	case BaseLong:
		builder.PrependInt64(value.(int64))
	case BaseULong:
		builder.PrependUint64(value.(uint64))
	case BaseFloat:
		builder.PrependFloat32(value.(float32))
	case BaseDouble:
		builder.PrependFloat64(value.(float64))
	}
}

func fieldDefault(field *Field) any {
	switch field.Type.Base {
	case BaseBool:
		return field.DefaultInteger != 0
	case BaseByte:
		return int8(field.DefaultInteger)
	case BaseUByte:
		return uint8(field.DefaultInteger)
	case BaseShort:
		return int16(field.DefaultInteger)
	case BaseUShort:
		return uint16(field.DefaultInteger)
	case BaseInt:
		return int32(field.DefaultInteger)
	case BaseUInt:
		return uint32(field.DefaultInteger)
	case BaseLong:
		return field.DefaultInteger
	case BaseULong:
		return uint64(field.DefaultInteger)
	case BaseFloat:
		return float32(field.DefaultReal)
	case BaseDouble:
		return field.DefaultReal
	default:
		return nil
	}
}

func forceDefault(base BaseType, value any) any {
	switch base {
	case BaseBool:
		return !value.(bool)
	case BaseByte:
		return value.(int8) + 1
	case BaseUByte:
		return value.(uint8) + 1
	case BaseShort:
		return value.(int16) + 1
	case BaseUShort:
		return value.(uint16) + 1
	case BaseInt:
		v := value.(int32)
		if v == math.MaxInt32 {
			return v - 1
		}
		return v + 1
	case BaseUInt:
		return value.(uint32) + 1
	case BaseLong:
		v := value.(int64)
		if v == math.MaxInt64 {
			return v - 1
		}
		return v + 1
	case BaseULong:
		return value.(uint64) + 1
	case BaseFloat:
		return value.(float32) + 1
	case BaseDouble:
		return value.(float64) + 1
	default:
		return value
	}
}

func asValuesMap(value any, field string) (map[string]any, error) {
	if value == nil {
		return nil, nil
	}
	values, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("protocol: field %s must be an object", field)
	}
	return values, nil
}

func intValue(value any) (int64, error) {
	switch v := value.(type) {
	case float64:
		if v != math.Trunc(v) {
			return 0, errors.New("must be an integer")
		}
		return int64(v), nil
	case float32:
		if v != float32(math.Trunc(float64(v))) {
			return 0, errors.New("must be an integer")
		}
		return int64(v), nil
	default:
		return asInt64(value)
	}
}

func uintValue(value any) (uint64, error) {
	switch v := value.(type) {
	case float64:
		if v != math.Trunc(v) || v < 0 {
			return 0, errors.New("must be a non-negative integer")
		}
		return uint64(v), nil
	case float32:
		if v != float32(math.Trunc(float64(v))) || v < 0 {
			return 0, errors.New("must be a non-negative integer")
		}
		return uint64(v), nil
	default:
		return asUint64(value)
	}
}

func floatValue(value any) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	default:
		return asFloat64(value)
	}
}
