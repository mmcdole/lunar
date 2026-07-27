package compare

import (
	"math"
	"runtime"
	"testing"

	golua "github.com/Shopify/go-lua"
	lugo "github.com/mmcdole/lugo"
	gopherlua "github.com/yuin/gopher-lua"
)

type workload struct {
	name   string
	source string
	want   float64
}

var runtimeWorkloads = []workload{
	{
		name: "numeric_for_10000",
		source: `
function benchmark()
	local total = 0
	for i = 1, 10000 do
		total = total + i
	end
	benchmark_result = total
end
`,
		want: 50_005_000,
	},
	{
		name: "fixed_lua_calls_1000",
		source: `
local function increment(value)
	return value + 1
end
function benchmark()
	local value = 0
	for _ = 1, 1000 do
		value = increment(value)
	end
	benchmark_result = value
end
`,
		want: 1_000,
	},
	{
		name: "table_field_get_set_10000",
		source: `
local object = { value = 0 }
function benchmark()
	object.value = 0
	for _ = 1, 10000 do
		object.value = object.value + 1
	end
	benchmark_result = object.value
end
`,
		want: 10_000,
	},
	{
		name: "string_append_256",
		source: `
function benchmark()
	local value = ""
	for _ = 1, 256 do
		value = value .. "abcdefgh"
	end
	benchmark_result = #value
end
`,
		want: 2_048,
	},
}

func BenchmarkInterpreter(b *testing.B) {
	for _, workload := range runtimeWorkloads {
		b.Run("case="+workload.name, func(b *testing.B) {
			b.Run("runtime=lugo", func(b *testing.B) {
				benchmarkLugo(b, workload)
			})
			b.Run("runtime=gopherlua", func(b *testing.B) {
				benchmarkGopherLua(b, workload)
			})
			b.Run("runtime=golua", func(b *testing.B) {
				benchmarkGoLua(b, workload)
			})
		})
	}
}

func benchmarkLugo(b *testing.B, workload workload) {
	state, err := lugo.New(lugo.Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := state.Close(); err != nil {
			b.Error(err)
		}
	})
	chunk, err := state.LoadString("@compare.lua", workload.source)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := state.CallInto(chunk.Value(), nil, nil); err != nil {
		b.Fatal(err)
	}
	function, err := state.Global("benchmark")
	if err != nil {
		b.Fatal(err)
	}
	if _, err := state.CallInto(function, nil, nil); err != nil {
		b.Fatal(err)
	}
	validateLugoResult(b, state, workload)

	runtime.GC()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := state.CallInto(function, nil, nil); err != nil {
			b.Fatal(err)
		}
	}
	validateLugoResult(b, state, workload)
}

func validateLugoResult(
	b *testing.B,
	state *lugo.State,
	workload workload,
) {
	b.Helper()
	result, err := state.Global("benchmark_result")
	if err != nil {
		b.Fatal(err)
	}
	number, ok := result.AsNumber()
	if !ok || number != workload.want {
		b.Fatalf(
			"result = (%v, %v), want %v",
			number,
			ok,
			workload.want,
		)
	}
}

func benchmarkGopherLua(b *testing.B, workload workload) {
	state := gopherlua.NewState(gopherlua.Options{SkipOpenLibs: true})
	b.Cleanup(state.Close)
	if err := state.DoString(workload.source); err != nil {
		b.Fatal(err)
	}
	call := gopherlua.P{
		Fn:      state.GetGlobal("benchmark"),
		NRet:    0,
		Protect: true,
	}
	if err := state.CallByParam(call); err != nil {
		b.Fatal(err)
	}
	validateGopherLuaResult(b, state, workload)

	runtime.GC()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := state.CallByParam(call); err != nil {
			b.Fatal(err)
		}
	}
	validateGopherLuaResult(b, state, workload)
}

func validateGopherLuaResult(
	b *testing.B,
	state *gopherlua.LState,
	workload workload,
) {
	b.Helper()
	result, ok := state.GetGlobal("benchmark_result").(gopherlua.LNumber)
	if !ok || float64(result) != workload.want {
		b.Fatalf(
			"result = (%v, %v), want %v",
			result,
			ok,
			workload.want,
		)
	}
}

func benchmarkGoLua(b *testing.B, workload workload) {
	state := golua.NewState()
	if err := golua.DoString(state, workload.source); err != nil {
		b.Fatal(err)
	}
	state.Global("benchmark")
	functionIndex := state.Top()
	call := func() {
		state.PushValue(functionIndex)
		if err := state.ProtectedCall(0, 0, 0); err != nil {
			b.Fatal(err)
		}
	}
	call()
	validateGoLuaResult(b, state, workload)

	runtime.GC()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		call()
	}
	validateGoLuaResult(b, state, workload)
}

func validateGoLuaResult(
	b *testing.B,
	state *golua.State,
	workload workload,
) {
	b.Helper()
	state.Global("benchmark_result")
	result, ok := state.ToNumber(-1)
	state.Pop(1)
	if !ok || math.Float64bits(result) != math.Float64bits(workload.want) {
		b.Fatalf(
			"result = (%v, %v), want %v",
			result,
			ok,
			workload.want,
		)
	}
}
