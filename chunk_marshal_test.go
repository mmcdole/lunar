package lua

import (
	"bytes"
	"encoding"
	"errors"
	"strings"
	"testing"
)

var _ encoding.BinaryMarshaler = (*Prototype)(nil)

// The pipeline the API exists for: compile once without a State, keep the
// bytes, and load them into a State later without parsing again.
func TestPrototypeMarshalRoundTrip(t *testing.T) {
	prototype, err := Compile("@price.lua", `
		local quantity, unit = ...
		return quantity * unit
	`)
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := prototype.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) == 0 {
		t.Fatal("MarshalBinary produced no bytes")
	}

	decoded, err := UnmarshalPrototype("@price.lua", encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SourceName() != prototype.SourceName() {
		t.Errorf(
			"decoded source = %q; want %q",
			decoded.SourceName(),
			prototype.SourceName(),
		)
	}
	if decoded.IsVararg() != prototype.IsVararg() {
		t.Errorf("decoded vararg = %v", decoded.IsVararg())
	}
	first, last := prototype.LineRange()
	decodedFirst, decodedLast := decoded.LineRange()
	if decodedFirst != first || decodedLast != last {
		t.Errorf(
			"decoded line range = (%d, %d); want (%d, %d)",
			decodedFirst, decodedLast, first, last,
		)
	}

	// The decoded Prototype runs, and produces what the original does.
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	for name, candidate := range map[string]*Prototype{
		"compiled": prototype,
		"decoded":  decoded,
	} {
		function, err := state.LoadPrototype(candidate)
		if err != nil {
			t.Fatalf("%s: LoadPrototype: %v", name, err)
		}
		result, err := state.CallOne(function.Value(), Number(6), Number(7.5))
		if err != nil {
			t.Fatalf("%s: call: %v", name, err)
		}
		if value, _ := result.AsNumber(); value != 45 {
			t.Errorf("%s produced %v; want 45", name, value)
		}
	}
}

// One set of bytes must be loadable into several States, which is what
// makes the cache worth keeping.
func TestUnmarshalledPrototypeLoadsIntoManyStates(t *testing.T) {
	prototype, err := Compile("@shared.lua", `return "shared result"`)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := prototype.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalPrototype("@shared.lua", encoded)
	if err != nil {
		t.Fatal(err)
	}

	for index := 0; index < 3; index++ {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		function, err := state.LoadPrototype(decoded)
		if err != nil {
			state.Close()
			t.Fatalf("state %d: %v", index, err)
		}
		result, err := state.CallOne(function.Value())
		if err != nil {
			state.Close()
			t.Fatalf("state %d: %v", index, err)
		}
		if text, _ := result.AsString(); text != "shared result" {
			t.Errorf("state %d produced %q", index, text)
		}
		state.Close()
	}
}

// string.dump writes the same format, so a chunk dumped by Lua decodes in
// Go and vice versa.
func TestMarshalInteroperatesWithStringDump(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	for _, open := range []func() error{state.OpenBase, state.OpenString} {
		if err := open(); err != nil {
			t.Fatal(err)
		}
	}

	// Lua dumps, Go decodes.
	results, err := state.DoString("@dumped.lua", `
		return string.dump(function() return 21 * 2 end)
	`)
	if err != nil {
		t.Fatal(err)
	}
	dumped, ok := results[0].AsString()
	if !ok {
		t.Fatal("string.dump did not return a string")
	}
	decoded, err := UnmarshalPrototype("@dumped.lua", []byte(dumped))
	if err != nil {
		t.Fatalf("decoding a string.dump chunk: %v", err)
	}
	function, err := state.LoadPrototype(decoded)
	if err != nil {
		t.Fatal(err)
	}
	result, err := state.CallOne(function.Value())
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := result.AsNumber(); value != 42 {
		t.Fatalf("decoded string.dump chunk produced %v", value)
	}

	// Go marshals, Lua loads.
	prototype, err := Compile("@marshalled.lua", `return 6 * 9`)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := prototype.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := state.LoadString("@marshalled.lua", string(encoded))
	if err != nil {
		t.Fatalf("LoadString over marshalled bytes: %v", err)
	}
	result, err = state.CallOne(loaded.Value())
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := result.AsNumber(); value != 54 {
		t.Fatalf("marshalled chunk produced %v", value)
	}
}

// Nested functions, upvalues, and constants must survive the round trip,
// not just a flat chunk.
func TestMarshalPreservesNestedFunctionsAndUpvalues(t *testing.T) {
	prototype, err := Compile("@closure.lua", `
		local base = 10
		local function add(amount) return base + amount end
		local function twice(amount) return add(amount) + add(amount) end
		return twice(5)
	`)
	if err != nil {
		t.Fatal(err)
	}
	if prototype.ChildCount() == 0 {
		t.Fatal("the test chunk has no nested functions")
	}

	encoded, err := prototype.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalPrototype("@closure.lua", encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ChildCount() != prototype.ChildCount() {
		t.Fatalf(
			"decoded child count = %d; want %d",
			decoded.ChildCount(),
			prototype.ChildCount(),
		)
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	function, err := state.LoadPrototype(decoded)
	if err != nil {
		t.Fatal(err)
	}
	result, err := state.CallOne(function.Value())
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := result.AsNumber(); value != 30 {
		t.Fatalf("closure chunk produced %v; want 30", value)
	}
}

// Debug information must survive so a decoded chunk still attributes its
// errors to the right line.
func TestMarshalPreservesLineInformation(t *testing.T) {
	prototype, err := Compile("@lines.lua", "local value = 1\nerror('failed here')")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := prototype.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalPrototype("@lines.lua", encoded)
	if err != nil {
		t.Fatal(err)
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}
	function, err := state.LoadPrototype(decoded)
	if err != nil {
		t.Fatal(err)
	}
	_, err = state.Call(function.Value())
	if err == nil {
		t.Fatal("expected the decoded chunk to fail")
	}
	if !strings.Contains(err.Error(), "lines.lua:2:") {
		t.Fatalf("decoded failure = %q; want line 2 attribution", err.Error())
	}
}

func TestUnmarshalPrototypeRejectsBadInput(t *testing.T) {
	prototype, err := Compile("@ok.lua", `return 1`)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := prototype.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		data []byte
	}{
		{name: "empty", data: nil},
		{name: "source text", data: []byte(`return 1`)},
		{name: "wrong signature", data: []byte("\x1bLua\x99 not a chunk")},
		{name: "truncated", data: encoded[:len(encoded)/2]},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoded, err := UnmarshalPrototype("@bad.lua", test.data)
			if err == nil {
				t.Fatalf("accepted %s input: %v", test.name, decoded)
			}
			var failure *Error
			if !errors.As(err, &failure) {
				t.Fatalf("error is not a *Error: %v", err)
			}
			if failure.Category() != SyntaxError {
				t.Fatalf("category = %v; want SyntaxError", failure.Category())
			}
		})
	}
}

// A corrupted body must be rejected rather than decoded into something the
// executor would run.
func TestUnmarshalPrototypeRejectsCorruptedBody(t *testing.T) {
	prototype, err := Compile("@corrupt.lua", `
		local total = 0
		for index = 1, 10 do total = total + index end
		return total
	`)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := prototype.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	corrupted := bytes.Clone(encoded)
	// Leave the header intact and damage the body.
	for index := len(corrupted) / 2; index < len(corrupted); index++ {
		corrupted[index] ^= 0xff
	}
	if _, err := UnmarshalPrototype("@corrupt.lua", corrupted); err == nil {
		t.Fatal("a corrupted chunk body was accepted")
	}
}

func TestMarshalBinaryRejectsNilPrototype(t *testing.T) {
	var prototype *Prototype
	if _, err := prototype.MarshalBinary(); !errors.Is(
		err,
		ErrInvalidPrototype,
	) {
		t.Fatalf("nil MarshalBinary = %v; want ErrInvalidPrototype", err)
	}
}
