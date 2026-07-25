package lua

import (
	"fmt"
	"math"
)

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
driver:
	for len(thread.frames) > stopDepth {
		if len(thread.continuations) != 0 {
			last := len(thread.continuations) - 1
			depth := int(thread.continuations[last].frameDepth)
			if depth == len(thread.frames) {
				if failure := resumeExecutionContinuation(thread); failure != nil {
					return failExecution(thread, stopDepth, failure)
				}
			} else if depth > len(thread.frames) {
				panic("lua: orphaned execution continuation")
			}
		}
		for {
			current := runInstructions(thread, stopDepth)
			switch current.opcode() {
			case opGetGlobal, opGetTable, opSelf,
				opGetGlobalMiss, opGetTableMiss, opSelfMiss:
				frameIndex := len(thread.frames) - 1
				if failure := slowTableGet(
					thread,
					frameIndex,
					current,
				); failure != nil {
					return failExecution(thread, stopDepth, failure)
				}
			case opSetGlobal, opSetTable,
				opSetGlobalMiss, opSetTableMiss:
				frameIndex := len(thread.frames) - 1
				if failure := slowTableSet(
					thread,
					frameIndex,
					current,
				); failure != nil {
					return failExecution(thread, stopDepth, failure)
				}
			case opNewTable:
				executeNewTable(
					thread,
					len(thread.frames)-1,
					current,
				)
			case opAdd, opSub, opMul, opDiv, opMod, opPow, opUnaryMinus:
				frameIndex := len(thread.frames) - 1
				if failure := slowArithmetic(
					thread,
					frameIndex,
					current,
				); failure != nil {
					return failExecution(thread, stopDepth, failure)
				}
			case opLength:
				frameIndex := len(thread.frames) - 1
				if failure := slowLength(
					thread,
					frameIndex,
					current,
				); failure != nil {
					return failExecution(thread, stopDepth, failure)
				}
			case opConcat:
				frameIndex := len(thread.frames) - 1
				if failure := slowConcat(
					thread,
					frameIndex,
					current,
				); failure != nil {
					return failExecution(thread, stopDepth, failure)
				}
			case opEqual:
				frameIndex := len(thread.frames) - 1
				if failure := slowEquality(
					thread,
					frameIndex,
					current,
				); failure != nil {
					return failExecution(thread, stopDepth, failure)
				}
			case opLessThan, opLessEqual:
				frameIndex := len(thread.frames) - 1
				if failure := slowOrder(
					thread,
					frameIndex,
					current,
				); failure != nil {
					return failExecution(thread, stopDepth, failure)
				}
			case opForPrep:
				frameIndex := len(thread.frames) - 1
				if failure := prepareNumericFor(
					thread,
					frameIndex,
					current,
				); failure != nil {
					return failExecution(thread, stopDepth, failure)
				}
			case opIteratorLoop:
				frameIndex := len(thread.frames) - 1
				if failure := startIteratorCall(
					thread,
					frameIndex,
					current,
				); failure != nil {
					return failExecution(thread, stopDepth, failure)
				}
			case opCall:
				frameIndex := len(thread.frames) - 1
				frame := thread.frames[frameIndex]
				callBase := int(frame.base) + current.a()
				argumentCount := current.b() - 1
				if current.b() == 0 {
					argumentCount = thread.top - callBase - 1
				}
				wantedResults := current.c() - 1
				callee, direct := luaFunctionSlot(thread.values[callBase])
				if !direct {
					failure := enterLuaCallMetamethod(
						thread,
						frameIndex,
						int(frame.pc)-1,
						callBase,
						argumentCount,
						wantedResults,
						false,
					)
					if failure != nil {
						return failExecution(thread, stopDepth, failure)
					}
					continue
				}
				if failure := thread.pushLuaCall(
					callee,
					callBase,
					argumentCount,
					wantedResults,
				); failure != nil {
					return failExecution(thread, stopDepth, failure)
				}
			case opTailCall:
				frameIndex := len(thread.frames) - 1
				frame := thread.frames[frameIndex]
				callBase := int(frame.base) + current.a()
				argumentCount := current.b() - 1
				if current.b() == 0 {
					argumentCount = thread.top - callBase - 1
				}
				callee, direct := luaFunctionSlot(thread.values[callBase])
				if !direct {
					failure := enterLuaCallMetamethod(
						thread,
						frameIndex,
						int(frame.pc)-1,
						callBase,
						argumentCount,
						0,
						true,
					)
					if failure != nil {
						return failExecution(thread, stopDepth, failure)
					}
					continue
				}
				if failure := thread.replaceLuaCall(
					callee,
					callBase,
					argumentCount,
				); failure != nil {
					return failExecution(thread, stopDepth, failure)
				}
			case opReturn:
				frame := thread.frames[len(thread.frames)-1]
				firstResult := int(frame.base) + current.a()
				resultCount := current.b() - 1
				if current.b() == 0 {
					resultCount = thread.top - firstResult
				}
				thread.finishLuaCall(firstResult, resultCount)
				if len(thread.frames) == stopDepth {
					return executionResult{kind: executionReturned}
				}
				if len(thread.continuations) != 0 {
					last := len(thread.continuations) - 1
					if int(thread.continuations[last].frameDepth) ==
						len(thread.frames) {
						continue driver
					}
				}
			case opClose:
				frame := thread.frames[len(thread.frames)-1]
				thread.closeUpvalues(int(frame.base) + current.a())
			case opClosure:
				frameIndex := len(thread.frames) - 1
				bindingPC := int(thread.frames[frameIndex].pc)
				thread.frames[frameIndex].pc = uint32(installClosure(
					thread,
					frameIndex,
					current,
					bindingPC,
				))
			case opVararg:
				if failure := executeVararg(
					thread,
					len(thread.frames)-1,
					current,
				); failure != nil {
					return failExecution(thread, stopDepth, failure)
				}
			case opSetList:
				if failure := executeSetList(
					thread,
					len(thread.frames)-1,
					current,
				); failure != nil {
					return failExecution(thread, stopDepth, failure)
				}
			default:
				frameIndex := len(thread.frames) - 1
				return failExecution(
					thread,
					stopDepth,
					newExecutionRuntimeError(
						thread,
						frameIndex,
						int(thread.frames[frameIndex].pc)-1,
						"opcode %s is not executable yet",
						current.opcode(),
					),
				)
			}
		}
	}
	return executionResult{kind: executionReturned}
}

func operandSlot(
	values []slot,
	constants []slot,
	base int,
	operand int,
) slot {
	if isConstantOperand(operand) {
		return constants[constantIndex(operand)]
	}
	return values[base+operand]
}

// runInstructions owns the compact dispatch frame. Operations that need
// coercion or metamethod machinery publish their PC and return to execute,
// keeping cold semantic state out of this function's register allocation.
func runInstructions(thread *Thread, stopDepth int) instruction {
reload:
	function := thread.frames[len(thread.frames)-1].function
	prototype := function.prototype
	values := thread.values
	base := int(thread.frames[len(thread.frames)-1].base)
	pc := int(thread.frames[len(thread.frames)-1].pc)
	code := prototype.code

	for {
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
				prototype.constants[current.bx()],
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
				function.upvalues[current.b()].read(),
			)

		case opSetUpvalue:
			function.upvalues[current.b()].write(
				values[base+current.a()],
			)

		case opGetGlobal, opGetTable, opSelf:
			result := executeRawTableGet(thread, current)
			if result == tableInstructionHandled {
				break
			}
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return result

		case opSetGlobal, opSetTable:
			result := executeRawTableSet(thread, current)
			if result == tableInstructionHandled {
				break
			}
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return result

		case opAdd, opSub, opMul, opDiv, opMod:
			left := operandSlot(
				values,
				prototype.constants,
				base,
				current.b(),
			)
			right := operandSlot(
				values,
				prototype.constants,
				base,
				current.c(),
			)
			if left.ref == nil && right.ref == nil {
				leftNumber := math.Float64frombits(left.bits)
				rightNumber := math.Float64frombits(right.bits)
				var result float64
				switch current.opcode() {
				case opAdd:
					result = leftNumber + rightNumber
				case opSub:
					result = leftNumber - rightNumber
				case opMul:
					result = leftNumber * rightNumber
				case opDiv:
					result = leftNumber / rightNumber
				case opMod:
					result = leftNumber -
						math.Floor(leftNumber/rightNumber)*rightNumber
				}
				writeSlot(
					&values[base+current.a()],
					numberSlot(result),
				)
				break
			}
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return current

		case opPow:
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return current

		case opUnaryMinus:
			source := values[base+current.b()]
			if source.ref == nil {
				writeSlot(
					&values[base+current.a()],
					numberSlot(-math.Float64frombits(source.bits)),
				)
				break
			}
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return current

		case opNot:
			source := values[base+current.b()]
			result := source.ref == nilMarkerPointer ||
				source.ref == falseMarkerPointer
			if result {
				writeSlot(&values[base+current.a()], trueSlot)
			} else {
				writeSlot(&values[base+current.a()], falseSlot)
			}

		case opLength:
			source := values[base+current.b()]
			switch source.kind() {
			case StringKind:
				writeSlot(
					&values[base+current.a()],
					numberSlot(float64(
						len((*luaString)(source.ref).text),
					)),
				)
			case TableKind:
				writeTableLength(
					&values[base+current.a()],
					(*Table)(source.ref),
				)
			default:
				thread.frames[len(thread.frames)-1].pc = uint32(pc)
				return current
			}

		case opConcat:
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return current

		case opJump:
			pc += current.sbx()

		case opEqual:
			left := operandSlot(
				values,
				prototype.constants,
				base,
				current.b(),
			)
			right := operandSlot(
				values,
				prototype.constants,
				base,
				current.c(),
			)
			var equal bool
			switch {
			case left.ref == nil && right.ref == nil:
				equal = math.Float64frombits(left.bits) ==
					math.Float64frombits(right.bits)
			case left.ref == right.ref && left.bits == right.bits:
				equal = true
			case left.kind() != right.kind():
				equal = false
			case left.kind() == StringKind ||
				left.kind() == TableKind ||
				left.kind() == UserDataKind:
				thread.frames[len(thread.frames)-1].pc = uint32(pc)
				return current
			default:
				equal = false
			}
			if equal == (current.a() != 0) {
				jump := code[pc]
				pc++
				pc += jump.sbx()
			} else {
				pc++
			}

		case opLessThan, opLessEqual:
			left := operandSlot(
				values,
				prototype.constants,
				base,
				current.b(),
			)
			right := operandSlot(
				values,
				prototype.constants,
				base,
				current.c(),
			)
			var (
				compared bool
				result   bool
			)
			if left.ref == nil && right.ref == nil {
				leftNumber := math.Float64frombits(left.bits)
				rightNumber := math.Float64frombits(right.bits)
				compared = true
				if current.opcode() == opLessThan {
					result = leftNumber < rightNumber
				} else {
					result = leftNumber <= rightNumber
				}
			}
			if !compared {
				thread.frames[len(thread.frames)-1].pc = uint32(pc)
				return current
			}
			if result == (current.a() != 0) {
				jump := code[pc]
				pc++
				pc += jump.sbx()
			} else {
				pc++
			}

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

		case opCall, opReturn:
			frameIndex := len(thread.frames) - 1
			if current.opcode() == opCall {
				thread.frames[frameIndex].pc = uint32(pc)
				if thread.tryEnterFixedLuaCall(base, current) {
					goto reload
				}
			} else if frameIndex > stopDepth &&
				thread.tryCompleteFixedLuaReturn(frameIndex, current) {
				goto reload
			}
			return code[pc-1]

		case opTailCall:
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return current

		case opClose:
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return current

		case opClosure:
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return current

		case opVararg:
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return current

		case opForPrep:
			register := base + current.a()
			initial := values[register]
			limit := values[register+1]
			step := values[register+2]
			if initial.ref == nil &&
				limit.ref == nil &&
				step.ref == nil {
				writeSlot(
					&values[register],
					numberSlot(
						math.Float64frombits(initial.bits)-
							math.Float64frombits(step.bits),
					),
				)
				pc += current.sbx()
				break
			}
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return current

		case opForLoop:
			register := base + current.a()
			step := math.Float64frombits(values[register+2].bits)
			index := math.Float64frombits(values[register].bits) + step
			limit := math.Float64frombits(values[register+1].bits)
			if step > 0 && index <= limit ||
				!(step > 0) && limit <= index {
				value := numberSlot(index)
				writeSlot(&values[register], value)
				writeSlot(&values[register+3], value)
				pc += current.sbx()
			}

		case opIteratorLoop:
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return current

		default:
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return current
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
		slotFromFunction(closure),
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

func executeVararg(
	thread *Thread,
	frameIndex int,
	code instruction,
) *Error {
	frame := thread.frames[frameIndex]
	prototype := frame.function.prototype
	parameters := int(prototype.parameters)
	extraCount := frame.varargCount()
	firstExtra := int(frame.resultBase) + 1 + parameters
	resultCount := code.b() - 1
	destination := int(frame.base) + code.a()
	if code.b() == 0 {
		resultCount = extraCount
		required := uint64(destination) + uint64(resultCount)
		if required > uint64(len(thread.values)) {
			if failure := prepareOpenVararg(
				thread,
				destination,
				resultCount,
			); failure != nil {
				return failure
			}
		}
		thread.top = int(required)
	}
	copied := resultCount
	if copied > extraCount {
		copied = extraCount
	}
	copy(
		thread.values[destination:destination+copied],
		thread.values[firstExtra:firstExtra+copied],
	)
	thread.fillNil(
		destination+copied,
		destination+resultCount,
	)
	return nil
}

func luaFunctionSlot(value slot) (*Function, bool) {
	if value.kind() != FunctionKind {
		return nil, false
	}
	return (*Function)(value.ref), true
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
