package lua

import (
	"errors"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestCompileAndLoadPrototypeAcrossStates(t *testing.T) {
	prototype, err := Compile("@shared.lua", `return marker`)
	if err != nil {
		t.Fatal(err)
	}
	if prototype.SourceName() != "@shared.lua" {
		t.Fatalf("source name = %q", prototype.SourceName())
	}

	first, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := first.SetRawGlobal("marker", Number(1)); err != nil {
		t.Fatal(err)
	}
	if err := second.SetRawGlobal("marker", Number(2)); err != nil {
		t.Fatal(err)
	}

	firstFunction, err := first.LoadPrototype(prototype)
	if err != nil {
		t.Fatal(err)
	}
	secondFunction, err := second.LoadPrototype(prototype)
	if err != nil {
		t.Fatal(err)
	}
	if firstFunction == secondFunction ||
		firstFunction.Prototype() != prototype ||
		secondFunction.Prototype() != prototype {
		t.Fatal("loading did not preserve shared prototype and distinct closure identity")
	}

	firstResults, err := first.Call(firstFunction.Value())
	if err != nil {
		t.Fatal(err)
	}
	secondResults, err := second.Call(secondFunction.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, firstResults, Number(1))
	assertTestValues(t, secondResults, Number(2))
}

func TestLoadStringDoesNotExecute(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	function, err := state.LoadString("@load.lua", `
executions = (executions or 0) + 1
return executions
`)
	if err != nil {
		t.Fatal(err)
	}
	executions, err := state.RawGlobal("executions")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, executions, Nil())

	results, err := state.Call(function.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(1))
}

func TestLoadStringAcceptsBinaryChunksAndBoundsInput(t *testing.T) {
	prototype, err := Compile("@binary-source.lua", `return marker`)
	if err != nil {
		t.Fatal(err)
	}
	dumped, err := dumpPrototype(prototype)
	if err != nil {
		t.Fatal(err)
	}

	state, err := New(Options{
		MaxLoadBytes: len(dumped) * 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.SetRawGlobal("marker", Number(42)); err != nil {
		t.Fatal(err)
	}
	function, err := state.LoadString("@binary.luac", dumped)
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(function.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(42))

	const text = "return 10"
	exact, err := New(Options{MaxLoadBytes: len(text)})
	if err != nil {
		t.Fatal(err)
	}
	defer exact.Close()
	if _, err := exact.LoadString("@exact.lua", text); err != nil {
		t.Fatalf("load at exact input limit: %v", err)
	}

	limited, err := New(Options{MaxLoadBytes: len(text) - 1})
	if err != nil {
		t.Fatal(err)
	}
	defer limited.Close()
	_, loadErr := limited.LoadString("@limited.lua", text)
	var resource *Error
	if !errors.As(loadErr, &resource) ||
		resource.Category() != ResourceError {
		t.Fatalf(
			"limited source error = %#v; want ResourceError",
			loadErr,
		)
	}

	binaryLimited, err := New(Options{MaxLoadBytes: len(dumped) - 1})
	if err != nil {
		t.Fatal(err)
	}
	defer binaryLimited.Close()
	_, loadErr = binaryLimited.LoadString("@limited.luac", dumped)
	if !errors.As(loadErr, &resource) ||
		resource.Category() != ResourceError {
		t.Fatalf(
			"limited binary error = %#v; want ResourceError",
			loadErr,
		)
	}
}

func TestLoadRejectsInvalidPrototypeAndReportsSyntax(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	for _, prototype := range []*Prototype{nil, &Prototype{}} {
		if _, loadErr := state.LoadPrototype(prototype); !errors.Is(
			loadErr,
			ErrInvalidPrototype,
		) {
			t.Fatalf("invalid prototype error = %v", loadErr)
		}
	}

	_, syntaxErr := state.LoadString("@broken.lua", `local =`)
	var luaErr *Error
	if !errors.As(syntaxErr, &luaErr) ||
		luaErr.Category() != SyntaxError ||
		luaErr.Value().Kind() != StringKind {
		t.Fatalf("syntax error = %#v", syntaxErr)
	}
	if got := luaErr.Error(); !strings.HasPrefix(got, "broken.lua") {
		t.Fatalf("syntax description = %q", got)
	}
	if message, ok := luaErr.Value().AsString(); !ok ||
		message != luaErr.Error() {
		t.Fatalf("syntax error value = %q, %v", message, ok)
	}

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if _, loadErr := state.LoadPrototype(nil); !errors.Is(loadErr, ErrClosed) {
		t.Fatalf("closed load error = %v", loadErr)
	}
	if _, loadErr := state.LoadString("@closed.lua", `return 1`); !errors.Is(
		loadErr,
		ErrClosed,
	) {
		t.Fatalf("closed source load error = %v", loadErr)
	}
}

func TestStateCallReturnsValuesAndClearsRootExecution(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	table, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	function := mustLoadString(t, state, "@values.lua", `return ...`)

	results, err := state.Call(
		function.Value(),
		Number(1),
		Nil(),
		table.Value(),
		state.String("last"),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Number(1),
		Nil(),
		table.Value(),
		state.String("last"),
	)
	assertRootThreadReady(t, state.main)

	runtime.GC()
	resultTable, ok := results[2].AsTable()
	if !ok || resultTable != table {
		t.Fatal("owning call result did not retain canonical table identity")
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	resultTable, ok = results[2].AsTable()
	if !ok || resultTable != table {
		t.Fatal("call result became invalid after state close")
	}
}

func TestStateCallOneAndCallDiscardUseLuaResultAdjustment(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	empty := mustLoadString(t, state, "@empty.lua", `return`)
	result, err := state.CallOne(empty.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, result, Nil())

	producer := mustLoadString(t, state, "@producer.lua", `
call_count = call_count + 1
return 41, 42, 43
`)
	if err := state.SetRawGlobal("call_count", Number(0)); err != nil {
		t.Fatal(err)
	}
	result, err = state.CallOne(producer.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, result, Number(41))
	if err := state.CallDiscard(producer.Value()); err != nil {
		t.Fatal(err)
	}
	calls, err := state.RawGlobal("call_count")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, calls, Number(2))
	assertRootThreadReady(t, state.main)
}

func TestStateCallUsesRootCallMetamethodLayout(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	chunk := mustLoadString(t, state, "@callable.lua", `
return function(self, ...)
	return self, ...
end
`)
	values, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	handler, ok := values[0].AsFunction()
	if !ok {
		t.Fatal("chunk did not return the call handler")
	}
	callable, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	metatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__call", handler.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(callable.Value(), metatable); err != nil {
		t.Fatal(err)
	}

	results, err := state.Call(
		callable.Value(),
		Number(7),
		state.String("value"),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		callable.Value(),
		Number(7),
		state.String("value"),
	)
}

func TestStateCallProtectsFailuresAndRemainsReusable(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	failing := mustLoadString(t, state, "@failure.lua", `
local function inner()
	return nil + 1
end
return inner()
`)
	if _, callErr := state.Call(failing.Value()); callErr == nil {
		t.Fatal("runtime failure returned nil")
	} else {
		var luaErr *Error
		if !errors.As(callErr, &luaErr) ||
			luaErr.Category() != RuntimeError {
			t.Fatalf("runtime error = %#v", callErr)
		}
		traceback := luaErr.Traceback()
		if len(traceback) != 1 || traceback[0].TailCalls != 1 {
			t.Fatalf("tail-call traceback = %#v", traceback)
		}
	}
	assertRootThreadReady(t, state.main)

	success := mustLoadString(t, state, "@success.lua", `return 42`)
	results, err := state.Call(success.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(42))

	if _, callErr := state.Call(Number(1)); callErr == nil {
		t.Fatal("calling a number returned nil")
	} else {
		var luaErr *Error
		if !errors.As(callErr, &luaErr) ||
			luaErr.Category() != RuntimeError {
			t.Fatalf("non-callable error = %#v", callErr)
		}
	}
	assertRootThreadReady(t, state.main)
}

func TestStateCallValidatesIngressBeforeMutation(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	other, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	function := mustLoadString(t, state, "@identity.lua", `return ...`)
	foreign, err := other.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	thread := state.main

	for _, test := range []struct {
		name     string
		callable Value
		args     []Value
		want     error
	}{
		{
			name:     "invalid callable",
			callable: Value{},
			want:     ErrInvalidValue,
		},
		{
			name:     "foreign callable",
			callable: foreign.Value(),
			want:     ErrForeignValue,
		},
		{
			name:     "invalid argument",
			callable: function.Value(),
			args:     []Value{{}},
			want:     ErrInvalidValue,
		},
		{
			name:     "foreign argument",
			callable: function.Value(),
			args:     []Value{foreign.Value()},
			want:     ErrForeignValue,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			beforeValues := slices.Clone(thread.values)
			beforeFrames := slices.Clone(thread.frames)
			if _, callErr := state.Call(test.callable, test.args...); !errors.Is(
				callErr,
				test.want,
			) {
				t.Fatalf("call error = %v; want %v", callErr, test.want)
			}
			if thread.status != ThreadReady ||
				thread.top != 0 ||
				thread.frameExtent != 0 ||
				!slices.Equal(thread.values, beforeValues) ||
				!slices.Equal(thread.frames, beforeFrames) {
				t.Fatal("rejected ingress changed root execution state")
			}
		})
	}

	thread.status = ThreadRunning
	if _, callErr := state.Call(function.Value()); !errors.Is(
		callErr,
		ErrRunning,
	) {
		t.Fatalf("running call error = %v", callErr)
	}
	thread.status = ThreadReady
}

func TestStateCallIntoHandlesOverlapAndShortDestination(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	identity := mustLoadString(t, state, "@identity.lua", `return ...`)

	values := []Value{Number(10), Number(20), Number(90), Number(91)}
	count, err := state.CallInto(
		identity.Value(),
		values[:2],
		values[1:3],
	)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("result count = %d; want 2", count)
	}
	assertTestValues(
		t,
		values,
		Number(10),
		Number(10),
		Number(20),
		Number(91),
	)

	producer := mustLoadString(t, state, "@producer.lua", `
side_effect = side_effect + 1
return 1, 2, 3
`)
	if err := state.SetRawGlobal("side_effect", Number(0)); err != nil {
		t.Fatal(err)
	}
	destination := make([]Value, 2, 8)
	destination[0] = Number(70)
	destination[1] = Number(71)
	count, callErr := state.CallInto(
		producer.Value(),
		nil,
		destination,
	)
	var capacityErr *ResultCapacityError
	if !errors.As(callErr, &capacityErr) ||
		capacityErr.Required != 3 ||
		capacityErr.Available != 2 ||
		count != 3 {
		t.Fatalf("capacity result = (%d, %#v)", count, callErr)
	}
	assertTestValues(
		t,
		capacityErr.Results(),
		Number(1),
		Number(2),
		Number(3),
	)
	copyOfResults := capacityErr.Results()
	copyOfResults[0] = Number(99)
	assertTestValues(
		t,
		capacityErr.Results(),
		Number(1),
		Number(2),
		Number(3),
	)
	assertTestValues(t, destination, Number(70), Number(71))
	sideEffect, err := state.RawGlobal("side_effect")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, sideEffect, Number(1))
	assertRootThreadReady(t, state.main)
}

func TestResultCapacityErrorOwnsReferenceResults(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	table, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	identity := mustLoadString(t, state, "@overflow-owner.lua", `return ...`)

	count, callErr := state.CallInto(
		identity.Value(),
		[]Value{table.Value(), Nil()},
		nil,
	)
	var capacityError *ResultCapacityError
	if !errors.As(callErr, &capacityError) || count != 2 {
		t.Fatalf("short call = (%d, %#v)", count, callErr)
	}
	if err := state.CallDiscard(identity.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	results := capacityError.Results()
	assertTestValues(t, results, table.Value(), Nil())
	resultTable, ok := results[0].AsTable()
	if !ok || resultTable != table {
		t.Fatal("overflow result lost table identity after State.Close")
	}
}

func TestStateCallIntoLeavesDestinationUntouchedOnFailure(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	failing := mustLoadString(t, state, "@failure.lua", `return nil + 1`)
	destination := []Value{Number(80), Number(81)}
	if count, callErr := state.CallInto(
		failing.Value(),
		nil,
		destination,
	); count != 0 || callErr == nil {
		t.Fatalf("failed call = (%d, %v)", count, callErr)
	}
	assertTestValues(t, destination, Number(80), Number(81))
	assertRootThreadReady(t, state.main)
}

func TestLoadPrototypeInitializesRootUpvalues(t *testing.T) {
	builder := testPrototypeBuilder(
		makeABC(opGetUpvalue, 0, 0, 0),
		makeABC(opGetUpvalue, 1, 1, 0),
		makeABC(opReturn, 0, 3, 0),
	)
	builder.upvalues = 2
	prototype, syntaxError := builder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	function, err := state.LoadPrototype(prototype)
	if err != nil {
		t.Fatal(err)
	}
	if function.UpvalueCount() != 2 {
		t.Fatalf("loaded upvalue count = %d; want 2", function.UpvalueCount())
	}
	results, err := state.Call(function.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Nil(), Nil())
}

func TestStateCallHandlesZeroResultsAndPreservesDestinationTail(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	empty := mustLoadString(t, state, "@empty.lua", `return`)
	results, err := state.Call(empty.Value())
	if err != nil {
		t.Fatal(err)
	}
	if results != nil {
		t.Fatalf("zero-result Call returned %#v; want nil", results)
	}
	destination := []Value{Number(70), Number(71)}
	count, err := state.CallInto(empty.Value(), nil, destination)
	if err != nil || count != 0 {
		t.Fatalf("zero-result CallInto = (%d, %v)", count, err)
	}
	assertTestValues(t, destination, Number(70), Number(71))

	one := mustLoadString(t, state, "@one.lua", `return 9`)
	count, err = state.CallInto(one.Value(), nil, destination)
	if err != nil || count != 1 {
		t.Fatalf("one-result CallInto = (%d, %v)", count, err)
	}
	assertTestValues(t, destination, Number(9), Number(71))
}

func TestStateCallRunsNativeRootsAndNativeCallMetamethods(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	host, err := state.NewNativeFunction(func(frame Frame) Outcome {
		number, ok := frame.Number(0)
		if !ok {
			return frame.ArgTypeError(0, NumberKind)
		}
		second, _ := frame.Argument(1)
		return frame.ReturnValues(
			Number(number+1),
			second,
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(
		host.Value(),
		Number(6),
		state.String("direct"),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(7), state.String("direct"))

	callable, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	metatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	callHandler, err := state.NewNativeFunction(func(frame Frame) Outcome {
		if _, ok := frame.Table(0); !ok {
			return frame.ArgTypeError(0, TableKind)
		}
		number, ok := frame.Number(1)
		if !ok {
			return frame.ArgTypeError(1, NumberKind)
		}
		second, _ := frame.Argument(2)
		return frame.ReturnValues(Number(number+1), second)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__call", callHandler.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(callable.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	results, err = state.Call(
		callable.Value(),
		Number(8),
		state.String("metamethod"),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(9), state.String("metamethod"))

	failing, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.RaiseString("native failure")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, callErr := state.Call(failing.Value()); callErr == nil {
		t.Fatal("native root failure returned nil")
	} else {
		var luaErr *Error
		if !errors.As(callErr, &luaErr) ||
			luaErr.Category() != RuntimeError ||
			luaErr.Error() != "native failure" {
			t.Fatalf("native root failure = %#v", callErr)
		}
	}
	assertRootThreadReady(t, state.main)
}

func TestStateCallCleansRootExecutionAfterNativePanic(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	host, err := state.NewNativeFunction(func(Frame) Outcome {
		panic("host panic")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetRawGlobal("host", host.Value()); err != nil {
		t.Fatal(err)
	}
	chunk := mustLoadString(t, state, "@panic.lua", `
local retained = "closed"
saved = function()
	return retained
end
host()
`)

	var recovered any
	func() {
		defer func() {
			recovered = recover()
		}()
		_, _ = state.Call(chunk.Value())
	}()
	if recovered != "host panic" {
		t.Fatalf("recovered panic = %#v", recovered)
	}
	assertRootThreadReady(t, state.main)

	saved, err := state.RawGlobal("saved")
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(saved)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, state.String("closed"))
}

func TestStateCallRejectsNativeReentry(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	target := mustLoadString(t, state, "@target.lua", `return 1`)
	host, err := state.NewNativeFunction(func(frame Frame) Outcome {
		if _, callErr := frame.State().Call(target.Value()); !errors.Is(
			callErr,
			ErrRunning,
		) {
			return frame.RaiseString("nested call was not rejected")
		}
		return frame.ReturnBool(true)
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(host.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true))
}

func TestStateCallPreservesTrailingNilResults(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	function := mustLoadString(
		t,
		state,
		"@trailing-nil.lua",
		`return 1, nil, 3, nil`,
	)
	results, err := state.Call(function.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(1), Nil(), Number(3), Nil())

	destination := []Value{
		Number(40),
		Number(41),
		Number(42),
		Number(43),
		Number(44),
	}
	count, err := state.CallInto(function.Value(), nil, destination)
	if err != nil || count != 4 {
		t.Fatalf("trailing-nil CallInto = (%d, %v)", count, err)
	}
	assertTestValues(
		t,
		destination,
		Number(1),
		Nil(),
		Number(3),
		Nil(),
		Number(44),
	)
}

func TestStateCallResourceFailureLeavesMainThreadReusable(t *testing.T) {
	state, err := New(Options{MaxValues: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	large := mustLoadString(t, state, "@large.lua", `return 1, 2, 3`)
	if _, callErr := state.Call(large.Value()); callErr == nil {
		t.Fatal("value limit returned nil")
	} else {
		var luaErr *Error
		if !errors.As(callErr, &luaErr) ||
			luaErr.Category() != ResourceError {
			t.Fatalf("value limit error = %#v", callErr)
		}
		if message, ok := luaErr.Value().AsString(); !ok ||
			message != luaErr.Error() {
			t.Fatalf("resource error value = %q, %v", message, ok)
		}
	}
	assertRootThreadReady(t, state.main)

	small := mustLoadString(t, state, "@small.lua", `return 1`)
	results, err := state.Call(small.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(1))
}

func assertStringLuaError(
	t *testing.T,
	got error,
	category ErrorCategory,
	message string,
	trace ...TraceFrame,
) *Error {
	t.Helper()
	var luaErr *Error
	if !errors.As(got, &luaErr) {
		t.Fatalf("error = %v; want *Error", got)
	}
	if luaErr.Category() != category || luaErr.Error() != message {
		t.Fatalf(
			"error = (%v, %q); want (%v, %q)",
			luaErr.Category(),
			luaErr.Error(),
			category,
			message,
		)
	}
	if value, ok := luaErr.Value().AsString(); !ok || value != message {
		t.Fatalf(
			"error value = (%q, %v); want %q",
			value,
			ok,
			message,
		)
	}
	actualTrace := luaErr.Traceback()
	if len(actualTrace) != len(trace) {
		t.Fatalf("traceback = %+v; want %+v", actualTrace, trace)
	}
	for index := range trace {
		if actualTrace[index].Source != trace[index].Source ||
			actualTrace[index].Line != trace[index].Line ||
			actualTrace[index].TailCalls != trace[index].TailCalls {
			t.Fatalf("traceback = %+v; want %+v", actualTrace, trace)
		}
	}
	return luaErr
}

func TestStateCallPositionsLuaResourceFailures(t *testing.T) {
	t.Run("recursive Lua call", func(t *testing.T) {
		state, err := New(Options{MaxFrames: 3})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()

		function := mustLoadString(t, state, "@resource.lua", `
local function recurse()
	return 1 + recurse()
end
return recurse()
`)
		_, callErr := state.Call(function.Value())
		const message = "resource.lua:3: stack overflow"
		assertStringLuaError(
			t,
			callErr,
			ResourceError,
			message,
			TraceFrame{Source: "@resource.lua", Line: 3},
			TraceFrame{Source: "@resource.lua", Line: 3},
			TraceFrame{
				Source:    "@resource.lua",
				Line:      3,
				TailCalls: 1,
			},
		)
		if _, repeated := state.Call(function.Value()); repeated == nil ||
			repeated.Error() != message {
			t.Fatalf("repeated recursive call error = %v", repeated)
		}
		after := mustLoadString(t, state, "@after-resource.lua", `return 42`)
		results, afterErr := state.Call(after.Value())
		if afterErr != nil {
			t.Fatal(afterErr)
		}
		assertTestValues(t, results, Number(42))
	})

	t.Run("Lua call value window", func(t *testing.T) {
		const limit = 8
		state, err := New(Options{MaxValues: limit})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()

		factory := mustLoadString(t, state, "@large-child.lua", `
return function()
	local a, b, c, d, e, f, g, h, i = 1, 2, 3, 4, 5, 6, 7, 8, 9
	return a
end
`)
		created, err := state.Call(factory.Value())
		if err != nil {
			t.Fatal(err)
		}
		if len(created) != 1 {
			t.Fatalf("large child count = %d; want 1", len(created))
		}
		child, ok := created[0].AsFunction()
		if !ok {
			t.Fatalf("large child = %v; want function", created[0])
		}
		caller := mustLoadString(t, state, "@pure-value-resource.lua", `
local child = ...
return child()
`)
		_, callErr := state.Call(caller.Value(), child.Value())
		const message = "pure-value-resource.lua:3: value stack limit of 8 exceeded"
		assertStringLuaError(
			t,
			callErr,
			ResourceError,
			message,
			TraceFrame{
				Source: "@pure-value-resource.lua",
				Line:   3,
			},
		)
	})

	t.Run("proper tail calls reuse the frame quota", func(t *testing.T) {
		state, err := New(Options{MaxFrames: 1})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()

		function := mustLoadString(t, state, "@tail-resource.lua", `
local function descend(count)
	if count == 0 then
		return count
	end
	return descend(count - 1)
end
return descend(...)
`)
		results, callErr := state.Call(function.Value(), Number(10_000))
		if callErr != nil {
			t.Fatal(callErr)
		}
		assertTestValues(t, results, Number(0))
	})

	t.Run("metamethod call", func(t *testing.T) {
		state, err := New(Options{MaxFrames: 1})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()

		handler := mustLoadString(t, state, "@handler.lua", `return 99`)
		metatable, err := state.NewTableWithCapacity(0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err = metatable.RawSetString("__index", handler.Value()); err != nil {
			t.Fatal(err)
		}
		target, err := state.NewTableWithCapacity(0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err = state.SetMetatable(target.Value(), metatable); err != nil {
			t.Fatal(err)
		}
		caller := mustLoadString(t, state, "@metamethod-resource.lua", `
local target = ...
return target.missing
`)
		_, callErr := state.Call(caller.Value(), target.Value())
		const message = "metamethod-resource.lua:3: stack overflow"
		assertStringLuaError(
			t,
			callErr,
			ResourceError,
			message,
			TraceFrame{
				Source: "@metamethod-resource.lua",
				Line:   3,
			},
		)
		assertRootThreadReady(t, state.main)
	})

	t.Run("native result called by Lua", func(t *testing.T) {
		const limit = 16
		state, err := New(Options{MaxValues: limit})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()

		values := make([]Value, limit+1)
		for index := range values {
			values[index] = Number(float64(index + 1))
		}
		native, err := state.NewNativeFunction(func(frame Frame) Outcome {
			return frame.ReturnValues(values...)
		})
		if err != nil {
			t.Fatal(err)
		}
		callableMetatable, err := state.NewTableWithCapacity(0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err = callableMetatable.RawSetString(
			"__call",
			native.Value(),
		); err != nil {
			t.Fatal(err)
		}
		callable, err := state.NewTableWithCapacity(0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err = state.SetMetatable(
			callable.Value(),
			callableMetatable,
		); err != nil {
			t.Fatal(err)
		}
		tests := []struct {
			name       string
			sourceName string
			source     string
			argument   Value
			trace      []TraceFrame
		}{
			{
				name:       "ordinary call",
				sourceName: "@native-resource.lua",
				source: `
local callback = ...
local results = {callback()}
return results
`,
				trace: []TraceFrame{
					{Source: "=[Go]"},
					{Source: "@native-resource.lua", Line: 3},
				},
			},
			{
				name:       "tail call",
				sourceName: "@tail-native-resource.lua",
				source: `
local function forward(callback)
	return callback()
end
local results = {forward(...)}
return results
`,
				trace: []TraceFrame{
					{Source: "=[Go]"},
					{Source: "@tail-native-resource.lua", Line: 3},
					{Source: "@tail-native-resource.lua", Line: 5},
				},
			},
			{
				name:       "tail call through __call",
				sourceName: "@tail-callable-resource.lua",
				source: `
local function forward(callback)
	return callback()
end
local results = {forward(...)}
return results
`,
				argument: callable.Value(),
				trace: []TraceFrame{
					{Source: "=[Go]"},
					{Source: "@tail-callable-resource.lua", Line: 3},
					{Source: "@tail-callable-resource.lua", Line: 5},
				},
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				function := mustLoadString(
					t,
					state,
					test.sourceName,
					test.source,
				)
				argument := test.argument
				if !argument.Valid() {
					argument = native.Value()
				}
				_, callErr := state.Call(
					function.Value(),
					argument,
				)
				message := strings.TrimPrefix(test.sourceName, "@") +
					":3: value stack limit of 16 exceeded"
				assertStringLuaError(
					t,
					callErr,
					ResourceError,
					message,
					test.trace...,
				)
			})
		}
	})

	t.Run("root native call has no Lua source", func(t *testing.T) {
		state, err := New(Options{MaxValues: 3})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()

		native, err := state.NewNativeFunction(func(frame Frame) Outcome {
			return frame.ReturnValues(
				Number(1),
				Number(2),
				Number(3),
				Number(4),
			)
		})
		if err != nil {
			t.Fatal(err)
		}
		_, ingressErr := state.Call(
			native.Value(),
			Number(1),
			Number(2),
			Number(3),
		)
		const message = "value stack limit of 3 exceeded"
		assertStringLuaError(
			t,
			ingressErr,
			ResourceError,
			message,
		)
		_, callErr := state.Call(native.Value())
		assertStringLuaError(
			t,
			callErr,
			ResourceError,
			message,
			TraceFrame{Source: "=[Go]"},
		)
	})
}

func TestStateCallRejectsClosedState(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	function := mustLoadString(t, state, "@closed.lua", `return 1`)
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if _, callErr := state.Call(function.Value()); !errors.Is(
		callErr,
		ErrClosed,
	) {
		t.Fatalf("closed Call error = %v", callErr)
	}
	destination := []Value{Number(9)}
	if count, callErr := state.CallInto(
		function.Value(),
		nil,
		destination,
	); count != 0 || !errors.Is(callErr, ErrClosed) {
		t.Fatalf("closed CallInto = (%d, %v)", count, callErr)
	}
	assertTestValues(t, destination, Number(9))
}

func TestStateCallIntoWarmFixedCallDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	function := mustLoadString(t, state, "@identity.lua", `return ...`)
	arguments := []Value{Number(12)}
	results := make([]Value, 1)
	if _, err := state.CallInto(
		function.Value(),
		arguments,
		results,
	); err != nil {
		t.Fatal(err)
	}

	allocations := testing.AllocsPerRun(1000, func() {
		if _, callErr := state.CallInto(
			function.Value(),
			arguments,
			results,
		); callErr != nil {
			panic(callErr)
		}
	})
	if allocations != 0 {
		t.Fatalf("warm CallInto allocations = %v; want 0", allocations)
	}

	native, err := state.NewNativeFunction(func(frame Frame) Outcome {
		number, ok := frame.Number(0)
		if !ok {
			return frame.ArgTypeError(0, NumberKind)
		}
		return frame.ReturnNumber(number)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.CallInto(
		native.Value(),
		arguments,
		results,
	); err != nil {
		t.Fatal(err)
	}
	allocations = testing.AllocsPerRun(1000, func() {
		if _, callErr := state.CallInto(
			native.Value(),
			arguments,
			results,
		); callErr != nil {
			panic(callErr)
		}
	})
	if allocations != 0 {
		t.Fatalf("warm native CallInto allocations = %v; want 0", allocations)
	}
}

func BenchmarkStateCallBoundary(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	function := mustLoadString(b, state, "@boundary.lua", `return ...`)
	arguments := []Value{Number(12)}
	results := make([]Value, 1)
	if _, err := state.CallInto(
		function.Value(),
		arguments,
		results,
	); err != nil {
		b.Fatal(err)
	}
	native, err := state.NewNativeFunction(func(frame Frame) Outcome {
		number, ok := frame.Number(0)
		if !ok {
			return frame.ArgTypeError(0, NumberKind)
		}
		return frame.ReturnNumber(number)
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Run("CallInto one result", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, callErr := state.CallInto(
				function.Value(),
				arguments,
				results,
			); callErr != nil {
				b.Fatal(callErr)
			}
		}
	})
	b.Run("Call one result", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, callErr := state.Call(
				function.Value(),
				Number(12),
			); callErr != nil {
				b.Fatal(callErr)
			}
		}
	})
	b.Run("CallInto native one result", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, callErr := state.CallInto(
				native.Value(),
				arguments,
				results,
			); callErr != nil {
				b.Fatal(callErr)
			}
		}
	})
}

func mustLoadString(
	t testing.TB,
	state *State,
	sourceName string,
	source string,
) *Function {
	t.Helper()
	function, err := state.LoadString(sourceName, source)
	if err != nil {
		t.Fatal(err)
	}
	return function
}

func assertRootThreadReady(t *testing.T, thread *threadObject) {
	t.Helper()
	if thread.status != ThreadReady ||
		thread.top != 0 ||
		thread.frameExtent != 0 ||
		len(thread.frames) != 0 ||
		len(thread.continuations) != 0 ||
		thread.openUpvalues != nil ||
		thread.activeNativeToken != 0 ||
		thread.nativeCallDepth != 0 ||
		thread.errorHandlerDepth != 0 {
		t.Fatalf(
			"root thread retained execution state: status=%v top=%d extent=%d "+
				"frames=%d continuations=%d upvalues=%p token=%d native=%d handler=%d",
			thread.status,
			thread.top,
			thread.frameExtent,
			len(thread.frames),
			len(thread.continuations),
			thread.openUpvalues,
			thread.activeNativeToken,
			thread.nativeCallDepth,
			thread.errorHandlerDepth,
		)
	}
	for index, value := range thread.values {
		if value != (slot{}) {
			t.Fatalf("root stack slot %d retained %v", index, value.owningValue())
		}
	}
}

func assertTestValues(t *testing.T, got []Value, want ...Value) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("value count = %d; want %d", len(got), len(want))
	}
	for index := range want {
		assertTestValue(t, got[index], want[index])
	}
}
