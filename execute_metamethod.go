package lua

const (
	continuationComparison uint32 = 1 << iota
	continuationInvert
	continuationIgnoreResult
	continuationConcat
)

// executionContinuation records only work suspended while a Lua metamethod
// runs. Ordinary calls need no continuation: their destination and result
// count already live in the activation.
type executionContinuation struct {
	frameDepth  uint32
	scratchBase uint32
	savedTop    uint32
	savedExtent uint32
	nextPC      uint32
	code        instruction
	flags       uint32
}

//go:noinline
func startMetamethodCall(
	thread *Thread,
	frameIndex int,
	instructionPC int,
	callable slot,
	first slot,
	second slot,
	third slot,
	argumentCount int,
	wantedResults int,
	continuation executionContinuation,
) *Error {
	if argumentCount < 0 || argumentCount > 3 ||
		(wantedResults != 0 && wantedResults != 1) {
		panic("lua: invalid metamethod call shape")
	}
	function, direct := luaFunctionSlot(callable)
	if !direct {
		function = luaCallMetamethod(thread, callable)
		if function == nil {
			return newExecutionRuntimeError(
				thread,
				frameIndex,
				instructionPC,
				"attempt to call a %s value",
				callable.kind(),
			)
		}
	}
	if len(thread.frames) >= thread.state.options.MaxFrames {
		return newResourceError(
			"lua: call frame limit of %d exceeded",
			thread.state.options.MaxFrames,
		)
	}

	savedTop := thread.top
	savedExtent := thread.frameExtent
	scratchBase := thread.liveValueExtent()
	stagedValues := argumentCount + 1
	callArgumentCount := argumentCount
	if !direct {
		stagedValues++
		callArgumentCount++
	}
	limit := thread.state.options.MaxValues
	if scratchBase < 0 ||
		scratchBase > limit ||
		stagedValues > limit-scratchBase ||
		uint64(scratchBase+stagedValues) > uint64(^uint32(0)) {
		return newResourceError(
			"lua: value stack limit of %d exceeded",
			limit,
		)
	}
	layout, resourceError := thread.planLuaCallLayout(
		function,
		scratchBase,
		callArgumentCount,
		wantedResults,
	)
	if resourceError != nil {
		return resourceError
	}

	thread.reserveValues(layout.required)
	thread.reserveFrames(len(thread.frames) + 1)
	thread.values[scratchBase] = callable
	if argumentCount > 0 {
		thread.values[scratchBase+1] = first
	}
	if argumentCount > 1 {
		thread.values[scratchBase+2] = second
	}
	if argumentCount > 2 {
		thread.values[scratchBase+3] = third
	}
	stageEnd := scratchBase + argumentCount + 1
	if !direct {
		thread.insertCallMetamethod(
			function,
			scratchBase,
			argumentCount,
		)
		stageEnd++
	}
	thread.top = stageEnd
	thread.frameExtent = stageEnd

	continuation.frameDepth = uint32(len(thread.frames))
	continuation.scratchBase = uint32(scratchBase)
	continuation.savedTop = uint32(savedTop)
	continuation.savedExtent = uint32(savedExtent)
	if wantedResults == 0 {
		continuation.flags |= continuationIgnoreResult
	}
	thread.commitLuaCall(function, layout, stageEnd)
	thread.continuations = append(thread.continuations, continuation)
	return nil
}

//go:noinline
func resumeExecutionContinuation(thread *Thread) *Error {
	last := len(thread.continuations) - 1
	continuation := thread.continuations[last]
	if int(continuation.frameDepth) != len(thread.frames) {
		panic("lua: execution continuation resumed at the wrong depth")
	}

	scratchBase := int(continuation.scratchBase)
	result := nilSlot
	if continuation.flags&continuationIgnoreResult == 0 {
		result = thread.values[scratchBase]
	}
	previousExtent := thread.liveValueExtent()
	thread.top = int(continuation.savedTop)
	thread.frameExtent = int(continuation.savedExtent)
	thread.clearInactive(scratchBase, previousExtent)
	thread.continuations[last] = executionContinuation{}
	thread.continuations = thread.continuations[:last]

	frameIndex := int(continuation.frameDepth) - 1
	frame := &thread.frames[frameIndex]
	if continuation.flags&continuationIgnoreResult != 0 {
		frame.pc = continuation.nextPC
		return nil
	}
	if continuation.flags&continuationConcat != 0 {
		frame.pc = continuation.nextPC
		writeSlot(
			&thread.values[int(frame.base)+continuation.code.c()],
			result,
		)
		return slowConcat(thread, frameIndex, continuation.code)
	}
	if continuation.flags&continuationComparison == 0 {
		writeSlot(
			&thread.values[int(frame.base)+continuation.code.a()],
			result,
		)
		frame.pc = continuation.nextPC
		return nil
	}

	truth := result.ref != nilMarkerPointer &&
		result.ref != falseMarkerPointer
	if continuation.flags&continuationInvert != 0 {
		truth = !truth
	}
	setComparisonPC(
		thread,
		frameIndex,
		int(continuation.nextPC),
		continuation.code,
		truth,
	)
	return nil
}

func setComparisonPC(
	thread *Thread,
	frameIndex int,
	followerPC int,
	code instruction,
	result bool,
) {
	nextPC := followerPC + 1
	if result == (code.a() != 0) {
		nextPC += thread.frames[frameIndex].
			function.
			prototype.
			code[followerPC].
			sbx()
	}
	thread.frames[frameIndex].pc = uint32(nextPC)
}
