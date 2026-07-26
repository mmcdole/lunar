package lua

import (
	"strconv"
	"strings"
	"testing"
)

func TestCompileSourceCapturesAndMutatesUpvalue(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@closure.lua",
		"local value = 1\n"+
			"return function(replacement)\n"+
			"  value = replacement\n"+
			"  return value\n"+
			"end",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if len(prototype.children) != 1 {
		t.Fatalf("child count = %d, want 1", len(prototype.children))
	}
	child := prototype.children[0]
	if child.UpvalueCount() != 1 ||
		child.debug == nil ||
		len(child.debug.upvalues) != 1 ||
		child.debug.upvalues[0].text != "value" {
		t.Fatal("child did not publish its captured value")
	}

	closurePC := opcodeIndex(prototype.code, opClosure)
	if closurePC < 0 || closurePC+1 >= len(prototype.code) {
		t.Fatal("parent has no complete closure instruction")
	}
	closure := prototype.code[closurePC]
	binding := prototype.code[closurePC+1]
	if closure.bx() != 0 ||
		binding.opcode() != opMove ||
		binding.a() != 0 ||
		binding.b() != 0 ||
		binding.c() != 0 {
		t.Fatalf(
			"closure binding = %s A:%d B:%d C:%d",
			binding.opcode(),
			binding.a(),
			binding.b(),
			binding.c(),
		)
	}
	if opcodeIndex(prototype.code, opClose) >= 0 {
		t.Fatal("function scope emitted a redundant CLOSE")
	}

	var set instruction
	var get instruction
	for _, code := range child.code {
		switch code.opcode() {
		case opSetUpvalue:
			set = code
		case opGetUpvalue:
			get = code
		}
	}
	if set.opcode() != opSetUpvalue ||
		set.a() != 0 ||
		set.b() != 0 ||
		set.c() != 0 {
		t.Fatalf(
			"SETUPVAL = A:%d B:%d C:%d",
			set.a(),
			set.b(),
			set.c(),
		)
	}
	if get.opcode() != opGetUpvalue || get.b() != 0 {
		t.Fatalf("GETUPVAL = A:%d B:%d", get.a(), get.b())
	}
}

func TestCompileSourceDeduplicatesAndChainsUpvalues(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@chain.lua",
		"local captured = 1\n"+
			"return function()\n"+
			"  return function()\n"+
			"    return captured + captured\n"+
			"  end\n"+
			"end",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	middle := prototype.children[0]
	inner := middle.children[0]
	if middle.UpvalueCount() != 1 || inner.UpvalueCount() != 1 {
		t.Fatalf(
			"upvalue counts = middle %d, inner %d; want 1 each",
			middle.UpvalueCount(),
			inner.UpvalueCount(),
		)
	}

	rootClosure := opcodeIndex(prototype.code, opClosure)
	middleClosure := opcodeIndex(middle.code, opClosure)
	if rootClosure < 0 || middleClosure < 0 {
		t.Fatal("nested closure instruction is missing")
	}
	if binding := prototype.code[rootClosure+1]; binding.opcode() != opMove ||
		binding.b() != 0 {
		t.Fatalf("root binding = %s B:%d, want MOVE from R0", binding.opcode(), binding.b())
	}
	if binding := middle.code[middleClosure+1]; binding.opcode() != opGetUpvalue ||
		binding.b() != 0 {
		t.Fatalf(
			"middle binding = %s B:%d, want GETUPVAL 0",
			binding.opcode(),
			binding.b(),
		)
	}

	gets := 0
	for _, code := range inner.code {
		if code.opcode() == opGetUpvalue {
			gets++
			if code.b() != 0 {
				t.Fatalf("inner GETUPVAL index = %d, want 0", code.b())
			}
		}
	}
	if gets != 2 {
		t.Fatalf("inner GETUPVAL count = %d, want 2", gets)
	}
}

func TestCompileSourceClosesCapturedLexicalBlock(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@close.lua",
		"local closure\n"+
			"do\n"+
			"  local captured = 1\n"+
			"  closure = function() return captured end\n"+
			"end\n"+
			"local replacement = 2\n"+
			"return closure, replacement",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	closurePC := opcodeIndex(prototype.code, opClosure)
	closePC := opcodeIndex(prototype.code, opClose)
	if closurePC < 0 || closePC != closurePC+2 {
		t.Fatalf(
			"CLOSE pc = %d, closure pc = %d; want closure, binding, CLOSE",
			closePC,
			closurePC,
		)
	}
	closeCode := prototype.code[closePC]
	if closeCode.a() != 1 || closeCode.b() != 0 || closeCode.c() != 0 {
		t.Fatalf(
			"CLOSE = A:%d B:%d C:%d, want A:1 B:0 C:0",
			closeCode.a(),
			closeCode.b(),
			closeCode.c(),
		)
	}
	binding := prototype.code[closurePC+1]
	if binding.opcode() != opMove || binding.b() != 1 {
		t.Fatalf(
			"captured-block binding = %s B:%d, want MOVE from R1",
			binding.opcode(),
			binding.b(),
		)
	}
	if len(prototype.debug.locals) != 3 {
		t.Fatalf(
			"debug local count = %d, want 3",
			len(prototype.debug.locals),
		)
	}
	captured := prototype.debug.locals[1]
	replacement := prototype.debug.locals[2]
	if captured.endPC != uint32(closePC) ||
		replacement.startPC < uint32(closePC+1) {
		t.Fatalf(
			"local lifetimes overlap CLOSE: captured end %d, replacement start %d",
			captured.endPC,
			replacement.startPC,
		)
	}

	uncaptured, syntaxError := compileSource(
		"@close.lua",
		"do local value = 1 end\nreturn 2",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if opcodeIndex(uncaptured.code, opClose) >= 0 {
		t.Fatal("uncaptured lexical block emitted CLOSE")
	}
}

func TestCompileSourceLocalFunctionIsRecursive(t *testing.T) {
	recursive, syntaxError := compileSource(
		"@recursive.lua",
		"local function recurse() return recurse end\n"+
			"return recurse",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	child := recursive.children[0]
	if child.UpvalueCount() != 1 {
		t.Fatalf("recursive child upvalues = %d, want 1", child.UpvalueCount())
	}
	closurePC := opcodeIndex(recursive.code, opClosure)
	if closurePC < 0 ||
		recursive.code[closurePC].a() != 0 ||
		recursive.code[closurePC+1].opcode() != opMove ||
		recursive.code[closurePC+1].b() != 0 {
		t.Fatal("local function did not close over its own destination")
	}
	if recursive.debug.locals[0].startPC != uint32(closurePC+2) {
		t.Fatalf(
			"recursive local starts at pc %d, want %d",
			recursive.debug.locals[0].startPC,
			closurePC+2,
		)
	}

	initializer, syntaxError := compileSource(
		"@recursive.lua",
		"local recurse = function() return recurse end\n"+
			"return recurse",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	child = initializer.children[0]
	if child.UpvalueCount() != 0 ||
		opcodeIndex(child.code, opGetGlobal) < 0 {
		t.Fatal("ordinary local initializer incorrectly captured its destination")
	}
}

func TestCompileSourceNamedMethodAndUpvalueFunctionStores(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@method.lua",
		"function object.path:method(first, second, ...)\n"+
			"  return self, first, second, ...\n"+
			"end",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	child := prototype.children[0]
	if child.ParameterCount() != 3 || !child.IsVararg() {
		t.Fatalf(
			"method metadata = %d parameters, vararg %v",
			child.ParameterCount(),
			child.IsVararg(),
		)
	}
	if child.varargFlags != varargHasArg|varargIsVararg {
		t.Fatalf(
			"method vararg flags = %#x, want HASARG|ISVARARG",
			child.varargFlags,
		)
	}
	wantLocals := []string{"self", "first", "second", "arg"}
	if len(child.debug.locals) != len(wantLocals) {
		t.Fatalf(
			"method debug local count = %d, want %d",
			len(child.debug.locals),
			len(wantLocals),
		)
	}
	for index, want := range wantLocals {
		if got := child.debug.locals[index].name.text; got != want {
			t.Fatalf("method local %d = %q, want %q", index, got, want)
		}
	}
	if opcodeIndex(prototype.code, opSetField) < 0 {
		t.Fatal("named method did not store through its final table field")
	}

	upvalueStore, syntaxError := compileSource(
		"@method.lua",
		"local target\n"+
			"return function()\n"+
			"  function target() end\n"+
			"end",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	middle := upvalueStore.children[0]
	if middle.UpvalueCount() != 1 ||
		opcodeIndex(middle.code, opSetUpvalue) < 0 {
		t.Fatal("named function did not store into an enclosing upvalue")
	}
}

func TestCompileSourceFunctionParametersAndVarargIsolation(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@parameters.lua",
		"return function(first, first) return first end",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	child := prototype.children[0]
	if child.ParameterCount() != 2 ||
		len(child.debug.locals) != 2 ||
		child.debug.locals[0].name != child.debug.locals[1].name {
		t.Fatal("duplicate parameter names were not retained as distinct locals")
	}
	ret := child.code[len(child.code)-1]
	if ret.opcode() != opReturn || ret.a() != 1 {
		t.Fatalf("duplicate parameter resolved from R%d, want R1", ret.a())
	}

	legacy, syntaxError := compileSource(
		"@parameters.lua",
		"return function(...) return arg end",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	child = legacy.children[0]
	if child.ParameterCount() != 0 ||
		child.varargFlags !=
			varargHasArg|varargIsVararg|varargNeedsArg ||
		len(child.debug.locals) != 1 ||
		child.debug.locals[0].name.text != "arg" {
		t.Fatal("legacy Lua 5.1 arg local metadata is incomplete")
	}

	if _, syntaxError = compileSource(
		"@parameters.lua",
		"return function(...) return function() return ... end end",
	); syntaxError == nil ||
		!strings.Contains(syntaxError.Error(), "outside a vararg function") {
		t.Fatalf("nested vararg isolation error = %v", syntaxError)
	}
}

func TestCompileSourceUsesLuaFunctionLineRanges(t *testing.T) {
	tests := []struct {
		name            string
		source          string
		first, last     int
		closureLine     int
		globalStoreLine int
	}{
		{
			name:        "anonymous",
			source:      "return function\n() end",
			first:       2,
			last:        2,
			closureLine: 2,
		},
		{
			name:        "local",
			source:      "local function value\n() end",
			first:       2,
			last:        2,
			closureLine: 2,
		},
		{
			name:            "named global",
			source:          "function value\n() end",
			first:           1,
			last:            2,
			closureLine:     2,
			globalStoreLine: 1,
		},
		{
			name:        "named local",
			source:      "local value\nfunction value\n() end",
			first:       2,
			last:        3,
			closureLine: 2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prototype, syntaxError := compileSource(
				"@lines.lua",
				test.source,
			)
			if syntaxError != nil {
				t.Fatal(syntaxError)
			}
			first, last := prototype.children[0].LineRange()
			if first != test.first || last != test.last {
				t.Fatalf(
					"function lines = %d..%d, want %d..%d",
					first,
					last,
					test.first,
					test.last,
				)
			}
			closurePC := opcodeIndex(prototype.code, opClosure)
			if got := prototype.LineAt(closurePC); got != test.closureLine {
				t.Fatalf(
					"CLOSURE line = %d, want %d",
					got,
					test.closureLine,
				)
			}
			if test.globalStoreLine != 0 {
				storePC := opcodeIndex(prototype.code, opSetGlobal)
				if got := prototype.LineAt(storePC); got != test.globalStoreLine {
					t.Fatalf(
						"SETGLOBAL line = %d, want %d",
						got,
						test.globalStoreLine,
					)
				}
			}
		})
	}
}

func TestCompileSourceEndsFallthroughLocalsBeforeImplicitReturn(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@local-lines.lua",
		"return function()\n"+
			"  local value = 1\n"+
			"end",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	child := prototype.children[0]
	returnPC := opcodeIndex(child.code, opReturn)
	if returnPC < 0 || len(child.debug.locals) != 1 {
		t.Fatal("fallthrough function metadata is incomplete")
	}
	if got := int(child.debug.locals[0].endPC); got != returnPC {
		t.Fatalf(
			"fallthrough local ends at pc %d, want implicit RETURN pc %d",
			got,
			returnPC,
		)
	}
}

func TestCompileSourceCapturesShadowedAndSiblingLocals(t *testing.T) {
	shadowed, syntaxError := compileSource(
		"@shadow.lua",
		"local captured = 1\n"+
			"local first = function() return captured end\n"+
			"do\n"+
			"  local captured = 2\n"+
			"  second = function() return captured end\n"+
			"end\n"+
			"return first, second",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if len(shadowed.children) != 2 {
		t.Fatalf("shadowed child count = %d, want 2", len(shadowed.children))
	}
	firstClosure := opcodeIndex(shadowed.code, opClosure)
	secondClosure := -1
	for pc := firstClosure + 1; pc < len(shadowed.code); pc++ {
		if shadowed.code[pc].opcode() == opClosure {
			secondClosure = pc
			break
		}
	}
	if firstClosure < 0 || secondClosure < 0 {
		t.Fatal("shadowed closures are missing")
	}
	firstBinding := shadowed.code[firstClosure+1]
	secondBinding := shadowed.code[secondClosure+1]
	if firstBinding.opcode() != opMove ||
		secondBinding.opcode() != opMove ||
		firstBinding.b() == secondBinding.b() {
		t.Fatalf(
			"shadowed bindings = R%d and R%d",
			firstBinding.b(),
			secondBinding.b(),
		)
	}
	closePC := opcodeIndex(shadowed.code, opClose)
	storePC := -1
	for pc := secondClosure + 2; pc < closePC; pc++ {
		if shadowed.code[pc].opcode() == opSetGlobal {
			storePC = pc
			break
		}
	}
	if storePC < 0 ||
		closePC <= storePC ||
		shadowed.code[closePC].a() != secondBinding.b() {
		t.Fatal("inner shadowed local did not close at its owning block")
	}

	siblings, syntaxError := compileSource(
		"@shadow.lua",
		"local captured = 1\n"+
			"return function() return captured end, "+
			"function() return captured end",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if len(siblings.children) != 2 {
		t.Fatalf("sibling child count = %d, want 2", len(siblings.children))
	}
	bindings := 0
	for pc, code := range siblings.code {
		if code.opcode() != opClosure {
			continue
		}
		binding := siblings.code[pc+1]
		if binding.opcode() != opMove || binding.b() != 0 {
			t.Fatalf(
				"sibling binding = %s B:%d, want MOVE from R0",
				binding.opcode(),
				binding.b(),
			)
		}
		bindings++
	}
	if bindings != 2 {
		t.Fatalf("sibling binding count = %d, want 2", bindings)
	}
}

func TestCompileSourcePlacesClosureValuesWithoutExtraPaths(t *testing.T) {
	for _, source := range []string{
		"return consume(function() return 1 end)",
		"return {callback = function() end, function() end}",
		"local first, second; " +
			"first, second = function() end, function() end; " +
			"return first and second",
		"return enabled and function() return enabled end",
	} {
		prototype, syntaxError := compileSource(
			"@placement.lua",
			source,
		)
		if syntaxError != nil {
			t.Fatalf("%q: %v", source, syntaxError)
		}
		if prototype.ChildCount() == 0 ||
			opcodeIndex(prototype.code, opClosure) < 0 {
			t.Fatalf("%q emitted no closure", source)
		}
	}
}

func TestCompileSourcePreservesTailCallWithCapturedRootLocals(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@tail.lua",
		"return function()\n"+
			"  local captured = 1\n"+
			"  local function read() return captured end\n"+
			"  return read()\n"+
			"end",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	child := prototype.children[0]
	tailPC := opcodeIndex(child.code, opTailCall)
	if tailPC < 0 ||
		tailPC+1 >= len(child.code) ||
		child.code[tailPC+1].opcode() != opReturn {
		t.Fatal("captured function did not retain TAILCALL/RETURN adjacency")
	}
	if opcodeIndex(child.code, opClose) >= 0 {
		t.Fatal("function-level capture emitted CLOSE before frame teardown")
	}
}

func TestCompileSourceEnforcesFunctionLimitsAndGrammar(t *testing.T) {
	for _, test := range []struct {
		count     int
		wantError bool
	}{
		{count: maxLuaUpvalues},
		{count: maxLuaUpvalues + 1, wantError: true},
	} {
		count := test.count
		names := numberedNames("up", count)
		source := "return function(" +
			strings.Join(names, ",") +
			") return function() return " +
			strings.Join(names, ",") +
			" end end"
		prototype, syntaxError := compileSource("@limits.lua", source)
		if test.wantError {
			if syntaxError == nil ||
				!strings.Contains(syntaxError.Error(), "upvalues") {
				t.Fatalf("%d upvalues error = %v", count, syntaxError)
			}
			continue
		}
		if syntaxError != nil {
			t.Fatalf("%d upvalues: %v", count, syntaxError)
		}
		if got := prototype.children[0].children[0].UpvalueCount(); got != count {
			t.Fatalf("upvalue count = %d, want %d", got, count)
		}
	}

	for _, test := range []struct {
		count     int
		wantError bool
	}{
		{count: maxActiveLocals},
		{count: maxActiveLocals + 1, wantError: true},
	} {
		count := test.count
		source := "return function(" +
			strings.Join(numberedNames("parameter", count), ",") +
			") end"
		_, syntaxError := compileSource("@limits.lua", source)
		if test.wantError != (syntaxError != nil) {
			t.Fatalf("%d parameters error = %v", count, syntaxError)
		}
	}

	for _, test := range []struct {
		name      string
		fixed     int
		method    bool
		wantError bool
	}{
		{name: "vararg boundary", fixed: maxActiveLocals - 1},
		{
			name:      "vararg overflow",
			fixed:     maxActiveLocals,
			wantError: true,
		},
		{
			name:   "method vararg boundary",
			fixed:  maxActiveLocals - 2,
			method: true,
		},
		{
			name:      "method vararg overflow",
			fixed:     maxActiveLocals - 1,
			method:    true,
			wantError: true,
		},
	} {
		names := numberedNames("parameter", test.fixed)
		parameters := strings.Join(names, ",")
		if parameters != "" {
			parameters += ","
		}
		var source string
		if test.method {
			source = "function object:method(" + parameters + "...) end"
		} else {
			source = "return function(" + parameters + "...) end"
		}
		_, syntaxError := compileSource("@limits.lua", source)
		if test.wantError != (syntaxError != nil) {
			t.Fatalf("%s error = %v", test.name, syntaxError)
		}
	}

	for _, source := range []string{
		"return function(parameter,) end",
		"return function(..., parameter) end",
		"function () end",
		"function object:method:other() end",
		"function object[field]() end",
		"local function object.field() end",
		"local function name(parameter",
	} {
		if _, syntaxError := compileSource("@grammar.lua", source); syntaxError == nil {
			t.Fatalf("compiler accepted malformed function %q", source)
		}
	}
}

func opcodeIndex(code []instruction, operation opcode) int {
	for pc, value := range code {
		if value.opcode() == operation {
			return pc
		}
	}
	return -1
}

func numberedNames(prefix string, count int) []string {
	names := make([]string, count)
	for index := range names {
		names[index] = prefix + strconv.Itoa(index)
	}
	return names
}
