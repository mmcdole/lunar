package lua

// tryEnterFixedLuaCall enters the common direct fixed Lua call without
// repeating public-boundary validation or growing either execution stack.
// False leaves the execution stacks unchanged so the checked path can grow
// them or report a configured resource failure.
//
// Verified fixed argument and result windows already fit the caller's live
// frame, so only the callee frame end can extend the value stack. The reserve
// helpers bound backing capacities by the immutable resource limits; a
// capacity hit therefore also proves those limits. Entry cannot lower the
// live extent and consequently has no dead suffix to clear.
//
//go:noinline
func (thread *threadObject) tryEnterFixedLuaCall(
	callerBase int,
	code instruction,
) bool {
	argumentField := code.b()
	resultField := code.c()
	if argumentField == 0 || resultField == 0 {
		return false
	}
	callBase := callerBase + code.a()
	callable := thread.values[callBase]
	if callable.ref == nil || callable.bits != uint64(FunctionKind) {
		return false
	}
	function := functionObjectFromSlot(callable)
	argumentCount := argumentField - 1
	wantedResults := resultField - 1
	prototype := function.prototype
	if prototype.varargFlags&varargIsVararg != 0 ||
		len(thread.frames) >= cap(thread.frames) {
		return false
	}

	base := callBase + 1
	frameEnd := base + int(prototype.registers)
	if frameEnd < 0 ||
		uint64(frameEnd) > uint64(^uint32(0)) ||
		frameEnd > cap(thread.values) {
		return false
	}

	if frameEnd > len(thread.values) {
		thread.values = thread.values[:frameEnd]
	}
	thread.setupFixedLuaCall(
		prototype,
		base,
		frameEnd,
		argumentCount,
	)
	thread.publishFunctionCall(
		function,
		base,
		callBase,
		frameEnd,
		wantedResults,
	)
	return true
}

// tryEnterFixedNativeCall enters a direct native CALL whose fixed argument and
// result windows fit the verified caller frame. It leaves both stacks untouched
// on a miss so the ordinary checked path can grow storage or report limits.
// Native execution, Frame validation, cancellation, and return handling remain
// in invokeNativeCall; this helper only avoids rebuilding an already-proved
// call layout.
// Only verified Lua execution may enter here: fixed calls have no open values
// beyond the caller extent, so publishing the call leaves no dead suffix.
//
//go:noinline
func (thread *threadObject) tryEnterFixedNativeCall(
	callerBase int,
	code instruction,
) bool {
	argumentField := code.b()
	resultField := code.c()
	if argumentField == 0 || resultField == 0 {
		return false
	}
	callBase := callerBase + code.a()
	callable := thread.values[callBase]
	if callable.ref == nil ||
		callable.bits != uint64(FunctionKind)|nativeFunctionSlotFlag {
		return false
	}
	if len(thread.frames) >= cap(thread.frames) ||
		len(thread.frames) >= thread.frameLimit() {
		return false
	}

	frameEnd := callBase + argumentField
	wantedResults := resultField - 1
	required := frameEnd
	if resultEnd := callBase + wantedResults; resultEnd > required {
		required = resultEnd
	}
	if required < 0 ||
		uint64(required) > uint64(^uint32(0)) ||
		required > len(thread.values) ||
		required > thread.valueLimit() {
		return false
	}

	thread.publishFunctionCall(
		functionObjectFromSlot(callable),
		callBase+1,
		callBase,
		frameEnd,
		wantedResults,
	)
	return true
}

// tryCompleteFixedLuaReturn completes the common fixed-result nested return.
// The executor supplies a postDepth above its stop depth, which proves that a
// caller exists. False leaves the current activation unchanged for the
// checked executor path.
//
//go:noinline
func (thread *threadObject) tryCompleteFixedLuaReturn(
	postDepth int,
	code instruction,
) bool {
	if code.b() == 0 {
		return false
	}
	frame := thread.frames[postDepth]
	if frame.wantedResults == allResults {
		return false
	}
	if count := len(thread.continuations); count != 0 &&
		int(thread.continuations[count-1].frameDepth) == postDepth {
		return false
	}
	caller := thread.frames[postDepth-1]
	thread.completeLuaReturn(
		int(frame.base)+code.a(),
		code.b()-1,
		int(frame.wantedResults),
		int(caller.base)+int(caller.function.prototype.registers),
	)
	return true
}

// completeLuaReturn completes a verified Lua return without repeating range
// validation. Result sources may overlap their destination.
//
//go:noinline
func (thread *threadObject) completeLuaReturn(
	firstResult int,
	resultCount int,
	outputCount int,
	newTop int,
) {
	frameIndex := len(thread.frames) - 1
	frame := thread.frames[frameIndex]
	resultBase := int(frame.resultBase)
	oldExtent := thread.liveValueExtent()

	thread.closeUpvalues(int(frame.base))
	copyCount := resultCount
	if copyCount > outputCount {
		copyCount = outputCount
	}
	switch copyCount {
	case 0:
	case 1:
		value := thread.values[firstResult]
		writeSlot(&thread.values[resultBase], value)
	case 2:
		first := thread.values[firstResult]
		second := thread.values[firstResult+1]
		writeSlot(&thread.values[resultBase], first)
		writeSlot(&thread.values[resultBase+1], second)
	default:
		copy(
			thread.values[resultBase:resultBase+copyCount],
			thread.values[firstResult:firstResult+copyCount],
		)
	}
	thread.fillNil(resultBase+copyCount, resultBase+outputCount)

	thread.frames[frameIndex] = activation{}
	thread.frames = thread.frames[:frameIndex]
	thread.frameExtent = int(frame.callerExtent)

	thread.top = newTop
	thread.clearDeadSuffix(oldExtent)
}
