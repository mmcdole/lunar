package lua

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenDebugInstallsFreshCanonicalLibrary(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	before, err := state.RawGlobal("debug")
	if err != nil {
		t.Fatal(err)
	}
	if !before.IsNil() {
		t.Fatalf("new state debug = %v; want nil", before)
	}
	loadedBeforeOpen := mustLoadString(
		t,
		state,
		"@open-debug.lua",
		`return debug.getregistry()`,
	)
	if err := state.OpenDebug(); err != nil {
		t.Fatal(err)
	}

	libraryValue, err := state.RawGlobal("debug")
	if err != nil {
		t.Fatal(err)
	}
	library, ok := libraryValue.AsTable()
	if !ok {
		t.Fatalf("debug = %v; want table", libraryValue)
	}
	want := make(map[string]Kind, len(debugLibraryFunctions))
	previous := make(map[string]Value, len(debugLibraryFunctions))
	for _, definition := range debugLibraryFunctions {
		want[definition.name] = FunctionKind
		previous[definition.name] = library.RawGetString(definition.name)
	}
	assertTableSurface(t, library, want)

	results, err := state.Call(loadedBeforeOpen.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("getregistry result count = %d; want 1", len(results))
	}
	registry, ok := results[0].AsTable()
	if !ok || registry.runtimeObject() != state.registry {
		t.Fatal("debug.getregistry did not return the canonical registry")
	}
	loaded := state.registry.rawGetStringValue(loadedModulesRegistryKey)
	loadedTable, ok := loaded.AsTable()
	if !ok {
		t.Fatalf("registry _LOADED = %v; want table", loaded)
	}
	if current := loadedTable.RawGetString("debug"); !sameTestObject(
		current,
		libraryValue,
	) {
		t.Fatal("_LOADED.debug does not identify the debug library")
	}

	if err := state.SetRawGlobal("debug", Number(1)); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenDebug(); err != nil {
		t.Fatal(err)
	}
	reopenedValue, err := state.RawGlobal("debug")
	if err != nil {
		t.Fatal(err)
	}
	reopened, ok := reopenedValue.AsTable()
	if !ok {
		t.Fatalf("reopened debug = %v; want table", reopenedValue)
	}
	if sameTestObject(libraryValue, reopenedValue) {
		t.Fatal("reopening did not replace the debug table")
	}
	for name, old := range previous {
		current := reopened.RawGetString(name)
		if sameTestObject(old, current) {
			t.Fatalf("reopened debug.%s is not a fresh Function", name)
		}
	}

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenDebug(); !errors.Is(err, ErrClosed) {
		t.Fatalf("OpenDebug after Close = %v; want ErrClosed", err)
	}
}

func TestDebugRegistryMetatablesAndEnvironments(t *testing.T) {
	state := newStateWithDebug(t, Options{})
	defer state.Close()

	chunk := mustLoadString(t, state, "@debug-objects.lua", `
local registry=debug.getregistry()
registry.debug_marker=71

local protected={}
local protectedMT={__metatable="locked"}
setmetatable(protected,protectedMT)
local rawMT=debug.getmetatable(protected)
local setResult=debug.setmetatable(protected,nil)

local numberMT={marker=72}
local typeSet=debug.setmetatable(1,numberMT)
local sharedNumberMT=debug.getmetatable(2)
debug.setmetatable(1,nil)

local function luaFunction() return selected end
local functionEnvironment={selected=73}
local functionSet=debug.setfenv(luaFunction,functionEnvironment)

local nativeEnvironment=debug.getfenv(print)
local replacementNativeEnvironment={marker=74}
local nativeSet=debug.setfenv(print,replacementNativeEnvironment)
local nativeChanged=debug.getfenv(print)==replacementNativeEnvironment
debug.setfenv(print,nativeEnvironment)

local data=newproxy()
local dataEnvironment={marker=75}
local dataSet=debug.setfenv(data,dataEnvironment)

local thread=coroutine.create(function() return 1 end)
local threadEnvironment={marker=76}
local threadSet=debug.setfenv(thread,threadEnvironment)

return registry.debug_marker,
       rawMT==protectedMT,getmetatable(protected),setResult,
       typeSet,sharedNumberMT==numberMT,
       luaFunction(),functionSet==luaFunction,
       nativeSet==print,nativeChanged,
       dataSet==data,debug.getfenv(data)==dataEnvironment,
       threadSet==thread,debug.getfenv(thread)==threadEnvironment,
       debug.getfenv(1)
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Number(71),
		Bool(true),
		Nil(),
		Bool(true),
		Bool(true),
		Bool(true),
		Number(73),
		Bool(true),
		Bool(true),
		Bool(true),
		Bool(true),
		Bool(true),
		Bool(true),
		Bool(true),
		Nil(),
	)
}

func TestDebugUpvaluesPreserveExactArityAndNativePrivacy(t *testing.T) {
	state := newStateWithDebug(t, Options{})
	defer state.Close()

	native, err := state.NewNativeFunction(
		func(frame Frame) Outcome { return frame.Return() },
		Number(91),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetRawGlobal("captured_native", native.Value()); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@debug-upvalues.lua", `
local shared=17
local function read() return shared end
local function alias() return shared end
local name,value=debug.getupvalue(read,1)
local setName=debug.setupvalue(read,1,23)
return name,value,setName,read(),alias(),
       select("#",debug.getupvalue(read,0)),
       select("#",debug.getupvalue(read,2)),
       select("#",debug.setupvalue(read,0,1)),
       select("#",debug.getupvalue(captured_native,1)),
       select("#",debug.setupvalue(captured_native,1,1)),
       debug.getinfo(captured_native,"u").nups
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		state.String("shared"),
		Number(17),
		state.String("shared"),
		Number(23),
		Number(23),
		Number(0),
		Number(0),
		Number(0),
		Number(0),
		Number(0),
		Number(1),
	)
}

func TestDebugInfoCoversFunctionsCallsLinesAndTailFrames(t *testing.T) {
	state := newStateWithDebug(t, Options{})
	defer state.Close()

	native, err := state.NewNativeFunction(
		func(frame Frame) Outcome { return frame.Return() },
		Number(1),
		Number(2),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetRawGlobal("debug_native", native.Value()); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@debug-info.lua", `
local function described(a)
	local b=a+1
	return b
end
local direct=debug.getinfo(described)
local native=debug.getinfo(debug_native)
local empty=debug.getinfo(described,"")
local lines=debug.getinfo(described,"L").activelines
local lineCount,lineValues=0,true
for line,value in pairs(lines) do
	lineCount=lineCount+1
	lineValues=lineValues and type(line)=="number" and value==true
end

local function inspect()
	local info=debug.getinfo(1,"n")
	return info.name,info.namewhat
end
local localCall=inspect
local localName,localKind=localCall()
global_debug_inspect=inspect
local globalName,globalKind=global_debug_inspect()
local holder={field=inspect,method=inspect}
local fieldName,fieldKind=holder.field()
local methodName,methodKind=holder:method()
local captured=inspect
local function throughUpvalue()
	local name,kind=captured()
	return name,kind
end
local upvalueName,upvalueKind=throughUpvalue()

local function tail(n)
	if n==0 then
		local info=debug.getinfo(2,"SlnufL")
		return info
	end
	return tail(n-1)
end
local tailInfo=tail(2)

return direct.source,direct.short_src,direct.what,
       direct.linedefined,direct.lastlinedefined,direct.currentline,
       direct.nups,direct.name,direct.namewhat,direct.func==described,
       direct.activelines,
       native.source,native.short_src,native.what,
       native.linedefined,native.lastlinedefined,native.currentline,
       native.nups,native.name,native.namewhat,native.func==debug_native,
       native.activelines,
       next(empty),lineCount>0,lineValues,
       localName,localKind,globalName,globalKind,
       fieldName,fieldKind,methodName,methodKind,
       upvalueName,upvalueKind,
       tailInfo.source,tailInfo.short_src,tailInfo.what,
       tailInfo.linedefined,tailInfo.lastlinedefined,
       tailInfo.currentline,tailInfo.nups,tailInfo.name,
       tailInfo.namewhat,tailInfo.func,tailInfo.activelines
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		state.String("@debug-info.lua"),
		state.String("debug-info.lua"),
		state.String("Lua"),
		Number(2),
		Number(5),
		Number(-1),
		Number(0),
		Nil(),
		state.String(""),
		Bool(true),
		Nil(),
		state.String("=[C]"),
		state.String("[C]"),
		state.String("C"),
		Number(-1),
		Number(-1),
		Number(-1),
		Number(2),
		Nil(),
		state.String(""),
		Bool(true),
		Nil(),
		Nil(),
		Bool(true),
		Bool(true),
		state.String("localCall"),
		state.String("local"),
		state.String("global_debug_inspect"),
		state.String("global"),
		state.String("field"),
		state.String("field"),
		state.String("method"),
		state.String("method"),
		state.String("captured"),
		state.String("upvalue"),
		state.String("=(tail call)"),
		state.String("(tail call)"),
		state.String("tail"),
		Number(-1),
		Number(-1),
		Number(-1),
		Number(0),
		state.String(""),
		state.String(""),
		Nil(),
		Nil(),
	)
}

func TestDebugLocalsMutateLiveAndSuspendedActivations(t *testing.T) {
	state := newStateWithDebug(t, Options{})
	defer state.Close()

	chunk := mustLoadString(t, state, "@debug-locals.lua", `
local function active(first,second)
	local third=30
	local firstName,firstValue=debug.getlocal(1,1)
	local secondName,secondValue=debug.getlocal(1,2)
	local thirdName,thirdValue=debug.getlocal(1,3)
	local changed=debug.setlocal(1,3,44)
	local missingGet=select("#",debug.getlocal(1,1000))
	local missingSet=select("#",debug.setlocal(1,1000,9))
	return firstName,firstValue,secondName,secondValue,
	       thirdName,thirdValue,changed,third,missingGet,missingSet
end

local temporaryName,temporaryValue=debug.getlocal(0,1)
local activeResults={active(10,20)}

local thread=coroutine.create(function(argument)
	local localValue=argument+1
	coroutine.yield(localValue)
	return argument,localValue
end)
local started,yielded=coroutine.resume(thread,50)
local topInfo=debug.getinfo(thread,0,"S")
local argumentName,argumentValue=debug.getlocal(thread,1,1)
local localName,localValue=debug.getlocal(thread,1,2)
local changedArgument=debug.setlocal(thread,1,1,60)
local resumed,finalArgument,finalLocal=coroutine.resume(thread)

return temporaryName,temporaryValue,
       activeResults[1],activeResults[2],
       activeResults[3],activeResults[4],
       activeResults[5],activeResults[6],
       activeResults[7],activeResults[8],
       activeResults[9],activeResults[10],
       started,yielded,topInfo.what,
       argumentName,argumentValue,localName,localValue,changedArgument,
       resumed,finalArgument,finalLocal,
       debug.getinfo(thread,0)
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		state.String("(*temporary)"),
		Number(0),
		state.String("first"),
		Number(10),
		state.String("second"),
		Number(20),
		state.String("third"),
		Number(30),
		state.String("third"),
		Number(44),
		Number(1),
		Number(1),
		Bool(true),
		Number(51),
		state.String("C"),
		state.String("argument"),
		Number(50),
		state.String("localValue"),
		Number(51),
		state.String("argument"),
		Bool(true),
		Number(60),
		Number(51),
		Nil(),
	)
}

func TestDebugTracebackFormattingElisionAndErrorObjects(t *testing.T) {
	state := newStateWithDebug(t, Options{})
	defer state.Close()

	chunk := mustLoadString(t, state, "@debug-trace.lua", `
local marker={}
local same=debug.traceback(marker)==marker
local falseSame=debug.traceback(false)==false
local handlerValue
local ok=select(1,xpcall(function() error(marker) end,function(value)
	handlerValue=debug.traceback(value)
	return handlerValue
end))

local function shallow()
	local trace=debug.traceback("message",1)
	return trace
end
local shallowTrace=shallow()
local normalized=string.gsub(shallowTrace,"\n","|")

local function deep(level)
	if level==0 then
		return debug.traceback("deep",1)
	end
	local trace=deep(level-1)
	return trace
end
local deepTrace=deep(35)
local _,frameMarkers=string.gsub(deepTrace,"\n\t","")

local absent=debug.traceback()
local explicitEmpty=debug.traceback("")
local numeric=debug.traceback(12)
local negative=debug.traceback("negative",-1)

return same,falseSame,not ok,handlerValue==marker,
       string.find(normalized,"message|stack traceback:",1,true)==1,
       string.find(normalized,"debug%-trace.lua:")~=nil,
       string.find(deepTrace,"\n\t...",1,true)~=nil,
       frameMarkers,
       string.find(absent,"stack traceback:",1,true)==1,
       string.sub(explicitEmpty,1,1)=="\n",
       string.find(numeric,"12\nstack traceback:",1,true)==1,
       string.find(negative,"stack traceback:",1,true)~=nil
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Bool(true),
		Bool(true),
		Bool(true),
		Bool(true),
		Bool(true),
		Bool(true),
		Bool(true),
		Number(22),
		Bool(true),
		Bool(true),
		Bool(true),
		Bool(true),
	)
}

func TestDebugConsoleUsesStateStreamsAndIsolatesCommands(t *testing.T) {
	var stderr bytes.Buffer
	state := newStateWithDebug(t, Options{
		Stdin: strings.NewReader(
			"debug_console_marker=17\n" +
				"local command_local=19\n" +
				"assert(command_local==nil)\n" +
				"this is not lua\n" +
				"error('console failure')\n" +
				"cont\n",
		),
		Stderr: &stderr,
	})
	defer state.Close()

	chunk := mustLoadString(
		t,
		state,
		"@debug-console.lua",
		`debug.debug(); return debug_console_marker`,
	)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(17))

	output := stderr.String()
	if count := strings.Count(output, "lua_debug> "); count != 6 {
		t.Fatalf("console prompt count = %d; want 6\n%s", count, output)
	}
	if !strings.Contains(output, "(debug command):1:") {
		t.Fatalf("console did not identify command source:\n%s", output)
	}
	if !strings.Contains(output, "console failure") {
		t.Fatalf("console did not report runtime failure:\n%s", output)
	}
}

func TestDebugConsoleReturnsAtEOF(t *testing.T) {
	var stderr bytes.Buffer
	state := newStateWithDebug(t, Options{
		Stdin:  strings.NewReader("debug_eof_marker=29"),
		Stderr: &stderr,
	})
	defer state.Close()

	chunk := mustLoadString(
		t,
		state,
		"@debug-console-eof.lua",
		`debug.debug(); return debug_eof_marker`,
	)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(29))
	if got := stderr.String(); got != "lua_debug> " {
		t.Fatalf("EOF console output = %q; want one prompt", got)
	}
}

var debugLibraryLua51Cases = []lua51Case{
	{
		name:   "raw_metatable_and_shared_type_metatable",
		source: `local mt={__metatable="locked"}; local t=setmetatable({},mt); local raw=debug.getmetatable(t); local changed=debug.setmetatable(t,nil); local numberMT={}; local typeChanged=debug.setmetatable(1,numberMT); local shared=debug.getmetatable(2)==numberMT; debug.setmetatable(1,nil); return raw==mt,getmetatable(t),changed,typeChanged,shared`,
		want:   "ok true nil true true true",
	},
	{
		name:   "function_userdata_and_thread_environments",
		source: `local function f() return selected end; local fe={selected=31}; local u=newproxy(); local ue={}; local co=coroutine.create(function() end); local ce={}; local a=debug.setfenv(f,fe); local b=debug.setfenv(u,ue); local c=debug.setfenv(co,ce); return f(),a==f,debug.getfenv(f)==fe,b==u,debug.getfenv(u)==ue,c==co,debug.getfenv(co)==ce,debug.getfenv(1)`,
		want:   "ok 31 true true true true true true nil",
	},
	{
		name:   "upvalue_arity_alias_and_native_privacy",
		source: `local x=7; local function a() return x end; local function b() return x end; local n,v=debug.getupvalue(a,1); local set=debug.setupvalue(a,1,9); return n,v,set,a(),b(),select("#",debug.getupvalue(a,2)),select("#",debug.setupvalue(a,2,1)),select("#",debug.getupvalue(print,1)),select("#",debug.setupvalue(print,1,1))`,
		want:   "ok 'x' 7 'x' 9 9 0 0 0 0",
	},
	{
		name:   "direct_lua_and_native_function_info",
		source: `local function f(a) local b=a+1; return b end; local l=debug.getinfo(f); local c=debug.getinfo(print); local e=debug.getinfo(f,""); return l.source,l.short_src,l.what,l.linedefined,l.lastlinedefined,l.currentline,l.nups,l.name,l.namewhat,l.func==f,l.activelines,c.source,c.short_src,c.what,c.linedefined,c.lastlinedefined,c.currentline,c.nups,c.name,c.namewhat,c.func==print,c.activelines,next(e)`,
		want:   "ok '=case' 'case' 'Lua' 1 1 -1 0 nil '' true nil '=[C]' '[C]' 'C' -1 -1 -1 0 nil '' true nil nil",
	},
	{
		name:   "getinfo_out_of_range_and_exact_errors",
		source: `local a=select("#",debug.getinfo(100000)); local value=debug.getinfo(100000); local ok1,e1=pcall(debug.getinfo,{}); local ok2,e2=pcall(debug.getinfo,function() end,"x"); return a,value,ok1,string.find(e1,"function or level expected",1,true)~=nil,ok2,string.find(e2,"invalid option",1,true)~=nil`,
		want:   "ok 1 nil false true false true",
	},
	{
		name:   "logical_tail_info",
		source: `local function f(n) if n==0 then local i=debug.getinfo(2,"SlnufL"); return i.source,i.short_src,i.what,i.linedefined,i.lastlinedefined,i.currentline,i.nups,i.name,i.namewhat,i.func,i.activelines end return f(n-1) end return f(2)`,
		want:   "ok '=(tail call)' '(tail call)' 'tail' -1 -1 -1 0 '' '' nil nil",
	},
	{
		name:   "locals_mutation_and_absent_arity",
		source: `local function f(a,b) local c=3; local n1,v1=debug.getlocal(1,1); local n2,v2=debug.getlocal(1,2); local n3,v3=debug.getlocal(1,3); local changed=debug.setlocal(1,3,4); return n1,v1,n2,v2,n3,v3,changed,c,select("#",debug.getlocal(1,1000)),select("#",debug.setlocal(1,1000,1)) end return f(1,2)`,
		want:   "ok 'a' 1 'b' 2 'c' 3 'c' 4 1 1",
	},
	{
		name:   "suspended_thread_inspection_and_mutation",
		source: `local co=coroutine.create(function(a) local b=a+1; coroutine.yield(b); return a,b end); local started,yielded=coroutine.resume(co,10); local top=debug.getinfo(co,0,"S"); local an,av=debug.getlocal(co,1,1); local bn,bv=debug.getlocal(co,1,2); local changed=debug.setlocal(co,1,1,20); local resumed,a,b=coroutine.resume(co); return started,yielded,top.what,an,av,bn,bv,changed,resumed,a,b,debug.getinfo(co,0)`,
		want:   "ok true 11 'C' 'a' 10 'b' 11 'a' true 20 11 nil",
	},
	{
		name:   "traceback_non_string_is_preserved",
		source: `local marker={}; local first=debug.traceback(marker)==marker; local second=debug.traceback(false)==false; local seen; local ok=select(1,xpcall(function() error(marker) end,function(value) seen=debug.traceback(value); return seen end)); return first,second,not ok,seen==marker`,
		want:   "ok true true true true",
	},
	{
		name:   "traceback_headers_and_explicit_empty_message",
		source: `local absent=debug.traceback(); local empty=debug.traceback(""); local number=debug.traceback(12); return string.find(absent,"stack traceback:",1,true)==1,string.sub(empty,1,1)=="\n",string.find(number,"12\nstack traceback:",1,true)==1`,
		want:   "ok true true true",
	},
	{
		name:   "traceback_elides_middle_frames",
		source: `local function f(n) if n==0 then return debug.traceback("m",1) end local trace=f(n-1); return trace end; local trace=f(35); local _,markers=string.gsub(trace,"\n\t",""); return string.find(trace,"\n\t...",1,true)~=nil,markers`,
		want:   "ok true 22",
	},
}

func TestDebugLibraryMatchesLua51Cases(t *testing.T) {
	for _, test := range debugLibraryLua51Cases {
		t.Run(test.name, func(t *testing.T) {
			if got := runDebugLua51Case(t, test.source); got != test.want {
				t.Fatalf(
					"%s\n got: %s\nwant: %s",
					test.source,
					got,
					test.want,
				)
			}
		})
	}
}

// TestDebugLibraryCasesMatchLua51Oracle re-derives every recorded expectation
// from an actual Lua 5.1 interpreter when LUNAR_LUA51 is set.
func TestDebugLibraryCasesMatchLua51Oracle(t *testing.T) {
	binary := os.Getenv("LUNAR_LUA51")
	if binary == "" {
		t.Skip("set LUNAR_LUA51 to a Lua 5.1 interpreter to verify")
	}

	driver := &strings.Builder{}
	driver.WriteString(lua51OracleDriver)
	driver.WriteString("local sources = {\n")
	for _, test := range debugLibraryLua51Cases {
		driver.WriteString(quoteLuaString(test.source))
		driver.WriteString(",\n")
	}
	driver.WriteString("}\nrun(sources)\n")

	path := filepath.Join(t.TempDir(), "debug-oracle.lua")
	if err := os.WriteFile(path, []byte(driver.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(binary, path).Output()
	if err != nil {
		t.Fatalf("%s: %v", binary, err)
	}
	lines := strings.Split(strings.TrimRight(string(output), "\n"), "\n")
	if len(lines) != len(debugLibraryLua51Cases) {
		t.Fatalf(
			"oracle produced %d lines; want %d\n%s",
			len(lines),
			len(debugLibraryLua51Cases),
			output,
		)
	}
	record := os.Getenv("LUNAR_LUA51_RECORD") != ""
	for index, test := range debugLibraryLua51Cases {
		if record {
			t.Logf("%s: %q", test.name, lines[index])
			continue
		}
		if lines[index] != test.want {
			t.Errorf(
				"%s: oracle disagrees\n got: %s\nwant: %s",
				test.name,
				lines[index],
				test.want,
			)
		}
	}
}

func newStateWithDebug(t testing.TB, options Options) *State {
	t.Helper()
	state, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	openers := []func() error{
		state.OpenBase,
		state.OpenCoroutine,
		state.OpenString,
		state.OpenDebug,
	}
	for _, open := range openers {
		if err := open(); err != nil {
			state.Close()
			t.Fatal(err)
		}
	}
	return state
}

func runDebugLua51Case(t *testing.T, source string) string {
	t.Helper()
	state := newStateWithDebug(t, Options{})
	defer state.Close()

	chunk, err := state.LoadString("=case", source)
	if err != nil {
		return "syntax " + formatLua51Text(err.Error())
	}
	results, err := state.Call(chunk.Value())
	if err != nil {
		var failure *Error
		if errors.As(err, &failure) {
			return "error " + formatLua51Value(failure.Value())
		}
		return "error " + formatLua51Text(err.Error())
	}
	parts := make([]string, 0, len(results)+1)
	parts = append(parts, "ok")
	for _, value := range results {
		parts = append(parts, formatLua51Value(value))
	}
	return strings.Join(parts, " ")
}

func sameTestObject(left, right Value) bool {
	same, applicable := left.SameObject(right)
	return applicable && same
}
