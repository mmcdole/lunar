package lua

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestOpenOSInstallsFreshCanonicalLibrary(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	before, err := state.Global("os")
	if err != nil {
		t.Fatal(err)
	}
	if !before.IsNil() {
		t.Fatalf("new state os = %v; want nil", before)
	}
	loadedBeforeOpen := mustLoadString(
		t,
		state,
		"@open-os.lua",
		`return os.difftime(9, 4)`,
	)
	if err := state.OpenOS(); err != nil {
		t.Fatal(err)
	}

	libraryValue, err := state.Global("os")
	if err != nil {
		t.Fatal(err)
	}
	library, ok := libraryValue.Table()
	if !ok {
		t.Fatalf("os = %v; want table", libraryValue)
	}
	want := make(map[string]Kind, len(osLibraryFunctions))
	previous := make(map[string]Value, len(osLibraryFunctions))
	for _, definition := range osLibraryFunctions {
		want[definition.name] = FunctionKind
		previous[definition.name] = library.RawGetString(definition.name)
	}
	assertTableSurface(t, library, want)

	results, err := state.Call(loadedBeforeOpen.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(5))

	if err := state.SetGlobal("os", Number(1)); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenOS(); err != nil {
		t.Fatal(err)
	}
	reopenedValue, err := state.Global("os")
	if err != nil {
		t.Fatal(err)
	}
	reopened, ok := reopenedValue.Table()
	if !ok {
		t.Fatalf("reopened os = %v; want table", reopenedValue)
	}
	if same, applicable := libraryValue.SameObject(
		reopenedValue,
	); !applicable || same {
		t.Fatal("reopening did not replace the os table")
	}
	for name, old := range previous {
		current := reopened.RawGetString(name)
		if same, applicable := old.SameObject(
			current,
		); !applicable || same {
			t.Fatalf("reopened os.%s is not a fresh Function", name)
		}
	}

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenOS(); !errors.Is(err, ErrClosed) {
		t.Fatalf("OpenOS after Close = %v; want ErrClosed", err)
	}
}

func TestOSLibraryEnvironmentAndFilesystemOperations(t *testing.T) {
	const environmentName = "BADGER_LUA_OS_TEST_VALUE"
	t.Setenv(environmentName, "")

	state := newStateWithOS(t)
	defer state.Close()
	chunk := mustLoadString(t, state, "@environment.lua", `
local empty = os.getenv("BADGER_LUA_OS_TEST_VALUE")
local missing = os.getenv("BADGER_LUA_OS_TEST_MISSING")
return empty, missing
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, state.String(""), Nil())

	directory := t.TempDir()
	from := filepath.Join(directory, "from")
	to := filepath.Join(directory, "to")
	if err := os.WriteFile(from, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := "return os.rename(" + strconv.Quote(from) +
		", " + strconv.Quote(to) + "), os.remove(" +
		strconv.Quote(to) + ")"
	chunk = mustLoadString(t, state, "@filesystem.lua", source)
	results, err = state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true), Bool(true))
	if _, err := os.Stat(to); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed file stat = %v; want not exist", err)
	}

	missing := filepath.Join(directory, "missing")
	source = "return os.rename(" + strconv.Quote(missing) +
		", " + strconv.Quote(to) + ")"
	chunk = mustLoadString(t, state, "@rename-failure.lua", source)
	results, err = state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || !results[0].IsNil() {
		t.Fatalf("rename failure = %v; want nil, message, errno", results)
	}
	message, ok := results[1].AsString()
	if !ok || !strings.HasPrefix(message, missing+": ") ||
		strings.Contains(message, "rename ") {
		t.Fatalf("rename failure message = %q", message)
	}
	code, ok := results[2].AsNumber()
	if !ok || code == 0 {
		t.Fatalf("rename failure errno = %v", results[2])
	}
}

func TestOSLibraryTemporaryNameCreatesClosedFile(t *testing.T) {
	state := newStateWithOS(t)
	defer state.Close()
	chunk := mustLoadString(
		t,
		state,
		"@tmpname.lua",
		`return os.tmpname()`,
	)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("tmpname results = %v", results)
	}
	name, ok := results[0].AsString()
	if !ok {
		t.Fatalf("tmpname = %v; want string", results[0])
	}
	defer os.Remove(name)
	file, err := os.OpenFile(name, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("temporary file is not closed and reopenable: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOSLibraryCoreMatchesLua51(t *testing.T) {
	runLua51Cases(t, osLibraryLua51Cases)
}

func newStateWithOS(t *testing.T) *State {
	t.Helper()
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.OpenBase(); err != nil {
		state.Close()
		t.Fatal(err)
	}
	if err := state.OpenOS(); err != nil {
		state.Close()
		t.Fatal(err)
	}
	return state
}

var osLibraryLua51Cases = []lua51Case{
	{
		name:   "difftime truncates both operands to time_t",
		source: `return os.difftime(12.9, 2.9)`,
		want:   "ok 10",
	},
	{
		name:   "difftime defaults its second operand",
		source: `return os.difftime("12")`,
		want:   "ok 12",
	},
	{
		name:   "difftime reports its first argument",
		source: `return os.difftime()`,
		want: "error 'case:1: bad argument #1 to 'difftime' " +
			"(number expected, got no value)'",
	},
	{
		name:   "getenv requires a string",
		source: `return os.getenv({})`,
		want: "error 'case:1: bad argument #1 to 'getenv' " +
			"(string expected, got table)'",
	},
	{
		name:   "remove requires a string",
		source: `return os.remove()`,
		want: "error 'case:1: bad argument #1 to 'remove' " +
			"(string expected, got no value)'",
	},
	{
		name:   "rename requires its destination",
		source: `return os.rename("from")`,
		want: "error 'case:1: bad argument #2 to 'rename' " +
			"(string expected, got no value)'",
	},
}
