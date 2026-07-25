package lua

import (
	"math"
	"strings"
	"testing"
	"unsafe"
)

func TestCompilerInternsTokenTextWithoutRetainingSource(t *testing.T) {
	source := strings.Repeat("x", 128) +
		` identifier "decoded\ntext" identifier "plain"`
	lex := newLexer("@tokens.lua", source)
	unit := newCompileUnit("@tokens.lua")

	name, err := lex.next()
	if err != nil {
		t.Fatal(err)
	}
	if name.kind != tokenName || name.ownedText {
		t.Fatal("identifier did not borrow its source text")
	}
	internedName := unit.internToken(name)
	if internedName.text != strings.Repeat("x", 128) {
		t.Fatalf("first token = %q", internedName.text)
	}
	if unsafe.StringData(internedName.text) == unsafe.StringData(name.text) {
		t.Fatal("interned source token still aliases the source buffer")
	}

	identifier, err := lex.next()
	if err != nil {
		t.Fatal(err)
	}
	if identifier.kind != tokenName || identifier.text != "identifier" {
		t.Fatalf("identifier token = (%s, %q)", identifier.kind, identifier.text)
	}
	firstIdentifier := unit.internToken(identifier)
	if unsafe.StringData(firstIdentifier.text) ==
		unsafe.StringData(identifier.text) {
		t.Fatal("interned identifier still aliases the source buffer")
	}

	decoded, err := lex.next()
	if err != nil {
		t.Fatal(err)
	}
	if decoded.kind != tokenString || !decoded.ownedText {
		t.Fatal("decoded string was not marked as owned text")
	}
	internedDecoded := unit.internToken(decoded)
	if internedDecoded.text != "decoded\ntext" {
		t.Fatalf("decoded text = %q", internedDecoded.text)
	}
	if unsafe.StringData(internedDecoded.text) !=
		unsafe.StringData(decoded.text) {
		t.Fatal("compiler copied already-owned decoded text")
	}

	repeated, err := lex.next()
	if err != nil {
		t.Fatal(err)
	}
	if again := unit.internToken(repeated); again != firstIdentifier {
		t.Fatal("equal token text did not share compilation-local identity")
	}
	plain, err := lex.next()
	if err != nil {
		t.Fatal(err)
	}
	if plain.kind != tokenString || plain.ownedText {
		t.Fatal("unescaped string did not borrow its source text")
	}
	internedPlain := unit.internToken(plain)
	if unsafe.StringData(internedPlain.text) ==
		unsafe.StringData(plain.text) {
		t.Fatal("interned string constant still aliases the source buffer")
	}

	if len(unit.strings) != 5 {
		t.Fatalf("interned string count = %d, want 5", len(unit.strings))
	}
}

func TestCompilerDeduplicatesCompactConstants(t *testing.T) {
	unit := newCompileUnit("@constants.lua")
	function, syntaxError := unit.newFunction(nil, 1, 0, 0)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	constants := []slot{
		nilSlot,
		nilSlot,
		slotFromValue(Bool(true)),
		slotFromValue(Bool(false)),
		slotFromValue(Number(42.5)),
		slotFromValue(Number(42.5)),
		slotFromValue(Number(0)),
		slotFromValue(Number(math.Copysign(0, -1))),
		prototypeStringSlot(newLuaString("field")),
		prototypeStringSlot(newLuaString("field")),
	}
	wantIndexes := []int{0, 0, 1, 2, 3, 3, 4, 5, 6, 6}
	for index, value := range constants {
		got, err := function.constant(value, 2)
		if err != nil {
			t.Fatalf("constant %d: %v", index, err)
		}
		if got != wantIndexes[index] {
			t.Fatalf(
				"constant %d index = %d, want %d",
				index,
				got,
				wantIndexes[index],
			)
		}
	}
	if len(function.builder.constants) != 7 {
		t.Fatalf(
			"constant pool length = %d, want 7",
			len(function.builder.constants),
		)
	}
	if function.builder.constants[4].bits != math.Float64bits(0) {
		t.Fatal("constant pool changed positive zero")
	}
	if function.builder.constants[5].bits !=
		math.Float64bits(math.Copysign(0, -1)) {
		t.Fatal("constant pool changed negative zero")
	}
	firstString := function.builder.constants[6]
	if firstString.ref != unsafe.Pointer(unit.strings["field"]) {
		t.Fatal("constant pool did not retain the canonical compiler string")
	}

	if _, err := function.constant(
		slot{ref: trueMarkerPointer, bits: uint64(TableKind)},
		3,
	); err == nil || err.Category() != SyntaxError {
		t.Fatal("compiler accepted a non-scalar constant")
	}
}

func TestCompilerEmitsInstructionsLinesAndRegisterHighWater(t *testing.T) {
	unit := newCompileUnit("@code.lua")
	function, syntaxError := unit.newFunction(nil, 3, 2, 0)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	outer := function.registerTop
	base, syntaxError := function.reserveRegisters(3, 4)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if base != 2 {
		t.Fatalf("temporary base = %d, want 2", base)
	}
	inner, syntaxError := function.reserveRegisters(2, 4)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if inner != 5 {
		t.Fatalf("inner base = %d, want 5", inner)
	}
	function.releaseRegisters(inner)
	if _, syntaxError = function.reserveRegisters(1, 5); syntaxError != nil {
		t.Fatal(syntaxError)
	}
	function.releaseRegisters(outer)

	constant, syntaxError := function.constant(
		slotFromValue(Number(7)),
		5,
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	function.emitABx(opLoadK, 0, constant, 5)
	function.emitABC(opReturn, 0, 2, 0, 6)

	prototype, syntaxError := function.finish(6)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if prototype.RegisterCount() != 7 {
		t.Fatalf("register count = %d, want 7", prototype.RegisterCount())
	}
	if prototype.LineAt(0) != 5 || prototype.LineAt(1) != 6 {
		t.Fatalf(
			"instruction lines = (%d, %d), want (5, 6)",
			prototype.LineAt(0),
			prototype.LineAt(1),
		)
	}
	if prototype.code[0].opcode() != opLoadK ||
		prototype.code[0].bx() != constant ||
		prototype.code[1].opcode() != opReturn {
		t.Fatal("emitted bytecode differs from requested instructions")
	}
	if cap(prototype.code) != len(prototype.code) ||
		cap(prototype.constants) != len(prototype.constants) {
		t.Fatal("compiler retained spare emission capacity after sealing")
	}
}

func TestCompilerUsesCanonicalMinimumAndRejectsRegisterOverflow(t *testing.T) {
	unit := newCompileUnit("@registers.lua")
	function, syntaxError := unit.newFunction(nil, 1, 0, 0)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if _, syntaxError = function.reserveRegisters(
		maxLuaRegisters+1,
		1,
	); syntaxError == nil || syntaxError.Category() != SyntaxError {
		t.Fatal("compiler accepted a register frame beyond Lua's limit")
	}
	if function.registerTop != 0 {
		t.Fatal("failed reservation changed the live register top")
	}
	function.emitABC(opReturn, 0, 1, 0, 1)
	prototype, syntaxError := function.finish(1)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if prototype.RegisterCount() != 2 {
		t.Fatalf(
			"empty function register count = %d, want 2",
			prototype.RegisterCount(),
		)
	}
}

func TestCompilerPatchesAllocationFreeJumpLists(t *testing.T) {
	unit := newCompileUnit("@jumps.lua")
	function, syntaxError := unit.newFunction(nil, 1, 0, 0)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	first := function.emitJump(2)
	function.emitABC(opLoadNil, 0, 0, 0, 3)
	second := function.emitJump(4)
	list, syntaxError := function.joinJumps(first, second)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if syntaxError = function.patchJumpsToHere(list); syntaxError != nil {
		t.Fatal(syntaxError)
	}
	target := function.emitABC(opReturn, 0, 1, 0, 5)

	prototype, syntaxError := function.finish(5)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	for _, pc := range []int{first.pc(), second.pc()} {
		if got := pc + 1 + prototype.code[pc].sbx(); got != target {
			t.Fatalf("jump at %d targets %d, want %d", pc, got, target)
		}
	}
	if function.unresolvedJumps != 0 {
		t.Fatalf(
			"unresolved jump count = %d, want 0",
			function.unresolvedJumps,
		)
	}
}

func TestCompilerRejectsUnresolvedControlFlow(t *testing.T) {
	unit := newCompileUnit("@jumps.lua")
	function, syntaxError := unit.newFunction(nil, 1, 0, 0)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	function.emitJump(1)
	function.emitABC(opReturn, 0, 1, 0, 2)
	if _, syntaxError = function.finish(2); syntaxError == nil ||
		syntaxError.Category() != SyntaxError ||
		!strings.Contains(syntaxError.Error(), "unresolved control flow") {
		t.Fatalf("finish error = %v", syntaxError)
	}
}

func TestCompilerRejectsUnboundExpressionResult(t *testing.T) {
	unit := newCompileUnit("@result.lua")
	function, syntaxError := unit.newFunction(nil, 1, 0, 0)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	function.emitDeferredABC(opAdd, 0, 0, 1)
	function.emitABC(opReturn, 0, 1, 0, 1)
	if _, syntaxError = function.finish(1); syntaxError == nil ||
		syntaxError.Category() != SyntaxError ||
		!strings.Contains(syntaxError.Error(), "unresolved expression") {
		t.Fatalf("finish error = %v", syntaxError)
	}
}
