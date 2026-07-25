package lua

import "math"

func (parser *sourceParser) adjustExpressionList(
	list expressionList,
	wanted int,
	line uint32,
) (int, *Error) {
	if wanted < 0 || list.valueCount == 0 {
		panic("lua: invalid expression-list adjustment")
	}
	emitter := parser.function
	base := list.registerBase
	values := parser.values[list.valueBase : list.valueBase+list.valueCount]

	for index := 0; index+1 < len(values); index++ {
		if syntaxError := parser.writeExpression(
			&values[index],
			base+index,
			values[index].line,
		); syntaxError != nil {
			return 0, syntaxError
		}
	}

	lastIndex := len(values) - 1
	last := &values[lastIndex]
	fixed := lastIndex
	if last.kind == expressionVararg {
		results := wanted - fixed
		if results < 0 {
			results = 0
		}
		if results != 0 {
			target, syntaxError := emitter.reserveRegisters(
				results,
				last.line,
			)
			if syntaxError != nil {
				return 0, syntaxError
			}
			if target != base+fixed {
				panic("lua: compiler lost expression-list register order")
			}
			emitter.emitABC(
				opVararg,
				target,
				results+1,
				0,
				last.line,
			)
		}
		fixed += results
	} else {
		register, syntaxError := parser.expressionToTemporary(
			last,
			last.line,
		)
		if syntaxError != nil {
			return 0, syntaxError
		}
		if register != base+lastIndex {
			panic("lua: compiler lost expression-list register order")
		}
		fixed++
	}

	if fixed < wanted {
		target, syntaxError := emitter.reserveRegisters(
			wanted-fixed,
			line,
		)
		if syntaxError != nil {
			return 0, syntaxError
		}
		if target != base+fixed {
			panic("lua: compiler lost expression-list register order")
		}
		emitter.emitABC(opLoadNil, target, base+wanted-1, 0, line)
	}
	if emitter.registerTop < base+wanted {
		panic("lua: compiler produced too few expression-list registers")
	}
	emitter.releaseRegisters(base + wanted)
	return base, nil
}

func (parser *sourceParser) storeExpression(
	target *compiledExpression,
	value *compiledExpression,
	line uint32,
) *Error {
	emitter := parser.function
	switch target.kind {
	case expressionLocal:
		return parser.writeExpression(value, target.info, line)
	case expressionGlobal:
		register, syntaxError := parser.expressionToRegister(value, line)
		if syntaxError != nil {
			return syntaxError
		}
		emitter.emitABx(opSetGlobal, register, target.info, line)
	case expressionIndexed:
		operand, syntaxError := parser.expressionToRK(value, line)
		if syntaxError != nil {
			return syntaxError
		}
		emitter.emitABC(
			opSetTable,
			target.info,
			target.aux,
			operand,
			line,
		)
	default:
		panic("lua: expression is not assignable")
	}
	parser.releaseExpressionTemporaries(*target, *value)
	return nil
}

func isArithmeticOperator(operation binaryOperator) bool {
	switch operation {
	case binaryAdd, binarySubtract, binaryMultiply, binaryDivide,
		binaryModulo, binaryPower:
		return true
	default:
		return false
	}
}

func plainNumericConstant(value compiledExpression) bool {
	return value.kind == expressionConstant &&
		value.constant.kind() == NumberKind &&
		value.trueExits == emptyJumpList &&
		value.falseExits == emptyJumpList
}

// fallThroughIfTrue makes the truthy path continue at the next instruction
// and leaves every false path pending.
func (parser *sourceParser) fallThroughIfTrue(
	value *compiledExpression,
	line uint32,
) *Error {
	var exit jumpList
	switch value.kind {
	case expressionCondition:
		exit = jumpAt(value.info)
		parser.function.invertComparison(exit)
		value.kind = expressionInvalid
	case expressionConstant:
		if constantTruth(value.constant) {
			break
		}
		var syntaxError *Error
		exit, syntaxError = parser.emitValueExit(value, false, line)
		if syntaxError != nil {
			return syntaxError
		}
	default:
		var syntaxError *Error
		exit, syntaxError = parser.emitValueExit(value, false, line)
		if syntaxError != nil {
			return syntaxError
		}
	}

	var syntaxError *Error
	value.falseExits, syntaxError = parser.function.joinJumps(
		value.falseExits,
		exit,
	)
	if syntaxError != nil {
		return syntaxError
	}
	if syntaxError = parser.function.patchConditionToHere(
		value.trueExits,
	); syntaxError != nil {
		return syntaxError
	}
	value.trueExits = emptyJumpList
	return nil
}

// fallThroughIfFalse makes the false path continue at the next instruction
// and leaves every truthy path pending.
func (parser *sourceParser) fallThroughIfFalse(
	value *compiledExpression,
	line uint32,
) *Error {
	var exit jumpList
	switch value.kind {
	case expressionCondition:
		exit = jumpAt(value.info)
		value.kind = expressionInvalid
	case expressionConstant:
		if !constantTruth(value.constant) {
			break
		}
		var syntaxError *Error
		exit, syntaxError = parser.emitValueExit(value, true, line)
		if syntaxError != nil {
			return syntaxError
		}
	default:
		var syntaxError *Error
		exit, syntaxError = parser.emitValueExit(value, true, line)
		if syntaxError != nil {
			return syntaxError
		}
	}

	var syntaxError *Error
	value.trueExits, syntaxError = parser.function.joinJumps(
		value.trueExits,
		exit,
	)
	if syntaxError != nil {
		return syntaxError
	}
	if syntaxError = parser.function.patchConditionToHere(
		value.falseExits,
	); syntaxError != nil {
		return syntaxError
	}
	value.falseExits = emptyJumpList
	return nil
}

func (parser *sourceParser) emitValueExit(
	value *compiledExpression,
	truth bool,
	line uint32,
) (jumpList, *Error) {
	register, syntaxError := parser.expressionValueToRegister(value, line)
	if syntaxError != nil {
		return emptyJumpList, syntaxError
	}
	condition := 0
	if truth {
		condition = 1
	}
	parser.function.emitABC(
		opTestSet,
		noRegister,
		register,
		condition,
		line,
	)
	exit := parser.function.emitJump(line)
	parser.releaseExpressionTemporaries(*value)
	return exit, nil
}

func constantTruth(value slot) bool {
	switch value.kind() {
	case NilKind:
		return false
	case BoolKind:
		return value.ref == trueMarkerPointer
	default:
		return true
	}
}

func (parser *sourceParser) prepareConcat(
	value compiledExpression,
	line uint32,
) (compiledExpression, *Error) {
	_, syntaxError := parser.expressionToTemporary(&value, line)
	return value, syntaxError
}

func (parser *sourceParser) emitConcat(
	left compiledExpression,
	right compiledExpression,
	line uint32,
) (compiledExpression, *Error) {
	if right.kind == expressionDeferred &&
		right.trueExits == emptyJumpList &&
		right.falseExits == emptyJumpList {
		code := parser.function.builder.code[right.info]
		if code.opcode() == opConcat && left.info+1 == code.b() {
			parser.function.builder.code[right.info] = code.withB(left.info)
			parser.releaseExpressionTemporaries(left)
			right.line = line
			return right, nil
		}
	}

	rightRegister, syntaxError := parser.expressionToTemporary(&right, line)
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	if rightRegister != left.info+1 {
		panic("lua: compiler produced a non-contiguous concatenation")
	}
	pc := parser.function.emitDeferredABC(
		opConcat,
		left.info,
		rightRegister,
		line,
	)
	parser.releaseExpressionTemporaries(left, right)
	return compiledExpression{
		kind: expressionDeferred,
		info: pc,
		line: line,
	}, nil
}

func (parser *sourceParser) emitUnary(
	operation tokenKind,
	value compiledExpression,
	line uint32,
) (compiledExpression, *Error) {
	if operation == tokenNot {
		return parser.emitNot(value, line)
	}
	if value.kind == expressionConstant &&
		value.trueExits == emptyJumpList &&
		value.falseExits == emptyJumpList {
		switch operation {
		case '-':
			if value.constant.kind() == NumberKind {
				number := math.Float64frombits(value.constant.bits)
				value.constant = slotFromValue(Number(-number))
				return value, nil
			}
		}
	}

	register, syntaxError := parser.expressionToRegister(&value, line)
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	var opcode opcode
	switch operation {
	case '-':
		opcode = opUnaryMinus
	case '#':
		opcode = opLength
	default:
		panic("lua: unknown unary operator")
	}
	parser.releaseExpressionTemporaries(value)
	pc := parser.function.emitDeferredABC(opcode, register, 0, line)
	return compiledExpression{
		kind: expressionDeferred,
		info: pc,
		line: line,
	}, nil
}

func (parser *sourceParser) emitNot(
	value compiledExpression,
	line uint32,
) (compiledExpression, *Error) {
	switch value.kind {
	case expressionConstant:
		value.constant = slotFromValue(Bool(!constantTruth(value.constant)))
	case expressionCondition:
		parser.function.invertComparison(jumpAt(value.info))
	default:
		register, syntaxError := parser.expressionValueToRegister(
			&value,
			line,
		)
		if syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
		pc := parser.function.emitDeferredABC(opNot, register, 0, line)
		parser.releaseExpressionTemporaries(value)
		value.kind = expressionDeferred
		value.info = pc
		value.line = line
	}
	value.trueExits, value.falseExits =
		value.falseExits, value.trueExits
	parser.function.discardJumpValues(value.trueExits)
	parser.function.discardJumpValues(value.falseExits)
	return value, nil
}

func (parser *sourceParser) emitBinary(
	operation binaryOperator,
	left, right compiledExpression,
	preparedRK int,
	prepared bool,
	line uint32,
) (compiledExpression, *Error) {
	if folded, ok := foldNumericBinary(operation, left, right); ok {
		return folded, nil
	}

	leftRK := preparedRK
	var syntaxError *Error
	if !prepared {
		leftRK, syntaxError = parser.expressionToRK(&left, line)
		if syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
	}
	rightRK, syntaxError := parser.expressionToRK(&right, line)
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}

	emitter := parser.function
	switch operation {
	case binaryAdd, binarySubtract, binaryMultiply, binaryDivide,
		binaryModulo, binaryPower:
		var opcode opcode
		switch operation {
		case binaryAdd:
			opcode = opAdd
		case binarySubtract:
			opcode = opSub
		case binaryMultiply:
			opcode = opMul
		case binaryDivide:
			opcode = opDiv
		case binaryModulo:
			opcode = opMod
		case binaryPower:
			opcode = opPow
		}
		parser.releaseExpressionTemporaries(left, right)
		pc := emitter.emitDeferredABC(opcode, leftRK, rightRK, line)
		return compiledExpression{
			kind: expressionDeferred,
			info: pc,
			line: line,
		}, nil
	default:
		comparison := opEqual
		inversion := 1
		switch operation {
		case binaryEqual:
			comparison = opEqual
		case binaryNotEqual:
			comparison = opEqual
			inversion = 0
		case binaryLess:
			comparison = opLessThan
		case binaryLessEqual:
			comparison = opLessEqual
		case binaryGreater:
			comparison = opLessThan
			leftRK, rightRK = rightRK, leftRK
		case binaryGreaterEqual:
			comparison = opLessEqual
			leftRK, rightRK = rightRK, leftRK
		default:
			panic("lua: unknown binary operator")
		}
		emitter.emitABC(comparison, inversion, leftRK, rightRK, line)
		jump := emitter.emitJump(line)
		parser.releaseExpressionTemporaries(left, right)
		return compiledExpression{
			kind: expressionCondition,
			info: jump.pc(),
			line: line,
		}, nil
	}
}

func foldNumericBinary(
	operation binaryOperator,
	left, right compiledExpression,
) (compiledExpression, bool) {
	if left.kind != expressionConstant ||
		right.kind != expressionConstant ||
		left.trueExits != emptyJumpList ||
		left.falseExits != emptyJumpList ||
		right.trueExits != emptyJumpList ||
		right.falseExits != emptyJumpList ||
		left.constant.kind() != NumberKind ||
		right.constant.kind() != NumberKind {
		return compiledExpression{}, false
	}
	a := math.Float64frombits(left.constant.bits)
	b := math.Float64frombits(right.constant.bits)
	var result float64
	switch operation {
	case binaryAdd:
		result = a + b
	case binarySubtract:
		result = a - b
	case binaryMultiply:
		result = a * b
	case binaryDivide:
		if b == 0 {
			return compiledExpression{}, false
		}
		result = a / b
	case binaryModulo:
		if b == 0 {
			return compiledExpression{}, false
		}
		result = a - math.Floor(a/b)*b
	case binaryPower:
		result = math.Pow(a, b)
	default:
		return compiledExpression{}, false
	}
	if math.IsNaN(result) {
		return compiledExpression{}, false
	}
	return compiledExpression{
		kind:     expressionConstant,
		constant: slotFromValue(Number(result)),
		line:     left.line,
	}, true
}

func lowestExpressionTemporary(values ...compiledExpression) int {
	base := noRegister
	for _, value := range values {
		var candidate int
		switch value.kind {
		case expressionTemporary:
			candidate = value.info
		case expressionIndexed:
			candidate = value.ownedBase
		default:
			continue
		}
		if candidate < base {
			base = candidate
		}
	}
	return base
}

// A temporary result owns the lowest register in the live suffix used to
// compute it. Indexed expressions similarly own the lowest temporary holding
// their table or key. Releasing that register releases every operand above it
// while preserving locals and surrounding expressions.
func (parser *sourceParser) releaseExpressionTemporaries(
	values ...compiledExpression,
) {
	emitter := parser.function
	base := lowestExpressionTemporary(values...)
	if base != noRegister {
		emitter.releaseRegisters(base)
	}
}

func (parser *sourceParser) expressionToRK(
	value *compiledExpression,
	line uint32,
) (int, *Error) {
	if value.kind == expressionCondition ||
		value.trueExits != emptyJumpList ||
		value.falseExits != emptyJumpList {
		return parser.expressionToTemporary(value, line)
	}
	switch value.kind {
	case expressionConstant:
		index, syntaxError := parser.function.constant(
			value.constant,
			line,
		)
		if syntaxError != nil {
			return 0, syntaxError
		}
		if index <= maxRegisterConstant {
			return registerOrConstant(index, true), nil
		}
	case expressionLocal, expressionTemporary:
		return value.info, nil
	}
	register, syntaxError := parser.expressionToTemporary(value, line)
	return register, syntaxError
}

func (parser *sourceParser) expressionToRegister(
	value *compiledExpression,
	line uint32,
) (int, *Error) {
	if value.kind == expressionCondition ||
		value.trueExits != emptyJumpList ||
		value.falseExits != emptyJumpList {
		return parser.expressionToTemporary(value, line)
	}
	return parser.expressionValueToRegister(value, line)
}

func (parser *sourceParser) expressionValueToRegister(
	value *compiledExpression,
	line uint32,
) (int, *Error) {
	switch value.kind {
	case expressionLocal, expressionTemporary:
		return value.info, nil
	case expressionIndexed:
		if value.ownedBase != noRegister {
			register := value.ownedBase
			if syntaxError := parser.writeExpression(
				value,
				register,
				line,
			); syntaxError != nil {
				return 0, syntaxError
			}
			return register, nil
		}
	case expressionCondition, expressionInvalid:
		panic("lua: conditional expression has no fall-through value")
	}
	register, syntaxError := parser.function.reserveRegisters(1, line)
	if syntaxError != nil {
		return 0, syntaxError
	}
	if syntaxError = parser.writeExpressionValue(
		value,
		register,
		line,
	); syntaxError != nil {
		return 0, syntaxError
	}
	return register, nil
}

func (parser *sourceParser) expressionToTemporary(
	value *compiledExpression,
	line uint32,
) (int, *Error) {
	if value.kind == expressionTemporary {
		if value.trueExits != emptyJumpList ||
			value.falseExits != emptyJumpList {
			if syntaxError := parser.writeExpression(
				value,
				value.info,
				line,
			); syntaxError != nil {
				return 0, syntaxError
			}
		}
		return value.info, nil
	}
	if value.kind == expressionIndexed &&
		value.ownedBase != noRegister {
		register := value.ownedBase
		if syntaxError := parser.writeExpression(
			value,
			register,
			line,
		); syntaxError != nil {
			return 0, syntaxError
		}
		return register, nil
	}
	register, syntaxError := parser.function.reserveRegisters(1, line)
	if syntaxError != nil {
		return 0, syntaxError
	}
	if syntaxError = parser.writeExpression(value, register, line); syntaxError != nil {
		return 0, syntaxError
	}
	return register, nil
}

func (parser *sourceParser) writeExpression(
	value *compiledExpression,
	target int,
	line uint32,
) *Error {
	hasFallthrough := value.kind != expressionCondition &&
		value.kind != expressionInvalid
	if value.kind == expressionCondition {
		var syntaxError *Error
		value.trueExits, syntaxError = parser.function.joinJumps(
			value.trueExits,
			jumpAt(value.info),
		)
		if syntaxError != nil {
			return syntaxError
		}
		value.kind = expressionInvalid
	}
	if hasFallthrough {
		if syntaxError := parser.writeExpressionValue(
			value,
			target,
			line,
		); syntaxError != nil {
			return syntaxError
		}
	}

	if value.trueExits == emptyJumpList &&
		value.falseExits == emptyJumpList {
		value.kind = expressionTemporary
		value.info = target
		return nil
	}

	emitter := parser.function
	needsBoolean := emitter.jumpListNeedsValue(value.trueExits) ||
		emitter.jumpListNeedsValue(value.falseExits)
	falseTarget := emitter.currentPC()
	trueTarget := falseTarget
	if needsBoolean {
		var normalExit jumpList
		if hasFallthrough {
			normalExit = emitter.emitJump(line)
		}
		falseTarget = emitter.currentPC()
		emitter.emitABC(opLoadBool, target, 0, 1, line)
		trueTarget = emitter.currentPC()
		emitter.emitABC(opLoadBool, target, 1, 0, line)
		if syntaxError := emitter.patchJumpsToHere(
			normalExit,
		); syntaxError != nil {
			return syntaxError
		}
	}
	finalTarget := emitter.currentPC()
	if syntaxError := emitter.patchConditionalJumps(
		value.falseExits,
		finalTarget,
		target,
		falseTarget,
	); syntaxError != nil {
		return syntaxError
	}
	if syntaxError := emitter.patchConditionalJumps(
		value.trueExits,
		finalTarget,
		target,
		trueTarget,
	); syntaxError != nil {
		return syntaxError
	}
	value.trueExits = emptyJumpList
	value.falseExits = emptyJumpList
	value.kind = expressionTemporary
	value.info = target
	return nil
}

func (parser *sourceParser) writeExpressionValue(
	value *compiledExpression,
	target int,
	line uint32,
) *Error {
	emitter := parser.function
	switch value.kind {
	case expressionConstant:
		switch value.constant.kind() {
		case NilKind:
			emitter.emitABC(opLoadNil, target, target, 0, line)
		case BoolKind:
			boolean := 0
			if value.constant.ref == trueMarkerPointer {
				boolean = 1
			}
			emitter.emitABC(opLoadBool, target, boolean, 0, line)
		default:
			index, syntaxError := emitter.constant(value.constant, line)
			if syntaxError != nil {
				return syntaxError
			}
			emitter.emitABx(opLoadK, target, index, line)
		}
	case expressionLocal, expressionTemporary:
		if target != value.info {
			emitter.emitABC(opMove, target, value.info, 0, line)
		}
	case expressionDeferred:
		emitter.bindResult(value.info, target)
	case expressionGlobal:
		emitter.emitABx(opGetGlobal, target, value.info, line)
	case expressionIndexed:
		emitter.emitABC(opGetTable, target, value.info, value.aux, line)
		if value.ownedBase != noRegister {
			switch {
			case target == value.ownedBase:
				emitter.releaseRegisters(target + 1)
			case target < value.ownedBase:
				emitter.releaseRegisters(value.ownedBase)
			default:
				panic("lua: indexed result overlaps its operand temporaries")
			}
		}
	case expressionVararg:
		emitter.emitABC(opVararg, target, 2, 0, line)
	default:
		panic("lua: expression has no direct value")
	}
	value.kind = expressionTemporary
	value.info = target
	value.aux = 0
	value.ownedBase = noRegister
	value.assignable = false
	return nil
}
