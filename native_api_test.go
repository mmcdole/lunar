package lua_test

import (
	"errors"
	"math"
	"testing"

	"github.com/mmcdole/lunar"
)

func TestNativeFrameCoercingAndIntegerArguments(t *testing.T) {
	state, err := lua.New(lua.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	function, err := state.NewNativeFunction(func(frame lua.Frame) lua.Outcome {
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
		lua.Number(42),
		state.String(" 0x2a "),
		lua.Number(12.5),
		state.String("text"),
		lua.Number(math.Inf(1)),
		lua.Number(math.Inf(-1)),
		lua.Number(math.NaN()),
		lua.Number(0x1p63),
		lua.Number(-0x1p63),
		lua.Nil(),
		lua.Bool(false),
		lua.Number(math.Nextafter(0x1p63, 0)),
		state.String("17"),
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeFrameArgTypeErrorAcceptsSeveralKinds(t *testing.T) {
	state, err := lua.New(lua.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	function, err := state.NewNativeFunction(func(frame lua.Frame) lua.Outcome {
		frame.ThrowArgTypeError(
			0,
			lua.StringKind,
			lua.NumberKind,
			lua.NilKind,
		)
		// Unreachable: the throw above does not return.
		return lua.Outcome{}
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = state.Call(function.Value(), lua.Bool(true))
	var failure *lua.Error
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
		expected []lua.Kind
	}{
		{name: "none"},
		{name: "invalid", expected: []lua.Kind{lua.InvalidKind}},
		{name: "out of range", expected: []lua.Kind{lua.TableKind + 1}},
		{name: "duplicate", expected: []lua.Kind{lua.NumberKind, lua.NumberKind}},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, err := lua.New(lua.Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			function, err := state.NewNativeFunction(func(frame lua.Frame) lua.Outcome {
				frame.ThrowArgTypeError(0, test.expected...)
				// Unreachable: the throw above does not return.
				return lua.Outcome{}
			})
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer func() {
					if recover() == nil {
						t.Error("invalid expected kinds did not panic")
					}
				}()
				_, _ = state.Call(function.Value())
			}()

		})
	}
}

func TestNativeFrameReturnArgumentsAdjustsResults(t *testing.T) {
	state, err := lua.New(lua.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	function, err := state.NewNativeFunction(func(frame lua.Frame) lua.Outcome {
		return frame.ReturnArguments()
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments := []lua.Value{lua.Number(1), lua.Nil(), state.String("three")}
	for _, test := range []struct {
		name     string
		wanted   int
		expected []lua.Value
	}{
		{
			name:     "all",
			wanted:   -1,
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
			expected: []lua.Value{
				lua.Number(1),
				lua.Nil(),
				state.String("three"),
				lua.Nil(),
				lua.Nil(),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var results []lua.Value
			var err error
			if test.wanted < 0 {
				results, err = state.Call(function.Value(), arguments...)
			} else {
				results, err = state.CallN(function.Value(), test.wanted, arguments...)
			}
			if err != nil {
				t.Fatal(err)
			}
			assertPublicValues(t, state, results, test.expected...)

		})
	}
}

func TestNativeFrameThrowErrorPreservesOrdinaryGoCause(t *testing.T) {
	state, err := lua.New(lua.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	cause := errors.New("host lookup failed")
	function, err := state.NewNativeFunction(func(frame lua.Frame) lua.Outcome {
		frame.ThrowError(cause)
		// Unreachable: the throw above does not return.
		return lua.Outcome{}
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = state.Call(function.Value())
	var failure *lua.Error
	if !errors.As(err, &failure) {
		t.Fatalf("ThrowError result = %#v; want *Error", err)
	}
	if failure.Category() != lua.RuntimeError ||
		failure.Error() != cause.Error() ||
		!errors.Is(failure, cause) {
		t.Fatalf("ThrowError result = %#v", failure)
	}
	value, ok := failure.Value().AsString()
	if !ok || value != cause.Error() {
		t.Fatalf("ThrowError Lua value = (%q, %v)", value, ok)
	}
}

// Each installed function gets its own closure state, so two entries built
// from one factory do not share it.
func TestSetFunctionsInstallsIndependentClosureState(t *testing.T) {
	state, err := lua.New(lua.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	counter := func() lua.NativeFunc {
		next := 10.0
		return func(frame lua.Frame) lua.Outcome {
			current := next
			next++
			return frame.ReturnNumber(current)
		}
	}
	if err := state.SetFunctions(
		table,
		map[string]lua.NativeFunc{
			"first":  counter(),
			"second": counter(),
		},
	); err != nil {
		t.Fatal(err)
	}
	firstValue := table.RawGetString("first")
	first, ok := firstValue.AsFunction()
	if !ok {
		t.Fatalf("first = %v, want function", firstValue)
	}
	secondValue := table.RawGetString("second")
	second, ok := secondValue.AsFunction()
	if !ok {
		t.Fatalf("second = %v, want function", secondValue)
	}
	for _, test := range []struct {
		function *lua.Function
		want     lua.Value
	}{
		{function: first, want: lua.Number(10)},
		{function: first, want: lua.Number(11)},
		{function: second, want: lua.Number(10)},
	} {
		results, err := state.Call(test.function.Value())
		if err != nil {
			t.Fatal(err)
		}
		assertPublicValues(t, state, results, test.want)
	}
}

func TestSetFunctionsValidationIsAllOrNothing(t *testing.T) {
	state, err := lua.New(lua.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetString("existing", lua.Number(1)); err != nil {
		t.Fatal(err)
	}
	valid := func(frame lua.Frame) lua.Outcome { return frame.Return() }
	if err := state.SetFunctions(
		table,
		map[string]lua.NativeFunc{
			"good": valid,
			"bad":  nil,
		},
	); !errors.Is(err, lua.ErrInvalidNativeFunction) {
		t.Fatalf("nil function error = %v", err)
	}
	if !table.RawGetString("good").IsNil() ||
		!table.RawGetString("bad").IsNil() {
		t.Fatal("nil function failure partially installed fields")
	}
	assertPublicValues(t, state, []lua.Value{table.RawGetString("existing")}, lua.Number(1))

	other, err := lua.New(lua.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	foreignTable, err := other.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetFunctions(
		foreignTable,
		map[string]lua.NativeFunc{"field": valid},
	); !errors.Is(err, lua.ErrForeignValue) {
		t.Fatalf("foreign table error = %v", err)
	}
	if err := state.SetFunctions(
		nil,
		map[string]lua.NativeFunc{"field": valid},
	); !errors.Is(err, lua.ErrInvalidValue) {
		t.Fatalf("nil table error = %v", err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}

}

func assertPublicValues(t *testing.T, state *lua.State, got []lua.Value, want ...lua.Value) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("results = %v; want %v", got, want)
	}
	for i := range want {
		equal, err := state.RawEqual(got[i], want[i])
		if err != nil || !equal {
			t.Fatalf("result %d = %v; want %v (error %v)", i, got[i], want[i], err)
		}
	}
}
