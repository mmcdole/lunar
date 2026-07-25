package lua

import (
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenBaseIsExplicitAndUsesTheGlobalEnvironment(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}

	before, err := state.Global("pcall")
	if err != nil {
		t.Fatal(err)
	}
	if !before.IsNil() {
		t.Fatalf("new state pcall = %v; want nil", before)
	}
	loadedBeforeOpen := mustLoadString(
		t,
		state,
		"@open-base.lua",
		`return pcall(function() return 42 end)`,
	)
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"pcall", "xpcall"} {
		value, globalErr := state.Global(name)
		if globalErr != nil {
			t.Fatal(globalErr)
		}
		if value.Kind() != FunctionKind {
			t.Fatalf("%s = %v; want function", name, value)
		}
	}
	global, err := state.Global("_G")
	if err != nil {
		t.Fatal(err)
	}
	if same, applicable := global.SameObject(state.globals.Value()); !applicable || !same {
		t.Fatal("_G does not identify the canonical global environment")
	}
	version, err := state.Global("_VERSION")
	if err != nil {
		t.Fatal(err)
	}
	if text, ok := version.AsString(); !ok || text != "Lua 5.1" {
		t.Fatalf("_VERSION = (%q, %v); want Lua 5.1", text, ok)
	}

	results, err := state.Call(loadedBeforeOpen.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true), Number(42))

	oldPCall, _ := state.Global("pcall")
	oldXPCall, _ := state.Global("xpcall")
	if err := state.SetGlobal("pcall", Number(1)); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("_G", Nil()); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}
	newPCall, _ := state.Global("pcall")
	newXPCall, _ := state.Global("xpcall")
	for name, pair := range map[string][2]Value{
		"pcall":  {oldPCall, newPCall},
		"xpcall": {oldXPCall, newXPCall},
	} {
		if pair[1].Kind() != FunctionKind {
			t.Fatalf("reopened %s = %v; want function", name, pair[1])
		}
		if same, applicable := pair[0].SameObject(pair[1]); !applicable || same {
			t.Fatalf("reopened %s did not receive a fresh function", name)
		}
	}
	global, _ = state.Global("_G")
	if same, applicable := global.SameObject(state.globals.Value()); !applicable || !same {
		t.Fatal("reopening did not restore _G")
	}

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenBase(); !errors.Is(err, ErrClosed) {
		t.Fatalf("OpenBase after Close = %v; want ErrClosed", err)
	}
}

func TestBasePCallLua51ResultsAndArguments(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	testCases := []struct {
		name   string
		source string
		want   []Value
	}{
		{
			name: "no target results",
			source: `
return pcall(function() end)
`,
			want: []Value{Bool(true)},
		},
		{
			name: "all target results",
			source: `
return pcall(function(...) return ... end, 1, nil, 3, nil)
`,
			want: []Value{
				Bool(true),
				Number(1),
				Nil(),
				Number(3),
				Nil(),
			},
		},
		{
			name: "fixed caller adjustment",
			source: `
local one = pcall(function() return 1, 2 end)
local a, b, c, d = pcall(function() return 9 end)
return one, a, b, c, d
`,
			want: []Value{
				Bool(true),
				Bool(true),
				Number(9),
				Nil(),
				Nil(),
			},
		},
		{
			name: "explicit nil target is protected",
			source: `
return pcall(nil)
`,
			want: []Value{Bool(false)},
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			chunk := mustLoadString(t, state, "@pcall.lua", test.source)
			results, callErr := state.Call(chunk.Value())
			if callErr != nil {
				t.Fatal(callErr)
			}
			if test.name == "explicit nil target is protected" {
				if len(results) != 2 ||
					results[0].Truth() ||
					!strings.Contains(
						results[1].String(),
						"attempt to call a nil value",
					) {
					t.Fatalf("pcall(nil) = %v", results)
				}
				return
			}
			assertTestValues(t, results, test.want...)
		})
	}

	missing := mustLoadString(t, state, "@missing-pcall.lua", `return pcall()`)
	if _, callErr := state.Call(missing.Value()); callErr == nil ||
		callErr.Error() !=
			"missing-pcall.lua:1: bad argument #1 to 'pcall' (value expected)" {
		t.Fatalf("pcall() error = %v", callErr)
	}
}

func TestBasePCallPreservesCallableAndErrorIdentity(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	callable, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	callableBody, err := state.NewNativeFunction(func(frame Frame) Outcome {
		first, _ := frame.Argument(0)
		second, _ := frame.Argument(1)
		return frame.ReturnValues(first, second)
	})
	if err != nil {
		t.Fatal(err)
	}
	metatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__call", callableBody.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(callable.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("callable", callable.Value()); err != nil {
		t.Fatal(err)
	}

	marker, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	raiser, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.Raise(marker.Value())
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("raise_marker", raiser.Value()); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@pcall-identity.lua", `
local callOK, self, argument = pcall(callable, 17)
local raiseOK, value = pcall(raise_marker)
return callOK, self, argument, raiseOK, value
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 5 {
		t.Fatalf("result count = %d; want 5", len(results))
	}
	assertTestValue(t, results[0], Bool(true))
	if same, applicable := results[1].SameObject(callable.Value()); !applicable || !same {
		t.Fatal("__call did not receive the original callable table")
	}
	assertTestValue(t, results[2], Number(17))
	assertTestValue(t, results[3], Bool(false))
	if same, applicable := results[4].SameObject(marker.Value()); !applicable || !same {
		t.Fatal("pcall did not preserve the arbitrary error object")
	}
}

func TestBaseXPCallMatchesLua51HandlerSemantics(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	testCases := []struct {
		name   string
		source string
		want   []Value
	}{
		{
			name: "target gets no arguments and bad handler is ignored",
			source: `
return xpcall(function(...) return 3, ... end, 7, 8, 9)
`,
			want: []Value{Bool(true), Number(3)},
		},
		{
			name: "handler receives the error",
			source: `
return xpcall(
	function() return nil + 1 end,
	function(value) return value end
)
`,
			want: []Value{Bool(false)},
		},
		{
			name: "no handler result becomes nil",
			source: `
return xpcall(
	function() return nil + 1 end,
	function() end
)
`,
			want: []Value{Bool(false), Nil()},
		},
		{
			name: "only first handler result survives",
			source: `
return xpcall(
	function() return nil + 1 end,
	function() return 4, 5 end
)
`,
			want: []Value{Bool(false), Number(4)},
		},
		{
			name: "bad handler fails only when needed",
			source: `
return xpcall(function() return nil + 1 end, 7)
`,
			want: []Value{
				Bool(false),
				state.String("error in error handling"),
			},
		},
		{
			name: "failing handler becomes fixed error",
			source: `
return xpcall(
	function() return nil + 1 end,
	function() return nil + 2 end
)
`,
			want: []Value{
				Bool(false),
				state.String("error in error handling"),
			},
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			chunk := mustLoadString(t, state, "@xpcall.lua", test.source)
			results, callErr := state.Call(chunk.Value())
			if callErr != nil {
				t.Fatal(callErr)
			}
			if test.name == "handler receives the error" {
				if len(results) != 2 ||
					results[0].Truth() ||
					!strings.Contains(
						results[1].String(),
						"attempt to perform arithmetic",
					) {
					t.Fatalf("xpcall transformed error = %v", results)
				}
				return
			}
			assertTestValues(t, results, test.want...)
		})
	}

	missing := mustLoadString(t, state, "@missing-xpcall.lua", `return xpcall(function() end)`)
	if _, callErr := state.Call(missing.Value()); callErr == nil ||
		callErr.Error() !=
			"missing-xpcall.lua:1: bad argument #2 to 'xpcall' (value expected)" {
		t.Fatalf("xpcall without handler error = %v", callErr)
	}
}

func TestBaseXPCallPreservesErrorIdentityAndRejectsCallableHandler(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	marker, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	raiser, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.Raise(marker.Value())
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("raise_marker", raiser.Value()); err != nil {
		t.Fatal(err)
	}

	callableHandler, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	handlerBody, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnString("must not run")
	})
	if err != nil {
		t.Fatal(err)
	}
	handlerMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := handlerMetatable.RawSetString("__call", handlerBody.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(callableHandler.Value(), handlerMetatable); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("callable_handler", callableHandler.Value()); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@xpcall-identity.lua", `
local firstOK, first = xpcall(raise_marker, function(value) return value end)
local secondOK, second = xpcall(raise_marker, callable_handler)
return firstOK, first, secondOK, second
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 4 {
		t.Fatalf("result count = %d; want 4", len(results))
	}
	assertTestValue(t, results[0], Bool(false))
	if same, applicable := results[1].SameObject(marker.Value()); !applicable || !same {
		t.Fatal("xpcall handler did not receive the original error object")
	}
	assertTestValue(t, results[2], Bool(false))
	if message, ok := results[3].AsString(); !ok ||
		message != "error in error handling" {
		t.Fatalf("callable handler failure = %v", results[3])
	}
}

func TestBaseXPCallDiscardsExtraArgumentsBeforeCallingTarget(t *testing.T) {
	const valueLimit = 8
	state := newStateWithBase(t, Options{MaxValues: valueLimit})
	defer state.Close()

	target, err := state.NewNativeFunction(func(frame Frame) Outcome {
		call := frame.activation()
		if frame.depth != 2 {
			return frame.ReturnBool(false)
		}
		xpcall := frame.thread.frames[frame.depth-2]
		expectedBase := int(xpcall.base) + 2
		if int(call.resultBase) != expectedBase {
			return frame.ReturnBool(false)
		}
		for index := int(call.base); index < len(frame.thread.values); index++ {
			if frame.thread.values[index] != (slot{}) {
				return frame.ReturnBool(false)
			}
		}
		return frame.ReturnBool(true)
	})
	if err != nil {
		t.Fatal(err)
	}
	xpcall, err := state.Global("xpcall")
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(
		xpcall,
		target.Value(),
		Nil(),
		Number(1),
		Number(2),
		Number(3),
		Number(4),
		Number(5),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true), Bool(true))
}

func TestProtectedCallsPreserveAnExplicitNilError(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	raiser, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.Raise(Nil())
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnBool(
			frame.ArgumentCount() == 1 &&
				frame.Kind(0) == NilKind,
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("raise_nil", raiser.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("handle_nil", handler.Value()); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@nil-error.lua", `
local firstOK, first = pcall(raise_nil)
local secondOK, second = xpcall(raise_nil, handle_nil)
return firstOK, first, secondOK, second
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Bool(false),
		Nil(),
		Bool(false),
		Bool(true),
	)
}

func newStateWithBase(t testing.TB, options Options) *State {
	t.Helper()
	state, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.OpenBase(); err != nil {
		state.Close()
		t.Fatal(err)
	}
	return state
}

// lua51Case is one recorded differential case. Each source is a complete Lua
// 5.1 chunk and want is the outcome PUC Lua 5.1.5 produces for it, in the
// shared spelling formatLua51Value defines.
//
// Recorded expectations are verified against a real interpreter by
// TestLua51OracleMatchesLibraryCases. Regenerate or re-verify with:
//
//	BADGER_LUA51=/path/to/lua-5.1.5/src/lua go test -run OracleMatches -v
//
// and record new cases with BADGER_LUA51_RECORD=1 set as well.
type lua51Case struct {
	name   string
	source string
	want   string
}

// runLua51Case executes source on a State with every implemented library open
// and reports the outcome the recorded Lua 5.1 driver would print.
func runLua51Case(t *testing.T, source string) string {
	t.Helper()
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := state.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenMath(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenTable(); err != nil {
		t.Fatal(err)
	}
	installTestPrelude(t, state)

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

// formatLua51Value spells one result the way the Lua driver does. Numbers use
// Lua 5.1's own %.14g primitive, with non-finite values normalized because
// their C-library spelling is platform-dependent.
func formatLua51Value(value Value) string {
	switch value.Kind() {
	case NumberKind:
		number, _ := value.AsNumber()
		switch {
		case math.IsNaN(number):
			return "nan"
		case math.IsInf(number, 1):
			return "inf"
		case math.IsInf(number, -1):
			return "-inf"
		}
		return value.String()
	case StringKind:
		text, _ := value.AsString()
		return formatLua51Text(text)
	case NilKind, BoolKind:
		return value.String()
	default:
		return value.Kind().String()
	}
}

func formatLua51Text(text string) string {
	return "'" + text + "'"
}

func runLua51Cases(t *testing.T, cases []lua51Case) {
	t.Helper()
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := runLua51Case(t, test.source); got != test.want {
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

// TestLua51OracleMatchesLibraryCases re-derives every recorded expectation
// from a real Lua 5.1 interpreter. It is skipped unless BADGER_LUA51 names one,
// because the reference binary is deliberately not carried in this repository.
func TestLua51OracleMatchesLibraryCases(t *testing.T) {
	binary := os.Getenv("BADGER_LUA51")
	if binary == "" {
		t.Skip("set BADGER_LUA51 to a Lua 5.1 interpreter to verify")
	}
	cases := make([]lua51Case, 0, len(mathLibraryLua51Cases)+len(tableLibraryLua51Cases))
	cases = append(cases, mathLibraryLua51Cases...)
	cases = append(cases, tableLibraryLua51Cases...)

	driver := &strings.Builder{}
	driver.WriteString(lua51OracleDriver)
	driver.WriteString("local sources = {\n")
	for _, test := range cases {
		driver.WriteString(quoteLuaString(test.source))
		driver.WriteString(",\n")
	}
	driver.WriteString("}\nrun(sources)\n")

	path := filepath.Join(t.TempDir(), "oracle.lua")
	if err := os.WriteFile(path, []byte(driver.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(binary, path).Output()
	if err != nil {
		t.Fatalf("%s: %v", binary, err)
	}
	lines := strings.Split(strings.TrimRight(string(output), "\n"), "\n")
	if len(lines) != len(cases) {
		t.Fatalf("oracle produced %d lines; want %d", len(lines), len(cases))
	}
	record := os.Getenv("BADGER_LUA51_RECORD") != ""
	for index, test := range cases {
		if record {
			t.Logf("{\n\tname:   %q,\n\tsource: %q,\n\twant:   %q,\n},", test.name, test.source, lines[index])
			continue
		}
		if lines[index] != test.want {
			t.Errorf(
				"%s: oracle disagrees with the recorded expectation\n%s\n got: %s\nwant: %s",
				test.name,
				test.source,
				lines[index],
				test.want,
			)
		}
	}
}

// lua51OracleDriver formats results exactly as formatLua51Value does.
const lua51OracleDriver = `
local positiveInfinity = math.huge
local negativeInfinity = -positiveInfinity

local function fmt(v)
  local t = type(v)
  if t == "number" then
    if v ~= v then return "nan" end
    if v == positiveInfinity then return "inf" end
    if v == negativeInfinity then return "-inf" end
    return string.format("%.14g", v)
  elseif t == "string" then
    return "'" .. v .. "'"
  elseif t == "nil" or t == "boolean" then
    return tostring(v)
  end
  return t
end

local function collect(...)
  local n = select("#", ...)
  local out = { (select(1, ...)) and "ok" or "error" }
  for i = 2, n do out[#out + 1] = fmt((select(i, ...))) end
  return table.concat(out, " ")
end

function run(sources)
  for i = 1, #sources do
    local chunk, err = loadstring(sources[i], "=case")
    if not chunk then
      io.write("syntax '", tostring(err), "'\n")
    else
      io.write(collect(pcall(chunk)), "\n")
    end
  end
end
`

// quoteLuaString spells text as a Lua 5.1 double-quoted literal.
func quoteLuaString(text string) string {
	quoted := &strings.Builder{}
	quoted.WriteByte('"')
	for index := 0; index < len(text); index++ {
		switch character := text[index]; character {
		case '"', '\\':
			quoted.WriteByte('\\')
			quoted.WriteByte(character)
		case '\n':
			quoted.WriteString("\\n")
		case '\r':
			quoted.WriteString("\\r")
		case '\t':
			quoted.WriteString("\\t")
		default:
			if character < 0x20 || character == 0x7f {
				fmt.Fprintf(quoted, "\\%03d", character)
				continue
			}
			quoted.WriteByte(character)
		}
	}
	quoted.WriteByte('"')
	return quoted.String()
}

// The differential cases need a few base-library primitives that the base
// library does not implement yet: metamethod installation, multiple-result
// counting, and Lua-level error raising. installTestPrelude supplies exactly
// those four, and only for tests. They are deliberate throwaway scaffolding
// with no claim to being the eventual base-library implementations; delete
// them once library_base.go provides the real ones.
func installTestPrelude(t *testing.T, state *State) {
	t.Helper()
	prelude := [...]struct {
		name  string
		entry NativeFunc
	}{
		{name: "error", entry: testPreludeError},
		{name: "select", entry: testPreludeSelect},
		{name: "setmetatable", entry: testPreludeSetMetatable},
		{name: "tostring", entry: testPreludeToString},
	}
	for _, definition := range prelude {
		function, err := state.NewNativeFunction(definition.entry)
		if err != nil {
			t.Fatal(err)
		}
		if err := state.SetGlobal(
			definition.name,
			function.Value(),
		); err != nil {
			t.Fatal(err)
		}
	}
}

func testPreludeError(frame Frame) Outcome {
	value, present := frame.argument(0)
	if !present {
		value = nilSlot
	}
	level := 1
	if _, hasLevel := frame.argument(1); hasLevel {
		if supplied, ok := frame.integerArgument(1); ok {
			level = supplied
		}
	}
	if value.kind() == StringKind && level > 0 {
		return libraryError(frame, "%s", (*luaString)(value.ref).text)
	}
	return frame.Raise(value.owningValue())
}

func testPreludeSelect(frame Frame) Outcome {
	count := frame.ArgumentCount()
	if text, ok := frame.String(0); ok && text == "#" {
		return frame.ReturnNumber(float64(count - 1))
	}
	index, ok := frame.integerArgument(0)
	if !ok || index < 1 {
		return baseArgumentError(frame, 0, "index out of range")
	}
	call := frame.activation()
	base := int(call.base) + index
	if base > frame.thread.top {
		base = frame.thread.top
	}
	return frame.returnCompactValues(
		[2]slot{},
		0,
		frame.thread.values[base:frame.thread.top],
	)
}

func testPreludeSetMetatable(frame Frame) Outcome {
	target, ok := frame.Table(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	var metatable *Table
	if value, present := frame.argument(1); present &&
		value.kind() != NilKind {
		metatable, ok = frame.Table(1)
		if !ok {
			return baseArgumentTypeError(frame, 1, "table")
		}
	}
	if err := frame.State().SetMetatable(
		target.Value(),
		metatable,
	); err != nil {
		return frame.RaiseString(err.Error())
	}
	return frame.returnCompactValues([2]slot{slotFromValue(target.Value())}, 1, nil)
}

func testPreludeToString(frame Frame) Outcome {
	value, present := frame.argument(0)
	if !present {
		return baseArgumentError(frame, 0, "value expected")
	}
	return frame.ReturnString(value.owningValue().String())
}
