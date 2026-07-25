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
		opTestSet,
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
	if seen[opLoadBool] {
		t.Fatal("short-circuit comparison was eagerly materialized")
	}
	for index, code := range prototype.code {
		if code.opcode() == opLessThan {
			if index+1 >= len(prototype.code) ||
				prototype.code[index+1].opcode() != opJump {
				t.Fatal("comparison is not followed by its branch")
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
	concatWidth := 0
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
			concatWidth = code.c() - code.b()
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
	if concatCount != 1 || concatWidth != 2 {
		t.Fatalf(
			"CONCAT shape = %d instruction(s), width %d; want 1 and 2",
			concatCount,
			concatWidth,
		)
	}
}

func TestCompileSourcePreservesExplicitConcatGrouping(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@concat.lua",
		"return (first .. second) .. third",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	concatCount := 0
	for _, code := range prototype.code {
		if code.opcode() == opConcat {
			concatCount++
		}
	}
	if concatCount != 2 {
		t.Fatalf("grouped CONCAT count = %d, want 2", concatCount)
	}
}

func TestCompileSourceDoesNotCoalesceConditionalConcatOperand(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@concat.lua",
		`return "a" .. (flag and ("b" .. "c"))`,
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	concatCount := 0
	for _, code := range prototype.code {
		if code.opcode() == opConcat {
			concatCount++
		}
	}
	if concatCount != 2 {
		t.Fatalf("conditional CONCAT count = %d, want 2", concatCount)
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

func TestCompileSourceBindsExpressionResultToAssignmentTarget(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@assignment.lua",
		"local value = 0\nvalue = value + increment\nreturn value",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	adds := 0
	for _, code := range prototype.code {
		switch code.opcode() {
		case opAdd:
			adds++
			if code.a() != 0 {
				t.Fatalf(
					"ADD destination = R%d, want local R0",
					code.a(),
				)
			}
		case opMove:
			t.Fatal("local arithmetic assignment emitted an avoidable MOVE")
		}
	}
	if adds != 1 {
		t.Fatalf("ADD count = %d, want 1", adds)
	}
}

func TestCompileSourceBindsGlobalExpressionBeforeStore(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@assignment.lua",
		"output = input + 1\nreturn output",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	addPC := -1
	storePC := -1
	for pc, code := range prototype.code {
		switch code.opcode() {
		case opAdd:
			addPC = pc
		case opSetGlobal:
			storePC = pc
		case opMove:
			t.Fatal("global arithmetic assignment emitted an avoidable MOVE")
		}
	}
	if addPC < 0 || storePC != addPC+1 {
		t.Fatalf(
			"ADD/SETGLOBAL positions = %d/%d, want adjacent",
			addPC,
			storePC,
		)
	}
	if prototype.code[addPC].a() != prototype.code[storePC].a() {
		t.Fatal("SETGLOBAL does not consume the arithmetic result directly")
	}
}

func TestCompileSourceKeepsShortCircuitComparisonAsBranches(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@condition.lua",
		"local left, right, yes, no = ...\n"+
			"return left < right and yes or no",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	comparisonPC := -1
	testSetCount := 0
	for pc, code := range prototype.code {
		switch code.opcode() {
		case opLessThan:
			comparisonPC = pc
		case opTestSet:
			testSetCount++
		case opLoadBool:
			t.Fatal("short-circuit comparison materialized a boolean")
		}
	}
	if comparisonPC < 0 ||
		comparisonPC+1 >= len(prototype.code) ||
		prototype.code[comparisonPC+1].opcode() != opJump {
		t.Fatal("comparison is not represented by a conditional branch")
	}
	if testSetCount == 0 {
		t.Fatal("logical expression does not preserve operand values")
	}
}

func TestCompileSourceMaterializesComparisonOnlyInValueContext(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@condition.lua",
		"local left, right = ...\n"+
			"local result = left < right\n"+
			"return result",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	comparisonPC := -1
	var booleans []instruction
	for pc, code := range prototype.code {
		switch code.opcode() {
		case opLessThan:
			comparisonPC = pc
		case opLoadBool:
			booleans = append(booleans, code)
		}
	}
	if comparisonPC < 0 ||
		prototype.code[comparisonPC+1].opcode() != opJump {
		t.Fatal("value comparison is not represented by a branch")
	}
	if len(booleans) != 2 ||
		booleans[0].a() != 2 ||
		booleans[0].b() != 0 ||
		booleans[0].c() != 1 ||
		booleans[1].a() != 2 ||
		booleans[1].b() != 1 ||
		booleans[1].c() != 0 {
		t.Fatalf(
			"comparison materialization = %#v, want false/true in R2",
			booleans,
		)
	}
}

func TestCompileSourceInvertsComparisonForNot(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@condition.lua",
		"local left, right = ...\n"+
			"local result = not (left < right)\n"+
			"return result",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	comparison := instruction(0)
	loadBoolCount := 0
	for _, code := range prototype.code {
		switch code.opcode() {
		case opLessThan:
			comparison = code
		case opNot:
			t.Fatal("not-comparison emitted a redundant NOT")
		case opLoadBool:
			loadBoolCount++
		}
	}
	if comparison.opcode() != opLessThan || comparison.a() != 0 {
		t.Fatalf(
			"inverted comparison = %s A:%d, want LT A:0",
			comparison.opcode(),
			comparison.a(),
		)
	}
	if loadBoolCount != 2 {
		t.Fatalf("LOADBOOL count = %d, want 2", loadBoolCount)
	}
}

func TestCompileSourceResolvesLogicalLeftBeforeRightOperand(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@condition.lua",
		"local left, right = ...\n"+
			"return (left < right and 1) + external",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	lastBoolean := -1
	global := -1
	for pc, code := range prototype.code {
		switch code.opcode() {
		case opLoadBool:
			lastBoolean = pc
		case opGetGlobal:
			global = pc
		}
	}
	if lastBoolean < 0 || global <= lastBoolean {
		t.Fatalf(
			"logical materialization/global positions = %d/%d",
			lastBoolean,
			global,
		)
	}
}

func TestCompileSourceDoesNotFoldConditionalNumericOperand(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@condition.lua",
		"return 2 + (flag and 1)",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	for _, code := range prototype.code {
		if code.opcode() == opAdd {
			return
		}
	}
	t.Fatal("conditional numeric operand was incorrectly constant-folded")
}

func TestCompileSourceDoesNotFoldUnaryConditionalOperand(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@condition.lua",
		"return -(flag and 1)",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	for _, code := range prototype.code {
		if code.opcode() == opUnaryMinus {
			return
		}
	}
	t.Fatal("conditional unary operand was incorrectly constant-folded")
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

func TestCompileSourceKeepsHighRKSpillAboveActiveLocals(t *testing.T) {
	var source strings.Builder
	source.WriteString("local sink = 0\n")
	for number := 1; number <= 256; number++ {
		source.WriteString("sink = ")
		source.WriteString(strconv.Itoa(number))
		source.WriteByte('\n')
	}
	source.WriteString(
		"local left, right = ...\n" +
			"left = right + 256\n" +
			"return left",
	)

	prototype, syntaxError := compileSource("@spill.lua", source.String())
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	constantIndex := -1
	for index, constant := range prototype.constants {
		if constant.kind() == NumberKind &&
			constant.bits == math.Float64bits(256) {
			constantIndex = index
			break
		}
	}
	if constantIndex <= maxRegisterConstant {
		t.Fatalf(
			"constant index = %d, want above RK limit %d",
			constantIndex,
			maxRegisterConstant,
		)
	}

	spill := -1
	var add instruction
	for _, code := range prototype.code {
		if code.opcode() == opLoadK && code.bx() == constantIndex {
			spill = code.a()
		}
		if code.opcode() == opAdd {
			add = code
		}
	}
	if spill < 3 || spill >= prototype.RegisterCount() {
		t.Fatalf(
			"spill register = R%d outside temporary suffix [R3,R%d)",
			spill,
			prototype.RegisterCount(),
		)
	}
	if add.opcode() != opAdd ||
		add.a() != 1 ||
		add.b() != 2 ||
		add.c() != spill {
		t.Fatalf(
			"ADD = A:%d B:%d C:%d, want A:1 B:2 C:%d",
			add.a(),
			add.b(),
			add.c(),
			spill,
		)
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
	source.WriteString(
		"local left, right = ...\n" +
			`return "prefix" .. 256 .. (left + right)`,
	)

	prototype, syntaxError := compileSource(
		"@concat.lua",
		source.String(),
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	constantIndex := -1
	for index, constant := range prototype.constants {
		if constant.kind() == NumberKind &&
			constant.bits == math.Float64bits(256) {
			constantIndex = index
			break
		}
	}
	if constantIndex <= maxRegisterConstant {
		t.Fatal("test constant did not exceed the RK range")
	}

	spill := -1
	var concat instruction
	concatCount := 0
	for _, code := range prototype.code {
		if code.opcode() == opLoadK && code.bx() == constantIndex {
			spill = code.a()
		}
		if code.opcode() == opConcat {
			concat = code
			concatCount++
		}
	}
	if concatCount != 1 ||
		concat.b() < 3 ||
		concat.c()-concat.b() != 2 ||
		concat.c() >= prototype.RegisterCount() {
		t.Fatalf(
			"CONCAT = count:%d B:%d C:%d registers:%d",
			concatCount,
			concat.b(),
			concat.c(),
			prototype.RegisterCount(),
		)
	}
	if spill < concat.b() || spill > concat.c() {
		t.Fatalf(
			"high constant spill R%d is outside CONCAT range [R%d,R%d]",
			spill,
			concat.b(),
			concat.c(),
		)
	}
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
		{"break outside loop", "break", "no loop to break"},
		{"leading empty statement", "; value = 1", "unsupported statement"},
		{"repeated semicolon", "value = 1;; value = 2", "unsupported statement"},
		{"hexadecimal fraction", "return 0x1.2", "return must be the last"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prototype, syntaxError := compileSource("@bad.lua", test.source)
			if prototype != nil || syntaxError == nil {
				t.Fatal("malformed source was accepted")
			}
			if syntaxError.Category() != SyntaxError ||
				syntaxError.Value().Kind() != StringKind ||
				!strings.Contains(syntaxError.Error(), "bad.lua:1:") ||
				!strings.Contains(syntaxError.Error(), test.want) {
				t.Fatalf("syntax error = %v", syntaxError)
			}
			if message, ok := syntaxError.Value().AsString(); !ok ||
				message != syntaxError.Error() {
				t.Fatalf("syntax error value = %q, %v", message, ok)
			}
		})
	}
}

func TestCompileSourceExpressionMatrix(t *testing.T) {
	atoms := []string{
		"nil",
		"false",
		"true",
		"0",
		"1",
		`"text"`,
		"left",
		"right",
		"(...)",
		"(left < right and 1)",
	}
	operators := []string{
		"and",
		"or",
		"==",
		"~=",
		"<",
		"<=",
		">",
		">=",
		"..",
		"+",
		"-",
		"*",
		"/",
		"%",
		"^",
	}
	for _, left := range atoms {
		for _, operation := range operators {
			for _, right := range atoms {
				source := "local left, right = ...\nreturn (" +
					left + ") " + operation + " (" + right + ")"
				if _, syntaxError := compileSource(
					"@matrix.lua",
					source,
				); syntaxError != nil {
					t.Fatalf("%s: %v", source, syntaxError)
				}
			}
		}
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
		"return outer(first(), middle(), final(...))",
		"local object = factory(); return object:method((argument()))",
		"local first, second = produce(); return consume(first, second)",
		"return {first(), name = value(), [key()] = other(), final(...)}",
		"consume {1, 2, nested = {3, 4}}",
		"local index, object = ...; object[index], index = left(), right()",
		"factory()[key()], other.field = first(), second(...)",
		"local function recurse(value) return recurse, value end; return recurse",
		"function object.path:method(first, ...) return self, first, ... end",
		"local captured = 1; return function() return function() return captured end end",
		"do local captured = 1; sink = function() return captured end end",
		"if first then value = 1 elseif second then value = 2 else value = 3 end",
		"if left and (right or not fallback) then return value else return end",
		"if not (left and right) then return elseif (not left) or right then return end",
		"if condition then local captured = 1; sink = function() return captured end end",
		"while running and not stopped do if finished then break end running = next(running) end",
		"repeat local value = next() until value and ready",
		"for index = first(), last(), step() do consume(index) end",
		"for key, value in iterator, state, control do consume(key, value) end",
		"repeat local value = 1; sink = function() return value end until done",
		"; value = 1",
		"value = 1;; value = 2",
		"if true return else",
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
