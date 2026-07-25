package lua

import "math"

const allResults = -1

// activation is the complete persistent state of one call. The executor
// keeps its hot program counter, base, code, and constants in local variables
// and publishes them here only at call, return, yield, and error seams.
type activation struct {
	function      *Function
	base          uint32
	resultBase    uint32
	pc            uint32
	tailCalls     uint32
	callerExtent  uint32
	wantedResults int32
}

func (frame activation) varargCount() int {
	return int(frame.base) -
		int(frame.resultBase) -
		1 -
		int(frame.function.prototype.parameters)
}

type callLayout struct {
	base          int
	resultBase    int
	frameEnd      int
	required      int
	argumentCount int
	wantedResults int
}

func (thread *Thread) pushFunctionCall(
	function *Function,
	resultBase int,
	argumentCount int,
	wantedResults int,
) *Error {
	if len(thread.frames) >= thread.state.options.MaxFrames {
		return newResourceError("stack overflow")
	}
	layout, resourceError := thread.planFunctionCall(
		function,
		resultBase,
		resultBase,
		argumentCount,
		wantedResults,
	)
	if resourceError != nil {
		return resourceError
	}

	thread.reserveValues(layout.required)
	thread.reserveFrames(len(thread.frames) + 1)
	oldExtent := thread.liveValueExtent()
	thread.commitFunctionCall(function, layout, oldExtent)
	return nil
}

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
func (thread *Thread) tryEnterFixedLuaCall(
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
	function := (*Function)(callable.ref)
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

func (thread *Thread) pushFunctionMetamethodCall(
	function *Function,
	callBase int,
	argumentCount int,
	wantedResults int,
) *Error {
	if len(thread.frames) >= thread.state.options.MaxFrames {
		return newResourceError("stack overflow")
	}
	thread.checkFunctionCallWindow(
		function,
		callBase,
		argumentCount,
		wantedResults,
	)
	if argumentCount == int(^uint(0)>>1) {
		return newResourceError("value stack index overflow")
	}
	layout, resourceError := thread.planFunctionCallLayout(
		function,
		callBase,
		argumentCount+1,
		wantedResults,
	)
	if resourceError != nil {
		return resourceError
	}

	thread.reserveValues(layout.required)
	thread.reserveFrames(len(thread.frames) + 1)
	oldExtent := thread.liveValueExtent()
	thread.insertCallMetamethod(function, callBase, argumentCount)
	thread.commitFunctionCall(function, layout, oldExtent)
	return nil
}

func (thread *Thread) commitFunctionCall(
	function *Function,
	layout callLayout,
	oldExtent int,
) {
	thread.setupFunctionCall(function, layout)
	thread.publishFunctionCall(
		function,
		layout.base,
		layout.resultBase,
		layout.frameEnd,
		layout.wantedResults,
	)
	thread.clearDeadSuffix(oldExtent)
}

func (thread *Thread) publishFunctionCall(
	function *Function,
	base int,
	resultBase int,
	frameEnd int,
	wantedResults int,
) {
	frameIndex := len(thread.frames)
	thread.frames = thread.frames[:frameIndex+1]
	thread.frames[frameIndex] = activation{
		function:      function,
		base:          uint32(base),
		resultBase:    uint32(resultBase),
		callerExtent:  uint32(thread.frameExtent),
		wantedResults: int32(wantedResults),
	}
	thread.top = frameEnd
	if frameEnd > thread.frameExtent {
		thread.frameExtent = frameEnd
	}
}

func (thread *Thread) replaceFunctionCall(
	function *Function,
	callBase int,
	argumentCount int,
) *Error {
	if len(thread.frames) == 0 {
		panic("lua: tail call without an activation")
	}
	index := len(thread.frames) - 1
	current := thread.frames[index]
	layout, resourceError := thread.planFunctionCall(
		function,
		callBase,
		int(current.resultBase),
		argumentCount,
		int(current.wantedResults),
	)
	if resourceError != nil {
		return resourceError
	}

	thread.reserveValues(layout.required)
	oldExtent := thread.liveValueExtent()
	thread.commitTailCall(
		function,
		callBase,
		argumentCount,
		layout,
		oldExtent,
	)
	return nil
}

func (thread *Thread) replaceFunctionMetamethodCall(
	function *Function,
	callBase int,
	argumentCount int,
) *Error {
	if len(thread.frames) == 0 {
		panic("lua: tail call without an activation")
	}
	current := thread.frames[len(thread.frames)-1]
	thread.checkFunctionCallWindow(
		function,
		callBase,
		argumentCount,
		int(current.wantedResults),
	)
	if argumentCount == int(^uint(0)>>1) {
		return newResourceError("value stack index overflow")
	}
	layout, resourceError := thread.planFunctionCallLayout(
		function,
		int(current.resultBase),
		argumentCount+1,
		int(current.wantedResults),
	)
	if resourceError != nil {
		return resourceError
	}

	thread.reserveValues(layout.required)
	oldExtent := thread.liveValueExtent()
	thread.insertCallMetamethod(function, callBase, argumentCount)
	thread.commitTailCall(
		function,
		callBase,
		argumentCount+1,
		layout,
		oldExtent,
	)
	return nil
}

func (thread *Thread) commitTailCall(
	function *Function,
	callBase int,
	argumentCount int,
	layout callLayout,
	oldExtent int,
) {
	index := len(thread.frames) - 1
	current := thread.frames[index]
	thread.closeUpvalues(int(current.base))

	valueCount := argumentCount + 1
	copy(
		thread.values[layout.resultBase:layout.resultBase+valueCount],
		thread.values[callBase:callBase+valueCount],
	)
	thread.setupFunctionCall(function, layout)

	thread.frames[index] = activation{
		function:      function,
		base:          uint32(layout.base),
		resultBase:    current.resultBase,
		tailCalls:     saturatedIncrement(current.tailCalls),
		callerExtent:  current.callerExtent,
		wantedResults: current.wantedResults,
	}
	thread.top = layout.frameEnd
	thread.frameExtent = int(current.callerExtent)
	if layout.frameEnd > thread.frameExtent {
		thread.frameExtent = layout.frameEnd
	}
	thread.clearDeadSuffix(oldExtent)
}

func (thread *Thread) finishLuaCall(firstResult, resultCount int) {
	if len(thread.frames) == 0 {
		panic("lua: return without an activation")
	}
	if firstResult < 0 ||
		resultCount < 0 ||
		firstResult > len(thread.values) ||
		resultCount > len(thread.values)-firstResult {
		panic("lua: return result range is outside the value stack")
	}

	frameIndex := len(thread.frames) - 1
	frame := thread.frames[frameIndex]
	resultBase := int(frame.resultBase)
	wanted := int(frame.wantedResults)
	outputCount := resultCount
	if wanted != allResults {
		outputCount = wanted
	}
	if outputCount < 0 ||
		resultBase > len(thread.values) ||
		outputCount > len(thread.values)-resultBase {
		panic("lua: adjusted result range is outside the value stack")
	}

	newTop := resultBase + outputCount
	if wanted != allResults && frameIndex != 0 {
		caller := thread.frames[frameIndex-1]
		newTop = int(caller.base) + int(caller.function.prototype.registers)
	}
	thread.completeLuaReturn(
		firstResult,
		resultCount,
		outputCount,
		newTop,
	)
}

// finishNativeCall publishes results already written at resultBase by a
// terminal Frame method and removes the native activation.
func (thread *Thread) finishNativeCall(outputCount int) {
	if len(thread.frames) == 0 {
		panic("lua: native return without an activation")
	}
	frameIndex := len(thread.frames) - 1
	frame := thread.frames[frameIndex]
	if frame.function == nil ||
		frame.function.prototype != nil ||
		outputCount < 0 {
		panic("lua: invalid native return")
	}
	wanted := int(frame.wantedResults)
	if wanted != allResults && outputCount != wanted {
		panic("lua: native return count was not adjusted")
	}
	resultBase := int(frame.resultBase)
	if resultBase < 0 ||
		resultBase > len(thread.values) ||
		outputCount > len(thread.values)-resultBase {
		panic("lua: native return range is outside the value stack")
	}

	oldExtent := thread.liveValueExtent()
	thread.frames[frameIndex] = activation{}
	thread.frames = thread.frames[:frameIndex]
	thread.frameExtent = int(frame.callerExtent)

	newTop := resultBase + outputCount
	if wanted != allResults && frameIndex != 0 {
		caller := thread.frames[frameIndex-1]
		if prototype := caller.function.prototype; prototype != nil {
			newTop = int(caller.base) + int(prototype.registers)
		}
	}
	thread.top = newTop
	thread.clearDeadSuffix(oldExtent)
}

// tryCompleteFixedLuaReturn completes the common fixed-result nested return.
// The executor supplies a postDepth above its stop depth, which proves that a
// caller exists. False leaves the current activation unchanged for the
// checked executor path.
//
//go:noinline
func (thread *Thread) tryCompleteFixedLuaReturn(
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
func (thread *Thread) completeLuaReturn(
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

func (thread *Thread) unwindCalls(stopDepth int) {
	if stopDepth < 0 || stopDepth > len(thread.frames) {
		panic("lua: invalid call unwind depth")
	}
	if stopDepth == len(thread.frames) {
		return
	}

	oldExtent := thread.liveValueExtent()
	frameExtent := int(thread.frames[stopDepth].callerExtent)
	top := frameExtent
	for index := len(thread.frames) - 1; index >= stopDepth; index-- {
		thread.closeUpvalues(int(thread.frames[index].base))
		thread.frames[index] = activation{}
	}
	thread.frames = thread.frames[:stopDepth]
	for len(thread.continuations) != 0 {
		last := len(thread.continuations) - 1
		if int(thread.continuations[last].frameDepth) < stopDepth {
			break
		}
		if int(thread.continuations[last].frameDepth) == stopDepth {
			frameExtent = int(thread.continuations[last].savedExtent)
			top = int(thread.continuations[last].savedTop)
		}
		thread.continuations[last] = executionContinuation{}
		thread.continuations = thread.continuations[:last]
	}
	thread.frameExtent = frameExtent
	thread.top = top
	thread.clearDeadSuffix(oldExtent)
}

func (thread *Thread) planFunctionCall(
	function *Function,
	callBase int,
	resultBase int,
	argumentCount int,
	wantedResults int,
) (callLayout, *Error) {
	thread.checkFunctionCall(function, callBase, argumentCount, wantedResults)
	return thread.planFunctionCallLayout(
		function,
		resultBase,
		argumentCount,
		wantedResults,
	)
}

func (thread *Thread) planFunctionCallLayout(
	function *Function,
	resultBase int,
	argumentCount int,
	wantedResults int,
) (callLayout, *Error) {
	if resultBase < 0 {
		panic("lua: negative result base")
	}
	maxInt := int(^uint(0) >> 1)
	if resultBase == maxInt || argumentCount > maxInt-resultBase-1 {
		return callLayout{}, newResourceError("value stack index overflow")
	}

	argumentEnd := resultBase + 1 + argumentCount
	base := resultBase + 1
	frameEnd := argumentEnd
	if prototype := function.prototype; prototype != nil {
		if prototype.varargFlags&varargIsVararg != 0 {
			padded := argumentCount
			if parameters := int(prototype.parameters); padded < parameters {
				padded = parameters
			}
			argumentEnd = resultBase + 1 + padded
			base = argumentEnd
		}
		registers := int(prototype.registers)
		if registers > maxInt-base {
			return callLayout{}, newResourceError("value stack index overflow")
		}
		frameEnd = base + registers
	}
	required := argumentEnd
	if frameEnd > required {
		required = frameEnd
	}
	if wantedResults != allResults {
		if wantedResults > maxInt-resultBase {
			return callLayout{}, newResourceError("value stack index overflow")
		}
		if resultEnd := resultBase + wantedResults; resultEnd > required {
			required = resultEnd
		}
	}
	if required > thread.state.options.MaxValues ||
		uint64(required) > uint64(^uint32(0)) {
		return callLayout{}, newResourceError(
			"value stack limit of %d exceeded",
			thread.state.options.MaxValues,
		)
	}
	return callLayout{
		base:          base,
		resultBase:    resultBase,
		frameEnd:      frameEnd,
		required:      required,
		argumentCount: argumentCount,
		wantedResults: wantedResults,
	}, nil
}

func (thread *Thread) checkFunctionCall(
	function *Function,
	callBase int,
	argumentCount int,
	wantedResults int,
) {
	thread.checkFunctionCallWindow(
		function,
		callBase,
		argumentCount,
		wantedResults,
	)
	stackFunction := thread.values[callBase]
	if stackFunction.kind() != FunctionKind ||
		(*Function)(stackFunction.ref) != function {
		panic("lua: call slot does not contain the requested function")
	}
}

func (thread *Thread) checkFunctionCallWindow(
	function *Function,
	callBase int,
	argumentCount int,
	wantedResults int,
) {
	if thread == nil || thread.state == nil || thread.state.runtime == nil {
		panic("lua: call on an invalid thread")
	}
	if function == nil ||
		function.owner != thread.owner ||
		(function.prototype != nil && !function.prototype.sealed) ||
		(function.prototype == nil &&
			(function.body == nil ||
				function.nativeBodyUnchecked().entry == nil)) {
		panic("lua: call to an invalid function")
	}
	if callBase < 0 ||
		argumentCount < 0 ||
		callBase >= thread.top ||
		argumentCount > thread.top-callBase-1 {
		panic("lua: call range is outside the active value stack")
	}
	if wantedResults < allResults ||
		int64(wantedResults) > int64(^uint32(0)>>1) {
		panic("lua: invalid requested result count")
	}
}

func (thread *Thread) insertCallMetamethod(
	function *Function,
	callBase int,
	argumentCount int,
) {
	copy(
		thread.values[callBase+1:callBase+argumentCount+2],
		thread.values[callBase:callBase+argumentCount+1],
	)
	writeSlot(
		&thread.values[callBase],
		slotFromFunction(function),
	)
}

func (thread *Thread) setupFunctionCall(
	function *Function,
	layout callLayout,
) {
	if function.prototype == nil {
		return
	}
	prototype := function.prototype
	parameters := int(prototype.parameters)
	if prototype.varargFlags&varargIsVararg != 0 {
		arguments := layout.argumentCount
		if arguments < parameters {
			thread.fillNil(
				layout.resultBase+1+arguments,
				layout.resultBase+1+parameters,
			)
		}
		thread.fillNil(layout.base, layout.frameEnd)
		copy(
			thread.values[layout.base:layout.base+parameters],
			thread.values[layout.resultBase+1:layout.resultBase+1+parameters],
		)
		thread.fillNil(
			layout.resultBase+1,
			layout.resultBase+1+parameters,
		)
		if prototype.varargFlags&varargNeedsArg != 0 {
			writeSlot(
				&thread.values[layout.base+parameters],
				thread.makeLegacyArgTable(prototype, layout),
			)
		}
		return
	}

	thread.setupFixedLuaCall(
		prototype,
		layout.base,
		layout.frameEnd,
		layout.argumentCount,
	)
}

func (thread *Thread) setupFixedLuaCall(
	prototype *Prototype,
	base int,
	frameEnd int,
	argumentCount int,
) {
	parameters := int(prototype.parameters)
	preserved := parameters
	if preserved > argumentCount {
		preserved = argumentCount
	}
	thread.fillNil(base+preserved, frameEnd)
}

func (thread *Thread) makeLegacyArgTable(
	prototype *Prototype,
	layout callLayout,
) slot {
	extraCount := layout.argumentCount - int(prototype.parameters)
	if extraCount < 0 {
		extraCount = 0
	}
	table := newTable(thread.owner, extraCount, 1)
	first := layout.resultBase + 1 + int(prototype.parameters)
	for index := 0; index < extraCount; index++ {
		table.setInteger(index+1, thread.values[first+index])
	}
	name := thread.owner.strings.make("n")
	table.store.set(
		slotFromValue(stringValue(name)),
		slot{bits: math.Float64bits(float64(extraCount))},
		name.hash,
	)
	return slotFromTable(table)
}

func (thread *Thread) reserveValues(required int) {
	if required < 0 || required > thread.state.options.MaxValues {
		panic("lua: invalid value stack reservation")
	}
	if required <= len(thread.values) {
		return
	}
	if required <= cap(thread.values) {
		thread.values = thread.values[:required]
		return
	}

	limit := thread.state.options.MaxValues
	capacity := cap(thread.values)
	if capacity < 16 {
		capacity = 16
	}
	if capacity > limit {
		capacity = limit
	}
	for capacity < required {
		growth := capacity / 2
		if growth == 0 {
			growth = 1
		}
		if capacity > limit-growth {
			capacity = limit
			break
		}
		capacity += growth
	}
	if capacity < required {
		capacity = required
	}
	grown := make([]slot, required, capacity)
	copy(grown, thread.values)
	thread.values = grown
	for upvalue := thread.openUpvalues; upvalue != nil; upvalue = upvalue.next {
		upvalue.cell = &thread.values[upvalue.stackIndex()]
	}
}

func (thread *Thread) reserveFrames(required int) {
	if required < 0 || required > thread.state.options.MaxFrames {
		panic("lua: invalid call frame reservation")
	}
	if required <= cap(thread.frames) {
		return
	}

	limit := thread.state.options.MaxFrames
	capacity := cap(thread.frames)
	if capacity < 8 {
		capacity = 8
	}
	if capacity > limit {
		capacity = limit
	}
	for capacity < required {
		growth := capacity / 2
		if growth == 0 {
			growth = 1
		}
		if capacity > limit-growth {
			capacity = limit
			break
		}
		capacity += growth
	}
	if capacity < required {
		capacity = required
	}
	grown := make([]activation, len(thread.frames), capacity)
	copy(grown, thread.frames)
	thread.frames = grown
}

func (thread *Thread) fillNil(from, to int) {
	for index := from; index < to; index++ {
		thread.values[index] = nilSlot
	}
}

func (thread *Thread) clearInactive(from, to int) {
	if from < 0 {
		from = 0
	}
	if to > len(thread.values) {
		to = len(thread.values)
	}
	for index := from; index < to; index++ {
		thread.values[index] = slot{}
	}
}

func (thread *Thread) liveValueExtent() int {
	if thread.frameExtent > thread.top {
		return thread.frameExtent
	}
	return thread.top
}

func (thread *Thread) clearDeadSuffix(previousExtent int) {
	liveExtent := thread.liveValueExtent()
	if liveExtent < previousExtent {
		thread.clearInactive(liveExtent, previousExtent)
	}
}

func saturatedIncrement(value uint32) uint32 {
	if value != ^uint32(0) {
		value++
	}
	return value
}
