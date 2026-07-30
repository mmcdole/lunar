package lua

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStateWithPackage(t *testing.T, options Options) *State {
	t.Helper()
	state, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.OpenBase(); err != nil {
		state.Close()
		t.Fatal(err)
	}
	if err := state.OpenPackage(); err != nil {
		state.Close()
		t.Fatal(err)
	}
	return state
}

func TestPackageEnvironmentPathExpandsOnlyDoubleSeparators(t *testing.T) {
	t.Setenv("LUNAR_LUA_PATH_TEST", "before;;middle\x01after")
	if got := packageEnvironmentPath(
		"LUNAR_LUA_PATH_TEST",
		"default",
	); got != "before;default;middle\x01after" {
		t.Fatalf("expanded path = %q", got)
	}
	t.Setenv("LUNAR_LUA_PATH_TEST", "")
	if got := packageEnvironmentPath(
		"LUNAR_LUA_PATH_TEST",
		"default",
	); got != "" {
		t.Fatalf("explicit empty path = %q", got)
	}
}

func TestOpenPackageInstallsCanonicalLua51Surface(t *testing.T) {
	t.Setenv("LUA_PATH", "before;;after")
	t.Setenv("LUA_CPATH", "")

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	// Named libraries populate _LOADED even when package has not opened yet.
	if err := state.OpenMath(); err != nil {
		t.Fatal(err)
	}
	registryLoadedValue := state.registry.rawGetStringValue(
		loadedModulesRegistryKey,
	)
	registryLoaded, ok := registryLoadedValue.AsTable()
	if !ok {
		t.Fatalf("registry _LOADED = %v; want Table", registryLoadedValue)
	}
	mathValue, err := state.RawGlobal("math")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, registryLoaded.RawGetString("math"), mathValue)

	expectedPath, expectedCPath, err := initialPackagePaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := state.OpenPackage(); err != nil {
		t.Fatal(err)
	}
	packageValue, err := state.RawGlobal("package")
	if err != nil {
		t.Fatal(err)
	}
	library, ok := packageValue.AsTable()
	if !ok {
		t.Fatalf("package = %v; want Table", packageValue)
	}

	for _, name := range []string{"loadlib", "seeall"} {
		if value := library.RawGetString(name); value.Kind() != FunctionKind {
			t.Fatalf("package.%s = %v; want Function", name, value)
		}
	}
	loadersValue := library.RawGetString("loaders")
	loaders, ok := loadersValue.AsTable()
	if !ok {
		t.Fatalf("package.loaders = %v; want Table", loadersValue)
	}
	for index := 1; index <= 4; index++ {
		if value := loaders.RawGetInt(index); value.Kind() != FunctionKind {
			t.Fatalf("package.loaders[%d] = %v; want Function", index, value)
		}
	}
	assertTestValue(t, loaders.RawGetInt(5), Nil())
	if value := library.RawGetString("preload"); value.Kind() != TableKind {
		t.Fatalf("package.preload = %v; want Table", value)
	}
	assertTestValue(t, library.RawGetString("loaded"), registryLoaded.Value())
	assertTestValue(t, library.RawGetString("path"), state.String(expectedPath))
	assertTestValue(t, library.RawGetString("cpath"), state.String(expectedCPath))
	assertTestValue(
		t,
		library.RawGetString("config"),
		state.String(string(os.PathSeparator)+"\n;\n?\n!\n-"),
	)
	assertTestValue(t, registryLoaded.RawGetString("package"), packageValue)

	for _, name := range []string{"require", "module"} {
		value, globalErr := state.RawGlobal(name)
		if globalErr != nil {
			t.Fatal(globalErr)
		}
		if value.Kind() != FunctionKind {
			t.Fatalf("%s = %v; want Function", name, value)
		}
	}
	loadlib, err := state.RawGlobal("loadlib")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, loadlib, Nil())

	// Opening another named library updates the same loaded table.
	if err := state.OpenTable(); err != nil {
		t.Fatal(err)
	}
	tableValue, err := state.RawGlobal("table")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, registryLoaded.RawGetString("table"), tableValue)

	marker, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := registryLoaded.RawSetString("marker", marker.Value()); err != nil {
		t.Fatal(err)
	}
	oldPackage := library
	oldPreload, _ := library.RawGetString("preload").AsTable()
	oldLoaders := loaders
	oldRequire, _ := state.RawGlobal("require")

	if err := state.OpenPackage(); err != nil {
		t.Fatal(err)
	}
	newPackageValue, _ := state.RawGlobal("package")
	newPackage, _ := newPackageValue.AsTable()
	newPreload, _ := newPackage.RawGetString("preload").AsTable()
	newLoaders, _ := newPackage.RawGetString("loaders").AsTable()
	newRequire, _ := state.RawGlobal("require")
	if newPackage == oldPackage || newLoaders == oldLoaders {
		t.Fatal("OpenPackage reused a replaceable package object")
	}
	if newPreload != oldPreload {
		t.Fatal("OpenPackage replaced the State-owned preload table")
	}
	sameRequire, applicable := oldRequire.SameObject(newRequire)
	if !applicable || sameRequire {
		t.Fatal("OpenPackage reused the global require Function")
	}
	assertTestValue(t, newPackage.RawGetString("loaded"), registryLoaded.Value())
	assertTestValue(t, registryLoaded.RawGetString("marker"), marker.Value())
	assertTestValue(
		t,
		registryLoaded.RawGetString("package"),
		newPackage.Value(),
	)
	assertTestValue(t, oldPackage.RawGetString("loaded"), registryLoaded.Value())

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenPackage(); !errors.Is(err, ErrClosed) {
		t.Fatalf("OpenPackage after Close = %v; want ErrClosed", err)
	}
}

func TestPackageRequireCachingLoadersAndSentinel(t *testing.T) {
	state := newStateWithPackage(t, Options{})
	defer state.Close()
	if err := state.OpenString(); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@package-require.lua", `
local calls=0
package.preload.value=function(name, extra)
	calls=calls+1
	return {name=name,extra=extra}
end
local first=require("value","discarded")
local second=require("value")

local nilCalls=0
package.preload.nilmod=function()
	nilCalls=nilCalls+1
end
local nilFirst=require("nilmod")
local nilSecond=require("nilmod")

local falseCalls=0
package.preload.falsemod=function()
	falseCalls=falseCalls+1
	return false
end
local falseFirst=require("falsemod")
local falseSecond=require("falsemod")

package.preload.written=function(name)
	package.loaded[name]="written"
end
local written=require("written")

package.preload.cleared=function(name)
	package.loaded[name]=nil
end
local cleared=require("cleared")

package.preload.override=function(name)
	package.loaded[name]="stored"
	return "returned"
end
local overridden=require("override")

package.preload.cycle=function(name)
	return require(name)
end
local cycleOK,cycleError=pcall(require,"cycle")
local previousOK,previousError=pcall(require,"cycle")

local marker={}
sentinels={}
package.preload.failure=function(name)
	sentinels[1]=package.loaded[name]
	error(marker,0)
end
local failureOK,failureError=pcall(require,"failure")
local failedAgain,failedAgainError=pcall(require,"failure")

return first==second,first.name,first.extra,calls,
	nilFirst,nilSecond,nilCalls,
	falseFirst,falseSecond,falseCalls,
	written,cleared,overridden,package.loaded.override,
	cycleOK,string.find(cycleError,"loop or previous error",1,true)~=nil,
	previousOK,string.find(previousError,"loop or previous error",1,true)~=nil,
	failureOK,failureError==marker,
	failedAgain,string.find(failedAgainError,"loop or previous error",1,true)~=nil
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Bool(true),
		state.String("value"),
		Nil(),
		Number(1),
		Bool(true),
		Bool(true),
		Number(1),
		Bool(false),
		Bool(false),
		Number(2),
		state.String("written"),
		Nil(),
		state.String("returned"),
		state.String("returned"),
		Bool(false),
		Bool(true),
		Bool(false),
		Bool(true),
		Bool(false),
		Bool(true),
		Bool(false),
		Bool(true),
	)

	sentinelsValue, err := state.RawGlobal("sentinels")
	if err != nil {
		t.Fatal(err)
	}
	sentinels, _ := sentinelsValue.AsTable()
	sentinel, ok := sentinels.RawGetInt(1).AsUserData()
	if !ok {
		t.Fatal("failed module did not expose its userdata sentinel")
	}
	if err := sentinel.SetData("host mutation"); err != nil {
		t.Fatal(err)
	}
	if environment, environmentErr := state.UserDataEnvironment(
		sentinel,
	); environmentErr != nil || environment != nil {
		t.Fatalf(
			"sentinel environment = (%p, %v); want nil",
			environment,
			environmentErr,
		)
	}

	// A failed module keeps the old private sentinel. A freshly reopened
	// require must still recognize its exact identity even if a host changed
	// the ordinary userdata payload.
	if err := state.OpenPackage(); err != nil {
		t.Fatal(err)
	}
	retry := mustLoadString(t, state, "@package-reopen-sentinel.lua", `
package.preload.second=function(name)
	sentinels[2]=package.loaded[name]
	error("second failure",0)
end
pcall(require,"second")
local ok,message=pcall(require,"failure")
return ok,type(message),string.find(message,"loop or previous error",1,true)~=nil,
	sentinels[1]==sentinels[2]
`)
	results, err = state.Call(retry.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Bool(false),
		state.String("string"),
		Bool(true),
		Bool(true),
	)
}

func TestPackageRequireSentinelRemainsCompact(t *testing.T) {
	state := newStateWithPackage(t, Options{})
	defer state.Close()

	chunk := mustLoadString(t, state, "@package-compact-sentinel.lua", `
package.preload.empty=function() end
package.preload.failed=function()
	error("stopped",0)
end
local failed=pcall(require,"failed")
return require("empty"),failed
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true), Bool(false))

	entries, keys, stale := hostDirectoryKindCounts(
		&state.runtime.hosts,
		UserDataKind,
	)
	if entries != 0 || keys != 0 || stale != 0 {
		t.Fatalf(
			"Lua-only require published userdata handles: entries=%d keys=%d stale=%d",
			entries,
			keys,
			stale,
		)
	}
}

func TestPackageRequireUsesLua51SearcherRules(t *testing.T) {
	state := newStateWithPackage(t, Options{})
	defer state.Close()
	if err := state.OpenString(); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@package-searchers.lua", `
local calls={}
local callable=setmetatable({},{
	__call=function(_,name)
		calls[#calls+1]="callable:"..name
		return setmetatable({}, {__call=function() return "ignored" end})
	end,
})
local loaders={}
loaders[1]=function(name)
	calls[#calls+1]="first:"..name
	return "\n\tfirst miss"
end
loaders[2]=function(name)
	calls[#calls+1]="second:"..name
	return 42
end
loaders[3]=callable
loaders[5]=function()
	calls[#calls+1]="after hole"
	return function() return "wrong" end
end
package.loaders=loaders

local ok,message=pcall(function() return require("missing") end)
local fakeLoaded={}
package.loaded=fakeLoaded
local oldLoaders=package.loaders
package.preload.redirected=function() return "registry" end
package.loaders={
	function(name) return package.preload[name] end,
}
local redirected=require("redirected")

return ok,message,calls[1],calls[2],calls[3],calls[4],
	redirected,fakeLoaded.redirected,
	type(oldLoaders[3])
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 9 {
		t.Fatalf("searcher results = %d, want 9", len(results))
	}
	assertTestValues(
		t,
		results[2:],
		state.String("first:missing"),
		state.String("second:missing"),
		state.String("callable:missing"),
		Nil(),
		state.String("registry"),
		Nil(),
		state.String("table"),
	)
	if truth, ok := results[0].AsBool(); !ok || truth {
		t.Fatalf("missing require success = %v", results[0])
	}
	message, ok := results[1].AsString()
	if !ok ||
		!strings.Contains(message, "\n\tfirst miss42") ||
		strings.Contains(message, "after hole") {
		t.Fatalf("searcher error = %q", message)
	}
}

func TestPackageRequireHonorsRegistryAndTableMetamethods(t *testing.T) {
	state := newStateWithPackage(t, Options{})
	defer state.Close()

	chunk := mustLoadString(t, state, "@package-metamethods.lua", `
local actualLoaders=package.loaders
setmetatable(package,{
	__index=function(_,key)
		if key=="loaders" then return actualLoaders end
	end,
})
package.loaders=nil

local writes=0
setmetatable(package.loaded,{
	__index=function(_,name)
		if name=="virtual" then return 92 end
	end,
	__newindex=function(target,name,value)
		writes=writes+1
		rawset(target,name,value)
	end,
})
package.preload.meta=function() return 93 end
local virtual=require("virtual")
local meta=require("meta")
package.loaders=actualLoaders
return virtual,meta,writes
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Number(92),
		Number(93),
		Number(1),
	)

	packageValue, _ := state.RawGlobal("package")
	library, _ := packageValue.AsTable()
	oldLoaded, _ := library.RawGetString("loaded").AsTable()
	preload, _ := library.RawGetString("preload").AsTable()
	redirectLoader, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnNumber(94)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := preload.RawSetString(
		"registry-redirect",
		redirectLoader.Value(),
	); err != nil {
		t.Fatal(err)
	}
	redirectedLoaded, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.registry.rawSetStringValue(
		loadedModulesRegistryKey,
		redirectedLoaded.Value(),
	); err != nil {
		t.Fatal(err)
	}
	requireValue, _ := state.RawGlobal("require")
	results, err = state.Call(
		requireValue,
		state.String("registry-redirect"),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(94))
	assertTestValue(
		t,
		redirectedLoaded.RawGetString("registry-redirect"),
		Number(94),
	)
	assertTestValue(
		t,
		oldLoaded.RawGetString("registry-redirect"),
		Nil(),
	)
	assertTestValue(t, library.RawGetString("loaded"), oldLoaded.Value())
}

func TestPackageLuaAndNativeFileSearchers(t *testing.T) {
	directory := t.TempDir()
	nestedDirectory := filepath.Join(directory, "nested")
	if err := os.Mkdir(nestedDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(nestedDirectory, "module.lua"),
		[]byte(`local name=...; return {name=name,marker=fileMarker}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	initDirectory := filepath.Join(directory, "initial")
	if err := os.Mkdir(initDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(initDirectory, "init.lua"),
		[]byte(`return "from init"`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, "shebang.lua"),
		[]byte("#!/usr/bin/env lua\nreturn 73"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, "broken.lua"),
		[]byte("local ="),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	prototype, err := Compile("@binary-package.lua", `return 74`)
	if err != nil {
		t.Fatal(err)
	}
	dumped, err := dumpPrototype(prototype)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, "binary.lua"),
		[]byte(dumped),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	nativePath := filepath.Join(directory, "native.so")
	if err := os.WriteFile(nativePath, []byte("not a library"), 0o600); err != nil {
		t.Fatal(err)
	}
	rootPath := filepath.Join(directory, "root.so")
	if err := os.WriteFile(rootPath, []byte("not a library"), 0o600); err != nil {
		t.Fatal(err)
	}

	state := newStateWithPackage(t, Options{})
	defer state.Close()
	if err := state.OpenString(); err != nil {
		t.Fatal(err)
	}
	if err := state.SetRawGlobal("fileMarker", Number(71)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "?.lua") + ";" +
		filepath.Join(directory, "?", "init.lua")
	source := `
package.path=` + quoteLuaString(path) + `
package.cpath=` + quoteLuaString(filepath.Join(directory, "?.so")) + `

local nested=require("nested.module")
local initial=require("initial")
local shebang=require("shebang")
local binary=require("binary")
local missingOK,missingError=pcall(function() require("absent") end)
local brokenOK,brokenError=pcall(function() require("broken") end)
local nativeOK,nativeError=pcall(function() require("native") end)
local rootOK,rootError=pcall(function() require("root.child") end)
local loaded,loadError,where=package.loadlib("anything","luaopen_anything")

return nested.name,nested.marker,initial,shebang,binary,
	missingOK,missingError,
	brokenOK,brokenError,
	nativeOK,nativeError,
	rootOK,rootError,
	loaded,loadError,where
`
	chunk := mustLoadString(t, state, "@package-files.lua", source)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 16 {
		t.Fatalf("file-search results = %d, want 16", len(results))
	}
	assertTestValues(
		t,
		results[:7],
		state.String("nested.module"),
		Number(71),
		state.String("from init"),
		Number(73),
		Number(74),
		Bool(false),
		results[6],
	)
	missingMessage, _ := results[6].AsString()
	if !strings.Contains(
		missingMessage,
		"no file '"+filepath.Join(directory, "absent.lua")+"'",
	) || !strings.Contains(
		missingMessage,
		"no file '"+filepath.Join(directory, "absent", "init.lua")+"'",
	) {
		t.Fatalf("missing module error = %q", missingMessage)
	}
	if brokenOK, _ := results[7].AsBool(); brokenOK {
		t.Fatalf("broken module succeeded: %v", results[8])
	}
	brokenMessage, _ := results[8].AsString()
	if !strings.Contains(brokenMessage, "error loading module 'broken'") ||
		!strings.Contains(brokenMessage, "broken.lua:1:") {
		t.Fatalf("broken module error = %q", brokenMessage)
	}
	if nativeOK, _ := results[9].AsBool(); nativeOK {
		t.Fatalf("native module succeeded: %v", results[10])
	}
	nativeMessage, _ := results[10].AsString()
	if !strings.Contains(nativeMessage, dynamicLibrariesUnavailable) {
		t.Fatalf("native module error = %q", nativeMessage)
	}
	if rootOK, _ := results[11].AsBool(); rootOK {
		t.Fatalf("native root module succeeded: %v", results[12])
	}
	rootMessage, _ := results[12].AsString()
	if !strings.Contains(rootMessage, dynamicLibrariesUnavailable) ||
		!strings.Contains(rootMessage, rootPath) {
		t.Fatalf("native root module error = %q", rootMessage)
	}
	assertTestValues(
		t,
		results[13:],
		Nil(),
		state.String(dynamicLibrariesUnavailable),
		state.String("absent"),
	)
}

func TestPackageModuleAndSeeAllUseCallerEnvironment(t *testing.T) {
	state := newStateWithPackage(t, Options{})
	defer state.Close()

	chunk := mustLoadString(t, state, "@package-module.lua", `
fallback=77
local optionCalls=0
local function option(target)
	optionCalls=optionCalls+1
	target.optionValue=78
	return "discarded"
end
local function define()
	module("alpha.beta",option,package.seeall)
	exported=76
	return _M,_NAME,_PACKAGE,fallback,optionValue
end
local moduleTable,moduleName,packageName,inherited,optionValue=define()

package.loaded.reused={}
local function reuse()
	module("reused")
	reusedValue=79
end
reuse()

local failed
local function failOption()
	module("failed",function() error("option failed",0) end)
end
local optionOK,optionError=pcall(failOption)
failed=getfenv(failOption)==package.loaded.failed

conflict=1
local conflictOK,conflictError=pcall(function() module("conflict.child") end)

local metatable={marker=80}
local seeallTarget={}
setmetatable(seeallTarget,metatable)
package.seeall(seeallTarget)

local function tailModule()
	return module("tail.module")
end
tailModule()

return moduleTable==alpha.beta,moduleName,packageName,
	inherited,optionValue,alpha.beta.exported,optionCalls,
	package.loaded["alpha.beta"]==moduleTable,getfenv(define)==moduleTable,
	_G.reused==nil,package.loaded.reused.reusedValue,
	optionOK,optionError,failed,
	conflictOK,conflictError,
	getmetatable(seeallTarget)==metatable,metatable.marker,
	seeallTarget.fallback,
	getfenv(tailModule)==package.loaded["tail.module"]
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 20 {
		t.Fatalf("module results = %d, want 20", len(results))
	}
	assertTestValues(
		t,
		results[:12],
		Bool(true),
		state.String("alpha.beta"),
		state.String("alpha."),
		Number(77),
		Number(78),
		Number(76),
		Number(1),
		Bool(true),
		Bool(true),
		Bool(true),
		Number(79),
		Bool(false),
	)
	optionMessage, _ := results[12].AsString()
	if optionMessage != "option failed" {
		t.Fatalf("option error = %q", optionMessage)
	}
	assertTestValue(t, results[13], Bool(true))
	assertTestValue(t, results[14], Bool(false))
	conflictMessage, _ := results[15].AsString()
	if !strings.Contains(
		conflictMessage,
		"name conflict for module 'conflict.child'",
	) {
		t.Fatalf("conflict error = %q", conflictMessage)
	}
	assertTestValues(
		t,
		results[16:],
		Bool(true),
		Number(80),
		Number(77),
		Bool(true),
	)

	moduleValue, err := state.RawGlobal("module")
	if err != nil {
		t.Fatal(err)
	}
	_, err = state.Call(moduleValue, state.String("direct"))
	if err == nil || !strings.Contains(
		err.Error(),
		"'module' not called from a Lua function",
	) {
		t.Fatalf("direct module call error = %v", err)
	}
}

func TestPackageLuaLoaderPreservesResourceFailures(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(directory, "large.lua"),
		[]byte("return 1 --"+strings.Repeat("x", 128)),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	state := newStateWithPackage(t, Options{MaxLoadBytes: 32})
	defer state.Close()
	packageValue, err := state.RawGlobal("package")
	if err != nil {
		t.Fatal(err)
	}
	library, _ := packageValue.AsTable()
	if err := library.RawSetString(
		"path",
		state.String(filepath.Join(directory, "?.lua")),
	); err != nil {
		t.Fatal(err)
	}

	requireValue, err := state.RawGlobal("require")
	if err != nil {
		t.Fatal(err)
	}
	_, err = state.Call(requireValue, state.String("large"))
	var failure *Error
	if !errors.As(err, &failure) ||
		failure.Category() != ResourceError {
		t.Fatalf(
			"oversized module error = %#v; want ResourceError",
			err,
		)
	}
	loaded, _ := library.RawGetString("loaded").AsTable()
	assertTestValue(t, loaded.RawGetString("large"), Nil())
}

func TestPackageRequireCachedIsAllocationFree(t *testing.T) {
	requireStableAllocationAccounting(t)
	state := newStateWithPackage(t, Options{})
	defer state.Close()

	packageValue, err := state.RawGlobal("package")
	if err != nil {
		t.Fatal(err)
	}
	library, _ := packageValue.AsTable()
	preload, _ := library.RawGetString("preload").AsTable()
	loader, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnNumber(91)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := preload.RawSetString("cached", loader.Value()); err != nil {
		t.Fatal(err)
	}
	requireValue, err := state.RawGlobal("require")
	if err != nil {
		t.Fatal(err)
	}
	name := state.String("cached")
	var destination [1]Value
	if _, err := state.CallInto(
		requireValue,
		[]Value{name},
		destination[:],
	); err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, destination[:], Number(91))
	for range 16 {
		if _, err := state.CallInto(
			requireValue,
			[]Value{name},
			destination[:],
		); err != nil {
			t.Fatal(err)
		}
	}
	arguments := []Value{name}
	allocations := testing.AllocsPerRun(256, func() {
		if _, err := state.CallInto(
			requireValue,
			arguments,
			destination[:],
		); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("warm cached require allocated %v times", allocations)
	}
}

func BenchmarkPackageRequireCached(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		b.Fatal(err)
	}
	if err := state.OpenPackage(); err != nil {
		b.Fatal(err)
	}
	packageValue, _ := state.RawGlobal("package")
	library, _ := packageValue.AsTable()
	preload, _ := library.RawGetString("preload").AsTable()
	loader, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnNumber(1)
	})
	if err != nil {
		b.Fatal(err)
	}
	if err := preload.RawSetString("cached", loader.Value()); err != nil {
		b.Fatal(err)
	}
	requireValue, _ := state.RawGlobal("require")
	arguments := []Value{state.String("cached")}
	var destination [1]Value
	if _, err := state.CallInto(
		requireValue,
		arguments,
		destination[:],
	); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := state.CallInto(
			requireValue,
			arguments,
			destination[:],
		); err != nil {
			b.Fatal(err)
		}
	}
}

func TestPackageLibraryCasesMatchLua51(t *testing.T) {
	runLua51Cases(t, packageLibraryLua51Cases)
}

var packageLibraryLua51Cases = []lua51Case{
	{
		name:   "package_surface",
		source: `return type(package),type(require),type(module),type(package.loadlib),type(package.seeall),type(package.loaders),#package.loaders,type(package.loaded),type(package.preload),loadlib==nil`,
		want:   "ok 'table' 'function' 'function' 'function' 'function' 'table' 4 'table' 'table' true",
	},
	{
		name:   "require_caches_truthy_loader_result",
		source: `local calls=0; package.preload.cached=function(name,extra) calls=calls+1; return {name=name,extra=extra} end; local a=require("cached","discarded"); local b=require("cached"); return a==b,a.name,a.extra,calls`,
		want:   "ok true 'cached' nil 1",
	},
	{
		name:   "require_nil_false_and_cleared_results",
		source: `local nc,fc=0,0; package.preload.nilmod=function() nc=nc+1 end; package.preload.falsemod=function() fc=fc+1; return false end; package.preload.cleared=function(name) package.loaded[name]=nil end; local n1=require("nilmod"); local n2=require("nilmod"); local f1=require("falsemod"); local f2=require("falsemod"); local clear=require("cleared"); return n1,n2,nc,f1,f2,fc,clear`,
		want:   "ok true true 1 false false 2 nil",
	},
	{
		name:   "require_accumulates_string_and_number_searcher_results",
		source: `local calls=0; local loaders=package.loaders; package.loaders={function() calls=calls+1; return "\n\tfirst" end,function() calls=calls+1; return 42 end}; local ok,message=pcall(function() require("missing") end); package.loaders=loaders; return ok,type(message),string.find(message,"\n\tfirst42",1,true)~=nil,calls`,
		want:   "ok false 'string' true 2",
	},
	{
		name:   "require_uses_original_package_and_registry_loaded",
		source: `local original=package; local loaded=original.loaded; local fake={}; original.loaded=fake; original.preload.stable=function() return 7 end; package={}; local value=require("stable"); package=original; original.loaded=loaded; return value,fake.stable`,
		want:   "ok 7 nil",
	},
	{
		name:   "require_cycle_and_previous_error_keep_sentinel",
		source: `package.preload.cycle=function(name) return require(name) end; local a,b=pcall(require,"cycle"); local c,d=pcall(require,"cycle"); return a,string.find(b,"loop or previous error",1,true)~=nil,c,string.find(d,"loop or previous error",1,true)~=nil`,
		want:   "ok false true false true",
	},
	{
		name:   "require_coerces_and_truncates_module_names",
		source: `package.preload["12"]=function(name) return name end; package.preload.abc=function(name) return name end; return require(12),require("abc\000tail")`,
		want:   "ok '12' 'abc'",
	},
	{
		name:   "require_rejects_invalid_loader_tables",
		source: `local loaders=package.loaders; package.loaders=false; local a,b=pcall(require,"missing"); package.loaders=loaders; local preload=package.preload; package.preload=false; local c,d=pcall(require,"missing"); package.preload=preload; return a,string.find(b,"'package.loaders' must be a table",1,true)~=nil,c,string.find(d,"'package.preload' must be a table",1,true)~=nil`,
		want:   "ok false true false true",
	},
	{
		name:   "module_builds_environment_and_seeall_fallback",
		source: `fallback=5; local function define() module("oracle.mod",package.seeall); value=6; return _NAME,_PACKAGE,fallback end; local name,prefix,inherited=define(); return name,prefix,inherited,oracle.mod.value,package.loaded["oracle.mod"]==oracle.mod,getfenv(define)==oracle.mod`,
		want:   "ok 'oracle.mod' 'oracle.' 5 6 true true",
	},
	{
		name:   "module_changes_a_native_tail_callers_environment",
		source: `local function define() return module("oracle.tail") end; local ok=pcall(define); return ok,getfenv(define)==package.loaded["oracle.tail"]`,
		want:   "ok true true",
	},
}
