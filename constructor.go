package lua

type constructorState struct {
	pendingList    compiledExpression
	tableRegister  int
	allocationPC   int
	listTotal      int
	recordTotal    int
	batchCount     int
	lastListLine   uint32
	hasPendingList bool
}

func (parser *sourceParser) parseConstructor() (
	compiledExpression,
	*Error,
) {
	line := parser.current.line
	if _, syntaxError := parser.expect('{'); syntaxError != nil {
		return compiledExpression{}, syntaxError
	}

	tableRegister, syntaxError := parser.function.reserveRegisters(1, line)
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	state := constructorState{
		tableRegister: tableRegister,
		allocationPC: parser.function.emitABC(
			opNewTable,
			tableRegister,
			0,
			0,
			line,
		),
	}

	for parser.current.kind != '}' {
		if state.hasPendingList {
			if syntaxError = parser.closeConstructorListField(
				&state,
			); syntaxError != nil {
				return compiledExpression{}, syntaxError
			}
		}
		if syntaxError = parser.parseConstructorField(
			&state,
		); syntaxError != nil {
			return compiledExpression{}, syntaxError
		}

		if parser.current.kind != ',' && parser.current.kind != ';' {
			if parser.current.kind != '}' {
				return compiledExpression{}, parser.expected('}')
			}
			break
		}
		if syntaxError = parser.advance(); syntaxError != nil {
			return compiledExpression{}, syntaxError
		}
		if parser.current.kind == '}' {
			break
		}
	}

	openFinal, syntaxError := parser.finishConstructorList(&state)
	if syntaxError != nil {
		return compiledExpression{}, syntaxError
	}
	if _, syntaxError = parser.expect('}'); syntaxError != nil {
		return compiledExpression{}, syntaxError
	}

	arrayHint := state.listTotal
	if openFinal {
		arrayHint--
	}
	allocation := parser.function.builder.code[state.allocationPC]
	if allocation.opcode() != opNewTable ||
		allocation.a() != tableRegister {
		panic("lua: malformed table constructor allocation")
	}
	parser.function.builder.code[state.allocationPC] = allocation.
		withB(intToFloatingByte(arrayHint)).
		withC(intToFloatingByte(state.recordTotal))

	return compiledExpression{
		kind: expressionTemporary,
		info: tableRegister,
		line: line,
	}, nil
}

func (parser *sourceParser) parseConstructorField(
	state *constructorState,
) *Error {
	if parser.current.kind == '[' {
		return parser.parseConstructorRecord(state, false)
	}
	if parser.current.kind == tokenName {
		next, syntaxError := parser.peekToken()
		if syntaxError != nil {
			return syntaxError
		}
		if next.kind == '=' {
			return parser.parseConstructorRecord(state, true)
		}
	}
	return parser.parseConstructorListField(state)
}

func (parser *sourceParser) parseConstructorListField(
	state *constructorState,
) *Error {
	if state.listTotal == maxSetListIndex {
		return parser.syntaxError(
			parser.current.line,
			"table constructor has too many list fields",
		)
	}
	value, syntaxError := parser.parseExpression()
	if syntaxError != nil {
		return syntaxError
	}
	state.listTotal++
	state.batchCount++
	state.pendingList = value
	state.hasPendingList = true
	return nil
}

func (parser *sourceParser) parseConstructorRecord(
	state *constructorState,
	nameKey bool,
) *Error {
	if state.recordTotal == maxSetListIndex {
		return parser.syntaxError(
			parser.current.line,
			"table constructor has too many record fields",
		)
	}
	mark := parser.function.registerTop
	line := parser.current.line

	var key compiledExpression
	if nameKey {
		name, syntaxError := parser.expect(tokenName)
		if syntaxError != nil {
			return syntaxError
		}
		key = compiledExpression{
			kind: expressionConstant,
			constant: prototypeStringSlot(
				parser.unit.internToken(name),
			),
			line: name.line,
		}
	} else {
		if _, syntaxError := parser.expect('['); syntaxError != nil {
			return syntaxError
		}
		var syntaxError *Error
		key, syntaxError = parser.parseExpression()
		if syntaxError != nil {
			return syntaxError
		}
		if _, syntaxError = parser.expect(']'); syntaxError != nil {
			return syntaxError
		}
	}
	if _, syntaxError := parser.expect('='); syntaxError != nil {
		return syntaxError
	}

	keyOperand, syntaxError := parser.expressionToRK(&key, key.line)
	if syntaxError != nil {
		return syntaxError
	}
	value, syntaxError := parser.parseExpression()
	if syntaxError != nil {
		return syntaxError
	}
	valueOperand, syntaxError := parser.expressionToRK(&value, value.line)
	if syntaxError != nil {
		return syntaxError
	}
	parser.function.emitABC(
		opSetTable,
		state.tableRegister,
		keyOperand,
		valueOperand,
		line,
	)
	parser.function.releaseRegisters(mark)
	state.recordTotal++
	return nil
}

func (parser *sourceParser) closeConstructorListField(
	state *constructorState,
) *Error {
	if !state.hasPendingList {
		panic("lua: table constructor has no pending list field")
	}
	line := state.pendingList.line
	state.lastListLine = line
	register, syntaxError := parser.expressionToNextRegister(
		&state.pendingList,
		line,
	)
	if syntaxError != nil {
		return syntaxError
	}
	expected := state.tableRegister + state.batchCount
	if register != expected {
		panic("lua: compiler lost table constructor register order")
	}
	state.pendingList = compiledExpression{}
	state.hasPendingList = false
	if state.batchCount == fieldsPerFlush {
		parser.emitConstructorSetList(state, false, line)
	}
	return nil
}

func (parser *sourceParser) finishConstructorList(
	state *constructorState,
) (bool, *Error) {
	if !state.hasPendingList {
		if state.batchCount != 0 {
			parser.emitConstructorSetList(
				state,
				false,
				state.lastListLine,
			)
		}
		return false, nil
	}
	line := state.pendingList.line
	if canExpandResults(state.pendingList) {
		expected := state.tableRegister + state.batchCount
		switch state.pendingList.kind {
		case expressionCall:
			if state.pendingList.aux != expected {
				panic("lua: compiler lost open constructor call order")
			}
			if syntaxError := parser.setCallResultCount(
				&state.pendingList,
				openResultCount,
				line,
			); syntaxError != nil {
				return false, syntaxError
			}
		case expressionVararg:
			register, syntaxError :=
				parser.function.reserveRegisters(1, line)
			if syntaxError != nil {
				return false, syntaxError
			}
			if register != expected {
				panic("lua: compiler lost open constructor vararg order")
			}
			parser.function.emitABC(
				opVararg,
				register,
				0,
				0,
				line,
			)
		default:
			panic("lua: expandable constructor value is not a call or vararg")
		}
		state.pendingList = compiledExpression{}
		state.hasPendingList = false
		parser.emitConstructorSetList(state, true, line)
		return true, nil
	}

	if syntaxError := parser.closeConstructorListField(
		state,
	); syntaxError != nil {
		return false, syntaxError
	}
	if state.batchCount != 0 {
		parser.emitConstructorSetList(state, false, line)
	}
	return false, nil
}

func (parser *sourceParser) emitConstructorSetList(
	state *constructorState,
	open bool,
	line uint32,
) {
	if state.batchCount <= 0 || state.batchCount > fieldsPerFlush {
		panic("lua: invalid table constructor flush width")
	}
	block := (state.listTotal-1)/fieldsPerFlush + 1
	count := state.batchCount
	if open {
		count = 0
	}
	if block <= maxOperandC {
		parser.function.emitABC(
			opSetList,
			state.tableRegister,
			count,
			block,
			line,
		)
	} else {
		parser.function.emitABC(
			opSetList,
			state.tableRegister,
			count,
			0,
			line,
		)
		parser.function.emit(instruction(block), line)
	}
	parser.function.releaseRegisters(state.tableRegister + 1)
	state.batchCount = 0
}
