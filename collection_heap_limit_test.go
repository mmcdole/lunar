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
