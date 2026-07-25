package lua

const openResultCount = -1

func canExpandResults(value compiledExpression) bool {
	if value.trueExits != emptyJumpList ||
		value.falseExits != emptyJumpList {
		return false
	}
	return value.kind == expressionCall ||
		value.kind == expressionVararg
}

func (parser *sourceParser) expressionToNextRegister(
	value *compiledExpression,
	line uint32,
) (int, *Error) {
	register, syntaxError := parser.expressionToTemporary(value, line)
	if syntaxError != nil {
		return 0, syntaxError
	}
	if register != parser.function.registerTop-1 {
		panic("lua: compiler call operand is not at the register top")
	}
	return register, nil
}

func (parser *sourceParser) forceSingleCallResult(
	value *compiledExpression,
) int {
	if value.kind != expressionCall {
		panic("lua: expression is not a call")
	}
	emitter := parser.function
	code := emitter.builder.code[value.info]
	if code.opcode() != opCall || code.a() != value.aux {
		panic("lua: malformed call expression")
	}
	if emitter.registerTop != value.aux+1 {
		panic("lua: call result is not at the register top")
	}
	emitter.builder.code[value.info] = code.withC(2)
	register := value.aux
	value.kind = expressionTemporary
	value.info = register
	value.aux = 0
	value.ownedBase = noRegister
	value.assignable = false
	return register
}

func (parser *sourceParser) setCallResultCount(
	value *compiledExpression,
	count int,
	line uint32,
) *Error {
	if value.kind != expressionCall || count < openResultCount {
		panic("lua: invalid call result adjustment")
	}
	emitter := parser.function
	code := emitter.builder.code[value.info]
	if code.opcode() != opCall || code.a() != value.aux {
		panic("lua: malformed call expression")
	}
	base := value.aux
	if emitter.registerTop != base+1 {
		panic("lua: adjusted call is not at the register top")
	}

	resultOperand := 0
	switch count {
	case openResultCount:
	case 0:
		resultOperand = 1
		emitter.releaseRegisters(base)
	default:
		if count >= maxOperandC {
			return parser.syntaxError(line, "call requests too many results")
		}
		resultOperand = count + 1
		if count > 1 {
			if _, syntaxError := emitter.reserveRegisters(
				count-1,
				line,
			); syntaxError != nil {
				return syntaxError
			}
		}
	}
	emitter.builder.code[value.info] = code.withC(resultOperand)
	return nil
}

func (parser *sourceParser) makeTailCall(
	value *compiledExpression,
) {
	if value.kind != expressionCall {
		panic("lua: expression is not a call")
	}
	emitter := parser.function
	code := emitter.builder.code[value.info]
	if code.opcode() != opCall || code.a() != value.aux {
		panic("lua: malformed call expression")
	}
	emitter.builder.code[value.info] = makeABC(
		opTailCall,
		code.a(),
		code.b(),
		0,
	)
}

func (parser *sourceParser) parseCallSuffix(
	function compiledExpression,
) (compiledExpression, *Error) {
	line := parser.current.line
	base, syntaxError := parser.expressionToNextRegister(
		&function,
		line,
	)
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	return parser.parseCallArguments(base, 0, line)
}

func (parser *sourceParser) parseMethodCall(
	receiver compiledExpression,
) (compiledExpression, *Error) {
	line := parser.current.line
	receiverRegister, syntaxError := parser.expressionToRegister(
		&receiver,
		line,
	)
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

	base := lowestExpressionTemporary(receiver)
	if base == noRegister {
		base = parser.function.registerTop
	} else {
		parser.function.releaseRegisters(base)
	}
	reserved, syntaxError := parser.function.reserveRegisters(2, line)
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	if reserved != base {
		panic("lua: compiler lost method call register order")
	}
	keyOperand, syntaxError := parser.expressionToRK(&key, name.line)
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	parser.function.emitABC(
		opSelf,
		base,
		receiverRegister,
		keyOperand,
		name.line,
	)
	parser.releaseExpressionTemporaries(key)
	return parser.parseCallArguments(base, 1, parser.current.line)
}

func (parser *sourceParser) parseCallArguments(
	base int,
	implicit int,
	line uint32,
) (compiledExpression, *Error) {
	explicit := 0
	openArguments := false

	switch parser.current.kind {
	case '(':
		if parser.current.line != parser.previousLine {
			return compiledExpression{}, parser.syntaxError(
				parser.current.line,
				"ambiguous syntax (function call x new statement) near %s",
				parser.current.kind,
			)
		}
		if syntaxError := parser.advance(); syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
		if parser.current.kind != ')' {
			list, syntaxError := parser.parseExpressionList()
			if syntaxError != nil {
				return compiledExpression{}, syntaxError
			}
			defer parser.releaseExpressionList(list)
			explicit = list.valueCount
			values := parser.values[list.valueBase : list.valueBase+list.valueCount]
			last := &values[len(values)-1]
			expected := base + 1 + implicit + len(values) - 1
			if canExpandResults(*last) {
				switch last.kind {
				case expressionCall:
					if last.aux != expected {
						panic("lua: compiler lost open call argument order")
					}
					if syntaxError = parser.setCallResultCount(
						last,
						openResultCount,
						last.line,
					); syntaxError != nil {
						return compiledExpression{}, syntaxError
					}
					openArguments = true
				case expressionVararg:
					target, reserveError :=
						parser.function.reserveRegisters(1, last.line)
					if reserveError != nil {
						return compiledExpression{}, reserveError
					}
					if target != expected {
						panic("lua: compiler lost open vararg argument order")
					}
					parser.function.emitABC(
						opVararg,
						target,
						0,
						0,
						last.line,
					)
					openArguments = true
				}
			} else {
				register, registerError :=
					parser.expressionToNextRegister(last, last.line)
				if registerError != nil {
					return compiledExpression{}, registerError
				}
				if register != expected {
					panic("lua: compiler lost call argument order")
				}
			}
		}
		if _, syntaxError := parser.expect(')'); syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
	case tokenString:
		value := parser.current
		text := parser.unit.internToken(value)
		if syntaxError := parser.advance(); syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
		argument := compiledExpression{
			kind:     expressionConstant,
			constant: prototypeStringSlot(text),
			line:     value.line,
		}
		register, syntaxError := parser.expressionToNextRegister(
			&argument,
			value.line,
		)
		if syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
		if register != base+1+implicit {
			panic("lua: compiler lost string argument order")
		}
		explicit = 1
	case '{':
		argument, syntaxError := parser.parseConstructor()
		if syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
		register, syntaxError := parser.expressionToNextRegister(
			&argument,
			argument.line,
		)
		if syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
		if register != base+1+implicit {
			panic("lua: compiler lost constructor argument order")
		}
		explicit = 1
	default:
		return compiledExpression{}, parser.syntaxError(
			parser.current.line,
			"function arguments expected near %s",
			parser.current.kind,
		)
	}

	argumentOperand := implicit + explicit + 1
	if openArguments {
		argumentOperand = 0
	}
	pc := parser.function.emitABC(
		opCall,
		base,
		argumentOperand,
		2,
		line,
	)
	parser.function.releaseRegisters(base + 1)
	return compiledExpression{
		kind: expressionCall,
		info: pc,
		aux:  base,
		line: line,
	}, nil
}
