package lua

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestStringLibraryInstallationAndSurface(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	before, err := state.Global("string")
	if err != nil {
		t.Fatal(err)
	}
	if !before.IsNil() {
		t.Fatalf("new state string = %v; want nil", before)
	}
	if metatable, err := state.Metatable(state.String("")); err != nil {
		t.Fatal(err)
	} else if metatable != nil {
		t.Fatal("new state already has a string metatable")
	}
	loadedBeforeOpen := mustLoadString(
		t,
		state,
		"@open-string.lua",
		`return ("abc"):upper()`,
	)
	if err := state.OpenString(); err != nil {
		t.Fatal(err)
	}

	libraryValue, err := state.Global("string")
	if err != nil {
		t.Fatal(err)
	}
	library, ok := libraryValue.Table()
	if !ok {
		t.Fatalf("string = %v; want table", libraryValue)
	}
	want := make(map[string]bool, len(stringLibraryFunctions)+1)
	previous := make(map[string]Value, len(stringLibraryFunctions)+1)
	for _, definition := range stringLibraryFunctions {
		want[definition.name] = true
	}
	// The standard distribution's LUA_COMPAT_GFIND alias is part of the
	// surface and must be the same canonical Function as gmatch.
	want["gfind"] = true
	for name := range want {
		value := library.RawGetString(name)
		if value.Kind() != FunctionKind {
			t.Fatalf("string.%s = %v; want function", name, value)
		}
		previous[name] = value
	}
	found := 0
	for key := nilSlot; ; {
		nextKey, _, present, err := library.runtimeObject().next(key)
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		name, isString := nextKey.owningValue().AsString()
		if !isString {
			t.Fatalf("string has a non-string key %v", nextKey.owningValue())
		}
		if !want[name] {
			t.Fatalf("string.%s is not part of the Lua 5.1 surface", name)
		}
		found++
		key = nextKey
	}
	if found != len(want) {
		t.Fatalf("string has %d entries; want %d", found, len(want))
	}
	if same, applicable := previous["gfind"].SameObject(
		previous["gmatch"],
	); !applicable || !same {
		t.Fatal("string.gfind is not the canonical string.gmatch Function")
	}

	// Every Lua string indexes through one metatable whose __index is the
	// library, which is what makes method syntax work.
	metatable, err := state.Metatable(state.String(""))
	if err != nil {
		t.Fatal(err)
	}
	if metatable == nil {
		t.Fatal("OpenString did not install a string metatable")
	}
	if same, applicable := metatable.RawGetString("__index").SameObject(
		libraryValue,
	); !applicable || !same {
		t.Fatal("__index is not the string library")
	}
	if other, err := state.Metatable(state.String("nonempty")); err != nil {
		t.Fatal(err)
	} else if other != metatable {
		t.Fatal("strings do not share one metatable")
	}

	results, err := state.Call(loadedBeforeOpen.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, state.String("ABC"))

	// Reopening replaces the table, every Function, and the metatable.
	if err := state.SetGlobal("string", Number(1)); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenString(); err != nil {
		t.Fatal(err)
	}
	reopenedValue, err := state.Global("string")
	if err != nil {
		t.Fatal(err)
	}
	reopened, ok := reopenedValue.Table()
	if !ok {
		t.Fatalf("reopened string = %v; want table", reopenedValue)
	}
	if same, applicable := libraryValue.SameObject(
		reopenedValue,
	); !applicable || same {
		t.Fatal("reopening did not replace the string table")
	}
	for name := range want {
		if same, applicable := previous[name].SameObject(
			reopened.RawGetString(name),
		); !applicable || same {
			t.Fatalf("reopened string.%s is not a fresh Function", name)
		}
	}
	reopenedMetatable, err := state.Metatable(state.String(""))
	if err != nil {
		t.Fatal(err)
	}
	if reopenedMetatable == metatable {
		t.Fatal("reopening did not replace the string metatable")
	}
	if same, applicable := reopenedMetatable.RawGetString(
		"__index",
	).SameObject(reopenedValue); !applicable || !same {
		t.Fatal("reopened __index is not the reopened library")
	}
}

func TestStringDumpSerializesLuaFunctions(t *testing.T) {
	state := newStateWithString(t)
	defer state.Close()

	chunk := mustLoadString(t, state, "@string-dump.lua", `
local offset = 2
local function add(value)
	return value + offset
end
return add, string.dump(add), pcall(string.dump, string.len)
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 4 {
		t.Fatalf("string.dump test returned %d values; want 4", len(results))
	}
	function, ok := results[0].Function()
	if !ok {
		t.Fatalf("first result = %v; want Function", results[0])
	}
	dumped, ok := results[1].AsString()
	if !ok {
		t.Fatalf("second result = %v; want string", results[1])
	}
	want, err := dumpPrototype(function.Prototype())
	if err != nil {
		t.Fatal(err)
	}
	if dumped != want {
		t.Fatal("string.dump did not serialize the function's Prototype")
	}
	if truth, ok := results[2].AsBool(); !ok || truth {
		t.Fatalf("native dump status = %v; want false", results[2])
	}
	if message, ok := results[3].AsString(); !ok ||
		message != "unable to dump given function" {
		t.Fatalf("native dump failure = %v", results[3])
	}
}

func TestStringLibraryMatchesLua51(t *testing.T) {
	runLua51Cases(t, stringLibraryLua51Cases)
}

// TestStringLibraryDefinesCUndefinedFormatting records this library's one
// deliberate divergence from a given PUC build.
//
// Lua 5.1 hands %o, %u, %x, %X, %c, and %d the result of a C cast from double
// to an integer type. That cast is undefined for a negative value and for a
// magnitude the type cannot hold, and the mainstream architectures disagree:
// arm64 saturates, x86-64 wraps. Every case below is one C leaves undefined,
// so it describes Badger's rule rather than any interpreter's output.
func TestStringLibraryDefinesCUndefinedFormatting(t *testing.T) {
	testCases := []struct {
		name   string
		source string
		want   string
	}{
		{
			// The two's-complement reading is what Lua 5.3 and later produce
			// now that they have real integers, and it keeps %x and %d
			// describing the same value.
			name:   "negative unsigned conversions wrap",
			source: "return string.format('%x|%X|%o|%u', -1, -1, -1, -1)",
			want:   "ok 'ffffffffffffffff|FFFFFFFFFFFFFFFF|1777777777777777777777|18446744073709551615'",
		},
		{
			name:   "negative unsigned conversions truncate toward zero",
			source: "return string.format('%x|%x', -3.5, -0.5)",
			want:   "ok 'fffffffffffffffd|0'",
		},
		{
			name:   "magnitudes beyond the type saturate",
			source: "return string.format('%x|%x|%d|%d', 1e30, -1e30, 1e30, -1e30)",
			want:   "ok 'ffffffffffffffff|8000000000000000|9223372036854775807|-9223372036854775808'",
		},
		{
			name:   "infinities and NaN are defined",
			source: "return string.format('%d|%d|%d|%x|%x|%x', 1/0, -1/0, 0/0, 1/0, -1/0, 0/0)",
			want:   "ok '9223372036854775807|-9223372036854775808|0|ffffffffffffffff|8000000000000000|0'",
		},
		{
			// %c converts through int and then to unsigned char, so only the
			// low byte of the 32-bit conversion survives.
			name:   "character conversion keeps the low byte",
			source: "return string.format('%c', 65), string.format('%c', 321) == string.char(65), string.format('%c', -191) == string.char(65)",
			want:   "ok 'A' true true",
		},
		{
			// A NUL byte truncates the item, because PUC measures each
			// formatted item with strlen.
			name:   "a zero byte truncates its item",
			source: "return #string.format('[%c]', 0), #string.format('[%5c]', 0)",
			want:   "ok 2 6",
		},
	}
	for _, test := range testCases {
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

// TestStringLibraryReentersLuaSafely covers what a recorded case cannot
// observe: a gsub replacement runs through the same nested-call checkpoint
// Frame.Call uses, so it may call Lua, resume a coroutine, recurse into gsub,
// and fail without leaving the executor inconsistent.
func TestStringLibraryReentersLuaSafely(t *testing.T) {
	testCases := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "replacement calls Lua",
			source: `
local function wrap(word) return "<" .. word .. ">" end
return ("a b"):gsub("%a", function(c) return wrap(c) end)
`,
			want: "ok '<a> <b>' 2",
		},
		{
			name: "replacement resumes a coroutine",
			source: `
local co = coroutine.wrap(function()
	while true do coroutine.yield() end
end)
return ("ab"):gsub("%a", function(c) co() return c:upper() end)
`,
			want: "ok 'AB' 2",
		},
		{
			name: "replacement cannot yield across gsub",
			source: `
local co = coroutine.create(function()
	return ("a"):gsub("%a", function() coroutine.yield() end)
end)
return coroutine.resume(co)
`,
			want: "ok false 'attempt to yield across metamethod/C-call boundary'",
		},
		{
			name: "nested gsub does not disturb the outer match",
			source: `
return ("a b"):gsub("%a", function(c)
	local inner = ("xy"):gsub("%a", "z")
	return c .. inner
end)
`,
			want: "ok 'azz bzz' 2",
		},
		{
			name: "replacement failure is catchable and abandons the rest",
			source: `
local seen = 0
local ok, message = pcall(string.gsub, "abc", "%a", function(c)
	seen = seen + 1
	if c == "b" then error("stop") end
	return c
end)
return ok, message, seen
`,
			want: "ok false 'case:5: stop' 2",
		},
		{
			name: "table replacement follows __index",
			source: `
local base = {a = "A"}
local t = setmetatable({}, {__index = base})
return ("ab"):gsub("%a", t)
`,
			want: "ok 'Ab' 2",
		},
		{
			name: "table replacement calls an __index function",
			source: `
local t = setmetatable({}, {__index = function(_, key)
	return key .. key
end})
return ("ab"):gsub("%a", t)
`,
			want: "ok 'aabb' 2",
		},
		{
			name: "table replacement propagates an __index failure",
			source: `
local t = setmetatable({}, {__index = function() error("bad key") end})
return ("a"):gsub("%a", t)
`,
			want: "error 'case:2: bad key'",
		},
		{
			name: "gmatch survives an interleaved gsub",
			source: `
local out = {}
for word in ("one two"):gmatch("%a+") do
	out[#out + 1] = (word:gsub("o", "0"))
end
return table.concat(out, ",")
`,
			want: "ok '0ne,tw0'",
		},
	}

	for _, test := range testCases {
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

func TestStringPatternMatchingCooperatesWithContextCancellation(t *testing.T) {
	state := newStateWithString(t)
	defer state.Close()

	subject := strings.Repeat("a", 32)
	pattern := strings.Repeat("a*", 16) + "b"
	chunk := mustLoadString(
		t,
		state,
		"@pattern-context.lua",
		"return string.match("+
			quoteLuaString(subject)+","+
			quoteLuaString(pattern)+")",
	)

	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(5*time.Millisecond, cancel)
	defer timer.Stop()
	started := time.Now()
	_, err := state.CallContext(ctx, chunk.Value())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("pattern cancellation = %v; want context.Canceled", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("pattern cancellation took %s", elapsed)
	}
	var failure *Error
	if !errors.As(err, &failure) || failure.Category() != ContextError {
		t.Fatalf("pattern cancellation = %#v; want ContextError", err)
	}
}

// TestStringLibraryCallbackFailuresCarryATraceback confirms the unprotected
// replacement call follows the executor's segment rule: the failing callback,
// the native gsub, and each surrounding Lua frame contribute one segment.
func TestStringLibraryCallbackFailuresCarryATraceback(t *testing.T) {
	state := newStateWithString(t)
	defer state.Close()

	chunk := mustLoadString(t, state, "@gsub-traceback.lua", `
local function outer()
	("abc"):gsub("%a", function() error("stop") end)
end
outer()
`)
	_, err := state.Call(chunk.Value())
	var failure *Error
	if !errors.As(err, &failure) {
		t.Fatalf("gsub failure = %v; want *Error", err)
	}
	if failure.Error() != "gsub-traceback.lua:3: stop" {
		t.Fatalf("message = %q", failure.Error())
	}
	want := []TraceFrame{
		{Source: "=[Go]", Function: "native function"}, // error
		{Source: "@gsub-traceback.lua", Line: 3},       // the replacement
		{Source: "=[Go]", Function: "native function"}, // string.gsub
		{Source: "@gsub-traceback.lua", Line: 3},       // outer
		{Source: "@gsub-traceback.lua", Line: 5},       // the main chunk
	}
	traceback := failure.Traceback()
	if len(traceback) != len(want) {
		t.Fatalf("traceback = %#v; want %d segments", traceback, len(want))
	}
	for index, entry := range traceback {
		if entry != want[index] {
			t.Fatalf(
				"traceback[%d] = %#v; want %#v",
				index,
				entry,
				want[index],
			)
		}
	}
}

// TestStringLibraryBoundsConstructedStrings records the one place this library
// must invent a limit. Lua 5.1 lets its allocator reject an impossible
// string.rep; this runtime has no string-size budget to consult, so the
// request is refused deterministically instead of being handed to the host.
func TestStringLibraryBoundsConstructedStrings(t *testing.T) {
	const want = "error 'case:1: resulting string too large'"
	for _, source := range []string{
		"return ('abcdefgh'):rep(2000000000)",
		"return ('a'):rep(2000000000)",
	} {
		if got := runLua51Case(t, "return "+source[len("return "):]); got != want {
			t.Errorf("%s\n got: %s\nwant: %s", source, got, want)
		}
	}
	// A request that merely allocates a lot still succeeds.
	if got := runLua51Case(
		t,
		"return #('a'):rep(1000000)",
	); got != "ok 1000000" {
		t.Errorf("large rep: %s", got)
	}
}

func TestPublishedSubstringsOwnTheirBackingStorage(t *testing.T) {
	state := newStateWithString(t)
	source := strings.Repeat("a", 1<<19) +
		"\xf1" +
		strings.Repeat("b", 1<<19)
	position := 1<<19 + 1
	chunk := mustLoadString(t, state, "@substring-ownership.lua", `
return function(subject, position)
	return subject:sub(position, position),
		subject:match("(.)", position)
end
`)
	loaded, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(
		loaded[0],
		state.String(source),
		Number(float64(position)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("substring call returned %d results; want 2", len(results))
	}

	sourceStart := uintptr(unsafe.Pointer(unsafe.StringData(source)))
	sourceEnd := sourceStart + uintptr(len(source))
	for index, result := range results {
		text, ok := result.AsString()
		if !ok || text != "\xf1" {
			t.Fatalf("result %d = %v; want one-byte string", index, result)
		}
		resultStart := uintptr(unsafe.Pointer(unsafe.StringData(text)))
		if resultStart >= sourceStart && resultStart < sourceEnd {
			t.Fatalf("result %d retains the subject backing storage", index)
		}
	}

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	for index, result := range results {
		if text, ok := result.AsString(); !ok || text != "\xf1" {
			t.Fatalf("closed result %d = %v", index, result)
		}
	}
}

// TestWarmStringLibraryScalarCallsDoNotAllocate holds the operations whose
// results are scalars, or whose result already exists, to the compact
// contract. Operations that must build new text are excluded: their allocation
// is the result, not boundary overhead.
func TestWarmStringLibraryScalarCallsDoNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state := newStateWithString(t)
	defer state.Close()

	testCases := []struct {
		name   string
		source string
	}{
		{name: "len", source: `
local len = string.len
local s = "the quick brown fox"
return function()
	local total = 0
	for index = 1, 100 do total = total + len(s) end
	return total
end
`},
		{name: "byte", source: `
local byte = string.byte
local s = "the quick brown fox"
return function()
	local total = 0
	for index = 1, 19 do total = total + byte(s, index) end
	return total
end
`},
		{name: "byte range", source: `
local byte = string.byte
local s = "abcdefgh"
return function()
	local total = 0
	for index = 1, 20 do
		local a, b, c = byte(s, 1, 3)
		total = total + a + b + c
	end
	return total
end
`},
		{name: "char single byte", source: `
local char = string.char
return function()
	local total = 0
	for value = 0, 255 do total = total + #char(value) end
	return total
end
`},
		{name: "find plain", source: `
local find = string.find
local s = "the quick brown fox"
return function()
	local total = 0
	for index = 1, 100 do
		local a, b = find(s, "brown")
		total = total + a + b
	end
	return total
end
`},
		{name: "find pattern", source: `
local find = string.find
local s = "the quick brown fox"
return function()
	local total = 0
	for index = 1, 100 do
		local a, b = find(s, "b%a+n")
		total = total + a + b
	end
	return total
end
`},
		{name: "find positions", source: `
local find = string.find
local s = "the quick brown fox"
return function()
	local total = 0
	for index = 1, 100 do
		local a, b, p = find(s, "()brown")
		total = total + a + b + p
	end
	return total
end
`},
		{name: "find miss", source: `
local find = string.find
local s = "the quick brown fox"
return function()
	local total = 0
	for index = 1, 100 do
		if find(s, "%d+%.%d+") then total = total + 1 end
	end
	return total
end
`},
		{name: "sub reuses interned text", source: `
local sub = string.sub
local s = "the quick brown fox"
return function()
	local total = 0
	for index = 1, 100 do total = total + #sub(s, 5, 9) end
	return total
end
`},
		{name: "upper of an upper string", source: `
local upper = string.upper
local s = "ALREADY UPPER"
return function()
	local total = 0
	for index = 1, 100 do total = total + #upper(s) end
	return total
end
`},
		{name: "match captures", source: `
local match = string.match
local s = "key=value"
return function()
	local total = 0
	for index = 1, 100 do
		local k, v = match(s, "(%w+)=(%w+)")
		total = total + #k + #v
	end
	return total
end
`},
		{name: "gsub miss reuses source", source: `
local gsub = string.gsub
local s = ("abcdefgh"):rep(128)
return function()
	local result, count = gsub(s, "%d", "x")
	if result ~= s then return -1 end
	return count
end
`},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			chunk := mustLoadString(t, state, "@string-alloc.lua", test.source)
			results, err := state.Call(chunk.Value())
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 {
				t.Fatalf("loader produced %d results; want 1", len(results))
			}
			body := results[0]
			var destination [1]Value
			for index := 0; index < 64; index++ {
				if _, err := state.CallInto(
					body,
					nil,
					destination[:],
				); err != nil {
					t.Fatal(err)
				}
			}
			allocations := testing.AllocsPerRun(64, func() {
				if _, err := state.CallInto(
					body,
					nil,
					destination[:],
				); err != nil {
					t.Fatal(err)
				}
			})
			if allocations != 0 {
				t.Fatalf("warm calls allocated %v times per run", allocations)
			}
		})
	}
}

func BenchmarkStringLibraryBuilders(b *testing.B) {
	state := newStateWithString(b)
	b.Cleanup(func() {
		_ = state.Close()
	})
	for _, benchmark := range []struct {
		name   string
		source string
	}{
		{
			name: "reverse",
			source: `
local operation = string.reverse
local text = ("abcdefgh"):rep(128)
return function() return operation(text) end
`,
		},
		{
			name: "upper",
			source: `
local operation = string.upper
local text = ("abcdefgh"):rep(128)
return function() return operation(text) end
`,
		},
		{
			name: "gsub miss",
			source: `
local operation = string.gsub
local text = ("abcdefgh"):rep(128)
return function() return (operation(text, "%d", "x")) end
`,
		},
		{
			name: "gsub replacement",
			source: `
local operation = string.gsub
local text = ("abcdefgh"):rep(128)
return function() return (operation(text, "a", "A")) end
`,
		},
		{
			name: "format",
			source: `
local operation = string.format
local text = ("abcdefgh"):rep(128)
return function() return operation("%s:%08d", text, 42) end
`,
		},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			chunk := mustLoadString(
				b,
				state,
				"@string-builder-benchmark.lua",
				benchmark.source,
			)
			loaded, err := state.Call(chunk.Value())
			if err != nil {
				b.Fatal(err)
			}
			var destination [1]Value
			for range 16 {
				if _, err := state.CallInto(
					loaded[0],
					nil,
					destination[:],
				); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := state.CallInto(
					loaded[0],
					nil,
					destination[:],
				); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func newStateWithString(t testing.TB) *State {
	t.Helper()
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, open := range []func() error{
		state.OpenBase,
		state.OpenTable,
		state.OpenString,
	} {
		if err := open(); err != nil {
			t.Fatal(err)
		}
	}
	return state
}

var stringLibraryLua51Cases = []lua51Case{
	{
		name:   "len",
		source: "return string.len('abc'), ('abc'):len(), string.len(''), string.len('a\\0b')",
		want:   "ok 3 3 0 3",
	},
	{
		name:   "len_coerces_number",
		source: "return string.len(1234), string.len(1.5)",
		want:   "ok 4 3",
	},
	{
		name:   "sub_basic",
		source: "return ('hello'):sub(2), ('hello'):sub(2,3), ('hello'):sub(1,1)",
		want:   "ok 'ello' 'el' 'h'",
	},
	{
		name:   "sub_negative",
		source: "return ('hello'):sub(-3), ('hello'):sub(-3,-2), ('hello'):sub(-100,100)",
		want:   "ok 'llo' 'll' 'hello'",
	},
	{
		name:   "sub_clamped",
		source: "return ('hello'):sub(0), ('hello'):sub(10), ('hello'):sub(2,100), ('hello'):sub(4,2)",
		want:   "ok 'hello' '' 'ello' ''",
	},
	{
		name:   "sub_truncates",
		source: "return ('hello'):sub(2.9), ('hello'):sub(1.2, 3.9)",
		want:   "ok 'ello' 'hel'",
	},
	{
		name:   "sub_needs_position",
		source: "return ('hello'):sub()",
		want:   "error 'case:1: bad argument #1 to 'sub' (number expected, got no value)'",
	},
	{
		name:   "sub_bad_position",
		source: "return ('hello'):sub({})",
		want:   "error 'case:1: bad argument #1 to 'sub' (number expected, got table)'",
	},
	{
		name:   "sub_bad_subject",
		source: "return string.sub(nil, 1)",
		want:   "error 'case:1: bad argument #1 to 'sub' (string expected, got nil)'",
	},
	{
		name:   "sub_nul",
		source: "return #('a\\0b'):sub(1,3), ('a\\0b'):sub(2,2) == '\\0'",
		want:   "ok 3 true",
	},
	{
		name:   "reverse",
		source: "return ('abc'):reverse(), (''):reverse(), ('a\\0b'):reverse() == 'b\\0a', string.reverse(string.char(0xc3, 0xa9)) == string.char(0xa9, 0xc3)",
		want:   "ok 'cba' '' true true",
	},
	{
		name:   "case",
		source: "return ('aBc1'):upper(), ('AbC1'):lower(), ('ABC'):upper(), ('abc'):lower()",
		want:   "ok 'ABC1' 'abc1' 'ABC' 'abc'",
	},
	{
		name:   "case_is_ascii_only",
		source: "return ('a\\200z'):upper() == 'A\\200Z', ('A\\200Z'):lower() == 'a\\200z'",
		want:   "ok true true",
	},
	{
		name:   "case_punctuation",
		source: "return ('a-b_c'):upper()",
		want:   "ok 'A-B_C'",
	},
	{
		name:   "rep",
		source: "return ('ab'):rep(3), ('ab'):rep(1), ('ab'):rep(0), ('ab'):rep(-2)",
		want:   "ok 'ababab' 'ab' '' ''",
	},
	{
		name:   "rep_empty",
		source: "return #(''):rep(1000000)",
		want:   "ok 0",
	},
	{
		name:   "rep_truncates",
		source: "return ('a'):rep(3.9)",
		want:   "ok 'aaa'",
	},
	{
		name:   "rep_needs_count",
		source: "return ('a'):rep()",
		want:   "error 'case:1: bad argument #1 to 'rep' (number expected, got no value)'",
	},
	{
		name:   "rep_bad_count",
		source: "return ('a'):rep('x')",
		want:   "error 'case:1: bad argument #1 to 'rep' (number expected, got string)'",
	},
	{
		name:   "byte",
		source: "return ('abc'):byte(), ('abc'):byte(2), ('abc'):byte(-1)",
		want:   "ok 97 98 99",
	},
	{
		name:   "byte_range",
		source: "return ('abc'):byte(1,-1)",
		want:   "ok 97 98 99",
	},
	{
		name:   "byte_range_count",
		source: "return select('#', ('abc'):byte(1,-1)), select('#', ('abc'):byte(5)), select('#', ('abc'):byte(0))",
		want:   "ok 3 0 0",
	},
	{
		name:   "byte_clamped",
		source: "return ('abc'):byte(0,10)",
		want:   "ok 97 98 99",
	},
	{
		name:   "byte_empty",
		source: "return select('#', (''):byte(1,-1))",
		want:   "ok 0",
	},
	{
		name:   "byte_nul",
		source: "return ('a\\0b'):byte(2)",
		want:   "ok 0",
	},
	{
		name:   "char",
		source: "return string.char(72,105), string.char(), string.char(0) == '\\0'",
		want:   "ok 'Hi' '' true",
	},
	{
		name:   "char_truncates",
		source: "return string.char(65.7)",
		want:   "ok 'A'",
	},
	{
		name:   "char_rejects_high",
		source: "return string.char(256)",
		want:   "error 'case:1: bad argument #1 to 'char' (invalid value)'",
	},
	{
		name:   "char_rejects_negative",
		source: "return string.char(-1)",
		want:   "error 'case:1: bad argument #1 to 'char' (invalid value)'",
	},
	{
		name:   "char_rejects_type",
		source: "return string.char('x')",
		want:   "error 'case:1: bad argument #1 to 'char' (number expected, got string)'",
	},
	{
		name:   "char_reports_index",
		source: "return string.char(65, 66, 300)",
		want:   "error 'case:1: bad argument #3 to 'char' (invalid value)'",
	},
	{
		name:   "char_roundtrip",
		source: "return string.char(('abc'):byte(1,-1))",
		want:   "ok 'abc'",
	},
	{
		name:   "dump_type_check",
		source: "return string.dump(5)",
		want:   "error 'case:1: bad argument #1 to 'dump' (function expected, got number)'",
	},
	{
		name:   "dump_rejects_native_function",
		source: "return pcall(string.dump, string.len)",
		want:   "ok false 'unable to dump given function'",
	},
	{
		name:   "find_plain",
		source: "return ('hello world'):find('o w')",
		want:   "ok 5 7",
	},
	{
		name:   "find_plain_absent",
		source: "return ('hello'):find('xyz')",
		want:   "ok nil",
	},
	{
		name:   "find_plain_flag",
		source: "return ('a.c'):find('.', 1, true), ('a.c'):find('.', 1)",
		want:   "ok 2 1 1",
	},
	{
		name:   "find_plain_flag_false",
		source: "return ('a+b'):find('+', 1, false)",
		want:   "ok 2 2",
	},
	{
		name:   "find_empty_pattern",
		source: "return ('abc'):find('')",
		want:   "ok 1 0",
	},
	{
		name:   "find_init",
		source: "return ('abcabc'):find('abc', 4), ('abcabc'):find('abc', -3)",
		want:   "ok 4 4 6",
	},
	{
		name:   "find_init_clamped",
		source: "return ('abc'):find('b', 10), ('abc'):find('', 10)",
		want:   "ok nil 4 3",
	},
	{
		name:   "find_captures",
		source: "return ('key=value'):find('(%w+)=(%w+)')",
		want:   "ok 1 9 'key' 'value'",
	},
	{
		name:   "find_anchor",
		source: "return ('hello'):find('^he'), ('hello'):find('^el')",
		want:   "ok 1 nil",
	},
	{
		name:   "find_bad_subject",
		source: "return string.find(nil, 'a')",
		want:   "error 'case:1: bad argument #1 to 'find' (string expected, got nil)'",
	},
	{
		name:   "find_bad_pattern",
		source: "return ('a'):find(nil)",
		want:   "error 'case:1: bad argument #1 to 'find' (string expected, got nil)'",
	},
	{
		name:   "find_bad_init",
		source: "return ('a'):find('a', {})",
		want:   "error 'case:1: bad argument #2 to 'find' (number expected, got table)'",
	},
	{
		name:   "find_malformed",
		source: "return ('abc'):find('[a')",
		want:   "error 'case:1: malformed pattern (missing ']')'",
	},
	{
		name:   "find_number_subject",
		source: "return string.find(12345, '34')",
		want:   "ok 3 4",
	},
	{
		name:   "match_whole",
		source: "return ('hello'):match('l+')",
		want:   "ok 'll'",
	},
	{
		name:   "match_captures",
		source: "return ('key=value'):match('(%w+)=(%w+)')",
		want:   "ok 'key' 'value'",
	},
	{
		name:   "match_positions",
		source: "return ('hello'):match('()ll()')",
		want:   "ok 3 5",
	},
	{
		name:   "match_absent",
		source: "return ('hello'):match('%d')",
		want:   "ok nil",
	},
	{
		name:   "match_init",
		source: "return ('abcabc'):match('a(b)c', 4)",
		want:   "ok 'b'",
	},
	{
		name:   "match_anchor",
		source: "return ('hello'):match('^h(e)')",
		want:   "ok 'e'",
	},
	{
		name:   "match_paren_is_matcher",
		source: "return ('abc'):match(')')",
		want:   "error 'case:1: invalid pattern capture'",
	},
	{
		name:   "match_malformed",
		source: "return ('abc'):match('%')",
		want:   "error 'case:1: malformed pattern (ends with '%')'",
	},
	{
		name:   "gmatch_words",
		source: "local out = {} for w in ('one two three'):gmatch('%a+') do out[#out+1] = w end return table.concat(out, ',')",
		want:   "ok 'one,two,three'",
	},
	{
		name:   "gmatch_pairs",
		source: "local out = {} for k, v in ('a=1, b=2'):gmatch('(%w+)=(%w+)') do out[#out+1] = k .. ':' .. v end return table.concat(out, ',')",
		want:   "ok 'a:1,b:2'",
	},
	{
		name:   "gmatch_empty_advances",
		source: "local n = 0 for w in ('abc'):gmatch('') do n = n + 1 end return n",
		want:   "ok 4",
	},
	{
		name:   "gmatch_no_match",
		source: "local n = 0 for w in ('abc'):gmatch('%d') do n = n + 1 end return n",
		want:   "ok 0",
	},
	{
		name:   "gmatch_caret_is_literal",
		source: "local out = {} for w in ('^a^b'):gmatch('^') do out[#out+1] = w end return #out",
		want:   "ok 2",
	},
	{
		name:   "gmatch_positions",
		source: "local out = {} for p in ('abc'):gmatch('()') do out[#out+1] = p end return table.concat(out, ',')",
		want:   "ok '1,2,3,4'",
	},
	{
		name:   "gmatch_exhausts",
		source: "local f = ('a'):gmatch('a') return f(), f(), f()",
		want:   "ok 'a' nil",
	},
	{
		name:   "gmatch_is_independent",
		source: "local a = ('ab'):gmatch('%a') local b = ('ab'):gmatch('%a') return a(), b(), a(), b()",
		want:   "ok 'a' 'a' 'b' 'b'",
	},
	{
		name:   "gmatch_bad_pattern",
		source: "return ('a'):gmatch(nil)",
		want:   "error 'case:1: bad argument #1 to 'gmatch' (string expected, got nil)'",
	},
	{
		name:   "gmatch_malformed_defers",
		source: "local f = ('abc'):gmatch('[a') return pcall(f)",
		want:   "ok false 'malformed pattern (missing ']')'",
	},
	{
		name:   "gmatch_nul",
		source: "local out = {} for w in ('a\\0b'):gmatch('%z') do out[#out+1] = 'z' end return #out",
		want:   "ok 1",
	},
	{
		name:   "gsub_string",
		source: "return ('hello world'):gsub('o', '0')",
		want:   "ok 'hell0 w0rld' 2",
	},
	{
		name:   "gsub_limit",
		source: "return ('aaa'):gsub('a', 'b', 2)",
		want:   "ok 'bba' 2",
	},
	{
		name:   "gsub_limit_zero",
		source: "return ('aaa'):gsub('a', 'b', 0)",
		want:   "ok 'aaa' 0",
	},
	{
		name:   "gsub_empty_pattern_limit",
		source: "return ('abc'):gsub('', '-', 2)",
		want:   "ok '-a-bc' 2",
	},
	{
		name:   "gsub_captures",
		source: "return ('hello world'):gsub('(%w+)', '<%1>')",
		want:   "ok '<hello> <world>' 2",
	},
	{
		name:   "gsub_whole",
		source: "return ('abc'):gsub('%a', '[%0]')",
		want:   "ok '[a][b][c]' 3",
	},
	{
		name:   "gsub_percent_literal",
		source: "return ('x'):gsub('x', '%%')",
		want:   "ok '%' 1",
	},
	{
		name:   "gsub_percent_escape",
		source: "return ('x'):gsub('x', '%a')",
		want:   "ok 'a' 1",
	},
	{
		name:   "gsub_percent_trailing",
		source: "return #(('x'):gsub('x', '%')), (('x'):gsub('x', '%')):byte(1)",
		want:   "ok 1 0",
	},
	{
		name:   "gsub_bad_capture_index",
		source: "return ('hello'):gsub('l', '%9')",
		want:   "error 'case:1: invalid capture index'",
	},
	{
		name:   "gsub_position_capture_replacement",
		source: "return ('hello'):gsub('()ll', '[%1]')",
		want:   "ok 'he[3]o' 1",
	},
	{
		name:   "gsub_empty_pattern",
		source: "return ('hello'):gsub('', '-')",
		want:   "ok '-h-e-l-l-o-' 6",
	},
	{
		name:   "gsub_anchor",
		source: "return ('aaa'):gsub('^a', 'X')",
		want:   "ok 'Xaa' 1",
	},
	{
		name:   "gsub_anchor_no_match",
		source: "return ('baa'):gsub('^a', 'X')",
		want:   "ok 'baa' 0",
	},
	{
		name:   "gsub_empty_end_anchor",
		source: "return ('abc'):gsub('$', '!')",
		want:   "ok 'abc!' 1",
	},
	{
		name:   "gsub_function",
		source: "return ('hello world'):gsub('%w+', string.upper)",
		want:   "ok 'HELLO WORLD' 2",
	},
	{
		name:   "gsub_function_captures",
		source: "return ('a=1'):gsub('(%w+)=(%w+)', function(k, v) return v .. '=' .. k end)",
		want:   "ok '1=a' 1",
	},
	{
		name:   "gsub_function_nil_keeps",
		source: "return ('abc'):gsub('%a', function(c) if c == 'b' then return 'B' end end)",
		want:   "ok 'aBc' 3",
	},
	{
		name:   "gsub_function_false_keeps",
		source: "return ('abc'):gsub('%a', function() return false end)",
		want:   "ok 'abc' 3",
	},
	{
		name:   "gsub_function_number",
		source: "return ('abc'):gsub('%a', function() return 7 end)",
		want:   "ok '777' 3",
	},
	{
		name:   "gsub_function_bad_value",
		source: "return ('abc'):gsub('%a', function() return {} end)",
		want:   "error 'case:1: invalid replacement value (a table)'",
	},
	{
		name:   "gsub_function_error",
		source: "return ('abc'):gsub('%a', function() error('boom') end)",
		want:   "error 'case:1: boom'",
	},
	{
		name:   "gsub_table",
		source: "return ('$name is $age'):gsub('%$(%w+)', {name = 'bob', age = 42})",
		want:   "ok 'bob is 42' 2",
	},
	{
		name:   "gsub_table_missing_keeps",
		source: "return ('a b'):gsub('%a', {a = 'X'})",
		want:   "ok 'X b' 2",
	},
	{
		name:   "gsub_table_index_metamethod",
		source: "local t = setmetatable({}, {__index = function(_, k) return k:upper() end}) return ('ab'):gsub('%a', t)",
		want:   "ok 'AB' 2",
	},
	{
		name:   "gsub_table_bad_value",
		source: "return ('a'):gsub('%a', {a = {}})",
		want:   "error 'case:1: invalid replacement value (a table)'",
	},
	{
		name:   "gsub_number_replacement",
		source: "return ('abc'):gsub('%a', 7)",
		want:   "ok '777' 3",
	},
	{
		name:   "gsub_bad_replacement",
		source: "return ('abc'):gsub('%a', true)",
		want:   "error 'case:1: bad argument #2 to 'gsub' (string/function/table expected)'",
	},
	{
		name:   "gsub_missing_replacement",
		source: "return ('abc'):gsub('%a')",
		want:   "error 'case:1: bad argument #2 to 'gsub' (string/function/table expected)'",
	},
	{
		name:   "gsub_malformed",
		source: "return ('abc'):gsub('%', 'x')",
		want:   "error 'case:1: malformed pattern (ends with '%')'",
	},
	{
		name:   "gsub_unfinished_capture_is_allowed",
		source: "return ('abc'):gsub('(a', 'x')",
		want:   "ok 'xbc' 1",
	},
	{
		name:   "gsub_unfinished_capture_read_fails",
		source: "return ('abc'):gsub('(a', '%1')",
		want:   "error 'case:1: unfinished capture'",
	},
	{
		name:   "gsub_nul",
		source: "return (('a\\0b'):gsub('%z', '!'))",
		want:   "ok 'a!b'",
	},
	{
		name:   "gsub_class_star",
		source: "return ('abc'):gsub('%z*', '!')",
		want:   "ok '!a!b!c!' 4",
	},
	{
		name:   "gsub_bad_subject",
		source: "return string.gsub(nil, 'a', 'b')",
		want:   "error 'case:1: bad argument #1 to 'gsub' (string expected, got nil)'",
	},
	{
		name:   "gsub_bad_limit",
		source: "return ('a'):gsub('a', 'b', {})",
		want:   "error 'case:1: bad argument #3 to 'gsub' (number expected, got table)'",
	},
	{
		name:   "gsub_validates_limit_before_replacement_type",
		source: "return string.gsub('a', 'a', true, {})",
		want:   "error 'case:1: bad argument #4 to 'gsub' (number expected, got table)'",
	},
	{
		name:   "gsub_no_match",
		source: "return ('abc'):gsub('%d', 'x')",
		want:   "ok 'abc' 0",
	},
	{
		name:   "format_literal",
		source: "return string.format('plain'), string.format('100%%')",
		want:   "ok 'plain' '100%'",
	},
	{
		name:   "format_s",
		source: "return string.format('[%s]', 'ab'), string.format('%s%s', 1, 'x'), string.format('%s', 12.5)",
		want:   "ok '[ab]' '1x' '12.5'",
	},
	{
		name:   "format_s_width",
		source: "return string.format('[%5s][%-5s][%.2s][%5.2s]', 'ab', 'ab', 'abcd', 'abcd')",
		want:   "ok '[   ab][ab   ][ab][   ab]'",
	},
	{
		name:   "format_s_nul",
		source: "return #string.format('%s', 'a\\0b'), #string.format('%s', 'a\\0b' .. ('x'):rep(200))",
		want:   "ok 1 203",
	},
	{
		name:   "format_s_nul_width_and_precision",
		source: "return string.format('[%5.4s][%-5.4s]', 'a\\0bc', 'a\\0bc')",
		want:   "ok '[    a][a    ]'",
	},
	{
		name:   "format_s_bad",
		source: "return string.format('%s', {})",
		want:   "error 'case:1: bad argument #2 to 'format' (string expected, got table)'",
	},
	{
		name:   "format_d",
		source: "return string.format('%d|%i|%5d|%-5d|%05d', 42, 42, 42, 42, 42)",
		want:   "ok '42|42|   42|42   |00042'",
	},
	{
		name:   "format_d_sign",
		source: "return string.format('%+d|% d|%+d|%d', 5, 5, -5, -5)",
		want:   "ok '+5| 5|-5|-5'",
	},
	{
		name:   "format_d_precision",
		source: "return string.format('%.5d|%.0d|%8.5d|%-8.5d', 42, 0, 42, 42)",
		want:   "ok '00042||   00042|00042   '",
	},
	{
		name:   "format_d_truncates",
		source: "return string.format('%d|%d|%d', 3.7, -3.7, 2^53)",
		want:   "ok '3|-3|9007199254740992'",
	},
	{
		name:   "format_d_bad",
		source: "return string.format('%d', 'x')",
		want:   "error 'case:1: bad argument #2 to 'format' (number expected, got string)'",
	},
	{
		name:   "format_x",
		source: "return string.format('%x|%X|%#x|%#X|%08x|%o|%#o|%u', 255, 255, 255, 255, 255, 8, 8, 42)",
		want:   "ok 'ff|FF|0xff|0XFF|000000ff|10|010|42'",
	},
	{
		name:   "format_x_zero",
		source: "return string.format('%#x|%#o', 0, 0)",
		want:   "ok '0|0'",
	},
	{
		name:   "format_alternate_octal_zero_precision",
		source: "return '[' .. string.format('%#.0o|%#.0x|%#.0u', 0, 0, 0) .. ']'",
		want:   "ok '[0||]'",
	},
	{
		name:   "format_f",
		source: "return string.format('%f|%.0f|%.2f|%10.3f|%-10.3f|', 1/3, 0.5, 1.005, 3.14159, 3.14159)",
		want:   "ok '0.333333|0|1.00|     3.142|3.142     |'",
	},
	{
		name:   "format_f_sign",
		source: "return string.format('%f|%+f|% f|%08.2f|%+08.2f', -1.5, 1.5, 1.5, -1.5, 1.5)",
		want:   "ok '-1.500000|+1.500000| 1.500000|-0001.50|+0001.50'",
	},
	{
		name:   "format_e",
		source: "return string.format('%e|%E|%.0e|%.10e|%12.2e', 1/3, 1/3, 1/3, 1/3, 1/3)",
		want:   "ok '3.333333e-01|3.333333E-01|3e-01|3.3333333333e-01|    3.33e-01'",
	},
	{
		name:   "format_g",
		source: "return string.format('%g|%G|%.3g|%.0g|%.17g|%g|%g', 1/3, 1/3, 1/3, 1/3, 1/3, 100000, 1000000)",
		want:   "ok '0.333333|0.333333|0.333|0.3|0.33333333333333331|100000|1e+06'",
	},
	{
		name:   "format_g_small",
		source: "return string.format('%g|%g|%g|%g', 0.0001, 0.00001, 1e20, 0)",
		want:   "ok '0.0001|1e-05|1e+20|0'",
	},
	{
		name:   "format_hash",
		source: "return string.format('%#g|%#e|%#f|%#G', 1.5, 1.5, 1.5, 1.5)",
		want:   "ok '1.50000|1.500000e+00|1.500000|1.50000'",
	},
	{
		name:   "format_hash_g_small",
		source: "return string.format('%#g|%#.3g', 0.0015, 1.0)",
		want:   "ok '0.00150000|1.00'",
	},
	{
		name:   "format_hash_g_zero",
		source: "return string.format('%#.4g|%#.1g|%#.0g', 0, 0, 0)",
		want:   "ok '0.000|0.|0.'",
	},
	{
		name:   "format_nonfinite",
		source: "return string.format('%f|%g|%e', 1/0, -1/0, 0/0)",
		want:   "ok 'inf|-inf|nan'",
	},
	{
		name:   "format_rejects_upper_f",
		source: "return string.format('%F', 1)",
		want:   "error 'case:1: invalid option '%F' to 'format''",
	},
	{
		name:   "format_nonfinite_width",
		source: "return string.format('[%10f][%-10f][%010f][%+f]', 1/0, 1/0, 1/0, 1/0)",
		want:   "ok '[       inf][inf       ][       inf][+inf]'",
	},
	{
		name:   "format_nonfinite_upper",
		source: "return string.format('%E|%G', 1/0, 0/0)",
		want:   "ok 'INF|NAN'",
	},
	{
		name:   "format_c",
		source: "return string.format('[%c][%3c][%-3c]', 65, 66, 67)",
		want:   "ok '[A][  B][C  ]'",
	},
	{
		name:   "format_q",
		source: "return string.format('%q', 'simple')",
		want:   "ok '\"simple\"'",
	},
	{
		name:   "format_q_escapes",
		source: "return string.format('%q', 'a\"b') .. '|' .. string.format('%q', 'c\\\\d')",
		want:   "ok '\"a\\\"b\"|\"c\\\\d\"'",
	},
	{
		name:   "format_q_newline",
		source: "return #string.format('%q', 'a\\nb'), string.format('%q', 'a\\rb')",
		want:   "ok 6 '\"a\\rb\"'",
	},
	{
		name:   "format_q_nul",
		source: "return string.format('%q', 'a\\0b')",
		want:   "ok '\"a\\000b\"'",
	},
	{
		name:   "format_q_bad",
		source: "return string.format('%q', {})",
		want:   "error 'case:1: bad argument #2 to 'format' (string expected, got table)'",
	},
	{
		name:   "format_missing_value",
		source: "return string.format('%d')",
		want:   "error 'case:1: bad argument #2 to 'format' (no value)'",
	},
	{
		name:   "format_missing_value_late",
		source: "return string.format('%d %d', 1)",
		want:   "error 'case:1: bad argument #3 to 'format' (no value)'",
	},
	{
		name:   "format_trailing_percent",
		source: "return string.format('%')",
		want:   "error 'case:1: bad argument #2 to 'format' (no value)'",
	},
	{
		name:   "format_trailing_percent_with_value",
		source: "return string.format('%', 1)",
		want:   "error 'case:1: invalid option '%' to 'format''",
	},
	{
		name:   "format_invalid_option",
		source: "return string.format('%z', 1)",
		want:   "error 'case:1: invalid option '%z' to 'format''",
	},
	{
		name:   "format_invalid_option_l",
		source: "return string.format('%l', 1)",
		want:   "error 'case:1: invalid option '%l' to 'format''",
	},
	{
		name:   "format_repeated_flags",
		source: "return string.format('%---------d', 1)",
		want:   "error 'case:1: invalid format (repeated flags)'",
	},
	{
		name:   "format_flags_boundary",
		source: "return string.format('[%-----d]', 1)",
		want:   "ok '[1]'",
	},
	{
		name:   "format_flags_overflow",
		source: "return string.format('%------d', 1)",
		want:   "error 'case:1: invalid format (repeated flags)'",
	},
	{
		name:   "format_zero_precision_zero",
		source: "return '[' .. string.format('%.0d|%.0x|%.0o|%.0u', 0, 0, 0, 0) .. ']'",
		want:   "ok '[|||]'",
	},
	{
		name:   "format_zero_precision_nonzero",
		source: "return string.format('%.0d|%.3d', 7, 7)",
		want:   "ok '7|007'",
	},
	{
		name:   "format_width_too_long",
		source: "return string.format('%1000d', 1)",
		want:   "error 'case:1: invalid format (width or precision too long)'",
	},
	{
		name:   "format_precision_too_long",
		source: "return string.format('%.1000f', 1)",
		want:   "error 'case:1: invalid format (width or precision too long)'",
	},
	{
		name:   "format_two_digit_width",
		source: "return string.format('[%99d]', 1) == '[' .. (' '):rep(98) .. '1]'",
		want:   "ok true",
	},
	{
		name:   "format_mixed",
		source: "return string.format('%s=%d (%.1f%%)', 'x', 3, 55.55)",
		want:   "ok 'x=3 (55.5%)'",
	},
	{
		name:   "format_number_template",
		source: "return string.format(12)",
		want:   "ok '12'",
	},
	{
		name:   "method_syntax",
		source: "return ('abc'):upper(), ('abc'):len(), ('a,b'):find(',')",
		want:   "ok 'ABC' 3 2 2",
	},
	{
		name:   "string_index",
		source: "return ('abc').upper == string.upper, ('abc').nope",
		want:   "ok true nil",
	},
	{
		name:   "metatable_shape",
		source: "local mt = getmetatable('') return mt.__index == string, type(mt)",
		want:   "ok true 'table'",
	},
}
