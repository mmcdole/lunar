package lua

import (
	"errors"
	"strings"
	"testing"
)

// checkString is the helper shape Throw exists to enable: an argument
// check that fails from below the NativeFunc, where returning an Outcome
// is not possible.
func checkString(frame Frame, index int) string {
	text, ok := frame.String(index)
	if !ok {
		frame.ThrowArgTypeError(index, StringKind)
	}
	return text
}

func TestFrameThrowFromHelperRaisesCatchableError(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	reached := false
	greet, err := state.NewNativeFunction(func(frame Frame) Outcome {
		name := checkString(frame, 0)
		reached = true
		return frame.ReturnString("hello " + name)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("greet", greet.Value()); err != nil {
		t.Fatal(err)
	}

	results, err := state.DoString("@greet.lua", `return greet("world")`)
	if err != nil {
		t.Fatalf("successful call failed: %v", err)
	}
	if text, _ := results[0].AsString(); text != "hello world" {
		t.Fatalf("greet(\"world\") = %q", text)
	}
	if !reached {
		t.Fatal("successful call did not run past the helper")
	}

	reached = false
	results, err = state.DoString("@greet.lua", `return pcall(greet, {})`)
	if err != nil {
		t.Fatalf("pcall over a throwing helper failed: %v", err)
	}
	if ok, _ := results[0].AsBool(); ok {
		t.Fatal("pcall reported success for a thrown argument error")
	}
	message, _ := results[1].AsString()
	const want = "bad argument #1 (string expected, got table)"
	if !strings.Contains(message, want) {
		t.Fatalf("thrown message = %q; want it to contain %q", message, want)
	}
	if reached {
		t.Fatal("Throw returned to the callback instead of unwinding")
	}
}

// Throw must complete a callback exactly as returning the matching Raise
// would, so the two paths are compared on the same failure.
func TestFrameThrowMatchesReturnedRaise(t *testing.T) {
	tests := []struct {
		name    string
		thrown  func(Frame)
		raised  func(Frame) Outcome
		wantErr error
	}{
		{
			name:   "string",
			thrown: func(frame Frame) { frame.ThrowString("broken") },
			raised: func(frame Frame) Outcome {
				return frame.RaiseString("broken")
			},
		},
		{
			name: "value",
			thrown: func(frame Frame) {
				frame.Throw(frame.State().String("as value"))
			},
			raised: func(frame Frame) Outcome {
				return frame.Raise(frame.State().String("as value"))
			},
		},
		{
			name:    "error",
			thrown:  func(frame Frame) { frame.ThrowError(errTestHostFailure) },
			raised:  func(frame Frame) Outcome { return frame.RaiseError(errTestHostFailure) },
			wantErr: errTestHostFailure,
		},
		{
			name:   "argument",
			thrown: func(frame Frame) { frame.ThrowArgError(1, "must be positive") },
			raised: func(frame Frame) Outcome {
				return frame.ArgError(1, "must be positive")
			},
		},
		{
			name:   "argument type",
			thrown: func(frame Frame) { frame.ThrowArgTypeError(0, NumberKind) },
			raised: func(frame Frame) Outcome {
				return frame.ArgTypeError(0, NumberKind)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			thrownFailure := runFailingCallback(t, func(frame Frame) Outcome {
				test.thrown(frame)
				panic("Throw returned")
			})
			raisedFailure := runFailingCallback(t, test.raised)

			if thrownFailure.Error() != raisedFailure.Error() {
				t.Fatalf(
					"thrown message = %q; raised message = %q",
					thrownFailure.Error(),
					raisedFailure.Error(),
				)
			}
			if thrownFailure.Category() != raisedFailure.Category() {
				t.Fatalf(
					"thrown category = %v; raised category = %v",
					thrownFailure.Category(),
					raisedFailure.Category(),
				)
			}
			if test.wantErr != nil && !errors.Is(thrownFailure, test.wantErr) {
				t.Fatalf("thrown failure lost its Go cause: %v", thrownFailure)
			}
		})
	}
}

var errTestHostFailure = errors.New("host failure")

func runFailingCallback(t *testing.T, entry NativeFunc) *Error {
	t.Helper()
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	function, err := state.NewNativeFunction(entry)
	if err != nil {
		t.Fatal(err)
	}
	_, err = state.Call(function.Value(), Nil(), Nil())
	if err == nil {
		t.Fatal("callback reported success; want a failure")
	}
	var failure *Error
	if !errors.As(err, &failure) {
		t.Fatalf("call error is not a *Error: %v", err)
	}
	return failure
}

// A throw belongs to the frame that produced it. When a nested callback
// throws, the nested boundary must absorb it and report the failure to the
// outer callback as an ordinary Frame.Call error.
func TestFrameThrowUnwindsToItsOwnBoundary(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	inner, err := state.NewNativeFunction(func(frame Frame) Outcome {
		frame.ThrowString("inner failed")
		panic("Throw returned")
	})
	if err != nil {
		t.Fatal(err)
	}

	outerResumed := false
	outer, err := state.NewNativeFunction(func(frame Frame) Outcome {
		_, callErr := frame.Call(inner.Value())
		if callErr == nil {
			t.Error("nested throw did not surface as a call error")
		}
		outerResumed = true
		var failure *Error
		if !errors.As(callErr, &failure) {
			return frame.RaiseString("nested error was not a *Error")
		}
		if !strings.Contains(failure.Error(), "inner failed") {
			t.Errorf("nested failure = %q", failure.Error())
		}
		return frame.ReturnString("recovered")
	})
	if err != nil {
		t.Fatal(err)
	}

	results, err := state.Call(outer.Value())
	if err != nil {
		t.Fatalf("outer callback failed: %v", err)
	}
	if !outerResumed {
		t.Fatal("inner throw unwound past the nested boundary")
	}
	if text, _ := results[0].AsString(); text != "recovered" {
		t.Fatalf("outer result = %q", text)
	}
}

// A Throw crossing a Lua frame must still reach its own boundary, and the
// resulting error must carry the Lua position of the call site.
func TestFrameThrowThroughLuaFrameCarriesPosition(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	failing, err := state.NewNativeFunction(func(frame Frame) Outcome {
		frame.ThrowString("deep failure")
		panic("Throw returned")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("failing", failing.Value()); err != nil {
		t.Fatal(err)
	}

	_, err = state.DoString("@caller.lua", "local function step() failing() end\nstep()")
	if err == nil {
		t.Fatal("expected the thrown error to reach the host")
	}
	var failure *Error
	if !errors.As(err, &failure) {
		t.Fatalf("error is not a *Error: %v", err)
	}
	if !strings.Contains(failure.Error(), "deep failure") {
		t.Fatalf("failure = %q", failure.Error())
	}
	if len(failure.Traceback()) == 0 {
		t.Fatal("thrown failure carries no traceback")
	}
}

// Panics that are not throws keep Lunar's documented behavior: they
// propagate to the host rather than becoming Lua errors.
func TestUnrelatedPanicStillReachesTheHost(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	function, err := state.NewNativeFunction(func(frame Frame) Outcome {
		panic(errTestHostFailure)
	})
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("host panic was swallowed by the native boundary")
		}
		if recovered != error(errTestHostFailure) {
			t.Fatalf("recovered %v; want the original panic value", recovered)
		}
	}()
	_, _ = state.Call(function.Value())
}

// Throwing from a coroutine must unwind that thread's native boundary and
// surface through Resume, not the State that created the thread.
func TestFrameThrowInsideCoroutine(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	failing, err := state.NewNativeFunction(func(frame Frame) Outcome {
		frame.ThrowString("coroutine failure")
		panic("Throw returned")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("failing", failing.Value()); err != nil {
		t.Fatal(err)
	}

	body, err := state.LoadString("@body.lua", "failing()")
	if err != nil {
		t.Fatal(err)
	}
	thread, err := state.NewThread(body.Value())
	if err != nil {
		t.Fatal(err)
	}
	_, status, err := thread.Resume()
	if err == nil {
		t.Fatal("expected the coroutine to fail")
	}
	if !strings.Contains(err.Error(), "coroutine failure") {
		t.Fatalf("resume error = %v", err)
	}
	if status != ThreadDead {
		t.Fatalf("status after a thrown failure = %v; want ThreadDead", status)
	}
}
