// Package conformance runs the official PUC-Rio Lua 5.1 test suite against
// Lunar. The vendored files under testdata/lua5.1-tests remain byte-for-byte
// upstream. Each file runs in a fresh State inside a staged copy of the suite
// directory, invoked the way the suite's own all.lua driver invokes it. The
// default driver round-trips through string.dump and loadstring; big.lua
// retains the suite's special source-loaded coroutine invocation.
package conformance

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	lua "github.com/mmcdole/lunar"
)

// dumpDriver mirrors all.lua's global dofile replacement so nested dofile
// calls, notably verybig.lua's generated program, take the same dump/undump
// path as the outer suite file.
const dumpDriver = `
dofile = function(n)
  local f = assert(loadfile(n))
  local b = string.dump(f)
  f = assert(loadstring(b))
  return f()
end
return dofile(%q)
`

// bigDriver mirrors all.lua's special coroutine invocation. The staged copy
// omits only big.lua's platform-specific 32-bit string-overflow probe.
const bigDriver = `
local f = coroutine.wrap(assert(loadfile("big.lua")))
assert(f() == "b")
assert(f() == "a")
`

type suiteTest struct {
	name          string
	invocation    string // overrides dumpDriver when all.lua invokes the file specially
	postcondition string // Lua statements receiving the file's first return value as r
	skipReason    string
}

// suiteTests lists every executable file in the suite in all.lua's order.
// Empty policy fields mean the default invocation with no extra assertion.
var suiteTests = []suiteTest{
	{name: "main.lua", skipReason: "drives the standalone lua.c interpreter binary through os.execute"},
	{name: "gc.lua", skipReason: "counts incremental collector steps; Lunar collects synchronously (see Scope in the root README); weak tables and finalizers are covered natively by collection_weak_test.go and collection_finalizer_test.go"},
	{name: "db.lua", skipReason: "requires debug.sethook and debug.gethook, an intentional Lunar limit"},
	{name: "calls.lua", postcondition: `assert(r == deep and deep ~= nil)`},
	{name: "strings.lua"},
	{name: "literals.lua"},
	{name: "attrib.lua", postcondition: `assert(r == 27)`},
	{name: "locals.lua", postcondition: `assert(r == 5)`},
	{name: "constructs.lua"},
	{name: "code.lua"}, // self-skips its opcode section without the suite's C test library
	{name: "big.lua", invocation: bigDriver},
	{name: "nextvar.lua"},
	{name: "pm.lua"},
	{name: "api.lua"}, // self-skips without the suite's C test library
	{name: "events.lua", postcondition: `assert(r == 12)`},
	{name: "vararg.lua"},
	{name: "closure.lua"},
	{name: "errors.lua"},
	{name: "math.lua"},
	{name: "sort.lua"},
	{name: "verybig.lua", postcondition: `assert(r == 10)`},
	{name: "files.lua"},
}

func TestLua51Suite(t *testing.T) {
	source, err := filepath.Abs(filepath.Join("testdata", "lua5.1-tests"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range suiteTests {
		t.Run(test.name, func(t *testing.T) {
			if test.skipReason != "" {
				t.Skip(test.skipReason)
			}
			runSuiteTest(t, source, test)
		})
	}
}

func TestDumpDriverCoversNestedDofile(t *testing.T) {
	t.Chdir(t.TempDir())
	for name, source := range map[string]string{
		"outer.lua": `return dofile("inner.lua")`,
		"inner.lua": `return "nested result"`,
	} {
		if err := os.WriteFile(name, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	state, err := lua.New(lua.Options{
		Libraries:    lua.FullLibraries(),
		ScriptLoader: lua.HostLoader(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	driver := `
local originalDump = string.dump
local dumps = 0
string.dump = function(f)
  dumps = dumps + 1
  return originalDump(f)
end
local result = (function()
` + fmt.Sprintf(dumpDriver, "outer.lua") + `
end)()
assert(result == "nested result")
assert(dumps == 2, "nested dofile bypassed dump/undump")
`
	if _, err := state.DoString("@nested-driver.lua", driver); err != nil {
		t.Fatal(err)
	}
}

func runSuiteTest(t *testing.T, source string, test suiteTest) {
	t.Helper()
	staged := t.TempDir()
	stageSuite(t, source, staged)
	t.Chdir(staged)

	state, err := lua.New(lua.Options{
		Libraries:    lua.FullLibraries(),
		ScriptLoader: lua.HostLoader(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := state.Close(); err != nil {
			t.Errorf("close state: %v", err)
		}
	}()

	// all.lua seeds the random generator before any file runs.
	if _, err := state.DoString("@prelude.lua", `math.randomseed(0)`); err != nil {
		t.Fatal(err)
	}

	invocation := test.invocation
	if invocation == "" {
		invocation = fmt.Sprintf(dumpDriver, test.name)
	}
	if test.postcondition != "" {
		invocation = fmt.Sprintf(
			"local r = (function() %s end)()\n%s",
			invocation,
			test.postcondition,
		)
	}
	if _, err := state.DoString("@driver:"+test.name, invocation); err != nil {
		t.Fatalf("%s: %v", test.name, err)
	}
}

// stageSuite copies the vendored suite into a writable directory: several
// files create, rewrite, and remove files next to themselves, and attrib.lua
// requires modules from a P1 package directory.
func stageSuite(t *testing.T, source, destination string) {
	t.Helper()
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(destination, entry.Name()),
			data,
			0o644,
		); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{"P1", filepath.Join("libs", "P1")} {
		if err := os.MkdirAll(filepath.Join(destination, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	applyStagedSuitePatches(t, destination)
}
