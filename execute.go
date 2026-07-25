package lua

import "fmt"

type executionResultKind uint8

const (
	executionReturned executionResultKind = iota
	executionFailed
)

type executionResult struct {
	err  *Error
	kind executionResultKind
}

// execute runs Lua activations until the stack returns to stopDepth. It keeps
// the current frame's hot state in Go locals and publishes only at seams that
// can replace a frame, grow the value stack, or leave the executor.
func execute(thread *Thread, stopDepth int) executionResult {
	if thread == nil ||
		stopDepth < 0 ||
		stopDepth > len(thread.frames) {
		panic("lua: invalid executor entry")
	}
	if len(thread.frames) == stopDepth {
		return executionResult{kind: executionReturned}
	}

reload:
	for {
		frameIndex := len(thread.frames) - 1
		frame := thread.frames[frameIndex]
		function := frame.function
		prototype := function.prototype
		values := thread.values
		base := int(frame.base)
		pc := int(frame.pc)
		code := prototype.code
		constants := prototype.constants
		upvalues := function.upvalues

		for {
			instructionPC := pc
			current := code[pc]
			pc++

			switch current.opcode() {
			case opMove:
				writeSlot(
					&values[base+current.a()],
					values[base+current.b()],
				)

			case opLoadK:
				writeSlot(
					&values[base+current.a()],
					constants[current.bx()],
				)

			case opLoadBool:
				value := falseSlot
				if current.b() != 0 {
					value = trueSlot
				}
				writeSlot(&values[base+current.a()], value)
				if current.c() != 0 {
					pc++
				}

			case opLoadNil:
				for register := current.a(); register <= current.b(); register++ {
					values[base+register] = nilSlot
				}

			case opGetUpvalue:
				writeSlot(
					&values[base+current.a()],
					upvalues[current.b()].read(),
				)

			case opSetUpvalue:
				upvalues[current.b()].write(values[base+current.a()])

			case opNot:
				source := values[base+current.b()]
				result := source.ref == nilMarkerPointer ||
					source.ref == falseMarkerPointer
				if result {
					writeSlot(&values[base+current.a()], trueSlot)
				} else {
					writeSlot(&values[base+current.a()], falseSlot)
				}

			case opJump:
				pc += current.sbx()

			case opTest:
				source := values[base+current.a()]
				truth := source.ref != nilMarkerPointer &&
					source.ref != falseMarkerPointer
				if truth == (current.c() != 0) {
					jump := code[pc]
					pc++
					pc += jump.sbx()
				} else {
					pc++
				}

			case opTestSet:
				source := values[base+current.b()]
				truth := source.ref != nilMarkerPointer &&
					source.ref != falseMarkerPointer
				if truth == (current.c() != 0) {
					writeSlot(&values[base+current.a()], source)
					jump := code[pc]
					pc++
					pc += jump.sbx()
				} else {
					pc++
				}

			case opCall:
				callBase := base + current.a()
				argumentCount := current.b() - 1
				if current.b() == 0 {
					argumentCount = thread.top - callBase - 1
				}
				wantedResults := current.c() - 1
				thread.frames[frameIndex].pc = uint32(pc)

				callee, direct := luaFunctionSlot(values[callBase])
				if !direct {
					callErr := enterLuaCallMetamethod(
						thread,
						frameIndex,
						instructionPC,
						callBase,
						argumentCount,
						wantedResults,
						false,
					)
					if callErr != nil {
						return failExecution(
							thread,
							stopDepth,
							callErr,
						)
					}
					continue reload
				}
				if callErr := thread.pushLuaCall(
					callee,
					callBase,
					argumentCount,
					wantedResults,
				); callErr != nil {
					return failExecution(thread, stopDepth, callErr)
				}
				continue reload

			case opTailCall:
				callBase := base + current.a()
				argumentCount := current.b() - 1
				if current.b() == 0 {
					argumentCount = thread.top - callBase - 1
				}
				thread.frames[frameIndex].pc = uint32(pc)

				callee, direct := luaFunctionSlot(values[callBase])
				if !direct {
					callErr := enterLuaCallMetamethod(
						thread,
						frameIndex,
						instructionPC,
						callBase,
						argumentCount,
						0,
						true,
					)
					if callErr != nil {
						return failExecution(
							thread,
							stopDepth,
							callErr,
						)
					}
					continue reload
				}
				if callErr := thread.replaceLuaCall(
					callee,
					callBase,
					argumentCount,
				); callErr != nil {
					return failExecution(thread, stopDepth, callErr)
				}
				continue reload

			case opReturn:
				firstResult := base + current.a()
				resultCount := current.b() - 1
				if current.b() == 0 {
					resultCount = thread.top - firstResult
				}
				thread.frames[frameIndex].pc = uint32(pc)
				thread.finishLuaCall(firstResult, resultCount)
				if len(thread.frames) == stopDepth {
					return executionResult{kind: executionReturned}
				}
				continue reload

			case opClose:
				thread.closeUpvalues(base + current.a())

			case opClosure:
				pc = installClosure(thread, frameIndex, current, pc)

			case opVararg:
				parameters := int(prototype.parameters)
				extraCount := frame.varargCount()
				firstExtra := int(frame.resultBase) + 1 + parameters
				resultCount := current.b() - 1
				if current.b() == 0 {
					resultCount = extraCount
					destination := base + current.a()
					required := uint64(destination) + uint64(resultCount)
					if required > uint64(len(values)) {
						thread.frames[frameIndex].pc = uint32(pc)
						if failure := prepareOpenVararg(
							thread,
							destination,
							resultCount,
						); failure != nil {
							return failExecution(
								thread,
								stopDepth,
								failure,
							)
						}
						values = thread.values
					}
					thread.top = int(required)
				}
				copied := resultCount
				if copied > extraCount {
					copied = extraCount
				}
				destination := base + current.a()
				copy(
					values[destination:destination+copied],
					values[firstExtra:firstExtra+copied],
				)
				thread.fillNil(
					destination+copied,
					destination+resultCount,
				)

			default:
				thread.frames[frameIndex].pc = uint32(pc)
				return failExecution(
					thread,
					stopDepth,
					newExecutionRuntimeError(
						thread,
						frameIndex,
						instructionPC,
						"opcode %s is not executable yet",
						current.opcode(),
					),
				)
			}
		}
	}
}

// Keep allocating closure construction out of the instruction loop's frame.
//
//go:noinline
func installClosure(
	thread *Thread,
	frameIndex int,
	code instruction,
	bindingPC int,
) int {
	frame := thread.frames[frameIndex]
	parent := frame.function
	prototype := parent.prototype
	child := prototype.children[code.bx()]
	count := int(child.upvalues)
	var bindings []*upvalue
	if count != 0 {
		bindings = make([]*upvalue, count)
	}
	for index := 0; index < count; index++ {
		binding := prototype.code[bindingPC+index]
		if binding.opcode() == opMove {
			bindings[index] = thread.captureUpvalue(
				int(frame.base) + binding.b(),
			)
		} else {
			bindings[index] = parent.upvalues[binding.b()]
		}
	}
	closure := newLuaFunctionOwned(
		thread.owner,
		child,
		parent.environment,
		bindings,
	)
	writeSlot(
		&thread.values[int(frame.base)+code.a()],
		slotFromValue(closure.Value()),
	)
	return bindingPC + count
}

// Keep uncommon stack growth and resource errors off the open-vararg path.
//
//go:noinline
func prepareOpenVararg(
	thread *Thread,
	destination int,
	resultCount int,
) *Error {
	limit := thread.state.options.MaxValues
	if destination < 0 ||
		resultCount < 0 ||
		destination > limit ||
		resultCount > limit-destination {
		return newResourceError(
			"lua: value stack limit of %d exceeded",
			limit,
		)
	}
	required := destination + resultCount
	if uint64(required) > uint64(^uint32(0)) {
		return newResourceError(
			"lua: value stack limit of %d exceeded",
			limit,
		)
	}
	thread.reserveValues(required)
	return nil
}

func luaFunctionSlot(value slot) (*Function, bool) {
	if value.kind() != FunctionKind {
		return nil, false
	}
	return (*Function)(value.ref), true
}

func luaCallMetamethod(thread *Thread, value slot) *Function {
	var metatable *Table
	switch value.kind() {
	case TableKind:
		metatable = (*Table)(value.ref).metatable
	case UserDataKind:
		metatable = (*UserData)(value.ref).metatable
	case FunctionKind:
		return nil
	default:
		metatable = thread.state.typeMetatables[value.kind()]
	}
	if metatable == nil {
		return nil
	}
	metamethod := slotFromValue(metatable.RawGetString("__call"))
	function, _ := luaFunctionSlot(metamethod)
	return function
}

// Keep call-metamethod lookup and call-window insertion off direct calls.
//
//go:noinline
func enterLuaCallMetamethod(
	thread *Thread,
	frameIndex int,
	instructionPC int,
	callBase int,
	argumentCount int,
	wantedResults int,
	tail bool,
) *Error {
	called := thread.values[callBase]
	function := luaCallMetamethod(thread, called)
	if function == nil {
		return newExecutionRuntimeError(
			thread,
			frameIndex,
			instructionPC,
			"attempt to call a %s value",
			called.kind(),
		)
	}
	if tail {
		return thread.replaceLuaMetamethodCall(
			function,
			callBase,
			argumentCount,
		)
	}
	return thread.pushLuaMetamethodCall(
		function,
		callBase,
		argumentCount,
		wantedResults,
	)
}

func newExecutionRuntimeError(
	thread *Thread,
	frameIndex int,
	pc int,
	format string,
	arguments ...any,
) *Error {
	message := fmt.Sprintf(format, arguments...)
	prototype := thread.frames[frameIndex].function.prototype
	source := sourceID(prototype.SourceName())
	line := prototype.LineAt(pc)
	if line != 0 {
		message = fmt.Sprintf("%s:%d: %s", source, line, message)
	} else {
		message = fmt.Sprintf("%s: %s", source, message)
	}
	return &Error{
		value:       thread.state.String(message),
		description: message,
		category:    RuntimeError,
	}
}

func failExecution(
	thread *Thread,
	stopDepth int,
	failure *Error,
) executionResult {
	if failure == nil {
		panic("lua: executor failed without an error")
	}
	if len(failure.traceback) == 0 {
		failure.traceback = executionTraceback(thread, stopDepth)
	}
	thread.unwindLuaCalls(stopDepth)
	return executionResult{
		kind: executionFailed,
		err:  failure,
	}
}

func executionTraceback(thread *Thread, stopDepth int) []TraceFrame {
	traceback := make([]TraceFrame, 0, len(thread.frames)-stopDepth)
	for index := len(thread.frames) - 1; index >= stopDepth; index-- {
		frame := &thread.frames[index]
		prototype := frame.function.prototype
		pc := int(frame.pc) - 1
		traceback = append(traceback, TraceFrame{
			Source:    prototype.SourceName(),
			Line:      prototype.LineAt(pc),
			TailCalls: frame.tailCalls,
		})
	}
	return traceback
}
