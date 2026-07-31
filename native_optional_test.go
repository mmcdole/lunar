package lua

import "testing"

// Each Opt accessor has three behaviors worth pinning: absent yields the
// default, a valid argument is read, and an invalid one is rejected rather
// than silently replaced by the default.
func TestFrameOptionalArguments(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	table, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	fallbackTable, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	function, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	fallbackFunction, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := state.NewUserData("payload")
	if err != nil {
		t.Fatal(err)
	}
	fallbackData, err := state.NewUserData("fallback")
	if err != nil {
		t.Fatal(err)
	}

	// Argument layout: 0 nil, 1 missing at call time, 2 boolean, 3 number,
	// 4 numeric string, 5 string, 6 table, 7 function, 8 userdata.
	const (
		explicitNil = 0
		boolean     = 2
		number      = 3
		numericText = 4
		text        = 5
		tableArg    = 6
		functionArg = 7
		userDataArg = 8
		missing     = 99
	)

	probe, err := state.NewNativeFunction(func(frame Frame) Outcome {
		// Absent arguments take the default.
		if value, ok := frame.OptBool(missing, true); !ok || !value {
			t.Errorf("OptBool(missing) = (%v, %v)", value, ok)
		}
		if value, ok := frame.OptBool(explicitNil, true); !ok || !value {
			t.Errorf("OptBool(nil) = (%v, %v)", value, ok)
		}
		if value, ok := frame.OptNumber(missing, 7.5); !ok || value != 7.5 {
			t.Errorf("OptNumber(missing) = (%v, %v)", value, ok)
		}
		if value, ok := frame.OptInteger(missing, 25); !ok || value != 25 {
			t.Errorf("OptInteger(missing) = (%v, %v)", value, ok)
		}
		if value, ok := frame.OptString(missing, "default"); !ok ||
			value != "default" {
			t.Errorf("OptString(missing) = (%q, %v)", value, ok)
		}
		if value, ok := frame.OptCoerceString(missing, "default"); !ok ||
			value != "default" {
			t.Errorf("OptCoerceString(missing) = (%q, %v)", value, ok)
		}
		if value, ok := frame.OptCoerceNumber(missing, 3.5); !ok ||
			value != 3.5 {
			t.Errorf("OptCoerceNumber(missing) = (%v, %v)", value, ok)
		}
		if value, ok := frame.OptTable(missing, fallbackTable); !ok ||
			value != fallbackTable {
			t.Errorf("OptTable(missing) = (%v, %v)", value, ok)
		}
		if value, ok := frame.OptFunction(missing, fallbackFunction); !ok ||
			value != fallbackFunction {
			t.Errorf("OptFunction(missing) = (%v, %v)", value, ok)
		}
		if value, ok := frame.OptUserData(missing, fallbackData); !ok ||
			value != fallbackData {
			t.Errorf("OptUserData(missing) = (%v, %v)", value, ok)
		}
		if value, ok := frame.OptIntegerInRange(missing, 25, 1, 10); !ok ||
			value != 25 {
			t.Errorf("OptIntegerInRange(missing) = (%v, %v)", value, ok)
		}

		// Present and valid arguments are read.
		if value, ok := frame.OptBool(boolean, false); !ok || !value {
			t.Errorf("OptBool(true) = (%v, %v)", value, ok)
		}
		if value, ok := frame.OptNumber(number, 0); !ok || value != 42 {
			t.Errorf("OptNumber(42) = (%v, %v)", value, ok)
		}
		if value, ok := frame.OptInteger(number, 0); !ok || value != 42 {
			t.Errorf("OptInteger(42) = (%v, %v)", value, ok)
		}
		if value, ok := frame.OptIntegerInRange(number, 0, 1, 50); !ok ||
			value != 42 {
			t.Errorf("OptIntegerInRange(42) = (%v, %v)", value, ok)
		}
		if value, ok := frame.OptString(text, ""); !ok || value != "given" {
			t.Errorf("OptString(given) = (%q, %v)", value, ok)
		}
		if value, ok := frame.OptCoerceNumber(numericText, 0); !ok ||
			value != 12 {
			t.Errorf("OptCoerceNumber(\"12\") = (%v, %v)", value, ok)
		}
		if value, ok := frame.OptCoerceString(number, ""); !ok ||
			value != "42" {
			t.Errorf("OptCoerceString(42) = (%q, %v)", value, ok)
		}
		if value, ok := frame.OptTable(tableArg, nil); !ok || value != table {
			t.Errorf("OptTable(table) = (%v, %v)", value, ok)
		}
		if value, ok := frame.OptFunction(functionArg, nil); !ok ||
			value != function {
			t.Errorf("OptFunction(function) = (%v, %v)", value, ok)
		}
		if value, ok := frame.OptUserData(userDataArg, nil); !ok ||
			value != data {
			t.Errorf("OptUserData(userdata) = (%v, %v)", value, ok)
		}

		// Present but wrong arguments are rejected, not defaulted.
		if value, ok := frame.OptBool(number, true); ok {
			t.Errorf("OptBool(number) = (%v, true); want rejection", value)
		}
		if value, ok := frame.OptNumber(text, 1); ok {
			t.Errorf("OptNumber(string) = (%v, true); want rejection", value)
		}
		if value, ok := frame.OptNumber(numericText, 1); ok {
			t.Errorf(
				"OptNumber(numeric string) = (%v, true); want the exact check",
				value,
			)
		}
		if value, ok := frame.OptString(boolean, "d"); ok {
			t.Errorf("OptString(boolean) = (%q, true); want rejection", value)
		}
		if value, ok := frame.OptTable(number, fallbackTable); ok {
			t.Errorf("OptTable(number) = (%v, true); want rejection", value)
		}
		if value, ok := frame.OptFunction(number, fallbackFunction); ok {
			t.Errorf("OptFunction(number) = (%v, true); want rejection", value)
		}
		if value, ok := frame.OptUserData(number, fallbackData); ok {
			t.Errorf("OptUserData(number) = (%v, true); want rejection", value)
		}
		// Out of range is a rejection, not a fall back to the default.
		if value, ok := frame.OptIntegerInRange(number, 7, 1, 10); ok {
			t.Errorf(
				"OptIntegerInRange(42, 1..10) = (%v, true); want rejection",
				value,
			)
		}
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := state.Call(
		probe.Value(),
		Nil(),
		Nil(),
		Bool(true),
		Number(42),
		state.String("12"),
		state.String("given"),
		table.Value(),
		function.Value(),
		data.Value(),
	); err != nil {
		t.Fatal(err)
	}
}

// The helpers exist to shorten callbacks, so the shortened form must
// behave like the IsMissingOrNil sequence it replaces.
func TestOptionalArgumentsMatchTheManualSequence(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	describe, err := state.NewNativeFunction(func(frame Frame) Outcome {
		manual := int64(25)
		if !frame.IsMissingOrNil(1) {
			value, ok := frame.IntegerInRange(1, 1, 100)
			if !ok {
				return frame.ArgError(1, "integer from 1 through 100 expected")
			}
			manual = value
		}

		helper, ok := frame.OptIntegerInRange(1, 25, 1, 100)
		if !ok {
			return frame.ArgError(1, "integer from 1 through 100 expected")
		}
		if manual != helper {
			t.Errorf("manual = %d; helper = %d", manual, helper)
		}
		return frame.ReturnNumber(float64(helper))
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("describe", describe.Value()); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		source string
		want   float64
	}{
		{source: `return describe("label")`, want: 25},
		{source: `return describe("label", nil)`, want: 25},
		{source: `return describe("label", 60)`, want: 60},
	} {
		results, err := state.DoString("@describe.lua", test.source)
		if err != nil {
			t.Fatalf("%s: %v", test.source, err)
		}
		if value, _ := results[0].AsNumber(); value != test.want {
			t.Errorf("%s = %v; want %v", test.source, value, test.want)
		}
	}

	if _, err := state.DoString(
		"@describe.lua",
		`return describe("label", 500)`,
	); err == nil {
		t.Fatal("out-of-range argument was accepted")
	}
}
