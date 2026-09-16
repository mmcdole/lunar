package lua

import (
	"errors"
	"math"
	"strings"
	"testing"
	"unsafe"
)

func TestNativeFrameRepresentation(t *testing.T) {
	pointerSize := unsafe.Sizeof(uintptr(0))
	if size, want := unsafe.Sizeof(Frame{}), 2*pointerSize+8; size != want {
		t.Fatalf("Frame size = %d bytes; want %d", size, want)
	}
	if size, want := unsafe.Sizeof(Outcome{}), 2*pointerSize+16; size != want {
		t.Fatalf("Outcome size = %d bytes; want %d", size, want)
	}
}

func TestFrameTypedBooleanAndObjectArguments(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	data, err := state.NewUserData("payload")
	if err != nil {
		t.Fatal(err)
	}
	entry := mustLoadString(t, state, "@typed-thread.lua", "return")
	thread, err := state.NewThread(entry.Value())
	if err != nil {
		t.Fatal(err)
	}
	probe, err := state.NewNativeFunction(func(frame Frame) Outcome {
		if value, ok := frame.Bool(0); !ok || value {
			frame.ThrowString("false boolean argument was not accepted")
		}
		if value, ok := frame.Bool(1); !ok || !value {
			frame.ThrowString("true boolean argument was not accepted")
		}
		if _, ok := frame.Bool(2); ok {
			frame.ThrowString("userdata was accepted as a boolean")
		}
		if _, ok := frame.Bool(4); ok {
			frame.ThrowString("missing argument was accepted as a boolean")
		}
		if got, ok := frame.UserData(2); !ok || got != data {
			frame.ThrowString("userdata argument lost canonical identity")
		}
		if _, ok := frame.UserData(3); ok {
			frame.ThrowString("thread was accepted as userdata")
		}
		if got, ok := frame.Thread(3); !ok || got != thread {
			frame.ThrowString("thread argument lost canonical identity")
		}
		if _, ok := frame.Thread(2); ok {
			frame.ThrowString("userdata was accepted as a thread")
		}
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.CallDiscard(
		probe.Value(),
		Bool(false),
		Bool(true),
		data.Value(),
		thread.Value(),
	); err != nil {
		t.Fatal(err)
	}
}

func TestNativeFrameAdjustsAndWritesResults(t *testing.T) {
	tests := []struct {
		name     string
		wanted   int
		callback NativeFunc
		expected []Value
	}{
		{
			name:   "none",
			wanted: allResults,
			callback: func(frame Frame) Outcome {
				return frame.Return()
			},
		},
		{
			name:   "discard",
			wanted: 0,
			callback: func(frame Frame) Outcome {
				return frame.ReturnValues(Number(1), Number(2))
			},
		},
		{
			name:   "open",
			wanted: allResults,
			callback: func(frame Frame) Outcome {
				return frame.ReturnValues(Number(1), Number(2))
			},
			expected: []Value{Number(1), Number(2)},
		},
		{
			name:   "truncate",
			wanted: 1,
			callback: func(frame Frame) Outcome {
				return frame.ReturnValues(Number(1), Number(2))
			},
			expected: []Value{Number(1)},
		},
		{
			name:   "pad",
			wanted: 3,
			callback: func(frame Frame) Outcome {
				return frame.ReturnValues(Number(1), Number(2))
			},
			expected: []Value{Number(1), Number(2), Nil()},
		},
		{
			name:   "nil",
			wanted: allResults,
			callback: func(frame Frame) Outcome {
				return frame.ReturnNil()
			},
			expected: []Value{Nil()},
		},
		{
			name:   "bool",
			wanted: allResults,
			callback: func(frame Frame) Outcome {
				return frame.ReturnBool(true)
			},
			expected: []Value{Bool(true)},
		},
		{
			name:   "number",
			wanted: allResults,
			callback: func(frame Frame) Outcome {
				return frame.ReturnNumber(math.Copysign(0, -1))
			},
			expected: []Value{Number(math.Copysign(0, -1))},
		},
		{
			name:   "string",
			wanted: allResults,
			callback: func(frame Frame) Outcome {
				return frame.ReturnString("result")
			},
			expected: []Value{stateNeutralString("result")},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			function, err := state.NewNativeFunction(test.callback)
			if err != nil {
				t.Fatal(err)
			}
			thread := stageNativeTestCall(
				t,
				state,
				function,
				test.wanted,
				Number(99),
			)
			if failure := invokeNativeCall(thread); failure != nil {
				t.Fatal(failure)
			}
			assertNativeTestResults(t, thread, test.expected...)
		})
	}
}

func TestNativeFrameSkipsDiscardedStringConstruction(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	const text = "discarded native string"
	function, err := state.NewNativeFunction(
		func(frame Frame) Outcome {
			return frame.ReturnString(text)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	thread := stageNativeTestCall(t, state, function, 0)
	if failure := invokeNativeCall(thread); failure != nil {
		t.Fatal(failure)
	}
	hash := hashString(text)
	if found := state.runtime.strings.lookupProtected(text, hash); found.valid() {
		t.Fatal("discarded result entered the protected string cache")
	}
	if found, _, _ := state.runtime.strings.lookupProbation(text, hash); found.valid() {
		t.Fatal("discarded result entered the probationary string cache")
	}
}

func TestNativeFrameSkipsDiscardedValueImport(t *testing.T) {
	t.Run("return", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		value := state.String(strings.Repeat("discarded-return-", 8))
		function, err := state.NewNativeFunction(
			func(frame Frame) Outcome {
				return frame.ReturnValue(value)
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		thread := stageNativeTestCall(t, state, function, 0)
		state.resetCollectionDebt()
		if failure := invokeNativeCall(thread); failure != nil {
			t.Fatal(failure)
		}
		if state.runtime.collection.debt != 0 ||
			state.runtime.collection.attributedStrings != nil {
			t.Fatal("discarded return imported its owning Value")
		}
	})

	t.Run("failed yield", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		value := state.String(strings.Repeat("discarded-yield-", 8))
		function, err := state.NewNativeFunction(
			func(frame Frame) Outcome {
				return frame.YieldValue(value)
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		thread := stageNativeTestCall(t, state, function, allResults)
		state.resetCollectionDebt()
		failure := invokeNativeCall(thread)
		if failure == nil {
			t.Fatal("main-thread YieldValue succeeded")
		}
		if state.runtime.collection.attributedStrings != nil {
			t.Fatal("failed yield imported its owning Value")
		}
		thread.unwindCalls(0)
	})
}

func TestNativeFrameStringRoundTripChargesAtImport(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	var observed string
	function, err := state.NewNativeFunction(
		func(frame Frame) Outcome {
			var ok bool
			observed, ok = frame.String(0)
			if !ok {
				frame.ThrowString("missing string argument")
			}
			return frame.ReturnValue(frame.State().String(observed))
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	thread := stageNativeTestCall(
		t,
		state,
		function,
		allResults,
		Nil(),
	)
	state.resetCollectionDebt()

	text := strings.Repeat("native-string-view-", 8)
	reference := state.runtime.strings.make(text)
	writeSlot(&thread.values[1], stringSlot(reference))
	beforeCall := state.runtime.collection.debt
	if beforeCall != stringRefRetainedBytes(reference) {
		t.Fatalf(
			"runtime string debt = %d; want %d",
			beforeCall,
			stringRefRetainedBytes(reference),
		)
	}
	if state.runtime.collection.attributedStrings != nil {
		t.Fatal("internal runtime string entered the attribution set")
	}

	if failure := invokeNativeCall(thread); failure != nil {
		t.Fatal(failure)
	}
	if observed != text {
		t.Fatalf("Frame.String = %q; want %q", observed, text)
	}
	if got, want := state.runtime.collection.debt,
		beforeCall+stringRefRetainedBytes(reference); got != want {
		t.Fatalf(
			"Frame.String round-trip debt = %d; want %d",
			got,
			want,
		)
	}
	if _, found := state.runtime.collection.attributedStrings[reference]; !found {
		t.Fatal("Frame.String round-trip import did not record attribution")
	}
	if thread.top != 1 ||
		!thread.values[0].isString() ||
		stringSlotText(thread.values[0]) != text {
		t.Fatal("Frame.String round trip returned the wrong compact value")
	}
}

func TestCompactErrorStringAttributionBeginsOnReimport(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	state.resetCollectionDebt()

	text := strings.Repeat("compact-error-", 8)
	reference := state.runtime.strings.make(text)
	compact := stringSlot(reference)
	failure := &Error{
		compactValue:    compact,
		description:     text,
		category:        RuntimeError,
		hasCompactValue: true,
	}
	beforeCatch := state.runtime.collection.debt
	if got := failure.mustValueSlot(state.runtime); !rawSlotEqual(got, compact) {
		t.Fatal("compact error changed identity while staying inside Lua")
	}
	if got := state.runtime.collection.debt; got != beforeCatch {
		t.Fatalf(
			"internal compact error changed debt from %d to %d",
			beforeCatch,
			got,
		)
	}
	if state.runtime.collection.attributedStrings != nil {
		t.Fatal("internal compact error entered the attribution set")
	}

	failure.exposeValue()
	if got := state.runtime.collection.debt; got != beforeCatch {
		t.Fatalf(
			"compact error exposure changed debt from %d to %d",
			beforeCatch,
			got,
		)
	}
	if state.runtime.collection.attributedStrings != nil {
		t.Fatal("compact error exposure created attribution")
	}
	if failure.hasCompactValue || !failure.value.Valid() {
		t.Fatal("compact error was not converted to an owning Value")
	}
	if got, ok := failure.Value().AsString(); !ok || got != text {
		t.Fatalf("exposed error Value = (%q, %v)", got, ok)
	}
	imported, err := state.runtime.importValue(failure.Value())
	if err != nil {
		t.Fatal(err)
	}
	if !rawSlotEqual(imported, compact) {
		t.Fatal("compact error reimport changed identity")
	}
	if got, want := state.runtime.collection.debt,
		beforeCatch+stringRefRetainedBytes(reference); got != want {
		t.Fatalf("compact error reimport debt = %d; want %d", got, want)
	}
	if _, found := state.runtime.collection.attributedStrings[reference]; !found {
		t.Fatal("compact error reimport did not record attribution")
	}
}

func TestProtectedErrorImportsStateNeutralStringOnDemand(t *testing.T) {
	t.Run("uncaught", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		value := state.String(strings.Repeat("uncaught-native-error-", 8))
		function, err := state.NewNativeFunction(
			func(frame Frame) Outcome {
				frame.Throw(value)
				// Unreachable: the throw above does not return.
				return Outcome{}
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		state.resetCollectionDebt()
		if _, err := state.Call(function.Value()); err == nil {
			t.Fatal("uncaught native error succeeded")
		}
		if state.runtime.collection.attributedStrings != nil {
			t.Fatal("uncaught host-facing error entered the Lua heap")
		}
	})

	t.Run("caught", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		if err := state.OpenBase(); err != nil {
			t.Fatal(err)
		}
		value := state.String(strings.Repeat("caught-native-error-", 8))
		compact := slotFromValue(value)
		reference := stringRef{ref: compact.ref, bits: compact.bits}
		function, err := state.NewNativeFunction(
			func(frame Frame) Outcome {
				frame.Throw(value)
				// Unreachable: the throw above does not return.
				return Outcome{}
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := state.RawSetGlobal("raise_host", function.Value()); err != nil {
			t.Fatal(err)
		}
		caller := mustLoadString(
			t,
			state,
			"@caught-native-error.lua",
			`local ok, message = pcall(raise_host)
return ok, message`,
		)
		results := make([]Value, 2)
		state.resetCollectionDebt()
		if _, err := state.CallInto(caller.Value(), nil, results); err != nil {
			t.Fatal(err)
		}
		if truth := results[0].Truth(); truth {
			t.Fatal("pcall reported success")
		}
		if got, ok := results[1].AsString(); !ok || got != value.String() {
			t.Fatalf("caught error = (%q, %v)", got, ok)
		}
		if _, found := state.runtime.collection.attributedStrings[reference]; !found {
			t.Fatal("caught error bypassed long-string attribution")
		}
		if state.runtime.collection.debt < stringRefRetainedBytes(reference) {
			t.Fatalf(
				"caught error debt = %d; want at least %d",
				state.runtime.collection.debt,
				stringRefRetainedBytes(reference),
			)
		}
		if err := state.Collect(); err != nil {
			t.Fatal(err)
		}
		if state.runtime.collection.attributedStrings != nil {
			t.Fatal("collection retained a returned-only error string")
		}
	})
}

func TestNativeFrameRejectsInvalidOutcomesAndStaleUse(t *testing.T) {
	t.Run("zero outcome", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		function, err := state.NewNativeFunction(
			func(Frame) Outcome { return Outcome{} },
		)
		if err != nil {
			t.Fatal(err)
		}
		thread := stageNativeTestCall(t, state, function, allResults)
		failure := invokeNativeCall(thread)
		if failure == nil ||
			!strings.Contains(failure.Error(), "invalid outcome") {
			t.Fatalf("failure = %v", failure)
		}
		if thread.activeNativeToken != 0 || len(thread.frames) != 1 {
			t.Fatal("invalid Outcome did not leave clean token and unwindable frame")
		}
		thread.unwindCalls(0)
	})

	t.Run("terminal frame and close guard", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		var retained Frame
		function, err := state.NewNativeFunction(
			func(frame Frame) Outcome {
				retained = frame
				outcome := frame.Return()
				if err := state.Close(); !errors.Is(err, ErrRunning) {
					t.Fatalf("Close after terminal Outcome = %v", err)
				}
				assertNativePanic(t, func() {
					frame.Return()
				})
				return outcome
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		thread := stageNativeTestCall(t, state, function, allResults)
		if failure := invokeNativeCall(thread); failure != nil {
			t.Fatal(failure)
		}
		assertNativePanic(t, func() {
			retained.Argument(0)
		})
		if err := state.Close(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("wrong invocation outcome", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		var previous Outcome
		first, err := state.NewNativeFunction(
			func(frame Frame) Outcome {
				previous = frame.Return()
				return previous
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		thread := stageNativeTestCall(t, state, first, allResults)
		if failure := invokeNativeCall(thread); failure != nil {
			t.Fatal(failure)
		}

		second, err := state.NewNativeFunction(
			func(Frame) Outcome { return previous },
		)
		if err != nil {
			t.Fatal(err)
		}
		thread = stageNativeTestCall(t, state, second, allResults)
		failure := invokeNativeCall(thread)
		if failure == nil ||
			!strings.Contains(failure.Error(), "invalid outcome") {
			t.Fatalf("failure = %v", failure)
		}
		thread.unwindCalls(0)
	})

	t.Run("foreign runtime outcome", func(t *testing.T) {
		firstState, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer firstState.Close()
		var previous Outcome
		first, err := firstState.NewNativeFunction(
			func(frame Frame) Outcome {
				previous = frame.Return()
				return previous
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		thread := stageNativeTestCall(
			t,
			firstState,
			first,
			allResults,
		)
		if failure := invokeNativeCall(thread); failure != nil {
			t.Fatal(failure)
		}

		secondState, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer secondState.Close()
		second, err := secondState.NewNativeFunction(
			func(Frame) Outcome { return previous },
		)
		if err != nil {
			t.Fatal(err)
		}
		thread = stageNativeTestCall(
			t,
			secondState,
			second,
			allResults,
		)
		failure := invokeNativeCall(thread)
		if failure == nil ||
			!strings.Contains(failure.Error(), "invalid outcome") {
			t.Fatalf("failure = %v", failure)
		}
		thread.unwindCalls(0)
	})
}

func TestNativeFramePreflightsResultsAndLimits(t *testing.T) {
	t.Run("invalid Value is atomic", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		function, err := state.NewNativeFunction(
			func(frame Frame) Outcome {
				return frame.ReturnValues(Number(1), Value{})
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		caller := compileTestFunction(t, state, "@caller.lua", `return 0`)
		thread := state.main
		thread.reserveValues(int(caller.prototype.registers))
		thread.values[0] = slotFromFunctionObject(caller)
		thread.top = 1
		if failure := thread.pushFunctionCall(
			caller,
			0,
			0,
			allResults,
		); failure != nil {
			t.Fatal(failure)
		}
		callBase := int(thread.frames[0].base)
		functionObject := function.runtimeObject()
		writeSlot(
			&thread.values[callBase],
			slotFromFunctionObject(functionObject),
		)
		writeSlot(&thread.values[callBase+1], numberSlot(8))
		if failure := thread.pushFunctionCall(
			functionObject,
			callBase,
			1,
			allResults,
		); failure != nil {
			t.Fatal(failure)
		}
		callable := thread.values[callBase]
		assertNativePanic(t, func() {
			_ = invokeNativeCall(thread)
		})
		if thread.activeNativeToken != 0 ||
			len(thread.frames) != 1 ||
			!rawSlotEqual(thread.values[callBase], callable) {
			t.Fatal("failed result preflight changed result storage or token")
		}
		thread.unwindCalls(0)
	})

	t.Run("open result limit", func(t *testing.T) {
		state, err := New(Options{MaxValues: 3})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		function, err := state.NewNativeFunction(
			func(frame Frame) Outcome {
				return frame.ReturnValues(
					Number(1),
					Number(2),
					Number(3),
					Number(4),
				)
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		thread := stageNativeTestCall(t, state, function, allResults)
		failure := invokeNativeCall(thread)
		if failure == nil || failure.Category() != ResourceError {
			t.Fatalf("failure = %v; want ResourceError", failure)
		}
		if len(thread.frames) != 1 || thread.activeNativeToken != 0 {
			t.Fatal("resource failure did not preserve unwindable native activation")
		}
		thread.unwindCalls(0)
	})
}

func TestWarmNativeFrameCallDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	function, err := state.NewNativeFunction(
		func(frame Frame) Outcome {
			value, ok := frame.Number(0)
			if !ok {
				frame.ThrowArgTypeError(0, NumberKind)
			}
			return frame.ReturnValues(
				Number(value+1),
				Number(value+2),
			)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	thread := state.main
	thread.reserveValues(16)
	thread.reserveFrames(8)
	functionObject := function.runtimeObject()

	run := func() {
		oldExtent := thread.liveValueExtent()
		thread.top = 2
		thread.frameExtent = 0
		thread.clearInactive(0, oldExtent)
		thread.values[0] = slotFromFunctionObject(functionObject)
		thread.values[1] = numberSlot(41)
		if failure := thread.pushFunctionCall(
			functionObject,
			0,
			1,
			2,
		); failure != nil {
			panic(failure)
		}
		if failure := invokeNativeCall(thread); failure != nil {
			panic(failure)
		}
		if thread.top != 2 ||
			math.Float64frombits(thread.values[0].bits) != 42 ||
			math.Float64frombits(thread.values[1].bits) != 43 {
			panic("unexpected native result")
		}
	}
	run()
	if allocations := testing.AllocsPerRun(1000, run); allocations != 0 {
		t.Fatalf("warm native call allocations = %v; want 0", allocations)
	}
}

func BenchmarkNativeFrameOutcomes(b *testing.B) {
	tests := []struct {
		name     string
		callback NativeFunc
		results  int
	}{
		{
			name: "no results",
			callback: func(frame Frame) Outcome {
				return frame.Return()
			},
		},
		{
			name: "scalar result",
			callback: func(frame Frame) Outcome {
				return frame.ReturnNumber(42)
			},
			results: 1,
		},
		{
			name: "two Value results",
			callback: func(frame Frame) Outcome {
				return frame.ReturnValues(Number(1), Number(2))
			},
			results: 2,
		},
		{
			name: "closure Value",
			callback: func(frame Frame) Outcome {
				return frame.ReturnValue(Number(42))
			},
			results: 1,
		},
	}

	for _, test := range tests {
		b.Run(test.name, func(b *testing.B) {
			state, err := New(Options{})
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() {
				_ = state.Close()
			})
			function, err := state.NewNativeFunction(test.callback)
			if err != nil {
				b.Fatal(err)
			}
			thread := state.main
			thread.reserveValues(16)
			thread.reserveFrames(8)
			functionObject := function.runtimeObject()

			run := func() {
				oldExtent := thread.liveValueExtent()
				thread.top = 1
				thread.frameExtent = 0
				thread.clearInactive(0, oldExtent)
				thread.values[0] = slotFromFunctionObject(functionObject)
				if failure := thread.pushFunctionCall(
					functionObject,
					0,
					0,
					allResults,
				); failure != nil {
					panic(failure)
				}
				if failure := invokeNativeCall(thread); failure != nil {
					panic(failure)
				}
				if thread.top != test.results {
					panic("unexpected native result count")
				}
			}
			run()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				run()
			}
		})
	}
}
