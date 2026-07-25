// Package lua implements the Lua 5.1 language and a compact, typed Go
// embedding interface.
package lua

import (
	"fmt"
	"math"
	"strconv"
	"unsafe"
)

// Kind identifies the Lua type held by a Value.
type Kind uint8

const (
	// InvalidKind identifies the zero Value, which is not a Lua value.
	InvalidKind Kind = iota
	// NilKind identifies Lua nil.
	NilKind
	// BoolKind identifies a Lua boolean.
	BoolKind
	// NumberKind identifies a Lua number.
	NumberKind
	// StringKind identifies a Lua string.
	StringKind
	// FunctionKind identifies a Lua or native function.
	FunctionKind
	// UserDataKind identifies full userdata.
	UserDataKind
	// ThreadKind identifies a Lua thread.
	ThreadKind
	// TableKind identifies a Lua table.
	TableKind
)

var kindNames = [...]string{
	InvalidKind:  "invalid",
	NilKind:      "nil",
	BoolKind:     "boolean",
	NumberKind:   "number",
	StringKind:   "string",
	FunctionKind: "function",
	UserDataKind: "userdata",
	ThreadKind:   "thread",
	TableKind:    "table",
}

// String returns the Lua type name for kind.
func (kind Kind) String() string {
	if int(kind) >= len(kindNames) {
		return "invalid"
	}
	return kindNames[kind]
}

// Value is an owning Lua value.
//
// Its fields are private so executable state cannot be mutated through Go.
// Copying a Value is cheap, does not allocate, and keeps referenced Go memory
// visible to the garbage collector. The zero Value is invalid; use Nil() for
// Lua nil.
//
// Value is deliberately not comparable. Use State.RawEqual for Lua raw
// equality or SameObject when reference identity is specifically required.
type Value struct {
	_    [0]func()
	ref  unsafe.Pointer
	bits uint64
}

// slot is the private representation used by registers, tables, and upvalues.
// A nil pointer denotes a number, making the zero slot numeric zero. Public
// Value uses a real marker pointer so Value{} remains detectably invalid.
type slot struct {
	ref  unsafe.Pointer
	bits uint64
}

type scalarMarker struct {
	id byte
}

var (
	nilMarker    = scalarMarker{id: 1}
	falseMarker  = scalarMarker{id: 2}
	trueMarker   = scalarMarker{id: 3}
	numberMarker = scalarMarker{id: 4}

	nilMarkerPointer    = unsafe.Pointer(&nilMarker)
	falseMarkerPointer  = unsafe.Pointer(&falseMarker)
	trueMarkerPointer   = unsafe.Pointer(&trueMarker)
	numberMarkerPointer = unsafe.Pointer(&numberMarker)

	nilValue   = Value{ref: nilMarkerPointer, bits: uint64(NilKind)}
	falseValue = Value{ref: falseMarkerPointer, bits: uint64(BoolKind)}
	trueValue  = Value{ref: trueMarkerPointer, bits: uint64(BoolKind)}
	nilSlot    = slot{ref: nilMarkerPointer, bits: uint64(NilKind)}
)

// Nil returns the Lua nil value.
func Nil() Value {
	return nilValue
}

// Bool returns the Lua boolean corresponding to value.
func Bool(value bool) Value {
	if value {
		return trueValue
	}
	return falseValue
}

// Number returns a Lua number.
func Number(value float64) Value {
	return Value{ref: numberMarkerPointer, bits: math.Float64bits(value)}
}

// Valid reports whether value contains a Lua value.
func (value Value) Valid() bool {
	return value.ref != nil
}

// Kind returns the Lua type held by value. It returns InvalidKind for the zero
// Value.
func (value Value) Kind() Kind {
	switch value.ref {
	case nil:
		return InvalidKind
	case nilMarkerPointer:
		return NilKind
	case falseMarkerPointer, trueMarkerPointer:
		return BoolKind
	case numberMarkerPointer:
		return NumberKind
	default:
		kind := Kind(value.bits & 0xff)
		if kind < StringKind || kind > TableKind {
			return InvalidKind
		}
		return kind
	}
}

// IsNil reports whether value is Lua nil.
func (value Value) IsNil() bool {
	return value.ref == nilMarkerPointer
}

// Truth reports Lua truthiness. Only nil and false are false.
func (value Value) Truth() bool {
	if !value.Valid() {
		return false
	}
	return value.ref != nilMarkerPointer && value.ref != falseMarkerPointer
}

// AsBool returns the contained boolean and whether value is a boolean.
func (value Value) AsBool() (bool, bool) {
	switch value.ref {
	case falseMarkerPointer:
		return false, true
	case trueMarkerPointer:
		return true, true
	default:
		return false, false
	}
}

// AsNumber returns the contained number and whether value is a number.
func (value Value) AsNumber() (float64, bool) {
	if value.ref != numberMarkerPointer {
		return 0, false
	}
	return math.Float64frombits(value.bits), true
}

// AsString returns the contained string and whether value is a string.
func (value Value) AsString() (string, bool) {
	if value.Kind() != StringKind {
		return "", false
	}
	return (*luaString)(value.ref).text, true
}

// Table returns the canonical table and whether value is a table.
func (value Value) Table() (*Table, bool) {
	if value.Kind() != TableKind {
		return nil, false
	}
	return (*Table)(value.ref), true
}

// Function returns the canonical function and whether value is a function.
func (value Value) Function() (*Function, bool) {
	if value.Kind() != FunctionKind {
		return nil, false
	}
	return (*Function)(value.ref), true
}

// UserData returns the canonical userdata and whether value is userdata.
func (value Value) UserData() (*UserData, bool) {
	if value.Kind() != UserDataKind {
		return nil, false
	}
	return (*UserData)(value.ref), true
}

// Thread returns the canonical thread and whether value is a thread.
func (value Value) Thread() (*Thread, bool) {
	if value.Kind() != ThreadKind {
		return nil, false
	}
	return (*Thread)(value.ref), true
}

// SameObject reports reference identity.
//
// applicable is true only for tables, functions, userdata, and threads.
// Strings compare by contents under Lua semantics and therefore are not
// reference objects for this operation.
func (value Value) SameObject(other Value) (same, applicable bool) {
	kind := value.Kind()
	if kind != other.Kind() || !kind.isReference() {
		return false, false
	}
	return value.ref == other.ref, true
}

// String returns a stable diagnostic representation without executing Lua.
func (value Value) String() string {
	switch value.Kind() {
	case InvalidKind:
		return "<invalid>"
	case NilKind:
		return "nil"
	case BoolKind:
		boolean, _ := value.AsBool()
		return strconv.FormatBool(boolean)
	case NumberKind:
		number, _ := value.AsNumber()
		return strconv.FormatFloat(number, 'g', -1, 64)
	case StringKind:
		text, _ := value.AsString()
		return text
	case FunctionKind:
		return fmt.Sprintf("function: %p", value.ref)
	case UserDataKind:
		return fmt.Sprintf("userdata: %p", value.ref)
	case ThreadKind:
		return fmt.Sprintf("thread: %p", value.ref)
	case TableKind:
		return fmt.Sprintf("table: %p", value.ref)
	default:
		return "<invalid>"
	}
}

func (kind Kind) isReference() bool {
	switch kind {
	case FunctionKind, UserDataKind, ThreadKind, TableKind:
		return true
	default:
		return false
	}
}

func objectValue(kind Kind, pointer unsafe.Pointer) Value {
	if !kind.isReference() || pointer == nil {
		panic("lua: invalid canonical object")
	}
	return Value{ref: pointer, bits: uint64(kind)}
}

func stringValue(pointer *luaString) Value {
	if pointer == nil {
		panic("lua: nil string object")
	}
	return Value{ref: unsafe.Pointer(pointer), bits: uint64(StringKind)}
}

func (value Value) owner() *runtimeState {
	switch value.Kind() {
	case FunctionKind:
		return (*Function)(value.ref).owner
	case UserDataKind:
		return (*UserData)(value.ref).owner
	case ThreadKind:
		return (*Thread)(value.ref).owner
	case TableKind:
		return (*Table)(value.ref).owner
	default:
		return nil
	}
}

func slotFromValue(value Value) slot {
	if !value.Valid() {
		panic("lua: invalid Value at compact-value seam")
	}
	if value.ref == numberMarkerPointer {
		return slot{bits: value.bits}
	}
	return slot{ref: value.ref, bits: value.bits}
}

func (value slot) owningValue() Value {
	if value.ref == nil {
		return Value{ref: numberMarkerPointer, bits: value.bits}
	}
	return Value{ref: value.ref, bits: value.bits}
}

func (value slot) kind() Kind {
	switch value.ref {
	case nil:
		return NumberKind
	case nilMarkerPointer:
		return NilKind
	case falseMarkerPointer, trueMarkerPointer:
		return BoolKind
	default:
		kind := Kind(value.bits & 0xff)
		if kind < StringKind || kind > TableKind {
			return InvalidKind
		}
		return kind
	}
}

func rawEqual(left, right Value) bool {
	kind := left.Kind()
	if kind != right.Kind() {
		return false
	}
	switch kind {
	case InvalidKind:
		return false
	case NilKind:
		return true
	case BoolKind:
		return left.ref == right.ref
	case NumberKind:
		leftNumber, _ := left.AsNumber()
		rightNumber, _ := right.AsNumber()
		return leftNumber == rightNumber
	case StringKind:
		leftString := (*luaString)(left.ref)
		rightString := (*luaString)(right.ref)
		return leftString == rightString ||
			leftString.hash == rightString.hash && leftString.text == rightString.text
	default:
		return left.ref == right.ref
	}
}

func rawSlotEqual(left, right slot) bool {
	if left.ref == right.ref && left.bits == right.bits {
		return true
	}
	kind := left.kind()
	if kind != right.kind() {
		return false
	}
	switch kind {
	case NumberKind:
		return math.Float64frombits(left.bits) == math.Float64frombits(right.bits)
	case StringKind:
		leftString := (*luaString)(left.ref)
		rightString := (*luaString)(right.ref)
		return leftString.hash == rightString.hash && leftString.text == rightString.text
	case NilKind, BoolKind, FunctionKind, UserDataKind, ThreadKind, TableKind:
		return left.ref == right.ref
	default:
		return false
	}
}
