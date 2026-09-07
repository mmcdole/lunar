package compare

import "testing"

// These probes isolate paths absent from the canonical interpreter rows.
// Keep their results separate from the established program comparisons.
var diagnosticWorkloads = []workload{
	{name: "call_only", source: `function benchmark() benchmark_result = 1 end`, want: 1},
	{name: "numeric_pow_10000", source: `
function benchmark()
  local total = 0
  for i = 1, 10000 do total = total + i^2 end
  benchmark_result = total
end`, want: 333383335000},
	{name: "native_sqrt_10000", source: `
local sqrt = math.sqrt
function benchmark()
  local total = 0
  for i = 1, 10000 do total = total + sqrt(i*i) end
  benchmark_result = total
end`, want: 50005000},
	{name: "array_get_10000", source: `
local values = {}
for i = 1, 10000 do values[i] = i end
function benchmark()
  local total = 0
  for i = 1, 10000 do total = total + values[i] end
  benchmark_result = total
end`, want: 50005000},
	{name: "ipairs_10000", source: `
local values = {}
for i = 1, 10000 do values[i] = i end
function benchmark()
  local total = 0
  for _, value in ipairs(values) do total = total + value end
  benchmark_result = total
end`, want: 50005000},
	{name: "pairs_10000", source: `
local values = {}
for i = 1, 10000 do values[i] = i end
function benchmark()
  local total = 0
  for _, value in pairs(values) do total = total + value end
  benchmark_result = total
end`, want: 50005000},
	{name: "lua_iterator_10000", source: `
local function nextvalue(limit, previous)
  local value = previous + 1
  if value <= limit then return value end
end
function benchmark()
  local total = 0
  for value in nextvalue, 10000, 0 do total = total + value end
  benchmark_result = total
end`, want: 50005000},
	{name: "short_concat_10000", source: `
local left, right = "abcdef", "ghijkl"
function benchmark()
  local total = 0
  for i = 1, 10000 do local value = left .. right; total = total + #value end
  benchmark_result = total
end`, want: 120000},
	{name: "table_churn_1000", source: `
function benchmark()
  local total = 0
  for i = 1, 1000 do
    local value = {i, i+1, value=i+2}
    total = total + value[1] + value[2] + value.value
  end
  benchmark_result = total
end`, want: 1504500},
	{name: "string_sub_identity_1000", source: `
local sub = string.sub
local subject = string.rep("x", 65536)
function benchmark()
  local total = 0
  for i = 1, 1000 do local value = sub(subject, 1); total = total + #value end
  benchmark_result = total
end`, want: 65536000},
	{name: "collect_live_10000", source: `
local values = {}
for i = 1, 10000 do values[i] = {i, value="value" .. i} end
function benchmark()
  collectgarbage("collect")
  benchmark_result = #values
end`, want: 10000},
}

func diagnosticSource(w workload) string {
	return w.source + `
function benchmark_program()
  benchmark()
  benchmark_program_result = tostring(benchmark_result)
end
`
}

func diagnosticSpec(w workload) programSpec {
	return programSpec{name: w.name, sourceFile: w.name + ".lua",
		libraries: programLibraryBase | programLibraryString | programLibraryMath}
}

func BenchmarkDiagnostics(b *testing.B) {
	for _, w := range diagnosticWorkloads {
		b.Run("case="+w.name, func(b *testing.B) {
			for _, engine := range programEngines {
				b.Run("runtime="+engine.name, func(b *testing.B) {
					benchmarkPreparedProgram(b, engine, diagnosticSpec(w), diagnosticSource(w),
						programOracle{floatValues: []float64{w.want}})
				})
			}
		})
	}
}

func TestDiagnosticsExecute(t *testing.T) {
	for _, w := range diagnosticWorkloads {
		for _, engine := range programEngines {
			t.Run(w.name+"/"+engine.name, func(t *testing.T) {
				prepared, err := engine.prepare(diagnosticSpec(w), diagnosticSource(w))
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := prepared.close(); err != nil {
						t.Error(err)
					}
				}()
				for i := 0; i < 2; i++ {
					if err := prepared.run(); err != nil {
						t.Fatal(err)
					}
					validatePreparedResult(t, prepared, programOracle{floatValues: []float64{w.want}}, "diagnostic")
				}
			})
		}
	}
}
