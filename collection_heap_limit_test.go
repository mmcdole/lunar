package lua

import (
	"errors"
	"strings"
	"testing"
)

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
	if failure.Category() != ResourceError {
		t.Fatalf("category = %v; want ResourceError", failure.Category())
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

func TestHeapLimitIsCatchableByLua(t *testing.T) {
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

	results, err := state.DoString("@caught.lua", `
		local kept = {}
		local ok, message = pcall(function()
			for index = 1, 4096 do
				kept[index] = string.rep("z", 64 * 1024)
			end
		end)
		kept = nil
		return ok, message
	`)
	if err != nil {
		t.Fatalf("pcall over the heap limit failed: %v", err)
	}
	if ok, _ := results[0].AsBool(); ok {
		t.Fatal("pcall reported success under an exceeded heap limit")
	}
	if message, _ := results[1].AsString(); !strings.Contains(
		message,
		"heap limit exceeded",
	) {
		t.Fatalf("caught message = %q", message)
	}
}

// An xpcall error handler receives bounded emergency headroom, mirroring
// MaxValues and MaxFrames, so it can allocate its report instead of dying
// with Lua's "error in error handling".
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
		local kept = {}
		local ok, message = xpcall(function()
			for index = 1, 8192 do
				kept[index] = string.rep("x", 64 * 1024)
			end
		end, function(err)
			-- The handler allocates while the State is over the limit.
			return "handled: " .. string.format("%s", err)
		end)
		return ok, message
	`)
	if err != nil {
		t.Fatalf("xpcall over the heap limit failed: %v", err)
	}
	if ok, _ := results[0].AsBool(); ok {
		t.Fatal("xpcall reported success under an exceeded heap limit")
	}
	message, _ := results[1].AsString()
	if message == "error in error handling" {
		t.Fatal("the error handler died instead of reporting the failure")
	}
	if !strings.Contains(message, "handled: ") ||
		!strings.Contains(message, "heap limit exceeded") {
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
	if err := state.SetRawGlobal("held", table.Value()); err != nil {
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
