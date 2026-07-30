package lua

import (
	"errors"
	"testing"
)

func TestPreloadModuleBeforeAfterAndAcrossOpenPackage(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	beforeCalls := 0
	if err := state.PreloadModule(
		"host.before",
		func(frame Frame) Outcome {
			beforeCalls++
			name, _ := frame.String(0)
			capture, _ := frame.Capture(0).AsNumber()
			return frame.ReturnString(name + ":" + Number(capture).String())
		},
		Number(7),
	); err != nil {
		t.Fatal(err)
	}
	preload := state.modulePreloads
	if preload == nil {
		t.Fatal("PreloadModule did not create the State preload table")
	}
	if err := state.Collect(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenPackage(); err != nil {
		t.Fatal(err)
	}
	packageValue, err := state.Global("package")
	if err != nil {
		t.Fatal(err)
	}
	library, ok := packageValue.AsTable()
	if !ok {
		t.Fatalf("package = %v, want table", packageValue)
	}
	published, ok := library.RawGetString("preload").AsTable()
	if !ok || published.runtimeObject() != preload {
		t.Fatal("OpenPackage did not publish the State preload table")
	}
	assertRequiredString(t, state, "host.before", "host.before:7")
	assertRequiredString(t, state, "host.before", "host.before:7")
	if beforeCalls != 1 {
		t.Fatalf("cached loader calls = %d, want 1", beforeCalls)
	}

	if err := state.PreloadModule(
		"host.after",
		func(frame Frame) Outcome {
			return frame.ReturnString("after")
		},
	); err != nil {
		t.Fatal(err)
	}
	assertRequiredString(t, state, "host.after", "after")

	reopenCalls := 0
	if err := state.PreloadModule(
		"host.reopen",
		func(frame Frame) Outcome {
			reopenCalls++
			return frame.ReturnString("reopened")
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenPackage(); err != nil {
		t.Fatal(err)
	}
	reopenedPackage, err := state.Global("package")
	if err != nil {
		t.Fatal(err)
	}
	reopenedLibrary, _ := reopenedPackage.AsTable()
	reopenedPreload, _ := reopenedLibrary.RawGetString("preload").AsTable()
	if reopenedPreload.runtimeObject() != preload {
		t.Fatal("reopening package replaced the preload table")
	}
	assertRequiredString(t, state, "host.reopen", "reopened")
	if reopenCalls != 1 {
		t.Fatalf("reopened loader calls = %d, want 1", reopenCalls)
	}

	if err := state.PreloadModule(
		"host.nul\x00ignored",
		func(frame Frame) Outcome {
			name, _ := frame.String(0)
			return frame.ReturnString(name)
		},
	); err != nil {
		t.Fatal(err)
	}
	assertRequiredString(t, state, "host.nul", "host.nul")
}

func assertRequiredString(
	t *testing.T,
	state *State,
	name, want string,
) {
	t.Helper()
	require, err := state.Global("require")
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(require, String(name))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("require(%q) returned %d values", name, len(results))
	}
	text, ok := results[0].AsString()
	if !ok || text != want {
		t.Fatalf("require(%q) = (%q, %v), want %q", name, text, ok, want)
	}
}

func TestPreloadModuleValidatesBeforeCreatingPreloadTable(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.PreloadModule("nil", nil); !errors.Is(
		err,
		ErrInvalidNativeFunction,
	) {
		t.Fatalf("nil loader error = %v", err)
	}
	if state.modulePreloads != nil {
		t.Fatal("nil loader created a preload table")
	}
	if err := state.PreloadModule(
		"invalid",
		func(frame Frame) Outcome { return frame.Return() },
		Value{},
	); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("invalid capture error = %v", err)
	}
	if state.modulePreloads != nil {
		t.Fatal("invalid capture created a preload table")
	}

	other, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := other.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	if err := state.PreloadModule(
		"foreign",
		func(frame Frame) Outcome { return frame.Return() },
		foreign.Value(),
	); !errors.Is(err, ErrForeignValue) {
		t.Fatalf("foreign capture error = %v", err)
	}
	if state.modulePreloads != nil {
		t.Fatal("foreign capture created a preload table")
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}

	captures := make([]Value, maxNativeCaptures+1)
	for index := range captures {
		captures[index] = Number(float64(index))
	}
	if err := state.PreloadModule(
		"too-many",
		func(frame Frame) Outcome { return frame.Return() },
		captures...,
	); !errors.Is(err, ErrNativeCaptureLimit) {
		t.Fatalf("capture-limit error = %v", err)
	}
	if state.modulePreloads != nil {
		t.Fatal("capture-limit failure created a preload table")
	}

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.PreloadModule(
		"closed",
		func(frame Frame) Outcome { return frame.Return() },
	); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed PreloadModule error = %v", err)
	}
}

func TestSetFunctionsInstallsIndependentCapturedFunctions(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	entry := func(frame Frame) Outcome {
		value := frame.Capture(0)
		number, _ := value.AsNumber()
		frame.SetCapture(0, Number(number+1))
		return frame.ReturnValue(value)
	}
	if err := state.SetFunctions(
		table,
		map[string]NativeFunc{
			"first":  entry,
			"second": entry,
		},
		Number(10),
	); err != nil {
		t.Fatal(err)
	}
	firstValue := table.RawGetString("first")
	first, ok := firstValue.AsFunction()
	if !ok {
		t.Fatalf("first = %v, want function", firstValue)
	}
	secondValue := table.RawGetString("second")
	second, ok := secondValue.AsFunction()
	if !ok {
		t.Fatalf("second = %v, want function", secondValue)
	}
	for _, test := range []struct {
		function *Function
		want     Value
	}{
		{function: first, want: Number(10)},
		{function: first, want: Number(11)},
		{function: second, want: Number(10)},
	} {
		results, err := state.Call(test.function.Value())
		if err != nil {
			t.Fatal(err)
		}
		assertTestValues(t, results, test.want)
	}
}

func TestSetFunctionsValidationIsAllOrNothing(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetString("existing", Number(1)); err != nil {
		t.Fatal(err)
	}
	valid := func(frame Frame) Outcome { return frame.Return() }
	if err := state.SetFunctions(
		table,
		map[string]NativeFunc{
			"good": valid,
			"bad":  nil,
		},
	); !errors.Is(err, ErrInvalidNativeFunction) {
		t.Fatalf("nil function error = %v", err)
	}
	if !table.RawGetString("good").IsNil() ||
		!table.RawGetString("bad").IsNil() {
		t.Fatal("nil function failure partially installed fields")
	}
	assertTestValue(t, table.RawGetString("existing"), Number(1))

	other, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	foreignCapture, err := other.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetFunctions(
		table,
		map[string]NativeFunc{"captured": valid},
		foreignCapture.Value(),
	); !errors.Is(err, ErrForeignValue) {
		t.Fatalf("foreign capture error = %v", err)
	}
	if !table.RawGetString("captured").IsNil() {
		t.Fatal("foreign capture failure installed a field")
	}

	foreignTable, err := other.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetFunctions(
		foreignTable,
		map[string]NativeFunc{"field": valid},
	); !errors.Is(err, ErrForeignValue) {
		t.Fatalf("foreign table error = %v", err)
	}
	if err := state.SetFunctions(
		nil,
		map[string]NativeFunc{"field": valid},
	); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("nil table error = %v", err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}

	captures := make([]Value, maxNativeCaptures+1)
	for index := range captures {
		captures[index] = Bool(true)
	}
	if err := state.SetFunctions(
		table,
		map[string]NativeFunc{"too_many": valid},
		captures...,
	); !errors.Is(err, ErrNativeCaptureLimit) {
		t.Fatalf("capture-limit error = %v", err)
	}
	if !table.RawGetString("too_many").IsNil() {
		t.Fatal("capture-limit failure installed a field")
	}
}
