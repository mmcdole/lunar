package lua

import (
	"errors"
	"strings"
	"testing"
)

func TestOpenBaseIsExplicitAndUsesTheGlobalEnvironment(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}

	before, err := state.Global("pcall")
	if err != nil {
		t.Fatal(err)
	}
	if !before.IsNil() {
		t.Fatalf("new state pcall = %v; want nil", before)
	}
	loadedBeforeOpen := mustLoadString(
		t,
		state,
		"@open-base.lua",
		`return pcall(function() return 42 end)`,
	)
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"pcall", "xpcall"} {
		value, globalErr := state.Global(name)
		if globalErr != nil {
			t.Fatal(globalErr)
		}
		if value.Kind() != FunctionKind {
			t.Fatalf("%s = %v; want function", name, value)
		}
	}
	global, err := state.Global("_G")
	if err != nil {
		t.Fatal(err)
	}
	if same, applicable := global.SameObject(state.globals.Value()); !applicable || !same {
		t.Fatal("_G does not identify the canonical global environment")
	}
	version, err := state.Global("_VERSION")
	if err != nil {
		t.Fatal(err)
	}
	if text, ok := version.AsString(); !ok || text != "Lua 5.1" {
		t.Fatalf("_VERSION = (%q, %v); want Lua 5.1", text, ok)
	}

	results, err := state.Call(loadedBeforeOpen.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true), Number(42))

	oldPCall, _ := state.Global("pcall")
	oldXPCall, _ := state.Global("xpcall")
	if err := state.SetGlobal("pcall", Number(1)); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("_G", Nil()); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}
	newPCall, _ := state.Global("pcall")
	newXPCall, _ := state.Global("xpcall")
	for name, pair := range map[string][2]Value{
		"pcall":  {oldPCall, newPCall},
		"xpcall": {oldXPCall, newXPCall},
	} {
		if pair[1].Kind() != FunctionKind {
			t.Fatalf("reopened %s = %v; want function", name, pair[1])
		}
		if same, applicable := pair[0].SameObject(pair[1]); !applicable || same {
			t.Fatalf("reopened %s did not receive a fresh function", name)
		}
	}
	global, _ = state.Global("_G")
	if same, applicable := global.SameObject(state.globals.Value()); !applicable || !same {
		t.Fatal("reopening did not restore _G")
	}

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenBase(); !errors.Is(err, ErrClosed) {
		t.Fatalf("OpenBase after Close = %v; want ErrClosed", err)
	}
}

func TestBasePCallLua51ResultsAndArguments(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	testCases := []struct {
		name   string
		source string
		want   []Value
	}{
		{
			name: "no target results",
			source: `
return pcall(function() end)
`,
			want: []Value{Bool(true)},
		},
		{
			name: "all target results",
			source: `
return pcall(function(...) return ... end, 1, nil, 3, nil)
`,
			want: []Value{
				Bool(true),
				Number(1),
				Nil(),
				Number(3),
				Nil(),
			},
		},
		{
			name: "fixed caller adjustment",
			source: `
local one = pcall(function() return 1, 2 end)
local a, b, c, d = pcall(function() return 9 end)
return one, a, b, c, d
`,
			want: []Value{
				Bool(true),
				Bool(true),
				Number(9),
				Nil(),
				Nil(),
			},
		},
		{
			name: "explicit nil target is protected",
			source: `
return pcall(nil)
`,
			want: []Value{Bool(false)},
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			chunk := mustLoadString(t, state, "@pcall.lua", test.source)
			results, callErr := state.Call(chunk.Value())
			if callErr != nil {
				t.Fatal(callErr)
			}
			if test.name == "explicit nil target is protected" {
				if len(results) != 2 ||
					results[0].Truth() ||
					!strings.Contains(
						results[1].String(),
						"attempt to call a nil value",
					) {
					t.Fatalf("pcall(nil) = %v", results)
				}
				return
			}
			assertTestValues(t, results, test.want...)
		})
	}

	missing := mustLoadString(t, state, "@missing-pcall.lua", `return pcall()`)
	if _, callErr := state.Call(missing.Value()); callErr == nil ||
		callErr.Error() !=
			"missing-pcall.lua:1: bad argument #1 to 'pcall' (value expected)" {
		t.Fatalf("pcall() error = %v", callErr)
	}
}

func TestBasePCallPreservesCallableAndErrorIdentity(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	callable, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	callableBody, err := state.NewNativeFunction(func(frame Frame) Outcome {
		first, _ := frame.Argument(0)
		second, _ := frame.Argument(1)
		return frame.ReturnValues(first, second)
	})
	if err != nil {
		t.Fatal(err)
	}
	metatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__call", callableBody.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(callable.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("callable", callable.Value()); err != nil {
		t.Fatal(err)
	}

	marker, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	raiser, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.Raise(marker.Value())
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("raise_marker", raiser.Value()); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@pcall-identity.lua", `
local callOK, self, argument = pcall(callable, 17)
local raiseOK, value = pcall(raise_marker)
return callOK, self, argument, raiseOK, value
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 5 {
		t.Fatalf("result count = %d; want 5", len(results))
	}
	assertTestValue(t, results[0], Bool(true))
	if same, applicable := results[1].SameObject(callable.Value()); !applicable || !same {
		t.Fatal("__call did not receive the original callable table")
	}
	assertTestValue(t, results[2], Number(17))
	assertTestValue(t, results[3], Bool(false))
	if same, applicable := results[4].SameObject(marker.Value()); !applicable || !same {
		t.Fatal("pcall did not preserve the arbitrary error object")
	}
}

func TestBaseXPCallMatchesLua51HandlerSemantics(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	testCases := []struct {
		name   string
		source string
		want   []Value
	}{
		{
			name: "target gets no arguments and bad handler is ignored",
			source: `
return xpcall(function(...) return 3, ... end, 7, 8, 9)
`,
			want: []Value{Bool(true), Number(3)},
		},
		{
			name: "handler receives the error",
			source: `
return xpcall(
	function() return nil + 1 end,
	function(value) return value end
)
`,
			want: []Value{Bool(false)},
		},
		{
			name: "no handler result becomes nil",
			source: `
return xpcall(
	function() return nil + 1 end,
	function() end
)
`,
			want: []Value{Bool(false), Nil()},
		},
		{
			name: "only first handler result survives",
			source: `
return xpcall(
	function() return nil + 1 end,
	function() return 4, 5 end
)
`,
			want: []Value{Bool(false), Number(4)},
		},
		{
			name: "bad handler fails only when needed",
			source: `
return xpcall(function() return nil + 1 end, 7)
`,
			want: []Value{
				Bool(false),
				state.String("error in error handling"),
			},
		},
		{
			name: "failing handler becomes fixed error",
			source: `
return xpcall(
	function() return nil + 1 end,
	function() return nil + 2 end
)
`,
			want: []Value{
				Bool(false),
				state.String("error in error handling"),
			},
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			chunk := mustLoadString(t, state, "@xpcall.lua", test.source)
			results, callErr := state.Call(chunk.Value())
			if callErr != nil {
				t.Fatal(callErr)
			}
			if test.name == "handler receives the error" {
				if len(results) != 2 ||
					results[0].Truth() ||
					!strings.Contains(
						results[1].String(),
						"attempt to perform arithmetic",
					) {
					t.Fatalf("xpcall transformed error = %v", results)
				}
				return
			}
			assertTestValues(t, results, test.want...)
		})
	}

	missing := mustLoadString(t, state, "@missing-xpcall.lua", `return xpcall(function() end)`)
	if _, callErr := state.Call(missing.Value()); callErr == nil ||
		callErr.Error() !=
			"missing-xpcall.lua:1: bad argument #2 to 'xpcall' (value expected)" {
		t.Fatalf("xpcall without handler error = %v", callErr)
	}
}

func TestBaseXPCallPreservesErrorIdentityAndRejectsCallableHandler(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	marker, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	raiser, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.Raise(marker.Value())
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("raise_marker", raiser.Value()); err != nil {
		t.Fatal(err)
	}

	callableHandler, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	handlerBody, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnString("must not run")
	})
	if err != nil {
		t.Fatal(err)
	}
	handlerMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := handlerMetatable.RawSetString("__call", handlerBody.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(callableHandler.Value(), handlerMetatable); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("callable_handler", callableHandler.Value()); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@xpcall-identity.lua", `
local firstOK, first = xpcall(raise_marker, function(value) return value end)
local secondOK, second = xpcall(raise_marker, callable_handler)
return firstOK, first, secondOK, second
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 4 {
		t.Fatalf("result count = %d; want 4", len(results))
	}
	assertTestValue(t, results[0], Bool(false))
	if same, applicable := results[1].SameObject(marker.Value()); !applicable || !same {
		t.Fatal("xpcall handler did not receive the original error object")
	}
	assertTestValue(t, results[2], Bool(false))
	if message, ok := results[3].AsString(); !ok ||
		message != "error in error handling" {
		t.Fatalf("callable handler failure = %v", results[3])
	}
}

func TestBaseXPCallDiscardsExtraArgumentsBeforeCallingTarget(t *testing.T) {
	const valueLimit = 8
	state := newStateWithBase(t, Options{MaxValues: valueLimit})
	defer state.Close()

	target, err := state.NewNativeFunction(func(frame Frame) Outcome {
		call := frame.activation()
		if frame.depth != 2 {
			return frame.ReturnBool(false)
		}
		xpcall := frame.thread.frames[frame.depth-2]
		expectedBase := int(xpcall.base) + 2
		if int(call.resultBase) != expectedBase {
			return frame.ReturnBool(false)
		}
		for index := int(call.base); index < len(frame.thread.values); index++ {
			if frame.thread.values[index] != (slot{}) {
				return frame.ReturnBool(false)
			}
		}
		return frame.ReturnBool(true)
	})
	if err != nil {
		t.Fatal(err)
	}
	xpcall, err := state.Global("xpcall")
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(
		xpcall,
		target.Value(),
		Nil(),
		Number(1),
		Number(2),
		Number(3),
		Number(4),
		Number(5),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true), Bool(true))
}

func TestProtectedCallsPreserveAnExplicitNilError(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	raiser, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.Raise(Nil())
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnBool(
			frame.ArgumentCount() == 1 &&
				frame.Kind(0) == NilKind,
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("raise_nil", raiser.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("handle_nil", handler.Value()); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@nil-error.lua", `
local firstOK, first = pcall(raise_nil)
local secondOK, second = xpcall(raise_nil, handle_nil)
return firstOK, first, secondOK, second
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Bool(false),
		Nil(),
		Bool(false),
		Bool(true),
	)
}

func newStateWithBase(t testing.TB, options Options) *State {
	t.Helper()
	state, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.OpenBase(); err != nil {
		state.Close()
		t.Fatal(err)
	}
	return state
}
