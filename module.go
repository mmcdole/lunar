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
// Loader and captures follow NewNativeFunction's validation and environment
// rules. The module name is interpreted like Lua 5.1 require and therefore
// ends at its first NUL byte.
func (state *State) PreloadModule(
	name string,
	loader NativeFunc,
	captures ...Value,
) error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	if loader == nil {
		return ErrInvalidNativeFunction
	}
	compact, err := state.compactNativeCaptures(captures)
	if err != nil {
		return err
	}
	function := newNativeFunctionOwned(
		state,
		state.constructionEnvironment(),
		loader,
		compact,
	)
	return state.ensureModulePreloads().rawSetStringSlot(
		luaCString(name),
		slotFromFunctionObject(function),
	)
}

// SetFunctions installs native functions into table.
//
// Each function receives its own copy of captures. SetFunctions validates the
// State, table, every function, and every capture before changing table, so a
// validation failure never leaves a partial installation. Existing fields
// with matching names are replaced.
func (state *State) SetFunctions(
	table *Table,
	functions map[string]NativeFunc,
	captures ...Value,
) error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	target, err := state.acceptTable(table)
	if err != nil {
		return err
	}
	if len(captures) > maxNativeCaptures {
		return ErrNativeCaptureLimit
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
	for _, capture := range captures {
		if err := state.runtime.accept(capture); err != nil {
			return err
		}
	}
	if len(names) == 0 {
		runtime.KeepAlive(table)
		return nil
	}

	compact, err := state.compactNativeCaptures(captures)
	if err != nil {
		return err
	}
	environment := state.constructionEnvironment()
	created := make([]*functionObject, len(names))
	for index, name := range names {
		created[index] = newNativeFunctionOwned(
			state,
			environment,
			functions[name],
			slices.Clone(compact),
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
