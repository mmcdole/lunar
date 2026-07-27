package lua

import (
	"errors"
	"math"
	"runtime"
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

func TestNativeFunctionConstructionAndCaptureOwnership(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	table, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	captures := []Value{Number(7), table.Value()}
	function, err := state.NewNativeFunction(
		func(frame Frame) Outcome {
			return frame.Return()
		},
		captures...,
	)
	if err != nil {
		t.Fatal(err)
	}
	captures[0] = Number(99)

	if function.Prototype() != nil {
		t.Fatal("native Function has a Prototype")
	}
	if function.UpvalueCount() != 2 {
		t.Fatalf("capture count = %d; want 2", function.UpvalueCount())
	}
	object := function.runtimeObject()
	if object.nativeBody() == nil {
		t.Fatal("native Function did not retain its immutable executable kind")
	}
	body := object.nativeBody()
	if got := body.captures[0].owningValue(); !rawEqual(got, Number(7)) {
		t.Fatalf("capture 0 = %v; want 7", got)
	}
	if got, ok := body.captures[1].owningValue().Table(); !ok || got != table {
		t.Fatalf("capture 1 = (%p, %v); want %p", got, ok, table)
	}
	environment, err := state.FunctionEnvironment(function)
	if err != nil {
		t.Fatal(err)
	}
	if environment.runtimeObject() != state.main.globals {
		t.Fatal("native Function did not use the State global environment")
	}
	if got, ok := function.Value().Function(); !ok || got != function {
		t.Fatal("native Function did not preserve canonical identity")
	}

	if _, err := state.NewNativeFunction(nil); !errors.Is(err, ErrInvalidNativeFunction) {
		t.Fatalf("nil entry error = %v", err)
	}
	tooMany := make([]Value, maxNativeCaptures+1)
	for index := range tooMany {
		tooMany[index] = Nil()
	}
	if _, err := state.NewNativeFunction(
		func(frame Frame) Outcome { return frame.Return() },
		tooMany...,
	); !errors.Is(err, ErrNativeCaptureLimit) {
		t.Fatalf("capture-limit error = %v", err)
	}
	if _, err := state.NewNativeFunction(
		func(frame Frame) Outcome { return frame.Return() },
		Value{},
	); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("invalid capture error = %v", err)
	}

	foreign, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Close()
	foreignTable, err := foreign.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.NewNativeFunction(
		func(frame Frame) Outcome { return frame.Return() },
		foreignTable.Value(),
	); !errors.Is(err, ErrForeignValue) {
		t.Fatalf("foreign capture error = %v", err)
	}
}

func TestConstructionUsesFunctionAndThreadEnvironmentsByObjectKind(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	functionEnvironment, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	threadEnvironment, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	prototype, err := Compile("@constructed-prototype.lua", "return 43")
	if err != nil {
		t.Fatal(err)
	}

	var createdNative *Function
	var createdData *UserData
	var loaded *Function
	var loadedPrototype *Function
	constructor, err := state.NewNativeFunction(func(frame Frame) Outcome {
		if frame.Environment() != functionEnvironment {
			return frame.RaiseString("callback lost its function environment")
		}
		if frame.GlobalEnvironment() != threadEnvironment {
			return frame.RaiseString("callback lost its thread environment")
		}

		var constructionErr error
		createdNative, constructionErr = state.NewNativeFunction(
			func(inner Frame) Outcome {
				return inner.Return()
			},
		)
		if constructionErr != nil {
			return frame.RaiseString(constructionErr.Error())
		}
		createdData, constructionErr = state.NewUserData("created")
		if constructionErr != nil {
			return frame.RaiseString(constructionErr.Error())
		}
		loaded, constructionErr = state.LoadString(
			"@constructed.lua",
			"return 42",
		)
		if constructionErr != nil {
			return frame.RaiseString(constructionErr.Error())
		}
		loadedPrototype, constructionErr = state.LoadPrototype(prototype)
		if constructionErr != nil {
			return frame.RaiseString(constructionErr.Error())
		}
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetFunctionEnvironment(
		constructor,
		functionEnvironment,
	); err != nil {
		t.Fatal(err)
	}

	thread, err := state.NewThread(constructor.Value())
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetThreadEnvironment(thread, threadEnvironment); err != nil {
		t.Fatal(err)
	}
	results, status, err := thread.Resume()
	if err != nil || status != ThreadDead || len(results) != 0 {
		t.Fatalf(
			"constructor resume = (results=%v, status=%v, err=%v)",
			results,
			status,
			err,
		)
	}
	if createdNative == nil ||
		createdData == nil ||
		loaded == nil ||
		loadedPrototype == nil {
		t.Fatal("callback did not construct every object")
	}

	if environment, environmentErr := state.FunctionEnvironment(
		createdNative,
	); environmentErr != nil || environment != functionEnvironment {
		t.Fatalf(
			"created native environment = (%p, %v); want %p",
			environment,
			environmentErr,
			functionEnvironment,
		)
	}
	if environment, environmentErr := state.UserDataEnvironment(
		createdData,
	); environmentErr != nil || environment != functionEnvironment {
		t.Fatalf(
			"created userdata environment = (%p, %v); want %p",
			environment,
			environmentErr,
			functionEnvironment,
		)
	}
	if environment, environmentErr := state.FunctionEnvironment(
		loaded,
	); environmentErr != nil || environment != threadEnvironment {
		t.Fatalf(
			"loaded function environment = (%p, %v); want %p",
			environment,
			environmentErr,
			threadEnvironment,
		)
	}
	if environment, environmentErr := state.FunctionEnvironment(
		loadedPrototype,
	); environmentErr != nil || environment != threadEnvironment {
		t.Fatalf(
			"LoadPrototype environment = (%p, %v); want %p",
			environment,
			environmentErr,
			threadEnvironment,
		)
	}
}

func TestNativeFrameTypedArgumentsAndCaptures(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	table, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	data, err := state.NewUserData("payload")
	if err != nil {
		t.Fatal(err)
	}
	other, err := state.NewNativeFunction(
		func(frame Frame) Outcome { return frame.Return() },
	)
	if err != nil {
		t.Fatal(err)
	}

	var function *Function
	function, err = state.NewNativeFunction(
		func(frame Frame) Outcome {
			if frame.ArgumentCount() != 8 {
				t.Fatalf("argument count = %d; want 8", frame.ArgumentCount())
			}
			if frame.State() != state || frame.Thread() != state.MainThread() {
				t.Fatal("Frame did not expose its executing State and Thread")
			}
			if frame.Environment().runtimeObject() != state.main.globals {
				t.Fatal("Frame did not expose its function environment")
			}
			if value, ok := frame.Bool(0); !ok || !value {
				t.Fatalf("Bool(0) = (%v, %v)", value, ok)
			}
			if value, ok := frame.Number(1); !ok || value != 12.5 {
				t.Fatalf("Number(1) = (%v, %v)", value, ok)
			}
			if value, ok := frame.String(2); !ok || value != "text" {
				t.Fatalf("String(2) = (%q, %v)", value, ok)
			}
			if value, ok := frame.Table(3); !ok || value != table {
				t.Fatalf("Table(3) = (%p, %v)", value, ok)
			}
			if value, ok := frame.Function(4); !ok || value != other {
				t.Fatalf("Function(4) = (%p, %v)", value, ok)
			}
			if value, ok := frame.UserData(5); !ok || value != data {
				t.Fatalf("UserData(5) = (%p, %v)", value, ok)
			}
			if value, ok := frame.LuaThread(6); !ok || value != state.MainThread() {
				t.Fatalf("LuaThread(6) = (%p, %v)", value, ok)
			}
			if _, ok := frame.Number(0); ok {
				t.Fatal("typed read coerced a boolean to a number")
			}
			if _, ok := frame.String(8); ok {
				t.Fatal("missing argument passed an exact typed read")
			}
			explicitNil, present := frame.Argument(7)
			if !present || !explicitNil.IsNil() || frame.Kind(7) != NilKind {
				t.Fatal("explicit nil argument lost its Lua kind")
			}
			missing, present := frame.Argument(8)
			if present || !missing.IsNil() || frame.Kind(8) != InvalidKind {
				t.Fatal("missing argument did not retain no-value kind")
			}
			if frame.CaptureCount() != 2 {
				t.Fatalf("capture count = %d; want 2", frame.CaptureCount())
			}
			if value, ok := frame.Capture(0).AsNumber(); !ok || value != 4 {
				t.Fatalf("capture 0 = (%v, %v)", value, ok)
			}
			if value, ok := frame.Capture(1).AsString(); !ok || value != "old" {
				t.Fatalf("capture 1 = (%q, %v)", value, ok)
			}
			frame.SetCapture(1, state.String("new"))
			return frame.ReturnValues(
				Number(17),
				frame.Capture(1),
			)
		},
		Number(4),
		state.String("old"),
	)
	if err != nil {
		t.Fatal(err)
	}

	thread := stageNativeTestCall(
		t,
		state,
		function,
		allResults,
		Bool(true),
		Number(12.5),
		state.String("text"),
		table.Value(),
		other.Value(),
		data.Value(),
		state.MainThread().Value(),
		Nil(),
	)
	if failure := invokeNativeCall(thread); failure != nil {
		t.Fatal(failure)
	}
	assertNativeTestResults(t, thread, Number(17), state.String("new"))
	if value, ok := function.runtimeObject().
		nativeBody().captures[1].owningValue().AsString(); !ok ||
		value != "new" {
		t.Fatalf("updated capture = (%q, %v)", value, ok)
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

func TestExecutorPropagatesOpenNativeResults(t *testing.T) {
	many := NativeFunc(func(frame Frame) Outcome {
		return frame.ReturnValues(Number(1), Number(2))
	})
	none := NativeFunc(func(frame Frame) Outcome {
		return frame.Return()
	})
	tests := []struct {
		name     string
		source   string
		callback NativeFunc
		expected []Value
	}{
		{
			name:     "return suffix",
			source:   `return 9, host()`,
			callback: many,
			expected: []Value{Number(9), Number(1), Number(2)},
		},
		{
			name: "call arguments",
			source: `
local function consume(a, b)
	return a, b
end
return consume(host())
`,
			callback: many,
			expected: []Value{Number(1), Number(2)},
		},
		{
			name: "constructor suffix",
			source: `
local values = {9, host()}
return values[1], values[2], values[3]
`,
			callback: many,
			expected: []Value{Number(9), Number(1), Number(2)},
		},
		{
			name: "tail forwarding",
			source: `
local function forward()
	return host()
end
return forward()
`,
			callback: many,
			expected: []Value{Number(1), Number(2)},
		},
		{
			name: "nested open tail suffix",
			source: `
local function forward()
	return host()
end
return 9, forward()
`,
			callback: many,
			expected: []Value{Number(9), Number(1), Number(2)},
		},
		{
			name:     "zero-result suffix",
			source:   `return 9, host()`,
			callback: none,
			expected: []Value{Number(9)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			host, err := state.NewNativeFunction(test.callback)
			if err != nil {
				t.Fatal(err)
			}
			if err := state.SetGlobal("host", host.Value()); err != nil {
				t.Fatal(err)
			}
			chunk := compileTestFunction(
				t,
				state,
				"@native-open-results.lua",
				test.source,
			)
			thread, result := executeTestFunction(t, state, chunk)
			assertExecutionReturned(t, result)
			assertExecutionValues(t, thread, test.expected...)
			if len(thread.frames) != 0 ||
				len(thread.continuations) != 0 {
				t.Fatal("open native result retained execution state")
			}
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
				return frame.RaiseString("missing string argument")
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
				return frame.Raise(value)
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
				return frame.Raise(value)
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := state.SetGlobal("raise_host", function.Value()); err != nil {
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

func TestNativeFrameRaisesProtectedErrors(t *testing.T) {
	t.Run("arbitrary value", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		raised := state.String("native failure")
		function, err := state.NewNativeFunction(
			func(frame Frame) Outcome {
				return frame.Raise(raised)
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		thread := stageNativeTestCall(t, state, function, allResults)
		failure := invokeNativeCall(thread)
		if failure == nil ||
			failure.Category() != RuntimeError ||
			!rawEqual(failure.Value(), raised) ||
			failure.Error() != "native failure" {
			t.Fatalf("failure = %#v", failure)
		}
		if len(thread.frames) != 1 || thread.activeNativeToken != 0 {
			t.Fatal("native failure did not leave one unwindable activation")
		}
		thread.unwindCalls(0)
	})

	t.Run("argument type", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		function, err := state.NewNativeFunction(
			func(frame Frame) Outcome {
				if _, ok := frame.Number(1); !ok {
					return frame.ArgTypeError(1, NumberKind)
				}
				return frame.Return()
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		thread := stageNativeTestCall(t, state, function, allResults, Bool(true))
		failure := invokeNativeCall(thread)
		if failure == nil ||
			!strings.Contains(
				failure.Error(),
				"bad argument #2 (number expected, got no value)",
			) {
			t.Fatalf("failure = %v", failure)
		}
		thread.unwindCalls(0)
	})

	t.Run("argument reason", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		function, err := state.NewNativeFunction(
			func(frame Frame) Outcome {
				return frame.ArgError(0, "value must be positive")
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		thread := stageNativeTestCall(t, state, function, allResults, Number(-1))
		failure := invokeNativeCall(thread)
		if failure == nil ||
			failure.Error() != "bad argument #1 (value must be positive)" {
			t.Fatalf("failure = %v", failure)
		}
		thread.unwindCalls(0)
	})

	t.Run("string error", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		function, err := state.NewNativeFunction(
			func(frame Frame) Outcome {
				return frame.RaiseString("native string failure")
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		thread := stageNativeTestCall(t, state, function, allResults)
		failure := invokeNativeCall(thread)
		if failure == nil || failure.Error() != "native string failure" {
			t.Fatalf("failure = %v", failure)
		}
		thread.unwindCalls(0)
	})

	t.Run("executor traceback", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		host, err := state.NewNativeFunction(
			func(frame Frame) Outcome {
				return frame.Raise(state.String("native traceback"))
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := state.SetGlobal("host", host.Value()); err != nil {
			t.Fatal(err)
		}
		chunk := compileTestFunction(t, state, "@native-trace.lua", `
local function inner()
	host()
	return 1
end
local function outer()
	local result = inner()
	return result
end
return outer()
`)
		thread, result := executeTestFunction(t, state, chunk)
		if result.kind != executionFailed ||
			result.err == nil ||
			result.err.Error() != "native traceback" {
			t.Fatalf("execution result = %+v", result)
		}
		traceback := result.err.Traceback()
		if len(traceback) < 3 ||
			traceback[0].Source != "=[Go]" ||
			traceback[0].Function != "native function" ||
			traceback[1].Source != "@native-trace.lua" {
			t.Fatalf("native traceback = %+v", traceback)
		}
		if len(thread.frames) != 0 ||
			len(thread.continuations) != 0 ||
			thread.top != 0 ||
			thread.frameExtent != 0 {
			t.Fatal("native failure left executable state")
		}
	})
}

func TestNativeErrorsUnwindEveryContinuationMode(t *testing.T) {
	tests := []struct {
		name   string
		event  string
		source string
	}{
		{
			name:   "store result",
			event:  "__index",
			source: `return target.missing`,
		},
		{
			name:   "discard result",
			event:  "__newindex",
			source: `target.missing = 1`,
		},
		{
			name:   "comparison",
			event:  "__lt",
			source: `return left < right`,
		},
		{
			name:   "concatenation",
			event:  "__concat",
			source: `return left .. right`,
		},
		{
			name:   "iterator",
			source: `for key in iterator, nil, nil do end`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			failing, err := state.NewNativeFunction(
				func(frame Frame) Outcome {
					return frame.RaiseString("continued native failure")
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			newTable := func(name string) *Table {
				t.Helper()
				table, tableErr := state.NewTable(0, 0)
				if tableErr != nil {
					t.Fatal(tableErr)
				}
				if tableErr = state.SetGlobal(
					name,
					table.Value(),
				); tableErr != nil {
					t.Fatal(tableErr)
				}
				return table
			}
			install := func(
				event string,
				values ...Value,
			) {
				t.Helper()
				metatable, tableErr := state.NewTable(0, 1)
				if tableErr != nil {
					t.Fatal(tableErr)
				}
				if tableErr = metatable.RawSetString(
					event,
					failing.Value(),
				); tableErr != nil {
					t.Fatal(tableErr)
				}
				for _, value := range values {
					if tableErr = state.SetMetatable(
						value,
						metatable,
					); tableErr != nil {
						t.Fatal(tableErr)
					}
				}
			}

			switch test.event {
			case "__index", "__newindex":
				target := newTable("target")
				install(test.event, target.Value())
			case "__lt":
				left := newTable("left")
				right := newTable("right")
				install(test.event, left.Value(), right.Value())
			case "__concat":
				left := newTable("left")
				newTable("right")
				install(test.event, left.Value())
			case "":
				if err := state.SetGlobal(
					"iterator",
					failing.Value(),
				); err != nil {
					t.Fatal(err)
				}
			default:
				t.Fatalf("unknown event %q", test.event)
			}

			chunk := compileTestFunction(
				t,
				state,
				"@native-continuation-error.lua",
				test.source,
			)
			thread, result := executeTestFunction(t, state, chunk)
			if result.kind != executionFailed ||
				result.err == nil ||
				!strings.Contains(
					result.err.Error(),
					"continued native failure",
				) {
				t.Fatalf("execution result = %#v", result)
			}
			traceback := result.err.Traceback()
			if len(traceback) < 2 ||
				traceback[0].Source != "=[Go]" {
				t.Fatalf("traceback = %#v", traceback)
			}
			if thread.activeNativeToken != 0 ||
				len(thread.frames) != 0 ||
				len(thread.continuations) != 0 ||
				thread.top != 0 ||
				thread.frameExtent != 0 ||
				thread.openUpvalues != nil {
				t.Fatal("native continuation failure left executable state")
			}
		})
	}
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

func TestNativeCallbackPanicCleansEveryTransition(t *testing.T) {
	tests := []struct {
		name            string
		source          string
		afterTerminal   bool
		indexMetamethod bool
		remainingCalls  int
	}{
		{
			name: "ordinary call",
			source: `
local retained = 23
local result = host(retained)
return result
`,
			remainingCalls: 1,
		},
		{
			name: "after terminal outcome",
			source: `
local result = host()
return result
`,
			afterTerminal:  true,
			remainingCalls: 1,
		},
		{
			name:            "metamethod continuation",
			source:          `return target.missing`,
			indexMetamethod: true,
			remainingCalls:  1,
		},
		{
			name:           "root tail call",
			source:         `return host()`,
			remainingCalls: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			host, err := state.NewNativeFunction(
				func(frame Frame) Outcome {
					if test.afterTerminal {
						_ = frame.ReturnNumber(1)
					}
					panic("host panic")
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := state.SetGlobal("host", host.Value()); err != nil {
				t.Fatal(err)
			}
			if test.indexMetamethod {
				target, tableErr := state.NewTable(0, 0)
				if tableErr != nil {
					t.Fatal(tableErr)
				}
				metatable, tableErr := state.NewTable(0, 1)
				if tableErr != nil {
					t.Fatal(tableErr)
				}
				if tableErr = metatable.RawSetString(
					"__index",
					host.Value(),
				); tableErr != nil {
					t.Fatal(tableErr)
				}
				if tableErr = state.SetMetatable(
					target.Value(),
					metatable,
				); tableErr != nil {
					t.Fatal(tableErr)
				}
				if tableErr = state.SetGlobal(
					"target",
					target.Value(),
				); tableErr != nil {
					t.Fatal(tableErr)
				}
			}

			caller := compileTestFunction(
				t,
				state,
				"@native-panic.lua",
				test.source,
			)
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

			recovered := func() (recovered any) {
				defer func() {
					recovered = recover()
				}()
				_ = runTestExecutor(t, thread, 0)
				return nil
			}()
			if recovered != "host panic" {
				t.Fatalf("panic = %v; want host panic", recovered)
			}
			if thread.activeNativeToken != 0 ||
				len(thread.continuations) != 0 ||
				len(thread.frames) != test.remainingCalls {
				t.Fatal("callback panic left borrowed execution state")
			}
			if test.remainingCalls != 0 {
				frame := thread.frames[0]
				wantExtent := int(frame.base) +
					int(frame.function.prototype.registers)
				if frame.function != caller ||
					thread.top != wantExtent ||
					thread.frameExtent != wantExtent {
					t.Fatal("callback panic did not restore the Lua caller")
				}
			} else if thread.top != 0 || thread.frameExtent != 0 {
				t.Fatal("tail callback panic retained a root stack")
			}

			thread.unwindCalls(0)
			if thread.top != 0 ||
				thread.frameExtent != 0 ||
				thread.openUpvalues != nil {
				t.Fatal("callback panic cleanup left executable roots")
			}
			for _, value := range thread.values {
				if value != (slot{}) {
					t.Fatal("callback panic retained a dead stack value")
				}
			}
		})
	}
}

func TestExecutorUsesNativeFunctionsAtEveryCallSeam(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	native := func(entry NativeFunc) *Function {
		t.Helper()
		function, functionErr := state.NewNativeFunction(entry)
		if functionErr != nil {
			t.Fatal(functionErr)
		}
		return function
	}
	installMetamethod := func(value Value, name string, function *Function) {
		t.Helper()
		metatable, tableErr := state.NewTable(0, 1)
		if tableErr != nil {
			t.Fatal(tableErr)
		}
		if tableErr = metatable.RawSetString(name, function.Value()); tableErr != nil {
			t.Fatal(tableErr)
		}
		if tableErr = state.SetMetatable(value, metatable); tableErr != nil {
			t.Fatal(tableErr)
		}
	}
	newGlobalTable := func(name string) *Table {
		t.Helper()
		table, tableErr := state.NewTable(0, 1)
		if tableErr != nil {
			t.Fatal(tableErr)
		}
		if tableErr = state.SetGlobal(name, table.Value()); tableErr != nil {
			t.Fatal(tableErr)
		}
		return table
	}

	callable := newGlobalTable("callable")
	installMetamethod(
		callable.Value(),
		"__call",
		native(func(frame Frame) Outcome {
			value, ok := frame.Number(1)
			if !ok {
				return frame.ArgTypeError(1, NumberKind)
			}
			return frame.ReturnNumber(value + 1)
		}),
	)

	indexed := newGlobalTable("indexed")
	installMetamethod(
		indexed.Value(),
		"__index",
		native(func(frame Frame) Outcome {
			key, _ := frame.Argument(1)
			return frame.ReturnValue(key)
		}),
	)

	assigned := newGlobalTable("assigned")
	installMetamethod(
		assigned.Value(),
		"__newindex",
		native(func(frame Frame) Outcome {
			target, ok := frame.Table(0)
			if !ok {
				return frame.ArgTypeError(0, TableKind)
			}
			key, _ := frame.Argument(1)
			value, _ := frame.Argument(2)
			if setErr := target.RawSet(key, value); setErr != nil {
				return frame.Raise(state.String(setErr.Error()))
			}
			return frame.Return()
		}),
	)

	addLeft := newGlobalTable("add_left")
	newGlobalTable("add_right")
	installMetamethod(
		addLeft.Value(),
		"__add",
		native(func(frame Frame) Outcome {
			return frame.ReturnNumber(42)
		}),
	)

	concatLeft := newGlobalTable("concat_left")
	newGlobalTable("concat_right")
	installMetamethod(
		concatLeft.Value(),
		"__concat",
		native(func(frame Frame) Outcome {
			return frame.ReturnString("joined")
		}),
	)

	lengthValue, err := state.NewUserData("length")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("length_value", lengthValue.Value()); err != nil {
		t.Fatal(err)
	}
	installMetamethod(
		lengthValue.Value(),
		"__len",
		native(func(frame Frame) Outcome {
			return frame.ReturnNumber(9)
		}),
	)

	lessThan := native(func(frame Frame) Outcome {
		return frame.ReturnBool(true)
	})
	compareMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareMetatable.RawSetString("__lt", lessThan.Value()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"compare_left", "compare_right"} {
		value := newGlobalTable(name)
		if err := state.SetMetatable(value.Value(), compareMetatable); err != nil {
			t.Fatal(err)
		}
	}

	iterator := native(func(frame Frame) Outcome {
		control, ok := frame.Number(1)
		if !ok {
			return frame.ArgTypeError(1, NumberKind)
		}
		control++
		if control > 3 {
			return frame.ReturnNil()
		}
		return frame.ReturnValues(
			Number(control),
			Number(control*2),
		)
	})
	if err := state.SetGlobal("iterator", iterator.Value()); err != nil {
		t.Fatal(err)
	}

	method := native(func(frame Frame) Outcome {
		if value, ok := frame.Table(0); !ok || value == nil {
			return frame.ArgTypeError(0, TableKind)
		}
		number, ok := frame.Number(1)
		if !ok {
			return frame.ArgTypeError(1, NumberKind)
		}
		return frame.ReturnNumber(number * 2)
	})
	receiver := newGlobalTable("receiver")
	if err := receiver.RawSetString("method", method.Value()); err != nil {
		t.Fatal(err)
	}

	chunk := compileTestFunction(t, state, "@native-seams.lua", `
assigned.saved = 6
local sum = 0
for key, value in iterator, nil, 0 do
	sum = sum + key + value
end
return callable(4),
	indexed.answer,
	assigned.saved,
	add_left + add_right,
	concat_left .. concat_right,
	#length_value,
	compare_left < compare_right,
	sum,
	receiver:method(8)
`)
	thread, result := executeTestFunction(t, state, chunk)
	assertExecutionReturned(t, result)
	assertExecutionValues(
		t,
		thread,
		Number(5),
		state.String("answer"),
		Number(6),
		Number(42),
		state.String("joined"),
		Number(9),
		Bool(true),
		Number(18),
		Number(16),
	)
	if len(thread.continuations) != 0 {
		t.Fatal("native calls retained an execution continuation")
	}
}

func TestExecutorCallsAndTailCallsNativeFunctions(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{name: "call", source: `return host_add(20, 22)`},
		{name: "tail call", source: `local function run() return host_add(20, 22) end return run()`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			add, err := state.NewNativeFunction(
				func(frame Frame) Outcome {
					left, leftOK := frame.Number(0)
					if !leftOK {
						return frame.ArgTypeError(0, NumberKind)
					}
					right, rightOK := frame.Number(1)
					if !rightOK {
						return frame.ArgTypeError(1, NumberKind)
					}
					return frame.ReturnNumber(left + right)
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := state.SetGlobal("host_add", add.Value()); err != nil {
				t.Fatal(err)
			}
			chunk := compileTestFunction(t, state, "@native.lua", test.source)
			thread, result := executeTestFunction(t, state, chunk)
			assertExecutionReturned(t, result)
			assertExecutionValues(t, thread, Number(42))
		})
	}
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
				return frame.ArgTypeError(0, NumberKind)
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

func BenchmarkExecutorNativeCall(b *testing.B) {
	const iterations = 1000
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})

	native, err := state.NewNativeFunction(
		func(frame Frame) Outcome {
			value, ok := frame.Number(0)
			if !ok {
				return frame.ArgTypeError(0, NumberKind)
			}
			return frame.ReturnNumber(value + 1)
		},
	)
	if err != nil {
		b.Fatal(err)
	}
	luaFunction := compileTestFunction(
		b,
		state,
		"@lua-callee.lua",
		`local value = ...; return value + 1`,
	)
	caller := compileTestFunction(b, state, "@native-call.lua", `
local target, iterations = ...
local value = 0
for _ = 1, iterations do
	value = target(value)
end
return value
`)

	for _, test := range []struct {
		name   string
		target Value
	}{
		{name: "native", target: native.Value()},
		{name: "lua control", target: luaFunction.owningValue()},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.ReportMetric(iterations, "calls/op")
			benchmarkExecutorFunction(
				b,
				state,
				caller,
				test.target,
				Number(iterations),
			)
		})
	}
}

func BenchmarkNativeFrameOutcomes(b *testing.B) {
	tests := []struct {
		name     string
		captures []Value
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
			name:     "captured Value",
			captures: []Value{Number(42)},
			callback: func(frame Frame) Outcome {
				return frame.ReturnValue(frame.Capture(0))
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
			function, err := state.NewNativeFunction(
				test.callback,
				test.captures...,
			)
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

func stageNativeTestCall(
	t *testing.T,
	state *State,
	function *Function,
	wantedResults int,
	arguments ...Value,
) *threadObject {
	t.Helper()
	thread := state.main
	if len(thread.frames) != 0 || thread.activeNativeToken != 0 {
		t.Fatal("test Thread has active native work")
	}
	oldExtent := thread.liveValueExtent()
	thread.top = 0
	thread.frameExtent = 0
	thread.clearInactive(0, oldExtent)

	required := 1 + len(arguments)
	thread.reserveValues(required)
	object := function.runtimeObject()
	thread.values[0] = slotFromFunctionObject(object)
	for index, argument := range arguments {
		thread.values[index+1] = slotFromValue(argument)
	}
	thread.top = required
	if failure := thread.pushFunctionCall(
		object,
		0,
		len(arguments),
		wantedResults,
	); failure != nil {
		t.Fatal(failure)
	}
	runtime.KeepAlive(function)
	return thread
}

func assertNativeTestResults(
	t *testing.T,
	thread *threadObject,
	expected ...Value,
) {
	t.Helper()
	if len(thread.frames) != 0 {
		t.Fatalf("native activation count = %d; want 0", len(thread.frames))
	}
	if thread.top != len(expected) {
		t.Fatalf("native result count = %d; want %d", thread.top, len(expected))
	}
	for index, value := range expected {
		assertTestSlot(t, thread.values[index], value)
	}
}

func assertNativePanic(t *testing.T, operation func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("operation did not panic")
		}
	}()
	operation()
}
