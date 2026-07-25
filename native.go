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
// The Frame is borrowed for the duration of the call. The callback must
// return an Outcome produced by that Frame. Retaining a Frame or using it
// after producing a terminal Outcome is a programming error. Go panics are
// propagated after the borrowed activation is removed; Raise and ArgError
// are the protected Lua-error paths.
type NativeFunc func(Frame) Outcome

type nativeOutcomeKind uint8

const (
	nativeOutcomeInvalid nativeOutcomeKind = iota
	nativeOutcomeReturn
	nativeOutcomeError
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

// Frame is a borrowed view of one native call.
//
// Argument indexes are zero-based. Typed argument methods perform exact Lua
// type checks and do not coerce values. Owning Values and object handles read
// from a Frame may be retained, but the Frame itself is valid only until a
// terminal Return, Raise, or ArgError method is called, or until the
// NativeFunc returns.
type Frame struct {
	thread *Thread
	token  uint64
	depth  int
}

// NewNativeFunction constructs a canonical native Function with optional
// captured Values.
//
// Captures are copied into the Function's compact private storage. Reference
// Values must belong to state; immutable strings and scalar Values are
// State-neutral.
func (state *State) NewNativeFunction(
	entry NativeFunc,
	captures ...Value,
) (*Function, error) {
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
		if err := state.runtime.accept(capture); err != nil {
			return nil, err
		}
	}

	var compact []slot
	if len(captures) != 0 {
		compact = make([]slot, len(captures))
		for index, capture := range captures {
			compact[index] = slotFromValue(capture)
		}
	}
	return newNativeFunctionOwned(
		state.runtime,
		state.globals,
		entry,
		compact,
	), nil
}

// ArgumentCount returns the number of supplied arguments.
func (frame Frame) ArgumentCount() int {
	call := frame.call()
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

// String returns argument index and whether it is exactly a Lua string.
func (frame Frame) String(index int) (string, bool) {
	value, present := frame.argument(index)
	if !present || value.kind() != StringKind {
		return "", false
	}
	return (*luaString)(value.ref).text, true
}

// Table returns argument index and whether it is exactly a Lua table.
func (frame Frame) Table(index int) (*Table, bool) {
	value, present := frame.argument(index)
	if !present || value.kind() != TableKind {
		return nil, false
	}
	return (*Table)(value.ref), true
}

// Function returns argument index and whether it is exactly a function.
func (frame Frame) Function(index int) (*Function, bool) {
	value, present := frame.argument(index)
	if !present || value.kind() != FunctionKind {
		return nil, false
	}
	return (*Function)(value.ref), true
}

// UserData returns argument index and whether it is exactly Lua userdata.
func (frame Frame) UserData(index int) (*UserData, bool) {
	value, present := frame.argument(index)
	if !present || value.kind() != UserDataKind {
		return nil, false
	}
	return (*UserData)(value.ref), true
}

// LuaThread returns argument index and whether it is exactly a Lua thread.
//
// The name distinguishes an argument containing a Thread from Thread, which
// returns the Thread executing this callback.
func (frame Frame) LuaThread(index int) (*Thread, bool) {
	value, present := frame.argument(index)
	if !present || value.kind() != ThreadKind {
		return nil, false
	}
	return (*Thread)(value.ref), true
}

// State returns the State executing this callback.
func (frame Frame) State() *State {
	frame.call()
	return frame.thread.state
}

// Thread returns the Thread executing this callback.
func (frame Frame) Thread() *Thread {
	frame.call()
	return frame.thread
}

// Environment returns the executing native Function's Lua 5.1 environment.
func (frame Frame) Environment() *Table {
	return frame.call().function.environment
}

// CaptureCount returns the native Function's fixed capture count.
func (frame Frame) CaptureCount() int {
	call := frame.call()
	return len(call.function.nativeBodyUnchecked().captures)
}

// Capture returns captured Value index. An out-of-range index is a
// programming error.
func (frame Frame) Capture(index int) Value {
	call := frame.call()
	frame.checkCaptureIndex(call.function, index)
	return call.function.nativeBodyUnchecked().captures[index].owningValue()
}

// SetCapture replaces captured Value index.
//
// An invalid, foreign, or out-of-range Value is a programming error.
func (frame Frame) SetCapture(index int, value Value) {
	call := frame.call()
	frame.checkCaptureIndex(call.function, index)
	if err := frame.thread.owner.accept(value); err != nil {
		panic(err)
	}
	writeSlot(
		&call.function.nativeBodyUnchecked().captures[index],
		slotFromValue(value),
	)
}

// Return completes the callback without results.
func (frame Frame) Return() Outcome {
	call := frame.call()
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
	call := frame.call()
	if err := frame.thread.owner.accept(value); err != nil {
		panic(err)
	}
	return frame.returnOne(call, slotFromValue(value))
}

// ReturnValues completes the callback with values.
//
// Values are validated before any result slot is changed. The caller's
// requested result count is applied before the compact result window is
// written.
func (frame Frame) ReturnValues(values ...Value) Outcome {
	call := frame.call()
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
			slotFromValue(values[index]),
		)
	}
	frame.thread.fillNil(
		resultBase+copied,
		resultBase+outputCount,
	)
	return frame.sealReturn(outputCount)
}

// ReturnNil completes the callback with one Lua nil result.
func (frame Frame) ReturnNil() Outcome {
	call := frame.call()
	return frame.returnOne(call, nilSlot)
}

// ReturnBool completes the callback with one Lua boolean result.
func (frame Frame) ReturnBool(value bool) Outcome {
	call := frame.call()
	result := falseSlot
	if value {
		result = trueSlot
	}
	return frame.returnOne(call, result)
}

// ReturnNumber completes the callback with one Lua number result.
func (frame Frame) ReturnNumber(value float64) Outcome {
	call := frame.call()
	return frame.returnOne(call, numberSlot(value))
}

// ReturnString completes the callback with one Lua string result.
func (frame Frame) ReturnString(value string) Outcome {
	call := frame.call()
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

// Raise completes the callback with an arbitrary Lua error Value.
func (frame Frame) Raise(value Value) Outcome {
	frame.call()
	if err := frame.thread.owner.accept(value); err != nil {
		panic(err)
	}
	return frame.sealError(&Error{
		value:       value,
		description: value.String(),
		category:    RuntimeError,
	})
}

// RaiseString completes the callback with a string Lua error.
func (frame Frame) RaiseString(message string) Outcome {
	frame.call()
	return frame.raiseString(message)
}

// ArgError completes the callback with a Lua argument error.
//
// index is zero-based. It may name a missing argument.
func (frame Frame) ArgError(index int, reason string) Outcome {
	frame.call()
	if index < 0 {
		panic("lua: negative native argument index")
	}
	return frame.raiseString(fmt.Sprintf(
		"bad argument #%d (%s)",
		index+1,
		reason,
	))
}

// ArgTypeError completes the callback with a Lua argument-type error.
//
// index is zero-based. It may name a missing argument. InvalidKind is not a
// valid expected kind.
func (frame Frame) ArgTypeError(index int, expected Kind) Outcome {
	call := frame.call()
	if index < 0 {
		panic("lua: negative native argument index")
	}
	if expected <= InvalidKind || expected > TableKind {
		panic("lua: invalid expected argument kind")
	}
	actual := "no value"
	count := frame.thread.top - int(call.base)
	if index < count {
		actual = frame.thread.values[int(call.base)+index].kind().String()
	}
	return frame.ArgError(index, fmt.Sprintf(
		"%s expected, got %s",
		expected,
		actual,
	))
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
	call := frame.call()
	if index < 0 {
		panic("lua: negative native argument index")
	}
	count := frame.thread.top - int(call.base)
	if index >= count {
		return nilSlot, false
	}
	return frame.thread.values[int(call.base)+index], true
}

func (frame Frame) call() *activation {
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

func (frame Frame) checkCaptureIndex(function *Function, index int) {
	if index < 0 ||
		index >= len(function.nativeBodyUnchecked().captures) {
		panic("lua: native capture index out of range")
	}
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

func (frame Frame) sealReturn(outputCount int) Outcome {
	frame.seal()
	return Outcome{
		owner:       frame.thread.owner,
		token:       frame.token,
		resultCount: uint32(outputCount),
		kind:        nativeOutcomeReturn,
	}
}

func (frame Frame) sealError(failure *Error) Outcome {
	if failure == nil {
		panic("lua: nil native failure")
	}
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

func invokeNativeCall(thread *Thread) *Error {
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
	case int(thread.nativeCallDepth) >= thread.nativeCallLimit():
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
	callbackReturned := false
	nativeFrameDepth := len(thread.frames) - 1
	defer func() {
		thread.activeNativeToken = parentToken
		thread.nativeCallDepth--
		if !callbackReturned {
			thread.unwindCalls(nativeFrameDepth)
		}
	}()

	outcome := body.entry(Frame{
		thread: thread,
		token:  token,
		depth:  len(thread.frames),
	})
	callbackReturned = true
	if outcome.owner != thread.owner ||
		outcome.token != token ||
		thread.activeNativeToken != token|nativeTerminalBit {
		return newNativeRuntimeError(
			thread,
			"native function returned an invalid outcome",
		)
	}

	switch outcome.kind {
	case nativeOutcomeReturn:
		if outcome.failure != nil {
			return newNativeRuntimeError(
				thread,
				"native function returned an invalid outcome",
			)
		}
		thread.finishNativeCall(int(outcome.resultCount))
		return nil
	case nativeOutcomeError:
		if outcome.failure == nil || outcome.resultCount != 0 {
			return newNativeRuntimeError(
				thread,
				"native function returned an invalid outcome",
			)
		}
		return outcome.failure
	default:
		return newNativeRuntimeError(
			thread,
			"native function returned an invalid outcome",
		)
	}
}

func (thread *Thread) nextNativeToken() uint64 {
	if thread.owner.nativeSequence >= nativeTokenMask {
		panic("lua: native callback token space exhausted")
	}
	thread.owner.nativeSequence++
	return thread.owner.nativeSequence
}

func newNativeRuntimeError(thread *Thread, message string) *Error {
	return &Error{
		value:       thread.state.String(message),
		description: message,
		category:    RuntimeError,
	}
}
