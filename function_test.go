package lua

import (
	"errors"
	"testing"
)

func TestCompactUpvalueLifecycle(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	thread := state.MainThread()
	thread.values = []slot{
		nilSlot,
		slotFromValue(Number(10)),
		slotFromValue(state.String("captured")),
	}

	high := thread.captureUpvalue(2)
	middle := thread.captureUpvalue(1)
	if thread.captureUpvalue(2) != high {
		t.Fatal("capturing the same register created a second upvalue")
	}
	if thread.openUpvalues != high || high.next != middle {
		t.Fatal("open upvalues are not ordered by descending stack index")
	}

	middle.write(slotFromValue(Number(11)))
	if got, ok := middle.read().owningValue().AsNumber(); !ok || got != 11 {
		t.Fatalf("open upvalue read = (%v, %v)", got, ok)
	}

	thread.closeUpvalues(2)
	if high.thread != nil || high.index != -1 {
		t.Fatal("high upvalue did not close")
	}
	if thread.openUpvalues != middle {
		t.Fatal("closing one upvalue disturbed a lower open upvalue")
	}
	if text, ok := high.read().owningValue().AsString(); !ok || text != "captured" {
		t.Fatalf("closed upvalue = (%q, %v)", text, ok)
	}

	thread.closeUpvalues(0)
	if thread.openUpvalues != nil || middle.thread != nil {
		t.Fatal("remaining upvalues did not close")
	}
	middle.write(slotFromValue(Bool(true)))
	if got, ok := middle.read().owningValue().AsBool(); !ok || !got {
		t.Fatalf("closed upvalue write = (%v, %v)", got, ok)
	}

	closed := newClosedUpvalue(nilSlot)
	if !closed.read().owningValue().IsNil() {
		t.Fatal("new closed upvalue did not retain nil")
	}
}

func TestControlledObjectMetadata(t *testing.T) {
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

	environment, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	prototype, syntaxError := testPrototypeBuilder(
		makeABC(opReturn, 0, 1, 0),
	).seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	function := &Function{
		objectHeader: objectHeader{owner: state.runtime},
		prototype:    prototype,
		environment:  state.globals,
	}
	if err := state.SetFunctionEnvironment(function, environment); err != nil {
		t.Fatal(err)
	}
	if got, err := state.FunctionEnvironment(function); err != nil || got != environment {
		t.Fatalf("FunctionEnvironment = (%p, %v)", got, err)
	}

	foreignEnvironment, err := other.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetFunctionEnvironment(function, foreignEnvironment); !errors.Is(err, ErrForeignValue) {
		t.Fatalf("foreign function environment error = %v", err)
	}

	data, err := state.NewUserData("payload")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetUserDataEnvironment(data, environment); err != nil {
		t.Fatal(err)
	}
	if got, err := state.UserDataEnvironment(data); err != nil || got != environment {
		t.Fatalf("UserDataEnvironment = (%p, %v)", got, err)
	}

	table, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	metatable, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(table.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	if got, err := state.Metatable(table.Value()); err != nil || got != metatable {
		t.Fatalf("table metatable = (%p, %v)", got, err)
	}
	if err := state.SetMetatable(Number(1), metatable); err != nil {
		t.Fatal(err)
	}
	if got, err := state.Metatable(Number(2)); err != nil || got != metatable {
		t.Fatalf("number type metatable = (%p, %v)", got, err)
	}
	if err := state.SetMetatable(table.Value(), foreignEnvironment); !errors.Is(err, ErrForeignValue) {
		t.Fatalf("foreign metatable error = %v", err)
	}
}
