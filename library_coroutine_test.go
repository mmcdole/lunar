package lua

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestCoroutineLibraryInstallationAndArgumentChecks(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}

	before, err := state.Global("coroutine")
	if err != nil {
		t.Fatal(err)
	}
	if !before.IsNil() {
		t.Fatalf("new state coroutine = %v; want nil", before)
	}
	loadedBeforeOpen := mustLoadString(
		t,
		state,
		"@open-coroutine.lua",
		`return coroutine.status(coroutine.create(function() end))`,
	)
	if err := state.OpenCoroutine(); err != nil {
		t.Fatal(err)
	}

	oldLibraryValue, err := state.Global("coroutine")
	if err != nil {
		t.Fatal(err)
	}
	oldLibrary, ok := oldLibraryValue.Table()
	if !ok {
		t.Fatalf("coroutine = %v; want table", oldLibraryValue)
	}
	oldFunctions := make(map[string]Value, len(coroutineLibraryFunctions))
	for _, definition := range coroutineLibraryFunctions {
		value := oldLibrary.RawGetString(definition.name)
		if value.Kind() != FunctionKind {
			t.Fatalf("coroutine.%s = %v; want function", definition.name, value)
		}
		oldFunctions[definition.name] = value
	}

	results, err := state.Call(loadedBeforeOpen.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, state.String("suspended"))

	if err := state.SetGlobal("coroutine", Number(1)); err != nil {
		t.Fatal(err)
	}
	// Lua 5.1's base-library opener includes the coroutine library. It must
	// restore the module just as the dedicated opener does.
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}
	newLibraryValue, err := state.Global("coroutine")
	if err != nil {
		t.Fatal(err)
	}
	newLibrary, ok := newLibraryValue.Table()
	if !ok {
		t.Fatalf("reopened coroutine = %v; want table", newLibraryValue)
	}
	if same, applicable := oldLibraryValue.SameObject(newLibraryValue); !applicable || same {
		t.Fatal("reopening coroutine did not install a fresh table")
	}
	for _, definition := range coroutineLibraryFunctions {
		value := newLibrary.RawGetString(definition.name)
		if value.Kind() != FunctionKind {
			t.Fatalf("reopened coroutine.%s = %v; want function", definition.name, value)
		}
		if same, applicable := oldFunctions[definition.name].SameObject(value); !applicable || same {
			t.Fatalf("reopened coroutine.%s did not receive a fresh function", definition.name)
		}
	}

	native, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("native_function", native.Value()); err != nil {
		t.Fatal(err)
	}
	callable, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	callableMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := callableMetatable.RawSetString("__call", native.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(callable.Value(), callableMetatable); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("callable", callable.Value()); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name    string
		source  string
		message string
	}{
		{
			name:    "create missing",
			source:  `return coroutine.create()`,
			message: "bad argument #1 to 'create' (Lua function expected)",
		},
		{
			name:    "create number",
			source:  `return coroutine.create(1)`,
			message: "bad argument #1 to 'create' (Lua function expected)",
		},
		{
			name:    "create native function",
			source:  `return coroutine.create(native_function)`,
			message: "bad argument #1 to 'create' (Lua function expected)",
		},
		{
			name:    "create callable object",
			source:  `return coroutine.create(callable)`,
			message: "bad argument #1 to 'create' (Lua function expected)",
		},
		{
			name:    "wrap native function",
			source:  `return coroutine.wrap(native_function)`,
			message: "bad argument #1 to 'wrap' (Lua function expected)",
		},
		{
			name:    "resume number",
			source:  `return coroutine.resume(1)`,
			message: "bad argument #1 to 'resume' (coroutine expected)",
		},
		{
			name:    "status number",
			source:  `return coroutine.status(1)`,
			message: "bad argument #1 to 'status' (coroutine expected)",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			chunk := mustLoadString(
				t,
				state,
				"@coroutine-argument.lua",
				test.source,
			)
			if _, callErr := state.Call(chunk.Value()); callErr == nil ||
				callErr.Error() != "coroutine-argument.lua:1: "+test.message {
				t.Fatalf("error = %v; want %q", callErr, test.message)
			}
		})
	}

	results = runCoroutineLibraryChunk(t, state, "@coroutine-alias.lua", `
local alias = coroutine.create
local aliasOK, aliasError = pcall(function()
	return alias(1)
end)
local indirectOK, indirectError = pcall(coroutine.create, 1)
return aliasOK, aliasError, indirectOK, indirectError
`)
	if len(results) != 4 {
		t.Fatalf("argument-name result count = %d; want 4", len(results))
	}
	assertTestValues(t, results[0:1], Bool(false))
	aliasError, ok := results[1].AsString()
	if !ok ||
		aliasError !=
			"coroutine-alias.lua:4: bad argument #1 to 'alias' "+
				"(Lua function expected)" {
		t.Fatalf("aliased create error = %q", aliasError)
	}
	assertTestValues(t, results[2:3], Bool(false))
	indirectError, ok := results[3].AsString()
	if !ok ||
		indirectError !=
			"bad argument #1 to '?' (Lua function expected)" {
		t.Fatalf("indirect create error = %q", indirectError)
	}

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenCoroutine(); !errors.Is(err, ErrClosed) {
		t.Fatalf("OpenCoroutine after Close = %v; want ErrClosed", err)
	}
}

func TestCoroutineResumeYieldRunningAndStatus(t *testing.T) {
	state := newCoroutineLibraryTestState(t)

	results := runCoroutineLibraryChunk(t, state, "@resume-status.lua", `
local main = coroutine.running()
local co = coroutine.create(function(first, second)
	local self = coroutine.running()
	local a, b, c = coroutine.yield(
		first, nil, second, self, coroutine.status(self)
	)
	return a, b, c
end)
local initial = coroutine.status(co)
local firstOK, first, middle, last, self, inside = coroutine.resume(co, 10, 20)
local suspended = coroutine.status(co)
local secondOK, resumedFirst, resumedMiddle, resumedLast =
	coroutine.resume(co, 30, nil, 50)
local dead = coroutine.status(co)
local deadOK, deadMessage = coroutine.resume(co)
return main, initial,
	firstOK, first, middle, last, self == co, inside, suspended,
	secondOK, resumedFirst, resumedMiddle, resumedLast, dead,
	deadOK, deadMessage
`)
	assertTestValues(
		t,
		results,
		Nil(),
		state.String("suspended"),
		Bool(true),
		Number(10),
		Nil(),
		Number(20),
		Bool(true),
		state.String("running"),
		state.String("suspended"),
		Bool(true),
		Number(30),
		Nil(),
		Number(50),
		state.String("dead"),
		Bool(false),
		state.String("cannot resume dead coroutine"),
	)
}

func TestCoroutineNestedResumeStatesAndErrors(t *testing.T) {
	state := newCoroutineLibraryTestState(t)

	results := runCoroutineLibraryChunk(t, state, "@nested-coroutine.lua", `
local parent
local child = coroutine.create(function()
	local self = coroutine.running()
	local resumedParent, resumeError = coroutine.resume(parent)
	coroutine.yield(
		coroutine.status(parent),
		coroutine.status(self),
		resumedParent,
		resumeError
	)
	return coroutine.status(parent)
end)
parent = coroutine.create(function()
	local initialChild = coroutine.status(child)
	local childOK, parentState, selfState, parentOK, parentError =
		coroutine.resume(child)
	local yieldedChild = coroutine.status(child)
	coroutine.yield(
		initialChild,
		childOK,
		parentState,
		selfState,
		parentOK,
		parentError,
		yieldedChild
	)
	local finalChildOK, childSawParent = coroutine.resume(child)
	return finalChildOK,
		childSawParent,
		coroutine.status(child),
		coroutine.status(coroutine.running())
end)

local firstOK,
	initialChild,
	childOK,
	parentState,
	selfState,
	parentOK,
	parentError,
	yieldedChild = coroutine.resume(parent)
local yieldedParent = coroutine.status(parent)
local finalOK,
	finalChildOK,
	childSawParent,
	deadChild,
	runningParent = coroutine.resume(parent)
local deadParent = coroutine.status(parent)
return firstOK,
	initialChild,
	childOK,
	parentState,
	selfState,
	parentOK,
	parentError,
	yieldedChild,
	yieldedParent,
	finalOK,
	finalChildOK,
	childSawParent,
	deadChild,
	runningParent,
	deadParent
`)
	assertTestValues(
		t,
		results,
		Bool(true),
		state.String("suspended"),
		Bool(true),
		state.String("normal"),
		state.String("running"),
		Bool(false),
		state.String("cannot resume normal coroutine"),
		state.String("suspended"),
		state.String("suspended"),
		Bool(true),
		Bool(true),
		state.String("normal"),
		state.String("dead"),
		state.String("running"),
		state.String("dead"),
	)

	results = runCoroutineLibraryChunk(t, state, "@self-resume.lua", `
local co
co = coroutine.create(function()
	return coroutine.resume(co)
end)
local outerOK, innerOK, message = coroutine.resume(co)
return outerOK, innerOK, message, coroutine.status(co)
`)
	assertTestValues(
		t,
		results,
		Bool(true),
		Bool(false),
		state.String("cannot resume running coroutine"),
		state.String("dead"),
	)
}

func TestCoroutineResumeAndWrapPreserveErrorsAndValues(t *testing.T) {
	state := newCoroutineLibraryTestState(t)
	marker, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("error_marker", marker.Value()); err != nil {
		t.Fatal(err)
	}
	raiseValue, err := state.NewNativeFunction(func(frame Frame) Outcome {
		value, _ := frame.Argument(0)
		return frame.Raise(value)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("raise_value", raiseValue.Value()); err != nil {
		t.Fatal(err)
	}

	results := runCoroutineLibraryChunk(t, state, "@resume-errors.lua", `
local nilThread = coroutine.create(function()
	raise_value(nil)
end)
local nilOK, nilError = coroutine.resume(nilThread)
local objectThread = coroutine.create(function()
	raise_value(error_marker)
end)
local objectOK, objectError = coroutine.resume(objectThread)
return nilOK,
	nilError,
	objectOK,
	objectError,
	objectError == error_marker,
	coroutine.status(nilThread),
	coroutine.status(objectThread)
`)
	if len(results) != 7 {
		t.Fatalf("resume error result count = %d; want 7", len(results))
	}
	assertTestValues(
		t,
		results,
		Bool(false),
		Nil(),
		Bool(false),
		marker.Value(),
		Bool(true),
		state.String("dead"),
		state.String("dead"),
	)

	results = runCoroutineLibraryChunk(t, state, "@wrap-values.lua", `
local wrapped = coroutine.wrap(function(first, second)
	local a, b, c = coroutine.yield(first, nil, second)
	return a, b, c
end)
local first, middle, last = wrapped(1, 3)
local returnedFirst, returnedMiddle, returnedLast = wrapped(4, nil, 6)
local deadOK, deadError = pcall(wrapped)
return first,
	middle,
	last,
	returnedFirst,
	returnedMiddle,
	returnedLast,
	deadOK,
	deadError
`)
	if len(results) != 8 {
		t.Fatalf("wrap result count = %d; want 8", len(results))
	}
	assertTestValues(
		t,
		results[:7],
		Number(1),
		Nil(),
		Number(3),
		Number(4),
		Nil(),
		Number(6),
		Bool(false),
	)
	deadError, ok := results[7].AsString()
	if !ok || deadError != "cannot resume dead coroutine" {
		t.Fatalf("wrapped dead-coroutine error = %q", deadError)
	}

	results = runCoroutineLibraryChunk(t, state, "@wrap-errors.lua", `
local wrappedObject = coroutine.wrap(function()
	raise_value(error_marker)
end)
local objectOK, objectError = pcall(wrappedObject)
local wrappedString = coroutine.wrap(function()
	raise_value("boom")
end)
local stringOK, stringError = pcall(wrappedString)
local wrappedDirect = coroutine.wrap(function()
	raise_value("direct")
end)
local function callWrappedDirect()
	return wrappedDirect()
end
local directOK, directError = pcall(callWrappedDirect)
return objectOK,
	objectError,
	objectError == error_marker,
	stringOK,
	stringError,
	directOK,
	directError
`)
	if len(results) != 7 {
		t.Fatalf("wrapped error result count = %d; want 7", len(results))
	}
	assertTestValues(
		t,
		results[:4],
		Bool(false),
		marker.Value(),
		Bool(true),
		Bool(false),
	)
	stringError, ok := results[4].AsString()
	if !ok || stringError != "boom" {
		t.Fatalf("wrapped string error = %q", stringError)
	}
	assertTestValues(t, results[5:6], Bool(false))
	directError, ok := results[6].AsString()
	if !ok ||
		!strings.HasSuffix(directError, "direct") ||
		!strings.Contains(directError, "wrap-errors.lua:") {
		t.Fatalf("direct wrapped string error = %q", directError)
	}
}

func TestCoroutineNestedNativePanicRestoresExecution(t *testing.T) {
	state := newCoroutineLibraryTestState(t)
	panicking, err := state.NewNativeFunction(func(Frame) Outcome {
		panic("nested coroutine panic")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("panic_in_child", panicking.Value()); err != nil {
		t.Fatal(err)
	}
	entry := mustLoadString(t, state, "@nested-panic.lua", `
crashing_child = coroutine.create(function()
	panic_in_child()
end)
crashing_parent = coroutine.create(function()
	return coroutine.resume(crashing_child)
end)
return coroutine.resume(crashing_parent)
`)
	func() {
		defer func() {
			if recovered := recover(); recovered != "nested coroutine panic" {
				t.Fatalf("panic = %v", recovered)
			}
		}()
		_, _ = state.Call(entry.Value())
	}()

	for _, name := range []string{"crashing_child", "crashing_parent"} {
		value, err := state.Global(name)
		if err != nil {
			t.Fatal(err)
		}
		thread, ok := value.Thread()
		if !ok {
			t.Fatalf("%s = %v; want thread", name, value)
		}
		assertDeadCoroutineClean(t, thread)
	}
	assertStateExecutionIdle(t, state)

	recovery := mustLoadString(t, state, "@after-panic.lua", `return 42`)
	results, err := state.Call(recovery.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(42))
}

func TestCoroutineYieldBoundaryMatchesLua51(t *testing.T) {
	state := newCoroutineLibraryTestState(t)

	mainYield := mustLoadString(
		t,
		state,
		"@main-yield.lua",
		`return coroutine.yield(1)`,
	)
	if _, err := state.Call(mainYield.Value()); err == nil ||
		!strings.HasSuffix(
			err.Error(),
			"attempt to yield across metamethod/C-call boundary",
		) {
		t.Fatalf("main coroutine.yield error = %v", err)
	}

	results := runCoroutineLibraryChunk(t, state, "@protected-yield.lua", `
local pcallThread = coroutine.create(function()
	return pcall(function()
		return coroutine.yield("blocked")
	end)
end)
local outerPCallOK, innerPCallOK, pcallError = coroutine.resume(pcallThread)
local xpcallThread = coroutine.create(function()
	return xpcall(
		function()
			return coroutine.yield("blocked")
		end,
		function(message)
			return "handled:" .. message
		end
	)
end)
local outerXPCallOK, innerXPCallOK, xpcallError =
	coroutine.resume(xpcallThread)
return outerPCallOK,
	innerPCallOK,
	pcallError,
	outerXPCallOK,
	innerXPCallOK,
	xpcallError
`)
	assertTestValues(
		t,
		results,
		Bool(true),
		Bool(false),
		state.String("attempt to yield across metamethod/C-call boundary"),
		Bool(true),
		Bool(false),
		state.String(
			"handled:attempt to yield across metamethod/C-call boundary",
		),
	)

	yieldingHook := mustLoadString(t, state, "@yielding-hook.lua", `
return function()
	return coroutine.yield("blocked")
end
`)
	hookResults, err := state.Call(yieldingHook.Value())
	if err != nil {
		t.Fatal(err)
	}
	hook, ok := hookResults[0].Function()
	if !ok {
		t.Fatalf("yielding hook = %v; want function", hookResults[0])
	}
	target, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	metatable, err := state.NewTable(0, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"__index",
		"__newindex",
		"__add",
		"__lt",
		"__concat",
	} {
		if err := metatable.RawSetString(name, hook.Value()); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.SetMetatable(target.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("yield_target", target.Value()); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name   string
		source string
	}{
		{name: "index", source: `return yield_target.missing`},
		{name: "newindex", source: `yield_target.missing = 1`},
		{name: "arithmetic", source: `return yield_target + 1`},
		{name: "comparison", source: `return yield_target < yield_target`},
		{name: "concatenation", source: `return yield_target .. "x"`},
		{
			name: "iterator",
			source: `
for value in function()
	return coroutine.yield("blocked")
end do
end
`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "return coroutine.resume(coroutine.create(function()\n" +
				test.source + "\nend))"
			barrierResults := runCoroutineLibraryChunk(
				t,
				state,
				"@yield-barrier.lua",
				source,
			)
			assertTestValues(
				t,
				barrierResults,
				Bool(false),
				state.String(
					"attempt to yield across metamethod/C-call boundary",
				),
			)
		})
	}

	callable, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	callableMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := callableMetatable.RawSetString("__call", hook.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(callable.Value(), callableMetatable); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("yield_callable", callable.Value()); err != nil {
		t.Fatal(err)
	}
	results = runCoroutineLibraryChunk(t, state, "@allowed-yield.lua", `
local callThread = coroutine.create(function()
	local function yieldThroughCall(...)
		return coroutine.yield(...)
	end
	return yieldThroughCall(1, nil, 3)
end)
local firstOK, first, middle, last = coroutine.resume(callThread)
local finalOK, resumedFirst, resumedMiddle, resumedLast =
	coroutine.resume(callThread, 4, nil, 6)
local metamethodThread = coroutine.create(function()
	return yield_callable()
end)
local metamethodOK, metamethodYield = coroutine.resume(metamethodThread)
local metamethodFinalOK, metamethodReturn =
	coroutine.resume(metamethodThread, 9)
return firstOK,
	first,
	middle,
	last,
	finalOK,
	resumedFirst,
	resumedMiddle,
	resumedLast,
	metamethodOK,
	metamethodYield,
	metamethodFinalOK,
	metamethodReturn
`)
	assertTestValues(
		t,
		results,
		Bool(true),
		Number(1),
		Nil(),
		Number(3),
		Bool(true),
		Number(4),
		Nil(),
		Number(6),
		Bool(true),
		state.String("blocked"),
		Bool(true),
		Number(9),
	)
}

func TestWarmCoroutineLibraryResumeDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, resumer := newCoroutineLibraryResumer(t)
	destination := make([]Value, 2)
	for range 4 {
		count, err := state.CallInto(
			resumer.Value(),
			nil,
			destination,
		)
		if err != nil || count != 2 {
			t.Fatalf("warmup = (count=%d, err=%v)", count, err)
		}
		assertTestValues(t, destination, Bool(true), Number(1))
	}
	allocations := testing.AllocsPerRun(1_000, func() {
		count, err := state.CallInto(
			resumer.Value(),
			nil,
			destination,
		)
		if err != nil || count != 2 {
			panic("warm coroutine library resume failed")
		}
	})
	if allocations != 0 {
		t.Fatalf("warm library resume allocations = %v; want 0", allocations)
	}
	runtime.KeepAlive(resumer)
}

func BenchmarkCoroutineLibraryResumeYield(b *testing.B) {
	state, resumer := newCoroutineLibraryResumer(b)
	destination := make([]Value, 2)
	if _, err := state.CallInto(
		resumer.Value(),
		nil,
		destination,
	); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := state.CallInto(
			resumer.Value(),
			nil,
			destination,
		); err != nil {
			b.Fatal(err)
		}
	}
}

func newCoroutineLibraryTestState(t testing.TB) *State {
	t.Helper()
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.OpenBase(); err != nil {
		_ = state.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = state.Close()
	})
	return state
}

func newCoroutineLibraryResumer(t testing.TB) (*State, *Function) {
	t.Helper()
	state := newCoroutineLibraryTestState(t)
	entry := mustLoadString(t, state, "@library-resume-benchmark.lua", `
local thread = coroutine.create(function()
	while true do
		coroutine.yield(1)
	end
end)
return function()
	return coroutine.resume(thread)
end
`)
	results, err := state.Call(entry.Value())
	if err != nil {
		t.Fatal(err)
	}
	resumer, ok := results[0].Function()
	if !ok {
		t.Fatalf("resumer = %v; want function", results[0])
	}
	return state, resumer
}

func runCoroutineLibraryChunk(
	t *testing.T,
	state *State,
	sourceName string,
	source string,
) []Value {
	t.Helper()
	chunk := mustLoadString(t, state, sourceName, source)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	return results
}
