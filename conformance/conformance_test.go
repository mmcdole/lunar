// Package conformance runs the official PUC-Rio Lua 5.1 test suite against
// Lunar. The vendored files under testdata/lua5.1-tests are never modified;
// see PROVENANCE.md. Each file runs in a fresh State inside a staged copy of
// the suite directory, invoked exactly the way the suite's own all.lua driver
// invokes it — including the round trip through string.dump and loadstring,
// which exercises Lunar's binary chunk writer and reader on every file.
package conformance

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	lua "github.com/mmcdole/lunar"
)

// dumpDriver mirrors all.lua's redefined dofile: load the source, dump it to
// a binary chunk, reload the chunk, and run that.
const dumpDriver = `
local f = assert(loadfile(%q))
local b = string.dump(f)
f = assert(loadstring(b))
return f()
`

// suiteFiles lists every executable file in the suite in all.lua's order.
// check is extra Lua appended to the driver's result, mirroring the return
// value assertions all.lua makes. A non-empty skip names the Lunar limit or
// suite requirement that keeps the file from applying; the reasons are
// documented in README.md alongside this file.
var suiteFiles = []struct {
	name   string
	driver string // overrides dumpDriver when the suite runs the file another way
	check  string // Lua statements receiving the file's return value as "r"
	skip   string
}{
	{name: "main.lua", skip: "drives the standalone lua.c interpreter binary through os.execute"},
	{name: "gc.lua", skip: "counts incremental collector steps; Lunar collects synchronously (see Scope in the root README); weak tables and finalizers are covered natively by collection_weak_test.go and collection_finalizer_test.go"},
	{name: "db.lua", skip: "requires debug.sethook and debug.gethook, an intentional Lunar limit"},
	{name: "calls.lua", check: `assert(r == deep and deep ~= nil)`},
	{name: "strings.lua"},
	{name: "literals.lua"},
	{name: "attrib.lua", check: `assert(r == 27)`},
	{name: "locals.lua", check: `assert(r == 5)`},
	{name: "constructs.lua"},
	{name: "code.lua"}, // self-skips its opcode section without the suite's C test library
	{name: "big.lua", skip: "asserts 32-bit size_t overflow errors; on 64-bit Go the suite's 4 GiB concatenation simply succeeds"},
	{name: "nextvar.lua"},
	{name: "pm.lua"},
	{name: "api.lua"}, // self-skips without the suite's C test library
	{name: "events.lua", check: `assert(r == 12)`},
	{name: "vararg.lua"},
	{name: "closure.lua"},
	{name: "errors.lua", skip: "asserts the reference's exact error wording; Lunar reports the same locations and near-tokens with different phrasing (tracked deviation)"},
	{name: "math.lua"},
	{name: "sort.lua"},
	{name: "verybig.lua", check: `assert(r == 10)`},
	{name: "files.lua"},
}

func TestLua51Suite(t *testing.T) {
	source, err := filepath.Abs(filepath.Join("testdata", "lua5.1-tests"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range suiteFiles {
		t.Run(file.name, func(t *testing.T) {
			if file.skip != "" {
				t.Skip(file.skip)
			}
			runSuiteFile(t, source, file.name, file.driver, file.check)
		})
	}
}

func runSuiteFile(t *testing.T, source, name, driver, check string) {
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

	if driver == "" {
		driver = fmt.Sprintf(dumpDriver, name)
	}
	if check != "" {
		driver = fmt.Sprintf("local r = (function() %s end)()\n%s", driver, check)
	}
	if _, err := state.DoString("@driver:"+name, driver); err != nil {
		t.Fatalf("%s: %v", name, err)
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
}
