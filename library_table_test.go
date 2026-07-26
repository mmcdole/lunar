package lua

import (
	"errors"
	"math/rand"
	"testing"
	"time"
)

func TestTableLibraryInstallationAndSurface(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	before, err := state.Global("table")
	if err != nil {
		t.Fatal(err)
	}
	if !before.IsNil() {
		t.Fatalf("new state table = %v; want nil", before)
	}
	loadedBeforeOpen := mustLoadString(
		t,
		state,
		"@open-table.lua",
		`return table.concat({1, 2, 3}, "-")`,
	)
	if err := state.OpenTable(); err != nil {
		t.Fatal(err)
	}

	libraryValue, err := state.Global("table")
	if err != nil {
		t.Fatal(err)
	}
	library, ok := libraryValue.Table()
	if !ok {
		t.Fatalf("table = %v; want table", libraryValue)
	}
	previous := make(map[string]Value, len(tableLibraryFunctions))
	for _, definition := range tableLibraryFunctions {
		value := library.RawGetString(definition.name)
		if value.Kind() != FunctionKind {
			t.Fatalf(
				"table.%s = %v; want function",
				definition.name,
				value,
			)
		}
		previous[definition.name] = value
	}
	found := 0
	for key := nilSlot; ; {
		nextKey, _, present, err := library.next(key)
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		name, isString := nextKey.owningValue().AsString()
		if !isString {
			t.Fatalf("table has a non-string key %v", nextKey.owningValue())
		}
		if _, expected := previous[name]; !expected {
			t.Fatalf("table.%s is not part of the Lua 5.1 surface", name)
		}
		found++
		key = nextKey
	}
	if found != len(tableLibraryFunctions) {
		t.Fatalf(
			"table has %d entries; want %d",
			found,
			len(tableLibraryFunctions),
		)
	}

	results, err := state.Call(loadedBeforeOpen.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, state.String("1-2-3"))

	if err := state.SetGlobal("table", Number(1)); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenTable(); err != nil {
		t.Fatal(err)
	}
	reopenedValue, err := state.Global("table")
	if err != nil {
		t.Fatal(err)
	}
	reopened, ok := reopenedValue.Table()
	if !ok {
		t.Fatalf("reopened table = %v; want table", reopenedValue)
	}
	if same, applicable := libraryValue.SameObject(
		reopenedValue,
	); !applicable || same {
		t.Fatal("reopening did not replace the table library")
	}
	for _, definition := range tableLibraryFunctions {
		value := reopened.RawGetString(definition.name)
		if same, applicable := previous[definition.name].SameObject(
			value,
		); !applicable || same {
			t.Fatalf(
				"reopened table.%s is not a fresh Function",
				definition.name,
			)
		}
	}

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenTable(); !errors.Is(err, ErrClosed) {
		t.Fatalf("OpenTable after Close = %v; want ErrClosed", err)
	}
}

func TestTableLibraryMatchesLua51(t *testing.T) {
	runLua51Cases(t, tableLibraryLua51Cases)
}

// TestTableLibrarySortReentersLuaSafely covers what the recorded cases cannot
// observe from Lua: a comparator runs through the same nested-call checkpoint
// Frame.Call uses, so it may itself call Lua, resume a coroutine, mutate the
// table under the sort, and fail without leaving the executor inconsistent.
func TestTableLibrarySortReentersLuaSafely(t *testing.T) {
	testCases := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "comparator calls Lua",
			source: `
local key = function(v) return -v end
local t = {3, 1, 2}
table.sort(t, function(a, b) return key(a) < key(b) end)
return table.concat(t, ",")
`,
			want: "ok '3,2,1'",
		},
		{
			name: "comparator resumes a coroutine",
			source: `
local co = coroutine.wrap(function()
	while true do coroutine.yield() end
end)
local t = {3, 1, 2}
table.sort(t, function(a, b) co() return a < b end)
return table.concat(t, ",")
`,
			want: "ok '1,2,3'",
		},
		{
			name: "comparator cannot yield across the sort",
			source: `
local co = coroutine.create(function()
	local t = {3, 1, 2}
	table.sort(t, function(a, b) coroutine.yield() return a < b end)
end)
return coroutine.resume(co)
`,
			want: "ok false 'attempt to yield across metamethod/C-call boundary'",
		},
		{
			name: "comparator failure is catchable and the sort is abandoned",
			source: `
local t = {3, 1, 2}
local ok, message = pcall(table.sort, t, function() error("stop") end)
return ok, message, #t
`,
			want: "ok false 'case:3: stop' 3",
		},
		{
			name: "comparator truncating the table sees raw nil",
			source: `
local t = {5, 4, 3, 2, 1}
local ok = pcall(table.sort, t, function(a, b)
	t[5] = nil
	return (a or 0) < (b or 0)
end)
return ok
`,
			want: "ok true",
		},
		{
			name: "sort survives a comparator that grows the table",
			source: `
local t = {4, 3, 2, 1}
table.sort(t, function(a, b) t[#t + 1] = nil return a < b end)
return t[1], t[2], t[3], t[4]
`,
			want: "ok 1 2 3 4",
		},
		{
			name: "nested sorts do not interfere",
			source: `
local inner = {3, 1, 2}
local outer = {2, 1}
table.sort(outer, function(a, b)
	table.sort(inner)
	return a < b
end)
return table.concat(outer, ","), table.concat(inner, ",")
`,
			want: "ok '1,2' '1,2,3'",
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

func TestTableLibrarySortReleasesExtraArgumentsBeforeComparator(
	t *testing.T,
) {
	const valueLimit = 8
	state, err := New(Options{
		MaxValues: valueLimit,
		MaxFrames: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenTable(); err != nil {
		t.Fatal(err)
	}

	target, err := state.NewTable(2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.RawSetInt(1, Number(2)); err != nil {
		t.Fatal(err)
	}
	if err := target.RawSetInt(2, Number(1)); err != nil {
		t.Fatal(err)
	}

	called := false
	released := false
	comparator, err := state.NewNativeFunction(func(frame Frame) Outcome {
		called = true
		call := frame.activation()
		if frame.depth == 2 {
			sortCall := frame.thread.frames[frame.depth-2]
			released = int(call.resultBase) == int(sortCall.base)+2
			for index := frame.thread.top; index < len(frame.thread.values); index++ {
				if frame.thread.values[index] != (slot{}) {
					released = false
					break
				}
			}
		}
		return frame.ReturnBool(false)
	})
	if err != nil {
		t.Fatal(err)
	}

	libraryValue, err := state.Global("table")
	if err != nil {
		t.Fatal(err)
	}
	library, ok := libraryValue.Table()
	if !ok {
		t.Fatalf("table = %v; want table", libraryValue)
	}
	sort := library.RawGetString("sort")
	arguments := []Value{
		target.Value(),
		comparator.Value(),
		Number(1),
		Number(2),
		Number(3),
		Number(4),
		Number(5),
	}
	count, err := state.CallInto(sort, arguments, nil)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("table.sort returned %d results; want 0", count)
	}
	if !called {
		t.Fatal("table.sort did not call its comparator")
	}
	if !released {
		t.Fatal("table.sort retained arguments above its comparator")
	}
}

// TestTableLibraryCallbackFailuresCarryATraceback confirms the unprotected
// call path follows the executor's segment rule. A comparator failure unwinds
// through the comparator, the native sort, the Lua function that called sort,
// and the main chunk, and each of those frames contributes exactly one
// segment: the nested call captures its own before restoring the checkpoint,
// and propagation appends the outer ones.
func TestTableLibraryCallbackFailuresCarryATraceback(t *testing.T) {
	state := newStateWithTable(t)
	defer state.Close()
	installTestPrelude(t, state)

	chunk := mustLoadString(t, state, "@sort-traceback.lua", `
local function outer()
	table.sort({3, 1, 2}, function() error("stop") end)
end
outer()
`)
	_, err := state.Call(chunk.Value())
	var failure *Error
	if !errors.As(err, &failure) {
		t.Fatalf("sort failure = %v; want *Error", err)
	}
	if failure.Error() != "sort-traceback.lua:3: stop" {
		t.Fatalf("message = %q", failure.Error())
	}

	want := []TraceFrame{
		{Source: "=[Go]", Function: "native function"}, // error
		{Source: "@sort-traceback.lua", Line: 3},       // the comparator
		{Source: "=[Go]", Function: "native function"}, // table.sort
		{Source: "@sort-traceback.lua", Line: 3},       // outer
		{Source: "@sort-traceback.lua", Line: 5},       // the main chunk
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

// TestWarmTableLibrarySequenceCallsDoNotAllocate holds the raw sequence
// operations to the compact contract. concat is excluded: it must build one
// Lua string, which is a real result rather than boundary overhead.
func TestWarmTableLibrarySequenceCallsDoNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state := newStateWithTable(t)
	defer state.Close()

	testCases := []struct {
		name   string
		source string
	}{
		{name: "insert and remove", source: `
local insert, remove = table.insert, table.remove
local t = {1, 2, 3}
return function()
	for index = 1, 50 do
		insert(t, index)
		remove(t)
	end
	return #t
end
`},
		{name: "positional insert and remove", source: `
local insert, remove = table.insert, table.remove
local t = {1, 2, 3, 4}
return function()
	for index = 1, 50 do
		insert(t, 2, index)
		remove(t, 2)
	end
	return #t
end
`},
		{name: "getn and maxn", source: `
local getn, maxn = table.getn, table.maxn
local t = {1, 2, 3}
return function()
	local total = 0
	for index = 1, 50 do total = total + getn(t) + maxn(t) end
	return total
end
`},
		{name: "sort", source: `
local sort = table.sort
local t = {5, 2, 8, 1, 9, 3, 7, 4, 6, 0}
return function()
	for index = 1, 10 do
		t[1], t[10] = t[10], t[1]
		sort(t)
	end
	return t[1]
end
`},
		{name: "sort with a comparator", source: `
local sort = table.sort
local t = {5, 2, 8, 1, 9, 3, 7, 4, 6, 0}
local function before(a, b) return a < b end
return function()
	for index = 1, 10 do
		t[1], t[10] = t[10], t[1]
		sort(t, before)
	end
	return t[1]
end
`},
		{name: "foreachi", source: `
local foreachi = table.foreachi
local t = {1, 2, 3, 4, 5}
local function visit() end
return function()
	for index = 1, 20 do foreachi(t, visit) end
	return #t
end
`},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			chunk := mustLoadString(t, state, "@table-alloc.lua", test.source)
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

func TestSparseTableInsertShiftMatchesTheRawLoop(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	const (
		firstKey = -3
		lastKey  = 5
		keyCount = lastKey - firstKey + 1
	)
	for mask := 0; mask < 1<<keyCount; mask++ {
		for position := firstKey - 1; position <= lastKey+2; position++ {
			reference := newTable(state.runtime, 0, 0)
			sparse := newTable(state.runtime, 0, 0)
			for offset := 0; offset < keyCount; offset++ {
				if mask&(1<<offset) == 0 {
					continue
				}
				key := firstKey + offset
				value := numberSlot(float64(100 + key))
				reference.rawSetIntegerSlot(key, value)
				sparse.rawSetIntegerSlot(key, value)
			}
			installTableInsertSentinels(state, reference)
			installTableInsertSentinels(state, sparse)

			inserted := numberSlot(float64(-mask - 1))
			referenceTableInsert(reference, position, inserted)
			sparseTableInsert(sparse, position, inserted)
			assertEquivalentTableInsertResult(
				t,
				reference,
				sparse,
				firstKey-2,
				lastKey+4,
				mask,
				position,
			)
		}
	}

	random := rand.New(rand.NewSource(0x51))
	for caseIndex := 0; caseIndex < 1_000; caseIndex++ {
		reference := newTable(state.runtime, 0, 0)
		sparse := newTable(state.runtime, 0, 0)
		for mutation := 0; mutation < 100; mutation++ {
			key := random.Intn(97) - 32
			value := nilSlot
			if random.Intn(4) != 0 {
				value = numberSlot(float64(caseIndex*100 + mutation + 1))
			}
			reference.rawSetIntegerSlot(key, value)
			sparse.rawSetIntegerSlot(key, value)
		}
		installTableInsertSentinels(state, reference)
		installTableInsertSentinels(state, sparse)

		position := random.Intn(121) - 48
		inserted := numberSlot(float64(-caseIndex - 1))
		referenceTableInsert(reference, position, inserted)
		sparseTableInsert(sparse, position, inserted)
		assertEquivalentTableInsertResult(
			t,
			reference,
			sparse,
			-52,
			92,
			caseIndex,
			position,
		)
	}
}

func TestTableInsertAtMinimumIntegerCompletesWithLua51Mapping(t *testing.T) {
	state := newStateWithTable(t)
	defer state.Close()

	const minimumPosition = -1 << 31
	probe := newTable(state.runtime, 0, 0)
	for index := 1; index <= 3; index++ {
		probe.rawSetIntegerSlot(index, numberSlot(float64(index)))
	}
	if !useSparseTableInsertShift(
		probe,
		minimumPosition,
		probe.RawLen(),
	) {
		t.Fatal("minimum integer position did not select the sparse shift")
	}

	chunk := mustLoadString(t, state, "@large-negative-insert.lua", `
local t = {
	[-2147483648] = "minimum",
	[-2147483647] = "next",
	[-7] = "negative",
	[0] = "zero",
	[1.5] = "fraction",
	marker = "record",
}
t[1], t[2], t[3] = "one", "two", "three"
table.insert(t, -2147483648, "new")
return t[-2147483648], t[-2147483647], t[-2147483646],
	t[-7] == nil, t[-6], t[0] == nil,
	t[1], t[2], t[3], t[4], t[1.5], t.marker
`)
	started := time.Now()
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("sparse insert took %s", elapsed)
	}
	assertTestValues(
		t,
		results,
		state.String("new"),
		state.String("minimum"),
		state.String("next"),
		Bool(true),
		state.String("negative"),
		Bool(true),
		state.String("zero"),
		state.String("one"),
		state.String("two"),
		state.String("three"),
		state.String("fraction"),
		state.String("record"),
	)
}

func TestSparseTableInsertShiftDoesNotAllocateForSmallStorage(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	const minimumPosition = -1 << 31
	baseline := newTable(state.runtime, 8, 64)
	for key, value := range map[int]float64{
		minimumPosition:     1,
		minimumPosition + 1: 2,
		-7:                  3,
		0:                   4,
		1:                   5,
		2:                   6,
		3:                   7,
	} {
		baseline.rawSetIntegerSlot(key, numberSlot(value))
	}

	array := make([]slot, len(baseline.array), cap(baseline.array))
	entries := make([]tableEntry, len(baseline.store.entries))
	var working Table
	restore := func() {
		working.objectHeader.owner = baseline.owner
		working.array = array[:len(baseline.array)]
		copy(working.array, baseline.array)
		working.arrayUsed = baseline.arrayUsed
		working.store.entries = entries
		copy(working.store.entries, baseline.store.entries)
		working.store.count = baseline.store.count
		working.store.deleted = baseline.store.deleted
		working.store.integerKeys = baseline.store.integerKeys
		working.metatable = baseline.metatable
		working.structuralVersion = baseline.structuralVersion
		working.absentMetamethods = baseline.absentMetamethods
	}
	restore()
	working.shiftSparseIntegerRangeUp(minimumPosition, 3)

	if allocations := testing.AllocsPerRun(100, func() {
		restore()
		working.shiftSparseIntegerRangeUp(minimumPosition, 3)
	}); allocations != 0 {
		t.Fatalf("sparse insert shift allocated %v times", allocations)
	}
}

func BenchmarkTableLibraryDenseInsert(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenTable(); err != nil {
		b.Fatal(err)
	}
	chunk := mustLoadString(b, state, "@dense-table-insert.lua", `
local insert, remove = table.insert, table.remove
local t = {}
for index = 1, 128 do t[index] = index end
return function()
	insert(t, 2, 0)
	remove(t, 2)
end
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		b.Fatal(err)
	}
	function, ok := results[0].Function()
	if !ok {
		b.Fatal("loader did not return a function")
	}
	callable := function.Value()
	if _, err := state.CallInto(callable, nil, nil); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := state.CallInto(callable, nil, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTableInsertSparseShift(b *testing.B) {
	const position = -64 * 1024
	for _, benchmark := range []struct {
		name  string
		shift func(*Table, int, slot)
	}{
		{name: "raw loop", shift: referenceTableInsert},
		{name: "sparse storage", shift: sparseTableInsert},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			state, err := New(Options{})
			if err != nil {
				b.Fatal(err)
			}
			defer state.Close()
			b.ReportAllocs()
			for range b.N {
				table := newTable(state.runtime, 8, 16)
				for key := 1; key <= 3; key++ {
					table.rawSetIntegerSlot(
						key,
						numberSlot(float64(key)),
					)
				}
				table.rawSetIntegerSlot(-7, numberSlot(7))
				table.rawSetIntegerSlot(0, numberSlot(8))
				benchmark.shift(
					table,
					position,
					numberSlot(9),
				)
			}
		})
	}
}

func installTableInsertSentinels(state *State, table *Table) {
	for key, value := range map[float64]float64{
		-9.5:  901,
		-0.5:  902,
		0.5:   903,
		2.25:  904,
		100.5: 905,
	} {
		table.rawSetSlot(numberSlot(key), numberSlot(value))
	}
	table.rawSetSlot(
		slotFromValue(state.String("record")),
		numberSlot(906),
	)
	table.rawSetSlot(trueSlot, numberSlot(907))
}

func referenceTableInsert(table *Table, position int, value slot) {
	end := table.RawLen() + 1
	if position > end {
		end = position
	}
	for index := end; index > position; index-- {
		previous, _ := table.rawIntSlot(index - 1)
		table.rawSetIntegerSlot(index, previous)
	}
	table.rawSetIntegerSlot(position, value)
}

func sparseTableInsert(table *Table, position int, value slot) {
	end := table.RawLen() + 1
	if position > end {
		end = position
	}
	table.shiftSparseIntegerRangeUp(position, end-1)
	table.rawSetIntegerSlot(position, value)
}

func assertEquivalentTableInsertResult(
	t *testing.T,
	reference, sparse *Table,
	first, last int,
	caseIndex, position int,
) {
	t.Helper()
	for key := first; key <= last; key++ {
		want, _ := reference.rawIntSlot(key)
		got, _ := sparse.rawIntSlot(key)
		if !rawSlotEqual(got, want) {
			t.Fatalf(
				"case %d position %d: integer key %d = %v; want %v",
				caseIndex,
				position,
				key,
				got.owningValue(),
				want.owningValue(),
			)
		}
	}
	for _, key := range []slot{
		numberSlot(-9.5),
		numberSlot(-0.5),
		numberSlot(0.5),
		numberSlot(2.25),
		numberSlot(100.5),
		trueSlot,
	} {
		want, _ := reference.rawSlot(key)
		got, _ := sparse.rawSlot(key)
		if !rawSlotEqual(got, want) {
			t.Fatalf(
				"noninteger key %v = %v; want %v",
				key.owningValue(),
				got.owningValue(),
				want.owningValue(),
			)
		}
	}
	if reference.RawLen() != sparse.RawLen() {
		t.Fatalf(
			"RawLen = %d; want %d",
			sparse.RawLen(),
			reference.RawLen(),
		)
	}
	if reference.structuralVersion != sparse.structuralVersion {
		t.Fatalf(
			"structural version = %d; want %d",
			sparse.structuralVersion,
			reference.structuralVersion,
		)
	}
	assertTableStorageAccounting(t, reference)
	assertTableStorageAccounting(t, sparse)
}

func assertTableStorageAccounting(t *testing.T, table *Table) {
	t.Helper()
	arrayUsed := 0
	for _, value := range table.array {
		if value.kind() != NilKind {
			arrayUsed++
		}
	}
	if arrayUsed != table.arrayUsed {
		t.Fatalf("arrayUsed = %d; counted %d", table.arrayUsed, arrayUsed)
	}

	count, deleted, integerKeys := 0, 0, 0
	for _, entry := range table.store.entries {
		if entry.hash == entryHashEmpty {
			continue
		}
		if entry.value.kind() == NilKind {
			deleted++
			continue
		}
		count++
		if isPositiveIntegerKey(entry.key) {
			integerKeys++
		}
	}
	if count != table.store.count ||
		deleted != table.store.deleted ||
		integerKeys != table.store.integerKeys {
		t.Fatalf(
			"store accounting = count:%d deleted:%d integers:%d; "+
				"counted %d/%d/%d",
			table.store.count,
			table.store.deleted,
			table.store.integerKeys,
			count,
			deleted,
			integerKeys,
		)
	}
}

func TestWarmTableConcatUsesOneBackingAllocation(t *testing.T) {
	requireStableAllocationAccounting(t)
	state := newStateWithTable(t)
	defer state.Close()

	chunk := mustLoadString(t, state, "@concat-alloc.lua", `
local concat = table.concat
local values = {"alpha", "beta", "gamma"}
return function()
	return concat(values, "|")
end
`)
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
	if allocations != 1 {
		t.Fatalf("warm concat allocated %v times per run; want 1", allocations)
	}
	if text, ok := destination[0].AsString(); !ok ||
		text != "alpha|beta|gamma" {
		t.Fatalf("warm concat result = %v", destination[0])
	}
}

func TestWarmLargeTableConcatHasConstantAllocationCount(t *testing.T) {
	requireStableAllocationAccounting(t)
	state := newStateWithTable(t)
	defer state.Close()

	chunk := mustLoadString(t, state, "@concat-large-alloc.lua", `
local concat = table.concat
local values = {}
for index = 1, 1000 do
	values[index] = "12345678"
end
return function()
	return concat(values)
end
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("loader produced %d results; want 1", len(results))
	}
	body := results[0]
	var destination [1]Value
	for index := 0; index < 16; index++ {
		if _, err := state.CallInto(
			body,
			nil,
			destination[:],
		); err != nil {
			t.Fatal(err)
		}
	}
	allocations := testing.AllocsPerRun(32, func() {
		if _, err := state.CallInto(
			body,
			nil,
			destination[:],
		); err != nil {
			t.Fatal(err)
		}
	})
	// An 8 KiB result is intentionally too long for the short-string cache.
	// Its representation minimum is one exact builder buffer and one
	// luaString header, independent of the 1,000 input elements.
	if allocations > 2 {
		t.Fatalf("large concat allocated %v times per run; want at most 2", allocations)
	}
	if text, ok := destination[0].AsString(); !ok || len(text) != 8000 {
		t.Fatalf("large concat result = %v", destination[0])
	}
}

func newStateWithTable(t *testing.T) *State {
	t.Helper()
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenTable(); err != nil {
		t.Fatal(err)
	}
	return state
}

func BenchmarkTableConcat(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		b.Fatal(err)
	}
	if err := state.OpenTable(); err != nil {
		b.Fatal(err)
	}

	chunk := mustLoadString(b, state, "@concat-benchmark.lua", `
local concat = table.concat
local values = {"alpha", "beta", "gamma"}
return function()
	return concat(values, "|")
end
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		b.Fatal(err)
	}
	body := results[0]
	var destination [1]Value
	for index := 0; index < 64; index++ {
		if _, err := state.CallInto(body, nil, destination[:]); err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := state.CallInto(body, nil, destination[:]); err != nil {
			b.Fatal(err)
		}
	}
}

var tableLibraryLua51Cases = []lua51Case{
	{
		name:   "getn",
		source: "return table.getn({1,2,3}), table.getn({}), table.getn({'a','b'})",
		want:   "ok 3 0 2",
	},
	{
		name:   "getn_matches_length",
		source: "local t = {1,2,nil,4} return table.getn(t), #t",
		want:   "ok 4 4",
	},
	{
		name:   "maxn",
		source: "return table.maxn({[1]=1,[100]=1,[2.5]=1,x=1})",
		want:   "ok 100",
	},
	{
		name:   "maxn_empty_and_negative",
		source: "return table.maxn({}), table.maxn({[-5]=1}), table.maxn({[0]=1})",
		want:   "ok 0 0 0",
	},
	{
		name:   "maxn_ignores_non_numeric_keys",
		source: "return table.maxn({x=1, [true]=1, [3]=1})",
		want:   "ok 3",
	},
	{
		name:   "concat_default_separator",
		source: "return table.concat({1,2,3})",
		want:   "ok '123'",
	},
	{
		name:   "concat_separator",
		source: "return table.concat({1,2,3}, ', ')",
		want:   "ok '1, 2, 3'",
	},
	{
		name:   "concat_empty",
		source: "return table.concat({}), table.concat({}, ','), table.concat({1,2,3}, ',', 3, 1)",
		want:   "ok '' '' ''",
	},
	{
		name:   "concat_range",
		source: "return table.concat({'a','b','c','d'}, '-', 2, 3)",
		want:   "ok 'b-c'",
	},
	{
		name:   "concat_single",
		source: "return table.concat({'a','b'}, '-', 2, 2)",
		want:   "ok 'b'",
	},
	{
		name:   "concat_numeric_separator",
		source: "return table.concat({1,2}, 5)",
		want:   "ok '152'",
	},
	{
		name:   "concat_coerces_bounds",
		source: "return table.concat({'a','b','c'}, '-', '1', '2')",
		want:   "ok 'a-b'",
	},
	{
		name:   "concat_number_spelling",
		source: "return table.concat({1/3, 1e20, -0.5, 100}, ' ')",
		want:   "ok '0.33333333333333 1e+20 -0.5 100'",
	},
	{
		name:   "concat_rejects_boolean",
		source: "return table.concat({true})",
		want:   "error 'case:1: invalid value (boolean) at index 1 in table for 'concat''",
	},
	{
		name:   "concat_rejects_table",
		source: "return table.concat({1,{},3})",
		want:   "error 'case:1: invalid value (table) at index 2 in table for 'concat''",
	},
	{
		name:   "concat_rejects_hole",
		source: "return table.concat({1,2,3}, ',', 1, 5)",
		want:   "error 'case:1: invalid value (nil) at index 4 in table for 'concat''",
	},
	{
		name:   "concat_rejects_bad_separator",
		source: "return table.concat({}, {})",
		want:   "error 'case:1: bad argument #2 to 'concat' (string expected, got table)'",
	},
	{
		name:   "concat_is_raw",
		source: "local t = setmetatable({}, {__index = function() return 'x' end}) return table.concat(t, ',', 1, 2)",
		want:   "error 'case:1: invalid value (nil) at index 1 in table for 'concat''",
	},
	{
		name:   "concat_wrong_table",
		source: "return table.concat(1)",
		want:   "error 'case:1: bad argument #1 to 'concat' (table expected, got number)'",
	},
	{
		name:   "concat_method_argument",
		source: "local o = {concat = table.concat} return o:concat({})",
		want:   "error 'case:1: bad argument #1 to 'concat' (string expected, got table)'",
	},
	{
		name:   "insert_appends",
		source: "local t = {1,2} table.insert(t, 3) return table.concat(t, ','), #t",
		want:   "ok '1,2,3' 3",
	},
	{
		name:   "insert_into_empty",
		source: "local t = {} table.insert(t, 'a') return t[1], #t",
		want:   "ok 'a' 1",
	},
	{
		name:   "insert_at_position",
		source: "local t = {1,2,3} table.insert(t, 2, 'x') return table.concat(t, ','), #t",
		want:   "ok '1,x,2,3' 4",
	},
	{
		name:   "insert_at_front",
		source: "local t = {1,2,3} table.insert(t, 1, 'x') return table.concat(t, ',')",
		want:   "ok 'x,1,2,3'",
	},
	{
		name:   "insert_at_end_position",
		source: "local t = {1,2,3} table.insert(t, 4, 'x') return table.concat(t, ',')",
		want:   "ok '1,2,3,x'",
	},
	{
		name:   "insert_beyond_end",
		source: "local t = {1,2,3} table.insert(t, 6, 'z') return t[6], tostring(t[4]), tostring(t[5]), t[3]",
		want:   "ok 'z' 'nil' 'nil' 3",
	},
	{
		name:   "insert_at_zero",
		source: "local t = {1,2,3} table.insert(t, 0, 'z') return t[0], tostring(t[1]), t[2], t[3], t[4], #t",
		want:   "ok 'z' 'nil' 1 2 3 4",
	},
	{
		name:   "insert_negative",
		source: "local t = {1,2,3} table.insert(t, -1, 'z') return t[-1], tostring(t[1]), t[2], t[3], t[4], #t",
		want:   "ok 'z' 'nil' 1 2 3 4",
	},
	{
		name:   "insert_truncates_position",
		source: "local t = {1,2,3} table.insert(t, 2.7, 'x') return table.concat(t, ',')",
		want:   "ok '1,x,2,3'",
	},
	{
		name:   "insert_returns_nothing",
		source: "local t = {} return table.insert(t, 1)",
		want:   "ok",
	},
	{
		name:   "insert_wrong_arity",
		source: "local t = {} return table.insert(t, 1, 2, 3)",
		want:   "error 'case:1: wrong number of arguments to 'insert''",
	},
	{
		name:   "insert_needs_arguments",
		source: "local t = {} return table.insert(t)",
		want:   "error 'case:1: wrong number of arguments to 'insert''",
	},
	{
		name:   "insert_wrong_table",
		source: "return table.insert(nil, 1)",
		want:   "error 'case:1: bad argument #1 to 'insert' (table expected, got nil)'",
	},
	{
		name:   "insert_is_raw",
		source: "local seen = false local t = setmetatable({}, {__newindex = function() seen = true end}) table.insert(t, 'a') return seen, t[1]",
		want:   "ok false 'a'",
	},
	{
		name:   "remove_last",
		source: "local t = {1,2,3} local v = table.remove(t) return v, table.concat(t, ','), #t",
		want:   "ok 3 '1,2' 2",
	},
	{
		name:   "remove_at_position",
		source: "local t = {1,2,3} local v = table.remove(t, 1) return v, table.concat(t, ','), #t",
		want:   "ok 1 '2,3' 2",
	},
	{
		name:   "remove_middle",
		source: "local t = {'a','b','c'} local v = table.remove(t, 2) return v, table.concat(t, ',')",
		want:   "ok 'b' 'a,c'",
	},
	{
		name:   "remove_from_empty",
		source: "local t = {} return table.remove(t)",
		want:   "ok",
	},
	{
		name:   "remove_out_of_range",
		source: "local t = {1,2,3} return table.remove(t, 0), table.remove(t, 4), #t",
		want:   "ok nil nil 3",
	},
	{
		name:   "remove_result_count",
		source: "local t = {} return select('#', table.remove(t))",
		want:   "ok 0",
	},
	{
		name:   "remove_single",
		source: "local t = {'a'} local v = table.remove(t) return v, #t, tostring(t[1])",
		want:   "ok 'a' 0 'nil'",
	},
	{
		name:   "remove_truncates_position",
		source: "local t = {1,2,3} return table.remove(t, 2.9), table.concat(t, ',')",
		want:   "ok 2 '1,3'",
	},
	{
		name:   "remove_wrong_table",
		source: "return table.remove('x')",
		want:   "error 'case:1: bad argument #1 to 'remove' (table expected, got string)'",
	},
	{
		name:   "setn_is_obsolete",
		source: "return table.setn({}, 3)",
		want:   "error 'case:1: 'setn' is obsolete'",
	},
	{
		name:   "setn_checks_table_first",
		source: "return table.setn(1, 3)",
		want:   "error 'case:1: bad argument #1 to 'setn' (table expected, got number)'",
	},
	{
		name:   "setn_ignores_second_argument",
		source: "return table.setn({})",
		want:   "error 'case:1: 'setn' is obsolete'",
	},
	{
		name:   "foreachi_visits_the_sequence",
		source: "local out = {} table.foreachi({'a','b','c'}, function(k, v) out[#out+1] = k .. v end) return table.concat(out, ',')",
		want:   "ok '1a,2b,3c'",
	},
	{
		name:   "foreachi_visits_holes",
		source: "local out = {} table.foreachi({1,2,nil,4}, function(k, v) out[#out+1] = k .. ':' .. tostring(v) end) return table.concat(out, ',')",
		want:   "ok '1:1,2:2,3:nil,4:4'",
	},
	{
		name:   "foreachi_stops_on_a_result",
		source: "return table.foreachi({10,20,30}, function(k, v) if v == 20 then return 'hit' end end)",
		want:   "ok 'hit'",
	},
	{
		name:   "foreachi_returns_false",
		source: "return table.foreachi({1,2}, function(k, v) if k == 2 then return false end end)",
		want:   "ok false",
	},
	{
		name:   "foreachi_completes",
		source: "return table.foreachi({1,2}, function() end)",
		want:   "ok",
	},
	{
		name:   "foreach_visits_the_sequence",
		source: "local out = {} table.foreach({'a','b','c'}, function(k, v) out[#out+1] = k .. v end) return table.concat(out, ',')",
		want:   "ok '1a,2b,3c'",
	},
	{
		name:   "foreach_stops_on_a_result",
		source: "return table.foreach({10,20,30}, function(k, v) if v == 20 then return 'hit' end end)",
		want:   "ok 'hit'",
	},
	{
		name:   "foreach_sees_string_keys",
		source: "local t = {} t.only = 'v' local k, v return table.foreach(t, function(key, value) return key .. '=' .. value end)",
		want:   "ok 'only=v'",
	},
	{
		name:   "foreach_propagates_errors",
		source: "return table.foreach({1}, function() error('boom') end)",
		want:   "error 'case:1: boom'",
	},
	{
		name:   "foreachi_propagates_errors",
		source: "return table.foreachi({1}, function() error('boom') end)",
		want:   "error 'case:1: boom'",
	},
	{
		name:   "foreach_needs_a_function",
		source: "return table.foreach({}, 1)",
		want:   "error 'case:1: bad argument #2 to 'foreach' (function expected, got number)'",
	},
	{
		name:   "foreachi_needs_a_function",
		source: "return table.foreachi({}, 1)",
		want:   "error 'case:1: bad argument #2 to 'foreachi' (function expected, got number)'",
	},
	{
		name:   "foreach_rejects_callable_table",
		source: "local c = setmetatable({}, {__call = function() end}) return table.foreach({1}, c)",
		want:   "error 'case:1: bad argument #2 to 'foreach' (function expected, got table)'",
	},
	{
		name:   "sort_default",
		source: "local t = {5,2,8,1,9,3,7,4,6,0} table.sort(t) return table.concat(t, ',')",
		want:   "ok '0,1,2,3,4,5,6,7,8,9'",
	},
	{
		name:   "sort_reverse",
		source: "local t = {5,2,8,1,9,3,7,4,6,0} table.sort(t, function(a, b) return a > b end) return table.concat(t, ',')",
		want:   "ok '9,8,7,6,5,4,3,2,1,0'",
	},
	{
		name:   "sort_strings",
		source: "local t = {'pear','apple','fig'} table.sort(t) return table.concat(t, ',')",
		want:   "ok 'apple,fig,pear'",
	},
	{
		name:   "sort_empty_and_single",
		source: "local a = {} table.sort(a) local b = {'x'} table.sort(b) return #a, b[1]",
		want:   "ok 0 'x'",
	},
	{
		name:   "sort_two",
		source: "local t = {2,1} table.sort(t) return table.concat(t, ',')",
		want:   "ok '1,2'",
	},
	{
		name:   "sort_three",
		source: "local t = {3,1,2} table.sort(t) return table.concat(t, ',')",
		want:   "ok '1,2,3'",
	},
	{
		name:   "sort_comparison_count",
		source: "local n = 0 local t = {} for i = 1, 20 do t[i] = (i * 7) % 20 end table.sort(t, function(a, b) n = n + 1 return a < b end) return n, table.concat(t, ',')",
		want:   "ok 74 '0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19'",
	},
	{
		name:   "sort_comparison_count_sorted",
		source: "local n = 0 local t = {} for i = 1, 16 do t[i] = i end table.sort(t, function(a, b) n = n + 1 return a < b end) return n",
		want:   "ok 53",
	},
	{
		name:   "sort_comparison_count_reversed",
		source: "local n = 0 local t = {} for i = 1, 16 do t[i] = 17 - i end table.sort(t, function(a, b) n = n + 1 return a < b end) return n",
		want:   "ok 57",
	},
	{
		name:   "sort_equal_elements",
		source: "local t = {} for i = 1, 12 do t[i] = 1 end table.sort(t) return table.concat(t, ',')",
		want:   "ok '1,1,1,1,1,1,1,1,1,1,1,1'",
	},
	{
		name:   "sort_uses_lt_metamethod",
		source: "local mt = {__lt = function(a, b) return a.v < b.v end} local src = {3,1,2} local t = {} for i = 1, 3 do t[i] = setmetatable({v = src[i]}, mt) end table.sort(t) return t[1].v, t[2].v, t[3].v",
		want:   "ok 1 2 3",
	},
	{
		name:   "sort_mixed_types",
		source: "return table.sort({3,1,2,'x'})",
		want:   "error 'attempt to compare string with number'",
	},
	{
		name:   "sort_two_tables_without_lt",
		source: "return table.sort({{},{}})",
		want:   "error 'attempt to compare two table values'",
	},
	{
		name:   "sort_invalid_order_function",
		source: "local t = {} for i = 1, 12 do t[i] = i end return table.sort(t, function() return true end)",
		want:   "error 'case:1: invalid order function for sorting'",
	},
	{
		name:   "sort_comparator_error",
		source: "return table.sort({3,1,2}, function() error('boom') end)",
		want:   "error 'case:1: boom'",
	},
	{
		name:   "sort_rejects_non_function",
		source: "return table.sort({3,1,2}, 5)",
		want:   "error 'case:1: bad argument #2 to 'sort' (function expected, got number)'",
	},
	{
		name:   "sort_accepts_nil_comparator",
		source: "local t = {2,1} table.sort(t, nil) return table.concat(t, ',')",
		want:   "ok '1,2'",
	},
	{
		name:   "sort_ignores_extra_arguments",
		source: "local t = {2,1} table.sort(t, nil, 'extra') return table.concat(t, ',')",
		want:   "ok '1,2'",
	},
	{
		name:   "sort_wrong_table",
		source: "return table.sort(1)",
		want:   "error 'case:1: bad argument #1 to 'sort' (table expected, got number)'",
	},
	{
		name:   "sort_returns_nothing",
		source: "return table.sort({})",
		want:   "ok",
	},
	{
		name:   "sort_is_raw",
		source: "local reads = 0 local t = setmetatable({3,1,2}, {__index = function() reads = reads + 1 end}) table.sort(t) return reads, table.concat(t, ',')",
		want:   "ok 0 '1,2,3'",
	},
	{
		name:   "sort_inconsistent_nil_guard",
		source: "local t = {} for i = 1, 12 do t[i] = i end return table.sort(t, function(a, b) return a ~= nil end)",
		want:   "error 'case:1: invalid order function for sorting'",
	},
	{
		name:   "sort_always_false_is_valid",
		source: "local t = {} for i = 1, 12 do t[i] = i end table.sort(t, function() return false end) return table.concat(t, ',')",
		want:   "ok '1,8,7,9,10,11,6,4,5,2,3,12'",
	},
	{
		name:   "sort_non_strict_order",
		source: "local t = {} for i = 1, 30 do t[i] = i % 3 end return table.sort(t, function(a, b) return a <= b end)",
		want:   "error 'case:1: attempt to compare number with nil'",
	},
	{
		name:   "sort_reads_past_the_end",
		source: "local t = {} for i = 1, 30 do t[i] = i % 3 end return table.sort(t, function(a, b) return (a or -1) <= (b or -1) end)",
		want:   "error 'case:1: invalid order function for sorting'",
	},
	{
		name:   "sort_comparison_counts_ascending",
		source: "local out = {} for n = 1, 25 do local c = 0 local t = {} for i = 1, n do t[i] = i end table.sort(t, function(a, b) c = c + 1 return a < b end) out[n] = c end return table.concat(out, ',')",
		want:   "ok '0,1,3,7,9,12,15,20,25,28,31,35,39,43,47,53,59,65,71,75,79,83,87,92,97'",
	},
	{
		name:   "sort_comparison_counts_descending",
		source: "local out = {} for n = 1, 25 do local c = 0 local t = {} for i = 1, n do t[i] = n - i + 1 end table.sort(t, function(a, b) c = c + 1 return a < b end) out[n] = c end return table.concat(out, ',')",
		want:   "ok '0,1,3,7,9,12,14,19,25,28,32,39,42,46,52,57,60,65,69,76,83,90,98,103,108'",
	},
	{
		name:   "sort_comparison_counts_shuffled",
		source: "local out = {} for n = 1, 25 do local c = 0 local t = {} for i = 1, n do t[i] = (i * 7) % (n + 1) end table.sort(t, function(a, b) c = c + 1 return a < b end) out[n] = c .. ':' .. table.concat(t, '') end return table.concat(out, ',')",
		want:   "ok '0:1,1:12,3:123,7:1234,9:12345,13:000000,14:1234567,24:12345678,25:123456789,25:12345678910,31:1234567891011,34:123456789101112,43:0000007777777,41:1234567891011121314,53:123456789101112131415,53:12345678910111213141516,61:1234567891011121314151617,61:123456789101112131415161718,67:12345678910111213141516171819,76:000000777777714141414141414,71:123456789101112131415161718192021,87:12345678910111213141516171819202122,95:1234567891011121314151617181920212223,96:123456789101112131415161718192021222324,97:12345678910111213141516171819202122232425'",
	},
	{
		name:   "sort_permutes_correctly_across_sizes",
		source: "local ok = true for n = 1, 40 do local t = {} for i = 1, n do t[i] = (i * 13) % (n + 1) end table.sort(t) for i = 2, n do if t[i - 1] > t[i] then ok = false end end end return ok",
		want:   "ok true",
	},
}
