package lua

import (
	"errors"
	"runtime"
	"testing"
)

func TestFrameCallInvokesLuaNativeAndCallableValues(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	luaTarget := mustLoadString(t, state, "@nested-lua.lua", `
local first, middle, last = ...
return first, middle, last, nil
`)
	nativeTarget, err := state.NewNativeFunction(func(frame Frame) Outcome {
		first, _ := frame.Argument(0)
		last, _ := frame.Argument(2)
		return frame.ReturnValues(first, Nil(), last, Nil())
	})
	if err != nil {
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
	callHandler, err := state.NewNativeFunction(func(frame Frame) Outcome {
		self, _ := frame.Argument(0)
		first, _ := frame.Argument(1)
		last, _ := frame.Argument(3)
		return frame.ReturnValues(self, first, Nil(), last, Nil())
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := callableMetatable.RawSetString(
		"__call",
		callHandler.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		callable.Value(),
		callableMetatable,
	); err != nil {
		t.Fatal(err)
	}

	arguments := []Value{
		Number(11),
		Nil(),
		state.String("last"),
	}
	for _, test := range []struct {
		name     string
		callable Value
		want     []Value
	}{
		{
			name:     "Lua function",
			callable: luaTarget.Value(),
			want: []Value{
				Number(11),
				Nil(),
				state.String("last"),
				Nil(),
			},
		},
		{
			name:     "native function",
			callable: nativeTarget.Value(),
			want: []Value{
				Number(11),
				Nil(),
				state.String("last"),
				Nil(),
			},
		},
		{
			name:     "callable table",
			callable: callable.Value(),
			want: []Value{
				callable.Value(),
				Number(11),
				Nil(),
				state.String("last"),
				Nil(),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var nestedErr error
			frameRemainedValid := false
			host, constructionErr := state.NewNativeFunction(
				func(frame Frame) Outcome {
					before, beforePresent := frame.Argument(0)
					results, callErr := frame.Call(
						test.callable,
						arguments...,
					)
					nestedErr = callErr
					after, afterPresent := frame.Argument(0)
					frameRemainedValid =
						beforePresent &&
							afterPresent &&
							rawEqual(before, after) &&
							frame.ArgumentCount() == 1 &&
							frame.State() == state &&
							frame.Thread() == state.MainThread()
					if callErr != nil {
						return frame.RaiseString(callErr.Error())
					}
					return frame.ReturnValues(results...)
				},
			)
			if constructionErr != nil {
				t.Fatal(constructionErr)
			}
			results, callErr := state.Call(
				host.Value(),
				state.String("outer argument"),
			)
			if callErr != nil {
				t.Fatal(callErr)
			}
			if nestedErr != nil {
				t.Fatalf("nested call error = %v", nestedErr)
			}
			if !frameRemainedValid {
				t.Fatal("outer Frame was not restored after nested call")
			}
			assertTestValues(t, results, test.want...)
			assertRootThreadReady(t, state.MainThread())
		})
	}
}

func TestFrameCallRejectsInputsAtomically(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	foreignState, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer foreignState.Close()

	foreign, err := foreignState.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	target, err := state.NewNativeFunction(func(frame Frame) Outcome {
		calls++
		return frame.ReturnNumber(1)
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name      string
		callable  Value
		arguments []Value
		want      error
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
			name:      "invalid argument",
			callable:  target.Value(),
			arguments: []Value{{}},
			want:      ErrInvalidValue,
		},
		{
			name:      "foreign argument",
			callable:  target.Value(),
			arguments: []Value{foreign.Value()},
			want:      ErrForeignValue,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			beforeCalls := calls
			destination := []Value{Number(70), Number(71)}
			var gotErr error
			frameRemainedValid := false
			host, constructionErr := state.NewNativeFunction(
				func(frame Frame) Outcome {
					before, _ := frame.Argument(0)
					count, callErr := frame.CallInto(
						test.callable,
						test.arguments,
						destination,
					)
					gotErr = callErr
					after, _ := frame.Argument(0)
					frameRemainedValid =
						count == 0 &&
							rawEqual(before, after) &&
							frame.ArgumentCount() == 1
					return frame.ReturnBool(true)
				},
			)
			if constructionErr != nil {
				t.Fatal(constructionErr)
			}
			results, callErr := state.Call(
				host.Value(),
				state.String("outer"),
			)
			if callErr != nil {
				t.Fatal(callErr)
			}
			assertTestValues(t, results, Bool(true))
			if !errors.Is(gotErr, test.want) {
				t.Fatalf("nested call error = %v; want %v", gotErr, test.want)
			}
			if calls != beforeCalls {
				t.Fatalf(
					"rejected nested call ran target %d times; want %d",
					calls,
					beforeCalls,
				)
			}
			if !frameRemainedValid {
				t.Fatal("rejected input changed the outer Frame")
			}
			assertTestValues(
				t,
				destination,
				Number(70),
				Number(71),
			)
			assertRootThreadReady(t, state.MainThread())
		})
	}
}

func TestFrameCallIntoHandlesOverlapAndCapacityAtomically(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	identity := mustLoadString(t, state, "@nested-identity.lua", `return ...`)
	values := []Value{
		Number(10),
		Number(20),
		Number(90),
		Number(91),
	}
	var overlapCount int
	var overlapErr error
	overlapHost, err := state.NewNativeFunction(func(frame Frame) Outcome {
		overlapCount, overlapErr = frame.CallInto(
			identity.Value(),
			values[:2],
			values[1:3],
		)
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Call(overlapHost.Value()); err != nil {
		t.Fatal(err)
	}
	if overlapErr != nil || overlapCount != 2 {
		t.Fatalf(
			"overlap CallInto = (%d, %v); want (2, nil)",
			overlapCount,
			overlapErr,
		)
	}
	assertTestValues(
		t,
		values,
		Number(10),
		Number(10),
		Number(20),
		Number(91),
	)

	producer := mustLoadString(t, state, "@nested-producer.lua", `
nested_side_effect = nested_side_effect + 1
return 1, nil, 3
`)
	if err := state.SetGlobal("nested_side_effect", Number(0)); err != nil {
		t.Fatal(err)
	}
	destination := []Value{Number(80), Number(81)}
	var required int
	var capacityFailure error
	capacityHost, err := state.NewNativeFunction(func(frame Frame) Outcome {
		required, capacityFailure = frame.CallInto(
			producer.Value(),
			nil,
			destination,
		)
		if _, present := frame.Argument(0); !present {
			return frame.RaiseString("outer Frame lost its argument")
		}
		return frame.ReturnBool(true)
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(capacityHost.Value(), Number(7))
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true))
	var capacityError *ResultCapacityError
	if !errors.As(capacityFailure, &capacityError) ||
		required != 3 ||
		capacityError.Required != 3 ||
		capacityError.Available != 2 {
		t.Fatalf(
			"short nested destination = (%d, %#v)",
			required,
			capacityFailure,
		)
	}
	assertTestValues(t, destination, Number(80), Number(81))
	sideEffect, err := state.Global("nested_side_effect")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, sideEffect, Number(1))

	failing := mustLoadString(t, state, "@nested-into-failure.lua", `
nested_failure_side_effect = 27
return nil + 1
`)
	failureDestination := []Value{Number(60), Number(61)}
	var nestedFailure *Error
	failureHost, err := state.NewNativeFunction(func(frame Frame) Outcome {
		count, callErr := frame.CallInto(
			failing.Value(),
			nil,
			failureDestination,
		)
		if count != 0 || !errors.As(callErr, &nestedFailure) {
			return frame.RaiseString("ordinary nested failure was not returned")
		}
		return frame.ReturnBool(true)
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err = state.Call(failureHost.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true))
	assertTestValues(
		t,
		failureDestination,
		Number(60),
		Number(61),
	)
	sideEffect, err = state.Global("nested_failure_side_effect")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, sideEffect, Number(27))
	assertRootThreadReady(t, state.MainThread())
}

func TestFrameCallSurvivesStackRelocationAndRejectsStateReentry(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	target := mustLoadString(t, state, "@nested-stack-growth.lua", `
local function grow(depth, payload)
	local first, second, third, fourth = depth, payload, depth, payload
	if depth == 0 then
		return payload, nil
	end
	local result, trailing = grow(depth - 1, payload)
	if first == -1 or second == nil or third == -1 or fourth == nil then
		return nil
	end
	return result, trailing
end
return grow(...)
`)
	marker, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var stateCallError error
	stacksGrew := false
	frameValid := false
	host, err := state.NewNativeFunction(
		func(frame Frame) Outcome {
			beforeValues := cap(frame.thread.values)
			beforeFrames := cap(frame.thread.frames)
			_, stateCallError = state.Call(target.Value())
			results, callErr := frame.Call(
				target.Value(),
				Number(64),
				marker.Value(),
			)
			if callErr != nil {
				return frame.RaiseString(callErr.Error())
			}
			argument, argumentPresent := frame.Argument(0)
			capture := frame.Capture(0)
			argumentSame, argumentApplicable :=
				argument.SameObject(marker.Value())
			captureSame, captureApplicable :=
				capture.SameObject(marker.Value())
			frameValid =
				argumentPresent &&
					argumentApplicable &&
					argumentSame &&
					captureApplicable &&
					captureSame
			stacksGrew =
				cap(frame.thread.values) > beforeValues &&
					cap(frame.thread.frames) > beforeFrames
			return frame.ReturnValues(results...)
		},
		marker.Value(),
	)
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(host.Value(), marker.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, marker.Value(), Nil())
	if !errors.Is(stateCallError, ErrRunning) {
		t.Fatalf("State.Call during callback = %v; want ErrRunning", stateCallError)
	}
	if !stacksGrew {
		t.Fatal("nested target did not force both execution stacks to grow")
	}
	if !frameValid {
		t.Fatal("stack relocation invalidated outer arguments or captures")
	}
	assertRootThreadReady(t, state.MainThread())
}

func TestFrameCallDistinguishesLuaCallableFailures(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	plain, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	recursiveHandler, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	recursiveMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := recursiveMetatable.RawSetString(
		"__call",
		recursiveHandler.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		recursiveHandler.Value(),
		recursiveMetatable,
	); err != nil {
		t.Fatal(err)
	}
	outer, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	outerMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := outerMetatable.RawSetString(
		"__call",
		recursiveHandler.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(outer.Value(), outerMetatable); err != nil {
		t.Fatal(err)
	}

	for _, callable := range []Value{plain.Value(), outer.Value()} {
		var nestedError error
		host, constructionErr := state.NewNativeFunction(
			func(frame Frame) Outcome {
				_, nestedError = frame.Call(callable)
				if nestedError == nil {
					return frame.RaiseString("noncallable nested value ran")
				}
				return frame.ReturnBool(true)
			},
		)
		if constructionErr != nil {
			t.Fatal(constructionErr)
		}
		results, callErr := state.Call(host.Value())
		if callErr != nil {
			t.Fatal(callErr)
		}
		assertTestValues(t, results, Bool(true))
		failure, ok := nestedError.(*Error)
		if !ok ||
			failure.Category() != RuntimeError ||
			failure.Error() != "attempt to call a table value" {
			t.Fatalf("noncallable nested failure = %#v", nestedError)
		}
	}
	assertRootThreadReady(t, state.MainThread())
}

func TestFrameRaiseErrorPreservesFailureAndAppendsOuterTrace(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	marker, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("nested cause")
	original := &Error{
		value:       marker.Value(),
		description: "nested marker failure",
		category:    ResourceError,
		cause:       cause,
	}
	raiser, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.sealError(original)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("nested_raise", raiser.Value()); err != nil {
		t.Fatal(err)
	}
	nested := mustLoadString(t, state, "@nested-trace.lua", `
local function leaf()
	nested_trace_side_effect = 19
	nested_raise()
end
leaf()
return 1
`)

	var nestedTrace []TraceFrame
	var nestedFailure *Error
	bridge, err := state.NewNativeFunction(func(frame Frame) Outcome {
		_, callErr := frame.Call(nested.Value())
		if !errors.As(callErr, &nestedFailure) {
			return frame.RaiseString("nested failure was not a Lua error")
		}
		nestedTrace = nestedFailure.Traceback()
		return frame.RaiseError(nestedFailure)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("nested_bridge", bridge.Value()); err != nil {
		t.Fatal(err)
	}
	outer := mustLoadString(t, state, "@outer-trace.lua", `
local function invoke()
	local result = nested_bridge()
	return result
end
return invoke()
`)
	_, callErr := state.Call(outer.Value())
	var failure *Error
	if !errors.As(callErr, &failure) {
		t.Fatalf("raised nested failure = %#v; want *Error", callErr)
	}
	if failure == nestedFailure {
		t.Fatal("RaiseError reused and mutated the nested failure object")
	}
	if failure.Category() != ResourceError ||
		!errors.Is(failure, cause) ||
		failure.Error() != "nested marker failure" {
		t.Fatalf("propagated failure = %#v", failure)
	}
	if same, applicable := failure.Value().SameObject(marker.Value()); !applicable ||
		!same {
		t.Fatal("RaiseError lost arbitrary error Value identity")
	}
	sideEffect, err := state.Global("nested_trace_side_effect")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, sideEffect, Number(19))

	if len(nestedTrace) != 3 ||
		nestedTrace[0].Source != "=[Go]" ||
		nestedTrace[1].Source != "@nested-trace.lua" ||
		nestedTrace[2].Source != "@nested-trace.lua" {
		t.Fatalf("nested-only traceback = %+v", nestedTrace)
	}
	trace := failure.Traceback()
	if len(trace) != len(nestedTrace)+2 {
		t.Fatalf(
			"propagated traceback = %+v; want nested %d + outer 2",
			trace,
			len(nestedTrace),
		)
	}
	for index := range nestedTrace {
		if trace[index] != nestedTrace[index] {
			t.Fatalf(
				"propagated trace prefix %d = %+v; want %+v",
				index,
				trace[index],
				nestedTrace[index],
			)
		}
	}
	outerTrace := trace[len(nestedTrace):]
	if outerTrace[0].Source != "=[Go]" ||
		outerTrace[1].Source != "@outer-trace.lua" ||
		outerTrace[1].TailCalls != 1 {
		t.Fatalf("outer traceback = %+v", outerTrace)
	}
	assertRootThreadReady(t, state.MainThread())
}

func TestFrameCallLetsNestedPCallAndXPCallCatchLuaErrors(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	marker, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	raiser, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.Raise(marker.Value())
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("nested_protected_raise", raiser.Value()); err != nil {
		t.Fatal(err)
	}
	target := mustLoadString(t, state, "@nested-protected-call.lua", `
local pcallOK, pcallValue = pcall(nested_protected_raise)
local xpcallOK, xpcallValue = xpcall(
	nested_protected_raise,
	function(value) return value end
)
return pcallOK, pcallValue, xpcallOK, xpcallValue
`)
	host, err := state.NewNativeFunction(func(frame Frame) Outcome {
		results, callErr := frame.Call(target.Value())
		if callErr != nil {
			return frame.RaiseString(callErr.Error())
		}
		return frame.ReturnValues(results...)
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(host.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 4 {
		t.Fatalf("protected nested result count = %d; want 4", len(results))
	}
	assertTestValue(t, results[0], Bool(false))
	assertTestValue(t, results[2], Bool(false))
	for _, index := range []int{1, 3} {
		if same, applicable := results[index].SameObject(marker.Value()); !applicable ||
			!same {
			t.Fatalf(
				"protected nested error result %d lost marker identity",
				index,
			)
		}
	}
	assertRootThreadReady(t, state.MainThread())
}

func TestFrameRaiseErrorAppendsEachNestedTraceSegmentOnce(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	fail, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.RaiseString("nested bridge failure")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("nested_trace_fail", fail.Value()); err != nil {
		t.Fatal(err)
	}
	inner := mustLoadString(t, state, "@trace-inner.lua", `
local value = nested_trace_fail()
return value
`)

	var innerTrace []TraceFrame
	bridgeOne, err := state.NewNativeFunction(func(frame Frame) Outcome {
		_, callErr := frame.Call(inner.Value())
		var failure *Error
		if !errors.As(callErr, &failure) {
			return frame.RaiseString("inner bridge lost its Lua error")
		}
		innerTrace = failure.Traceback()
		return frame.RaiseError(failure)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("nested_trace_bridge_one", bridgeOne.Value()); err != nil {
		t.Fatal(err)
	}
	middle := mustLoadString(t, state, "@trace-middle.lua", `
local value = nested_trace_bridge_one()
return value
`)

	var middleTrace []TraceFrame
	bridgeTwo, err := state.NewNativeFunction(func(frame Frame) Outcome {
		_, callErr := frame.Call(middle.Value())
		var failure *Error
		if !errors.As(callErr, &failure) {
			return frame.RaiseString("outer bridge lost its Lua error")
		}
		middleTrace = failure.Traceback()
		return frame.RaiseError(failure)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("nested_trace_bridge_two", bridgeTwo.Value()); err != nil {
		t.Fatal(err)
	}
	outer := mustLoadString(t, state, "@trace-outer.lua", `
local value = nested_trace_bridge_two()
return value
`)

	_, callErr := state.Call(outer.Value())
	var failure *Error
	if !errors.As(callErr, &failure) {
		t.Fatalf("nested bridge failure = %#v; want *Error", callErr)
	}
	if failure.Error() != "nested bridge failure" {
		t.Fatalf("nested bridge description = %q", failure.Error())
	}

	assertTraceSuffix := func(
		name string,
		trace []TraceFrame,
		prefix []TraceFrame,
		luaSource string,
	) {
		t.Helper()
		if len(trace) != len(prefix)+2 {
			t.Fatalf(
				"%s trace = %+v; want prefix %d + two frames",
				name,
				trace,
				len(prefix),
			)
		}
		for index := range prefix {
			if trace[index] != prefix[index] {
				t.Fatalf(
					"%s trace prefix %d = %+v; want %+v",
					name,
					index,
					trace[index],
					prefix[index],
				)
			}
		}
		suffix := trace[len(prefix):]
		if suffix[0].Source != "=[Go]" ||
			suffix[1].Source != luaSource {
			t.Fatalf("%s trace suffix = %+v", name, suffix)
		}
	}
	assertTraceSuffix("inner", innerTrace, nil, "@trace-inner.lua")
	assertTraceSuffix(
		"middle",
		middleTrace,
		innerTrace,
		"@trace-middle.lua",
	)
	assertTraceSuffix(
		"outer",
		failure.Traceback(),
		middleTrace,
		"@trace-outer.lua",
	)
	assertRootThreadReady(t, state.MainThread())
}

func TestWarmFrameCallIntoDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	luaTarget := mustLoadString(
		t,
		state,
		"@warm-nested-lua.lua",
		`return ...`,
	)
	emptyTarget := mustLoadString(
		t,
		state,
		"@warm-nested-empty.lua",
		`return`,
	)
	nativeTarget, err := state.NewNativeFunction(func(frame Frame) Outcome {
		number, ok := frame.Number(0)
		if !ok {
			return frame.ArgTypeError(0, NumberKind)
		}
		return frame.ReturnNumber(number)
	})
	if err != nil {
		t.Fatal(err)
	}
	prebuiltArguments := []Value{Number(41)}

	for _, test := range []struct {
		name   string
		target *Function
		mode   int
	}{
		{name: "Lua literal arguments", target: luaTarget},
		{
			name:   "Lua prebuilt arguments",
			target: luaTarget,
			mode:   1,
		},
		{
			name:   "Lua stack-array arguments",
			target: luaTarget,
			mode:   2,
		},
		{name: "native literal arguments", target: nativeTarget},
		{
			name:   "native prebuilt arguments",
			target: nativeTarget,
			mode:   1,
		},
		{
			name:   "native stack-array arguments",
			target: nativeTarget,
			mode:   2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			nestedDestination := make([]Value, 1)
			outerDestination := make([]Value, 1)
			host, constructionErr := state.NewNativeFunction(
				func(frame Frame) Outcome {
					var stackArguments [1]Value
					var arguments []Value
					switch test.mode {
					case 0:
						arguments = []Value{Number(41)}
					case 1:
						arguments = prebuiltArguments
					case 2:
						stackArguments[0] = Number(41)
						arguments = stackArguments[:]
					default:
						panic("invalid warm argument mode")
					}
					count, callErr := frame.CallInto(
						test.target.Value(),
						arguments,
						nestedDestination,
					)
					if callErr != nil || count != 1 {
						return frame.RaiseString("warm nested call failed")
					}
					return frame.ReturnValue(nestedDestination[0])
				},
			)
			if constructionErr != nil {
				t.Fatal(constructionErr)
			}
			for range 8 {
				count, callErr := state.CallInto(
					host.Value(),
					nil,
					outerDestination,
				)
				if callErr != nil || count != 1 {
					t.Fatalf(
						"warmup = (%d, %v)",
						count,
						callErr,
					)
				}
			}
			allocations := testing.AllocsPerRun(1_000, func() {
				count, callErr := state.CallInto(
					host.Value(),
					nil,
					outerDestination,
				)
				if callErr != nil || count != 1 {
					panic("warm nested CallInto failed")
				}
			})
			if allocations != 0 {
				t.Fatalf(
					"warm nested CallInto allocations = %v; want 0",
					allocations,
				)
			}
			assertTestValues(t, outerDestination, Number(41))
			runtime.KeepAlive(host)
		})
	}

	t.Run("no arguments or results", func(t *testing.T) {
		host, constructionErr := state.NewNativeFunction(
			func(frame Frame) Outcome {
				count, callErr := frame.CallInto(
					emptyTarget.Value(),
					nil,
					nil,
				)
				if callErr != nil || count != 0 {
					return frame.RaiseString("empty nested call failed")
				}
				return frame.Return()
			},
		)
		if constructionErr != nil {
			t.Fatal(constructionErr)
		}
		for range 8 {
			count, callErr := state.CallInto(host.Value(), nil, nil)
			if callErr != nil || count != 0 {
				t.Fatalf("empty warmup = (%d, %v)", count, callErr)
			}
		}
		allocations := testing.AllocsPerRun(1_000, func() {
			count, callErr := state.CallInto(host.Value(), nil, nil)
			if callErr != nil || count != 0 {
				panic("empty nested CallInto failed")
			}
		})
		if allocations != 0 {
			t.Fatalf(
				"empty nested CallInto allocations = %v; want 0",
				allocations,
			)
		}
		runtime.KeepAlive(host)
	})
}

func TestWarmFrameCallAllocationContract(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	empty := mustLoadString(t, state, "@nested-empty.lua", `return`)
	one := mustLoadString(t, state, "@nested-one.lua", `return 41`)

	emptyHost, err := state.NewNativeFunction(func(frame Frame) Outcome {
		results, callErr := frame.Call(empty.Value())
		if callErr != nil || results != nil {
			return frame.RaiseString("zero-result nested Call failed")
		}
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 8 {
		if count, callErr := state.CallInto(
			emptyHost.Value(),
			nil,
			nil,
		); callErr != nil || count != 0 {
			t.Fatalf("zero-result warmup = (%d, %v)", count, callErr)
		}
	}
	allocations := testing.AllocsPerRun(1_000, func() {
		if count, callErr := state.CallInto(
			emptyHost.Value(),
			nil,
			nil,
		); callErr != nil || count != 0 {
			panic("zero-result nested Call failed")
		}
	})
	if allocations != 0 {
		t.Fatalf(
			"zero-result nested Call allocations = %v; want 0",
			allocations,
		)
	}

	outerDestination := make([]Value, 1)
	oneHost, err := state.NewNativeFunction(func(frame Frame) Outcome {
		results, callErr := frame.Call(one.Value())
		if callErr != nil || len(results) != 1 {
			return frame.RaiseString("one-result nested Call failed")
		}
		return frame.ReturnValue(results[0])
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 8 {
		if count, callErr := state.CallInto(
			oneHost.Value(),
			nil,
			outerDestination,
		); callErr != nil || count != 1 {
			t.Fatalf("one-result warmup = (%d, %v)", count, callErr)
		}
	}
	allocations = testing.AllocsPerRun(1_000, func() {
		if count, callErr := state.CallInto(
			oneHost.Value(),
			nil,
			outerDestination,
		); callErr != nil || count != 1 {
			panic("one-result nested Call failed")
		}
	})
	if allocations != 1 {
		t.Fatalf(
			"one-result nested Call allocations = %v; want 1",
			allocations,
		)
	}
	assertTestValues(t, outerDestination, Number(41))
	runtime.KeepAlive(emptyHost)
	runtime.KeepAlive(oneHost)
}

func BenchmarkFrameNestedCall(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	luaTarget := mustLoadString(
		b,
		state,
		"@benchmark-nested-lua.lua",
		`return ...`,
	)
	nativeTarget, err := state.NewNativeFunction(func(frame Frame) Outcome {
		value, _ := frame.Argument(0)
		return frame.ReturnValue(value)
	})
	if err != nil {
		b.Fatal(err)
	}
	callable, err := state.NewTable(0, 0)
	if err != nil {
		b.Fatal(err)
	}
	metatable, err := state.NewTable(0, 1)
	if err != nil {
		b.Fatal(err)
	}
	if err := metatable.RawSetString("__call", nativeTarget.Value()); err != nil {
		b.Fatal(err)
	}
	if err := state.SetMetatable(callable.Value(), metatable); err != nil {
		b.Fatal(err)
	}
	arguments := []Value{Number(41)}

	for _, test := range []struct {
		name     string
		callable Value
	}{
		{name: "Lua", callable: luaTarget.Value()},
		{name: "native", callable: nativeTarget.Value()},
		{name: "callable", callable: callable.Value()},
	} {
		b.Run("CallInto/"+test.name, func(b *testing.B) {
			nestedDestination := make([]Value, 1)
			outerDestination := make([]Value, 1)
			host, constructionErr := state.NewNativeFunction(
				func(frame Frame) Outcome {
					count, callErr := frame.CallInto(
						test.callable,
						arguments,
						nestedDestination,
					)
					if callErr != nil || count != 1 {
						panic("nested benchmark CallInto failed")
					}
					return frame.ReturnValue(nestedDestination[0])
				},
			)
			if constructionErr != nil {
				b.Fatal(constructionErr)
			}
			for range 8 {
				if _, callErr := state.CallInto(
					host.Value(),
					nil,
					outerDestination,
				); callErr != nil {
					b.Fatal(callErr)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, callErr := state.CallInto(
					host.Value(),
					nil,
					outerDestination,
				); callErr != nil {
					b.Fatal(callErr)
				}
			}
			runtime.KeepAlive(host)
		})
	}

	b.Run("Call/Lua", func(b *testing.B) {
		outerDestination := make([]Value, 1)
		host, constructionErr := state.NewNativeFunction(
			func(frame Frame) Outcome {
				results, callErr := frame.Call(
					luaTarget.Value(),
					Number(41),
				)
				if callErr != nil {
					panic(callErr)
				}
				return frame.ReturnValue(results[0])
			},
		)
		if constructionErr != nil {
			b.Fatal(constructionErr)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if _, callErr := state.CallInto(
				host.Value(),
				nil,
				outerDestination,
			); callErr != nil {
				b.Fatal(callErr)
			}
		}
		runtime.KeepAlive(host)
	})
}
