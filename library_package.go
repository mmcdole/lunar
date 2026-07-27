package lua

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

const (
	loadedModulesRegistryKey = "_LOADED"

	defaultLuaPathUnix = "./?.lua;" +
		"/usr/local/share/lua/5.1/?.lua;" +
		"/usr/local/share/lua/5.1/?/init.lua;" +
		"/usr/local/lib/lua/5.1/?.lua;" +
		"/usr/local/lib/lua/5.1/?/init.lua"
	defaultLuaCPathUnix = "./?.so;" +
		"/usr/local/lib/lua/5.1/?.so;" +
		"/usr/local/lib/lua/5.1/loadall.so"

	defaultLuaPathWindows = `.\?.lua;!\lua\?.lua;!\lua\?\init.lua;` +
		`!\?.lua;!\?\init.lua`
	defaultLuaCPathWindows = `.\?.dll;!\?.dll;!\loadall.dll`

	dynamicLibrariesUnavailable = "dynamic libraries not enabled; check your Lua installation"
)

// OpenPackage installs Lua 5.1's package table plus the global require and
// module functions.
//
// Lua modules load through the same bounded source and binary pipeline as
// LoadFile. Native C modules are deliberately unavailable in this pure-Go
// runtime; package.loadlib and the C searchers expose Lua 5.1's standard
// "dynamic libraries absent" fallback.
//
// Opening is explicit and idempotent in effect. Each call installs fresh
// package, preload, loader, and Function objects while preserving the
// registry-backed package.loaded table and its cached modules.
func (state *State) OpenPackage() error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	path, cpath, err := initialPackagePaths()
	if err != nil {
		return err
	}
	loaded, err := state.ensureLoadedModules()
	if err != nil {
		return err
	}
	library := newTable(state, 0, 8)
	preload := newTable(state, 0, 0)
	loaders := newTable(state, 4, 0)
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
	cLoader, err := state.newPackageFunction(
		library,
		packageCLoader,
	)
	if err != nil {
		return err
	}
	cRootLoader, err := state.newPackageFunction(
		library,
		packageCRootLoader,
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
		cLoader,
		cRootLoader,
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
		{name: "path", value: stringSlot(state.runtime.strings.make(path))},
		{name: "cpath", value: stringSlot(state.runtime.strings.make(cpath))},
		{
			name: "config",
			value: stringSlot(state.runtime.strings.make(
				string(os.PathSeparator) + "\n;\n?\n!\n-",
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

func initialPackagePaths() (string, string, error) {
	defaultPath := defaultLuaPathUnix
	defaultCPath := defaultLuaCPathUnix
	if runtime.GOOS == "windows" {
		defaultPath = defaultLuaPathWindows
		defaultCPath = defaultLuaCPathWindows
	}
	path := packageEnvironmentPath("LUA_PATH", defaultPath)
	cpath := packageEnvironmentPath("LUA_CPATH", defaultCPath)
	if runtime.GOOS != "windows" {
		return path, cpath, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", "", fmt.Errorf(
			"lua: resolve executable directory: %w",
			err,
		)
	}
	directory := executable
	if separator := strings.LastIndexAny(directory, `\/`); separator >= 0 {
		directory = directory[:separator]
	} else {
		return "", "", fmt.Errorf(
			"lua: executable path has no directory separator",
		)
	}
	return strings.ReplaceAll(path, "!", directory),
		strings.ReplaceAll(cpath, "!", directory),
		nil
}

func packageEnvironmentPath(name, fallback string) string {
	path, present := os.LookupEnv(name)
	if !present {
		return fallback
	}
	return strings.ReplaceAll(path, ";;", ";"+fallback+";")
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
	file, filename, diagnostics, failure := packageFindFile(
		frame,
		name,
		"path",
		&control,
	)
	if failure != nil {
		return frame.sealError(failure)
	}
	if file == nil {
		return frame.ReturnString(diagnostics)
	}
	defer file.Close()

	prototype, err := loadFileReaderPrototype(
		"@"+filename,
		filename,
		file,
		&control,
	)
	if err != nil {
		if luaFailure, ok := err.(*Error); ok {
			switch luaFailure.category {
			case ContextError, ResourceError:
				return frame.sealError(luaFailure)
			}
		}
		return libraryError(
			frame,
			"error loading module '%s' from file '%s':\n\t%s",
			name,
			filename,
			err.Error(),
		)
	}
	function := frame.thread.state.loadPrototypeObject(prototype)
	return frame.returnOne(
		frame.activation(),
		slotFromFunctionObject(function),
	)
}

func packageCLoader(frame Frame) Outcome {
	name, _, outcome, failed := packageNameArgument(frame, 0)
	if failed {
		return outcome
	}
	control, outcome, failed := frameLoadControl(frame)
	if failed {
		return outcome
	}
	file, filename, diagnostics, failure := packageFindFile(
		frame,
		name,
		"cpath",
		&control,
	)
	if failure != nil {
		return frame.sealError(failure)
	}
	if file == nil {
		return frame.ReturnString(diagnostics)
	}
	_ = file.Close()
	return packageDynamicLoadError(frame, name, filename)
}

func packageCRootLoader(frame Frame) Outcome {
	name, _, outcome, failed := packageNameArgument(frame, 0)
	if failed {
		return outcome
	}
	dot := strings.IndexByte(name, '.')
	if dot < 0 {
		return frame.Return()
	}
	control, outcome, failed := frameLoadControl(frame)
	if failed {
		return outcome
	}
	file, filename, diagnostics, failure := packageFindFile(
		frame,
		name[:dot],
		"cpath",
		&control,
	)
	if failure != nil {
		return frame.sealError(failure)
	}
	if file == nil {
		return frame.ReturnString(diagnostics)
	}
	_ = file.Close()
	return packageDynamicLoadError(frame, name, filename)
}

func packageDynamicLoadError(
	frame Frame,
	name string,
	filename string,
) Outcome {
	return libraryError(
		frame,
		"error loading module '%s' from file '%s':\n\t%s",
		name,
		filename,
		dynamicLibrariesUnavailable,
	)
}

func packageFindFile(
	frame Frame,
	name string,
	field string,
	control *loadControl,
) (*os.File, string, string, *Error) {
	pathValue, failure := frame.indexCompact(
		slotFromTableObject(frame.environmentObject()),
		stringSlot(frame.thread.owner.strings.make(field)),
	)
	if failure != nil {
		return nil, "", "", failure
	}
	path, ok := compactText(pathValue)
	if !ok {
		return nil, "", "", libraryFailure(
			frame,
			"'package.%s' must be a string",
			field,
		)
	}
	path = luaCString(path)
	mappedName := strings.ReplaceAll(
		name,
		".",
		string(os.PathSeparator),
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
		file, err := os.Open(filename)
		if err == nil {
			return file, filename, "", nil
		}
		diagnostics.WriteString("\n\tno file '")
		diagnostics.WriteString(filename)
		diagnostics.WriteByte('\'')
		cursor = end
	}
	return nil, "", diagnostics.String(), nil
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
