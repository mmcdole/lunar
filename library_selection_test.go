package lua

import (
	"errors"
	"testing"
)

func TestNewInstallsArbitraryLibrarySubset(t *testing.T) {
	state, err := New(Options{
		Libraries: LibrarySet{
			MathLibrary,
			StringLibrary,
			MathLibrary,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	assertGlobalKind(t, state, "math", TableKind)
	assertGlobalKind(t, state, "string", TableKind)
	assertGlobalKind(t, state, "table", NilKind)
	assertGlobalKind(t, state, "type", NilKind)
	assertGlobalKind(t, state, "coroutine", NilKind)
}

func TestCoreLibrariesInstallCapabilitySafeProfile(t *testing.T) {
	state, err := New(Options{Libraries: CoreLibraries()})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	for name, kind := range map[string]Kind{
		"type":      FunctionKind,
		"coroutine": TableKind,
		"package":   TableKind,
		"table":     TableKind,
		"string":    TableKind,
		"math":      TableKind,
	} {
		assertGlobalKind(t, state, name, kind)
	}
	for _, name := range []string{"io", "os", "debug"} {
		assertGlobalKind(t, state, name, NilKind)
	}
}

func TestFullLibrariesInstallEveryStandardLibrary(t *testing.T) {
	state, err := New(Options{Libraries: FullLibraries()})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	for _, name := range []string{
		"coroutine",
		"package",
		"table",
		"io",
		"os",
		"string",
		"math",
		"debug",
	} {
		assertGlobalKind(t, state, name, TableKind)
	}
	assertGlobalKind(t, state, "type", FunctionKind)
}

func TestCoroutineLibraryDoesNotInstallBaseGlobals(t *testing.T) {
	state, err := New(Options{
		Libraries: LibrarySet{CoroutineLibrary},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	assertGlobalKind(t, state, "coroutine", TableKind)
	assertGlobalKind(t, state, "type", NilKind)
}

func TestNewRejectsInvalidLibraryBeforeConstruction(t *testing.T) {
	state, err := New(Options{
		Libraries: LibrarySet{BaseLibrary, Library(255)},
	})
	if state != nil {
		t.Fatal("New returned a State for an invalid LibrarySet")
	}
	if !errors.Is(err, ErrInvalidLibrary) {
		t.Fatalf("New error = %v; want ErrInvalidLibrary", err)
	}
}

func TestLibraryProfilesReturnIndependentSets(t *testing.T) {
	first := CoreLibraries()
	first[0] = DebugLibrary
	if second := CoreLibraries(); second[0] != BaseLibrary {
		t.Fatalf("CoreLibraries shared mutable storage: %v", second)
	}

	first = FullLibraries()
	first[0] = DebugLibrary
	if second := FullLibraries(); second[0] != BaseLibrary {
		t.Fatalf("FullLibraries shared mutable storage: %v", second)
	}
}

func assertGlobalKind(
	t *testing.T,
	state *State,
	name string,
	want Kind,
) {
	t.Helper()
	value, err := state.RawGlobal(name)
	if err != nil {
		t.Fatal(err)
	}
	if got := value.Kind(); got != want {
		t.Fatalf("global %q kind = %v; want %v", name, got, want)
	}
}
