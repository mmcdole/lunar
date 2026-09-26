// Package lua implements the Lua 5.1 language and a compact, typed Go
// embedding interface.
package lua

import (
	"fmt"
	"math"
	"runtime"
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
// A nil pointer denotes a scalar: a number, nil, or a boolean. Numbers store
// their IEEE-754 bits directly, making the zero slot numeric zero. nil, false,
// and true occupy the three highest bit patterns, which are negative quiet NaNs
// with an all-ones payload.
//
// No number slot may hold those patterns, nor a NaN that reaches them by
// negation, abs, or quieting (the only payload-preserving transformations
// that arithmetic and Go's math package perform). Numbers entering from
// outside the runtime (host API, reflection, binary chunks) pass through
// canonicalNumberBits; every other NaN is produced by hardware or math.NaN
// from numbers that already satisfy the invariant.
//
// Keeping scalars pointer-free lets writeSlot and fills update only the bits
// word when the destination already holds a scalar, so those stores never
// execute a GC write barrier. Reference slots keep kind tags in their low byte
// (4..8, or 0 for dead keys), so the reserved patterns never collide with them
// and nil, boolean, and truth tests need not inspect ref.
//
// Public Value uses a real marker pointer so Value{} remains detectably
// invalid.
type slot struct {
	ref  unsafe.Pointer
	bits uint64
}

const (
	nilSlotBits   uint64 = ^uint64(0)
	falseSlotBits uint64 = ^uint64(0) - 1
	trueSlotBits  uint64 = ^uint64(0) - 2
	// Every number slot's bits are below trueSlotBits.
	firstReservedSlotBits = trueSlotBits

	canonicalNaNBits uint64 = 0x7ff8000000000000
)

// numberSlot wraps a number produced inside the runtime. Its NaNs, if any,
// already satisfy the slot invariant.
func numberSlot(value float64) slot {
	return slot{bits: math.Float64bits(value)}
}

// canonicalNumberBits returns bits for a number that may have come from
// outside the runtime. A NaN whose sign-set, quieted form would collide with a
// reserved scalar pattern collapses to canonicalNaNBits; other payloads are
// preserved.
func canonicalNumberBits(bits uint64) uint64 {
	if bits|1<<63|1<<51 >= firstReservedSlotBits {
		return canonicalNaNBits
	}
	return bits
}

// hostNumberSlot wraps a number whose NaN payload is not trusted.
func hostNumberSlot(value float64) slot {
	return slot{bits: canonicalNumberBits(math.Float64bits(value))}
}

func boolSlot(value bool) slot {
	if value {
		return slot{bits: trueSlotBits}
	}
	return slot{bits: falseSlotBits}
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
	// The interpreter loop and frame fills spell these as slot{bits: ...}
	// literals: Go loads package variables from memory, but a literal
	// becomes an immediate.
	nilSlot   = slot{bits: nilSlotBits}
	falseSlot = slot{bits: falseSlotBits}
	trueSlot  = slot{bits: trueSlotBits}
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

// String returns a State-neutral Lua string.
//
// The returned Value may be shared among States and remains valid after any
// State is closed. State.String may reuse a State's short-string cache when
// constructing strings repeatedly.
func String(text string) Value {
	return stringValue(newStringRef(text))
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
	return stringSlotText(slotFromValue(value)), true
}

// AsTable returns the canonical table and whether value is a table.
func (value Value) AsTable() (*Table, bool) {
	if value.Kind() != TableKind {
		return nil, false
	}
	token := (*hostToken)(value.ref)
	if token.kind != TableKind || token.object == nil {
		return nil, false
	}
	table := (*Table)(token)
	runtime.KeepAlive(value)
	return table, true
}

// AsFunction returns the canonical function and whether value is a function.
func (value Value) AsFunction() (*Function, bool) {
	if value.Kind() != FunctionKind {
		return nil, false
	}
	token := (*hostToken)(value.ref)
	if token.kind != FunctionKind || token.object == nil {
		return nil, false
	}
	function := (*Function)(token)
	runtime.KeepAlive(value)
	return function, true
}

// AsUserData returns the canonical userdata and whether value is userdata.
func (value Value) AsUserData() (*UserData, bool) {
	if value.Kind() != UserDataKind {
		return nil, false
	}
	token := (*hostToken)(value.ref)
	if token.kind != UserDataKind || token.object == nil {
		return nil, false
	}
	data := (*UserData)(token)
	runtime.KeepAlive(value)
	return data, true
}

// AsThread returns the canonical thread and whether value is a thread.
func (value Value) AsThread() (*Thread, bool) {
	if value.Kind() != ThreadKind {
		return nil, false
	}
	token := (*hostToken)(value.ref)
	if token.kind != ThreadKind || token.object == nil {
		return nil, false
	}
	thread := (*Thread)(token)
	runtime.KeepAlive(value)
	return thread, true
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
	return value.objectIdentity() == other.objectIdentity(), true
}

// String returns a stable diagnostic representation without executing Lua.
// Numbers use Lua 5.1's `%.14g`-style spelling, which is not a lossless
// serialization format.
func (value Value) String() string {
	if !value.Valid() {
		return "<invalid>"
	}
	return slotFromValue(value).diagnosticString()
}

func (value slot) diagnosticString() string {
	switch value.kind() {
	case NilKind:
		return "nil"
	case BoolKind:
		return strconv.FormatBool(value.bits == trueSlotBits)
	case NumberKind:
		var buffer [32]byte
		return string(appendLuaNumber(
			buffer[:0],
			math.Float64frombits(value.bits),
		))
	case StringKind:
		return stringSlotText(value)
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

func objectSlot(kind Kind, pointer unsafe.Pointer) slot {
	if !kind.isReference() || pointer == nil {
		panic("lua: invalid canonical object")
	}
	return slot{ref: pointer, bits: uint64(kind)}
}

func referenceSlotHeader(value slot) *objectHeader {
	switch value.kind() {
	case FunctionKind:
		return &(*functionObject)(value.ref).objectHeader
	case UserDataKind:
		return &(*userDataObject)(value.ref).objectHeader
	case ThreadKind:
		return &(*threadObject)(value.ref).objectHeader
	case TableKind:
		return &(*tableObject)(value.ref).objectHeader
	default:
		panic("lua: slot is not a reference object")
	}
}

func slotFromUserDataObject(data *userDataObject) slot {
	if data == nil || data.owner == nil {
		panic("lua: invalid canonical userdata")
	}
	return objectSlot(UserDataKind, unsafe.Pointer(data))
}

func (value Value) objectIdentity() unsafe.Pointer {
	switch value.Kind() {
	case FunctionKind, UserDataKind, ThreadKind, TableKind:
		token := (*hostToken)(value.ref)
		if token.kind != value.Kind() {
			return nil
		}
		return token.object
	default:
		return value.ref
	}
}

func stringValue(value stringRef) Value {
	return stringSlot(value).owningValue()
}

func stringSlot(value stringRef) slot {
	if !value.valid() {
		panic("lua: invalid string reference")
	}
	return slot(value)
}

func (value Value) owner() *runtimeState {
	switch value.Kind() {
	case FunctionKind:
		return (*hostToken)(value.ref).owner
	case UserDataKind:
		return (*hostToken)(value.ref).owner
	case ThreadKind:
		return (*hostToken)(value.ref).owner
	case TableKind:
		return (*hostToken)(value.ref).owner
	default:
		return nil
	}
}

func slotFromValue(value Value) slot {
	if !value.Valid() {
		panic("lua: invalid Value at compact-value seam")
	}
	switch value.ref {
	case numberMarkerPointer:
		return slot{bits: canonicalNumberBits(value.bits)}
	case nilMarkerPointer:
		return nilSlot
	case falseMarkerPointer:
		return falseSlot
	case trueMarkerPointer:
		return trueSlot
	}
	switch value.Kind() {
	case FunctionKind:
		token := (*hostToken)(value.ref)
		if token.kind != FunctionKind || token.object == nil {
			panic("lua: invalid reference host token")
		}
		result := slotFromFunctionObject((*functionObject)(token.object))
		runtime.KeepAlive(value)
		return result
	case UserDataKind, ThreadKind, TableKind:
		token := (*hostToken)(value.ref)
		if token.kind != value.Kind() || token.object == nil {
			panic("lua: invalid reference host token")
		}
		result := objectSlot(token.kind, token.object)
		runtime.KeepAlive(value)
		return result
	default:
		return slot{ref: value.ref, bits: value.bits}
	}
}

func (value slot) owningValue() Value {
	if value.ref == nil {
		switch value.bits {
		case nilSlotBits:
			return nilValue
		case falseSlotBits:
			return falseValue
		case trueSlotBits:
			return trueValue
		}
		return Value{ref: numberMarkerPointer, bits: value.bits}
	}
	if value.isUserData() {
		return userDataObjectFromSlot(value).owningValue()
	}
	if value.isTable() {
		return tableObjectFromSlot(value).owningValue()
	}
	if value.isFunction() {
		return functionObjectFromSlot(value).owningValue()
	}
	if value.isThread() {
		return threadObjectFromSlot(value).owningValue()
	}
	return Value{ref: value.ref, bits: value.bits}
}

func (value slot) kind() Kind {
	if value.ref == nil {
		switch {
		case value.bits < firstReservedSlotBits:
			return NumberKind
		case value.bits == nilSlotBits:
			return NilKind
		default:
			return BoolKind
		}
	}
	kind := Kind(value.bits & 0xff)
	if kind < StringKind || kind > TableKind {
		return InvalidKind
	}
	return kind
}

// Typed predicates are the compact counterpart to Lua's ttis* tests. Slots
// inside the runtime are already verified values, so a caller asking one
// specific type question need not decode the complete Kind.
//
// The reserved scalar patterns never occur in reference slots, so nil,
// boolean, and truth tests read only the bits word.
func (value slot) isNil() bool {
	return value.bits == nilSlotBits
}

func (value slot) isBool() bool {
	return value.bits-trueSlotBits <= falseSlotBits-trueSlotBits
}

func (value slot) isTrue() bool {
	return value.bits == trueSlotBits
}

// truth reports Lua truthiness: nil and false are the two highest patterns.
func (value slot) truth() bool {
	return value.bits < falseSlotBits
}

func (value slot) isNumber() bool {
	return value.ref == nil && value.bits < firstReservedSlotBits
}

// bothNumbers is the binary-operator fast-path test. It may report false for
// two numbers: the OR of two number patterns can reach the reserved range
// (for example -Inf with a large subnormal, or two negative NaNs whose
// payloads together fill the low bits). Callers must therefore send a false
// result to a slow path that still accepts numbers. In exchange the float
// case costs the same two non-destructive pointer tests as a pointer-only
// check plus a single compare-and-branch on the ORed bits.
func bothNumbers(left, right slot) bool {
	return left.ref == nil && right.ref == nil &&
		left.bits|right.bits < firstReservedSlotBits
}

func (value slot) isString() bool {
	return value.ref != nil && Kind(value.bits&0xff) == StringKind
}

func (value slot) isFunction() bool {
	return value.ref != nil && Kind(value.bits&0xff) == FunctionKind
}

func (value slot) isUserData() bool {
	return value.ref != nil && Kind(value.bits&0xff) == UserDataKind
}

func (value slot) isThread() bool {
	return value.ref != nil && Kind(value.bits&0xff) == ThreadKind
}

func (value slot) isTable() bool {
	return value.ref != nil && Kind(value.bits&0xff) == TableKind
}

func (value slot) owner() *runtimeState {
	switch value.kind() {
	case FunctionKind:
		return (*functionObject)(value.ref).owner
	case UserDataKind:
		return (*userDataObject)(value.ref).owner
	case ThreadKind:
		return (*threadObject)(value.ref).owner
	case TableKind:
		return (*tableObject)(value.ref).owner
	default:
		return nil
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
		leftSlot := slotFromValue(left)
		rightSlot := slotFromValue(right)
		return stringSlotsEqual(leftSlot, rightSlot)
	default:
		return left.objectIdentity() == right.objectIdentity()
	}
}

func rawSlotEqual(left, right slot) bool {
	if left.ref == nil || right.ref == nil {
		if left.ref != nil || right.ref != nil {
			return false
		}
		if left.bits >= firstReservedSlotBits ||
			right.bits >= firstReservedSlotBits {
			return left.bits == right.bits
		}
		return math.Float64frombits(left.bits) ==
			math.Float64frombits(right.bits)
	}
	if left.ref == right.ref && left.bits == right.bits {
		return true
	}
	kind := left.kind()
	if kind != right.kind() {
		return false
	}
	switch kind {
	case StringKind:
		return stringSlotsEqual(left, right)
	case FunctionKind, UserDataKind, ThreadKind, TableKind:
		return left.ref == right.ref
	default:
		return false
	}
}
