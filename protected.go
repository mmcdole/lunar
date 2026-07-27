package lua

import "fmt"

const (
	protectedFrameReserve = 8
	protectedValueReserve = 64
)

// executionCheckpoint is the non-transactional boundary used by nested calls.
// Lua-visible mutations survive a failure; only private call machinery and
// stack roots created above the boundary are restored.
type executionCheckpoint struct {
	frameDepth         int
	continuationDepth  int
	top                int
	frameExtent        int
	liveExtent         int
	nativeToken        uint64
	nativeCallDepth    uint16
	runtimeNativeDepth uint16
}

func captureExecutionCheckpoint(frame Frame) executionCheckpoint {
	frame.activation()
	thread := frame.thread
	return executionCheckpoint{
		frameDepth:         len(thread.frames),
		continuationDepth:  len(thread.continuations),
		top:                thread.top,
		frameExtent:        thread.frameExtent,
		liveExtent:         thread.liveValueExtent(),
		nativeToken:        frame.token,
		nativeCallDepth:    thread.nativeCallDepth,
		runtimeNativeDepth: thread.owner.nativeCallDepth,
	}
}

// restore removes every activation and continuation created above checkpoint.
// It closes escaped stack cells before clearing their former storage. The
// returned extent bounds all scratch slots that were live before restoration.
func (checkpoint executionCheckpoint) restore(
	thread *threadObject,
	clearScratch bool,
) int {
	if thread == nil ||
		checkpoint.frameDepth <= 0 ||
		checkpoint.frameDepth > len(thread.frames) ||
		checkpoint.continuationDepth > len(thread.continuations) ||
		checkpoint.top < 0 ||
		checkpoint.frameExtent < 0 ||
		checkpoint.liveExtent < checkpoint.top ||
		checkpoint.liveExtent < checkpoint.frameExtent ||
		checkpoint.liveExtent > len(thread.values) ||
		thread.activeNativeToken != checkpoint.nativeToken ||
		thread.nativeCallDepth != checkpoint.nativeCallDepth ||
		thread.owner.nativeCallDepth != checkpoint.runtimeNativeDepth {
		panic("lua: invalid nested execution checkpoint")
	}

	previousExtent := thread.liveValueExtent()
	if previousExtent < checkpoint.liveExtent {
		panic("lua: protected execution lowered its checkpoint extent")
	}
	thread.closeUpvalues(checkpoint.liveExtent)
	for index := len(thread.frames) - 1; index >= checkpoint.frameDepth; index-- {
		thread.frames[index] = activation{}
	}
	thread.frames = thread.frames[:checkpoint.frameDepth]
	for index := len(thread.continuations) - 1; index >= checkpoint.continuationDepth; index-- {
		thread.continuations[index] = executionContinuation{}
	}
	thread.continuations = thread.continuations[:checkpoint.continuationDepth]
	thread.top = checkpoint.top
	thread.frameExtent = checkpoint.frameExtent
	if clearScratch {
		thread.clearInactive(checkpoint.liveExtent, previousExtent)
	}
	if thread.openUpvalues != nil &&
		thread.openUpvalues.stackIndex() >= checkpoint.liveExtent {
		panic("lua: protected execution retained a scratch upvalue")
	}
	return previousExtent
}

func (thread *threadObject) valueLimit() int {
	return thread.state.limits.values
}

func (thread *threadObject) frameLimit() int {
	return thread.state.limits.frames
}

func (thread *threadObject) nativeCallLimit() int {
	if thread.errorHandlerDepth != 0 {
		return protectedResourceLimit(
			maxNativeCallDepth,
			protectedFrameReserve,
		)
	}
	return maxNativeCallDepth
}

func protectedResourceLimit(limit, minimumReserve int) int {
	reserve := limit / 8
	if reserve < minimumReserve {
		reserve = minimumReserve
	}
	maxInt := int(^uint(0) >> 1)
	if limit > maxInt-reserve {
		return maxInt
	}
	return limit + reserve
}

func (thread *threadObject) enterErrorHandler() {
	if thread.errorHandlerDepth == ^uint16(0) {
		panic("lua: protected error-handler nesting overflow")
	}
	if thread.errorHandlerDepth == 0 {
		thread.state.limits = resourceLimits{
			values: protectedResourceLimit(
				thread.state.options.MaxValues,
				protectedValueReserve,
			),
			frames: protectedResourceLimit(
				thread.state.options.MaxFrames,
				protectedFrameReserve,
			),
		}
	}
	thread.errorHandlerDepth++
}

func (thread *threadObject) leaveErrorHandler() {
	if thread.errorHandlerDepth == 0 {
		panic("lua: protected error-handler nesting underflow")
	}
	thread.errorHandlerDepth--
	if thread.errorHandlerDepth != 0 {
		return
	}
	thread.state.limits = resourceLimits{
		values: thread.state.options.MaxValues,
		frames: thread.state.options.MaxFrames,
	}

	valueLimit := thread.state.options.MaxValues
	if thread.liveValueExtent() > valueLimit {
		panic("lua: error handler retained values beyond the configured limit")
	}
	if len(thread.values) > valueLimit {
		thread.clearInactive(valueLimit, len(thread.values))
		thread.values = thread.values[:valueLimit]
	}
	if cap(thread.values) > valueLimit {
		thread.values = thread.values[:len(thread.values):valueLimit]
	}

	frameLimit := thread.state.options.MaxFrames
	if len(thread.frames) > frameLimit {
		panic("lua: error handler retained frames beyond the configured limit")
	}
	if cap(thread.frames) > frameLimit {
		thread.frames = thread.frames[:len(thread.frames):frameLimit]
	}
}

// runProtectedCall enters target above the native pcall/xpcall activation and
// drives the same executor until the target returns or fails. A handler, when
// present, runs before the failing frames are restored so debug operations can
// inspect the original failure stack.
func runProtectedCall(
	frame Frame,
	target slot,
	arguments []slot,
	handler slot,
	hasHandler bool,
) Outcome {
	checkpoint := captureExecutionCheckpoint(frame)
	thread := frame.thread
	restored := false
	handlerActive := false
	defer func() {
		if !restored {
			checkpoint.restore(thread, true)
		}
		if handlerActive {
			thread.leaveErrorHandler()
		}
	}()

	resultBase, failure := startNestedCall(
		thread,
		target,
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
				panic("lua: protected call returned invalid execution state")
			}
			resultCount := thread.top - resultBase
			previousExtent := checkpoint.restore(thread, false)
			restored = true
			return frame.returnProtectedSuccess(
				resultBase,
				resultCount,
				checkpoint.liveExtent,
				previousExtent,
			)
		case executionFailed:
			if result.err == nil {
				panic("lua: protected target failed without an error")
			}
			failure = result.err
		default:
			panic("lua: protected call produced an invalid execution result")
		}
	}
	if failure == nil {
		panic("lua: protected target failed without an error")
	}
	if !isCatchableProtectedFailure(failure) {
		snapshotExecutionFailure(
			thread,
			checkpoint.frameDepth,
			failure,
		)
		checkpoint.restore(thread, true)
		restored = true
		return frame.sealError(failure)
	}

	positionExecutionFailure(thread, failure)
	errorValue := failure.mustValueSlot()
	if !hasHandler {
		checkpoint.restore(thread, true)
		restored = true
		return frame.returnProtectedFailure(errorValue)
	}

	handlerFunction, handlerIsFunction := functionSlot(handler)
	if !handlerIsFunction {
		checkpoint.restore(thread, true)
		restored = true
		return frame.returnProtectedFailure(
			stringSlot(thread.owner.strings.make("error in error handling")),
		)
	}

	thread.enterErrorHandler()
	handlerActive = true
	failureDepth := len(thread.frames)
	handlerArguments := [1]slot{errorValue}
	handlerBase, handlerFailure := startNestedCall(
		thread,
		slotFromFunctionObject(handlerFunction),
		callArguments{compact: handlerArguments[:]},
		1,
	)
	if handlerFailure == nil {
		result := driveExecution(thread, failureDepth)
		if result.kind == executionReturned {
			if len(thread.frames) != failureDepth {
				panic("lua: protected error handler returned invalid execution state")
			}
			handlerValue := thread.values[handlerBase]
			checkpoint.restore(thread, true)
			restored = true
			thread.leaveErrorHandler()
			handlerActive = false
			return frame.returnProtectedFailure(handlerValue)
		}
		if result.kind != executionFailed {
			panic("lua: protected error handler produced an invalid execution result")
		}
		handlerFailure = result.err
	}
	if handlerFailure == nil {
		panic("lua: protected error handler failed without an error")
	}
	if !isCatchableProtectedFailure(handlerFailure) {
		snapshotExecutionFailure(
			thread,
			checkpoint.frameDepth,
			handlerFailure,
		)
		checkpoint.restore(thread, true)
		restored = true
		thread.leaveErrorHandler()
		handlerActive = false
		return frame.sealError(handlerFailure)
	}

	checkpoint.restore(thread, true)
	restored = true
	thread.leaveErrorHandler()
	handlerActive = false
	return frame.returnProtectedFailure(
		stringSlot(thread.owner.strings.make("error in error handling")),
	)
}

func isCatchableProtectedFailure(failure *Error) bool {
	if failure == nil {
		return false
	}
	switch failure.category {
	case RuntimeError, ResourceError:
		return true
	default:
		return false
	}
}

type callArguments struct {
	owning  []Value
	compact []slot
}

func startNestedCall(
	thread *threadObject,
	callable slot,
	arguments callArguments,
	wantedResults int,
) (int, *Error) {
	if failure := thread.state.execution.pendingExit; failure != nil {
		return 0, failure
	}
	function, direct := functionSlot(callable)
	if !direct {
		function = callMetamethodFunction(thread, callable)
		if function == nil {
			message := fmt.Sprintf(
				"attempt to call a %s value",
				callable.kind(),
			)
			return 0, &Error{
				value:       thread.state.String(message),
				description: message,
				category:    RuntimeError,
			}
		}
	}
	if len(thread.frames) >= thread.frameLimit() {
		return 0, newResourceError("stack overflow")
	}

	scratchBase := thread.liveValueExtent()
	argumentCount := len(arguments.compact)
	if arguments.compact == nil {
		argumentCount = len(arguments.owning)
	}
	stagedCount := argumentCount + 1
	callArgumentCount := argumentCount
	if !direct {
		stagedCount++
		callArgumentCount++
	}
	limit := thread.valueLimit()
	if scratchBase < 0 ||
		scratchBase > limit ||
		stagedCount > limit-scratchBase ||
		uint64(scratchBase+stagedCount) > uint64(^uint32(0)) {
		return 0, newResourceError(
			"value stack limit of %d exceeded",
			thread.state.options.MaxValues,
		)
	}
	layout, resourceError := thread.planFunctionCallLayout(
		function,
		scratchBase,
		callArgumentCount,
		wantedResults,
	)
	if resourceError != nil {
		return 0, resourceError
	}

	thread.reserveValues(layout.required)
	thread.reserveFrames(len(thread.frames) + 1)
	writeSlot(&thread.values[scratchBase], callable)
	argumentBase := scratchBase + 1
	if arguments.compact != nil {
		copy(
			thread.values[argumentBase:argumentBase+argumentCount],
			arguments.compact,
		)
	} else {
		for index, value := range arguments.owning {
			writeSlot(
				&thread.values[argumentBase+index],
				slotFromValue(value),
			)
		}
	}
	if !direct {
		thread.insertCallMetamethod(
			function,
			scratchBase,
			argumentCount,
		)
	}
	stageEnd := scratchBase + stagedCount
	thread.top = stageEnd
	thread.frameExtent = stageEnd
	thread.commitFunctionCall(function, layout, stageEnd)
	return scratchBase, nil
}

func (frame Frame) returnProtectedSuccess(
	firstResult int,
	resultCount int,
	scratchBase int,
	previousExtent int,
) Outcome {
	call := frame.activation()
	if resultCount < 0 || resultCount == int(^uint(0)>>1) {
		frame.thread.clearInactive(scratchBase, previousExtent)
		return frame.sealError(newResourceError("value stack index overflow"))
	}
	outputCount, failure := frame.prepareResults(call, resultCount+1)
	if failure != nil {
		frame.thread.clearInactive(scratchBase, previousExtent)
		return frame.sealError(failure)
	}

	resultBase := int(call.resultBase)
	if outputCount != 0 {
		writeSlot(&frame.thread.values[resultBase], trueSlot)
	}
	copied := resultCount
	if available := outputCount - 1; copied > available {
		copied = available
	}
	if copied < 0 {
		copied = 0
	}
	copy(
		frame.thread.values[resultBase+1:resultBase+1+copied],
		frame.thread.values[firstResult:firstResult+copied],
	)
	frame.thread.fillNil(
		resultBase+1+copied,
		resultBase+outputCount,
	)

	clearFrom := scratchBase
	if resultEnd := resultBase + outputCount; resultEnd > clearFrom {
		clearFrom = resultEnd
	}
	frame.thread.clearInactive(clearFrom, previousExtent)
	return frame.sealReturn(outputCount)
}

func (frame Frame) returnProtectedFailure(value slot) Outcome {
	call := frame.activation()
	outputCount, failure := frame.prepareResults(call, 2)
	if failure != nil {
		return frame.sealError(failure)
	}
	resultBase := int(call.resultBase)
	if outputCount != 0 {
		writeSlot(&frame.thread.values[resultBase], falseSlot)
	}
	if outputCount > 1 {
		writeSlot(&frame.thread.values[resultBase+1], value)
	}
	frame.thread.fillNil(resultBase+2, resultBase+outputCount)
	return frame.sealReturn(outputCount)
}
