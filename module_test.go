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
			// State a module needs travels in the Go closure.
			return frame.ReturnString(name + ":" + Number(7).String())
		},
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
	published, ok := rawStr(library, "preload").AsTable()
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
	reopenedPreload, _ := rawStr(reopenedLibrary, "preload").AsTable()
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

// Captured state now lives in the Go closure. Each installed function must
// still get its own, so two entries built from one factory do not share it.
func TestSetFunctionsInstallsIndependentClosureState(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	counter := func() NativeFunc {
		next := 10.0
		return func(frame Frame) Outcome {
			current := next
			next++
			return frame.ReturnNumber(current)
		}
	}
	if err := state.SetFunctions(
		table,
		map[string]NativeFunc{
			"first":  counter(),
			"second": counter(),
		},
	); err != nil {
		t.Fatal(err)
	}
	firstValue := rawStr(table, "first")
	first, ok := firstValue.AsFunction()
	if !ok {
		t.Fatalf("first = %v, want function", firstValue)
	}
	secondValue := rawStr(table, "second")
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
	if !rawStr(table, "good").IsNil() ||
		!rawStr(table, "bad").IsNil() {
		t.Fatal("nil function failure partially installed fields")
	}
	assertTestValue(t, rawStr(table, "existing"), Number(1))

	other, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
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

	if !rawStr(table, "too_many").IsNil() {
		t.Fatal("capture-limit failure installed a field")
	}
}
