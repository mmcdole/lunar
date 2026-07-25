package lua

import "math"

const allResults = -1

// activation is the complete persistent state of one Lua call. The executor
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

type luaCallLayout struct {
	base          int
	resultBase    int
	frameEnd      int
	required      int
	argumentCount int
	wantedResults int
}

func (thread *Thread) pushLuaCall(
	function *Function,
	resultBase int,
	argumentCount int,
	wantedResults int,
) *Error {
	if len(thread.frames) >= thread.state.options.MaxFrames {
		return newResourceError(
			"lua: call frame limit of %d exceeded",
			thread.state.options.MaxFrames,
		)
	}
	layout, resourceError := thread.planLuaCall(
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
	thread.commitLuaCall(function, layout, oldExtent)
	return nil
}

func (thread *Thread) pushLuaMetamethodCall(
	function *Function,
	callBase int,
	argumentCount int,
	wantedResults int,
) *Error {
	if len(thread.frames) >= thread.state.options.MaxFrames {
		return newResourceError(
			"lua: call frame limit of %d exceeded",
			thread.state.options.MaxFrames,
		)
	}
	thread.checkLuaCallWindow(
		function,
		callBase,
		argumentCount,
		wantedResults,
	)
	if argumentCount == int(^uint(0)>>1) {
		return newResourceError("lua: value stack index overflow")
	}
	layout, resourceError := thread.planLuaCallLayout(
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
	thread.commitLuaCall(function, layout, oldExtent)
	return nil
}

func (thread *Thread) commitLuaCall(
	function *Function,
	layout luaCallLayout,
	oldExtent int,
) {
	callerExtent := thread.frameExtent
	thread.setupLuaCall(function, layout)
	thread.frames = append(thread.frames, activation{
		function:      function,
		base:          uint32(layout.base),
		resultBase:    uint32(layout.resultBase),
		callerExtent:  uint32(callerExtent),
		wantedResults: int32(layout.wantedResults),
	})
	thread.top = layout.frameEnd
	if layout.frameEnd > thread.frameExtent {
		thread.frameExtent = layout.frameEnd
	}
	thread.clearDeadSuffix(oldExtent)
}

func (thread *Thread) replaceLuaCall(
	function *Function,
	callBase int,
	argumentCount int,
) *Error {
	if len(thread.frames) == 0 {
		panic("lua: tail call without an activation")
	}
	index := len(thread.frames) - 1
	current := thread.frames[index]
	layout, resourceError := thread.planLuaCall(
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
	thread.commitLuaTailCall(
		function,
		callBase,
		argumentCount,
		layout,
		oldExtent,
	)
	return nil
}

func (thread *Thread) replaceLuaMetamethodCall(
	function *Function,
	callBase int,
	argumentCount int,
) *Error {
	if len(thread.frames) == 0 {
		panic("lua: tail call without an activation")
	}
	current := thread.frames[len(thread.frames)-1]
	thread.checkLuaCallWindow(
		function,
		callBase,
		argumentCount,
		int(current.wantedResults),
	)
	if argumentCount == int(^uint(0)>>1) {
		return newResourceError("lua: value stack index overflow")
	}
	layout, resourceError := thread.planLuaCallLayout(
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
	thread.commitLuaTailCall(
		function,
		callBase,
		argumentCount+1,
		layout,
		oldExtent,
	)
	return nil
}

func (thread *Thread) commitLuaTailCall(
	function *Function,
	callBase int,
	argumentCount int,
	layout luaCallLayout,
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
	thread.setupLuaCall(function, layout)

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

	oldExtent := thread.liveValueExtent()

	thread.closeUpvalues(int(frame.base))
	copyCount := resultCount
	if copyCount > outputCount {
		copyCount = outputCount
	}
	copy(
		thread.values[resultBase:resultBase+copyCount],
		thread.values[firstResult:firstResult+copyCount],
	)
	thread.fillNil(resultBase+copyCount, resultBase+outputCount)

	thread.frames[frameIndex] = activation{}
	thread.frames = thread.frames[:frameIndex]
	thread.frameExtent = int(frame.callerExtent)

	newTop := resultBase + outputCount
	if wanted != allResults && len(thread.frames) != 0 {
		caller := thread.frames[len(thread.frames)-1]
		newTop = int(caller.base) + int(caller.function.prototype.registers)
	}
	thread.top = newTop
	thread.clearDeadSuffix(oldExtent)
}

func (thread *Thread) unwindLuaCalls(stopDepth int) {
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

func (thread *Thread) planLuaCall(
	function *Function,
	callBase int,
	resultBase int,
	argumentCount int,
	wantedResults int,
) (luaCallLayout, *Error) {
	thread.checkLuaCall(function, callBase, argumentCount, wantedResults)
	return thread.planLuaCallLayout(
		function,
		resultBase,
		argumentCount,
		wantedResults,
	)
}

func (thread *Thread) planLuaCallLayout(
	function *Function,
	resultBase int,
	argumentCount int,
	wantedResults int,
) (luaCallLayout, *Error) {
	if resultBase < 0 {
		panic("lua: negative result base")
	}
	maxInt := int(^uint(0) >> 1)
	if resultBase == maxInt || argumentCount > maxInt-resultBase-1 {
		return luaCallLayout{}, newResourceError("lua: value stack index overflow")
	}

	argumentEnd := resultBase + 1 + argumentCount
	base := resultBase + 1
	if function.prototype.varargFlags&varargIsVararg != 0 {
		padded := argumentCount
		if parameters := int(function.prototype.parameters); padded < parameters {
			padded = parameters
		}
		argumentEnd = resultBase + 1 + padded
		base = argumentEnd
	}
	registers := int(function.prototype.registers)
	if registers > maxInt-base {
		return luaCallLayout{}, newResourceError("lua: value stack index overflow")
	}
	frameEnd := base + registers
	required := argumentEnd
	if frameEnd > required {
		required = frameEnd
	}
	if wantedResults != allResults {
		if wantedResults > maxInt-resultBase {
			return luaCallLayout{}, newResourceError("lua: value stack index overflow")
		}
		if resultEnd := resultBase + wantedResults; resultEnd > required {
			required = resultEnd
		}
	}
	if required > thread.state.options.MaxValues ||
		uint64(required) > uint64(^uint32(0)) {
		return luaCallLayout{}, newResourceError(
			"lua: value stack limit of %d exceeded",
			thread.state.options.MaxValues,
		)
	}
	return luaCallLayout{
		base:          base,
		resultBase:    resultBase,
		frameEnd:      frameEnd,
		required:      required,
		argumentCount: argumentCount,
		wantedResults: wantedResults,
	}, nil
}

func (thread *Thread) checkLuaCall(
	function *Function,
	callBase int,
	argumentCount int,
	wantedResults int,
) {
	thread.checkLuaCallWindow(
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

func (thread *Thread) checkLuaCallWindow(
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
		function.prototype == nil ||
		!function.prototype.sealed {
		panic("lua: call to an invalid Lua function")
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

func (thread *Thread) setupLuaCall(
	function *Function,
	layout luaCallLayout,
) {
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

	preserved := parameters
	arguments := layout.argumentCount
	if preserved > arguments {
		preserved = arguments
	}
	thread.fillNil(layout.base+preserved, layout.frameEnd)
}

func (thread *Thread) makeLegacyArgTable(
	prototype *Prototype,
	layout luaCallLayout,
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
