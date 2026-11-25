package protocol

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// BaseType mirrors reflection.BaseType.
type BaseType int8

const (
	BaseNone BaseType = iota
	BaseUType
	BaseBool
	BaseByte
	BaseUByte
	BaseShort
	BaseUShort
	BaseInt
	BaseUInt
	BaseLong
	BaseULong
	BaseFloat
	BaseDouble
	BaseString
	BaseVector
	BaseObj
	BaseUnion
	BaseArray
	BaseVector64
)

// String returns the FlatBuffers name of the base type.
func (b BaseType) String() string {
	switch b {
	case BaseNone:
		return "none"
	case BaseUType:
		return "utype"
	case BaseBool:
		return "bool"
	case BaseByte:
		return "byte"
	case BaseUByte:
		return "ubyte"
	case BaseShort:
		return "short"
	case BaseUShort:
		return "ushort"
	case BaseInt:
		return "int"
	case BaseUInt:
		return "uint"
	case BaseLong:
		return "long"
	case BaseULong:
		return "ulong"
	case BaseFloat:
		return "float"
	case BaseDouble:
		return "double"
	case BaseString:
		return "string"
	case BaseVector:
		return "vector"
	case BaseObj:
		return "obj"
	case BaseUnion:
		return "union"
	case BaseArray:
		return "array"
	case BaseVector64:
		return "vector64"
	default:
		return fmt.Sprintf("basetype(%d)", int8(b))
	}
}

// IsScalar reports whether the type is stored inline as a scalar.
func (b BaseType) IsScalar() bool {
	return b >= BaseBool && b <= BaseDouble
}

func (b BaseType) size() int {
	switch b {
	case BaseBool, BaseByte, BaseUByte:
		return 1
	case BaseShort, BaseUShort:
		return 2
	case BaseInt, BaseUInt, BaseFloat:
		return 4
	case BaseLong, BaseULong, BaseDouble:
		return 8
	default:
		return 0
	}
}

func (b BaseType) signed() bool {
	switch b {
	case BaseByte, BaseShort, BaseInt, BaseLong:
		return true
	default:
		return false
	}
}

// Type is a field or element type from .bfbs reflection.
type Type struct {
	Base        BaseType
	Element     BaseType
	Index       int
	FixedLength uint16
	BaseSize    uint32
	ElementSize uint32
}

// Field is a table field from .bfbs reflection.
type Field struct {
	Name           string
	ID             uint16
	Offset         uint16
	Type           Type
	DefaultInteger int64
	DefaultReal    float64
	Deprecated     bool
	Required       bool
	Optional       bool
	Key            bool
}

// Object is a table or struct from .bfbs reflection.
type Object struct {
	Name     string
	Fields   []*Field
	IsStruct bool
	MinAlign int32
	ByteSize int32

	byName map[string]*Field
	byID   map[uint16]*Field
}

// Field returns the field with the given name, or nil.
func (o *Object) Field(name string) *Field { return o.byName[name] }

// FieldByID returns the field with the given ID, or nil.
func (o *Object) FieldByID(id uint16) *Field { return o.byID[id] }

// EnumValue is a single enum member.
type EnumValue struct {
	Name  string
	Value int64
}

// Enum is an enum from .bfbs reflection.
type Enum struct {
	Name       string
	Values     []EnumValue
	IsUnion    bool
	Underlying Type
}

// Has reports whether v is a declared value.
func (e *Enum) Has(v int64) bool {
	for _, value := range e.Values {
		if value.Value == v {
			return true
		}
	}
	return false
}

// Schema is the subset of .bfbs reflection used by the harness.
type Schema struct {
	Objects   []*Object
	Enums     []*Enum
	Root      *Object
	FileIdent string
	// DeclaredFileIdent is the source schema's file_identifier, if any.
	DeclaredFileIdent string

	byName map[string]*Object
}

// LoadSchema reads and parses a .bfbs file.
func LoadSchema(path string) (*Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("protocol: read schema: %w", err)
	}
	return ParseSchema(data)
}

// ParseSchema parses a binary FlatBuffer schema produced by
// flatc --binary --schema.
func ParseSchema(data []byte) (schema *Schema, err error) {
	defer recoverMalformed(&err)

	d := &decoder{b: data}
	root := d.root()

	s := &Schema{}
	if len(data) >= 8 {
		s.FileIdent = string(data[4:8])
	}
	if ident, ok := d.getString(root, 2); ok {
		s.DeclaredFileIdent = ident
	}

	start, n, ok := d.getVector(root, 0)
	if !ok {
		return nil, malformedf("schema has no objects")
	}
	d.check(start, n*4)
	for i := 0; i < n; i++ {
		s.Objects = append(s.Objects, parseObject(d, d.deref(start+i*4)))
	}

	if start, n, ok := d.getVector(root, 1); ok {
		d.check(start, n*4)
		for i := 0; i < n; i++ {
			s.Enums = append(s.Enums, parseEnum(d, d.deref(start+i*4)))
		}
	}

	if rootPos, ok := d.getTable(root, 4); ok {
		rootObj := parseObject(d, rootPos)
		if existing := s.object(rootObj.Name); existing != nil {
			rootObj = existing
		} else {
			s.Objects = append(s.Objects, rootObj)
		}
		s.Root = rootObj
	}

	sort.Slice(s.Objects, func(i, j int) bool { return s.Objects[i].Name < s.Objects[j].Name })
	s.byName = make(map[string]*Object, len(s.Objects))
	for _, obj := range s.Objects {
		s.byName[obj.Name] = obj
	}
	return s, nil
}

func (s *Schema) object(name string) *Object {
	if s.byName == nil {
		for _, obj := range s.Objects {
			if obj.Name == name {
				return obj
			}
		}
		return nil
	}
	return s.byName[name]
}

// Object returns the object with the given fully-qualified or short name, or
// nil.
func (s *Schema) Object(name string) *Object {
	if s == nil {
		return nil
	}
	if obj := s.object(name); obj != nil {
		return obj
	}
	for _, obj := range s.Objects {
		if ShortName(obj.Name) == name {
			return obj
		}
	}
	return nil
}

// ObjectAt returns the object at a reflection index, or nil.
func (s *Schema) ObjectAt(index int) *Object {
	if s == nil || index < 0 || index >= len(s.Objects) {
		return nil
	}
	return s.Objects[index]
}

// EnumAt returns the enum at a reflection index, or nil.
func (s *Schema) EnumAt(index int) *Enum {
	if s == nil || index < 0 || index >= len(s.Enums) {
		return nil
	}
	return s.Enums[index]
}

// RootTable returns the schema's root table, or nil.
func (s *Schema) RootTable() *Object {
	if s == nil {
		return nil
	}
	return s.Root
}

// ShortName strips the namespace from a fully-qualified name.
func ShortName(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

func parseObject(d *decoder, pos int) *Object {
	name, _ := d.getString(pos, 0)
	obj := &Object{
		Name:     name,
		IsStruct: d.getBool(pos, 2, false),
		MinAlign: d.getI32(pos, 3, 0),
		ByteSize: d.getI32(pos, 4, 0),
		byName:   map[string]*Field{},
		byID:     map[uint16]*Field{},
	}

	if start, n, ok := d.getVector(pos, 1); ok {
		d.check(start, n*4)
		for i := 0; i < n; i++ {
			field := parseField(d, d.deref(start+i*4))
			obj.Fields = append(obj.Fields, field)
			obj.byName[field.Name] = field
			obj.byID[field.ID] = field
		}
	}
	sort.Slice(obj.Fields, func(i, j int) bool { return obj.Fields[i].ID < obj.Fields[j].ID })
	return obj
}

func parseField(d *decoder, pos int) *Field {
	name, _ := d.getString(pos, 0)
	field := &Field{
		Name:           name,
		ID:             d.getU16(pos, 2, 0),
		Offset:         d.getU16(pos, 3, 0),
		DefaultInteger: d.getI64(pos, 4, 0),
		DefaultReal:    d.getF64(pos, 5, 0),
		Deprecated:     d.getBool(pos, 6, false),
		Required:       d.getBool(pos, 7, false),
		Key:            d.getBool(pos, 8, false),
		Optional:       d.getBool(pos, 11, false),
	}
	if typePos, ok := d.getTable(pos, 1); ok {
		field.Type = parseType(d, typePos)
	}
	return field
}

func parseType(d *decoder, pos int) Type {
	return Type{
		Base:        BaseType(d.getU8(pos, 0, 0)),
		Element:     BaseType(d.getU8(pos, 1, 0)),
		Index:       int(d.getI32(pos, 2, -1)),
		FixedLength: d.getU16(pos, 3, 0),
		BaseSize:    d.getU32(pos, 4, 4),
		ElementSize: d.getU32(pos, 5, 0),
	}
}

func parseEnum(d *decoder, pos int) *Enum {
	name, _ := d.getString(pos, 0)
	enum := &Enum{
		Name:    name,
		IsUnion: d.getBool(pos, 2, false),
	}
	if typePos, ok := d.getTable(pos, 3); ok {
		enum.Underlying = parseType(d, typePos)
	}
	if start, n, ok := d.getVector(pos, 1); ok {
		d.check(start, n*4)
		for i := 0; i < n; i++ {
			valuePos := d.deref(start + i*4)
			valueName, _ := d.getString(valuePos, 0)
			enum.Values = append(enum.Values, EnumValue{
				Name:  valueName,
				Value: d.getI64(valuePos, 1, 0),
			})
		}
	}
	return enum
}
