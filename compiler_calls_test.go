package lua

import (
	"strconv"
	"strings"
	"testing"
)

func TestCompileSourceUsesContiguousCallWindow(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@calls.lua",
		"local result = function_value(1, 2)\nreturn result",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var call instruction
	moves := 0
	for _, code := range prototype.code {
		switch code.opcode() {
		case opCall:
			call = code
		case opMove:
			moves++
		}
	}
	if call.opcode() != opCall ||
		call.a() != 0 ||
		call.b() != 3 ||
		call.c() != 2 {
		t.Fatalf(
			"CALL = A:%d B:%d C:%d, want A:0 B:3 C:2",
			call.a(),
			call.b(),
			call.c(),
		)
	}
	if moves != 0 {
		t.Fatalf("call window emitted %d avoidable MOVE instructions", moves)
	}
}

func TestCompileSourcePassesFinalCallAsOpenArguments(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@calls.lua",
		"return outer(1, inner())",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var inner instruction
	var outer instruction
	for _, code := range prototype.code {
		switch code.opcode() {
		case opCall:
			inner = code
		case opTailCall:
			outer = code
		}
	}
	if inner.opcode() != opCall || inner.c() != 0 {
		t.Fatalf("inner CALL C = %d, want open results", inner.c())
	}
	if outer.opcode() != opTailCall || outer.b() != 0 {
		t.Fatalf(
			"outer TAILCALL = B:%d C:%d, want open arguments",
			outer.b(),
			outer.c(),
		)
	}
}

func TestCompileSourceParenthesesCloseCallResults(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@calls.lua",
		"return outer((inner()))",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var inner instruction
	var outer instruction
	for _, code := range prototype.code {
		switch code.opcode() {
		case opCall:
			inner = code
		case opTailCall:
			outer = code
		}
	}
	if inner.opcode() != opCall || inner.c() != 2 {
		t.Fatalf("parenthesized inner CALL C = %d, want one result", inner.c())
	}
	if outer.opcode() != opTailCall || outer.b() != 2 {
		t.Fatalf(
			"outer TAILCALL B = %d, want function plus one argument",
			outer.b(),
		)
	}
}

func TestCompileSourceLogicalExpressionsCloseMultipleResults(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@calls.lua",
		"return outer(flag and inner())",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var inner instruction
	var outer instruction
	for _, code := range prototype.code {
		switch code.opcode() {
		case opCall:
			inner = code
		case opTailCall:
			outer = code
		}
	}
	if inner.opcode() != opCall || inner.c() != 2 {
		t.Fatalf("logical inner CALL C = %d, want one result", inner.c())
	}
	if outer.opcode() != opTailCall || outer.b() != 2 {
		t.Fatalf("outer TAILCALL B = %d, want one argument", outer.b())
	}

	prototype, syntaxError = compileSource(
		"@calls.lua",
		"return outer(flag and ...)",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	var variable instruction
	outer = 0
	for _, code := range prototype.code {
		switch code.opcode() {
		case opVararg:
			variable = code
		case opTailCall:
			outer = code
		}
	}
	if variable.opcode() != opVararg || variable.b() != 2 {
		t.Fatalf("logical VARARG B = %d, want one result", variable.b())
	}
	if outer.opcode() != opTailCall || outer.b() != 2 {
		t.Fatalf("vararg outer TAILCALL B = %d, want one argument", outer.b())
	}
}

func TestCompileSourceAdjustsCallResultsForLocals(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@calls.lua",
		"local first, second, third = produce()\n"+
			"return first, second, third",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var call instruction
	for _, code := range prototype.code {
		if code.opcode() == opCall {
			call = code
		}
	}
	if call.opcode() != opCall || call.c() != 4 {
		t.Fatalf("CALL C = %d, want three results", call.c())
	}
}

func TestCompileSourceKeepsFinalReturnCallOpen(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@calls.lua",
		"return 1, produce()",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var call instruction
	var result instruction
	for _, code := range prototype.code {
		switch code.opcode() {
		case opCall:
			call = code
		case opReturn:
			result = code
		}
	}
	if call.opcode() != opCall || call.c() != 0 {
		t.Fatalf("final CALL C = %d, want open results", call.c())
	}
	if result.opcode() != opReturn || result.b() != 0 {
		t.Fatalf("RETURN B = %d, want open results", result.b())
	}
}

func TestCompileSourceClosesNonfinalCallsAndAdjustsFinalCall(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@calls.lua",
		"local first, second = left(), right()\n"+
			"return first, second",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var calls []instruction
	for _, code := range prototype.code {
		if code.opcode() == opCall {
			calls = append(calls, code)
		}
	}
	if len(calls) != 2 ||
		calls[0].c() != 2 ||
		calls[1].c() != 2 {
		t.Fatalf("CALL result operands = %#v, want one result each", calls)
	}

	prototype, syntaxError = compileSource(
		"@calls.lua",
		"local only = 1, discarded()\nreturn only",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	var discarded instruction
	for _, code := range prototype.code {
		if code.opcode() == opCall {
			discarded = code
		}
	}
	if discarded.opcode() != opCall || discarded.c() != 1 {
		t.Fatalf(
			"discarded CALL C = %d, want no results",
			discarded.c(),
		)
	}

	prototype, syntaxError = compileSource(
		"@calls.lua",
		"return produce(), 1",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	var firstCall instruction
	var result instruction
	for _, code := range prototype.code {
		switch code.opcode() {
		case opCall:
			firstCall = code
		case opReturn:
			result = code
		}
	}
	if firstCall.opcode() != opCall ||
		firstCall.c() != 2 ||
		result.b() != 3 {
		t.Fatalf(
			"nonfinal CALL/RETURN = C:%d B:%d, want C:2 B:3",
			firstCall.c(),
			result.b(),
		)
	}
}

func TestCompileSourceKeepsOnlyFinalArgumentProducerOpen(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@calls.lua",
		"return outer(first(), middle(), final())",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var inner []instruction
	var outer instruction
	for _, code := range prototype.code {
		switch code.opcode() {
		case opCall:
			inner = append(inner, code)
		case opTailCall:
			outer = code
		}
	}
	if len(inner) != 3 ||
		inner[0].c() != 2 ||
		inner[1].c() != 2 ||
		inner[2].c() != 0 {
		t.Fatalf(
			"inner CALL result operands = %#v, want 2, 2, 0",
			inner,
		)
	}
	if outer.opcode() != opTailCall || outer.b() != 0 {
		t.Fatalf("outer TAILCALL B = %d, want open arguments", outer.b())
	}

	prototype, syntaxError = compileSource(
		"@calls.lua",
		"return outer(1, ...)",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	var variable instruction
	outer = 0
	for _, code := range prototype.code {
		switch code.opcode() {
		case opVararg:
			variable = code
		case opTailCall:
			outer = code
		}
	}
	if variable.opcode() != opVararg ||
		variable.b() != 0 ||
		outer.opcode() != opTailCall ||
		outer.b() != 0 {
		t.Fatalf(
			"open VARARG/TAILCALL = B:%d B:%d, want 0 and 0",
			variable.b(),
			outer.b(),
		)
	}
}

func TestCompileSourceSupportsEmptyOpenAndChainedCalls(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@calls.lua",
		"empty()\n"+
			"object:empty()\n"+
			"return factory()()",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var calls []instruction
	var method instruction
	var tail instruction
	for _, code := range prototype.code {
		switch code.opcode() {
		case opCall:
			calls = append(calls, code)
		case opSelf:
			method = code
		case opTailCall:
			tail = code
		}
	}
	if len(calls) != 3 ||
		calls[0].b() != 1 ||
		calls[1].a() != method.a() ||
		calls[1].b() != 2 ||
		calls[2].c() != 2 ||
		tail.b() != 1 {
		t.Fatalf(
			"empty/chained calls = calls %#v, SELF %#v, TAILCALL %#v",
			calls,
			method,
			tail,
		)
	}

	prototype, syntaxError = compileSource(
		"@calls.lua",
		"return object:method(produce())",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	calls = calls[:0]
	tail = 0
	for _, code := range prototype.code {
		switch code.opcode() {
		case opCall:
			calls = append(calls, code)
		case opTailCall:
			tail = code
		}
	}
	if len(calls) != 1 ||
		calls[0].c() != 0 ||
		tail.opcode() != opTailCall ||
		tail.b() != 0 {
		t.Fatalf(
			"open method call = calls %#v, TAILCALL %#v",
			calls,
			tail,
		)
	}
}

func TestCompileSourceEmitsMethodAndStatementCalls(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@calls.lua",
		"object:method(argument)\nprint \"done\"",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var self instruction
	var calls []instruction
	for _, code := range prototype.code {
		switch code.opcode() {
		case opSelf:
			self = code
		case opCall:
			calls = append(calls, code)
		}
	}
	if self.opcode() != opSelf {
		t.Fatal("method call did not emit SELF")
	}
	if len(calls) != 2 ||
		calls[0].a() != self.a() ||
		calls[0].b() != 3 ||
		calls[0].c() != 1 ||
		calls[1].b() != 2 ||
		calls[1].c() != 1 {
		t.Fatalf("statement CALL instructions = %#v", calls)
	}
}

func TestCompileSourceProtectsMethodCallWindowFromKeySpills(t *testing.T) {
	var source strings.Builder
	source.WriteString("local object, sink = ...\n")
	for number := 0; number < 256; number++ {
		source.WriteString("sink = ")
		source.WriteString(strconv.Itoa(number))
		source.WriteByte('\n')
	}
	source.WriteString("object:method()")

	prototype, syntaxError := compileSource("@calls.lua", source.String())
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	methodIndex := -1
	for index, constant := range prototype.constants {
		if constant.kind() == StringKind &&
			(*luaString)(constant.ref).text == "method" {
			methodIndex = index
			break
		}
	}
	if methodIndex <= maxRegisterConstant {
		t.Fatalf(
			"method constant index = %d, want above RK limit %d",
			methodIndex,
			maxRegisterConstant,
		)
	}

	keyRegister := -1
	var self instruction
	var call instruction
	for _, code := range prototype.code {
		switch code.opcode() {
		case opLoadK:
			if code.bx() == methodIndex {
				keyRegister = code.a()
			}
		case opSelf:
			self = code
		case opCall:
			call = code
		}
	}
	if keyRegister < 0 ||
		self.opcode() != opSelf ||
		keyRegister <= self.a()+1 ||
		self.c() != keyRegister ||
		call.a() != self.a() ||
		call.b() != 2 {
		t.Fatalf(
			"method spill = key R%d, SELF A:%d B:%d C:%d, CALL A:%d B:%d",
			keyRegister,
			self.a(),
			self.b(),
			self.c(),
			call.a(),
			call.b(),
		)
	}
}

func TestCompileSourceEvaluatesMethodReceiverOnce(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@calls.lua",
		"return make_receiver():method()",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	calls := 0
	selfs := 0
	var receiverCall instruction
	var self instruction
	for _, code := range prototype.code {
		switch code.opcode() {
		case opCall:
			calls++
			receiverCall = code
		case opSelf:
			selfs++
			self = code
		}
	}
	if calls != 1 ||
		selfs != 1 ||
		receiverCall.c() != 2 ||
		self.b() != receiverCall.a() {
		t.Fatalf(
			"receiver lowering = %d CALL, %d SELF; CALL A:%d C:%d, SELF B:%d",
			calls,
			selfs,
			receiverCall.a(),
			receiverCall.c(),
			self.b(),
		)
	}
}

func TestCompileSourceIndexesCallResult(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@calls.lua",
		"return produce().field",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	var call instruction
	var get instruction
	for _, code := range prototype.code {
		switch code.opcode() {
		case opCall:
			call = code
		case opGetTable:
			get = code
		}
	}
	if call.opcode() != opCall ||
		call.c() != 2 ||
		get.opcode() != opGetTable ||
		get.b() != call.a() {
		t.Fatalf(
			"CALL/GETTABLE = call A:%d C:%d, get A:%d B:%d C:%d",
			call.a(),
			call.c(),
			get.a(),
			get.b(),
			get.c(),
		)
	}
}

func TestCompileSourceRejectsAmbiguousOrIncompleteCalls(t *testing.T) {
	for _, source := range []string{
		"function_value\n()",
		"function_value -- comment\n()",
		"function_value",
		"(function_value)",
		"return function_value(",
		"return object:method",
	} {
		if _, syntaxError := compileSource(
			"@invalid.lua",
			source,
		); syntaxError == nil || syntaxError.Category() != SyntaxError {
			t.Fatalf("%q: syntax error = %v", source, syntaxError)
		}
	}
}

func TestCompileSourceAllowsStringCallAcrossNewline(t *testing.T) {
	prototype, syntaxError := compileSource(
		"@calls.lua",
		"function_value\n\"argument\"",
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	for _, code := range prototype.code {
		if code.opcode() == opCall {
			if code.b() != 2 || code.c() != 1 {
				t.Fatalf(
					"string statement CALL = B:%d C:%d, want B:2 C:1",
					code.b(),
					code.c(),
				)
			}
			return
		}
	}
	t.Fatal("string call did not emit CALL")
}

func TestCompileSourceEnforcesCallRegisterLimit(t *testing.T) {
	callWithArguments := func(count int) string {
		var source strings.Builder
		source.WriteString("return consume(")
		for index := 0; index < count; index++ {
			if index != 0 {
				source.WriteByte(',')
			}
			source.WriteString(strconv.Itoa(index))
		}
		source.WriteByte(')')
		return source.String()
	}

	prototype, syntaxError := compileSource(
		"@calls.lua",
		callWithArguments(maxLuaRegisters-1),
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	var call instruction
	for _, code := range prototype.code {
		if code.opcode() == opTailCall {
			call = code
		}
	}
	if call.opcode() != opTailCall || call.b() != maxLuaRegisters {
		t.Fatalf(
			"maximum TAILCALL B = %d, want %d",
			call.b(),
			maxLuaRegisters,
		)
	}

	if _, syntaxError = compileSource(
		"@calls.lua",
		callWithArguments(maxLuaRegisters),
	); syntaxError == nil || syntaxError.Category() != SyntaxError {
		t.Fatalf("oversized call error = %v", syntaxError)
	}
}
