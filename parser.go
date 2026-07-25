package lua

const (
	maxSyntaxNesting = 200
	maxActiveLocals  = 200
)

type sourceParser struct {
	unit         *compileUnit
	lexer        *lexer
	current      token
	function     *functionState
	names        []*luaString
	values       []compiledExpression
	targets      []assignmentTarget
	nesting      int
	previousLine uint32
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
	function, syntaxError := unit.newFunction(
		nil,
		0,
		0,
		varargIsVararg,
	)
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
	function.enterFunctionBlock()
	terminated, syntaxError := parser.parseBlock()
	if syntaxError != nil {
		return nil, syntaxError
	}
	if parser.current.kind != tokenEOF {
		return nil, parser.expected(tokenEOF)
	}
	if !terminated {
		function.leaveBlock(parser.current.line)
		function.emitABC(opReturn, 0, 1, 0, parser.current.line)
	} else {
		function.leaveBlock(parser.current.line)
	}
	return function.finish(0)
}

func (parser *sourceParser) advance() *Error {
	value, err := parser.lexer.next()
	if err != nil {
		return parser.lexerError(err)
	}
	parser.previousLine = parser.current.line
	parser.current = value
	return nil
}

func (parser *sourceParser) peekToken() (token, *Error) {
	value, err := parser.lexer.peek()
	if err != nil {
		return token{}, parser.lexerError(err)
	}
	return value, nil
}

func (parser *sourceParser) lexerError(err error) *Error {
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

func isBlockFollower(kind tokenKind) bool {
	switch kind {
	case tokenElse, tokenElseIf, tokenEnd, tokenUntil, tokenEOF:
		return true
	default:
		return false
	}
}

func (parser *sourceParser) parseBlock() (bool, *Error) {
	for !isBlockFollower(parser.current.kind) {
		terminal := parser.current.kind
		if terminal == tokenReturn || terminal == tokenBreak {
			var syntaxError *Error
			if terminal == tokenReturn {
				syntaxError = parser.parseReturn()
			} else {
				syntaxError = parser.parseBreak()
			}
			if syntaxError != nil {
				return false, syntaxError
			}
			if _, syntaxError = parser.accept(';'); syntaxError != nil {
				return false, syntaxError
			}
			if !isBlockFollower(parser.current.kind) {
				return false, parser.syntaxError(
					parser.current.line,
					"%s must be the last statement in its block",
					terminal,
				)
			}
			return true, nil
		}
		if syntaxError := parser.parseStatement(); syntaxError != nil {
			return false, syntaxError
		}
		if _, syntaxError := parser.accept(';'); syntaxError != nil {
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
	case tokenDo:
		return parser.parseDo()
	case tokenFunction:
		return parser.parseFunctionStatement()
	case tokenFor:
		return parser.parseFor()
	case tokenIf:
		return parser.parseIf()
	case tokenLocal:
		return parser.parseLocal()
	case tokenRepeat:
		return parser.parseRepeat()
	case tokenWhile:
		return parser.parseWhile()
	case tokenName, '(':
		return parser.parsePrefixStatement()
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
	_, syntaxError := parser.parseBlock()
	if syntaxError != nil {
		return syntaxError
	}
	end, syntaxError := parser.expect(tokenEnd)
	if syntaxError != nil {
		return syntaxError
	}
	parser.function.leaveBlock(end.line)
	return nil
}

func (parser *sourceParser) parseIf() *Error {
	if syntaxError := parser.enterNesting(); syntaxError != nil {
		return syntaxError
	}
	defer parser.leaveNesting()

	emitter := parser.function
	endExits := emptyJumpList
	for {
		if parser.current.kind != tokenIf &&
			parser.current.kind != tokenElseIf {
			panic("lua: compiler lost an if branch")
		}
		if syntaxError := parser.advance(); syntaxError != nil {
			return syntaxError
		}
		condition, syntaxError := parser.parseExpression()
		if syntaxError != nil {
			return syntaxError
		}
		falseExits, syntaxError := parser.conditionFalseExits(
			&condition,
			condition.line,
		)
		if syntaxError != nil {
			return syntaxError
		}
		if _, syntaxError = parser.expect(tokenThen); syntaxError != nil {
			return syntaxError
		}

		emitter.enterBlock()
		terminated, syntaxError := parser.parseBlock()
		if syntaxError != nil {
			return syntaxError
		}
		exitLine := parser.previousLine
		emitter.leaveBlock(exitLine)

		follower := parser.current.kind
		hasNextArm := follower == tokenElseIf || follower == tokenElse
		if !hasNextArm && follower != tokenEnd {
			return parser.expected(tokenEnd)
		}
		if hasNextArm && !terminated {
			exit := emitter.emitJump(exitLine)
			endExits, syntaxError = emitter.joinJumps(
				exit,
				endExits,
			)
			if syntaxError != nil {
				return syntaxError
			}
		}
		if syntaxError = emitter.patchConditionToHere(
			falseExits,
		); syntaxError != nil {
			return syntaxError
		}

		switch follower {
		case tokenElseIf:
			continue
		case tokenElse:
			if syntaxError = parser.advance(); syntaxError != nil {
				return syntaxError
			}
			emitter.enterBlock()
			_, syntaxError = parser.parseBlock()
			if syntaxError != nil {
				return syntaxError
			}
			exitLine = parser.previousLine
			emitter.leaveBlock(exitLine)
			if _, syntaxError = parser.expect(tokenEnd); syntaxError != nil {
				return syntaxError
			}
			return emitter.patchJumpsToHere(endExits)

		case tokenEnd:
			if _, syntaxError = parser.expect(tokenEnd); syntaxError != nil {
				return syntaxError
			}
			return emitter.patchJumpsToHere(endExits)
		}
	}
}

func (parser *sourceParser) parseLocal() *Error {
	line := parser.current.line
	if syntaxError := parser.advance(); syntaxError != nil {
		return syntaxError
	}
	if parser.current.kind == tokenFunction {
		return parser.parseLocalFunction(line)
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

func (parser *sourceParser) parseReturn() *Error {
	line := parser.current.line
	if syntaxError := parser.advance(); syntaxError != nil {
		return syntaxError
	}
	if isBlockFollower(parser.current.kind) ||
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

	if len(expressions) == 1 && !canExpandResults(*last) {
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

	if canExpandResults(*last) && last.kind == expressionCall {
		if last.aux != values.registerBase+lastIndex {
			panic("lua: compiler lost return call register order")
		}
		if len(expressions) == 1 {
			parser.makeTailCall(last)
		} else if syntaxError = parser.setCallResultCount(
			last,
			openResultCount,
			last.line,
		); syntaxError != nil {
			return syntaxError
		}
		emitter.emitABC(opReturn, values.registerBase, 0, 0, line)
		return nil
	}

	if canExpandResults(*last) && last.kind == expressionVararg {
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
			kind:       expressionLocal,
			info:       register,
			line:       line,
			assignable: true,
		}, nil
	}
	if index, ok, syntaxError := parser.function.resolveUpvalue(
		name,
		line,
	); syntaxError != nil {
		return compiledExpression{}, syntaxError
	} else if ok {
		return compiledExpression{
			kind:       expressionUpvalue,
			info:       index,
			line:       line,
			assignable: true,
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
		kind:       expressionGlobal,
		info:       index,
		line:       line,
		assignable: true,
	}, nil
}
