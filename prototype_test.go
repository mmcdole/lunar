package lua

import (
	"testing"
	"unsafe"
)

func TestPrototypeSealOwnsCompactMetadata(t *testing.T) {
	source := newInternedText("module.lua")
	localName := newInternedText("result")
	upvalueName := newInternedText("outer")
	constantString := newInternedText("shared")

	childBuilder := testPrototypeBuilder(
		makeABC(opReturn, 0, 1, 0),
	)
	childBuilder.sourceName = source
	childBuilder.upvalues = 1
	child, syntaxError := childBuilder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	code := []instruction{
		makeABx(opLoadK, 0, 0),
		makeABx(opClosure, 1, 0),
		makeABC(opMove, 0, 0, 0),
		makeABC(opReturn, 0, 1, 0),
	}
	constants := []slot{
		nilSlot,
		slotFromValue(Bool(true)),
		slotFromValue(Number(42.5)),
		prototypeStringSlot(constantString),
	}
	children := []*Prototype{child}
	lines := []int{3, 4, 4, 5}
	locals := []prototypeLocalBuilder{
		{name: localName, startPC: 0, endPC: 4},
	}
	upvalueNames := []*internedText{upvalueName}
	builder := &prototypeBuilder{
		sourceName:  source,
		lineDefined: 2,
		lastLine:    5,
		parameters:  0,
		registers:   2,
		upvalues:    1,
		varargFlags: varargHasArg | varargIsVararg | varargNeedsArg,
		code:        code,
		constants:   constants,
		children:    children,
		debug: &prototypeDebugBuilder{
			lines:    lines,
			locals:   locals,
			upvalues: upvalueNames,
		},
	}

	prototype, syntaxError := builder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if prototype.SourceName() != "module.lua" {
		t.Fatalf("SourceName = %q", prototype.SourceName())
	}
	if first, last := prototype.LineRange(); first != 2 || last != 5 {
		t.Fatalf("LineRange = (%d, %d)", first, last)
	}
	if parameterCount(prototype) != 0 ||
		registerCount(prototype) != 2 ||
		upvalueCount(prototype) != 1 ||
		!isVararg(prototype) ||
		childCount(prototype) != 1 {
		t.Fatal("sealed metadata differs from its builder")
	}
	if prototype.lineAt(2) != 4 || prototype.lineAt(-1) != 0 ||
		prototype.lineAt(len(code)) != 0 {
		t.Fatal("LineAt returned invalid debug information")
	}
	if cap(prototype.code) != len(prototype.code) ||
		cap(prototype.constants) != len(prototype.constants) ||
		cap(prototype.children) != len(prototype.children) ||
		cap(prototype.debug.lines) != len(prototype.debug.lines) ||
		cap(prototype.debug.locals) != len(prototype.debug.locals) ||
		cap(prototype.debug.upvalues) != len(prototype.debug.upvalues) {
		t.Fatal("sealed prototype retained spare slice capacity")
	}
	if unsafe.Sizeof(slot{}) != 16 {
		t.Fatalf("prototype constant slot size = %d; want 16", unsafe.Sizeof(slot{}))
	}
	if unsafe.Sizeof(uintptr(0)) == 8 {
		if size := unsafe.Sizeof(Prototype{}); size != 104 {
			t.Fatalf("Prototype size = %d; want 104", size)
		}
		if size := unsafe.Sizeof(localInfo{}); size != 16 {
			t.Fatalf("localInfo size = %d; want 16", size)
		}
	}
	if prototype.sourceName != source ||
		prototype.debug.locals[0].name != localName ||
		prototype.debug.upvalues[0] != upvalueName {
		t.Fatal("prototype did not retain compilation-shared strings")
	}
	constant := prototype.constants[3]
	if stringSlotText(constant) != constantString.text ||
		constant.ref != unsafe.Pointer(unsafe.StringData(constantString.text)) {
		t.Fatal("prototype constant did not cross into flat string storage")
	}

	code[0] = makeABC(opReturn, 0, 1, 0)
	constants[0] = slotFromValue(Number(99))
	children[0] = nil
	lines[0] = 99
	locals[0].name = newInternedText("changed")
	upvalueNames[0] = newInternedText("changed")
	if prototype.code[0].opcode() != opLoadK ||
		!prototype.constants[0].owningValue().IsNil() ||
		prototype.children[0] != child ||
		prototype.debug.lines[0] != 3 ||
		prototype.debug.locals[0].name != localName ||
		prototype.debug.upvalues[0] != upvalueName {
		t.Fatal("sealed prototype aliases mutable builder slices")
	}

	if _, syntaxError = builder.seal(); syntaxError == nil ||
		syntaxError.Category() != SyntaxError {
		t.Fatal("a consumed prototype builder was accepted")
	}
}

func TestPrototypeVarargAndDebugValidation(t *testing.T) {
	for _, flags := range []int{
		0,
		varargIsVararg,
		varargHasArg | varargIsVararg,
		varargHasArg | varargIsVararg | varargNeedsArg,
	} {
		builder := testPrototypeBuilder(
			makeABC(opReturn, 0, 1, 0),
		)
		builder.varargFlags = flags
		if _, syntaxError := builder.seal(); syntaxError != nil {
			t.Errorf("vararg flags %#x: %v", flags, syntaxError)
		}
	}
	partialDebug := testPrototypeBuilder(
		makeABC(opReturn, 0, 1, 0),
	)
	partialDebug.upvalues = 2
	partialDebug.debug = &prototypeDebugBuilder{
		upvalues: []*internedText{newInternedText("named")},
	}
	if _, syntaxError := partialDebug.seal(); syntaxError != nil {
		t.Fatalf("partial debug upvalue names: %v", syntaxError)
	}

	cases := []struct {
		name   string
		change func(*prototypeBuilder)
	}{
		{
			name: "unknown flag",
			change: func(builder *prototypeBuilder) {
				builder.varargFlags = 8
			},
		},
		{
			name: "arg on fixed arity",
			change: func(builder *prototypeBuilder) {
				builder.varargFlags = varargHasArg
			},
		},
		{
			name: "needs undeclared arg",
			change: func(builder *prototypeBuilder) {
				builder.varargFlags = varargIsVararg | varargNeedsArg
			},
		},
		{
			name: "arg register overflow",
			change: func(builder *prototypeBuilder) {
				builder.parameters = 2
				builder.registers = 2
				builder.varargFlags = varargHasArg | varargIsVararg
			},
		},
		{
			name: "line count",
			change: func(builder *prototypeBuilder) {
				builder.debug = &prototypeDebugBuilder{lines: []int{1, 2}}
			},
		},
		{
			name: "upvalue names",
			change: func(builder *prototypeBuilder) {
				builder.upvalues = 1
				builder.debug = &prototypeDebugBuilder{
					upvalues: []*internedText{
						newInternedText("one"),
						newInternedText("two"),
					},
				}
			},
		},
		{
			name: "local lifetime",
			change: func(builder *prototypeBuilder) {
				builder.debug = &prototypeDebugBuilder{
					locals: []prototypeLocalBuilder{
						{startPC: 1, endPC: 0},
					},
				}
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			builder := testPrototypeBuilder(
				makeABC(opReturn, 0, 1, 0),
			)
			test.change(builder)
			assertPrototypeSyntaxError(t, builder)
		})
	}
}

func TestPrototypeInstructionVerification(t *testing.T) {
	childBuilder := testPrototypeBuilder(
		makeABC(opReturn, 0, 1, 0),
	)
	childBuilder.upvalues = 2
	child, syntaxError := childBuilder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	valid := []*prototypeBuilder{
		{
			sourceName: newInternedText("one-register-frame"),
			registers:  1,
			code: []instruction{
				makeABC(opReturn, 0, 1, 0),
			},
		},
		{
			sourceName: newInternedText("setlist"),
			registers:  3,
			code: []instruction{
				makeABC(opNewTable, 0, 0, 0),
				makeABC(opSetList, 0, 1, 0),
				instruction(1),
				makeABC(opReturn, 0, 1, 0),
			},
		},
		{
			sourceName: newInternedText("closure"),
			registers:  3,
			upvalues:   1,
			children:   []*Prototype{child},
			code: []instruction{
				makeABx(opClosure, 0, 0),
				makeABC(opMove, 0, 1, 0),
				makeABC(opGetUpvalue, 0, 0, 0),
				makeABC(opReturn, 0, 1, 0),
			},
		},
		{
			sourceName: newInternedText("test"),
			registers:  2,
			code: []instruction{
				makeABC(opEqual, 0, 0, 1),
				makeAsBx(opJump, 0, 0),
				makeABC(opReturn, 0, 1, 0),
			},
		},
		{
			sourceName:  newInternedText("open-results"),
			registers:   2,
			varargFlags: varargIsVararg,
			code: []instruction{
				makeABC(opVararg, 0, 0, 0),
				makeABC(opReturn, 0, 0, 0),
			},
		},
		{
			sourceName: newInternedText("tail-call"),
			registers:  2,
			code: []instruction{
				makeABC(opTailCall, 0, 1, 0),
				makeABC(opReturn, 0, 0, 0),
			},
		},
		{
			sourceName: newInternedText("standalone-open-return"),
			registers:  2,
			code: []instruction{
				makeABC(opReturn, 0, 0, 0),
			},
		},
		{
			sourceName: newInternedText("jump-to-open-return"),
			registers:  2,
			code: []instruction{
				makeAsBx(opJump, 0, 0),
				makeABC(opReturn, 0, 0, 0),
			},
		},
		{
			sourceName: newInternedText("largest-setlist-block"),
			registers:  2,
			code: []instruction{
				makeABC(opNewTable, 0, 0, 0),
				makeABC(opSetList, 0, 1, 0),
				instruction(maxSetListIndex/fieldsPerFlush + 1),
				makeABC(opReturn, 0, 1, 0),
			},
		},
		{
			sourceName: newInternedText("string-fields"),
			registers:  2,
			constants: []slot{
				prototypeStringSlot(newInternedText("field")),
			},
			code: []instruction{
				makeABC(
					opGetField,
					0,
					1,
					registerOrConstant(0, true),
				),
				makeABC(
					opSetField,
					1,
					registerOrConstant(0, true),
					0,
				),
				makeABC(
					opSelfField,
					0,
					1,
					registerOrConstant(0, true),
				),
				makeABC(opReturn, 0, 1, 0),
			},
		},
	}
	for _, builder := range valid {
		if _, syntaxError := builder.seal(); syntaxError != nil {
			t.Errorf("%s: %v", prototypeStringText(builder.sourceName), syntaxError)
		}
	}

	invalid := []struct {
		name    string
		builder func() *prototypeBuilder
	}{
		{
			name: "empty code",
			builder: func() *prototypeBuilder {
				return testPrototypeBuilder()
			},
		},
		{
			name: "missing return",
			builder: func() *prototypeBuilder {
				return testPrototypeBuilder(makeABC(opMove, 0, 0, 0))
			},
		},
		{
			name: "unknown opcode",
			builder: func() *prototypeBuilder {
				return testPrototypeBuilder(
					instruction(0x3f),
					makeABC(opReturn, 0, 1, 0),
				)
			},
		},
		{
			name: "executor outcome opcode",
			builder: func() *prototypeBuilder {
				return testPrototypeBuilder(
					instruction(opGetTableMiss),
					makeABC(opReturn, 0, 1, 0),
				)
			},
		},
		{
			name: "malformed constant",
			builder: func() *prototypeBuilder {
				builder := testPrototypeBuilder(
					makeABx(opLoadK, 0, 0),
					makeABC(opReturn, 0, 1, 0),
				)
				builder.constants = []slot{
					{ref: nilMarkerPointer, bits: uint64(NumberKind)},
				}
				return builder
			},
		},
		{
			name: "global name is not string",
			builder: func() *prototypeBuilder {
				builder := testPrototypeBuilder(
					makeABx(opGetGlobal, 0, 0),
					makeABC(opReturn, 0, 1, 0),
				)
				builder.constants = []slot{slotFromValue(Number(1))}
				return builder
			},
		},
		{
			name: "field key is a register",
			builder: func() *prototypeBuilder {
				return testPrototypeBuilder(
					makeABC(opGetField, 0, 1, 0),
					makeABC(opReturn, 0, 1, 0),
				)
			},
		},
		{
			name: "field key is not a string",
			builder: func() *prototypeBuilder {
				builder := testPrototypeBuilder(
					makeABC(
						opSetField,
						0,
						registerOrConstant(0, true),
						1,
					),
					makeABC(opReturn, 0, 1, 0),
				)
				builder.constants = []slot{numberSlot(1)}
				return builder
			},
		},
		{
			name: "method key is out of range",
			builder: func() *prototypeBuilder {
				return testPrototypeBuilder(
					makeABC(
						opSelfField,
						0,
						1,
						registerOrConstant(0, true),
					),
					makeABC(opReturn, 0, 1, 0),
				)
			},
		},
		{
			name: "constant out of range",
			builder: func() *prototypeBuilder {
				return testPrototypeBuilder(
					makeABC(
						opAdd,
						0,
						registerOrConstant(0, true),
						0,
					),
					makeABC(opReturn, 0, 1, 0),
				)
			},
		},
		{
			name: "register out of range",
			builder: func() *prototypeBuilder {
				return testPrototypeBuilder(
					makeABC(opMove, 0, 2, 0),
					makeABC(opReturn, 0, 1, 0),
				)
			},
		},
		{
			name: "test without jump",
			builder: func() *prototypeBuilder {
				return testPrototypeBuilder(
					makeABC(opTest, 0, 0, 0),
					makeABC(opReturn, 0, 1, 0),
				)
			},
		},
		{
			name: "jump outside code",
			builder: func() *prototypeBuilder {
				return testPrototypeBuilder(
					makeAsBx(opJump, 0, 20),
					makeABC(opReturn, 0, 1, 0),
				)
			},
		},
		{
			name: "jump into metadata",
			builder: func() *prototypeBuilder {
				builder := testPrototypeBuilder(
					makeAsBx(opJump, 0, 1),
					makeABC(opSetList, 0, 1, 0),
					instruction(1),
					makeABC(opReturn, 0, 1, 0),
				)
				builder.registers = 2
				return builder
			},
		},
		{
			name: "jump into closure binding",
			builder: func() *prototypeBuilder {
				builder := testPrototypeBuilder(
					makeAsBx(opJump, 0, 1),
					makeABx(opClosure, 0, 0),
					makeABC(opMove, 1, 0, 0),
					makeABC(opGetUpvalue, 0, 0, 0),
					makeABC(opReturn, 0, 1, 0),
				)
				builder.upvalues = 1
				builder.children = []*Prototype{child}
				return builder
			},
		},
		{
			name: "setlist missing extra word",
			builder: func() *prototypeBuilder {
				return testPrototypeBuilder(
					makeABC(opSetList, 0, 1, 0),
				)
			},
		},
		{
			name: "setlist zero extra word",
			builder: func() *prototypeBuilder {
				return testPrototypeBuilder(
					makeABC(opSetList, 0, 1, 0),
					0,
					makeABC(opReturn, 0, 1, 0),
				)
			},
		},
		{
			name: "closure missing binding",
			builder: func() *prototypeBuilder {
				builder := testPrototypeBuilder(
					makeABx(opClosure, 0, 0),
					makeABC(opReturn, 0, 1, 0),
				)
				builder.children = []*Prototype{child}
				return builder
			},
		},
		{
			name: "closure invalid binding",
			builder: func() *prototypeBuilder {
				builder := testPrototypeBuilder(
					makeABx(opClosure, 0, 0),
					makeABC(opLoadNil, 0, 0, 0),
					makeABC(opGetUpvalue, 0, 0, 0),
					makeABC(opReturn, 0, 1, 0),
				)
				builder.upvalues = 1
				builder.children = []*Prototype{child}
				return builder
			},
		},
		{
			name: "fixed arity vararg",
			builder: func() *prototypeBuilder {
				return testPrototypeBuilder(
					makeABC(opVararg, 0, 1, 0),
					makeABC(opReturn, 0, 1, 0),
				)
			},
		},
		{
			name: "vararg with required legacy arg",
			builder: func() *prototypeBuilder {
				builder := testPrototypeBuilder(
					makeABC(opVararg, 1, 1, 0),
					makeABC(opReturn, 0, 1, 0),
				)
				builder.varargFlags = varargHasArg |
					varargIsVararg |
					varargNeedsArg
				return builder
			},
		},
		{
			name: "single register concat",
			builder: func() *prototypeBuilder {
				return testPrototypeBuilder(
					makeABC(opConcat, 0, 1, 1),
					makeABC(opReturn, 0, 1, 0),
				)
			},
		},
		{
			name: "open result without consumer",
			builder: func() *prototypeBuilder {
				return testPrototypeBuilder(
					makeABC(opCall, 0, 1, 0),
					makeABC(opReturn, 0, 1, 0),
				)
			},
		},
		{
			name: "open result before return window",
			builder: func() *prototypeBuilder {
				return testPrototypeBuilder(
					makeABC(opCall, 0, 1, 0),
					makeABC(opReturn, 1, 0, 0),
				)
			},
		},
		{
			name: "open result before call arguments",
			builder: func() *prototypeBuilder {
				builder := testPrototypeBuilder(
					makeABC(opVararg, 0, 0, 0),
					makeABC(opCall, 1, 0, 2),
					makeABC(opReturn, 1, 2, 0),
				)
				builder.registers = 3
				builder.varargFlags = varargIsVararg
				return builder
			},
		},
		{
			name: "open result before setlist values",
			builder: func() *prototypeBuilder {
				builder := testPrototypeBuilder(
					makeABC(opVararg, 0, 0, 0),
					makeABC(opSetList, 2, 0, 1),
					makeABC(opReturn, 0, 1, 0),
				)
				builder.registers = 3
				builder.varargFlags = varargIsVararg
				return builder
			},
		},
		{
			name: "nil child",
			builder: func() *prototypeBuilder {
				builder := testPrototypeBuilder(
					makeABC(opReturn, 0, 1, 0),
				)
				builder.children = []*Prototype{nil}
				return builder
			},
		},
		{
			name: "unsealed child",
			builder: func() *prototypeBuilder {
				builder := testPrototypeBuilder(
					makeABC(opReturn, 0, 1, 0),
				)
				builder.children = []*Prototype{{}}
				return builder
			},
		},
		{
			name: "setlist index overflow",
			builder: func() *prototypeBuilder {
				return testPrototypeBuilder(
					makeABC(opSetList, 0, 1, 0),
					instruction(maxSetListIndex/fieldsPerFlush+2),
					makeABC(opReturn, 0, 1, 0),
				)
			},
		},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			assertPrototypeSyntaxError(t, test.builder())
		})
	}
}

func testPrototypeBuilder(code ...instruction) *prototypeBuilder {
	return &prototypeBuilder{
		sourceName: newInternedText("test.lua"),
		registers:  2,
		code:       code,
	}
}

func assertPrototypeSyntaxError(t *testing.T, builder *prototypeBuilder) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("malformed prototype panicked: %v", recovered)
		}
	}()
	prototype, syntaxError := builder.seal()
	if prototype != nil {
		t.Fatal("malformed prototype was published")
	}
	if syntaxError == nil {
		t.Fatal("malformed prototype was accepted")
	}
	if syntaxError.Category() != SyntaxError {
		t.Fatalf("error category = %v; want SyntaxError", syntaxError.Category())
	}
}
