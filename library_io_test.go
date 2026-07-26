package lua

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type ioCloseTrackingWriter struct {
	strings.Builder
	closeCount int
}

func (writer *ioCloseTrackingWriter) Close() error {
	writer.closeCount++
	return nil
}

func TestOpenIOBuildsCanonicalFilesAndPrivateDefaults(t *testing.T) {
	state := newStateWithIO(t, Options{})
	defer state.Close()

	library := ioLibraryTable(t, state)
	metatable := fileMetatable(t, state)
	if index := metatable.RawGetString("__index"); index.Kind() !=
		TableKind {
		t.Fatalf("FILE* __index = %v", index)
	} else {
		table, _ := index.Table()
		if table != metatable {
			t.Fatal("FILE* __index is not the metatable itself")
		}
	}
	if library.RawGetString("popen").Kind() != NilKind {
		t.Fatal("OpenIO published an unimplemented popen stub")
	}

	stdin := ioFileField(t, library, "stdin")
	stdout := ioFileField(t, library, "stdout")
	stderr := ioFileField(t, library, "stderr")
	for name, data := range map[string]*UserData{
		"stdin":  stdin,
		"stdout": stdout,
		"stderr": stderr,
	} {
		if !isManagedUserDataClass(data, &fileResourceClass) {
			t.Fatalf("io.%s is not a canonical file", name)
		}
		if data.Data() != nil {
			t.Fatalf("io.%s exposed its runtime payload", name)
		}
		if err := data.SetData("replacement"); !errors.Is(
			err,
			ErrReadOnlyUserData,
		) {
			t.Fatalf("io.%s SetData = %v", name, err)
		}
		lease, open := acquireManagedResource(data)
		if !open || lease.owned {
			t.Fatalf("io.%s resource = (open %v, owned %v)", name, open, lease.owned)
		}
		lease.release()
	}

	inputFunction := ioFunctionField(t, library, "input")
	outputFunction := ioFunctionField(t, library, "output")
	closeFunction := ioFunctionField(t, library, "close")
	environment, err := state.FunctionEnvironment(inputFunction)
	if err != nil {
		t.Fatal(err)
	}
	for name, function := range map[string]*Function{
		"output": outputFunction,
		"close":  closeFunction,
		"open":   ioFunctionField(t, library, "open"),
		"type":   ioFunctionField(t, library, "type"),
	} {
		got, environmentErr := state.FunctionEnvironment(function)
		if environmentErr != nil {
			t.Fatal(environmentErr)
		}
		if got != environment {
			t.Fatalf("io.%s does not share the private environment", name)
		}
	}
	if value, found := environment.rawIntSlot(ioDefaultInput); !found ||
		value.ref != slotFromValue(stdin.Value()).ref {
		t.Fatal("private input default is not io.stdin")
	}
	if value, _ := environment.rawIntSlot(ioDefaultOutput); value.ref !=
		slotFromValue(stdout.Value()).ref {
		t.Fatal("private output default is not io.stdout")
	}

	inputLease, _ := acquireManagedResource(stdin)
	inputHandle := inputLease.value.(*fileHandle)
	if inputHandle.input != &state.streams.stdin {
		t.Fatal("io.stdin does not share the State input cursor")
	}
	inputLease.release()
	outputLease, _ := acquireManagedResource(stdout)
	outputHandle := outputLease.value.(*fileHandle)
	if outputHandle.output != &state.streams.stdout {
		t.Fatal("io.stdout does not share the State output endpoint")
	}
	outputLease.release()
	errorLease, _ := acquireManagedResource(stderr)
	errorHandle := errorLease.value.(*fileHandle)
	if errorHandle.output != &state.streams.stderr {
		t.Fatal("io.stderr does not share the State error endpoint")
	}
	errorLease.release()

	results := runIOChunk(t, state, `
return io.type(io.stdin),io.type(io.stdout),io.type(io.stderr),
	io.type(nil),io.type({}),tostring(io.stdin)
`)
	assertTestValues(
		t,
		results[:5],
		state.String("file"),
		state.String("file"),
		state.String("file"),
		Nil(),
		Nil(),
	)
	text, ok := results[5].AsString()
	if !ok || !strings.HasPrefix(text, "file (0x") {
		t.Fatalf("standard file tostring = %v", results[5])
	}
}

func TestIOOpenCloseTypeAndDiagnostics(t *testing.T) {
	state := newStateWithIO(t, Options{})
	defer state.Close()
	path := filepath.Join(t.TempDir(), "opened.lua")

	results := runIOChunk(t, state, `
local file,message,code=io.open(`+luaTestQuote(path)+`,"w")
local before=io.type(file)
local text=tostring(file)
local closed=file:close()
local after=io.type(file)
local closedText=tostring(file)
local againOK,againError=pcall(file.close,file)
local standardResult,standardMessage=io.stdin:close()
local nilOK,nilError=pcall(io.close,nil)
return file,message,code,before,text,closed,after,closedText,
	againOK,againError,standardResult,standardMessage,
	io.type(io.stdin),nilOK,nilError
`)
	if len(results) != 15 {
		t.Fatalf("result count = %d, want 15", len(results))
	}
	data, ok := results[0].UserData()
	if !ok {
		t.Fatalf("io.open result = %v", results[0])
	}
	openFunction := ioFunctionField(
		t,
		ioLibraryTable(t, state),
		"open",
	)
	privateEnvironment, err := state.FunctionEnvironment(openFunction)
	if err != nil {
		t.Fatal(err)
	}
	if data.environment != privateEnvironment {
		t.Fatal("regular file does not share the IO private environment")
	}
	assertTestValues(
		t,
		results[1:4],
		Nil(),
		Nil(),
		state.String("file"),
	)
	openText, _ := results[4].AsString()
	if !strings.HasPrefix(openText, "file (0x") {
		t.Fatalf("open tostring = %q", openText)
	}
	assertTestValues(
		t,
		results[5:9],
		Bool(true),
		state.String("closed file"),
		state.String("file (closed)"),
		Bool(false),
	)
	againError, _ := results[9].AsString()
	if !strings.Contains(
		againError,
		"attempt to use a closed file",
	) {
		t.Fatalf("second close error = %q", againError)
	}
	assertTestValues(
		t,
		results[10:14],
		Nil(),
		state.String("cannot close standard file"),
		state.String("file"),
		Bool(false),
	)
	nilError, _ := results[14].AsString()
	if !strings.Contains(
		nilError,
		"(FILE* expected, got nil)",
	) {
		t.Fatalf("io.close(nil) error = %q", nilError)
	}
	if _, open := acquireManagedResource(data); open {
		t.Fatal("closed file resource remains available")
	}

	missing := runIOChunk(t, state, `
local ok,message=pcall(io.type)
return ok,message
`)
	if value, _ := missing[0].AsBool(); value {
		t.Fatal("io.type() accepted a missing value")
	}
	missingMessage, _ := missing[1].AsString()
	if !strings.Contains(
		missingMessage,
		"(value expected)",
	) {
		t.Fatalf("io.type() error = %q", missingMessage)
	}
}

func TestIOFileClassificationCannotBeForged(t *testing.T) {
	state := newStateWithIO(t, Options{})
	defer state.Close()
	library := ioLibraryTable(t, state)
	metatable := fileMetatable(t, state)

	unrelated, err := state.NewUserData("host")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		unrelated.Value(),
		metatable,
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("forged_file", unrelated.Value()); err != nil {
		t.Fatal(err)
	}
	results := runIOChunk(t, state, `
local ok,message=pcall(io.close,forged_file)
return io.type(forged_file),ok,message
`)
	assertTestValues(t, results[:2], Nil(), Bool(false))
	message, _ := results[2].AsString()
	if !strings.Contains(message, "FILE* expected, got userdata") {
		t.Fatalf("forged close error = %q", message)
	}

	stdin := ioFileField(t, library, "stdin")
	other, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(stdin.Value(), other); err != nil {
		t.Fatal(err)
	}
	results = runIOChunk(t, state, `return io.type(io.stdin)`)
	assertTestValues(t, results, Nil())
	if err := state.SetMetatable(stdin.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	results = runIOChunk(t, state, `return io.type(io.stdin)`)
	assertTestValues(t, results, state.String("file"))
}

func TestIOOpenModesAndFailureTuples(t *testing.T) {
	for _, test := range []struct {
		mode string
		ok   bool
	}{
		{mode: "r", ok: true},
		{mode: "rb", ok: true},
		{mode: "r+", ok: true},
		{mode: "rb+", ok: true},
		{mode: "r+b", ok: true},
		{mode: "w", ok: true},
		{mode: "wb", ok: true},
		{mode: "w+", ok: true},
		{mode: "a", ok: true},
		{mode: "ab+", ok: true},
		{mode: "", ok: false},
		{mode: "z", ok: false},
		{mode: "br", ok: false},
		{mode: "r++", ok: false},
		{mode: "rbb", ok: false},
		{mode: "rw", ok: false},
	} {
		_, got := fileOpenFlags(test.mode)
		if got != test.ok {
			t.Errorf("fileOpenFlags(%q) valid = %v; want %v", test.mode, got, test.ok)
		}
	}

	state := newStateWithIO(t, Options{})
	defer state.Close()
	directory := t.TempDir()
	path := filepath.Join(directory, "mode-file")
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := state.SetGlobal(
		"nul_path",
		state.String(path+"\x00ignored"),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal(
		"nul_mode",
		state.String("w\x00ignored"),
	); err != nil {
		t.Fatal(err)
	}
	results := runIOChunk(t, state, `
local file,message,code=io.open(nul_path,nul_mode)
return file,message,code
`)
	data, ok := results[0].UserData()
	if !ok {
		t.Fatalf("NUL-truncated open = %v, %v, %v", results[0], results[1], results[2])
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if info.Size() != 0 {
		t.Fatalf("w mode left size %d; want 0", info.Size())
	}
	if first, err := closeManagedResource(data); err != nil || !first {
		t.Fatalf("close truncated-mode file = (%v, %v)", first, err)
	}

	invalid := runIOChunk(t, state, `
return io.open(`+luaTestQuote(path)+`,"invalid")
`)
	assertTestValues(t, invalid[:1], Nil())
	invalidMessage, _ := invalid[1].AsString()
	if !strings.HasPrefix(invalidMessage, path+": ") {
		t.Fatalf("invalid-mode message = %q", invalidMessage)
	}
	if code, _ := invalid[2].AsNumber(); code != float64(syscall.EINVAL) {
		t.Fatalf("invalid-mode errno = %v; want %d", invalid[2], syscall.EINVAL)
	}

	missingPath := filepath.Join(directory, "missing", "file")
	missing := runIOChunk(t, state, `
return io.open(`+luaTestQuote(missingPath)+`)
`)
	assertTestValues(t, missing[:1], Nil())
	missingMessage, _ := missing[1].AsString()
	if !strings.HasPrefix(missingMessage, missingPath+": ") {
		t.Fatalf("missing-file message = %q", missingMessage)
	}
	if code, ok := missing[2].AsNumber(); !ok || code == 0 {
		t.Fatalf("missing-file errno = %v", missing[2])
	}
}

func TestIOAppendModeUsesOperatingSystemAppend(t *testing.T) {
	state := newStateWithIO(t, Options{})
	defer state.Close()
	path := filepath.Join(t.TempDir(), "append")
	if err := os.WriteFile(path, []byte("begin"), 0o600); err != nil {
		t.Fatal(err)
	}

	results := runIOChunk(t, state, `
return io.open(`+luaTestQuote(path)+`,"a+")
`)
	data, ok := results[0].UserData()
	if !ok {
		t.Fatalf("io.open(a+) = %v", results)
	}
	lease, open := acquireManagedResource(data)
	if !open {
		t.Fatal("a+ handle is closed")
	}
	handle := lease.value.(*fileHandle)
	if _, err := handle.seeker.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.output.WriteString("-end"); err != nil {
		t.Fatal(err)
	}
	if err := handle.output.Flush(); err != nil {
		t.Fatal(err)
	}
	lease.release()
	if _, err := closeManagedResource(data); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "begin-end" {
		t.Fatalf("append content = %q", content)
	}
}

func TestIODefaultsAreLuaStateAndSurviveReopening(t *testing.T) {
	state := newStateWithIO(t, Options{})
	defer state.Close()
	metatable := fileMetatable(t, state)
	oldOutputPath := filepath.Join(t.TempDir(), "old-output")

	runIOChunk(t, state, `
old_io=io
old_input=io.input
old_close=io.close
old_output=io.output(`+luaTestQuote(oldOutputPath)+`)
`)
	oldLibrary := ioLibraryTable(t, state)
	oldInput := ioFunctionField(t, oldLibrary, "input")
	oldEnvironment, err := state.FunctionEnvironment(oldInput)
	if err != nil {
		t.Fatal(err)
	}

	if err := state.OpenIO(); err != nil {
		t.Fatal(err)
	}
	newLibrary := ioLibraryTable(t, state)
	newInput := ioFunctionField(t, newLibrary, "input")
	newEnvironment, err := state.FunctionEnvironment(newInput)
	if err != nil {
		t.Fatal(err)
	}
	if oldLibrary == newLibrary || oldEnvironment == newEnvironment {
		t.Fatal("OpenIO reused the library or its private defaults")
	}
	if fileMetatable(t, state) != metatable {
		t.Fatal("OpenIO replaced the registry FILE* metatable")
	}

	results := runIOChunk(t, state, `
local closed=old_close()
return old_io~=io,old_io.stdin~=io.stdin,
	old_input()==old_io.stdin,io.input()==io.stdin,
	io.type(old_output),io.type(io.stdout),closed,
	old_io.output()==old_output
`)
	assertTestValues(
		t,
		results,
		Bool(true),
		Bool(true),
		Bool(true),
		Bool(true),
		state.String("closed file"),
		state.String("file"),
		Bool(true),
		Bool(true),
	)
}

func TestIODefaultFilenameFailuresRaiseAndPreserveDefaults(t *testing.T) {
	state := newStateWithIO(t, Options{})
	defer state.Close()
	missing := filepath.Join(t.TempDir(), "missing", "file")
	results := runIOChunk(t, state, `
local beforeInput=io.input()
local beforeOutput=io.output()
local inputOK,inputError=pcall(io.input,`+luaTestQuote(missing)+`)
local outputOK,outputError=pcall(io.output,`+luaTestQuote(missing)+`)
local wrongOK,wrongError=pcall(io.input,{})
return inputOK,inputError,outputOK,outputError,wrongOK,wrongError,
	io.input()==beforeInput,io.output()==beforeOutput
`)
	assertTestValues(
		t,
		[]Value{results[0], results[2], results[4], results[6], results[7]},
		Bool(false),
		Bool(false),
		Bool(false),
		Bool(true),
		Bool(true),
	)
	for _, index := range []int{1, 3} {
		message, _ := results[index].AsString()
		if !strings.Contains(message, "bad argument #1") ||
			!strings.Contains(message, missing+": ") {
			t.Fatalf("filename failure %d = %q", index, message)
		}
	}
	wrong, _ := results[5].AsString()
	if !strings.Contains(wrong, "FILE* expected, got table") {
		t.Fatalf("wrong default input = %q", wrong)
	}
}

func TestIOOwnedFilesCloseWithStateWhileStandardsStayBorrowed(t *testing.T) {
	output := &ioCloseTrackingWriter{}
	state := newStateWithIO(t, Options{Stdout: output})
	path := filepath.Join(t.TempDir(), "owned")
	results := runIOChunk(t, state, `
return io.open(`+luaTestQuote(path)+`,"w")
`)
	data, ok := results[0].UserData()
	if !ok {
		t.Fatalf("owned open = %v", results)
	}
	lease, open := acquireManagedResource(data)
	if !open {
		t.Fatal("owned file was not open")
	}
	handle := lease.value.(*fileHandle)
	file := handle.closer.(*os.File)
	lease.release()

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if _, open := acquireManagedResource(data); open {
		t.Fatal("State.Close left owned file resource open")
	}
	if _, err := file.Write([]byte("x")); err == nil {
		t.Fatal("State.Close did not close the operating-system file")
	}
	if output.closeCount != 0 {
		t.Fatalf(
			"State.Close closed the borrowed standard writer %d times",
			output.closeCount,
		)
	}
}

func newStateWithIO(t testing.TB, options Options) *State {
	t.Helper()
	state := newStateWithBase(t, options)
	if err := state.OpenIO(); err != nil {
		state.Close()
		t.Fatal(err)
	}
	return state
}

func runIOChunk(t *testing.T, state *State, source string) []Value {
	t.Helper()
	chunk := mustLoadString(t, state, "=io-test", source)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	return results
}

func ioLibraryTable(t testing.TB, state *State) *Table {
	t.Helper()
	value, err := state.Global("io")
	if err != nil {
		t.Fatal(err)
	}
	library, ok := value.Table()
	if !ok {
		t.Fatalf("global io = %v", value)
	}
	return library
}

func fileMetatable(t testing.TB, state *State) *Table {
	t.Helper()
	value := state.registry.RawGetString(luaFileHandleRegistryKey)
	metatable, ok := value.Table()
	if !ok {
		t.Fatalf("registry FILE* = %v", value)
	}
	return metatable
}

func ioFileField(t testing.TB, library *Table, name string) *UserData {
	t.Helper()
	value := library.RawGetString(name)
	data, ok := value.UserData()
	if !ok {
		t.Fatalf("io.%s = %v", name, value)
	}
	return data
}

func ioFunctionField(t testing.TB, library *Table, name string) *Function {
	t.Helper()
	value := library.RawGetString(name)
	function, ok := value.Function()
	if !ok {
		t.Fatalf("io.%s = %v", name, value)
	}
	return function
}

func luaTestQuote(text string) string {
	return fmt.Sprintf("%q", text)
}
