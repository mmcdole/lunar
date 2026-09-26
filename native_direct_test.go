package lua

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newDirectTestState(t *testing.T) *State {
	t.Helper()
	state, err := New(Options{Libraries: FullLibraries()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { state.Close() })
	return state
}

// The fixed-argument CALL form may take the direct entry; the vararg form
// (B == 0) always takes the ordinary native path. Every result, error
// message, and coercion must agree between them.
func TestDirectNativeCallsMatchOrdinaryPath(t *testing.T) {
	state := newDirectTestState(t)
	_, err := state.DoString("@direct.lua", `
local values = {
  0, 1, -1, 2.5, -2.5, 1/0, -1/0, 0/0, 3, 4, 5, 255, 256, -3, 1e300, -1e300,
  "7", " 8 ", "0x10", "abc", "", "hello world", "x", true, false, {}, print,
}
local nilish = {}
local function describe(ok, a)
  if a ~= a then return tostring(ok) .. ":nan" end
  if not ok then
    -- The failing call's line and the name it was called through differ
    -- between the two forms; the rest of the message must not.
    a = tostring(a):gsub("^[^:]*:%d+: ", ""):gsub("to '[^']*'", "to '?'")
  end
  return tostring(ok) .. ":" .. type(a) .. ":" .. tostring(a)
end
local function check(name, f, fixed, args, n)
  local ok1, r1 = pcall(fixed)
  local ok2, r2 = pcall(function() return (f(unpack(args, 1, n))) end)
  local d1, d2 = describe(ok1, r1), describe(ok2, r2)
  if d1 ~= d2 then
    error(name .. ": fixed " .. d1 .. " vs ordinary " .. d2, 0)
  end
end
local count = 0
for _, a in ipairs(values) do
  check("floor", math.floor, function() return (math.floor(a)) end, {a}, 1)
  check("ceil", math.ceil, function() return (math.ceil(a)) end, {a}, 1)
  check("abs", math.abs, function() return (math.abs(a)) end, {a}, 1)
  check("sqrt", math.sqrt, function() return (math.sqrt(a)) end, {a}, 1)
  check("len", string.len, function() return (string.len(a)) end, {a}, 1)
  check("char", string.char, function() return (string.char(a)) end, {a}, 1)
  check("byte1", string.byte, function() return (string.byte(a)) end, {a}, 1)
  for _, b in ipairs(values) do
    check("max", math.max, function() return (math.max(a, b)) end, {a, b}, 2)
    check("min", math.min, function() return (math.min(a, b)) end, {a, b}, 2)
    check("sub2", string.sub, function() return (string.sub(a, b)) end, {a, b}, 2)
    check("byte2", string.byte, function() return (string.byte(a, b)) end, {a, b}, 2)
    check("subnil", string.sub, function() return (string.sub(a, b, nil)) end, {a, b, nil}, 3)
    for _, c in ipairs({0, 1, 2, 3, -1, -2, 100, 0/0, "2", nil}) do
      check("sub3", string.sub, function() return (string.sub(a, b, c)) end, {a, b, c}, 3)
      check("byte3", string.byte, function() return (string.byte(a, b, c)) end, {a, b, c}, 3)
      count = count + 1
    end
  end
end
-- nil and booleans are scalars without a reference; none may pass as a number.
for _, v in ipairs({{nil}, {true}, {false}}) do
  local x = v[1]
  check("floornil", math.floor, function() return (math.floor(x)) end, {x}, 1)
  check("maxnil", math.max, function() return (math.max(1, x)) end, {1, x}, 2)
  check("charnil", string.char, function() return (string.char(x)) end, {x}, 1)
  check("subnil2", string.sub, function() return (string.sub("abc", x)) end, {"abc", x}, 2)
  check("bytenil2", string.byte, function() return (string.byte("abc", x)) end, {"abc", x}, 2)
  check("bytenil3", string.byte, function() return (string.byte("abc", 2, x)) end, {"abc", 2, x}, 3)
  check("subnil3", string.sub, function() return (string.sub("abc", 2, x)) end, {"abc", 2, x}, 3)
end
check("floor0", math.floor, function() return (math.floor()) end, {}, 0)
check("max0", math.max, function() return (math.max()) end, {}, 0)
check("sub1", string.sub, function() return (string.sub("abc")) end, {"abc"}, 1)
check("char0", string.char, function() return (string.char()) end, {}, 0)
check("char2", string.char, function() return (string.char(65, 66)) end, {65, 66}, 2)
local s = "abcdef"
assert(s:sub(2, 3) == "bc" and s:byte(2) == 98 and s:len() == 6)
-- Statement calls (C == 1) discard the result.
math.floor(1.5); string.sub(s, 1, 2)
assert(count > 0)
`)
	if err != nil {
		t.Fatal(err)
	}
}

// A host replacement of a library global has no direct entry.
func TestDirectNativeCallReplacedGlobalRunsHost(t *testing.T) {
	state := newDirectTestState(t)
	calls := 0
	replacement, err := state.NewNativeFunction(func(frame Frame) Outcome {
		calls++
		return frame.ReturnNumber(42)
	})
	if err != nil {
		t.Fatal(err)
	}
	mathValue, err := state.RawGlobal("math")
	if err != nil {
		t.Fatal(err)
	}
	library, _ := mathValue.AsTable()
	if err := library.RawSetString("floor", replacement.Value()); err != nil {
		t.Fatal(err)
	}
	values, err := state.DoString("@replaced.lua",
		`local x = math.floor(1.5) return x`)
	if err != nil {
		t.Fatal(err)
	}
	if number, _ := values[0].AsNumber(); number != 42 || calls != 1 {
		t.Fatalf("replacement returned %v after %d calls", values[0], calls)
	}
}

// At the native call-depth limit a direct builtin fails exactly as the
// ordinary native call does.
func TestDirectNativeCallHonorsNativeDepth(t *testing.T) {
	state := newDirectTestState(t)
	_, err := state.DoString("@depth.lua", `
local depth, mismatches, failures = 0, 0, 0
local function rec()
  depth = depth + 1
  local ok1, e1 = pcall(function() local x = math.floor(1.5) return x end)
  local ok2, e2 = pcall(function(...) local x = math.floor(...) return x end, 1.5)
  e1 = tostring(e1):gsub("^[^:]*:%d+: ", "")
  e2 = tostring(e2):gsub("^[^:]*:%d+: ", "")
  if not ok1 then failures = failures + 1 end
  if ok1 ~= ok2 or e1 ~= e2 then
    mismatches = mismatches + 1
  end
  pcall(rec)
end
pcall(rec)
assert(depth > 50, depth)
assert(mismatches == 0, mismatches)
assert(failures > 0, failures)
`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestDirectNativeCallCancellation(t *testing.T) {
	state := newDirectTestState(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := state.SetContext(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := state.DoString("@cancel.lua", `
local s = "abcdef"
while true do
  local x = math.floor(1.5)
  local y = string.sub(s, 2, 4)
end
`)
	var luaErr *Error
	if !errors.As(err, &luaErr) || luaErr.Category() != ContextError {
		t.Fatalf("err = %v; want context error", err)
	}
}

// Substrings created on the direct path are charged and collected at the
// same safe point as an ordinary native return.
func TestDirectNativeCallServicesCollection(t *testing.T) {
	state := newDirectTestState(t)
	values, err := state.DoString("@collect.lua", `
local parts = {}
for i = 1, 40000 do parts[i] = tostring(i * 7919 % 100003) end
local text = table.concat(parts)
local peak = 0
for i = 1, 200000 do
  local a = i % (#text - 40) + 1
  local s = string.sub(text, a, a + 30)
  if i % 1000 == 0 then
    local kb = collectgarbage("count")
    if kb > peak then peak = kb end
  end
end
return peak
`)
	if err != nil {
		t.Fatal(err)
	}
	if peak, _ := values[0].AsNumber(); peak > 16*1024 {
		t.Fatalf("peak heap %v KB; substrings are not collected", peak)
	}
}
