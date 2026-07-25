package lua

type assignmentTargetKind uint8

const (
	assignmentInvalid assignmentTargetKind = iota
	assignmentLocal
	assignmentUpvalue
	assignmentGlobal
	assignmentIndexed
)

type assignmentTarget struct {
	kind        assignmentTargetKind
	destination int
	key         int
}

func (parser *sourceParser) parsePrefixStatement() *Error {
	line := parser.current.line
	registerMark := parser.function.registerTop
	first, syntaxError := parser.parsePrefixExpression()
	if syntaxError != nil {
		return syntaxError
	}

	if parser.current.kind == ',' || parser.current.kind == '=' {
		return parser.parseAssignment(first, registerMark, line)
	}
	if first.kind == expressionCall {
		return parser.setCallResultCount(&first, 0, line)
	}
	if first.assignable {
		return parser.expected('=')
	}
	return parser.syntaxError(
		line,
		"statement is neither an assignment nor a function call",
	)
}

func (parser *sourceParser) parseAssignment(
	first compiledExpression,
	registerMark int,
	line uint32,
) *Error {
	targetBase := len(parser.targets)
	defer func() {
		parser.targets = parser.targets[:targetBase]
	}()

	if syntaxError := parser.appendAssignmentTarget(
		first,
		targetBase,
		line,
	); syntaxError != nil {
		return syntaxError
	}
	for parser.current.kind == ',' {
		if len(parser.targets)-targetBase == maxLuaRegisters {
			return parser.syntaxError(
				parser.current.line,
				"assignment has more than %d targets",
				maxLuaRegisters,
			)
		}
		if syntaxError := parser.advance(); syntaxError != nil {
			return syntaxError
		}
		target, syntaxError := parser.parsePrefixExpression()
		if syntaxError != nil {
			return syntaxError
		}
		if syntaxError = parser.appendAssignmentTarget(
			target,
			targetBase,
			target.line,
		); syntaxError != nil {
			return syntaxError
		}
	}
	if _, syntaxError := parser.expect('='); syntaxError != nil {
		return syntaxError
	}

	values, syntaxError := parser.parseExpressionList()
	if syntaxError != nil {
		return syntaxError
	}
	defer parser.releaseExpressionList(values)
	targets := parser.targets[targetBase:]
	storeLine := parser.previousLine
	if storeLine == 0 {
		storeLine = line
	}
	if syntaxError = parser.lowerAssignment(
		targets,
		values,
		storeLine,
	); syntaxError != nil {
		return syntaxError
	}
	parser.function.releaseRegisters(registerMark)
	return nil
}

func (parser *sourceParser) appendAssignmentTarget(
	expression compiledExpression,
	targetBase int,
	line uint32,
) *Error {
	target, syntaxError := parser.assignmentTarget(expression, line)
	if syntaxError != nil {
		return syntaxError
	}
	if target.kind == assignmentLocal {
		if syntaxError = parser.preserveAssignmentLocal(
			parser.targets[targetBase:],
			target.destination,
			line,
		); syntaxError != nil {
			return syntaxError
		}
	}
	parser.targets = append(parser.targets, target)
	return nil
}

func (parser *sourceParser) assignmentTarget(
	expression compiledExpression,
	line uint32,
) (assignmentTarget, *Error) {
	if !expression.assignable {
		return assignmentTarget{}, parser.syntaxError(
			line,
			"assignment target is not a variable",
		)
	}

	var target assignmentTarget
	switch expression.kind {
	case expressionLocal:
		target.kind = assignmentLocal
		target.destination = expression.info
	case expressionUpvalue:
		target.kind = assignmentUpvalue
		target.destination = expression.info
	case expressionGlobal:
		target.kind = assignmentGlobal
		target.destination = expression.info
	case expressionIndexed:
		target.kind = assignmentIndexed
		target.destination = expression.info
		target.key = expression.aux
	}
	if target.kind == assignmentInvalid {
		panic("lua: assignable expression has no assignment target")
	}
	return target, nil
}

// preserveAssignmentLocal snapshots a local used by an earlier indexed
// target before a later target can overwrite it. One register serves every
// conflicting table/key operand for this local.
func (parser *sourceParser) preserveAssignmentLocal(
	targets []assignmentTarget,
	localRegister int,
	line uint32,
) *Error {
	savedRegister := noRegister
	for index := range targets {
		target := &targets[index]
		if target.kind != assignmentIndexed {
			continue
		}
		tableConflict := target.destination == localRegister
		keyConflict := !isConstantOperand(target.key) &&
			target.key == localRegister
		if !tableConflict && !keyConflict {
			continue
		}
		if savedRegister == noRegister {
			var syntaxError *Error
			savedRegister, syntaxError =
				parser.function.reserveRegisters(1, line)
			if syntaxError != nil {
				return syntaxError
			}
			parser.function.emitABC(
				opMove,
				savedRegister,
				localRegister,
				0,
				line,
			)
		}
		if tableConflict {
			target.destination = savedRegister
		}
		if keyConflict {
			target.key = savedRegister
		}
	}
	return nil
}

func (parser *sourceParser) lowerAssignment(
	targets []assignmentTarget,
	values expressionList,
	line uint32,
) *Error {
	if len(targets) == 0 || values.valueCount == 0 {
		panic("lua: invalid assignment shape")
	}
	emitter := parser.function
	valueBase := values.registerBase

	if values.valueCount == len(targets) {
		last := len(targets) - 1
		for index := 0; index < last; index++ {
			value := &parser.values[values.valueBase+index]
			if value.kind != expressionTemporary ||
				value.info != valueBase+index {
				panic("lua: compiler lost assignment value order")
			}
		}
		lastValue := &parser.values[values.valueBase+last]
		if syntaxError := parser.emitAssignmentStore(
			&targets[last],
			lastValue,
			line,
		); syntaxError != nil {
			return syntaxError
		}
		emitter.releaseRegisters(valueBase + last)
		for index := last - 1; index >= 0; index-- {
			if syntaxError := parser.emitAssignmentRegisterStore(
				&targets[index],
				valueBase+index,
				line,
			); syntaxError != nil {
				return syntaxError
			}
		}
		return nil
	}

	base, syntaxError := parser.adjustExpressionList(
		values,
		len(targets),
		line,
	)
	if syntaxError != nil {
		return syntaxError
	}
	for index := len(targets) - 1; index >= 0; index-- {
		if syntaxError = parser.emitAssignmentRegisterStore(
			&targets[index],
			base+index,
			line,
		); syntaxError != nil {
			return syntaxError
		}
	}
	return nil
}

func (parser *sourceParser) emitAssignmentRegisterStore(
	target *assignmentTarget,
	register int,
	line uint32,
) *Error {
	value := compiledExpression{
		kind: expressionTemporary,
		info: register,
		line: line,
	}
	return parser.emitAssignmentStore(target, &value, line)
}

func (parser *sourceParser) emitAssignmentStore(
	target *assignmentTarget,
	value *compiledExpression,
	line uint32,
) *Error {
	emitter := parser.function
	switch target.kind {
	case assignmentLocal:
		return parser.writeExpression(
			value,
			target.destination,
			line,
		)
	case assignmentUpvalue:
		register, syntaxError := parser.expressionToRegister(
			value,
			line,
		)
		if syntaxError != nil {
			return syntaxError
		}
		emitter.emitABC(
			opSetUpvalue,
			register,
			target.destination,
			0,
			line,
		)
	case assignmentGlobal:
		register, syntaxError := parser.expressionToRegister(
			value,
			line,
		)
		if syntaxError != nil {
			return syntaxError
		}
		emitter.emitABx(
			opSetGlobal,
			register,
			target.destination,
			line,
		)
	case assignmentIndexed:
		operand, syntaxError := parser.expressionToRK(value, line)
		if syntaxError != nil {
			return syntaxError
		}
		emitter.emitABC(
			opSetTable,
			target.destination,
			target.key,
			operand,
			line,
		)
	default:
		panic("lua: invalid assignment target")
	}
	return nil
}
