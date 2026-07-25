package lua

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"unsafe"
)

func TestCompileSourceDirectExpressionSlice(t *testing.T) {
	const source = "local x = input + 3 * 4\n" +
		"local y = x > 10 and x or 0\n" +
		"return y"

	prototype, syntaxError := compileSource("@sample.lua", source)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if !prototype.sealed ||
		prototype.SourceName() != "@sample.lua" ||
		prototype.ParameterCount() != 0 ||
		!prototype.IsVararg() {
		t.Fatal("main chunk metadata is incomplete")
	}
	if prototype.RegisterCount() < 2 ||
		prototype.debug == nil ||
		len(prototype.debug.lines) != len(prototype.code) ||
		len(prototype.debug.locals) != 2 {
		t.Fatal("compiler did not publish compact register/debug metadata")
	}

	seen := make(map[opcode]bool)
	for _, code := range prototype.code {
		seen[code.opcode()] = true
	}
	for _, operation := range []opcode{
		opGetGlobal,
		opAdd,
		opLessThan,
		opTest,
		opJump,
		opReturn,
	} {
		if !seen[operation] {
			t.Fatalf("sample bytecode does not contain %s", operation)
		}
	}
	if seen[opMul] {
		t.Fatal("pure numeric multiplication was not folded")
	}
	for index, code := range prototype.code {
		if code.opcode() == opTest {
			if index+1 >= len(prototype.code) ||
				prototype.code[index+1].opcode() != opJump {
				t.Fatal("logical test is not followed by its branch")
			}
		}
	}
}

func TestCompileSourceOwnsRetainedTokenText(t *testing.T) {
	source := "local retained_name = \"retained_value\"\nreturn retained_name"
	prototype, syntaxError := compileSource("@ownership.lua", source)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	name := prototype.debug.locals[0].name.text
	nameOffset := strings.Index(source, name)
	if nameOffset < 0 {
		t.Fatal("test source does not contain its local name")
	}
	if unsafe.StringData(name) ==
		unsafe.StringData(source[nameOffset:nameOffset+len(name)]) {
		t.Fatal("debug local name retains the source buffer")
	}

	var retained *luaString
	for _, constant := range prototype.constants {
		if constant.kind() == StringKind {
			text := (*luaString)(constant.ref)
			if text.text == "retained_value" {
				retained = text
				break
			}
		}
	}
	if retained == nil {
		t.Fatal("compiled string constant is missing")
	}
	valueOffset := strings.Index(source, retained.text)
	if unsafe.StringData(retained.text) ==
		unsafe.StringData(
			source[valueOffset:valueOffset+len(retained.text)],
		) {
		t.Fatal("string constant retains the source buffer")
	}
}

func TestCompileSourcePrecedenceAndSignedZero(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@precedence.lua",
		"local a = -2^2\n"+
			"local b = 2^-2\n"+
			"local c = 1+2*3\n"+
			"local p = 0\n"+
			"local n = -0\n"+
			"return a,b,c,p,n,\"a\"..\"b\"..\"c\"",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	numbers := make(map[uint64]bool)
	concatCount := 0
	for _, constant := range prototype.constants {
		if constant.kind() == NumberKind {
			numbers[constant.bits] = true
		}
	}
	for _, code := range prototype.code {
		if code.opcode() == opConcat {
			concatCount++
			if code.b() >= code.c() {
				t.Fatal("compiler emitted a reversed CONCAT range")
			}
		}
	}
	for _, number := range []float64{-4, 0.25, 7} {
		if !numbers[math.Float64bits(number)] {
			t.Fatalf("folded constant %v is missing", number)
		}
	}
	if !numbers[math.Float64bits(0)] ||
		!numbers[math.Float64bits(math.Copysign(0, -1))] {
		t.Fatal("compiler merged positive and negative zero")
	}
	if concatCount != 2 {
		t.Fatalf("CONCAT count = %d, want 2", concatCount)
	}
}

func TestCompileSourceLocalScopeAndActivation(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@scope.lua",
		"local x = x\n"+
			"do\n"+
			"  local x = x + 1\n"+
			"  x = x + 1\n"+
			"end\n"+
			"x = x + 1\n"+
			"return x",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if len(prototype.debug.locals) != 2 {
		t.Fatalf(
			"debug local count = %d, want 2",
			len(prototype.debug.locals),
		)
	}
	outer := prototype.debug.locals[0]
	inner := prototype.debug.locals[1]
	if outer.name != inner.name ||
		inner.startPC <= outer.startPC ||
		inner.endPC >= outer.endPC {
		t.Fatal("nested local lifetimes are not properly scoped")
	}
	if len(prototype.code) == 0 ||
		prototype.code[0].opcode() != opGetGlobal {
		t.Fatal("local x = x did not resolve the RHS before activation")
	}
}

func TestCompileSourceVarargReturnAdjustment(t *testing.T) {
	open, syntaxError := compileSource("@vararg.lua", "return 1, ...")
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if len(open.code) < 2 {
		t.Fatal("open return emitted too little bytecode")
	}
	vararg := open.code[len(open.code)-2]
	ret := open.code[len(open.code)-1]
	if vararg.opcode() != opVararg ||
		vararg.b() != 0 ||
		ret.opcode() != opReturn ||
		ret.b() != 0 {
		t.Fatal("final vararg was not preserved as an open return")
	}

	fixed, syntaxError := compileSource("@vararg.lua", "return (...)")
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	vararg = fixed.code[len(fixed.code)-2]
	ret = fixed.code[len(fixed.code)-1]
	if vararg.opcode() != opVararg ||
		vararg.b() != 2 ||
		ret.opcode() != opReturn ||
		ret.b() != 2 {
		t.Fatal("parentheses did not force vararg to one result")
	}
}

func TestCompileSourceAdjustsLocalVarargResults(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@locals.lua",
		"local first, second, third = 1, ...\n"+
			"return first, second, third",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var vararg instruction
	for _, code := range prototype.code {
		if code.opcode() == opVararg {
			vararg = code
			break
		}
	}
	if vararg.opcode() != opVararg {
		t.Fatal("local initializer did not emit VARARG")
	}
	if vararg.a() != 1 || vararg.b() != 3 {
		t.Fatalf(
			"VARARG operands = A:%d B:%d, want A:1 B:3",
			vararg.a(),
			vararg.b(),
		)
	}
}

func TestCompileSourceUsesInitializerRegistersAsLocalSlots(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@locals.lua",
		"local value = input + 1\nreturn value",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	for _, code := range prototype.code {
		if code.opcode() == opMove {
			t.Fatal("local initializer emitted an avoidable MOVE")
		}
	}
	if prototype.code[0].opcode() != opGetGlobal ||
		prototype.code[0].a() != 0 ||
		prototype.code[1].opcode() != opAdd ||
		prototype.code[1].a() != 0 {
		t.Fatal("initializer was not compiled directly into its local slot")
	}
}

func TestCompileSourceSpillsLargeRKConstants(t *testing.T) {
	var source strings.Builder
	source.WriteString("local x = 0\n")
	for number := 1; number <= 256; number++ {
		source.WriteString("x = ")
		source.WriteString(strconv.Itoa(number))
		source.WriteByte('\n')
	}
	source.WriteString("return x + 256")

	prototype, syntaxError := compileSource("@constants.lua", source.String())
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if len(prototype.constants) != 257 {
		t.Fatalf(
			"constant count = %d, want 257",
			len(prototype.constants),
		)
	}
	add := prototype.code[len(prototype.code)-2]
	if add.opcode() != opAdd {
		t.Fatalf("penultimate opcode = %s, want ADD", add.opcode())
	}
	if isConstantOperand(add.c()) {
		t.Fatal("constant above the RK range was not spilled to a register")
	}
}

func TestCompileSourceKeepsConcatOperandsContiguousAfterRKSpill(t *testing.T) {
	var source strings.Builder
	source.WriteString("local sink = 0\n")
	for number := 1; number <= 256; number++ {
		source.WriteString("sink = ")
		source.WriteString(strconv.Itoa(number))
		source.WriteByte('\n')
	}
	source.WriteString(`return "prefix" .. (256 + (left + right))`)

	prototype, syntaxError := compileSource(
		"@concat.lua",
		source.String(),
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	for _, code := range prototype.code {
		if code.opcode() != opConcat {
			continue
		}
		if code.c() != code.b()+1 {
			t.Fatalf(
				"CONCAT range = [%d,%d], want adjacent operands",
				code.b(),
				code.c(),
			)
		}
		return
	}
	t.Fatal("large-constant expression did not emit CONCAT")
}

func TestCompileSourceErrors(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"missing local name", "local = 1", "expected <name>"},
		{"missing assignment", "value + 1", "expected '='"},
		{"return is last", "return 1; value = 2", "return must be the last"},
		{"missing end", "do local x = 1", "expected end"},
		{"unsupported statement", "if true then end", "unsupported statement"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prototype, syntaxError := compileSource("@bad.lua", test.source)
			if prototype != nil || syntaxError == nil {
				t.Fatal("malformed source was accepted")
			}
			if syntaxError.Category() != SyntaxError ||
				!syntaxError.Value().IsNil() ||
				!strings.Contains(syntaxError.Error(), "bad.lua:1:") ||
				!strings.Contains(syntaxError.Error(), test.want) {
				t.Fatalf("syntax error = %v", syntaxError)
			}
		})
	}
}

func TestCompileSourceEnforcesActiveLocalLimit(t *testing.T) {
	var source strings.Builder
	source.WriteString("local ")
	for index := 0; index <= maxActiveLocals; index++ {
		if index != 0 {
			source.WriteByte(',')
		}
		source.WriteByte('v')
		source.WriteString(strconv.Itoa(index))
	}

	if _, syntaxError := compileSource(
		"@locals.lua",
		source.String(),
	); syntaxError == nil ||
		!strings.Contains(syntaxError.Error(), "active locals") {
		t.Fatalf("local-limit error = %v", syntaxError)
	}
}

func FuzzCompileSourceDoesNotPanic(fuzz *testing.F) {
	for _, source := range []string{
		"",
		"return 1",
		"local x = input + 3 * 4; return x",
		"local a, b = ...; return a and b or false",
		"do local x = \"value\" .. suffix end",
		"return (((((((((nil)))))))))",
		"local =",
		"return 1 +",
	} {
		fuzz.Add(source)
	}
	fuzz.Fuzz(func(t *testing.T, source string) {
		_, _ = compileSource("@fuzz.lua", source)
	})
}
