package lua

import (
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProtectedFailureClosesScratchUpvaluesAndPreservesMutations(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	chunk := mustLoadString(t, state, "@protected-upvalue.lua", `
local escaped
local retained = 0
local ok = pcall(function()
	retained = 7
	local value = 41
	escaped = function() return value + 1 end
	return nil + 1
end)
return ok, retained, escaped()
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Bool(false),
		Number(7),
		Number(42),
	)
	assertRootThreadReady(t, state.main)
}

func TestProtectedFailureRemovesOnlyItsContinuations(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	indexed, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	failingIndex := mustLoadString(
		t,
		state,
		"@failing-index.lua",
		`return nil + 1`,
	)
	metatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__index", failingIndex.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(indexed.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("indexed", indexed.Value()); err != nil {
		t.Fatal(err)
	}

	observer, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnNumber(float64(len(frame.thread.continuations)))
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("continuation_count", observer.Value()); err != nil {
		t.Fatal(err)
	}

	nestedIndex, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	protectedIndex := mustLoadString(t, state, "@protected-index.lua", `
local ok = pcall(function() return nil + 1 end)
if ok then
	return -1
end
return 55
`)
	nestedMetatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := nestedMetatable.RawSetString("__index", protectedIndex.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(nestedIndex.Value(), nestedMetatable); err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("nested_index", nestedIndex.Value()); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@protected-continuation.lua", `
local ok = pcall(function() return indexed.missing end)
return ok, continuation_count(), nested_index.missing, continuation_count()
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Bool(false),
		Number(0),
		Number(55),
		Number(0),
	)
	assertRootThreadReady(t, state.main)
}

func TestProtectedFailureRestoresEveryContinuationMode(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	failing := mustLoadString(
		t,
		state,
		"@failing-continuation.lua",
		`return nil + 1`,
	)
	observer, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnNumber(float64(len(frame.thread.continuations)))
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("continuation_count", observer.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("failing_generator", failing.Value()); err != nil {
		t.Fatal(err)
	}

	newIndexTarget, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	newIndexMetatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := newIndexMetatable.RawSetString("__newindex", failing.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(newIndexTarget.Value(), newIndexMetatable); err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("newindex_target", newIndexTarget.Value()); err != nil {
		t.Fatal(err)
	}

	left, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	right, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	eventMetatable, err := state.NewTableWithCapacity(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := eventMetatable.RawSetString("__lt", failing.Value()); err != nil {
		t.Fatal(err)
	}
	if err := eventMetatable.RawSetString("__concat", failing.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(left.Value(), eventMetatable); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(right.Value(), eventMetatable); err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("left_operand", left.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("right_operand", right.Value()); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@protected-continuation-modes.lua", `
local newindexOK = pcall(function()
	newindex_target.missing = 1
end)
local newindexCount = continuation_count()
local compareOK = pcall(function()
	return left_operand < right_operand
end)
local compareCount = continuation_count()
local concatOK = pcall(function()
	return left_operand .. right_operand
end)
local concatCount = continuation_count()
local iteratorOK = pcall(function()
	for value in failing_generator do
	end
end)
local iteratorCount = continuation_count()
return newindexOK, newindexCount,
	compareOK, compareCount,
	concatOK, concatCount,
	iteratorOK, iteratorCount
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Bool(false),
		Number(0),
		Bool(false),
		Number(0),
		Bool(false),
		Number(0),
		Bool(false),
		Number(0),
	)
	assertRootThreadReady(t, state.main)
}

func TestXPCallHandlerRunsOverTheLiveFailureStack(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	var observedDepth int
	var observedFailingFrames int
	handler, err := state.NewNativeFunction(func(frame Frame) Outcome {
		observedDepth = len(frame.thread.frames)
		for index := 0; index < len(frame.thread.frames)-1; index++ {
			activation := frame.thread.frames[index]
			if activation.function != nil &&
				activation.function.prototype != nil &&
				activation.function.prototype.SourceName() == "@live-stack.lua" {
				observedFailingFrames++
			}
		}
		value, _ := frame.Argument(0)
		return frame.ReturnValue(value)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("inspect_failure", handler.Value()); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@live-stack.lua", `
local function inner()
	return nil + 1
end
local function outer()
	local value = inner()
	return value
end
return xpcall(outer, inspect_failure)
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 ||
		results[0].Truth() ||
		!strings.Contains(
			results[1].String(),
			"attempt to perform arithmetic",
		) {
		t.Fatalf("xpcall results = %v", results)
	}
	if observedDepth < 5 || observedFailingFrames < 3 {
		t.Fatalf(
			"handler observed depth %d with %d source frames; want live failure stack",
			observedDepth,
			observedFailingFrames,
		)
	}
	assertRootThreadReady(t, state.main)
}

func TestProtectedCallsRestoreAfterNativePanics(t *testing.T) {
	testCases := []struct {
		name   string
		source string
	}{
		{
			name:   "target",
			source: `return pcall(panic_native)`,
		},
		{
			name: "handler",
			source: `
return xpcall(
	function() return nil + 1 end,
	panic_native
)
`,
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			state := newStateWithBase(t, Options{})
			defer state.Close()

			panicValue := &struct{ name string }{name: test.name}
			panicking, err := state.NewNativeFunction(func(Frame) Outcome {
				panic(panicValue)
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := state.RawSetGlobal("panic_native", panicking.Value()); err != nil {
				t.Fatal(err)
			}
			chunk := mustLoadString(t, state, "@protected-panic.lua", test.source)

			var recovered any
			func() {
				defer func() {
					recovered = recover()
				}()
				_, _ = state.Call(chunk.Value())
			}()
			if recovered != panicValue {
				t.Fatalf("recovered panic = %#v; want %#v", recovered, panicValue)
			}
			assertRootThreadReady(t, state.main)

			after := mustLoadString(t, state, "@after-panic.lua", `return 23`)
			results, callErr := state.Call(after.Value())
			if callErr != nil {
				t.Fatal(callErr)
			}
			assertTestValues(t, results, Number(23))
		})
	}
}

func TestXPCallUsesAndThenRemovesEmergencyFrameHeadroom(t *testing.T) {
	const frameLimit = 3
	state := newStateWithBase(t, Options{MaxFrames: frameLimit})
	defer state.Close()

	chunk := mustLoadString(t, state, "@protected-frame-limit.lua", `
local function recurse()
	local result = recurse()
	return result
end
local function handler()
	return "handled"
end
local firstOK, first = xpcall(recurse, handler)
local secondOK, second = xpcall(recurse, handler)
local thirdOK, third = xpcall(recurse, handler)
return firstOK, first, secondOK, second, thirdOK, third
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Bool(false),
		state.String("handled"),
		Bool(false),
		state.String("handled"),
		Bool(false),
		state.String("handled"),
	)
	thread := state.main
	if cap(thread.frames) > frameLimit {
		t.Fatalf(
			"frame capacity after handler = %d; configured limit is %d",
			cap(thread.frames),
			frameLimit,
		)
	}
	assertRootThreadReady(t, thread)
}

func TestXPCallHandlerHeadroomExhaustionBecomesFixedError(t *testing.T) {
	const frameLimit = 3
	state := newStateWithBase(t, Options{MaxFrames: frameLimit})
	defer state.Close()

	chunk := mustLoadString(t, state, "@protected-handler-limit.lua", `
local function target()
	local result = target()
	return result
end
local function handler()
	local function exhaust()
		local result = exhaust()
		return result
	end
	return exhaust()
end
local firstOK, first = xpcall(target, handler)
local secondOK, second = xpcall(target, handler)
return firstOK, first, secondOK, second
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Bool(false),
		state.String("error in error handling"),
		Bool(false),
		state.String("error in error handling"),
	)
	thread := state.main
	if cap(thread.frames) > frameLimit {
		t.Fatalf(
			"frame capacity after exhausted handler = %d; limit is %d",
			cap(thread.frames),
			frameLimit,
		)
	}
	assertRootThreadReady(t, thread)
}

func TestProtectedEntryResourceFailureIsPositionedAndCatchable(t *testing.T) {
	state := newStateWithBase(t, Options{MaxFrames: 2})
	defer state.Close()

	chunk := mustLoadString(
		t,
		state,
		"@protected-entry-resource.lua",
		`return pcall(function() return 1 end)`,
	)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Bool(false),
		state.String("protected-entry-resource.lua:1: stack overflow"),
	)
	assertRootThreadReady(t, state.main)
}

func TestXPCallUsesAndThenRemovesEmergencyValueHeadroom(t *testing.T) {
	const source = `
local function target()
	return 1
end
local function handler()
	return "handled"
end
return xpcall(target, handler)
`
	prototype, err := Compile("@protected-value-limit.lua", source)
	if err != nil {
		t.Fatal(err)
	}
	valueLimit := registerCount(prototype) + 1
	state := newStateWithBase(t, Options{MaxValues: valueLimit})
	defer state.Close()
	chunk, err := state.LoadPrototype(prototype)
	if err != nil {
		t.Fatal(err)
	}

	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Bool(false),
		state.String("handled"),
	)
	thread := state.main
	if cap(thread.values) > valueLimit {
		t.Fatalf(
			"value capacity after handler = %d; configured limit is %d",
			cap(thread.values),
			valueLimit,
		)
	}
	assertRootThreadReady(t, thread)
}

func TestNestedProtectedCallsRestoreNativeOwnership(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	target, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnNumber(17)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("native_target", target.Value()); err != nil {
		t.Fatal(err)
	}
	chunk := mustLoadString(t, state, "@nested-protected.lua", `
local outerOK, innerOK, value = pcall(function()
	return pcall(native_target)
end)
local caughtOK, caught = pcall(function()
	return xpcall(
		function() return nil + 1 end,
		function(value) return value end
	)
end)
return outerOK, innerOK, value, caughtOK, caught
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 5 {
		t.Fatalf("nested result count = %d; want 5", len(results))
	}
	assertTestValues(
		t,
		results[:4],
		Bool(true),
		Bool(true),
		Number(17),
		Bool(true),
	)
	if results[4].Truth() {
		t.Fatalf("inner xpcall status = %v; want false", results[4])
	}
	assertRootThreadReady(t, state.main)
}

func TestProtectedCallsDoNotCatchHostContextFailures(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	testCases := []struct {
		name        string
		source      string
		traceSource []string
	}{
		{
			name:        "target",
			source:      `return pcall(raise_context)`,
			traceSource: []string{"=[Go]", "=[Go]", "@protected-context.lua"},
		},
		{
			name: "xpcall handler",
			source: `
return xpcall(
	function() return nil + 1 end,
	raise_context
)
`,
			traceSource: []string{
				"=[Go]",
				"@protected-context.lua",
				"=[Go]",
				"@protected-context.lua",
			},
		},
	}
	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			failure := &Error{
				value:       state.String("context stopped"),
				description: "context stopped",
				category:    ContextError,
			}
			raiser, err := state.NewNativeFunction(func(frame Frame) Outcome {
				return frame.sealError(failure)
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := state.RawSetGlobal(
				"raise_context",
				raiser.Value(),
			); err != nil {
				t.Fatal(err)
			}
			chunk := mustLoadString(
				t,
				state,
				"@protected-context.lua",
				test.source,
			)
			_, callErr := state.Call(chunk.Value())
			var luaErr *Error
			if !errors.As(callErr, &luaErr) ||
				luaErr != failure ||
				luaErr.Category() != ContextError {
				t.Fatalf(
					"protected context failure = %#v; want original failure",
					callErr,
				)
			}
			trace := luaErr.Traceback()
			if len(trace) != len(test.traceSource) {
				t.Fatalf(
					"protected context traceback = %+v; want %d frames",
					trace,
					len(test.traceSource),
				)
			}
			for index, source := range test.traceSource {
				if trace[index].Source != source {
					t.Fatalf(
						"protected context frame %d = %+v; want source %q",
						index,
						trace[index],
						source,
					)
				}
			}
			assertRootThreadReady(t, state.main)
		})
	}
}

func TestXPCallNativeHandlerCanReportNativeDepthOverflow(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	handlerCalls := 0
	handler, err := state.NewNativeFunction(func(frame Frame) Outcome {
		handlerCalls++
		return frame.ReturnString("handled C stack overflow")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("native_depth_handler", handler.Value()); err != nil {
		t.Fatal(err)
	}
	chunk := mustLoadString(t, state, "@protected-native-depth.lua", `
local function recurse()
	local ok, value = xpcall(recurse, native_depth_handler)
	return value
end
return xpcall(recurse, native_depth_handler)
`)

	for iteration := 0; iteration < 3; iteration++ {
		results, callErr := state.Call(chunk.Value())
		if callErr != nil {
			t.Fatal(callErr)
		}
		assertTestValues(
			t,
			results,
			Bool(true),
			state.String("handled C stack overflow"),
		)
		assertRootThreadReady(t, state.main)
	}
	if handlerCalls != 3 {
		t.Fatalf("native depth handler calls = %d; want 3", handlerCalls)
	}
}

func TestProtectedOpenResultsMayCrossTheCheckpointBoundary(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	const resultCount = 100
	values := make([]Value, resultCount)
	for index := range values {
		values[index] = Number(float64(index + 1))
	}
	producer, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnValues(values...)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("many_results", producer.Value()); err != nil {
		t.Fatal(err)
	}
	chunk := mustLoadString(
		t,
		state,
		"@protected-open-results.lua",
		`return pcall(many_results)`,
	)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != resultCount+1 {
		t.Fatalf(
			"protected result count = %d; want %d",
			len(results),
			resultCount+1,
		)
	}
	assertTestValue(t, results[0], Bool(true))
	for index, value := range values {
		assertTestValue(t, results[index+1], value)
	}
	assertRootThreadReady(t, state.main)
}

func TestCaughtFailureDoesNotCaptureATraceback(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	failure := &Error{
		value:       state.String("caught"),
		description: "caught",
		category:    RuntimeError,
	}
	raiser, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.sealError(failure)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("raise_without_trace", raiser.Value()); err != nil {
		t.Fatal(err)
	}
	chunk := mustLoadString(
		t,
		state,
		"@protected-trace.lua",
		`return pcall(raise_without_trace)`,
	)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Bool(false),
		state.String("caught"),
	)
	if len(failure.traceback) != 0 {
		t.Fatalf("caught failure traceback = %#v; want none", failure.traceback)
	}
}

func TestProtectedScratchDoesNotRetainFailedObjects(t *testing.T) {
	state, collected := protectedScratchLifetimeFixture(t)
	defer state.Close()

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		runtime.GC()
		select {
		case <-collected:
			runtime.KeepAlive(state)
			return
		case <-deadline.C:
			t.Fatal("protected scratch retained a failed call's table")
		case <-ticker.C:
		}
	}
}

func protectedScratchLifetimeFixture(t *testing.T) (*State, <-chan struct{}) {
	t.Helper()
	state := newStateWithBase(t, Options{})
	collected := make(chan struct{}, 1)
	watch, err := state.NewNativeFunction(func(frame Frame) Outcome {
		table, ok := frame.Table(0)
		if !ok {
			frame.ThrowArgTypeError(0, TableKind)
		}
		runtime.SetFinalizer(table, func(*Table) {
			collected <- struct{}{}
		})
		return frame.Return()
	})
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("watch_scratch", watch.Value()); err != nil {
		state.Close()
		t.Fatal(err)
	}
	chunk := mustLoadString(t, state, "@protected-lifetime.lua", `
local ok = pcall(function()
	local scratch = {}
	watch_scratch(scratch)
	return nil + 1
end)
return ok
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(false))
	assertRootThreadReady(t, state.main)
	return state, collected
}

func TestXPCallReleasesDiscardedArgumentsBeforeCallingTarget(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	collected := make(chan struct{}, 1)
	makeWatched, err := state.NewNativeFunction(func(frame Frame) Outcome {
		table, tableErr := state.NewTableWithCapacity(0, 0)
		if tableErr != nil {
			frame.ThrowString(tableErr.Error())
		}
		runtime.SetFinalizer(table, func(*Table) {
			collected <- struct{}{}
		})
		return frame.ReturnValue(table.Value())
	})
	if err != nil {
		t.Fatal(err)
	}
	observe, err := state.NewNativeFunction(func(frame Frame) Outcome {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			runtime.GC()
			runtime.Gosched()
			select {
			case <-collected:
				return frame.ReturnBool(true)
			default:
			}
		}
		return frame.ReturnBool(false)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("make_watched_extra", makeWatched.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("observe_discarded_extra", observe.Value()); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@xpcall-extra-lifetime.lua", `
return xpcall(
	observe_discarded_extra,
	false,
	make_watched_extra()
)
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true), Bool(true))
}

func TestWarmPCallSuccessDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state := newStateWithBase(t, Options{})
	defer state.Close()

	target := mustLoadString(t, state, "@protected-target.lua", `return ...`)
	if err := state.RawSetGlobal("protected_target", target.Value()); err != nil {
		t.Fatal(err)
	}
	caller := mustLoadString(
		t,
		state,
		"@protected-warm.lua",
		`return pcall(protected_target, 1, 2, 3)`,
	)
	results := make([]Value, 4)
	for range 4 {
		count, err := state.CallInto(caller.Value(), nil, results)
		if err != nil {
			t.Fatal(err)
		}
		if count != 4 {
			t.Fatalf("warm result count = %d; want 4", count)
		}
	}

	allocations := testing.AllocsPerRun(1_000, func() {
		count, err := state.CallInto(caller.Value(), nil, results)
		if err != nil || count != 4 {
			panic("warm protected call failed")
		}
	})
	if allocations != 0 {
		t.Fatalf("warm pcall allocations = %v; want 0", allocations)
	}
}

func TestWarmCaughtFailurePlumbingDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state := newStateWithBase(t, Options{})
	defer state.Close()

	failure := &Error{
		value:       state.String("prebuilt failure"),
		description: "prebuilt failure",
		category:    RuntimeError,
	}
	raiser, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.sealError(failure)
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := state.NewNativeFunction(func(frame Frame) Outcome {
		value, _ := frame.Argument(0)
		return frame.ReturnValue(value)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("prebuilt_raise", raiser.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("prebuilt_handler", handler.Value()); err != nil {
		t.Fatal(err)
	}

	callers := []*Function{
		mustLoadString(
			t,
			state,
			"@warm-pcall-failure.lua",
			`return pcall(prebuilt_raise)`,
		),
		mustLoadString(
			t,
			state,
			"@warm-xpcall-failure.lua",
			`return xpcall(prebuilt_raise, prebuilt_handler)`,
		),
	}
	results := make([]Value, 2)
	for _, caller := range callers {
		for range 4 {
			count, callErr := state.CallInto(caller.Value(), nil, results)
			if callErr != nil {
				t.Fatal(callErr)
			}
			if count != 2 {
				t.Fatalf("warm caught result count = %d; want 2", count)
			}
		}
		allocations := testing.AllocsPerRun(1_000, func() {
			count, callErr := state.CallInto(caller.Value(), nil, results)
			if callErr != nil || count != 2 {
				panic("warm caught call failed")
			}
		})
		if allocations != 0 {
			t.Fatalf(
				"%s caught-failure allocations = %v; want 0",
				caller.Prototype().SourceName(),
				allocations,
			)
		}
	}
}

func BenchmarkBasePCall(b *testing.B) {
	state := newStateWithBase(b, Options{})
	defer state.Close()
	target := mustLoadString(b, state, "@benchmark-target.lua", `return ...`)
	if err := state.RawSetGlobal("benchmark_target", target.Value()); err != nil {
		b.Fatal(err)
	}
	caller := mustLoadString(
		b,
		state,
		"@benchmark-pcall.lua",
		`return pcall(benchmark_target, 1, 2, 3)`,
	)
	results := make([]Value, 4)
	for range 4 {
		if _, err := state.CallInto(caller.Value(), nil, results); err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := state.CallInto(caller.Value(), nil, results); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBaseXPCallCaught(b *testing.B) {
	state := newStateWithBase(b, Options{})
	defer state.Close()
	failure := &Error{
		value:       state.String("benchmark failure"),
		description: "benchmark failure",
		category:    RuntimeError,
	}
	raiser, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.sealError(failure)
	})
	if err != nil {
		b.Fatal(err)
	}
	handler, err := state.NewNativeFunction(func(frame Frame) Outcome {
		value, _ := frame.Argument(0)
		return frame.ReturnValue(value)
	})
	if err != nil {
		b.Fatal(err)
	}
	if err := state.RawSetGlobal("benchmark_raise", raiser.Value()); err != nil {
		b.Fatal(err)
	}
	if err := state.RawSetGlobal("benchmark_handler", handler.Value()); err != nil {
		b.Fatal(err)
	}
	caller := mustLoadString(
		b,
		state,
		"@benchmark-xpcall.lua",
		`return xpcall(benchmark_raise, benchmark_handler)`,
	)
	results := make([]Value, 2)
	for range 4 {
		if _, err := state.CallInto(caller.Value(), nil, results); err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := state.CallInto(caller.Value(), nil, results); err != nil {
			b.Fatal(err)
		}
	}
}
