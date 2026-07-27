package lua

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"
)

type contextTestKey struct{}

func TestContextAdmissionAndFrameContext(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	calls := 0
	var seen context.Context
	probe, err := state.NewNativeFunction(func(frame Frame) Outcome {
		calls++
		seen = frame.Context()
		value, _ := frame.Argument(0)
		return frame.ReturnValue(value)
	})
	if err != nil {
		t.Fatal(err)
	}

	results, err := state.Call(probe.Value(), Number(1))
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(1))
	if seen != context.Background() {
		t.Fatalf("raw Call Frame.Context = %v; want context.Background", seen)
	}

	ctx := context.WithValue(
		context.Background(),
		contextTestKey{},
		"request",
	)
	results, err = state.CallContext(ctx, probe.Value(), Number(2))
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(2))
	if seen != ctx || seen.Value(contextTestKey{}) != "request" {
		t.Fatalf("context-aware Frame.Context = %v; want exact supplied context", seen)
	}

	destination := []Value{Number(70), Number(71)}
	if count, callErr := state.CallIntoContext(
		nil,
		probe.Value(),
		[]Value{Number(3)},
		destination,
	); count != 0 || !errors.Is(callErr, ErrNilContext) {
		t.Fatalf("nil-context CallInto = (%d, %v)", count, callErr)
	}
	assertTestValues(t, destination, Number(70), Number(71))
	if calls != 2 {
		t.Fatalf("calls after nil admission = %d; want 2", calls)
	}
	if _, callErr := state.CallContext(nil, probe.Value()); !errors.Is(
		callErr,
		ErrNilContext,
	) {
		t.Fatalf("nil-context Call = %v; want ErrNilContext", callErr)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, callErr := state.CallContext(
		cancelled,
		Value{},
	); !errors.Is(callErr, ErrInvalidValue) {
		t.Fatalf(
			"cancelled call with invalid callable = %v; want ErrInvalidValue",
			callErr,
		)
	}
	_, callErr := state.CallContext(
		cancelled,
		probe.Value(),
		Number(4),
	)
	failure := assertContextFailure(
		t,
		callErr,
		context.Canceled,
		context.Canceled,
		"context canceled",
	)
	if len(failure.Traceback()) != 0 {
		t.Fatalf("pre-entry cancellation traceback = %+v; want none", failure.Traceback())
	}
	if calls != 2 {
		t.Fatalf("pre-entry cancellation invoked native target %d times", calls)
	}
	assertContextStateIdle(t, state)

	fresh := context.WithValue(
		context.Background(),
		contextTestKey{},
		"fresh",
	)
	results, err = state.CallContext(fresh, probe.Value(), Number(5))
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(5))
	if seen != fresh {
		t.Fatal("state reused a stale execution context")
	}
	assertContextStateIdle(t, state)
}

func TestContextCausesPreserveStatusAndExplanation(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	calls := 0
	target, err := state.NewNativeFunction(func(frame Frame) Outcome {
		calls++
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelCause := errors.New("request quota revoked")
	deadlineCause := errors.New("request lease expired")

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, expire := context.WithDeadline(
		context.Background(),
		time.Unix(1, 0),
	)
	defer expire()
	cancelledWithCause, cancelWithCause := context.WithCancelCause(
		context.Background(),
	)
	cancelWithCause(cancelCause)
	expiredWithCause, expireWithCause := context.WithDeadlineCause(
		context.Background(),
		time.Unix(1, 0),
		deadlineCause,
	)
	defer expireWithCause()

	tests := []struct {
		name        string
		ctx         context.Context
		status      error
		explanation error
	}{
		{
			name:        "cancelled",
			ctx:         cancelled,
			status:      context.Canceled,
			explanation: context.Canceled,
		},
		{
			name:        "deadline",
			ctx:         expired,
			status:      context.DeadlineExceeded,
			explanation: context.DeadlineExceeded,
		},
		{
			name:        "cancel cause",
			ctx:         cancelledWithCause,
			status:      context.Canceled,
			explanation: cancelCause,
		},
		{
			name:        "deadline cause",
			ctx:         expiredWithCause,
			status:      context.DeadlineExceeded,
			explanation: deadlineCause,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			<-test.ctx.Done()
			_, callErr := state.CallContext(test.ctx, target.Value())
			failure := assertContextFailure(
				t,
				callErr,
				test.status,
				test.explanation,
				test.explanation.Error(),
			)
			if len(failure.Traceback()) != 0 {
				t.Fatalf(
					"pre-entry cause traceback = %+v; want none",
					failure.Traceback(),
				)
			}
			assertContextStateIdle(t, state)
		})
	}
	if calls != 0 {
		t.Fatalf("pre-cancelled cause targets ran %d times", calls)
	}
}

func TestContextCancellationAfterNativeReturnIsAtomic(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	cause := errors.New("stop atomic execution")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancelNow, err := state.NewNativeFunction(func(frame Frame) Outcome {
		if frame.Context() != ctx {
			return frame.RaiseString("native callback lost its context")
		}
		cancel(cause)
		return frame.ReturnNumber(99)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("context_cancel_now", cancelNow.Value()); err != nil {
		t.Fatal(err)
	}
	chunk := mustLoadString(t, state, "@context-atomic.lua", `before_cancel = 17
context_cancel_now()
after_cancel = 23
return 1, 2`)

	destination := []Value{Number(70), Number(71), Number(72)}
	count, callErr := state.CallIntoContext(
		ctx,
		chunk.Value(),
		nil,
		destination,
	)
	if count != 0 {
		t.Fatalf("cancelled CallInto count = %d; want 0", count)
	}
	failure := assertContextFailure(
		t,
		callErr,
		context.Canceled,
		cause,
		"context-atomic.lua:2: stop atomic execution",
	)
	assertTestValues(
		t,
		destination,
		Number(70),
		Number(71),
		Number(72),
	)
	before, err := state.Global("before_cancel")
	if err != nil {
		t.Fatal(err)
	}
	after, err := state.Global("after_cancel")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, before, Number(17))
	assertTestValue(t, after, Nil())
	assertExactContextTrace(
		t,
		failure,
		TraceFrame{
			Source:   "=[Go]",
			Function: "native function",
		},
		TraceFrame{
			Source: "@context-atomic.lua",
			Line:   2,
		},
	)
	assertContextStateIdle(t, state)

	success := mustLoadString(t, state, "@context-after-cancel.lua", `return 42`)
	results, err := state.Call(success.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(42))

	live, cancelAfter := context.WithCancel(context.Background())
	results, err = state.CallContext(live, success.Value())
	if err != nil {
		t.Fatal(err)
	}
	cancelAfter()
	assertTestValues(t, results, Number(42))
	results, err = state.Call(success.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(42))
	assertContextStateIdle(t, state)
}

func TestContextInterruptsLuaLoopsAndBlockingNativeCallbacks(t *testing.T) {
	t.Run("Lua loop cancellation", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()

		started := make(chan struct{}, 1)
		start, err := state.NewNativeFunction(func(frame Frame) Outcome {
			started <- struct{}{}
			return frame.Return()
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := state.SetGlobal("context_loop_started", start.Value()); err != nil {
			t.Fatal(err)
		}

		var source strings.Builder
		source.WriteString("context_loop_started()\nlocal value = 0\nwhile true do\n")
		for range 512 {
			source.WriteString("value = value + 1; value = value - 1\n")
		}
		source.WriteString("end")
		loop := mustLoadString(
			t,
			state,
			"@context-loop.lua",
			source.String(),
		)

		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, callErr := state.CallContext(ctx, loop.Value())
			result <- callErr
		}()
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("Lua loop did not start")
		}
		timer := time.AfterFunc(time.Millisecond, cancel)
		defer timer.Stop()
		select {
		case callErr := <-result:
			failure := assertContextFailure(
				t,
				callErr,
				context.Canceled,
				context.Canceled,
				"context-loop.lua:3: context canceled",
			)
			trace := failure.Traceback()
			if len(trace) != 1 ||
				trace[0].Source != "@context-loop.lua" ||
				trace[0].Line != 3 {
				t.Fatalf("loop cancellation traceback = %+v", trace)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Lua loop did not observe cancellation")
		}
		assertContextStateIdle(t, state)
	})

	t.Run("deadline", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		loop := mustLoadString(
			t,
			state,
			"@context-deadline.lua",
			`local value = 0
while true do
	value = value + 1
end`,
		)
		ctx, cancel := context.WithTimeout(
			context.Background(),
			10*time.Millisecond,
		)
		defer cancel()
		result := make(chan error, 1)
		go func() {
			_, callErr := state.CallContext(ctx, loop.Value())
			result <- callErr
		}()
		select {
		case callErr := <-result:
			failure := assertContextFailure(
				t,
				callErr,
				context.DeadlineExceeded,
				context.DeadlineExceeded,
				"context-deadline.lua:2: context deadline exceeded",
			)
			assertExactContextTrace(
				t,
				failure,
				TraceFrame{
					Source: "@context-deadline.lua",
					Line:   2,
				},
			)
		case <-time.After(2 * time.Second):
			t.Fatal("Lua loop did not observe its deadline")
		}
		assertContextStateIdle(t, state)
	})

	t.Run("blocking native", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		entered := make(chan struct{}, 1)
		native, err := state.NewNativeFunction(func(frame Frame) Outcome {
			entered <- struct{}{}
			<-frame.Context().Done()
			return frame.ReturnNumber(99)
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, callErr := state.CallContext(ctx, native.Value())
			result <- callErr
		}()
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("native callback did not start")
		}
		cancel()
		select {
		case callErr := <-result:
			failure := assertContextFailure(
				t,
				callErr,
				context.Canceled,
				context.Canceled,
				"context canceled",
			)
			assertExactContextTrace(
				t,
				failure,
				TraceFrame{
					Source:   "=[Go]",
					Function: "native function",
				},
			)
		case <-time.After(2 * time.Second):
			t.Fatal("native callback did not return after cancellation")
		}
		assertContextStateIdle(t, state)
	})
}

func TestContextPollingResumesWithoutRepeatingBackedges(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	iterations := int(contextPollInterval)*4 + 1
	iterationSum := iterations * (iterations + 1) / 2
	tailCalls := int(contextPollInterval)*2 + 1
	iteratorIterations := int(contextPollInterval)*2 + 1
	iteratorSum := iteratorIterations * (iteratorIterations + 1) / 2

	tests := []struct {
		name   string
		source string
		want   []Value
	}{
		{
			name: "numeric for",
			source: fmt.Sprintf(`local sum = 0
for index = 1, %d do
	sum = sum + index
end
return sum`, iterations),
			want: []Value{Number(float64(iterationSum))},
		},
		{
			name: "while",
			source: fmt.Sprintf(`local index, sum = 0, 0
while index < %d do
	index = index + 1
	sum = sum + index
end
return index, sum`, iterations),
			want: []Value{
				Number(float64(iterations)),
				Number(float64(iterationSum)),
			},
		},
		{
			name: "repeat",
			source: fmt.Sprintf(`local index = 0
repeat
	index = index + 1
until index == %d
return index`, iterations),
			want: []Value{Number(float64(iterations))},
		},
		{
			name: "tail calls",
			source: fmt.Sprintf(`local function descend(remaining)
	if remaining == 0 then
		return 77
	end
	return descend(remaining - 1)
end
return descend(%d)`, tailCalls),
			want: []Value{Number(77)},
		},
		{
			name: "generic for",
			source: fmt.Sprintf(`local function step(_, control)
	control = control + 1
	if control <= %d then
		return control
	end
end
local sum = 0
for value in step, nil, 0 do
	sum = sum + value
end
return sum`, iteratorIterations),
			want: []Value{Number(float64(iteratorSum))},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := mustLoadString(
				t,
				state,
				"@context-resume-"+test.name+".lua",
				test.source,
			)
			results, callErr := state.CallContext(ctx, target.Value())
			if callErr != nil {
				t.Fatal(callErr)
			}
			assertTestValues(t, results, test.want...)
			assertContextStateIdle(t, state)
		})
	}
}

func TestNestedFrameCallInheritsContextAndAppendsTraceOnce(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	cause := errors.New("nested request stopped")
	ctx, cancel := context.WithCancelCause(
		context.WithValue(
			context.Background(),
			contextTestKey{},
			"nested",
		),
	)
	cancelNow, err := state.NewNativeFunction(func(frame Frame) Outcome {
		if frame.Context() != ctx ||
			frame.Context().Value(contextTestKey{}) != "nested" {
			return frame.RaiseString("nested target lost its context")
		}
		cancel(cause)
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("context_nested_cancel", cancelNow.Value()); err != nil {
		t.Fatal(err)
	}
	inner := mustLoadString(t, state, "@context-inner.lua", `local function inner()
	context_nested_cancel()
end
inner()`)

	var nestedFailure *Error
	var nestedTrace []TraceFrame
	frameRestored := false
	bridge, err := state.NewNativeFunction(func(frame Frame) Outcome {
		if frame.Context() != ctx {
			return frame.RaiseString("outer Frame did not inherit context")
		}
		_, callErr := frame.Call(inner.Value())
		if !errors.As(callErr, &nestedFailure) {
			return frame.RaiseString("nested cancellation was not a Lua error")
		}
		nestedTrace = nestedFailure.Traceback()
		frameRestored = frame.Context() == ctx &&
			frame.ArgumentCount() == 1 &&
			frame.Kind(0) == NumberKind
		return frame.RaiseError(nestedFailure)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("context_nested_bridge", bridge.Value()); err != nil {
		t.Fatal(err)
	}
	outer := mustLoadString(
		t,
		state,
		"@context-outer.lua",
		`context_nested_bridge(7)`,
	)
	_, callErr := state.CallContext(ctx, outer.Value())
	failure := assertContextFailure(
		t,
		callErr,
		context.Canceled,
		cause,
		"context-inner.lua:2: nested request stopped",
	)
	if !frameRestored {
		t.Fatal("Frame.Call did not restore the outer Frame and its context")
	}
	assertContextTraceFrames(
		t,
		nestedTrace,
		TraceFrame{
			Source:   "=[Go]",
			Function: "native function",
		},
		TraceFrame{Source: "@context-inner.lua", Line: 2},
		TraceFrame{Source: "@context-inner.lua", Line: 4},
	)
	assertExactContextTrace(
		t,
		failure,
		TraceFrame{
			Source:   "=[Go]",
			Function: "native function",
		},
		TraceFrame{Source: "@context-inner.lua", Line: 2},
		TraceFrame{Source: "@context-inner.lua", Line: 4},
		TraceFrame{
			Source:   "=[Go]",
			Function: "native function",
		},
		TraceFrame{Source: "@context-outer.lua", Line: 1},
	)
	assertContextStateIdle(t, state)
}

func TestProtectedCallsCannotCatchContextCancellation(t *testing.T) {
	tests := []struct {
		name         string
		sourceName   string
		source       string
		handlerCalls int
		trace        []TraceFrame
	}{
		{
			name:       "pcall target",
			sourceName: "@context-pcall.lua",
			source: `local ok = pcall(function()
	context_cancel_target()
end)
after_protected_context = true`,
			trace: []TraceFrame{
				{
					Source:   "=[Go]",
					Function: "native function",
				},
				{Source: "@context-pcall.lua", Line: 2},
				{
					Source:   "=[Go]",
					Function: "native function",
				},
				{Source: "@context-pcall.lua", Line: 1},
			},
		},
		{
			name:       "xpcall target",
			sourceName: "@context-xpcall-target.lua",
			source: `local ok = xpcall(
	function()
		context_cancel_target()
	end,
	function()
		context_handler_called()
	end
)
after_protected_context = true`,
			trace: []TraceFrame{
				{
					Source:   "=[Go]",
					Function: "native function",
				},
				{Source: "@context-xpcall-target.lua", Line: 3},
				{
					Source:   "=[Go]",
					Function: "native function",
				},
				{Source: "@context-xpcall-target.lua", Line: 1},
			},
		},
		{
			name:         "xpcall handler",
			sourceName:   "@context-xpcall-handler.lua",
			handlerCalls: 1,
			source: `local ok = xpcall(
	function()
		return nil + 1
	end,
	function()
		context_cancel_handler()
	end
)
after_protected_context = true`,
			trace: []TraceFrame{
				{
					Source:   "=[Go]",
					Function: "native function",
				},
				{Source: "@context-xpcall-handler.lua", Line: 6},
				{Source: "@context-xpcall-handler.lua", Line: 3},
				{
					Source:   "=[Go]",
					Function: "native function",
				},
				{Source: "@context-xpcall-handler.lua", Line: 1},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := newStateWithBase(t, Options{})
			defer state.Close()

			ctx, cancel := context.WithCancel(context.Background())
			target, err := state.NewNativeFunction(func(frame Frame) Outcome {
				cancel()
				return frame.Return()
			})
			if err != nil {
				t.Fatal(err)
			}
			handlerCalls := 0
			handler, err := state.NewNativeFunction(func(frame Frame) Outcome {
				handlerCalls++
				cancel()
				return frame.Return()
			})
			if err != nil {
				t.Fatal(err)
			}
			observeHandler, err := state.NewNativeFunction(func(frame Frame) Outcome {
				handlerCalls++
				return frame.Return()
			})
			if err != nil {
				t.Fatal(err)
			}
			for name, function := range map[string]*Function{
				"context_cancel_target":  target,
				"context_cancel_handler": handler,
				"context_handler_called": observeHandler,
			} {
				if err := state.SetGlobal(name, function.Value()); err != nil {
					t.Fatal(err)
				}
			}
			chunk := mustLoadString(
				t,
				state,
				test.sourceName,
				test.source,
			)
			_, callErr := state.CallContext(ctx, chunk.Value())
			failure := assertContextFailure(
				t,
				callErr,
				context.Canceled,
				context.Canceled,
				sourceID(test.sourceName)+
					":"+contextTraceTopLuaLine(test.trace)+
					": context canceled",
			)
			after, err := state.Global("after_protected_context")
			if err != nil {
				t.Fatal(err)
			}
			assertTestValue(t, after, Nil())
			if handlerCalls != test.handlerCalls {
				t.Fatalf(
					"xpcall handler calls = %d; want %d",
					handlerCalls,
					test.handlerCalls,
				)
			}
			assertContextTraceFrames(t, failure.Traceback(), test.trace...)
			assertContextStateIdle(t, state)
		})
	}
}

func TestCoroutineContextsAreInheritedAndScopedPerResume(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()
	if err := state.OpenCoroutine(); err != nil {
		t.Fatal(err)
	}
	installYieldTestFunction(t, state)

	var seen []any
	probe, err := state.NewNativeFunction(func(frame Frame) Outcome {
		seen = append(
			seen,
			frame.Context().Value(contextTestKey{}),
		)
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("context_probe", probe.Value()); err != nil {
		t.Fatal(err)
	}
	entry := mustLoadString(t, state, "@context-resume.lua", `context_probe()
local value = native_yield("yielded")
context_probe()
return value`)
	thread, err := state.NewThread(entry.Value())
	if err != nil {
		t.Fatal(err)
	}

	first, cancelFirst := context.WithCancel(
		context.WithValue(
			context.Background(),
			contextTestKey{},
			"first",
		),
	)
	results, status, err := thread.ResumeContext(first)
	if err != nil || status != ThreadSuspended {
		t.Fatalf("first context resume = (status=%v, err=%v)", status, err)
	}
	assertTestValues(t, results, state.String("yielded"))
	if len(seen) != 1 || seen[0] != "first" {
		t.Fatalf("first resume contexts = %#v", seen)
	}
	cancelFirst()
	if state.execution != (executionControl{}) ||
		thread.runtimeObject().contextBudget != 0 {
		t.Fatalf(
			"yield retained execution context: state=%+v budget=%d",
			state.execution,
			thread.runtimeObject().contextBudget,
		)
	}

	second := context.WithValue(
		context.Background(),
		contextTestKey{},
		"second",
	)
	results, status, err = thread.ResumeContext(second, Number(9))
	if err != nil || status != ThreadDead {
		t.Fatalf("second context resume = (status=%v, err=%v)", status, err)
	}
	assertTestValues(t, results, Number(9))
	if len(seen) != 2 || seen[1] != "second" {
		t.Fatalf("second resume contexts = %#v", seen)
	}
	assertDeadCoroutineClean(t, thread)
	assertContextStateIdle(t, state)

	rawEntry := mustLoadString(
		t,
		state,
		"@context-raw-resume.lua",
		`context_probe()`,
	)
	rawThread, err := state.NewThread(rawEntry.Value())
	if err != nil {
		t.Fatal(err)
	}
	if _, status, err := rawThread.Resume(); err != nil ||
		status != ThreadDead {
		t.Fatalf("raw resume = (status=%v, err=%v)", status, err)
	}
	if len(seen) != 3 || seen[2] != nil {
		t.Fatalf("raw Resume Frame.Context values = %#v", seen)
	}

	childEntry := mustLoadString(t, state, "@context-child-inherit.lua", `context_probe()
local value = native_yield("child")
context_probe()
return value`)
	child, err := state.NewThread(childEntry.Value())
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("context_inherited_child", child.Value()); err != nil {
		t.Fatal(err)
	}
	parent := mustLoadString(t, state, "@context-parent-inherit.lua", `context_probe()
local ok, value = coroutine.resume(context_inherited_child)
context_probe()
return ok, value`)
	parentContext := context.WithValue(
		context.Background(),
		contextTestKey{},
		"parent",
	)
	results, err = state.CallContext(parentContext, parent.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Bool(true),
		state.String("child"),
	)
	if len(seen) != 6 ||
		seen[3] != "parent" ||
		seen[4] != "parent" ||
		seen[5] != "parent" {
		t.Fatalf("parent/child inherited contexts = %#v", seen)
	}
	if child.Status() != ThreadSuspended ||
		child.runtimeObject().contextBudget != 0 ||
		state.execution != (executionControl{}) {
		t.Fatalf(
			"internally yielded child retained context state: status=%v budget=%d state=%+v",
			child.Status(),
			child.runtimeObject().contextBudget,
			state.execution,
		)
	}

	childContext := context.WithValue(
		context.Background(),
		contextTestKey{},
		"external child",
	)
	results, status, err = child.ResumeContext(childContext, Number(23))
	if err != nil || status != ThreadDead {
		t.Fatalf("external child resume = (status=%v, err=%v)", status, err)
	}
	assertTestValues(t, results, Number(23))
	if len(seen) != 7 || seen[6] != "external child" {
		t.Fatalf("external child context = %#v", seen)
	}
	assertContextStateIdle(t, state)
}

func TestCoroutineContextAdmissionAndCancellationAreAtomic(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	calls := 0
	entry, err := state.NewNativeFunction(func(frame Frame) Outcome {
		calls++
		return frame.ReturnNumber(41)
	})
	if err != nil {
		t.Fatal(err)
	}
	thread, err := state.NewThread(entry.Value())
	if err != nil {
		t.Fatal(err)
	}
	destination := []Value{Number(70), Number(71)}

	if count, status, resumeErr := thread.ResumeIntoContext(
		nil,
		nil,
		destination,
	); count != 0 ||
		status != ThreadSuspended ||
		!errors.Is(resumeErr, ErrNilContext) {
		t.Fatalf(
			"nil-context resume = (count=%d, status=%v, err=%v)",
			count,
			status,
			resumeErr,
		)
	}
	assertTestValues(t, destination, Number(70), Number(71))
	if calls != 0 ||
		thread.runtimeObject().top != 1 ||
		len(thread.runtimeObject().frames) != 0 {
		t.Fatal("nil context changed the initial coroutine")
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	other, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	foreign, err := other.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if count, status, resumeErr := thread.ResumeIntoContext(
		cancelled,
		[]Value{foreign.Value()},
		destination,
	); count != 0 ||
		status != ThreadSuspended ||
		!errors.Is(resumeErr, ErrForeignValue) {
		t.Fatalf(
			"cancelled resume with foreign argument = (count=%d, status=%v, err=%v)",
			count,
			status,
			resumeErr,
		)
	}
	assertTestValues(t, destination, Number(70), Number(71))
	if calls != 0 ||
		thread.runtimeObject().top != 1 ||
		len(thread.runtimeObject().frames) != 0 {
		t.Fatal("rejected resume argument changed the suspended coroutine")
	}

	count, status, resumeErr := thread.ResumeIntoContext(
		cancelled,
		nil,
		destination,
	)
	if count != 0 || status != ThreadSuspended {
		t.Fatalf(
			"pre-cancelled resume = (count=%d, status=%v)",
			count,
			status,
		)
	}
	failure := assertContextFailure(
		t,
		resumeErr,
		context.Canceled,
		context.Canceled,
		"context canceled",
	)
	if len(failure.Traceback()) != 0 {
		t.Fatalf(
			"pre-cancelled resume traceback = %+v; want none",
			failure.Traceback(),
		)
	}
	assertTestValues(t, destination, Number(70), Number(71))
	if calls != 0 ||
		thread.Status() != ThreadSuspended ||
		thread.runtimeObject().top != 1 ||
		len(thread.runtimeObject().frames) != 0 {
		t.Fatal("pre-cancelled resume changed the suspended coroutine")
	}

	live := context.WithValue(
		context.Background(),
		contextTestKey{},
		"resume",
	)
	count, status, err = thread.ResumeIntoContext(
		live,
		nil,
		destination,
	)
	if err != nil || count != 1 || status != ThreadDead {
		t.Fatalf(
			"fresh resume = (count=%d, status=%v, err=%v)",
			count,
			status,
			err,
		)
	}
	assertTestValues(t, destination, Number(41), Number(71))
	if calls != 1 {
		t.Fatalf("fresh resume calls = %d; want 1", calls)
	}
	assertDeadCoroutineClean(t, thread)
	assertContextStateIdle(t, state)
}

func TestContextCancellationPropagatesAcrossCoroutines(t *testing.T) {
	t.Run("coroutine.resume", func(t *testing.T) {
		state := newStateWithBase(t, Options{})
		defer state.Close()
		if err := state.OpenCoroutine(); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancelNow, err := state.NewNativeFunction(func(frame Frame) Outcome {
			if frame.Context() != ctx {
				return frame.RaiseString("child lost parent context")
			}
			cancel()
			return frame.Return()
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := state.SetGlobal(
			"context_child_cancel",
			cancelNow.Value(),
		); err != nil {
			t.Fatal(err)
		}
		childEntry := mustLoadString(
			t,
			state,
			"@context-cancel-child.lua",
			`context_child_cancel()
child_after_context = true`,
		)
		child, err := state.NewThread(childEntry.Value())
		if err != nil {
			t.Fatal(err)
		}
		if err := state.SetGlobal("context_cancel_child", child.Value()); err != nil {
			t.Fatal(err)
		}
		parent := mustLoadString(
			t,
			state,
			"@context-cancel-parent.lua",
			`local ok = coroutine.resume(context_cancel_child)
parent_after_context = true`,
		)

		_, callErr := state.CallContext(ctx, parent.Value())
		failure := assertContextFailure(
			t,
			callErr,
			context.Canceled,
			context.Canceled,
			"context-cancel-child.lua:1: context canceled",
		)
		assertExactContextTrace(
			t,
			failure,
			TraceFrame{
				Source:   "=[Go]",
				Function: "native function",
			},
			TraceFrame{Source: "@context-cancel-child.lua", Line: 1},
			TraceFrame{
				Source:   "=[Go]",
				Function: "native function",
			},
			TraceFrame{Source: "@context-cancel-parent.lua", Line: 1},
		)
		if child.Status() != ThreadDead {
			t.Fatalf("cancelled child status = %v; want dead", child.Status())
		}
		assertDeadCoroutineClean(t, child)
		childAfter, err := state.Global("child_after_context")
		if err != nil {
			t.Fatal(err)
		}
		parentAfter, err := state.Global("parent_after_context")
		if err != nil {
			t.Fatal(err)
		}
		assertTestValue(t, childAfter, Nil())
		assertTestValue(t, parentAfter, Nil())
		assertContextStateIdle(t, state)
	})

	t.Run("coroutine.wrap", func(t *testing.T) {
		state := newStateWithBase(t, Options{})
		defer state.Close()
		if err := state.OpenCoroutine(); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancelNow, err := state.NewNativeFunction(func(frame Frame) Outcome {
			cancel()
			return frame.Return()
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := state.SetGlobal("context_wrap_cancel", cancelNow.Value()); err != nil {
			t.Fatal(err)
		}
		chunk := mustLoadString(t, state, "@context-wrap.lua", `local wrapped = coroutine.wrap(function()
	context_wrap_cancel()
end)
wrapped()
after_wrapped_context = true`)
		_, callErr := state.CallContext(ctx, chunk.Value())
		assertContextFailure(
			t,
			callErr,
			context.Canceled,
			context.Canceled,
			"context-wrap.lua:2: context canceled",
		)
		after, err := state.Global("after_wrapped_context")
		if err != nil {
			t.Fatal(err)
		}
		assertTestValue(t, after, Nil())
		assertContextStateIdle(t, state)
	})

	t.Run("yield publication", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		ctx, cancel := context.WithCancel(context.Background())
		entry, err := state.NewNativeFunction(func(frame Frame) Outcome {
			cancel()
			return frame.YieldValues(Number(1), Number(2))
		})
		if err != nil {
			t.Fatal(err)
		}
		thread, err := state.NewThread(entry.Value())
		if err != nil {
			t.Fatal(err)
		}
		destination := []Value{Number(70), Number(71)}
		count, status, resumeErr := thread.ResumeIntoContext(
			ctx,
			nil,
			destination,
		)
		if count != 0 || status != ThreadDead {
			t.Fatalf(
				"cancelled yield = (count=%d, status=%v)",
				count,
				status,
			)
		}
		failure := assertContextFailure(
			t,
			resumeErr,
			context.Canceled,
			context.Canceled,
			"context canceled",
		)
		assertExactContextTrace(
			t,
			failure,
			TraceFrame{
				Source:   "=[Go]",
				Function: "native function",
			},
		)
		assertTestValues(t, destination, Number(70), Number(71))
		assertDeadCoroutineClean(t, thread)
		assertContextStateIdle(t, state)
	})
}

func TestContextCleanupPreservesPanicsAndResourceLimits(t *testing.T) {
	t.Run("panic", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()

		ctx, cancel := context.WithCancel(context.Background())
		marker := &struct{ name string }{name: "context panic"}
		panicking, err := state.NewNativeFunction(func(frame Frame) Outcome {
			cancel()
			panic(marker)
		})
		if err != nil {
			t.Fatal(err)
		}
		var recovered any
		func() {
			defer func() {
				recovered = recover()
			}()
			_, _ = state.CallContext(ctx, panicking.Value())
		}()
		if recovered != marker {
			t.Fatalf("context panic = %#v; want %#v", recovered, marker)
		}
		assertContextStateIdle(t, state)
		recovery := mustLoadString(t, state, "@context-panic-recovery.lua", `return 42`)
		results, err := state.Call(recovery.Value())
		if err != nil {
			t.Fatal(err)
		}
		assertTestValues(t, results, Number(42))
		runtime.KeepAlive(marker)
	})

	t.Run("invalid outcome", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()

		ctx, cancel := context.WithCancel(context.Background())
		invalid, err := state.NewNativeFunction(func(Frame) Outcome {
			cancel()
			return Outcome{}
		})
		if err != nil {
			t.Fatal(err)
		}
		_, callErr := state.CallContext(ctx, invalid.Value())
		var failure *Error
		if !errors.As(callErr, &failure) ||
			failure.Category() != RuntimeError ||
			!strings.Contains(failure.Error(), "invalid outcome") {
			t.Fatalf(
				"cancelled invalid outcome = %#v; want runtime error",
				callErr,
			)
		}
		if errors.Is(callErr, context.Canceled) {
			t.Fatal("cancellation masked an invalid native outcome")
		}
		assertContextStateIdle(t, state)
	})

	t.Run("xpcall emergency limits", func(t *testing.T) {
		const frameLimit = 3
		state := newStateWithBase(t, Options{MaxFrames: frameLimit})
		defer state.Close()
		ctx, cancel := context.WithCancel(context.Background())
		handler, err := state.NewNativeFunction(func(frame Frame) Outcome {
			if frame.Context() != ctx {
				return frame.RaiseString("error handler lost context")
			}
			cancel()
			return frame.ReturnString("ignored")
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := state.SetGlobal("context_limit_handler", handler.Value()); err != nil {
			t.Fatal(err)
		}
		chunk := mustLoadString(t, state, "@context-limit.lua", `local function recurse()
	local value = recurse()
	return value
end
local ok = xpcall(recurse, context_limit_handler)
after_context_limit = true`)
		_, callErr := state.CallContext(ctx, chunk.Value())
		assertContextFailure(
			t,
			callErr,
			context.Canceled,
			context.Canceled,
			"context-limit.lua:2: context canceled",
		)
		after, err := state.Global("after_context_limit")
		if err != nil {
			t.Fatal(err)
		}
		assertTestValue(t, after, Nil())
		if cap(state.main.frames) > frameLimit {
			t.Fatalf(
				"context handler frame capacity = %d; limit %d",
				cap(state.main.frames),
				frameLimit,
			)
		}
		assertContextStateIdle(t, state)
	})
}

type contextLifetimeMarker struct {
	_ *byte
}

func TestExecutionContextDoesNotRetainContextGraphs(t *testing.T) {
	t.Run("completed call", func(t *testing.T) {
		state, collected := completedContextLifetimeFixture(t)
		defer state.Close()
		waitForContextCollection(t, collected, state)
	})

	t.Run("suspended coroutine", func(t *testing.T) {
		state, thread, collected := suspendedContextLifetimeFixture(t)
		defer state.Close()
		waitForContextCollection(t, collected, thread)
		if thread.Status() != ThreadSuspended {
			t.Fatalf(
				"context lifetime coroutine status = %v; want suspended",
				thread.Status(),
			)
		}
	})

	t.Run("returned error", func(t *testing.T) {
		state, failure, collected := failedContextLifetimeFixture(t)
		defer state.Close()
		waitForContextCollection(t, collected, failure)
		if failure.Category() != ContextError ||
			!errors.Is(failure, context.Canceled) {
			t.Fatalf("retained context failure = %#v", failure)
		}
	})
}

func completedContextLifetimeFixture(
	t *testing.T,
) (*State, <-chan struct{}) {
	t.Helper()
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	collected := make(chan struct{}, 1)
	marker := &contextLifetimeMarker{}
	runtime.SetFinalizer(marker, func(*contextLifetimeMarker) {
		collected <- struct{}{}
	})
	ctx := context.WithValue(
		context.Background(),
		contextTestKey{},
		marker,
	)
	probe, err := state.NewNativeFunction(func(frame Frame) Outcome {
		if frame.Context().Value(contextTestKey{}) == nil {
			return frame.RaiseString("context marker missing")
		}
		return frame.Return()
	})
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	if _, err := state.CallContext(ctx, probe.Value()); err != nil {
		state.Close()
		t.Fatal(err)
	}
	assertContextStateIdle(t, state)
	return state, collected
}

func suspendedContextLifetimeFixture(
	t *testing.T,
) (*State, *Thread, <-chan struct{}) {
	t.Helper()
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	collected := make(chan struct{}, 1)
	marker := &contextLifetimeMarker{}
	runtime.SetFinalizer(marker, func(*contextLifetimeMarker) {
		collected <- struct{}{}
	})
	ctx := context.WithValue(
		context.Background(),
		contextTestKey{},
		marker,
	)
	entry, err := state.NewNativeFunction(func(frame Frame) Outcome {
		if frame.Context().Value(contextTestKey{}) == nil {
			return frame.RaiseString("context marker missing")
		}
		return frame.Yield()
	})
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	thread, err := state.NewThread(entry.Value())
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	if _, status, err := thread.ResumeContext(ctx); err != nil ||
		status != ThreadSuspended {
		state.Close()
		t.Fatalf(
			"context lifetime yield = (status=%v, err=%v)",
			status,
			err,
		)
	}
	if state.execution != (executionControl{}) ||
		thread.runtimeObject().contextBudget != 0 {
		state.Close()
		t.Fatal("suspended coroutine retained active context machinery")
	}
	return state, thread, collected
}

func failedContextLifetimeFixture(
	t *testing.T,
) (*State, *Error, <-chan struct{}) {
	t.Helper()
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	collected := make(chan struct{}, 1)
	marker := &contextLifetimeMarker{}
	runtime.SetFinalizer(marker, func(*contextLifetimeMarker) {
		collected <- struct{}{}
	})
	parent := context.WithValue(
		context.Background(),
		contextTestKey{},
		marker,
	)
	ctx, cancel := context.WithCancel(parent)
	cancel()
	target, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.Return()
	})
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	_, callErr := state.CallContext(ctx, target.Value())
	var failure *Error
	if !errors.As(callErr, &failure) {
		state.Close()
		t.Fatalf("context lifetime error = %#v; want *Error", callErr)
	}
	return state, failure, collected
}

func waitForContextCollection(
	t *testing.T,
	collected <-chan struct{},
	retained any,
) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		runtime.GC()
		select {
		case <-collected:
			runtime.KeepAlive(retained)
			return
		case <-deadline.C:
			t.Fatal("execution context retained a completed context graph")
		case <-ticker.C:
		}
	}
}

func TestContextRuntimeRecordsStayCompact(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("64-bit runtime size contract")
	}
	if size := unsafe.Sizeof(activation{}); size != 32 {
		t.Fatalf("activation size = %d; want 32", size)
	}
	if size := unsafe.Sizeof(Frame{}); size != 24 {
		t.Fatalf("Frame size = %d; want 24", size)
	}
}

func TestWarmContextBoundariesDoNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)

	t.Run("CallIntoContext", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		target := mustLoadString(t, state, "@context-warm-call.lua", `return ...`)
		arguments := []Value{Number(41)}
		destination := make([]Value, 1)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		for range 8 {
			count, callErr := state.CallIntoContext(
				ctx,
				target.Value(),
				arguments,
				destination,
			)
			if callErr != nil || count != 1 {
				t.Fatalf("context call warmup = (%d, %v)", count, callErr)
			}
		}
		allocations := testing.AllocsPerRun(1_000, func() {
			count, callErr := state.CallIntoContext(
				ctx,
				target.Value(),
				arguments,
				destination,
			)
			if callErr != nil || count != 1 {
				panic("warm context call failed")
			}
		})
		if allocations != 0 {
			t.Fatalf(
				"warm CallIntoContext allocations = %v; want 0",
				allocations,
			)
		}
		assertTestValues(t, destination, Number(41))
	})

	t.Run("nested Frame.CallInto", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		target := mustLoadString(t, state, "@context-warm-nested.lua", `return ...`)
		nestedDestination := make([]Value, 1)
		arguments := []Value{Number(41)}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		host, err := state.NewNativeFunction(func(frame Frame) Outcome {
			if frame.Context() != ctx {
				return frame.RaiseString("warm nested context mismatch")
			}
			count, callErr := frame.CallInto(
				target.Value(),
				arguments,
				nestedDestination,
			)
			if callErr != nil || count != 1 {
				return frame.RaiseString("warm nested call failed")
			}
			return frame.ReturnValue(nestedDestination[0])
		})
		if err != nil {
			t.Fatal(err)
		}
		destination := make([]Value, 1)
		for range 8 {
			if count, callErr := state.CallIntoContext(
				ctx,
				host.Value(),
				nil,
				destination,
			); callErr != nil || count != 1 {
				t.Fatalf("nested context warmup = (%d, %v)", count, callErr)
			}
		}
		allocations := testing.AllocsPerRun(1_000, func() {
			if count, callErr := state.CallIntoContext(
				ctx,
				host.Value(),
				nil,
				destination,
			); callErr != nil || count != 1 {
				panic("warm nested context call failed")
			}
		})
		if allocations != 0 {
			t.Fatalf(
				"warm nested context allocations = %v; want 0",
				allocations,
			)
		}
		assertTestValues(t, destination, Number(41))
	})

	t.Run("ResumeIntoContext", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		installYieldTestFunction(t, state)
		entry := mustLoadString(t, state, "@context-warm-resume.lua", `while true do
	native_yield(1)
end`)
		thread, err := state.NewThread(entry.Value())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		destination := make([]Value, 1)
		for range 8 {
			count, status, resumeErr := thread.ResumeIntoContext(
				ctx,
				nil,
				destination,
			)
			if resumeErr != nil ||
				count != 1 ||
				status != ThreadSuspended {
				t.Fatalf(
					"context resume warmup = (%d, %v, %v)",
					count,
					status,
					resumeErr,
				)
			}
		}
		allocations := testing.AllocsPerRun(1_000, func() {
			count, status, resumeErr := thread.ResumeIntoContext(
				ctx,
				nil,
				destination,
			)
			if resumeErr != nil ||
				count != 1 ||
				status != ThreadSuspended {
				panic("warm context resume failed")
			}
		})
		if allocations != 0 {
			t.Fatalf(
				"warm ResumeIntoContext allocations = %v; want 0",
				allocations,
			)
		}
		assertTestValues(t, destination, Number(1))
		runtime.KeepAlive(thread)
	})
}

func BenchmarkContextCallBoundary(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	target := mustLoadString(b, state, "@benchmark-context-call.lua", `return ...`)
	arguments := []Value{Number(41)}
	destination := make([]Value, 1)
	background := context.Background()
	active, cancel := context.WithCancel(context.Background())
	b.Cleanup(cancel)

	b.Run("raw", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, callErr := state.CallInto(
				target.Value(),
				arguments,
				destination,
			); callErr != nil {
				b.Fatal(callErr)
			}
		}
	})
	b.Run("background", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, callErr := state.CallIntoContext(
				background,
				target.Value(),
				arguments,
				destination,
			); callErr != nil {
				b.Fatal(callErr)
			}
		}
	})
	b.Run("cancelable", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, callErr := state.CallIntoContext(
				active,
				target.Value(),
				arguments,
				destination,
			); callErr != nil {
				b.Fatal(callErr)
			}
		}
	})
}

func BenchmarkContextDispatch256Moves(b *testing.B) {
	code := make([]instruction, 0, 257)
	for range 256 {
		code = append(code, makeABC(opMove, 0, 0, 0))
	}
	code = append(code, makeABC(opReturn, 0, 2, 0))
	builder := testPrototypeBuilder(code...)
	builder.registers = 1
	prototype, syntaxError := builder.seal()
	if syntaxError != nil {
		b.Fatal(syntaxError)
	}
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	target := newLuaFunction(state.runtime, prototype, state.main.globals, nil)
	targetValue := target.owningValue()
	destination := make([]Value, 1)
	active, cancel := context.WithCancel(context.Background())
	b.Cleanup(cancel)

	for _, test := range []struct {
		name string
		ctx  context.Context
	}{
		{name: "raw"},
		{name: "background", ctx: context.Background()},
		{name: "cancelable", ctx: active},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ReportMetric(257, "opcodes/op")
			for range b.N {
				var callErr error
				if test.ctx == nil {
					_, callErr = state.CallInto(
						targetValue,
						nil,
						destination,
					)
				} else {
					_, callErr = state.CallIntoContext(
						test.ctx,
						targetValue,
						nil,
						destination,
					)
				}
				if callErr != nil {
					b.Fatal(callErr)
				}
			}
		})
	}
}

func BenchmarkContextLoopPolling(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	target := mustLoadString(
		b,
		state,
		"@benchmark-context-loop.lua",
		`local sum = 0
for index = 1, 100 do
	sum = sum + index
end
return sum`,
	)
	destination := make([]Value, 1)
	active, cancel := context.WithCancel(context.Background())
	b.Cleanup(cancel)

	for _, test := range []struct {
		name string
		ctx  context.Context
	}{
		{name: "raw"},
		{name: "background", ctx: context.Background()},
		{name: "cancelable", ctx: active},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ReportMetric(100, "backedges/op")
			for range b.N {
				var callErr error
				if test.ctx == nil {
					_, callErr = state.CallInto(
						target.Value(),
						nil,
						destination,
					)
				} else {
					_, callErr = state.CallIntoContext(
						test.ctx,
						target.Value(),
						nil,
						destination,
					)
				}
				if callErr != nil {
					b.Fatal(callErr)
				}
			}
		})
	}
}

func BenchmarkContextCoroutineResumeYield(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	installYieldTestFunction(b, state)
	entry := mustLoadString(b, state, "@benchmark-context-resume.lua", `while true do
	native_yield(1)
end`)
	thread, err := state.NewThread(entry.Value())
	if err != nil {
		b.Fatal(err)
	}
	destination := make([]Value, 1)
	ctx, cancel := context.WithCancel(context.Background())
	b.Cleanup(cancel)
	if _, _, err := thread.ResumeIntoContext(ctx, nil, destination); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, err := thread.ResumeIntoContext(
			ctx,
			nil,
			destination,
		); err != nil {
			b.Fatal(err)
		}
	}
}

func assertContextFailure(
	t testing.TB,
	err error,
	status error,
	explanation error,
	description string,
) *Error {
	t.Helper()
	var failure *Error
	if !errors.As(err, &failure) {
		t.Fatalf("context failure = %#v; want *Error", err)
	}
	if failure.Category() != ContextError {
		t.Fatalf(
			"context failure category = %v; want ContextError",
			failure.Category(),
		)
	}
	if !errors.Is(failure, status) {
		t.Fatalf("context failure does not match status %v: %#v", status, failure)
	}
	if !errors.Is(failure, explanation) {
		t.Fatalf(
			"context failure does not match explanation %v: %#v",
			explanation,
			failure,
		)
	}
	if failure.Error() != description {
		t.Fatalf(
			"context description = %q; want %q",
			failure.Error(),
			description,
		)
	}
	value, ok := failure.Value().AsString()
	if !ok || value != description {
		t.Fatalf("context error Value = %q, %v; want %q", value, ok, description)
	}
	return failure
}

func assertExactContextTrace(
	t testing.TB,
	failure *Error,
	expected ...TraceFrame,
) {
	t.Helper()
	assertContextTraceFrames(t, failure.Traceback(), expected...)
}

func assertContextTraceFrames(
	t testing.TB,
	got []TraceFrame,
	expected ...TraceFrame,
) {
	t.Helper()
	if len(got) != len(expected) {
		t.Fatalf(
			"context traceback = %+v; want %+v",
			got,
			expected,
		)
	}
	for index := range expected {
		if got[index] != expected[index] {
			t.Fatalf(
				"context traceback frame %d = %+v; want %+v",
				index,
				got[index],
				expected[index],
			)
		}
	}
}

func contextTraceTopLuaLine(trace []TraceFrame) string {
	for _, frame := range trace {
		if frame.Source != "=[Go]" {
			return strconv.Itoa(frame.Line)
		}
	}
	return "0"
}

func assertContextStateIdle(t *testing.T, state *State) {
	t.Helper()
	assertStateExecutionIdle(t, state)
	if state.execution != (executionControl{}) {
		t.Fatalf("state retained execution context: %+v", state.execution)
	}
	if state.main.contextBudget != 0 {
		t.Fatalf(
			"main thread retained context budget %d",
			state.main.contextBudget,
		)
	}
}
