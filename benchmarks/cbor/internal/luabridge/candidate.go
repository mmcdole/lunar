//go:build !gopherlua_reference

package luabridge

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	engine "github.com/mmcdole/lugo"
)

type (
	Value = engine.Value
	Table = engine.Table
)

// State adapts the benchmark operations to Lugo's owned public API.
type State struct {
	state      *engine.State
	guarded    bool
	iterator   engine.Value
	visitor    engine.Value
	visit      func(Value, Value) error
	visitError error
}

func NewState(options Options) (*State, error) {
	now := time.Now
	if options.FixedUnixTime != 0 {
		fixed := time.Unix(options.FixedUnixTime, 0)
		now = func() time.Time { return fixed }
	}
	runtime, err := engine.New(engine.Options{Now: now})
	if err != nil {
		return nil, err
	}
	closeOnError := func(failure error) (*State, error) {
		_ = runtime.Close()
		return nil, failure
	}
	for _, open := range []func() error{
		runtime.OpenBase,
		runtime.OpenPackage,
		runtime.OpenMath,
		runtime.OpenTable,
		runtime.OpenString,
		runtime.OpenIO,
		runtime.OpenOS,
		runtime.OpenDebug,
		runtime.OpenCoroutine,
	} {
		if err := open(); err != nil {
			return closeOnError(err)
		}
	}

	bridge := &State{state: runtime}
	chunk, err := runtime.LoadString("@cbor-iterator.lua", `
return function(target, emit)
	for key, value in next, target do
		emit(key, value)
	end
end
`)
	if err != nil {
		return closeOnError(err)
	}
	results, err := runtime.Call(chunk.Value())
	if err != nil {
		return closeOnError(err)
	}
	if len(results) != 1 || results[0].Kind() != engine.FunctionKind {
		return closeOnError(fmt.Errorf("iterator helper did not return a function"))
	}
	bridge.iterator = results[0]

	visitor, err := runtime.NewNativeFunction(func(frame engine.Frame) engine.Outcome {
		key, keyPresent := frame.Argument(0)
		value, valuePresent := frame.Argument(1)
		if !keyPresent || !valuePresent {
			bridge.visitError = fmt.Errorf("iterator callback omitted key or value")
			return frame.RaiseString(bridge.visitError.Error())
		}
		if bridge.visit == nil {
			bridge.visitError = fmt.Errorf("iterator callback ran outside traversal")
			return frame.RaiseString(bridge.visitError.Error())
		}
		if err := bridge.visit(key, value); err != nil {
			bridge.visitError = err
			return frame.RaiseString(err.Error())
		}
		return frame.Return()
	})
	if err != nil {
		return closeOnError(err)
	}
	bridge.visitor = visitor.Value()
	return bridge, nil
}

func (state *State) Close() error {
	return state.state.Close()
}

func (state *State) RuntimeVersion() string {
	return "Lugo (Lua 5.1)"
}

func (state *State) ConfigureExecution(guarded bool, interval int) error {
	if interval != 0 {
		return fmt.Errorf("Lugo uses operation-scoped contexts and does not support configurable polling intervals")
	}
	state.guarded = guarded
	return nil
}

func (state *State) String(text string) Value {
	return state.state.String(text)
}

func (state *State) NewTable(arrayHint, recordHint int) (*Table, error) {
	return state.state.NewTable(arrayHint, recordHint)
}

func (state *State) Global(name string) (Value, error) {
	return state.state.Global(name)
}

func (state *State) SetGlobal(name string, value Value) error {
	return state.state.SetGlobal(name, value)
}

func (state *State) PrependPackagePath(root string) error {
	value, err := state.state.Global("package")
	if err != nil {
		return err
	}
	table, ok := value.Table()
	if !ok {
		return fmt.Errorf("package library is %s, not a table", value.Kind())
	}
	current, ok := table.RawGetString("path").AsString()
	if !ok {
		return fmt.Errorf("package.path is not a string")
	}
	return table.RawSetString(
		"path",
		state.state.String(filepath.Join(root, "?.lua")+";"+current),
	)
}

func (state *State) DoString(sourceName, source string) error {
	chunk, err := state.state.LoadString(sourceName, source)
	if err != nil {
		return err
	}
	_, err = state.state.Call(chunk.Value())
	return err
}

func (state *State) DoFile(path string) error {
	chunk, err := state.state.LoadFile(path)
	if err != nil {
		return err
	}
	_, err = state.state.Call(chunk.Value())
	return err
}

func (state *State) NewLogFunction(verbose bool) (Value, error) {
	function, err := state.state.NewNativeFunction(func(frame engine.Frame) engine.Outcome {
		if verbose {
			for index := 0; index < frame.ArgumentCount(); index++ {
				value, _ := frame.Argument(index)
				fmt.Println("lua:", value.String())
			}
		}
		return frame.Return()
	})
	if err != nil {
		return Value{}, err
	}
	return function.Value(), nil
}

func (state *State) CallOne(function Value, argument Value) (Value, error) {
	results, err := state.state.Call(function, argument)
	if err != nil {
		return Value{}, err
	}
	if len(results) != 1 {
		return Value{}, fmt.Errorf("call returned %d results, want 1", len(results))
	}
	return results[0], nil
}

func (state *State) CallGlobalBool(name string) error {
	function, err := state.state.Global(name)
	if err != nil {
		return err
	}
	if function.Kind() != engine.FunctionKind {
		return fmt.Errorf("global %s is %s, not a function", name, function.Kind())
	}
	var result [1]engine.Value
	var count int
	if state.guarded {
		count, err = state.state.CallIntoContext(
			context.Background(),
			function,
			nil,
			result[:],
		)
	} else {
		count, err = state.state.CallInto(function, nil, result[:])
	}
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("%s returned %d results, want 1", name, count)
	}
	value, ok := result[0].AsBool()
	if !ok || !value {
		return fmt.Errorf("%s returned %s", name, result[0])
	}
	return nil
}

func (state *State) ForEach(
	table *Table,
	visit func(Value, Value) error,
) error {
	if state.visit != nil {
		return fmt.Errorf("nested bridge traversal is unsupported")
	}
	state.visit = visit
	state.visitError = nil
	defer func() {
		state.visit = nil
		state.visitError = nil
	}()
	arguments := [...]engine.Value{table.Value(), state.visitor}
	_, err := state.state.CallInto(state.iterator, arguments[:], nil)
	if state.visitError != nil {
		return state.visitError
	}
	return err
}

func Nil() Value {
	return engine.Nil()
}

func Bool(value bool) Value {
	return engine.Bool(value)
}

func Number(value float64) Value {
	return engine.Number(value)
}

func TableValue(table *Table) Value {
	return table.Value()
}

func ValueKind(value Value) Kind {
	return Kind(value.Kind())
}

func ValueTypeName(value Value) string {
	return value.Kind().String()
}

func ValueBool(value Value) (bool, bool) {
	return value.AsBool()
}

func ValueNumber(value Value) (float64, bool) {
	return value.AsNumber()
}

func ValueString(value Value) (string, bool) {
	return value.AsString()
}

func ValueTable(value Value) (*Table, bool) {
	return value.Table()
}

func TableRawGetString(table *Table, key string) Value {
	return table.RawGetString(key)
}

func TableRawSetString(table *Table, key string, value Value) error {
	return table.RawSetString(key, value)
}

func TableRawSetInt(table *Table, key int, value Value) error {
	return table.RawSetInt(key, value)
}

func TableLen(table *Table) int {
	return table.RawLen()
}
