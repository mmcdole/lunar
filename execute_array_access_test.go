package lua

import "testing"

func TestExecutorNumericArrayAccessKeepsDistinctKeys(t *testing.T) {
	const source = `
local near_one = 1 + 2^-52
local wide, huge, infinity = 2^32 + 1, 2^64, 1/0
local target = {10, 20, 30, field = "record"}
local function set(key, value) target[key] = value end
local function get(key) return target[key] end
set(1.5, "fraction")
set(near_one, "near")
set("1", "string")
set(wide, "wide")
set(huge, "huge")
set(infinity, "positive infinity")
set(-infinity, "negative infinity")
set(-1, "negative")
set(0, "zero")
set(-1 * 0, "updated zero")
set(1, "array")
set(2, false)
set(3, {answer = 33})
return get(1), get(1.0), get(2), get(3).answer,
       get(1.5), get(near_one), get("1"), get(wide), get(huge),
       get(infinity), get(-infinity), get(-1), get(0), get(-1 * 0),
       get(0/0), target.field
`
	state, thread, result := executeTestChunk(t, source)
	defer state.Close()
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread,
		state.String("array"), state.String("array"), Bool(false), Number(33),
		state.String("fraction"), state.String("near"), state.String("string"),
		state.String("wide"), state.String("huge"),
		state.String("positive infinity"), state.String("negative infinity"),
		state.String("negative"), state.String("updated zero"), state.String("updated zero"),
		Nil(), state.String("record"),
	)
}

func TestExecutorNumericArrayAccessPreservesMetamethods(t *testing.T) {
	state, results := runArrayAccessSource(t, `
local reads, writes, written = 0, 0, nil
local target = setmetatable({10, 20, 30, field = "record"}, {
  __index = function(t, key)
    reads = reads + 1
    return "missing"
  end,
  __newindex = function(t, key, value)
    writes = writes + 1
    written = value
  end
})
target[2] = false
local present = target[2]
target[2] = nil
local deleted = target[2]
target[2] = 99
local intercepted = rawget(target, 2)
target[1] = function() return 41 end
local called = target[1]()
target[1] = "text"
target[3] = {answer = 42}
return reads, writes, present, deleted, intercepted, written,
       called, target[1], target[3].answer, target
`)
	assertTestValues(t, results[:9], Number(1), Number(1), Bool(false),
		state.String("missing"), Nil(), Number(99), Number(41),
		state.String("text"), Number(42))
	target, ok := results[9].AsTable()
	if !ok {
		t.Fatal("last result is not the target table")
	}
	if used := target.runtimeObject().arrayUsed; used != 2 {
		t.Fatalf("array occupancy after deletion and intercepted write = %d, want 2", used)
	}
	assertTableLaneInvariant(t, target.runtimeObject())
}

func TestExecutorNumericArrayAccessReleasesReplacedReferences(t *testing.T) {
	_, results := runArrayAccessSource(t, `
local function prepare()
  local first, second = {}, {}
  local weak = setmetatable({first, second}, {__mode = "v"})
  local target = {first, 17}
  target[1] = second
  return target, weak
end
local function present(table, key) return table[key] ~= nil end
local target, weak = prepare()
collectgarbage("collect")
local first_dead = not present(weak, 1)
local second_alive = present(weak, 2)
target[1] = nil
collectgarbage("collect")
return first_dead, second_alive, not present(weak, 2), target
`)
	assertTestValues(t, results[:3], Bool(true), Bool(true), Bool(true))
	target, ok := results[3].AsTable()
	if !ok {
		t.Fatal("last result is not the target table")
	}
	if used := target.runtimeObject().arrayUsed; used != 1 {
		t.Fatalf("array occupancy after reference deletion = %d, want 1", used)
	}
	assertTableLaneInvariant(t, target.runtimeObject())
}

func TestExecutorNumericTableBoundaryKeysRemainDistinct(t *testing.T) {
	_, results := runArrayAccessSource(t, `
local keys = {2^31-1, 2^31, 2^53, 2^53+2, 2^63-1024, 2^63, -2^63}
local target = {"first", "second"}
for i = 1, #keys do target[keys[i]] = i end
local matched = 0
for i = 1, #keys do
  if target[keys[i]] == i then matched = matched + 1 end
end
for i = 1, #keys, 2 do target[keys[i]] = nil end
local remaining, sum = 0, 0
for key, value in pairs(target) do
  remaining = remaining + 1
  if type(value) == "number" then sum = sum + value end
end
return matched, remaining, sum, target[1], target[2], target
`)
	assertTestValues(t, results[:3], Number(7), Number(5), Number(12))
	first, _ := results[3].AsString()
	second, _ := results[4].AsString()
	if first != "first" || second != "second" {
		t.Fatalf("array values changed: %q, %q", first, second)
	}
	target, ok := results[5].AsTable()
	if !ok {
		t.Fatal("last result is not the target table")
	}
	assertTableLaneInvariant(t, target.runtimeObject())
}

func runArrayAccessSource(t *testing.T, source string) (*State, []Value) {
	t.Helper()
	state, err := New(Options{Libraries: LibrarySet{BaseLibrary}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	chunk := mustLoadString(t, state, "@array-access.lua", source)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	return state, results
}
