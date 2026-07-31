package lua

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

// The host operations must agree with the executor, so each case is also
// evaluated as Lua source and the two results compared.
func TestArithMatchesLuaEvaluation(t *testing.T) {
	tests := []struct {
		name       string
		operator   Operator
		left       float64
		right      float64
		expression string
	}{
		{"add", AddOperator, 6, 7, "6 + 7"},
		{"subtract", SubtractOperator, 6, 7, "6 - 7"},
		{"multiply", MultiplyOperator, 6, 7, "6 * 7"},
		{"divide", DivideOperator, 6, 7, "6 / 7"},
		{"modulo", ModuloOperator, 13, 5, "13 % 5"},
		{"modulo negative", ModuloOperator, -13, 5, "-13 % 5"},
		{"power", PowerOperator, 2, 10, "2 ^ 10"},
		{"negate", NegateOperator, 6, 0, "-6"},
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := state.Arith(
				test.operator,
				Number(test.left),
				Number(test.right),
			)
			if err != nil {
				t.Fatalf("Arith: %v", err)
			}
			host, ok := result.AsNumber()
			if !ok {
				t.Fatalf("Arith returned %v", result.Kind())
			}

			evaluated, err := state.DoString(
				"@arith.lua",
				"return "+test.expression,
			)
			if err != nil {
				t.Fatalf("evaluating %q: %v", test.expression, err)
			}
			want, _ := evaluated[0].AsNumber()
			if host != want {
				t.Fatalf(
					"Arith(%v) = %v; %q = %v",
					test.operator,
					host,
					test.expression,
					want,
				)
			}
		})
	}
}

func TestConcatMatchesLuaEvaluation(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	tests := []struct {
		name       string
		left       Value
		right      Value
		expression string
	}{
		{"strings", state.String("ab"), state.String("cd"), `"ab" .. "cd"`},
		{"number left", Number(12), state.String("x"), `12 .. "x"`},
		{"number right", state.String("x"), Number(12), `"x" .. 12`},
		{"numbers", Number(1), Number(2), `1 .. 2`},
		{"empty", state.String(""), state.String("tail"), `"" .. "tail"`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := state.Concat(test.left, test.right)
			if err != nil {
				t.Fatalf("Concat: %v", err)
			}
			host, ok := result.AsString()
			if !ok {
				t.Fatalf("Concat returned %v", result.Kind())
			}
			evaluated, err := state.DoString(
				"@concat.lua",
				"return "+test.expression,
			)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := evaluated[0].AsString()
			if host != want {
				t.Fatalf("Concat = %q; %q = %q", host, test.expression, want)
			}
		})
	}
}

// Numeric strings coerce exactly as the executor coerces them.
func TestArithCoercesNumericStrings(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	result, err := state.Arith(
		AddOperator,
		state.String("10"),
		state.String("32"),
	)
	if err != nil {
		t.Fatalf("Arith over numeric strings: %v", err)
	}
	if value, _ := result.AsNumber(); value != 42 {
		t.Fatalf("\"10\" + \"32\" = %v", value)
	}

	if _, err := state.Arith(
		AddOperator,
		state.String("ten"),
		Number(1),
	); err == nil {
		t.Fatal("a nonnumeric string was accepted")
	}
}

func TestArithAndConcatHonorMetamethods(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	results, err := state.DoString("@meta.lua", `
		local meta = {}
		meta.__add = function(left, right) return "added" end
		meta.__unm = function(operand) return "negated" end
		meta.__concat = function(left, right) return "joined" end
		local subject = setmetatable({}, meta)
		return subject
	`)
	if err != nil {
		t.Fatal(err)
	}
	subject := results[0]

	added, err := state.Arith(AddOperator, subject, Number(1))
	if err != nil {
		t.Fatalf("__add: %v", err)
	}
	if text, _ := added.AsString(); text != "added" {
		t.Fatalf("__add produced %v", added)
	}

	// The metamethod is found from either side.
	added, err = state.Arith(AddOperator, Number(1), subject)
	if err != nil {
		t.Fatalf("__add from the right: %v", err)
	}
	if text, _ := added.AsString(); text != "added" {
		t.Fatalf("__add from the right produced %v", added)
	}

	negated, err := state.Arith(NegateOperator, subject, Nil())
	if err != nil {
		t.Fatalf("__unm: %v", err)
	}
	if text, _ := negated.AsString(); text != "negated" {
		t.Fatalf("__unm produced %v", negated)
	}

	joined, err := state.Concat(subject, state.String("tail"))
	if err != nil {
		t.Fatalf("__concat: %v", err)
	}
	if text, _ := joined.AsString(); text != "joined" {
		t.Fatalf("__concat produced %v", joined)
	}
}

func TestArithAndConcatReportTypeErrors(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	table, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}

	_, err = state.Arith(AddOperator, table.Value(), Number(1))
	if err == nil {
		t.Fatal("arithmetic on a table was accepted")
	}
	if !strings.Contains(err.Error(), "attempt to perform arithmetic") {
		t.Fatalf("arithmetic error = %q", err.Error())
	}

	_, err = state.Concat(table.Value(), state.String("x"))
	if err == nil {
		t.Fatal("concatenating a table was accepted")
	}
	if !strings.Contains(err.Error(), "attempt to concatenate") {
		t.Fatalf("concat error = %q", err.Error())
	}
	var failure *Error
	if !errors.As(err, &failure) {
		t.Fatalf("error is not a *Error: %v", err)
	}
	if failure.Category() != RuntimeError {
		t.Fatalf("category = %v; want RuntimeError", failure.Category())
	}
}

// The Frame forms must behave like the State forms, which is what a
// callback implementing an operator relies on.
func TestFrameArithAndConcat(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	combine, err := state.NewNativeFunction(func(frame Frame) Outcome {
		left, _ := frame.Argument(0)
		right, _ := frame.Argument(1)

		sum, callErr := frame.Arith(AddOperator, left, right)
		if callErr != nil {
			return frame.RaiseError(callErr)
		}
		joined, callErr := frame.Concat(left, right)
		if callErr != nil {
			return frame.RaiseError(callErr)
		}
		negated, callErr := frame.Arith(NegateOperator, left, Nil())
		if callErr != nil {
			return frame.RaiseError(callErr)
		}
		return frame.ReturnValues(sum, joined, negated)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("combine", combine.Value()); err != nil {
		t.Fatal(err)
	}

	results, err := state.DoString("@frame.lua", "return combine(6, 7)")
	if err != nil {
		t.Fatal(err)
	}
	if sum, _ := results[0].AsNumber(); sum != 13 {
		t.Errorf("sum = %v", sum)
	}
	if joined, _ := results[1].AsString(); joined != "67" {
		t.Errorf("joined = %q", joined)
	}
	if negated, _ := results[2].AsNumber(); negated != -6 {
		t.Errorf("negated = %v", negated)
	}
}

// A metamethod invoked by a host operation may itself fail; the failure
// must reach the host rather than be swallowed.
func TestArithMetamethodFailurePropagates(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	results, err := state.DoString("@failing.lua", `
		return setmetatable({}, {
			__add = function() error("metamethod refused") end,
		})
	`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Arith(AddOperator, results[0], Number(1)); err == nil {
		t.Fatal("a failing metamethod reported success")
	} else if !strings.Contains(err.Error(), "metamethod refused") {
		t.Fatalf("error = %q", err.Error())
	}
}

// A foreign Value is reported as an error, exactly as Frame.Equal reports
// it — never as a panic.
func TestFrameArithAndConcatRejectForeignValues(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	other, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	foreign, err := other.NewTable()
	if err != nil {
		t.Fatal(err)
	}

	probe, err := state.NewNativeFunction(func(frame Frame) Outcome {
		if _, err := frame.Arith(
			AddOperator,
			foreign.Value(),
			Number(1),
		); !errors.Is(err, ErrForeignValue) {
			t.Errorf("Frame.Arith(foreign) = %v; want ErrForeignValue", err)
		}
		if _, err := frame.Concat(
			foreign.Value(),
			frame.State().String("x"),
		); !errors.Is(err, ErrForeignValue) {
			t.Errorf("Frame.Concat(foreign) = %v; want ErrForeignValue", err)
		}
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Call(probe.Value()); err != nil {
		t.Fatal(err)
	}
}

func TestArithAndConcatContextForms(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	if _, err := state.ArithContext(
		nil,
		AddOperator,
		Number(1),
		Number(2),
	); !errors.Is(err, ErrNilContext) {
		t.Fatalf("ArithContext(nil) = %v; want ErrNilContext", err)
	}
	if _, err := state.ConcatContext(
		nil,
		state.String("a"),
		state.String("b"),
	); !errors.Is(err, ErrNilContext) {
		t.Fatalf("ConcatContext(nil) = %v; want ErrNilContext", err)
	}

	ctx := context.Background()
	result, err := state.ArithContext(ctx, MultiplyOperator, Number(6), Number(7))
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := result.AsNumber(); value != 42 {
		t.Fatalf("ArithContext = %v", value)
	}
	joined, err := state.ConcatContext(ctx, state.String("a"), Number(1))
	if err != nil {
		t.Fatal(err)
	}
	if text, _ := joined.AsString(); text != "a1" {
		t.Fatalf("ConcatContext = %q", text)
	}
}

// Division and modulo by zero follow Lua 5.1 float semantics rather than
// raising, so they are pinned explicitly.
func TestArithFloatEdgeCases(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	result, err := state.Arith(DivideOperator, Number(1), Number(0))
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := result.AsNumber(); !math.IsInf(value, 1) {
		t.Fatalf("1/0 = %v; want +Inf", value)
	}

	result, err = state.Arith(ModuloOperator, Number(1), Number(0))
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := result.AsNumber(); !math.IsNaN(value) {
		t.Fatalf("1%%0 = %v; want NaN", value)
	}
}

func TestInvalidOperatorPanics(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	defer func() {
		if recover() == nil {
			t.Fatal("an out-of-range Operator was accepted")
		}
	}()
	_, _ = state.Arith(Operator(200), Number(1), Number(2))
}

func TestOperatorString(t *testing.T) {
	for operator, want := range map[Operator]string{
		AddOperator:      "add",
		SubtractOperator: "subtract",
		MultiplyOperator: "multiply",
		DivideOperator:   "divide",
		ModuloOperator:   "modulo",
		PowerOperator:    "power",
		NegateOperator:   "negate",
		Operator(200):    "unknown",
	} {
		if got := operator.String(); got != want {
			t.Errorf("Operator(%d).String() = %q; want %q", operator, got, want)
		}
	}
}
