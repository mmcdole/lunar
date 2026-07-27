package lua

import "unsafe"

type continuationMode uint32

const (
	continuationStoreResult continuationMode = iota
	continuationDiscardResult
	continuationCompare
	continuationCompareInverted
	continuationConcat
	continuationIterator
	continuationFinalizerGuard
)

// executionContinuation records only work suspended across a function call.
// Ordinary calls need no continuation: their destination and result count
// already live in the activation.
type executionContinuation struct {
	frameDepth  uint32
	scratchBase uint32
	savedTop    uint32
	savedExtent uint32
	nextPC      uint32
	code        instruction
	mode        continuationMode
}

func (thread *threadObject) pushExecutionContinuation(
	continuation executionContinuation,
) {
	previousCapacity := cap(thread.continuations)
	thread.continuations = append(thread.continuations, continuation)
	if cap(thread.continuations) != previousCapacity {
		thread.chargeContinuationGrowth(previousCapacity)
	}
}

//go:noinline
func (thread *threadObject) chargeContinuationGrowth(previousCapacity int) {
	thread.owner.collection.chargeCapacityGrowth(
		previousCapacity,
		cap(thread.continuations),
		uint64(unsafe.Sizeof(executionContinuation{})),
	)
}

//go:noinline
func startMetamethodCall(
	thread *threadObject,
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
	switch continuation.mode {
	case continuationStoreResult:
		if wantedResults == 0 {
			continuation.mode = continuationDiscardResult
		}
	case continuationCompare,
		continuationCompareInverted,
		continuationConcat:
		if wantedResults != 1 {
			panic("lua: invalid metamethod continuation mode")
		}
	default:
		panic("lua: invalid metamethod continuation mode")
	}
	function, direct := functionSlot(callable)
	if !direct {
		function = callMetamethodFunction(thread, callable)
		if function == nil {
			return newExecutionTypeError(
				thread,
				frameIndex,
				instructionPC,
				-1,
				"call",
				callable.kind(),
			)
		}
	}
	if len(thread.frames) >= thread.frameLimit() {
		return newResourceError("stack overflow")
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
	limit := thread.valueLimit()
	if scratchBase < 0 ||
		scratchBase > limit ||
		stagedValues > limit-scratchBase ||
		uint64(scratchBase+stagedValues) > uint64(^uint32(0)) {
		return newResourceError(
			"value stack limit of %d exceeded",
			limit,
		)
	}
	layout, resourceError := thread.planFunctionCallLayout(
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
	thread.commitFunctionCall(function, layout, stageEnd)
	thread.pushExecutionContinuation(continuation)
	return nil
}

// startIteratorCall stages the generic-for generator and its two arguments in
// the verified scratch window. All resource checks complete before those
// caller registers are changed.
//
//go:noinline
func startIteratorCall(
	thread *threadObject,
	frameIndex int,
	code instruction,
) *Error {
	frame := thread.frames[frameIndex]
	nextPC := int(frame.pc)
	instructionPC := nextPC - 1
	base := int(frame.base) + code.a()
	generator := thread.values[base]
	state := thread.values[base+1]
	control := thread.values[base+2]

	function, direct := functionSlot(generator)
	if !direct {
		function = callMetamethodFunction(thread, generator)
		if function == nil {
			return newExecutionTypeError(
				thread,
				frameIndex,
				instructionPC,
				-1,
				"call",
				generator.kind(),
			)
		}
	}
	if len(thread.frames) >= thread.frameLimit() {
		return newResourceError("stack overflow")
	}

	callBase := base + 3
	argumentCount := 2
	if !direct {
		argumentCount++
	}
	layout, resourceError := thread.planFunctionCallLayout(
		function,
		callBase,
		argumentCount,
		code.c(),
	)
	if resourceError != nil {
		return resourceError
	}

	savedTop := thread.top
	savedExtent := thread.frameExtent
	oldExtent := thread.liveValueExtent()
	thread.reserveValues(layout.required)
	thread.reserveFrames(len(thread.frames) + 1)
	writeSlot(&thread.values[callBase], generator)
	writeSlot(&thread.values[callBase+1], state)
	writeSlot(&thread.values[callBase+2], control)
	if !direct {
		thread.insertCallMetamethod(function, callBase, 2)
	}

	continuation := executionContinuation{
		frameDepth:  uint32(len(thread.frames)),
		savedTop:    uint32(savedTop),
		savedExtent: uint32(savedExtent),
		nextPC:      uint32(nextPC),
		code:        code,
		mode:        continuationIterator,
	}
	thread.commitFunctionCall(function, layout, oldExtent)
	thread.pushExecutionContinuation(continuation)
	return nil
}

//go:noinline
func resumeExecutionContinuation(thread *threadObject) *Error {
	last := len(thread.continuations) - 1
	continuation := thread.continuations[last]
	if int(continuation.frameDepth) != len(thread.frames) {
		panic("lua: execution continuation resumed at the wrong depth")
	}
	if continuation.mode == continuationFinalizerGuard {
		panic("lua: finalizer call guard escaped its collection seam")
	}
	if continuation.mode == continuationIterator {
		thread.continuations[last] = executionContinuation{}
		thread.continuations = thread.continuations[:last]
		resumeIteratorContinuation(thread, continuation)
		return nil
	}

	scratchBase := int(continuation.scratchBase)
	result := nilSlot
	if continuation.mode != continuationDiscardResult {
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
	switch continuation.mode {
	case continuationDiscardResult:
		frame.pc = continuation.nextPC
		return nil
	case continuationConcat:
		frame.pc = continuation.nextPC
		writeSlot(
			&thread.values[int(frame.base)+continuation.code.c()],
			result,
		)
		return slowConcat(thread, frameIndex, continuation.code)
	case continuationStoreResult:
		writeSlot(
			&thread.values[int(frame.base)+continuation.code.a()],
			result,
		)
		frame.pc = continuation.nextPC
		return nil
	case continuationCompare, continuationCompareInverted:
	default:
		panic("lua: invalid execution continuation mode")
	}

	truth := result.ref != nilMarkerPointer &&
		result.ref != falseMarkerPointer
	if continuation.mode == continuationCompareInverted {
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

func resumeIteratorContinuation(
	thread *threadObject,
	continuation executionContinuation,
) {
	frameIndex := int(continuation.frameDepth) - 1
	frame := &thread.frames[frameIndex]
	base := int(frame.base) + continuation.code.a()
	followerPC := int(continuation.nextPC)
	nextPC := followerPC + 1
	first := thread.values[base+3]
	if !first.isNil() {
		writeSlot(&thread.values[base+2], first)
		follower := frame.function.prototype.code[followerPC]
		if follower.opcode() != opJump {
			panic("lua: TFORLOOP continuation lost its jump")
		}
		nextPC += follower.sbx()
	}
	frame.pc = uint32(nextPC)
}

func setComparisonPC(
	thread *threadObject,
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
