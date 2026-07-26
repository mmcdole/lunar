package lua

import (
	"strconv"
	"strings"
	"testing"
)

func TestCompileSourceLowersIfChainAsDirectControlFlow(t *testing.T) {
	prototype := compileControlPrototype(t, `
local condition, left, right = ...
if condition then
	left = 1
elseif left < right then
	left = 2
else
	left = 3
end
return left
`)

	conditionJumps := controlConditionJumps(t, prototype)
	if len(conditionJumps) != 2 {
		t.Fatalf(
			"condition jump count = %d, want 2\n%s",
			len(conditionJumps),
			formatControlCode(prototype),
		)
	}
	firstControl := conditionJumps[0] - 1
	secondControl := conditionJumps[1] - 1
	if prototype.code[firstControl].opcode() != opTest ||
		prototype.code[secondControl].opcode() != opLessThan {
		t.Fatalf(
			"condition controls = %s, %s; want TEST, LT\n%s",
			prototype.code[firstControl].opcode(),
			prototype.code[secondControl].opcode(),
			formatControlCode(prototype),
		)
	}
	if target := controlJumpTarget(t, prototype, conditionJumps[0]); target != secondControl {
		t.Fatalf(
			"first false target = %d, want second condition at %d\n%s",
			target,
			secondControl,
			formatControlCode(prototype),
		)
	}

	conditionSet := map[int]bool{
		conditionJumps[0]: true,
		conditionJumps[1]: true,
	}
	var escapes []int
	for pc, code := range prototype.code {
		switch code.opcode() {
		case opLoadBool, opTestSet:
			t.Fatalf(
				"control-only condition emitted %s at pc %d\n%s",
				code.opcode(),
				pc,
				formatControlCode(prototype),
			)
		case opJump:
			if !conditionSet[pc] {
				escapes = append(escapes, pc)
			}
		}
	}
	if len(escapes) != 2 {
		t.Fatalf(
			"arm escape count = %d, want 2\n%s",
			len(escapes),
			formatControlCode(prototype),
		)
	}
	end := controlJumpTarget(t, prototype, escapes[0])
	if other := controlJumpTarget(t, prototype, escapes[1]); other != end {
		t.Fatalf(
			"arm escapes target %d and %d\n%s",
			end,
			other,
			formatControlCode(prototype),
		)
	}
	if escapes[0]+1 != secondControl {
		t.Fatalf(
			"first arm escape is not immediately before elseif\n%s",
			formatControlCode(prototype),
		)
	}
	if target := controlJumpTarget(t, prototype, conditionJumps[1]); target != escapes[1]+1 {
		t.Fatalf(
			"elseif false target = %d, want else at %d\n%s",
			target,
			escapes[1]+1,
			formatControlCode(prototype),
		)
	}
	if end >= len(prototype.code) ||
		prototype.code[end].opcode() != opReturn {
		t.Fatalf(
			"if merge target = %d, want following RETURN\n%s",
			end,
			formatControlCode(prototype),
		)
	}

	withoutElse := compileControlPrototype(t, `
local first, second = ...
if first then
	second = 1
elseif second then
	second = 2
end
return second
`)
	conditionJumps = controlConditionJumps(t, withoutElse)
	conditionSet = make(map[int]bool, len(conditionJumps))
	for _, pc := range conditionJumps {
		conditionSet[pc] = true
	}
	escapes = escapes[:0]
	for pc, code := range withoutElse.code {
		if code.opcode() == opJump && !conditionSet[pc] {
			escapes = append(escapes, pc)
		}
	}
	if len(conditionJumps) != 2 || len(escapes) != 1 {
		t.Fatalf(
			"if/elseif has %d condition jumps and %d escapes, want 2 and 1\n%s",
			len(conditionJumps),
			len(escapes),
			formatControlCode(withoutElse),
		)
	}
	merge := controlJumpTarget(t, withoutElse, escapes[0])
	if finalFalse := controlJumpTarget(
		t,
		withoutElse,
		conditionJumps[1],
	); finalFalse != merge {
		t.Fatalf(
			"final false target = %d, escape target = %d\n%s",
			finalFalse,
			merge,
			formatControlCode(withoutElse),
		)
	}
}

func TestCompileSourceUsesSingleResultConditionsWithoutBoxing(t *testing.T) {
	prototype := compileControlPrototype(t, `
local left, right, object, key = ...
if left then end
if object[key] then end
if predicate() then end
if ... then end
if left < right then end
if not (left < right) then end
if not left then end
if not predicate then end
if not predicate() then end
if not (left + right) then end
if not object[key] then end
if left and right or key then end
`)

	tests := 0
	comparisons := 0
	callResults := 0
	varargResults := 0
	for pc, code := range prototype.code {
		switch code.opcode() {
		case opTestSet:
			t.Fatalf(
				"statement condition retained TESTSET at pc %d\n%s",
				pc,
				formatControlCode(prototype),
			)
		case opLoadBool:
			t.Fatalf(
				"statement condition materialized a boolean at pc %d\n%s",
				pc,
				formatControlCode(prototype),
			)
		case opTest:
			tests++
			assertFollowedByJump(t, prototype, pc)
		case opEqual, opLessThan, opLessEqual:
			comparisons++
			assertFollowedByJump(t, prototype, pc)
		case opCall:
			if code.c() == 2 {
				callResults++
			}
		case opVararg:
			if code.b() == 2 {
				varargResults++
			}
		}
	}
	if tests < 6 || comparisons != 2 ||
		callResults != 2 || varargResults != 1 {
		t.Fatalf(
			"TEST=%d comparisons=%d one-result CALL=%d VARARG=%d\n%s",
			tests,
			comparisons,
			callResults,
			varargResults,
			formatControlCode(prototype),
		)
	}
	if opcodeIndex(prototype.code, opNot) >= 0 {
		t.Fatalf(
			"control-only not emitted a redundant NOT\n%s",
			formatControlCode(prototype),
		)
	}

	notCondition := compileControlPrototype(t, `
local value = ...
if not value then result = 1 end
`)
	notPC := opcodeIndex(notCondition.code, opTest)
	if notPC < 0 ||
		notCondition.code[notPC].c() != 1 ||
		notPC+1 >= len(notCondition.code) ||
		notCondition.code[notPC+1].opcode() != opJump {
		t.Fatalf(
			"not-condition did not branch on truthy input\n%s",
			formatControlCode(notCondition),
		)
	}

	valueNot := compileControlPrototype(t, `
local left, right = ...
local direct = not left
return (not left) and right, direct
`)
	if opcodeIndex(valueNot.code, opNot) < 0 ||
		opcodeIndex(valueNot.code, opLoadBool) < 0 {
		t.Fatalf(
			"value-producing not lost boolean materialization\n%s",
			formatControlCode(valueNot),
		)
	}
}

func TestCompileSourceUsesLuaConstantTruthinessInConditions(t *testing.T) {
	prototype := compileControlPrototype(t, `
if nil then result = 1 end
if false then result = 2 end
if true then result = 3 end
if 0 then result = 4 end
if -0 then result = 5 end
if "" then result = 6 end
if "value" then result = 7 end
if 1 + 2 then result = 8 end
if not false then result = 9 end
`)

	jumps := 0
	for pc, code := range prototype.code {
		switch code.opcode() {
		case opJump:
			jumps++
		case opTest, opTestSet, opLoadBool, opLoadNil:
			t.Fatalf(
				"constant condition emitted %s at pc %d\n%s",
				code.opcode(),
				pc,
				formatControlCode(prototype),
			)
		}
	}
	if jumps != 2 {
		t.Fatalf(
			"constant false jump count = %d, want 2\n%s",
			jumps,
			formatControlCode(prototype),
		)
	}

	constructed := compileControlPrototype(t, `
if {} then table_seen = true end
if function() end then closure_seen = true end
`)
	if opcodeIndex(constructed.code, opNewTable) < 0 ||
		opcodeIndex(constructed.code, opClosure) < 0 {
		t.Fatalf(
			"non-scalar truthy values were not constructed\n%s",
			formatControlCode(constructed),
		)
	}
}

func TestCompileSourcePreservesCompoundNotExitLists(t *testing.T) {
	tests := []struct {
		expression  string
		conditions  [2]int
		firstToThen bool
	}{
		{
			expression:  "not (left and right)",
			conditions:  [2]int{0, 1},
			firstToThen: true,
		},
		{
			expression: "not (left or right)",
			conditions: [2]int{1, 1},
		},
		{
			expression: "(not left) and right",
			conditions: [2]int{1, 0},
		},
		{
			expression:  "(not left) or right",
			conditions:  [2]int{0, 0},
			firstToThen: true,
		},
	}
	for _, test := range tests {
		t.Run(test.expression, func(t *testing.T) {
			prototype := compileControlPrototype(
				t,
				"local left, right = ...\nif "+
					test.expression+
					" then result = 1 else result = 2 end\n"+
					"return result",
			)
			conditions := controlConditionJumps(t, prototype)
			if len(conditions) != 2 {
				t.Fatalf(
					"condition count = %d, want 2\n%s",
					len(conditions),
					formatControlCode(prototype),
				)
			}
			for index, jumpPC := range conditions {
				control := prototype.code[jumpPC-1]
				if control.opcode() != opTest ||
					control.c() != test.conditions[index] {
					t.Fatalf(
						"condition %d = %s C:%d, want TEST C:%d\n%s",
						index,
						control.opcode(),
						control.c(),
						test.conditions[index],
						formatControlCode(prototype),
					)
				}
			}
			conditionSet := map[int]bool{
				conditions[0]: true,
				conditions[1]: true,
			}
			escape := -1
			for pc, code := range prototype.code {
				switch code.opcode() {
				case opNot, opTestSet, opLoadBool:
					t.Fatalf(
						"compound not emitted %s at pc %d\n%s",
						code.opcode(),
						pc,
						formatControlCode(prototype),
					)
				case opJump:
					if !conditionSet[pc] {
						escape = pc
					}
				}
			}
			if escape < 0 {
				t.Fatalf(
					"if/else has no arm escape\n%s",
					formatControlCode(prototype),
				)
			}
			thenTarget := conditions[1] + 1
			elseTarget := escape + 1
			firstTarget := elseTarget
			if test.firstToThen {
				firstTarget = thenTarget
			}
			if got := controlJumpTarget(
				t,
				prototype,
				conditions[0],
			); got != firstTarget {
				t.Fatalf(
					"first condition target = %d, want %d\n%s",
					got,
					firstTarget,
					formatControlCode(prototype),
				)
			}
			if got := controlJumpTarget(
				t,
				prototype,
				conditions[1],
			); got != elseTarget {
				t.Fatalf(
					"second condition target = %d, want else at %d\n%s",
					got,
					elseTarget,
					formatControlCode(prototype),
				)
			}
		})
	}
}

func TestCompileSourceClosesCapturedConditionalArmBeforeMerge(t *testing.T) {
	prototype := compileControlPrototype(t, `
local outward
local condition = ...
if condition then
	local captured = 1
	outward = function() return captured end
else
	outward = function() return 0 end
end
local reused = 2
return outward, reused
`)

	closePC := opcodeIndex(prototype.code, opClose)
	if closePC < 0 || closePC+1 >= len(prototype.code) ||
		prototype.code[closePC+1].opcode() != opJump {
		t.Fatalf(
			"captured arm is not closed immediately before its escape\n%s",
			formatControlCode(prototype),
		)
	}
	firstClosure := opcodeIndex(prototype.code, opClosure)
	if firstClosure < 0 ||
		firstClosure+1 >= len(prototype.code) ||
		prototype.code[firstClosure+1].opcode() != opMove {
		t.Fatalf(
			"captured arm has no closure binding\n%s",
			formatControlCode(prototype),
		)
	}
	binding := prototype.code[firstClosure+1]
	if prototype.code[closePC].a() != binding.b() {
		t.Fatalf(
			"CLOSE starts at R%d, captured binding uses R%d\n%s",
			prototype.code[closePC].a(),
			binding.b(),
			formatControlCode(prototype),
		)
	}

	conditionJumps := controlConditionJumps(t, prototype)
	if len(conditionJumps) != 1 {
		t.Fatalf(
			"condition jump count = %d, want 1\n%s",
			len(conditionJumps),
			formatControlCode(prototype),
		)
	}
	falseTarget := controlJumpTarget(t, prototype, conditionJumps[0])
	if falseTarget <= closePC+1 ||
		falseTarget >= len(prototype.code) ||
		prototype.code[falseTarget].opcode() != opClosure {
		t.Fatalf(
			"false arm target = %d, want else CLOSURE after CLOSE/escape\n%s",
			falseTarget,
			formatControlCode(prototype),
		)
	}

	var captured, reused *localInfo
	for index := range prototype.debug.locals {
		local := &prototype.debug.locals[index]
		switch local.name.text {
		case "captured":
			captured = local
		case "reused":
			reused = local
		}
	}
	if captured == nil || reused == nil ||
		captured.endPC != uint32(closePC) ||
		reused.startPC <= uint32(closePC+1) {
		t.Fatalf(
			"local ranges do not respect branch merge: captured=%+v reused=%+v",
			captured,
			reused,
		)
	}
}

func TestCompileSourceClosesElseAndNestedCapturedScopes(t *testing.T) {
	elseCapture := compileControlPrototype(t, `
local outward
local condition = ...
if condition then
	outward = function() return 0 end
else
	local captured = 1
	outward = function() return captured end
end
local reused = 2
return outward, reused
`)
	closePC := opcodeIndex(elseCapture.code, opClose)
	if closePC < 0 ||
		closePC+1 >= len(elseCapture.code) ||
		elseCapture.code[closePC+1].opcode() != opLoadK ||
		elseCapture.code[closePC+1].a() != elseCapture.code[closePC].a() {
		t.Fatalf(
			"else capture was not closed before register reuse\n%s",
			formatControlCode(elseCapture),
		)
	}
	conditionJumps := controlConditionJumps(t, elseCapture)
	if len(conditionJumps) != 1 {
		t.Fatalf(
			"else-capture condition count = %d, want 1\n%s",
			len(conditionJumps),
			formatControlCode(elseCapture),
		)
	}
	conditionSet := map[int]bool{conditionJumps[0]: true}
	escape := -1
	for pc, code := range elseCapture.code {
		if code.opcode() == opJump && !conditionSet[pc] {
			escape = pc
			break
		}
	}
	if escape < 0 ||
		controlJumpTarget(t, elseCapture, escape) != closePC+1 ||
		controlJumpTarget(t, elseCapture, conditionJumps[0]) >= closePC {
		t.Fatalf(
			"else capture paths do not merge around CLOSE\n%s",
			formatControlCode(elseCapture),
		)
	}

	elseifCapture := compileControlPrototype(t, `
local outward
local first, second = ...
if first then
	outward = function() return 0 end
elseif second then
	local captured = 1
	outward = function() return captured end
else
	outward = function() return 2 end
end
local reused = 3
return outward, reused
`)
	closePC = opcodeIndex(elseifCapture.code, opClose)
	conditionJumps = controlConditionJumps(t, elseifCapture)
	if closePC < 0 ||
		closePC+1 >= len(elseifCapture.code) ||
		elseifCapture.code[closePC+1].opcode() != opJump ||
		len(conditionJumps) != 2 ||
		controlJumpTarget(
			t,
			elseifCapture,
			conditionJumps[1],
		) != closePC+2 {
		t.Fatalf(
			"elseif capture does not close before escape/else\n%s",
			formatControlCode(elseifCapture),
		)
	}
	merge := controlJumpTarget(t, elseifCapture, closePC+1)
	if merge >= len(elseifCapture.code) ||
		elseifCapture.code[merge].opcode() != opLoadK ||
		elseifCapture.code[merge].a() !=
			elseifCapture.code[closePC].a() {
		t.Fatalf(
			"elseif capture register was not reused after merge\n%s",
			formatControlCode(elseifCapture),
		)
	}

	nestedCapture := compileControlPrototype(t, `
local outward
local outer, inner = ...
if outer then
	local captured = 1
	if inner then
		outward = function() return captured end
	end
end
local reused = 2
return outward, reused
`)
	closePC = -1
	closeCount := 0
	for pc, code := range nestedCapture.code {
		if code.opcode() == opClose {
			closePC = pc
			closeCount++
		}
	}
	if closeCount != 1 {
		t.Fatalf(
			"nested capture CLOSE count = %d, want 1\n%s",
			closeCount,
			formatControlCode(nestedCapture),
		)
	}
	closurePC := opcodeIndex(nestedCapture.code, opClosure)
	if closurePC < 0 ||
		closurePC+1 >= len(nestedCapture.code) ||
		nestedCapture.code[closurePC+1].opcode() != opMove ||
		nestedCapture.code[closurePC+1].b() !=
			nestedCapture.code[closePC].a() {
		t.Fatalf(
			"nested closure and outer CLOSE disagree\n%s",
			formatControlCode(nestedCapture),
		)
	}
	conditionJumps = controlConditionJumps(t, nestedCapture)
	if len(conditionJumps) != 2 ||
		controlJumpTarget(t, nestedCapture, conditionJumps[0]) != closePC+1 ||
		controlJumpTarget(t, nestedCapture, conditionJumps[1]) != closePC {
		t.Fatalf(
			"nested conditions do not converge through the owning CLOSE\n%s",
			formatControlCode(nestedCapture),
		)
	}
}

func TestCompileSourceConditionalTargetsExecutableClosureWord(t *testing.T) {
	prototype := compileControlPrototype(t, `
local captured = ...
if captured then
	result = 1
elseif function() return captured end then
	result = 2
else
	result = 3
end
`)
	closurePC := opcodeIndex(prototype.code, opClosure)
	conditionJumps := controlConditionJumps(t, prototype)
	if closurePC < 0 ||
		closurePC+1 >= len(prototype.code) ||
		prototype.code[closurePC+1].opcode() != opMove ||
		len(conditionJumps) != 2 {
		t.Fatalf(
			"upvalue-bearing elseif closure is incomplete\n%s",
			formatControlCode(prototype),
		)
	}
	if target := controlJumpTarget(t, prototype, conditionJumps[0]); target != closurePC {
		t.Fatalf(
			"prior false edge targets pc %d, want CLOSURE at %d\n%s",
			target,
			closurePC,
			formatControlCode(prototype),
		)
	}
	roles, syntaxError := classifyPrototypeWords(prototype)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if roles[closurePC] != executableWord ||
		roles[closurePC+1] != closureBindingWord {
		t.Fatalf(
			"closure word roles = %d/%d, want executable/binding",
			roles[closurePC],
			roles[closurePC+1],
		)
	}
}

func TestCompileSourceTreatsReturnsAsArmLocalControlFlow(t *testing.T) {
	prototype := compileControlPrototype(t, `
local first, second = ...
if first then
	return invoke()
elseif second then
	return 2;
else
	return 3
end
after = 4
`)

	tailPC := opcodeIndex(prototype.code, opTailCall)
	if tailPC < 0 ||
		tailPC+1 >= len(prototype.code) ||
		prototype.code[tailPC+1].opcode() != opReturn {
		t.Fatalf(
			"return call lost TAILCALL/RETURN adjacency\n%s",
			formatControlCode(prototype),
		)
	}
	conditionJumps := controlConditionJumps(t, prototype)
	jumps := 0
	for _, code := range prototype.code {
		if code.opcode() == opJump {
			jumps++
		}
	}
	if len(conditionJumps) != 2 || jumps != 2 {
		t.Fatalf(
			"returning arms emitted escape jumps: conditions=%d jumps=%d\n%s",
			len(conditionJumps),
			jumps,
			formatControlCode(prototype),
		)
	}
	if opcodeIndex(prototype.code, opSetGlobal) < 0 {
		t.Fatal("statement following terminal arms was not compiled")
	}

	for _, source := range []string{
		"if true then return 1; else return 2; end",
		"if true then return 1; elseif false then return 2; end",
		"if true then return else return end",
		"if true then return elseif false then return end",
	} {
		compileControlPrototype(t, source)
	}
	if _, syntaxError := compileSource(
		"@return.lua",
		"if true then return 1; value = 2 end",
	); syntaxError == nil ||
		!strings.Contains(syntaxError.Error(), "return must be the last") {
		t.Fatalf("statement after arm return error = %v", syntaxError)
	}
}

func TestCompileSourceKeepsConditionalScopesIndependent(t *testing.T) {
	prototype := compileControlPrototype(t, `
local first = ...
if first then
	local branch = 1
elseif branch then
	local branch = 2
else
	local branch = 3
end
return branch
`)

	branchGlobals := 0
	for _, code := range prototype.code {
		if code.opcode() != opGetGlobal {
			continue
		}
		constant := prototype.constants[code.bx()]
		if constant.kind() == StringKind &&
			stringSlotText(constant) == "branch" {
			branchGlobals++
		}
	}
	if branchGlobals != 2 {
		t.Fatalf(
			"out-of-scope branch global loads = %d, want 2\n%s",
			branchGlobals,
			formatControlCode(prototype),
		)
	}
	branchLocals := 0
	for _, local := range prototype.debug.locals {
		if local.name.text == "branch" {
			branchLocals++
		}
	}
	if branchLocals != 3 {
		t.Fatalf("branch debug local count = %d, want 3", branchLocals)
	}
}

func TestCompileSourceAttributesConditionalControlLines(t *testing.T) {
	prototype := compileControlPrototype(t, `local condition = ...
if
	condition
then
	value = 1
else
	value = 2
end
return value`)

	conditionJumps := controlConditionJumps(t, prototype)
	if len(conditionJumps) != 1 {
		t.Fatalf(
			"condition jump count = %d, want 1\n%s",
			len(conditionJumps),
			formatControlCode(prototype),
		)
	}
	controlPC := conditionJumps[0] - 1
	if prototype.LineAt(controlPC) != 3 ||
		prototype.LineAt(conditionJumps[0]) != 3 {
		t.Fatalf(
			"condition lines = %d/%d, want 3/3\n%s",
			prototype.LineAt(controlPC),
			prototype.LineAt(conditionJumps[0]),
			formatControlCode(prototype),
		)
	}

	conditionSet := map[int]bool{conditionJumps[0]: true}
	escape := -1
	for pc, code := range prototype.code {
		if code.opcode() == opJump && !conditionSet[pc] {
			escape = pc
			break
		}
	}
	if escape < 0 || prototype.LineAt(escape) != 5 {
		t.Fatalf(
			"then-arm escape line = %d, want last body line 5\n%s",
			prototype.LineAt(escape),
			formatControlCode(prototype),
		)
	}
}

func TestCompileSourceRejectsMalformedAndOverNestedConditionals(t *testing.T) {
	for _, source := range []string{
		"if then end",
		"if true end",
		"if true then",
		"elseif true then end",
		"else",
		"if true then else else end",
		"if true then else elseif true then end",
		"if true then end end",
	} {
		if _, syntaxError := compileSource(
			"@conditional.lua",
			source,
		); syntaxError == nil {
			t.Fatalf("compiler accepted malformed conditional %q", source)
		}
	}

	validDepth := maxSyntaxNesting - 1
	valid := strings.Repeat(
		"if true then ",
		validDepth,
	) + strings.Repeat("end ", validDepth)
	compileControlPrototype(t, valid)
	invalid := "if true then " + valid + " end"
	if _, syntaxError := compileSource(
		"@conditional.lua",
		invalid,
	); syntaxError == nil ||
		!strings.Contains(syntaxError.Error(), "syntax nesting") {
		t.Fatalf("over-nesting error = %v", syntaxError)
	}
}

func TestCompileSourceBuildsLongElseIfChainIteratively(t *testing.T) {
	const branches = 2000
	var source strings.Builder
	source.Grow(branches * 40)
	source.WriteString("if condition0 then result = 0 ")
	for index := 1; index < branches; index++ {
		source.WriteString("elseif condition")
		source.WriteString(strconv.Itoa(index))
		source.WriteString(" then result = ")
		source.WriteString(strconv.Itoa(index))
		source.WriteByte(' ')
	}
	source.WriteString("else result = -1 end return result")

	prototype := compileControlPrototype(t, source.String())
	conditions := controlConditionJumps(t, prototype)
	if len(conditions) != branches {
		t.Fatalf(
			"long chain condition count = %d, want %d",
			len(conditions),
			branches,
		)
	}
	if prototype.RegisterCount() > 2 {
		t.Fatalf(
			"long chain register high-water = %d, want at most 2",
			prototype.RegisterCount(),
		)
	}
	for pc, code := range prototype.code {
		if code.opcode() == opTestSet {
			t.Fatalf("long chain retained TESTSET at pc %d", pc)
		}
	}
}

func compileControlPrototype(t *testing.T, source string) *Prototype {
	t.Helper()
	prototype, syntaxError := compileSource("@control.lua", source)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	return prototype
}

func controlConditionJumps(
	t *testing.T,
	prototype *Prototype,
) []int {
	t.Helper()
	var jumps []int
	for pc := 0; pc+1 < len(prototype.code); pc++ {
		switch prototype.code[pc].opcode() {
		case opTest, opTestSet, opEqual, opLessThan, opLessEqual:
			if prototype.code[pc+1].opcode() != opJump {
				t.Fatalf(
					"%s at pc %d is not followed by JMP\n%s",
					prototype.code[pc].opcode(),
					pc,
					formatControlCode(prototype),
				)
			}
			jumps = append(jumps, pc+1)
		}
	}
	return jumps
}

func assertFollowedByJump(
	t *testing.T,
	prototype *Prototype,
	pc int,
) {
	t.Helper()
	if pc+1 >= len(prototype.code) ||
		prototype.code[pc+1].opcode() != opJump {
		t.Fatalf(
			"%s at pc %d is not followed by JMP\n%s",
			prototype.code[pc].opcode(),
			pc,
			formatControlCode(prototype),
		)
	}
}

func controlJumpTarget(
	t *testing.T,
	prototype *Prototype,
	pc int,
) int {
	t.Helper()
	if pc < 0 ||
		pc >= len(prototype.code) ||
		prototype.code[pc].opcode() != opJump {
		t.Fatalf("pc %d is not a JMP", pc)
	}
	target := pc + 1 + prototype.code[pc].sbx()
	if target < 0 || target >= len(prototype.code) {
		t.Fatalf(
			"JMP at pc %d targets invalid pc %d\n%s",
			pc,
			target,
			formatControlCode(prototype),
		)
	}
	return target
}

func formatControlCode(prototype *Prototype) string {
	var output strings.Builder
	for pc, code := range prototype.code {
		output.WriteString(strconv.Itoa(pc))
		output.WriteString(": ")
		output.WriteString(code.opcode().String())
		output.WriteString(" A=")
		output.WriteString(strconv.Itoa(code.a()))
		output.WriteString(" B=")
		output.WriteString(strconv.Itoa(code.b()))
		output.WriteString(" C=")
		output.WriteString(strconv.Itoa(code.c()))
		output.WriteString(" line=")
		output.WriteString(strconv.Itoa(prototype.LineAt(pc)))
		if code.opcode() == opJump {
			output.WriteString(" target=")
			output.WriteString(strconv.Itoa(pc + 1 + code.sbx()))
		}
		output.WriteByte('\n')
	}
	return output.String()
}
