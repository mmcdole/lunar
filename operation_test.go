package lua

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestStateIndexAndSetIndexApplyLuaSemantics(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	direct, err := state.NewTableWithCapacity(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := direct.RawSetString("present", Number(41)); err != nil {
		t.Fatal(err)
	}
	if got, err := state.Index(
		direct.Value(),
		String("present"),
	); err != nil {
		t.Fatal(err)
	} else {
		assertTestValue(t, got, Number(41))
	}
	if got, err := state.Index(
		direct.Value(),
		String("missing"),
	); err != nil {
		t.Fatal(err)
	} else if !got.IsNil() {
		t.Fatalf("missing Index = %v, want nil", got)
	}

	fallback, err := state.NewTableWithCapacity(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := fallback.RawSetString("inherited", Number(42)); err != nil {
		t.Fatal(err)
	}
	chained, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	chainedMetatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := chainedMetatable.RawSetString(
		"__index",
		fallback.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(chained.Value(), chainedMetatable); err != nil {
		t.Fatal(err)
	}
	if got, err := state.Index(
		chained.Value(),
		String("inherited"),
	); err != nil {
		t.Fatal(err)
	} else {
		assertTestValue(t, got, Number(42))
	}

	indexCalls := 0
	indexHandler, err := state.NewNativeFunction(func(frame Frame) Outcome {
		indexCalls++
		key, _ := frame.String(1)
		return frame.ReturnString("handled:" + key)
	})
	if err != nil {
		t.Fatal(err)
	}
	computed, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	computedMetatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := computedMetatable.RawSetString(
		"__index",
		indexHandler.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		computed.Value(),
		computedMetatable,
	); err != nil {
		t.Fatal(err)
	}
	if raw, err := computed.RawGet(String("virtual")); err != nil {
		t.Fatal(err)
	} else if !raw.IsNil() || indexCalls != 0 {
		t.Fatal("RawGet invoked __index")
	}
	if got, err := state.Index(
		computed.Value(),
		String("virtual"),
	); err != nil {
		t.Fatal(err)
	} else if text, ok := got.AsString(); !ok || text != "handled:virtual" {
		t.Fatalf("computed Index = %v", got)
	}
	if indexCalls != 1 {
		t.Fatalf("__index calls = %d, want 1", indexCalls)
	}

	newIndexCalls := 0
	var assignedKey, assignedValue Value
	newIndexHandler, err := state.NewNativeFunction(func(frame Frame) Outcome {
		newIndexCalls++
		assignedKey, _ = frame.Argument(1)
		assignedValue, _ = frame.Argument(2)
		return frame.ReturnString("discarded")
	})
	if err != nil {
		t.Fatal(err)
	}
	directMetatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := directMetatable.RawSetString(
		"__newindex",
		newIndexHandler.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(direct.Value(), directMetatable); err != nil {
		t.Fatal(err)
	}
	if err := state.SetIndex(
		direct.Value(),
		String("present"),
		Number(43),
	); err != nil {
		t.Fatal(err)
	}
	if newIndexCalls != 0 {
		t.Fatal("SetIndex invoked __newindex for an existing field")
	}
	if got := rawStr(direct, "present"); got.String() != "43" {
		t.Fatalf("direct SetIndex stored %v, want 43", got)
	}
	if err := state.SetIndex(
		direct.Value(),
		String("virtual"),
		Number(44),
	); err != nil {
		t.Fatal(err)
	}
	if newIndexCalls != 1 {
		t.Fatalf("__newindex calls = %d, want 1", newIndexCalls)
	}
	assertTestValue(t, assignedKey, String("virtual"))
	assertTestValue(t, assignedValue, Number(44))
	if raw := rawStr(direct, "virtual"); !raw.IsNil() {
		t.Fatalf("__newindex assignment leaked into target: %v", raw)
	}

	redirected, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	redirectMetatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := redirectMetatable.RawSetString(
		"__newindex",
		redirected.Value(),
	); err != nil {
		t.Fatal(err)
	}
	redirectSource, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		redirectSource.Value(),
		redirectMetatable,
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetIndex(
		redirectSource.Value(),
		String("redirected"),
		Number(45),
	); err != nil {
		t.Fatal(err)
	}
	if got := rawStr(redirected, "redirected"); got.String() != "45" {
		t.Fatalf("table-valued __newindex stored %v, want 45", got)
	}
}

func TestStateToStringAndLenApplyLuaSemantics(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	for _, test := range []struct {
		value Value
		want  string
	}{
		{value: Nil(), want: "nil"},
		{value: Bool(true), want: "true"},
		{value: Number(12.5), want: "12.5"},
		{value: String("text"), want: "text"},
	} {
		got, err := state.ToString(test.value)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("ToString(%v) = %q, want %q", test.value, got, test.want)
		}
	}

	target, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	toStringCalls := 0
	toStringHandler, err := state.NewNativeFunction(func(frame Frame) Outcome {
		toStringCalls++
		return frame.ReturnString("semantic")
	})
	if err != nil {
		t.Fatal(err)
	}
	metatable, err := state.NewTableWithCapacity(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString(
		"__tostring",
		toStringHandler.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(target.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	diagnostic := target.Value().String()
	if toStringCalls != 0 || !strings.HasPrefix(diagnostic, "table: ") {
		t.Fatalf("diagnostic String = %q, calls = %d", diagnostic, toStringCalls)
	}
	if got, err := state.ToString(target.Value()); err != nil {
		t.Fatal(err)
	} else if got != "semantic" {
		t.Fatalf("metamethod ToString = %q, want semantic", got)
	}
	if toStringCalls != 1 {
		t.Fatalf("__tostring calls = %d, want 1", toStringCalls)
	}

	badToString, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnNumber(1)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString(
		"__tostring",
		badToString.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ToString(target.Value()); err == nil ||
		!strings.Contains(err.Error(), "__tostring") {
		t.Fatalf("non-string __tostring error = %v", err)
	}

	if length, err := state.Len(String("a\x00é")); err != nil {
		t.Fatal(err)
	} else {
		assertTestValue(t, length, Number(4))
	}
	sequence, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	if err := sequence.RawSetInt(1, Bool(true)); err != nil {
		t.Fatal(err)
	}
	if err := sequence.RawSetInt(2, Bool(true)); err != nil {
		t.Fatal(err)
	}
	if length, err := state.Len(sequence.Value()); err != nil {
		t.Fatal(err)
	} else {
		assertTestValue(t, length, Number(2))
	}

	marker, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	lengthHandler, err := state.NewNativeFunction(func(frame Frame) Outcome {
		if frame.ArgumentCount() != 2 {
			frame.ThrowString("length did not receive two arguments")
		}
		second, _ := frame.Argument(1)
		if !second.IsNil() {
			frame.ThrowString("length fallback was not nil")
		}
		return frame.ReturnValue(marker.Value())
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := state.NewUserData("payload")
	if err != nil {
		t.Fatal(err)
	}
	lengthMetatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := lengthMetatable.RawSetString(
		"__len",
		lengthHandler.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(data.Value(), lengthMetatable); err != nil {
		t.Fatal(err)
	}
	if length, err := state.Len(data.Value()); err != nil {
		t.Fatal(err)
	} else if same, applicable := length.SameObject(marker.Value()); !applicable ||
		!same {
		t.Fatalf("arbitrary __len result = %v", length)
	}
	if _, err := state.Len(Bool(false)); err == nil ||
		!strings.Contains(err.Error(), "get length") {
		t.Fatalf("missing __len error = %v", err)
	}
}

func TestSemanticGlobalsAndRawGlobalsRemainDistinct(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	indexCalls := 0
	index, err := state.NewNativeFunction(func(frame Frame) Outcome {
		indexCalls++
		key, _ := frame.String(1)
		return frame.ReturnString("global:" + key)
	})
	if err != nil {
		t.Fatal(err)
	}
	setCalls := 0
	var assigned Value
	newIndex, err := state.NewNativeFunction(func(frame Frame) Outcome {
		setCalls++
		assigned, _ = frame.Argument(2)
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	metatable, err := state.NewTableWithCapacity(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__index", index.Value()); err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString(
		"__newindex",
		newIndex.Value(),
	); err != nil {
		t.Fatal(err)
	}
	globals := state.main.globals.owningValue()
	if err := state.SetMetatable(globals, metatable); err != nil {
		t.Fatal(err)
	}

	if raw, err := state.RawGlobal("virtual"); err != nil {
		t.Fatal(err)
	} else if !raw.IsNil() || indexCalls != 0 {
		t.Fatal("RawGlobal invoked __index")
	}
	if got, err := state.Global("virtual"); err != nil {
		t.Fatal(err)
	} else if text, ok := got.AsString(); !ok || text != "global:virtual" {
		t.Fatalf("semantic Global = %v", got)
	}
	if indexCalls != 1 {
		t.Fatalf("global __index calls = %d, want 1", indexCalls)
	}

	if err := state.SetGlobal("virtual", Number(9)); err != nil {
		t.Fatal(err)
	}
	if setCalls != 1 {
		t.Fatalf("global __newindex calls = %d, want 1", setCalls)
	}
	assertTestValue(t, assigned, Number(9))
	if raw, err := state.RawGlobal("virtual"); err != nil {
		t.Fatal(err)
	} else if !raw.IsNil() {
		t.Fatalf("semantic virtual assignment was stored raw: %v", raw)
	}
	if err := state.RawSetGlobal("raw", Number(10)); err != nil {
		t.Fatal(err)
	}
	if setCalls != 1 {
		t.Fatal("SetRawGlobal invoked __newindex")
	}
	if got, err := state.Global("raw"); err != nil {
		t.Fatal(err)
	} else {
		assertTestValue(t, got, Number(10))
	}
}

func TestSemanticOperationsContextReentryYieldAndErrors(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	target, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	metatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "operation")
	var observed context.Context
	var reentryErr error
	handler, err := state.NewNativeFunction(func(frame Frame) Outcome {
		observed = frame.Context()
		_, reentryErr = state.Index(target.Value(), String("nested"))
		return frame.ReturnString("handled")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__index", handler.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(target.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	if got, err := indexCtx(t, state, ctx, target.Value(),
		String("key"),
	); err != nil {
		t.Fatal(err)
	} else if text, ok := got.AsString(); !ok || text != "handled" {
		t.Fatalf("context Index = %v", got)
	}
	if observed != ctx {
		t.Fatal("IndexContext did not expose its exact context")
	}
	if !errors.Is(reentryErr, ErrRunning) {
		t.Fatalf("State reentry error = %v, want ErrRunning", reentryErr)
	}
	if _, err := indexCtx(t, state, nil, target.Value(),
		String("key"),
	); !errors.Is(err, ErrNilContext) {
		t.Fatalf("nil context error = %v, want ErrNilContext", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := indexCtx(t, state, cancelled, target.Value(),
		String("key"),
	); err == nil {
		t.Fatal("cancelled IndexContext succeeded")
	} else {
		var failure *Error
		if !errors.As(err, &failure) ||
			failure.Category() != ContextError ||
			!errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled IndexContext error = %#v", err)
		}
	}

	yielding, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.YieldValue(String("not allowed"))
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__index", yielding.Value()); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Index(
		target.Value(),
		String("yield"),
	); err == nil ||
		err.Error() != "attempt to yield across metamethod/C-call boundary" {
		t.Fatalf("yielding __index error = %v", err)
	}
	assertRootThreadReady(t, state.main)

	failingChunk := mustLoadString(t, state, "@operation-index-error.lua", `
return function()
	local missing = nil
	return missing.field
end
`)
	results, err := state.Call(failingChunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	failing, ok := results[0].AsFunction()
	if !ok {
		t.Fatalf("failing handler = %v", results[0])
	}
	if err := metatable.RawSetString("__index", failing.Value()); err != nil {
		t.Fatal(err)
	}
	_, err = state.Index(target.Value(), String("failure"))
	var failure *Error
	if !errors.As(err, &failure) {
		t.Fatalf("metamethod failure = %#v, want *Error", err)
	}
	traceback := failure.Traceback()
	foundSource := false
	for _, frame := range traceback {
		if frame.Source == "@operation-index-error.lua" {
			foundSource = true
			break
		}
	}
	if !foundSource {
		t.Fatalf("metamethod traceback = %+v", traceback)
	}
	assertRootThreadReady(t, state.main)
}

func TestSemanticOperationsRejectInvalidForeignAndClosedValues(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	target, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Index(Value{}, String("key")); !errors.Is(
		err,
		ErrInvalidValue,
	) {
		t.Fatalf("invalid target error = %v, want ErrInvalidValue", err)
	}

	other, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := other.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Index(
		foreign.Value(),
		String("key"),
	); !errors.Is(err, ErrForeignValue) {
		t.Fatalf("foreign target error = %v, want ErrForeignValue", err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Index(
		target.Value(),
		String("key"),
	); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed Index error = %v, want ErrClosed", err)
	}
}
