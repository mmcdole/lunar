package lua

import (
	"context"
	"math"
)

type luaOperation uint8

const (
	luaIndexOperation luaOperation = iota
	luaSetIndexOperation
	luaToStringOperation
	luaLengthOperation
	luaEqualOperation
	luaLessThanOperation
	luaLessEqualOperation
	luaOperationCount
)

var luaOperationEntries = [...]NativeFunc{
	luaIndexOperation:     luaIndexOperationEntry,
	luaSetIndexOperation:  luaSetIndexOperationEntry,
	luaToStringOperation:  luaToStringOperationEntry,
	luaLengthOperation:    luaLengthOperationEntry,
	luaEqualOperation:     luaEqualOperationEntry,
	luaLessThanOperation:  luaLessThanOperationEntry,
	luaLessEqualOperation: luaLessEqualOperationEntry,
}

func luaIndexOperationEntry(frame Frame) Outcome {
	target, targetPresent := frame.argument(0)
	key, keyPresent := frame.argument(1)
	if !targetPresent || !keyPresent {
		panic("lua: invalid Index operation invocation")
	}
	result, failure := frame.indexCompact(target, key)
	if failure != nil {
		return frame.sealError(failure)
	}
	return frame.returnOne(frame.activation(), result)
}

func luaSetIndexOperationEntry(frame Frame) Outcome {
	target, targetPresent := frame.argument(0)
	key, keyPresent := frame.argument(1)
	value, valuePresent := frame.argument(2)
	if !targetPresent || !keyPresent || !valuePresent {
		panic("lua: invalid SetIndex operation invocation")
	}
	if failure := frame.setIndexCompact(target, key, value); failure != nil {
		return frame.sealError(failure)
	}
	return frame.Return()
}

func luaToStringOperationEntry(frame Frame) Outcome {
	value, present := frame.argument(0)
	if !present {
		panic("lua: invalid ToString operation invocation")
	}
	result, failure := frame.toStringSlot(value, false)
	if failure != nil {
		return frame.sealError(failure)
	}
	return frame.returnOne(frame.activation(), result)
}

func luaLengthOperationEntry(frame Frame) Outcome {
	value, present := frame.argument(0)
	if !present {
		panic("lua: invalid Len operation invocation")
	}
	result, failure := frame.lengthSlot(value, false)
	if failure != nil {
		return frame.sealError(failure)
	}
	return frame.returnOne(frame.activation(), result)
}

func luaEqualOperationEntry(frame Frame) Outcome {
	left, leftPresent := frame.argument(0)
	right, rightPresent := frame.argument(1)
	if !leftPresent || !rightPresent {
		panic("lua: invalid Equal operation invocation")
	}
	result, failure := frame.equalSlots(left, right, false)
	if failure != nil {
		return frame.sealError(failure)
	}
	return frame.ReturnBool(result)
}

func luaLessThanOperationEntry(frame Frame) Outcome {
	left, leftPresent := frame.argument(0)
	right, rightPresent := frame.argument(1)
	if !leftPresent || !rightPresent {
		panic("lua: invalid LessThan operation invocation")
	}
	result, failure := frame.orderSlots(
		left,
		right,
		luaLessThanOperation,
		false,
	)
	if failure != nil {
		return frame.sealError(failure)
	}
	return frame.ReturnBool(result)
}

func luaLessEqualOperationEntry(frame Frame) Outcome {
	left, leftPresent := frame.argument(0)
	right, rightPresent := frame.argument(1)
	if !leftPresent || !rightPresent {
		panic("lua: invalid LessEqual operation invocation")
	}
	result, failure := frame.orderSlots(
		left,
		right,
		luaLessEqualOperation,
		false,
	)
	if failure != nil {
		return frame.sealError(failure)
	}
	return frame.ReturnBool(result)
}

func (state *State) runLuaOperation(
	ctx context.Context,
	operation luaOperation,
	arguments []Value,
	destination []Value,
) (int, error) {
	if err := state.checkOpen(); err != nil {
		return 0, err
	}
	if operation >= luaOperationCount {
		panic("lua: invalid Lua operation")
	}
	if _, err := state.prepareReadyMainThread(); err != nil {
		return 0, err
	}
	function := state.operationBridges[operation]
	if function == nil {
		function = newNativeFunctionOwned(
			state,
			state.main.globals,
			luaOperationEntries[operation],
			nil,
		)
		state.operationBridges[operation] = function
	}
	_, count, err := state.callMain(
		ctx,
		function.owningValue(),
		arguments,
		destination,
		false,
		allResults,
	)
	return count, err
}

func (state *State) luaOperationValue(
	ctx context.Context,
	operation luaOperation,
	arguments []Value,
) (Value, error) {
	var destination [1]Value
	count, err := state.runLuaOperation(
		ctx,
		operation,
		arguments,
		destination[:],
	)
	if err != nil {
		return Value{}, err
	}
	if count != 1 {
		panic("lua: Lua value operation returned the wrong result count")
	}
	return destination[0], nil
}

func (state *State) luaOperationBool(
	ctx context.Context,
	operation luaOperation,
	arguments []Value,
) (bool, error) {
	result, err := state.luaOperationValue(ctx, operation, arguments)
	if err != nil {
		return false, err
	}
	value, ok := result.AsBool()
	if !ok {
		panic("lua: Lua boolean operation returned a non-boolean")
	}
	return value, nil
}

// Index applies ordinary Lua indexing on the main Thread.
//
// A raw table hit returns directly. Otherwise Index follows __index and may
// execute Lua. Use Frame.Index from a native callback.
func (state *State) Index(target, key Value) (Value, error) {
	arguments := [2]Value{target, key}
	return state.luaOperationValue(nil, luaIndexOperation, arguments[:])
}

// IndexContext applies Index while making ctx available to executing Lua.
func (state *State) IndexContext(
	ctx context.Context,
	target, key Value,
) (Value, error) {
	if ctx == nil {
		return Value{}, ErrNilContext
	}
	arguments := [2]Value{target, key}
	return state.luaOperationValue(ctx, luaIndexOperation, arguments[:])
}

// SetIndex applies an ordinary Lua assignment on the main Thread.
//
// An existing table field is replaced directly. Otherwise SetIndex follows
// __newindex and may execute Lua. Use Frame.SetIndex from a native callback.
func (state *State) SetIndex(target, key, value Value) error {
	arguments := [3]Value{target, key, value}
	count, err := state.runLuaOperation(
		nil,
		luaSetIndexOperation,
		arguments[:],
		nil,
	)
	if err == nil && count != 0 {
		panic("lua: SetIndex operation returned a result")
	}
	return err
}

// SetIndexContext applies SetIndex while making ctx available to executing
// Lua.
func (state *State) SetIndexContext(
	ctx context.Context,
	target, key, value Value,
) error {
	if ctx == nil {
		return ErrNilContext
	}
	arguments := [3]Value{target, key, value}
	count, err := state.runLuaOperation(
		ctx,
		luaSetIndexOperation,
		arguments[:],
		nil,
	)
	if err == nil && count != 0 {
		panic("lua: SetIndex operation returned a result")
	}
	return err
}

// ToString converts value using Lua's tostring semantics.
//
// ToString honors __tostring and requires that metamethod to return a string.
// It differs from Value.String, which is diagnostic and never executes Lua.
func (state *State) ToString(value Value) (string, error) {
	arguments := [1]Value{value}
	result, err := state.luaOperationValue(
		nil,
		luaToStringOperation,
		arguments[:],
	)
	if err != nil {
		return "", err
	}
	text, ok := result.AsString()
	if !ok {
		panic("lua: ToString operation returned a non-string")
	}
	return text, nil
}

// ToStringContext applies ToString while making ctx available to executing
// Lua.
func (state *State) ToStringContext(
	ctx context.Context,
	value Value,
) (string, error) {
	if ctx == nil {
		return "", ErrNilContext
	}
	arguments := [1]Value{value}
	result, err := state.luaOperationValue(
		ctx,
		luaToStringOperation,
		arguments[:],
	)
	if err != nil {
		return "", err
	}
	text, ok := result.AsString()
	if !ok {
		panic("lua: ToString operation returned a non-string")
	}
	return text, nil
}

// Len applies Lua's length operator and preserves an arbitrary __len result.
func (state *State) Len(value Value) (Value, error) {
	arguments := [1]Value{value}
	return state.luaOperationValue(nil, luaLengthOperation, arguments[:])
}

// LenContext applies Len while making ctx available to executing Lua.
func (state *State) LenContext(
	ctx context.Context,
	value Value,
) (Value, error) {
	if ctx == nil {
		return Value{}, ErrNilContext
	}
	arguments := [1]Value{value}
	return state.luaOperationValue(ctx, luaLengthOperation, arguments[:])
}

// Equal applies ordinary Lua equality, including __eq where applicable.
func (state *State) Equal(left, right Value) (bool, error) {
	arguments := [2]Value{left, right}
	return state.luaOperationBool(nil, luaEqualOperation, arguments[:])
}

// EqualContext applies Equal while making ctx available to executing Lua.
func (state *State) EqualContext(
	ctx context.Context,
	left, right Value,
) (bool, error) {
	if ctx == nil {
		return false, ErrNilContext
	}
	arguments := [2]Value{left, right}
	return state.luaOperationBool(ctx, luaEqualOperation, arguments[:])
}

// LessThan applies Lua's < operation.
func (state *State) LessThan(left, right Value) (bool, error) {
	arguments := [2]Value{left, right}
	return state.luaOperationBool(nil, luaLessThanOperation, arguments[:])
}

// LessThanContext applies LessThan while making ctx available to executing
// Lua.
func (state *State) LessThanContext(
	ctx context.Context,
	left, right Value,
) (bool, error) {
	if ctx == nil {
		return false, ErrNilContext
	}
	arguments := [2]Value{left, right}
	return state.luaOperationBool(ctx, luaLessThanOperation, arguments[:])
}

// LessEqual applies Lua's <= operation, including Lua 5.1's reversed __lt
// fallback when no shared __le handler exists.
func (state *State) LessEqual(left, right Value) (bool, error) {
	arguments := [2]Value{left, right}
	return state.luaOperationBool(nil, luaLessEqualOperation, arguments[:])
}

// LessEqualContext applies LessEqual while making ctx available to executing
// Lua.
func (state *State) LessEqualContext(
	ctx context.Context,
	left, right Value,
) (bool, error) {
	if ctx == nil {
		return false, ErrNilContext
	}
	arguments := [2]Value{left, right}
	return state.luaOperationBool(ctx, luaLessEqualOperation, arguments[:])
}

// Global applies ordinary Lua indexing to the main Thread's global
// environment.
func (state *State) Global(name string) (Value, error) {
	return state.global(nil, name)
}

// GlobalContext applies Global while making ctx available to executing Lua.
func (state *State) GlobalContext(
	ctx context.Context,
	name string,
) (Value, error) {
	if ctx == nil {
		return Value{}, ErrNilContext
	}
	return state.global(ctx, name)
}

func (state *State) global(
	ctx context.Context,
	name string,
) (Value, error) {
	if err := state.checkOpen(); err != nil {
		return Value{}, err
	}
	arguments := [2]Value{
		state.main.globals.owningValue(),
		state.String(name),
	}
	return state.luaOperationValue(ctx, luaIndexOperation, arguments[:])
}

// SetGlobal applies an ordinary Lua assignment to the main Thread's global
// environment.
func (state *State) SetGlobal(name string, value Value) error {
	return state.setGlobal(nil, name, value)
}

// SetGlobalContext applies SetGlobal while making ctx available to executing
// Lua.
func (state *State) SetGlobalContext(
	ctx context.Context,
	name string,
	value Value,
) error {
	if ctx == nil {
		return ErrNilContext
	}
	return state.setGlobal(ctx, name, value)
}

func (state *State) setGlobal(
	ctx context.Context,
	name string,
	value Value,
) error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	arguments := [3]Value{
		state.main.globals.owningValue(),
		state.String(name),
		value,
	}
	count, err := state.runLuaOperation(
		ctx,
		luaSetIndexOperation,
		arguments[:],
		nil,
	)
	if err == nil && count != 0 {
		panic("lua: SetGlobal operation returned a result")
	}
	return err
}

// ToString converts value using Lua's tostring semantics from a native
// callback. The returned string is owned by Go.
func (frame Frame) ToString(value Value) (string, error) {
	frame.activation()
	if failure := frame.thread.state.execution.pendingExit; failure != nil {
		return "", failure.exposeValue()
	}
	if err := frame.thread.owner.accept(value); err != nil {
		return "", err
	}
	result, failure := frame.toStringSlot(slotFromValue(value), true)
	if failure != nil {
		return "", failure.exposeValue()
	}
	return stringSlotText(result), nil
}

func (frame Frame) toStringSlot(
	value slot,
	admitArgument bool,
) (slot, *Error) {
	if method, found := metamethodSlot(
		frame.thread,
		value,
		metaToString,
	); found {
		arguments := [1]slot{value}
		var result slot
		var failure *Error
		if admitArgument {
			result, failure = frame.callBoundaryCompactOne(
				method,
				arguments[:],
				compactCallAdmission(0),
			)
		} else {
			result, failure = frame.callCompactOne(method, arguments[:])
		}
		if failure != nil {
			return nilSlot, failure
		}
		if !result.isString() {
			return nilSlot, libraryFailure(
				frame,
				"'__tostring' must return a string",
			)
		}
		return result, nil
	}
	if value.isString() {
		return value, nil
	}
	return stringSlot(
		frame.thread.owner.strings.make(value.diagnosticString()),
	), nil
}

// Len applies Lua's length operator from a native callback and preserves an
// arbitrary __len result.
func (frame Frame) Len(value Value) (Value, error) {
	frame.activation()
	if failure := frame.thread.state.execution.pendingExit; failure != nil {
		return Value{}, failure.exposeValue()
	}
	if err := frame.thread.owner.accept(value); err != nil {
		return Value{}, err
	}
	result, failure := frame.lengthSlot(slotFromValue(value), true)
	if failure != nil {
		return Value{}, failure.exposeValue()
	}
	return result.owningValue(), nil
}

func (frame Frame) lengthSlot(
	value slot,
	admitArgument bool,
) (slot, *Error) {
	switch value.kind() {
	case StringKind:
		return numberSlot(float64(stringSlotLen(value))), nil
	case TableKind:
		return numberSlot(float64(
			tableObjectFromSlot(value).rawLen(),
		)), nil
	}
	method, found := binaryMetamethod(
		frame.thread,
		value,
		nilSlot,
		metaLength,
	)
	if !found {
		return nilSlot, libraryFailure(
			frame,
			"attempt to get length of a %s value",
			value.kind(),
		)
	}
	arguments := [2]slot{value, nilSlot}
	if admitArgument {
		return frame.callBoundaryCompactOne(
			method,
			arguments[:],
			compactCallAdmission(0),
		)
	}
	return frame.callCompactOne(method, arguments[:])
}

// Equal applies ordinary Lua equality from a native callback.
func (frame Frame) Equal(left, right Value) (bool, error) {
	frame.activation()
	if failure := frame.thread.state.execution.pendingExit; failure != nil {
		return false, failure.exposeValue()
	}
	if err := frame.thread.owner.accept(left); err != nil {
		return false, err
	}
	if err := frame.thread.owner.accept(right); err != nil {
		return false, err
	}
	result, failure := frame.equalSlots(
		slotFromValue(left),
		slotFromValue(right),
		true,
	)
	if failure != nil {
		return false, failure.exposeValue()
	}
	return result, nil
}

func (frame Frame) equalSlots(
	left, right slot,
	admitArguments bool,
) (bool, *Error) {
	if rawSlotEqual(left, right) {
		return true, nil
	}
	if left.kind() != right.kind() ||
		(!left.isTable() && !left.isUserData()) {
		return false, nil
	}
	method, found := matchingMetamethod(
		frame.thread,
		left,
		right,
		metaEqual,
	)
	if !found {
		return false, nil
	}
	arguments := [2]slot{left, right}
	var result slot
	var failure *Error
	if admitArguments {
		result, failure = frame.callBoundaryCompactOne(
			method,
			arguments[:],
			compactCallAdmission(0)|compactCallAdmission(1),
		)
	} else {
		result, failure = frame.callCompactOne(method, arguments[:])
	}
	if failure != nil {
		return false, failure
	}
	return truthySlot(result), nil
}

// LessThan applies Lua's < operation from a native callback.
func (frame Frame) LessThan(left, right Value) (bool, error) {
	return frame.order(left, right, luaLessThanOperation)
}

// LessEqual applies Lua's <= operation from a native callback.
func (frame Frame) LessEqual(left, right Value) (bool, error) {
	return frame.order(left, right, luaLessEqualOperation)
}

func (frame Frame) order(
	left, right Value,
	operation luaOperation,
) (bool, error) {
	frame.activation()
	if failure := frame.thread.state.execution.pendingExit; failure != nil {
		return false, failure.exposeValue()
	}
	if err := frame.thread.owner.accept(left); err != nil {
		return false, err
	}
	if err := frame.thread.owner.accept(right); err != nil {
		return false, err
	}
	result, failure := frame.orderSlots(
		slotFromValue(left),
		slotFromValue(right),
		operation,
		true,
	)
	if failure != nil {
		return false, failure.exposeValue()
	}
	return result, nil
}

func (frame Frame) orderSlots(
	left, right slot,
	operation luaOperation,
	admitArguments bool,
) (bool, *Error) {
	leftKind := left.kind()
	rightKind := right.kind()
	if leftKind != rightKind {
		return false, libraryFailure(
			frame,
			"attempt to compare %s with %s",
			leftKind,
			rightKind,
		)
	}
	switch leftKind {
	case NumberKind:
		leftNumber := math.Float64frombits(left.bits)
		rightNumber := math.Float64frombits(right.bits)
		if operation == luaLessEqualOperation {
			return leftNumber <= rightNumber, nil
		}
		return leftNumber < rightNumber, nil
	case StringKind:
		leftText := stringSlotText(left)
		rightText := stringSlotText(right)
		if operation == luaLessEqualOperation {
			return leftText <= rightText, nil
		}
		return leftText < rightText, nil
	}

	event := metaLessThan
	if operation == luaLessEqualOperation {
		event = metaLessEqual
	}
	method, found := matchingMetamethod(
		frame.thread,
		left,
		right,
		event,
	)
	invert := false
	if !found && operation == luaLessEqualOperation {
		method, found = matchingMetamethod(
			frame.thread,
			right,
			left,
			metaLessThan,
		)
		if found {
			left, right = right, left
			invert = true
		}
	}
	if !found {
		return false, libraryFailure(
			frame,
			"attempt to compare two %s values",
			leftKind,
		)
	}
	arguments := [2]slot{left, right}
	var result slot
	var failure *Error
	if admitArguments {
		result, failure = frame.callBoundaryCompactOne(
			method,
			arguments[:],
			compactCallAdmission(0)|compactCallAdmission(1),
		)
	} else {
		result, failure = frame.callCompactOne(method, arguments[:])
	}
	if failure != nil {
		return false, failure
	}
	ordered := truthySlot(result)
	if invert {
		ordered = !ordered
	}
	return ordered, nil
}

// Global applies ordinary Lua indexing to the executing Thread's global
// environment.
func (frame Frame) Global(name string) (Value, error) {
	frame.activation()
	if failure := frame.thread.state.execution.pendingExit; failure != nil {
		return Value{}, failure.exposeValue()
	}
	result, failure := frame.indexCompact(
		slotFromTableObject(frame.thread.globals),
		stringSlot(frame.thread.owner.strings.make(name)),
	)
	if failure != nil {
		return Value{}, failure.exposeValue()
	}
	return result.owningValue(), nil
}

// SetGlobal applies an ordinary Lua assignment to the executing Thread's
// global environment.
func (frame Frame) SetGlobal(name string, value Value) error {
	frame.activation()
	if failure := frame.thread.state.execution.pendingExit; failure != nil {
		return failure.exposeValue()
	}
	if err := frame.thread.owner.accept(value); err != nil {
		return err
	}
	if failure := frame.setIndexSlots(
		slotFromTableObject(frame.thread.globals),
		stringSlot(frame.thread.owner.strings.make(name)),
		slotFromValue(value),
		true,
	); failure != nil {
		return failure.exposeValue()
	}
	return nil
}
