package lua

import "math"

type expressionKind uint8

const (
	expressionInvalid expressionKind = iota
	expressionConstant
	expressionLocal
	expressionGlobal
	expressionTemporary
	expressionVararg
)

type compiledExpression struct {
	constant slot
	jumps    jumpList
	kind     expressionKind
	info     int
	line     uint32
}

type expressionList struct {
	valueBase    int
	valueCount   int
	registerBase int
}

func (parser *sourceParser) parseExpressionList() (
	list expressionList,
	syntaxError *Error,
) {
	list.valueBase = len(parser.values)
	list.registerBase = parser.function.registerTop
	defer func() {
		if syntaxError != nil {
			parser.values = parser.values[:list.valueBase]
		}
	}()

	for {
		value, expressionError := parser.parseExpression()
		if expressionError != nil {
			return list, expressionError
		}
		more := parser.current.kind == ','
		if more {
			if _, expressionError = parser.expressionToTemporary(
				&value,
				value.line,
			); expressionError != nil {
				return list, expressionError
			}
		}
		parser.values = append(parser.values, value)
		list.valueCount++
		if !more {
			return list, nil
		}
		if expressionError = parser.advance(); expressionError != nil {
			return list, expressionError
		}
	}
}

func (parser *sourceParser) releaseExpressionList(list expressionList) {
	if list.valueBase < 0 ||
		list.valueBase+list.valueCount != len(parser.values) {
		panic("lua: compiler expression-list stack is unbalanced")
	}
	parser.values = parser.values[:list.valueBase]
}

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
			values[index],
			base+index,
			values[index].line,
		); syntaxError != nil {
			return 0, syntaxError
		}
	}

	lastIndex := len(values) - 1
	last := values[lastIndex]
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
			&last,
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

type binaryOperator uint8

const (
	binaryInvalid binaryOperator = iota
	binaryOr
	binaryAnd
	binaryEqual
	binaryNotEqual
	binaryLess
	binaryLessEqual
	binaryGreater
	binaryGreaterEqual
	binaryConcat
	binaryAdd
	binarySubtract
	binaryMultiply
	binaryDivide
	binaryModulo
	binaryPower
)

func binaryBinding(kind tokenKind) (binaryOperator, int, int) {
	switch kind {
	case tokenOr:
		return binaryOr, 1, 1
	case tokenAnd:
		return binaryAnd, 2, 2
	case tokenEqual:
		return binaryEqual, 3, 3
	case tokenNotEqual:
		return binaryNotEqual, 3, 3
	case '<':
		return binaryLess, 3, 3
	case tokenLessEqual:
		return binaryLessEqual, 3, 3
	case '>':
		return binaryGreater, 3, 3
	case tokenGreaterEqual:
		return binaryGreaterEqual, 3, 3
	case tokenConcat:
		return binaryConcat, 5, 4
	case '+':
		return binaryAdd, 6, 6
	case '-':
		return binarySubtract, 6, 6
	case '*':
		return binaryMultiply, 7, 7
	case '/':
		return binaryDivide, 7, 7
	case '%':
		return binaryModulo, 7, 7
	case '^':
		return binaryPower, 10, 9
	default:
		return binaryInvalid, 0, 0
	}
}

func (parser *sourceParser) parseExpression() (compiledExpression, *Error) {
	return parser.parseSubexpression(0)
}

func (parser *sourceParser) parseSubexpression(
	limit int,
) (compiledExpression, *Error) {
	if syntaxError := parser.enterNesting(); syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	defer parser.leaveNesting()

	left, syntaxError := parser.parseUnary()
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	for {
		operation, leftPower, rightPower := binaryBinding(parser.current.kind)
		if operation == binaryInvalid || leftPower <= limit {
			return left, nil
		}
		operator := parser.current
		if syntaxError = parser.advance(); syntaxError != nil {
			return compiledExpression{}, syntaxError
		}

		if operation == binaryAnd || operation == binaryOr {
			left, syntaxError = parser.prepareLogical(left, operation, operator.line)
			if syntaxError != nil {
				return compiledExpression{}, syntaxError
			}
			right, syntaxError := parser.parseSubexpression(rightPower)
			if syntaxError != nil {
				return compiledExpression{}, syntaxError
			}
			if syntaxError = parser.writeExpression(
				right,
				left.info,
				operator.line,
			); syntaxError != nil {
				return compiledExpression{}, syntaxError
			}
			parser.function.releaseRegisters(left.info + 1)
			if syntaxError = parser.function.patchJumpsToHere(
				left.jumps,
			); syntaxError != nil {
				return compiledExpression{}, syntaxError
			}
			left.jumps = emptyJumpList
			continue
		}

		var preparedRK int
		prepared := false
		if operation == binaryConcat {
			left, syntaxError = parser.prepareConcat(left, operator.line)
			if syntaxError != nil {
				return compiledExpression{}, syntaxError
			}
		} else if left.kind != expressionConstant {
			preparedRK, syntaxError = parser.expressionToRK(
				&left,
				operator.line,
			)
			if syntaxError != nil {
				return compiledExpression{}, syntaxError
			}
			prepared = true
		}

		right, syntaxError := parser.parseSubexpression(rightPower)
		if syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
		if operation == binaryConcat {
			left, syntaxError = parser.emitConcat(left, right, operator.line)
		} else {
			left, syntaxError = parser.emitBinary(
				operation,
				left,
				right,
				preparedRK,
				prepared,
				operator.line,
			)
		}
		if syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
	}
}

func (parser *sourceParser) parseUnary() (compiledExpression, *Error) {
	operation := parser.current.kind
	if operation != tokenNot && operation != '-' && operation != '#' {
		return parser.parsePrimary()
	}
	line := parser.current.line
	if syntaxError := parser.advance(); syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	value, syntaxError := parser.parseSubexpression(8)
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	return parser.emitUnary(operation, value, line)
}

func (parser *sourceParser) parsePrimary() (compiledExpression, *Error) {
	value := parser.current
	switch value.kind {
	case tokenNil:
		if syntaxError := parser.advance(); syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
		return compiledExpression{
			kind:     expressionConstant,
			constant: nilSlot,
			line:     value.line,
		}, nil
	case tokenFalse, tokenTrue:
		if syntaxError := parser.advance(); syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
		return compiledExpression{
			kind: expressionConstant,
			constant: slotFromValue(
				Bool(value.kind == tokenTrue),
			),
			line: value.line,
		}, nil
	case tokenNumber:
		if syntaxError := parser.advance(); syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
		return compiledExpression{
			kind:     expressionConstant,
			constant: slotFromValue(Number(value.number)),
			line:     value.line,
		}, nil
	case tokenString:
		text := parser.unit.internToken(value)
		if syntaxError := parser.advance(); syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
		return compiledExpression{
			kind:     expressionConstant,
			constant: prototypeStringSlot(text),
			line:     value.line,
		}, nil
	case tokenName:
		name := parser.unit.internToken(value)
		if syntaxError := parser.advance(); syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
		return parser.resolveVariable(name, value.line)
	case tokenDots:
		if !parser.function.isVararg() {
			return compiledExpression{}, parser.syntaxError(
				value.line,
				"cannot use ... outside a vararg function",
			)
		}
		if syntaxError := parser.advance(); syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
		return compiledExpression{
			kind: expressionVararg,
			line: value.line,
		}, nil
	case '(':
		if syntaxError := parser.advance(); syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
		expression, syntaxError := parser.parseExpression()
		if syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
		if _, syntaxError = parser.expect(')'); syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
		if expression.kind == expressionVararg {
			if _, syntaxError = parser.expressionToTemporary(
				&expression,
				value.line,
			); syntaxError != nil {
				return compiledExpression{}, syntaxError
			}
		}
		return expression, nil
	default:
		return compiledExpression{}, parser.syntaxError(
			value.line,
			"expected expression near %s",
			value.kind,
		)
	}
}

func (parser *sourceParser) prepareLogical(
	value compiledExpression,
	operation binaryOperator,
	line uint32,
) (compiledExpression, *Error) {
	register, syntaxError := parser.expressionToTemporary(&value, line)
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	condition := 0
	if operation == binaryOr {
		condition = 1
	}
	parser.function.emitABC(
		opTest,
		register,
		0,
		condition,
		line,
	)
	jump := parser.function.emitJump(line)
	value.kind = expressionTemporary
	value.info = register
	value.jumps = jump
	return value, nil
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
	rightRegister, syntaxError := parser.expressionToTemporary(&right, line)
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	if rightRegister != left.info+1 {
		panic("lua: compiler produced a non-contiguous concatenation")
	}
	parser.function.emitABC(
		opConcat,
		left.info,
		left.info,
		rightRegister,
		line,
	)
	parser.function.releaseRegisters(left.info + 1)
	left.kind = expressionTemporary
	return left, nil
}

func (parser *sourceParser) emitUnary(
	operation tokenKind,
	value compiledExpression,
	line uint32,
) (compiledExpression, *Error) {
	if value.kind == expressionConstant {
		switch operation {
		case '-':
			if value.constant.kind() == NumberKind {
				number := math.Float64frombits(value.constant.bits)
				value.constant = slotFromValue(Number(-number))
				return value, nil
			}
		case tokenNot:
			truth := true
			switch value.constant.kind() {
			case NilKind:
				truth = false
			case BoolKind:
				truth = value.constant.ref == trueMarkerPointer
			}
			value.constant = slotFromValue(Bool(!truth))
			return value, nil
		}
	}

	register, syntaxError := parser.expressionToRegister(&value, line)
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	result := register
	if value.kind != expressionTemporary {
		result, syntaxError = parser.function.reserveRegisters(1, line)
		if syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
	}
	var opcode opcode
	switch operation {
	case '-':
		opcode = opUnaryMinus
	case tokenNot:
		opcode = opNot
	case '#':
		opcode = opLength
	default:
		panic("lua: unknown unary operator")
	}
	parser.function.emitABC(opcode, result, register, 0, line)
	return compiledExpression{
		kind: expressionTemporary,
		info: result,
		line: line,
	}, nil
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

	result, syntaxError := parser.binaryResultRegister(left, right, line)
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
		emitter.emitABC(opcode, result, leftRK, rightRK, line)
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
		emitter.emitAsBx(opJump, 0, 1, line)
		emitter.emitABC(opLoadBool, result, 0, 1, line)
		emitter.emitABC(opLoadBool, result, 1, 0, line)
	}
	emitter.releaseRegisters(result + 1)
	return compiledExpression{
		kind: expressionTemporary,
		info: result,
		line: line,
	}, nil
}

func foldNumericBinary(
	operation binaryOperator,
	left, right compiledExpression,
) (compiledExpression, bool) {
	if left.kind != expressionConstant ||
		right.kind != expressionConstant ||
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

func (parser *sourceParser) binaryResultRegister(
	left, right compiledExpression,
	line uint32,
) (int, *Error) {
	emitter := parser.function
	if left.kind == expressionTemporary &&
		right.kind == expressionTemporary {
		if left.info < right.info {
			return left.info, nil
		}
		return right.info, nil
	}
	if left.kind == expressionTemporary {
		return left.info, nil
	}
	if right.kind == expressionTemporary {
		return right.info, nil
	}
	return emitter.reserveRegisters(1, line)
}

func (parser *sourceParser) expressionToRK(
	value *compiledExpression,
	line uint32,
) (int, *Error) {
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
	switch value.kind {
	case expressionLocal, expressionTemporary:
		return value.info, nil
	}
	return parser.expressionToTemporary(value, line)
}

func (parser *sourceParser) expressionToTemporary(
	value *compiledExpression,
	line uint32,
) (int, *Error) {
	if value.kind == expressionTemporary {
		return value.info, nil
	}
	register, syntaxError := parser.function.reserveRegisters(1, line)
	if syntaxError != nil {
		return 0, syntaxError
	}
	if syntaxError = parser.writeExpression(*value, register, line); syntaxError != nil {
		return 0, syntaxError
	}
	value.kind = expressionTemporary
	value.info = register
	return register, nil
}

func (parser *sourceParser) writeExpression(
	value compiledExpression,
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
	case expressionGlobal:
		emitter.emitABx(opGetGlobal, target, value.info, line)
	case expressionVararg:
		emitter.emitABC(opVararg, target, 2, 0, line)
	default:
		panic("lua: invalid compiled expression")
	}
	return nil
}
