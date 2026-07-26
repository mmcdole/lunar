package lua

import (
	"runtime"
	"strings"
	"testing"
)

func TestExecutorRunsCompiledControlFlowAndCalls(t *testing.T) {
	const source = `
local function choose(value)
	if value then
		return not false, value
	end
	return false, nil
end
return choose(...)
`
	tests := []struct {
		name string
		arg  Value
		want []Value
	}{
		{
			name: "truthy",
			arg:  Number(7),
			want: []Value{Bool(true), Number(7)},
		},
		{
			name: "false",
			arg:  Bool(false),
			want: []Value{Bool(false), Nil()},
		},
		{
			name: "nil",
			arg:  Nil(),
			want: []Value{Bool(false), Nil()},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, thread, result := executeTestChunk(
				t,
				source,
				test.arg,
			)
			defer state.Close()
			assertExecutionReturned(t, result)
			assertExecutionValues(t, thread, test.want...)
		})
	}
}

func TestExecutorPreservesExactVarargResults(t *testing.T) {
	const source = `
local function relay(...)
	return ...
end
return relay(...)
`
	state, thread, result := executeTestChunk(
		t,
		source,
		Number(1),
		Nil(),
		Nil(),
	)
	defer state.Close()
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(1), Nil(), Nil())
}

func TestExecutorHandlesFixedCallResults(t *testing.T) {
	const source = `
local function identity(...)
	return ...
end
local first, second = identity(...)
return first, second
`
	state, thread, result := executeTestChunk(
		t,
		source,
		Number(10),
		Number(20),
		Number(30),
	)
	defer state.Close()
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(10), Number(20))
}

func TestExecutorDirectFixedCallsAdjustArgumentsAndResults(t *testing.T) {
	const source = `
local function none(value)
	local copy = value
end
local function one(value)
	return value
end
local function pair(left, right)
	return right, left
end

none(1)
local padded, missing = one(2)
local truncated = pair(3, 4)
local missing_right, missing_left = pair(5)
local excess = one(6, 7)
local exact_right, exact_left = pair(8, 9)
return padded, missing, truncated, missing_right, missing_left,
	excess, exact_right, exact_left
`
	prototype, syntaxError := compileSource("@direct-calls.lua", source)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if len(prototype.children) != 3 {
		t.Fatalf(
			"direct-call fixture has %d children; want 3",
			len(prototype.children),
		)
	}
	for index, child := range prototype.children {
		if child.varargFlags&varargIsVararg != 0 {
			t.Fatalf("direct callee %d compiled as vararg", index)
		}
	}

	var calls []instruction
	for _, code := range prototype.code {
		if code.opcode() == opCall {
			calls = append(calls, code)
		}
	}
	wantShapes := [][2]int{
		{2, 1},
		{2, 3},
		{3, 2},
		{2, 3},
		{3, 2},
		{3, 3},
	}
	if len(calls) != len(wantShapes) {
		t.Fatalf(
			"direct-call fixture emitted %d CALLs; want %d",
			len(calls),
			len(wantShapes),
		)
	}
	for index, want := range wantShapes {
		if calls[index].b() != want[0] ||
			calls[index].c() != want[1] {
			t.Fatalf(
				"CALL %d = B:%d C:%d; want B:%d C:%d",
				index,
				calls[index].b(),
				calls[index].c(),
				want[0],
				want[1],
			)
		}
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	function := newLuaFunction(state.runtime, prototype, state.main.globals, nil)
	thread, result := executeTestFunction(t, state, function)
	assertExecutionReturned(t, result)
	assertExecutionValues(
		t,
		thread,
		Number(2),
		Nil(),
		Number(4),
		Nil(),
		Number(5),
		Number(6),
		Number(9),
		Number(8),
	)
}

func TestExecutorFixedCallSlowShapes(t *testing.T) {
	const source = `
local function variable(...)
	return ...
end
local function produce()
	return 2, 3
end
local function collect(first, second, third)
	return first, second, third
end
local function open_return()
	return 4, produce()
end

local variable_first = variable(1, 9)
local first, second, third = collect(1, produce())
local fourth, fifth, sixth = open_return()
return variable_first, first, second, third, fourth, fifth, sixth
`
	prototype, syntaxError := compileSource("@slow-calls.lua", source)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if len(prototype.children) != 4 {
		t.Fatalf(
			"slow-call fixture has %d children; want 4",
			len(prototype.children),
		)
	}
	if prototype.children[0].varargFlags&varargIsVararg == 0 {
		t.Fatal("variable callee did not compile as vararg")
	}
	for index := 1; index < len(prototype.children); index++ {
		if prototype.children[index].varargFlags&varargIsVararg != 0 {
			t.Fatalf("fixed callee %d compiled as vararg", index)
		}
	}

	var calls []instruction
	for _, code := range prototype.code {
		if code.opcode() == opCall {
			calls = append(calls, code)
		}
	}
	wantShapes := [][2]int{
		{3, 2},
		{1, 0},
		{0, 4},
		{1, 4},
	}
	if len(calls) != len(wantShapes) {
		t.Fatalf(
			"slow-call fixture emitted %d CALLs; want %d",
			len(calls),
			len(wantShapes),
		)
	}
	for index, want := range wantShapes {
		if calls[index].b() != want[0] ||
			calls[index].c() != want[1] {
			t.Fatalf(
				"CALL %d = B:%d C:%d; want B:%d C:%d",
				index,
				calls[index].b(),
				calls[index].c(),
				want[0],
				want[1],
			)
		}
	}
	openReturn := prototype.children[3]
	var openCall, openResult bool
	for _, code := range openReturn.code {
		switch code.opcode() {
		case opCall:
			openCall = code.c() == 0
		case opReturn:
			openResult = code.b() == 0
		}
	}
	if !openCall || !openResult {
		t.Fatal("open-return fixture did not emit open CALL and RETURN")
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	function := newLuaFunction(state.runtime, prototype, state.main.globals, nil)
	thread, result := executeTestFunction(t, state, function)
	assertExecutionReturned(t, result)
	assertExecutionValues(
		t,
		thread,
		Number(1),
		Number(1),
		Number(2),
		Number(3),
		Number(4),
		Number(2),
		Number(3),
	)
}

func TestExecutorClosesCapturedValuesAtReturn(t *testing.T) {
	const source = `
local value = ...
return function()
	return value
end
`
	state, thread, result := executeTestChunk(
		t,
		source,
		stateNeutralString("captured"),
	)
	defer state.Close()
	assertExecutionReturned(t, result)
	if thread.top != 1 {
		t.Fatalf("closure result count = %d; want 1", thread.top)
	}
	closure, ok := thread.values[0].owningValue().Function()
	if !ok || closure.UpvalueCount() != 1 {
		t.Fatalf("closure result = %v", thread.values[0].owningValue())
	}
	if testUpvalueIsOpen(testFunctionUpvalue(closure, 0)) ||
		thread.openUpvalues != nil {
		t.Fatal("return left the captured local open")
	}

	thread.top = 1
	if callErr := thread.pushFunctionCall(closure, 0, 0, 1); callErr != nil {
		t.Fatal(callErr)
	}
	result = execute(thread, 0)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, stateNeutralString("captured"))
}

func TestExecutorDirectReturnClosesUpvalues(t *testing.T) {
	const source = `local function make(value)
	local function get()
		return value
	end
	return get
end
local get = make("captured")
local result = get()
return result`
	prototype, syntaxError := compileSource("@direct-upvalue.lua", source)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if len(prototype.children) != 1 ||
		len(prototype.children[0].children) != 1 {
		t.Fatal("direct-upvalue fixture has unexpected closure structure")
	}
	makePrototype := prototype.children[0]
	getPrototype := makePrototype.children[0]
	if makePrototype.varargFlags&varargIsVararg != 0 ||
		getPrototype.varargFlags&varargIsVararg != 0 {
		t.Fatal("direct-upvalue fixture compiled a callee as vararg")
	}
	var calls []instruction
	for _, code := range prototype.code {
		if code.opcode() == opCall {
			calls = append(calls, code)
		}
	}
	if len(calls) != 2 ||
		calls[0].b() != 2 ||
		calls[0].c() != 2 ||
		calls[1].b() != 1 ||
		calls[1].c() != 2 {
		t.Fatalf("direct-upvalue CALL shapes = %#v", calls)
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	function := newLuaFunction(state.runtime, prototype, state.main.globals, nil)
	thread, result := executeTestFunction(t, state, function)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, state.String("captured"))
	if thread.openUpvalues != nil {
		t.Fatal("direct returns retained an open upvalue")
	}
}

func TestExecutorRunsTransitiveUpvalueBinding(t *testing.T) {
	const source = `
local captured = ...
return function()
	return function()
		return captured
	end
end
`
	prototype, syntaxError := compileSource("@transitive-upvalue.lua", source)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if len(prototype.children) != 1 ||
		len(prototype.children[0].children) != 1 {
		t.Fatal("compiler did not produce the expected closure chain")
	}
	middlePrototype := prototype.children[0]
	closurePC := opcodeIndex(middlePrototype.code, opClosure)
	if closurePC < 0 || closurePC+1 >= len(middlePrototype.code) {
		t.Fatal("middle function has no complete closure instruction")
	}
	binding := middlePrototype.code[closurePC+1]
	if binding.opcode() != opGetUpvalue || binding.b() != 0 {
		t.Fatalf(
			"inner closure binding = %s B:%d; want GETUPVAL 0",
			binding.opcode(),
			binding.b(),
		)
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	function := newLuaFunction(state.runtime, prototype, state.main.globals, nil)
	thread := state.MainThread()
	setTestCall(
		thread,
		0,
		function,
		stateNeutralString("grandparent"),
	)
	if callErr := thread.pushFunctionCall(function, 0, 1, 1); callErr != nil {
		t.Fatal(callErr)
	}
	result := execute(thread, 0)
	assertExecutionReturned(t, result)

	middle, ok := thread.values[0].owningValue().Function()
	if !ok {
		t.Fatalf(
			"root result = %v; want middle closure",
			thread.values[0].owningValue(),
		)
	}
	if callErr := thread.pushFunctionCall(middle, 0, 0, 1); callErr != nil {
		t.Fatal(callErr)
	}
	result = execute(thread, 0)
	assertExecutionReturned(t, result)

	inner, ok := thread.values[0].owningValue().Function()
	if !ok {
		t.Fatalf(
			"middle result = %v; want inner closure",
			thread.values[0].owningValue(),
		)
	}
	if callErr := thread.pushFunctionCall(inner, 0, 0, 1); callErr != nil {
		t.Fatal(callErr)
	}
	result = execute(thread, 0)
	assertExecutionReturned(t, result)
	assertExecutionValues(
		t,
		thread,
		stateNeutralString("grandparent"),
	)
}

func TestExecutorRunsNot(t *testing.T) {
	const source = `local value = ...; return not value`
	prototype, syntaxError := compileSource("@not.lua", source)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if opcodeIndex(prototype.code, opNot) < 0 {
		t.Fatal("value-producing not did not compile to NOT")
	}

	tests := []struct {
		name string
		arg  Value
		want Value
	}{
		{name: "truthy", arg: Number(0), want: Bool(false)},
		{name: "false", arg: Bool(false), want: Bool(true)},
		{name: "nil", arg: Nil(), want: Bool(true)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			function := newLuaFunction(
				state.runtime,
				prototype,
				state.main.globals,
				nil,
			)
			thread := state.MainThread()
			setTestCall(thread, 0, function, test.arg)
			if callErr := thread.pushFunctionCall(
				function,
				0,
				1,
				1,
			); callErr != nil {
				t.Fatal(callErr)
			}
			result := execute(thread, 0)
			assertExecutionReturned(t, result)
			assertExecutionValues(t, thread, test.want)
		})
	}
}

func TestExecutorRunsOperandValuedLogicalExpressions(t *testing.T) {
	const source = `return (...) and "yes" or "no"`
	prototype, syntaxError := compileSource("@logical.lua", source)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	hasConditionalMove := false
	for _, code := range prototype.code {
		if code.opcode() == opTest || code.opcode() == opTestSet {
			hasConditionalMove = true
			break
		}
	}
	if !hasConditionalMove {
		t.Fatal("logical expression did not exercise conditional execution")
	}

	tests := []struct {
		arg  Value
		want string
	}{
		{Bool(true), "yes"},
		{Number(0), "yes"},
		{Bool(false), "no"},
		{Nil(), "no"},
	}
	for _, test := range tests {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		function := newLuaFunction(
			state.runtime,
			prototype,
			state.main.globals,
			nil,
		)
		thread := state.MainThread()
		setTestCall(thread, 0, function, test.arg)
		if callErr := thread.pushFunctionCall(function, 0, 1, 1); callErr != nil {
			t.Fatal(callErr)
		}
		result := execute(thread, 0)
		assertExecutionReturned(t, result)
		if thread.top != 1 {
			t.Fatalf("logical result count = %d; want 1", thread.top)
		}
		text, ok := thread.values[0].owningValue().AsString()
		if !ok || text != test.want {
			t.Fatalf("logical result = (%q, %v); want %q", text, ok, test.want)
		}
		if err := state.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExecutorRunsTestSetAndPreservesDestination(t *testing.T) {
	compiled, syntaxError := compileSource(
		"@testset.lua",
		`local value = ...; return value and "replacement"`,
	)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if opcodeIndex(compiled.code, opTestSet) < 0 {
		t.Fatal("operand-valued and did not compile to TESTSET")
	}

	builder := testPrototypeBuilder(
		makeABC(opTestSet, 0, 1, 0),
		makeAsBx(opJump, 0, 0),
		makeABC(opReturn, 0, 2, 0),
	)
	builder.parameters = 2
	builder.registers = 2
	prototype, syntaxError := builder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	tests := []struct {
		name   string
		source Value
		want   Value
	}{
		{
			name:   "matching condition copies source",
			source: Bool(false),
			want:   Bool(false),
		},
		{
			name:   "nonmatching condition preserves destination",
			source: Number(7),
			want:   stateNeutralString("preserved"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			function := newLuaFunction(
				state.runtime,
				prototype,
				state.main.globals,
				nil,
			)
			thread := state.MainThread()
			setTestCall(
				thread,
				0,
				function,
				stateNeutralString("preserved"),
				test.source,
			)
			if callErr := thread.pushFunctionCall(
				function,
				0,
				2,
				1,
			); callErr != nil {
				t.Fatal(callErr)
			}
			result := execute(thread, 0)
			assertExecutionReturned(t, result)
			assertExecutionValues(t, thread, test.want)
		})
	}
}

func TestExecutorHonorsLoadBoolSkip(t *testing.T) {
	builder := testPrototypeBuilder(
		makeABC(opLoadBool, 0, 1, 1),
		makeABC(opLoadBool, 0, 0, 0),
		makeABC(opReturn, 0, 2, 0),
	)
	builder.registers = 1
	prototype, syntaxError := builder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	function := newLuaFunction(state.runtime, prototype, state.main.globals, nil)
	thread := state.MainThread()
	setTestCall(thread, 0, function)
	if callErr := thread.pushFunctionCall(function, 0, 0, 1); callErr != nil {
		t.Fatal(callErr)
	}
	result := execute(thread, 0)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Bool(true))
}

func TestExecutorFailureCapturesTraceAndUnwinds(t *testing.T) {
	const source = `
local captured = ...
local function retain()
	return captured
end
return captured()
`
	state, thread, result := executeTestChunk(t, source, Number(42))
	defer state.Close()
	if result.kind != executionFailed || result.err == nil {
		t.Fatalf("execution result = %+v; want failure", result)
	}
	if result.err.Category() != RuntimeError ||
		!strings.Contains(
			result.err.Error(),
			"attempt to call local 'captured' (a number value)",
		) {
		t.Fatalf("runtime error = %v", result.err)
	}
	traceback := result.err.Traceback()
	if len(traceback) != 1 ||
		traceback[0].Source != "@test.lua" ||
		traceback[0].Line == 0 {
		t.Fatalf("traceback = %+v", traceback)
	}
	if len(thread.frames) != 0 ||
		thread.top != 0 ||
		thread.frameExtent != 0 ||
		thread.openUpvalues != nil {
		t.Fatal("failed execution retained frames, values, or open upvalues")
	}
	for index := range thread.values {
		if thread.values[index] != (slot{}) {
			t.Fatalf("failed execution retained slot %d", index)
		}
	}
}

func TestExecutorHonorsLuaCallMetamethods(t *testing.T) {
	t.Run("table", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		handler := compileTestFunction(t, state, "@handler.lua", `return ...`)
		metatable, err := state.NewTable(0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := metatable.RawSetString("__call", handler.Value()); err != nil {
			t.Fatal(err)
		}
		target, err := state.NewTable(0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := state.SetMetatable(target.Value(), metatable); err != nil {
			t.Fatal(err)
		}
		caller := compileTestFunction(t, state, "@caller.lua", `
local target = ...
local a, b, c, d = target("first", nil, "last")
return a, b, c, d
`)

		thread, result := executeTestFunction(t, state, caller, target.Value())
		assertExecutionReturned(t, result)
		assertExecutionValues(
			t,
			thread,
			target.Value(),
			state.String("first"),
			Nil(),
			state.String("last"),
		)
	})

	t.Run("scalar", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		handler := compileTestFunction(t, state, "@handler.lua", `return ...`)
		metatable, err := state.NewTable(0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := metatable.RawSetString("__call", handler.Value()); err != nil {
			t.Fatal(err)
		}
		if err := state.SetMetatable(Number(0), metatable); err != nil {
			t.Fatal(err)
		}
		caller := compileTestFunction(t, state, "@caller.lua", `
local target = ...
return target("value")
`)

		thread, result := executeTestFunction(t, state, caller, Number(7))
		assertExecutionReturned(t, result)
		assertExecutionValues(
			t,
			thread,
			Number(7),
			state.String("value"),
		)
	})

	t.Run("direct function wins", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		direct := compileTestFunction(
			t,
			state,
			"@direct.lua",
			`return "direct"`,
		)
		trap := compileTestFunction(
			t,
			state,
			"@trap.lua",
			`return "metamethod"`,
		)
		metatable, err := state.NewTable(0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := metatable.RawSetString("__call", trap.Value()); err != nil {
			t.Fatal(err)
		}
		if err := state.SetMetatable(direct.Value(), metatable); err != nil {
			t.Fatal(err)
		}
		caller := compileTestFunction(t, state, "@caller.lua", `
local target = ...
return target()
`)

		thread, result := executeTestFunction(t, state, caller, direct.Value())
		assertExecutionReturned(t, result)
		assertExecutionValues(t, thread, state.String("direct"))
	})

	t.Run("nonfunction metamethod is not called recursively", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		handler := compileTestFunction(t, state, "@handler.lua", `return true`)
		numberMetatable, err := state.NewTable(0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := numberMetatable.RawSetString("__call", handler.Value()); err != nil {
			t.Fatal(err)
		}
		if err := state.SetMetatable(Number(0), numberMetatable); err != nil {
			t.Fatal(err)
		}
		targetMetatable, err := state.NewTable(0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := targetMetatable.RawSetString("__call", Number(7)); err != nil {
			t.Fatal(err)
		}
		target, err := state.NewTable(0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := state.SetMetatable(target.Value(), targetMetatable); err != nil {
			t.Fatal(err)
		}
		caller := compileTestFunction(t, state, "@caller.lua", `
local value = ...
return value()
`)

		_, result := executeTestFunction(t, state, caller, target.Value())
		if result.kind != executionFailed ||
			result.err == nil ||
			!strings.Contains(
				result.err.Error(),
				"attempt to call local 'value' (a table value)",
			) {
			t.Fatalf("nonfunction __call result = %+v", result)
		}
	})
}

func TestExecutorTracksEliminatedTailCalls(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	failing := compileTestFunction(t, state, "@failing.lua", `
local value = ...
return value()
`)
	caller := compileTestFunction(t, state, "@caller.lua", `
local target, value = ...
return target(value)
`)

	thread, result := executeTestFunction(
		t,
		state,
		caller,
		failing.Value(),
		Number(42),
	)
	if result.kind != executionFailed || result.err == nil {
		t.Fatalf("tail-call result = %+v; want failure", result)
	}
	traceback := result.err.Traceback()
	if len(traceback) != 1 ||
		traceback[0].Source != "@failing.lua" ||
		traceback[0].Line == 0 ||
		traceback[0].TailCalls != 1 {
		t.Fatalf("tail-call traceback = %+v", traceback)
	}
	if len(thread.frames) != 0 || thread.top != 0 {
		t.Fatal("tail-call failure retained execution state")
	}
}

func TestExecutorTracksCallMetamethodTailCalls(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	handler := compileTestFunction(t, state, "@handler.lua", `
local _, value = ...
return value()
`)
	metatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__call", handler.Value()); err != nil {
		t.Fatal(err)
	}
	target, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(target.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	caller := compileTestFunction(t, state, "@caller.lua", `
local target, value = ...
return target(value)
`)

	_, result := executeTestFunction(
		t,
		state,
		caller,
		target.Value(),
		Number(42),
	)
	if result.kind != executionFailed || result.err == nil {
		t.Fatalf("metamethod tail-call result = %+v; want failure", result)
	}
	traceback := result.err.Traceback()
	if len(traceback) != 1 ||
		traceback[0].Source != "@handler.lua" ||
		traceback[0].Line == 0 ||
		traceback[0].TailCalls != 1 {
		t.Fatalf("metamethod tail-call traceback = %+v", traceback)
	}
}

func TestExecutorCapturesNestedCallTraceback(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	failing := compileTestFunction(t, state, "@failing.lua", `
local value = ...
return value()
`)
	caller := compileTestFunction(t, state, "@caller.lua", `
local target, value = ...
local result = target(value)
return result
`)

	_, result := executeTestFunction(
		t,
		state,
		caller,
		failing.Value(),
		Number(42),
	)
	if result.kind != executionFailed || result.err == nil {
		t.Fatalf("nested call result = %+v; want failure", result)
	}
	traceback := result.err.Traceback()
	if len(traceback) != 2 ||
		traceback[0].Source != "@failing.lua" ||
		traceback[0].Line == 0 ||
		traceback[0].TailCalls != 0 ||
		traceback[1].Source != "@caller.lua" ||
		traceback[1].Line == 0 ||
		traceback[1].TailCalls != 0 {
		t.Fatalf("nested traceback = %+v", traceback)
	}
}

func TestExecutorDirectCallPublishesCallerPC(t *testing.T) {
	const source = `local function fail(value)
	return value()
end
local result = fail(42)
return result`
	prototype, syntaxError := compileSource("@direct-trace.lua", source)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if len(prototype.children) != 1 ||
		prototype.children[0].varargFlags&varargIsVararg != 0 {
		t.Fatal("direct-trace callee did not compile as a fixed function")
	}
	var call instruction
	for _, code := range prototype.code {
		if code.opcode() == opCall {
			call = code
			break
		}
	}
	if call.opcode() != opCall || call.b() != 2 || call.c() != 2 {
		t.Fatalf(
			"direct-trace CALL = B:%d C:%d; want B:2 C:2",
			call.b(),
			call.c(),
		)
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	function := newLuaFunction(state.runtime, prototype, state.main.globals, nil)
	thread, result := executeTestFunction(t, state, function)
	if result.kind != executionFailed || result.err == nil {
		t.Fatalf("direct-trace execution = %+v; want failure", result)
	}
	traceback := result.err.Traceback()
	if len(traceback) != 2 ||
		traceback[0].Source != "@direct-trace.lua" ||
		traceback[0].Line != 2 ||
		traceback[0].TailCalls != 0 ||
		traceback[1].Source != "@direct-trace.lua" ||
		traceback[1].Line != 4 ||
		traceback[1].TailCalls != 0 {
		t.Fatalf("direct-trace traceback = %+v", traceback)
	}
	if len(thread.frames) != 0 ||
		thread.top != 0 ||
		thread.frameExtent != 0 {
		t.Fatal("direct-trace failure retained execution state")
	}
}

func TestExecutionFailureFinalizationPreservesSuspendedCaller(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	thread := state.MainThread()
	caller := newTestLuaFunction(t, state, 0, 4, 0, 0)
	failing := compileTestFunction(t, state, "@failing.lua", `
local value = ...
return value()
`)

	setTestCall(thread, 0, caller)
	if callErr := thread.pushFunctionCall(caller, 0, 0, 0); callErr != nil {
		t.Fatal(callErr)
	}
	callerFrame := thread.frames[0]
	retainedIndex := int(callerFrame.base)
	retained := state.String("caller")
	thread.values[retainedIndex] = slotFromValue(retained)
	callBase := retainedIndex + 1
	thread.values[callBase] = slotFromValue(failing.Value())
	thread.values[callBase+1] = slotFromValue(Number(9))
	if callErr := thread.pushFunctionCall(failing, callBase, 1, 0); callErr != nil {
		t.Fatal(callErr)
	}

	result := driveExecution(thread, 1)
	if result.kind != executionFailed || result.err == nil {
		t.Fatalf("partial execution result = %+v; want failure", result)
	}
	if len(thread.frames) != 2 ||
		thread.frames[0].function != caller ||
		thread.frames[1].function != failing {
		t.Fatalf("live failure left frames = %#v", thread.frames)
	}
	if len(result.err.traceback) != 0 {
		t.Fatal("execution driver eagerly captured a caught traceback")
	}

	finalizeExecutionFailure(thread, 1, result.err)
	if len(result.err.traceback) != 1 ||
		result.err.traceback[0].Source != "@failing.lua" {
		t.Fatalf("finalized traceback = %#v", result.err.traceback)
	}
	if len(thread.frames) != 1 ||
		thread.frames[0].function != caller ||
		thread.top != thread.frameExtent {
		t.Fatalf(
			"partial failure left %d frames, top %d, extent %d",
			len(thread.frames),
			thread.top,
			thread.frameExtent,
		)
	}
	assertTestSlot(t, thread.values[retainedIndex], retained)
	thread.unwindCalls(0)
}

func TestExecutorDirectReturnHonorsStopDepth(t *testing.T) {
	const source = `local value = ...
local function identity(item)
	return item
end
local result = identity(value)
return result`
	prototype, syntaxError := compileSource("@partial-return.lua", source)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if len(prototype.children) != 1 ||
		prototype.children[0].varargFlags&varargIsVararg != 0 {
		t.Fatal("partial-return helper did not compile as a fixed function")
	}
	var call instruction
	for _, code := range prototype.code {
		if code.opcode() == opCall {
			call = code
			break
		}
	}
	if call.opcode() != opCall || call.b() != 2 || call.c() != 2 {
		t.Fatalf(
			"partial-return CALL = B:%d C:%d; want B:2 C:2",
			call.b(),
			call.c(),
		)
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	thread := state.MainThread()
	caller := newTestLuaFunction(t, state, 0, 6, 0, 0)
	child := newLuaFunction(state.runtime, prototype, state.main.globals, nil)

	setTestCall(thread, 0, caller)
	if callErr := thread.pushFunctionCall(caller, 0, 0, 0); callErr != nil {
		t.Fatal(callErr)
	}
	callerFrame := thread.frames[0]
	retainedIndex := int(callerFrame.base)
	retained := state.String("suspended caller")
	thread.values[retainedIndex] = slotFromValue(retained)
	callBase := int(callerFrame.base) + 1
	thread.values[callBase] = slotFromValue(child.Value())
	thread.values[callBase+1] = numberSlot(42)
	if callErr := thread.pushFunctionCall(child, callBase, 1, 1); callErr != nil {
		t.Fatal(callErr)
	}

	result := execute(thread, 1)
	assertExecutionReturned(t, result)
	if len(thread.frames) != 1 ||
		thread.frames[0] != callerFrame ||
		thread.top != int(callerFrame.base)+
			int(caller.prototype.registers) ||
		thread.frameExtent != thread.top ||
		len(thread.continuations) != 0 {
		t.Fatalf(
			"partial return left %d frames, top %d, extent %d, %d continuations",
			len(thread.frames),
			thread.top,
			thread.frameExtent,
			len(thread.continuations),
		)
	}
	assertTestSlot(t, thread.values[callBase], Number(42))
	assertTestSlot(t, thread.values[retainedIndex], retained)
	thread.unwindCalls(0)
}

func TestExecutorSharesAndClosesCapturedUpvalues(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	factory := compileTestFunction(t, state, "@factory.lua", `
local value = ...
local function get()
	return value
end
local function set(next)
	value = next
end
return get, set
`)

	thread, result := executeTestFunction(
		t,
		state,
		factory,
		state.String("before"),
	)
	assertExecutionReturned(t, result)
	getter, getOK := thread.values[0].owningValue().Function()
	setter, setOK := thread.values[1].owningValue().Function()
	if !getOK ||
		!setOK ||
		getter.UpvalueCount() != 1 ||
		setter.UpvalueCount() != 1 ||
		testFunctionUpvalue(getter, 0) != testFunctionUpvalue(setter, 0) ||
		testUpvalueIsOpen(testFunctionUpvalue(getter, 0)) {
		t.Fatal("sibling closures do not share one closed upvalue")
	}

	runtime.GC()
	thread, result = executeTestFunction(t, state, getter)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, state.String("before"))
	thread, result = executeTestFunction(t, state, setter, Number(99))
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread)
	thread, result = executeTestFunction(t, state, getter)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(99))
}

func TestExecutorRecursiveClosureRetainsSelfAcrossGC(t *testing.T) {
	state, thread, result := executeTestChunk(t, `
local function recurse()
	return recurse
end
return recurse
`)
	defer state.Close()
	assertExecutionReturned(t, result)
	function, ok := thread.values[0].owningValue().Function()
	if !ok || function.UpvalueCount() != 1 {
		t.Fatal("recursive closure did not capture itself")
	}
	if testUpvalueIsOpen(testFunctionUpvalue(function, 0)) {
		t.Fatal("returned recursive closure left its upvalue open")
	}

	runtime.GC()
	thread, result = executeTestFunction(t, state, function)
	assertExecutionReturned(t, result)
	returned, ok := thread.values[0].owningValue().Function()
	if !ok || returned != function {
		t.Fatal("recursive closure lost its identity across collection")
	}
}

func TestExecutorClosesCapturedBlockBeforeRegisterReuse(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	prototype, syntaxError := compileSource("@block.lua", `
local getter
do
	local value = ...
	getter = function()
		return value
	end
end
return getter
`)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	hasClose := false
	for _, code := range prototype.code {
		if code.opcode() == opClose {
			hasClose = true
			break
		}
	}
	if !hasClose {
		t.Fatal("captured block did not exercise CLOSE")
	}
	factory := newLuaFunction(state.runtime, prototype, state.main.globals, nil)

	thread, result := executeTestFunction(
		t,
		state,
		factory,
		state.String("block"),
	)
	assertExecutionReturned(t, result)
	getter, ok := thread.values[0].owningValue().Function()
	if !ok ||
		getter.UpvalueCount() != 1 ||
		testUpvalueIsOpen(testFunctionUpvalue(getter, 0)) {
		t.Fatal("block closure retained an open stack value")
	}
	thread, result = executeTestFunction(t, state, getter)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, state.String("block"))
}

func TestExecutorWarmScalarReturnDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	builder := testPrototypeBuilder(
		makeABC(opReturn, 0, 2, 0),
	)
	builder.parameters = 1
	builder.registers = 2
	prototype, syntaxError := builder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	function := newLuaFunction(state.runtime, prototype, state.main.globals, nil)
	thread := state.MainThread()
	thread.reserveValues(8)
	thread.reserveFrames(1)

	run := func() {
		thread.values[0] = slotFromValue(function.Value())
		thread.values[1] = slotFromValue(Number(17))
		thread.top = 2
		if callErr := thread.pushFunctionCall(function, 0, 1, 1); callErr != nil {
			panic(callErr)
		}
		result := execute(thread, 0)
		if result.kind != executionReturned || thread.top != 1 {
			panic("unexpected scalar execution result")
		}
	}
	run()
	if allocations := testing.AllocsPerRun(1000, run); allocations != 0 {
		t.Fatalf("warm scalar executor allocations = %v; want 0", allocations)
	}
}

func TestExecutorWarmNestedFixedCallsDoNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	const source = `local function increment(value)
	return value + 1
end
return function(value)
	local result = increment(value)
	return result
end`
	prototype, syntaxError := compileSource("@warm-calls.lua", source)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if len(prototype.children) != 2 {
		t.Fatalf(
			"warm-call fixture has %d children; want 2",
			len(prototype.children),
		)
	}
	increment := prototype.children[0]
	kernelPrototype := prototype.children[1]
	if increment.varargFlags&varargIsVararg != 0 ||
		kernelPrototype.varargFlags&varargIsVararg != 0 {
		t.Fatal("warm-call fixture compiled a callee as vararg")
	}
	var call instruction
	for _, code := range kernelPrototype.code {
		if code.opcode() == opCall {
			call = code
			break
		}
	}
	if call.opcode() != opCall || call.b() != 2 || call.c() != 2 {
		t.Fatalf(
			"warm nested CALL = B:%d C:%d; want B:2 C:2",
			call.b(),
			call.c(),
		)
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	initializer := newLuaFunction(
		state.runtime,
		prototype,
		state.main.globals,
		nil,
	)
	thread, result := executeTestFunction(t, state, initializer)
	assertExecutionReturned(t, result)
	if thread.top != 1 {
		t.Fatalf("warm-call initializer returned %d values; want 1", thread.top)
	}
	kernel, ok := thread.values[0].owningValue().Function()
	if !ok {
		t.Fatal("warm-call initializer did not return a function")
	}

	thread.reserveValues(32)
	thread.reserveFrames(8)
	arguments := []slot{numberSlot(41)}
	run := func() {
		benchmarkRunExecutor(thread, kernel, arguments)
		number, ok := slotToNumber(thread.values[0])
		if !ok || number != 42 {
			panic("unexpected nested call result")
		}
	}
	run()
	if allocations := testing.AllocsPerRun(1000, run); allocations != 0 {
		t.Fatalf("warm nested call allocations = %v; want 0", allocations)
	}
}

func BenchmarkExecutorDispatch256Moves(b *testing.B) {
	code := make([]instruction, 0, 257)
	for range 256 {
		code = append(code, makeABC(opMove, 0, 0, 0))
	}
	code = append(code, makeABC(opReturn, 0, 2, 0))
	builder := testPrototypeBuilder(code...)
	builder.registers = 1
	prototype, syntaxError := builder.seal()
	if syntaxError != nil {
		b.Fatal(syntaxError)
	}
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	function := newLuaFunction(state.runtime, prototype, state.main.globals, nil)

	benchmarkExecutorFunction(b, state, function)
	b.ReportMetric(257, "opcodes/op")
}

func BenchmarkExecutorVarargCallReturn(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	callee := compileTestFunction(b, state, "@callee.lua", `return ...`)
	caller := compileTestFunction(b, state, "@caller.lua", `
local target, value = ...
local result = target(value)
return result
`)

	benchmarkExecutorFunction(
		b,
		state,
		caller,
		callee.Value(),
		Number(17),
	)
}

func BenchmarkExecutorLuaCallMatrix(b *testing.B) {
	const iterations = 1000
	for _, test := range []struct {
		name   string
		source string
	}{
		{
			name: "zero results",
			source: `
local function consume(value)
	local copy = value
end
function benchmark_kernel(iterations)
	for value = 1, iterations do
		consume(value)
	end
	return iterations
end
`,
		},
		{
			name: "one result",
			source: `
local function identity(value)
	return value
end
function benchmark_kernel(iterations)
	local value = 1
	for _ = 1, iterations do
		value = identity(value)
	end
	return value
end
`,
		},
		{
			name: "two results",
			source: `
local function swap(left, right)
	return right, left
end
function benchmark_kernel(iterations)
	local left, right = 1, 2
	for _ = 1, iterations do
		left, right = swap(left, right)
	end
	return left + right
end
`,
		},
		{
			name: "closed upvalue",
			source: `
local captured = 17
local function read()
	return captured
end
function benchmark_kernel(iterations)
	local value
	for _ = 1, iterations do
		value = read()
	end
	return value
end
`,
		},
	} {
		b.Run(test.name, func(b *testing.B) {
			benchmarkExecutorSource(
				b,
				"@call-matrix.lua",
				test.source,
				iterations,
				"lua-calls/op",
				Number(iterations),
			)
		})
	}
}

func BenchmarkExecutorUpvalueLoop(b *testing.B) {
	const iterations = 1000
	for _, test := range []struct {
		name   string
		source string
	}{
		{
			name: "closed read",
			source: `
local captured = 17
function benchmark_kernel(iterations)
	local value
	for _ = 1, iterations do
		value = captured
	end
	return value
end
`,
		},
		{
			name: "closed write",
			source: `
local captured = 0
function benchmark_kernel(iterations)
	for value = 1, iterations do
		captured = value
	end
	return captured
end
`,
		},
	} {
		b.Run(test.name, func(b *testing.B) {
			benchmarkExecutorSource(
				b,
				"@upvalue-loop.lua",
				test.source,
				iterations,
				"upvalue-ops/op",
				Number(iterations),
			)
		})
	}
}

func BenchmarkExecutorClosedUpvalueCall(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	factory := compileTestFunction(b, state, "@factory.lua", `
local captured = ...
return function()
	return captured
end
	`)
	thread := state.MainThread()
	thread.reserveValues(32)
	thread.reserveFrames(8)
	benchmarkRunExecutor(
		thread,
		factory,
		[]slot{slotFromValue(Number(17))},
	)
	closure, ok := thread.values[0].owningValue().Function()
	if !ok {
		b.Fatal("factory did not return a closure")
	}

	benchmarkExecutorFunction(b, state, closure)
}

func executeTestChunk(
	t *testing.T,
	source string,
	arguments ...Value,
) (*State, *Thread, executionResult) {
	t.Helper()
	prototype, syntaxError := compileSource("@test.lua", source)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	function := newLuaFunction(
		state.runtime,
		prototype,
		state.main.globals,
		nil,
	)
	thread := state.MainThread()
	setTestCall(thread, 0, function, arguments...)
	if callErr := thread.pushFunctionCall(
		function,
		0,
		len(arguments),
		allResults,
	); callErr != nil {
		state.Close()
		t.Fatal(callErr)
	}
	return state, thread, execute(thread, 0)
}

func compileTestFunction(
	t testing.TB,
	state *State,
	sourceName string,
	source string,
) *Function {
	t.Helper()
	prototype, syntaxError := compileSource(sourceName, source)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	return newLuaFunction(state.runtime, prototype, state.main.globals, nil)
}

func executeTestFunction(
	t testing.TB,
	state *State,
	function *Function,
	arguments ...Value,
) (*Thread, executionResult) {
	t.Helper()
	thread := state.MainThread()
	if len(thread.frames) != 0 {
		t.Fatal("test thread still has active calls")
	}
	oldExtent := thread.liveValueExtent()
	thread.top = 0
	thread.frameExtent = 0
	thread.clearInactive(0, oldExtent)
	setTestCall(thread, 0, function, arguments...)
	if callErr := thread.pushFunctionCall(
		function,
		0,
		len(arguments),
		allResults,
	); callErr != nil {
		t.Fatal(callErr)
	}
	return thread, execute(thread, 0)
}

func benchmarkExecutorFunction(
	b *testing.B,
	state *State,
	function *Function,
	arguments ...Value,
) {
	b.Helper()
	argumentSlots := make([]slot, len(arguments))
	for index, argument := range arguments {
		argumentSlots[index] = slotFromValue(argument)
	}
	thread := state.MainThread()
	required := 1 + len(argumentSlots)
	thread.reserveValues(max(required, 32))
	thread.reserveFrames(8)
	benchmarkRunExecutor(thread, function, argumentSlots)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkRunExecutor(thread, function, argumentSlots)
	}
}

func benchmarkExecutorSource(
	b *testing.B,
	name string,
	source string,
	operations int,
	metric string,
	arguments ...Value,
) {
	b.Helper()
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	initializer := compileTestFunction(b, state, name, source)
	thread, result := executeTestFunction(b, state, initializer)
	if result.kind != executionReturned ||
		result.err != nil ||
		thread.top != 0 {
		b.Fatalf("benchmark initialization = %+v", result)
	}
	value, valueErr := state.Global("benchmark_kernel")
	if valueErr != nil {
		b.Fatal(valueErr)
	}
	function, ok := value.Function()
	if !ok {
		b.Fatal("benchmark did not publish benchmark_kernel")
	}
	b.ReportMetric(float64(operations), metric)
	benchmarkExecutorFunction(b, state, function, arguments...)
}

func benchmarkRunExecutor(
	thread *Thread,
	function *Function,
	arguments []slot,
) {
	oldExtent := thread.liveValueExtent()
	thread.top = 0
	thread.frameExtent = 0
	thread.clearInactive(0, oldExtent)
	thread.values[0] = slotFromValue(function.Value())
	copy(thread.values[1:], arguments)
	thread.top = 1 + len(arguments)
	if callErr := thread.pushFunctionCall(
		function,
		0,
		len(arguments),
		allResults,
	); callErr != nil {
		panic(callErr)
	}
	result := execute(thread, 0)
	if result.kind != executionReturned ||
		result.err != nil ||
		thread.top != 1 {
		panic("unexpected benchmark execution result")
	}
}

func assertExecutionReturned(t *testing.T, result executionResult) {
	t.Helper()
	if result.kind != executionReturned || result.err != nil {
		t.Fatalf("execution result = %+v; want return", result)
	}
}

func assertExecutionValues(t *testing.T, thread *Thread, expected ...Value) {
	t.Helper()
	if thread.top != len(expected) {
		t.Fatalf("result count = %d; want %d", thread.top, len(expected))
	}
	for index, value := range expected {
		assertTestSlot(t, thread.values[index], value)
	}
}

func stateNeutralString(text string) Value {
	return stringValue(newLuaString(text))
}
