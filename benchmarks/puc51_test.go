//go:build puc51 && cgo

package compare

import (
	"runtime"
	"testing"

	"github.com/mmcdole/lunar/benchmarks/internal/puc51"
)

func init() {
	programEngines = append(programEngines, programEngine{
		name: "puc51", prepare: preparePUC51Program, nativeHeap: true,
	})
	interpreterEngines = append(interpreterEngines, interpreterEngine{
		name: "puc51", benchmark: benchmarkPUC51,
	})
}

func preparePUC51Program(spec programSpec, source string) (*preparedProgram, error) {
	var libraries puc51.Libraries
	if spec.libraries&programLibraryBase != 0 {
		libraries |= puc51.Base
	}
	if spec.libraries&programLibraryString != 0 {
		libraries |= puc51.String
	}
	if spec.libraries&programLibraryMath != 0 {
		libraries |= puc51.Math
	}
	state, err := puc51.New(libraries)
	if err != nil {
		return nil, err
	}
	if err := state.Prepare("@programs/wrapped-"+spec.sourceFile, source, "benchmark_program"); err != nil {
		_ = state.Close()
		return nil, err
	}
	return &preparedProgram{
		run: state.Run,
		result: func() (string, error) {
			return state.GlobalString("benchmark_program_result")
		},
		close: state.Close,
	}, nil
}

func preparePUC51Interpreter(tb testing.TB, workload workload) *puc51.State {
	tb.Helper()
	state, err := puc51.New(0)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := state.Close(); err != nil {
			tb.Error(err)
		}
	})
	if err := state.Prepare("@compare.lua", workload.source, "benchmark"); err != nil {
		tb.Fatal(err)
	}
	return state
}

func validatePUC51Result(tb testing.TB, state *puc51.State, workload workload) {
	tb.Helper()
	result, err := state.GlobalNumber("benchmark_result")
	if err != nil {
		tb.Fatal(err)
	}
	if result != workload.want {
		tb.Fatalf("result = %v, want %v", result, workload.want)
	}
}

func benchmarkPUC51(b *testing.B, workload workload) {
	state := preparePUC51Interpreter(b, workload)
	if err := state.Run(); err != nil {
		b.Fatal(err)
	}
	validatePUC51Result(b, state, workload)

	runtime.GC()
	// Go allocation counters cannot observe PUC Lua's native heap.
	b.ResetTimer()
	for b.Loop() {
		if err := state.Run(); err != nil {
			b.Fatal(err)
		}
	}
	validatePUC51Result(b, state, workload)
}

func TestPUC51InterpreterExecute(t *testing.T) {
	for _, workload := range runtimeWorkloads {
		t.Run(workload.name, func(t *testing.T) {
			state := preparePUC51Interpreter(t, workload)
			for range 2 {
				if err := state.Run(); err != nil {
					t.Fatal(err)
				}
				validatePUC51Result(t, state, workload)
			}
		})
	}
}
