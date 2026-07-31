package lua

import (
	"fmt"
	"runtime"
	"slices"
)

// PreloadModule registers a native loader in the State-owned package.preload
// table.
//
// Registration works before or after OpenPackage. Every OpenPackage call
// publishes the same preload table, so registrations survive reopening.
// Require still caches successful loads in package.loaded.
//
// The loader follows NewNativeFunction's validation and environment rules.
// The module name is interpreted like Lua 5.1 require and therefore
// ends at its first NUL byte.
func (state *State) PreloadModule(
	name string,
	loader NativeFunc,
) error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	if loader == nil {
		return ErrInvalidNativeFunction
	}
	function := newNativeFunctionOwned(
		state,
		state.constructionEnvironment(),
		loader,
		nil,
	)
	return state.ensureModulePreloads().rawSetStringSlot(
		luaCString(name),
		slotFromFunctionObject(function),
	)
}

// SetFunctions installs native functions into table.
//
// SetFunctions validates the State, table, and every function before changing
// table, so a validation failure never leaves a partial installation. Existing
// fields with matching names are replaced.
func (state *State) SetFunctions(
	table *Table,
	functions map[string]NativeFunc,
) error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	target, err := state.acceptTable(table)
	if err != nil {
		return err
	}

	names := make([]string, 0, len(functions))
	for name, entry := range functions {
		if entry == nil {
			return fmt.Errorf(
				"lua: function %q: %w",
				name,
				ErrInvalidNativeFunction,
			)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	if len(names) == 0 {
		runtime.KeepAlive(table)
		return nil
	}

	environment := state.constructionEnvironment()
	created := make([]*functionObject, len(names))
	for index, name := range names {
		created[index] = newNativeFunctionOwned(
			state,
			environment,
			functions[name],
			nil,
		)
	}
	for index, name := range names {
		if err := target.rawSetStringSlot(
			name,
			slotFromFunctionObject(created[index]),
		); err != nil {
			panic("lua: validated SetFunctions installation failed")
		}
	}
	runtime.KeepAlive(table)
	return nil
}
