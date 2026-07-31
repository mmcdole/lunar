package lua

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestZeroSourcePolicyDeniesFileLoadingButKeepsPreloads(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	if _, err := state.LoadFile("unreachable.lua"); !errors.Is(
		err,
		ErrSourceLoadingDisabled,
	) {
		t.Fatalf("LoadFile error = %v; want ErrSourceLoadingDisabled", err)
	}
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenString(); err != nil {
		t.Fatal(err)
	}
	if err := state.PreloadModule(
		"host",
		func(frame Frame) Outcome {
			return frame.ReturnString("preloaded")
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenPackage(); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@denied-source.lua", `
local loaded,loadError=loadfile("unreachable.lua")
local doOK,doError=pcall(dofile,"unreachable.lua")
local requireOK,requireError=pcall(require,"unreachable")
return require("host"),
	loaded==nil,type(loadError),
	doOK,type(doError),
	requireOK,type(requireError),
	string.find(requireError,"source-file loading is disabled",1,true)~=nil
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		state.String("preloaded"),
		Bool(true),
		state.String("string"),
		Bool(false),
		state.String("string"),
		Bool(false),
		state.String("string"),
		Bool(true),
	)
}

func TestOSSourceLoadsFilesSnapshotsLuaPathAndAllowsStdin(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "direct.lua")
	if err := os.WriteFile(path, []byte(`return 41`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LUA_PATH", "snapshot/?.lua")

	state, err := New(Options{
		Source: OSSource(),
		Stdin:  strings.NewReader(`return 42`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	t.Setenv("LUA_PATH", "changed/?.lua")

	function, err := state.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(function.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(41))

	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenString(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenPackage(); err != nil {
		t.Fatal(err)
	}
	packageValue, err := state.RawGlobal("package")
	if err != nil {
		t.Fatal(err)
	}
	library, _ := packageValue.AsTable()
	assertTestValue(
		t,
		rawStr(library, "path"),
		state.String("snapshot/?.lua"),
	)

	stdin := mustLoadString(
		t,
		state,
		"@os-source-stdin.lua",
		`local chunk,message=loadfile(); return type(chunk),message,chunk()`,
	)
	results, err = state.Call(stdin.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		state.String("function"),
		Nil(),
		Number(42),
	)
}

func TestFSSourceLoadsLogicalPathsAndRequiredModules(t *testing.T) {
	t.Setenv("LUA_PATH", "ambient/?.lua")
	files := fstest.MapFS{
		"direct.lua": {
			Data: []byte(`return 51`),
		},
		"alpha.lua": {
			Data: []byte(`return {name="alpha"}`),
		},
		"nested/module/init.lua": {
			Data: []byte(`return {name="nested"}`),
		},
		"alternate/beta.chunk": {
			Data: []byte(`return {name="beta"}`),
		},
	}
	state, err := New(Options{
		Source: FSSource(files),
		Stdin:  strings.NewReader(`return "must not load"`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	function, err := state.LoadFile("direct.lua")
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(function.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(51))

	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenString(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenPackage(); err != nil {
		t.Fatal(err)
	}
	chunk := mustLoadString(t, state, "@fs-source.lua", `
local alpha=require("alpha")
local nested=require("nested.module")
package.path="alternate/?.chunk"
local beta=require("beta")
local stdin,stdinError=loadfile()
return alpha.name,nested.name,beta.name,
	stdin==nil,
	string.find(stdinError,"source-file loading is disabled",1,true)~=nil
`)
	results, err = state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		state.String("alpha"),
		state.String("nested"),
		state.String("beta"),
		Bool(true),
		Bool(true),
	)
}

func TestWithPackagePathIsImmutableAndOverridesTheDefault(t *testing.T) {
	files := fstest.MapFS{}
	original := FSSource(files)
	customized := original.WithPackagePath("modules/?.luau")

	originalState, err := New(Options{Source: original})
	if err != nil {
		t.Fatal(err)
	}
	defer originalState.Close()
	if err := originalState.OpenPackage(); err != nil {
		t.Fatal(err)
	}
	originalPackage, _ := originalState.RawGlobal("package")
	originalTable, _ := originalPackage.AsTable()
	assertTestValue(
		t,
		rawStr(originalTable, "path"),
		originalState.String("?.lua;?/init.lua"),
	)

	customState, err := New(Options{Source: customized})
	if err != nil {
		t.Fatal(err)
	}
	defer customState.Close()
	if err := customState.OpenPackage(); err != nil {
		t.Fatal(err)
	}
	customPackage, _ := customState.RawGlobal("package")
	customTable, _ := customPackage.AsTable()
	assertTestValue(
		t,
		rawStr(customTable, "path"),
		customState.String("modules/?.luau"),
	)
}

type trackedSourceReader struct {
	*strings.Reader
	closed bool
}

func (reader *trackedSourceReader) Close() error {
	reader.closed = true
	return nil
}

func TestCustomSourceReceivesContextClosesReadersAndSearchesCandidates(t *testing.T) {
	type openCall struct {
		ctx  context.Context
		name string
	}
	var calls []openCall
	var readers []*trackedSourceReader
	opener := func(
		ctx context.Context,
		name string,
	) (io.ReadCloser, error) {
		calls = append(calls, openCall{ctx: ctx, name: name})
		source := ""
		switch name {
		case "direct.lua":
			source = `return 61`
		case "module/init.lua":
			source = `return 62`
		default:
			return nil, fs.ErrNotExist
		}
		reader := &trackedSourceReader{Reader: strings.NewReader(source)}
		readers = append(readers, reader)
		return reader, nil
	}
	state, err := New(Options{Source: CustomSource(opener)})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	ctx := context.WithValue(
		context.Background(),
		struct{ name string }{"source"},
		"marker",
	)
	function, err := loadFileCtx(t, state, ctx, "direct.lua")
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(function.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(61))
	if len(calls) != 1 || calls[0].ctx != ctx {
		t.Fatalf("direct open calls = %#v; want supplied context", calls)
	}
	if len(readers) != 1 || !readers[0].closed {
		t.Fatal("direct source reader was not closed")
	}

	if err := state.OpenPackage(); err != nil {
		t.Fatal(err)
	}
	require, err := state.RawGlobal("require")
	if err != nil {
		t.Fatal(err)
	}
	results, err = state.Call(require, String("module"))
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(62))
	if len(calls) != 3 ||
		calls[1].name != "module.lua" ||
		calls[2].name != "module/init.lua" {
		t.Fatalf("require open calls = %#v", calls)
	}
	if calls[1].ctx == nil || calls[2].ctx == nil {
		t.Fatal("ordinary require supplied a nil context")
	}
	if len(readers) != 2 || !readers[1].closed {
		t.Fatal("module source reader was not closed")
	}
}

func TestCustomSourceOpenFailuresPreserveCauses(t *testing.T) {
	sentinel := errors.New("source backend unavailable")
	state, err := New(Options{
		Source: CustomSource(func(
			context.Context,
			string,
		) (io.ReadCloser, error) {
			return nil, sentinel
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	if _, err := state.LoadFile("direct.lua"); !errors.Is(err, sentinel) {
		t.Fatalf("LoadFile error = %#v; want sentinel cause", err)
	}
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}
	doFile := mustLoadString(
		t,
		state,
		"@custom-source-dofile.lua",
		`return dofile("direct.lua")`,
	)
	if _, err := state.Call(doFile.Value()); !errors.Is(err, sentinel) {
		t.Fatalf("dofile error = %#v; want sentinel cause", err)
	}
	if err := state.OpenPackage(); err != nil {
		t.Fatal(err)
	}
	require, err := state.RawGlobal("require")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Call(require, String("module")); !errors.Is(
		err,
		sentinel,
	) {
		t.Fatalf("require error = %#v; want sentinel cause", err)
	}

	readSentinel := errors.New("source reader failed")
	readState, err := New(Options{
		Source: CustomSource(func(
			context.Context,
			string,
		) (io.ReadCloser, error) {
			return io.NopCloser(&finalErrorReader{
				text:    "local = trailing",
				failure: readSentinel,
			}), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer readState.Close()
	if err := readState.OpenPackage(); err != nil {
		t.Fatal(err)
	}
	readRequire, err := readState.RawGlobal("require")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readState.Call(
		readRequire,
		String("module"),
	); !errors.Is(err, readSentinel) {
		t.Fatalf("require read error = %#v; want reader cause", err)
	}
}

func TestSourcePolicyRejectsNilBackendsAndNilReaders(t *testing.T) {
	if _, err := New(Options{Source: FSSource(nil)}); !errors.Is(
		err,
		ErrNilSourceFS,
	) {
		t.Fatalf("nil FSSource error = %v; want ErrNilSourceFS", err)
	}
	if _, err := New(Options{Source: CustomSource(nil)}); !errors.Is(
		err,
		ErrNilSourceOpener,
	) {
		t.Fatalf(
			"nil CustomSource error = %v; want ErrNilSourceOpener",
			err,
		)
	}

	state, err := New(Options{
		Source: CustomSource(func(
			context.Context,
			string,
		) (io.ReadCloser, error) {
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if _, err := state.LoadFile("nil.lua"); !errors.Is(err, ErrNilReader) {
		t.Fatalf("nil source reader error = %v; want ErrNilReader", err)
	}

	sentinel := errors.New("open failed with reader")
	returned := &trackedSourceReader{Reader: strings.NewReader("")}
	errorState, err := New(Options{
		Source: CustomSource(func(
			context.Context,
			string,
		) (io.ReadCloser, error) {
			return returned, sentinel
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer errorState.Close()
	if _, err := errorState.LoadFile("error.lua"); !errors.Is(
		err,
		sentinel,
	) {
		t.Fatalf("reader-with-error result = %v; want sentinel", err)
	}
	if !returned.closed {
		t.Fatal("reader returned with an open error was not closed")
	}
}

func TestCustomSourceCancellationWinsAfterOpening(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader := &trackedSourceReader{Reader: strings.NewReader(`return 1`)}
	state, err := New(Options{
		Source: CustomSource(func(
			openCtx context.Context,
			name string,
		) (io.ReadCloser, error) {
			if openCtx != ctx {
				t.Fatalf("opener context = %p; want %p", openCtx, ctx)
			}
			if name != "cancel.lua" {
				t.Fatalf("opener name = %q", name)
			}
			cancel()
			return reader, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	_, err = loadFileCtx(t, state, ctx, "cancel.lua")
	var failure *Error
	if !errors.As(err, &failure) ||
		failure.Category() != ContextError {
		t.Fatalf("cancelled open error = %#v; want ContextError", err)
	}
	if !reader.closed {
		t.Fatal("reader returned during cancellation was not closed")
	}
}

func TestPackageModuleNameUsesPolicySeparator(t *testing.T) {
	if got := packageModuleName("one.two", "/"); got != "one/two" {
		t.Fatalf("slash module name = %q", got)
	}
	if got := packageModuleName("one.two", `\`); got != `one\two` {
		t.Fatalf("Windows module name = %q", got)
	}
}
