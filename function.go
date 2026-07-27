package lua

import "unsafe"

const nativeFunctionSlotFlag = uint64(1) << 8

// Function is the canonical representation of a Lua or native function.
//
// Its executable kind and capture shape are private and fixed at
// construction. Lua 5.1 environments and captured values remain mutable
// through controlled operations.
//
// A Function must not be copied after first use. Retain and pass its pointer.
type Function struct {
	objectHeader
	prototype   *Prototype
	environment *tableObject
	body        unsafe.Pointer
}

type nativeFunctionData struct {
	entry    NativeFunc
	captures []slot
}

// nativeFunctionAllocation keeps the public Function and its native-only data
// in one allocation. Function.body points at data directly; nativeBody never
// derives the larger allocation from a Function pointer.
type nativeFunctionAllocation struct {
	Function
	data nativeFunctionData
}

func newLuaFunction(
	owner *runtimeState,
	prototype *Prototype,
	environment *tableObject,
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
	environment *tableObject,
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
	created := &Function{
		objectHeader: objectHeader{owner: owner},
		prototype:    prototype,
		environment:  environment,
	}
	if len(upvalues) != 0 {
		created.body = unsafe.Pointer(unsafe.SliceData(upvalues))
	}
	return created
}

func newNativeFunctionOwned(
	owner *runtimeState,
	environment *tableObject,
	entry NativeFunc,
	captures []slot,
) *Function {
	if owner == nil || entry == nil {
		panic("lua: invalid native function")
	}
	if len(captures) > maxNativeCaptures {
		panic("lua: native function capture limit exceeded")
	}
	if environment == nil || environment.owner != owner {
		panic("lua: invalid native function environment")
	}
	for _, capture := range captures {
		if err := owner.acceptSlot(capture); err != nil {
			panic("lua: invalid native function capture")
		}
	}
	allocation := &nativeFunctionAllocation{
		Function: Function{
			objectHeader: objectHeader{owner: owner},
			environment:  environment,
		},
		data: nativeFunctionData{
			entry:    entry,
			captures: captures,
		},
	}
	allocation.Function.body = unsafe.Pointer(&allocation.data)
	return &allocation.Function
}

// Value returns the owning Lua value for function.
func (function *Function) Value() Value {
	if function == nil || function.owner == nil {
		return Value{}
	}
	return canonicalFunctionSlot(function).owningValue()
}

func slotFromFunction(function *Function) slot {
	if function == nil || function.owner == nil {
		panic("lua: invalid canonical function")
	}
	return canonicalFunctionSlot(function)
}

func canonicalFunctionSlot(function *Function) slot {
	bits := uint64(FunctionKind)
	if function.prototype == nil {
		bits |= nativeFunctionSlotFlag
	}
	return slot{
		ref:  unsafe.Pointer(function),
		bits: bits,
	}
}

// Prototype returns function's immutable Prototype.
func (function *Function) Prototype() *Prototype {
	if function == nil {
		return nil
	}
	return function.prototype
}

// UpvalueCount returns the fixed Lua upvalue or native capture count.
func (function *Function) UpvalueCount() int {
	if function == nil {
		return 0
	}
	if function.prototype != nil {
		return int(function.prototype.upvalues)
	}
	if native := function.nativeBody(); native != nil {
		return len(native.captures)
	}
	return 0
}

func (function *Function) nativeBody() *nativeFunctionData {
	if function == nil ||
		function.owner == nil ||
		function.prototype != nil ||
		function.body == nil {
		return nil
	}
	return function.nativeBodyUnchecked()
}

// nativeBodyUnchecked returns the body of a canonical native Function.
// Executor and Frame seams establish both invariants before using it.
func (function *Function) nativeBodyUnchecked() *nativeFunctionData {
	return (*nativeFunctionData)(function.body)
}

func (function *Function) luaUpvalueUnchecked(index int) *upvalue {
	address := unsafe.Add(
		function.body,
		uintptr(index)*unsafe.Sizeof((*upvalue)(nil)),
	)
	return *(**upvalue)(address)
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
