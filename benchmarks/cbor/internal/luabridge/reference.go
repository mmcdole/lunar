//go:build gopherlua_reference

package luabridge

import (
	"context"
	"fmt"
	"path/filepath"

	engine "github.com/yuin/gopher-lua"
)

type (
	Value = engine.LValue
	Table = engine.LTable
)

// State adapts the same benchmark operations to stock GopherLua.
type State struct {
	state *engine.LState
}

func NewState(options Options) (*State, error) {
	runtime := engine.NewState(engine.Options{
		RegistrySize:     20 * 1024,
		RegistryMaxSize:  1024 * 1024,
		RegistryGrowStep: 4096,
	})
	if options.FixedUnixTime != 0 {
		table, ok := runtime.GetGlobal("os").(*engine.LTable)
		if !ok {
			runtime.Close()
			return nil, fmt.Errorf("os library is unavailable")
		}
		table.RawSetString("time", runtime.NewFunction(func(L *engine.LState) int {
			L.Push(engine.LNumber(options.FixedUnixTime))
			return 1
		}))
	}
	return &State{state: runtime}, nil
}

func (state *State) Close() error {
	state.state.Close()
	return nil
}

func (state *State) RuntimeVersion() string {
	return "GopherLua v1.1.2 (" + engine.LuaVersion + ")"
}

func (state *State) ConfigureExecution(guarded bool, interval int) error {
	if interval != 0 {
		return fmt.Errorf("stock GopherLua does not support configurable polling intervals")
	}
	if guarded {
		state.state.SetContext(context.Background())
	}
	return nil
}

func (state *State) String(text string) Value {
	return engine.LString(text)
}

func (state *State) NewTable(arrayHint, recordHint int) (*Table, error) {
	return state.state.CreateTable(arrayHint, recordHint), nil
}

func (state *State) Global(name string) (Value, error) {
	return state.state.GetGlobal(name), nil
}

func (state *State) SetGlobal(name string, value Value) error {
	state.state.SetGlobal(name, value)
	return nil
}

func (state *State) PrependPackagePath(root string) error {
	table, ok := state.state.GetGlobal("package").(*engine.LTable)
	if !ok {
		return fmt.Errorf("package library is unavailable")
	}
	current := state.state.GetField(table, "path").String()
	state.state.SetField(
		table,
		"path",
		engine.LString(filepath.Join(root, "?.lua")+";"+current),
	)
	return nil
}

func (state *State) DoString(_ string, source string) error {
	return state.state.DoString(source)
}

func (state *State) DoFile(path string) error {
	return state.state.DoFile(path)
}

func (state *State) NewLogFunction(verbose bool) (Value, error) {
	return state.state.NewFunction(func(L *engine.LState) int {
		if verbose {
			for index := 1; index <= L.GetTop(); index++ {
				fmt.Println("lua:", L.ToString(index))
			}
		}
		return 0
	}), nil
}

func (state *State) CallOne(function Value, argument Value) (Value, error) {
	if err := state.state.CallByParam(
		engine.P{Fn: function, NRet: 1, Protect: true},
		argument,
	); err != nil {
		return engine.LNil, err
	}
	result := state.state.Get(-1)
	state.state.Pop(1)
	return result, nil
}

func (state *State) CallGlobalBool(name string) error {
	function := state.state.GetGlobal(name)
	if function.Type() != engine.LTFunction {
		return fmt.Errorf("global %s is %s, not a function", name, function.Type())
	}
	if err := state.state.CallByParam(
		engine.P{Fn: function, NRet: 1, Protect: true},
	); err != nil {
		return err
	}
	result := state.state.Get(-1)
	state.state.Pop(1)
	value, ok := result.(engine.LBool)
	if !ok || !bool(value) {
		return fmt.Errorf("%s returned %s", name, result)
	}
	return nil
}

func (state *State) ForEach(
	table *Table,
	visit func(Value, Value) error,
) error {
	var visitError error
	table.ForEach(func(key, value engine.LValue) {
		if visitError == nil {
			visitError = visit(key, value)
		}
	})
	return visitError
}

func Nil() Value {
	return engine.LNil
}

func Bool(value bool) Value {
	return engine.LBool(value)
}

func Number(value float64) Value {
	return engine.LNumber(value)
}

func TableValue(table *Table) Value {
	return table
}

func ValueKind(value Value) Kind {
	switch value.(type) {
	case *engine.LNilType:
		return NilKind
	case engine.LBool:
		return BoolKind
	case engine.LNumber:
		return NumberKind
	case engine.LString:
		return StringKind
	case *engine.LFunction:
		return FunctionKind
	case *engine.LUserData:
		return UserDataKind
	case *engine.LState:
		return ThreadKind
	case *engine.LTable:
		return TableKind
	default:
		return InvalidKind
	}
}

func ValueTypeName(value Value) string {
	return value.Type().String()
}

func ValueBool(value Value) (bool, bool) {
	result, ok := value.(engine.LBool)
	return bool(result), ok
}

func ValueNumber(value Value) (float64, bool) {
	result, ok := value.(engine.LNumber)
	return float64(result), ok
}

func ValueString(value Value) (string, bool) {
	result, ok := value.(engine.LString)
	return string(result), ok
}

func ValueTable(value Value) (*Table, bool) {
	result, ok := value.(*engine.LTable)
	return result, ok
}

func TableRawGetString(table *Table, key string) Value {
	return table.RawGetString(key)
}

func TableRawSetString(table *Table, key string, value Value) error {
	table.RawSetString(key, value)
	return nil
}

func TableRawSetInt(table *Table, key int, value Value) error {
	table.RawSetInt(key, value)
	return nil
}

func TableLen(table *Table) int {
	return table.Len()
}
