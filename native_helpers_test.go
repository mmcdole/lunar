package lua

import (
	"errors"
	"math"
	"testing"
)

func TestNativeFrameCoercingAndIntegerArguments(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	function, err := state.NewNativeFunction(func(frame Frame) Outcome {
		if value, ok := frame.CoerceNumber(0); !ok || value != 42 {
			t.Fatalf("CoerceNumber(number) = (%v, %v)", value, ok)
		}
		if value, ok := frame.CoerceNumber(1); !ok || value != 42 {
			t.Fatalf("CoerceNumber(numeric string) = (%v, %v)", value, ok)
		}
		if _, ok := frame.CoerceNumber(3); ok {
			t.Fatal("CoerceNumber accepted a nonnumeric string")
		}
		if value, ok := frame.CoerceString(2); !ok || value != "12.5" {
			t.Fatalf("CoerceString(number) = (%q, %v)", value, ok)
		}
		if value, ok := frame.CoerceString(3); !ok || value != "text" {
			t.Fatalf("CoerceString(string) = (%q, %v)", value, ok)
		}
		if _, ok := frame.CoerceString(10); ok {
			t.Fatal("CoerceString accepted a boolean")
		}

		if value, ok := frame.Integer(0); !ok || value != 42 {
			t.Fatalf("Integer(integral) = (%d, %v)", value, ok)
		}
		for _, index := range []int{1, 2, 4, 5, 6, 7, 12, 13} {
			if value, ok := frame.Integer(index); ok {
				t.Fatalf("Integer(%d) = (%d, true); want rejection", index, value)
			}
		}
		if value, ok := frame.Integer(8); !ok || value != math.MinInt64 {
			t.Fatalf("Integer(min int64) = (%d, %v)", value, ok)
		}
		const largestRepresentableInt64 = int64(9223372036854774784)
		if value, ok := frame.Integer(11); !ok ||
			value != largestRepresentableInt64 {
			t.Fatalf("Integer(largest float below 2^63) = (%d, %v)", value, ok)
		}

		if value, ok := frame.IntegerInRange(0, 42, 42); !ok || value != 42 {
			t.Fatalf("IntegerInRange(inclusive) = (%d, %v)", value, ok)
		}
		if _, ok := frame.IntegerInRange(0, 43, 50); ok {
			t.Fatal("IntegerInRange accepted a value below the range")
		}
		if _, ok := frame.IntegerInRange(0, 50, 43); ok {
			t.Fatal("IntegerInRange accepted an inverted range")
		}

		if !frame.IsMissingOrNil(9) {
			t.Fatal("IsMissingOrNil rejected explicit nil")
		}
		if !frame.IsMissingOrNil(13) {
			t.Fatal("IsMissingOrNil rejected a missing argument")
		}
		if frame.IsMissingOrNil(10) {
			t.Fatal("IsMissingOrNil treated false as absent")
		}
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = state.Call(
		function.Value(),
		Number(42),
		state.String(" 0x2a "),
		Number(12.5),
		state.String("text"),
		Number(math.Inf(1)),
		Number(math.Inf(-1)),
		Number(math.NaN()),
		Number(0x1p63),
		Number(-0x1p63),
		Nil(),
		Bool(false),
		Number(math.Nextafter(0x1p63, 0)),
		state.String("17"),
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeFrameArgTypeErrorAcceptsSeveralKinds(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	function, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ArgTypeError(
			0,
			StringKind,
			NumberKind,
			NilKind,
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = state.Call(function.Value(), Bool(true))
	var failure *Error
	if !errors.As(err, &failure) {
		t.Fatalf("ArgTypeError result = %#v; want *Error", err)
	}
	const want = "bad argument #1 (string, number, or nil expected, got boolean)"
	if failure.Error() != want {
		t.Fatalf("ArgTypeError = %q; want %q", failure.Error(), want)
	}
}

func TestNativeFrameArgTypeErrorRejectsInvalidExpectedKinds(t *testing.T) {
	for _, test := range []struct {
		name     string
		expected []Kind
	}{
		{name: "none"},
		{name: "invalid", expected: []Kind{InvalidKind}},
		{name: "out of range", expected: []Kind{TableKind + 1}},
		{name: "duplicate", expected: []Kind{NumberKind, NumberKind}},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			function, err := state.NewNativeFunction(func(frame Frame) Outcome {
				return frame.ArgTypeError(0, test.expected...)
			})
			if err != nil {
				t.Fatal(err)
			}
			thread := stageNativeTestCall(
				t,
				state,
				function,
				allResults,
			)
			assertNativePanic(t, func() {
				_ = invokeNativeCall(thread)
			})
			thread.unwindCalls(0)
		})
	}
}

func TestNativeFrameReturnArgumentsAdjustsResults(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	function, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnArguments()
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments := []Value{Number(1), Nil(), state.String("three")}
	for _, test := range []struct {
		name     string
		wanted   int
		expected []Value
	}{
		{
			name:     "all",
			wanted:   allResults,
			expected: arguments,
		},
		{
			name:     "truncate",
			wanted:   2,
			expected: arguments[:2],
		},
		{
			name:   "pad",
			wanted: 5,
			expected: []Value{
				Number(1),
				Nil(),
				state.String("three"),
				Nil(),
				Nil(),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			thread := stageNativeTestCall(
				t,
				state,
				function,
				test.wanted,
				arguments...,
			)
			if failure := invokeNativeCall(thread); failure != nil {
				t.Fatal(failure)
			}
			assertNativeTestResults(t, thread, test.expected...)
		})
	}
}

func TestNativeFrameRaiseErrorPreservesOrdinaryGoCause(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	cause := errors.New("host lookup failed")
	function, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.RaiseError(cause)
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = state.Call(function.Value())
	var failure *Error
	if !errors.As(err, &failure) {
		t.Fatalf("RaiseError result = %#v; want *Error", err)
	}
	if failure.Category() != RuntimeError ||
		failure.Error() != cause.Error() ||
		!errors.Is(failure, cause) {
		t.Fatalf("RaiseError result = %#v", failure)
	}
	value, ok := failure.Value().AsString()
	if !ok || value != cause.Error() {
		t.Fatalf("RaiseError Lua value = (%q, %v)", value, ok)
	}
}
