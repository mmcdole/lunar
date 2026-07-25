package lua

import "fmt"

type instruction uint32

type opcode uint8

const (
	opMove opcode = iota
	opLoadK
	opLoadBool
	opLoadNil
	opGetUpvalue
	opGetGlobal
	opGetTable
	opSetGlobal
	opSetUpvalue
	opSetTable
	opNewTable
	opSelf
	opAdd
	opSub
	opMul
	opDiv
	opMod
	opPow
	opUnaryMinus
	opNot
	opLength
	opConcat
	opJump
	opEqual
	opLessThan
	opLessEqual
	opTest
	opTestSet
	opCall
	opTailCall
	opReturn
	opForLoop
	opForPrep
	opIteratorLoop
	opSetList
	opClose
	opClosure
	opVararg
	opCount
)

const (
	opcodeBits    = 6
	operandABits  = 8
	operandBBits  = 9
	operandCBits  = 9
	operandBxBits = operandBBits + operandCBits

	opcodeShift    = 0
	operandAShift  = opcodeShift + opcodeBits
	operandCShift  = operandAShift + operandABits
	operandBShift  = operandCShift + operandCBits
	operandBxShift = operandCShift

	maxOperandA   = (1 << operandABits) - 1
	maxOperandB   = (1 << operandBBits) - 1
	maxOperandC   = (1 << operandCBits) - 1
	maxOperandBx  = (1 << operandBxBits) - 1
	maxOperandsBx = maxOperandBx >> 1

	registerConstantBit = 1 << (operandBBits - 1)
	maxRegisterConstant = registerConstantBit - 1
	fieldsPerFlush      = 50
	noRegister          = maxOperandA
)

var operationNames = [...]string{
	opMove:         "MOVE",
	opLoadK:        "LOADK",
	opLoadBool:     "LOADBOOL",
	opLoadNil:      "LOADNIL",
	opGetUpvalue:   "GETUPVAL",
	opGetGlobal:    "GETGLOBAL",
	opGetTable:     "GETTABLE",
	opSetGlobal:    "SETGLOBAL",
	opSetUpvalue:   "SETUPVAL",
	opSetTable:     "SETTABLE",
	opNewTable:     "NEWTABLE",
	opSelf:         "SELF",
	opAdd:          "ADD",
	opSub:          "SUB",
	opMul:          "MUL",
	opDiv:          "DIV",
	opMod:          "MOD",
	opPow:          "POW",
	opUnaryMinus:   "UNM",
	opNot:          "NOT",
	opLength:       "LEN",
	opConcat:       "CONCAT",
	opJump:         "JMP",
	opEqual:        "EQ",
	opLessThan:     "LT",
	opLessEqual:    "LE",
	opTest:         "TEST",
	opTestSet:      "TESTSET",
	opCall:         "CALL",
	opTailCall:     "TAILCALL",
	opReturn:       "RETURN",
	opForLoop:      "FORLOOP",
	opForPrep:      "FORPREP",
	opIteratorLoop: "TFORLOOP",
	opSetList:      "SETLIST",
	opClose:        "CLOSE",
	opClosure:      "CLOSURE",
	opVararg:       "VARARG",
}

func (operation opcode) String() string {
	if operation >= opCount {
		return "INVALID"
	}
	return operationNames[operation]
}

func makeABC(operation opcode, a, b, c int) instruction {
	if operation >= opCount ||
		a < 0 || a > maxOperandA ||
		b < 0 || b > maxOperandB ||
		c < 0 || c > maxOperandC {
		panic(fmt.Sprintf(
			"lua: invalid ABC instruction %d %d %d %d",
			operation,
			a,
			b,
			c,
		))
	}
	return instruction(operation)<<opcodeShift |
		instruction(a)<<operandAShift |
		instruction(b)<<operandBShift |
		instruction(c)<<operandCShift
}

func makeABx(operation opcode, a, bx int) instruction {
	if operation >= opCount ||
		a < 0 || a > maxOperandA ||
		bx < 0 || bx > maxOperandBx {
		panic(fmt.Sprintf(
			"lua: invalid ABx instruction %d %d %d",
			operation,
			a,
			bx,
		))
	}
	return instruction(operation)<<opcodeShift |
		instruction(a)<<operandAShift |
		instruction(bx)<<operandBxShift
}

func makeAsBx(operation opcode, a, sbx int) instruction {
	if sbx < -maxOperandsBx || sbx > maxOperandsBx {
		panic(fmt.Sprintf(
			"lua: signed instruction operand %d is out of range",
			sbx,
		))
	}
	return makeABx(operation, a, sbx+maxOperandsBx)
}

func (code instruction) opcode() opcode {
	return opcode(code & ((1 << opcodeBits) - 1))
}

func (code instruction) a() int {
	return int(code>>operandAShift) & maxOperandA
}

func (code instruction) b() int {
	return int(code>>operandBShift) & maxOperandB
}

func (code instruction) c() int {
	return int(code>>operandCShift) & maxOperandC
}

func (code instruction) bx() int {
	return int(code>>operandBxShift) & maxOperandBx
}

func (code instruction) sbx() int {
	return code.bx() - maxOperandsBx
}

func (code instruction) withA(a int) instruction {
	if a < 0 || a > maxOperandA {
		panic("lua: instruction A operand is out of range")
	}
	mask := instruction(maxOperandA) << operandAShift
	return code&^mask | instruction(a)<<operandAShift
}

func (code instruction) withB(b int) instruction {
	if b < 0 || b > maxOperandB {
		panic("lua: instruction B operand is out of range")
	}
	mask := instruction(maxOperandB) << operandBShift
	return code&^mask | instruction(b)<<operandBShift
}

func (code instruction) withBx(bx int) instruction {
	if bx < 0 || bx > maxOperandBx {
		panic("lua: instruction Bx operand is out of range")
	}
	mask := instruction(maxOperandBx) << operandBxShift
	return code&^mask | instruction(bx)<<operandBxShift
}

func (code instruction) withSBx(sbx int) instruction {
	if sbx < -maxOperandsBx || sbx > maxOperandsBx {
		panic("lua: instruction sBx operand is out of range")
	}
	return code.withBx(sbx + maxOperandsBx)
}

func registerOrConstant(index int, constant bool) int {
	if index < 0 || index > maxRegisterConstant {
		panic("lua: register/constant operand is out of range")
	}
	if constant {
		return index | registerConstantBit
	}
	return index
}

func isConstantOperand(operand int) bool {
	return operand&registerConstantBit != 0
}

func constantIndex(operand int) int {
	return operand &^ registerConstantBit
}

func intToFloatingByte(value int) int {
	if value < 0 {
		panic("lua: negative floating-byte value")
	}
	exponent := 0
	for value >= 16 {
		value = (value >> 1) + (value & 1)
		exponent++
	}
	if value < 8 {
		return value
	}
	encoded := ((exponent + 1) << 3) | (value - 8)
	if encoded > 0xff {
		panic("lua: value exceeds floating-byte range")
	}
	return encoded
}

func floatingByteToInt(value int) int {
	if value < 0 || value > 0xff {
		panic("lua: floating byte is out of range")
	}
	if value < 8 {
		return value
	}
	mantissa := uint((value & 7) + 8)
	exponent := uint((value >> 3) - 1)
	decoded := mantissa << exponent
	maxInt := uint(^uint(0) >> 1)
	if decoded > maxInt {
		panic("lua: floating byte exceeds integer range")
	}
	return int(decoded)
}
