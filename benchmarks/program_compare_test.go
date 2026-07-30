package compare

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"math"
	"runtime"
	"strconv"
	"strings"
	"testing"

	golua "github.com/Shopify/go-lua"
	lunar "github.com/mmcdole/lunar"
	gopherlua "github.com/yuin/gopher-lua"
)

//go:embed programs/*.lua
var programSourceFS embed.FS

type programLibraries uint8

const (
	programLibraryBase programLibraries = 1 << iota
	programLibraryString
	programLibraryMath
)

type programSpec struct {
	name              string
	sourceFile        string
	sourcePage        string
	sourceArchivePath string
	sourceSHA256      string
	libraries         programLibraries
}

var programSpecs = []programSpec{
	{
		name:              "binarytrees",
		sourceFile:        "binarytrees.lua",
		sourcePage:        "https://benchmarksgame-team.pages.debian.net/benchmarksgame/program/binarytrees-lua-2.html",
		sourceArchivePath: "binarytrees/binarytrees.lua-2.lua",
		sourceSHA256:      "58afb23db343d5c59e0c23b9d8b6188dab41fc378b0e588f965c0d24000173ed",
		libraries:         programLibraryBase | programLibraryString,
	},
	{
		name:              "fannkuchredux",
		sourceFile:        "fannkuchredux.lua",
		sourcePage:        "https://benchmarksgame-team.pages.debian.net/benchmarksgame/program/fannkuchredux-lua-1.html",
		sourceArchivePath: "fannkuchredux/fannkuchredux.lua",
		sourceSHA256:      "e6db90f101bafdfc2f213ce700d247e9b719c23a3782fc2c843c39ea5f1b157a",
		libraries:         programLibraryBase | programLibraryString,
	},
	{
		name:              "nbody",
		sourceFile:        "nbody.lua",
		sourcePage:        "https://benchmarksgame-team.pages.debian.net/benchmarksgame/program/nbody-lua-2.html",
		sourceArchivePath: "nbody/nbody.lua-2.lua",
		sourceSHA256:      "841c93a66ccbf952ba188b96f35ff1267f68f75c8869bd3b019bcc3f99099c1a",
		libraries: programLibraryBase |
			programLibraryString |
			programLibraryMath,
	},
	{
		name:              "spectralnorm",
		sourceFile:        "spectralnorm.lua",
		sourcePage:        "https://benchmarksgame-team.pages.debian.net/benchmarksgame/program/spectralnorm-lua-1.html",
		sourceArchivePath: "spectralnorm/spectralnorm.lua",
		sourceSHA256:      "1acdfef437c9cae18f1dfb8394acc9144028d3e38ca4c581f7d699294fe81fed",
		libraries: programLibraryBase |
			programLibraryString |
			programLibraryMath,
	},
}

type programOracle struct {
	exact        string
	floatValues  []float64
	absTolerance float64
}

type programCase struct {
	name   string
	input  int
	oracle programOracle
}

type preparedProgram struct {
	run    func() error
	result func() (string, error)
	close  func() error
}

type programEngine struct {
	name    string
	prepare func(programSpec, string) (*preparedProgram, error)
}

var programEngines = []programEngine{
	{name: "lunar", prepare: prepareLunarProgram},
	{name: "gopherlua", prepare: prepareGopherLuaProgram},
	{name: "golua", prepare: prepareGoLuaProgram},
}

func BenchmarkPrograms(b *testing.B) {
	// These are intentionally scaled local inputs. They are not the inputs
	// used for official Computer Language Benchmarks Game scores.
	cases := []programCase{
		{
			name:  "binarytrees",
			input: 12,
			oracle: programOracle{exact: "" +
				"stretch tree of depth 13\t check: 16383\n" +
				"4096\t trees of depth 4\t check: 126976\n" +
				"1024\t trees of depth 6\t check: 130048\n" +
				"256\t trees of depth 8\t check: 130816\n" +
				"64\t trees of depth 10\t check: 131008\n" +
				"16\t trees of depth 12\t check: 131056\n" +
				"long lived tree of depth 12\t check: 8191\n"},
		},
		{
			name:  "fannkuchredux",
			input: 8,
			oracle: programOracle{
				exact: "1616\nPfannkuchen(8) = 22\n",
			},
		},
		{
			name:  "nbody",
			input: 20_000,
			oracle: programOracle{
				floatValues:  []float64{-0.169075164, -0.169089263},
				absTolerance: 5e-9,
			},
		},
		{
			name:  "spectralnorm",
			input: 150,
			oracle: programOracle{
				floatValues:  []float64{1.274222873},
				absTolerance: 5e-9,
			},
		},
	}

	for _, benchmarkCase := range cases {
		spec, ok := findProgramSpec(benchmarkCase.name)
		if !ok {
			b.Fatalf("unknown program %q", benchmarkCase.name)
		}
		source, err := loadProgramSource(spec)
		if err != nil {
			b.Fatal(err)
		}
		wrapped := wrapProgramSource(source, benchmarkCase.input)
		b.Run("program="+benchmarkCase.name, func(b *testing.B) {
			for _, engine := range programEngines {
				b.Run("runtime="+engine.name, func(b *testing.B) {
					benchmarkPreparedProgram(
						b,
						engine,
						spec,
						wrapped,
						benchmarkCase.oracle,
					)
				})
			}
		})
	}
}

func benchmarkPreparedProgram(
	b *testing.B,
	engine programEngine,
	spec programSpec,
	wrappedSource string,
	oracle programOracle,
) {
	prepared, err := engine.prepare(spec, wrappedSource)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := prepared.close(); err != nil {
			b.Error(err)
		}
	})

	if err := prepared.run(); err != nil {
		b.Fatalf("warmup: %v", err)
	}
	validatePreparedResult(b, prepared, oracle, "warmup")

	runtime.GC()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := prepared.run(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	validatePreparedResult(b, prepared, oracle, "timed run")
}

func TestProgramsExecute(t *testing.T) {
	// Keep the ordinary test suite quick. BenchmarkPrograms owns the scaled
	// measurement inputs; these cases only exercise the complete code paths.
	cases := []programCase{
		{
			name:  "binarytrees",
			input: 6,
			oracle: programOracle{exact: "" +
				"stretch tree of depth 7\t check: 255\n" +
				"64\t trees of depth 4\t check: 1984\n" +
				"16\t trees of depth 6\t check: 2032\n" +
				"long lived tree of depth 6\t check: 127\n"},
		},
		{
			name:   "fannkuchredux",
			input:  5,
			oracle: programOracle{exact: "11\nPfannkuchen(5) = 7\n"},
		},
		{
			name:  "nbody",
			input: 10,
			oracle: programOracle{
				floatValues:  []float64{-0.169075164, -0.169073022},
				absTolerance: 5e-9,
			},
		},
		{
			name:  "spectralnorm",
			input: 10,
			oracle: programOracle{
				floatValues:  []float64{1.271844019},
				absTolerance: 5e-9,
			},
		},
	}

	for _, smokeCase := range cases {
		spec, ok := findProgramSpec(smokeCase.name)
		if !ok {
			t.Fatalf("unknown program %q", smokeCase.name)
		}
		source, err := loadProgramSource(spec)
		if err != nil {
			t.Fatal(err)
		}
		wrapped := wrapProgramSource(source, smokeCase.input)
		t.Run(smokeCase.name, func(t *testing.T) {
			for _, engine := range programEngines {
				t.Run(engine.name, func(t *testing.T) {
					prepared, err := engine.prepare(spec, wrapped)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := prepared.close(); err != nil {
							t.Error(err)
						}
					})
					if err := prepared.run(); err != nil {
						t.Fatal(err)
					}
					validatePreparedResult(
						t,
						prepared,
						smokeCase.oracle,
						"smoke run",
					)
				})
			}
		})
	}
}

func TestProgramSourcesMatchUpstream(t *testing.T) {
	seen := make(map[string]bool, len(programSpecs))
	for _, spec := range programSpecs {
		if seen[spec.name] {
			t.Fatalf("duplicate program %q", spec.name)
		}
		seen[spec.name] = true
		if spec.sourcePage == "" || spec.sourceArchivePath == "" {
			t.Errorf("program %q has incomplete provenance", spec.name)
		}

		source, err := loadProgramSource(spec)
		if err != nil {
			t.Fatal(err)
		}
		got := fmt.Sprintf("%x", sha256.Sum256([]byte(source)))
		if got != spec.sourceSHA256 {
			t.Errorf(
				"%s SHA-256 = %s, want %s",
				spec.sourceFile,
				got,
				spec.sourceSHA256,
			)
		}

		wrapped := wrapProgramSource(source, 1)
		if strings.Count(wrapped, source) != 1 {
			t.Errorf(
				"wrapped %s does not contain exactly one unchanged source copy",
				spec.name,
			)
		}
	}
}

func validatePreparedResult(
	tb testing.TB,
	prepared *preparedProgram,
	oracle programOracle,
	stage string,
) {
	tb.Helper()
	output, err := prepared.result()
	if err != nil {
		tb.Fatalf("%s result: %v", stage, err)
	}
	if err := oracle.validate(output); err != nil {
		tb.Fatalf("%s: %v", stage, err)
	}
}

func (oracle programOracle) validate(output string) error {
	if oracle.exact != "" {
		if output != oracle.exact {
			return fmt.Errorf("output = %q, want %q", output, oracle.exact)
		}
		return nil
	}
	if len(oracle.floatValues) == 0 {
		return fmt.Errorf("oracle has neither exact nor numeric values")
	}
	fields := strings.Fields(output)
	if len(fields) != len(oracle.floatValues) {
		return fmt.Errorf(
			"output fields = %q, want %d numeric values",
			fields,
			len(oracle.floatValues),
		)
	}
	for index, field := range fields {
		got, err := strconv.ParseFloat(field, 64)
		if err != nil {
			return fmt.Errorf("output field %d (%q): %w", index, field, err)
		}
		want := oracle.floatValues[index]
		if math.IsNaN(got) || math.Abs(got-want) > oracle.absTolerance {
			return fmt.Errorf(
				"output value %d = %.12g, want %.12g ± %.3g",
				index,
				got,
				want,
				oracle.absTolerance,
			)
		}
	}
	return nil
}

func findProgramSpec(name string) (programSpec, bool) {
	for _, spec := range programSpecs {
		if spec.name == name {
			return spec, true
		}
	}
	return programSpec{}, false
}

func loadProgramSource(spec programSpec) (string, error) {
	path := "programs/" + spec.sourceFile
	source, err := programSourceFS.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(source), nil
}

func wrapProgramSource(source string, input int) string {
	return `local __benchmark_output = ""
local function __benchmark_write(...)
  for i = 1, select("#", ...) do
    __benchmark_output = __benchmark_output .. tostring(select(i, ...))
  end
end
local io = { write = __benchmark_write }
local arg = { "` + strconv.Itoa(input) + `" }

function benchmark_program()
  __benchmark_output = ""
  do
` + source + `  end
  benchmark_program_result = __benchmark_output
end
`
}

func prepareLunarProgram(
	spec programSpec,
	source string,
) (*preparedProgram, error) {
	state, err := lunar.New(lunar.Options{})
	if err != nil {
		return nil, fmt.Errorf("create Lunar state: %w", err)
	}
	fail := func(err error) (*preparedProgram, error) {
		_ = state.Close()
		return nil, err
	}
	if spec.libraries&programLibraryBase != 0 {
		if err := state.OpenBase(); err != nil {
			return fail(fmt.Errorf("open Lunar base library: %w", err))
		}
	}
	if spec.libraries&programLibraryString != 0 {
		if err := state.OpenString(); err != nil {
			return fail(fmt.Errorf("open Lunar string library: %w", err))
		}
	}
	if spec.libraries&programLibraryMath != 0 {
		if err := state.OpenMath(); err != nil {
			return fail(fmt.Errorf("open Lunar math library: %w", err))
		}
	}

	sourceName := "@programs/wrapped-" + spec.sourceFile
	chunk, err := state.LoadString(sourceName, source)
	if err != nil {
		return fail(fmt.Errorf("load Lunar %s: %w", spec.name, err))
	}
	if _, err := state.CallInto(chunk.Value(), nil, nil); err != nil {
		return fail(fmt.Errorf("initialize Lunar %s: %w", spec.name, err))
	}
	run, err := state.RawGlobal("benchmark_program")
	if err != nil {
		return fail(fmt.Errorf("resolve Lunar benchmark_program: %w", err))
	}
	if run.Kind() != lunar.FunctionKind {
		return fail(fmt.Errorf(
			"Lunar %s benchmark_program has kind %s, want function",
			spec.name,
			run.Kind(),
		))
	}

	return &preparedProgram{
		run: func() error {
			if _, err := state.CallInto(run, nil, nil); err != nil {
				return fmt.Errorf("Lunar %s: %w", spec.name, err)
			}
			return nil
		},
		result: func() (string, error) {
			value, err := state.RawGlobal("benchmark_program_result")
			if err != nil {
				return "", err
			}
			result, ok := value.AsString()
			if !ok {
				return "", fmt.Errorf(
					"result has kind %s, want string",
					value.Kind(),
				)
			}
			return result, nil
		},
		close: state.Close,
	}, nil
}

func prepareGopherLuaProgram(
	spec programSpec,
	source string,
) (*preparedProgram, error) {
	state := gopherlua.NewState(gopherlua.Options{SkipOpenLibs: true})
	fail := func(err error) (*preparedProgram, error) {
		state.Close()
		return nil, err
	}
	if spec.libraries&programLibraryBase != 0 {
		gopherlua.OpenBase(state)
		state.Pop(1)
	}
	if spec.libraries&programLibraryString != 0 {
		gopherlua.OpenString(state)
		state.Pop(1)
	}
	if spec.libraries&programLibraryMath != 0 {
		gopherlua.OpenMath(state)
		state.Pop(1)
	}

	sourceName := "@programs/wrapped-" + spec.sourceFile
	chunk, err := state.Load(strings.NewReader(source), sourceName)
	if err != nil {
		return fail(fmt.Errorf("load GopherLua %s: %w", spec.name, err))
	}
	if err := state.CallByParam(gopherlua.P{
		Fn:      chunk,
		NRet:    0,
		Protect: true,
	}); err != nil {
		return fail(fmt.Errorf("initialize GopherLua %s: %w", spec.name, err))
	}
	run, ok := state.GetGlobal("benchmark_program").(*gopherlua.LFunction)
	if !ok {
		return fail(fmt.Errorf(
			"GopherLua %s benchmark_program is not a function",
			spec.name,
		))
	}
	call := gopherlua.P{Fn: run, NRet: 0, Protect: true}

	return &preparedProgram{
		run: func() error {
			if err := state.CallByParam(call); err != nil {
				return fmt.Errorf("GopherLua %s: %w", spec.name, err)
			}
			return nil
		},
		result: func() (string, error) {
			value, ok := state.GetGlobal(
				"benchmark_program_result",
			).(gopherlua.LString)
			if !ok {
				return "", fmt.Errorf("result is not a string")
			}
			return string(value), nil
		},
		close: func() error {
			state.Close()
			return nil
		},
	}, nil
}

func prepareGoLuaProgram(
	spec programSpec,
	source string,
) (*preparedProgram, error) {
	state := golua.NewState()
	if spec.libraries&programLibraryBase != 0 {
		golua.Require(state, "_G", golua.BaseOpen, true)
		state.Pop(1)
	}
	if spec.libraries&programLibraryString != 0 {
		golua.Require(state, "string", golua.StringOpen, true)
		state.Pop(1)
	}
	if spec.libraries&programLibraryMath != 0 {
		golua.Require(state, "math", golua.MathOpen, true)
		state.Pop(1)
	}

	sourceName := "@programs/wrapped-" + spec.sourceFile
	if err := state.Load(strings.NewReader(source), sourceName, "t"); err != nil {
		return nil, fmt.Errorf("load go-lua %s: %w", spec.name, err)
	}
	if err := state.ProtectedCall(0, 0, 0); err != nil {
		return nil, fmt.Errorf("initialize go-lua %s: %w", spec.name, err)
	}
	state.Global("benchmark_program")
	if !state.IsFunction(-1) {
		state.Pop(1)
		return nil, fmt.Errorf(
			"go-lua %s benchmark_program is not a function",
			spec.name,
		)
	}
	functionIndex := state.Top()

	return &preparedProgram{
		run: func() error {
			state.PushValue(functionIndex)
			if err := state.ProtectedCall(0, 0, 0); err != nil {
				return fmt.Errorf("go-lua %s: %w", spec.name, err)
			}
			return nil
		},
		result: func() (string, error) {
			state.Global("benchmark_program_result")
			result, ok := state.ToString(-1)
			state.Pop(1)
			if !ok {
				return "", fmt.Errorf("result is not a string")
			}
			return result, nil
		},
		close: func() error { return nil },
	}, nil
}
