package lua

import (
	"context"
	"io"
	"testing"
)

// Helpers for tests written against accessors that are no longer public.
// Tests live in the package, so they read the compact representation
// directly rather than keeping a shipped accessor alive for their sake.

func rawInt(table *Table, key int) Value { return table.RawGetInt(key) }

func rawStr(table *Table, key string) Value { return table.RawGetString(key) }

func rawLen(table *Table) int { return table.RawLen() }

func registerCount(prototype *Prototype) int {
	return int(prototype.registers)
}

func parameterCount(prototype *Prototype) int {
	return int(prototype.parameters)
}

func childCount(prototype *Prototype) int {
	return len(prototype.children)
}

func isVararg(prototype *Prototype) bool {
	return prototype != nil && prototype.varargFlags&varargIsVararg != 0
}

func upvalueCount(target any) int {
	switch typed := target.(type) {
	case *Prototype:
		if typed == nil {
			return 0
		}
		return int(typed.upvalues)
	case *Function:
		object := typed.runtimeObject()
		if object == nil {
			return 0
		}
		if object.prototype != nil {
			return int(object.prototype.upvalues)
		}
		if native := object.nativeBody(); native != nil {
			return len(native.captures)
		}
		return 0
	default:
		panic("lua: unsupported upvalueCount target")
	}
}

// Lua 5.1 thread and userdata environments are no longer part of the public
// surface. Tests still exercise the runtime behaviour through these.

func threadEnvironment(thread *Thread) (*Table, error) {
	object := thread.runtimeObject()
	if object == nil || object.owner == nil {
		return nil, ErrInvalidValue
	}
	if object.owner.closed.Load() {
		return nil, ErrClosed
	}
	return object.globals.owningHandle(), nil
}

func setThreadEnvironment(thread *Thread, environment *Table) error {
	object := thread.runtimeObject()
	if object == nil || object.owner == nil {
		return ErrInvalidValue
	}
	if object.owner.closed.Load() {
		return ErrClosed
	}
	target := environment.runtimeObject()
	if target == nil {
		return ErrInvalidValue
	}
	if target.owner != object.owner {
		return ErrForeignValue
	}
	object.globals = target
	return nil
}

func userDataEnvironment(data *UserData) (*Table, error) {
	object := data.runtimeObject()
	if object == nil || object.owner == nil {
		return nil, ErrInvalidValue
	}
	if object.owner.closed.Load() {
		return nil, ErrClosed
	}
	return object.environment.owningHandle(), nil
}

func setUserDataEnvironment(data *UserData, environment *Table) error {
	object := data.runtimeObject()
	if object == nil || object.owner == nil {
		return ErrInvalidValue
	}
	if object.owner.closed.Load() {
		return ErrClosed
	}
	if environment == nil {
		object.environment = nil
		return nil
	}
	target := environment.runtimeObject()
	if target == nil || target.owner != object.owner {
		return ErrForeignValue
	}
	object.environment = target
	return nil
}

// callCtx installs ctx for one call, matching what the removed CallContext
// did. Tests that need the same shape for other operations follow this form.
func callCtx(
	t testing.TB,
	state *State,
	ctx context.Context,
	callable Value,
	arguments ...Value,
) ([]Value, error) {
	t.Helper()
	if err := state.SetContext(ctx); err != nil {
		return nil, err
	}
	defer func() { _ = state.RemoveContext() }()
	return state.Call(callable, arguments...)
}

func loadCtx(
	t testing.TB,
	state *State,
	ctx context.Context,
	sourceName string,
	reader io.Reader,
) (*Function, error) {
	t.Helper()
	if err := state.SetContext(ctx); err != nil {
		return nil, err
	}
	defer func() { _ = state.RemoveContext() }()
	return state.Load(sourceName, reader)
}

func loadStringCtx(
	t testing.TB,
	state *State,
	ctx context.Context,
	sourceName string,
	source string,
) (*Function, error) {
	t.Helper()
	if err := state.SetContext(ctx); err != nil {
		return nil, err
	}
	defer func() { _ = state.RemoveContext() }()
	return state.LoadString(sourceName, source)
}

func callIntoCtx(
	t testing.TB,
	state *State,
	ctx context.Context,
	callable Value,
	arguments []Value,
	destination []Value,
) (int, error) {
	t.Helper()
	if err := state.SetContext(ctx); err != nil {
		return 0, err
	}
	defer func() { _ = state.RemoveContext() }()
	return state.CallInto(callable, arguments, destination)
}

func indexCtx(
	t testing.TB,
	state *State,
	ctx context.Context,
	target Value,
	key Value,
) (Value, error) {
	t.Helper()
	if err := state.SetContext(ctx); err != nil {
		return Value{}, err
	}
	defer func() { _ = state.RemoveContext() }()
	return state.Index(target, key)
}

func loadFileCtx(
	t testing.TB,
	state *State,
	ctx context.Context,
	path string,
) (*Function, error) {
	t.Helper()
	if err := state.SetContext(ctx); err != nil {
		return nil, err
	}
	defer func() { _ = state.RemoveContext() }()
	return state.LoadFile(path)
}
