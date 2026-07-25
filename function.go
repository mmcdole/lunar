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
		if value == nil || value.cell == nil {
			panic("lua: Lua function has an invalid upvalue")
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

func slotFromFunction(function *Function) slot {
	return objectSlot(FunctionKind, unsafe.Pointer(function))
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

// An upvalue's cell points into a Thread value stack while open. storage.bits
// holds the absolute stack index needed for ordering and relocation. Once
// closed, cell points at storage, which holds the Lua value.
type upvalue struct {
	cell    *slot
	next    *upvalue
	storage slot
}

func newClosedUpvalue(value slot) *upvalue {
	created := &upvalue{storage: value}
	created.cell = &created.storage
	return created
}

func (thread *Thread) captureUpvalue(index int) *upvalue {
	if thread == nil || index < 0 || index >= len(thread.values) {
		panic("lua: invalid open upvalue index")
	}
	link := &thread.openUpvalues
	for *link != nil && (*link).stackIndex() > index {
		link = &(*link).next
	}
	if *link != nil && (*link).stackIndex() == index {
		return *link
	}
	created := &upvalue{
		next:    *link,
		storage: slot{bits: uint64(index)},
	}
	created.cell = &thread.values[index]
	*link = created
	return created
}

func (upvalue *upvalue) read() slot {
	return *upvalue.cell
}

func (upvalue *upvalue) write(value slot) {
	writeSlot(upvalue.cell, value)
}

func (upvalue *upvalue) stackIndex() int {
	return int(upvalue.storage.bits)
}

func (thread *Thread) closeUpvalues(from int) {
	if thread == nil {
		return
	}
	for thread.openUpvalues != nil &&
		thread.openUpvalues.stackIndex() >= from {
		upvalue := thread.openUpvalues
		thread.openUpvalues = upvalue.next
		writeSlot(&upvalue.storage, *upvalue.cell)
		upvalue.cell = &upvalue.storage
		upvalue.next = nil
	}
}
