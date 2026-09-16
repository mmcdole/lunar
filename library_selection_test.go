package lua_test

import (
	"errors"
	"testing"

	"github.com/mmcdole/lunar"
)

func TestNewInstallsArbitraryLibrarySubset(t *testing.T) {
	state, err := lua.New(lua.Options{
		Libraries: lua.LibrarySet{
			lua.MathLibrary,
			lua.StringLibrary,
			lua.MathLibrary,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	assertGlobalKind(t, state, "math", lua.TableKind)
	assertGlobalKind(t, state, "string", lua.TableKind)
	assertGlobalKind(t, state, "table", lua.NilKind)
	assertGlobalKind(t, state, "type", lua.NilKind)
	assertGlobalKind(t, state, "coroutine", lua.NilKind)
}

func TestCoreLibrariesInstallCapabilitySafeProfile(t *testing.T) {
	state, err := lua.New(lua.Options{Libraries: lua.CoreLibraries()})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	for name, kind := range map[string]lua.Kind{
		"type":      lua.FunctionKind,
		"coroutine": lua.TableKind,
		"package":   lua.TableKind,
		"table":     lua.TableKind,
		"string":    lua.TableKind,
		"math":      lua.TableKind,
	} {
		assertGlobalKind(t, state, name, kind)
	}
	for _, name := range []string{"io", "os", "debug"} {
		assertGlobalKind(t, state, name, lua.NilKind)
	}
}

func TestFullLibrariesInstallEveryStandardLibrary(t *testing.T) {
	state, err := lua.New(lua.Options{Libraries: lua.FullLibraries()})
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
		assertGlobalKind(t, state, name, lua.TableKind)
	}
	assertGlobalKind(t, state, "type", lua.FunctionKind)
}

func TestCoroutineLibraryDoesNotInstallBaseGlobals(t *testing.T) {
	state, err := lua.New(lua.Options{
		Libraries: lua.LibrarySet{lua.CoroutineLibrary},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	assertGlobalKind(t, state, "coroutine", lua.TableKind)
	assertGlobalKind(t, state, "type", lua.NilKind)
}

func TestNewRejectsInvalidLibraryBeforeConstruction(t *testing.T) {
	state, err := lua.New(lua.Options{
		Libraries: lua.LibrarySet{lua.BaseLibrary, lua.Library(255)},
	})
	if state != nil {
		t.Fatal("New returned a State for an invalid LibrarySet")
	}
	if !errors.Is(err, lua.ErrInvalidLibrary) {
		t.Fatalf("New error = %v; want ErrInvalidLibrary", err)
	}
}

func TestLibraryProfilesReturnIndependentSets(t *testing.T) {
	first := lua.CoreLibraries()
	first[0] = lua.DebugLibrary
	if second := lua.CoreLibraries(); second[0] != lua.BaseLibrary {
		t.Fatalf("CoreLibraries shared mutable storage: %v", second)
	}

	first = lua.FullLibraries()
	first[0] = lua.DebugLibrary
	if second := lua.FullLibraries(); second[0] != lua.BaseLibrary {
		t.Fatalf("FullLibraries shared mutable storage: %v", second)
	}
}

func assertGlobalKind(
	t *testing.T,
	state *lua.State,
	name string,
	want lua.Kind,
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
