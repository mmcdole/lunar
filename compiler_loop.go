package lua

// loopState records the lexical-scope depth at which a loop body begins and
// the pending exits produced by break statements. Loop control is separate
// from blockState: lexical blocks own locals and upvalue closing, while loops
// own only non-local control flow.
type loopState struct {
	blockBase int
	breaks    jumpList
}

func (function *functionState) enterLoop() {
	if function.registerTop != function.registerFloor {
		panic("lua: compiler loop entered with live temporaries")
	}
	function.loops = append(function.loops, loopState{
		blockBase: len(function.blocks),
	})
}

func (function *functionState) emitBreak(line uint32) *Error {
	if len(function.loops) == 0 {
		return newSourceSyntaxError(
			function.unit.sourceName.text,
			line,
			"no loop to break",
		)
	}
	loop := &function.loops[len(function.loops)-1]
	function.emitCloseForExit(loop.blockBase, line)
	exit := function.emitJump(line)
	breaks, syntaxError := function.joinJumps(exit, loop.breaks)
	if syntaxError != nil {
		return syntaxError
	}
	loop.breaks = breaks
	return nil
}

func (function *functionState) leaveLoop() *Error {
	index := len(function.loops) - 1
	if index < 0 {
		panic("lua: compiler loop stack underflow")
	}
	loop := function.loops[index]
	if len(function.blocks) != loop.blockBase {
		panic("lua: compiler left a loop with active lexical scopes")
	}
	if syntaxError := function.patchJumpsToHere(loop.breaks); syntaxError != nil {
		return syntaxError
	}
	function.loops = function.loops[:index]
	return nil
}

func (function *functionState) emitPendingBranch(
	operation opcode,
	register int,
	line uint32,
) int {
	switch operation {
	case opForPrep, opForLoop:
	default:
		panic("lua: invalid pending branch opcode")
	}
	pc := function.emitAsBx(operation, register, -1, line)
	function.unresolvedJumps++
	return pc
}

func (function *functionState) patchBranch(pc, target int) *Error {
	if pc < 0 || pc >= len(function.builder.code) {
		panic("lua: invalid pending branch")
	}
	switch function.builder.code[pc].opcode() {
	case opForPrep, opForLoop:
	default:
		panic("lua: instruction is not a pending branch")
	}
	if syntaxError := function.setControlTarget(pc, target); syntaxError != nil {
		return syntaxError
	}
	function.unresolvedJumps--
	return nil
}

func (parser *sourceParser) parseBreak() *Error {
	line := parser.current.line
	if syntaxError := parser.advance(); syntaxError != nil {
		return syntaxError
	}
	return parser.function.emitBreak(line)
}

func (parser *sourceParser) parseWhile() *Error {
	if syntaxError := parser.enterNesting(); syntaxError != nil {
		return syntaxError
	}
	defer parser.leaveNesting()

	line := parser.current.line
	if syntaxError := parser.advance(); syntaxError != nil {
		return syntaxError
	}
	emitter := parser.function
	conditionStart := emitter.currentPC()
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
	if _, syntaxError = parser.expect(tokenDo); syntaxError != nil {
		return syntaxError
	}

	emitter.enterLoop()
	emitter.enterBlock()
	if _, syntaxError = parser.parseBlock(); syntaxError != nil {
		return syntaxError
	}
	end, syntaxError := parser.expect(tokenEnd)
	if syntaxError != nil {
		return syntaxError
	}
	emitter.leaveBlock(end.line)

	backEdge := emitter.emitJump(line)
	if syntaxError = emitter.patchJumps(
		backEdge,
		conditionStart,
	); syntaxError != nil {
		return syntaxError
	}
	if syntaxError = emitter.patchConditionToHere(
		falseExits,
	); syntaxError != nil {
		return syntaxError
	}
	return emitter.leaveLoop()
}

func (parser *sourceParser) parseRepeat() *Error {
	if syntaxError := parser.enterNesting(); syntaxError != nil {
		return syntaxError
	}
	defer parser.leaveNesting()

	line := parser.current.line
	if syntaxError := parser.advance(); syntaxError != nil {
		return syntaxError
	}
	emitter := parser.function
	repeatStart := emitter.currentPC()
	emitter.enterLoop()
	emitter.enterBlock()
	if _, syntaxError := parser.parseBlock(); syntaxError != nil {
		return syntaxError
	}
	until, syntaxError := parser.expect(tokenUntil)
	if syntaxError != nil {
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

	body := emitter.blocks[len(emitter.blocks)-1]
	if !body.captured {
		emitter.leaveBlock(until.line)
		if syntaxError = emitter.patchCondition(
			falseExits,
			repeatStart,
		); syntaxError != nil {
			return syntaxError
		}
		return emitter.leaveLoop()
	}
	if falseExits == emptyJumpList {
		emitter.leaveBlock(until.line)
		return emitter.leaveLoop()
	}

	// The body scope remains active in the until condition. When one of its
	// locals is captured, both the successful exit and the next iteration
	// must close that iteration's cells before control leaves the scope.
	if syntaxError = emitter.emitBreak(condition.line); syntaxError != nil {
		return syntaxError
	}
	if syntaxError = emitter.patchConditionToHere(
		falseExits,
	); syntaxError != nil {
		return syntaxError
	}
	emitter.leaveBlock(until.line)
	backEdge := emitter.emitJump(line)
	if syntaxError = emitter.patchJumps(
		backEdge,
		repeatStart,
	); syntaxError != nil {
		return syntaxError
	}
	return emitter.leaveLoop()
}

func (parser *sourceParser) parseFor() *Error {
	if syntaxError := parser.enterNesting(); syntaxError != nil {
		return syntaxError
	}
	defer parser.leaveNesting()

	line := parser.current.line
	if syntaxError := parser.advance(); syntaxError != nil {
		return syntaxError
	}
	name, syntaxError := parser.expect(tokenName)
	if syntaxError != nil {
		return syntaxError
	}
	firstName := parser.unit.internToken(name)

	emitter := parser.function
	emitter.enterBlock()
	var end token
	switch parser.current.kind {
	case '=':
		end, syntaxError = parser.parseNumericFor(firstName, line)
	case ',', tokenIn:
		end, syntaxError = parser.parseGenericFor(firstName, line)
	default:
		return parser.syntaxError(
			parser.current.line,
			"expected = or in near %s",
			parser.current.kind,
		)
	}
	if syntaxError != nil {
		return syntaxError
	}
	emitter.leaveBlock(end.line)
	return nil
}

func (parser *sourceParser) parseNumericFor(
	name *internedText,
	line uint32,
) (token, *Error) {
	emitter := parser.function
	if len(emitter.locals)+4 > maxActiveLocals {
		return token{}, parser.syntaxError(
			line,
			"function has more than %d active locals",
			maxActiveLocals,
		)
	}
	if _, syntaxError := parser.expect('='); syntaxError != nil {
		return token{}, syntaxError
	}

	base := emitter.registerTop
	initial, syntaxError := parser.parseExpression()
	if syntaxError != nil {
		return token{}, syntaxError
	}
	if register, registerError := parser.expressionToNextRegister(
		&initial,
		initial.line,
	); registerError != nil {
		return token{}, registerError
	} else if register != base {
		panic("lua: compiler lost numeric-for initial register")
	}
	if _, syntaxError = parser.expect(','); syntaxError != nil {
		return token{}, syntaxError
	}

	limit, syntaxError := parser.parseExpression()
	if syntaxError != nil {
		return token{}, syntaxError
	}
	if register, registerError := parser.expressionToNextRegister(
		&limit,
		limit.line,
	); registerError != nil {
		return token{}, registerError
	} else if register != base+1 {
		panic("lua: compiler lost numeric-for limit register")
	}

	hasStep, syntaxError := parser.accept(',')
	if syntaxError != nil {
		return token{}, syntaxError
	}
	if hasStep {
		step, expressionError := parser.parseExpression()
		if expressionError != nil {
			return token{}, expressionError
		}
		if register, registerError := parser.expressionToNextRegister(
			&step,
			step.line,
		); registerError != nil {
			return token{}, registerError
		} else if register != base+2 {
			panic("lua: compiler lost numeric-for step register")
		}
	} else {
		step := compiledExpression{
			kind:     expressionConstant,
			constant: slotFromValue(Number(1)),
			line:     line,
		}
		if register, registerError := parser.expressionToNextRegister(
			&step,
			line,
		); registerError != nil {
			return token{}, registerError
		} else if register != base+2 {
			panic("lua: compiler lost numeric-for default step register")
		}
	}

	controls := []*internedText{
		parser.unit.internBorrowed("(for index)"),
		parser.unit.internBorrowed("(for limit)"),
		parser.unit.internBorrowed("(for step)"),
	}
	emitter.activateLocals(controls, base)
	emitter.enterLoop()
	if _, syntaxError = parser.expect(tokenDo); syntaxError != nil {
		return token{}, syntaxError
	}
	preparation := emitter.emitPendingBranch(opForPrep, base, line)

	emitter.enterBlock()
	variableBase, reserveError := emitter.reserveRegisters(1, line)
	if reserveError != nil {
		return token{}, reserveError
	}
	if variableBase != base+3 {
		panic("lua: compiler lost numeric-for variable register")
	}
	emitter.activateLocals([]*internedText{name}, variableBase)
	bodyStart := emitter.currentPC()
	if _, syntaxError = parser.parseBlock(); syntaxError != nil {
		return token{}, syntaxError
	}
	end, syntaxError := parser.expect(tokenEnd)
	if syntaxError != nil {
		return token{}, syntaxError
	}
	emitter.leaveBlock(end.line)

	loop := emitter.emitPendingBranch(opForLoop, base, line)
	if syntaxError = emitter.patchBranch(preparation, loop); syntaxError != nil {
		return token{}, syntaxError
	}
	if syntaxError = emitter.patchBranch(loop, bodyStart); syntaxError != nil {
		return token{}, syntaxError
	}
	if syntaxError = emitter.leaveLoop(); syntaxError != nil {
		return token{}, syntaxError
	}
	return end, nil
}

func (parser *sourceParser) parseGenericFor(
	firstName *internedText,
	line uint32,
) (token, *Error) {
	nameBase := len(parser.names)
	defer func() {
		parser.names = parser.names[:nameBase]
	}()
	parser.names = append(parser.names, firstName)
	for parser.current.kind == ',' {
		if syntaxError := parser.advance(); syntaxError != nil {
			return token{}, syntaxError
		}
		name, syntaxError := parser.expect(tokenName)
		if syntaxError != nil {
			return token{}, syntaxError
		}
		parser.names = append(
			parser.names,
			parser.unit.internToken(name),
		)
	}
	if _, syntaxError := parser.expect(tokenIn); syntaxError != nil {
		return token{}, syntaxError
	}
	names := parser.names[nameBase:]
	emitter := parser.function
	if len(emitter.locals)+3+len(names) > maxActiveLocals {
		return token{}, parser.syntaxError(
			line,
			"function has more than %d active locals",
			maxActiveLocals,
		)
	}

	values, syntaxError := parser.parseExpressionList()
	if syntaxError != nil {
		return token{}, syntaxError
	}
	defer parser.releaseExpressionList(values)
	base, syntaxError := parser.adjustExpressionList(values, 3, line)
	if syntaxError != nil {
		return token{}, syntaxError
	}
	controls := []*internedText{
		parser.unit.internBorrowed("(for generator)"),
		parser.unit.internBorrowed("(for state)"),
		parser.unit.internBorrowed("(for control)"),
	}
	emitter.activateLocals(controls, base)

	// TFORLOOP stages the generator and two arguments in A+3..A+5 before
	// installing its results. Those call slots exist even when C is 1 or 2.
	scratch, reserveError := emitter.reserveRegisters(3, line)
	if reserveError != nil {
		return token{}, reserveError
	}
	if scratch != base+3 {
		panic("lua: compiler lost generic-for scratch register order")
	}
	emitter.releaseRegisters(scratch)

	emitter.enterLoop()
	if _, syntaxError = parser.expect(tokenDo); syntaxError != nil {
		return token{}, syntaxError
	}
	preparation := emitter.emitJump(line)

	emitter.enterBlock()
	variableBase, reserveError := emitter.reserveRegisters(len(names), line)
	if reserveError != nil {
		return token{}, reserveError
	}
	if variableBase != base+3 {
		panic("lua: compiler lost generic-for variable registers")
	}
	emitter.activateLocals(names, variableBase)
	bodyStart := emitter.currentPC()
	if _, syntaxError = parser.parseBlock(); syntaxError != nil {
		return token{}, syntaxError
	}
	end, syntaxError := parser.expect(tokenEnd)
	if syntaxError != nil {
		return token{}, syntaxError
	}
	emitter.leaveBlock(end.line)

	iterator := emitter.currentPC()
	if syntaxError = emitter.patchJumps(
		preparation,
		iterator,
	); syntaxError != nil {
		return token{}, syntaxError
	}
	emitter.emitABC(opIteratorLoop, base, 0, len(names), line)
	backEdge := emitter.emitJump(line)
	if syntaxError = emitter.patchJumps(
		backEdge,
		bodyStart,
	); syntaxError != nil {
		return token{}, syntaxError
	}
	if syntaxError = emitter.leaveLoop(); syntaxError != nil {
		return token{}, syntaxError
	}
	return end, nil
}
