package compare

import (
	"crypto/sha256"
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

// awfyUpstreamSHA256 pins every vendored file to upstream commit
// 74306fec151070fd07157cefeacf19e7e0bcdc89.
// bit.lua is Lunar's own stand-in and is not listed.
var awfyUpstreamSHA256 = map[string]string{
	"benchmark.lua":      "f854c782efb9513bd60805e6e833e4df268b997ea9ff95744de0ad6d00733e63",
	"bounce.lua":         "e54bf6160d07938100b9c4bf00af6603ba500388761ae51e0fd2d920fe7c67df",
	"cd.lua":             "e1d7114fdba480bdc97207f9f8b0f2cd62b9393377a095cd43f0d130273a365e",
	"deltablue.lua":      "8f0064b61bfdcafafa260d0c5fb35dc7e55c255ca59b23f8f7beb3024684fb63",
	"hashindextable.lua": "c1f415af1b69f85908afc6b8b8fc5f1ae7c3879a1f0feeb2eaae00f97ed4473a",
	"json.lua":           "79196a37531206523459ea5400aa00ca6425942f4f5c23595f14f96affcabbf1",
	"list.lua":           "863cff8f08d7b48bbf5f01b12d487dace4aee25777fbcc456c43819a51e59e07",
	"mandelbrot-fn.lua":  "8bba1d7624431d4c0323c18bab50e270fcdb0d3a0522bc5bc9231bd5a167ab20",
	"mandelbrot.lua":     "d6b063615e2f6057a3db7257c66325af35dcbdf8b0294eb2580c294b64425e98",
	"nbody.lua":          "6bd49dde32cf69dd4b9e6a971d182d357ac4b7e29910dfbd79c3626b73d8cd7f",
	"permute.lua":        "7e7c7cd4dd1d10b85a4474d868a70da04be5b5ec9b287bee8421a111fcee2068",
	"queens.lua":         "03f9349ae7ba3aad09a5bba4f7102e77ea340e757906f9c8e53c133d8e469381",
	"richards.lua":       "b9620354ecafb1a1d3fe6b587630ff87df3fde359092eaeb210ed07f572a9990",
	"sieve.lua":          "3b6a63b07b5ed506e97337ba339983aa71a54021bd73c9f83b4e069aa5d89fed",
	"som.lua":            "5c07cf378452f0c391c76d3788dc15274ff34b82692bb5cfe6f7e648b82212d0",
	"storage.lua":        "142e767645d31351104ed3f326d8b24dd68eab06b1ff687fc7233330afefa3d2",
	"towers.lua":         "d0902a6d929a57e6412586687585732bccfdf39a81c805bbdcd8485b4c1b4c75",
}

func TestAWFYSourcesMatchUpstream(t *testing.T) {
	entries, err := awfySourceFS.ReadDir("awfy")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "bit.lua" {
			continue
		}
		want, ok := awfyUpstreamSHA256[name]
		if !ok {
			t.Errorf("%s has no pinned upstream hash", name)
			continue
		}
		data, err := awfySourceFS.ReadFile("awfy/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != want {
			t.Errorf("%s SHA-256 = %s; want %s", name, got, want)
		}
	}
}
