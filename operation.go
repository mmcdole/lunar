package lua

type luaOperation uint8

const (
	luaIndexOperation luaOperation = iota
	luaSetIndexOperation
	luaToStringOperation
	luaLengthOperation
	luaEqualOperation
	luaOperationCount
)

var luaOperationEntries = [...]NativeFunc{
	luaIndexOperation:    luaIndexOperationEntry,
	luaSetIndexOperation: luaSetIndexOperationEntry,
	luaToStringOperation: luaToStringOperationEntry,
	luaLengthOperation:   luaLengthOperationEntry,
	luaEqualOperation:    luaEqualOperationEntry,
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

func (state *State) runLuaOperation(
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
		function.owningValue(),
		arguments,
		destination,
		false,
		allResults,
	)
	return count, err
}

func (state *State) luaOperationValue(
	operation luaOperation,
	arguments []Value,
) (Value, error) {
	var destination [1]Value
	count, err := state.runLuaOperation(
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
	operation luaOperation,
	arguments []Value,
) (bool, error) {
	result, err := state.luaOperationValue(operation, arguments)
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
	return state.luaOperationValue(luaIndexOperation, arguments[:])
}

// SetIndex applies an ordinary Lua assignment on the main Thread.
//
// An existing table field is replaced directly. Otherwise SetIndex follows
// __newindex and may execute Lua. Use Frame.SetIndex from a native callback.
func (state *State) SetIndex(target, key, value Value) error {
	arguments := [3]Value{target, key, value}
	count, err := state.runLuaOperation(
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
	return state.luaOperationValue(luaLengthOperation, arguments[:])
}

// Equal applies ordinary Lua equality, including __eq where applicable.
func (state *State) Equal(left, right Value) (bool, error) {
	arguments := [2]Value{left, right}
	return state.luaOperationBool(luaEqualOperation, arguments[:])
}

// Global applies ordinary Lua indexing to the main Thread's global
// environment.
func (state *State) Global(name string) (Value, error) {
	return state.global(name)
}

func (state *State) global(
	name string,
) (Value, error) {
	if err := state.checkOpen(); err != nil {
		return Value{}, err
	}
	arguments := [2]Value{
		state.main.globals.owningValue(),
		state.String(name),
	}
	return state.luaOperationValue(luaIndexOperation, arguments[:])
}

// SetGlobal applies an ordinary Lua assignment to the main Thread's global
// environment.
func (state *State) SetGlobal(name string, value Value) error {
	return state.setGlobal(name, value)
}

func (state *State) setGlobal(
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

// Index applies ordinary Lua indexing from a native callback.
//
// A raw table hit returns directly. Otherwise Index follows the bounded
// __index chain and may synchronously invoke Lua. Invalid or foreign Values
// are rejected before Lua executes. Execution failures are returned as
// *Error; a yield across this native-call boundary becomes Lua 5.1's
// illegal-yield failure. The borrowed Frame remains valid after Index
// returns.
func (frame Frame) Index(target, key Value) (Value, error) {
	frame.activation()
	if failure := frame.thread.state.execution.pendingExit; failure != nil {
		return Value{}, failure.exposeValue()
	}
	if err := frame.thread.owner.accept(target); err != nil {
		return Value{}, err
	}
	if err := frame.thread.owner.accept(key); err != nil {
		return Value{}, err
	}
	result, failure := frame.indexSlots(
		slotFromValue(target),
		slotFromValue(key),
		true,
	)
	if failure != nil {
		return Value{}, failure.exposeValue()
	}
	return result.owningValue(), nil
}

func (frame Frame) indexCompact(target, key slot) (slot, *Error) {
	return frame.indexSlots(target, key, false)
}

// indexSlots admits boundary values only when a callable metamethod can retain
// them. Raw lookup and table-valued __index chains merely observe their
// immutable representations.
func (frame Frame) indexSlots(
	target slot,
	key slot,
	admitArguments bool,
) (slot, *Error) {
	targetBoundary := admitArguments
	for step := 0; step < maxTableMetamethodChain; step++ {
		var handler slot
		var found bool
		if target.isTable() {
			table := (*tableObject)(target.ref)
			if result, hit := table.rawSlot(key); hit &&
				!result.isNil() {
				return result, nil
			}
			handler, found = metamethodSlot(frame.thread, target, metaIndex)
			if !found {
				return nilSlot, nil
			}
		} else {
			handler, found = metamethodSlot(frame.thread, target, metaIndex)
			if !found {
				return nilSlot, libraryFailure(
					frame,
					"attempt to index a %s value",
					target.kind(),
				)
			}
		}
		if _, callable := functionSlot(handler); callable {
			arguments := [2]slot{target, key}
			if admitArguments {
				admission := compactCallAdmission(1)
				if targetBoundary {
					admission |= compactCallAdmission(0)
				}
				return frame.callBoundaryCompactOne(
					handler,
					arguments[:],
					admission,
				)
			}
			return frame.callCompactOne(handler, arguments[:])
		}
		target = handler
		targetBoundary = false
	}
	return nilSlot, libraryFailure(frame, "loop in gettable")
}

// SetIndex applies an ordinary Lua table assignment from a native callback.
//
// An existing table field is replaced directly. Otherwise SetIndex follows
// the bounded __newindex chain and may synchronously invoke Lua. Invalid or
// foreign Values are rejected before Lua executes. Execution failures are
// returned as *Error; a yield across this native-call boundary becomes Lua
// 5.1's illegal-yield failure. The borrowed Frame remains valid after SetIndex
// returns.
func (frame Frame) SetIndex(target, key, value Value) error {
	frame.activation()
	if failure := frame.thread.state.execution.pendingExit; failure != nil {
		return failure.exposeValue()
	}
	if err := frame.thread.owner.accept(target); err != nil {
		return err
	}
	if err := frame.thread.owner.accept(key); err != nil {
		return err
	}
	if err := frame.thread.owner.accept(value); err != nil {
		return err
	}
	if failure := frame.setIndexSlots(
		slotFromValue(target),
		slotFromValue(key),
		slotFromValue(value),
		true,
	); failure != nil {
		return failure.exposeValue()
	}
	return nil
}

func (frame Frame) setIndexCompact(
	target slot,
	key slot,
	value slot,
) *Error {
	return frame.setIndexSlots(target, key, value, false)
}

// setIndexSlots admits boundary values at the operation that retains them:
// direct storage or a callable metamethod's argument window. Table-valued
// __newindex traversal and no-op assignments remain observation-only.
func (frame Frame) setIndexSlots(
	target slot,
	key slot,
	value slot,
	admitArguments bool,
) *Error {
	targetBoundary := admitArguments
	for range maxTableMetamethodChain {
		var handler slot
		var found bool
		if target.isTable() {
			table := (*tableObject)(target.ref)
			normalized, index, arrayKey, hash, status :=
				normalizeTableKey(key)
			switch status {
			case tableKeyNil:
				return libraryFailure(frame, "table index is nil")
			case tableKeyNaN:
				return libraryFailure(frame, "table index is NaN")
			case tableKeyValid:
			default:
				panic("lua: invalid normalized table key status")
			}
			if current, location, present := table.resolveNormalizedSlot(
				normalized,
				index,
				arrayKey,
				hash,
			); present {
				if admitArguments &&
					!sameSlotRepresentation(current, value) {
					value = frame.thread.owner.importAcceptedSlot(value)
				}
				table.replaceResolvedSlot(location, value)
				return nil
			}
			handler, found = metamethodSlot(
				frame.thread,
				target,
				metaNewIndex,
			)
			if !found {
				if admitArguments {
					table.rawSetBoundaryNormalizedSlot(
						normalized,
						index,
						arrayKey,
						hash,
						value,
					)
				} else {
					table.rawSetNormalizedSlot(
						normalized,
						index,
						arrayKey,
						hash,
						value,
					)
				}
				return nil
			}
		} else {
			handler, found = metamethodSlot(
				frame.thread,
				target,
				metaNewIndex,
			)
			if !found {
				return libraryFailure(
					frame,
					"attempt to index a %s value",
					target.kind(),
				)
			}
		}
		if _, callable := functionSlot(handler); callable {
			arguments := [3]slot{target, key, value}
			if admitArguments {
				admission := compactCallAdmission(1) |
					compactCallAdmission(2)
				if targetBoundary {
					admission |= compactCallAdmission(0)
				}
				return frame.callBoundaryCompactNone(
					handler,
					arguments[:],
					admission,
				)
			}
			return frame.callCompactNone(handler, arguments[:])
		}
		target = handler
		targetBoundary = false
	}
	return libraryFailure(frame, "loop in settable")
}
