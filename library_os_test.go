package lua

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestOpenOSInstallsFreshCanonicalLibrary(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	before, err := state.Global("os")
	if err != nil {
		t.Fatal(err)
	}
	if !before.IsNil() {
		t.Fatalf("new state os = %v; want nil", before)
	}
	loadedBeforeOpen := mustLoadString(
		t,
		state,
		"@open-os.lua",
		`return os.difftime(9, 4)`,
	)
	if err := state.OpenOS(); err != nil {
		t.Fatal(err)
	}

	libraryValue, err := state.Global("os")
	if err != nil {
		t.Fatal(err)
	}
	library, ok := libraryValue.Table()
	if !ok {
		t.Fatalf("os = %v; want table", libraryValue)
	}
	want := make(map[string]Kind, len(osLibraryFunctions))
	previous := make(map[string]Value, len(osLibraryFunctions))
	for _, definition := range osLibraryFunctions {
		want[definition.name] = FunctionKind
		previous[definition.name] = library.RawGetString(definition.name)
	}
	assertTableSurface(t, library, want)

	results, err := state.Call(loadedBeforeOpen.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(5))

	if err := state.SetGlobal("os", Number(1)); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenOS(); err != nil {
		t.Fatal(err)
	}
	reopenedValue, err := state.Global("os")
	if err != nil {
		t.Fatal(err)
	}
	reopened, ok := reopenedValue.Table()
	if !ok {
		t.Fatalf("reopened os = %v; want table", reopenedValue)
	}
	if same, applicable := libraryValue.SameObject(
		reopenedValue,
	); !applicable || same {
		t.Fatal("reopening did not replace the os table")
	}
	for name, old := range previous {
		current := reopened.RawGetString(name)
		if same, applicable := old.SameObject(
			current,
		); !applicable || same {
			t.Fatalf("reopened os.%s is not a fresh Function", name)
		}
	}

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenOS(); !errors.Is(err, ErrClosed) {
		t.Fatalf("OpenOS after Close = %v; want ErrClosed", err)
	}
}

func TestOSLibraryEnvironmentAndFilesystemOperations(t *testing.T) {
	const environmentName = "BADGER_LUA_OS_TEST_VALUE"
	t.Setenv(environmentName, "")

	state := newStateWithOS(t)
	defer state.Close()
	chunk := mustLoadString(t, state, "@environment.lua", `
local empty = os.getenv("BADGER_LUA_OS_TEST_VALUE")
local missing = os.getenv("BADGER_LUA_OS_TEST_MISSING")
local truncated = os.getenv("BADGER_LUA_OS_TEST_VALUE\000ignored")
return empty, missing, truncated
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, state.String(""), Nil(), state.String(""))

	directory := t.TempDir()
	from := filepath.Join(directory, "from")
	to := filepath.Join(directory, "to")
	if err := os.WriteFile(from, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := "return os.rename(" + strconv.Quote(from) +
		", " + strconv.Quote(to) + "), os.remove(" +
		strconv.Quote(to) + ")"
	chunk = mustLoadString(t, state, "@filesystem.lua", source)
	results, err = state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true), Bool(true))
	if _, err := os.Stat(to); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed file stat = %v; want not exist", err)
	}

	missing := filepath.Join(directory, "missing")
	source = "return os.rename(" + strconv.Quote(missing) +
		", " + strconv.Quote(to) + ")"
	chunk = mustLoadString(t, state, "@rename-failure.lua", source)
	results, err = state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || !results[0].IsNil() {
		t.Fatalf("rename failure = %v; want nil, message, errno", results)
	}
	message, ok := results[1].AsString()
	if !ok || !strings.HasPrefix(message, missing+": ") ||
		strings.Contains(message, "rename ") {
		t.Fatalf("rename failure message = %q", message)
	}
	code, ok := results[2].AsNumber()
	if !ok || code == 0 {
		t.Fatalf("rename failure errno = %v", results[2])
	}
}

func TestOSLibraryTemporaryNameCreatesClosedFile(t *testing.T) {
	state := newStateWithOS(t)
	defer state.Close()
	chunk := mustLoadString(
		t,
		state,
		"@tmpname.lua",
		`return os.tmpname()`,
	)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("tmpname results = %v", results)
	}
	name, ok := results[0].AsString()
	if !ok {
		t.Fatalf("tmpname = %v; want string", results[0])
	}
	defer os.Remove(name)
	file, err := os.OpenFile(name, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("temporary file is not closed and reopenable: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOSExecuteQueryAndArgumentContract(t *testing.T) {
	state := newStateWithOS(t)
	defer state.Close()
	chunk := mustLoadString(t, state, "@execute-contract.lua", `
local queryCount = select("#", os.execute())
local query = os.execute()
local nilQuery = os.execute(nil)
local ok, message = pcall(function() os.execute({}) end)
return queryCount, query, nilQuery, ok, message
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	available := Number(0)
	if hostShellAvailable() {
		available = Number(1)
	}
	if len(results) != 5 {
		t.Fatalf("execute contract results = %v", results)
	}
	assertTestValues(
		t,
		results[:4],
		Number(1),
		available,
		available,
		Bool(false),
	)
	message, ok := results[4].AsString()
	if !ok || !strings.Contains(
		message,
		"bad argument #1 to 'execute' (string expected, got table)",
	) {
		t.Fatalf("execute argument failure = %q", message)
	}

	missing := exec.Command(
		filepath.Join(t.TempDir(), "missing-command-processor"),
	)
	if process, startErr := startChildProcess(missing); startErr == nil ||
		process != nil {
		t.Fatalf("unstartable process = (%v, %v)", process, startErr)
	}
}

func TestOSExitReturnsInspectableRequestAndLeavesStateReusable(
	t *testing.T,
) {
	state := newStateWithOS(t)
	chunk := mustLoadString(t, state, "@exit.lua", `
before_exit = true
os.exit(23)
after_exit = true
`)
	destination := []Value{Number(80), Number(81)}
	count, err := state.CallInto(chunk.Value(), nil, destination)
	failure, request := requireExitRequest(t, err, 23)
	if count != 0 {
		t.Fatalf("exit result count = %d; want 0", count)
	}
	assertTestValues(t, destination, Number(80), Number(81))
	if !strings.Contains(
		failure.Error(),
		"exit.lua:3: exit requested with status 23",
	) {
		t.Fatalf("exit description = %q", failure.Error())
	}
	errorText, ok := failure.Value().AsString()
	if !ok || errorText != failure.Error() {
		t.Fatalf(
			"exit value = (%q, %v); want positioned description",
			errorText,
			ok,
		)
	}
	trace := failure.Traceback()
	foundSource := false
	for _, entry := range trace {
		if entry.Source == "@exit.lua" {
			foundSource = true
			break
		}
	}
	if !foundSource {
		t.Fatalf("exit traceback = %+v; want @exit.lua", trace)
	}
	if request.Error() != "lua: exit requested with status 23" {
		t.Fatalf("request description = %q", request.Error())
	}

	before, err := state.Global("before_exit")
	if err != nil {
		t.Fatal(err)
	}
	after, err := state.Global("after_exit")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, before, Bool(true))
	assertTestValue(t, after, Nil())

	recovery := mustLoadString(t, state, "@after-exit.lua", `return 42`)
	results, err := state.Call(recovery.Value())
	if err != nil {
		t.Fatalf("call after exit request: %v", err)
	}
	assertTestValues(t, results, Number(42))
	assertRootThreadReady(t, state.main)

	description := failure.Error()
	value := failure.Value()
	trace = failure.Traceback()
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if failure.Error() != description ||
		request.ExitCode() != 23 ||
		!value.Valid() ||
		len(failure.Traceback()) != len(trace) {
		t.Fatal("exit request changed after State.Close")
	}
}

func TestOSExitStatusConversionAndArgumentFailure(t *testing.T) {
	state := newStateWithOS(t)
	defer state.Close()

	tests := []struct {
		name   string
		source string
		want   int
	}{
		{name: "omitted", source: `os.exit()`, want: 0},
		{name: "nil", source: `os.exit(nil)`, want: 0},
		{name: "numeric string", source: `os.exit("7.9")`, want: 7},
		{name: "fraction", source: `os.exit(-7.9)`, want: -7},
		{name: "ordinary status", source: `os.exit(300)`, want: 300},
		{
			name:   "positive saturation",
			source: `os.exit(1e100)`,
			want:   2147483647,
		},
		{
			name:   "negative saturation",
			source: `os.exit(-1e100)`,
			want:   -2147483648,
		},
		{name: "defined NaN", source: `os.exit(0/0)`, want: 0},
		{name: "extra arguments", source: `os.exit(12, 99)`, want: 12},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			chunk := mustLoadString(
				t,
				state,
				"@exit-status.lua",
				test.source,
			)
			_, err := state.Call(chunk.Value())
			requireExitRequest(t, err, test.want)
			assertRootThreadReady(t, state.main)
		})
	}

	chunk := mustLoadString(t, state, "@exit-argument.lua", `
local function invoke()
	os.exit({})
end
local ok, message = pcall(invoke)
return ok, message
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("argument failure results = %v", results)
	}
	assertTestValue(t, results[0], Bool(false))
	message, ok := results[1].AsString()
	if !ok || !strings.Contains(
		message,
		"bad argument #1 to 'exit' (number expected, got table)",
	) {
		t.Fatalf("exit argument failure = %q", message)
	}
}

func TestOSExitBypassesLuaProtectionAndCoroutines(t *testing.T) {
	state := newStateWithOS(t)
	defer state.Close()

	tests := []struct {
		name      string
		source    string
		code      int
		untouched string
	}{
		{
			name: "pcall",
			source: `
pcall_continued = false
pcall(function() os.exit(31) end)
pcall_continued = true
`,
			code:      31,
			untouched: "pcall_continued",
		},
		{
			name: "xpcall target",
			source: `
xpcall_handler_calls = 0
xpcall_continued = false
xpcall(
	function() os.exit(32) end,
	function() xpcall_handler_calls = xpcall_handler_calls + 1 end
)
xpcall_continued = true
`,
			code:      32,
			untouched: "xpcall_continued",
		},
		{
			name: "xpcall handler",
			source: `
xpcall_handler_continued = false
xpcall(
	function() error("ordinary") end,
	function() os.exit(33) end
)
xpcall_handler_continued = true
`,
			code:      33,
			untouched: "xpcall_handler_continued",
		},
		{
			name: "coroutine resume",
			source: `
exit_coroutine = coroutine.create(function() os.exit(34) end)
resume_continued = false
coroutine.resume(exit_coroutine)
resume_continued = true
`,
			code:      34,
			untouched: "resume_continued",
		},
		{
			name: "coroutine wrap",
			source: `
wrap_continued = false
coroutine.wrap(function() os.exit(35) end)()
wrap_continued = true
`,
			code:      35,
			untouched: "wrap_continued",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			chunk := mustLoadString(
				t,
				state,
				"@protected-exit.lua",
				test.source,
			)
			_, err := state.Call(chunk.Value())
			requireExitRequest(t, err, test.code)
			value, globalErr := state.Global(test.untouched)
			if globalErr != nil {
				t.Fatal(globalErr)
			}
			assertTestValue(t, value, Bool(false))
			assertRootThreadReady(t, state.main)
		})
	}

	handlerCalls, err := state.Global("xpcall_handler_calls")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, handlerCalls, Number(0))
	coroutineValue, err := state.Global("exit_coroutine")
	if err != nil {
		t.Fatal(err)
	}
	coroutine, ok := coroutineValue.Thread()
	if !ok || coroutine.Status() != ThreadDead {
		t.Fatalf("exit coroutine = (%v, %v); want dead", coroutine, ok)
	}
}

func TestOSExitPropagatesAcrossLoadAndNativeCallBoundaries(t *testing.T) {
	state := newStateWithOS(t)
	defer state.Close()

	load := mustLoadString(t, state, "@load-exit.lua", `
load(function() os.exit(41) end)
load_continued = true
`)
	_, err := state.Call(load.Value())
	requireExitRequest(t, err, 41)
	continued, err := state.Global("load_continued")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, continued, Nil())

	exit := mustLoadString(t, state, "@nested-exit.lua", `os.exit(42)`)
	followup := mustLoadString(t, state, "@nested-followup.lua", `
native_followup_ran = true
`)
	var nestedFailure error
	var followupFailure error
	var invalidFailure error
	native, err := state.NewNativeFunction(func(frame Frame) Outcome {
		_, nestedFailure = frame.Call(exit.Value())
		_, followupFailure = frame.Call(followup.Value())
		_, invalidFailure = frame.Call(Value{})
		return frame.ReturnString("ignored")
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(native.Value())
	if results != nil {
		t.Fatalf("native exit results = %v; want nil", results)
	}
	failure, _ := requireExitRequest(t, err, 42)
	if nestedFailure != failure ||
		followupFailure != failure ||
		invalidFailure != failure {
		t.Fatalf(
			"nested failures = (%p, %p, %p); want first request %p",
			nestedFailure,
			followupFailure,
			invalidFailure,
			failure,
		)
	}
	followupRan, err := state.Global("native_followup_ran")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, followupRan, Nil())

	invalid, err := state.NewNativeFunction(func(frame Frame) Outcome {
		_, _ = frame.Call(exit.Value())
		return Outcome{}
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = state.Call(invalid.Value())
	requireExitRequest(t, err, 42)
	assertRootThreadReady(t, state.main)
}

func TestOSExitAtExternalCoroutineAndContextBoundaries(t *testing.T) {
	state := newStateWithOS(t)
	defer state.Close()

	entry := mustLoadString(t, state, "@resume-exit.lua", `os.exit(51)`)
	thread, err := state.NewThread(entry.Value())
	if err != nil {
		t.Fatal(err)
	}
	destination := []Value{Number(90)}
	count, status, err := thread.ResumeInto(nil, destination)
	requireExitRequest(t, err, 51)
	if count != 0 || status != ThreadDead {
		t.Fatalf(
			"exit resume = (count=%d, status=%v); want (0, dead)",
			count,
			status,
		)
	}
	assertTestValues(t, destination, Number(90))

	contextExit := mustLoadString(
		t,
		state,
		"@context-exit.lua",
		`os.exit(52)`,
	)
	count, err = state.CallIntoContext(
		context.Background(),
		contextExit.Value(),
		nil,
		destination,
	)
	requireExitRequest(t, err, 52)
	if count != 0 {
		t.Fatalf("context exit count = %d; want 0", count)
	}
	assertTestValues(t, destination, Number(90))

	recovery := mustLoadString(t, state, "@exit-recovery.lua", `return 53`)
	results, err := state.Call(recovery.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(53))
}

func TestOSExitAndCancellationUseFirstObservedHostControl(t *testing.T) {
	t.Run("cancellation before exit", func(t *testing.T) {
		state := newStateWithOS(t)
		defer state.Close()
		exit := mustLoadString(
			t,
			state,
			"@cancel-before-exit.lua",
			`os.exit(61)`,
		)
		ctx, cancel := context.WithCancel(context.Background())
		native, err := state.NewNativeFunction(func(frame Frame) Outcome {
			cancel()
			_, nestedErr := frame.Call(exit.Value())
			var failure *Error
			if !errors.As(nestedErr, &failure) {
				panic("nested cancellation did not return *Error")
			}
			return frame.RaiseError(failure)
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = state.CallContext(ctx, native.Value())
		var failure *Error
		if !errors.As(err, &failure) ||
			failure.Category() != ContextError ||
			!errors.Is(err, context.Canceled) {
			t.Fatalf("cancel-before-exit error = %#v", err)
		}
		var request *ExitRequest
		if errors.As(err, &request) {
			t.Fatalf("cancel-before-exit exposed request %+v", request)
		}
	})

	t.Run("exit before cancellation poll", func(t *testing.T) {
		state := newStateWithOS(t)
		defer state.Close()
		exit := mustLoadString(
			t,
			state,
			"@exit-before-cancel.lua",
			`os.exit(62)`,
		)
		ctx, cancel := context.WithCancel(context.Background())
		native, err := state.NewNativeFunction(func(frame Frame) Outcome {
			_, _ = frame.Call(exit.Value())
			cancel()
			return frame.Return()
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = state.CallContext(ctx, native.Value())
		requireExitRequest(t, err, 62)
		if errors.Is(err, context.Canceled) {
			t.Fatalf("exit-before-cancel error = %#v; cancellation won", err)
		}
	})
}

func TestOSLibraryCoreMatchesLua51(t *testing.T) {
	runLua51Cases(t, osLibraryLua51Cases)
}

func requireExitRequest(
	t testing.TB,
	err error,
	wantCode int,
) (*Error, *ExitRequest) {
	t.Helper()
	if err == nil {
		t.Fatal("call succeeded; want exit request")
	}
	var failure *Error
	if !errors.As(err, &failure) || failure.Category() != ExitError {
		t.Fatalf("exit error = %#v; want categorized *Error", err)
	}
	var request *ExitRequest
	if !errors.As(err, &request) {
		t.Fatalf("exit error = %#v; want *ExitRequest cause", err)
	}
	if request.ExitCode() != wantCode {
		t.Fatalf(
			"exit status = %d; want %d",
			request.ExitCode(),
			wantCode,
		)
	}
	return failure, request
}

func newStateWithOS(t *testing.T) *State {
	t.Helper()
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.OpenBase(); err != nil {
		state.Close()
		t.Fatal(err)
	}
	if err := state.OpenOS(); err != nil {
		state.Close()
		t.Fatal(err)
	}
	return state
}

var osLibraryLua51Cases = []lua51Case{
	{
		name: "date formats the ISO C locale surface",
		source: `return os.date(` +
			`"!%a|%A|%b|%B|%c|%d|%H|%I|%j|%m|%M|%p|` +
			`%S|%U|%w|%W|%x|%X|%y|%Y|%%", 0)`,
		want: "ok 'Thu|Thursday|Jan|January|" +
			"Thu Jan  1 00:00:00 1970|01|00|12|001|01|00|" +
			"AM|00|00|4|00|01/01/70|00:00:00|70|1970|%'",
	},
	{
		name: "date returns the UTC calendar table",
		source: `
local t = os.date("!*t", 0)
return t.sec, t.min, t.hour, t.day, t.month, t.year,
  t.wday, t.yday, t.isdst
`,
		want: "ok 0 0 0 1 1 1970 5 1 false",
	},
	{
		name:   "date strips only one UTC prefix",
		source: `return os.date("!!%Y", 0)`,
		want:   "ok '!1970'",
	},
	{
		name:   "date requires an exact table format",
		source: `return os.date("!*tX", 0)`,
		want:   "ok '*tX'",
	},
	{
		name:   "date retains a trailing percent",
		source: `return os.date("!abc%", 0)`,
		want:   "ok 'abc%'",
	},
	{
		name:   "date format stops at an embedded NUL",
		source: `return os.date("!abc\000%Y", 0)`,
		want:   "ok 'abc'",
	},
	{
		name:   "time requires a table",
		source: `return os.time("table")`,
		want: "error 'case:1: bad argument #1 to 'time' " +
			"(table expected, got string)'",
	},
	{
		name:   "time reports a missing required field",
		source: `return os.time({})`,
		want:   "error 'case:1: field 'day' missing in date table'",
	},
	{
		name:   "setlocale rejects an unknown category",
		source: `return os.setlocale(nil, "badger")`,
		want: "error 'case:1: bad argument #2 to 'setlocale' " +
			"(invalid option 'badger')'",
	},
	{
		name:   "setlocale exposes the C locale",
		source: `return os.setlocale("C", "numeric")`,
		want:   "ok 'C'",
	},
	{
		name:   "difftime truncates both operands to time_t",
		source: `return os.difftime(12.9, 2.9)`,
		want:   "ok 10",
	},
	{
		name:   "difftime defaults its second operand",
		source: `return os.difftime("12")`,
		want:   "ok 12",
	},
	{
		name:   "difftime reports its first argument",
		source: `return os.difftime()`,
		want: "error 'case:1: bad argument #1 to 'difftime' " +
			"(number expected, got no value)'",
	},
	{
		name: "execute queries the command processor",
		source: `
local count = select("#", os.execute())
return os.execute() ~= 0, os.execute(nil, "ignored") ~= 0, count
`,
		want: "ok true true 1",
	},
	{
		name:   "execute requires a string command",
		source: `return os.execute({})`,
		want: "error 'case:1: bad argument #1 to 'execute' " +
			"(string expected, got table)'",
	},
	{
		name:   "getenv requires a string",
		source: `return os.getenv({})`,
		want: "error 'case:1: bad argument #1 to 'getenv' " +
			"(string expected, got table)'",
	},
	{
		name:   "remove requires a string",
		source: `return os.remove()`,
		want: "error 'case:1: bad argument #1 to 'remove' " +
			"(string expected, got no value)'",
	},
	{
		name:   "rename requires its destination",
		source: `return os.rename("from")`,
		want: "error 'case:1: bad argument #2 to 'rename' " +
			"(string expected, got no value)'",
	},
}
