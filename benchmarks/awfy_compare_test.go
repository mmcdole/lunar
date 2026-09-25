package compare

import (
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The Are We Fast Yet Lua port, vendored unchanged except for bit.lua, a
// pure-Lua stand-in for LuaJIT's bit module that Lua 5.1 lacks. Every runtime
// runs the same stand-in. Provenance is recorded in PROGRAMS.md.
//
//go:embed awfy/*.lua
var awfySourceFS embed.FS

// awfyCase is one Are We Fast Yet benchmark and its inner-iteration count.
// Counts are scaled so most operations take roughly 0.15-0.5 s on PUC Lua
// 5.1; benchmarks that treat the count as a problem size use a value they
// can verify. DeltaBlue uses 1000 because go-lua's time grows quadratically
// with it (9.9 s at 2000). Havlak is omitted: its smallest verified size
// still takes about 6 s on PUC Lua.
type awfyCase struct {
	name   string
	module string
	inner  int
}

var awfyCases = []awfyCase{
	{name: "richards", module: "richards", inner: 5},
	{name: "deltablue", module: "deltablue", inner: 1000},
	{name: "json", module: "json", inner: 25},
	{name: "cd", module: "cd", inner: 100},
	{name: "bounce", module: "bounce", inner: 200},
	{name: "list", module: "list", inner: 500},
	{name: "mandelbrot", module: "mandelbrot", inner: 500},
	{name: "nbody", module: "nbody", inner: 250_000},
	{name: "permute", module: "permute", inner: 250},
	{name: "queens", module: "queens", inner: 400},
	{name: "sieve", module: "sieve", inner: 1000},
	{name: "storage", module: "storage", inner: 30},
	{name: "towers", module: "towers", inner: 200},
}

func BenchmarkAWFY(b *testing.B) {
	for _, benchmarkCase := range awfyCases {
		source, err := awfyProgramSource(benchmarkCase)
		if err != nil {
			b.Fatal(err)
		}
		spec := programSpec{
			name:       benchmarkCase.name,
			sourceFile: benchmarkCase.module + ".lua",
			libraries: programLibraryBase |
				programLibraryString |
				programLibraryMath,
		}
		b.Run("program="+benchmarkCase.name, func(b *testing.B) {
			for _, engine := range programEngines {
				b.Run("runtime="+engine.name, func(b *testing.B) {
					benchmarkPreparedProgram(
						b,
						engine,
						spec,
						source,
						programOracle{exact: "ok"},
					)
				})
			}
		})
	}
}

// awfyProgramSource bundles every vendored module into one chunk behind a
// local require, so each runtime loads identical code without a package
// library, then exposes benchmark_program for the shared program harness.
func awfyProgramSource(benchmarkCase awfyCase) (string, error) {
	entries, err := awfySourceFS.ReadDir("awfy")
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	var source strings.Builder
	source.WriteString(`local __sources, __loaded = {}, {}
local function require(name)
  local value = __loaded[name]
  if value ~= nil then
    return value
  end
  local load_module = __sources[name]
  if load_module == nil then
    error("module '" .. name .. "' not found", 2)
  end
  value = load_module(name)
  if value == nil then
    value = true
  end
  __loaded[name] = value
  return value
end
`)
	for _, name := range names {
		module := strings.TrimSuffix(name, ".lua")
		text, err := awfySourceFS.ReadFile("awfy/" + name)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&source, "__sources[%q] = function(...)\n", module)
		source.Write(text)
		source.WriteString("\nend\n")
	}
	fmt.Fprintf(&source, `local __benchmark = require(%q)
function benchmark_program()
  if not __benchmark:inner_benchmark_loop(%s) then
    error("%s failed verification")
  end
  benchmark_program_result = "ok"
end
`, benchmarkCase.module, strconv.Itoa(benchmarkCase.inner), benchmarkCase.name)
	return source.String(), nil
}

func TestAWFYProgramsVerifyOnEveryRuntime(t *testing.T) {
	if testing.Short() {
		t.Skip("runs every Are We Fast Yet benchmark once per runtime")
	}
	for _, benchmarkCase := range awfyCases {
		source, err := awfyProgramSource(benchmarkCase)
		if err != nil {
			t.Fatal(err)
		}
		spec := programSpec{
			name:       benchmarkCase.name,
			sourceFile: benchmarkCase.module + ".lua",
			libraries: programLibraryBase |
				programLibraryString |
				programLibraryMath,
		}
		for _, engine := range programEngines {
			t.Run(benchmarkCase.name+"/"+engine.name, func(t *testing.T) {
				prepared, err := engine.prepare(spec, source)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := prepared.close(); err != nil {
						t.Error(err)
					}
				}()
				if err := prepared.run(); err != nil {
					t.Fatal(err)
				}
				result, err := prepared.result()
				if err != nil {
					t.Fatal(err)
				}
				if result != "ok" {
					t.Fatalf("result = %q; want ok", result)
				}
			})
		}
	}
}
