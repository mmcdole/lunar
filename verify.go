package lua

import "fmt"

const maxSetListIndex = 1<<31 - 1

type prototypeWordRole uint8

const (
	executableWord prototypeWordRole = iota
	setListExtraWord
	closureBindingWord
)

func verifyPrototypeHeader(
	prototype *Prototype,
	builder *prototypeBuilder,
) *Error {
	if builder.lineDefined < 0 ||
		builder.lastLine < 0 ||
		uint64(builder.lineDefined) > uint64(^uint32(0)) ||
		uint64(builder.lastLine) > uint64(^uint32(0)) {
		return newPrototypeSyntaxError(
			builder.sourceName,
			-1,
			"source line is out of range",
		)
	}
	if builder.lastLine < builder.lineDefined {
		return newPrototypeSyntaxError(
			builder.sourceName,
			-1,
			"function ends before it begins",
		)
	}
	if builder.registers < 1 || builder.registers > maxLuaRegisters {
		return newPrototypeSyntaxError(
			builder.sourceName,
			-1,
			"register count %d is outside Lua's range 1..%d",
			builder.registers,
			maxLuaRegisters,
		)
	}
	if builder.parameters < 0 || builder.parameters > builder.registers {
		return newPrototypeSyntaxError(
			builder.sourceName,
			-1,
			"fixed parameter count %d exceeds %d registers",
			builder.parameters,
			builder.registers,
		)
	}
	if builder.upvalues < 0 || builder.upvalues > maxLuaUpvalues {
		return newPrototypeSyntaxError(
			builder.sourceName,
			-1,
			"upvalue count %d is outside Lua's range 0..%d",
			builder.upvalues,
			maxLuaUpvalues,
		)
	}
	if builder.varargFlags < 0 || builder.varargFlags&^varargMask != 0 {
		return newPrototypeSyntaxError(
			builder.sourceName,
			-1,
			"invalid vararg flags %#x",
			builder.varargFlags,
		)
	}
	if builder.varargFlags&varargHasArg != 0 &&
		builder.varargFlags&varargIsVararg == 0 {
		return newPrototypeSyntaxError(
			builder.sourceName,
			-1,
			"legacy arg table requires a vararg function",
		)
	}
	if builder.varargFlags&varargNeedsArg != 0 &&
		builder.varargFlags&(varargHasArg|varargIsVararg) !=
			varargHasArg|varargIsVararg {
		return newPrototypeSyntaxError(
			builder.sourceName,
			-1,
			"required arg table is not declared",
		)
	}
	argumentRegisters := builder.parameters
	if builder.varargFlags&varargHasArg != 0 {
		argumentRegisters++
	}
	if argumentRegisters > builder.registers {
		return newPrototypeSyntaxError(
			builder.sourceName,
			-1,
			"parameters and legacy arg table exceed the register frame",
		)
	}

	prototype.lineDefined = uint32(builder.lineDefined)
	prototype.lastLine = uint32(builder.lastLine)
	prototype.parameters = uint8(builder.parameters)
	prototype.registers = uint8(builder.registers)
	prototype.upvalues = uint8(builder.upvalues)
	prototype.varargFlags = uint8(builder.varargFlags)

	debug, syntaxError := sealPrototypeDebug(
		builder.sourceName,
		len(prototype.code),
		builder.upvalues,
		builder.debug,
	)
	if syntaxError != nil {
		return syntaxError
	}
	prototype.debug = debug
	return nil
}

func sealPrototypeDebug(
	sourceName *luaString,
	codeLength int,
	upvalues int,
	builder *prototypeDebugBuilder,
) (*prototypeDebug, *Error) {
	if builder == nil {
		return nil, nil
	}
	if len(builder.lines) != 0 && len(builder.lines) != codeLength {
		return nil, newPrototypeSyntaxError(
			sourceName,
			-1,
			"debug line count %d does not match %d instruction words",
			len(builder.lines),
			codeLength,
		)
	}
	if len(builder.upvalues) > upvalues {
		return nil, newPrototypeSyntaxError(
			sourceName,
			-1,
			"debug upvalue count %d exceeds %d upvalues",
			len(builder.upvalues),
			upvalues,
		)
	}

	var lines []uint32
	if len(builder.lines) != 0 {
		lines = make([]uint32, len(builder.lines))
		for index, line := range builder.lines {
			if line < 0 || uint64(line) > uint64(^uint32(0)) {
				return nil, newPrototypeSyntaxError(
					sourceName,
					index,
					"debug source line is out of range",
				)
			}
			lines[index] = uint32(line)
		}
	}

	var locals []localInfo
	if len(builder.locals) != 0 {
		locals = make([]localInfo, len(builder.locals))
		for index, local := range builder.locals {
			if local.name == nil {
				return nil, newPrototypeSyntaxError(
					sourceName,
					-1,
					"local %d has no debug name",
					index,
				)
			}
			if local.startPC < 0 ||
				local.endPC < local.startPC ||
				local.endPC > codeLength {
				return nil, newPrototypeSyntaxError(
					sourceName,
					-1,
					"local %d has invalid lifetime [%d, %d)",
					index,
					local.startPC,
					local.endPC,
				)
			}
			locals[index] = localInfo{
				name:    local.name,
				startPC: uint32(local.startPC),
				endPC:   uint32(local.endPC),
			}
		}
	}
	for index, name := range builder.upvalues {
		if name == nil {
			return nil, newPrototypeSyntaxError(
				sourceName,
				-1,
				"upvalue %d has no debug name",
				index,
			)
		}
	}

	if len(lines) == 0 && len(locals) == 0 && len(builder.upvalues) == 0 {
		return nil, nil
	}
	return &prototypeDebug{
		lines:    lines,
		locals:   locals,
		upvalues: exactSlice(builder.upvalues),
	}, nil
}

func verifyPrototype(prototype *Prototype) *Error {
	if prototype == nil {
		return newPrototypeSyntaxError(nil, -1, "missing function prototype")
	}
	if len(prototype.code) == 0 {
		return prototype.syntaxError(-1, "function has no instructions")
	}
	if len(prototype.constants) > maxOperandBx+1 {
		return prototype.syntaxError(
			-1,
			"constant count %d exceeds the instruction format",
			len(prototype.constants),
		)
	}
	if len(prototype.children) > maxOperandBx+1 {
		return prototype.syntaxError(
			-1,
			"child function count %d exceeds the instruction format",
			len(prototype.children),
		)
	}
	for index, constant := range prototype.constants {
		if !validPrototypeConstant(constant) {
			return prototype.syntaxError(
				-1,
				"constant %d has a non-scalar or malformed value",
				index,
			)
		}
	}
	for index, child := range prototype.children {
		if child == nil {
			return prototype.syntaxError(
				-1,
				"child function %d is missing",
				index,
			)
		}
		if !child.sealed {
			return prototype.syntaxError(
				-1,
				"child function %d is not a sealed prototype",
				index,
			)
		}
	}

	roles, syntaxError := classifyPrototypeWords(prototype)
	if syntaxError != nil {
		return syntaxError
	}
	last := len(prototype.code) - 1
	if roles[last] != executableWord ||
		prototype.code[last].opcode() != opReturn {
		return prototype.syntaxError(last, "function does not end with RETURN")
	}

	for pc, role := range roles {
		if role != executableWord {
			continue
		}
		if syntaxError := verifyPrototypeInstruction(prototype, roles, pc); syntaxError != nil {
			return syntaxError
		}
	}
	if syntaxError := verifyOpenResultFlow(prototype, roles); syntaxError != nil {
		return syntaxError
	}
	return nil
}

func validPrototypeConstant(value slot) bool {
	switch value.ref {
	case nil:
		return true
	case nilMarkerPointer:
		return value.bits == uint64(NilKind)
	case falseMarkerPointer, trueMarkerPointer:
		return value.bits == uint64(BoolKind)
	default:
		return value.bits == uint64(StringKind)
	}
}

func classifyPrototypeWords(
	prototype *Prototype,
) ([]prototypeWordRole, *Error) {
	roles := make([]prototypeWordRole, len(prototype.code))
	for pc := 0; pc < len(prototype.code); pc++ {
		code := prototype.code[pc]
		operation := code.opcode()
		if operation >= opCount {
			return nil, prototype.syntaxError(
				pc,
				"unknown opcode %d",
				operation,
			)
		}

		switch operation {
		case opSetList:
			if code.c() != 0 {
				continue
			}
			extraPC := pc + 1
			if extraPC >= len(prototype.code) {
				return nil, prototype.syntaxError(
					pc,
					"SETLIST is missing its extended block number",
				)
			}
			if prototype.code[extraPC] == 0 {
				return nil, prototype.syntaxError(
					extraPC,
					"SETLIST extended block number is zero",
				)
			}
			block := uint64(prototype.code[extraPC])
			count := uint64(code.b())
			if count == 0 {
				// The executor checks the dynamic result count before adding
				// it. At publication time only the first possible index is
				// known.
				count = 1
			}
			lastIndex := (block-1)*fieldsPerFlush + count
			if lastIndex > maxSetListIndex {
				return nil, prototype.syntaxError(
					extraPC,
					"SETLIST index %d exceeds Lua's supported range",
					lastIndex,
				)
			}
			roles[extraPC] = setListExtraWord
			pc = extraPC

		case opClosure:
			childIndex := code.bx()
			if childIndex >= len(prototype.children) {
				return nil, prototype.syntaxError(
					pc,
					"CLOSURE child index %d is out of range",
					childIndex,
				)
			}
			child := prototype.children[childIndex]
			if child == nil {
				return nil, prototype.syntaxError(
					pc,
					"CLOSURE references a missing child",
				)
			}
			for binding := 0; binding < int(child.upvalues); binding++ {
				bindingPC := pc + 1 + binding
				if bindingPC >= len(prototype.code) {
					return nil, prototype.syntaxError(
						pc,
						"CLOSURE is missing upvalue binding %d",
						binding,
					)
				}
				if syntaxError := verifyClosureBinding(
					prototype,
					bindingPC,
				); syntaxError != nil {
					return nil, syntaxError
				}
				roles[bindingPC] = closureBindingWord
			}
			pc += int(child.upvalues)
		}
	}
	return roles, nil
}

func verifyClosureBinding(prototype *Prototype, pc int) *Error {
	code := prototype.code[pc]
	if syntaxError := prototype.checkZero(pc, code.c(), "C"); syntaxError != nil {
		return syntaxError
	}
	switch code.opcode() {
	case opMove:
		return prototype.checkRegisters(pc, code.a(), code.b())
	case opGetUpvalue:
		if syntaxError := prototype.checkRegister(
			pc,
			code.a(),
			"binding destination",
		); syntaxError != nil {
			return syntaxError
		}
		if code.b() >= int(prototype.upvalues) {
			return prototype.syntaxError(
				pc,
				"captured upvalue %d is out of range",
				code.b(),
			)
		}
		return nil
	default:
		return prototype.syntaxError(
			pc,
			"CLOSURE binding uses %s instead of MOVE or GETUPVAL",
			code.opcode(),
		)
	}
}

func verifyPrototypeInstruction(
	prototype *Prototype,
	roles []prototypeWordRole,
	pc int,
) *Error {
	code := prototype.code[pc]
	operation := code.opcode()

	switch operation {
	case opMove:
		if syntaxError := prototype.checkRegisters(pc, code.a(), code.b()); syntaxError != nil {
			return syntaxError
		}
		return prototype.checkZero(pc, code.c(), "C")

	case opLoadK:
		if syntaxError := prototype.checkRegister(pc, code.a(), "destination"); syntaxError != nil {
			return syntaxError
		}
		return prototype.checkConstant(pc, code.bx(), false)

	case opLoadBool:
		if syntaxError := prototype.checkRegister(pc, code.a(), "destination"); syntaxError != nil {
			return syntaxError
		}
		if code.b() > 1 || code.c() > 1 {
			return prototype.syntaxError(pc, "LOADBOOL operands must be booleans")
		}
		if code.c() != 0 {
			return prototype.checkControlTarget(roles, pc, pc+2, "LOADBOOL skip")
		}

	case opLoadNil:
		if code.b() < code.a() {
			return prototype.syntaxError(pc, "LOADNIL range is reversed")
		}
		return prototype.checkRegisters(pc, code.a(), code.b())

	case opGetUpvalue:
		if syntaxError := prototype.checkRegister(pc, code.a(), "destination"); syntaxError != nil {
			return syntaxError
		}
		if code.b() >= int(prototype.upvalues) {
			return prototype.syntaxError(pc, "upvalue %d is out of range", code.b())
		}
		return prototype.checkZero(pc, code.c(), "C")

	case opGetGlobal:
		if syntaxError := prototype.checkRegister(pc, code.a(), "destination"); syntaxError != nil {
			return syntaxError
		}
		return prototype.checkConstant(pc, code.bx(), true)

	case opGetTable:
		if syntaxError := prototype.checkRegisters(pc, code.a(), code.b()); syntaxError != nil {
			return syntaxError
		}
		return prototype.checkRegisterOrConstant(pc, code.c(), "table key")

	case opSetGlobal:
		if syntaxError := prototype.checkRegister(pc, code.a(), "source"); syntaxError != nil {
			return syntaxError
		}
		return prototype.checkConstant(pc, code.bx(), true)

	case opSetUpvalue:
		if syntaxError := prototype.checkRegister(pc, code.a(), "source"); syntaxError != nil {
			return syntaxError
		}
		if code.b() >= int(prototype.upvalues) {
			return prototype.syntaxError(pc, "upvalue %d is out of range", code.b())
		}
		return prototype.checkZero(pc, code.c(), "C")

	case opSetTable:
		if syntaxError := prototype.checkRegister(pc, code.a(), "table"); syntaxError != nil {
			return syntaxError
		}
		if syntaxError := prototype.checkRegisterOrConstant(pc, code.b(), "table key"); syntaxError != nil {
			return syntaxError
		}
		return prototype.checkRegisterOrConstant(pc, code.c(), "table value")

	case opNewTable:
		if syntaxError := prototype.checkRegister(pc, code.a(), "destination"); syntaxError != nil {
			return syntaxError
		}
		if code.b() > 0xff || code.c() > 0xff {
			return prototype.syntaxError(pc, "NEWTABLE hint is not a floating byte")
		}

	case opSelf:
		if syntaxError := prototype.checkRegisterRange(pc, code.a(), 2, "SELF result"); syntaxError != nil {
			return syntaxError
		}
		if syntaxError := prototype.checkRegister(pc, code.b(), "receiver"); syntaxError != nil {
			return syntaxError
		}
		return prototype.checkRegisterOrConstant(pc, code.c(), "method key")

	case opAdd, opSub, opMul, opDiv, opMod, opPow:
		if syntaxError := prototype.checkRegister(pc, code.a(), "destination"); syntaxError != nil {
			return syntaxError
		}
		if syntaxError := prototype.checkRegisterOrConstant(pc, code.b(), "left operand"); syntaxError != nil {
			return syntaxError
		}
		return prototype.checkRegisterOrConstant(pc, code.c(), "right operand")

	case opUnaryMinus, opNot, opLength:
		if syntaxError := prototype.checkRegisters(pc, code.a(), code.b()); syntaxError != nil {
			return syntaxError
		}
		return prototype.checkZero(pc, code.c(), "C")

	case opConcat:
		if syntaxError := prototype.checkRegister(pc, code.a(), "destination"); syntaxError != nil {
			return syntaxError
		}
		if code.b() >= code.c() {
			return prototype.syntaxError(
				pc,
				"CONCAT requires at least two ordered registers",
			)
		}
		return prototype.checkRegisters(pc, code.b(), code.c())

	case opJump:
		if code.a() != 0 {
			return prototype.syntaxError(pc, "JMP has a nonzero unused A operand")
		}
		return prototype.checkControlTarget(
			roles,
			pc,
			pc+1+code.sbx(),
			"JMP",
		)

	case opEqual, opLessThan, opLessEqual:
		if code.a() > 1 {
			return prototype.syntaxError(pc, "%s inversion flag is not boolean", operation)
		}
		if syntaxError := prototype.checkRegisterOrConstant(pc, code.b(), "left operand"); syntaxError != nil {
			return syntaxError
		}
		if syntaxError := prototype.checkRegisterOrConstant(pc, code.c(), "right operand"); syntaxError != nil {
			return syntaxError
		}
		return prototype.checkTestFollower(roles, pc)

	case opTest:
		if syntaxError := prototype.checkRegister(pc, code.a(), "condition"); syntaxError != nil {
			return syntaxError
		}
		if code.b() != 0 || code.c() > 1 {
			return prototype.syntaxError(pc, "TEST has invalid control operands")
		}
		return prototype.checkTestFollower(roles, pc)

	case opTestSet:
		if syntaxError := prototype.checkRegisters(pc, code.a(), code.b()); syntaxError != nil {
			return syntaxError
		}
		if code.c() > 1 {
			return prototype.syntaxError(pc, "TESTSET condition flag is not boolean")
		}
		return prototype.checkTestFollower(roles, pc)

	case opCall:
		if syntaxError := prototype.checkRegister(pc, code.a(), "function"); syntaxError != nil {
			return syntaxError
		}
		if code.b() != 0 {
			if syntaxError := prototype.checkRegisterRange(
				pc,
				code.a(),
				code.b(),
				"CALL arguments",
			); syntaxError != nil {
				return syntaxError
			}
		}
		if code.c() > 1 {
			return prototype.checkRegisterRange(
				pc,
				code.a(),
				code.c()-1,
				"CALL results",
			)
		}

	case opTailCall:
		if syntaxError := prototype.checkRegister(pc, code.a(), "function"); syntaxError != nil {
			return syntaxError
		}
		if code.c() != 0 {
			return prototype.syntaxError(pc, "TAILCALL has a nonzero unused C operand")
		}
		if code.b() != 0 {
			return prototype.checkRegisterRange(
				pc,
				code.a(),
				code.b(),
				"TAILCALL arguments",
			)
		}
		return nil

	case opReturn:
		if syntaxError := prototype.checkRegister(pc, code.a(), "return base"); syntaxError != nil {
			return syntaxError
		}
		if code.c() != 0 {
			return prototype.syntaxError(pc, "RETURN has a nonzero unused C operand")
		}
		if code.b() > 1 {
			return prototype.checkRegisterRange(
				pc,
				code.a(),
				code.b()-1,
				"RETURN values",
			)
		}
		return nil

	case opForLoop, opForPrep:
		if syntaxError := prototype.checkRegisterRange(
			pc,
			code.a(),
			4,
			operation.String()+" registers",
		); syntaxError != nil {
			return syntaxError
		}
		if syntaxError := prototype.checkControlTarget(
			roles,
			pc,
			pc+1+code.sbx(),
			operation.String(),
		); syntaxError != nil {
			return syntaxError
		}
		if operation == opForPrep {
			return nil
		}

	case opIteratorLoop:
		if code.b() != 0 || code.c() == 0 {
			return prototype.syntaxError(pc, "TFORLOOP has invalid result operands")
		}
		if syntaxError := prototype.checkRegisterRange(
			pc,
			code.a(),
			code.c()+3,
			"TFORLOOP registers",
		); syntaxError != nil {
			return syntaxError
		}
		return prototype.checkTestFollower(roles, pc)

	case opSetList:
		if syntaxError := prototype.checkRegister(pc, code.a(), "table"); syntaxError != nil {
			return syntaxError
		}
		if code.b() != 0 {
			if syntaxError := prototype.checkRegisterRange(
				pc,
				code.a()+1,
				code.b(),
				"SETLIST values",
			); syntaxError != nil {
				return syntaxError
			}
		}
		nextPC := pc + 1
		if code.c() == 0 {
			nextPC++
		}
		return prototype.checkFallthrough(roles, pc, nextPC)

	case opClose:
		if syntaxError := prototype.checkRegister(pc, code.a(), "close base"); syntaxError != nil {
			return syntaxError
		}
		if code.b() != 0 || code.c() != 0 {
			return prototype.syntaxError(pc, "CLOSE has nonzero unused operands")
		}

	case opClosure:
		if syntaxError := prototype.checkRegister(pc, code.a(), "destination"); syntaxError != nil {
			return syntaxError
		}
		if code.bx() >= len(prototype.children) {
			return prototype.syntaxError(pc, "CLOSURE child is out of range")
		}
		nextPC := pc + 1 + int(prototype.children[code.bx()].upvalues)
		return prototype.checkFallthrough(roles, pc, nextPC)

	case opVararg:
		if prototype.varargFlags&varargIsVararg == 0 {
			return prototype.syntaxError(pc, "VARARG appears in a fixed-arity function")
		}
		if prototype.varargFlags&varargNeedsArg != 0 {
			return prototype.syntaxError(
				pc,
				"VARARG cannot execute while the legacy arg table is required",
			)
		}
		if syntaxError := prototype.checkRegister(pc, code.a(), "result base"); syntaxError != nil {
			return syntaxError
		}
		if code.c() != 0 {
			return prototype.syntaxError(pc, "VARARG has a nonzero unused C operand")
		}
		if code.b() > 1 {
			return prototype.checkRegisterRange(
				pc,
				code.a(),
				code.b()-1,
				"VARARG results",
			)
		}

	default:
		return prototype.syntaxError(pc, "unknown opcode %d", operation)
	}

	return prototype.checkFallthrough(roles, pc, pc+1)
}

func verifyOpenResultFlow(
	prototype *Prototype,
	roles []prototypeWordRole,
) *Error {
	for pc, role := range roles {
		if role != executableWord {
			continue
		}
		code := prototype.code[pc]
		if isOpenResultProducer(code) {
			nextPC := pc + 1
			if nextPC >= len(prototype.code) ||
				roles[nextPC] == setListExtraWord ||
				!isOpenResultConsumer(prototype.code[nextPC]) {
				return prototype.syntaxError(
					pc,
					"open results are not consumed by the following instruction",
				)
			}
		}
	}
	return nil
}

func isOpenResultProducer(code instruction) bool {
	switch code.opcode() {
	case opCall, opTailCall:
		return code.c() == 0
	case opVararg:
		return code.b() == 0
	default:
		return false
	}
}

func isOpenResultConsumer(code instruction) bool {
	switch code.opcode() {
	case opCall, opTailCall:
		return code.b() == 0
	case opReturn, opSetList:
		return code.b() == 0
	default:
		return false
	}
}

func (prototype *Prototype) checkRegister(
	pc int,
	register int,
	purpose string,
) *Error {
	if register < int(prototype.registers) {
		return nil
	}
	return prototype.syntaxError(
		pc,
		"%s register %d is outside the %d-register frame",
		purpose,
		register,
		prototype.registers,
	)
}

func (prototype *Prototype) checkRegisters(
	pc int,
	registers ...int,
) *Error {
	for _, register := range registers {
		if syntaxError := prototype.checkRegister(pc, register, "operand"); syntaxError != nil {
			return syntaxError
		}
	}
	return nil
}

func (prototype *Prototype) checkRegisterRange(
	pc int,
	first int,
	count int,
	purpose string,
) *Error {
	if count <= 0 {
		return prototype.syntaxError(pc, "%s has an invalid width %d", purpose, count)
	}
	last := first + count - 1
	if first < 0 || last < first || last >= int(prototype.registers) {
		return prototype.syntaxError(
			pc,
			"%s [%d, %d] exceeds the %d-register frame",
			purpose,
			first,
			last,
			prototype.registers,
		)
	}
	return nil
}

func (prototype *Prototype) checkRegisterOrConstant(
	pc int,
	operand int,
	purpose string,
) *Error {
	if isConstantOperand(operand) {
		return prototype.checkConstant(pc, constantIndex(operand), false)
	}
	return prototype.checkRegister(pc, operand, purpose)
}

func (prototype *Prototype) checkConstant(
	pc int,
	index int,
	requireString bool,
) *Error {
	if index < 0 || index >= len(prototype.constants) {
		return prototype.syntaxError(pc, "constant %d is out of range", index)
	}
	if requireString && prototype.constants[index].kind() != StringKind {
		return prototype.syntaxError(pc, "global name constant %d is not a string", index)
	}
	return nil
}

func (prototype *Prototype) checkZero(
	pc int,
	value int,
	operand string,
) *Error {
	if value == 0 {
		return nil
	}
	return prototype.syntaxError(pc, "unused %s operand is nonzero", operand)
}

func (prototype *Prototype) checkTestFollower(
	roles []prototypeWordRole,
	pc int,
) *Error {
	jumpPC := pc + 1
	if jumpPC >= len(prototype.code) ||
		roles[jumpPC] != executableWord ||
		prototype.code[jumpPC].opcode() != opJump {
		return prototype.syntaxError(pc, "test instruction is not followed by JMP")
	}
	return prototype.checkControlTarget(roles, pc, pc+2, "test skip")
}

func (prototype *Prototype) checkFallthrough(
	roles []prototypeWordRole,
	pc int,
	target int,
) *Error {
	if target >= len(prototype.code) {
		return prototype.syntaxError(pc, "execution falls beyond the function")
	}
	if roles[target] != executableWord {
		return prototype.syntaxError(pc, "execution enters instruction metadata")
	}
	return nil
}

func (prototype *Prototype) checkControlTarget(
	roles []prototypeWordRole,
	pc int,
	target int,
	operation string,
) *Error {
	if target < 0 || target >= len(prototype.code) {
		return prototype.syntaxError(
			pc,
			"%s target %d is outside the function",
			operation,
			target,
		)
	}
	if roles[target] == setListExtraWord {
		return prototype.syntaxError(
			pc,
			"%s target %d enters instruction metadata",
			operation,
			target,
		)
	}
	return nil
}

func (prototype *Prototype) syntaxError(
	pc int,
	format string,
	arguments ...any,
) *Error {
	var sourceName *luaString
	if prototype != nil {
		sourceName = prototype.sourceName
	}
	return newPrototypeSyntaxError(sourceName, pc, format, arguments...)
}

func newPrototypeSyntaxError(
	sourceName *luaString,
	pc int,
	format string,
	arguments ...any,
) *Error {
	message := fmt.Sprintf(format, arguments...)
	source := prototypeStringText(sourceName)
	switch {
	case source != "" && pc >= 0:
		message = fmt.Sprintf(
			"%s: instruction %d: %s",
			sourceID(source),
			pc,
			message,
		)
	case source != "":
		message = fmt.Sprintf("%s: %s", sourceID(source), message)
	case pc >= 0:
		message = fmt.Sprintf("instruction %d: %s", pc, message)
	}
	return &Error{
		value:       Nil(),
		description: message,
		category:    SyntaxError,
	}
}
