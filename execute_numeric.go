package lua

import "math"

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
	thread *threadObject,
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
		invalidOperand := code.b()
		if leftOK {
			invalid = right
			invalidOperand = code.c()
		}
		return newExecutionTypeError(
			thread,
			frameIndex,
			instructionPC,
			operandRegister(invalidOperand),
			"perform arithmetic on",
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
		nilSlot,
		2,
		1,
		executionContinuation{
			nextPC: uint32(nextPC),
			code:   code,
		},
	)
}

//go:noinline
func slowEquality(
	thread *threadObject,
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
		(!left.isTable() && !left.isUserData()) {
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
		nilSlot,
		2,
		1,
		executionContinuation{
			nextPC: uint32(nextPC),
			code:   code,
			mode:   continuationCompare,
		},
	)
}

//go:noinline
func slowOrder(
	thread *threadObject,
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
		leftText := stringSlotText(left)
		rightText := stringSlotText(right)
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
	mode := continuationCompare
	if !found && code.opcode() == opLessEqual {
		method, found = matchingMetamethod(
			thread,
			right,
			left,
			metaLessThan,
		)
		if found {
			left, right = right, left
			mode = continuationCompareInverted
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
		nilSlot,
		2,
		1,
		executionContinuation{
			nextPC: uint32(nextPC),
			code:   code,
			mode:   mode,
		},
	)
}

//go:noinline
func prepareNumericFor(
	thread *threadObject,
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
