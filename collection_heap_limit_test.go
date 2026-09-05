package lua

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestHeapLimitAppliesWhileCollectionStopped(t *testing.T) {
	for _, stop := range []string{"lua", "host"} {
		for _, protected := range []bool{false, true} {
			name := stop + "/direct"
			if protected {
				name = stop + "/pcall"
			}
			t.Run(name, func(t *testing.T) {
				state, err := New(Options{MaxHeapBytes: 1 << 20})
				if err != nil {
					t.Fatal(err)
				}
				defer state.Close()
				if err := state.OpenBase(); err != nil {
					t.Fatal(err)
				}
				if err := state.OpenString(); err != nil {
					t.Fatal(err)
				}

				source := `
					kept = {}
					for index = 1, 64 do
						kept[index] = string.rep("x", 64 * 1024)
					end
				`
				if protected {
					source = "return pcall(function() " + source + " end)"
				}
				if stop == "lua" {
					source = `collectgarbage("stop"); ` + source
				} else if err := state.StopGC(); err != nil {
					t.Fatal(err)
				}

				_, err = state.DoString("@stopped-heap.lua", source)
				var failure *Error
				if !errors.As(err, &failure) || failure.Category() != LimitError {
					t.Fatalf("stopped heap growth error = %v; want LimitError", err)
				}
				if !strings.Contains(failure.Error(), "heap limit exceeded") {
					t.Fatalf("message = %q", failure.Error())
				}
				if !state.runtime.collection.stopped {
					t.Fatal("heap enforcement restarted automatic collection")
				}
			})
		}
	}
}

func TestHeapLimitStopsSustainedGrowth(t *testing.T) {
	state, err := New(Options{MaxHeapBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	for _, open := range []func() error{
		state.OpenBase,
		state.OpenString,
		state.OpenTable,
	} {
		if err := open(); err != nil {
			t.Fatal(err)
		}
	}

	// Retaining every chunk keeps the heap growing across safe points, so
	// no collection can bring the State back under the limit.
	_, err = state.DoString("@grow.lua", `
		local kept = {}
		for index = 1, 4096 do
			kept[index] = string.rep("x", 64 * 1024)
		end
	`)
	if err == nil {
		t.Fatal("unbounded retention completed under a heap limit")
	}
	var failure *Error
	if !errors.As(err, &failure) {
		t.Fatalf("error is not a *Error: %v", err)
	}
	if failure.Category() != LimitError {
		t.Fatalf("category = %v; want LimitError", failure.Category())
	}
	if !strings.Contains(failure.Error(), "heap limit exceeded") {
		t.Fatalf("message = %q", failure.Error())
	}
}

// The limit must bound retention, not allocation rate: a loop that
// allocates far more than the limit in total stays legal as long as each
// allocation becomes unreachable.
func TestHeapLimitAllowsCollectableChurn(t *testing.T) {
	state, err := New(Options{MaxHeapBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	for _, open := range []func() error{
		state.OpenBase,
		state.OpenString,
		state.OpenTable,
	} {
		if err := open(); err != nil {
			t.Fatal(err)
		}
	}

	results, err := state.DoString("@churn.lua", `
		local total = 0
		for index = 1, 2048 do
			local scratch = string.rep("y", 64 * 1024)
			total = total + #scratch
		end
		return total
	`)
	if err != nil {
		t.Fatalf("collectable churn hit the heap limit: %v", err)
	}
	if total, _ := results[0].AsNumber(); total != 2048*64*1024 {
		t.Fatalf("allocated total = %v", total)
	}
}

func TestHeapLimitIsNotCatchableByLua(t *testing.T) {
	state, err := New(Options{MaxHeapBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	for _, open := range []func() error{
		state.OpenBase,
		state.OpenString,
		state.OpenTable,
	} {
		if err := open(); err != nil {
			t.Fatal(err)
		}
	}

	// A script must not be able to catch the ceiling that bounds it and keep
	// allocating, so the failure passes straight through pcall to the host.
	_, err = state.DoString("@caught.lua", `
		local kept = {}
		local rounds = 0
		while true do
			rounds = rounds + 1
			pcall(function()
				for index = 1, 64 do
					kept[#kept + 1] = string.rep("z", 64 * 1024)
				end
			end)
			if rounds > 4096 then
				return "escaped the ceiling"
			end
		end
	`)
	if err == nil {
		t.Fatal("pcall absorbed the heap ceiling and kept allocating")
	}
	var failure *Error
	if !errors.As(err, &failure) {
		t.Fatalf("error is not a *Error: %v", err)
	}
	if failure.Category() != LimitError {
		t.Fatalf("category = %v; want LimitError", failure.Category())
	}
	if !strings.Contains(failure.Error(), "heap limit exceeded") {
		t.Fatalf("message = %q", failure.Error())
	}
}

// An xpcall error handler receives bounded emergency headroom, mirroring
// MaxValues and MaxFrames. A heap ceiling is not catchable, so the headroom
// exists for the ordinary case: a handler reporting some other failure while
// the State already sits near its limit must be able to allocate its report
// instead of dying with Lua's "error in error handling".
func TestHeapLimitGrantsErrorHandlersHeadroom(t *testing.T) {
	state, err := New(Options{MaxHeapBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	for _, open := range []func() error{
		state.OpenBase,
		state.OpenString,
		state.OpenTable,
	} {
		if err := open(); err != nil {
			t.Fatal(err)
		}
	}

	results, err := state.DoString("@handled.lua", `
		-- Park the State just under its ceiling.
		ballast = {}
		for index = 1, 96 do
			ballast[index] = string.rep("x", 64 * 1024)
		end
		local ok, message = xpcall(function()
			error("ordinary failure")
		end, function(err)
			-- The handler allocates while the State sits near the limit.
			return "handled: " .. string.format("%s", err)
		end)
		return ok, message
	`)
	if err != nil {
		t.Fatalf("xpcall near the heap limit failed: %v", err)
	}
	if ok, _ := results[0].AsBool(); ok {
		t.Fatal("xpcall reported success for a failing target")
	}
	message, _ := results[1].AsString()
	if message == "error in error handling" {
		t.Fatal("the error handler died instead of reporting the failure")
	}
	if !strings.Contains(message, "handled: ") ||
		!strings.Contains(message, "ordinary failure") {
		t.Fatalf("handler report = %q", message)
	}
}

func TestZeroHeapLimitLeavesTheHeapUnlimited(t *testing.T) {
	state, err := New(Options{MaxValues: 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenString(); err != nil {
		t.Fatal(err)
	}

	// MaxValues bounds slots, not bytes: one slot may hold a large string.
	if _, err := state.DoString(
		"@fat.lua",
		`big = string.rep("x", 32 * 1024 * 1024) return #big`,
	); err != nil {
		t.Fatalf("unlimited heap rejected a large allocation: %v", err)
	}
	measured, err := state.HeapBytes()
	if err != nil {
		t.Fatal(err)
	}
	if measured < 32<<20 {
		t.Fatalf("HeapBytes = %d; want at least the retained string", measured)
	}
}

func TestNegativeHeapLimitIsRejected(t *testing.T) {
	if _, err := New(Options{MaxHeapBytes: -1}); !errors.Is(
		err,
		ErrNegativeCapacity,
	) {
		t.Fatalf("New(MaxHeapBytes: -1) error = %v; want ErrNegativeCapacity", err)
	}
}

// Raw operations and explicit collection do not run the executor, so a
// host can build and inspect a State holding more than the limit allows.
// Only operations that reach an execution safe point enforce it.
func TestHeapLimitAppliesOnlyToExecution(t *testing.T) {
	state, err := New(Options{MaxHeapBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	table, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetString("blob", state.String(
		strings.Repeat("q", 4<<20),
	)); err != nil {
		t.Fatalf("raw construction hit the heap limit: %v", err)
	}
	if err := state.RawSetGlobal("held", table.Value()); err != nil {
		t.Fatalf("raw global assignment hit the heap limit: %v", err)
	}
	if err := state.Collect(); err != nil {
		t.Fatalf("explicit Collect raised the heap limit: %v", err)
	}
	measured, err := state.HeapBytes()
	if err != nil {
		t.Fatal(err)
	}
	if measured < 4<<20 {
		t.Fatalf("HeapBytes = %d; want the retained string to survive", measured)
	}

	// The same over-limit State refuses to execute.
	if _, err := state.DoString("@run.lua", "return 1"); err == nil {
		t.Fatal("execution proceeded with the heap already over the limit")
	} else if !strings.Contains(err.Error(), "heap limit exceeded") {
		t.Fatalf("execution error = %v", err)
	}
}

type heapLimitBenchmarkMode struct {
	name    string
	limit   int
	stopped bool
	near    bool
}

var heapLimitBenchmarkModes = [...]heapLimitBenchmarkMode{
	{name: "running_unlimited"},
	{name: "running_limited", limit: 8 << 20},
	{name: "stopped_unlimited", stopped: true},
	{name: "stopped_limited_under", limit: 8 << 20, stopped: true},
	{name: "stopped_limited_near", limit: 8 << 20, stopped: true, near: true},
}

const heapLimitBenchmarkIterations = 4096

func BenchmarkHeapLimitAllocation(b *testing.B) {
	for _, mode := range heapLimitBenchmarkModes {
		b.Run(mode.name, func(b *testing.B) {
			state, allocate, safePoints := newHeapLimitBenchmarkState(b, mode)
			runHeapLimitBenchmark(b, state, allocate, heapLimitBenchmarkIterations*1024)
			// Clear setup-only host tokens before establishing the measured
			// heap baseline; keep both kernel functions rooted for every batch.
			runtime.GC()
			collectHeapLimitBenchmark(b, state, mode)

			b.ReportAllocs()
			b.SetBytes(heapLimitBenchmarkIterations * 1024)
			b.ResetTimer()
			for range b.N {
				runHeapLimitBenchmark(b, state, allocate, heapLimitBenchmarkIterations*1024)
				// Each operation allocates one bounded batch. Explicit cleanup
				// also bounds the stopped collector's attribution storage, and
				// stays outside the allocation/enforcement measurement.
				b.StopTimer()
				collectHeapLimitBenchmark(b, state, mode)
				b.StartTimer()
			}
			b.StopTimer()
			runtime.KeepAlive(allocate)
			runtime.KeepAlive(safePoints)
		})
	}
}

func BenchmarkHeapLimitSafePoints(b *testing.B) {
	for _, mode := range heapLimitBenchmarkModes {
		b.Run(mode.name, func(b *testing.B) {
			state, allocate, safePoints := newHeapLimitBenchmarkState(b, mode)
			// Stabilize the setup graph before priming heap enforcement, so
			// cleanup cannot erase the measurement state this benchmark probes.
			runtime.GC()
			collectHeapLimitBenchmark(b, state, mode)
			// Near the limit, transient allocation makes charged growth exceed
			// the remaining headroom while the measured heap still fits. Once
			// that measurement completes, allocation-free calls must not keep
			// scanning the same retained graph at every execution safe point.
			runHeapLimitBenchmark(b, state, allocate, heapLimitBenchmarkIterations*1024)
			runHeapLimitBenchmark(b, state, safePoints, heapLimitBenchmarkIterations)
			if mode.limit != 0 {
				held, err := state.HeapBytes()
				if err != nil {
					b.Fatal(err)
				}
				if held >= uint64(mode.limit) {
					b.Fatalf("primed heap = %d; want below %d", held, mode.limit)
				}
			}

			b.ReportAllocs()
			b.ResetTimer()
			b.ReportMetric(heapLimitBenchmarkIterations, "native-calls/op")
			for range b.N {
				runHeapLimitBenchmark(b, state, safePoints, heapLimitBenchmarkIterations)
			}
			b.StopTimer()
			runtime.KeepAlive(allocate)
			runtime.KeepAlive(safePoints)
		})
	}
}

func newHeapLimitBenchmarkState(
	b *testing.B,
	mode heapLimitBenchmarkMode,
) (*State, Value, Value) {
	b.Helper()
	state, err := New(Options{MaxHeapBytes: mode.limit})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := state.Close(); err != nil {
			b.Error(err)
		}
	})
	if err := state.OpenString(); err != nil {
		b.Fatal(err)
	}
	functions, err := state.DoString("@heap-limit-benchmark.lua", `
local repeat_string, string_length = string.rep, string.len
local ballast = {}
for index = 1, 4096 do
	ballast[index] = { index, index + 1, index + 2, index + 3 }
end
heap_benchmark_ballast = ballast
return function()
	local total = 0
	for index = 1, 4096 do
		local scratch = repeat_string("p", 1024)
		total = total + #scratch
	end
	return total
end, function()
	local total = 0
	for index = 1, 4096 do
		total = total + string_length("p")
	end
	return total
end
`)
	if err != nil {
		b.Fatal(err)
	}
	if len(functions) != 2 {
		b.Fatalf("benchmark factory returned %d functions; want 2", len(functions))
	}
	runHeapLimitBenchmark(b, state, functions[0], heapLimitBenchmarkIterations*1024)
	runHeapLimitBenchmark(b, state, functions[1], heapLimitBenchmarkIterations)
	if err := state.Collect(); err != nil {
		b.Fatal(err)
	}
	if mode.near {
		held, err := state.HeapBytes()
		if err != nil {
			b.Fatal(err)
		}
		target := uint64(mode.limit - (2 << 20))
		if held >= target {
			b.Fatalf("benchmark graph = %d bytes; want room below %d", held, target)
		}
		if err := state.RawSetGlobal("heap_benchmark_padding", state.String(
			strings.Repeat("b", int(target-held)),
		)); err != nil {
			b.Fatal(err)
		}
		if err := state.Collect(); err != nil {
			b.Fatal(err)
		}
	}
	if mode.stopped {
		if err := state.StopGC(); err != nil {
			b.Fatal(err)
		}
	}
	return state, functions[0], functions[1]
}

func runHeapLimitBenchmark(
	b *testing.B,
	state *State,
	function Value,
	want float64,
) {
	b.Helper()
	value, err := state.CallOne(function)
	if err != nil {
		b.Fatal(err)
	}
	if result, ok := value.AsNumber(); !ok || result != want {
		b.Fatalf("benchmark result = %v; want %v", value, want)
	}
}

func collectHeapLimitBenchmark(
	b *testing.B,
	state *State,
	mode heapLimitBenchmarkMode,
) {
	b.Helper()
	if err := state.Collect(); err != nil {
		b.Fatal(err)
	}
	// Explicit collection resumes Lua's collector; restore the benchmark's
	// selected policy before its next allocation batch.
	if mode.stopped {
		if err := state.StopGC(); err != nil {
			b.Fatal(err)
		}
	}
}
