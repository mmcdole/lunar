package lua

import "math"

// nativeDirectFunc is an optional internal entry for a small library builtin.
//
// It receives the fixed argument window of a Lua CALL and returns the
// builtin's first result. It must have no side effect other than string
// creation through the runtime's pool, and it must return false, before any
// side effect, whenever the ordinary NativeFunc could do anything other than
// return exactly one value: wrong or coercible argument types, errors, or
// multiple results. A false return sends the call down the ordinary native
// path unchanged, so error messages, coercions and tracebacks stay identical.
//
// The entry belongs to the canonical library function object. A host that
// replaces or wraps a library global installs a different function that has
// no direct entry, so the replacement is always called normally.
type nativeDirectFunc func(thread *threadObject, arguments []slot) (slot, bool)

type directCallResult uint8

const (
	directCallMissed directCallResult = iota
	directCallDone
	directCallCollect
)

// tryDirectNativeCall completes a fixed-argument, at-most-one-result CALL of
// a native function with a direct entry without building an activation.
//
// It reproduces every observable check of the ordinary path that could make
// the call behave differently: the frame and native call-depth limits
// (which would fail with a resource error), a pending os.exit (which the
// ordinary path reports after the callback), context polling (the ordinary
// path polls when a budget is active; here a due poll is left to it), and
// collection servicing after allocation (returned as directCallCollect so the
// driver services it at the same safe point the native return would).
// frameTop is the caller's register extent; the ordinary fixed return resets
// thread.top to it, so a differing top is left to the ordinary path.
//
//go:noinline
func (thread *threadObject) tryDirectNativeCall(
	callerBase int,
	frameTop int,
	code instruction,
	callable slot,
) directCallResult {
	argumentField := code.b()
	resultField := code.c()
	if argumentField == 0 || resultField == 0 || resultField > 2 {
		return directCallMissed
	}
	direct := (*functionObject)(callable.ref).nativeBodyUnchecked().direct
	if direct == nil ||
		thread.top != frameTop ||
		len(thread.frames) >= thread.frameLimit() ||
		int(thread.owner.nativeCallDepth) >= thread.nativeCallLimit() ||
		thread.state.execution.pendingExit != nil {
		return directCallMissed
	}
	budget := thread.contextBudget
	if budget == 1 {
		return directCallMissed
	}
	callBase := callerBase + code.a()
	result, ok := direct(
		thread,
		thread.values[callBase+1:callBase+argumentField],
	)
	if !ok {
		return directCallMissed
	}
	if budget != 0 {
		thread.contextBudget = budget - 1
	}
	if resultField == 2 {
		writeSlot(&thread.values[callBase], result)
	}
	if thread.owner.collection.runnable {
		return directCallCollect
	}
	return directCallDone
}

func directNumber(value slot) (float64, bool) {
	if value.ref != nil {
		return 0, false
	}
	return math.Float64frombits(value.bits), true
}

// directOptionalPosition reads an optional position argument that is absent,
// nil, or an actual number. Strings coerce in the ordinary path and miss here.
func directOptionalPosition(
	arguments []slot,
	index int,
	fallback int64,
) (int64, bool) {
	if index >= len(arguments) {
		return fallback, true
	}
	value := arguments[index]
	if value.ref == nilMarkerPointer {
		return fallback, true
	}
	number, ok := directNumber(value)
	if !ok {
		return 0, false
	}
	return saturatingInt64(number), true
}

func directUnaryMath(
	arguments []slot,
	operation func(float64) float64,
) (slot, bool) {
	if len(arguments) == 0 {
		return slot{}, false
	}
	number, ok := directNumber(arguments[0])
	if !ok {
		return slot{}, false
	}
	return numberSlot(operation(number)), true
}

func mathFloorDirect(_ *threadObject, arguments []slot) (slot, bool) {
	return directUnaryMath(arguments, math.Floor)
}

func mathCeilDirect(_ *threadObject, arguments []slot) (slot, bool) {
	return directUnaryMath(arguments, math.Ceil)
}

func mathAbsDirect(_ *threadObject, arguments []slot) (slot, bool) {
	return directUnaryMath(arguments, math.Abs)
}

func mathSqrtDirect(_ *threadObject, arguments []slot) (slot, bool) {
	return directUnaryMath(arguments, math.Sqrt)
}

func mathMaxDirect(_ *threadObject, arguments []slot) (slot, bool) {
	if len(arguments) == 0 {
		return slot{}, false
	}
	largest, ok := directNumber(arguments[0])
	if !ok {
		return slot{}, false
	}
	for _, argument := range arguments[1:] {
		number, valid := directNumber(argument)
		if !valid {
			return slot{}, false
		}
		if number > largest {
			largest = number
		}
	}
	return numberSlot(largest), true
}

func mathMinDirect(_ *threadObject, arguments []slot) (slot, bool) {
	if len(arguments) == 0 {
		return slot{}, false
	}
	smallest, ok := directNumber(arguments[0])
	if !ok {
		return slot{}, false
	}
	for _, argument := range arguments[1:] {
		number, valid := directNumber(argument)
		if !valid {
			return slot{}, false
		}
		if number < smallest {
			smallest = number
		}
	}
	return numberSlot(smallest), true
}

func stringLenDirect(_ *threadObject, arguments []slot) (slot, bool) {
	if len(arguments) == 0 || !arguments[0].isString() {
		return slot{}, false
	}
	return numberSlot(float64(stringSlotLen(arguments[0]))), true
}

// stringSubDirect mirrors stringSub, including its choice of string
// constructor for each result shape.
func stringSubDirect(thread *threadObject, arguments []slot) (slot, bool) {
	if len(arguments) < 2 || !arguments[0].isString() {
		return slot{}, false
	}
	text := stringSlotText(arguments[0])
	number, ok := directNumber(arguments[1])
	if !ok {
		return slot{}, false
	}
	first := saturatingInt64(number)
	last, ok := directOptionalPosition(arguments, 2, -1)
	if !ok {
		return slot{}, false
	}

	start := relativePosition(first, len(text))
	end := relativePosition(last, len(text))
	if start < 1 {
		start = 1
	}
	if end > int64(len(text)) {
		end = int64(len(text))
	}
	pool := &thread.owner.strings
	if start > end {
		return stringSlot(pool.make("")), true
	}
	if start == 1 && end == int64(len(text)) {
		return stringSlot(pool.make(text)), true
	}
	return stringSlot(pool.makeBorrowed(text[start-1 : end])), true
}

// stringByteDirect handles string.byte calls that produce zero or one byte.
// Zero results adjust to nil in a one-result CALL.
func stringByteDirect(_ *threadObject, arguments []slot) (slot, bool) {
	if len(arguments) == 0 || !arguments[0].isString() {
		return slot{}, false
	}
	text := stringSlotText(arguments[0])
	first, ok := directOptionalPosition(arguments, 1, 1)
	if !ok {
		return slot{}, false
	}
	first = relativePosition(first, len(text))
	last := first
	if len(arguments) > 2 && arguments[2].ref != nilMarkerPointer {
		supplied, valid := directNumber(arguments[2])
		if !valid {
			return slot{}, false
		}
		last = relativePosition(saturatingInt64(supplied), len(text))
	}
	if first < 1 {
		first = 1
	}
	if last > int64(len(text)) {
		last = int64(len(text))
	}
	if first > last {
		return nilSlot, true
	}
	if last != first {
		return slot{}, false
	}
	return numberSlot(float64(text[first-1])), true
}

// stringCharDirect handles the single-argument form, which returns one of the
// canonical single-byte strings and never allocates.
func stringCharDirect(_ *threadObject, arguments []slot) (slot, bool) {
	if len(arguments) != 1 {
		return slot{}, false
	}
	number, ok := directNumber(arguments[0])
	if !ok {
		return slot{}, false
	}
	value := libraryInteger(number)
	if value < 0 || value > 255 {
		return slot{}, false
	}
	return stringSlot(singleByteStringRef(byte(value))), true
}
