package lua

// callCompactOne invokes callable with compact arguments and keeps exactly one
// compact result. It is the shared internal seam for library callbacks and
// comparisons; neither arguments nor the result materialize owning Values.
func (frame Frame) callCompactOne(
	callable slot,
	arguments []slot,
) (slot, *Error) {
	return frame.callCompactFixed(callable, arguments, 1)
}

// callCompactNone invokes callable with compact arguments and discards every
// result. It is the internal lua_call(..., 0) seam for library callbacks and
// metamethods.
func (frame Frame) callCompactNone(
	callable slot,
	arguments []slot,
) *Error {
	_, failure := frame.callCompactFixed(callable, arguments, 0)
	return failure
}

func (frame Frame) callCompactFixed(
	callable slot,
	arguments []slot,
	wantedResults int,
) (slot, *Error) {
	if wantedResults < 0 || wantedResults > 1 {
		panic("lua: compact fixed call requires zero or one result")
	}
	thread := frame.thread
	checkpoint := captureExecutionCheckpoint(frame)
	restored := false
	defer func() {
		if !restored {
			checkpoint.restore(thread, true)
		}
	}()

	resultBase, failure := startNestedCall(
		thread,
		callable,
		callArguments{compact: arguments},
		wantedResults,
	)
	if failure == nil {
		result := driveExecution(thread, checkpoint.frameDepth)
		switch result.kind {
		case executionReturned:
			if len(thread.frames) != checkpoint.frameDepth ||
				len(thread.continuations) != checkpoint.continuationDepth {
				panic("lua: compact call returned invalid execution state")
			}
			value := nilSlot
			if wantedResults == 1 {
				if thread.top <= resultBase {
					panic("lua: compact call omitted its adjusted result")
				}
				value = thread.values[resultBase]
			}
			checkpoint.restore(thread, true)
			restored = true
			return value, nil
		case executionFailed:
			if result.err == nil {
				panic("lua: compact call failed without an error")
			}
			failure = result.err
			snapshotExecutionFailure(
				thread,
				checkpoint.frameDepth,
				failure,
			)
		default:
			panic("lua: compact call produced an invalid execution result")
		}
	}
	checkpoint.restore(thread, true)
	restored = true
	return nilSlot, failure
}

// callCompactAllAndReturn invokes callable with compact arguments and returns
// every result directly from the current native activation. It is the
// allocation-free lua_call(..., LUA_MULTRET) seam used by library functions
// such as dofile.
func (frame Frame) callCompactAllAndReturn(
	callable slot,
	arguments []slot,
) Outcome {
	thread := frame.thread
	checkpoint := captureExecutionCheckpoint(frame)
	restored := false
	defer func() {
		if !restored {
			checkpoint.restore(thread, true)
		}
	}()

	resultBase, failure := startNestedCall(
		thread,
		callable,
		callArguments{compact: arguments},
		allResults,
	)
	if failure == nil {
		result := driveExecution(thread, checkpoint.frameDepth)
		switch result.kind {
		case executionReturned:
			if len(thread.frames) != checkpoint.frameDepth ||
				len(thread.continuations) != checkpoint.continuationDepth ||
				thread.top < resultBase {
				panic("lua: compact call returned invalid execution state")
			}
			resultCount := thread.top - resultBase
			previousExtent := checkpoint.restore(thread, false)
			restored = true
			return frame.returnScratchValues(
				resultBase,
				resultCount,
				checkpoint.liveExtent,
				previousExtent,
			)
		case executionFailed:
			if result.err == nil {
				panic("lua: compact call failed without an error")
			}
			failure = result.err
			snapshotExecutionFailure(
				thread,
				checkpoint.frameDepth,
				failure,
			)
		default:
			panic("lua: compact call produced an invalid execution result")
		}
	}
	checkpoint.restore(thread, true)
	restored = true
	return frame.sealError(failure)
}

func (frame Frame) returnScratchValues(
	firstResult int,
	resultCount int,
	scratchBase int,
	previousExtent int,
) Outcome {
	call := frame.activation()
	if resultCount < 0 {
		frame.thread.clearInactive(scratchBase, previousExtent)
		return frame.sealError(newResourceError("value stack index overflow"))
	}
	outputCount, failure := frame.prepareResults(call, resultCount)
	if failure != nil {
		frame.thread.clearInactive(scratchBase, previousExtent)
		return frame.sealError(failure)
	}

	resultBase := int(call.resultBase)
	copied := resultCount
	if copied > outputCount {
		copied = outputCount
	}
	copy(
		frame.thread.values[resultBase:resultBase+copied],
		frame.thread.values[firstResult:firstResult+copied],
	)
	frame.thread.fillNil(
		resultBase+copied,
		resultBase+outputCount,
	)

	clearFrom := scratchBase
	if resultEnd := resultBase + outputCount; resultEnd > clearFrom {
		clearFrom = resultEnd
	}
	frame.thread.clearInactive(clearFrom, previousExtent)
	return frame.sealReturn(outputCount)
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
	result, failure := frame.indexCompact(
		slotFromValue(target),
		slotFromValue(key),
	)
	if failure != nil {
		return Value{}, failure.exposeValue()
	}
	return result.owningValue(), nil
}

func (frame Frame) indexCompact(target, key slot) (slot, *Error) {
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
			return frame.callCompactOne(handler, arguments[:])
		}
		target = handler
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
	if failure := frame.setIndexCompact(
		slotFromValue(target),
		slotFromValue(key),
		slotFromValue(value),
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
			if _, location, present := table.resolveNormalizedSlot(
				normalized,
				index,
				arrayKey,
				hash,
			); present {
				table.replaceResolvedSlot(location, value)
				return nil
			}
			handler, found = metamethodSlot(
				frame.thread,
				target,
				metaNewIndex,
			)
			if !found {
				table.rawSetNormalizedSlot(
					normalized,
					index,
					arrayKey,
					hash,
					value,
				)
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
			return frame.callCompactNone(handler, arguments[:])
		}
		target = handler
	}
	return libraryFailure(frame, "loop in settable")
}

// Call invokes callable synchronously and in protected mode on the Thread
// executing frame.
//
// Callable may be a Function or a value with a Function-valued __call
// metamethod. Results are owning Values and remain valid after this callback
// returns. Invalid or foreign inputs are rejected before Lua executes.
// Execution failures are returned as *Error, and Lua-visible side effects are
// not rolled back. ExitError is terminal for the enclosing public execution:
// after a nested call observes one, later Call, Index, or SetIndex operations
// return that first request and a callback's ordinary Outcome cannot suppress
// it.
//
// A Go panic from the nested call propagates after the outer Frame is
// restored. A yield on this same Thread returns Lua 5.1's illegal-yield error;
// a separate child coroutine resumed by the call may yield normally.
//
// The borrowed Frame remains valid after Call returns. It must not be used
// concurrently or from a callback entered by this call.
func (frame Frame) Call(
	callable Value,
	arguments ...Value,
) ([]Value, error) {
	results, _, err := frame.callNested(
		callable,
		arguments,
		nil,
		true,
	)
	return results, err
}

// CallInto invokes callable synchronously and in protected mode, writing its
// results into destination.
//
// Arguments are staged before execution, so arguments and destination may
// overlap. On success, count entries are written and the destination tail is
// unchanged. On an execution failure or input error, destination is unchanged
// and count is zero. If Lua produces more than len(destination) results, count
// is the required size and a *ResultCapacityError is returned; destination is
// unchanged, but Lua side effects have already occurred.
//
// CallInto otherwise follows Call. When the target itself does not allocate, a
// warm call with sufficient internal capacity and caller-provided result
// storage adds no boundary allocation. The borrowed Frame remains valid after
// return.
func (frame Frame) CallInto(
	callable Value,
	arguments []Value,
	destination []Value,
) (count int, err error) {
	_, count, err = frame.callNested(
		callable,
		arguments,
		destination,
		false,
	)
	return count, err
}

func (frame Frame) callNested(
	callable Value,
	arguments []Value,
	destination []Value,
	allocateResults bool,
) (owned []Value, count int, err error) {
	frame.activation()
	thread := frame.thread
	if failure := thread.state.execution.pendingExit; failure != nil {
		return nil, 0, failure
	}
	if err := thread.owner.accept(callable); err != nil {
		return nil, 0, err
	}
	for _, argument := range arguments {
		if err := thread.owner.accept(argument); err != nil {
			return nil, 0, err
		}
	}

	checkpoint := captureExecutionCheckpoint(frame)
	restored := false
	defer func() {
		if !restored {
			checkpoint.restore(thread, true)
		}
	}()

	resultBase, failure := startNestedCall(
		thread,
		slotFromValue(callable),
		callArguments{owning: arguments},
		allResults,
	)
	if failure == nil {
		result := driveExecution(thread, checkpoint.frameDepth)
		switch result.kind {
		case executionReturned:
			if len(thread.frames) != checkpoint.frameDepth ||
				len(thread.continuations) != checkpoint.continuationDepth ||
				thread.top < resultBase {
				panic("lua: nested call returned invalid execution state")
			}
			count = thread.top - resultBase
		case executionFailed:
			if result.err == nil {
				panic("lua: nested call failed without an error")
			}
			failure = result.err
			snapshotExecutionFailure(
				thread,
				checkpoint.frameDepth,
				failure,
			)
		default:
			panic("lua: nested call produced an invalid execution result")
		}
	}
	if failure != nil {
		checkpoint.restore(thread, true)
		restored = true
		return nil, 0, failure.exposeValue()
	}

	if allocateResults {
		if count != 0 {
			owned = make([]Value, count)
			for index := range owned {
				owned[index] =
					thread.values[resultBase+index].owningValue()
			}
		}
	} else {
		if count > len(destination) {
			checkpoint.restore(thread, true)
			restored = true
			return nil, count, &ResultCapacityError{
				Required:  count,
				Available: len(destination),
			}
		}
		for index := 0; index < count; index++ {
			destination[index] =
				thread.values[resultBase+index].owningValue()
		}
	}

	checkpoint.restore(thread, true)
	restored = true
	return owned, count, nil
}

// RaiseError completes the callback by propagating a protected execution
// failure.
//
// The arbitrary Lua error Value, category, Go cause, and nested traceback are
// preserved. As the propagated error unwinds, the executor appends each
// surrounding traceback segment exactly once. Failure must be non-nil and its
// Value must be valid for this State; invalid or foreign reference Values
// panic.
func (frame Frame) RaiseError(failure *Error) Outcome {
	frame.activation()
	value, valid := failure.valueSlot()
	if !valid {
		panic("lua: invalid Lua error")
	}
	if err := frame.thread.owner.acceptSlot(value); err != nil {
		panic(err)
	}

	propagated := *failure
	propagated.traceback = append([]TraceFrame(nil), failure.traceback...)
	return frame.sealError(&propagated)
}
