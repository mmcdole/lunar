package lua

import "unsafe"

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
}

func newLuaFunction(
	owner *runtimeState,
	prototype *Prototype,
	environment *Table,
	upvalues []*upvalue,
) *Function {
	return newLuaFunctionOwned(
		owner,
		prototype,
		environment,
		exactSlice(upvalues),
	)
}

func newLuaFunctionOwned(
	owner *runtimeState,
	prototype *Prototype,
	environment *Table,
	upvalues []*upvalue,
) *Function {
	if owner == nil || prototype == nil || !prototype.sealed {
		panic("lua: invalid Lua function")
	}
	if environment == nil || environment.owner != owner {
		panic("lua: invalid Lua function environment")
	}
	if len(upvalues) != int(prototype.upvalues) {
		panic("lua: Lua function upvalue count does not match its prototype")
	}
	for _, value := range upvalues {
		if value == nil {
			panic("lua: Lua function has a nil upvalue")
		}
	}
	return &Function{
		objectHeader: objectHeader{owner: owner},
		prototype:    prototype,
		environment:  environment,
		upvalues:     upvalues,
	}
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
