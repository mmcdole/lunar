package lua

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
)

const (
	loadedModulesRegistryKey = "_LOADED"

	dynamicLibrariesUnavailable = "dynamic libraries not enabled; check your Lua installation"
)

// OpenPackage installs Lua 5.1's package table plus the global require and
// module functions.
//
// Lua modules load through the State's ScriptLoader and the same bounded
// source and binary pipeline as LoadFile. package.loaders contains the preload
// and Lua-source searchers. Native C modules are deliberately unavailable in
// this pure-Go runtime, so package.cpath is empty and package.loadlib reports
// that dynamic libraries are unavailable.
//
// Each call installs fresh package, loader, and Function objects while
// preserving the State-owned package.preload table, the registry-backed
// package.loaded table, and its cached modules.
func (state *State) OpenPackage() error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	loaded, err := state.ensureLoadedModules()
	if err != nil {
		return err
	}
	library := newTable(state, 0, 8)
	preload := state.ensureModulePreloads()
	loaders := newTable(state, 2, 0)
	sentinel := state.ensurePackageSentinel()

	loadlib, err := state.newPackageFunction(
		library,
		packageLoadLib,
	)
	if err != nil {
		return err
	}
	seeall, err := state.newPackageFunction(
		library,
		packageSeeAll,
	)
	if err != nil {
		return err
	}
	preloadLoader, err := state.newPackageFunction(
		library,
		packagePreloadLoader,
	)
	if err != nil {
		return err
	}
	luaLoader, err := state.newPackageFunction(
		library,
		packageLuaLoader,
	)
	if err != nil {
		return err
	}
	require, err := state.newPackageFunction(
		library,
		packageRequire,
		slotFromUserDataObject(sentinel),
	)
	if err != nil {
		return err
	}
	module, err := state.newPackageFunction(
		library,
		packageModule,
	)
	if err != nil {
		return err
	}

	for index, loader := range [...]*functionObject{
		preloadLoader,
		luaLoader,
	} {
		loaders.rawSetIntegerSlot(index+1, slotFromFunctionObject(loader))
	}
	for _, field := range []struct {
		name  string
		value slot
	}{
		{name: "loadlib", value: slotFromFunctionObject(loadlib)},
		{name: "seeall", value: slotFromFunctionObject(seeall)},
		{name: "loaders", value: slotFromTableObject(loaders)},
		{
			name: "path",
			value: stringSlot(
				state.runtime.strings.make(state.scriptLoader.packagePath),
			),
		},
		{name: "cpath", value: stringSlot(state.runtime.strings.make(""))},
		{
			name: "config",
			value: stringSlot(state.runtime.strings.make(
				state.scriptLoader.separator + "\n;\n?\n!\n-",
			)),
		},
		{name: "loaded", value: slotFromTableObject(loaded)},
		{name: "preload", value: slotFromTableObject(preload)},
	} {
		if err := library.rawSetStringSlot(field.name, field.value); err != nil {
			return err
		}
	}

	globals := state.globalEnvironment()
	if err := globals.rawSetStringSlot(
		"package",
		slotFromTableObject(library),
	); err != nil {
		return err
	}
	if err := globals.rawSetStringSlot(
		"require",
		slotFromFunctionObject(require),
	); err != nil {
		return err
	}
	if err := globals.rawSetStringSlot(
		"module",
		slotFromFunctionObject(module),
	); err != nil {
		return err
	}
	state.setLoadedModule(loaded, "package", slotFromTableObject(library))
	return nil
}

func (state *State) ensureModulePreloads() *tableObject {
	if state.modulePreloads == nil {
		state.modulePreloads = newTable(state, 0, 0)
	}
	return state.modulePreloads
}

func (state *State) newPackageFunction(
	environment *tableObject,
	entry NativeFunc,
	captures ...slot,
) (*functionObject, error) {
	function, err := state.newNativeFunctionObject(entry, captures)
	if err != nil {
		return nil, err
	}
	function.environment = environment
	return function, nil
}

func (state *State) ensureLoadedModules() (*tableObject, error) {
	key := stringSlot(state.runtime.strings.make(loadedModulesRegistryKey))
	if value, found := state.registry.rawSlot(key); found {
		if !value.isTable() {
			return nil, fmt.Errorf(
				"lua: registry %s must be a table",
				loadedModulesRegistryKey,
			)
		}
		return (*tableObject)(value.ref), nil
	}
	loaded := newTable(state, 0, 8)
	state.registry.rawSetSlot(key, slotFromTableObject(loaded))
	return loaded, nil
}

func (state *State) ensurePackageSentinel() *userDataObject {
	if state.packageSentinel != nil {
		return state.packageSentinel
	}
	// PUC uses one static light-userdata address. A State-owned userdata gives
	// Lua the same stable, exact identity across package reopenings without
	// deriving identity from its host-mutable payload. It deliberately has no
	// environment, so retaining the marker cannot retain the global graph.
	object := newUserDataObject(state, nil, nil, nil)
	state.packageSentinel = object
	return object
}

func (state *State) setLoadedModule(
	loaded *tableObject,
	name string,
	value slot,
) {
	if loaded == nil || loaded.owner != state.runtime {
		panic("lua: invalid loaded-module table")
	}
	status := loaded.rawSetSlot(
		stringSlot(state.runtime.strings.make(name)),
		value,
	)
	if status != tableKeyValid {
		panic("lua: string module name produced an invalid table key")
	}
}

func packageNameArgument(
	frame Frame,
	index int,
) (string, slot, Outcome, bool) {
	value, present := frame.argument(index)
	if !present {
		return "", nilSlot,
			baseArgumentTypeError(frame, index, "string"),
			true
	}
	text, ok := compactText(value)
	if !ok {
		return "", nilSlot,
			baseArgumentTypeError(frame, index, "string"),
			true
	}
	text = luaCString(text)
	if value.isString() &&
		len(text) == stringSlotLen(value) {
		return text, value, Outcome{}, false
	}
	key := stringSlot(frame.thread.owner.strings.make(text))
	return text, key, Outcome{}, false
}

func packageRequire(frame Frame) Outcome {
	name, nameKey, outcome, failed := packageNameArgument(frame, 0)
	if failed {
		return outcome
	}
	frame.discardArgumentsAfter(1)
	sentinel := frame.nativeCapture(0)

	loaded, failure := packageLoadedTarget(frame)
	if failure != nil {
		return frame.sealError(failure)
	}
	cached, failure := frame.indexCompact(loaded, nameKey)
	if failure != nil {
		return frame.sealError(failure)
	}
	if truthySlot(cached) {
		if isPackageLoadSentinel(cached, sentinel) {
			return libraryError(
				frame,
				"loop or previous error loading module '%s'",
				name,
			)
		}
		return frame.returnOne(frame.activation(), cached)
	}

	packageTable := slotFromTableObject(frame.environmentObject())
	loaders, failure := frame.indexCompact(
		packageTable,
		stringSlot(frame.thread.owner.strings.make("loaders")),
	)
	if failure != nil {
		return frame.sealError(failure)
	}
	if !loaders.isTable() {
		return libraryError(frame, "'package.loaders' must be a table")
	}

	var (
		diagnostics strings.Builder
		module      slot
	)
	for index := 1; ; index++ {
		loader, found := (*tableObject)(loaders.ref).rawIntSlot(index)
		if !found || loader.isNil() {
			return libraryError(
				frame,
				"module '%s' not found:%s",
				name,
				diagnostics.String(),
			)
		}
		arguments := [1]slot{nameKey}
		result, callFailure := frame.callCompactOne(
			loader,
			arguments[:],
		)
		if callFailure != nil {
			return frame.sealError(callFailure)
		}
		if result.isFunction() {
			module = result
			break
		}
		if text, ok := compactText(result); ok {
			diagnostics.WriteString(text)
		}
	}

	if failure := frame.setIndexCompact(
		loaded,
		nameKey,
		sentinel,
	); failure != nil {
		return frame.sealError(failure)
	}
	arguments := [1]slot{nameKey}
	result, failure := frame.callCompactOne(module, arguments[:])
	if failure != nil {
		return frame.sealError(failure)
	}
	if !result.isNil() {
		if failure := frame.setIndexCompact(
			loaded,
			nameKey,
			result,
		); failure != nil {
			return frame.sealError(failure)
		}
	}
	result, failure = frame.indexCompact(loaded, nameKey)
	if failure != nil {
		return frame.sealError(failure)
	}
	if isPackageLoadSentinel(result, sentinel) {
		result = trueSlot
		if failure := frame.setIndexCompact(
			loaded,
			nameKey,
			result,
		); failure != nil {
			return frame.sealError(failure)
		}
	}
	return frame.returnOne(frame.activation(), result)
}

func packageLoadedTarget(frame Frame) (slot, *Error) {
	return frame.indexCompact(
		slotFromTableObject(frame.thread.state.registry),
		stringSlot(
			frame.thread.owner.strings.make(loadedModulesRegistryKey),
		),
	)
}

func isPackageLoadSentinel(value, sentinel slot) bool {
	return value.isUserData() &&
		sentinel.isUserData() &&
		value.ref == sentinel.ref
}

func packagePreloadLoader(frame Frame) Outcome {
	name, nameKey, outcome, failed := packageNameArgument(frame, 0)
	if failed {
		return outcome
	}
	preload, failure := frame.indexCompact(
		slotFromTableObject(frame.environmentObject()),
		stringSlot(frame.thread.owner.strings.make("preload")),
	)
	if failure != nil {
		return frame.sealError(failure)
	}
	if !preload.isTable() {
		return libraryError(frame, "'package.preload' must be a table")
	}
	loader, failure := frame.indexCompact(preload, nameKey)
	if failure != nil {
		return frame.sealError(failure)
	}
	if !loader.isNil() {
		return frame.returnOne(frame.activation(), loader)
	}
	return frame.ReturnString(
		fmt.Sprintf("\n\tno field package.preload['%s']", name),
	)
}

func packageLuaLoader(frame Frame) Outcome {
	name, _, outcome, failed := packageNameArgument(frame, 0)
	if failed {
		return outcome
	}
	control, outcome, failed := frameLoadControl(frame)
	if failed {
		return outcome
	}
	reader, filename, diagnostics, failure := packageFindSource(
		frame,
		name,
		&control,
	)
	if failure != nil {
		if luaFailure, ok := failure.(*Error); ok {
			return frame.sealError(luaFailure)
		}
		sourceFailure := libraryFailure(frame, "%s", failure.Error())
		sourceFailure.cause = failure
		return frame.sealError(sourceFailure)
	}
	if reader == nil {
		return frame.ReturnString(diagnostics)
	}
	defer reader.Close()

	prototype, err := loadFileReaderPrototype(
		"@"+filename,
		filename,
		reader,
		&control,
	)
	if err != nil {
		if luaFailure, ok := err.(*Error); ok {
			switch luaFailure.category {
			case ContextError, ResourceError:
				return frame.sealError(luaFailure)
			}
		}
		loadFailure := libraryFailure(
			frame,
			"error loading module '%s' from file '%s':\n\t%s",
			name,
			filename,
			err.Error(),
		)
		loadFailure.cause = err
		return frame.sealError(loadFailure)
	}
	function := frame.thread.state.loadPrototypeObject(prototype)
	return frame.returnOne(
		frame.activation(),
		slotFromFunctionObject(function),
	)
}

func packageFindSource(
	frame Frame,
	name string,
	control *loadControl,
) (io.ReadCloser, string, string, error) {
	pathValue, failure := frame.indexCompact(
		slotFromTableObject(frame.environmentObject()),
		stringSlot(frame.thread.owner.strings.make("path")),
	)
	if failure != nil {
		return nil, "", "", failure
	}
	path, ok := compactText(pathValue)
	if !ok {
		return nil, "", "", libraryFailure(
			frame,
			"'package.path' must be a string",
		)
	}
	path = luaCString(path)
	mappedName := packageModuleName(
		name,
		frame.thread.state.scriptLoader.separator,
	)

	var diagnostics strings.Builder
	for cursor := 0; cursor < len(path); {
		if failure := control.check(); failure != nil {
			return nil, "", "", failure
		}
		for cursor < len(path) && path[cursor] == ';' {
			cursor++
		}
		if cursor == len(path) {
			break
		}
		end := strings.IndexByte(path[cursor:], ';')
		if end < 0 {
			end = len(path)
		} else {
			end += cursor
		}
		template := path[cursor:end]
		filename := strings.ReplaceAll(template, "?", mappedName)
		reader, err := frame.thread.state.scriptLoader.open(
			frame.Context(),
			filename,
			control,
		)
		if err == nil {
			return reader, filename, "", nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, "", "", &fileLoadError{
				operation: "open",
				name:      filename,
				cause:     err,
			}
		}
		diagnostics.WriteString("\n\tno file '")
		diagnostics.WriteString(filename)
		diagnostics.WriteByte('\'')
		cursor = end
	}
	return nil, "", diagnostics.String(), nil
}

func packageModuleName(name, separator string) string {
	return strings.ReplaceAll(name, ".", separator)
}

func packageLoadLib(frame Frame) Outcome {
	_, _, outcome, failed := packageNameArgument(frame, 0)
	if failed {
		return outcome
	}
	_, _, outcome, failed = packageNameArgument(frame, 1)
	if failed {
		return outcome
	}
	frame.discardArgumentsAfter(2)
	last := [1]slot{
		stringSlot(frame.thread.owner.strings.make("absent")),
	}
	return frame.returnCompactValues(
		[2]slot{
			nilSlot,
			stringSlot(
				frame.thread.owner.strings.make(
					dynamicLibrariesUnavailable,
				),
			),
		},
		2,
		last[:],
	)
}

func packageModule(frame Frame) Outcome {
	name, nameKey, outcome, failed := packageNameArgument(frame, 0)
	if failed {
		return outcome
	}
	loaded, failure := packageLoadedTarget(frame)
	if failure != nil {
		return frame.sealError(failure)
	}
	module, failure := frame.indexCompact(loaded, nameKey)
	if failure != nil {
		return frame.sealError(failure)
	}
	if !module.isTable() {
		var conflict bool
		module, conflict, failure = packageModuleTable(frame, name)
		if failure != nil {
			return frame.sealError(failure)
		}
		if conflict {
			return libraryError(
				frame,
				"name conflict for module '%s'",
				name,
			)
		}
		if failure := frame.setIndexCompact(
			loaded,
			nameKey,
			module,
		); failure != nil {
			return frame.sealError(failure)
		}
	}

	nameField := stringSlot(frame.thread.owner.strings.make("_NAME"))
	initialized, failure := frame.indexCompact(module, nameField)
	if failure != nil {
		return frame.sealError(failure)
	}
	if initialized.isNil() {
		lastDot := strings.LastIndexByte(name, '.')
		packageName := ""
		if lastDot >= 0 {
			packageName = name[:lastDot+1]
		}
		for _, field := range [...]struct {
			name  string
			value slot
		}{
			{name: "_M", value: module},
			{name: "_NAME", value: nameKey},
			{
				name: "_PACKAGE",
				value: stringSlot(
					frame.thread.owner.strings.make(packageName),
				),
			},
		} {
			if failure := frame.setIndexCompact(
				module,
				stringSlot(
					frame.thread.owner.strings.make(field.name),
				),
				field.value,
			); failure != nil {
				return frame.sealError(failure)
			}
		}
	}

	// A native tail-call target is pushed above its Lua caller; only Lua-to-Lua
	// tail calls replace an activation. The immediate caller is therefore
	// still physical here even when its source instruction was TAILCALL.
	caller, status := frame.thread.logicalFrame(1)
	if status != logicalFramePhysical ||
		caller.function == nil ||
		caller.function.prototype == nil {
		return libraryError(
			frame,
			"'module' not called from a Lua function",
		)
	}
	caller.function.environment = (*tableObject)(module.ref)

	arguments := [1]slot{module}
	for index := 1; index < frame.ArgumentCount(); index++ {
		option, _ := frame.argument(index)
		if failure := frame.callCompactNone(
			option,
			arguments[:],
		); failure != nil {
			return frame.sealError(failure)
		}
	}
	return frame.Return()
}

func packageModuleTable(
	frame Frame,
	name string,
) (slot, bool, *Error) {
	current := slotFromTableObject(frame.thread.globals)
	cursor := 0
	for {
		end := strings.IndexByte(name[cursor:], '.')
		if end < 0 {
			end = len(name)
		} else {
			end += cursor
		}
		key := stringSlot(
			frame.thread.owner.strings.make(name[cursor:end]),
		)
		table := (*tableObject)(current.ref)
		next, found := table.rawSlot(key)
		if !found || next.isNil() {
			next = slotFromTableObject(newTable(frame.thread.state, 0, 1))
			if failure := frame.setIndexCompact(
				current,
				key,
				next,
			); failure != nil {
				return nilSlot, false, failure
			}
		} else if !next.isTable() {
			return nilSlot, true, nil
		}
		current = next
		if end == len(name) {
			return current, false, nil
		}
		cursor = end + 1
	}
}

func packageSeeAll(frame Frame) Outcome {
	module, ok := frame.tableObject(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	metatable := module.metatable
	if metatable == nil {
		metatable = newTable(frame.thread.state, 0, 1)
		module.metatable = metatable
	}
	if failure := frame.setIndexCompact(
		slotFromTableObject(metatable),
		stringSlot(frame.thread.owner.strings.make("__index")),
		slotFromTableObject(frame.thread.globals),
	); failure != nil {
		return frame.sealError(failure)
	}
	return frame.Return()
}
