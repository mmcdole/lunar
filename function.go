package lua

import (
	"runtime"
	"unsafe"
)

const nativeFunctionSlotFlag = uint64(1) << 8

// Function is an opaque owning handle for a Lua or native function.
//
// Repeated publication of the same live Lua function returns the same handle
// pointer. Execution slots retain the compact function directly and do not
// pass through this handle.
//
// Function must not be copied after first use. Retain and pass its pointer.
type Function hostToken

type functionObject struct {
	objectHeader
	prototype   *Prototype
	environment *tableObject
	body        unsafe.Pointer
	gcMark      objectMark
}

type nativeFunctionData struct {
	entry    NativeFunc
	captures []slot
}

// nativeFunctionAllocation keeps the compact function and its native-only
// data in one allocation. functionObject.body points at data directly;
// nativeBody never derives the larger allocation from a function pointer.
type nativeFunctionAllocation struct {
	functionObject
	data nativeFunctionData
}

func newLuaFunction(
	state *State,
	prototype *Prototype,
	environment *tableObject,
	upvalues []*upvalue,
) *functionObject {
	return newLuaFunctionOwned(
		state,
		prototype,
		environment,
		exactSlice(upvalues),
	)
}

func newLuaFunctionOwned(
	state *State,
	prototype *Prototype,
	environment *tableObject,
	upvalues []*upvalue,
) *functionObject {
	if state == nil ||
		state.runtime == nil ||
		prototype == nil ||
		!prototype.isSealed() {
		panic("lua: invalid Lua function")
	}
	if environment == nil || environment.owner != state.runtime {
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
	if len(upvalues) != cap(upvalues) {
		upvalues = exactSlice(upvalues)
	}
	created := &functionObject{
		prototype:   prototype,
		environment: environment,
	}
	if len(upvalues) != 0 {
		created.body = unsafe.Pointer(unsafe.SliceData(upvalues))
	}
	state.registerFunction(created)
	return created
}

func newNativeFunctionOwned(
	state *State,
	environment *tableObject,
	entry NativeFunc,
	captures []slot,
) *functionObject {
	if state == nil || state.runtime == nil || entry == nil {
		panic("lua: invalid native function")
	}
	if len(captures) > maxNativeCaptures {
		panic("lua: native function capture limit exceeded")
	}
	if environment == nil || environment.owner != state.runtime {
		panic("lua: invalid native function environment")
	}
	for _, capture := range captures {
		if err := state.runtime.acceptSlot(capture); err != nil {
			panic("lua: invalid native function capture")
		}
	}
	if len(captures) != cap(captures) {
		captures = exactSlice(captures)
	}
	allocation := &nativeFunctionAllocation{
		functionObject: functionObject{
			environment: environment,
		},
		data: nativeFunctionData{
			entry:    entry,
			captures: captures,
		},
	}
	allocation.functionObject.body = unsafe.Pointer(&allocation.data)
	state.registerFunction(&allocation.functionObject)
	return &allocation.functionObject
}

// Value returns the owning Lua value for function.
func (function *Function) Value() Value {
	token := function.token()
	if token == nil ||
		token.owner == nil ||
		token.object == nil ||
		token.kind != FunctionKind {
		return Value{}
	}
	value := Value{ref: unsafe.Pointer(token), bits: uint64(FunctionKind)}
	runtime.KeepAlive(function)
	return value
}

func (function *Function) token() *hostToken {
	return (*hostToken)(function)
}

func (function *Function) runtimeObject() *functionObject {
	token := function.token()
	if token == nil ||
		token.kind != FunctionKind ||
		token.object == nil {
		return nil
	}
	return (*functionObject)(token.object)
}

func (function *functionObject) owningHandle() *Function {
	if function == nil {
		return nil
	}
	token := function.objectHeader.owningToken(
		FunctionKind,
		unsafe.Pointer(function),
	)
	return (*Function)(token)
}

func (function *functionObject) owningValue() Value {
	handle := function.owningHandle()
	return handle.Value()
}

func slotFromFunctionObject(function *functionObject) slot {
	if function == nil || function.owner == nil {
		panic("lua: invalid canonical function")
	}
	return canonicalFunctionSlot(function)
}

func canonicalFunctionSlot(function *functionObject) slot {
	bits := uint64(FunctionKind)
	if function.prototype == nil {
		bits |= nativeFunctionSlotFlag
	}
	return slot{
		ref:  unsafe.Pointer(function),
		bits: bits,
	}
}

func functionObjectFromSlot(value slot) *functionObject {
	if !value.isFunction() {
		panic("lua: slot is not a function")
	}
	return (*functionObject)(value.ref)
}

func functionHandleFromSlot(value slot) *Function {
	return functionObjectFromSlot(value).owningHandle()
}

// Prototype returns function's immutable Prototype.
func (function *Function) Prototype() *Prototype {
	object := function.runtimeObject()
	if object == nil {
		return nil
	}
	prototype := object.prototype
	runtime.KeepAlive(function)
	return prototype
}

// UpvalueCount returns the fixed Lua upvalue or native capture count.
func (function *Function) UpvalueCount() int {
	object := function.runtimeObject()
	if object == nil {
		return 0
	}
	var count int
	if object.prototype != nil {
		count = int(object.prototype.upvalues)
	} else if native := object.nativeBody(); native != nil {
		count = len(native.captures)
	}
	runtime.KeepAlive(function)
	return count
}

func (function *functionObject) nativeBody() *nativeFunctionData {
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
func (function *functionObject) nativeBodyUnchecked() *nativeFunctionData {
	return (*nativeFunctionData)(function.body)
}

func (function *functionObject) luaUpvalueUnchecked(index int) *upvalue {
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

func (thread *threadObject) captureUpvalue(index int) *upvalue {
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
	thread.owner.collection.charge(upvalueCellsRetainedBytes(1))
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

func (thread *threadObject) closeUpvalues(from int) {
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
