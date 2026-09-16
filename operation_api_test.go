package lua_test

import (
	"errors"
	"testing"

	"github.com/mmcdole/lunar"
)

// The State and Frame entry points must agree with the opcode path even though
// only the opcode path can suspend at a metamethod continuation.
func TestOperationsAgreeAcrossLuaStateAndFrame(t *testing.T) {
	type operations interface {
		Index(lua.Value, lua.Value) (lua.Value, error)
		SetIndex(lua.Value, lua.Value, lua.Value) error
		Equal(lua.Value, lua.Value) (bool, error)
		Len(lua.Value) (lua.Value, error)
	}
	index := func(api operations, args []lua.Value) (lua.Value, error) {
		return api.Index(args[0], args[1])
	}
	assign := func(api operations, args []lua.Value) (lua.Value, error) {
		if err := api.SetIndex(args[0], args[1], args[2]); err != nil {
			return lua.Value{}, err
		}
		return api.Index(args[0], args[1])
	}
	equal := func(api operations, args []lua.Value) (lua.Value, error) {
		same, err := api.Equal(args[0], args[1])
		return lua.Bool(same), err
	}
	length := func(api operations, args []lua.Value) (lua.Value, error) { return api.Len(args[0]) }
	for _, test := range []struct {
		name, source string
		operation    func(operations, []lua.Value) (lua.Value, error)
		want         lua.Value
		failure      bool
	}{
		{"present false", `return function(t,k) return t[k] end, {key=false}, "key"`, index, lua.Bool(false), false},
		{"index chain", `
local fallback = setmetatable({}, {__index=function(_, key) return key .. "!" end})
return function(t,k) return t[k] end, setmetatable({}, {__index=fallback}), "key"
`, index, lua.String("key!"), false},
		{"index cycle", `
local t = {}; setmetatable(t, {__index=t})
return function(t,k) return t[k] end, t, "missing"
`, index, lua.Value{}, true},
		{"assignment chain", `
local backing = {}
local t = setmetatable({}, {__index=backing, __newindex=backing})
return function(t,k,v) t[k]=v; return t[k] end, t, "key", 42
`, assign, lua.Number(42), false},
		{"invalid assignment", `return function(t,k,v) t[k]=v; return t[k] end, {}, 0/0, 42`, assign, lua.Value{}, true},
		{"shared equality handler", `
local mt = {__eq=function() return "truthy" end}
return function(a,b) return a==b end, setmetatable({}, mt), setmetatable({}, mt)
`, equal, lua.Bool(true), false},
		{"different equality handlers", `
return function(a,b) return a==b end,
 setmetatable({}, {__eq=function() return true end}),
 setmetatable({}, {__eq=function() return true end})
`, equal, lua.Bool(false), false},
		{"userdata length", `
local value = newproxy(true); getmetatable(value).__len = function() return "length" end
return function(v) return #v end, value
`, length, lua.String("length"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, route := range []string{"Lua", "State", "Frame"} {
				t.Run(route, func(t *testing.T) {
					state, err := lua.New(lua.Options{Libraries: lua.LibrarySet{lua.BaseLibrary}})
					if err != nil {
						t.Fatal(err)
					}
					defer state.Close()
					prepared, err := state.DoString("@operation-parity.lua", test.source)
					if err != nil {
						t.Fatal(err)
					}
					args := prepared[1:]
					var result lua.Value
					switch route {
					case "Lua":
						result, err = state.CallOne(prepared[0], args...)
					case "State":
						result, err = test.operation(state, args)
					case "Frame":
						callback, createErr := state.NewNativeFunction(func(frame lua.Frame) lua.Outcome {
							result, err := test.operation(frame, args)
							if err != nil {
								frame.ThrowError(err)
							}
							return frame.ReturnValue(result)
						})
						if createErr != nil {
							t.Fatal(createErr)
						}
						result, err = state.CallOne(callback.Value())
					}
					if test.failure {
						var failure *lua.Error
						if !errors.As(err, &failure) || failure.Category() != lua.RuntimeError {
							t.Fatalf("operation error = %v; want a Lua runtime error", err)
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						same, compareErr := state.RawEqual(result, test.want)
						if compareErr != nil || !same {
							t.Fatalf("result = %v; want %v (error %v)", result, test.want, compareErr)
						}
					}
					// Both successful and failed operations must leave the host boundary usable.
					if _, err := state.DoString("@after-operation.lua", "return 42"); err != nil {
						t.Fatalf("subsequent execution failed: %v", err)
					}
				})
			}
		})
	}
}
