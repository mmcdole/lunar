package lua

// Call invokes callable synchronously and in protected mode on the Thread
// executing frame.
//
// Callable may be a Function or a value with a Function-valued __call
// metamethod. Results are owning Values and remain valid after this callback
// returns. Invalid or foreign inputs are rejected before Lua executes. Lua
// failures are returned as *Error, and Lua-visible side effects are not rolled
// back.
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
// unchanged. On a Lua failure or input error, destination is unchanged and
// count is zero. If Lua produces more than len(destination) results, count is
// the required size and a *ResultCapacityError is returned; destination is
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
		return nil, 0, failure
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

// RaiseError completes the callback by propagating a protected Lua failure.
//
// The arbitrary Lua error Value, category, Go cause, and nested traceback are
// preserved. As the propagated error unwinds, the executor appends each
// surrounding traceback segment exactly once. Failure must be non-nil and its
// Value must be valid for this State; invalid or foreign reference Values
// panic.
func (frame Frame) RaiseError(failure *Error) Outcome {
	frame.activation()
	if failure == nil || !failure.value.Valid() {
		panic("lua: invalid Lua error")
	}
	if err := frame.thread.owner.accept(failure.value); err != nil {
		panic(err)
	}

	propagated := *failure
	propagated.traceback = append([]TraceFrame(nil), failure.traceback...)
	return frame.sealError(&propagated)
}
