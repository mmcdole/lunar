package lua

import (
	"fmt"
	"math"
	"unsafe"
)

type executionResultKind uint8

const (
	executionReturned executionResultKind = iota
	executionYielded
	executionFailed
)

type executionResult struct {
	err  *Error
	kind executionResultKind
}

// execute runs activations until the stack returns to stopDepth. It keeps
// the current frame's hot state in Go locals and publishes only at seams that
// can replace a frame, grow the value stack, or leave the executor.
func execute(thread *threadObject, stopDepth int) executionResult {
	var result executionResult
	if failure := serviceAutomaticCollection(thread); failure != nil {
		result = stopExecution(thread, failure)
	} else {
		result = driveExecution(thread, stopDepth)
	}
	if result.kind == executionFailed {
		finalizeExecutionFailure(thread, stopDepth, result.err)
	}
	return result
}

// driveExecution stops at stopDepth or at the first Lua failure. Failure
// leaves execution state live so a protected caller may inspect it before
// choosing when to unwind.
func driveExecution(thread *threadObject, stopDepth int) executionResult {
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
				frameDepth := len(thread.frames)
				if failure := resumeExecutionContinuation(thread); failure != nil {
					return stopExecution(thread, failure)
				}
				if len(thread.frames) == frameDepth {
					if failure := serviceAutomaticCollection(
						thread,
					); failure != nil {
						return stopExecution(thread, failure)
					}
				}
			} else if depth > len(thread.frames) {
				panic("lua: orphaned execution continuation")
			}
		}
		for {
			if thread.frames[len(thread.frames)-1].function.prototype == nil {
				if thread.contextBudget != 0 {
					if failure := pollExecutionContext(
						thread,
					); failure != nil {
						return stopExecution(thread, failure)
					}
				}
				if failure := invokeNativeCall(thread); failure != nil {
					return stopExecution(thread, failure)
				}
				if thread.status == ThreadSuspended {
					return executionResult{kind: executionYielded}
				}
				if len(thread.continuations) != 0 {
					last := len(thread.continuations) - 1
					if int(thread.continuations[last].frameDepth) ==
						len(thread.frames) {
						continue driver
					}
				}
				if failure := serviceAutomaticCollection(
					thread,
				); failure != nil {
					return stopExecution(thread, failure)
				}
				if len(thread.frames) == stopDepth {
					return executionResult{kind: executionReturned}
				}
				continue
			}
			current := runInstructions(thread, stopDepth)
			switch current.opcode() {
			case opGetGlobal, opGetTable, opGetField,
				opSelf, opSelfField,
				opGetGlobalMiss, opGetTableMiss, opGetFieldMiss,
				opSelfMiss, opSelfFieldMiss:
				frameIndex := len(thread.frames) - 1
				if failure := slowTableGet(
					thread,
					frameIndex,
					current,
				); failure != nil {
					return stopExecution(thread, failure)
				}
			case opSetGlobal, opSetTable, opSetField,
				opSetGlobalMiss, opSetTableMiss, opSetFieldMiss:
				frameIndex := len(thread.frames) - 1
				if failure := slowTableSet(
					thread,
					frameIndex,
					current,
				); failure != nil {
					return stopExecution(thread, failure)
				}
			case opNewTable:
				executeNewTable(
					thread,
					len(thread.frames)-1,
					current,
				)
				if failure := serviceAutomaticCollection(
					thread,
				); failure != nil {
					return stopExecution(thread, failure)
				}
			case opAdd, opSub, opMul, opDiv, opMod, opPow, opUnaryMinus:
				frameIndex := len(thread.frames) - 1
				if failure := slowArithmetic(
					thread,
					frameIndex,
					current,
				); failure != nil {
					return stopExecution(thread, failure)
				}
			case opLength:
				frameIndex := len(thread.frames) - 1
				if failure := slowLength(
					thread,
					frameIndex,
					current,
				); failure != nil {
					return stopExecution(thread, failure)
				}
			case opConcat:
				frameIndex := len(thread.frames) - 1
				if failure := slowConcat(
					thread,
					frameIndex,
					current,
				); failure != nil {
					return stopExecution(thread, failure)
				}
				if len(thread.frames) == frameIndex+1 {
					if failure := serviceAutomaticCollection(
						thread,
					); failure != nil {
						return stopExecution(thread, failure)
					}
				}
			case opEqual:
				frameIndex := len(thread.frames) - 1
				if failure := slowEquality(
					thread,
					frameIndex,
					current,
				); failure != nil {
					return stopExecution(thread, failure)
				}
			case opLessThan, opLessEqual:
				frameIndex := len(thread.frames) - 1
				if failure := slowOrder(
					thread,
					frameIndex,
					current,
				); failure != nil {
					return stopExecution(thread, failure)
				}
			case opForPrep:
				frameIndex := len(thread.frames) - 1
				if failure := prepareNumericFor(
					thread,
					frameIndex,
					current,
				); failure != nil {
					return stopExecution(thread, failure)
				}
			case opIteratorLoop:
				frameIndex := len(thread.frames) - 1
				if failure := startIteratorCall(
					thread,
					frameIndex,
					current,
				); failure != nil {
					return stopExecution(thread, failure)
				}
			case opCall:
				frameIndex := len(thread.frames) - 1
				frame := thread.frames[frameIndex]
				if thread.tryEnterFixedNativeCall(int(frame.base), current) {
					break
				}
				callBase := int(frame.base) + current.a()
				argumentCount := current.b() - 1
				if current.b() == 0 {
					argumentCount = thread.top - callBase - 1
				}
				wantedResults := current.c() - 1
				callee, direct := functionSlot(thread.values[callBase])
				if !direct {
					failure := enterCallMetamethod(
						thread,
						frameIndex,
						int(frame.pc)-1,
						callBase,
						argumentCount,
						wantedResults,
						false,
					)
					if failure != nil {
						return stopExecution(thread, failure)
					}
					continue
				}
				if failure := thread.pushFunctionCall(
					callee,
					callBase,
					argumentCount,
					wantedResults,
				); failure != nil {
					return stopExecution(thread, failure)
				}
			case opTailCall:
				if thread.contextStepDue() {
					if failure := pollExecutionContext(
						thread,
					); failure != nil {
						return stopExecution(thread, failure)
					}
				}
				frameIndex := len(thread.frames) - 1
				frame := thread.frames[frameIndex]
				callBase := int(frame.base) + current.a()
				argumentCount := current.b() - 1
				if current.b() == 0 {
					argumentCount = thread.top - callBase - 1
				}
				callee, direct := functionSlot(thread.values[callBase])
				if !direct {
					failure := enterCallMetamethod(
						thread,
						frameIndex,
						int(frame.pc)-1,
						callBase,
						argumentCount,
						0,
						true,
					)
					if failure != nil {
						return stopExecution(thread, failure)
					}
					continue
				}
				var failure *Error
				if callee.prototype == nil {
					failure = thread.pushFunctionCall(
						callee,
						callBase,
						argumentCount,
						allResults,
					)
				} else {
					failure = thread.replaceFunctionCall(
						callee,
						callBase,
						argumentCount,
					)
				}
				if failure != nil {
					return stopExecution(thread, failure)
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
					if failure := serviceAutomaticCollection(
						thread,
					); failure != nil {
						return stopExecution(thread, failure)
					}
					return executionResult{kind: executionReturned}
				}
				// Poll after the callee is gone: runInstructions does not
				// publish a returning frame's PC, while the caller's was
				// published at its call, so a cancellation is positioned at the
				// call site.
				if thread.contextBudget != 0 {
					if failure := pollExecutionContext(thread); failure != nil {
						return stopExecution(thread, failure)
					}
				}
				if len(thread.continuations) != 0 {
					last := len(thread.continuations) - 1
					if int(thread.continuations[last].frameDepth) ==
						len(thread.frames) {
						continue driver
					}
				}
				if failure := serviceAutomaticCollection(
					thread,
				); failure != nil {
					return stopExecution(thread, failure)
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
				if failure := serviceAutomaticCollection(
					thread,
				); failure != nil {
					return stopExecution(thread, failure)
				}
			case opVararg:
				if failure := executeVararg(
					thread,
					len(thread.frames)-1,
					current,
				); failure != nil {
					return stopExecution(thread, failure)
				}
			case opSetList:
				if failure := executeSetList(
					thread,
					len(thread.frames)-1,
					current,
				); failure != nil {
					return stopExecution(thread, failure)
				}
			case opCollectionPoll:
				if failure := serviceAutomaticCollection(
					thread,
				); failure != nil {
					return stopExecution(thread, failure)
				}
			case opContextPoll:
				if failure := pollExecutionContext(thread); failure != nil {
					return stopExecution(thread, failure)
				}
				frame := &thread.frames[len(thread.frames)-1]
				frame.pc = uint32(int(frame.pc) + current.sbx())
			default:
				panic("lua: executor returned unhandled opcode " +
					current.opcode().String())
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
func runInstructions(thread *threadObject, stopDepth int) instruction {
reload:
	function := thread.frames[len(thread.frames)-1].function
	prototype := function.prototype
	// Table helpers only index values; avoid keeping unused capacity live.
	values := thread.values[:len(thread.values):len(thread.values)]
	base := int(thread.frames[len(thread.frames)-1].base)
	pc := int(thread.frames[len(thread.frames)-1].pc)
	code := prototype.code
	// The verifier bounds every register, constant, and jump by the
	// prototype, and call entry sizes the stack to the frame. One check per
	// frame load therefore covers every unchecked access below.
	if base+int(prototype.registers) > len(values) {
		panic("lua: frame exceeds the value stack")
	}
	registers := unsafe.Pointer(unsafe.SliceData(values))
	constants := unsafe.Pointer(unsafe.SliceData(prototype.constants))
	instructions := unsafe.Pointer(unsafe.SliceData(code))
	var current instruction
	goto dispatch

contextBackedge:
	{
		// Keep polling in one cold block above dispatch. Duplicating this check
		// grows the loop and its frame; placing it below dispatch adds another
		// branch to every ordinary instruction.
		budget := thread.contextBudget
		if budget != 0 {
			budget--
			thread.contextBudget = budget
			if budget == 0 {
				thread.frames[len(thread.frames)-1].pc =
					uint32(pc - current.sbx())
				return current.executorOutcome(opContextPoll)
			}
		}
	}
	goto dispatch

dispatch:
	for {
		current = instructionAt(instructions, pc)
		pc++

		switch current.opcode() {
		case opMove:
			writeSlot(
				registerAt(registers, base+current.a()),
				(*registerAt(registers, base+current.b())),
			)

		case opLoadK:
			writeSlot(
				registerAt(registers, base+current.a()),
				*constantAt(constants, current.bx()),
			)

		case opLoadBool:
			value := falseSlot
			if current.b() != 0 {
				value = trueSlot
			}
			writeSlot(registerAt(registers, base+current.a()), value)
			if current.c() != 0 {
				pc++
			}

		case opLoadNil:
			for register := current.a(); register <= current.b(); register++ {
				writeSlot(registerAt(registers, base+register), nilSlot)
			}

		case opGetUpvalue:
			writeSlot(
				registerAt(registers, base+current.a()),
				function.luaUpvalueUnchecked(current.b()).read(),
			)

		case opSetUpvalue:
			function.luaUpvalueUnchecked(current.b()).write(
				(*registerAt(registers, base+current.a())),
			)

		case opGetTable, opSelf:
			result := executeRawTableGet(values, function, base, current)
			if result == tableInstructionHandled {
				break
			}
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return result

		case opGetGlobal, opGetField, opSelfField:
			result := executeRawStringTableGet(
				values,
				function,
				thread.state.typeMetatables[StringKind],
				base,
				current,
			)
			if result == tableInstructionHandled {
				break
			}
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return result

		case opSetTable:
			result := executeRawTableSet(values, function, base, current)
			if result == tableInstructionHandled {
				break
			}
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return result

		case opSetGlobal, opSetField:
			result := executeRawStringTableSet(values, function, base, current)
			if result == tableInstructionHandled {
				break
			}
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return result

		case opAdd, opSub, opMul, opDiv, opMod:
			left := operandSlotUnchecked(
				registers,
				constants,
				base,
				current.b(),
			)
			right := operandSlotUnchecked(
				registers,
				constants,
				base,
				current.c(),
			)
			if bothNumbers(left, right) {
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
					registerAt(registers, base+current.a()),
					numberSlot(result),
				)
				break
			}
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return current

		case opUnaryMinus:
			source := (*registerAt(registers, base+current.b()))
			if source.isNumber() {
				writeSlot(
					registerAt(registers, base+current.a()),
					numberSlot(-math.Float64frombits(source.bits)),
				)
				break
			}
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return current

		case opNot:
			source := (*registerAt(registers, base+current.b()))
			if !source.truth() {
				writeSlot(registerAt(registers, base+current.a()), trueSlot)
			} else {
				writeSlot(registerAt(registers, base+current.a()), falseSlot)
			}

		case opLength:
			source := (*registerAt(registers, base+current.b()))
			switch source.kind() {
			case StringKind:
				writeSlot(
					registerAt(registers, base+current.a()),
					numberSlot(float64(stringSlotLen(source))),
				)
			case TableKind:
				writeTableLength(
					registerAt(registers, base+current.a()),
					(*tableObject)(source.ref),
				)
			default:
				thread.frames[len(thread.frames)-1].pc = uint32(pc)
				return current
			}

		case opConcat:
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return current

		case opJump:
			offset := current.sbx()
			pc += offset
			if offset < 0 {
				goto contextBackedge
			}

		case opEqual:
			left := operandSlotUnchecked(
				registers,
				constants,
				base,
				current.b(),
			)
			right := operandSlotUnchecked(
				registers,
				constants,
				base,
				current.c(),
			)
			var equal bool
			switch {
			case left.ref == nil && right.ref == nil:
				// Two scalars. Numbers with equal bits always pass the
				// bothNumbers bits test, and the only distinct number
				// patterns that compare equal (the zeros) do too, so any
				// scalar pair outside it is equal exactly when its bits are.
				if left.bits|right.bits < firstReservedSlotBits {
					equal = math.Float64frombits(left.bits) ==
						math.Float64frombits(right.bits)
				} else {
					equal = left.bits == right.bits
				}
			case left.ref == right.ref && left.bits == right.bits:
				equal = true
			case left.kind() != right.kind():
				equal = false
			case left.isString():
				equal = left.bits == right.bits &&
					stringSlotsEqual(left, right)
			case left.isTable() ||
				left.isUserData():
				thread.frames[len(thread.frames)-1].pc = uint32(pc)
				return current
			default:
				equal = false
			}
			if equal == (current.a() != 0) {
				jump := instructionAt(instructions, pc)
				pc++
				offset := jump.sbx()
				pc += offset
				if offset < 0 {
					current = jump
					goto contextBackedge
				}
			} else {
				pc++
			}

		case opLessThan, opLessEqual:
			left := operandSlotUnchecked(
				registers,
				constants,
				base,
				current.b(),
			)
			right := operandSlotUnchecked(
				registers,
				constants,
				base,
				current.c(),
			)
			if bothNumbers(left, right) {
				leftNumber := math.Float64frombits(left.bits)
				rightNumber := math.Float64frombits(right.bits)
				var result bool
				if current.opcode() == opLessThan {
					result = leftNumber < rightNumber
				} else {
					result = leftNumber <= rightNumber
				}
				if result == (current.a() != 0) {
					jump := instructionAt(instructions, pc)
					pc++
					offset := jump.sbx()
					pc += offset
					if offset < 0 {
						current = jump
						goto contextBackedge
					}
				} else {
					pc++
				}
				break
			}
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return current

		case opTest:
			source := (*registerAt(registers, base+current.a()))
			truth := source.truth()
			if truth == (current.c() != 0) {
				jump := instructionAt(instructions, pc)
				pc++
				offset := jump.sbx()
				pc += offset
				if offset < 0 {
					current = jump
					goto contextBackedge
				}
			} else {
				pc++
			}

		case opTestSet:
			source := (*registerAt(registers, base+current.b()))
			truth := source.truth()
			if truth == (current.c() != 0) {
				writeSlot(registerAt(registers, base+current.a()), source)
				jump := instructionAt(instructions, pc)
				pc++
				offset := jump.sbx()
				pc += offset
				if offset < 0 {
					current = jump
					goto contextBackedge
				}
			} else {
				pc++
			}

		case opCall, opReturn:
			frameIndex := len(thread.frames) - 1
			if current.opcode() == opCall {
				thread.frames[frameIndex].pc = uint32(pc)
				callable := *registerAt(registers, base+current.a())
				if callable.bits == uint64(FunctionKind)|nativeFunctionSlotFlag &&
					callable.ref != nil {
					switch thread.tryDirectNativeCall(
						base,
						base+int(prototype.registers),
						current,
						callable,
					) {
					case directCallDone:
						continue
					case directCallCollect:
						return current.executorOutcome(opCollectionPoll)
					}
				} else if thread.tryEnterFixedLuaCall(base, current) {
					goto reload
				}
			} else if frameIndex > stopDepth &&
				thread.tryCompleteFixedLuaReturn(frameIndex, current) {
				goto reload
			}
			return instructionAt(instructions, pc-1)

		// The driver executes these; listing them keeps them explicit in the
		// dispatch table rather than relying on default.
		case opPow, opTailCall, opClose, opClosure, opVararg, opIteratorLoop:
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return current

		case opForPrep:
			register := base + current.a()
			initial := (*registerAt(registers, register))
			limit := (*registerAt(registers, register+1))
			step := (*registerAt(registers, register+2))
			if initial.isNumber() &&
				limit.isNumber() &&
				step.isNumber() {
				writeSlot(
					registerAt(registers, register),
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
			step := math.Float64frombits((*registerAt(registers, register+2)).bits)
			index := math.Float64frombits((*registerAt(registers, register)).bits) + step
			limit := math.Float64frombits((*registerAt(registers, register+1)).bits)
			if step > 0 && index <= limit ||
				!(step > 0) && limit <= index {
				value := numberSlot(index)
				writeSlot(registerAt(registers, register), value)
				writeSlot(registerAt(registers, register+3), value)
				offset := current.sbx()
				pc += offset
				if offset < 0 {
					goto contextBackedge
				}
			}

		default:
			thread.frames[len(thread.frames)-1].pc = uint32(pc)
			return current
		}
	}
}

// Avoids adding closure-construction locals to the dispatch frame.
//
//go:noinline
func installClosure(
	thread *threadObject,
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
			bindings[index] = parent.luaUpvalueUnchecked(binding.b())
		}
	}
	closure := newLuaFunctionOwned(
		thread.state,
		child,
		parent.environment,
		bindings,
	)
	writeSlot(
		&thread.values[int(frame.base)+code.a()],
		slotFromFunctionObject(closure),
	)
	return bindingPC + count
}

// Avoids adding stack-growth and resource-error locals to the open-vararg path.
//
//go:noinline
func prepareOpenVararg(
	thread *threadObject,
	destination int,
	resultCount int,
) *Error {
	limit := thread.valueLimit()
	if destination < 0 ||
		resultCount < 0 ||
		destination > limit ||
		resultCount > limit-destination {
		return newResourceError(
			"value stack limit of %d exceeded",
			limit,
		)
	}
	required := destination + resultCount
	if uint64(required) > uint64(^uint32(0)) {
		return newResourceError(
			"value stack limit of %d exceeded",
			limit,
		)
	}
	thread.reserveValues(required)
	return nil
}

func executeVararg(
	thread *threadObject,
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

func functionSlot(value slot) (*functionObject, bool) {
	if !value.isFunction() {
		return nil, false
	}
	return functionObjectFromSlot(value), true
}

// Avoids adding metamethod lookup and call-window insertion to direct calls.
//
//go:noinline
func enterCallMetamethod(
	thread *threadObject,
	frameIndex int,
	instructionPC int,
	callBase int,
	argumentCount int,
	wantedResults int,
	tail bool,
) *Error {
	called := thread.values[callBase]
	function := callMetamethodFunction(thread, called)
	if function == nil {
		register := callBase - int(thread.frames[frameIndex].base)
		return newExecutionTypeError(
			thread,
			frameIndex,
			instructionPC,
			register,
			"call",
			called.kind(),
		)
	}
	if tail {
		if function.prototype == nil {
			return thread.pushFunctionMetamethodCall(
				function,
				callBase,
				argumentCount,
				allResults,
			)
		}
		return thread.replaceFunctionMetamethodCall(
			function,
			callBase,
			argumentCount,
		)
	}
	return thread.pushFunctionMetamethodCall(
		function,
		callBase,
		argumentCount,
		wantedResults,
	)
}

func newExecutionRuntimeError(
	thread *threadObject,
	frameIndex int,
	pc int,
	format string,
	arguments ...any,
) *Error {
	message := fmt.Sprintf(format, arguments...)
	prototype := thread.frames[frameIndex].function.prototype
	message = executionErrorDescription(prototype, pc, message)
	return &Error{
		value:       thread.state.String(message),
		description: message,
		category:    RuntimeError,
	}
}

func newExecutionTypeError(
	thread *threadObject,
	frameIndex int,
	pc int,
	register int,
	operation string,
	kind Kind,
) *Error {
	prototype := thread.frames[frameIndex].function.prototype
	category, name, found := prototype.describeOperand(pc, register)
	if found {
		return newExecutionRuntimeError(
			thread,
			frameIndex,
			pc,
			"attempt to %s %s '%s' (a %s value)",
			operation,
			category,
			name,
			kind,
		)
	}
	return newExecutionRuntimeError(
		thread,
		frameIndex,
		pc,
		"attempt to %s a %s value",
		operation,
		kind,
	)
}

func operandRegister(operand int) int {
	if isConstantOperand(operand) {
		return -1
	}
	return operand
}

func stopExecution(thread *threadObject, failure *Error) executionResult {
	if failure == nil {
		panic("lua: executor failed without an error")
	}
	positionExecutionFailure(thread, failure)
	return executionResult{
		kind: executionFailed,
		err:  failure,
	}
}

func finalizeExecutionFailure(
	thread *threadObject,
	stopDepth int,
	failure *Error,
) {
	snapshotExecutionFailure(thread, stopDepth, failure)
	thread.unwindCalls(stopDepth)
}

func snapshotExecutionFailure(
	thread *threadObject,
	stopDepth int,
	failure *Error,
) {
	if failure == nil {
		panic("lua: executor failed without an error")
	}
	failure.traceback = appendExecutionTraceback(
		failure.traceback,
		thread,
		stopDepth,
	)
}

func positionExecutionFailure(
	thread *threadObject,
	failure *Error,
) {
	if failure == nil ||
		!failure.sourcePositionable ||
		failure.sourcePositioned ||
		len(failure.traceback) != 0 {
		return
	}
	for index := len(thread.frames) - 1; index >= 0; index-- {
		frame := &thread.frames[index]
		if frame.function == nil ||
			frame.function.prototype == nil {
			continue
		}
		failure.positionExecutionFailure(
			thread.state,
			frame.function.prototype,
			int(frame.pc)-1,
		)
		return
	}
}

func appendExecutionTraceback(
	prefix []TraceFrame,
	thread *threadObject,
	stopDepth int,
) []TraceFrame {
	if stopDepth < 0 || stopDepth > len(thread.frames) {
		panic("lua: invalid traceback stop depth")
	}
	frameCount := len(thread.frames) - stopDepth
	if frameCount == 0 {
		return prefix
	}
	traceback := make(
		[]TraceFrame,
		len(prefix),
		len(prefix)+frameCount,
	)
	copy(traceback, prefix)
	for index := len(thread.frames) - 1; index >= stopDepth; index-- {
		frame := &thread.frames[index]
		prototype := frame.function.prototype
		if prototype == nil {
			traceback = append(traceback, TraceFrame{
				Source:    "=[Go]",
				Function:  "native function",
				TailCalls: frame.tailCalls,
			})
			continue
		}
		pc := int(frame.pc) - 1
		traceback = append(traceback, TraceFrame{
			Source:    prototype.SourceName(),
			Line:      prototype.lineAt(pc),
			TailCalls: frame.tailCalls,
		})
	}
	return traceback
}

func registerAt(registers unsafe.Pointer, index int) *slot {
	return (*slot)(unsafe.Add(registers, uintptr(index)*unsafe.Sizeof(slot{})))
}

func constantAt(constants unsafe.Pointer, index int) *slot {
	return (*slot)(unsafe.Add(constants, uintptr(index)*unsafe.Sizeof(slot{})))
}

func instructionAt(code unsafe.Pointer, pc int) instruction {
	return *(*instruction)(unsafe.Add(code, uintptr(pc)*unsafe.Sizeof(instruction(0))))
}

func operandSlotUnchecked(
	registers unsafe.Pointer,
	constants unsafe.Pointer,
	base int,
	operand int,
) slot {
	if isConstantOperand(operand) {
		return *constantAt(constants, constantIndex(operand))
	}
	return *registerAt(registers, base+operand)
}
