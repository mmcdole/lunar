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
	if !ok || len(closure.upvalues) != 1 {
		t.Fatalf("closure result = %v", thread.values[0].owningValue())
	}
	if closure.upvalues[0].thread != nil || thread.openUpvalues != nil {
		t.Fatal("return left the captured local open")
	}

	thread.top = 1
	if callErr := thread.pushLuaCall(closure, 0, 0, 1); callErr != nil {
		t.Fatal(callErr)
	}
	result = execute(thread, 0)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, stateNeutralString("captured"))
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
			state.globals,
			nil,
		)
		thread := state.MainThread()
		setTestCall(thread, 0, function, test.arg)
		if callErr := thread.pushLuaCall(function, 0, 1, 1); callErr != nil {
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
	function := newLuaFunction(state.runtime, prototype, state.globals, nil)
	thread := state.MainThread()
	setTestCall(thread, 0, function)
	if callErr := thread.pushLuaCall(function, 0, 0, 1); callErr != nil {
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
		!strings.Contains(result.err.Error(), "attempt to call a number value") {
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
				"attempt to call a table value",
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

func TestExecutorPartialFailurePreservesSuspendedCaller(t *testing.T) {
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
	if callErr := thread.pushLuaCall(caller, 0, 0, 0); callErr != nil {
		t.Fatal(callErr)
	}
	callerFrame := thread.frames[0]
	retainedIndex := int(callerFrame.base)
	retained := state.String("caller")
	thread.values[retainedIndex] = slotFromValue(retained)
	callBase := retainedIndex + 1
	thread.values[callBase] = slotFromValue(failing.Value())
	thread.values[callBase+1] = slotFromValue(Number(9))
	if callErr := thread.pushLuaCall(failing, callBase, 1, 0); callErr != nil {
		t.Fatal(callErr)
	}

	result := execute(thread, 1)
	if result.kind != executionFailed || result.err == nil {
		t.Fatalf("partial execution result = %+v; want failure", result)
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
	thread.unwindLuaCalls(0)
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
		len(getter.upvalues) != 1 ||
		len(setter.upvalues) != 1 ||
		getter.upvalues[0] != setter.upvalues[0] ||
		getter.upvalues[0].thread != nil {
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
	factory := newLuaFunction(state.runtime, prototype, state.globals, nil)

	thread, result := executeTestFunction(
		t,
		state,
		factory,
		state.String("block"),
	)
	assertExecutionReturned(t, result)
	getter, ok := thread.values[0].owningValue().Function()
	if !ok ||
		len(getter.upvalues) != 1 ||
		getter.upvalues[0].thread != nil {
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
	function := newLuaFunction(state.runtime, prototype, state.globals, nil)
	thread := state.MainThread()
	thread.reserveValues(8)
	thread.reserveFrames(1)

	run := func() {
		thread.values[0] = slotFromValue(function.Value())
		thread.values[1] = slotFromValue(Number(17))
		thread.top = 2
		if callErr := thread.pushLuaCall(function, 0, 1, 1); callErr != nil {
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
	function := newLuaFunction(state.runtime, prototype, state.globals, nil)

	benchmarkExecutorFunction(b, state, function)
	b.ReportMetric(257, "opcodes/op")
}

func BenchmarkExecutorFixedCallReturn(b *testing.B) {
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
		state.globals,
		nil,
	)
	thread := state.MainThread()
	setTestCall(thread, 0, function, arguments...)
	if callErr := thread.pushLuaCall(
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
	return newLuaFunction(state.runtime, prototype, state.globals, nil)
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
	if callErr := thread.pushLuaCall(
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
	if callErr := thread.pushLuaCall(
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
