//go:build !gopherlua_reference

package luabridge

import (
	"fmt"
	"path/filepath"
	"time"

	engine "github.com/mmcdole/lunar"
)

type (
	Value = engine.Value
	Table = engine.Table
)

// State adapts the benchmark operations to Lunar's owned public API.
type State struct {
	state *engine.State
}

func NewState(options Options) (*State, error) {
	now := time.Now
	if options.FixedUnixTime != 0 {
		fixed := time.Unix(options.FixedUnixTime, 0)
		now = func() time.Time { return fixed }
	}
	runtime, err := engine.New(engine.Options{
		Libraries:    engine.FullLibraries(),
		ScriptLoader: engine.HostLoader(),
		Now:          now,
	})
	if err != nil {
		return nil, err
	}
	return &State{state: runtime}, nil
}

func (state *State) Close() error {
	return state.state.Close()
}

func (state *State) RuntimeVersion() string {
	return "Lunar (Lua 5.1)"
}

func (state *State) String(text string) Value {
	return state.state.String(text)
}

func (state *State) NewTable(arrayHint, recordHint int) (*Table, error) {
	return state.state.NewTableWithCapacity(arrayHint, recordHint)
}

func (state *State) Global(name string) (Value, error) {
	return state.state.RawGlobal(name)
}

func (state *State) SetGlobal(name string, value Value) error {
	return state.state.RawSetGlobal(name, value)
}

func (state *State) PrependPackagePath(root string) error {
	value, err := state.state.RawGlobal("package")
	if err != nil {
		return err
	}
	table, ok := value.AsTable()
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
	return state.state.CallOne(function, argument)
}

func (state *State) CallGlobalBool(name string) error {
	function, err := state.state.RawGlobal(name)
	if err != nil {
		return err
	}
	if function.Kind() != engine.FunctionKind {
		return fmt.Errorf("global %s is %s, not a function", name, function.Kind())
	}
	var result [1]engine.Value
	count, err := state.state.CallInto(function, nil, result[:])
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
	for key := engine.Nil(); ; {
		next, value, ok, err := table.Next(key)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if err := visit(next, value); err != nil {
			return err
		}
		key = next
	}
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
	return value.AsTable()
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
