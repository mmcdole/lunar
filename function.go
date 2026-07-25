package lua

import (
	"unsafe"
)

const varargIsVararg uint8 = 2

type prototypeConstant struct {
	ref  unsafe.Pointer
	bits uint64
}

type prototypeString struct {
	text string
	hash uint64
}

type instruction uint32

type localInfo struct {
	name    string
	startPC int
	endPC   int
}

type callInfo struct {
	name string
	pc   int
}

type prototypeDebug struct {
	lines    []int
	locals   []localInfo
	calls    []callInfo
	upvalues []string
}

// Prototype is an immutable, verified Lua function prototype.
//
// A Prototype is State-neutral and may be shared among States. Its constants
// can contain only nil, booleans, numbers, and immutable strings. Executable
// arrays are never exposed for mutation.
type Prototype struct {
	sourceName  string
	lineDefined int
	lastLine    int
	parameters  uint8
	registers   uint8
	upvalues    uint8
	varargFlags uint8
	code        []instruction
	constants   []prototypeConstant
	children    []*Prototype
	debug       prototypeDebug
}

// SourceName returns the source identifier recorded by the compiler or
// loader.
func (prototype *Prototype) SourceName() string {
	if prototype == nil {
		return ""
	}
	return prototype.sourceName
}

// LineRange returns the inclusive source line range for prototype.
func (prototype *Prototype) LineRange() (first, last int) {
	if prototype == nil {
		return 0, 0
	}
	return prototype.lineDefined, prototype.lastLine
}

// ParameterCount returns the number of fixed parameters.
func (prototype *Prototype) ParameterCount() int {
	if prototype == nil {
		return 0
	}
	return int(prototype.parameters)
}

// RegisterCount returns the number of registers required by an activation.
func (prototype *Prototype) RegisterCount() int {
	if prototype == nil {
		return 0
	}
	return int(prototype.registers)
}

// UpvalueCount returns the fixed upvalue count.
func (prototype *Prototype) UpvalueCount() int {
	if prototype == nil {
		return 0
	}
	return int(prototype.upvalues)
}

// IsVararg reports whether prototype accepts variable arguments.
func (prototype *Prototype) IsVararg() bool {
	return prototype != nil && prototype.varargFlags&varargIsVararg != 0
}

// ChildCount returns the number of nested function prototypes.
func (prototype *Prototype) ChildCount() int {
	if prototype == nil {
		return 0
	}
	return len(prototype.children)
}

// Function is the canonical representation of a Lua function.
//
// Its Prototype and upvalue shape are private and fixed at construction. Lua
// 5.1 environments and upvalue contents remain mutable through controlled
// operations. Native functions join the same type when the Frame calling
// convention is introduced.
type Function struct {
	objectHeader
	prototype   *Prototype
	environment *Table
	upvalues    []*upvalue
	intrinsic   uint16
}

// Value returns the owning Lua value for function.
func (function *Function) Value() Value {
	if function == nil || function.owner == nil {
		return Value{}
	}
	return objectValue(FunctionKind, unsafe.Pointer(function))
}

// Prototype returns function's immutable Prototype.
func (function *Function) Prototype() *Prototype {
	if function == nil {
		return nil
	}
	return function.prototype
}

// UpvalueCount returns function's fixed upvalue count.
func (function *Function) UpvalueCount() int {
	if function == nil {
		return 0
	}
	return len(function.upvalues)
}

type upvalue struct {
	thread *Thread
	index  int
	closed slot
	next   *upvalue
}

func newClosedUpvalue(value slot) *upvalue {
	return &upvalue{index: -1, closed: value}
}

func (thread *Thread) captureUpvalue(index int) *upvalue {
	if thread == nil || index < 0 || index >= len(thread.values) {
		panic("lua: invalid open upvalue index")
	}
	link := &thread.openUpvalues
	for *link != nil && (*link).index > index {
		link = &(*link).next
	}
	if *link != nil && (*link).index == index {
		return *link
	}
	created := &upvalue{thread: thread, index: index, next: *link}
	*link = created
	return created
}

func (upvalue *upvalue) read() slot {
	if upvalue == nil {
		panic("lua: nil upvalue")
	}
	if upvalue.thread == nil {
		return upvalue.closed
	}
	return upvalue.thread.values[upvalue.index]
}

func (upvalue *upvalue) write(value slot) {
	if upvalue == nil {
		panic("lua: nil upvalue")
	}
	if upvalue.thread == nil {
		writeSlot(&upvalue.closed, value)
		return
	}
	writeSlot(&upvalue.thread.values[upvalue.index], value)
}

func (thread *Thread) closeUpvalues(from int) {
	if thread == nil {
		return
	}
	for thread.openUpvalues != nil && thread.openUpvalues.index >= from {
		upvalue := thread.openUpvalues
		thread.openUpvalues = upvalue.next
		upvalue.next = nil
		upvalue.closed = thread.values[upvalue.index]
		upvalue.thread = nil
		upvalue.index = -1
	}
}
