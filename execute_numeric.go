package lua

import "math"

const (
	continuationComparison uint32 = 1 << iota
	continuationInvert
)

// executionContinuation records only work suspended while a Lua metamethod
// runs. Ordinary calls need no continuation: their destination and result
// count already live in the activation.
type executionContinuation struct {
	frameDepth  uint32
	scratchBase uint32
	savedTop    uint32
	savedExtent uint32
	nextPC      uint32
	code        instruction
	flags       uint32
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

func numberSlot(value float64) slot {
	return slot{bits: math.Float64bits(value)}
}

func slotTruth(value slot) bool {
	return value.ref != nilMarkerPointer && value.ref != falseMarkerPointer
}

func numericBinary(operation opcode, left, right float64) float64 {
	switch operation {
	case opAdd:
		return left + right
	case opSub:
		return left - right
	case opMul:
		return left * right
	case opDiv:
		return left / right
	case opMod:
		return left - math.Floor(left/right)*right
	case opPow:
		return math.Pow(left, right)
	default:
		panic("lua: invalid numeric binary operation")
	}
}

func arithmeticMetamethod(operation opcode) metamethod {
	switch operation {
	case opAdd:
		return metaAdd
	case opSub:
		return metaSub
	case opMul:
		return metaMul
	case opDiv:
		return metaDiv
	case opMod:
		return metaMod
	case opPow:
		return metaPow
	case opUnaryMinus:
		return metaUnaryMinus
	default:
		panic("lua: invalid arithmetic operation")
	}
}

//go:noinline
func slowArithmetic(
	thread *Thread,
	frameIndex int,
	code instruction,
) *Error {
	frame := thread.frames[frameIndex]
	prototype := frame.function.prototype
	nextPC := int(frame.pc)
	instructionPC := nextPC - 1
	base := int(frame.base)
	left := operandSlot(
		thread.values,
		prototype.constants,
		base,
		code.b(),
	)
	right := left
	if code.opcode() != opUnaryMinus {
		right = operandSlot(
			thread.values,
			prototype.constants,
			base,
			code.c(),
		)
	}

	leftNumber, leftOK := slotToNumber(left)
	rightNumber, rightOK := leftNumber, leftOK
	if code.opcode() != opUnaryMinus {
		rightNumber, rightOK = slotToNumber(right)
	}
	if leftOK && rightOK {
		result := -leftNumber
		if code.opcode() != opUnaryMinus {
			result = numericBinary(code.opcode(), leftNumber, rightNumber)
		}
		writeSlot(
			&thread.values[base+code.a()],
			numberSlot(result),
		)
		return nil
	}

	method, found := binaryMetamethod(
		thread,
		left,
		right,
		arithmeticMetamethod(code.opcode()),
	)
	if !found {
		invalid := left
		if leftOK {
			invalid = right
		}
		return newExecutionRuntimeError(
			thread,
			frameIndex,
			instructionPC,
			"attempt to perform arithmetic on a %s value",
			invalid.kind(),
		)
	}

	return startMetamethodCall(
		thread,
		frameIndex,
		instructionPC,
		method,
		left,
		right,
		executionContinuation{
			nextPC: uint32(nextPC),
			code:   code,
		},
	)
}

//go:noinline
func slowEquality(
	thread *Thread,
	frameIndex int,
	code instruction,
) *Error {
	frame := thread.frames[frameIndex]
	nextPC := int(frame.pc)
	instructionPC := nextPC - 1
	base := int(frame.base)
	left := operandSlot(
		thread.values,
		frame.function.prototype.constants,
		base,
		code.b(),
	)
	right := operandSlot(
		thread.values,
		frame.function.prototype.constants,
		base,
		code.c(),
	)
	if rawSlotEqual(left, right) {
		setComparisonPC(thread, frameIndex, nextPC, code, true)
		return nil
	}
	if left.kind() != right.kind() ||
		(left.kind() != TableKind && left.kind() != UserDataKind) {
		setComparisonPC(thread, frameIndex, nextPC, code, false)
		return nil
	}
	method, found := matchingMetamethod(
		thread,
		left,
		right,
		metaEqual,
	)
	if !found {
		setComparisonPC(thread, frameIndex, nextPC, code, false)
		return nil
	}
	return startMetamethodCall(
		thread,
		frameIndex,
		instructionPC,
		method,
		left,
		right,
		executionContinuation{
			nextPC: uint32(nextPC),
			code:   code,
			flags:  continuationComparison,
		},
	)
}

//go:noinline
func slowOrder(
	thread *Thread,
	frameIndex int,
	code instruction,
) *Error {
	frame := thread.frames[frameIndex]
	nextPC := int(frame.pc)
	instructionPC := nextPC - 1
	base := int(frame.base)
	left := operandSlot(
		thread.values,
		frame.function.prototype.constants,
		base,
		code.b(),
	)
	right := operandSlot(
		thread.values,
		frame.function.prototype.constants,
		base,
		code.c(),
	)
	leftKind := left.kind()
	rightKind := right.kind()
	if leftKind != rightKind {
		return newExecutionRuntimeError(
			thread,
			frameIndex,
			instructionPC,
			"attempt to compare %s with %s",
			leftKind,
			rightKind,
		)
	}
	if leftKind == StringKind {
		leftText := (*luaString)(left.ref).text
		rightText := (*luaString)(right.ref).text
		result := leftText < rightText
		if code.opcode() == opLessEqual {
			result = leftText <= rightText
		}
		setComparisonPC(thread, frameIndex, nextPC, code, result)
		return nil
	}

	event := metaLessThan
	if code.opcode() == opLessEqual {
		event = metaLessEqual
	}
	method, found := matchingMetamethod(thread, left, right, event)
	flags := uint32(continuationComparison)
	if !found && code.opcode() == opLessEqual {
		method, found = matchingMetamethod(
			thread,
			right,
			left,
			metaLessThan,
		)
		if found {
			left, right = right, left
			flags |= continuationInvert
		}
	}
	if !found {
		return newExecutionRuntimeError(
			thread,
			frameIndex,
			instructionPC,
			"attempt to compare two %s values",
			leftKind,
		)
	}

	return startMetamethodCall(
		thread,
		frameIndex,
		instructionPC,
		method,
		left,
		right,
		executionContinuation{
			nextPC: uint32(nextPC),
			code:   code,
			flags:  flags,
		},
	)
}

//go:noinline
func prepareNumericFor(
	thread *Thread,
	frameIndex int,
	code instruction,
) *Error {
	frame := thread.frames[frameIndex]
	nextPC := int(frame.pc)
	instructionPC := nextPC - 1
	base := int(frame.base) + code.a()
	initial, ok := slotToNumber(thread.values[base])
	if !ok {
		return newExecutionRuntimeError(
			thread,
			frameIndex,
			instructionPC,
			"'for' initial value must be a number",
		)
	}
	limit, ok := slotToNumber(thread.values[base+1])
	if !ok {
		return newExecutionRuntimeError(
			thread,
			frameIndex,
			instructionPC,
			"'for' limit must be a number",
		)
	}
	step, ok := slotToNumber(thread.values[base+2])
	if !ok {
		return newExecutionRuntimeError(
			thread,
			frameIndex,
			instructionPC,
			"'for' step must be a number",
		)
	}

	writeSlot(&thread.values[base], numberSlot(initial-step))
	writeSlot(&thread.values[base+1], numberSlot(limit))
	writeSlot(&thread.values[base+2], numberSlot(step))
	thread.frames[frameIndex].pc = uint32(nextPC + code.sbx())
	return nil
}

//go:noinline
func startMetamethodCall(
	thread *Thread,
	frameIndex int,
	instructionPC int,
	callable slot,
	left slot,
	right slot,
	continuation executionContinuation,
) *Error {
	function, direct := luaFunctionSlot(callable)
	if !direct {
		function = luaCallMetamethod(thread, callable)
		if function == nil {
			return newExecutionRuntimeError(
				thread,
				frameIndex,
				instructionPC,
				"attempt to call a %s value",
				callable.kind(),
			)
		}
	}
	if len(thread.frames) >= thread.state.options.MaxFrames {
		return newResourceError(
			"lua: call frame limit of %d exceeded",
			thread.state.options.MaxFrames,
		)
	}

	savedTop := thread.top
	savedExtent := thread.frameExtent
	scratchBase := thread.liveValueExtent()
	stagedValues := 3
	argumentCount := 2
	if !direct {
		stagedValues++
		argumentCount++
	}
	limit := thread.state.options.MaxValues
	if scratchBase < 0 ||
		scratchBase > limit ||
		stagedValues > limit-scratchBase ||
		uint64(scratchBase+stagedValues) > uint64(^uint32(0)) {
		return newResourceError(
			"lua: value stack limit of %d exceeded",
			limit,
		)
	}
	layout, resourceError := thread.planLuaCallLayout(
		function,
		scratchBase,
		argumentCount,
		1,
	)
	if resourceError != nil {
		return resourceError
	}

	thread.reserveValues(layout.required)
	thread.reserveFrames(len(thread.frames) + 1)
	thread.values[scratchBase] = callable
	thread.values[scratchBase+1] = left
	thread.values[scratchBase+2] = right
	stageEnd := scratchBase + 3
	if !direct {
		thread.insertCallMetamethod(function, scratchBase, 2)
		stageEnd++
	}
	thread.top = stageEnd
	thread.frameExtent = stageEnd

	continuation.frameDepth = uint32(len(thread.frames))
	continuation.scratchBase = uint32(scratchBase)
	continuation.savedTop = uint32(savedTop)
	continuation.savedExtent = uint32(savedExtent)
	thread.commitLuaCall(function, layout, stageEnd)
	thread.continuations = append(thread.continuations, continuation)
	return nil
}

//go:noinline
func resumeExecutionContinuation(thread *Thread) {
	last := len(thread.continuations) - 1
	continuation := thread.continuations[last]
	if int(continuation.frameDepth) != len(thread.frames) {
		panic("lua: execution continuation resumed at the wrong depth")
	}

	scratchBase := int(continuation.scratchBase)
	result := thread.values[scratchBase]
	previousExtent := thread.liveValueExtent()
	thread.top = int(continuation.savedTop)
	thread.frameExtent = int(continuation.savedExtent)
	thread.clearInactive(scratchBase, previousExtent)
	thread.continuations[last] = executionContinuation{}
	thread.continuations = thread.continuations[:last]

	frameIndex := int(continuation.frameDepth) - 1
	frame := &thread.frames[frameIndex]
	if continuation.flags&continuationComparison == 0 {
		writeSlot(
			&thread.values[int(frame.base)+continuation.code.a()],
			result,
		)
		frame.pc = continuation.nextPC
		return
	}

	truth := slotTruth(result)
	if continuation.flags&continuationInvert != 0 {
		truth = !truth
	}
	setComparisonPC(
		thread,
		frameIndex,
		int(continuation.nextPC),
		continuation.code,
		truth,
	)
}

func setComparisonPC(
	thread *Thread,
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
