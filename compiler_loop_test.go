package lua

import (
	"strings"
	"testing"
)

func TestCompileSourceLowersWhileAndNearestBreak(t *testing.T) {
	prototype := compileLoopPrototype(t, `
local outer, inner = ...
while outer do
	while inner do
		break
	end
	marker = 1
	break
end
return marker
`)

	var backward, forward []int
	for pc, code := range prototype.code {
		switch code.opcode() {
		case opLoadBool, opTestSet:
			t.Fatalf(
				"while condition materialized through %s at pc %d\n%s",
				code.opcode(),
				pc,
				formatControlCode(prototype),
			)
		case opJump:
			target := pc + 1 + code.sbx()
			if target <= pc {
				backward = append(backward, pc)
			} else {
				forward = append(forward, pc)
			}
		}
	}
	if len(backward) != 2 {
		t.Fatalf(
			"back-edge count = %d, want 2\n%s",
			len(backward),
			formatControlCode(prototype),
		)
	}
	for _, pc := range backward {
		target := pc + 1 + prototype.code[pc].sbx()
		if target >= len(prototype.code) ||
			prototype.code[target].opcode() != opTest {
			t.Fatalf(
				"while back edge at %d targets %d, want TEST\n%s",
				pc,
				target,
				formatControlCode(prototype),
			)
		}
	}

	marker := opcodeIndex(prototype.code, opSetGlobal)
	if marker < 0 {
		t.Fatal("statement after inner loop was not compiled")
	}
	innerBreak := false
	outerBreak := false
	for _, pc := range forward {
		target := pc + 1 + prototype.code[pc].sbx()
		switch {
		case pc < marker && target <= marker && marker-target <= 2:
			innerBreak = true
		case pc > marker &&
			target < len(prototype.code) &&
			prototype.code[target].opcode() == opGetGlobal:
			outerBreak = true
		}
	}
	if !innerBreak || !outerBreak {
		t.Fatalf(
			"breaks did not select their nearest loops: inner=%t outer=%t\n%s",
			innerBreak,
			outerBreak,
			formatControlCode(prototype),
		)
	}
}

func TestCompileSourceClosesCapturedWhileLocalsOnEveryExit(t *testing.T) {
	prototype := compileLoopPrototype(t, `
local result
local running, stop = ...
while running do
	local captured = running
	result = function() return captured end
	if stop then break end
	running = next(running)
end
return result
`)

	var closes []int
	for pc, code := range prototype.code {
		if code.opcode() == opClose {
			closes = append(closes, pc)
		}
	}
	if len(closes) != 2 {
		t.Fatalf(
			"CLOSE count = %d, want break and back-edge cleanup\n%s",
			len(closes),
			formatControlCode(prototype),
		)
	}
	closeBase := prototype.code[closes[0]].a()
	foundForward := false
	foundBackward := false
	for _, pc := range closes {
		if prototype.code[pc].a() != closeBase ||
			pc+1 >= len(prototype.code) ||
			prototype.code[pc+1].opcode() != opJump {
			t.Fatalf(
				"CLOSE at pc %d does not own a matching exit\n%s",
				pc,
				formatControlCode(prototype),
			)
		}
		target := pc + 2 + prototype.code[pc+1].sbx()
		if target <= pc {
			foundBackward = true
		} else {
			foundForward = true
		}
	}
	if !foundForward || !foundBackward {
		t.Fatalf(
			"captured while paths: forward=%t backward=%t\n%s",
			foundForward,
			foundBackward,
			formatControlCode(prototype),
		)
	}

	captured := loopDebugLocal(prototype, "captured", 0)
	if captured == nil ||
		captured.endPC != uint32(closes[len(closes)-1]) {
		t.Fatalf(
			"captured local lifetime = %+v, normal CLOSE pc = %d",
			captured,
			closes[len(closes)-1],
		)
	}
}

func TestCompileSourceBreakClosesOnlyTheInnermostCapturedLoop(t *testing.T) {
	prototype := compileLoopPrototype(t, `
local outerResult, innerResult
local outer, inner = ...
while outer do
	local outerValue = outer
	outerResult = function() return outerValue end
	while inner do
		local innerValue = inner
		innerResult = function() return innerValue end
		break
	end
	consume(outerValue)
	break
end
return outerResult, innerResult
`)

	var bindings []int
	for pc, code := range prototype.code {
		if code.opcode() != opClosure ||
			pc+1 >= len(prototype.code) ||
			prototype.code[pc+1].opcode() != opMove {
			continue
		}
		bindings = append(bindings, prototype.code[pc+1].b())
	}
	if len(bindings) != 2 || bindings[0] >= bindings[1] {
		t.Fatalf(
			"outer/inner capture registers = %v\n%s",
			bindings,
			formatControlCode(prototype),
		)
	}

	var closes []int
	for pc, code := range prototype.code {
		if code.opcode() == opClose {
			closes = append(closes, pc)
		}
	}
	if len(closes) != 4 {
		t.Fatalf(
			"nested loop CLOSE count = %d, want 4\n%s",
			len(closes),
			formatControlCode(prototype),
		)
	}
	if prototype.code[closes[0]].a() != bindings[1] ||
		prototype.code[closes[1]].a() != bindings[1] ||
		prototype.code[closes[2]].a() != bindings[0] ||
		prototype.code[closes[3]].a() != bindings[0] {
		t.Fatalf(
			"nested CLOSE bases do not preserve the outer capture\n%s",
			formatControlCode(prototype),
		)
	}
}

func TestCompileSourceLowersRepeatWithScopedConditionCleanup(t *testing.T) {
	visible := compileLoopPrototype(t, `
repeat
	local item = ...
until item
`)
	for _, code := range visible.code {
		if code.opcode() != opGetGlobal {
			continue
		}
		name := (*luaString)(visible.constants[code.bx()].ref).text
		if name == "item" {
			t.Fatal("repeat body local was not visible in its until condition")
		}
	}

	captured := compileLoopPrototype(t, `
local result
local done = ...
repeat
	local value = ...
	result = function() return value end
until done
return result
`)
	var closes []int
	for pc, code := range captured.code {
		if code.opcode() == opClose {
			closes = append(closes, pc)
		}
	}
	if len(closes) != 2 ||
		captured.code[closes[0]].a() != captured.code[closes[1]].a() {
		t.Fatalf(
			"captured repeat CLOSEs = %v\n%s",
			closes,
			formatControlCode(captured),
		)
	}
	trueCleanup := closes[0]
	falseCleanup := closes[1]
	if trueCleanup+1 >= len(captured.code) ||
		captured.code[trueCleanup+1].opcode() != opJump ||
		falseCleanup+1 >= len(captured.code) ||
		captured.code[falseCleanup+1].opcode() != opJump {
		t.Fatalf(
			"repeat cleanup paths are not explicit jumps\n%s",
			formatControlCode(captured),
		)
	}
	trueTarget := trueCleanup + 2 + captured.code[trueCleanup+1].sbx()
	falseTarget := falseCleanup + 2 + captured.code[falseCleanup+1].sbx()
	if trueTarget <= trueCleanup || falseTarget >= falseCleanup {
		t.Fatalf(
			"repeat cleanup targets = true:%d false:%d\n%s",
			trueTarget,
			falseTarget,
			formatControlCode(captured),
		)
	}
	conditions := controlConditionJumps(t, captured)
	if len(conditions) != 1 ||
		controlJumpTarget(t, captured, conditions[0]) != falseCleanup {
		t.Fatalf(
			"repeat false condition does not enter false cleanup\n%s",
			formatControlCode(captured),
		)
	}

	conditionCapture := compileLoopPrototype(t, `
repeat
	local value = 1
until function() return value end
`)
	closeCount := 0
	for _, code := range conditionCapture.code {
		if code.opcode() == opClose {
			closeCount++
		}
	}
	if closeCount != 2 {
		t.Fatalf(
			"condition capture CLOSE count = %d, want 2\n%s",
			closeCount,
			formatControlCode(conditionCapture),
		)
	}

	constantExit := compileLoopPrototype(t, `
repeat
	local value = 1
	sink = function() return value end
until true
`)
	closeCount = 0
	for _, code := range constantExit.code {
		if code.opcode() == opClose {
			closeCount++
		}
	}
	if closeCount != 1 {
		t.Fatalf(
			"constant repeat exit CLOSE count = %d, want 1\n%s",
			closeCount,
			formatControlCode(constantExit),
		)
	}

	compound := compileLoopPrototype(t, `
local first, second = ...
repeat
	local value = 1
	sink = function() return value end
until first and not second
`)
	closes = closes[:0]
	for pc, code := range compound.code {
		switch code.opcode() {
		case opClose:
			closes = append(closes, pc)
		case opTestSet, opNot:
			t.Fatalf(
				"compound repeat retained %s at pc %d\n%s",
				code.opcode(),
				pc,
				formatControlCode(compound),
			)
		}
	}
	conditions = controlConditionJumps(t, compound)
	if len(closes) != 2 || len(conditions) != 2 {
		t.Fatalf(
			"compound repeat CLOSEs=%v conditions=%v\n%s",
			closes,
			conditions,
			formatControlCode(compound),
		)
	}
	for _, condition := range conditions {
		if target := controlJumpTarget(
			t,
			compound,
			condition,
		); target != closes[1] {
			t.Fatalf(
				"compound false edge targets %d, want CLOSE at %d\n%s",
				target,
				closes[1],
				formatControlCode(compound),
			)
		}
	}
}

func TestCompileSourceUsesCanonicalNumericForLayout(t *testing.T) {
	prototype := compileLoopPrototype(t, `
local index, lower, upper = ...
for index = lower, upper do
	consume(index)
end
return index
`)
	preparation := opcodeIndex(prototype.code, opForPrep)
	loop := opcodeIndex(prototype.code, opForLoop)
	if preparation < 0 || loop < 0 {
		t.Fatalf("numeric loop is incomplete\n%s", formatControlCode(prototype))
	}
	base := prototype.code[preparation].a()
	if base != 3 ||
		prototype.code[loop].a() != base ||
		preparation+1+prototype.code[preparation].sbx() != loop ||
		loop+1+prototype.code[loop].sbx() != preparation+1 {
		t.Fatalf(
			"numeric loop A/targets are not canonical\n%s",
			formatControlCode(prototype),
		)
	}

	defaultStep := false
	for _, code := range prototype.code[:preparation] {
		if code.opcode() != opLoadK || code.a() != base+2 {
			continue
		}
		constant := prototype.constants[code.bx()]
		number, ok := constant.owningValue().AsNumber()
		defaultStep = ok && number == 1
	}
	if !defaultStep {
		t.Fatalf(
			"numeric loop did not load default step 1 into R%d\n%s",
			base+2,
			formatControlCode(prototype),
		)
	}

	wantLocals := []string{
		"index",
		"lower",
		"upper",
		"(for index)",
		"(for limit)",
		"(for step)",
		"index",
	}
	if got := loopDebugNames(prototype); !equalStrings(got, wantLocals) {
		t.Fatalf("debug locals = %q, want %q", got, wantLocals)
	}
	visible := loopDebugLocal(prototype, "index", 1)
	if visible == nil ||
		visible.startPC != uint32(preparation+1) ||
		visible.endPC != uint32(loop) {
		t.Fatalf(
			"visible numeric variable lifetime = %+v, want [%d,%d)",
			visible,
			preparation+1,
			loop,
		)
	}

	captured := compileLoopPrototype(t, `
local closures = {}
for index = 1, 3 do
	closures[index] = function() return index end
	if index == 2 then break end
end
return closures
`)
	preparation = opcodeIndex(captured.code, opForPrep)
	loop = opcodeIndex(captured.code, opForLoop)
	base = captured.code[preparation].a()
	closes := 0
	for pc, code := range captured.code {
		if code.opcode() != opClose {
			continue
		}
		closes++
		if code.a() != base+3 {
			t.Fatalf(
				"numeric loop CLOSE at pc %d starts at R%d, want R%d\n%s",
				pc,
				code.a(),
				base+3,
				formatControlCode(captured),
			)
		}
	}
	if closes != 2 ||
		loop == 0 ||
		captured.code[loop-1].opcode() != opClose {
		t.Fatalf(
			"captured numeric variable cleanup count/order is wrong\n%s",
			formatControlCode(captured),
		)
	}
}

func TestCompileSourceUsesCanonicalGenericForLayout(t *testing.T) {
	for _, test := range []struct {
		source    string
		results   int
		registers int
	}{
		{"for key in iterator do end", 1, 6},
		{"for key, value in iterator do end", 2, 6},
		{"for a, b, c, d in iterator do end", 4, 7},
	} {
		prototype := compileLoopPrototype(t, test.source)
		iterator := opcodeIndex(prototype.code, opIteratorLoop)
		if iterator < 0 ||
			prototype.code[iterator].c() != test.results ||
			prototype.RegisterCount() != test.registers ||
			iterator+1 >= len(prototype.code) ||
			prototype.code[iterator+1].opcode() != opJump {
			t.Fatalf(
				"%q has invalid iterator layout/register frame\n%s",
				test.source,
				formatControlCode(prototype),
			)
		}
		body := iterator + 2 + prototype.code[iterator+1].sbx()
		if body > iterator {
			t.Fatalf(
				"generic-for back edge targets %d after iterator %d\n%s",
				body,
				iterator,
				formatControlCode(prototype),
			)
		}
		foundPreparation := false
		for pc := 0; pc < iterator; pc++ {
			code := prototype.code[pc]
			if code.opcode() == opJump &&
				pc+1+code.sbx() == iterator {
				foundPreparation = true
			}
		}
		if !foundPreparation {
			t.Fatalf(
				"generic loop has no entry jump to iterator\n%s",
				formatControlCode(prototype),
			)
		}
	}

	expanded := compileLoopPrototype(t, `
for first, second in iterator() do end
`)
	call := opcodeIndex(expanded.code, opCall)
	if call < 0 || expanded.code[call].c() != 4 {
		t.Fatalf(
			"generic iterator call requests C=%d results, want 3\n%s",
			expanded.code[call].c()-1,
			formatControlCode(expanded),
		)
	}

	filled := compileLoopPrototype(t, "for key in iterator do end")
	filledIterator := opcodeIndex(filled.code, opIteratorLoop)
	filledNil := false
	for _, code := range filled.code[:filledIterator] {
		if code.opcode() == opLoadNil &&
			code.a() == 1 &&
			code.b() == 2 {
			filledNil = true
		}
	}
	if !filledNil {
		t.Fatalf(
			"single iterator value did not nil-fill state/control\n%s",
			formatControlCode(filled),
		)
	}

	scoped := compileLoopPrototype(t, `
for key, value in iterator do end
local reused = 1
return reused
`)
	scopedIterator := opcodeIndex(scoped.code, opIteratorLoop)
	afterLoop := scopedIterator + 2
	wantLocals := []string{
		"(for generator)",
		"(for state)",
		"(for control)",
		"key",
		"value",
		"reused",
	}
	if got := loopDebugNames(scoped); !equalStrings(got, wantLocals) {
		t.Fatalf("generic debug locals = %q, want %q", got, wantLocals)
	}
	for _, name := range []string{
		"(for generator)",
		"(for state)",
		"(for control)",
	} {
		local := loopDebugLocal(scoped, name, 0)
		if local == nil || local.endPC != uint32(afterLoop) {
			t.Fatalf("%s lifetime = %+v, loop end = %d", name, local, afterLoop)
		}
	}
	for _, name := range []string{"key", "value"} {
		local := loopDebugLocal(scoped, name, 0)
		if local == nil || local.endPC != uint32(scopedIterator) {
			t.Fatalf("%s lifetime = %+v, iterator = %d", name, local, scopedIterator)
		}
	}
	reused := loopDebugLocal(scoped, "reused", 0)
	if reused == nil || reused.startPC <= uint32(afterLoop) {
		t.Fatalf("post-loop local lifetime = %+v, loop end = %d", reused, afterLoop)
	}

	truncated := compileLoopPrototype(t, `
for key in first(), second(), third(), fourth() do end
`)
	callCount := 0
	for _, code := range truncated.code {
		if code.opcode() == opCall {
			callCount++
		}
	}
	if callCount != 4 {
		t.Fatalf(
			"generic-for evaluated %d of 4 header calls\n%s",
			callCount,
			formatControlCode(truncated),
		)
	}

	captured := compileLoopPrototype(t, `
local result
for key, value in iterator do
	result = function() return key, value end
	if key then break end
end
return result
`)
	iterator := opcodeIndex(captured.code, opIteratorLoop)
	base := captured.code[iterator].a()
	closeCount := 0
	for pc, code := range captured.code {
		if code.opcode() != opClose {
			continue
		}
		closeCount++
		if code.a() != base+3 {
			t.Fatalf(
				"generic CLOSE at pc %d starts at R%d, want R%d\n%s",
				pc,
				code.a(),
				base+3,
				formatControlCode(captured),
			)
		}
	}
	if closeCount != 2 ||
		iterator == 0 ||
		captured.code[iterator-1].opcode() != opClose {
		t.Fatalf(
			"captured generic variables are not closed on both paths\n%s",
			formatControlCode(captured),
		)
	}
}

func TestCompileSourceEnforcesLoopGrammarAndLimits(t *testing.T) {
	valid := []string{
		"while true do break end",
		"while true do do break end value = 1 end",
		"repeat local value = 1 until value",
		"repeat return until true",
		"repeat break; until true",
		"for index = 3, 1, -1 do end",
		"for key, value in iterator, state, control do end",
	}
	for _, source := range valid {
		compileLoopPrototype(t, source)
	}

	invalid := []struct {
		source string
		want   string
	}{
		{"break", "no loop to break"},
		{"while true do break value = 1 end", "break must be the last"},
		{
			"while true do local f = function() break end end",
			"no loop to break",
		},
		{"repeat value = 1", "expected until"},
		{"for index 1, 2 do end", "expected = or in"},
		{"for key, in iterator do end", "expected <name>"},
		{"for key in iterator end", "expected do"},
	}
	for _, test := range invalid {
		_, syntaxError := compileSource("@loop.lua", test.source)
		if syntaxError == nil ||
			!strings.Contains(syntaxError.Error(), test.want) {
			t.Errorf("%q error = %v, want %q", test.source, syntaxError, test.want)
		}
	}

	outer := strings.Join(numberedNames("local", 196), ",")
	compileLoopPrototype(
		t,
		"local "+outer+"\nfor index = 1, 1 do end",
	)
	tooMany := strings.Join(numberedNames("local", 197), ",")
	if _, syntaxError := compileSource(
		"@loop.lua",
		"local "+tooMany+"\nfor index = 1, 1 do end",
	); syntaxError == nil ||
		!strings.Contains(syntaxError.Error(), "active locals") {
		t.Fatalf("numeric-for active-local overflow = %v", syntaxError)
	}
}

func TestLoopOpcodeVerificationEnforcesCanonicalFramesAndPairs(t *testing.T) {
	validIterator := testPrototypeBuilder(
		makeABC(opIteratorLoop, 0, 0, 1),
		makeAsBx(opJump, 0, 0),
		makeABC(opReturn, 0, 1, 0),
	)
	validIterator.registers = 6
	if _, syntaxError := validIterator.seal(); syntaxError != nil {
		t.Fatalf("valid one-result TFORLOOP: %v", syntaxError)
	}
	for _, test := range []struct {
		results   int
		registers int
	}{
		{results: 3, registers: 6},
		{results: 4, registers: 7},
	} {
		builder := testPrototypeBuilder(
			makeABC(opIteratorLoop, 0, 0, test.results),
			makeAsBx(opJump, 0, 0),
			makeABC(opReturn, 0, 1, 0),
		)
		builder.registers = test.registers
		if _, syntaxError := builder.seal(); syntaxError != nil {
			t.Fatalf(
				"valid %d-result TFORLOOP: %v",
				test.results,
				syntaxError,
			)
		}
	}

	shortIterator := testPrototypeBuilder(
		makeABC(opIteratorLoop, 0, 0, 1),
		makeAsBx(opJump, 0, 0),
		makeABC(opReturn, 0, 1, 0),
	)
	shortIterator.registers = 5
	assertPrototypeSyntaxError(t, shortIterator)
	wideShortIterator := testPrototypeBuilder(
		makeABC(opIteratorLoop, 0, 0, 4),
		makeAsBx(opJump, 0, 0),
		makeABC(opReturn, 0, 1, 0),
	)
	wideShortIterator.registers = 6
	assertPrototypeSyntaxError(t, wideShortIterator)
	for _, code := range [][]instruction{
		{
			makeABC(opIteratorLoop, 0, 0, 0),
			makeAsBx(opJump, 0, 0),
			makeABC(opReturn, 0, 1, 0),
		},
		{
			makeABC(opIteratorLoop, 0, 1, 1),
			makeAsBx(opJump, 0, 0),
			makeABC(opReturn, 0, 1, 0),
		},
		{
			makeABC(opIteratorLoop, 0, 0, 1),
			makeABC(opReturn, 0, 1, 0),
		},
	} {
		builder := testPrototypeBuilder(code...)
		builder.registers = 6
		assertPrototypeSyntaxError(t, builder)
	}

	validNumeric := testPrototypeBuilder(
		makeAsBx(opForPrep, 0, 1),
		makeABC(opMove, 3, 3, 0),
		makeAsBx(opForLoop, 0, -2),
		makeABC(opReturn, 0, 1, 0),
	)
	validNumeric.registers = 4
	if _, syntaxError := validNumeric.seal(); syntaxError != nil {
		t.Fatalf("valid numeric loop: %v", syntaxError)
	}

	mismatchedNumeric := testPrototypeBuilder(
		makeAsBx(opForPrep, 0, 1),
		makeABC(opMove, 3, 3, 0),
		makeAsBx(opForLoop, 1, -2),
		makeABC(opReturn, 0, 1, 0),
	)
	mismatchedNumeric.registers = 5
	assertPrototypeSyntaxError(t, mismatchedNumeric)

	wrongBackEdge := testPrototypeBuilder(
		makeAsBx(opForPrep, 0, 1),
		makeABC(opMove, 3, 3, 0),
		makeAsBx(opForLoop, 0, -1),
		makeABC(opReturn, 0, 1, 0),
	)
	wrongBackEdge.registers = 4
	assertPrototypeSyntaxError(t, wrongBackEdge)
}

func compileLoopPrototype(t *testing.T, source string) *Prototype {
	t.Helper()
	prototype, syntaxError := compileSource("@loop.lua", source)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	return prototype
}

func loopDebugNames(prototype *Prototype) []string {
	if prototype.debug == nil {
		return nil
	}
	names := make([]string, len(prototype.debug.locals))
	for index, local := range prototype.debug.locals {
		names[index] = local.name.text
	}
	return names
}

func loopDebugLocal(
	prototype *Prototype,
	name string,
	occurrence int,
) *localInfo {
	if prototype.debug == nil {
		return nil
	}
	for index := range prototype.debug.locals {
		local := &prototype.debug.locals[index]
		if local.name.text != name {
			continue
		}
		if occurrence == 0 {
			return local
		}
		occurrence--
	}
	return nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
