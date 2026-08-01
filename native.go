package lua

import (
	"errors"
	"fmt"
	"math"
)

const (
	maxNativeCaptures  = 255
	maxNativeCallDepth = 200
	nativeTerminalBit  = uint64(1) << 63
	nativeTokenMask    = nativeTerminalBit - 1
)

// ErrInvalidNativeFunction reports construction with a nil native entry.
var ErrInvalidNativeFunction = errors.New("lua: native function is nil")

// ErrNativeCaptureLimit reports a native function with more than 255
// captures.
var ErrNativeCaptureLimit = errors.New("lua: native function capture limit exceeded")

// NativeFunc is a Go function callable by Lua.
//
// The Frame is borrowed for the duration of the call and is the callback's
// active execution capability. A callback that calls Lua synchronously,
// directly or through helper code, must pass that Frame along and use its
// Call* methods. State.Call* methods are outer entry points and return
// ErrRunning while the callback is active.
//
// The callback must return an Outcome produced by that Frame. Retaining a
// Frame or using it after producing a terminal Outcome is a programming
// error. Go panics are propagated after the borrowed activation is removed.
// The Throw* methods are the protected Lua-error paths, while Yield* outcomes
// suspend a yieldable coroutine.
type NativeFunc func(Frame) Outcome

type nativeOutcomeKind uint8

const (
	nativeOutcomeInvalid nativeOutcomeKind = iota
	nativeOutcomeReturn
	nativeOutcomeError
	nativeOutcomeYield
)

// Outcome is the terminal result of a NativeFunc.
//
// Outcomes are bound to the Frame that produced them. The zero value and an
// Outcome returned from another invocation become Lua runtime failures. A
// successful Outcome does not retain the executing Thread or State object
// graph.
type Outcome struct {
	owner       *runtimeState
	failure     *Error
	token       uint64
	resultCount uint32
	kind        nativeOutcomeKind
}

// Frame is the borrowed activation and execution capability for one native
// call.
//
// Argument indexes are zero-based. Typed argument methods perform exact Lua
// type checks and do not coerce values. Call*, Index, and SetIndex continue
// execution reentrantly on the callback's Thread; equivalent State execution
// methods cannot reenter an already-running State. Owning Values and object
// handles read from a Frame may be retained, but the Frame itself is valid
// only until a terminal Return* or Yield* method is called, a Throw* method
// unwinds, or the NativeFunc returns.
type Frame struct {
	thread *threadObject
	token  uint64
	depth  int
}

// NewNativeFunction constructs a canonical native Function.
//
// Its initial environment is the currently executing Function's environment,
// or the main Thread's global environment outside a callback. State to carry
// alongside the function belongs in the Go closure; an owning Value held that
// way keeps its Lua object reachable.
func (state *State) NewNativeFunction(
	entry NativeFunc,
) (*Function, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, ErrInvalidNativeFunction
	}
	function := newNativeFunctionOwned(
		state,
		state.constructionEnvironment(),
		entry,
		nil,
	)
	return function.owningHandle(), nil
}

func (state *State) newNativeFunctionObject(
	entry NativeFunc,
	captures []slot,
) (*functionObject, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, ErrInvalidNativeFunction
	}
	if len(captures) > maxNativeCaptures {
		return nil, ErrNativeCaptureLimit
	}
	for _, capture := range captures {
		if err := state.runtime.acceptSlot(capture); err != nil {
			return nil, err
		}
	}
	return newNativeFunctionOwned(
		state,
		state.constructionEnvironment(),
		entry,
		captures,
	), nil
}

// ArgumentCount returns the number of supplied arguments.
func (frame Frame) ArgumentCount() int {
	call := frame.activation()
	return frame.thread.top - int(call.base)
}

// Argument returns argument index as an owning Value and whether it was
// supplied. A missing argument returns Lua nil and false. A negative index is
// a programming error.
func (frame Frame) Argument(index int) (Value, bool) {
	value, present := frame.argument(index)
	return value.owningValue(), present
}

// Kind returns the exact Lua kind of argument index. A missing argument has
// InvalidKind, which distinguishes it from an explicit Lua nil.
func (frame Frame) Kind(index int) Kind {
	value, present := frame.argument(index)
	if !present {
		return InvalidKind
	}
	return value.kind()
}

// IsMissingOrNil reports whether argument index was omitted or is Lua nil.
//
// It is the common primitive for optional arguments: initialize a Go default,
// then read and validate the argument only when IsMissingOrNil returns false.
func (frame Frame) IsMissingOrNil(index int) bool {
	value, present := frame.argument(index)
	return !present || value.isNil()
}

// Bool returns argument index and whether it is exactly a Lua boolean.
func (frame Frame) Bool(index int) (bool, bool) {
	value, present := frame.argument(index)
	if !present {
		return false, false
	}
	switch value.ref {
	case falseMarkerPointer:
		return false, true
	case trueMarkerPointer:
		return true, true
	default:
		return false, false
	}
}

// Number returns argument index and whether it is exactly a Lua number.
func (frame Frame) Number(index int) (float64, bool) {
	value, present := frame.argument(index)
	if !present || value.ref != nil {
		return 0, false
	}
	return math.Float64frombits(value.bits), true
}

// CoerceNumber returns argument index as a Lua number.
//
// Exact numbers pass through. Strings are accepted only when their complete
// contents match Lunar's deterministic Lua numeric grammar. No metamethod is
// invoked.
func (frame Frame) CoerceNumber(index int) (float64, bool) {
	value, present := frame.argument(index)
	if !present {
		return 0, false
	}
	return slotToNumber(value)
}

// Integer returns argument index as an int64 when it is exactly a finite,
// integral Lua number representable by int64.
//
// Integer does not accept numeric strings, truncate fractions, or saturate
// values outside the int64 range.
func (frame Frame) Integer(index int) (int64, bool) {
	number, ok := frame.Number(index)
	if !ok ||
		math.IsNaN(number) ||
		math.IsInf(number, 0) ||
		math.Trunc(number) != number ||
		number < -0x1p63 ||
		number >= 0x1p63 {
		return 0, false
	}
	return int64(number), true
}

// IntegerInRange returns argument index as an int64 when Integer accepts it
// and it lies in the inclusive range [minimum, maximum].
//
// An inverted range rejects every value.
func (frame Frame) IntegerInRange(
	index int,
	minimum int64,
	maximum int64,
) (int64, bool) {
	value, ok := frame.Integer(index)
	if !ok || minimum > maximum || value < minimum || value > maximum {
		return 0, false
	}
	return value, true
}

// String returns argument index and whether it is exactly a Lua string.
func (frame Frame) String(index int) (string, bool) {
	value, present := frame.argument(index)
	if !present || !value.isString() {
		return "", false
	}
	return stringSlotText(value), true
}

// CoerceString returns argument index as a Lua string.
//
// Exact strings pass through and numbers use Lua's primitive number spelling.
// Other kinds are rejected and no metamethod is invoked.
func (frame Frame) CoerceString(index int) (string, bool) {
	value, present := frame.argument(index)
	if !present {
		return "", false
	}
	return compactText(value)
}

// Table returns argument index and whether it is exactly a Lua table.
func (frame Frame) Table(index int) (*Table, bool) {
	value, present := frame.argument(index)
	if !present || !value.isTable() {
		return nil, false
	}
	return tableHandleFromSlot(value), true
}

func (frame Frame) tableObject(index int) (*tableObject, bool) {
	value, present := frame.argument(index)
	if !present || !value.isTable() {
		return nil, false
	}
	return tableObjectFromSlot(value), true
}

// Function returns argument index and whether it is exactly a function.
func (frame Frame) Function(index int) (*Function, bool) {
	value, present := frame.argument(index)
	if !present || !value.isFunction() {
		return nil, false
	}
	return functionHandleFromSlot(value), true
}

func (frame Frame) functionObject(index int) (*functionObject, bool) {
	value, present := frame.argument(index)
	if !present || !value.isFunction() {
		return nil, false
	}
	return functionObjectFromSlot(value), true
}

// UserData returns argument index and whether it is exactly Lua userdata.
func (frame Frame) UserData(index int) (*UserData, bool) {
	value, present := frame.argument(index)
	if !present || !value.isUserData() {
		return nil, false
	}
	return userDataHandleFromSlot(value), true
}

func (frame Frame) userDataObject(index int) (*userDataObject, bool) {
	value, present := frame.argument(index)
	if !present || !value.isUserData() {
		return nil, false
	}
	return userDataObjectFromSlot(value), true
}

// Thread returns argument index and whether it is exactly a Lua thread.
func (frame Frame) Thread(index int) (*Thread, bool) {
	value, present := frame.argument(index)
	if !present || !value.isThread() {
		return nil, false
	}
	return threadHandleFromSlot(value), true
}

func (frame Frame) threadObject(index int) (*threadObject, bool) {
	value, present := frame.argument(index)
	if !present || !value.isThread() {
		return nil, false
	}
	return threadObjectFromSlot(value), true
}

// State returns the State that owns this callback.
//
// The returned State is useful for State-bound construction and loading, but
// it is not the callback's execution capability. State.Call* returns
// ErrRunning while the callback is active; use this Frame's matching Call*
// method for synchronous Lua reentry.
func (frame Frame) State() *State {
	frame.activation()
	return frame.thread.state
}

// CurrentThread returns the Thread executing this callback.
func (frame Frame) CurrentThread() *Thread {
	frame.activation()
	return frame.thread.owningHandle()
}

func (frame Frame) environmentObject() *tableObject {
	return frame.activation().function.environment
}

func (frame Frame) globalEnvironmentObject() *tableObject {
	frame.activation()
	return frame.thread.globals
}

// Return completes the callback without results.
func (frame Frame) Return() Outcome {
	call := frame.activation()
	outputCount, failure := frame.prepareResults(call, 0)
	if failure != nil {
		return frame.sealError(failure)
	}
	resultBase := int(call.resultBase)
	frame.thread.fillNil(resultBase, resultBase+outputCount)
	return frame.sealReturn(outputCount)
}

// ReturnValue completes the callback with one owning Value.
func (frame Frame) ReturnValue(value Value) Outcome {
	call := frame.activation()
	if err := frame.thread.owner.accept(value); err != nil {
		panic(err)
	}
	outputCount, failure := frame.prepareResults(call, 1)
	if failure != nil {
		return frame.sealError(failure)
	}
	if outputCount != 0 {
		resultBase := int(call.resultBase)
		writeSlot(
			&frame.thread.values[resultBase],
			frame.thread.owner.importAcceptedSlot(slotFromValue(value)),
		)
		frame.thread.fillNil(resultBase+1, resultBase+outputCount)
	}
	return frame.sealReturn(outputCount)
}

// ReturnValues completes the callback with values.
//
// Values are validated before any result slot is changed. The caller's
// requested result count is applied before the compact result window is
// written.
func (frame Frame) ReturnValues(values ...Value) Outcome {
	call := frame.activation()
	for _, value := range values {
		if err := frame.thread.owner.accept(value); err != nil {
			panic(err)
		}
	}
	outputCount, failure := frame.prepareResults(call, len(values))
	if failure != nil {
		return frame.sealError(failure)
	}
	resultBase := int(call.resultBase)
	copied := len(values)
	if copied > outputCount {
		copied = outputCount
	}
	for index := 0; index < copied; index++ {
		writeSlot(
			&frame.thread.values[resultBase+index],
			frame.thread.owner.importAcceptedSlot(
				slotFromValue(values[index]),
			),
		)
	}
	frame.thread.fillNil(
		resultBase+copied,
		resultBase+outputCount,
	)
	return frame.sealReturn(outputCount)
}

// ReturnArguments completes the callback by returning every supplied argument
// in order without materializing it as owning Values.
func (frame Frame) ReturnArguments() Outcome {
	call := frame.activation()
	base := int(call.base)
	return frame.returnCompactValues(
		[2]slot{},
		0,
		frame.thread.values[base:frame.thread.top],
	)
}

// ReturnNil completes the callback with one Lua nil result.
func (frame Frame) ReturnNil() Outcome {
	call := frame.activation()
	return frame.returnOne(call, nilSlot)
}

// ReturnBool completes the callback with one Lua boolean result.
func (frame Frame) ReturnBool(value bool) Outcome {
	call := frame.activation()
	result := falseSlot
	if value {
		result = trueSlot
	}
	return frame.returnOne(call, result)
}

// ReturnNumber completes the callback with one Lua number result.
func (frame Frame) ReturnNumber(value float64) Outcome {
	call := frame.activation()
	return frame.returnOne(call, numberSlot(value))
}

// ReturnString completes the callback with one Lua string result.
func (frame Frame) ReturnString(value string) Outcome {
	call := frame.activation()
	outputCount, failure := frame.prepareResults(call, 1)
	if failure != nil {
		return frame.sealError(failure)
	}
	if outputCount != 0 {
		writeSlot(
			&frame.thread.values[int(call.resultBase)],
			stringSlot(frame.thread.owner.strings.make(value)),
		)
		frame.thread.fillNil(
			int(call.resultBase)+1,
			int(call.resultBase)+outputCount,
		)
	}
	return frame.sealReturn(outputCount)
}

func (frame Frame) returnStringBytes(value []byte) Outcome {
	call := frame.activation()
	outputCount, failure := frame.prepareResults(call, 1)
	if failure != nil {
		return frame.sealError(failure)
	}
	if outputCount != 0 {
		writeSlot(
			&frame.thread.values[int(call.resultBase)],
			stringSlot(frame.thread.owner.strings.makeBytes(value)),
		)
		frame.thread.fillNil(
			int(call.resultBase)+1,
			int(call.resultBase)+outputCount,
		)
	}
	return frame.sealReturn(outputCount)
}

// resultWriter publishes a native result window one compact value at a time,
// allowing variable scalar result counts without constructing a slice.
type resultWriter struct {
	thread      *threadObject
	base        int
	outputCount int
	written     int
}

func (frame Frame) beginResults(supplied int) (resultWriter, *Error) {
	call := frame.activation()
	outputCount, failure := frame.prepareResults(call, supplied)
	if failure != nil {
		return resultWriter{}, failure
	}
	return resultWriter{
		thread:      frame.thread,
		base:        int(call.resultBase),
		outputCount: outputCount,
	}, nil
}

func (writer *resultWriter) put(value slot) {
	if writer.written < writer.outputCount {
		writeSlot(
			&writer.thread.values[writer.base+writer.written],
			value,
		)
	}
	writer.written++
}

func (frame Frame) finishResults(writer *resultWriter) Outcome {
	written := writer.written
	if written > writer.outputCount {
		written = writer.outputCount
	}
	writer.thread.fillNil(
		writer.base+written,
		writer.base+writer.outputCount,
	)
	return frame.sealReturn(writer.outputCount)
}

// Yield suspends the executing coroutine without yielded values.
//
// The borrowed Frame becomes invalid immediately. Yielding from the main
// Thread, across another native call, or across a metamethod or iterator
// boundary produces Lua 5.1's ordinary illegal-yield error instead.
func (frame Frame) Yield() Outcome {
	call := frame.activation()
	resultBase, previousTop, previousExtent, failure :=
		frame.prepareYield(call, 0)
	if failure != nil {
		return frame.sealError(failure)
	}
	frame.finishYield(call, resultBase, 0, previousTop, previousExtent)
	return frame.sealYield(0)
}

// YieldValue suspends the executing coroutine with one owning Value.
func (frame Frame) YieldValue(value Value) Outcome {
	call := frame.activation()
	if err := frame.thread.owner.accept(value); err != nil {
		panic(err)
	}
	resultBase, previousTop, previousExtent, failure :=
		frame.prepareYield(call, 1)
	if failure != nil {
		return frame.sealError(failure)
	}
	writeSlot(
		&frame.thread.values[resultBase],
		frame.thread.owner.importAcceptedSlot(slotFromValue(value)),
	)
	frame.finishYield(call, resultBase, 1, previousTop, previousExtent)
	return frame.sealYield(1)
}

// YieldValues suspends the executing coroutine with values.
//
// Values are validated before the execution stack is changed. Unlike a
// return, yielded values are not adjusted to the caller's requested result
// count; that adjustment applies later to the arguments supplied at resume.
func (frame Frame) YieldValues(values ...Value) Outcome {
	call := frame.activation()
	for _, value := range values {
		if err := frame.thread.owner.accept(value); err != nil {
			panic(err)
		}
	}
	resultBase, previousTop, previousExtent, failure :=
		frame.prepareYield(call, len(values))
	if failure != nil {
		return frame.sealError(failure)
	}
	for index, value := range values {
		writeSlot(
			&frame.thread.values[resultBase+index],
			frame.thread.owner.importAcceptedSlot(slotFromValue(value)),
		)
	}
	frame.finishYield(
		call,
		resultBase,
		len(values),
		previousTop,
		previousExtent,
	)
	return frame.sealYield(len(values))
}

// YieldArguments suspends the executing coroutine with every argument passed
// to this native call. It transfers compact slots directly and does not
// materialize owning Values.
func (frame Frame) YieldArguments() Outcome {
	call := frame.activation()
	argumentBase := int(call.base)
	argumentCount := frame.thread.top - argumentBase
	resultBase, previousTop, previousExtent, failure :=
		frame.prepareYield(call, argumentCount)
	if failure != nil {
		return frame.sealError(failure)
	}
	copy(
		frame.thread.values[resultBase:resultBase+argumentCount],
		frame.thread.values[argumentBase:argumentBase+argumentCount],
	)
	frame.finishYield(
		call,
		resultBase,
		argumentCount,
		previousTop,
		previousExtent,
	)
	return frame.sealYield(argumentCount)
}

func (frame Frame) raise(value Value) Outcome {
	frame.activation()
	if err := frame.thread.owner.accept(value); err != nil {
		panic(err)
	}
	return frame.sealError(&Error{
		value:       value,
		description: value.String(),
		category:    RuntimeError,
	})
}

func (frame Frame) raiseCompact(value slot) Outcome {
	frame.activation()
	if err := frame.thread.owner.acceptSlot(value); err != nil {
		panic(err)
	}
	return frame.sealError(&Error{
		compactValue:    value,
		description:     value.diagnosticString(),
		category:        RuntimeError,
		hasCompactValue: true,
	})
}

func (frame Frame) argError(index int, reason string) Outcome {
	frame.activation()
	if index < 0 {
		panic("lua: negative native argument index")
	}
	return frame.raiseString(fmt.Sprintf(
		"bad argument #%d (%s)",
		index+1,
		reason,
	))
}

func (frame Frame) argTypeError(index int, expected ...Kind) Outcome {
	call := frame.activation()
	if index < 0 {
		panic("lua: negative native argument index")
	}
	if len(expected) == 0 {
		panic("lua: missing expected argument kind")
	}
	var seen uint16
	expectedText := ""
	for expectedIndex, kind := range expected {
		if kind <= InvalidKind || kind > TableKind {
			panic("lua: invalid expected argument kind")
		}
		bit := uint16(1) << uint(kind)
		if seen&bit != 0 {
			panic("lua: duplicate expected argument kind")
		}
		seen |= bit
		switch {
		case expectedIndex == 0:
		case expectedIndex == len(expected)-1 && len(expected) == 2:
			expectedText += " or "
		case expectedIndex == len(expected)-1:
			expectedText += ", or "
		default:
			expectedText += ", "
		}
		expectedText += kind.String()
	}
	actual := "no value"
	count := frame.thread.top - int(call.base)
	if index < count {
		actual = frame.thread.values[int(call.base)+index].kind().String()
	}
	return frame.argError(index, fmt.Sprintf(
		"%s expected, got %s",
		expectedText,
		actual,
	))
}

// Throw raises an arbitrary Lua error Value and does not return.
//
// A native callback ends in exactly one of three ways: it returns a Return*
// Outcome, it returns a Yield* Outcome, or it throws. Throwing rather than
// returning a failure lets a helper called at any depth inside the callback
// report the error, which is where argument checks usually live.
//
// Throw unwinds with a private panic that Lunar recovers at the native call
// boundary, so the thrown callback reaches that boundary in the same state a
// returned one would. Host code between the Throw and the NativeFunc must not
// recover it; a deferred recover that swallows unknown panics strands the
// callback and is reported as an invalid outcome.
//
// Throw and its siblings return nothing, so they are written as statements.
// A guard clause reads "if !ok { frame.ThrowArgTypeError(0, lua.StringKind) }"
// and the callback continues below it only when the check passed.
func (frame Frame) Throw(value Value) {
	frame.throw(frame.raise(value))
}

// ThrowString raises a string Lua error and does not return. See Throw.
func (frame Frame) ThrowString(message string) {
	frame.activation()
	frame.throw(frame.raiseString(message))
}

// ThrowError raises err as a Lua error and does not return.
//
// The Go error is preserved as the cause, so errors.Is and errors.As still
// find it on the *Error a protected caller receives. See Throw.
func (frame Frame) ThrowError(err error) {
	frame.throw(frame.raiseError(err))
}

// Rethrow propagates a *Error returned by a nested Frame operation without
// losing its Value, category, or nested traceback, and does not return.
// See Throw.
func (frame Frame) Rethrow(failure *Error) {
	frame.throw(frame.reraise(failure))
}

// ThrowArgError raises a Lua argument error and does not return.
//
// index is zero-based. It may name a missing argument. See Throw.
func (frame Frame) ThrowArgError(index int, reason string) {
	frame.throw(frame.argError(index, reason))
}

// ThrowArgTypeError raises a Lua argument-type error and does not return.
//
// index is zero-based and may name a missing argument. At least one distinct
// expected kind is required. See Throw.
func (frame Frame) ThrowArgTypeError(index int, expected ...Kind) {
	frame.throw(frame.argTypeError(index, expected...))
}

// throw abandons the Go stack carrying an already sealed terminal Outcome.
// Sealing happens in the private raise that produced it, so a thrown callback
// and a returned one reach invokeNativeCall in the same state.
func (frame Frame) throw(outcome Outcome) {
	panic(nativeThrow{token: frame.token, outcome: outcome})
}

// nativeThrow carries a Frame.Throw outcome out of a callback. Only the
// boundary that opened the matching token recovers it; every other panic,
// including a nativeThrow belonging to an outer frame, keeps unwinding.
type nativeThrow struct {
	token   uint64
	outcome Outcome
}

func (frame Frame) raiseString(message string) Outcome {
	value := frame.thread.state.String(message)
	return frame.sealError(&Error{
		value:       value,
		description: message,
		category:    RuntimeError,
	})
}

func (frame Frame) argument(index int) (slot, bool) {
	call := frame.activation()
	if index < 0 {
		panic("lua: negative native argument index")
	}
	count := frame.thread.top - int(call.base)
	if index >= count {
		return nilSlot, false
	}
	return frame.thread.values[int(call.base)+index], true
}

func (frame Frame) activation() *activation {
	if frame.thread == nil ||
		frame.token == 0 ||
		frame.token&nativeTerminalBit != 0 ||
		frame.thread.activeNativeToken != frame.token ||
		frame.depth <= 0 ||
		frame.depth != len(frame.thread.frames) {
		panic("lua: stale or terminal native frame")
	}
	call := &frame.thread.frames[frame.depth-1]
	if call.function == nil ||
		call.function.prototype != nil ||
		call.function.body == nil {
		panic("lua: stale or invalid native frame")
	}
	return call
}

func (frame Frame) checkCaptureIndex(function *functionObject, index int) {
	if index < 0 ||
		index >= len(function.nativeBodyUnchecked().captures) {
		panic("lua: native capture index out of range")
	}
}

func (frame Frame) nativeCapture(index int) slot {
	call := frame.activation()
	frame.checkCaptureIndex(call.function, index)
	return call.function.nativeBodyUnchecked().captures[index]
}

func (frame Frame) returnOne(call *activation, value slot) Outcome {
	outputCount, failure := frame.prepareResults(call, 1)
	if failure != nil {
		return frame.sealError(failure)
	}
	resultBase := int(call.resultBase)
	if outputCount != 0 {
		writeSlot(&frame.thread.values[resultBase], value)
		frame.thread.fillNil(resultBase+1, resultBase+outputCount)
	}
	return frame.sealReturn(outputCount)
}

func (frame Frame) prepareResults(
	call *activation,
	supplied int,
) (int, *Error) {
	if supplied < 0 {
		panic("lua: negative native result count")
	}
	outputCount := supplied
	if wanted := int(call.wantedResults); wanted != allResults {
		outputCount = wanted
	}
	resultBase := int(call.resultBase)
	limit := frame.thread.valueLimit()
	if outputCount < 0 ||
		resultBase < 0 ||
		resultBase > limit ||
		outputCount > limit-resultBase ||
		uint64(resultBase)+uint64(outputCount) > uint64(^uint32(0)) {
		return 0, newResourceError(
			"value stack limit of %d exceeded",
			limit,
		)
	}
	required := resultBase + outputCount
	frame.thread.reserveValues(required)
	if required > frame.thread.top {
		frame.thread.top = required
	}
	if required > frame.thread.frameExtent {
		frame.thread.frameExtent = required
	}
	return outputCount, nil
}

func (frame Frame) prepareYield(
	call *activation,
	supplied int,
) (resultBase, previousTop, previousExtent int, failure *Error) {
	if supplied < 0 {
		panic("lua: negative native yield count")
	}
	thread := frame.thread
	if thread.isMain() ||
		thread.status != ThreadRunning ||
		thread.nativeCallDepth != 1 ||
		len(thread.continuations) != 0 {
		return 0, 0, 0, &Error{
			value: thread.state.String(
				"attempt to yield across metamethod/C-call boundary",
			),
			description: "attempt to yield across metamethod/C-call boundary",
			category:    RuntimeError,
		}
	}
	resultBase = int(call.resultBase)
	limit := thread.valueLimit()
	if resultBase < 0 ||
		resultBase > limit ||
		supplied > limit-resultBase ||
		uint64(resultBase)+uint64(supplied) > uint64(^uint32(0)) {
		return 0, 0, 0, newResourceError(
			"value stack limit of %d exceeded",
			limit,
		)
	}
	previousTop = thread.top
	previousExtent = thread.liveValueExtent()
	thread.reserveValues(resultBase + supplied)
	return resultBase, previousTop, previousExtent, nil
}

func (frame Frame) finishYield(
	call *activation,
	resultBase int,
	supplied int,
	previousTop int,
	previousExtent int,
) {
	thread := frame.thread
	resultEnd := resultBase + supplied
	thread.clearInactive(resultEnd, previousTop)
	thread.top = resultEnd
	thread.frameExtent = int(call.callerExtent)
	if resultEnd > thread.frameExtent {
		thread.frameExtent = resultEnd
	}
	thread.clearDeadSuffix(previousExtent)
}

func (frame Frame) sealReturn(outputCount int) Outcome {
	frame.seal()
	return Outcome{
		owner:       frame.thread.owner,
		token:       frame.token,
		resultCount: uint32(outputCount),
		kind:        nativeOutcomeReturn,
	}
}

func (frame Frame) sealYield(outputCount int) Outcome {
	frame.seal()
	return Outcome{
		owner:       frame.thread.owner,
		token:       frame.token,
		resultCount: uint32(outputCount),
		kind:        nativeOutcomeYield,
	}
}

func (frame Frame) sealError(failure *Error) Outcome {
	if failure == nil {
		panic("lua: nil native failure")
	}
	frame.thread.state.rememberExitRequest(failure)
	frame.seal()
	return Outcome{
		owner:   frame.thread.owner,
		failure: failure,
		token:   frame.token,
		kind:    nativeOutcomeError,
	}
}

func (frame Frame) seal() {
	if frame.thread.activeNativeToken != frame.token {
		panic("lua: stale or terminal native frame")
	}
	frame.thread.activeNativeToken = frame.token | nativeTerminalBit
}

func invokeNativeCall(thread *threadObject) (failure *Error) {
	if thread == nil ||
		thread.state == nil ||
		thread.owner == nil ||
		len(thread.frames) == 0 {
		panic("lua: invalid native callback entry")
	}
	parentToken := thread.activeNativeToken
	switch {
	case parentToken == 0 && thread.nativeCallDepth != 0:
		panic("lua: native callback depth has no active token")
	case parentToken != 0 &&
		(parentToken&nativeTerminalBit != 0 || thread.nativeCallDepth == 0):
		panic("lua: invalid parent native callback")
	case thread.owner.nativeCallDepth < thread.nativeCallDepth:
		panic("lua: thread native depth exceeds runtime depth")
	case int(thread.owner.nativeCallDepth) >= thread.nativeCallLimit():
		return newResourceError("C stack overflow")
	}
	call := &thread.frames[len(thread.frames)-1]
	function := call.function
	if function == nil ||
		function.prototype != nil ||
		function.body == nil {
		panic("lua: invalid native function activation")
	}
	body := function.nativeBodyUnchecked()
	if body.entry == nil {
		panic("lua: invalid native function activation")
	}

	token := thread.nextNativeToken()
	thread.activeNativeToken = token
	thread.nativeCallDepth++
	thread.owner.nativeCallDepth++
	callbackReturned := false
	nativeFrameDepth := len(thread.frames) - 1
	defer func() {
		// Frame.Throw unwinds to here. Recovering in this defer rather
		// than in a wrapper around the callback keeps the native call
		// path at one defer, and callbackReturned gates the recover so a
		// callback that returned normally never pays for it: reaching
		// here without it means a panic is in flight.
		var propagate any
		if !callbackReturned {
			recovered := recover()
			if thrown, ok := recovered.(nativeThrow); ok &&
				thrown.token == token {
				// Mirror the returned-error path, whose outcome this
				// already is: the frames stay for the failure to unwind,
				// and a pending exit outranks the callback's failure.
				// One divergence: the returned path polls the context
				// after the callback, so an expired context overrides
				// the callback's error there but not here. The next
				// instruction-level poll fires the same ContextError, so
				// the difference is a few instructions of visibility,
				// not outcome.
				callbackReturned = true
				failure = thrown.outcome.failure
				if pending := thread.state.execution.pendingExit; pending != nil {
					failure = pending
				}
			} else {
				propagate = recovered
			}
		}
		thread.activeNativeToken = parentToken
		thread.nativeCallDepth--
		thread.owner.nativeCallDepth--
		if !callbackReturned {
			thread.unwindCalls(nativeFrameDepth)
		}
		if propagate != nil {
			panic(propagate)
		}
	}()

	outcome := body.entry(Frame{
		thread: thread,
		token:  token,
		depth:  len(thread.frames),
	})
	callbackReturned = true
	if failure := thread.state.execution.pendingExit; failure != nil {
		return failure
	}
	if failure := validateNativeOutcome(
		thread,
		outcome,
		token,
		nativeFrameDepth,
	); failure != nil {
		return failure
	}
	if thread.contextBudget != 0 {
		if failure := pollExecutionContext(thread); failure != nil {
			return failure
		}
	}

	switch outcome.kind {
	case nativeOutcomeReturn:
		thread.finishNativeCall(int(outcome.resultCount))
		return nil
	case nativeOutcomeError:
		return outcome.failure
	case nativeOutcomeYield:
		thread.status = ThreadSuspended
		return nil
	default:
		panic("lua: validated invalid native outcome")
	}
}

func validateNativeOutcome(
	thread *threadObject,
	outcome Outcome,
	token uint64,
	nativeFrameDepth int,
) *Error {
	if outcome.owner != thread.owner ||
		outcome.token != token ||
		thread.activeNativeToken != token|nativeTerminalBit {
		return newNativeRuntimeError(
			thread,
			"native function returned an invalid outcome",
		)
	}
	valid := false
	switch outcome.kind {
	case nativeOutcomeReturn:
		valid = outcome.failure == nil
	case nativeOutcomeError:
		valid = outcome.failure != nil &&
			outcome.resultCount == 0
	case nativeOutcomeYield:
		call := &thread.frames[nativeFrameDepth]
		resultBase := int(call.resultBase)
		valid = outcome.failure == nil &&
			resultBase >= 0 &&
			resultBase <= thread.top &&
			int(outcome.resultCount) == thread.top-resultBase
	default:
		valid = false
	}
	if valid {
		return nil
	}
	return newNativeRuntimeError(
		thread,
		"native function returned an invalid outcome",
	)
}

func (thread *threadObject) nextNativeToken() uint64 {
	if thread.owner.nativeSequence >= nativeTokenMask {
		panic("lua: native callback token space exhausted")
	}
	thread.owner.nativeSequence++
	return thread.owner.nativeSequence
}

func newNativeRuntimeError(thread *threadObject, message string) *Error {
	return &Error{
		value:       thread.state.String(message),
		description: message,
		category:    RuntimeError,
	}
}
