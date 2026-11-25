// Package protocol implements the deterministic protocol core: .bfbs
// reflection, frame validation, field reading and mutation, and a dynamic
// FlatBuffers builder. It never runs actor code.
package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

var errMalformed = errors.New("protocol: malformed flatbuffer")

// decoder reads FlatBuffers with bounds checks. Methods panic with
// errMalformed on invalid input; exported entry points recover and return the
// error.
type decoder struct {
	b []byte
}

func (d *decoder) check(off, n int) {
	if off < 0 || n < 0 || off > len(d.b)-n {
		panic(errMalformed)
	}
}

func (d *decoder) u8(off int) uint8 {
	d.check(off, 1)
	return d.b[off]
}

func (d *decoder) u16(off int) uint16 {
	d.check(off, 2)
	return binary.LittleEndian.Uint16(d.b[off:])
}

func (d *decoder) u32(off int) uint32 {
	d.check(off, 4)
	return binary.LittleEndian.Uint32(d.b[off:])
}

func (d *decoder) i8(off int) int8 {
	return int8(d.u8(off))
}

func (d *decoder) i16(off int) int16 {
	return int16(d.u16(off))
}

func (d *decoder) i32(off int) int32 {
	d.check(off, 4)
	return int32(binary.LittleEndian.Uint32(d.b[off:]))
}

func (d *decoder) i64(off int) int64 {
	d.check(off, 8)
	return int64(binary.LittleEndian.Uint64(d.b[off:]))
}

func (d *decoder) u64(off int) uint64 {
	d.check(off, 8)
	return binary.LittleEndian.Uint64(d.b[off:])
}

func (d *decoder) f32(off int) float32 {
	d.check(off, 4)
	return math.Float32frombits(binary.LittleEndian.Uint32(d.b[off:]))
}

func (d *decoder) f64(off int) float64 {
	d.check(off, 8)
	return math.Float64frombits(binary.LittleEndian.Uint64(d.b[off:]))
}

func (d *decoder) boolean(off int) bool {
	return d.u8(off) != 0
}

// root returns the position of the root table.
func (d *decoder) root() int {
	off := int(d.u32(0))
	if off < 4 {
		panic(errMalformed)
	}
	return off
}

// tableInfo returns the vtable position and sizes for the table at pos.
func (d *decoder) tableInfo(pos int) (vtable, vtableSize, tableSize int) {
	soffset := int(d.i32(pos))
	vtable = pos - soffset
	vtableSize = int(d.u16(vtable))
	tableSize = int(d.u16(vtable + 2))
	if vtableSize < 4 || tableSize < 4 {
		panic(errMalformed)
	}
	d.check(vtable, vtableSize)
	d.check(pos, tableSize)
	return vtable, vtableSize, tableSize
}

// fieldPos returns the absolute position of field id within the table at pos.
func (d *decoder) fieldPos(tablePos int, id uint16) (int, bool) {
	vtable, vtableSize, _ := d.tableInfo(tablePos)
	slot := 4 + int(id)*2
	if slot+2 > vtableSize {
		return 0, false
	}
	offset := int(d.u16(vtable + slot))
	if offset == 0 {
		return 0, false
	}
	fieldPos := tablePos + offset
	if fieldPos < 0 || fieldPos >= len(d.b) {
		panic(errMalformed)
	}
	return fieldPos, true
}

// deref follows the uoffset stored at pos.
func (d *decoder) deref(pos int) int {
	target := pos + int(d.u32(pos))
	if target < 0 || target > len(d.b) {
		panic(errMalformed)
	}
	return target
}

func (d *decoder) stringAt(pos int) string {
	target := d.deref(pos)
	n := int(d.u32(target))
	d.check(target+4, n)
	return string(d.b[target+4 : target+4+n])
}

func (d *decoder) vectorAt(pos int) (start, n int) {
	target := d.deref(pos)
	n = int(d.u32(target))
	start = target + 4
	d.check(start, 0)
	return start, n
}

func (d *decoder) getU8(table int, id uint16, def uint8) uint8 {
	pos, ok := d.fieldPos(table, id)
	if !ok {
		return def
	}
	return d.u8(pos)
}

func (d *decoder) getU16(table int, id uint16, def uint16) uint16 {
	pos, ok := d.fieldPos(table, id)
	if !ok {
		return def
	}
	return d.u16(pos)
}

func (d *decoder) getU32(table int, id uint16, def uint32) uint32 {
	pos, ok := d.fieldPos(table, id)
	if !ok {
		return def
	}
	return d.u32(pos)
}

func (d *decoder) getI32(table int, id uint16, def int32) int32 {
	pos, ok := d.fieldPos(table, id)
	if !ok {
		return def
	}
	return d.i32(pos)
}

func (d *decoder) getI64(table int, id uint16, def int64) int64 {
	pos, ok := d.fieldPos(table, id)
	if !ok {
		return def
	}
	return d.i64(pos)
}

func (d *decoder) getF64(table int, id uint16, def float64) float64 {
	pos, ok := d.fieldPos(table, id)
	if !ok {
		return def
	}
	return d.f64(pos)
}

func (d *decoder) getBool(table int, id uint16, def bool) bool {
	pos, ok := d.fieldPos(table, id)
	if !ok {
		return def
	}
	return d.boolean(pos)
}

func (d *decoder) getString(table int, id uint16) (string, bool) {
	pos, ok := d.fieldPos(table, id)
	if !ok {
		return "", false
	}
	return d.stringAt(pos), true
}

func (d *decoder) getTable(table int, id uint16) (int, bool) {
	pos, ok := d.fieldPos(table, id)
	if !ok {
		return 0, false
	}
	return d.deref(pos), true
}

func (d *decoder) getVector(table int, id uint16) (start, n int, ok bool) {
	pos, ok := d.fieldPos(table, id)
	if !ok {
		return 0, 0, false
	}
	start, n = d.vectorAt(pos)
	return start, n, true
}

func recoverMalformed(err *error) {
	if r := recover(); r != nil {
		if e, ok := r.(error); ok {
			*err = e
			return
		}
		panic(r)
	}
}

func malformedf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errMalformed, fmt.Sprintf(format, args...))
}
