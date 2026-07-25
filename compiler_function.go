package lua

func (function *functionState) resolveUpvalue(
	name *luaString,
	line uint32,
) (int, bool, *Error) {
	parent := function.parent
	if parent == nil {
		return 0, false, nil
	}
	if localIndex, ok := parent.localIndex(name); ok {
		parent.markLocalCaptured(localIndex)
		index, syntaxError := function.addUpvalue(
			name,
			upvalueFromLocal,
			parent.locals[localIndex].register,
			line,
		)
		return index, true, syntaxError
	}

	parentIndex, ok, syntaxError := parent.resolveUpvalue(name, line)
	if syntaxError != nil || !ok {
		return 0, ok, syntaxError
	}
	index, syntaxError := function.addUpvalue(
		name,
		upvalueFromUpvalue,
		parentIndex,
		line,
	)
	return index, true, syntaxError
}

func (function *functionState) addUpvalue(
	name *luaString,
	source upvalueSource,
	index int,
	line uint32,
) (int, *Error) {
	for upvalueIndex, binding := range function.upvalues {
		if binding.source == source && binding.index == index {
			if function.builder.debug.upvalues[upvalueIndex] != name {
				panic("lua: one upvalue source resolved under two names")
			}
			return upvalueIndex, nil
		}
	}
	if len(function.upvalues) == maxLuaUpvalues {
		return 0, newSourceSyntaxError(
			function.unit.sourceName.text,
			line,
			"function has more than %d upvalues",
			maxLuaUpvalues,
		)
	}
	upvalueIndex := len(function.upvalues)
	function.upvalues = append(function.upvalues, upvalueBinding{
		source: source,
		index:  index,
	})
	function.builder.debug.upvalues = append(
		function.builder.debug.upvalues,
		name,
	)
	return upvalueIndex, nil
}

func (function *functionState) markLocalCaptured(localIndex int) {
	if localIndex < 0 || localIndex >= len(function.locals) {
		panic("lua: captured local index is out of range")
	}
	blockIndex := function.locals[localIndex].blockIndex
	if blockIndex < 0 || blockIndex >= len(function.blocks) {
		panic("lua: captured local has no owning block")
	}
	function.blocks[blockIndex].captured = true
}

func (parser *sourceParser) parseFunctionExpression() (
	compiledExpression,
	*Error,
) {
	if syntaxError := parser.advance(); syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	return parser.parseFunctionBody(parser.current.line, false)
}

func (parser *sourceParser) parseFunctionStatement() *Error {
	line := parser.current.line
	if syntaxError := parser.advance(); syntaxError != nil {
		return syntaxError
	}
	registerMark := parser.function.registerTop

	nameToken, syntaxError := parser.expect(tokenName)
	if syntaxError != nil {
		return syntaxError
	}
	targetExpression, syntaxError := parser.resolveVariable(
		parser.unit.internToken(nameToken),
		nameToken.line,
	)
	if syntaxError != nil {
		return syntaxError
	}
	for parser.current.kind == '.' {
		targetExpression, syntaxError = parser.parseFieldSuffix(
			targetExpression,
		)
		if syntaxError != nil {
			return syntaxError
		}
	}

	method := false
	if parser.current.kind == ':' {
		method = true
		targetExpression, syntaxError = parser.parseNamedFieldSuffix(
			targetExpression,
			':',
		)
		if syntaxError != nil {
			return syntaxError
		}
	}
	target, syntaxError := parser.assignmentTarget(targetExpression, line)
	if syntaxError != nil {
		return syntaxError
	}

	closure, syntaxError := parser.parseFunctionBody(line, method)
	if syntaxError != nil {
		return syntaxError
	}
	if syntaxError = parser.emitAssignmentStore(
		&target,
		&closure,
		line,
	); syntaxError != nil {
		return syntaxError
	}
	parser.function.fixLastLine(line)
	parser.function.releaseRegisters(registerMark)
	return nil
}

func (parser *sourceParser) parseLocalFunction(line uint32) *Error {
	if syntaxError := parser.advance(); syntaxError != nil {
		return syntaxError
	}
	nameToken, syntaxError := parser.expect(tokenName)
	if syntaxError != nil {
		return syntaxError
	}
	if len(parser.function.locals) == maxActiveLocals {
		return parser.syntaxError(
			line,
			"function has more than %d active locals",
			maxActiveLocals,
		)
	}

	emitter := parser.function
	register, syntaxError := emitter.reserveRegisters(1, line)
	if syntaxError != nil {
		return syntaxError
	}
	localIndex := len(emitter.locals)
	emitter.activateLocals(
		[]*luaString{parser.unit.internToken(nameToken)},
		register,
	)

	closure, syntaxError := parser.parseFunctionBody(
		parser.current.line,
		false,
	)
	if syntaxError != nil {
		return syntaxError
	}
	if syntaxError = parser.writeExpression(
		&closure,
		register,
		line,
	); syntaxError != nil {
		return syntaxError
	}
	local := emitter.locals[localIndex]
	emitter.builder.debug.locals[local.debugIndex].startPC =
		emitter.currentPC()
	return nil
}

func (parser *sourceParser) parseFunctionBody(
	line uint32,
	method bool,
) (compiledExpression, *Error) {
	if syntaxError := parser.enterNesting(); syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	defer parser.leaveNesting()

	if _, syntaxError := parser.expect('('); syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	nameBase := len(parser.names)
	defer func() {
		parser.names = parser.names[:nameBase]
	}()
	if method {
		parser.names = append(
			parser.names,
			parser.unit.internBorrowed("self"),
		)
	}

	vararg := false
	if parser.current.kind != ')' {
		for {
			if parser.current.kind == tokenDots {
				vararg = true
				if syntaxError := parser.advance(); syntaxError != nil {
					return compiledExpression{}, syntaxError
				}
				break
			}
			nameToken, syntaxError := parser.expect(tokenName)
			if syntaxError != nil {
				return compiledExpression{}, syntaxError
			}
			parser.names = append(
				parser.names,
				parser.unit.internToken(nameToken),
			)
			more, syntaxError := parser.accept(',')
			if syntaxError != nil {
				return compiledExpression{}, syntaxError
			}
			if !more {
				break
			}
		}
	}
	if _, syntaxError := parser.expect(')'); syntaxError != nil {
		return compiledExpression{}, syntaxError
	}

	parameters := len(parser.names) - nameBase
	varargFlags := 0
	if vararg {
		varargFlags = varargHasArg | varargIsVararg | varargNeedsArg
		parser.names = append(
			parser.names,
			parser.unit.internBorrowed("arg"),
		)
	}
	activeNames := parser.names[nameBase:]
	if len(activeNames) > maxActiveLocals {
		return compiledExpression{}, parser.syntaxError(
			line,
			"function has more than %d active locals",
			maxActiveLocals,
		)
	}

	parent := parser.function
	child, syntaxError := parser.unit.newFunction(
		parent,
		line,
		parameters,
		varargFlags,
	)
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	child.enterFunctionBlock()
	child.activateLocals(activeNames, 0)

	var closureLine uint32
	prototype, syntaxError := func() (*Prototype, *Error) {
		parser.function = child
		defer func() {
			parser.function = parent
		}()

		terminated, bodyError := parser.parseBlock()
		if bodyError != nil {
			return nil, bodyError
		}
		end, bodyError := parser.expect(tokenEnd)
		if bodyError != nil {
			return nil, bodyError
		}
		closureLine = end.line
		if !terminated {
			child.leaveBlock(end.line)
			child.emitABC(opReturn, 0, 1, 0, end.line)
		} else {
			child.leaveBlock(end.line)
		}
		return child.finish(end.line)
	}()
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	return parser.emitClosure(child, prototype, closureLine)
}

func (parser *sourceParser) emitClosure(
	child *functionState,
	prototype *Prototype,
	line uint32,
) (compiledExpression, *Error) {
	parent := parser.function
	if child.parent != parent {
		panic("lua: child function belongs to another parent")
	}
	if len(parent.builder.children) > maxOperandBx {
		return compiledExpression{}, parser.syntaxError(
			line,
			"function has too many child prototypes",
		)
	}
	if int(prototype.upvalues) != len(child.upvalues) {
		panic("lua: sealed child lost its upvalue layout")
	}

	childIndex := len(parent.builder.children)
	parent.builder.children = append(parent.builder.children, prototype)
	pc := parent.emitDeferredABx(opClosure, childIndex, line)
	for _, binding := range child.upvalues {
		switch binding.source {
		case upvalueFromLocal:
			parent.emitABC(opMove, 0, binding.index, 0, line)
		case upvalueFromUpvalue:
			parent.emitABC(opGetUpvalue, 0, binding.index, 0, line)
		default:
			panic("lua: invalid closure upvalue source")
		}
	}
	return compiledExpression{
		kind: expressionDeferred,
		info: pc,
		line: line,
	}, nil
}
