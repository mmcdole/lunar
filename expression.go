package lua

type expressionKind uint8

const (
	expressionInvalid expressionKind = iota
	expressionConstant
	expressionLocal
	expressionGlobal
	expressionDeferred
	expressionTemporary
	expressionIndexed
	expressionCall
	expressionCondition
	expressionVararg
)

// compiledExpression is a transient, move-only description of a source
// expression. A deferred expression owns an instruction whose destination is
// not bound yet. A condition owns the pending jump immediately following its
// comparison. An indexed expression retains its table register and RK key;
// ownedBase is the lowest temporary keeping those operands live. A call owns
// the fixed register base in aux and the CALL instruction in info until its
// consumer fixes the result count. The exit lists preserve Lua's
// operand-valued and/or semantics.
type compiledExpression struct {
	constant   slot
	trueExits  jumpList
	falseExits jumpList
	kind       expressionKind
	info       int
	aux        int
	ownedBase  int
	line       uint32
	assignable bool
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
			if operation == binaryAnd {
				syntaxError = parser.fallThroughIfTrue(
					&left,
					operator.line,
				)
			} else {
				syntaxError = parser.fallThroughIfFalse(
					&left,
					operator.line,
				)
			}
			if syntaxError != nil {
				return compiledExpression{}, syntaxError
			}
			right, syntaxError := parser.parseSubexpression(rightPower)
			if syntaxError != nil {
				return compiledExpression{}, syntaxError
			}
			if operation == binaryAnd {
				right.falseExits, syntaxError =
					parser.function.joinJumps(
						right.falseExits,
						left.falseExits,
					)
			} else {
				right.trueExits, syntaxError =
					parser.function.joinJumps(
						right.trueExits,
						left.trueExits,
					)
			}
			if syntaxError != nil {
				return compiledExpression{}, syntaxError
			}
			left = right
			continue
		}

		var preparedRK int
		prepared := false
		if operation == binaryConcat {
			left, syntaxError = parser.prepareConcat(left, operator.line)
			if syntaxError != nil {
				return compiledExpression{}, syntaxError
			}
		} else if !isArithmeticOperator(operation) ||
			!plainNumericConstant(left) {
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
	case tokenName, '(':
		return parser.parsePrefixExpression()
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
	default:
		return compiledExpression{}, parser.syntaxError(
			value.line,
			"expected expression near %s",
			value.kind,
		)
	}
}

func (parser *sourceParser) parsePrefixExpression() (
	compiledExpression,
	*Error,
) {
	var value compiledExpression
	switch prefix := parser.current; prefix.kind {
	case tokenName:
		name := parser.unit.internToken(prefix)
		if syntaxError := parser.advance(); syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
		var syntaxError *Error
		value, syntaxError = parser.resolveVariable(name, prefix.line)
		if syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
	case '(':
		if syntaxError := parser.advance(); syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
		var syntaxError *Error
		value, syntaxError = parser.parseExpression()
		if syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
		if _, syntaxError = parser.expect(')'); syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
		value.assignable = false
		switch value.kind {
		case expressionCall:
			parser.forceSingleCallResult(&value)
		case expressionVararg:
			if _, syntaxError = parser.expressionToTemporary(
				&value,
				prefix.line,
			); syntaxError != nil {
				return compiledExpression{}, syntaxError
			}
		}
	default:
		return compiledExpression{}, parser.syntaxError(
			prefix.line,
			"expected prefix expression near %s",
			prefix.kind,
		)
	}

	for {
		var syntaxError *Error
		switch parser.current.kind {
		case '.':
			value, syntaxError = parser.parseFieldSuffix(value)
		case '[':
			value, syntaxError = parser.parseIndexSuffix(value)
		case ':':
			value, syntaxError = parser.parseMethodCall(value)
		case '(', tokenString:
			value, syntaxError = parser.parseCallSuffix(value)
		default:
			return value, nil
		}
		if syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
	}
}

func (parser *sourceParser) parseFieldSuffix(
	table compiledExpression,
) (compiledExpression, *Error) {
	line := parser.current.line
	tableRegister, syntaxError := parser.expressionToRegister(&table, line)
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	if syntaxError = parser.advance(); syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	name, syntaxError := parser.expect(tokenName)
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	key := compiledExpression{
		kind:     expressionConstant,
		constant: prototypeStringSlot(parser.unit.internToken(name)),
		line:     name.line,
	}
	keyOperand, syntaxError := parser.expressionToRK(&key, name.line)
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	return indexedExpression(table, tableRegister, key, keyOperand, line), nil
}

func (parser *sourceParser) parseIndexSuffix(
	table compiledExpression,
) (compiledExpression, *Error) {
	line := parser.current.line
	tableRegister, syntaxError := parser.expressionToRegister(&table, line)
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	if syntaxError = parser.advance(); syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	key, syntaxError := parser.parseExpression()
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	if _, syntaxError = parser.expect(']'); syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	keyOperand, syntaxError := parser.expressionToRK(&key, line)
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	return indexedExpression(table, tableRegister, key, keyOperand, line), nil
}

func indexedExpression(
	table compiledExpression,
	tableRegister int,
	key compiledExpression,
	keyOperand int,
	line uint32,
) compiledExpression {
	return compiledExpression{
		kind:       expressionIndexed,
		info:       tableRegister,
		aux:        keyOperand,
		ownedBase:  lowestExpressionTemporary(table, key),
		line:       line,
		assignable: true,
	}
}
