package lua

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBaseLoadAndLoadStringUseLua51Semantics(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()
	if err := state.OpenString(); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@load-library.lua", `
local loaded,loadError=loadstring("return 1,nil,3")
local a,b,c=loaded()
local broken,syntaxError=loadstring("local =","=broken")

local pieces={"ret","urn ",7}
local calls=0
local streamed,streamError=load(function()
	calls=calls+1
	return pieces[calls],"ignored"
end,"=streamed")

local emptyCalls=0
local empty=load(function()
	emptyCalls=emptyCalls+1
	if emptyCalls==1 then return "" end
	return "return 99"
end)

local marker={}
local failed,readerError=load(function() error(marker,0) end)
local wrong,wrongError=load(function() return {} end)

return type(loaded),loadError,a,b,c,
	broken,type(syntaxError),string.find(syntaxError,"broken:1:",1,true)~=nil,
	type(streamed),streamError,calls,streamed(),
	type(empty),emptyCalls,select("#",empty()),
	failed==nil,readerError==marker,
	wrong==nil,wrongError
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 19 {
		t.Fatalf("load results = %d, want 19", len(results))
	}
	assertTestValues(
		t,
		results[:18],
		state.String("function"),
		Nil(),
		Number(1),
		Nil(),
		Number(3),
		Nil(),
		state.String("string"),
		Bool(true),
		state.String("function"),
		Nil(),
		Number(4),
		Number(7),
		state.String("function"),
		Number(1),
		Number(0),
		Bool(true),
		Bool(true),
		Bool(true),
	)
	wrongMessage, ok := results[18].AsString()
	if !ok || !strings.Contains(
		wrongMessage,
		"reader function must return a string",
	) {
		t.Fatalf("wrong reader result = %v", results[18])
	}
}

func TestArgumentlessLoadFileAndDoFileUseStateInput(t *testing.T) {
	testCases := []struct {
		name   string
		source string
		caller string
	}{
		{
			name:   "loadfile",
			source: `return 41,nil,43`,
			caller: `local loaded,message=loadfile()
return type(loaded),message,loaded()`,
		},
		{
			name:   "dofile",
			source: `return 51,nil,53`,
			caller: `return dofile()`,
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			state := newStateWithBase(t, Options{
				Source: OSSource(),
				Stdin:  strings.NewReader(test.source),
			})
			defer state.Close()
			chunk := mustLoadString(
				t,
				state,
				"@standard-input.lua",
				test.caller,
			)
			results, err := state.Call(chunk.Value())
			if err != nil {
				t.Fatal(err)
			}
			if test.name == "loadfile" {
				assertTestValues(
					t,
					results,
					state.String("function"),
					Nil(),
					Number(41),
					Nil(),
					Number(43),
				)
			} else {
				assertTestValues(
					t,
					results,
					Number(51),
					Nil(),
					Number(53),
				)
			}
		})
	}
}

func TestLoadedChunksUseTheThreadGlobalEnvironment(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	chunk := mustLoadString(t, state, "@load-environment.lua", `
marker=17
local loader=loadstring
local caller=function() return loader("return marker") end
setfenv(caller,{loader=loader,marker=99})
return caller()()
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(17))
}

func TestBaseLoadReaderCannotYieldAcrossNativeBoundary(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	chunk := mustLoadString(t, state, "@load-yield.lua", `
local thread=coroutine.create(function()
	local loaded,loadError=load(function()
		coroutine.yield("piece")
		return "return 1"
	end)
	return loaded,loadError
end)
local ok,loaded,loadError=coroutine.resume(thread)
return ok,loaded,type(loadError),loadError,coroutine.status(thread)
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 5 {
		t.Fatalf("yield results = %d, want 5", len(results))
	}
	assertTestValues(
		t,
		results[:3],
		Bool(true),
		Nil(),
		state.String("string"),
	)
	message, ok := results[3].AsString()
	if !ok || !strings.Contains(message, "yield") {
		t.Fatalf("load reader yield error = %v", results[3])
	}
	assertTestValue(t, results[4], state.String("dead"))
}

func TestBaseLoadReaderHandlesBinaryAndNoResultEOF(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()
	if err := state.OpenString(); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@load-reader-kinds.lua", `
local dumped=string.dump(function() return 91 end)
local offset=1
local binary,binaryError=load(function()
	if offset>#dumped then return nil end
	local piece=string.sub(dumped,offset,offset+2)
	offset=offset+#piece
	return piece
end,"=binary-reader")

local calls=0
local source,sourceError=load(function()
	calls=calls+1
	if calls==1 then return "return 44" end
end)

return type(binary),binaryError,binary(),
	type(source),sourceError,calls,source()
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		state.String("function"),
		Nil(),
		Number(91),
		state.String("function"),
		Nil(),
		Number(2),
		Number(44),
	)
}

func TestBaseLoadPropagatesContextCancellation(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	ctx, cancel := context.WithCancel(context.Background())
	reader, err := state.NewNativeFunction(func(frame Frame) Outcome {
		cancel()
		return frame.ReturnString("return 1")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetRawGlobal("cancelReader", reader.Value()); err != nil {
		t.Fatal(err)
	}
	chunk := mustLoadString(
		t,
		state,
		"@load-context.lua",
		`return load(cancelReader)`,
	)
	results, callErr := state.CallContext(ctx, chunk.Value())
	if results != nil {
		t.Fatalf("cancelled load returned %v", results)
	}
	var failure *Error
	if !errors.As(callErr, &failure) ||
		failure.Category() != ContextError {
		t.Fatalf("cancelled load error = %#v", callErr)
	}
}

func TestBaseLoadFileAndDoFile(t *testing.T) {
	directory := t.TempDir()
	valuesPath := filepath.Join(directory, "values.lua")
	if err := os.WriteFile(
		valuesPath,
		[]byte("#!/usr/bin/env lua\nreturn 1,nil,3,nil"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(directory, "runtime.lua")
	if err := os.WriteFile(
		runtimePath,
		[]byte(`error("file boom",0)`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	syntaxPath := filepath.Join(directory, "syntax.lua")
	if err := os.WriteFile(
		syntaxPath,
		[]byte("local ="),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	closurePath := filepath.Join(directory, "closure.lua")
	if err := os.WriteFile(
		closurePath,
		[]byte(`local captured=81; return function() return captured end`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	effectPath := filepath.Join(directory, "effect.lua")
	if err := os.WriteFile(
		effectPath,
		[]byte(`dofileMarker=(dofileMarker or 0)+1; return "ignored"`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	missingPath := filepath.Join(directory, "missing.lua")

	state := newStateWithBase(t, Options{Source: OSSource()})
	defer state.Close()
	source := `
local valuesPath=` + quoteLuaString(valuesPath) + `
local runtimePath=` + quoteLuaString(runtimePath) + `
local syntaxPath=` + quoteLuaString(syntaxPath) + `
local closurePath=` + quoteLuaString(closurePath) + `
local effectPath=` + quoteLuaString(effectPath) + `
local missingPath=` + quoteLuaString(missingPath) + `

local loaded,loadError=loadfile(valuesPath)
local a,b,c,d=loaded()
local doCount=select("#",dofile(valuesPath))
local da,db,dc,dd=dofile(valuesPath)
local closure=dofile(closurePath,"ignored")
dofile(effectPath,"ignored")
local runtimeOK,runtimeError=pcall(dofile,runtimePath)
local syntaxOK,syntaxError=pcall(dofile,syntaxPath)
local missingOK,missingError=pcall(dofile,missingPath)
local absent,openError=loadfile(missingPath)

return type(loaded),loadError,a,b,c,d,
	doCount,da,db,dc,dd,
	runtimeOK,runtimeError,
	syntaxOK,type(syntaxError),
	missingOK,type(missingError),
	absent==nil,type(openError),
	closure(),dofileMarker
`
	chunk := mustLoadString(t, state, "@file-library.lua", source)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		state.String("function"),
		Nil(),
		Number(1),
		Nil(),
		Number(3),
		Nil(),
		Number(4),
		Number(1),
		Nil(),
		Number(3),
		Nil(),
		Bool(false),
		state.String("file boom"),
		Bool(false),
		state.String("string"),
		Bool(false),
		state.String("string"),
		Bool(true),
		state.String("string"),
		Number(81),
		Number(1),
	)
}

func TestBaseLoadFunctionsHonorTheLoadLimit(t *testing.T) {
	const source = `return loadstring("return 123456789")`
	state := newStateWithBase(t, Options{MaxLoadBytes: 12})
	defer state.Close()

	chunk, err := Compile("@limit-driver.lua", source)
	if err != nil {
		t.Fatal(err)
	}
	driver, err := state.LoadPrototype(chunk)
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(driver.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || !results[0].IsNil() {
		t.Fatalf("limited loadstring results = %v", results)
	}
	message, ok := results[1].AsString()
	if !ok || !strings.Contains(message, "load input exceeds") {
		t.Fatalf("limited loadstring error = %v", results[1])
	}
}

func TestLoadLibraryCasesMatchLua51(t *testing.T) {
	runLua51Cases(t, loadLibraryLua51Cases)
}

var loadLibraryLua51Cases = []lua51Case{
	{
		name:   "loadstring_success_and_syntax_failure",
		source: `local f,e=loadstring("return 1,nil,3"); local a,b,c=f(); local bad,message=loadstring("local =","=broken"); return type(f),e,a,b,c,bad,type(message),string.find(message,"broken:1:",1,true)~=nil`,
		want:   "ok 'function' nil 1 nil 3 nil 'string' true",
	},
	{
		name:   "load_reader_fragments_numbers_and_result_adjustment",
		source: `local pieces={"ret","urn ",7}; local i=0; local f,e=load(function() i=i+1; return pieces[i],"ignored" end,"=reader"); return type(f),e,i,f()`,
		want:   "ok 'function' nil 4 7",
	},
	{
		name:   "load_reader_preserves_arbitrary_error_values",
		source: `local marker={}; local f,e=load(function() error(marker,0) end); return f,rawequal(e,marker)`,
		want:   "ok nil true",
	},
	{
		name:   "load_reader_preserves_nil_error_value",
		source: `local f,e=load(function() error(nil,0) end); return f,e,select("#",f,e)`,
		want:   "ok nil nil 2",
	},
	{
		name:   "load_reader_rejects_non_string_results",
		source: `local f,e=load(function() return {} end); return f,type(e),e`,
		want:   "ok nil 'string' 'case:1: reader function must return a string'",
	},
	{
		name:   "loadstring_uses_global_not_caller_environment",
		source: `marker=17; local loader=loadstring; local caller=function() return loader("return marker") end; setfenv(caller,{loader=loader,marker=99}); return caller()()`,
		want:   "ok 17",
	},
	{
		name:   "loadstring_coerces_and_truncates_optional_names",
		source: `local _,numeric=loadstring("local =",123); local _,nul=loadstring("local =","=visible\000hidden"); return string.find(numeric,"[string \"123\"]:1:",1,true)~=nil,string.find(nul,"visible:1:",1,true)~=nil,string.find(nul,"hidden",1,true)==nil`,
		want:   "ok true true true",
	},
}
