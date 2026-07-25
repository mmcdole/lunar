package lua

const (
	maxSyntaxNesting = 200
	maxActiveLocals  = 200
)

type sourceParser struct {
	unit     *compileUnit
	lexer    *lexer
	current  token
	function *functionState
	names    []*luaString
	values   []compiledExpression
	nesting  int
}

// compileSource compiles one Lua source chunk directly into an immutable
// Prototype. It is private until load.go defines the public loading contract.
func compileSource(sourceName, source string) (*Prototype, *Error) {
	if uint64(len(source)) > uint64(^uint32(0)) {
		return nil, newSourceSyntaxError(
			sourceName,
			1,
			"source exceeds the compiler's 32-bit offset range",
		)
	}

	unit := newCompileUnit(sourceName)
	function, syntaxError := unit.newFunction(0, 0, varargIsVararg)
	if syntaxError != nil {
		return nil, syntaxError
	}
	parser := &sourceParser{
		unit:     unit,
		lexer:    newLexer(sourceName, source),
		function: function,
	}
	if syntaxError = parser.advance(); syntaxError != nil {
		return nil, syntaxError
	}
	function.enterBlock()
	returned, syntaxError := parser.parseBlock(tokenEOF)
	if syntaxError != nil {
		return nil, syntaxError
	}
	if parser.current.kind != tokenEOF {
		return nil, parser.expected(tokenEOF)
	}
	if !returned {
		function.emitABC(opReturn, 0, 1, 0, parser.current.line)
	}
	function.leaveBlock()
	return function.finish(0)
}

func (parser *sourceParser) advance() *Error {
	value, err := parser.lexer.next()
	if err != nil {
		if syntaxError, ok := err.(*Error); ok {
			return syntaxError
		}
		return &Error{
			value:       Nil(),
			description: err.Error(),
			category:    SyntaxError,
			cause:       err,
		}
	}
	parser.current = value
	return nil
}

func (parser *sourceParser) accept(kind tokenKind) (bool, *Error) {
	if parser.current.kind != kind {
		return false, nil
	}
	return true, parser.advance()
}

func (parser *sourceParser) expect(kind tokenKind) (token, *Error) {
	if parser.current.kind != kind {
		return token{}, parser.expected(kind)
	}
	value := parser.current
	return value, parser.advance()
}

func (parser *sourceParser) expected(kind tokenKind) *Error {
	return parser.syntaxError(
		parser.current.line,
		"expected %s near %s",
		kind,
		parser.current.kind,
	)
}

func (parser *sourceParser) syntaxError(
	line uint32,
	format string,
	arguments ...any,
) *Error {
	return newSourceSyntaxError(
		parser.unit.sourceName.text,
		line,
		format,
		arguments...,
	)
}

func (parser *sourceParser) enterNesting() *Error {
	if parser.nesting == maxSyntaxNesting {
		return parser.syntaxError(
			parser.current.line,
			"syntax nesting exceeds %d levels",
			maxSyntaxNesting,
		)
	}
	parser.nesting++
	return nil
}

func (parser *sourceParser) leaveNesting() {
	parser.nesting--
}

func (parser *sourceParser) parseBlock(stop tokenKind) (bool, *Error) {
	for parser.current.kind != stop {
		if parser.current.kind == tokenEOF {
			if stop == tokenEOF {
				return false, nil
			}
			return false, parser.expected(stop)
		}
		if parser.current.kind == tokenReturn {
			if syntaxError := parser.parseReturn(); syntaxError != nil {
				return false, syntaxError
			}
			if _, syntaxError := parser.accept(';'); syntaxError != nil {
				return false, syntaxError
			}
			if parser.current.kind != stop {
				return false, parser.syntaxError(
					parser.current.line,
					"return must be the last statement in its block",
				)
			}
			return true, nil
		}
		if syntaxError := parser.parseStatement(); syntaxError != nil {
			return false, syntaxError
		}
		parser.function.releaseRegisters(
			parser.function.registerFloor,
		)
	}
	return false, nil
}

func (parser *sourceParser) parseStatement() *Error {
	switch parser.current.kind {
	case ';':
		return parser.advance()
	case tokenDo:
		return parser.parseDo()
	case tokenLocal:
		return parser.parseLocal()
	case tokenName:
		return parser.parseAssignment()
	default:
		return parser.syntaxError(
			parser.current.line,
			"unsupported statement starting with %s",
			parser.current.kind,
		)
	}
}

func (parser *sourceParser) parseDo() *Error {
	if syntaxError := parser.enterNesting(); syntaxError != nil {
		return syntaxError
	}
	defer parser.leaveNesting()

	if syntaxError := parser.advance(); syntaxError != nil {
		return syntaxError
	}
	parser.function.enterBlock()
	_, syntaxError := parser.parseBlock(tokenEnd)
	if syntaxError != nil {
		return syntaxError
	}
	if _, syntaxError = parser.expect(tokenEnd); syntaxError != nil {
		return syntaxError
	}
	parser.function.leaveBlock()
	return nil
}

func (parser *sourceParser) parseLocal() *Error {
	line := parser.current.line
	if syntaxError := parser.advance(); syntaxError != nil {
		return syntaxError
	}

	nameBase := len(parser.names)
	defer func() {
		parser.names = parser.names[:nameBase]
	}()
	for {
		nameToken, syntaxError := parser.expect(tokenName)
		if syntaxError != nil {
			return syntaxError
		}
		parser.names = append(
			parser.names,
			parser.unit.internToken(nameToken),
		)
		more, syntaxError := parser.accept(',')
		if syntaxError != nil {
			return syntaxError
		}
		if !more {
			break
		}
	}
	names := parser.names[nameBase:]
	if len(parser.function.locals)+len(names) > maxActiveLocals {
		return parser.syntaxError(
			line,
			"function has more than %d active locals",
			maxActiveLocals,
		)
	}

	emitter := parser.function
	base := emitter.registerTop
	hasValues, syntaxError := parser.accept('=')
	if syntaxError != nil {
		return syntaxError
	}
	if hasValues {
		values, listError := parser.parseExpressionList()
		if listError != nil {
			return listError
		}
		defer parser.releaseExpressionList(values)
		base, syntaxError = parser.adjustExpressionList(
			values,
			len(names),
			line,
		)
		if syntaxError != nil {
			return syntaxError
		}
	} else {
		base, syntaxError = emitter.reserveRegisters(len(names), line)
		if syntaxError != nil {
			return syntaxError
		}
		emitter.emitABC(
			opLoadNil,
			base,
			base+len(names)-1,
			0,
			line,
		)
	}

	emitter.releaseRegisters(base + len(names))
	emitter.activateLocals(names, base)
	return nil
}

func (parser *sourceParser) parseAssignment() *Error {
	nameToken := parser.current
	name := parser.unit.internToken(nameToken)
	target, syntaxError := parser.resolveVariable(name, nameToken.line)
	if syntaxError != nil {
		return syntaxError
	}
	if syntaxError = parser.advance(); syntaxError != nil {
		return syntaxError
	}
	if _, syntaxError = parser.expect('='); syntaxError != nil {
		return syntaxError
	}
	values, syntaxError := parser.parseExpressionList()
	if syntaxError != nil {
		return syntaxError
	}
	defer parser.releaseExpressionList(values)
	if target.kind == expressionLocal && values.valueCount == 1 {
		value := &parser.values[values.valueBase]
		return parser.writeExpression(value, target.info, nameToken.line)
	}
	base, syntaxError := parser.adjustExpressionList(values, 1, nameToken.line)
	if syntaxError != nil {
		return syntaxError
	}
	switch target.kind {
	case expressionLocal:
		value := compiledExpression{
			kind: expressionTemporary,
			info: base,
			line: nameToken.line,
		}
		return parser.writeExpression(
			&value,
			target.info,
			nameToken.line,
		)
	case expressionGlobal:
		parser.function.emitABx(
			opSetGlobal,
			base,
			target.info,
			nameToken.line,
		)
		return nil
	default:
		panic("lua: assignment target is not a variable")
	}
}

func (parser *sourceParser) parseReturn() *Error {
	line := parser.current.line
	if syntaxError := parser.advance(); syntaxError != nil {
		return syntaxError
	}
	if parser.current.kind == tokenEOF ||
		parser.current.kind == tokenEnd ||
		parser.current.kind == ';' {
		parser.function.emitABC(opReturn, 0, 1, 0, line)
		return nil
	}

	emitter := parser.function
	values, syntaxError := parser.parseExpressionList()
	if syntaxError != nil {
		return syntaxError
	}
	defer parser.releaseExpressionList(values)
	expressions := parser.values[values.valueBase : values.valueBase+values.valueCount]
	lastIndex := len(expressions) - 1
	last := &expressions[lastIndex]

	if len(expressions) == 1 && last.kind != expressionVararg {
		register, registerError := parser.expressionToRegister(
			last,
			last.line,
		)
		if registerError != nil {
			return registerError
		}
		emitter.emitABC(opReturn, register, 2, 0, line)
		return nil
	}

	if last.kind == expressionVararg {
		target, reserveError := emitter.reserveRegisters(1, last.line)
		if reserveError != nil {
			return reserveError
		}
		if target != values.registerBase+lastIndex {
			panic("lua: compiler lost return-list register order")
		}
		emitter.emitABC(opVararg, target, 0, 0, last.line)
		emitter.emitABC(opReturn, values.registerBase, 0, 0, line)
		return nil
	}

	register, registerError := parser.expressionToTemporary(last, last.line)
	if registerError != nil {
		return registerError
	}
	if register != values.registerBase+lastIndex {
		panic("lua: compiler lost return-list register order")
	}
	emitter.emitABC(
		opReturn,
		values.registerBase,
		len(expressions)+1,
		0,
		line,
	)
	return nil
}

func (parser *sourceParser) resolveVariable(
	name *luaString,
	line uint32,
) (compiledExpression, *Error) {
	if register, ok := parser.function.localRegister(name); ok {
		return compiledExpression{
			kind: expressionLocal,
			info: register,
			line: line,
		}, nil
	}
	index, syntaxError := parser.function.constant(
		prototypeStringSlot(name),
		line,
	)
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	return compiledExpression{
		kind: expressionGlobal,
		info: index,
		line: line,
	}, nil
}
