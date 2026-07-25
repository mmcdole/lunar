package lua

import (
	"strconv"
	"testing"
	"unsafe"
)

func TestLua51OpcodeOrderAndNames(t *testing.T) {
	names := [...]string{
		"MOVE",
		"LOADK",
		"LOADBOOL",
		"LOADNIL",
		"GETUPVAL",
		"GETGLOBAL",
		"GETTABLE",
		"SETGLOBAL",
		"SETUPVAL",
		"SETTABLE",
		"NEWTABLE",
		"SELF",
		"ADD",
		"SUB",
		"MUL",
		"DIV",
		"MOD",
		"POW",
		"UNM",
		"NOT",
		"LEN",
		"CONCAT",
		"JMP",
		"EQ",
		"LT",
		"LE",
		"TEST",
		"TESTSET",
		"CALL",
		"TAILCALL",
		"RETURN",
		"FORLOOP",
		"FORPREP",
		"TFORLOOP",
		"SETLIST",
		"CLOSE",
		"CLOSURE",
		"VARARG",
	}

	if got, want := int(opCount), len(names); got != want {
		t.Fatalf("opcode count = %d; want %d", got, want)
	}
	if got := unsafe.Sizeof(instruction(0)); got != 4 {
		t.Fatalf("instruction size = %d; want 4", got)
	}
	for index, name := range names {
		operation := opcode(index)
		if got := int(operation); got != index {
			t.Errorf("%s opcode = %d; want %d", name, got, index)
		}
		if got := operation.String(); got != name {
			t.Errorf("opcode %d name = %q; want %q", index, got, name)
		}
	}
	if got := opCount.String(); got != "INVALID" {
		t.Fatalf("sentinel name = %q; want INVALID", got)
	}
	if got := opcode(0xff).String(); got != "INVALID" {
		t.Fatalf("out-of-range name = %q; want INVALID", got)
	}
}

func TestInstructionFieldsAndPatching(t *testing.T) {
	abc := makeABC(opSetTable, 0x12, 0x101, 0x55)
	wantABC := instruction(opSetTable) |
		instruction(0x12)<<operandAShift |
		instruction(0x55)<<operandCShift |
		instruction(0x101)<<operandBShift
	if abc != wantABC {
		t.Fatalf("ABC encoding = %#08x; want %#08x", abc, wantABC)
	}
	if abc.opcode() != opSetTable || abc.a() != 0x12 || abc.b() != 0x101 || abc.c() != 0x55 {
		t.Fatalf(
			"ABC decoded as %s A=%d B=%d C=%d",
			abc.opcode(),
			abc.a(),
			abc.b(),
			abc.c(),
		)
	}

	maxABC := makeABC(opVararg, maxOperandA, maxOperandB, maxOperandC)
	if maxABC.opcode() != opVararg ||
		maxABC.a() != maxOperandA ||
		maxABC.b() != maxOperandB ||
		maxABC.c() != maxOperandC {
		t.Fatalf(
			"maximum ABC decoded as %s A=%d B=%d C=%d",
			maxABC.opcode(),
			maxABC.a(),
			maxABC.b(),
			maxABC.c(),
		)
	}

	abx := makeABx(opLoadK, 0xa5, 0x2aaaa)
	wantABx := instruction(opLoadK) |
		instruction(0xa5)<<operandAShift |
		instruction(0x2aaaa)<<operandBxShift
	if abx != wantABx {
		t.Fatalf("ABx encoding = %#08x; want %#08x", abx, wantABx)
	}
	if abx.opcode() != opLoadK || abx.a() != 0xa5 || abx.bx() != 0x2aaaa {
		t.Fatalf("ABx decoded as %s A=%d Bx=%d", abx.opcode(), abx.a(), abx.bx())
	}
	for _, fields := range []struct {
		a  int
		bx int
	}{
		{0, 0},
		{maxOperandA, maxOperandBx},
	} {
		code := makeABx(opClosure, fields.a, fields.bx)
		if code.a() != fields.a || code.bx() != fields.bx {
			t.Errorf(
				"ABx A=%d Bx=%d decoded as A=%d Bx=%d",
				fields.a,
				fields.bx,
				code.a(),
				code.bx(),
			)
		}
	}

	for _, offset := range []int{-maxOperandsBx, -1, 0, 1, maxOperandsBx} {
		code := makeAsBx(opJump, 0x2a, offset)
		if code.opcode() != opJump || code.a() != 0x2a || code.sbx() != offset {
			t.Errorf(
				"AsBx %d decoded as %s A=%d sBx=%d",
				offset,
				code.opcode(),
				code.a(),
				code.sbx(),
			)
		}
	}

	patchedA := abc.withA(0xef)
	if patchedA.opcode() != abc.opcode() ||
		patchedA.a() != 0xef ||
		patchedA.b() != abc.b() ||
		patchedA.c() != abc.c() {
		t.Fatalf("patching A changed another field: before %#08x, after %#08x", abc, patchedA)
	}

	patchedB := abc.withB(0x1aa)
	if patchedB.opcode() != abc.opcode() ||
		patchedB.a() != abc.a() ||
		patchedB.b() != 0x1aa ||
		patchedB.c() != abc.c() {
		t.Fatalf("patching B changed another field: before %#08x, after %#08x", abc, patchedB)
	}

	patchedC := abc.withC(0x155)
	if patchedC.opcode() != abc.opcode() ||
		patchedC.a() != abc.a() ||
		patchedC.b() != abc.b() ||
		patchedC.c() != 0x155 {
		t.Fatalf("patching C changed another field: before %#08x, after %#08x", abc, patchedC)
	}

	patchedBx := abx.withBx(0x15555)
	if patchedBx.opcode() != abx.opcode() ||
		patchedBx.a() != abx.a() ||
		patchedBx.bx() != 0x15555 {
		t.Fatalf("patching Bx changed another field: before %#08x, after %#08x", abx, patchedBx)
	}

	patchedSBx := abx.withSBx(-12345)
	if patchedSBx.opcode() != abx.opcode() ||
		patchedSBx.a() != abx.a() ||
		patchedSBx.sbx() != -12345 {
		t.Fatalf("patching sBx changed another field: before %#08x, after %#08x", abx, patchedSBx)
	}
}

func TestRegisterOrConstantOperands(t *testing.T) {
	for _, index := range []int{0, 1, maxRegisterConstant} {
		register := registerOrConstant(index, false)
		if isConstantOperand(register) {
			t.Errorf("register %d was marked constant", index)
		}
		if got := constantIndex(register); got != index {
			t.Errorf("register index = %d; want %d", got, index)
		}

		constant := registerOrConstant(index, true)
		if !isConstantOperand(constant) {
			t.Errorf("constant %d was marked register", index)
		}
		if got := constantIndex(constant); got != index {
			t.Errorf("constant index = %d; want %d", got, index)
		}
	}
	if got := registerOrConstant(maxRegisterConstant, true); got != maxOperandB {
		t.Fatalf("maximum constant operand = %#x; want %#x", got, maxOperandB)
	}
}

func TestFloatingByteEncoding(t *testing.T) {
	cases := []struct {
		value   int
		encoded int
		decoded int
	}{
		{0, 0, 0},
		{7, 7, 7},
		{8, 8, 8},
		{15, 15, 15},
		{16, 16, 16},
		{17, 17, 18},
		{31, 24, 32},
		{32, 24, 32},
		{33, 25, 36},
		{1 << 20, 144, 1 << 20},
	}
	for _, test := range cases {
		if got := intToFloatingByte(test.value); got != test.encoded {
			t.Errorf("intToFloatingByte(%d) = %d; want %d", test.value, got, test.encoded)
		}
		if got := floatingByteToInt(test.encoded); got != test.decoded {
			t.Errorf("floatingByteToInt(%d) = %d; want %d", test.encoded, got, test.decoded)
		}
	}

	previous := -1
	for value := 0; value <= 1<<20; value++ {
		encoded := intToFloatingByte(value)
		decoded := floatingByteToInt(encoded)
		if encoded < previous {
			t.Fatalf("encoding decreased at %d: %d after %d", value, encoded, previous)
		}
		if decoded < value {
			t.Fatalf("encoding %d rounded down: decoded %d", value, decoded)
		}
		if roundTrip := intToFloatingByte(decoded); roundTrip != encoded {
			t.Fatalf(
				"floating byte %d is not stable: decoded %d, re-encoded %d",
				encoded,
				decoded,
				roundTrip,
			)
		}
		previous = encoded
	}
}

func TestInvalidInstructionInputsPanic(t *testing.T) {
	tests := map[string]func(){
		"ABC opcode":  func() { makeABC(opCount, 0, 0, 0) },
		"ABC A low":   func() { makeABC(opMove, -1, 0, 0) },
		"ABC A high":  func() { makeABC(opMove, maxOperandA+1, 0, 0) },
		"ABC B low":   func() { makeABC(opMove, 0, -1, 0) },
		"ABC B high":  func() { makeABC(opMove, 0, maxOperandB+1, 0) },
		"ABC C low":   func() { makeABC(opMove, 0, 0, -1) },
		"ABC C high":  func() { makeABC(opMove, 0, 0, maxOperandC+1) },
		"ABx opcode":  func() { makeABx(opcode(0xff), 0, 0) },
		"ABx A low":   func() { makeABx(opLoadK, -1, 0) },
		"ABx A high":  func() { makeABx(opLoadK, maxOperandA+1, 0) },
		"ABx Bx low":  func() { makeABx(opLoadK, 0, -1) },
		"ABx Bx high": func() { makeABx(opLoadK, 0, maxOperandBx+1) },
		"AsBx low":    func() { makeAsBx(opJump, 0, -maxOperandsBx-1) },
		"AsBx high":   func() { makeAsBx(opJump, 0, maxOperandsBx+1) },
		"patch A low": func() { makeABC(opMove, 0, 0, 0).withA(-1) },
		"patch A high": func() {
			makeABC(opMove, 0, 0, 0).withA(maxOperandA + 1)
		},
		"patch Bx low": func() { makeABx(opLoadK, 0, 0).withBx(-1) },
		"patch Bx high": func() {
			makeABx(opLoadK, 0, 0).withBx(maxOperandBx + 1)
		},
		"patch sBx low": func() {
			makeAsBx(opJump, 0, 0).withSBx(-maxOperandsBx - 1)
		},
		"patch sBx high": func() {
			makeAsBx(opJump, 0, 0).withSBx(maxOperandsBx + 1)
		},
		"RK low":             func() { registerOrConstant(-1, true) },
		"RK high":            func() { registerOrConstant(maxRegisterConstant+1, false) },
		"floating low":       func() { intToFloatingByte(-1) },
		"floating byte low":  func() { floatingByteToInt(-1) },
		"floating byte high": func() { floatingByteToInt(0x100) },
	}
	if strconv.IntSize == 64 {
		tests["floating high"] = func() { intToFloatingByte(int(^uint(0) >> 1)) }
	}

	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("did not panic")
				}
			}()
			run()
		})
	}
}
